package engine

// A1：MCP 工具输出契约校验的执行管线（装饰器）。
// 接口冻结文档：docs/interfaces-output-validator-2026-10-11.md §2。
//
// validatingTool 装饰 eino tool.BaseTool：原工具执行成功后解包 eino-ext
// 序列化的 CallToolResult 包装串，取出待验文档（structuredContent 优先，
// 否则 content[0].text 解析 JSON），交 OutputValidator 校验；
// 通过 → 原结果原样返回；违规 → 标准违规错误串 + 审计日志（原文全文，
// 模型永不可见）。
//
// 装饰在两条工具路径都生效：静态合并（≤24 工具）与 toolsearch 动态
// 注入（装饰后的 BaseTool 原样交给 ToolsNode）。

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
)

// validatingTool 包装一个 eino 工具，执行后按输出契约校验结果。
type validatingTool struct {
	tool.BaseTool
	name      string
	schema    json.RawMessage // 可为 nil（无 schema → 结构化探测回落）
	validator OutputValidator
	audit     *slog.Logger
}

// wrapWithValidator 用输出校验装饰器包装工具。validator 为 nil 时不包装
// （A1 之前的行为），保底用 DefaultValidator。
func wrapWithValidator(t tool.BaseTool, name string, schema json.RawMessage, validator OutputValidator, audit *slog.Logger) tool.BaseTool {
	if invokable, ok := t.(tool.InvokableTool); ok {
		return &validatingInvokableTool{
			validatingTool: validatingTool{
				BaseTool:  t,
				name:      name,
				schema:    schema,
				validator: validator,
				audit:     audit,
			},
			inner: invokable,
		}
	}
	// eino-ext MCP 工具均为 InvokableTool；流式工具暂无校验路径，原样返回。
	return t
}

type validatingInvokableTool struct {
	validatingTool
	inner tool.InvokableTool
}

func (v *validatingInvokableTool) Info(ctx context.Context) (*schema.ToolInfo, error) {
	return v.inner.Info(ctx)
}

func (v *validatingInvokableTool) InvokableRun(ctx context.Context, argumentsInJSON string, opts ...tool.Option) (string, error) {
	out, err := v.inner.InvokableRun(ctx, argumentsInJSON, opts...)
	if err != nil {
		// 工具自身失败（含 eino-ext 对 isError:true 的报错路径）：
		// 失败本身已是模型可见的明确结果，不走输出契约校验。
		return out, err
	}
	doc, rawText, verr := unwrapCallToolResultDocument(out)
	if verr != nil {
		return v.violation(verr, rawText, out)
	}
	if err := v.validator.Validate(Input{ToolName: v.name, Schema: v.schema, Document: doc}); err != nil {
		var ve *ViolationError
		if !asViolationError(err, &ve) {
			ve = &ViolationError{Reason: err.Error()}
		}
		return v.violation(ve, rawText, out)
	}
	return out, nil
}

// violation 落违规：包装串全文进审计日志（结构化），模型只看到标准错误串
// （excerpt 取业务原文——structuredContent 的 JSON 或 content 首段 text，
// 非 eino-ext 包装串）。
func (v *validatingInvokableTool) violation(ve *ViolationError, rawText, wrappedOutput string) (string, error) {
	if ve.ToolName == "" {
		ve.ToolName = v.name
	}
	if v.audit != nil {
		v.audit.LogAttrs(context.Background(), slog.LevelWarn, "mcp tool output contract violation",
			slog.String("tool", v.name),
			slog.String("reason", ve.Reason),
			slog.String("raw_output", wrappedOutput),
		)
	}
	return ve.ModelFacingText(rawText), nil
}

// unwrapCallToolResultDocument 从 eino-ext 序列化的 CallToolResult 包装串
// 中取出待验文档与业务原文：
//   - doc：structuredContent 存在 → 直接使用（协议层已保证是结构化值）；
//     否则取 content[0] 的 text 字段做 json.Unmarshal；
//   - rawText：业务原文（structuredContent 的 JSON 序列化或 content[0]
//     的 text），用于违规报告的 excerpt——不含 eino-ext 包装层。
//
// 无 schema 工具同样经此解包：文本不是合法 JSON → 违规；是 JSON 标量/
// null（非 object/array）→ 违规（probe 侧判定）。
func unwrapCallToolResultDocument(wrapped string) (doc any, rawText string, verr *ViolationError) {
	var w struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		StructuredContent any `json:"structuredContent"`
	}
	if err := json.Unmarshal([]byte(wrapped), &w); err != nil {
		return nil, wrapped, &ViolationError{Reason: "structured content missing"}
	}
	if w.StructuredContent != nil {
		if b, err := json.Marshal(w.StructuredContent); err == nil {
			return w.StructuredContent, string(b), nil
		}
		return nil, wrapped, &ViolationError{Reason: "structured content missing"}
	}
	if len(w.Content) == 0 || w.Content[0].Type != "text" {
		return nil, wrapped, &ViolationError{Reason: "structured content missing"}
	}
	text := w.Content[0].Text
	var parsed any
	if err := json.Unmarshal([]byte(text), &parsed); err != nil {
		return nil, text, &ViolationError{Reason: "malformed json"}
	}
	return parsed, text, nil
}

// asViolationError 尝试把 err 解释为 ViolationError（含错误包装链）。
func asViolationError(err error, target **ViolationError) bool {
	var ve *ViolationError
	if errors.As(err, &ve) {
		*target = ve
		return true
	}
	return false
}

// ErrorAsViolation 是 asViolationError 的可测试门面（测试用）。
func ErrorAsViolation(err error, target **ViolationError) bool {
	return asViolationError(err, target)
}
