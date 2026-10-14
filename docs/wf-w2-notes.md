# W2 后端实施注记（Project Workflow 推进引擎）

> 日期：2026-10-13。波次契约：docs/wf-interfaces-frozen.md（§ 编号均指该文）。
> 设计：docs/project-workflow-design-2026-10-13.md（下称"设计"）。
> W1 交接：docs/wf-w1-backend-notes.md。

## 做了什么

### 1. service 状态面（internal/work/app/service_workflow.go）

五个占位方法原位替换为真实现，W1 定义面代码零改动：

- **Start**：幂等（已有 state 直接 `readState` 返回）→ 校验工单挂项目 + 项目有
  workflow → 定位 entry stage → 单事务（`s.uow`/emitCommitted）：InsertWorkflowState
  （ON CONFLICT(work_item_id) DO NOTHING 兜底并发）→ transition_log(from=NULL,
  actor=user/调用者) → **assignee 投影**（agent→'agent'、squad→'squad'、
  none→'none'+清 id，expectedRevision=0）→ env.Payload = wiEvent(updated)
  **发 `work_item.updated`**（复用 service_workitem.go 的 `wiEvent`，assignee
  派发器/前端消费面）→ **派第一棒**（事务内，EnqueueService 嵌套 Within 复用外层 tx）。
- **StateOf**：state + GetWorkflowByID + 工单现 status 组装 §2 形状；孤儿 state
  （定义已删）降级空 stages 裸形。
- **Restart**：回 entry、计数清零、finished_at=NULL、log(note='restarted')、
  投影+派发，单事务同 Start。
- **Detach**：FinishWorkflowState（WHERE finished_at IS NULL，幂等）+
  log(note='detached')——W1 注记的双写裁决兑现。
- **TryAdvance**（设计 §2.1/§4 逐条）：
  1) 无 state / finished_at 非空 / workflow disabled → (nil,nil)；
  2) work_item.status_key ≠ 当前 stage.exit_status_key → (nil,nil)；
  3) 存在性检查（§4.3）经注入谓词 `SetStageTaskTerminal`（none 执行者跳过=人工背书）；
  4) dispatch_count > MaxDispatchPerStage(3) → (nil,nil)；
  5) 无下一阶段 → FinishWorkflowState + log(note='completed')，返回终态；
  6) **单事务**：AdvanceWorkflowStateCAS（WHERE stage_id=$from，affected=0 →
     并发输家 (nil,nil)）→ transition_log → 投影 → advance_count 超 阶段数×3 →
     **返回包 `ErrAdvanceRunaway` 的错误（事务回滚）** → dispatch_count+1 → 派下一棒
     （dispatchStage 注入位）→ 返回新 state。
  不改 status（推进的因不是果，§3.2 不变量）。
- 新增 service 侧熔断收口 `PauseWorkflowOnRunaway`：wiring 收到 ErrAdvanceRunaway
  后调用——SetWorkflowEnabled(false) + 回传定义供 wiring 通知 created_by（§5.2）。

### 2. 注入接缝（work 域不 import orchestration，iron law）

| 注入位 | 作用 | wiring 实现 |
|---|---|---|
| `app.SetStageTaskTerminal(fn)` | §4.3 条件 2：agent_task_queue 存在 context->>'stage_id'=stage 且 status ∈ 终态（跨域读，cmd 层内联 SQL） | wiring_workflow.go 内联 SQL |
| `app.SetDispatcher(fn)` | §3/§5 派发矩阵：按 StageExecInfo.ExecutorType 分流 | EnqueueAssignment/SquadTask + 通知 |
| `svc.AppendTransitionNote` | already_active → MergeIntoPending 后在最新流水追加 note='coalesced'（**契约 §2 冻结面之外的 W2 追加段**，ports.go 注释标注） | wiring coalesced 路径 |

### 3. repo（ports.go W2 追加段 + pgrepo/workflow_repo.go）

