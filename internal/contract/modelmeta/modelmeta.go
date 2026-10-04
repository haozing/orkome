package modelmeta

import (
	"context"
	"fmt"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

type RuntimeConfig struct {
	Provider     string         `json:"provider"`
	Model        string         `json:"model"`
	BaseURL      string         `json:"base_url,omitempty"`
	CredentialID string         `json:"credential_id,omitempty"`
	Params       map[string]any `json:"params,omitempty"`
}

type Factory func(ctx context.Context, rc RuntimeConfig, secret string) (model.BaseChatModel, error)

var registry = map[string]Factory{}

func Register(provider string, f Factory) { registry[provider] = f }

func Build(ctx context.Context, rc RuntimeConfig, secret string) (model.BaseChatModel, error) {
	f, ok := registry[rc.Provider]
	if !ok {
		return nil, fmt.Errorf("modelmeta: unknown provider %q", rc.Provider)
	}
	return f(ctx, rc, secret)
}

func Known(provider string) bool { _, ok := registry[provider]; return ok }

func ValidConfig(rc RuntimeConfig) bool {
	return rc.Provider != "" && rc.Model != "" && Known(rc.Provider)
}

var _ = schema.AssistantMessage
