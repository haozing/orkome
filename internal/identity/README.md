# identity domain

Authentication, accounts, workspaces and member governance.

- **Responsibilities**: email code/PAT dual auth chains, session JWT sliding renewal, CSRF double
  binding, workspace/member/invitation/share-link/PAT, revocation orchestration (≤10min SLA).
- **Spec mapping**: internal archive 05 §1 (attribution source)/§2.7 predecessors; 06 identity/;
  internal archive 04 §2.1–2.2.
- **Services**: SessionService, TokenService, WorkspaceService (member-removal full revocation /
  two-phase deletion via optional UserRevocation/WorkspaceTeardown ports wired by the composition
  root), PinService (sidebar pins), EmailService (SMTP/Resend dual channel, dev logging).
- **Tests**: auth matrix, CSRF/renewal/revocation, last-owner protection, pin lifecycle
  (integration_test.go); both port branches (service_test.go).