九个新方法：GetWorkflowByID / GetWorkflowState / InsertWorkflowState（UNIQUE
work_item_id 兜底幂等）/ AdvanceWorkflowStateCAS / RestartWorkflowState /
FinishWorkflowState / AdjWorkflowDispatchCount / AppendTransitionLog /
AppendTransitionNote。全部只摸 work 域四张 workflow 表（allowlist 不动）。
中间形状 `WorkflowStateRecord`/`TransitionLogEntry` 定义在 ports.go（service
组装 §2 契约形状，repo 不感知定义表）。

### 4. wiring（cmd/orkome-server/wiring_workflow.go，新文件）

- `wiringWorkflow(log, db, bus, wfSvc, orchEnq)`：main.go 在 wiringWork 之后、
  wiringOrchestration 之后**一行调用**（消费 workDeps.Workflow + orchEnq）。
- **双门铃**（PhaseProject）：`work_item.updated` / `work_item:updated_by_agent`
  取 `WorkItemPayload.ID`；`task.completed|failed|cancelled` 取
  `TaskPayload.WorkItemID`（任务 id ≠ work_item_id）。取不到直接 return
  （payload 可能 nil）。五个事件同一套读库裁决（§2.2）。
- **派发矩阵**（契约 §5 逐条）：agent → EnqueueAssignment（originator=
  workflow.created_by）；`queued` 正常；`already_active` → orchpgrepo.MergeIntoPending
  + AppendTransitionNote('coalesced')；`blocked|model_not_configured` →
  WriteNotice(created_by, type='workflow_stage') 原地等待（不回减 dispatch_count：
  该次 advance 合法完成，只是没派出去——重派靠人工/评论触发，计数语义见"关键
  裁决 4"）；squad → EnqueueSquadTask；none → 只通知不派发。
- **防失控联动**：errors.Is(err, ErrAdvanceRunaway) → PauseWorkflowOnRunaway +
  WriteNotice(created_by)。
- TryAdvance 错误 log.Warn 不上抛（eventbus.run 已有 panic recover；数据态错误
  不能炸 relay 线程）。
- main.go 的 assignee 派发器 / resetIdleWorkItem **零改动**（§3.1 #2/#3 共存论证）。

### 5. main.go（两处，均在允许清单）

- wiringWorkflow 一行调用。
- decodeOutboxPayload 补 case：WorkItemCreated/Updated/UpdatedByAgent/Deleted →
  `events.WorkItemPayload`（契约 §4.1 relay 解码注记的顺手修正）。

### 6. HTTP（§3 工作项侧）

- handlers_workflow.go 追加 3 handler：POST start/restart → `{"state":{...}}`、
  DELETE → 204。
- routes.go 挂 `d.Workflow != nil` 守卫内 3 行注册。
- router_contract_test.go routeTable 追加 3 行登记（GET state 未挂，见"给 W3"）。
- buildTestAPIRouter 无需改（Workflow 字段 W1 已装配非 nil）。

### 7. 测试

- **单测** internal/work/app/service_workflow_state_test.go（fake repo）：
  TryAdvance 全条件分支（无 state/已终态/disabled/未达出口态/无任务背书/快乐路径/
  none 例外/CAS 幂等/终阶段/detached/dispatch 超限/runaway）、Start 幂等与事件
  投影（work_item.updated + assignee 负载）、Restart/Detach、派发分流与
  **§5 context 形状逐字段断言**（workflowStageContextForTest 与 wiring 同构，
  engine W3 消费锚）。W1 的 TestWorkflowStateFace_NotImplementedInW1 断言随占位
  替换退役（service_workflow_test.go 内删除该函数，其余零改动）。
