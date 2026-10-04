# conversation domain

Direct chat between humans and agents.

- **Responsibilities**: session CRUD/archival, message sending (same-request user row + task),
  three cancellation paths (deferred-verdict restored/stopped), draft restore (initiator-private),
  read state, quick actions (CRUD + generator), pending task view.
- **Spec mapping**: internal archive 05 §7, §2.5 cancellation; 06 conversation/;
  internal archive 04 §2.6.
- **Ports**: ChatEnqueuer/TaskCanceller (supplied by orchestration); ChatWriter (consumed by
  orchestration—terminal assistant rows / multimodal parts).
- **Tests**: full chat flow, both cancellation outcomes, privacy (drafts never in broadcasts),
  full HTTP group.
