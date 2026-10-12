package main

// 工作区删除 = 硬删（v0 裁决，FP-02 P1-1 / wave2-ws-delete）。ploykit 框架的
// DeleteWorkspaceCascade 只是裸 `DELETE FROM workspace`，完全依赖 DB 级联；而
// orkome 迁移里 52 处 `REFERENCES workspace(id)` 大多没有 ON DELETE CASCADE，
// 所以任何真实用过的工作区删除必撞 23503（首个撞墙表 work_item_status 的种子行）。
// 本文件把删除链补成三段式编排：
//
//  1. BeforeDelete（框架在 RunInTx 之前调用；返回错误 → 409 Conflict，不吞）：
//     独立小事务预清 agent_to_label / skill_to_label。这两张跨域 join 表没有
//     workspace_id 列、对 label 又无 ON DELETE CASCADE，若不先提交清掉，
//     CleanupWorkspaceTables 的 `DELETE FROM label` 必被 FK 挡死。
//
//  2. OnTeardown（框架事务内调用；注意框架会吞掉本钩子的错误并继续
//     DELETE workspace —— vendor workspace/app/service.go）：
//     a) 先取消活跃任务（既有行为，防引擎在删除途中再写行）；
//     b) workpgrepo.CleanupWorkspaceTables（FixE 交付的 work 域 21 表原语，
//        自带独立事务并提交，含静默 search-index 触发器的 teardown GUC）；
//     c) cleanupWorkspaceCrossDomainTables 在【框架事务】上按 FK 拓扑序清掉
//        其余全部产品域行（orchestration / conversation / channels /
//        automation / skills / 产品杂项面）。
//     框架随后在同一事务里 DELETE FROM workspace；framework 自有表
//     （member / workspace_invitation / workspace_role / workspace_share_link /
//     workspace_oidc_provider）与商用面（"order" / quota_* / schedule_plan /
//     subscription_event / webhook_*）自带 CASCADE，无需这里操心。
//
// 失败语义（"任一步失败 500 且工作区保留，不留半删状态"）：
//   - 预清失败 → 409，独立事务回滚，什么都没删；
//   - b 失败 → b 自身回滚（work 域行原样），c 未执行；框架吞错后继续
//     DELETE workspace，必被残留的 work_item_status 种子行 23503 挡下 →
//     框架事务整体回滚（c 已执行的部分一并回滚）→ 500，工作区全须全尾。
//     任何真实用过的工作区都有 work_item_status 种子，因此 b 失败不可能
//     滑成"清了一半却 204"的假成功；
//   - c 失败 → 框架事务回滚（跨域行原样），但 b 已独立提交 —— work 域行先没
//     了、工作区壳仍在。这是 FixE 原语自带事务的既定接缝：语句全部幂等，
//     排障后重试删除即自愈。

import (
	"context"
	"fmt"

	"github.com/haozing/ploykit/platform/pg"
	"github.com/haozing/ploykit/workspace"
	"github.com/jackc/pgx/v5"

	workpgrepo "orkome/internal/work/adapters/pgrepo"
)

// workspaceHardDeleteHooks 返回实现硬删裁决的工作区生命周期钩子。
// onMemberRemoved 是既有的成员移除吊销钩子（main._oldRevocation，原样透传）；
// cancelActiveTasks 是既有的活跃任务取消逻辑（main._oldTeardown 的函数值），
// 允许为 nil（集成测试用）。
func workspaceHardDeleteHooks(
	db *pg.DB,
	onMemberRemoved func(ctx context.Context, workspaceID, userID string) error,
	cancelActiveTasks func(ctx context.Context, workspaceID string) error,
) workspace.WorkspaceHooks {
	return workspace.WorkspaceHooks{
		OnMemberRemoved: onMemberRemoved,
		BeforeDelete: func(ctx context.Context, workspaceID string) error {
			return precleanWorkspaceLabelRefs(ctx, db, workspaceID)
		},
		OnTeardown: func(ctx context.Context, tx pgx.Tx, workspaceID string) error {
			if cancelActiveTasks != nil {
				if err := cancelActiveTasks(ctx, workspaceID); err != nil {
					return err
				}
			}
			if err := workpgrepo.CleanupWorkspaceTables(ctx, db, workspaceID); err != nil {
				return fmt.Errorf("cleanup work-domain tables: %w", err)
			}
			return cleanupWorkspaceCrossDomainTables(ctx, tx, workspaceID)
		},
	}
}

