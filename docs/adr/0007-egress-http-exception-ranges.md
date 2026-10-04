# ADR 0007: egress plaintext http allowed inside exception ranges

Date: 2026-10-04 | Status: implemented (egressx `httpWithinExtraAllow`)

## Background

egressx was originally https-only: all user-controllable outbound targets (agent base_url, MCP
endpoints) had to be public-internet HTTPS. The plugin-system design
(`plugin-system-design-2026-10-03.md` decision 6) introduced a third outbound class—**webhook
callback URLs**—and required: when the target IP is within `ENGINE_EGRESS_EXTRA_ALLOW_CIDRS`,
allow http, uniformly covering all three outbound sites. Motivation: first-party plugins
(agentblog etc.) are self-hosted on intranets where TLS terminates at the gateway or does not
exist; forcing https would lock intranet plugins out.

## Decision

- The https path behaves unchanged (exception ranges + private blacklist semantics as before;
  resolution failures deferred to dial-time enforcement).
- http is a **narrow gate**: **every** resolved address must hit an exception range to be allowed;
  with no exception ranges configured, any http is rejected outright.
- Unlike https, the http path is **fail-closed**: DNS-unresolvable targets are rejected directly—
  plaintext targets have no "dial-time second enforcement" to lean on (plaintext loopback inside
  exception ranges is the only legal shape).

## Consequences

- Intranet self-hosted plugins (http callbacks) work; public-internet http stays rejected
  (plaintext across the public internet is unacceptable).
- MCP/agent sides benefit in sync (same ValidateURL).
- Coverage note: the guard carrying the configured exception ranges is injected at the agent
  base_url / MCP / webhook sites; skills source-pack imports validate through a default guard with
  empty exception ranges, so http source-pack URLs are always rejected there.
- Dial-time enforcement (transport-level re-check of each resolved address) remains unwired—see
  ADR 0002's layering note; the http path compensates by being fail-closed at submit time.
- Security-boundary narrowing: plaintext allowed inside exception ranges—consistent with the
  existing "exception range = explicit trust" semantics, introducing no new trust assumptions.
