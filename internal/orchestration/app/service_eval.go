package app

import (
	"context"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"strings"
	"time"
)

type EvalReporter interface {
	WriteEvalReport(ctx context.Context, workspaceID, userID, taskID, title, body string) error
}

type EvalService struct {
	engine       Engine
	repo         Repo
	notification EvalReporter
	secret       func(ctx context.Context, ws, credID string) (string, error)
	log          func(msg string, args ...any)

	Rate       float64
	DailyQuota int
	Now        func() time.Time
}

func NewEvalService(engine Engine, repo Repo, notification EvalReporter,
	secret func(ctx context.Context, ws, credID string) (string, error),
	log func(msg string, args ...any)) *EvalService {
	return &EvalService{engine: engine, repo: repo, notification: notification, secret: secret, log: log,
		Rate: 0.1, DailyQuota: 100, Now: time.Now}
}

func (s *EvalService) sampled(taskID string) bool {
	if s.Rate <= 0 {
		return false
	}
	if s.Rate >= 1 {
		return true
	}
	h := fnv.New32a()
	_, _ = h.Write([]byte(taskID))
	return float64(h.Sum32()%10000) < s.Rate*10000
}

const evalSystem = "You are a task execution quality evaluator. Score the task output 1-5 with a one-line comment." +
	"Output only JSON: {\"score\":1-5,\"comment\":\"one sentence\"} and nothing else."

func (s *EvalService) OnTaskCompleted(ctx context.Context, task Task) {
	if !s.sampled(task.ID) {
		return
	}
	if task.OriginatorUserID == "" {
		return
	}

	if used, err := s.repo.CountEvalToday(ctx, task.WorkspaceID); err == nil && s.DailyQuota > 0 && used >= s.DailyQuota {
		if s.log != nil {
			s.log("eval skipped: daily quota exceeded", "ws", task.WorkspaceID, "used", used)
		}
		return
	}
	agent, ok, err := s.repo.GetAgent(ctx, task.WorkspaceID, task.AgentID)
	if err != nil || !ok {
		return
	}
	sec := ""
	if s.secret != nil {
		if id, _ := agent.RuntimeConfig["credential_id"].(string); id != "" {
			sec, _ = s.secret(ctx, task.WorkspaceID, id)
		}
	}

	rules := evalRules(task)

	score, comment, llmOK := 0, "", false
	if out, _, err := s.engine.Generate(ctx, agent, sec, evalSystem,
		"Task output:\n"+truncRunes(task.Result, 2000)+"\n\nPlease score."); err == nil {
		score, comment, llmOK = parseEvalOut(out)
	}

	var b strings.Builder
	fmt.Fprintf(&b, "Rule checks:\n")
	for _, r := range rules {
		fmt.Fprintf(&b, "- %s\n", r)
	}
	if llmOK {
		fmt.Fprintf(&b, "\nLLM score: %d/5\nComment: %s\n", score, comment)
	} else {
		b.WriteString("\nLLM score: unavailable (rules-only report this time)\n")
	}
	title := fmt.Sprintf("task quality evaluation (%s)", shortID(task.ID))

	if err := s.notification.WriteEvalReport(ctx, task.WorkspaceID, task.OriginatorUserID, task.ID, title, b.String()); err != nil && s.log != nil {
		s.log("eval report write failed", "err", err, "task", task.ID, "ws", task.WorkspaceID)
	}

	if err := s.repo.InsertAuxUsage(ctx, task.WorkspaceID, UsageReport{
		Provider: "eval", Model: "rules+llm", AgentLabel: "eval",
	}); err != nil && s.log != nil {
		s.log("eval usage row write failed", "err", err, "task", task.ID, "ws", task.WorkspaceID)
	}
}

func evalRules(t Task) []string {
	var out []string
	switch {
	case strings.TrimSpace(t.Result) == "":
		out = append(out, "output empty: fail")
	case len([]rune(t.Result)) < 20:
		out = append(out, "output too short (<20 chars): suspicious")
	case len([]rune(t.Result)) > 8000:
		out = append(out, "output too long (>8000 chars): suggest summary")
	default:
		out = append(out, "output length normal")
	}
	if t.Attempt > 1 {
		out = append(out, fmt.Sprintf("took %d attempts to complete: stability to watch", t.Attempt))
	}
	return out
}

func parseEvalOut(raw string) (int, string, bool) {
	raw = strings.TrimSpace(raw)
	if i := strings.Index(raw, "{"); i > 0 {
		raw = raw[i:]
	}
	if j := strings.LastIndex(raw, "}"); j >= 0 && j < len(raw)-1 {
		raw = raw[:j+1]
	}
	var out struct {
		Score   int    `json:"score"`
		Comment string `json:"comment"`
	}
	if json.Unmarshal([]byte(raw), &out) != nil || out.Score < 1 || out.Score > 5 {
		return 0, "", false
	}
	return out.Score, out.Comment, true
}

func truncRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

func shortID(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}
