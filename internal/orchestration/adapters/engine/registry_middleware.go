package engine

// C1：统一注册表的工具调用 middleware 管线（eino 原生协议）。
//
// 输出校验 middleware 实现 adk.ChatModelAgentMiddleware 的
// WrapInvokableToolCall（vendor adk/handler.go:193），与既有 steerMiddleware
// （engine.go）/ toolsearch（registry.go）同构共存——adk 构造 ChatModelAgent
// 时自动把 Handlers 转成 ToolsNode 的 ToolCallMiddlewares（vendor
// adk/chatmodel.go handlersToToolMiddlewares），不自研 chi 风格切片。
//
// 两个职责：
//
//  1. 输出校验（A1 红利对内置工具闭环）：注册表中 origin=builtin 的工具
//     执行成功后，输出按注册表侧 OutputSchema 过同一 OutputValidator。
//     MCP 桥接工具不在此校验（其输出是 eino-ext 的 CallToolResult 包装
//     串，解包语义不同），仍由 A1 的 wrapWithValidator 装饰器在同一
//     validator 上承担（tools_mcp.go）——两个执行位、同一个校验器。
//     内置工具的输出是平台自己的文本（非包装串），文档选取规则见
//     validateBuiltinOutput。
//
//  2. ConcurrencySafe fail-closed（P1 实装）：eino ToolsNode 对同一消息
//     的多个 tool_calls 默认并行执行（vendor compose/tool_node.go
//     executeSequentially 零值 false + parallelRun 分流）。本 middleware
//     对每个工具调用按注册表判定：未显式声明 ConcurrencySafe=true 的
//     工具一律过本 run 的串行闸（互斥锁）——不安全工具之间绝不并行；
//     显式声明 true 的只读/幂等工具直接放行（可彼此并行）。
//     fail-closed：未登记的工具（成员工具、未知名）一律按不安全处理。
//
// 行为不变优先：工具失败（error / eino Interrupt）原样透传，不走校验
// 也不改变闸语义；串行闸只影响同消息多 tool_calls 的并行度（护栏语义，
// 见契约文档 §3 ★ConcurrencySafe）。

import (
	"context"
	"encoding/json"
	"log/slog"
	"sync"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/components/tool"
)

// toolContractMiddleware 是 C1 的注册表管线 middleware（每任务一个实例）。
type toolContractMiddleware struct {
	adk.BaseChatModelAgentMiddleware

	reg       *toolRegistry
	validator OutputValidator
	audit     *slog.Logger

	// serialMu 是 fail-closed 串行闸：本轮所有未声明并发安全的工具调用
	// 经它逐个执行。eino react 循环一轮只过一次 ToolsNode，锁的生命周期
	// 天然收敛在一次 run 内（每任务 new 一个 middleware 实例）。
	serialMu sync.Mutex
}

// compile-time：确认实现了 adk 原生 middleware 接口。
var _ adk.ChatModelAgentMiddleware = (*toolContractMiddleware)(nil)

// newToolContractMiddleware 构造注册表管线 middleware。validator 为 nil
// 时用 A1 默认两级实现；audit 为 nil 时违规不落审计（调用方保证接线）。
func newToolContractMiddleware(reg *toolRegistry, validator OutputValidator, audit *slog.Logger) *toolContractMiddleware {
	if validator == nil {
		validator = NewDefaultValidator()
	}
	return &toolContractMiddleware{reg: reg, validator: validator, audit: audit}
}

// WrapInvokableToolCall 是管线执行位：对每个 invokable 工具调用按注册表
// 判定并发安全性（fail-closed）并包装输出校验（仅内置工具）。
// 所有内置工具与 MCP 桥接工具均为 InvokableTool，本钩子覆盖全量工具面。
func (m *toolContractMiddleware) WrapInvokableToolCall(_ context.Context,
	endpoint adk.InvokableToolCallEndpoint, tCtx *adk.ToolContext) (adk.InvokableToolCallEndpoint, error) {

	name := tCtx.Name
	safe := m.reg.concurrencySafeOf(name) // 未登记 → false（fail-closed）
	var schemaDoc json.RawMessage
	if m.reg.originOf(name) == "builtin" {
		schemaDoc = m.reg.outputSchemaOf(name)
	}
	validator, audit := m.validator, m.audit

	return func(ctx context.Context, argumentsInJSON string, opts ...tool.Option) (string, error) {
		if safe {
			out, err := endpoint(ctx, argumentsInJSON, opts...)
			if err != nil {
				return out, err // 工具失败（含 eino Interrupt）原样透传
			}
			return validateBuiltinOutput(name, schemaDoc, out, validator, audit)
		}
		m.serialMu.Lock()
		defer m.serialMu.Unlock()
		out, err := endpoint(ctx, argumentsInJSON, opts...)
		if err != nil {
			return out, err
		}
		return validateBuiltinOutput(name, schemaDoc, out, validator, audit)
	}, nil
}

// validateBuiltinOutput 校验一次内置工具的成功输出：
//   - 显式豁免（注册表登记但 schema 为 nil，如 request_approval /
//     search_workspace 的人类可读文本）→ 原样放行；
//   - 有 schema → JSON Schema 校验。内置工具输出是平台自己的文本
//     （非 eino-ext 包装串）：整体按 JSON 解析，解析失败按字符串文档
//     处理（{"type":"string"} 契约即为此形状）——与 MCP 路径的
//     unwrapCallToolResultDocument（解包包装串）是两套文档选取规则，
//     喂同一个校验器；
//   - 违规 → A1 标准违规串替换结果（golden 一致）+ 审计日志（原文全文）。
func validateBuiltinOutput(name string, schemaDoc json.RawMessage,
	out string, validator OutputValidator, audit *slog.Logger) (string, error) {

	if len(schemaDoc) == 0 {
		return out, nil // 豁免或未登记（未知名走 unknown handler 不进这里）
	}
	doc := any(out)
	var parsed any
	if err := json.Unmarshal([]byte(out), &parsed); err == nil {
		doc = parsed
	}
	if err := validator.Validate(Input{ToolName: name, Schema: schemaDoc, Document: doc}); err != nil {
		var ve *ViolationError
		if !asViolationError(err, &ve) {
			ve = &ViolationError{Reason: err.Error()} // 校验器自身故障 → fail-closed 按违规
		}
		if ve.ToolName == "" {
			ve.ToolName = name
		}
		if audit != nil {
			audit.LogAttrs(context.Background(), slog.LevelWarn, "builtin tool output contract violation",
				slog.String("tool", name),
				slog.String("reason", ve.Reason),
				slog.String("raw_output", out),
			)
		}
		return ve.ModelFacingText(out), nil
	}
	return out, nil
}
