package engine

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	einoopenai "github.com/cloudwego/eino-ext/components/model/openai"
	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"orkome/internal/contract/failuretax"
	"orkome/internal/contract/modelmeta"
	orchapp "orkome/internal/orchestration/app"
	"orkome/internal/platform/egressx"
)

func zeroBackoff(context.Context, int) time.Duration { return 0 }

func apiErr(code int) *einoopenai.APIError {
	return &einoopenai.APIError{HTTPStatusCode: code, Message: "provider error"}
}

func TestClassifyModelErr_Table(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want retryClass
	}{
		{"429 structured", apiErr(429), classTransient},
		{"408 structured", apiErr(408), classTransient},
		{"503 structured", apiErr(503), classTransient},
		{"500 structured", apiErr(500), classTransient},
		{"401 structured", apiErr(401), classAuth},
		{"403 structured", apiErr(403), classAuth},
		{"400 structured", apiErr(400), classRequest},
		{"404 structured", apiErr(404), classRequest},
		{"rate-limited text", errors.New("Rate limit reached, please retry"), classTransient},
		{"timeout text", errors.New("context deadline exceeded"), classTransient},
		{"network text", errors.New("dial tcp: connection refused"), classTransient},
		{"auth text", errors.New("invalid api key provided"), classAuth},
		{"context-overflow text", errors.New("this model's maximum context length is 8192 tokens"), classRequest},
		{"unknown errors conservatively judged request", errors.New("something exotic"), classRequest},
		{"nil", nil, classRequest},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, classifyModelErr(tc.err))
		})
	}
}

type seqModel struct {
	errs  []error
	calls atomic.Int32
	text  string
}

func (m *seqModel) Generate(_ context.Context, _ []*schema.Message, _ ...model.Option) (*schema.Message, error) {
	n := int(m.calls.Add(1))
	if n <= len(m.errs) {
		return nil, m.errs[n-1]
	}
	return &schema.Message{Role: schema.Assistant, Content: m.text,
		ResponseMeta: &schema.ResponseMeta{Usage: &schema.TokenUsage{PromptTokens: 3, CompletionTokens: 5}}}, nil
}

func (m *seqModel) Stream(ctx context.Context, in []*schema.Message, opts ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	msg, err := m.Generate(ctx, in, opts...)
	if err != nil {
		return nil, err
	}
	return schema.StreamReaderFromArray([]*schema.Message{msg}), nil
}

func (m *seqModel) callCount() int { return int(m.calls.Load()) }

func runAgent(t *testing.T, m model.BaseChatModel, retry *adk.ModelRetryConfig, failover *adk.ModelFailoverConfig[*schema.Message]) error {
	t.Helper()
	a, err := adk.NewChatModelAgent(context.Background(), &adk.ChatModelAgentConfig{
		Name: "retry-test", Instruction: "test",
		Model: m, ModelRetryConfig: retry, ModelFailoverConfig: failover,
	})
	require.NoError(t, err)
	r := adk.NewRunner(context.Background(), adk.RunnerConfig{Agent: a})
	iter := r.Query(context.Background(), "hello")
	for {
		ev, ok := iter.Next()
		if !ok {
			return nil
		}
		if ev.Err != nil {
			return ev.Err
		}
	}
}

func TestModelRetry_AbsorbsTransient(t *testing.T) {
	m := &seqModel{errs: []error{
		errors.New("error, status code: 429, message: rate limited"),
		errors.New("error, status code: 429, message: rate limited"),
	}, text: "done"}
	stats := &modelRetryStats{}
	err := runAgent(t, m, newModelRetryConfig(stats, zeroBackoff), nil)
	require.NoError(t, err, "transient sequence must be absorbed")
	assert.Equal(t, 3, m.callCount())
	assert.Equal(t, int32(2), stats.retries.Load(), "absorbs 2 retries (I-E6 metering)")
	assert.Equal(t, int32(0), stats.failovers.Load())
}

