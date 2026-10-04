# channels domain

IM-channel integration framework (platform-agnostic): the wire contract + the eight-step inbound
pipeline + per-installation single-connection lease supervision + outbound delivery (incl.
two-phase streaming) + the media-intent ledger (incl. ready-materialization) + LLM title callback.
Concrete channel adapters are the extension point (no production-registered adapter in v1; the
fake channel is injected in tests to run the DoDs).

## Structure and exported face

| Layer | Contents |
|---|---|
| wire/wire.go | `Kind`/`ChatType`/`MsgType`/`Source`/`MediaRef`/`InboundMessage`/`OutboundMessage` normalized envelopes; `Capability` bitmask (Has/String; incl. `CapStreamingReply` the two-phase streaming bit); the `Channel` interface (Kind/Connect blocking receive loop/Disconnect/Send/Capabilities); `Factory`+`Registry` (last-writer-wins)—named to avoid the top-level contract (06 verbatim) |
| domain/ | Pure decisions: control-command parsing (`/clear` `/new` `/task` whole-token, case-sensitive, tab-delimited, mutually exclusive on the first line), title derivation (first non-empty line, image alt, Markdown stripped, 30-rune cap), command routing (OutboundKind), media intents, `GuardedSender` |
| app/ | The eight-step inbound pipeline (auth/dedup/control-parse/media-intent/work-item & session invoke/audit/drop audit), debounce (same (session,generation) bursts merge, `/clear` hard boundary), router adapters, outbound render (Markdown link-adjacency split, channel-media HTML comments), title-callback service (LLM polish with explicit-CAS), supervisor (per-installation lease: SQL backend CAS, cancel-all), `ChannelEnqueuer`/`MediaMaterializer`/`Repo` ports |
| adapters/pgrepo | installation/binding/session/dedup/intent/delivery tables (the built subset of internal archive 03 §2 channels(12)) |
| adapters/http | webhook ingestion endpoints (per concrete channel kind) |
| adapters/workers | delivery retry worker (SKIP LOCKED + backoff + dead letter) |

## Internal archive section mapping

| Internal archive location | Correspondence |
|---|---|
| 06 channels/ | directory, wire contract, pipeline |
| 05 §10 channels | lease/dedup/debounce/title |
| 04 §2.7 channel endpoints | adapters/http |
