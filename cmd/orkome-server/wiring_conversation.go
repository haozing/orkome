package main

import (
	"context"
	"log/slog"

	"github.com/haozing/ploykit/platform/pg"
	convhttp "orkome/internal/conversation/adapters/http"
	convpgrepo "orkome/internal/conversation/adapters/pgrepo"
	convapp "orkome/internal/conversation/app"
	orchapp "orkome/internal/orchestration/app"
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
	quick.SetEnqueueWorkItemTask(func(ctx context.Context, ws, wi, agentID, userID, prompt string) (string, error) {
		t, _, err := enq.EnqueueQuickAction(ctx, ws, wi, agentID, userID, prompt)
		if err != nil {
			return "", err
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
