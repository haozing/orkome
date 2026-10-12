// checkpoint.go 把跨任务 session summary 从"纯 500 字截断"升级为
// "LLM 七段 checkpoint"。七段提示词结构（结构 + 保留规则 + preamble）逐段
// 搬运自 DeepSeek Harness（dsh）的 compaction-basic summarizer，针对 orkome
// 的 task/session 上下文做了参数化适配。
//
// 搬运来源（MIT License）：
//
//	deepseek-harness 仓库
//	packages/compaction/compaction-basic/src/summarizer.ts
//	https://github.com/deepseek-ai/deepseek-harness
//
// 保留的内容：
//   - 七段结构：Primary Request and Intent / Key Technical Concepts / Files and
//     Code / Errors and Fixes / Pending Jobs / Current Work / Next Step /
//     Critical Context（首段为请求背景，共八个小标题、七个正文段）
//   - Rules 保留规则：精确路径、命令、错误串、标识符、数值、函数签名、
//     语法片段必须原样保留；用户纠正必须忠实记录
//   - CHECKPOINT_PREAMBLE："既定背景直接续做"的注入框定
//
// 明确不搬运的内容（dsh-borrowing-table-v2 §1.2 裁决）：
//   - 暖前缀缓存复用（SummarizationInput 的 tools/replay 管线）：orkome 的
//     summary 是独立的 aux 小调用（Engine.Generate），没有"复刻上次主请求
//     前缀"的请求管线，抄了不生效。等 summary 调用与主请求同管线时再评估。
//
// 分层（pack-rollout-plan §8 审计修正）：本文件只含提示词与材料装配（纯
// 函数，domain 层零外部依赖）——LLM 调用经 app 层的 Engine.Generate
// （modelmeta 工厂）完成，保证 LLM 不进 repo 层；repo 只存取。
package domain

import "strings"

// SummaryOpenTag / SummaryCloseTag 是 checkpoint 文本在注入时的包裹标签
// （搬运自 dsh 的 <compacted-summary> 框定；用于"上一份 checkpoint 合并"
// 规则的识别）。
const (
	SummaryOpenTag  = "<compacted-summary>"
	SummaryCloseTag = "</compacted-summary>"
)

// CheckpointPreamble 是七段 checkpoint 注入到下一任务时使用的框定语
// （dsh CHECKPOINT_PREAMBLE 原文搬运）：让摘要成为"既定背景"，模型直接
// 续做而不复述、不致谢、不提"压缩过"这件事。
const CheckpointPreamble = "This is an automatically generated checkpoint condensing an earlier span of the conversation to free up context. Treat the captured context as established background and build on it without restating it. Continue the task directly from the messages that follow, without acknowledging this checkpoint."

// compactionInstruction 是七段压缩指令（dsh COMPACTION_INSTRUCTION 原文搬运）。
// 用 var 而非 const：最后一条 Prior checkpoint 规则里的包裹标签需引用
// SummaryOpenTag 常量（单一事实来源）；内容由 golden 测试锁死，不因 var 可
// 变而失控。
var compactionInstruction = strings.Join([]string{
	"You are now acting as a compaction engine for this AI coding assistant. Condense the conversation ABOVE into a structured checkpoint that lets another model resume the work with no loss of essential context.",
	"",
	"Output EXACTLY the Markdown structure below: keep every section, in order. Use terse bullets, not prose paragraphs. Write \"(none)\" for an empty section — never drop a section.",
	"",
	"## Primary Request and Intent",
	"- [the user's original and evolving goals; quote verbatim where the exact wording matters]",
	"",
	"## Key Technical Concepts",
	"- [technologies, frameworks, patterns, and conventions in play]",
	"",
	"## Files and Code",
	"- [exact path: why it matters, key changes or snippets]",
	"",
	"## Errors and Fixes",
	"- [error: how it was resolved, plus any related user feedback]",
	"",
	"## Pending Jobs",
	"- [explicitly requested work not yet completed]",
	"",
	"## Current Work",
	"- [precisely what was in progress at this checkpoint]",
	"",
	"## Next Step",
	"- [the single next action, directly in line with the most recent request, or \"(none)\"]",
	"",
	"## Critical Context",
	"- [decisions and their rationale, constraints, user preferences, open questions, data needed to continue]",
	"",
	"Rules:",
	"- Write concise English engineering prose. Preserve exact file paths, commands, error strings, identifiers, numeric values, function signatures, and syntax fragments.",
	"- Capture user feedback and explicit instructions faithfully, especially corrections.",
	"- Do NOT mention this summarization request or that the context was compacted.",
	"- Output only the checkpoint text: do not call any tool or take any other action.",
	"- If the conversation already contains a " + SummaryOpenTag + " block, it is a PRIOR checkpoint. Do not copy it forward verbatim: preserve still-true facts, drop stale ones, and merge newer information into a single consolidated summary under the same structure.",
}, "\n")

