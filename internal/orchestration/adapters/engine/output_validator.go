package engine

// A1：MCP 工具输出契约强校验（pack-tool-contract-design §2.2）。
// 接口冻结文档：docs/interfaces-output-validator-2026-10-11.md。
//
// 语义：MCP 工具输出从"插件自觉"变为"平台强制"——桥接层执行后按
// schema 校验，非法输出不进 task_message，模型看到标准违规错误而非
// 幻觉结构；原文全文只进审计日志。

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// OutputValidator 校验单个 MCP 工具调用的输出是否符合其输出契约。
//
// 管线位置：装饰器 wrapping eino tool.BaseTool 的 InvokableRun——工具
// 执行成功之后、结果进入模型上下文/task_message 之前。校验失败时调用方
// 以标准违规错误串（见 ViolationError）替换原结果返回。
//
// B1（provision 声明 schema）与 C1（内置工具注册表）通过实现本接口
// 接入，不需要改动调用方。
type OutputValidator interface {
	// Validate 返回 nil 表示放行；返回 *ViolationError 表示判违规；
	// 其他错误为校验器自身故障，调用方按违规处理（fail-closed）。
	Validate(vi Input) error
}

// Input 是一次输出校验的输入。
type Input struct {
	ToolName string          // 工具名（含 pack 前缀），用于违规报告与审计
	Schema   json.RawMessage // 工具的输出 JSON Schema；nil/空 = 未注册 schema
	Document any             // 待验文档：桥接层解包后的结构化结果（通常为 map[string]any）
}

// ViolationError 表示一次输出契约违规。
// 调用方用它生成面向模型的标准错误串（ModelFacingText）。
type ViolationError struct {
	ToolName string // 违规工具名
	Reason   string // 违规原因短语（枚举见接口文档 §4，v0 冻结）
}

func (e *ViolationError) Error() string {
	return e.Reason
}

// violationReason 枚举（接口文档 §4 冻结，B2/C1 依赖）。
const (
	violationMissingStructured = "structured content missing"   // 有 schema 但拿不到结构化文档
	violationSchemaMismatch    = "schema mismatch"              // 有 schema，文档不符合 schema
	violationMalformedJSON     = "malformed json"               // 无 schema，content 非合法 JSON
	violationUnstructuredText  = "unstructured text"            // 无 schema，合法 JSON 但非 object/array
)

// violationExcerptMax 违规报告中原文截断上限（任务书：前 500 字符）。
const violationExcerptMax = 500

// violationBody 是标准违规错误的 JSON 载荷（键集合冻结，顺序不保证）。
type violationBody struct {
	Code          string `json:"code"`
	Tool          string `json:"tool"`
	Reason        string `json:"reason"`
	OutputExcerpt string `json:"output_excerpt"`
}

// ModelFacingText 生成模型可见的标准违规错误（一行摘要 + JSON 载荷）。
// rawOutput 为工具原始输出（包装串），仅截前 500 字符进入本串，
// 全文只进审计日志。
func (e *ViolationError) ModelFacingText(rawOutput string) string {
	excerpt := rawOutput
	if r := []rune(excerpt); len(r) > violationExcerptMax {
		excerpt = string(r[:violationExcerptMax])
	}
	b, err := json.Marshal(violationBody{
		Code:          "TOOL_OUTPUT_CONTRACT_VIOLATION",
		Tool:          e.ToolName,
		Reason:        e.Reason,
		OutputExcerpt: excerpt,
	})
	if err != nil {
		// Marshal 静态结构不可能失败；保底也让模型看到失败而非原文。
		return fmt.Sprintf("tool output contract violation: %s (tool %s)", e.Reason, e.ToolName)
	}
	return "tool output contract violation: " + e.Reason + "\n" + string(b)
}

// NewDefaultValidator 返回 v0 的两级默认实现：
//
//	第一级 schemaValidator——注册了 schema 的工具走 JSON Schema 子集校验；
//	第二级 structuredProbe——未注册 schema 的工具做结构化 JSON 探测
//	（Document 必须是 object/array，即 map/slice；字符串/标量/nil → 违规）。
//
// schema 存在即只走第一级（schema 校验已蕴含"必须是结构化 JSON"）。
func NewDefaultValidator() OutputValidator {
	return &defaultValidator{}
}

type defaultValidator struct{}

