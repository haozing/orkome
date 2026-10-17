package main

// 助理接入 Skill 文档生成器（2026-10-15 裁决：接入即一份 skill 文档——
// 用户把文档链接+令牌发给任意 AI 客户端，AI 读完自动完成 MCP 注册并
// 掌握全部工具用法）。
//
// 设计要点：
//   - 工具目录零漂移：直接从已注册的 MCP server（s.ListTools()）枚举
//     name+description，服务器加了什么工具文档就有什么，没有第二份清单。
//   - 分组靠 groupOf 映射；新工具未映射时落「其他」区（可见降级，不静默丢）。
//   - 手工部分是"怎么用"（工作方式/注意事项），脚本部分是"有什么"（工具目录）。

import (
	"fmt"
	"html"
	"sort"
	"strings"

	"github.com/mark3labs/mcp-go/server"
)

// skillDocGroupOf 工具名 → 文档分组标题。新增工具未映射时落「其他」。
func skillDocGroupOf(name string) string {
	switch name {
	case "list_work_items", "get_work_item", "list_projects", "list_labels",
		"list_statuses", "list_skills", "read_skill":
		return "读取与发现"
	// list_agents 本体只读，但归「写」组（2026-10-17 裁决，与注册面
	// addWriteTools 同构）：它是 create/update_work_item 的 assignee_agent_id
	// 取值面（派单前置发现），紧贴两张写工具放。
	case "create_work_item", "update_work_item", "add_comment", "list_agents":
		return "工单写入"
	case "list_my_claims", "claim_work_item", "report_progress",
		"complete_work_item", "fail_work_item", "get_my_running_tasks":
		return "任务协议（接单干活）"
	case "list_mcp_servers", "list_webhook_subscriptions", "list_page_entries",
		"create_credential", "create_mcp_server", "create_webhook_subscription",
		"put_page_entry":
		return "集成配置（管理员）"
	}
	return "其他"
}

// skillDocGroupOrder 分组在文档中的展示顺序。
var skillDocGroupOrder = []string{"读取与发现", "工单写入", "任务协议（接单干活）", "集成配置（管理员）", "其他"}

