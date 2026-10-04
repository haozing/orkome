# ADR 0002 · SSRF gate is homegrown on netip (egressx), not doyensec/safeurl

Status: implemented (semantics aligned with the original internal spec, implementation vehicle deviates)

## Background

The original internal spec requires: every user-controllable server-side outbound target—model
base_url (runtime_config) and MCP endpoints—MUST be public-internet HTTPS + reject private ranges
per resolved address after DNS (the nine ranges 127/8, 10/8, 172.16/12, 192.168/16, 169.254/16,
0/8, ::1, fc00::/7, fe80::/10); exceptions only via `ENGINE_EGRESS_EXTRA_ALLOW_CIDRS`.
The implementation uniformly designated doyensec/safeurl:
`GetConfigBuilder().SetAllowedSchemes("https").SetBlockedIPsCIDR(...)`.

Hands-on evaluation: safeurl **v0.2.5 only ships the
WrappedClient form—dial-time validation**—it wraps http.Client and intercepts when the request is
actually issued; there is no standalone pure URL-validation API. But our inbound point is
**submit-time pre-validation** (the Create/Update handlers for agents and MCP servers), where no
request is issued and only the "should this URL be accepted" verdict is needed—the WrappedClient
form is useless there. safeurl was removed from go.mod (unused = not kept).

## Decision

Homegrown `internal/platform/egressx` (netip with equivalent semantics):

- `Guard.ValidateURL`: https for general internet targets; since ADR 0007, plain http is admitted
  only when every resolved address lands inside `ENGINE_EGRESS_EXTRA_ALLOW_CIDRS` (fail-closed when
  unresolvable). An IP-literal host is judged directly, otherwise every resolved address is judged
  after DNS (`netip.Addr.Unmap()` normalization, sealing IPv4-mapped IPv6 bypasses); the nine-range
  private list matches the safeurl `SetBlockedIPsCIDR` recommended list item by item
  (spike-verified alignment);
- Exception ranges via `NewGuard(extraAllowCIDRs)`; invalid CIDRs fail-fast at startup;
- Mount points (all sharing the guard built by `egressGuardOf`): Create/Update of orchestration's
  `AgentService` (runtime_config.base_url) and `MCPServerService` (endpoint URL)
  (`internal/orchestration/app/service.go`); webhook subscription callback URLs
  (`internal/webhooks/app/service.go`); skills source-pack URLs (`internal/skills/app/service.go`);
  and engine-side revalidation at build time for model base_url (`modelconfig.go`) and MCP tool
  clients (`tools_mcp.go`).

**Layering note (where enforcement lives)**: creation-time pre-validation is best-effort—DNS
resolution failures are not hard-rejected (a transient resolution glitch must not become a business
rejection); and after validation passes the resolution can still change (TOCTOU / DNS rebinding).
Engine-side revalidation at build time has landed: model clients are rebuilt per task and re-run
`ValidateURL` (`internal/orchestration/adapters/engine/modelconfig.go`, no client caching), and MCP
tool clients are re-validated at assembly (`tools_mcp.go`). What is still **not** wired is the
transport-level dial hook (a custom `DialContext`/`Control` re-checking each actually-resolved
address), so a narrow "DNS flips to a private address between build and dial" window remains.

## Consequences

- **Positive**: inbound pre-validation immediately useful (known private targets rejected, and
  plaintext http rejected unless it resolves inside the exception ranges of ADR 0007); zero
  third-party SSRF dependency; `Guard` is stateless and safely concurrently reusable; 6 unit tests
  cover the private-range matrix / scheme-and-host shape / public-domain acceptance / exception
  ranges / invalid-CIDR fail-fast / http exception ranges.
- **Negative**: dial-time enforcement not wired (the window above); the private range list and
  exception semantics must be maintained manually (no security-library update channel); the literal
  implementation vehicle deviates from the original spec (vehicle deviates, semantics aligned).
- **Replacement path**: if a later safeurl version offers a pure validation API, only the inside of
  `ValidateURL` changes (signature and semantics unchanged), mount points untouched; dial-time
  enforcement—whichever library—must be done in the engine HTTP transport layer (a custom
  `DialContext`/`Control` hook), complementary to this ADR's pre-validation, not either-or.

Related: [ADR 0007](./0007-egress-http-exception-ranges.md)
(plain-http exception ranges); `internal/platform/egressx/egressx.go`.
