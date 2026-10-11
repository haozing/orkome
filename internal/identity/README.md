# identity — product account extensions

Only what the ploykit framework does NOT provide. Login sessions, PAT CRUD,
account management, workspaces/members/invitations/share-links and email-code
login all live in `github.com/haozing/ploykit` now; this package layers the
orkome-specific extensions on top:

- **PinService** — sidebar pins (`pinned_item`, migration 1023), gated on the
  framework-owned `member` table (read-only).
- **UITokenService** — single-use 60s HS256 tickets for plugin embedding
  (`ui_token`, migration 1033); signed with a product-owned secret.
- **Email adapter** (`adapters/email`) — DevLog/SMTP/Resend backends exposing
  the framework `identity/app.EmailSender` port (login codes, invites,
  password-reset and email-verification links).
- **HTTP extensions** (`adapters/http`) — `MountExtensions` registers
  `GET/PUT /api/pins`, `PUT /api/pins/reorder`,
  `DELETE /api/pins/{itemType}/{itemId}`, `POST /api/ui-tokens`,
  `POST /api/ui-tokens/consume` and `POST /cli-token` (session→PAT exchange
  via the framework TokenService). The ambient auth chain
  (`webx.Authenticate` + product CSRF) is applied by the composition root,
  exactly like the framework's own mounts.

Tests: pin/uuid rules (domain), pin service fakes (app), DB-backed pin /
ui-token / cli-token coverage + route accounting (adapters/http — skips
without `TEST_DATABASE_URL`).
