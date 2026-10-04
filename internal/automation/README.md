# automation domain

Time- and event-driven automation: wakeup rules (all four kinds), autopilots (cron/webhook/manual
triggering creating work items or dispatching a leader), and the inbound webhook pipeline.

## Structure and exported face

| Layer | Contents |
|---|---|
| domain/rules.go | Pure decisions: `PlanLate` (late >5min voids), `IntervalNext`, `FieldPredicate.Eval` (conditional-wakeup predicate), `Interpolate` ({{date}} interpolation), `MintWebhookToken`/`MintSigningSecret` (crypto/rand), `ValidateSigningSecret` (32..128B), `SecretFingerprint` (sha256 first-8-hex masking), `QuotaWindow` (UTC calendar day); vocabulary constants (run states/trigger and wakeup kinds); dispatch-discipline constants (2s budget/50ms lock_timeout) and sentinels (`ErrQuotaExceeded`/`ErrLockTimeout`/`ErrWebhookTokenCollision`) |
| domain/wakeup.go | M7.5 pure decisions: `EventFilter.Matches` (event-type subscription + agent/task/actor filters, empty subscription fail-closed), `ExtractEventFacts` (payload fact extraction), `ChildrenDone` (children_done stage aggregation: `stage:N`/`all` fingerprints, ported from the old system's stageProgress), `EdgeTriggered`/`CondFingerprint` (fire only on turning-true fingerprint edges), `CapEvidence` (40KB cap), `ValidateInstruction`(≤4000)/`ValidateCheckinNote`(≤500), `ReceiptRetryWindow` (poison-receipt drop window) |
| app/service.go | Models and ports: Wakeup (a subset of internal archive 03 §3.5's full columns: event_types/filter quadruple/system_rule/expires_at/paused_reason), `DueWakeup` (four-way scan output), Receipt, `FactsReader` (**M7.5 adds two methods**: `ChildStageFacts`/`WakeupTargetAgent`—provided by work, wiring by the composition root), `SubscriberAwareCreator` (D15 subscriber-carrying extension port, backward-compatible optional), the full Repo port set |
| app/service_wakeup.go | `WakeupService`: Create (time/event/condition), Get/Update (revision CAS)/Delete/EditInstruction/CheckIn (≤500-rune receipt, idempotency key = task)/Trigger (wake now), ListWorkspace/ListPaused/Summaries; `RecordEvent` (event-type receipt-capture entry—wired via eventbus PhaseCascade by the composition root); `TickOnce` four-way scan dispatch; the system face (`EnsureChildDoneRule` baselining/`UpdateSystem`/workspace default Get/Set); `NotifyBatchDone`/`NotifyChildrenChanged` immediate-evaluation ports; the `fireOne` dispatch transaction (lock order: **owner fence lock → lock task → lock wakeup** → revision gate → enqueue; 2s budget/50ms lock_timeout/CAS miss rolls back wholesale; system rules resolve the target agent via a port when firing) |
| app/service_autopilot.go | `AutopilotService`: Create (mints credentials even without cron; **from M9-B1 the internal archive 05 §9 rule-version audit row lands in the same create transaction**—`InsertRuleVersion`, active version = first created_at DESC row, timestamp ruling in migration 024 §C)/CreateTrigger (webhook kind mints token+secret at creation)/UpdateTrigger (nil = unchanged, cron changes recompute next_run)/DeleteTrigger/CronPreview (timezone next run, ruling: iterate cronx.Next—schedx has past-window enumeration semantics, see method note)/ListRuns/GetRun/StartRun quota reserve-commit + single-transaction dispatch (D15: when the creator implements SubscriberAwareCreator, subscribers pass in with the create in the same transaction)/BackfillFromTask; **M9-B1 collaborator CRUD** (List/Add/Remove, internal archive 04 §2.7—no role column, same-shape ruling as the subscriber table 021); `SetUow` |
| app/service_webhook.go | `WebhookService`: Ingest signature verification ±5min **fail-closed empty-secret 403**/dedup/ConsumeOnce (SKIP LOCKED)/SetSigningSecret/RotateWebhookToken/ListDeliveries/GetDelivery/ReplayDelivery (reset to pending) |
| adapters/pgrepo | `dueScanSQL` four-way UNION ALL (time due/expires due/condition candidates/unprocessed receipts, single-statement LIMIT); receipts ((wakeup,revision,event_key) ON CONFLICT DO NOTHING); `LockTaskOwner` (advisory fence + work_item row lock—**the cross-domain read-side lock is registered in sql_allowlist** AUTOMATION-DISPATCH-LOCK-TASK); system rules (partial-unique-index idempotent ensure); trigger CRUD/runs/deliveries/subscriber read faces |
| adapters/http | Public webhook three-tier rate limiting + the full `/work-items/{id}/wakeups` suite (CRUD/enable/disable/instruction/checkin/trigger/tasks) + workspace views (`/work-item-wakeups`/`-paused`/`-summaries`) + the `/system-wakeups` group + the autopilot group (cron-preview/triggers CRUD/runs + **M9-B1 `/autopilots/{id}/collaborators` GET/POST/DELETE**) + `/deliveries` (incl. replay). `Deps.WakeupTasks` (linked-task read port, nil = empty list). **rule_version is write-only**: internal archive 04 §2.7 lists no versions endpoint (ruling note in handlers_collaborators.go) |
| adapters/workers | The `TickWorker` trio: WakeupTick / AutopilotCron / WebhookDelivery (1s polling) |

**Ports (declared by consumers)**: `TaskEnqueuer` (enqueue, orchestration),
`WorkItemCreator`+`SubscriberAwareCreator` (work-item creation/subscriber carrying, work),
`FactsReader` (work-item facts + children_done sub-item facts + target agent resolution, work),
`Repo`, `UoW` (=db.Within), `Deps.WakeupTasks` (wakeup-linked tasks, orchestration).

## M7.5 archive alignment (internal archive 05 §8 item by item)

| Archive clause | Implementation |
|---|---|
| Four kinds | time/event/condition/system all creatable (system only via the `EnsureChildDoneRule` platform entry) |
| Receipts: transactional capture for event kind | `RecordEvent` + `InsertReceipt` idempotency key (wakeup,revision,event_key) (deviation below) |
| Due-scan four-way UNION LIMIT 100 | `dueScanSQL` single statement; path 2 voids (enabled=false+timed_out+paused_reason='expired') |
| Dispatch transaction lock order | Owner fence lock (advisory) → work_item row → wakeup row → revision CAS → enqueue (fireOneSteps note) |
| children_done | Aggregation pure function + fingerprint-edge triggering (stage:N→all each fires its own edge); the target agent resolves per parent assignment at fire time |
| check-in ≤500 runes | `CheckIn` receipt (idempotency key = task); the fallback-comment suppression consumer sits at orchestration settlement (wiring by the composition root) |
| Evidence 40KB | `CapEvidence` (instruction + previous task + event keys merged then truncated) |
| Rate limiting and fusing | max_fires/fire_count present; paused_reason persisted (burst-pause writer belongs to loop protection, wired in a later milestone) |

## Known trade-offs (deliberate, not oversights)

- **Receipt-capture deviation (D16⑤)**: the archive's ideal is transactional capture inside the
  business write; the current event bus is a post-commit synchronous cascade (internal archive
  02 §6/I4), and `RecordEvent` consumes at PhaseCascade—the crash window (business committed,
  receipt not yet inserted) can lose a receipt. Convergence path: once inline capture exists in
  the event-source write path (an outbox-consumer shape or write-path port injection), this entry
  retires into a backstop. The idempotency key guarantees at-least-once semantics without double
  dispatch;
- **Receipt dispatch retry window (10min)**: under at-least-once semantics, a poison receipt
  (e.g. target agent permanently unavailable) would occupy the scan face—dropping past-window is
  an anti-starvation trade-off (`ReceiptRetryWindow`); old receipts with mismatched revision
  (rule already changed) are dropped immediately (same semantics as §2.3 ④: old facts must not
  dispatch under new rules);
- **children_done driving**: polling backstop (scan path 3, 1s) + `NotifyChildrenChanged`
  immediate acceleration (called by the work domain after sub-item changes); the archive
  specifies no work_item_child_event ledger consumption (old-system shape)—implemented as
  facts-port aggregation (ruling: ports over cross-domain direct queries, no sql_allowlist
  registration needed); leader resolution for squad assignments is carried inside the
  `WakeupTargetAgent` implementation in wiring;
- **`system_wakeup_setting` table (021 §B)**: internal archive 04 §2.7 requires workspace-default
  read/write but 03 automation(11) gave no storage slot—self-built in-domain (table_owners
  registers all 12 tables); convergence path: merge once identity opens workspace.settings;
- **Cross-domain read-side lock**: `LockTaskOwner`'s work_item-row FOR UPDATE touches a work-domain
  table—registered in arch/sql_allowlist.yml `AUTOMATION-DISPATCH-LOCK-TASK` (reroute condition:
  the work domain supplies a LockWorkItem port);
- **Homegrown TickWorker substrate instead of River**: internal archive 02 §8 decided River
  (ADR 0001);
- **autopilot**: quota = per-autopilot UTC calendar-day window (reserve-commit implemented);
  quota_period (workspace dimension) unwired; the autopilot body's GET/PATCH/DELETE not mounted
  (M9-B1 mounted collaborator CRUD and the creation-path version-audit row; when PATCH lands it
  must add the autopilot_rule_version row in the same transaction—the substantive-publish
  "checklist: trigger conditions / execution targets / instruction changes"); subscriber CRUD not
  mounted (this milestone's scope: subscriber read face + creation carrying); the trigger
  credential endpoint does not restrict kind=webhook (this domain's only tokenized trigger is the
  webhook entry);
- **`Deps.WakeupTasks` nil degradation**: when the wakeup-linked-task list port is unwired it
  returns an empty array (not 500)—the wiring list is in the domain report.

## Internal archive section mapping

| Internal archive location | Correspondence |
|---|---|
| 05 §8 wakeup | WakeupService/domain/wakeup.go/`dueScanSQL` |
| 05 §9 autopilot and webhook | AutopilotService/WebhookService |
| 05 §2.14 batch closeout | NotifyBatchDone (the batch_done fifth form) |
| 04 §2.7 automation endpoints | the full adapters/http set (route reconciliation catalog in cmd routeTable) |
| 03 §2 automation(11)+021 | migrations 010/017/021 |
| 02 §8 execution substrate | ADR 0001 (the homegrown trade-off) |
