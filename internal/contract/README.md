# contract domain

Cross-domain shared vocabulary (zero implementation): the only third-party import allowed by
domain layers (internal archive 02 §4 rule 1 "stdlib + contract"); all cross-domain type
collaboration goes through these packages. The package itself has zero dependencies (besides
stdlib and the modelmeta eino-factory gray zone, see trade-offs).

## Six-package vocabulary

| Package | One-line responsibility |
|---|---|
| apierr | API error-code catalog (internal archive 04 §5: unique, stable, localizable; the HTTP layer maps to status codes uniformly via the webx table) |
| dispatchreason | task admission/rejection reason codes: five admission states (queued/coalesced/deferred/steered/blocked) + rejection codes (reason_code shared by comment-trigger preview and the actual write path) |
| events | cross-domain business event vocabulary + Envelope (dotted event names task.queued; emitted post-commit via the Emitter, I4) |
| failuretax | failure vocabulary (15 values: platform 7 + agent_error 4 + agent_provider 4) + RetryWhitelist (5-value auto-retry allowlist) + ResumeUnsafe (session poison set)—the gatekeeper of retry/resume decisions |
| modelmeta | model provider catalog: provider → ChatModel factory registry (eino-ext providers registered as they land; fake for test goldens) |
| wswire | WS frame vocabulary (internal archive 04 §3): colon frame names (task:queued) uniquely mapped to dotted event names via WSName, frame shape {type,payload,event_id}, scope constants |

## Known trade-offs

- **Error-code subset**: apierr is a subset of the archive's 12 codes; the E_ACTIVE_DUPLICATE
  (active duplicate) semantics once borrowed E_REVISION_CONFLICT (violating "error codes MUST be
  stable", audit item C3);
- **Event-name coverage**: events ~65 names vs the archive's 86+; long tails like task:progress
  and notification:* realtime frames added as used; Envelope.EventID's comment says ULID but
  UUIDv4 is actually minted (per the ids convention);
- **modelmeta structural gray zone**: actually a factory registry rather than "model metadata",
  and carries an eino dependency (the archive places modelconfig on the orchestration side)—kept
  on file as a contract exception gray zone (audit "structural misplacement" entry);
- **wswire payload strong typing**: the frame shape and ChatDonePayload exist; strongly-typed
  payloads for 82+ events added as used;
- **dispatchreason constants partially unreferenced in production**: the decision tree has string
  literals not yet converged to constants (on account).

## Internal archive section mapping

| Internal archive location | Correspondence |
|---|---|
| 02 §4 rule 1 / §6 event pipeline | contract as the domain-legal dependency face; events + Emitter semantics |
| 04 §3 WS event catalog / §5 error-code catalog | wswire, apierr, dispatchreason |
| 05 §2.8 retry allowlist / §2.9 session poison | failuretax (RetryWhitelist / ResumeUnsafe verbatim per the archive) |
| 06 contract directory listing | all six packages registered |
| 09 §17 I-E6 vocabulary classification | failuretax consumer-side named tests (orchestration) |
