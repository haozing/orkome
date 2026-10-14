# Project Workflow 实施接口冻结（波次间契约）

> 本文是三个实施波次之间的**唯一契约锚**。依赖方只读本文，不读对方实现。
> 变更本文 = 主会话裁决，不允许实施 agent 单方漂移。
> 设计依据：docs/project-workflow-design-2026-10-13.md（下称"设计"）。

## 0. 归属切分（文件级，互斥）

| 文件/目录 | W1 后端 | W1 前端 | W2 引擎 | W3 引擎/UI | W3 前端 |
|---|---|---|---|---|---|
| migrations/1044_project_workflow.* | **写** | - | - | - | - |
| arch/sql_allowlist.yml | **写**（work 域加 4 表） | - | - | - | - |
| internal/work/domain/workflow*.go | **写** | - | - | - | - |
| internal/work/app/service_workflow.go + workflow_model.go | **写** | - | **读** | - | - |
| internal/work/app/ports.go Repo 接口段 | **写**（追加 workflow 段） | - | - | - | - |
| internal/work/adapters/pgrepo/workflow_repo.go | **写** | - | **读** | - | - |
| internal/work/adapters/http/handlers_workflow.go + routes.go 追加段 | **写** | - | **追加 advance 内部端点禁用——W2 走 eventbus 不走 HTTP** | - | - |
| internal/work/adapters/pgrepo/integration_test.go / 新 workflow_integration_test.go | **写** | - | **写**（新增文件，不改 W1 测试） | - | - |
| web/src/api/workflow.ts | - | **写** | - | - | **读** |
| web/src/components/workflow/*.tsx（新建） | - | **写** | - | - | **追加** |
| web/src/routes/_app.work-items.$id.tsx | - | - | - | - | **写**（侧栏区块） |
| web/src/i18n/locales/zh.ts + en.ts | - | **写**（nav.workflow 段） | - | - | **追加**（如需新键） |
| cmd/orkome-server/wiring_work.go | **写**（挂 WorkflowService 进 Deps） | - | **追加**（挂引擎） | - | - |
| cmd/orkome-server/wiring_workflow.go（新建，W2 建） | - | - | **写** | - | - |
| cmd/orkome-server/main.go | - | - | **追加**（decodeOutboxPayload 补 work_item 系解码） | - | - |
| internal/orchestration/adapters/engine/*.go | - | - | - | **写**（阶段简报分支） | - |

## 1. 数据模型（W1 落库，全部消费方按此读）

迁移 1044，四表（DDL 以设计 §1 为准，此处只列**契约字段**）：

- `project_workflow`：`project_id UNIQUE`（1:1）、`trigger_mode`('auto'|'manual')、`entry_stage_position`、`enabled`、`created_by`
- `workflow_stage`：`workflow_id`、`position`（≥1，workflow 内唯一）、`name`（workflow 内唯一）、`exit_status_key`（**软引用**，应用层校验存在且 category ∈ started|done）、`executor_type`('agent'|'squad'|'none')、`executor_id`（none 时 NULL）、`instruction`
- `work_item_workflow_state`：`work_item_id UNIQUE`、`workflow_id`、`stage_id`、`dispatch_count`、`advance_count`、`entered_at`、`finished_at`（NULL=在途）
- `workflow_transition_log`：`state_id`、`from_stage_id`（NULL=初始）、`to_stage_id`、`actor_type`('agent'|'user'|'system')、`actor_id`、`task_id`、`note`

> ★ W1 实施修订（已落库，迁移 1044 实际形态为准）：state 表的 `workflow_id`/`stage_id` 与 transition_log 的 `to_stage_id` 均为**软引用（无 FK）**——契约 §2 的 Delete 语义（删定义、在途 state 冻结 detached）与设计 §1 原文的 FK 冲突（集成测试实证 DELETE 撞 23503）；与 §1.1 exit_status_key 同一 brittle-FK 论证。**唯一硬引用：state.work_item_id → work_item ON DELETE CASCADE**（单删光标亡）。
> ★ W1 计划外改动追认：`cmd/orkome-server/router_contract_test.go` routeTable 登记 5 条 workflow 路由（既有 TestRouteContractReverseWalk 门禁要求挂载路由必须登记，属机械登记）。
> ★ detached note：note 列在 transition_log 不在 state 表；W1 Delete 只落 finished_at，W2 的 Detach/Restart 需同时写 transition_log（note='detached'/'restarted'）。

## 2. Go 服务层接口（W1 实现，W2/W3 消费）

包 `workapp`，文件 service_workflow.go：

```go
// —— W1：定义面 CRUD ——
type WorkflowService struct{ ... }
func NewWorkflowService(repo Repo, emit Emitter) *WorkflowService

// stages 一次整体替换式保存；exit_status_key 应用层校验（§设计1.1）
type WorkflowStageInput struct {
    Position      int    `json:"position"`
    Name          string `json:"name"`
    ExitStatusKey string `json:"exit_status_key"`
    ExecutorType  string `json:"executor_type"` // agent|squad|none
    ExecutorID    string `json:"executor_id,omitempty"`
    Instruction   string `json:"instruction"`
}
type WorkflowDefinition struct { ... } // 含 Stages []WorkflowStage（按 position 升序）
func (s *WorkflowService) Upsert(ctx, ws, projectID string, def WorkflowUpsertInput, actorUserID string) (*WorkflowDefinition, error)
func (s *WorkflowService) Get(ctx, ws, projectID string) (*WorkflowDefinition, error)  // 无定义返回 nil,nil
func (s *WorkflowService) Delete(ctx, ws, projectID, actorUserID string) error          // 在途 state 全部 finished_at=now + note=detached
func (s *WorkflowService) SetEnabled(ctx, ws, projectID string, enabled bool, actorUserID string) error

// —— W2：状态面（W1 只建表不做；本节签名冻结，W2 填实现）——
type WorkflowState struct {
    WorkItemID string `json:"work_item_id"`
    WorkflowID string `json:"workflow_id"`
    ProjectID  string `json:"project_id"`
    StageID    string `json:"stage_id"`
    StagePosition int `json:"stage_position"`
    StageName  string `json:"stage_name"`
    ExecutorType string `json:"executor_type"`
    ExecutorID string `json:"executor_id"`
    EntryStatusKey string `json:"entry_status_key"` // 进入本阶段时 status
    ExitStatusKey  string `json:"exit_status_key"`
    DispatchCount int `json:"dispatch_count"`
    AdvanceCount  int `json:"advance_count"`
    EnteredAt time.Time `json:"entered_at"`
    FinishedAt *time.Time `json:"finished_at,omitempty"`
    Stages []WorkflowStage `json:"stages"`
}
func (s *WorkflowService) Start(ctx, ws, workItemID, actorUserID string) (*WorkflowState, error)   // auto/manual 共用；已在途/已完成返回现 state
func (s *WorkflowService) StateOf(ctx, ws, workItemID string) (*WorkflowState, error)              // 无则 nil,nil
func (s *WorkflowService) Restart(ctx, ws, workItemID, actorUserID string) (*WorkflowState, error)
func (s *WorkflowService) Detach(ctx, ws, workItemID, actorUserID string) error
// 推进内核（W2 的 eventbus 监听器调；CAS 语义见设计 §4.1）
// 返回 nil 表示未推进（任一条件不满足）；推进成功返回新 state
func (s *WorkflowService) TryAdvance(ctx, ws, workItemID string) (*WorkflowState, error)
```

TryAdvance 的裁决逻辑（W2 实现，W1 不做）：设计 §2.1 三条件 + §4.3 存在性检查 + CAS UPDATE + transition_log + **不改 status**。

## 3. HTTP 契约（W1 实现；前端按此写 api 层）

路由挂 `internal/work/adapters/http/routes.go`，JSON 形状**逐字段冻结**：

```
PUT  /api/projects/{projectId}/workflow
  req:  {"trigger_mode":"auto|manual","entry_stage_position":1,
         "stages":[{"position":1,"name":"...","exit_status_key":"todo",
                    "executor_type":"agent|squad|none","executor_id":"uuid?","instruction":"..."}]}
  resp: {"workflow":{...definition, "stages":[...升序...], "enabled":true}}
  err:  400 validation（exit_status_key 不存在/closed、executor_id 空、position 断档/重复、name 重复）
GET  /api/projects/{projectId}/workflow   → {"workflow":{...}|null}
DELETE /api/projects/{projectId}/workflow → 204
POST /api/projects/{projectId}/workflow/enable | /disable  → {"workflow":{...}}

GET  /api/work-items/{workItemId}/workflow
  resp: {"state":{WorkflowState 如 §2}|null}   ← state 内嵌 stages（升序）供前端渲染进度
POST /api/work-items/{workItemId}/workflow/start   → {"state":{...}}
POST /api/work-items/{workItemId}/workflow/restart → {"state":{...}}
DELETE /api/work-items/{workItemId}/workflow       → 204
```

错误统一走现有 writeErr/apierr；WorkflowState.time 用 RFC3339。

## 4. 事件契约（W2 消费）

- 监听事件：`work_item.updated`、`work_item:updated_by_agent`、`task.completed`、`task.failed`、`task.cancelled`（经 eventbus；**payload 可能是 nil**——`decodeOutboxPayload` 只解码部分事件，监听器只取 `e.WorkspaceID` 与 payload.ID 兜底，**work_item_id 缺失时直接返回**；W3 在 `cmd/orkome-server/main.go` decodeOutboxPayload 补 work_item 系 → `WorkItemPayload`）
- 监听器动作：收到任一事件 → 对该 work_item 调 `TryAdvance` → 返回非 nil state 且是新阶段 → 调 §5 派发
- 归属：`cmd/orkome-server/wiring_workflow.go` 新建 wiring（main.go 一行调用），**不改 wiring_automation/wiring_orchestration 内部**

## 5. 派发接缝（W2；调既有入口，不许新造入队路径）

WiringWorkflow 持有 orchapp.EnqueueService，按 executor_type：
- `agent` → `EnqueueAssignment(ctx, ws, wi, agentID, workflow.CreatedBy)`；返回 reason `already_active` → `repo.MergeIntoPending(ws, wi, agentID, "")`（合并，transition_log note=coalesced）；`blocked`/`model_not_configured` → WriteNotice 通知 created_by，不派发
- `squad` → `EnqueueSquadTask(ctx, ws, wi, squadID, workflow.CreatedBy)`
- `none` → 不派发，WriteNotice 通知 created_by + 订阅者
- 任务 context 注入（**engine 侧 W3 消费此形状**）：
```json
{"trigger":"workflow_stage","workflow_id":"...","stage_id":"...","stage_name":"...",
 "stage_position":2,"stage_exit_status":"in_review","instruction":"...","work_item_title":"..."}
```
- 注意：EnqueueAssignment 内部已带 pending fence（already_active），无需 W2 重复判

> ★ W3 联调实证修订（缺陷修复记录）：EnqueueAssignment 的 context **硬编码** trigger=assignment（service.go:598），§5 形状无法经它注入——而 §4.3 存在性谓词按 context->>'stage_id' 匹配，丢了 stage_id 流程推不动。修复：wiring_workflow.go 的 agent 分支改为 **wiring 层直插 InsertTask**（复刻 EnqueueAssignment 四步语义：GetAgent 校验→模型配置校验→CountPendingFence→InsertTask 带 §5 context；23505 撞唯一索引按 coalesced 收敛）。模式先例 = wiring_automation.go 的 wakeupEnqueuer。TriggerEvidenceKind='workflow_stage'（新值）。
> ★ 契约对齐修订：PUT /api/projects/{projectId}/workflow 的 `name` 字段**可选**（不传后端回落 "Workflow"）；`trigger` context 值域新增 `workflow_stage`。

## 6. 前端契约（W1 前端做定义编辑器；W3 前端做消费侧）

- `web/src/api/workflow.ts` 导出：`getWorkflow(projectId)` / `upsertWorkflow(projectId, input)` / `deleteWorkflow(projectId)` / `setWorkflowEnabled(projectId, enabled)` / `getWorkItemWorkflow(workItemId)` / `startWorkItemWorkflow(workItemId)` / `restartWorkItemWorkflow(workItemId)` / `detachWorkItemWorkflow(workItemId)`——类型按 §3 JSON
- W1 前端交付：项目详情处的「流程」区块（空态引导 + 编辑器：阶段列表增删/上移下移/每阶段 执行者选择（agent/squad/无——数据用现有 agent/squad 列表 API）+ 出口状态下拉（现有 work-item-statuses API）+ 指令 textarea + 启停开关）
- W3 前端交付：工作项详情侧栏「流程」区块（当前阶段 ●●○○ + 执行者 + transition 时间线折叠 + restart/detach 按钮）+ 看板/列表卡片角标 `S2/4`（有 state 时）
- i18n：zh/en 双语键，前缀 `workflow.`；样式贴现有区块（shadcn/tailwind），不引入新依赖

## 7. 通用纪律（每个 agent 任务书必带）

- 门禁：改动包 `go build ./... && go test ./internal/work/... ./cmd/...`；DB 集成测试需 `TEST_DATABASE_URL=postgres://postgres:orkome_e2e@localhost:54329/orkome?sslmode=disable`（容器 orkome-test-pg 若未起：`docker start orkome-test-pg`）；前端 `pnpm -C web build` + `pnpm -C web test`
- SQL 纪律：新表 SQL 只进迁移文件 + arch/sql_allowlist.yml work 域段；repo 层禁跨域表（work 域只摸自己的 4 张表 + work_item/project 只读）
- 已知噪音勿修：golangci stdlib 报错、CRLF gofmt 全量误报、web lint 的 project-nav.tsx 预存 error、chunk>500kB 警告
- 中文 body 测试一律 `--data-binary @file`（GBK 控制台坑）
- 边产出边写 `docs/wf-<agent>-notes.md`（中断可续）
