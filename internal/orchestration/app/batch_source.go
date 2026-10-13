package app

// Batch v2.0 P2 通道 B：source 物化器（docs/batch-agent-tool-design-2026-10-13.md
// §1.2）。数据不流经 LLM——server 侧机械执行：
//
//	代调 source.tool（ToolExec 端口，组装根接线到引擎 MCP 工具面）
//	  → JSONPath 抽取元素数组（直链路径 $.a.b[*].c，零依赖）
//	  → payload_template 逐条代入（$.field 取值 + 字面量）
//	  → required 校验（缺失 → skipped + 原因，绝不沉默）
//	  → 对账计数（source_count = materialized + skipped）
//
// 与另一 agent 的 draft 通道（CreateDraftFromSource）解耦：本文件只做
// "source → 成品 payloads" 的纯映射，不落库；调用方（工具面/面板创建
// 入口）拿 Payloads 落 batch_item、计数落 batch.source_materialization。

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// MaterializedSource 是通道 B 的物化结果。
type MaterializedSource struct {
	SourceCount  int
	Materialized int
	Skipped      int
	Sample       []map[string]any // 前 3 条成品
	Payloads     []map[string]any // 全部成品（调用方落 batch_item）

	// SkipReasons 与 Payloads 平行（skipped 条目不在 Payloads 里，原因按
	// 抽取序排列）——调用方落 batch_item.skip_reason 用。
	SkipReasons []string
}

// ToolExec 代调数据源工具的最小端口（组装根接线到引擎工具面）。
type ToolExec interface {
	// Invoke 执行一个 agent 工具面上已注册的工具，返回其结果 JSON
	Invoke(ctx context.Context, ws, workItemID, agentID, toolName string, args map[string]any) (json.RawMessage, error)
}

// MaterializeFromSource 按 batch.Source 的 source/payload_template/required
// 契约物化全部成品 payloads。返回错误仅限结构性失败（source.tool 缺失、
// 代调失败、JSONPath 无命中/路径写错）——条目级字段缺失不算错误，计入
// Skipped 并带原因。
func MaterializeFromSource(ctx context.Context, exec ToolExec, b *Batch) (*MaterializedSource, error) {
	if exec == nil {
		return nil, fmt.Errorf("source materialization: ToolExec not wired")
	}
	src := mapSource(b.Source["source"])
	if src == nil {
		return nil, fmt.Errorf("source materialization: batch.source.source required")
	}
	srcTool, _ := src["tool"].(string)
	if strings.TrimSpace(srcTool) == "" {
		return nil, fmt.Errorf("source materialization: source.tool required")
	}

	// 代调参数：{work_item_id} 等占位符从 batch 字段/params 替换。
	callArgs := mapArgs(src["args"])
	tokens := sourceTokens(b)
	for k, v := range callArgs {
		if s, ok := v.(string); ok {
			callArgs[k] = replaceTokens(s, tokens)
		}
	}

	raw, err := exec.Invoke(ctx, b.WorkspaceID, b.WorkItemID,
		paramString(b.Params["agent_id"]), srcTool, callArgs)
	if err != nil {
		return nil, fmt.Errorf("source tool %q failed: %w", srcTool, err)
	}
	var doc any
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("source tool %q returned non-JSON: %w", srcTool, err)
	}
	doc = unwrapMCPContent(doc)

	// JSONPath 抽取（source.pick 非空时）；缺省 = 结果本身是元素数组。
	elements := []any{doc}
	if pick, _ := src["pick"].(string); strings.TrimSpace(pick) != "" {
		val, ok, perr := directPathGet(doc, pick)
		if perr != nil {
			return nil, fmt.Errorf("source pick %q: %w", pick, perr)
		}
		if !ok {
			return nil, fmt.Errorf("source pick %q matched nothing (path error is fatal, never silent)", pick)
		}
		if arr, isArr := val.([]any); isArr {
			elements = arr
		} else {
			elements = []any{val}
		}
	} else if arr, isArr := doc.([]any); isArr {
		elements = arr
	}

	tmpl := mapArgs(b.Source["payload_template"])
	if len(tmpl) == 0 {
		return nil, fmt.Errorf("source materialization: payload_template required")
	}
	required := stringsOf(b.Source["required"])

	out := &MaterializedSource{SourceCount: len(elements)}
	for i, el := range elements {
		payload, miss := applyTemplate(tmpl, el)
		if len(miss) == 0 {
			for _, f := range required {
				if v, ok := payload[f]; !ok || isEmptyVal(v) {
					miss = append(miss, "required field "+f+" missing/empty")
				}
			}
		}
		if len(miss) > 0 {
			out.Skipped++
			out.SkipReasons = append(out.SkipReasons,
				fmt.Sprintf("item#%d: %s", i, strings.Join(miss, "; ")))
			continue
		}
		out.Materialized++
		out.Payloads = append(out.Payloads, payload)
		if len(out.Sample) < 3 {
			out.Sample = append(out.Sample, payload)
		}
	}
	return out, nil
}

