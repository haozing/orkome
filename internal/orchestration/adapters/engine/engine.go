package engine

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/schema"

	"github.com/haozing/ploykit/platform/egressx"
	"github.com/haozing/ploykit/platform/ids"
	"orkome/internal/contract/failuretax"
	"orkome/internal/contract/modelmeta"
	orchapp "orkome/internal/orchestration/app"
)

type EinoEngine struct {
	CredentialFn func(ctx context.Context, workspaceID, credentialID string) (string, error)

	FailoverCredentials func(ctx context.Context, workspaceID, provider, excludeCredentialID string, limit int) ([]string, error)

	SupplementClaimer func(ctx context.Context, taskID string) (content string, ok bool, ack func() error)

	ChatHistory func(ctx context.Context, sessionID string, limit int) ([]ChatTurn, error)

	WorkBench orchapp.WorkBench

	Checkpoints *CheckpointStore

	TaskStatus func(ctx context.Context, taskID string) (string, error)

	WorkItemSession func(ctx context.Context, workItemID, agentID string) ([]orchapp.SessionTurn, error)

	WorkspaceSearch func(ctx context.Context, workspaceID, query string) ([]orchapp.SearchHit, error)

	SquadLoader func(ctx context.Context, workspaceID, squadID string) (orchapp.SquadInfo, bool, error)

	// SkillSource 技能注入（可选）。
	Skills SkillSource

	// PackToolSchemas 是 B1 存档（pack_tool_schema，迁移 1035）的读取端口
	// （C1）：桥接组装期一次拉全 workspace 存档，为协议未自描述 outputSchema
	// 的 MCP 工具补输出契约（优先级：协议自描述 → 本存档 → 结构化探测）。
	// nil = 未接线（无存档可用，回落探测；可测桩注入）。构造方式见
	// docs/handoff-b1-job-gate-2026-10-11.md §3（勿走 HTTP）。
	PackToolSchemas interface {
		ListPackToolSchemas(ctx context.Context, ws string) ([]orchapp.PackToolSchema, error)
	}

	// TaskContextPatcher 是 task.context 的回写端口（C1）：引擎组装完统一
	// 注册表后，把全量工具清单（新键 tools）与 MCP 桥接的细化态服务器
	// 状态（mcp_servers，ok/ssrf/connect_failed——取代派发期 resolved 态
	// 粗快照）写回任务上下文，供前端/审计读取。nil = 未接线（跳过回写，
	// 行为与 C1 之前一致）。语义冻结见 docs/interfaces-tool-registry
	// -2026-10-11.md §3。
	TaskContextPatcher orchapp.TaskContextPatcher

	InterimPush func(workspaceID, taskID, kind, content string)

	ProgressPush func(workspaceID, taskID, stage, callID string, seq int64)

	TaskMessages orchapp.TaskMessageSink

	LeaderDispatch orchapp.LeaderDispatcher

	ChildItems orchapp.ChildItemCreator

	SquadEvals orchapp.SquadEvaluationRecorder

	StatusUpdate orchapp.AgentStatusUpdater

	Checkiner orchapp.WakeupCheckiner

	// BatchTools 批量任务工具面（batch v2.0）：batch_create/status/results
	// 三工具的后端能力（*orchapp.BatchService 经窄接口注入）。nil = 不注入
	// （任务工具面无批量工具）。
	BatchTools orchapp.BatchToolPort

	MCPResolver orchapp.MCPResolver

	Egress *egressx.Guard

	// OutputValidator 校验 MCP 工具输出契约（A1）；nil 时用默认两级实现
	// （协议自描述 schema 优先 → 结构化探测回落）。
	OutputValidator OutputValidator

	// AuditLog 输出契约违规审计日志（结构化，含原文全文）；nil 用 slog 默认。
	AuditLog *slog.Logger

	RetryBackoffFunc func(ctx context.Context, attempt int) time.Duration

	Now        func() time.Time
	InstanceID string
}

type ChatTurn struct {
	Role    string
	Content string
}