// precleanWorkspaceLabelRefs 在独立事务里清掉指向本工作区 label 的跨域 join 行。
// 必须先于 CleanupWorkspaceTables 提交（见文件头第 1 段）。
func precleanWorkspaceLabelRefs(ctx context.Context, db *pg.DB, workspaceID string) error {
	return db.Within(ctx, func(ctx context.Context) error {
		tx := db.Tx(ctx)
		for _, stmt := range workspaceLabelRefPreclean {
			if _, err := tx.Exec(ctx, stmt, workspaceID); err != nil {
				return fmt.Errorf("workspace delete preclean: %w", err)
			}
		}
		return nil
	})
}

// workspaceLabelRefPreclean：agent_to_label / skill_to_label 均无 workspace_id
// 列，且对 label 一侧无 ON DELETE CASCADE；按 id/label 双侧子选覆盖。
var workspaceLabelRefPreclean = []string{
	`DELETE FROM agent_to_label WHERE agent_id IN (SELECT id FROM agent WHERE workspace_id = $1)
	   OR label_id IN (SELECT id FROM label WHERE workspace_id = $1)`,
	`DELETE FROM skill_to_label WHERE skill_id IN (SELECT id FROM skill WHERE workspace_id = $1)
	   OR label_id IN (SELECT id FROM label WHERE workspace_id = $1)`,
}

// cleanupWorkspaceCrossDomainTables 在调用方给定的框架事务上清其余产品域行。
// 全部语句幂等（重复删除时 0 行受影响），顺序即 FK 拓扑序（子/引用方在前）。
func cleanupWorkspaceCrossDomainTables(ctx context.Context, tx pgx.Tx, workspaceID string) error {
	for _, s := range workspaceCrossDomainCleanup {
		if _, err := tx.Exec(ctx, s.sql, workspaceID); err != nil {
			return fmt.Errorf("workspace teardown %s: %w", s.table, err)
		}
	}
	return nil
}

type workspaceDeleteStmt struct {
	table string // 语句针对的业务表：错误定位与单测排序断言用
	sql   string // 恒以 workspaceID 为唯一 $1
}