// buildAssistantSkillDoc 生成接入 Skill 文档。base 是到达本服务的公网
// origin（如 https://orkome.example.com），ws 是目标工作区 id。
// ws 为空 = 全站统一版（2026-10-16 裁决）：端点用 {workspaceId} 占位，
// 并追加「用令牌发现工作区」指引（GET /api/workspaces 对 PAT 可用，
// 已活体验证）——该版本文档与工作区无关，是 README/官网可引的固定链接。
func buildAssistantSkillDoc(base, ws string, tools map[string]*server.ServerTool) string {
	universal := ws == ""
	wsSeg := ws
	if universal {
		wsSeg = "{workspaceId}"
	}
	mcpURL := fmt.Sprintf("%s/api/mcp/w/%s/me", base, wsSeg)
	var b strings.Builder

	b.WriteString("# Orkome 助理接入指南（skill）\n\n")
	b.WriteString("> 你正在读的是一份接入文档。按「二、接入配置」注册 MCP 服务后，")
	b.WriteString("你将获得下面列出的全部工具，以当前用户的身份操作 Orkome 工作区。\n\n")

	// 一、关于 Orkome
	b.WriteString("## 一、关于 Orkome\n\n")
	b.WriteString("Orkome 是人与 AI 智能体协同的工作管理平台：工单（work item）是协作单元，")
	b.WriteString("智能体（agent）是一等协作者。人类建单、分派、审批；智能体认领、执行、汇报、交付。")
	b.WriteString("你通过 MCP 接入后，可以：查/建/改工单、写评论、认领被指派的任务并执行交付、")
	b.WriteString("以及配置工作区集成（MCP 服务器 / Webhook 订阅 / 页面入口）。\n\n")

	// 二、接入配置
	b.WriteString("## 二、接入配置\n\n")
	b.WriteString("MCP 端点（Streamable HTTP）：\n\n```\n")
	b.WriteString(mcpURL)
	b.WriteString("\n```\n\n")
	b.WriteString("认证：请求头 `Authorization: Bearer <用户的个人访问令牌 PAT>`")
	b.WriteString("（PAT 由用户在 Orkome「助理接入」页创建）。若你尚未拿到令牌，请先向用户索取——引导用户打开 Orkome 网页的「助理接入」页创建一枚 PAT 并发给你。\n\n")
	if universal {
		b.WriteString("**本链接是全站统一文档**：端点里的 `{workspaceId}` 需要替换成用户的工作区 ID。发现方式二选一：\n\n")
		b.WriteString("- 用用户的令牌自行发现（推荐）：`curl -H \"Authorization: Bearer <PAT>\" " + base + "/api/workspaces`，")
		b.WriteString("返回是工作区数组、每项的 `id` 字段即 `{workspaceId}`——只有一个工作区就直接用它，多个就问用户要接哪一个；\n")
		b.WriteString("- 或让用户打开 Orkome 网页「助理接入」页，复制其中带完整端点的链接发给你。\n\n")
	}
	b.WriteString("### Claude Code\n\n```bash\n")
	b.WriteString(fmt.Sprintf("claude mcp add --transport http orkome %s --header \"Authorization: Bearer <PAT>\"\n", mcpURL))
	b.WriteString("```\n\n")
	b.WriteString("### Codex（~/.codex/config.toml）\n\n```toml\n")
	b.WriteString("[mcp_servers.orkome]\n")
	b.WriteString(fmt.Sprintf("url = \"%s\"\n", mcpURL))
	b.WriteString("bearer_token_env_var = \"ORKOME_TOKEN\"\n# 启动前：export ORKOME_TOKEN=\"<PAT>\"\n")
	b.WriteString("```\n\n")
	b.WriteString("### 通用 MCP 客户端\n\n```\n")
	b.WriteString(fmt.Sprintf("POST %s\n", mcpURL))
	b.WriteString("Authorization: Bearer <PAT>\nContent-Type: application/json\nAccept: application/json, text/event-stream\n")
	b.WriteString("```\n\n")

	// 三、工具目录（从注册面自动生成）
	b.WriteString("## 三、工具目录\n\n")
	if len(tools) == 0 {
		b.WriteString("（工具注册面为空——服务端装配异常，请联系管理员。）\n\n")
	} else {
		byGroup := map[string][]string{}
		for name := range tools {
			g := skillDocGroupOf(name)
			byGroup[g] = append(byGroup[g], name)
		}
		for _, g := range skillDocGroupOrder {
			names := byGroup[g]
			if len(names) == 0 {
				continue
			}
			sort.Strings(names)
			b.WriteString(fmt.Sprintf("### %s（%d）\n\n", g, len(names)))
			for _, name := range names {
				desc := ""
				if t := tools[name]; t != nil {
					desc = t.Tool.Description
				}
				info := toolParamSummaryMe(name)
				if info != "" {
					b.WriteString(fmt.Sprintf("- **%s** — %s（参数：%s）\n", name, desc, info))
				} else {
					b.WriteString(fmt.Sprintf("- **%s** — %s\n", name, desc))
				}
			}
			b.WriteString("\n")
		}
	}

	// 四、工作方式
	b.WriteString("## 四、工作方式\n\n")
	b.WriteString("- **自然语言即操作**：用户说「建个工单…」→ `create_work_item`；")
	b.WriteString("「把 X 标完成」→ 先 `list_work_items` 找到工单再 `update_work_item`；")
	b.WriteString("模糊时先用读工具（`list_*` / `get_work_item`）弄清上下文再动手。\n")
	b.WriteString("- **接单循环**（被指派的工单自动认领执行）：\n")
	b.WriteString("  1. `list_my_claims` 看有无派给你的工单 → `claim_work_item` 认领（单事务防重，重复认领会失败）；\n")
	b.WriteString("  2. 干活过程中用 `report_progress` 汇报进度（纯进度消息，触发不了智能体派发）；\n")
	b.WriteString("  3. 交付：`complete_work_item`（结果写入工单产出 outcome 并按规则推进状态）或 `fail_work_item`（说清原因）。\n")
	b.WriteString("- **指派智能体 = 自动派单**：把工单指派/改派给智能体（`create/update_work_item` 的 `assignee_agent_id`，取值面见 `list_agents`）即自动触发派单，**仅指派变化时发生**——无需再手动调 dispatch。\n")
	b.WriteString("- **恢复上下文**：会话中断后用 `get_my_running_tasks` 找回在跑的任务，`get_work_item` 取单件全貌。\n")
	b.WriteString("- **配置集成**：读工具（`list_mcp_servers` 等）人人可用；写工具（`create_*`）")
	b.WriteString("要求用户是工作区 owner/admin，否则会收到权限错误。\n\n")

	// 四A、出站接入（P1-7）：「集成配置」组的怎么用篇——注册第三方 MCP 的
	// 推荐序列（probe 优先 → 失败排查口径 → auth_effective 判读 → 凭证语义
	// → 改密钥三态）。
	b.WriteString("### 出站接入：注册第三方 MCP 服务器（推荐序列）\n\n")
	b.WriteString("1. `create_mcp_server` 带 `probe=true`：注册前先试连目标端点并枚举工具（不落库）；")
	b.WriteString("试连失败默认拒绝注册，确认要强注册时再传 `force=true`（改为注册成功、响应附警告）。\n")
	b.WriteString("2. 失败先查两件事：URL 是否写对、令牌是否有效（对方是否真的在验 Bearer）。\n")
	b.WriteString("3. 判断是否真的带上了鉴权：看响应的 `auth_effective`（headers 信封非空才算真鉴权），")
	b.WriteString("不要看 `has_credentials`——后者只说明「存了凭证形状」，可能只是关联了审计用途的 credential_id。\n")
	b.WriteString("4. `credential_id` 只做审计关联、不参与鉴权：真正生效的凭证是 `bearer_token`（写入密封信封的 headers.Authorization）。\n")
	b.WriteString("5. 改密钥走 REST `PUT " + base + "/api/mcp-servers/{id}`：bearer_token 留空=保留现值，显式传 null=清空。\n\n")

	// 四B、REST 速查表：MCP 工具之外的直调面（PATCH 别名 / dispatch 三态 /
	// setup-status 冷启动体检）。
	b.WriteString("### REST 速查（同一 PAT 可直调）\n\n")
	b.WriteString("MCP 工具之外，同一枚 PAT 可直接调 REST（脚本化、JSON 直传场景）；")
	b.WriteString("除 `Authorization: Bearer <PAT>` 外还需带 `X-Workspace-Id` 头：\n\n")
	b.WriteString("- **更新工单**：`PUT` 或 `PATCH " + base + "/api/work-items/{id}`（二者等价，body 传要改的字段）。\n")
	b.WriteString("- **显式派单**：`POST " + base + "/api/work-items/{id}/dispatch`，body `{\"agent_id\": \"…\"}`——响应三态：")
	b.WriteString("201 返回 `{\"task_id\"}`（已入队）；409 带 `active_task_id`（工单已有在跑任务）；422 带 `reason_code`（不可派单的原因码）。\n")
	b.WriteString("- **冷启动检查**：`GET " + base + "/api/setup-status`——返回 `credential_count`（已存凭证数）与 ")
	b.WriteString("`agents[].ready`（各智能体可否派单），一眼判断派单链路是否健康。\n\n")

	// 五、注意事项
	b.WriteString("## 五、注意事项\n\n")

	// 验证方法（P0-3）：可粘贴的两步 curl——成功标志的数字从注册面动态
	// 取（len(tools)），绝不写死：服务器加/减工具，文档里的期望值跟着变。
	b.WriteString("### 验证方法（写入配置后先跑这个）\n\n")
	b.WriteString(fmt.Sprintf("两步 curl 验证连通与工具面。成功标志：第 2 步 tools/list 返回本服务注册的全部工具（当前共 %d 个，与「三、工具目录」一致）。\n", len(tools)))
	b.WriteString("失败对照：HTTP 401 = 令牌错误或已过期；403 = 令牌有效但非该工作区成员。\n\n")
	b.WriteString("```bash\n")
	b.WriteString("# 1) initialize（Accept 头不可省——Streamable HTTP 协议要求双类型）\n")
	b.WriteString(fmt.Sprintf("curl -i -X POST %s \\\n", mcpURL))
	b.WriteString("  -H \"Authorization: Bearer <PAT>\" \\\n")
	b.WriteString("  -H \"Content-Type: application/json\" \\\n")
	b.WriteString("  -H \"Accept: application/json, text/event-stream\" \\\n")
	b.WriteString("  -d '{\"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"initialize\",\"params\":{\"protocolVersion\":\"2025-03-26\",\"capabilities\":{},\"clientInfo\":{\"name\":\"curl-check\",\"version\":\"0\"}}}'\n")
	b.WriteString("# 2) tools/list（携带第 1 步响应头 mcp-session-id）\n")
	b.WriteString(fmt.Sprintf("curl -X POST %s \\\n", mcpURL))
	b.WriteString("  -H \"Authorization: Bearer <PAT>\" \\\n")
	b.WriteString("  -H \"Content-Type: application/json\" \\\n")
	b.WriteString("  -H \"Accept: application/json, text/event-stream\" \\\n")
	b.WriteString("  -H \"mcp-session-id: <从 initialize 响应头复制>\" \\\n")
	b.WriteString("  -d '{\"jsonrpc\":\"2.0\",\"id\":2,\"method\":\"tools/list\"}'\n")
	b.WriteString("```\n\n")

	b.WriteString("- **重启预期 + HTTP 降级**：多数客户端只在会话启动时连接 MCP 服务器，写入配置后需重启会话才能看到工具；")
	b.WriteString("等待重启期间可按「二、接入配置 → 通用 MCP 客户端」段用 HTTP 直接调用（先 initialize 取响应头 mcp-session-id，后续请求携带该头）。\n")
	b.WriteString("- **配置 schema 严格警示**：部分客户端的配置 schema 严格校验，未知字段可能导致整个服务器条目被静默忽略——写入配置后务必按上文方法验证。\n")
	b.WriteString("- **非 ASCII 文本坑（Windows）**：在 Windows shell 里内联非 ASCII 文本会被本地代码页（如 GBK）转码损坏，含中文的请求体请先写入 UTF-8 文件再用 `--data-binary @file` 提交。\n")
	b.WriteString("- PAT 等同账号完整权限，只存在于本次会话配置中，勿写入代码仓库或共享。\n")
	b.WriteString("- MCP 端点已绑定工作区（URL 内），全部操作作用于该工作区。\n")
	b.WriteString("- `create_webhook_subscription` / 轮换密钥返回的签名密钥仅明文回显一次，请提示用户立即保存。\n")

	return b.String()
}

