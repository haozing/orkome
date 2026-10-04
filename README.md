# Orkome

A multi-agent collaboration platform server where humans drive work items and AI agents execute tasks: one work item is composed of multiple tasks.

> **Sources of truth**: API contract = `cmd/orkome-server/router_contract_test.go` + `docs/route-compare/`; domain rules = per-domain `domain/` + `internal/contract/`; table structure = `migrations/` + `arch/sql_allowlist.yml`; architecture constraints = `internal/arch/` + `.golangci.yml`; decision history = `docs/adr/`.

## Quick Start

```bash
# Start a dev Postgres
docker run -d --name orkome-pg -e POSTGRES_PASSWORD=dev -e POSTGRES_DB=orkome -p 55432:5432 postgres:16-alpine

export DATABASE_URL="postgres://postgres:dev@localhost:55432/orkome"

go run ./cmd/migrate up        # migrations (idempotent; down/status subcommands available)
go run ./cmd/orkome-server  # start the server, default :8080
curl localhost:8080/healthz    # liveness
curl localhost:8080/readyz     # readiness (DB reachable + migrations complete)
```

## Common Commands

```bash
make build      # compile everything
make test       # unit tests (architecture rule tests always run)
make test-int   # integration tests (need TEST_DATABASE_URL pointing at a real Postgres)
make lint       # golangci-lint (includes depguard architecture rules, see .golangci.yml)
```

## Production Deployment (orkome.com)

- Domain: **orkome.com**—frontend and API deploy same-origin, so cookie sessions/CSRF/WS Origin need zero config.
- Email sender uses an `@orkome.com` address (`RESEND_FROM_EMAIL` or `SMTP_FROM_EMAIL`).
- Only a cross-origin setup (separate frontend domain) needs `CORS_ALLOWED_ORIGINS` / `WS_ALLOWED_ORIGINS`.

## Layout

See `docs/adr/` and each domain's README: `cmd/` (composition roots; `workers.go` is the unified background job registry with bounded drain) → `internal/platform/` (zero-business-knowledge foundation: pg/pgmigrate/redisx/wsx/relayx/eventbus/outboxx/cronx/webx/storagex/ids/logx/metrics + the security trio sealx credential sealing, egressx SSRF gate, redactx secret redaction) → `internal/contract/` (six cross-domain vocabulary packages) → `internal/<domain>/{domain,app,adapters}` (ten domains, each with a README). Dependency rules are doubly enforced by `.golangci.yml` depguard + `internal/arch` architecture tests (including platform purity); architecture decision records live in `docs/adr/`.

The frontend (SPA, React + Vite + Tailwind + Base UI) lives in `web/`; the dev server same-origin-proxies to `:8080`—see `web/README.md` and `make web-dev`.
