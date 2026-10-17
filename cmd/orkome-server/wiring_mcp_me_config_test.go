package main

import (
	"slices"
	"testing"

	"github.com/mark3labs/mcp-go/server"
)

// 配置面纯函数的钉子（addConfigTools 的辅助层；MCP 工具本体靠真机 E2E 验收）。

func TestSlugifyMe(t *testing.T) {
	cases := []struct{ in, want string }{
		{"我的控制台", "custom-entry-"},       // 全非 ASCII → 时间戳回退前缀
		{"Vendor Console", "custom-vendor-console"},
		{"Retail  POS!!", "custom-retail-pos"},
		{"--Edge--", "custom-edge"},
	}
	for _, c := range cases {
		got := slugifyMe(c.in)
		if c.want == "custom-entry-" {
			if len(got) < len("custom-entry-") || got[:len("custom-entry-")] != "custom-entry-" {
				t.Errorf("slugifyMe(%q) = %q, want prefix custom-entry-", c.in, got)
			}
			continue
		}
		if got != c.want {
			t.Errorf("slugifyMe(%q) = %q, want %q", c.in, got, c.want)
		}
	}
	// 长名限长（60 + custom- 前缀）
	long := slugifyMe("abcdefghijklmnopqrstuvwxyz-abcdefghijklmnopqrstuvwxyz-abcdefghijklmnopqrstuvwxyz")
	if len(long) > 67 {
		t.Errorf("slugifyMe long = %d chars, want <= 67", len(long))
	}
}

func TestValidAbsHTTPMe(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"https://vendor.example.com/console?ws={workspaceId}&t={uiToken}", true},
		{"http://127.0.0.1:8091/hooks", true},
		{"javascript:alert(1)", false},
		{"data:text/html,x", false},
		{"//proto-relative/x", false},
		{"no-scheme.example.com", false},
		{"ftp://example.com", false},
		{"", false},
	}
	for _, c := range cases {
		if got := validAbsHTTPMe(c.in); got != c.want {
			t.Errorf("validAbsHTTPMe(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

// agentRuntimeReadyMe 钉子：model/credential_id/base_url 三键齐且均为
// 非空字符串才算 ready——缺键、非字符串、空串任一情形都判 false。
func TestAgentRuntimeReadyMe(t *testing.T) {
	cases := []struct {
		name string
		rc   map[string]any
		want bool
	}{
		{"nil config", nil, false},
		{"empty config", map[string]any{}, false},
		{"all three set", map[string]any{
			"model": "deepseek-chat", "credential_id": "cred-1", "base_url": "https://api.example.com",
		}, true},
		{"missing model", map[string]any{"credential_id": "c", "base_url": "https://x"}, false},
		{"missing credential_id", map[string]any{"model": "m", "base_url": "https://x"}, false},
		{"missing base_url", map[string]any{"model": "m", "credential_id": "c"}, false},
		{"empty model", map[string]any{"model": "", "credential_id": "c", "base_url": "https://x"}, false},
		{"non-string model", map[string]any{"model": 42, "credential_id": "c", "base_url": "https://x"}, false},
		{"extra keys ok", map[string]any{
			"model": "m", "credential_id": "c", "base_url": "https://x", "temperature": 0.2,
		}, true},
	}
	for _, c := range cases {
		if got := agentRuntimeReadyMe(c.rc); got != c.want {
			t.Errorf("%s: agentRuntimeReadyMe(%v) = %v, want %v", c.name, c.rc, got, c.want)
		}
	}
}

// TestAssistantMeToolSurface 助理面工具清单回归钉：四组注册完毕后
// server.ListTools() 必须恰好是这 24 个名字（2026-10-17 +list_agents）。
// 零值 deps 即可——AddTool 只登记闭包不执行，DB 依赖在调用期才触达。
func TestAssistantMeToolSurface(t *testing.T) {
	s := server.NewMCPServer("orkome-assistant-test", "0.0.0")
	d := &assistantMEDeps{}
	d.addReadTools(s)
	d.addWriteTools(s)
	d.addTaskTools(s)
	d.addConfigTools(s)

	want := []string{
		// 读 7
		"list_work_items", "get_work_item", "list_projects", "list_labels",
		"list_statuses", "list_skills", "read_skill",
		// 写 4（list_agents 归写组：assignee_agent_id 的取值发现面）
		"list_agents", "create_work_item", "update_work_item", "add_comment",
		// 任务协议 5 + 恢复 1
		"list_my_claims", "claim_work_item", "report_progress",
		"complete_work_item", "fail_work_item", "get_my_running_tasks",
		// 配置 7（读 3 + 写 4）
		"list_mcp_servers", "list_webhook_subscriptions", "list_page_entries",
		"create_credential", "create_mcp_server", "create_webhook_subscription",
		"put_page_entry",
	}
	got := make([]string, 0, len(want))
	for name := range s.ListTools() {
		got = append(got, name)
	}
	slices.Sort(want)
	slices.Sort(got)
	if len(got) != len(want) || slices.Compare(got, want) != 0 {
		t.Errorf("assistant me tool surface = %v\nwant %v (count %d)", got, want, len(want))
	}
	if len(got) != 24 {
		t.Errorf("assistant me tool count = %d, want 24", len(got))
	}
}