// sourceTokens 占位符取值表：batch 自身字段优先，params 补充
// （{work_item_id}/{workspace_id}/{agent_id} 等）。
func sourceTokens(b *Batch) map[string]string {
	t := map[string]string{
		"workspace_id": b.WorkspaceID,
		"work_item_id": b.WorkItemID,
		"agent_id":     paramString(b.Params["agent_id"]),
	}
	for k, v := range b.Params {
		if s, ok := v.(string); ok && t[k] == "" {
			t[k] = s
		}
	}
	return t
}

// replaceTokens 替换 {key} 占位符；未知占位符原样保留（留给 required 校验
// 或工具自身报错，不静默吞）。
func replaceTokens(s string, tokens map[string]string) string {
	if !strings.Contains(s, "{") {
		return s
	}
	for k, v := range tokens {
		if v == "" {
			continue
		}
		s = strings.ReplaceAll(s, "{"+k+"}", v)
	}
	return s
}

// applyTemplate 把元素按模板映射成成品：值 "{$.field}" 从元素直链取值，
// 其余为字面量。返回缺失字段名列表（路径写错/取不到值）。
func applyTemplate(tmpl map[string]any, el any) (map[string]any, []string) {
	out := make(map[string]any, len(tmpl))
	var miss []string
	for k, v := range tmpl {
		ref, isStr := v.(string)
		if !isStr {
			out[k] = v
			continue
		}
		// 整串引用："$.field" → 取值（类型保真）。
		if strings.HasPrefix(ref, "$.") {
			val, ok, err := directPathGet(el, ref)
			if err != nil || !ok || isEmptyVal(val) {
				miss = append(miss, k+" ← "+ref+" not found")
				continue
			}
			out[k] = val
			continue
		}
		// 嵌内占位符："前缀 {$ .field} 后缀" → 文本代入（字符串化）。
		if strings.Contains(ref, "{$.") {
			resolved, m := interpolateBraces(ref, el)
			if m != "" {
				miss = append(miss, k+" ← "+m)
				continue
			}
			out[k] = resolved
			continue
		}
		out[k] = v
	}
	return out, miss
}

// interpolateBraces 把 "{$.a.b}" 占位符代入字符串模板；任一占位符未命中
// 返回 (原串, 首个未命中说明)。
func interpolateBraces(tmpl string, el any) (string, string) {
	out := tmpl
	for {
		i := strings.Index(out, "{$.")
		if i < 0 {
			return out, ""
		}
		j := strings.Index(out[i:], "}")
		if j < 0 {
			return out, "unclosed {$ placeholder"
		}
		path := out[i+1 : i+j] // 含前导 $
		val, ok, err := directPathGet(el, path)
		if err != nil {
			return out, "path error: " + err.Error()
		}
		if !ok || isEmptyVal(val) {
			return out, path + " not found"
		}
		vs, ok := val.(string)
		if !ok {
			b, _ := json.Marshal(val)
			vs = string(b)
		}
		out = out[:i] + vs + out[i+j+1:]
	}
}