type steerMiddleware struct {
	adk.BaseChatModelAgentMiddleware
	ctx    context.Context
	taskID string
	claim  func(ctx context.Context, taskID string) (string, bool, func() error)
	status func(ctx context.Context, taskID string) (string, error)
}

var errTaskCancelled = errors.New("task cancelled by user")

func (m *steerMiddleware) BeforeModelRewriteState(ctx context.Context,
	state *adk.ChatModelAgentState, _ *adk.ModelContext) (context.Context, *adk.ChatModelAgentState, error) {

	if m.status != nil {
		if st, err := m.status(m.ctx, m.taskID); err == nil && (st == "cancelled" || st == "completed" || st == "failed") {
			return ctx, state, errTaskCancelled
		}
	}
	if m.claim == nil {
		return ctx, state, nil
	}
	for {
		content, ok, ack := m.claim(m.ctx, m.taskID)
		if !ok {
			break
		}
		state.Messages = append(state.Messages, schema.UserMessage("[additional instruction] "+content))
		if ack != nil {
			_ = ack()
		}
	}
	return ctx, state, nil
}

var _ orchapp.Engine = (*EinoEngine)(nil)

func (e *EinoEngine) Name() string { return "eino" }

func (e *EinoEngine) Execute(ctx context.Context, task orchapp.Task, agent orchapp.Agent, credentialSecret string) (orchapp.EngineOutput, error) {
	return e.run(ctx, task, agent, credentialSecret, nil, "")
}

func (e *EinoEngine) Resume(ctx context.Context, task orchapp.Task, agent orchapp.Agent, credentialSecret, targetID string, decision orchapp.ApprovalDecision) (orchapp.EngineOutput, error) {
	return e.run(ctx, task, agent, credentialSecret, &decision, targetID)
}

func (e *EinoEngine) Generate(ctx context.Context, agent orchapp.Agent, credentialSecret, system, input string) (string, orchapp.UsageReport, error) {
	observeGenerate("aux")
	rc := runtimeConfig(agent)
	m, err := modelmeta.Build(ctx, rc, credentialSecret)
	if err != nil {
		return "", orchapp.UsageReport{}, &orchapp.EngineError{Reason: failuretax.ModelNotConfigured, Detail: err.Error()}
	}
	stats := &modelRetryStats{}
	aux := newAuxRetryModel(m, stats)
	if e.RetryBackoffFunc != nil {
		aux.backoff = e.RetryBackoffFunc
	}
	msg, err := aux.Generate(ctx, []*schema.Message{
		schema.SystemMessage(system),
		schema.UserMessage(input),
	})
	if err != nil {
		return "", orchapp.UsageReport{}, normalize(err)
	}
	u := orchapp.UsageReport{Provider: rc.Provider, Model: rc.Model, AgentLabel: "aux"}
	if meta := msg.ResponseMeta; meta != nil && meta.Usage != nil {
		u.PromptTokens, u.CompletionTokens = int64(meta.Usage.PromptTokens), int64(meta.Usage.CompletionTokens)
	}
	u.ModelRetries, u.ModelFailovers = stats.report()
	return msg.Content, u, nil
}

