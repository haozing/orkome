package app

import (
	"context"
	"errors"
	"strings"
	"testing"

	"orkome/internal/orchestration/domain"
)

// summaryFakeRepo 是分层职责单测的假仓储：嵌入 Repo 接口（未实现的方法
// panic 即可，测试路径不会触达），只覆写 summary 相关的读与写——验证
// "repo 只提供材料读取与成品落存，不做任何 summary 计算"的契约形状。
type summaryFakeRepo struct {
	Repo
	lastText       string
	messages       []TaskMessage
	sessionTurns   []SessionTurn
	agent          Agent
	completeCalled bool
	completeSum    string
	failCalled     bool
	failSum        string
	auxUsage       []UsageReport
}

func (f *summaryFakeRepo) LastTaskText(ctx context.Context, taskID string) (string, error) {
	return f.lastText, nil
}

func (f *summaryFakeRepo) ListTaskMessages(ctx context.Context, ws, taskID string, q TaskMessagesQuery) ([]TaskMessage, error) {
	return f.messages, nil
}

func (f *summaryFakeRepo) SessionTranscript(ctx context.Context, wi, agentID string, limit int) ([]SessionTurn, error) {
	return f.sessionTurns, nil
}

func (f *summaryFakeRepo) GetAgent(ctx context.Context, ws, id string) (Agent, bool, error) {
	return f.agent, true, nil
}

func (f *summaryFakeRepo) InsertAuxUsage(ctx context.Context, ws string, u UsageReport) error {
	f.auxUsage = append(f.auxUsage, u)
	return nil
}

func (f *summaryFakeRepo) CompleteRun(ctx context.Context, id, result, sessionRef, engineTag, summary string, usage UsageReport) error {
	f.completeCalled, f.completeSum = true, summary
	return nil
}

func (f *summaryFakeRepo) FailRun(ctx context.Context, id, reason, errMsg, summary string) (bool, error) {
	f.failCalled, f.failSum = true, summary
	return false, nil
}

// summaryFakeEngine 记录 Generate 的入参，回放固定输出。
type summaryFakeEngine struct {
	generateCalls int
	system        string
	input         string
	out           string
	err           error
}

func (e *summaryFakeEngine) Name() string { return "fake" }

func (e *summaryFakeEngine) Execute(ctx context.Context, task Task, agent Agent, secret string) (EngineOutput, error) {
	return EngineOutput{}, errors.New("unused")
}
func (e *summaryFakeEngine) Resume(ctx context.Context, task Task, agent Agent, secret, targetID string, d ApprovalDecision) (EngineOutput, error) {
	return EngineOutput{}, errors.New("unused")
}

func (e *summaryFakeEngine) Generate(ctx context.Context, agent Agent, secret, system, input string) (string, UsageReport, error) {
	e.generateCalls++
	e.system, e.input = system, input
	return e.out, UsageReport{}, e.err
}

func newSummaryFixture() (*summaryFakeRepo, *summaryFakeEngine) {
	repo := &summaryFakeRepo{
		lastText: "conclusion: adopted go-feed",
		messages: []TaskMessage{
			{Seq: 1, Kind: "text", Content: "midway thought"},
			{Seq: 2, Kind: "tool_use", Content: "search_workspace {}"},
			{Seq: 3, Kind: "tool_result", Content: "raw output"},
			{Seq: 4, Kind: "text", Content: "conclusion: adopted go-feed"},
		},
		agent: Agent{ID: "ag-1", WorkspaceID: "ws-1", RuntimeConfig: map[string]any{}},
	}
	eng := &summaryFakeEngine{out: "## Current Work\n- checkpoint body"}
	return repo, eng
}

func summaryTestTask() Task {
	return Task{ID: "task-1", WorkspaceID: "ws-1", AgentID: "ag-1"}
}

// TestSummaryServiceDisabledByteIdentical —— 验收项②：开关关闭时，
// SummaryService 的输出与旧 repo 内路径（domain.TaskSummary(lastText, fallback)）
// byte-identical，且完全不触碰 Engine。
func TestSummaryServiceDisabledByteIdentical(t *testing.T) {
	repo, eng := newSummaryFixture()
	svc := NewSummaryService(repo, eng, nil, nil) // Enabled 零值 = 关

	task := summaryTestTask()
	result := strings.Repeat("R", 600)

	if got, want := svc.ForComplete(ctx0s(), task, result),
		domain.TaskSummary(repo.lastText, result); got != want {
		t.Fatalf("ForComplete 开关关时必须与旧路径 byte-identical:\ngot:  %q\nwant: %q", got, want)
	}
	if got, want := svc.ForFail(ctx0s(), task, "engine_error", "boom"),
		domain.TaskSummary(repo.lastText, "engine_error: boom"); got != want {
		t.Fatalf("ForFail 开关关时必须与旧路径 byte-identical:\ngot:  %q\nwant: %q", got, want)
	}
	if eng.generateCalls != 0 {
		t.Fatalf("开关关时不允许任何 LLM 调用，got %d 次", eng.generateCalls)
	}
	if len(repo.auxUsage) != 0 {
		t.Fatalf("开关关时不应记 aux usage")
	}
}