- **集成** internal/work/adapters/pgrepo/workflow_engine_integration_test.go
  （batchEnv，13 个 TestWorkflowEngine_*，**13/13 全绿**，各 ~50-80s——batchEnv
  每用例重建 orkome_m85 + 全量迁移；跑全矩阵建议 `-timeout 30m`+）：
  | # | 用例 | 断言 |
  |---|---|---|
  | 1 | Start_BuildsState... | state 建立 + assignee 投影 + 任务落 agent_task_queue（context §5 形状逐字段）+ log 一行 from=NULL |
  | 2 | Start_Idempotent | 二次 Start 同 state、无第二行 log |
  | 3 | HappyPath | 任务 completed + status 出口态 → 前进 + 换人 + 两行 log + status 不被改写 |
  | 4 | StatusNotAtExit | 任务终态但 status 未达出口 → nil |
  | 5 | NoTerminalTask_NoneSkips | 出口态无任务 → nil；none 阶段无任务可推进 |
  | 6 | CAS_Idempotent | 两次 TryAdvance 只成功一次、advance_count=1 |
  | 7 | FinalStage_Completes | finished_at + note=completed + 无新任务 |
  | 8 | Detach_StopsAdvancing | note=detached 双写 + 冻结 + 二次 Detach 幂等 |
  | 9 | Disabled_NoAdvance | 停用 nil；重启用后同 state 续推（§4.8 逃生门） |
  | 10 | DispatchCountOverLimit | >3 不推进 |
  | 11 | AdvanceCountOverLimit | ErrAdvanceRunaway + 事务回滚（光标不动、无 log 行） |
  | 12 | Restart | 回 entry、计数清零、assignee 回第一棒、note=restarted |
  | 13 | StartAssigneeProjection | work_item.updated 负载 assignee=第一棒（assignee 派发器共存语义锚，§3.1 #2） |

## 关键裁决（需要主会话/W3 知悉）

1. **EntryStatusKey 落库语义**：W1 注记悬而未决的 entry_status——裁决为
   **不加列、不推导 transition_log**，`EntryStatusKey` 填工单**当前** status_key
   （读面实时值）。理由：设计 §2.1 的"进入本阶段时 status"在 none/返工场景下
   无稳定事实来源（光标进入与 status 变更无因果序），而前端进度区块真正要展示
   的是"当前停在什么状态"。契约 §2 字段名不动，语义见本节；W3 前端如需严格
   "进入时"语义需回补迁移加列（当前无消费需求，YAGNI）。
2. **已完成态的 Start 幂等返回现 state**（契约 §2 明文）。已完成/已 detached 的
   state 不复活；复活走 Restart。
3. **TryAdvance 的 final-stage 分支返回终态 state（非 nil）**：契约 §2 说"返回
   nil 表示未推进；推进成功返回新 state"——完成最后一阶段算推进成功（finished_at
   就是"新 state"），wiring 侧对 nil/非 nil 的处理因此天然正确。
4. **blocked 不回减 dispatch_count**：§4.5 说"派发 count 不增加（没派出去）"，
   实现上计数 +1 发生在 advance 事务内、blocked 判定在事务外的 dispatcher 回调
   （EnqueueAssignment 返回 reason 后才知道 blocked）。权衡：回减需要 dispatcher
   反向调用 repo（新增跨层接缝）；而 blocked 场景流程原地等待、无重试风暴
   （没有自动重派器），计数虚高 1 的代价是占掉 3 次配额中的 1 次——人工 restart
   即清零。已接受并在本节记录；如需严格语义，后续把"派发结果"回调注入 service
   再回减。
5. **state 重定义后的孤儿光标**：UpsertWorkflow 整体替换 stages 会使在途 state
   的 stage_id 悬空（软引用）。TryAdvance 对"stage_id 不在当前定义"的光标冻结
   不推进（安全侧）。人工 Restart 或重新 Start（幂等返回现 state）不能修复孤儿
   ——需要删 state 重进。这是 v0 接受的运维路径（改定义 = 全部在途单子人工
   处理），未做自动迁移。
6. **W1 测试一处退役**：TestWorkflowStateFace_NotImplementedInW1 断言五方法返回
   "not implemented in W1"——占位被真实现替换后该断言与 W2 交付直接矛盾，
   已删除（service_workflow_test.go 其余 10 个 W1 测试零改动全绿）。

## 给 W3 的交接

- **任务 context 形状（engine 消费，逐字段冻结 §5）**：dispatcher 注入实现组装
  `{"trigger":"workflow_stage","workflow_id","stage_id","stage_name",
  "stage_position","stage_exit_status","instruction","work_item_title"}`。
  同构锚：单测 TestWorkflowDispatch_ContextShapeContract（app 包）+
  集成 Test 1 的 SQL 断言；生产实现 = wiring_workflow.go agent 分支
  （wiring_workflow.go:63-70 附近，实际行号搜 `workflow_stage`）。
