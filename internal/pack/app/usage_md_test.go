package app

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// usageDocFixture 组一个五区块齐全的 pack（fakeRepo 复用 service_test.go 的）。
func usageDocFixture(t *testing.T) (*fakeRepo, Ports) {
	t.Helper()
	repo := newFakeRepo()
	_, err := repo.Upsert(context.Background(), "ws-1", "agentblog", "1.2.0")
	require.NoError(t, err)
	return repo, Ports{}
}

func TestUsageDocumentFullSections(t *testing.T) {
	repo, ports := usageDocFixture(t)
	ports.ToolDetails = &fakeToolDetails{items: []ToolSchemaDetail{{
		Server: "mcp.agentblog.dev", ToolName: "agentblog__create_post", Source: "protocol",
		OutputSchemaJSON: `{"type":"object","properties":{"job_id":{"type":"string"}}}`,
	}}}
	svc := NewPackService(repo, ports)
	svc.ports.MCP = &fakeMCP{refs: []MCPServerRef{{Name: "agentblog", Transport: "streamable_http", URL: "https://mcp.agentblog.dev", HasCreds: true}}}
	svc.ports.Agents = &fakeAgents{refs: []AgentRef{{ID: "ag-1", Name: "blogger"}}}
	svc.ports.Skills = &fakeSkills{refs: []SkillRef{{ID: "sk-1", Name: "writing"}}}
	svc.ports.UIEntry = &fakeUI{entry: &UIEntryInfo{Label: "AgentBlog 控制台", URLTemplate: "https://app.vendor.example/ui/{uiToken}/{workspaceId}",
		Slots: []UISlotInfo{{Slot: "workitem.detail.tab", Label: "文章联动", URLTemplate: "https://app.vendor.example/tab/{uiToken}/{workspaceId}", Height: 480}}}}
	svc.ports.Subs = &fakeSubs{refs: []SubRef{{
		SubscriptionID: "sub-1", URL: "https://vendor.example/hooks/orkome",
		EventTypes: []string{"task:completed", "task:failed"}, IsActive: true,
	}}}

	doc, err := svc.UsageDocument(context.Background(), "ws-1", "pack-a")
	require.NoError(t, err)

	for _, want := range []string{
		"# agentblog 使用指南（v1.2.0）",
		"## 概览",
		"- MCP 服务器数：1",
		"- 智能体数：1",
		"- 技能数：1",
		"## MCP 服务器",
		"**agentblog**：transport=streamable_http，url=https://mcp.agentblog.dev，已配置凭证",
		"## 工具契约",
		"### agentblog__create_post",
		"协议自描述",
		"```json",
		"agentblog__create_post",
		"## 事件订阅（orkome → 厂商接收端）",
		"https://vendor.example/hooks/orkome",
		"task:completed, task:failed",
		"X-Event-Id",
		"docs/webhook-events-guide-2026-10-14.md",
		"## 界面进驻",
		"AgentBlog 控制台",
		"{uiToken}",
		"{workspaceId}",
		"workitem.detail.tab",
		"## 使用约定",
		"job handle",
	} {
		require.True(t, strings.Contains(doc, want), "document should contain %q\n---\n%s", want, doc)
	}
	// schema 原文被格式化缩进（键名出现在 2 空格缩进行）。
	require.True(t, strings.Contains(doc, "\n  \"type\": \"object\""), "output schema should be pretty-printed\n%s", doc)
}

func TestUsageDocumentEmptySections(t *testing.T) {
	repo, ports := usageDocFixture(t)
	svc := NewPackService(repo, ports) // 五区块端口全 nil + 无明细 → 全空态

	doc, err := svc.UsageDocument(context.Background(), "ws-1", "pack-a")
	require.NoError(t, err)

	for _, want := range []string{
		"本包未注册 MCP 服务器",
		"本包未存档工具契约",
		"本包未配置事件订阅",
		"本包未进驻控制台界面",
		"## 使用约定",
	} {
		require.True(t, strings.Contains(doc, want), "document should contain %q\n---\n%s", want, doc)
	}
	// 空态不留悬空的 schema 代码块。
	require.False(t, strings.Contains(doc, "```json"), "empty document should not contain json fences\n%s", doc)
}

func TestUsageDocumentPackNotFound(t *testing.T) {
	repo, ports := usageDocFixture(t)
	svc := NewPackService(repo, ports)
	_, err := svc.UsageDocument(context.Background(), "ws-1", "pack-nope")
	require.Error(t, err)
}

// fakeToolDetails 是 ToolSchemaDetailReader 的内存实现。
type fakeToolDetails struct{ items []ToolSchemaDetail }

func (f *fakeToolDetails) ListDetailedByPack(context.Context, string, string) ([]ToolSchemaDetail, error) {
	return f.items, nil
}

// 压住 time 导入（fixture 不再单独引用时仍保留可读的安装时刻字段）。