func (d *defaultValidator) Validate(vi Input) error {
	if len(vi.Schema) > 0 {
		return validateJSONSchemaSubset(vi.Schema, vi.Document)
	}
	return probeStructured(vi.Document)
}

// probeStructured 是无 schema 工具的 v0 回落：结构化 JSON 探测。
// 合法 JSON 的 object/array（经 encoding/json 解码为 map/slice）放行；
// 字符串（纯文本）与其他标量 → 违规。
func probeStructured(doc any) error {
	switch doc.(type) {
	case map[string]any, []any:
		return nil
	default:
		return &ViolationError{Reason: violationUnstructuredText}
	}
}

// ---------------------------------------------------------------------------
// JSON Schema 子集校验器（v0 自研，零新增依赖）。
//
// 背景：仓内 vendor 无通用 JSON Schema 校验器（eino-contrib/invopop 均
// 只是结构定义包），A1 按"依赖冻结"自研覆盖 MCP outputSchema 生态最常用
// 关键字子集。支持：type（含数组）/properties/required/additionalProperties
// (bool)/items/minItems/maxItems/minLength/maxLength/pattern/minimum/
// maximum/exclusiveMinimum/exclusiveMaximum/enum/const/anyOf/oneOf/allOf/
// #/$defs 内部 $ref。遇到不支持的复合关键字 → 按"schema mismatch"违规
// fail-closed，绝不静默放行。升级为完整校验器时只换本节实现。

const (
	mismatchPrefix = "schema mismatch: " // reason 前缀，详情追加其后
)

type schemaNode struct {
	// 组合子句
	AnyOf []json.RawMessage `json:"anyOf"`
	OneOf []json.RawMessage `json:"oneOf"`
	AllOf []json.RawMessage `json:"allOf"`
	Ref   *string           `json:"$ref"`

	// 类型
	Type any `json:"type"` // string 或 []string

	// 对象
	Properties           map[string]json.RawMessage `json:"properties"`
	Required             []string                   `json:"required"`
	AdditionalProperties any                        `json:"additionalProperties"` // bool 或 schema（v0 仅支持 bool；schema 形态按不支持处理）

	// 数组
	Items    *json.RawMessage `json:"items"`
	MinItems *int             `json:"minItems"`
	MaxItems *int             `json:"maxItems"`

	// 字符串
	MinLength *int   `json:"minLength"`
	MaxLength *int   `json:"maxLength"`
	Pattern   string `json:"pattern"`

	// 数值
	Minimum          *float64 `json:"minimum"`
	Maximum          *float64 `json:"maximum"`
	ExclusiveMinimum *float64 `json:"exclusiveMinimum"`
	ExclusiveMaximum *float64 `json:"exclusiveMaximum"`

	// 枚举/常量
	Enum []any `json:"enum"`
	Const any  `json:"const"`
}

var unsupportedSchemaKeys = []string{
	"not", "if", "then", "else", "patternProperties", "propertyNames",
	"contains", "prefixItems", "dependentSchemas", "format",
	"multipleOf", "minProperties", "maxProperties", "dependencies",
	"exclusiveMinimumClaim", // 占位防误配，无实义
}

// validateJSONSchemaSubset 校验 doc 是否符合 schema。
// 违规返回 *ViolationError{Reason: "schema mismatch: <详情>"}。
func validateJSONSchemaSubset(schema json.RawMessage, doc any) error {
	var root schemaNode
	if err := json.Unmarshal(schema, &root); err != nil {
		return &ViolationError{Reason: mismatchPrefix + "invalid output schema document: " + err.Error()}
	}
	var defs map[string]json.RawMessage
	// 顶层（以及任意节点）的 $defs 都可能被引用；v0 只登记顶层 $defs。
	var top struct {
		Defs map[string]json.RawMessage `json:"$defs"`
	}
	if err := json.Unmarshal(schema, &top); err == nil {
		defs = top.Defs
	}
	if err := validateNode(schema, &root, doc, defs); err != nil {
		var ve *ViolationError
		if errors.As(err, &ve) {
			return ve
		}
		return &ViolationError{Reason: mismatchPrefix + err.Error()}
	}
	return nil
}