func (e *EinoEngine) run(ctx context.Context, task orchapp.Task, agent orchapp.Agent, credentialSecret string, decision *orchapp.ApprovalDecision, targetID string) (orchapp.EngineOutput, error) {

	ctx, span := startEngineSpan(ctx, task.ID, task.WorkspaceID)
	defer span.End()

	rc := runtimeConfig(agent)

	pipe := newEventPipe(e, task)
	defer pipe.flushTail()

	m, err := modelmeta.Build(ctx, rc, credentialSecret)
	if err != nil {
		return orchapp.EngineOutput{}, &orchapp.EngineError{Reason: failuretax.ModelNotConfigured, Detail: err.Error()}
	}

	stats := &modelRetryStats{}
	retryCfg := newModelRetryConfig(stats, e.RetryBackoffFunc)
	var failoverCfg *adk.ModelFailoverConfig[*schema.Message]
	if e.FailoverCredentials != nil {
		exclude := rc.CredentialID
		failoverCfg = newModelFailoverConfig(ctx, rc, func(ctx context.Context) ([]string, error) {
			return e.FailoverCredentials(ctx, task.WorkspaceID, rc.Provider, exclude, modelMaxFailovers)
		}, stats)
	}

	system := strings.TrimSpace(agent.Description)
	var history []ChatTurn
	if sid, _ := task.Context["chat_session_id"].(string); sid != "" && e.ChatHistory != nil {
		history, _ = e.ChatHistory(ctx, sid, 20)
	}

	var session []orchapp.SessionTurn
	fresh, _ := task.Context["force_fresh_session"].(bool)
	if task.WorkItemID != "" && task.SessionRefIn != "" && !fresh && e.WorkItemSession != nil {
		session, _ = e.WorkItemSession(ctx, task.WorkItemID, task.AgentID)
	}

	var memberTools []tool.BaseTool
	var memberRuns []memberRun
	squadBrief := ""
	var squadMembers []orchapp.SquadMember
	if task.IsLeaderTask && task.SquadID != "" && e.SquadLoader != nil {
		if sq, ok, _ := e.SquadLoader(ctx, task.WorkspaceID, task.SquadID); ok {
			squadBrief = squadBriefing(sq)
			squadMembers = sq.Members
			// consult 工具对所有小队成员生成（"咨询"= 同轮秒回的进程内子
			// agent；何时咨询 vs 正式派发由 leader 模型按任务性质现场判断
			// ——choreography 布尔已退役）。
			for _, m := range sq.Members {
				mt, ctr, err := e.buildMemberTool(ctx, task.WorkspaceID, m)
				if err != nil {
					continue
				}
				memberTools = append(memberTools, mt)
				memberRuns = append(memberRuns, memberRun{agentID: m.AgentID, label: m.Name, ctr: ctr})
			}
		}
	}
	// Project Workflow 阶段简报（W3）：trigger=workflow_stage 的任务注入
	//（stageBriefing 内部自行判型，非流程任务返回空串零影响；squad 执行者
	// 场景与 squadBrief 共存——leader 既看小队简报也看阶段简报）。
	stageBrief := stageBriefing(task)
	user := strings.TrimSpace(assembleUserPrompt(task, history, session) + "\n" + squadBrief + "\n" + stageBrief)

	var handlers []adk.ChatModelAgentMiddleware
	if e.SupplementClaimer != nil || e.TaskStatus != nil {
		handlers = append(handlers, &steerMiddleware{ctx: ctx, taskID: task.ID,
			claim: e.SupplementClaimer, status: e.TaskStatus})
	}
	if mw := e.skillMiddleware(ctx, task, agent); mw != nil {
		handlers = append(handlers, mw)
	}

	// C1：统一注册表装配——内置工具（buildBuiltinTools，纯搬移自本函数
	// 原装配段）与 MCP 桥接工具同一注册表；输出校验 middleware（同一
	// OutputValidator，A1 红利对内置工具闭环）+ ConcurrencySafe fail-closed
	// 并发闸走 eino 原生 ChatModelAgentMiddleware 协议，与上方
	// steer/skill 及下方 toolsearch 同构共存。
	reg := newToolRegistry()
	toolList := e.buildBuiltinTools(ctx, task, agent, reg, squadMembers)
	bridge := e.buildMCPTools(ctx, task, agent)
	registerMCPBridge(ctx, reg, bridge, e.archivedToolSchemas(ctx, task))
	handlers = append(handlers, newToolContractMiddleware(reg, e.OutputValidator, e.AuditLog))
	e.patchToolSnapshot(ctx, task, reg, bridge)

	pack := e.buildToolPack(ctx, task, agent, toolList, memberTools, bridge)
	if pack.searchMW != nil {
		handlers = append(handlers, pack.searchMW)
	}
	toolList = pack.static

	staticList := toolList
	cfgTools := adk.ToolsConfig{ToolsNodeConfig: compose.ToolsNodeConfig{}}
	cfgTools.ToolsNodeConfig.UnknownToolsHandler = unknownToolsHandler(func() []string {
		return toolNamesOf(context.Background(), staticList)
	})
	cfgTools.Tools = toolList
	a, err := adk.NewChatModelAgent(ctx, &adk.ChatModelAgentConfig{
		Name:                "worker-" + agent.ID[:8],
		Description:         "orkome task runner",
		Instruction:         system + "\nYou may call tools to complete the task; call request_approval before any dangerous operation.",
		Model:               m,
		Handlers:            handlers,
		ToolsConfig:         cfgTools,
		ModelRetryConfig:    retryCfg,
		ModelFailoverConfig: failoverCfg,
	})
	if err != nil {
		return orchapp.EngineOutput{}, &orchapp.EngineError{Reason: failuretax.AgentToolFailed, Detail: err.Error()}
	}
	rcfg := adk.RunnerConfig{Agent: a}
	if e.Checkpoints != nil {
		rcfg.CheckPointStore = e.Checkpoints
	}
	r := adk.NewRunner(ctx, rcfg)

	var iter *adk.AsyncIterator[*adk.AgentEvent]
	if decision != nil {

		iter, err = r.ResumeWithParams(ctx, "task:"+task.ID, &adk.ResumeParams{
			Targets: map[string]any{targetID: ApprovalDecision{
				Approved: decision.Approved, ByUserID: decision.ByUserID, Note: decision.Note,
			}},
		})
		if err != nil {
			return orchapp.EngineOutput{}, &orchapp.EngineError{Reason: "agent_error.state_oversized", Detail: err.Error()}
		}
	} else {
		iter = r.Query(ctx, user, adk.WithCheckPointID("task:"+task.ID))
	}
	final := ""
	var finalParts []orchapp.ContentPart
	var usage schema.TokenUsage
	for {
		ev, ok := iter.Next()
		if !ok {
			break
		}
		if ev.Err != nil {
			pipe.emit("error", "", errText(ev.Err))
			return orchapp.EngineOutput{}, normalize(ev.Err)
		}
		if ev.Action != nil {
			if iv := ev.Action.Interrupted; iv != nil && len(iv.InterruptContexts) > 0 {
				ic := iv.InterruptContexts[0]
				payload, _ := json.Marshal(ic.Info)
				pipe.emit("approval", "", string(payload))
				return orchapp.EngineOutput{
					Outcome: "awaiting_input",
					Interrupt: &orchapp.InterruptInfo{
						TargetID: ic.ID, Kind: "approval", Payload: string(payload),
					},
				}, nil
			}
			continue
		}
		if mo := ev.Output; mo != nil && mo.MessageOutput != nil {
			mv := mo.MessageOutput

			if mv.Role == schema.Tool && mv.Message != nil {
				pipe.emit("tool_result", toolCallID(mv.Message), mv.Message.Content)
			}
			if msg := mv.Message; msg != nil && msg.Role == schema.Assistant {

				for _, tc := range msg.ToolCalls {
					pipe.emit("tool_use", tc.ID, tc.Function.Name+" "+tc.Function.Arguments)
				}
				if msg.Content != "" {
					final = msg.Content
					pipe.emit("text", "", msg.Content)
				}

				if ps, textOf := multimodalOf(msg); len(ps) > 0 || textOf != "" {
					if textOf != "" && final == "" {
						final = textOf
					}
					if len(ps) > 0 {
						finalParts = ps
					}
				}
				if meta := msg.ResponseMeta; meta != nil && meta.Usage != nil {
					usage.PromptTokens += meta.Usage.PromptTokens
					usage.CompletionTokens += meta.Usage.CompletionTokens
				}
			}
		}
	}
	if strings.TrimSpace(final) == "" {
		pipe.emit("error", "", "empty final output")
		return orchapp.EngineOutput{}, &orchapp.EngineError{Reason: failuretax.AgentIterationLimit, Detail: "empty final output"}
	}

	sessionRef := ""
	if task.WorkItemID != "" {
		sessionRef = "session:" + ids.NewV4().String()
	}

	retries, failovers := stats.report()
	return orchapp.EngineOutput{
		Outcome:     "completed",
		Output:      final,
		SessionRef:  sessionRef,
		Parts:       finalParts,
		MemberUsage: memberUsageOf(memberRuns),
		Usage: orchapp.UsageReport{
			Provider: rc.Provider, Model: rc.Model, AgentLabel: "primary",
			PromptTokens: int64(usage.PromptTokens), CompletionTokens: int64(usage.CompletionTokens),
			ModelRetries: retries, ModelFailovers: failovers,
		},
	}, nil
}

