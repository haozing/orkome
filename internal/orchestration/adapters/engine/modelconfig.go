package engine

import (
	"context"
	"fmt"
	"sync"

	einoopenai "github.com/cloudwego/eino-ext/components/model/openai"
	"github.com/cloudwego/eino/components/model"

	"orkome/internal/contract/modelmeta"
	"github.com/haozing/ploykit/platform/egressx"
)

var (
	egressMu    sync.RWMutex
	egressGuard = egressx.DefaultGuard()
)

func SetEgressGuard(g *egressx.Guard) {
	if g == nil {
		return
	}
	egressMu.Lock()
	egressGuard = g
	egressMu.Unlock()
}

func currentEgressGuard() *egressx.Guard {
	egressMu.RLock()
	defer egressMu.RUnlock()
	return egressGuard
}

func init() {
	modelmeta.Register("openai", openaiFactory)
}

func openaiFactory(ctx context.Context, rc modelmeta.RuntimeConfig, secret string) (model.BaseChatModel, error) {
	if rc.BaseURL != "" {
		if err := currentEgressGuard().ValidateURL(ctx, rc.BaseURL); err != nil {
			return nil, fmt.Errorf("base_url failed the SSRF gate: %w", err)
		}
	}
	cfg := &einoopenai.ChatModelConfig{
		APIKey:  secret,
		BaseURL: rc.BaseURL,
		Model:   rc.Model,
	}
	applyOpenAIParams(cfg, rc.Params)
	return einoopenai.NewChatModel(ctx, cfg)
}

var _ modelmeta.Factory = openaiFactory

func applyOpenAIParams(cfg *einoopenai.ChatModelConfig, params map[string]any) {
	if len(params) == 0 {
		return
	}
	if v, ok := numeric(params["temperature"]); ok {
		cfg.Temperature = ptr(float32(v))
	}
	if v, ok := numeric(params["top_p"]); ok {
		cfg.TopP = ptr(float32(v))
	}
	if v, ok := params["max_tokens"]; ok {
		if n, ok := toInt(v); ok {
			cfg.MaxTokens = &n
		}
	}
	if v, ok := params["max_completion_tokens"]; ok {
		if n, ok := toInt(v); ok {
			cfg.MaxCompletionTokens = &n
		}
	}
	if s, ok := params["thinking"].(string); ok {
		switch s {
		case "low":
			cfg.ReasoningEffort = einoopenai.ReasoningEffortLevelLow
		case "medium":
			cfg.ReasoningEffort = einoopenai.ReasoningEffortLevelMedium
		case "high":
			cfg.ReasoningEffort = einoopenai.ReasoningEffortLevelHigh
		}
	}
}

func numeric(v any) (float64, bool) {
	f, ok := v.(float64)
	return f, ok
}

func toInt(v any) (int, bool) {
	switch n := v.(type) {
	case float64:
		return int(n), true
	case int:
		return n, true
	}
	return 0, false
}

func ptr[T any](v T) *T { return &v }
