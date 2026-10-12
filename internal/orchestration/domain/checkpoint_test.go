package domain

import (
	"strings"
	"testing"
)

// golden 七段提示词全文（搬运自 dsh summarizer.ts 的 COMPACTION_INSTRUCTION，
// 仅最后一条规则的标签插值展开为常量）。任何改动都应是有意识的：改这里
// 必须同步给出来源注释，否则就是手滑改坏七段结构。
const goldenInstruction = `You are now acting as a compaction engine for this AI coding assistant. Condense the conversation ABOVE into a structured checkpoint that lets another model resume the work with no loss of essential context.

Output EXACTLY the Markdown structure below: keep every section, in order. Use terse bullets, not prose paragraphs. Write "(none)" for an empty section — never drop a section.

## Primary Request and Intent
- [the user's original and evolving goals; quote verbatim where the exact wording matters]

## Key Technical Concepts
- [technologies, frameworks, patterns, and conventions in play]

## Files and Code
- [exact path: why it matters, key changes or snippets]

## Errors and Fixes
- [error: how it was resolved, plus any related user feedback]

## Pending Jobs
- [explicitly requested work not yet completed]

## Current Work
- [precisely what was in progress at this checkpoint]

## Next Step
- [the single next action, directly in line with the most recent request, or "(none)"]

## Critical Context
- [decisions and their rationale, constraints, user preferences, open questions, data needed to continue]

Rules:
- Write the summary in the same human language as the conversation content (a Chinese task yields a Chinese summary); keep technical terms, file paths, commands, and identifiers verbatim. Preserve exact file paths, commands, error strings, identifiers, numeric values, function signatures, and syntax fragments.
- Capture user feedback and explicit instructions faithfully, especially corrections.
- Do NOT mention this summarization request or that the context was compacted.
- Output only the checkpoint text: do not call any tool or take any other action.
- If the conversation already contains a <compacted-summary> block, it is a PRIOR checkpoint. Do not copy it forward verbatim: preserve still-true facts, drop stale ones, and merge newer information into a single consolidated summary under the same structure.`

const goldenPreamble = "This is an automatically generated checkpoint condensing an earlier span of the conversation to free up context. Treat the captured context as established background and build on it without restating it. Continue the task directly from the messages that follow, without acknowledging this checkpoint."

func TestCompactionInstructionGolden(t *testing.T) {
	if got := CompactionInstruction(); got != goldenInstruction {
		t.Fatalf("七段提示词与 golden 不一致（防手滑改坏搬运文本）\n--- got ---\n%s\n--- want ---\n%s", got, goldenInstruction)
	}
}

func TestCheckpointPreambleGolden(t *testing.T) {
	if got := CheckpointPreamble; got != goldenPreamble {
		t.Fatalf("CHECKPOINT_PREAMBLE 与 golden 不一致:\ngot:  %q\nwant: %q", got, goldenPreamble)
	}
}

// TestSevenSectionTitlesPresent 断言八个小标题按顺序存在且各只出现一次——
// 即使 golden 整文被误删，这条也兜底防结构损坏。
func TestSevenSectionTitlesPresent(t *testing.T) {
	titles := []string{
		"## Primary Request and Intent",
		"## Key Technical Concepts",
		"## Files and Code",
		"## Errors and Fixes",
		"## Pending Jobs",
		"## Current Work",
		"## Next Step",
		"## Critical Context",
	}
	got := CompactionInstruction()
	for _, title := range titles {
		if n := strings.Count(got, title); n != 1 {
			t.Errorf("段落标题 %q 出现 %d 次，期望恰好 1 次", title, n)
		}
	}
}

// TestPreservationRulesPresent 断言保留规则的关键句在（精确路径/命令/错误串/
// 用户纠正/不提压缩/不调工具/上一份 checkpoint 合并）。
func TestPreservationRulesPresent(t *testing.T) {
	got := CompactionInstruction()
	mustContain := []string{
		"Preserve exact file paths, commands, error strings, identifiers, numeric values, function signatures, and syntax fragments.",
		"Capture user feedback and explicit instructions faithfully, especially corrections.",
		"Do NOT mention this summarization request or that the context was compacted.",
		"Output only the checkpoint text: do not call any tool or take any other action.",
		// dsh 原文的 Prior checkpoint 规则只引用开标签；闭标签只出现在
		// FrameCheckpoint 的框定里（见 TestFrameCheckpoint）。
		SummaryOpenTag,
	}
	for _, s := range mustContain {
		if !strings.Contains(got, s) {
			t.Errorf("保留规则缺失关键句: %q", s)
		}
	}
}

func TestFrameCheckpoint(t *testing.T) {
	got := FrameCheckpoint("## Current Work\n- writing tests")
	want := goldenPreamble + "\n\n" + SummaryOpenTag + "\n## Current Work\n- writing tests\n" + SummaryCloseTag
	if got != want {
		t.Fatalf("FrameCheckpoint 框定不匹配:\ngot:  %q\nwant: %q", got, want)
	}
}

// TestFallbackMatchesLegacyByteIdentical 断言回落路径与 TaskSummary
// byte-identical——开关关闭时的行为即旧行为。
func TestFallbackMatchesLegacyByteIdentical(t *testing.T) {
	cases := []struct{ lastText, fallback string }{
		{"conclusion: RSS done", "full result"},
		{"", "engine_error: boom"},
		{"", strings.Repeat("R", 600)},
		{strings.Repeat("é", 501), ""},
		{"", ""},
	}
	for _, c := range cases {
		if got, want := Fallback(c.lastText, c.fallback), TaskSummary(c.lastText, c.fallback); got != want {
			t.Fatalf("Fallback(%q,%q) = %q, 旧路径 = %q，必须 byte-identical", c.lastText, c.fallback, got, want)
		}
	}
}
