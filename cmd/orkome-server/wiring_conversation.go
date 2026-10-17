package main

import (
	"context"
	"log/slog"

	"github.com/haozing/ploykit/platform/pg"
	convhttp "orkome/internal/conversation/adapters/http"
	convpgrepo "orkome/internal/conversation/adapters/pgrepo"
	convapp "orkome/internal/conversation/app"
	"orkome/internal/contract/apierr"
	orchapp "orkome/internal/orchestration/app"
	orchpgrepo "orkome/internal/orchestration/adapters/pgrepo"
	workpgrepo "orkome/internal/work/adapters/pgrepo"
)

// wiringConversation 装配 conversation 域的 HTTP 面。
//
// chat（直聊会话）已退役（2026-10-15 裁决：对话长在工单上——评论 @agent
// 派活、supplement 纠正、汇报 tab 看过程，不养独立聊天室）：ChatService
// 不再构造，convhttp.Mount 的 nil 守卫令 /api/chat/* 全部不挂载；服务与
// 表结构留仓沉睡（同工人模型先例），e2e harness（golden_test.go /
// scenario_s5）仍自行挂载以保引擎侧覆盖。orchestration 结算侧的
// chat:done 发射口保留（存量在途任务收尾用，见 main.go）。
//
// quick-actions 属同域独立服务：后端全绿、Web 产品面待消费，照常装配。
func wiringConversation(log *slog.Logger, db *pg.DB,
	enq *orchapp.EnqueueService,
	memberCheck func(ctx context.Context, workspaceID, userID string) (bool, error),
) convhttp.Deps {
	convRepo := convpgrepo.New(db)
	quick := convapp.NewQuickActionService(convRepo)

	wRepo := &workpgrepo.Repo{DB: db}
	orchRepo := orchpgrepo.New(db)
	quick.SetEnqueueWorkItemTask(func(ctx context.Context, ws, wi, agentID, userID, prompt string) (string, error) {
		t, reason, err := enq.EnqueueQuickAction(ctx, ws, wi, agentID, userID, prompt)
		if err != nil {
			return "", err
		}
		// 未入队也返回 nil error（Blocked/AlreadyActive 是决策不是故障），
		// 此时 t 为 nil——必须落成 409 语义而不是裸解引用（生产实证：
		// 指派任务在途时点 run → nil panic → 500，任务却已由指派链创建）。
		// P0-2：409 响应带上当前活跃任务（GetActiveTask，先例
		// handlers_taskview.go），前端可据 active_task_id 直达在途汇报页。
		if t == nil {
			details := []apierr.FieldError{{Field: "reason", Problem: reason}}
			if active, found, qerr := orchRepo.GetActiveTask(ctx, ws, wi); qerr == nil && found {
				details = append([]apierr.FieldError{{Field: "active_task_id", Problem: active.ID}}, details...)
			}
			e := apierr.New(apierr.ActiveDuplicate,
				"work item already has an active task for this agent ("+reason+")")
			e.Details = details
			return "", e
		}
		return t.ID, nil
	})
	quick.SetResolveTargetAgent(func(ctx context.Context, ws, wi string) (string, error) {
		item, _, err := wRepo.GetWorkItem(ctx, ws, wi)
		if err != nil {
			return "", err
		}
		if item.AssigneeType != "agent" {
			return "", nil
		}
		return item.AssigneeID, nil
	})

	return convhttp.Deps{
		QuickActions: quick,
		MemberCheck:  memberCheck,
	}
}
