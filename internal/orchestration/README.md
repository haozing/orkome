# orchestration domain (the heart)

Task-queue state machine + eino engine + squads.

- **Responsibilities**: enqueueing (assignment/comment-trigger/direct-chat/squad/retry), claiming
  (SKIP LOCKED + concurrency gate + I1 fence), terminal settlement (outbox in the same
  transaction), park-resume (HITL), liveness sweep (150s heartbeat death verdict), session
  continuation (session pointer chain + conclusion injection), MCP library merged resolution,
  squad Mode B choreography (member AgentTools in the same run + usage split per member),
  evalworker, hourly usage rollup.
- **Spec mapping**: internal archive 05 §2 in full (state machine/fence/retry/cancel/resume/
  session pointer §2.9), §6 MCP; 09 in full (three-state exits/tools/task_message event stream/
  HITL/session continuation/metering/two-layer retry/squad §15/aux generation §16).
- **Services**: AgentService, EnqueueService, SettlementService, TriggerService, MCPServerService,
  AgentBuilderService, EvalService.
- **workers**: Dispatcher (heartbeat + ownership), LivenessSweeper (death verdicts + 24h parking +
  rollup).
- **engine/**: the only eino engine assembly point (Execute/Resume/Generate; steer/cancel
  observation/summary-level task_message/choreography/multimodal).
- **Tests**: I1–I3/I6/I10 named, decision trees, HITL six DoDs, goldens, liveness, outbox crash
  replay, session chains, choreography, rollup.