// toolParamSummaryMe 给几个高频工具补一行参数速记（纯展示辅助；返回空串
// 表示不加）。刻意保持短——完整参数 schema 由 MCP 协议本身的 tools/list 提供。
func toolParamSummaryMe(name string) string {
	switch name {
	case "create_work_item":
		return "title 必填；description/project/assignee（'me'）/assignee_agent_id 可选"
	case "update_work_item":
		return "work_item_id 必填；传要改的字段；assignee_agent_id 改派智能体（仅指派变化时自动派单）"
	case "get_work_item":
		return "work_item_id"
	case "list_work_items":
		return "assignee='me' 查自己的；status/project 过滤"
	case "add_comment":
		return "work_item_id + content"
	case "read_skill":
		return "name（先用 list_skills 拿）"
	case "claim_work_item":
		return "work_item_id 必填"
	case "report_progress":
		return "task_id + content 必填"
	case "complete_work_item":
		return "task_id + result 必填"
	case "fail_work_item":
		return "task_id + reason 必填；detail 可选"
	case "list_agents":
		return "无参；返回 id/name/description/archived/ready——ready=false 缺模型配置，派单会静默失败"
	case "create_mcp_server":
		return "name/url 必填；bearer_token 传鉴权；probe=true 先试连（失败默认拒，force 可强行）；transport=streamable_http|sse"
	case "put_page_entry":
		return "label/url_template 必填；work_item_tab=true 加页签"
	case "create_webhook_subscription":
		return "url + events[] 必填"
	}
	return ""
}