func runtimeConfig(a orchapp.Agent) modelmeta.RuntimeConfig {
	var rc modelmeta.RuntimeConfig
	b, _ := json.Marshal(a.RuntimeConfig)
	_ = json.Unmarshal(b, &rc)
	return rc
}

func toolCallID(m *schema.Message) string { return m.ToolCallID }

func errText(err error) string { return err.Error() }

func assembleUserPrompt(t orchapp.Task, history []ChatTurn, session []orchapp.SessionTurn) string {
	var b strings.Builder
	if sid, _ := t.Context["chat_session_id"].(string); sid != "" {

		b.WriteString("You are in a conversation with the user. Prior messages (oldest → newest):\n")
		for _, h := range history {
			role := "user"
			if h.Role == "assistant" {
				role = "you"
			}
			b.WriteString(role + "：" + h.Content + "\n")
		}

		if atts, ok := t.Context["attachments"].([]any); ok && len(atts) > 0 {
			b.WriteString("\nThis message has attachments:\n")
			for _, a := range atts {
				if m, ok := a.(map[string]any); ok {
					name, _ := m["name"].(string)
					ct, _ := m["content_type"].(string)
					b.WriteString("- " + name + "（" + ct + "）\n")
				}
			}
		}
		if msg, ok := t.Context["message"].(string); ok {
			b.WriteString("\nUser's new message: " + msg + "\nPlease reply.")
		}
		return b.String()
	}

	if prior, ok := t.Context["prior_summary"].(string); ok && prior != "" {
		reason, _ := t.Context["prior_failure_reason"].(string)
		b.WriteString("The previous attempt did not finish. Its last progress: " + prior)
		if reason != "" {
			b.WriteString("(failure reason: " + reason + "）")
		}
		b.WriteString("\nContinue from the existing progress and avoid redoing finished work.\n\n")
	}

	if len(session) > 0 {
		b.WriteString("These are conclusions of your earlier tasks on this work item (oldest → newest); keep context continuity:\n")
		for i, s := range session {
			b.WriteString("turn " + strconv.Itoa(i+1) + " of turns: " + s.Content + "\n")
		}
		b.WriteString("\n")
	}
	b.WriteString("Please complete the task assigned to you and report a conclusion.\n")
	if title, ok := t.Context["work_item_title"].(string); ok && title != "" {
		b.WriteString("Work item title: " + title + "\n")
	}
	if t.WorkItemID != "" {
		b.WriteString("work item ID：" + t.WorkItemID + "\n")
	}
	if trigger, ok := t.Context["trigger"].(string); ok {
		b.WriteString("Trigger: " + trigger + "\n")
	}
	return b.String()
}

