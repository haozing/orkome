package engine

import (
	"fmt"
	"strings"

	orchapp "orkome/internal/orchestration/app"
)

// stageBriefing —— Project Workflow 阶段简报（W3；设计 §3"engine 侧唯一的
// 改动点"）。task.context.trigger == "workflow_stage" 时注入：告知执行者它在
// 流程的哪个阶段、要做什么（instruction 原文）、以及收尾义务（打出口态 =
// 自动进入下一阶段）。与 squadBriefing 同语言策略（英文骨架），instruction
// 原样嵌入不翻译。
//
// context 形状逐字段冻结于 docs/wf-interfaces-frozen.md §5（派发侧组装锚：
// internal/work/app/service_workflow_state_test.go TestWorkflowDispatch_
// ContextShapeContract + W2 集成 Test 1 的 SQL 断言）。
//
// 防御：trigger 不是 workflow_stage / 关键字段缺失 → 返回空串（调用方不
// 注入）——非流程任务（assignment/chat/squad/…）零影响。
func stageBriefing(task orchapp.Task) string {
	if trigger, _ := task.Context["trigger"].(string); trigger != "workflow_stage" {
		return ""
	}
	stageName, _ := task.Context["stage_name"].(string)
	position, _ := task.Context["stage_position"].(int)
	exitStatus, _ := task.Context["stage_exit_status"].(string)
	instruction, _ := task.Context["instruction"].(string)

	if strings.TrimSpace(exitStatus) == "" {
		return "" // 无出口态的简报是残缺指令（收尾义务无法表述），不注入
	}

	var b strings.Builder
	fmt.Fprintf(&b, "\n[Workflow Stage Briefing]\n"+
		"You are the executor of stage %q (stage %d) in this work item's project workflow.\n",
		stageName, position)
	fmt.Fprintf(&b, "Assignment: %s\n", orDash(instruction))
	fmt.Fprintf(&b, "Closing duty: when this stage's work is done, call update_work_item_status "+
		"to set the status to %q — that advances the work item to the next stage automatically.\n", exitStatus)
	b.WriteString("Do not create sub work items for tracking this stage unless the task genuinely requires decomposition.")
	return b.String()
}