// validateNode 校验单个 schema 节点。返回的裸 error 文本会被包成
// "schema mismatch: <详情>"。
func validateNode(raw json.RawMessage, node *schemaNode, doc any, defs map[string]json.RawMessage) error {
	// 不支持关键字：fail-closed（放行未验关键字等于没有契约）。
	if bad := firstUnsupportedKey(raw); bad != "" {
		return fmt.Errorf("schema uses unsupported keyword %q (fail-closed)", bad)
	}

	// $ref：仅支持同文档 "#/$defs/<name>"。
	if node.Ref != nil {
		target, err := resolveInternalRef(*node.Ref, defs)
		if err != nil {
			return err
		}
		var child schemaNode
		if err := json.Unmarshal(target, &child); err != nil {
			return fmt.Errorf("invalid $ref target: %w", err)
		}
		return validateNode(target, &child, doc, defs)
	}

	// allOf：全部满足。
	for _, sub := range node.AllOf {
		if err := validateSub(sub, doc, defs); err != nil {
			return err
		}
	}
	// anyOf：至少一个满足。
	if len(node.AnyOf) > 0 {
		ok := false
		for _, sub := range node.AnyOf {
			if validateSub(sub, doc, defs) == nil {
				ok = true
				break
			}
		}
		if !ok {
			return errors.New("value matches none of anyOf")
		}
	}
	// oneOf：恰好一个满足。
	if len(node.OneOf) > 0 {
		n := 0
		for _, sub := range node.OneOf {
			if validateSub(sub, doc, defs) == nil {
				n++
			}
		}
		if n != 1 {
			return fmt.Errorf("value matches %d of oneOf schemas (need exactly 1)", n)
		}
	}

	// enum / const
	if len(node.Enum) > 0 {
		if !anyEqualJSON(node.Enum, doc) {
			return errors.New("value not in enum")
		}
	}
	if node.Const != nil && !anyEqualJSON([]any{node.Const}, doc) {
		return errors.New("value != const")
	}

	// type（string 或数组形态）
	if types := node.typeNames(); len(types) > 0 {
		actual, ok := jsonTypeName(doc)
		if !ok {
			return fmt.Errorf("value of unsupported JSON kind %T", doc)
		}
		// integer 是 number 的子集：JSON number 整值可命中 integer。
		typeOK := containsStr(types, actual) ||
			(actual == "number" && containsStr(types, "integer") &&
				func() bool { f, isF := doc.(float64); return isF && f == float64(int64(f)) }())
		if !typeOK {
			return fmt.Errorf("expected type %s, got %s", strings.Join(types, "|"), actual)
		}
	}

	switch v := doc.(type) {
	case map[string]any:
		return validateObject(node, v, defs)
	case []any:
		return validateArray(node, v, defs)
	case string:
		return validateString(node, v)
	case float64:
		return validateNumber(node, v)
	}
	return nil
}

func validateSub(raw json.RawMessage, doc any, defs map[string]json.RawMessage) error {
	var child schemaNode
	if err := json.Unmarshal(raw, &child); err != nil {
		return err
	}
	return validateNode(raw, &child, doc, defs)
}

func validateObject(node *schemaNode, m map[string]any, defs map[string]json.RawMessage) error {
	for _, req := range node.Required {
		if _, ok := m[req]; !ok {
			return fmt.Errorf("missing required property %q", req)
		}
	}
	addlAllowed := true
	if b, ok := node.AdditionalProperties.(bool); ok {
		addlAllowed = b
	} else if node.AdditionalProperties != nil {
		// additionalProperties 为 schema 形态：v0 不支持，fail-closed 已由
		// firstUnsupportedKey 之外的此处兜底（bool 之外的非 nil 值）。
		return errors.New("additionalProperties as schema is unsupported (fail-closed)")
	}
	for k, v := range m {
		sub, ok := node.Properties[k]
		if !ok {
			if !addlAllowed {
				return fmt.Errorf("additional property %q not allowed", k)
			}
			continue
		}
		if err := validateSub(sub, v, defs); err != nil {
			return fmt.Errorf("property %q: %w", k, err)
		}
	}
	return nil
}

func validateArray(node *schemaNode, arr []any, defs map[string]json.RawMessage) error {
	if node.MinItems != nil && len(arr) < *node.MinItems {
		return fmt.Errorf("array length %d < minItems %d", len(arr), *node.MinItems)
	}
	if node.MaxItems != nil && len(arr) > *node.MaxItems {
		return fmt.Errorf("array length %d > maxItems %d", len(arr), *node.MaxItems)
	}
	if node.Items != nil {
		for i, v := range arr {
			if err := validateSub(*node.Items, v, defs); err != nil {
				return fmt.Errorf("item[%d]: %w", i, err)
			}
		}
	}
	return nil
}

