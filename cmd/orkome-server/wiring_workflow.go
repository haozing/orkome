package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"

	"github.com/haozing/ploykit/platform/pg"
	"github.com/jackc/pgx/v5/pgconn"
	"orkome/internal/contract/dispatchreason"
	"orkome/internal/contract/events"
	"orkome/internal/contract/modelmeta"
	notifpgrepo "orkome/internal/notification/adapters/pgrepo"
	orchdomain "orkome/internal/orchestration/domain"
	orchpgrepo "orkome/internal/orchestration/adapters/pgrepo"
	orchapp "orkome/internal/orchestration/app"
	"orkome/internal/platform/eventbus"
	workapp "orkome/internal/work/app"
)

// wiringWorkflow —— Project Workflow 推进引擎的装配面（W2；设计 §2/§3/§4）。
//
// 职责（全部在 cmd 组合根，iron law 合法层）：
//  1. eventbus 双门铃订阅：work_item.updated / work_item:updated_by_agent /
//     task.completed / task.failed / task.cancelled（§2.2——status 达出口态与
//     任务终态哪个先到都能触发同一套读库裁决）；
//  2. 派发矩阵注入（契约 §5）：agent→EnqueueAssignment（already_active→
//     MergeIntoPending + note=coalesced；blocked/model_not_configured→通知
//     created_by 原地等待）；squad→EnqueueSquadTask；none→只通知不派发；
//  3. 跨域存在性检查谓词注入（设计 §4.3 条件 2 的 agent_task_queue 读）；
//  4. 防失控联动（§5.2）：ErrAdvanceRunaway → 停用 workflow + 通知创建者。
//
// 注意：main.go 的 assignee 派发器与 resetIdleWorkItem 零改动共存（设计 §3.1
// #2/#3 已论证：EnqueueAssignment 的 pending fence + 派发器只认 agent 型投影、
// squad 型不触发；reset 两序皆安全）。
func wiringWorkflow(log *slog.Logger, db *pg.DB, bus *eventbus.Bus, wfSvc *workapp.WorkflowService, enq *orchapp.EnqueueService) {
	orepo := orchpgrepo.New(db)
	notifRepo := notifpgrepo.New(db)

	// —— 谓词：阶段任务终态存在性（设计 §4.3 条件 2；内联 SQL 在 cmd 层，
	// agent_task_queue 归 orchestration 域，不受 work 域 allowlist 约束）。
	workapp.SetStageTaskTerminal(func(ctx context.Context, ws, workItemID, stageID string) (bool, error) {
		var exists bool
		err := db.Tx(ctx).QueryRow(ctx, `
SELECT EXISTS(
  SELECT 1 FROM agent_task_queue
  WHERE work_item_id=$1::uuid AND context->>'stage_id'=$2
    AND status IN ('completed','failed','cancelled')
)`, workItemID, stageID).Scan(&exists)
		return exists, err
	})

	// —— 通知适配器：流程创建者（originator）收 blocked/防失控/人工环节通知。
	notifyCreator := func(ctx context.Context, ws, userID, title, body, dedup string) {
		if userID == "" {
			return
		}
		if err := notifRepo.WriteNotice(ctx, ws, userID, "workflow_stage", title, body, dedup); err != nil {
			log.Warn("workflow notice failed", "err", err)
		}
	}

	// notifyBlocked §4.4 降级：不派发、流程原地等待（缺人问人，不静默改道）。
	notifyBlocked := func(ctx context.Context, ws, workItemID string, st workapp.StageExecInfo, reason string) error {
		notifyCreator(ctx, ws, st.OriginatorUserID,
			"流程阶段等待执行者",
			"阶段「"+st.StageName+"」的执行者暂不可用（"+reason+"），工作项停在当前阶段；恢复执行者或调整流程配置后重试。",
			"wf_dispatch_blocked:"+ws+":"+workItemID+":"+st.StageID)
		return nil
	}

	// workflowModelOK 复刻 EnqueueAssignment 的模型配置校验（internal agent 必须
	// 有有效 runtime_config；external runtime 无此要求）。
	workflowModelOK := func(agent orchapp.Agent) bool {
		if agent.RuntimeKind == "external" {
			return true
		}
		b, err := json.Marshal(agent.RuntimeConfig)
		if err != nil {
			return false
		}
		var rc modelmeta.RuntimeConfig
		if err := json.Unmarshal(b, &rc); err != nil {
			return false
		}
		return modelmeta.ValidConfig(rc)
	}

	// workItemTitle 只读工单标题（context 注入用；存在性已在 service 层保证）。
	workItemTitle := func(ctx context.Context, ws, workItemID string) string {
		var title string
		_ = db.Tx(ctx).QueryRow(ctx,
			`SELECT title FROM work_item WHERE workspace_id=$1::uuid AND id=$2::uuid`,
			ws, workItemID).Scan(&title)
		return title
	}

	// —— 派发矩阵（契约 §5 逐条）。agent 分支不能走 EnqueueAssignment：它的
	// context 硬编码 trigger=assignment（service.go:598），§5 的 stage 字段进不去
	// ——而 §4.3 存在性谓词按 context->>'stage_id' 匹配，丢了 stage_id 流程就
	// 永远推不动（W3 前端联调实证的装配漏电）。改为 wiring 层直插 InsertTask，
	// 复刻 EnqueueAssignment 的四步语义（W3 前端联调实证后修复；模式先例 =
	// wiring_automation.go 的 wakeupEnqueuer）：
	//   ① GetAgent 校验（archived→blocked）②模型配置校验（缺→model_not_configured）
	//   ③ CountPendingFence（→already_active→MergeIntoPending）④InsertTask 带 §5 context。
	// originator/accountable=workflow.created_by（设计 §3：流程必须有主，不走
	// ResolveAttribution 的 owner 回退——创建流程的必是人）。
	workapp.SetDispatcher(func(ctx context.Context, ws, workItemID string, st workapp.StageExecInfo) error {
		switch st.ExecutorType {
		case "agent":
			agent, ok, err := orepo.GetAgent(ctx, ws, st.ExecutorID)
			if err != nil {
				return err
			}
			if !ok || agent.Archived {
				return notifyBlocked(ctx, ws, workItemID, st, dispatchreason.TargetUnavailable)
			}
			if !workflowModelOK(agent) {
				return notifyBlocked(ctx, ws, workItemID, st, dispatchreason.ModelNotConfigured)
			}
			if n, _ := orepo.CountPendingFence(ctx, workItemID, st.ExecutorID); n > 0 {
				merged, err := orepo.MergeIntoPending(ctx, ws, workItemID, st.ExecutorID, "")
				if err != nil {
					return err
				}
				if merged {
					if err := wfSvc.AppendTransitionNote(ctx, ws, workItemID, "coalesced"); err != nil {
						log.Warn("workflow coalesced note failed", "err", err)
					}
				}
				return nil
			}
			ctxMap := map[string]any{
				"trigger": "workflow_stage", "actor": st.OriginatorUserID,
				"workflow_id": st.WorkflowID, "stage_id": st.StageID,
				"stage_name": st.StageName, "stage_position": st.StagePosition,
				"stage_exit_status": st.ExitStatusKey, "instruction": st.Instruction,
			}
			if title := workItemTitle(ctx, ws, workItemID); title != "" {
				ctxMap["work_item_title"] = title
			}
			sessionRefIn, _, _ := orepo.GetLastSessionRef(ctx, workItemID, st.ExecutorID, orchapp.CurrentEngineTag())
			if _, err := orepo.InsertTask(ctx, orchapp.Task{
				WorkspaceID: ws, AgentID: st.ExecutorID, WorkItemID: workItemID,
				Status: "queued", MaxAttempts: 2, Context: ctxMap,
				OriginatorUserID: st.OriginatorUserID, AccountableUserID: st.OriginatorUserID,
				OriginatorSource: orchdomain.SrcTriggerOwner, TriggerEvidenceKind: "workflow_stage",
				SessionRefIn: sessionRefIn,
			}); err != nil {
				var pgErr *pgconn.PgError
				if errors.As(err, &pgErr) && pgErr.Code == "23505" {
					// 撞 uq_one_pending（§3.1 #1 DB 仲裁）：并发窗口下 fence 检查
					// 与插入之间别人入了队——按 coalesced 语义收敛，不算失败。
					if err := wfSvc.AppendTransitionNote(ctx, ws, workItemID, "coalesced"); err != nil {
						log.Warn("workflow coalesced note failed", "err", err)
					}
					return nil
				}
				return err
			}
			return nil
		case "squad":
			_, err := enq.EnqueueSquadTask(ctx, ws, workItemID, st.ExecutorID, st.OriginatorUserID)
			return err
		case "none":
			// 人工环节：不派发，通知创建者（订阅者经 work_item.updated 投影已收）。
			notifyCreator(ctx, ws, st.OriginatorUserID,
				"流程阶段等待人工处理",
				"工作项进入人工阶段「"+st.StageName+"」："+st.Instruction,
				"wf_stage_none:"+ws+":"+workItemID+":"+st.StageID)
			return nil
		}
		return nil
	})

	// —— 双门铃（§2.2）：五个事件同一套读库裁决；回调只取 work_item_id 然后
	// TryAdvance。payload 可能 nil（relay 解码缺口之外的形状差异），取不到 ID
	// 直接返回。TryAdvance 错误 log 不上抛（eventbus.run 已有 panic recover；
	// 数据态错误不能炸 relay 线程）。
	advance := func(ctx context.Context, e events.Envelope, wiID string) {
		if wiID == "" || e.WorkspaceID == "" {
			return
		}
		st, err := wfSvc.TryAdvance(ctx, e.WorkspaceID, wiID)
		switch {
		case err == nil:
			_ = st // nil=未推进（静默）；非 nil=已推进（投影事件已由 service 发出）
		case errors.Is(err, workapp.ErrAdvanceRunaway):
			// 防失控（§5.2 service 侧回执）：停用 workflow + 通知创建者。
			paused := wfSvc.PauseWorkflowOnRunaway(ctx, e.WorkspaceID, wiID, st, err)
			if paused != nil {
				notifyCreator(ctx, e.WorkspaceID, paused.CreatedBy,
					"流程已自动暂停（防失控熔断）",
					fmt.Sprintf("工作项推进循环异常（%v），流程已停用；请检查阶段配置后重新启用。", err),
					"wf_runaway:"+e.WorkspaceID+":"+paused.ID)
			}
		default:
			log.Warn("workflow advance failed", "event", e.Event, "work_item", wiID, "err", err)
		}
	}

	bus.On(eventbus.PhaseProject, func(ctx context.Context, e events.Envelope) {
		switch e.Event {
		case events.WorkItemCreated:
			// auto 模式自动进入（设计 §4.6）：建单挂项目即 Start（幂等静默：
			// 非流程单/无定义/manual/未启用都不动）。挂载项目的 PATCH 场景
			// 由 WorkItemUpdated 分支兜底——updated 路径先跑 MaybeAutoStart
			// （无 state 且符合 auto 条件才进），再走常规推进裁决。
			p, ok := e.Payload.(events.WorkItemPayload)
			if !ok {
				return
			}
			if err := wfSvc.MaybeAutoStart(ctx, e.WorkspaceID, p.ID, e.ActorUserID); err != nil {
				log.Warn("workflow auto start failed", "work_item", p.ID, "err", err)
			}
		case events.WorkItemUpdated, events.WorkItemUpdatedByAgent:
			p, ok := e.Payload.(events.WorkItemPayload)
			if !ok {
				return
			}
			if e.Event == events.WorkItemUpdated {
				// 挂载项目的 PATCH（project_id 从空到有）：MaybeAutoStart 幂等，
				// 已在途/已完成/manual 都是无操作。
				if err := wfSvc.MaybeAutoStart(ctx, e.WorkspaceID, p.ID, e.ActorUserID); err != nil {
					log.Warn("workflow auto start (attach) failed", "work_item", p.ID, "err", err)
				}
			}
			advance(ctx, e, p.ID)
		case events.TaskCompleted, events.TaskFailed, events.TaskCancelled:
			p, ok := e.Payload.(orchapp.TaskPayload)
			if !ok || p.WorkItemID == "" {
				return // payload nil / chat 等无工单任务
			}
			advance(ctx, e, p.WorkItemID)
		}
	})
}
