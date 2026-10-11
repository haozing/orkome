# notification domain (product projection over ploykit notify)

orkome's in-app notification surface as a thin projection layer on top of the
framework notify domain (ploykit `notify`: bell repo + `app.NotifyService`,
framework migrations 009 `notification` / 018 `notification_preference`).

- **Responsibilities**: PhaseProject consumption (subscriber → mentioner → mute → dedup_key
  idempotency), task-completed/approval/eval-report direct writes, preference routing (three mute
  groups: source group / a task / a session), notification archival-sweep worker.
- **Spec mapping**: internal archive 05 §11 notifications and subscriptions (I9 ordering);
  06 notification/; internal archive 04 §2.8 inbox.

## Write path (decision)

All product writes go through **direct repo inserts that fill BOTH column sets**
(`pgrepo.InsertNotification`, `WriteNotice`, `WriteTaskCompleted`,
`WriteApprovalRequest`, `WriteEvalReport`):

- `user_id` — the framework read surface (ploykit `notifyhttp` list/badge/read)
  queries it; filled with the recipient user id.
- `workspace_id` / `recipient_user_id` / `work_item_id` (product migration 1005
  columns) — the product projection surface queries them.

Writing through the framework `NotifyService` was rejected: `app.NotifyInput`
carries no workspace / work-item / recipient-agent fields and the framework
bell repo Insert does not set `workspace_id` (NOT NULL since migration 1005),
so the framework write path cannot produce a valid product row. The framework
service is still composed in wiring (`wiringNotification`) but only serves the
framework HTTP routes (badge / read-all / per-type preference toggles); it
reads the same `notification` table the product projection writes, joined via
the shared `user_id`.

**Dedup mapping**: orkome dedup keys map one-to-one onto the framework
`dedup_key` column and are enforced by the framework's partial unique index
`uq_notification_dedup (user_id, dedup_key)`; inserts use `ON CONFLICT DO
NOTHING`. Product keys are already namespaced (`task_completed:<uid>:<wi>`,
`approval:<task>`, `eval:<task>`, `autopilot_fuse:<ws>:<name>`,
`<eventID>:<uid>` for the projection) and are stored raw. (The framework
`Notify()` prefixes its `DedupKey` with `Type+":"` — that convention applies
only to framework-path writes, which orkome does not make.)

**Preferences** (migration 1034): mute groups live in the framework
`notification_preference` table on rows pinned to `notification_type='mute'`
(`workspace_id` + `muted_types` + `muted_work_item_ids` columns, partial
unique index `uq_notification_preference_mute`). Framework per-type toggle
rows keep the `(user_id, notification_type)` PK, so both surfaces share one
table without the product touching the framework PK or its upsert conflict
target. Session mutes are encoded inside `muted_types` as `session:<id>`
entries (see `splitMutedTypes`).

## HTTP surface split

- **product Mount** (this repo, `adapters/http.Mount`): workspace-scoped cursor
  inbox `GET /api/notifications` (+ `archived`/`summary`/`facets`), `unread-count`,
  `mark-all-read`, `archive-all`, `archive-all-read`, `{id}/read|unread|archive|unarchive`,
  `GET/PATCH/PUT /api/notification-preferences` (mute groups). Kept versions of
  `GET /api/notifications`, `{id}/read`, `{id}/archive` and `GET prefs`
  **supersede** the framework routes (framework shapes lack workspace scoping,
  cursors, `work_item_id` and the WS frame push) — the composition root filters
  those four patterns out of the framework Mount.
- **framework notifyhttp.Mount** (ploykit): `GET /api/notifications/badge`,
  `POST /api/notifications/read-all`, `PUT /api/notification-preferences/{type}`
  (registered through the filtered router in `wiring_notification.go`).
  Overlaps kept on purpose: badge (user-scoped count) vs unread-count
  (workspace-scoped), read-all (user-scoped) vs mark-all-read (workspace-scoped
  + excludes archived + WS frames).

- **Ports**: NotificationWriter/EvalReporter (consumed by orchestration —
  implementations are the repo's `WriteTaskCompleted`/`WriteEvalReport` methods;
  the port interfaces live in orchestration/app and are unchanged); archival
  sweeper ArchiveSweeper (workers, wired by the cmd registry — the framework
  has no notification retention, only webhook_delivery retention, so the
  product sweeper stays).
- **domain**: the three mute-group predicates (Muted, shared by projection and direct writes) +
  truncation caps (TruncateRunes: title 200 / summary 2000 / body 4000, rune-safe).
- **Tests**: I9, dedup idempotency, preference reconciliation, three mute groups, direct-write
  filtering, truncation caps, archival sweep.