func validateString(node *schemaNode, s string) error {
	if node.MinLength != nil && len([]rune(s)) < *node.MinLength {
		return fmt.Errorf("string length %d < minLength %d", len([]rune(s)), *node.MinLength)
	}
	if node.MaxLength != nil && len([]rune(s)) > *node.MaxLength {
		return fmt.Errorf("string length %d > maxLength %d", len([]rune(s)), *node.MaxLength)
	}
	if node.Pattern != "" {
		re, err := regexp.Compile(node.Pattern)
		if err != nil {
			return fmt.Errorf("schema pattern %q invalid: %w", node.Pattern, err)
		}
		if !re.MatchString(s) {
			return fmt.Errorf("string does not match pattern %q", node.Pattern)
		}
	}
	return nil
}

func validateNumber(node *schemaNode, f float64) error {
	if node.Minimum != nil && f < *node.Minimum {
		return fmt.Errorf("value %v < minimum %v", f, *node.Minimum)
	}
	if node.Maximum != nil && f > *node.Maximum {
		return fmt.Errorf("value %v > maximum %v", f, *node.Maximum)
	}
	if node.ExclusiveMinimum != nil && f <= *node.ExclusiveMinimum {
		return fmt.Errorf("value %v <= exclusiveMinimum %v", f, *node.ExclusiveMinimum)
	}
	if node.ExclusiveMaximum != nil && f >= *node.ExclusiveMaximum {
		return fmt.Errorf("value %v >= exclusiveMaximum %v", f, *node.ExclusiveMaximum)
	}
	return nil
}

// typeNames 解析 type 关键字的 string / []string 两种形态。
func (n *schemaNode) typeNames() []string {
	switch t := n.Type.(type) {
	case string:
		return []string{t}
	case []any:
		out := make([]string, 0, len(t))
		for _, v := range t {
			if s, ok := v.(string); ok {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}

// firstUnsupportedKey 探测 schema 节点里的不支持关键字（fail-closed 用）。
func firstUnsupportedKey(raw json.RawMessage) string {
	var probe map[string]json.RawMessage
	if err := json.Unmarshal(raw, &probe); err != nil {
		return ""
	}
	for _, k := range unsupportedSchemaKeys {
		if _, ok := probe[k]; ok {
			return k
		}
	}
	// additionalProperties 的 schema 形态（非 bool）也视为不支持。
	if ap, ok := probe["additionalProperties"]; ok {
		var b bool
		if json.Unmarshal(ap, &b) != nil {
			return "additionalProperties(schema)"
		}
	}
	return ""
}

// resolveInternalRef 解析 "#/$defs/<name>" 形态的内部引用。
func resolveInternalRef(ref string, defs map[string]json.RawMessage) (json.RawMessage, error) {
	if !strings.HasPrefix(ref, "#/$defs/") {
		return nil, fmt.Errorf("unsupported $ref %q (only internal #/$defs/ allowed)", ref)
	}
	name := strings.TrimPrefix(ref, "#/$defs/")
	target, ok := defs[name]
	if !ok {
		return nil, fmt.Errorf("$ref target #/$defs/%s not found", name)
	}
	return target, nil
}

// jsonTypeName 返回 doc 的 JSON 类型名（与 JSON Schema type 关键字对齐）。
func jsonTypeName(doc any) (string, bool) {
	switch doc.(type) {
	case map[string]any:
		return "object", true
	case []any:
		return "array", true
	case string:
		return "string", true
	case float64:
		return "number", true
	case bool:
		return "boolean", true
	case nil:
		return "null", true
	}
	return "", false
}

// anyEqualJSON 按 JSON 语义比较（经 Normalize 后逐字比对）。
func anyEqualJSON(cands []any, doc any) bool {
	nb, err := json.Marshal(doc)
	if err != nil {
		return false
	}
	var nDoc any
	if json.Unmarshal(nb, &nDoc) != nil {
		return false
	}
	for _, c := range cands {
		cb, err := json.Marshal(c)
		if err != nil {
			continue
		}
		var nCand any
		if json.Unmarshal(cb, &nCand) != nil {
			continue
		}
		if equalNormalized(nCand, nDoc) {
			return true
		}
	}
	return false
}

func equalNormalized(a, b any) bool {
	ab, err1 := json.Marshal(a)
	bb, err2 := json.Marshal(b)
	return err1 == nil && err2 == nil && string(ab) == string(bb)
}

func containsStr(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}