func normalize(err error) error {
	orig := unwrapRetryExhausted(err)
	msg := strings.ToLower(orig.Error())
	switch {
	case strings.Contains(msg, "unauthorized"), strings.Contains(msg, "api key"),
		strings.Contains(msg, "forbidden"), strings.Contains(msg, "authentication"):
		return &orchapp.EngineError{Reason: failuretax.ProviderAuth, Detail: err.Error()}
	case strings.Contains(msg, "429"), strings.Contains(msg, "rate limit"),
		strings.Contains(msg, "too many requests"), strings.Contains(msg, "overload"),
		strings.Contains(msg, "bad gateway"), strings.Contains(msg, "service unavailable"),
		strings.Contains(msg, "502"), strings.Contains(msg, "503"), strings.Contains(msg, "504"):
		return &orchapp.EngineError{Reason: failuretax.ProviderOverload, Detail: err.Error()}
	case strings.Contains(msg, "timeout"), strings.Contains(msg, "deadline"):
		return &orchapp.EngineError{Reason: failuretax.Timeout, Detail: err.Error()}
	case strings.Contains(msg, "connection"), strings.Contains(msg, "dial"), strings.Contains(msg, "unexpected eof"):
		return &orchapp.EngineError{Reason: failuretax.ProviderNetwork, Detail: err.Error()}
	case strings.Contains(msg, "context length"), strings.Contains(msg, "token limit"),
		strings.Contains(msg, "context window"), strings.Contains(msg, "maximum context"):
		return &orchapp.EngineError{Reason: failuretax.AgentCtxOverflow, Detail: err.Error()}
	case strings.Contains(msg, "400"), strings.Contains(msg, "invalid request"),
		strings.Contains(msg, "bad request"):
		return &orchapp.EngineError{Reason: failuretax.ProviderBadRequest, Detail: err.Error()}
	}
	return &orchapp.EngineError{Reason: failuretax.AgentToolFailed, Detail: err.Error()}
}