func TestModelRetry_NonTransientFailsFast(t *testing.T) {
	m := &seqModel{errs: []error{
		errors.New("error, status code: 401, message: invalid api key"),
		errors.New("should never happen"),
	}, text: "done"}
	stats := &modelRetryStats{}
	err := runAgent(t, m, newModelRetryConfig(stats, zeroBackoff), nil)
	require.Error(t, err, "non-transient errors must fail directly (internal archive 09 §13)")
	assert.Equal(t, 1, m.callCount(), "401 must not retry")
	assert.Zero(t, stats.retries.Load())
	assert.Contains(t, err.Error(), "401")
}

func TestModelRetry_ExhaustionCounting(t *testing.T) {
	m := &seqModel{errs: []error{
		errors.New("error, status code: 429, message: rate limited"),
		errors.New("error, status code: 429, message: rate limited"),
		errors.New("error, status code: 429, message: rate limited"),
		errors.New("error, status code: 429, message: rate limited"),
		errors.New("should never happen"),
	}, text: "done"}
	stats := &modelRetryStats{}
	err := runAgent(t, m, newModelRetryConfig(stats, zeroBackoff), nil)
	require.Error(t, err)
	assert.Equal(t, 4, m.callCount(), "exhausted after 1 initial + 3 retries")
	assert.Equal(t, int32(3), stats.retries.Load())
	var re *adk.RetryExhaustedError
	require.ErrorAs(t, err, &re, "exhaustion error must be adk.RetryExhaustedError")
	assert.Equal(t, failuretax.ProviderOverload, reasonOf(normalize(err)),
		"normalized to the last raw error after shelling (agent_provider.overload)")
}

func TestModelFailover_SwitchesCredential(t *testing.T) {
	primary := &seqModel{errs: []error{
		errors.New("error, status code: 503, message: upstream unavailable"),
		errors.New("error, status code: 503, message: upstream unavailable"),
		errors.New("error, status code: 503, message: upstream unavailable"),
		errors.New("error, status code: 503, message: upstream unavailable"),
		errors.New("should never happen"),
	}, text: "p"}
	backup := &seqModel{text: "backup-ok"}
	modelmeta.Register("failchain", func(_ context.Context, _ modelmeta.RuntimeConfig, secret string) (model.BaseChatModel, error) {
		if secret == "s2" {
			return backup, nil
		}
		return primary, nil
	})
	rc := modelmeta.RuntimeConfig{Provider: "failchain", Model: "m", CredentialID: "c1"}
	stats := &modelRetryStats{}
	fc := newModelFailoverConfig(context.Background(), rc, func(context.Context) ([]string, error) {
		return []string{"s2"}, nil
	}, stats)
	err := runAgent(t, primary, newModelRetryConfig(stats, zeroBackoff), fc)
	require.NoError(t, err, "failover to the second credential must succeed")
	assert.Equal(t, 4, primary.callCount())
	assert.Equal(t, 1, backup.callCount())
	assert.Equal(t, int32(3), stats.retries.Load())
	assert.Equal(t, int32(1), stats.failovers.Load(), "exactly one credential switch")
}

func TestModelFailover_CappedAtTwoSwitches(t *testing.T) {
	always429 := func() *seqModel {
		return &seqModel{errs: []error{
			errors.New("error, status code: 429, message: rate limited"),
			errors.New("error, status code: 429, message: rate limited"),
			errors.New("error, status code: 429, message: rate limited"),
			errors.New("error, status code: 429, message: rate limited"),
			errors.New("should never happen"),
		}}
	}
	p1, p2 := always429(), always429()
	third := &seqModel{text: "third-ok"}
	modelmeta.Register("capchain", func(_ context.Context, _ modelmeta.RuntimeConfig, secret string) (model.BaseChatModel, error) {
		switch secret {
		case "s2":
			return p2, nil
		case "s3":
			return third, nil
		}
		return p1, nil
	})
	rc := modelmeta.RuntimeConfig{Provider: "capchain", Model: "m", CredentialID: "c1"}
	stats := &modelRetryStats{}
	fc := newModelFailoverConfig(context.Background(), rc, func(context.Context) ([]string, error) {
		return []string{"s2", "s3", "s4-never"}, nil
	}, stats)
	err := runAgent(t, p1, newModelRetryConfig(stats, zeroBackoff), fc)
	require.NoError(t, err)
	assert.Equal(t, int32(2), stats.failovers.Load(), "at most 2 switches (third unreachable)")
	assert.Equal(t, 1, third.callCount(), "the second switch lands on the third credential")
}