// directPathGet 极简直链 JSONPath（设计 §1.2 够用即可，零依赖）：
// 仅支持 "$.a.b[*].c" 与根数组 "$[*]" 形态——$ 根、.field 逐层取对象
// 字段、[*] 展开数组、数组上的 .field 逐元素投影。返回 (值, 命中与否,
// 错误)；路径中途类型不匹配/字段缺失算未命中（ok=false），根非 $ 或
// 空段算路径错误（err）——设计裁决：错路径=报错，绝不沉默空数组。
func directPathGet(doc any, path string) (any, bool, error) {
	p := strings.TrimSpace(path)
	if p == "" {
		return nil, false, fmt.Errorf("empty path")
	}
	if p == "$" {
		return doc, true, nil
	}
	if !strings.HasPrefix(p, "$.") {
		return nil, false, fmt.Errorf("unsupported path %q (only $.a.b[*].c direct paths)", path)
	}
	cur := []any{doc}
	for _, seg := range strings.Split(strings.TrimPrefix(p, "$."), ".") {
		expand := strings.HasSuffix(seg, "[*]")
		field := strings.TrimSuffix(seg, "[*]")
		if expand && field == "" {
			// 段即 "[*]"（根数组展开 $[*] 或紧邻上一段已是元素流）。
			var expanded []any
			for _, node := range cur {
				if arr, ok := node.([]any); ok {
					expanded = append(expanded, arr...)
				}
			}
			cur = expanded
			if len(cur) == 0 {
				return nil, false, nil
			}
			continue
		}
		if field == "" {
			return nil, false, fmt.Errorf("empty segment in path %q", path)
		}
		next := []any{}
		for _, node := range cur {
			if arr, isArr := node.([]any); isArr {
				// 数组上的 .field：逐元素投影（jsonpath 语义）。
				for _, el := range arr {
					if v, ok := fieldOf(el, field); ok {
						next = append(next, v)
					}
				}
				continue
			}
			v, ok := fieldOf(node, field)
			if !ok {
				return nil, false, nil // 中途取不到 → 未命中（非错误）
			}
			next = append(next, v)
		}
		cur = next
		if len(cur) == 0 {
			return nil, false, nil
		}
		if expand {
			var expanded []any
			for _, node := range cur {
				if arr, isArr := node.([]any); isArr {
					expanded = append(expanded, arr...)
					continue
				}
				expanded = append(expanded, node)
			}
			cur = expanded
			if len(cur) == 0 {
				return nil, false, nil
			}
		}
	}
	if len(cur) == 1 {
		return cur[0], true, nil
	}
	return cur, true, nil
}

// fieldOf 从对象节点取字段（非对象或键缺失 → 未命中）。
func fieldOf(node any, field string) (any, bool) {
	m, ok := node.(map[string]any)
	if !ok {
		return nil, false
	}
	v, ok := m[field]
	return v, ok
}

// isEmptyVal 物化语义下的"空"：required/模板取值为 nil、空串、空数组
// 视为缺失（OCR 场景 file_id="" 落进扇出是沉默失败）。
func isEmptyVal(v any) bool {
	switch t := v.(type) {
	case nil:
		return true
	case string:
		return strings.TrimSpace(t) == ""
	case []any:
		return len(t) == 0
	case map[string]any:
		return len(t) == 0
	}
	return false
}

// paramString 安全取 params 字符串（nil/非串 → ""）。
func paramString(v any) string {
	s, _ := v.(string)
	return s
}

func mapSource(v any) map[string]any {
	m, _ := v.(map[string]any)
	return m
}

func mapArgs(v any) map[string]any {
	m, _ := v.(map[string]any)
	if m == nil {
		return map[string]any{}
	}
	return m
}

func stringsOf(v any) []string {
	arr, ok := v.([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(arr))
	for _, it := range arr {
		if s, ok := it.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

// unwrapMCPContent 拆 MCP 工具调用信封：{"content":[{"type":"text",
// "text":"<业务JSON字符串>"}]} → 解析 text 里的业务 JSON 作 doc；信封不
// 匹配（内置工具直返 JSON 等）原样返回。isError 的业务失败文本解析失败
// 时原样返回——交给 pick 的 matched-nothing fatal 报出来，不吞。
func unwrapMCPContent(doc any) any {
	env, ok := doc.(map[string]any)
	if !ok {
		return doc
	}
	content, ok := env["content"].([]any)
	if !ok || len(content) == 0 {
		return doc
	}
	first, ok := content[0].(map[string]any)
	if !ok || first["type"] != "text" {
		return doc
	}
	text, ok := first["text"].(string)
	if !ok {
		return doc
	}
	var inner any
	if err := json.Unmarshal([]byte(text), &inner); err != nil {
		return doc // 业务文本非 JSON（如 isError 的人类可读失败），原样返回
	}
	return inner
}
