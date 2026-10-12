package app

import (
	"context"
	"log/slog"
	"strings"

	"orkome/internal/orchestration/domain"
)

// SummaryService 负责任务结算 summary 的生成（A2 分层上移：从 pgrepo 挪到
// app 层，repo 只存取）。
//
// 两条路径：
//   - 开关关（默认）：纯截断回落 domain.Fallback（= domain.TaskSummary），
//     与上移前的 repo 内实现 byte-identical——"先上代码不改变行为"。
//   - 开关开：LLM 七段 checkpoint——经 Engine.Generate（modelmeta 工厂）
//     做一次独立 aux 小调用；失败 fail-open 回落到纯截断，结算永不被
//     summary 生成卡死。
//
// v0 明确不做暖前缀缓存复用（dsh-borrowing-table-v2 §1.2：orkome 的
// summary 是独立小调用，没有"复刻上次主请求前缀"的管线，抄了不生效）。
type SummaryService struct {
	repo   Repo
	engine Engine
	secret func(ctx context.Context, ws, credID string) (string, error)
	log    *slog.Logger

	// Enabled 是七段 LLM checkpoint 的总开关（默认关；cmd/orkome-server
	// config 的 ORKOME_SUMMARY_LLM_ENABLED 接线到这里）。
	Enabled bool
}

func NewSummaryService(repo Repo, engine Engine,
	secret func(ctx context.Context, ws, credID string) (string, error),
	log *slog.Logger) *SummaryService {
	return &SummaryService{repo: repo, engine: engine, secret: secret, log: log}
}

// ForComplete 生成 completed 结算的 summary：lastText 优先、result 兜底。
// 开关关时与旧 repo 路径（domain.TaskSummary(lastText, result)）byte-identical。
func (s *SummaryService) ForComplete(ctx context.Context, task Task, result string) string {
	if !s.Enabled {
		return domain.Fallback(s.lastText(ctx, task.ID), result)
	}
	material := domain.TaskMaterial{
		TaskID:       task.ID,
		WorkspaceID:  task.WorkspaceID,
		Messages:     s.taskMessages(ctx, task),
		FinalText:    result,
		SessionTurns: s.sessionTurns(ctx, task),
	}
	if out := s.generate(ctx, task, material); out != "" {
		return out
	}
	return domain.Fallback(s.lastText(ctx, task.ID), result)
}

// ForFail 生成 failed 结算的 summary：lastText 优先、reason+": "+errMsg 兜底
// （与旧 repo 路径的 fallback 口径一致）。
func (s *SummaryService) ForFail(ctx context.Context, task Task, reason, errMsg string) string {
	if !s.Enabled {
		return domain.Fallback(s.lastText(ctx, task.ID), reason+": "+errMsg)
	}
	material := domain.TaskMaterial{
		TaskID:       task.ID,
		WorkspaceID:  task.WorkspaceID,
		Messages:     s.taskMessages(ctx, task),
		FinalText:    reason + ": " + errMsg,
		SessionTurns: s.sessionTurns(ctx, task),
	}
	if out := s.generate(ctx, task, material); out != "" {
		return out
	}
	return domain.Fallback(s.lastText(ctx, task.ID), reason+": "+errMsg)
}

// generate 做七段 LLM 调用：组装材料 → Engine.Generate → 校验产出非空。
// 任何一步失败都返回空串，由调用方 fail-open 回落纯截断。
func (s *SummaryService) generate(ctx context.Context, task Task, m domain.TaskMaterial) string {
	if s.engine == nil {
		return ""
	}
	agent, ok, err := s.repo.GetAgent(ctx, task.WorkspaceID, task.AgentID)
	if err != nil || !ok {
		return ""
	}
	sec := s.resolveSecret(ctx, agent)
	out, _, err := s.engine.Generate(ctx, agent, sec,
		domain.SystemPrompt(), domain.UserPrompt(m))
	if err != nil {
		if s.log != nil {
			s.log.Warn("llm summary failed, falling back to truncation",
				"task", task.ID, "err", err.Error())
		}
		return ""
	}
	out = strings.TrimSpace(out)
	if out == "" {
		return ""
	}
	if s.repo != nil {
		_ = s.repo.InsertAuxUsage(ctx, task.WorkspaceID, UsageReport{
			Provider: "summary", Model: "llm-checkpoint", AgentLabel: "summary",
		})
	}
	return out
}

// lastText 读任务最后一条 kind='text' 的 task_message（材料优先级最高的
// 摘要来源；读失败按空串处理，与旧 repo 路径容错一致）。
func (s *SummaryService) lastText(ctx context.Context, taskID string) string {
	if s.repo == nil {
		return ""
	}
	txt, err := s.repo.LastTaskText(ctx, taskID)
	if err != nil {
		return ""
	}
	return txt
}

// taskMessages 拉取任务的执行轨迹（升序，截最新窗口），供七段摘要还原
// "当前工作/错误与修复"等段落。读失败降级为空材料。
func (s *SummaryService) taskMessages(ctx context.Context, task Task) []domain.Message {
	if s.repo == nil {
		return nil
	}
	msgs, err := s.repo.ListTaskMessages(ctx, task.WorkspaceID, task.ID,
		TaskMessagesQuery{Limit: 200, Desc: true})
	if err != nil {
		return nil
	}
	// Desc 取最新 200 条，再翻回升序喂给提示词。
	out := make([]domain.Message, 0, len(msgs))
	for i := len(msgs) - 1; i >= 0; i-- {
		out = append(out, domain.Message{Kind: msgs[i].Kind, Content: msgs[i].Content})
	}
	return out
}

// sessionTurns 拉同 work item + agent 的前序任务产出（跨任务 session 语境，
// 与派发侧 WorkItemSession 同源），供七段摘要还原"原始请求/关键上下文"。
func (s *SummaryService) sessionTurns(ctx context.Context, task Task) []domain.SessionTurn {
	if s.repo == nil || task.WorkItemID == "" {
		return nil
	}
	turns, err := s.repo.SessionTranscript(ctx, task.WorkItemID, task.AgentID, 5)
	if err != nil {
		return nil
	}
	out := make([]domain.SessionTurn, 0, len(turns))
	for _, t := range turns {
		out = append(out, domain.SessionTurn{Role: t.Role, Content: t.Content})
	}
	return out
}

func (s *SummaryService) resolveSecret(ctx context.Context, agent Agent) string {
	if s.secret == nil {
		return ""
	}
	id, _ := agent.RuntimeConfig["credential_id"].(string)
	if id == "" {
		return ""
	}
	sec, err := s.secret(ctx, agent.WorkspaceID, id)
	if err != nil {
		return ""
	}
	return sec
}
