# notification domain

Notification projection and preferences.

- **Responsibilities**: PhaseProject consumption (subscriber → mentioner → mute → dedup_key
  idempotency), task-completed/approval/eval-report direct writes into notification (all entries
  pass the mute filter uniformly, internal archive 05 §11 MUST), preference routing (three mute
  groups: source group / a task / a session), notification archival-sweep worker.
- **Spec mapping**: internal archive 05 §11 notifications and subscriptions (I9 ordering);
  06 notification/; internal archive 04 §2.8 inbox.
- **domain**: the three mute-group predicates (Muted, shared by projection and direct writes) +
  truncation caps (TruncateRunes: title 200 / summary 2000 / body 4000, rune-safe).
- **Ports**: NotificationWriter/EvalReporter (consumed by orchestration); archival sweeper
  ArchiveSweeper (workers, wired by the cmd registry).
- **Tests**: I9, dedup idempotency, preference reconciliation, three mute groups, direct-write
  filtering, truncation caps, archival sweep.
