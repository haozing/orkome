package engine

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"

	orchapp "orkome/internal/orchestration/app"
)

// W3 阶段简报单测：契约 §5 context 形状的 engine 侧消费行为。表驱动——
// 完整注入 / 缺 trigger 不注入 / 缺 exit_status 不注入；拼接顺序另测
//（与 squadBrief 共存）。

func wfFullContext() map[string]any {
	// 与 internal/work/app/service_workflow_state_test.go
	// TestWorkflowDispatch_ContextShapeContract 同构的完整 §5 形状。
	return map[string]any{
		"trigger":           "workflow_stage",
		"workflow_id":       "wf-7",
		"stage_id":          "st-9",
		"stage_name":        "方案设计",
		"stage_position":    2,
		"stage_exit_status": "in_review",
		"instruction":       "渲染后的指令",
		"work_item_title":   "工单标题",
	}
}

func TestStageBriefing_Table(t *testing.T) {
	t.Run("full context injects", func(t *testing.T) {
		got := stageBriefing(orchapp.Task{Context: wfFullContext()})
		assert.Contains(t, got, "[Workflow Stage Briefing]")
		assert.Contains(t, got, `You are the executor of stage "方案设计" (stage 2)`)
		assert.Contains(t, got, "Assignment: 渲染后的指令", "instruction 原样嵌入不翻译")
		assert.Contains(t, got, `set the status to "in_review"`)
		assert.Contains(t, got, "update_work_item_status")
		assert.Contains(t, got, "advances the work item to the next stage automatically")
		assert.Contains(t, got, "Do not create sub work items")
	})

	t.Run("missing trigger no injection", func(t *testing.T) {
		ctx := wfFullContext()
		delete(ctx, "trigger")
		assert.Empty(t, stageBriefing(orchapp.Task{Context: ctx}))
	})

	t.Run("non workflow trigger no injection", func(t *testing.T) {
		ctx := wfFullContext()
		ctx["trigger"] = "assignment"
		assert.Empty(t, stageBriefing(orchapp.Task{Context: ctx}), "非流程任务零影响")
	})

	t.Run("missing exit_status no injection", func(t *testing.T) {
		ctx := wfFullContext()
		delete(ctx, "stage_exit_status")
		assert.Empty(t, stageBriefing(orchapp.Task{Context: ctx}), "无出口态的简报是残缺指令")
	})

	t.Run("blank exit_status no injection", func(t *testing.T) {
		ctx := wfFullContext()
		ctx["stage_exit_status"] = "  "
		assert.Empty(t, stageBriefing(orchapp.Task{Context: ctx}))
	})

	t.Run("nil context no injection", func(t *testing.T) {
		assert.Empty(t, stageBriefing(orchapp.Task{}))
	})

	t.Run("empty instruction renders dash placeholder", func(t *testing.T) {
		ctx := wfFullContext()
		delete(ctx, "instruction")
		got := stageBriefing(orchapp.Task{Context: ctx})
		assert.Contains(t, got, "Assignment: —", "空指令给占位符，不注入残缺行")
	})
}

// 拼接顺序：user = prompt + squadBrief + stageBrief（engine.go 唯一拼接点）。
// squad 执行者场景两个简报共存——leader 既看小队简报也看阶段简报。
func TestStageBriefing_CoexistsWithSquadBrief_Order(t *testing.T) {
	task := orchapp.Task{
		ID: "t1", WorkspaceID: "ws-1", WorkItemID: "wi-1", Status: "running",
		IsLeaderTask: true, SquadID: "sq-1",
		Context: wfFullContext(),
	}
	prompt := strings.TrimSpace(assembleUserPrompt(task, nil, nil))
	squad := squadBriefing(orchapp.SquadInfo{Name: "alpha"})
	stage := stageBriefing(task)

	full := strings.TrimSpace(prompt + "\n" + squad + "\n" + stage)

	assert.Less(t, strings.Index(full, "[Squad Briefing]"), strings.Index(full, "[Workflow Stage Briefing]"),
		"squad brief before stage brief")
	assert.Less(t, strings.Index(full, "Please complete the task"), strings.Index(full, "[Squad Briefing]"),
		"base prompt first")
}