- **Start 后第一棒 evidence**：trigger_evidence_kind='assignment'（EnqueueAssignment
  既有值，不是新值——设计 §3.1 #2 已接受极端交错下 evidence 变 assignment 的
  语义无害）；事件 evidence = Start 事务发出的 `work_item.updated`（actor=调用者）。
  第一棒任务 originator/accountable = workflow.created_by。
- **engine 注入点**：internal/orchestration/adapters/engine/ 按
  `task.context.trigger == "workflow_stage"` 分支注入阶段简报（同 squadBrief），
  提示模板建议包含"完成后调用 update_work_item_status 将状态移至
  {stage_exit_status} 即进入下一阶段"。不改工具面。
- **GET /api/work-items/{id}/workflow 未挂**：StateOf 走内部/前端 W3 联调时再定
  （挂载即需同步 routeTable 登记）。前端 api 层的 getWorkItemWorkflow 现状会 404，
  W3 联调清单加一条。
- **防失控熔断联动点**：wiring_workflow.go `advance` 闭包内 errors.Is
  (ErrAdvanceRunaway) 分支——W3 若加 engine 侧推进（无），不影响此路径。
- **测试复跑**：容器 orkome-test-pg（47h 运行中无碍）；13 条集成用例各 ~50s
  （batchEnv 每用例重建 orkome_m85 + 全量迁移），`-timeout 25m` 起。

## 门禁输出（全部绿）

```
go build ./...                          → BUILD_OK
go vet ./internal/work/... ./cmd/...    → VET_OK（无输出）
go test ./internal/work/... ./cmd/... -count=1 →
  ok internal/work/adapters/http   1.3s
  ok internal/work/adapters/pgrepo 1.1s
  ok internal/work/adapters/workers 0.6s
  ok internal/work/app             0.8s
  ok internal/work/domain          0.6s
  ok cmd/orkome-server             2.4s（含 TestRouteContractWalk/ReverseWalk）
TEST_DATABASE_URL=… go test ./internal/work/adapters/pgrepo/ -run "TestWorkflow" -count=1 →
  13× TestWorkflowEngine_*（W2）+ TestWorkflowDefinitionLifecycle +
  TestWorkflowUpsert_WorkItemProjectFence（W1）全 PASS（容器 orkome-test-pg）
```
已知噪音未修：golangci stdlib 报错、CRLF gofmt 全量误报（任务书豁免）。

**集成调试期间修的测试侧问题**（非产品代码）：
1. `jsonb_build_object` 内的参数需显式 `::text/::int` cast（pgx 无法推断 → 42P18）；
2. 集成 env 对 TryAdvance 的单事务断言必须接真 `db.Within` 做 uow
   （否则 runaway 回滚无从谈起——RunawayError 用例的回滚断言依赖此）；
3. FinalStage 的"无新任务"断言只数 queued/dispatched/running（recStageTask
   自身的结算任务带同 stage_id，count 全量会误报）。

## 文件清单（W2 全量）

- internal/work/app/service_workflow.go（五方法真实现 + 注入位 + PauseWorkflowOnRunaway）
- internal/work/app/ports.go（Repo 接口 W2 追加段 + WorkflowStateRecord/Insert/TransitionLogEntry）
- internal/work/app/service_workflow_state_test.go（新，fake repo 单测 ~16 条）
- internal/work/app/service_workflow_test.go（仅删 1 个 W1 占位断言函数）
- internal/work/adapters/pgrepo/workflow_repo.go（追加状态面读写）
- internal/work/adapters/pgrepo/workflow_engine_integration_test.go（新，13 条）
- internal/work/adapters/http/handlers_workflow.go（追加 3 handler）
- internal/work/adapters/http/routes.go（追加 3 行注册）
- cmd/orkome-server/wiring_workflow.go（新）
- cmd/orkome-server/main.go（+1 行 wiring；decodeOutboxPayload +1 case）
- cmd/orkome-server/router_contract_test.go（routeTable +3 行）
- docs/wf-w2-notes.md（本文件）