func TestModelFailover_ExhaustedNormalized(t *testing.T) {
	always429 := func() *seqModel {
		return &seqModel{errs: []error{
			errors.New("error, status code: 429, message: rate limited"),
			errors.New("error, status code: 429, message: rate limited"),
			errors.New("error, status code: 429, message: rate limited"),
			errors.New("error, status code: 429, message: rate limited"),
			errors.New("should never happen"),
		}}
	}
	primary, backup := always429(), always429()
	modelmeta.Register("exhchain", func(_ context.Context, _ modelmeta.RuntimeConfig, secret string) (model.BaseChatModel, error) {
		if secret == "s2" {
			return backup, nil
		}
		return primary, nil
	})
	rc := modelmeta.RuntimeConfig{Provider: "exhchain", Model: "m", CredentialID: "c1"}
	stats := &modelRetryStats{}
	fc := newModelFailoverConfig(context.Background(), rc, func(context.Context) ([]string, error) {
		return []string{"s2"}, nil
	}, stats)
	err := runAgent(t, primary, newModelRetryConfig(stats, zeroBackoff), fc)
	require.Error(t, err, "chain exhaustion must fail")
	assert.Equal(t, int32(6), stats.retries.Load(), "two credentials each exhaust 3 attempts")
	assert.Equal(t, int32(1), stats.failovers.Load())
	assert.Equal(t, failuretax.ProviderOverload, reasonOf(normalize(err)),
		"exhaustion error normalized to the last raw error (must not stop at failover internal errors)")
}

func TestModelFailover_RequestFatalNoSwitch(t *testing.T) {
	primary := &seqModel{errs: []error{
		errors.New("error, status code: 400, message: invalid request"),
		errors.New("should never happen"),
	}, text: "p"}
	backup := &seqModel{text: "backup-ok"}
	modelmeta.Register("reqchain", func(_ context.Context, _ modelmeta.RuntimeConfig, secret string) (model.BaseChatModel, error) {
		if secret == "s2" {
			return backup, nil
		}
		return primary, nil
	})
	rc := modelmeta.RuntimeConfig{Provider: "reqchain", Model: "m", CredentialID: "c1"}
	stats := &modelRetryStats{}
	fc := newModelFailoverConfig(context.Background(), rc, func(context.Context) ([]string, error) {
		return []string{"s2"}, nil
	}, stats)
	err := runAgent(t, primary, newModelRetryConfig(stats, zeroBackoff), fc)
	require.Error(t, err)
	assert.Equal(t, 1, primary.callCount())
	assert.Zero(t, backup.callCount(), "400 unrelated to credentials, no switch")
	assert.Zero(t, stats.failovers.Load())
}

func TestAuxRetryModel_Generate(t *testing.T) {
	m := &seqModel{errs: []error{
		errors.New("error, status code: 429, message: rate limited"),
		errors.New("error, status code: 429, message: rate limited"),
	}, text: "aux"}
	a := newAuxRetryModel(m, &modelRetryStats{})
	a.backoff = zeroBackoff
	msg, err := a.Generate(context.Background(), []*schema.Message{schema.UserMessage("q")})
	require.NoError(t, err)
	assert.Equal(t, "aux", msg.Content)
	assert.Equal(t, 3, m.callCount())

	m2 := &seqModel{errs: []error{errors.New("error, status code: 400, message: bad")}, text: "x"}
	a2 := newAuxRetryModel(m2, &modelRetryStats{})
	a2.backoff = zeroBackoff
	_, err = a2.Generate(context.Background(), []*schema.Message{schema.UserMessage("q")})
	require.Error(t, err)
	assert.Equal(t, 1, m2.callCount(), "400 no retry")
}

