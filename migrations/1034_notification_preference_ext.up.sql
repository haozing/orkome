-- orkome mute-group preferences on the framework notification_preference table
-- (framework migration 018). The framework surface keeps its per-type rows
-- ((user_id, notification_type) PK, email/in-app toggles); orkome stores
-- workspace-scoped mute groups in rows pinned to notification_type='mute'.
-- The partial unique index keeps both row shapes on one table without
-- touching the framework PK or its ON CONFLICT (user_id, notification_type)
-- upsert path.

ALTER TABLE notification_preference
  ADD COLUMN IF NOT EXISTS workspace_id uuid REFERENCES workspace(id),
  ADD COLUMN IF NOT EXISTS muted_types text[] NOT NULL DEFAULT '{}',
  ADD COLUMN IF NOT EXISTS muted_work_item_ids text[] NOT NULL DEFAULT '{}';

CREATE UNIQUE INDEX IF NOT EXISTS uq_notification_preference_mute
  ON notification_preference (user_id, workspace_id) WHERE notification_type = 'mute';
