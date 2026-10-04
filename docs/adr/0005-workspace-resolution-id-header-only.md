# ADR 0005 · Workspace resolution: X-Workspace-Id header only (UUID), no slug

- Status: accepted (2026-10-02)
- Background: the original spec said "workspace slug or id in path/query → middleware
  resolves"; the implementation long recognized only the `X-Workspace-Id` request header (UUID), and
  a routes.go comment admitted "M2 shape". Contract and implementation disagreed.
- Decision: **keep the implementation, amend the spec**. The single source of workspace
  resolution = the `X-Workspace-Id` request header; missing/empty → 400 `E_VALIDATION`,
  non-member → 403 (same code as nonexistent). The value is not format-checked—it flows
  straight into the membership lookup, so a malformed value (e.g. a slug) lands on the same 403 as
  a nonexistent workspace. No slug-lookup resolution.
- Rationale:
  1. No compatibility burden in the dev stage; a single resolution channel beats a dual channel
     (slug lookup first → UUID) by removing one query path and one cache-invalidation face (the
     slug-first dual channel needs a 5min membership cache—a double invalidation source);
  2. Slugs can be renamed (the identity domain already supports it); carrying a mutable identifier
     in URLs/headers creates stale-reference problems; UUIDs are stable;
  3. Enumeration safety: slugs are guessable strings; the id header plus member checking yields a
     more consistent failure surface.
- Impact: clients must send the UUID header (per this project's "no compatibility" stance, switch
  directly—there is no slug channel to fall back on).
- Related: nine consumers share the middleware shape (missing header 400 → member check 403):
  work/notification/automation/orchestration/conversation/skills/content/webhooks; identity's own
  workspace endpoints are the one deviation (non-member → 404 "workspace not found").
