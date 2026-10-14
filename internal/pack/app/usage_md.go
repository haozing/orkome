package app

// UsageDocument：能力包使用指南的 Markdown 渲染（能力包平台化）。
// 受众是 LLM 智能体——把安装事实（服务器/工具契约/事件订阅/UI 进驻）
// 折叠成一份可贴进 agent 上下文的文档，语言直给、少修辞。
//
// 渲染只依赖 Get 的五区块聚合 + 工具 schema 明细端口；schema 原文
//（OutputSchemaJSON）不在 Get 的 ToolSchemaRef 里，走新端口单独取。

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// ToolSchemaDetailReader 读 pack_tool_schema 的明细行（带 output_schema 原文）。
// 与详情页的 ToolSchemaReader（无 schema 正文）分端口：详情页要轻、
// usage-document 要全。实现由组合根接线（orchestration pgrepo 适配）。
type ToolSchemaDetailReader interface {
	ListDetailedByPack(ctx context.Context, ws, packID string) ([]ToolSchemaDetail, error)
}

// ToolSchemaDetail 是一个工具的完整契约行。
type ToolSchemaDetail struct {
	Server           string `json:"server"`
	ToolName         string `json:"tool_name"`
	Source           string `json:"source"`
	OutputSchemaJSON string `json:"output_schema_json"`
}

// UsageDocument 渲染 pack 的中文 Markdown 使用指南（受众=LLM 智能体）。
// 空段一律给"本包未配置 X"句，不留空章节——消费方（agent）不用猜缺省语义。
func (s *PackService) UsageDocument(ctx context.Context, ws, packID string) (string, error) {
	d, err := s.Get(ctx, ws, packID)
	if err != nil {
		return "", err
	}
	var details []ToolSchemaDetail
	if s.ports.ToolDetails != nil {
		details, err = s.ports.ToolDetails.ListDetailedByPack(ctx, ws, packID)
		if err != nil {
			return "", err
		}
	}

	var b strings.Builder
	fmt.Fprintf(&b, "# %s 使用指南（v%s）\n\n", d.Name, d.Version)
	b.WriteString("> 本文档由 orkome 平台自动生成，描述能力包 " + d.Name + " 在本工作区安装的事实。\n\n")

	// 概览：装机时刻的关键数字。
	fmt.Fprintf(&b, "## 概览\n\n- 安装时间：%s\n- 版本：%s\n", d.InstalledAt.Format("2006-01-02 15:04:05 MST"), d.Version)
	fmt.Fprintf(&b, "- MCP 服务器数：%d\n- 智能体数：%d\n- 技能数：%d\n\n", len(d.Servers), len(d.Agents), len(d.Skills))

	// MCP 服务器。
	b.WriteString("## MCP 服务器\n\n")
	if len(d.Servers) == 0 {
		b.WriteString("本包未注册 MCP 服务器。\n\n")
	} else {
		for _, srv := range d.Servers {
			creds := "无凭证"
			if srv.HasCreds {
				creds = "已配置凭证"
			}
			fmt.Fprintf(&b, "- **%s**：transport=%s，url=%s，%s\n", srv.Name, srv.Transport, srv.URL, creds)
		}
		b.WriteString("\n")
	}

	// 工具契约。
	b.WriteString("## 工具契约\n\n")
	if len(details) == 0 {
		b.WriteString("本包未存档工具契约。\n\n")
	} else {
		for _, t := range details {
			fmt.Fprintf(&b, "### %s\n\n", t.ToolName)
			if t.Source == "protocol" {
				b.WriteString("来源：协议自描述（安装期从 MCP 服务器握手取得）。\n\n")
			} else {
				fmt.Fprintf(&b, "来源：%s。\n\n", t.Source)
			}
			b.WriteString("输出 schema：\n\n```json\n")
			b.WriteString(formatJSONOrRaw(t.OutputSchemaJSON))
			b.WriteString("\n```\n\n")
		}
	}

	// 事件订阅（orkome → 厂商接收端）。
	b.WriteString("## 事件订阅（orkome → 厂商接收端）\n\n")
	if len(d.Subscriptions) == 0 {
		b.WriteString("本包未配置事件订阅。\n\n")
	} else {
		for _, sub := range d.Subscriptions {
			fmt.Fprintf(&b, "- 接收端：%s，事件类型：%s\n", sub.URL, strings.Join(sub.EventTypes, ", "))
		}
		b.WriteString("\n投递约定：每条投递带 `X-Event-Id` 头，接收端须按其幂等去重（重试会产生重复投递）；")
		b.WriteString("签名验签方式详见仓库 docs/webhook-events-guide-2026-10-14.md。\n\n")
	}

	// 界面进驻。
	b.WriteString("## 界面进驻\n\n")
	if d.UIEntry == nil || d.UIEntry.Label == "" {
		b.WriteString("本包未进驻控制台界面。\n\n")
	} else {
		fmt.Fprintf(&b, "控制台入口：**%s**，地址模板 `%s`（`{uiToken}` 渲染为一次性 UI 令牌，`{workspaceId}` 渲染为当前工作区 ID）。\n\n", d.UIEntry.Label, d.UIEntry.URLTemplate)
		if len(d.UIEntry.Slots) == 0 {
			b.WriteString("本包未配置界面 slot。\n\n")
		} else {
			for _, sl := range d.UIEntry.Slots {
				fmt.Fprintf(&b, "- slot `%s`：%s（高度 %dpx）\n", sl.Slot, sl.Label, sl.Height)
			}
			b.WriteString("\n")
		}
	}

	// 使用约定。
	b.WriteString("## 使用约定\n\n")
	b.WriteString("- 工具全名含 pack 前缀（`{pack}__{tool}` 形态），调用时用全名。\n")
	b.WriteString("- 异步 job 类工具返回 job handle，用配套的 status 工具轮询，不要盲目重试。\n")
	b.WriteString("- 本文档描述安装时刻快照；资产装后归工作区所有，可自由修改，修改不回写能力包。\n")
	return b.String(), nil
}

// formatJSONOrRaw 把 schema 原文格式化缩进；非法 JSON 原样放行——
// schema 正文是渲染事实而非校验关口，解析失败不该让整份文档失败。
func formatJSONOrRaw(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "{}"
	}
	var v any
	if err := json.Unmarshal([]byte(trimmed), &v); err != nil {
		return trimmed
	}
	pretty, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return trimmed
	}
	return string(pretty)
}