// skillDocHTMLPage 把 markdown 文档包成浏览器可读的预览页（2026-10-15 修复：
// text/markdown 浏览器不原生渲染，直接发原始 markdown 会白屏）。转义后放
// <pre>；仅内联样式无脚本——CSP 由 handler 针对本页放宽 style-src。
// AI 客户端读的仍是原始 markdown（Accept 协商，见 handler）。
func skillDocHTMLPage(doc string) string {
	var b strings.Builder
	b.WriteString(`<!doctype html>
<html lang="zh">
<head>
<meta charset="utf-8">
<title>Orkome 接入文档（skill）</title>
<meta name="color-scheme" content="light dark">
<meta name="viewport" content="width=device-width, initial-scale=1">
<style>
  :root { color-scheme: light dark; }
  body { margin: 0; background: #ffffff; color: #18181b;
         font-family: ui-sans-serif, system-ui, -apple-system, "PingFang SC", "Microsoft YaHei", sans-serif; }
  main { max-width: 880px; margin: 0 auto; padding: 28px 20px 72px; }
  .banner { border: 1px solid #e4e4e7; border-radius: 8px; padding: 10px 14px;
            font-size: 13px; line-height: 1.6; color: #52525b; margin-bottom: 22px; }
  pre { font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, monospace;
        font-size: 13px; line-height: 1.75; white-space: pre-wrap; word-break: break-word; margin: 0; }
  @media (prefers-color-scheme: dark) {
    body { background: #09090b; color: #e4e4e7; }
    .banner { border-color: #27272a; color: #a1a1aa; }
  }
</style>
</head>
<body>
<main>
  <div class="banner">本页是需要交给 AI 阅读的接入文档——把本页地址与你创建的令牌（ork_…）发给你的 AI，说「读这份文档并接入 Orkome」。AI 读取该地址时会拿到原始 Markdown。</div>
  <pre>`)
	b.WriteString(html.EscapeString(doc))
	b.WriteString(`</pre>
</main>
</body>
</html>
`)
	return b.String()
}
