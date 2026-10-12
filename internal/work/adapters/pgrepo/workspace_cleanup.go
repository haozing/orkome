package pgrepo

import (
	"context"

	"github.com/haozing/ploykit/platform/pg"
)

// workspaceCleanupOrder deletes every work-domain business table scoped by
// workspace_id, in FK-safe order (children before parents, join tables before
// both sides). It exists so a workspace teardown can remove the domain's rows
// before DELETE workspace — the workspace FKs have no ON DELETE CASCADE, so
// even an "empty" workspace (its 7 seeded work_item_status rows) dies with
// 23503 work_item_status_workspace_id_fkey today (audit FP-11 §五#10 / FP-02
// P1-1). This function only ships the delete primitive; wiring it into the
// workspace delete path belongs to the workspace-domain fix (FixD).
//
// Cross-domain notes:
//   - work_item children in other domains are handled by ON DELETE CASCADE
//     (agent_task_queue, work_item_batch, work_item_wakeup: 1029/1030) or
//     ON DELETE SET NULL (autopilot_run, self parent/duplicate refs).
//   - work_item_wakeup_receipt is the one cross-domain grandchild WITHOUT a
//     cascade (1006), so it is deleted explicitly before work_item.
//   - comment_reaction / work_item_reaction / work_item_subscriber /
//     work_item_to_label / activity_log also cascade (1029) but are deleted
//     explicitly so the order documents intent and survives future cascade
//     changes.
//   - search_index_change / client_usage_daily are observability tables owned
//     by this domain (observability.go); the DELETE triggers on
//     work_item/comment/project are silenced for this transaction via the
//     schema's `orkome.workspace_teardown` GUC (migration 1020) and the queue
//     rows are removed explicitly.
//
// Runs in a single transaction: all rows go or none does. The transaction sets
// the schema's designed teardown flag (`orkome.workspace_teardown`, migration
// 1020) so the search-index AFTER DELETE triggers on work_item/comment/project
// do not re-enqueue rows into search_index_change mid-cleanup.
func CleanupWorkspaceTables(ctx context.Context, db *pg.DB, workspaceID string) error {
	return db.Within(ctx, func(ctx context.Context) error {
		tx := db.Tx(ctx)
		if _, err := tx.Exec(ctx, `SET LOCAL orkome.workspace_teardown = 'on'`); err != nil {
			return err
		}
		for _, stmt := range workspaceCleanupStatements {
			if _, err := tx.Exec(ctx, stmt, workspaceID); err != nil {
				return err
			}
		}
		return nil
	})
}

// workspaceCleanupStatements: each takes the workspace id as its single $1.
var workspaceCleanupStatements = []string{	// grandchildren & join tables that reference work domain parents
	`DELETE FROM work_item_wakeup_receipt WHERE workspace_id = $1`,
	`DELETE FROM work_item_wakeup WHERE workspace_id = $1`,
	`DELETE FROM comment_reaction WHERE comment_id IN (SELECT id FROM comment WHERE workspace_id = $1)`,
	`DELETE FROM comment WHERE workspace_id = $1`,
	`DELETE FROM activity_log WHERE workspace_id = $1`,
	`DELETE FROM work_item_reaction WHERE work_item_id IN (SELECT id FROM work_item WHERE workspace_id = $1)`,
	`DELETE FROM work_item_subscriber WHERE work_item_id IN (SELECT id FROM work_item WHERE workspace_id = $1)`,
	`DELETE FROM work_item_to_label WHERE work_item_id IN (SELECT id FROM work_item WHERE workspace_id = $1)
	   OR label_id IN (SELECT id FROM label WHERE workspace_id = $1)`,
	`DELETE FROM work_item_dependency WHERE workspace_id = $1`,
	`DELETE FROM work_item_child_event WHERE workspace_id = $1`,

	// the item itself (self-FKs parent/duplicate are ON DELETE SET NULL)
	`DELETE FROM work_item WHERE workspace_id = $1`,

	// catalog & user-private faces
	`DELETE FROM work_item_view_preference WHERE workspace_id = $1`,
	`DELETE FROM work_item_view WHERE workspace_id = $1`,
	`DELETE FROM work_item_source_context WHERE workspace_id = $1`,
	`DELETE FROM work_item_property_def WHERE workspace_id = $1`,
	`DELETE FROM label WHERE workspace_id = $1`,

	// projects
	`DELETE FROM project_resource WHERE project_id IN (SELECT id FROM project WHERE workspace_id = $1)`,
	`DELETE FROM project WHERE workspace_id = $1`,

	// observability faces owned by this domain (search-index queue rows would
	// otherwise block the workspace delete; the teardown GUC set above keeps
	// the DELETE triggers on work_item/comment/project from re-enqueueing)
	`DELETE FROM search_index_change WHERE workspace_id = $1`,
	`DELETE FROM client_usage_daily WHERE workspace_id = $1`,

	// status catalog last: its seed rows are what breaks bare workspace deletes
	`DELETE FROM work_item_status WHERE workspace_id = $1`,
}