// TestSummaryServiceEnabledUsesSevenSectionPrompt —— 开关开时走
// Engine.Generate，system 是七段提示词，user 材料含轨迹与最终文本；
// 产出透传，aux usage 记一行（可观测）。
func TestSummaryServiceEnabledUsesSevenSectionPrompt(t *testing.T) {
	repo, eng := newSummaryFixture()
	svc := NewSummaryService(repo, eng, nil, nil)
	svc.Enabled = true

	out := svc.ForComplete(ctx0s(), summaryTestTask(), "final answer")
	if out != eng.out {
		t.Fatalf("开关开时 summary 应为 LLM 产出 %q, got %q", eng.out, out)
	}
	if eng.system != domain.SystemPrompt() {
		t.Fatalf("system 必须是七段提示词")
	}
	for _, want := range []string{
		"=== Task final text ===", "final answer",
		"=== Task message log (oldest → newest) ===",
		"[text] conclusion: adopted go-feed",
		"[tool_result] raw output",
	} {
		if !strings.Contains(eng.input, want) {
			t.Errorf("user 材料缺 %q", want)
		}
	}
	if len(repo.auxUsage) != 1 || repo.auxUsage[0].AgentLabel != "summary" {
		t.Fatalf("LLM 路径应记一行 summary aux usage, got %+v", repo.auxUsage)
	}
}

// TestSummaryServiceEnabledFailOpen —— LLM 调用失败/空产出时 fail-open
// 回落纯截断（结算永不被 summary 卡死），与旧路径 byte-identical。
func TestSummaryServiceEnabledFailOpen(t *testing.T) {
	repo, eng := newSummaryFixture()
	eng.err = errors.New("provider 500")
	svc := NewSummaryService(repo, eng, nil, nil)
	svc.Enabled = true

	if got, want := svc.ForComplete(ctx0s(), summaryTestTask(), "res"),
		domain.TaskSummary(repo.lastText, "res"); got != want {
		t.Fatalf("LLM 失败回落必须 byte-identical:\ngot:  %q\nwant: %q", got, want)
	}

	eng.err = nil
	eng.out = "   "
	if got, want := svc.ForFail(ctx0s(), summaryTestTask(), "r", "d"),
		domain.TaskSummary(repo.lastText, "r: d"); got != want {
		t.Fatalf("LLM 空产出回落必须 byte-identical:\ngot:  %q\nwant: %q", got, want)
	}
}

// TestSummaryServiceForFailPrompt —— 失败路径的最终文本口径是
// reason+": "+errMsg（与旧 fallback 一致），供 Errors and Fixes 段落还原。
func TestSummaryServiceForFailPrompt(t *testing.T) {
	repo, eng := newSummaryFixture()
	svc := NewSummaryService(repo, eng, nil, nil)
	svc.Enabled = true
	eng.out = "checkpoint"

	svc.ForFail(ctx0s(), summaryTestTask(), "provider.timeout", "deadline exceeded")
	if !strings.Contains(eng.input, "provider.timeout: deadline exceeded") {
		t.Fatalf("失败材料应含 reason+errMsg 拼接口径, input=%q", eng.input)
	}
}

// TestSettlementLayering —— 分层职责验收项③：SettlementService 持 summary
// 生成，repo 只收成品（CompleteRun/FailRun 的 summary 参数 = 生成结果）。
// 未接线 SummaryService 时（老用法/老测试），回落 domain.TaskSummary，
// 与上移前 repo 内行为 byte-identical。
func TestSettlementLayering(t *testing.T) {
	repo, _ := newSummaryFixture()
	settle := &SettlementService{repo: repo}
	task := summaryTestTask()

	// 未接线：byte-identical 回落
	if got := settle.summaryForComplete(ctx0s(), task, "res"); got != domain.TaskSummary(repo.lastText, "res") {
		t.Fatalf("未接线时 ForComplete 应回落旧路径, got %q", got)
	}
	if got := settle.summaryForFail(ctx0s(), task, "r", "d"); got != domain.TaskSummary(repo.lastText, "r: d") {
		t.Fatalf("未接线时 ForFail 应回落旧路径, got %q", got)
	}

	// 接线后：走 SummaryService（此处仍开关关，产出等同旧路径）
	eng := &summaryFakeEngine{}
	ss := NewSummaryService(repo, eng, nil, nil)
	settle.SetSummarySummarizer(ss)
	if got := settle.summaryForComplete(ctx0s(), task, "res"); got != domain.TaskSummary(repo.lastText, "res") {
		t.Fatalf("接线+开关关应仍 byte-identical, got %q", got)
	}
	if eng.generateCalls != 0 {
		t.Fatalf("开关关不应触达 Engine")
	}
}

func ctx0s() context.Context { return context.Background() }
