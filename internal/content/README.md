# content domain

Attachments and avatars: upload/download (three modes) of five-host attachments with
cascade-cleanup deletion, avatar HMAC-signed URLs, local static serving.

## Structure and exported face

| Layer | Contents |
|---|---|
| domain/signer.go | `Signer` (avatar HMAC: Sig/Verify binding key+exp); `DownloadSigner` (attachment signed URLs—class-A ruling: HMAC binds attachment_id+intent+exp+member, member bound by default to prevent forwarding; verification checks only HMAC+expiry = session-free direct link) |
| domain/budget.go | source-context budgets (snapshot 1MiB/agent 64KiB/total 72KiB) and host quotas (≤100 items/500MiB) constants + validation (internal archive 05 §13 the sole authority) |
| domain/markdown.go | `BreakLinkAdjacency` (internal archive 05 §14 Markdown channel anti-injection: "](" adjacency split apart; reused by the channel domain's M8 outbound wiring) |
| app/service.go | `AttachmentService` (Upload streaming sha256/100MiB cap/redactx.Sanitize on persist/host-quota precheck, Get/Open (fail-closed digest verification on read, sentinel ErrDigestMismatch), SignedURL minting (intent+member+membership gate)/VerifySigned/OpenSigned session-free open, Delete cascade cleanup, ListByHost); `AvatarService` (Sign/Verify/Open) |
| app/intentsweep.go | `IntentSweeper`—the object-cleanup intent-ledger sweeper (lease claiming + exponential backoff 2^n capped at 15m + exhaustion dead-letter released; table pending migration, see WIRING) |
| adapters/pgrepo | attachment four methods (workspace predicate; Delete RETURNING key for storage cleanup) |
| adapters/http | upload-file, attachments metadata/three-mode download (auto inline judgment/proxy/signed), avatars signing, /uploads/* local static (sidecar restoration + traversal protection); Range single-span parsing (a-b/open tail/-N, out-of-range or multi-span falls back to full body) |

**Ports**: `Repo` (attachment persistence), `Store = storagex.Store` (object storage: Local atomic
writes + S3 path-style, MinIO-verified).

## Known trade-offs

- **Source-context use cases belong to the work domain**: the archive 05 §13 "comment-spawned sub
  work item" (capture token/limits/digest race guard/retry) is implemented in `internal/work/app`
  (SourceContextService); this domain carries only attachments/avatars/storage—mind the landing
  spot when mapping;
- **source_context_object_intent ledger table pending migration** (one of internal archive 03 §2
  content(2)): the use-case layer IntentSweeper (claim/backoff/dead letter) is ready with fake unit
  tests; the SQL and worker registration await a migration number (the main-path object cleanup is
  already covered by attachment-delete cascade);
- **Budget constants here, assembly on the work side**: the snapshot 1MiB/agent 64KiB/total 72KiB
  constants and validators live in domain/budget.go (06 "content domain: snapshot budgets"); for
  the work domain's SourceContextService wiring replacement see WIRING;
- **signed-download semantics (2026-10-02 class-A ruling)**: signature = HMAC(id+intent+exp+
  member), member bound by default; minting requires membership (service-layer memberGate);
  verification checks only HMAC+expiry (session-free direct link); member matching enforced only
  when authentication is present (anonymous direct links pass—forwarding prevention without
  sacrificing direct-link semantics);
- **Read-time digest is double-pass IO**: Open/OpenSigned hashes the whole object to verify before
  opening the real stream (fail-closed at the cost of a double read); historical rows with an
  empty inline sha256 pass, tightened after backfill.

## Internal archive section mapping

| Internal archive location | Correspondence |
|---|---|
| 05 §13 content | five attachment hosts/three modes/Range (source-context part per trade-off 1) |
| 06 content/ + storagex | directory and ports |
| 07 §7.5 content security | SVG forced download, atomic upload writes, CSP |
| 02 §11 optional degradation | S3 missing → local disk |
| 04 §2.8 endpoint group | adapters/http |
