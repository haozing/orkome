package main

import (
	"fmt"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// 接入文档生成器钉子：工具目录必须从注册面自动生成（零漂移），分组映射
// 兜底「其他」，端点 URL 用传入 base+ws 拼。

func TestBuildAssistantSkillDoc(t *testing.T) {
	tools := map[string]*server.ServerTool{
		"get_work_item":       {Tool: mcp.NewTool("get_work_item", mcp.WithDescription("fetch one work item"))},
		"create_work_item":    {Tool: mcp.NewTool("create_work_item", mcp.WithDescription("create"))},
		"claim_work_item":     {Tool: mcp.NewTool("claim_work_item", mcp.WithDescription("claim"))},
		"report_progress":     {Tool: mcp.NewTool("report_progress", mcp.WithDescription("progress"))},
		"complete_work_item":  {Tool: mcp.NewTool("complete_work_item", mcp.WithDescription("complete"))},
		"fail_work_item":      {Tool: mcp.NewTool("fail_work_item", mcp.WithDescription("fail"))},
		"create_mcp_server":   {Tool: mcp.NewTool("create_mcp_server", mcp.WithDescription("register server"))},
		"update_work_item":    {Tool: mcp.NewTool("update_work_item", mcp.WithDescription("update"))},
		"list_agents":         {Tool: mcp.NewTool("list_agents", mcp.WithDescription("list agents"))},
		"future_unknown_tool": {Tool: mcp.NewTool("future_unknown_tool", mcp.WithDescription("not yet grouped"))},
	}
	doc := buildAssistantSkillDoc("https://orkome.example.com", "ws-1", tools)

	for _, want := range []string{
		"https://orkome.example.com/api/mcp/w/ws-1/me",
		"## 二、接入配置",
		"claude mcp add --transport http orkome https://orkome.example.com/api/mcp/w/ws-1/me",
		"### 读取与发现（1）",
		// list_agents 归「工单写入」组（2026-10-17 裁决：assignee_agent_id
		// 的取值面，紧贴两张写工具）。
		"### 工单写入（3）",
		"### 任务协议（接单干活）（4）",
		"### 集成配置（管理员）（1）",
		"### 其他（1）",
		"future_unknown_tool",
		// 参数速记与注册面 schema 一致（回归钉：claim 按 work_item_id，
		// 认领后的三个动作按 task_id——曾整组错写成 work_item_id）。
		"**claim_work_item** — claim（参数：work_item_id 必填）",
		"**report_progress** — progress（参数：task_id + content 必填）",
		"**complete_work_item** — complete（参数：task_id + result 必填）",
		"**fail_work_item** — fail（参数：task_id + reason 必填；detail 可选）",
		// 新工具/新参数入速记（2026-10-17）。
		"**list_agents** — list agents（参数：无参；返回 id/name/description/archived/ready",
		"assignee_agent_id 改派智能体（仅指派变化时自动派单）",
		"probe=true 先试连（失败默认拒，force 可强行）；transport=streamable_http|sse",
		// REST 速查表 + 出站接入（P1-7）。
		"### REST 速查（同一 PAT 可直调）",
		"PUT` 或 `PATCH https://orkome.example.com/api/work-items/{id}`（二者等价",
		"POST https://orkome.example.com/api/work-items/{id}/dispatch",
		"409 带 `active_task_id`",
		"422 带 `reason_code`",
		"GET https://orkome.example.com/api/setup-status",
		"`agents[].ready`",
		"X-Workspace-Id",
		"仅指派变化时发生",
		"### 出站接入：注册第三方 MCP 服务器（推荐序列）",
		"`auth_effective`（headers 信封非空才算真鉴权）",
		"`credential_id` 只做审计关联、不参与鉴权",
		"PUT https://orkome.example.com/api/mcp-servers/{id}",
		"留空=保留现值，显式传 null=清空",
		// 注意事项新三段 + 验证 curl（P0-3/P0-6）。
		"### 验证方法（写入配置后先跑这个）",
		fmt.Sprintf("当前共 %d 个", len(tools)),
		"401 = 令牌错误或已过期；403 = 令牌有效但非该工作区成员",
		"curl -i -X POST https://orkome.example.com/api/mcp/w/ws-1/me",
		"-H \"Accept: application/json, text/event-stream\"",
		"-H \"mcp-session-id: <从 initialize 响应头复制>\"",
		"重启会话才能看到工具",
		"未知字段可能导致整个服务器条目被静默忽略",
		"--data-binary @file",
	} {
		if !strings.Contains(doc, want) {
			t.Errorf("doc missing %q", want)
		}
	}
	// 工具数动态（P0-3）：期望值必须随注册面长度走——注册面翻倍后句子跟着变，
	// 绝不出现写死的旧数字（25/23）。
	more := map[string]*server.ServerTool{}
	for k, v := range tools {
		more[k] = v
	}
	more["extra_a"] = tools["get_work_item"]
	more["extra_b"] = tools["get_work_item"]
	if doc2 := buildAssistantSkillDoc("https://orkome.example.com", "ws-1", more); !strings.Contains(doc2, fmt.Sprintf("当前共 %d 个", len(more))) {
		t.Errorf("expected dynamic tool count %d in doc", len(more))
	}
	// 26 工具的真实注册面走一遍：每个工具名都必须在文档里出现一次。
}

func TestBuildAssistantSkillDocEmptyTools(t *testing.T) {
	doc := buildAssistantSkillDoc("http://localhost:8080", "ws-2", nil)
	if !strings.Contains(doc, "工具注册面为空") {
		t.Error("empty tool surface must degrade visibly, not silently")
	}
}

func TestSkillDocHTMLPageEscapes(t *testing.T) {
	page := skillDocHTMLPage("<script>alert(1)</script> & **bold**")
	if strings.Contains(page, "<script>alert(1)</script>") {
		t.Error("raw <script> must be escaped in the preview page")
	}
	if !strings.Contains(page, "&lt;script&gt;") || !strings.Contains(page, "&amp;") {
		t.Error("escaped entities expected")
	}
	if !strings.Contains(page, "text/html") && !strings.Contains(page, "<!doctype html>") {
		t.Error("must produce an HTML document")
	}
}

func TestBuildAssistantSkillDocUniversal(t *testing.T) {
	tools := map[string]*server.ServerTool{
		"list_work_items": {Tool: mcp.NewTool("list_work_items", mcp.WithDescription("list"))},
	}
	doc := buildAssistantSkillDoc("https://orkome.example.com", "", tools)
	for _, want := range []string{
		"https://orkome.example.com/api/mcp/w/{workspaceId}/me",
		"本链接是全站统一文档",
		"/api/workspaces",
	} {
		if !strings.Contains(doc, want) {
			t.Errorf("universal doc missing %q", want)
		}
	}
	// 工作区版不得出现发现指引（避免噪音）。
	wsDoc := buildAssistantSkillDoc("https://orkome.example.com", "ws-9", tools)
	if strings.Contains(wsDoc, "本链接是全站统一文档") {
		t.Error("workspace-specific doc must not carry the universal discovery paragraph")
	}
}