func TestBackoffExponential(t *testing.T) {
	assert.Equal(t, 200*time.Millisecond, backoffFn(context.Background(), 1))
	assert.Equal(t, 400*time.Millisecond, backoffFn(context.Background(), 2))
	assert.Equal(t, 800*time.Millisecond, backoffFn(context.Background(), 3))
	assert.Equal(t, 2*time.Second, backoffFn(context.Background(), 8), "capped at 2s")
}

func reasonOf(err error) string {
	var ee *orchapp.EngineError
	if errors.As(err, &ee) {
		return ee.Reason
	}
	return ""
}

func retryTaskAgent(provider string) (orchapp.Task, orchapp.Agent) {
	task := orchapp.Task{ID: "00000000-0000-0000-0000-0000000000aa", WorkspaceID: "ws1",
		WorkItemID: "00000000-0000-0000-0000-0000000000bb",
		Context:    map[string]any{"trigger": "assignment", "work_item_title": "T"}}
	agent := orchapp.Agent{ID: "agent-0001", WorkspaceID: "ws1",
		RuntimeConfig: map[string]any{"provider": provider, "model": "m", "credential_id": "c1"}}
	return task, agent
}

func TestEngineRun_UsageReportsRetryCounts(t *testing.T) {
	var calls atomic.Int32
	RegisterFakeProviderForTest(func(string) (string, schema.TokenUsage, error) {
		if calls.Add(1) <= 2 {
			return "", schema.TokenUsage{}, errors.New("error, status code: 429, message: rate limited")
		}
		return "conclusion", schema.TokenUsage{PromptTokens: 1, CompletionTokens: 2}, nil
	})
	e := &EinoEngine{RetryBackoffFunc: zeroBackoff}
	task, agent := retryTaskAgent("fake")
	out, err := e.Execute(context.Background(), task, agent, "")
	require.NoError(t, err)
	assert.Equal(t, "conclusion", out.Output)
	assert.Equal(t, 2, out.Usage.ModelRetries, "absorbs 2 within the run (I-E6)")
	assert.Zero(t, out.Usage.ModelFailovers)
}

func TestEngineRun_FailoverWired(t *testing.T) {
	always429 := func() *seqModel {
		return &seqModel{errs: []error{
			errors.New("error, status code: 429, message: rate limited"),
			errors.New("error, status code: 429, message: rate limited"),
			errors.New("error, status code: 429, message: rate limited"),
			errors.New("error, status code: 429, message: rate limited"),
			errors.New("should never happen"),
		}}
	}
	primary := always429()
	backup := &seqModel{text: "backup-conclusion"}
	modelmeta.Register("engchain", func(_ context.Context, _ modelmeta.RuntimeConfig, secret string) (model.BaseChatModel, error) {
		if secret == "s2" {
			return backup, nil
		}
		return primary, nil
	})
	e := &EinoEngine{RetryBackoffFunc: zeroBackoff,
		FailoverCredentials: func(context.Context, string, string, string, int) ([]string, error) {
			return []string{"s2"}, nil
		}}
	task, agent := retryTaskAgent("engchain")
	out, err := e.Execute(context.Background(), task, agent, "s1")
	require.NoError(t, err)
	assert.Equal(t, "backup-conclusion", out.Output)
	assert.Equal(t, 3, out.Usage.ModelRetries)
	assert.Equal(t, 1, out.Usage.ModelFailovers)
}