func RegisterFakeProviderForTest(reply func(prompt string) (string, schema.TokenUsage, error)) {
	RegisterFakeProviderToolsForTest(func(hist []*schema.Message) (FakeStep, schema.TokenUsage, error) {
		prompt := ""
		for _, m := range hist {
			if m.Role == schema.User {
				prompt = m.Content
			}
		}
		c, u, e := reply(prompt)
		return FakeStep{Text: c}, u, e
	})
}

type FakeStep struct {
	ToolName     string
	ToolArgs     string
	Text         string
	MultiContent []schema.ChatMessagePart
}

func RegisterFakeProviderToolsForTest(step func(hist []*schema.Message) (FakeStep, schema.TokenUsage, error)) {
	modelmeta.Register("fake", func(ctx context.Context, rc modelmeta.RuntimeConfig, secret string) (model.BaseChatModel, error) {
		return &fakeModel{step: step}, nil
	})
}

type fakeModel struct {
	step func([]*schema.Message) (FakeStep, schema.TokenUsage, error)
}

func (f *fakeModel) Generate(_ context.Context, input []*schema.Message, _ ...model.Option) (*schema.Message, error) {

	for _, m := range input {
		if m.Role == schema.Tool && len(m.ToolCalls) == 0 {

		}
	}
	approvalDone := false
	for _, m := range input {
		if m.Role == schema.Assistant {
			for _, tc := range m.ToolCalls {
				if tc.Function.Name == "request_approval" {
					approvalDone = true
				}
			}
		}
	}
	st, usage, err := f.step(input)
	if err != nil {
		return nil, err
	}
	if approvalDone && st.ToolName == "request_approval" {
		st = FakeStep{Text: "Final conclusion after the approval completes."}
	}
	if st.ToolName != "" {
		return &schema.Message{
			Role: schema.Assistant,
			ToolCalls: []schema.ToolCall{{
				ID:       "call_" + st.ToolName,
				Function: schema.FunctionCall{Name: st.ToolName, Arguments: st.ToolArgs},
			}},
			ResponseMeta: &schema.ResponseMeta{Usage: &usage},
		}, nil
	}
	return &schema.Message{
		Role: schema.Assistant, Content: st.Text, MultiContent: st.MultiContent,
		ResponseMeta: &schema.ResponseMeta{Usage: &usage},
	}, nil
}

func (f *fakeModel) Stream(ctx context.Context, input []*schema.Message, opts ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	msg, err := f.Generate(ctx, input, opts...)
	if err != nil {
		return nil, err
	}
	return schema.StreamReaderFromArray([]*schema.Message{msg}), nil
}