// CompactionInstruction 返回七段压缩指令全文（golden 测试的锚点）。
func CompactionInstruction() string { return compactionInstruction }

// TaskMaterial 是一次 orkome 任务结算时可用的摘要材料：任务的执行轨迹
// （task_message，按 seq 升序）、最终产出/失败信息、以及 work item 会话里
// 前序任务的产出（再水合时的同源材料）。Truncated 标记沿用 task_message
// 的截断语义（2000 rune 处截断即丢失）。
type TaskMaterial struct {
	// TaskID / WorkspaceID 仅用于日志与用量归因，不进提示词。
	TaskID      string
	WorkspaceID string

	// Messages 是本任务的 task_message 明细（seq 升序），按 kind 标注角色。
	Messages []Message

	// FinalText 是任务的最终文本（completed 为 result，failed 为
	// reason+": "+error，与 domain.TaskSummary 的 fallback 口径一致）。
	FinalText string

	// SessionTurns 是同 work item+agent 的前序任务产出（跨任务 session 语境，
	// 与派发侧 SessionTranscript 同源），可为空。
	SessionTurns []SessionTurn

	// MaxTranscriptRunes 限制送入模型的轨迹长度（按 rune），0 时取
	// DefaultMaxTranscriptRunes。
	MaxTranscriptRunes int
}

// Message 是送入摘要的单条任务消息。
type Message struct {
	Kind    string // text / tool_use / tool_result / error / approval
	Content string
}

// SessionTurn 是同会话前序任务的产出（Role: assistant）。
type SessionTurn struct {
	Role    string
	Content string
}

// DefaultMaxTranscriptRunes 是轨迹窗口缺省值：约 24k rune，兼顾覆盖长任务
// 与控制 aux 调用成本（提示词本体约 1.6k rune）。可以后续按模型窗口调。
const DefaultMaxTranscriptRunes = 24000

// SystemPrompt 返回七段 checkpoint 的 system 提示词。dsh 原设计里
// compaction instruction 是作为"重放对话末尾的最终 user 消息"投递的（暖
// 前缀复用的一部分）；orkome 的独立小调用没有可复用的前缀，故整体上移为
// system 角色，user 消息只承载材料本身。
func SystemPrompt() string { return compactionInstruction }

// UserPrompt 把 orkome 的 task/session 材料参数化成摘要调用的 user 输入。
// 材料分三块呈现：先给最终结算文本（最能代表"任务要沉淀什么"），再给
// 任务执行轨迹，最后给跨任务会话转写；每块截到轨迹窗口内。
func UserPrompt(m TaskMaterial) string {
	limit := m.MaxTranscriptRunes
	if limit <= 0 {
		limit = DefaultMaxTranscriptRunes
	}
	var b strings.Builder
	b.WriteString("=== Task final text ===\n")
	b.WriteString(clipRunes(strings.TrimSpace(m.FinalText), limit))
	b.WriteString("\n\n=== Task message log (oldest → newest) ===\n")
	if len(m.Messages) == 0 {
		b.WriteString("(none)\n")
	}
	budget := limit
	for _, msg := range m.Messages {
		line := "[" + msg.Kind + "] " + strings.TrimSpace(msg.Content)
		if budget <= 0 {
			b.WriteString("…(transcript window exhausted)\n")
			break
		}
		seg := clipRunes(line, budget)
		b.WriteString(seg)
		b.WriteString("\n")
		budget -= len([]rune(seg))
	}
	if len(m.SessionTurns) > 0 {
		b.WriteString("\n=== Prior session turns (same work item + agent, oldest → newest) ===\n")
		for _, t := range m.SessionTurns {
			line := "[" + t.Role + "] " + strings.TrimSpace(t.Content)
			if budget <= 0 {
				b.WriteString("…(transcript window exhausted)\n")
				break
			}
			seg := clipRunes(line, budget)
			b.WriteString(seg)
			b.WriteString("\n")
			budget -= len([]rune(seg))
		}
	}
	b.WriteString("\n=== Produce the checkpoint now ===")
	return b.String()
}

// FrameCheckpoint 把一份 checkpoint 文本包进注入框定（dsh frameSummary 的
// 等价物）：preamble + 包裹标签，供下一任务把摘要当既定背景续做。
func FrameCheckpoint(summary string) string {
	return CheckpointPreamble + "\n\n" + SummaryOpenTag + "\n" + summary + "\n" + SummaryCloseTag
}

// Fallback 是开关关闭（或 LLM 调用失败）时的回落路径：与 domain.TaskSummary
// 完全一致的纯截断（byte-identical），保证"先上代码不改变行为"。
func Fallback(lastText, fallback string) string {
	return TaskSummary(lastText, fallback)
}

func clipRunes(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max])
}