func TestEngineRun_ExhaustedErrorNormalized(t *testing.T) {
	RegisterFakeProviderForTest(func(string) (string, schema.TokenUsage, error) {
		return "", schema.TokenUsage{}, errors.New("error, status code: 429, message: rate limited")
	})
	e := &EinoEngine{RetryBackoffFunc: zeroBackoff}
	task, agent := retryTaskAgent("fake")
	_, err := e.Execute(context.Background(), task, agent, "")
	require.Error(t, err)
	assert.Equal(t, failuretax.ProviderOverload, reasonOf(err),
		"exhaustion errors must normalize to agent_provider.overload")
}

func TestOpenAIProviderRegistered(t *testing.T) {
	assert.True(t, modelmeta.Known("openai"))
	rc := modelmeta.RuntimeConfig{Provider: "openai", Model: "gpt-5"}
	assert.True(t, modelmeta.ValidConfig(rc), "openai+model should pass enqueue validation")
}

func TestOpenAIFactory_Builds(t *testing.T) {
	m, err := modelmeta.Build(context.Background(),
		modelmeta.RuntimeConfig{Provider: "openai", Model: "gpt-5", CredentialID: "c"}, "sk-test")
	require.NoError(t, err)
	require.NotNil(t, m)
}

func TestOpenAIFactory_SSRFGate(t *testing.T) {
	defer SetEgressGuard(egressx.DefaultGuard())
	_, err := modelmeta.Build(context.Background(),
		modelmeta.RuntimeConfig{Provider: "openai", Model: "gpt-5", BaseURL: "https://127.0.0.1:8443/v1"}, "k")
	require.Error(t, err, "private base_url must be rejected (SSRF gate)")
	assert.Contains(t, err.Error(), "SSRF")

	g, gerr := egressx.NewGuard([]string{"127.0.0.0/8"})
	require.NoError(t, gerr)
	SetEgressGuard(g)
	_, err = modelmeta.Build(context.Background(),
		modelmeta.RuntimeConfig{Provider: "openai", Model: "gpt-5", BaseURL: "https://127.0.0.1:8443/v1"}, "k")
	require.NoError(t, err, "allowed inside the exception ranges (ENGINE_EGRESS_EXTRA_ALLOW_CIDRS semantics)")
}

func TestApplyOpenAIParams(t *testing.T) {
	cfg := &einoopenai.ChatModelConfig{}
	applyOpenAIParams(cfg, map[string]any{
		"temperature": 0.2, "max_tokens": 1024.0, "thinking": "high", "unknown_key": "x",
	})
	require.NotNil(t, cfg.Temperature)
	assert.InDelta(t, 0.2, *cfg.Temperature, 1e-6)
	require.NotNil(t, cfg.MaxTokens)
	assert.Equal(t, 1024, *cfg.MaxTokens)
	assert.Equal(t, einoopenai.ReasoningEffortLevelHigh, cfg.ReasoningEffort)
}

func TestGenerateUsageReportsRetries(t *testing.T) {
	var calls atomic.Int32
	RegisterFakeProviderForTest(func(string) (string, schema.TokenUsage, error) {
		if calls.Add(1) == 1 {
			return "", schema.TokenUsage{}, errors.New("error, status code: 503, message: unavailable")
		}
		return "aux-out", schema.TokenUsage{PromptTokens: 2}, nil
	})
	e := &EinoEngine{RetryBackoffFunc: zeroBackoff}
	agent := orchapp.Agent{ID: "agent-0001", WorkspaceID: "ws1",
		RuntimeConfig: map[string]any{"provider": "fake", "model": "m"}}
	text, u, err := e.Generate(context.Background(), agent, "k", "sys", "in")
	require.NoError(t, err)
	assert.Equal(t, "aux-out", text)
	assert.Equal(t, 1, u.ModelRetries)
	assert.Zero(t, u.ModelFailovers, "auxiliary path without failover (internal archive 09 §16.3 best-effort)")
}
