# W1 后端实施注记（Project Workflow 定义面）

> 日期：2026-10-13。波次契约：docs/wf-interfaces-frozen.md（§ 编号均指该文）。
> 设计：docs/project-workflow-design-2026-10-13.md。

## 做了什么

### 1. 迁移 1044（migrations/1044_project_workflow.up.sql / .down.sql）
四表：`project_workflow` / `workflow_stage` / `work_item_workflow_state` / `workflow_transition_log`。
- `project_workflow.project_id UNIQUE`（1:1）、trigger_mode CHECK、entry_stage_position、enabled、created_by。
- `workflow_stage`：(workflow_id,position) UNIQUE、(workflow_id,name) UNIQUE、position≥1 CHECK、executor_type CHECK；辅助索引 `idx_wf_stage_workflow (workflow_id, position)`。
- 两个 state 表 FK `work_item → work_item_workflow_state`、`state → transition_log` 均 ON DELETE CASCADE。
- down 按逆序 DROP 四表。

### 2. sql_allowlist（arch/sql_allowlist.yml）
work 域段追加 4 表名（comment_reaction 之后，批内字母序）。trace-guard 只扫 trace 词，与表归属清单无冲突。

### 3. domain（internal/work/domain/workflow.go + _test.go）
`ValidateWorkflowStages`（纯函数）：非空、position≥1 且无重复且连续 1..N（可乱序）、name 非空无重复、executor_type ∈ {agent,squad,none}、agent/squad 时 executor_id 必填（uuid 形状，`ParseUUID`）、none 时必须空、exit_status_key 非空。`SortWorkflowStages` 保存前排序。测试表驱动覆盖 12 条拒绝路径 + 3 条通过路径。

### 4. service（internal/work/app/service_workflow.go + workflow_model.go + _test.go）
- `WorkflowService` 签名逐字按 §2。Upsert：项目存在 → 触发模式/名称 → stages 排序 + domain 校验 → **逐个 exit_status_key 查 `ListStatuses` 校验存在且 category ∈ {started,done}**（§1.1 软引用的应用层守门）→ entry_stage_position 必须命中 → `emitCommitted` 包裹 repo 整体替换。事件名 `project.workflow_updated`（W1 无消费方，未注册进 events 包常量，W2 如需再收编）。
- Get：(nil, nil) 透传 → HTTP 层 `{"workflow":null}`。
- Delete：定义删除 + 在途 state `finished_at=now()`（见下"关键裁决 1"关于 note 的说明）。
- SetEnabled：只翻 enabled 位，NotFound 有定义才可翻。
- 状态面五方法（Start/StateOf/Restart/Detach/TryAdvance）：签名存在、返回 `apierr.New(apierr.Validation, "not implemented in W1")`；`WorkflowState` 结构体字段按 §2 逐字冻结，W2 原位替换实现体即可。

### 5. repo（internal/work/adapters/pgrepo/workflow_repo.go + workflow_integration_test.go）
`Repo` 上挂 4 方法（同 project_resource.go 模式）。UpsertWorkflow 单 `DB.Within` 事务：INSERT..SELECT..ON CONFLICT(project_id) upsert 头 → DELETE 旧 stages → 逐条 INSERT → 读回。项目不存在时 INSERT..SELECT 零行，用内部哨兵 errProjectNotFound 转 (nil, nil)，service 层经 GetProject 前置已兜 404。DELETE 用 `UPDATE state SET finished_at=now() ... WHERE workflow_id AND finished_at IS NULL` 再删定义。SELECT 列数与 Scan 一一对应已核对。

### 6. HTTP（internal/work/adapters/http/handlers_workflow.go + routes.go 追加）
五路由挂 `d.Workflow != nil` 守卫内：PUT/GET/DELETE `/api/projects/{projectId}/workflow` + POST `.../enable|disable`。JSON 形状按 §3：`{"workflow":{...}}`、GET 无定义 `{"workflow":null}`、DELETE 204。请求体 decode 用既有泛型助体（stages 经匿名 struct 逐字段搬运到 `workapp.WorkflowStageInput`，保证契约字段面）。
**注意**：工作项侧 4 端点（GET/start/restart/DELETE `/api/work-items/{id}/workflow`）是 §3 的一部分但属 W2 状态面，本波次未挂——§0 归属表把 handlers_workflow.go 写权给了 W1，W2 追加时直接在此文件续写。

### 7. wiring（cmd/orkome-server/wiring_work.go + router_contract_test.go）
wiringWork 构造 `app.NewWorkflowService(repo, bus)` + `SetOutbox(rec, db.Within)`，挂进 `workhttp.Deps.Workflow`（routes.go Deps struct 追加字段）。
**计划外但必需**：`cmd/orkome-server/router_contract_test.go` 的 routeTable 追加 5 行 + buildTestAPIRouter 的 workhttp.Deps 补 `Workflow:` 字段——`TestRouteContractReverseWalk` 反向走查（mux → 表）会把新挂载路由判为 undeclared 而红。这是既有门禁的机械登记，不是契约漂移；§0 未列此文件，已在下文"给主会话"标注。

## 关键裁决（需要主会话/W2 知悉）

