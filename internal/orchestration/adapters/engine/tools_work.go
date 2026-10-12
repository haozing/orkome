package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"

	"orkome/internal/contract/apierr"
	orchapp "orkome/internal/orchestration/app"
)

func isValidation(err error) bool {
	var ae *apierr.Error
	return errors.As(err, &ae) && ae.Code == apierr.Validation
}

type updateWorkItemStatusTool struct {
	update orchapp.AgentStatusUpdater
	task   orchapp.Task
	agent  orchapp.Agent
}

func (t *updateWorkItemStatusTool) Info(_ context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{
		Name: "update_work_item_status",
		Desc: "set the current work item status (via the platform state machine: position and triggers handled automatically). Input {\"status\":\"todo|in_progress|in_review|done|cancelled|...\"}",
	}, nil
}

func (t *updateWorkItemStatusTool) InvokableRun(ctx context.Context, args string, _ ...tool.Option) (string, error) {
	var in struct {
		Status string `json:"status"`
	}
	if err := json.Unmarshal([]byte(args), &in); err != nil || in.Status == "" {
		return "invalid args: status is required.", nil
	}
	if err := t.update.UpdateStatusByAgent(ctx, t.task.WorkspaceID, t.task.WorkItemID, t.agent.ID, in.Status); err != nil {

		if isValidation(err) {
			return "Status change rejected: " + err.Error(), nil
		}
		return toolFail("update_work_item_status", err)
	}
	return fmt.Sprintf("Work item status changed to %s.", in.Status), nil
}

// ---- C1 契约元数据（ContractTool 可选接口实现，行为不变，仅注册表侧声明）------

// updateWorkItemStatusTool 改工单状态（有副作用，走平台状态机），成功输出
// 是含状态名的模板文本（状态值由模型给、状态机校验，schema 只锚定形状）。
func (t *updateWorkItemStatusTool) OutputSchema() json.RawMessage {
	return json.RawMessage(`{"type":"string"}`)
}

// updateWorkItemStatusTool 有副作用，不并行。
func (t *updateWorkItemStatusTool) ConcurrencySafe() bool { return false }

type checkinWakeupTool struct {
	checkiner orchapp.WakeupCheckiner
	task      orchapp.Task
	wakeupID  string
}

func (t *checkinWakeupTool) Info(_ context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{
		Name: "checkin_wakeup",
		Desc: "after a scheduled check, report an all-clear check-in (≤500 chars) so the platform can suppress redundant follow-ups. Input {\"note\":\"one-line conclusion\"}",
	}, nil
}

func (t *checkinWakeupTool) InvokableRun(ctx context.Context, args string, _ ...tool.Option) (string, error) {
	var in struct {
		Note string `json:"note"`
	}
	_ = json.Unmarshal([]byte(args), &in)
	if err := t.checkiner.CheckInWakeup(ctx, t.task.WorkspaceID, t.task.WorkItemID, t.wakeupID, t.task.ID, in.Note); err != nil {
		if isValidation(err) {
			return "Check-in rejected: " + err.Error(), nil
		}
		return toolFail("checkin_wakeup", err)
	}
	return "Check-in recorded.", nil
}

// checkinWakeupTool 签到唤醒规则（有副作用；幂等键 = 每任务一次），
// 输出是文本（成功常量 / 拒绝原因 / toolFail 串），契约锚定字符串形状。
func (t *checkinWakeupTool) OutputSchema() json.RawMessage {
	return json.RawMessage(`{"type":"string"}`)
}

// checkinWakeupTool 有副作用，不并行。
func (t *checkinWakeupTool) ConcurrencySafe() bool { return false }