// workspaceCrossDomainCleanup：FixE 的 CleanupWorkspaceTables（work 域 21 表）
// 之外、`REFERENCES workspace(id)` 无级联的全部业务表，加上引用这些表的
// 无 workspace_id 子表，按拓扑序排列。逐组依据（FK 现状见迁移 1001~1036
// 与库目录实测）：
//
//   - notification 对 recipient_agent_id 无级联 → 必须先于 agent（workspace 级
//     CASCADE 在 DELETE workspace 时才触发，救不了 agent 这一步）；
//   - autopilot.target_agent_id、squad.leader_agent_id、chat_session.agent_id、
//     chat_pinned_agent、task_usage.agent_id、agent_credential、agent_mcp_server、
//     squad_member、agent_skill、work_item_wakeup（FixE 已清）等对 agent 无级联
//     → 一律先于 agent；
//   - agent_task_queue 对 agent/squad 均无级联（work_item 绑定的任务已随 FixE
//     的 work_item 级联消失，这里兜住 chat 等无单任务）；
//   - autopilot 家族：run/quota_reservation/subscriber/collaborator/rule_version
//     对 autopilot 无级联（后三者有 CASCADE 的以显式语句固化意图），trigger 对
//     autopilot 无级联，automation_webhook_delivery 对 trigger 无级联；
//   - chat 家族对 chat_session 无级联；channel 家族对 channel_installation
//     无级联（其中几张连 FK 都没有，只能按 installation/session 子选清孤儿）；
//   - event_outbox 未发布行随工作区消亡，防止删除后 outbox relay 复活孤儿行；
//   - work_item_batch(_item) 正常已随 work_item 级联，显式语句固化意图。
//
// 有意不清（retention-by-design，见交接笔记）：audit_event（审计留存，含
// workspace.delete 这条记录本身）、analytics_event（可观测留存，无 FK 不会挡删除）。
var workspaceCrossDomainCleanup = []workspaceDeleteStmt{
	{"notification", `DELETE FROM notification WHERE workspace_id = $1`},

	// automation 域
	{"automation_webhook_delivery", `DELETE FROM automation_webhook_delivery WHERE workspace_id = $1`},
	{"autopilot_run", `DELETE FROM autopilot_run WHERE autopilot_id IN (SELECT id FROM autopilot WHERE workspace_id = $1)`},
	{"autopilot_quota_reservation", `DELETE FROM autopilot_quota_reservation WHERE autopilot_id IN (SELECT id FROM autopilot WHERE workspace_id = $1)`},
	{"autopilot_subscriber", `DELETE FROM autopilot_subscriber WHERE autopilot_id IN (SELECT id FROM autopilot WHERE workspace_id = $1)`},
	{"autopilot_collaborator", `DELETE FROM autopilot_collaborator WHERE autopilot_id IN (SELECT id FROM autopilot WHERE workspace_id = $1)`},
	{"autopilot_rule_version", `DELETE FROM autopilot_rule_version WHERE autopilot_id IN (SELECT id FROM autopilot WHERE workspace_id = $1)`},
	{"autopilot_quota_period", `DELETE FROM autopilot_quota_period WHERE workspace_id = $1`},
	{"autopilot_trigger", `DELETE FROM autopilot_trigger WHERE autopilot_id IN (SELECT id FROM autopilot WHERE workspace_id = $1)`},
	{"autopilot", `DELETE FROM autopilot WHERE workspace_id = $1`},
	{"system_wakeup_setting", `DELETE FROM system_wakeup_setting WHERE workspace_id = $1`},

	// orchestration 域：任务及子表先于 agent/squad
	{"task_usage", `DELETE FROM task_usage WHERE workspace_id = $1`},
	{"task_message", `DELETE FROM task_message WHERE workspace_id = $1`},
	{"agent_task_checkpoint", `DELETE FROM agent_task_checkpoint WHERE workspace_id = $1`},
	{"agent_task_queue", `DELETE FROM agent_task_queue WHERE workspace_id = $1`},

	// conversation 域：子表先于 chat_session；chat 两表先于 agent
	{"chat_cancel_pending", `DELETE FROM chat_cancel_pending WHERE workspace_id = $1`},
	{"chat_draft_restore", `DELETE FROM chat_draft_restore WHERE workspace_id = $1`},
	{"channel_chat_context_generation", `DELETE FROM channel_chat_context_generation WHERE chat_session_id IN (SELECT id FROM chat_session WHERE workspace_id = $1)`},
	{"channel_outbound_card_message", `DELETE FROM channel_outbound_card_message WHERE chat_session_id IN (SELECT id FROM chat_session WHERE workspace_id = $1)`},
	{"chat_message", `DELETE FROM chat_message WHERE workspace_id = $1`},
	{"channel_chat_session_binding", `DELETE FROM channel_chat_session_binding WHERE workspace_id = $1`},
	{"chat_pinned_agent", `DELETE FROM chat_pinned_agent WHERE workspace_id = $1`},
	{"chat_session", `DELETE FROM chat_session WHERE workspace_id = $1`},

	// channels 域：周边子表按 installation 子选清，安装行最后
	{"channel_inbound_audit", `DELETE FROM channel_inbound_audit WHERE channel_installation_id IN (SELECT id FROM channel_installation WHERE workspace_id = $1)`},
	{"channel_outbound_message", `DELETE FROM channel_outbound_message WHERE channel_installation_id IN (SELECT id FROM channel_installation WHERE workspace_id = $1)`},
	{"channel_reply_delivery", `DELETE FROM channel_reply_delivery WHERE channel_installation_id IN (SELECT id FROM channel_installation WHERE workspace_id = $1)`},
	{"channel_task_delivery", `DELETE FROM channel_task_delivery WHERE channel_installation_id IN (SELECT id FROM channel_installation WHERE workspace_id = $1)`},
	{"channel_media_pending_object", `DELETE FROM channel_media_pending_object WHERE workspace_id = $1`},
	{"channel_binding_token", `DELETE FROM channel_binding_token WHERE workspace_id = $1`},
	{"channel_inbound_message_dedup", `DELETE FROM channel_inbound_message_dedup WHERE workspace_id = $1`},
	{"channel_user_binding", `DELETE FROM channel_user_binding WHERE workspace_id = $1`},
	{"channel_installation", `DELETE FROM channel_installation WHERE workspace_id = $1`},

	// 产品杂项面（无跨域子行）
	{"quick_action", `DELETE FROM quick_action WHERE workspace_id = $1`},
	{"pinned_item", `DELETE FROM pinned_item WHERE workspace_id = $1`},
	{"ui_token", `DELETE FROM ui_token WHERE workspace_id = $1`},
	{"pack_ui_entry", `DELETE FROM pack_ui_entry WHERE workspace_id = $1`},
	{"pack_tool_schema", `DELETE FROM pack_tool_schema WHERE workspace_id = $1`},
	{"attachment", `DELETE FROM attachment WHERE workspace_id = $1`},
	{"notification_preference", `DELETE FROM notification_preference WHERE workspace_id = $1`},
	{"source_context_object_intent", `DELETE FROM source_context_object_intent WHERE workspace_id = $1`},
	{"task_usage_hourly", `DELETE FROM task_usage_hourly WHERE workspace_id = $1`},
	{"agent_builder_draft", `DELETE FROM agent_builder_draft WHERE workspace_id = $1`},
	{"agent_model_credential", `DELETE FROM agent_model_credential WHERE workspace_id = $1`},
	{"event_outbox", `DELETE FROM event_outbox WHERE workspace_id = $1`},

	// skills 域：join/file 显式清（固化意图；skill 的 CASCADE 本会带走），
	// skill 先于 agent（agent_skill 对 agent 无级联）
	{"agent_skill", `DELETE FROM agent_skill WHERE skill_id IN (SELECT id FROM skill WHERE workspace_id = $1)`},
	{"skill_file", `DELETE FROM skill_file WHERE skill_id IN (SELECT id FROM skill WHERE workspace_id = $1)`},
	{"skill_to_label", `DELETE FROM skill_to_label WHERE skill_id IN (SELECT id FROM skill WHERE workspace_id = $1)`},
	{"skill", `DELETE FROM skill WHERE workspace_id = $1`},

	// orchestration 主体最后：全部对 agent/squad 无级联
	{"agent_invocation_target", `DELETE FROM agent_invocation_target WHERE agent_id IN (SELECT id FROM agent WHERE workspace_id = $1)`},
	{"agent_mcp_server", `DELETE FROM agent_mcp_server WHERE agent_id IN (SELECT id FROM agent WHERE workspace_id = $1)`},
	{"squad_member", `DELETE FROM squad_member WHERE squad_id IN (SELECT id FROM squad WHERE workspace_id = $1)`},
	{"squad", `DELETE FROM squad WHERE workspace_id = $1`},
	{"agent_to_label", `DELETE FROM agent_to_label WHERE agent_id IN (SELECT id FROM agent WHERE workspace_id = $1)`}, // belt：BeforeDelete 已提交预清
	{"agent_credential", `DELETE FROM agent_credential WHERE workspace_id = $1`},
	{"agent", `DELETE FROM agent WHERE workspace_id = $1`},
	{"workspace_mcp_server", `DELETE FROM workspace_mcp_server WHERE workspace_id = $1`},

	// work 域批次兜底（正常已随 work_item 级联）
	{"work_item_batch_item", `DELETE FROM work_item_batch_item WHERE workspace_id = $1`},
	{"work_item_batch", `DELETE FROM work_item_batch WHERE workspace_id = $1`},
}