1. **state 表的 workflow_id / stage_id / transition_log.to_stage_id 改为软引用（无 FK）**——偏离设计 §1 SQL 原文（原文 state.workflow_id REFERENCES project_workflow）。原因：契约 §2 明确 Delete 语义是"定义删除 + 在途 state 全部 finished_at=now + note=detached"，若保留 FK，只要该项目有任何在途/历史 state，DELETE project_workflow 即撞 23503（集成测试首跑实证）。这与 §1.1 exit_status_key 软引用是同一 brittle-FK 论证：定义的生命周期不能被光标历史绑死。work_item 的 CASCADE 保留（单删光标亡）。**建议 W2/主会话把此裁决回写进冻结契约 §1**。up.sql 内已留注释。
2. **detached note 未落**：契约 §2 说 `note='detached'`，但 note 列在 `workflow_transition_log`（不在 state 表）。W1 的 Delete 只做 `finished_at=now()`，不写 transition_log 行（W1 无状态面语义，写流水会伪造一条"推进"）。W2 实现 Detach 时用 state+log 双写表达 note=detached；届时 DeleteWorkflow 的 UPDATE 可顺手补插 log 行。
3. **事件名**：W1 不新增 events 常量，emit 用字面量 `project.workflow_updated`（任务书明示 W1 无消费方）。
4. **Upsert 大小上限**：stages ≤20（防滥用，契约未定值，前端编辑器足够）；name ≤100（对齐 project name）。W2 如需调整只动 service 层两处。
5. **HTTP enable/disable 返回**：`{"workflow":{...}}`（按 §3），实现是 SetEnabled 后回读 Get，保证返回体含最新 updated_at。

## 门禁输出摘要（全绿）

```
go build ./...                                  → BUILD_OK
go vet ./internal/work/... ./cmd/...            → VET_OK（无输出）
go test ./internal/work/... ./cmd/...           →
  ok internal/work/adapters/http   0.9s
  ok internal/work/adapters/pgrepo 0.7s
  ok internal/work/adapters/workers 0.4s
  ok internal/work/app             (cached)
  ok internal/work/domain          (cached)
  ok cmd/orkome-server             2.2s   （含 TestRouteContractWalk/ReverseWalk）
TEST_DATABASE_URL=… go test ./internal/work/adapters/pgrepo/ -run TestWorkflow -count=1 →
  ok internal/work/adapters/pgrepo 134.8s（TestWorkflowDefinitionLifecycle / TestWorkflowUpsert_WorkItemProjectFence，容器 orkome-test-pg 已起）
```
未修的既有噪音：golangci stdlib 报错、CRLF gofmt 全量误报（任务书豁免）。

## 给 W2 的交接提示

- **实现替换面**：只动 `service_workflow.go` 的五个状态面方法体 + `workflow_repo.go` 追加状态面查询 + `handlers_workflow.go` 追加 4 个工作项侧 handler + `routes.go` 追加 4 行注册 + router_contract_test.go routeTable 追加 4 行（`{"GET","/work-items/{id}/workflow"}` 等）。domain/迁移/定义面不用动。
- **WorkflowState 已冻结**在 service_workflow.go（含 json tag）；`EntryStatusKey` 字段 W1 无处落库（state 表没有该列）——W2 实现时要么进 state 表加列（需新迁移），要么从 transition_log 首行推导，请先定契约。
- **派发接缝**：`EnqueueAssignment/SquadTask` 归 orchestration；W2 的 wiring_workflow.go 持 orchapp.EnqueueService，work 域 service 不 import orchestration（iron law 保持）。
- **事件门铃**：`work_item.updated` / `work_item:updated_by_agent` / `task.completed|failed|cancelled`；relay 对 work_item 系 payload 为 nil（main.go decodeOutboxPayload 只认 Task*/Comment*），监听器取 work_item_id 需 W2 补解码（设计 §4.1 注）。
- **重跑集成测试**：容器没起先 `docker start orkome-test-pg`；测试复用 `batchEnv`（建库 orkome_m85，框架+产品迁移全跑，单测约 50s/用例）。
- 前端契约自测锚点：`PUT /api/projects/{id}/workflow` 空 stages / 断档 / exit_status_key=closed 均回 400 `E_VALIDATION`，Details[0].Field="stages" 或 "entry_stage_position"，可直接映射表单错误。

## 文件清单（W1 后端全量）

- migrations/1044_project_workflow.up.sql / .down.sql（新）
- arch/sql_allowlist.yml（work 域 +4 表）
- internal/work/domain/workflow.go / workflow_test.go（新）
- internal/work/app/workflow_model.go（新）、service_workflow.go（新）、service_workflow_test.go（新）、ports.go（追加 workflow 段 + WorkflowUpsert）
- internal/work/adapters/pgrepo/workflow_repo.go / workflow_integration_test.go（新）
- internal/work/adapters/http/handlers_workflow.go（新）、routes.go（Deps.Workflow 字段 + 5 行注册）
- cmd/orkome-server/wiring_work.go（挂 WorkflowService）
- cmd/orkome-server/router_contract_test.go（routeTable +5 行、buildTestAPIRouter 补 Workflow 字段——§0 未列，见上）
