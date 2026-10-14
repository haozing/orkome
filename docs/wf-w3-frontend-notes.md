# W3 前端交付笔记（wf-w3-frontend）

> 日期：2026-10-13 · agent：W3 前端（工作项详情流程区块 + 摘 mock）
> 依据：docs/wf-w1-frontend-notes.md（交接）+ docs/wf-w2-notes.md（关键裁决/给 W3）
> 契约：docs/wf-interfaces-frozen.md §2/§3/§6；设计 docs/project-workflow-design-2026-10-13.md §6.2。
> 已完成真后端联调（dev server + dev PG），curl 证据见 §5。

## 1. 文件清单

| 文件 | 动作 | 说明 |
|---|---|---|
| `web/src/api/workflow.ts` | 重写 | **删 USE_MOCK/mockStore 整段**（~90 行），八函数直连真后端；`getWorkItemWorkflow` 加 404→null 归一 |
| `web/src/components/workflow/workflow-panel.tsx` | 新建 | 工作项详情侧栏「流程」区块（§3） |
| `web/src/routes/_app.work-items.$id.tsx` | 追加 | 仅 2 处：+1 import、`<InfoPanel>` 上方挂 `<WorkflowPanel workItemId projectId>`（零重构既有布局） |
| `web/src/i18n/locales/zh.ts` `en.ts` | 追加 | 仅追加 `workflow.panel.*` 键段（zh 1021-1040 / en 对应段），既有键零改动 |
| `docs/wf-w3-frontend-notes.md` | 新建 | 本文件 |

未动：workflow-editor.tsx（getWorkItemWorkflow 移除 mock 后签名不变，无需同步修）、work-item-list.tsx（角标降级，见 §6）。

## 2. 摘 mock

- `USE_MOCK` 开关、`mockStore`、`seedMock`/`mockGet`/`mockUpsert`/`mockDelete`/`mockSetEnabled`、四函数的 `if (USE_MOCK)` 分支全部删除。
- `getWorkItemWorkflow`：**后端未挂**（W2 notes「给 W3」明示）→ catch `ApiError.status===404` 返回
  null（其余错误照抛）。ApiError 由 `@ploykit/client` 对非 2xx 抛出、`.status` 保留（读 dist 确认）。
  后端将来挂载该端点后**此处零改动**——404 分支自然不再命中；`{state:null}` 语义也已兼容。
- 编辑器路径的 `getWorkflow` 保持原语义：契约 §3 `{"workflow":null}`；实测后端无定义时确返回
  `{"workflow":null}`（200），无 404 分歧，W1 notes §6.5 的悬置项就此关闭。

## 3. workflow-panel.tsx（设计 §6.2 触点 2）

结构（贴 InfoPanel 侧栏样式，`pl-5 border-l` 由父容器 flex 排布衔接）：

- **有 state**：当前阶段名 + `S{n}/{total}` + 进度点（●●○○，state 内嵌升序 stages 对照
  stage_position；孤儿 state stages 空则不渲染）+ 执行者行（executor_type 图标+文案：agent=Bot /
  squad=Users / none=User，**v0 不做 agent/squad 名反查**——现有 `['agents']`/`['squads']` 缓存
  hook 虽在，但 squad→leader 反查语义未冻结，避免半吊子）+ 进入时间 + restart/detach 按钮
  （行内红底 confirm 条，复用编辑器删除条样式）。
  - `finished_at` 非空 → 绿色「已完成」徽标替代进度点。
- **无 state**：空态文案 + 「开始流程」按钮；**start 返回 400/404 后按钮隐藏**并显示
  「项目没有可用流程」提示（项目无流程 = Start 返回 404 NotFound、工单未挂项目 = 400 Validation，
  实测见 §5）。不用 W1 的 per-project workflow 缓存做前置判断——单工单页面不值得为按钮显隐多拉
  一个项目级查询，错误降级一次即收敛（组件实例存活期 startUnavailable 稳定）。
- **数据事实源**：GET 未挂 → 区块状态 = 首查 404→null（空态）+ start/restart 响应 setQueryData。
  restart/detach 的成功回包/空态回写本地缓存并 invalidate。
- **自动刷新（零新增接线）**：main.tsx 的 ws-query-bridge 对 `work_item:*`/`task:*` 帧已 invalidate
  `['work-item']` 前缀；本区块查询 key 取 `['work-item', workItemId, 'workflow-state']` 挂在其下，
  Start/阶段推进/任务终态的 WS 事件自动触发 refetch。查询 `retry:false + staleTime:Infinity`
  （GET 恒 404 时不打后端；WS 桥的 invalidate 对 staleTime:Infinity 仍强制 refetch，语义正确）。
- transition 历史：**v0 不做**——契约 §2 WorkflowState 无 log 列表（设计 §6.2 的"GET state +
  transition 历史"在后端只兑现了 state），等后端补投影再做，见 §7 follow-up。

## 4. 联调冒烟（dev server，全部真实 HTTP）

环境：dev PG（orkome-pg:55432）+ 我起的 server（`go run ./cmd/orkome-server`，**PID 24812，已杀**；
migrations pending 1044 先 `go run ./cmd/migrate up` → applied 1044）。

curl 证据（cookie jar + 每次写请求带登录后重新 mint 的 X-CSRF-Token，见 §8）：

```
PUT /api/projects/003fdb7e…/workflow            → 200 {"workflow":{...2 阶段 agent→none...}}
POST /api/work-items/9221e391…/workflow/start   → 200 {"state":{"stage_position":1,"stage_name":"S1-clear",
                                                  "executor_type":"agent","entry_status_key":"todo",...stages 升序...}}
POST …/workflow/restart                         → 200 {"state":{... 回 S1、dispatch_count=0 ...}}
DELETE …/work-items/9221e391…/workflow          → 204
DELETE /api/projects/003fdb7e…/workflow         → 204
```

任务落库（docker exec psql）：

```
agent_task_queue：1 行 work_item_id=9221e391…  agent_id=0284b43f…  trigger_evidence_kind='assignment'
  context: {"trigger":"assignment","actor":…,"work_item_title":"W3 smoke workflow item",…}
workflow_transition_log：3 行——(from=NULL,actor=user) → note='restarted,coalesced' → note='detached'
work_item_workflow_state：finished_at 非空（detached 冻结）✓
```

冒烟工单已删（DELETE /api/work-items/9221e391 → 204），不留脏数据。

## 5. 联调发现（**需主会话裁决/后端跟进，前端未擅自处理**）

1. **★ 生产 dispatcher 丢了 §5 stage context（W2 后端缺口，前端冒烟实证）**：
   `agent_task_queue.context` 实测 = `{"trigger":"assignment",...}`，**没有**
   `stage_id/stage_name/stage_position/stage_exit_status/instruction`。根因：
   `wiring_workflow.go` agent 分支直接调 `EnqueueAssignment`（该函数 context 固定
   `trigger:"assignment"`），**契约 §5 的 context 注入在生产 wiring 没有落地**——W2 集成测试
   13/13 绿是因为测试注入了自带 stage_id 的 fake dispatcher（workflow_engine_integration_test.go:84,172）。
   连带后果：`SetStageTaskTerminal` 谓词按 `context->>'stage_id'` 匹配 → 生产环境**永远匹配不到**，
   TryAdvance 条件 2（§4.3 任务背书）恒不成立 → **阶段永远无法推进**。这是引擎面（W2/W3-engine）
   的事，不在前端任务书范围，只记录。
2. **PUT 请求体必须带 `name`（1-100 chars）**：契约 §3 说"无 name 字段——流程名由后端管理"，
   实测不带 name → `400 invalid workflow name`。W1 编辑器保存**不带 name** → 对新项目创建流程会
   400。前端侧修复属契约漂移（改 WorkflowUpsertInput 加 name），**需主会话拍板**（W1 notes §3
   预言过这条）；临时绕法：项目先经后端建带 name 的定义。
3. **CSRF 联调配方（实测修正，供后续 agent 复用）**：任务书配方只说"GET /config 取 csrf_token +
   cookie jar"，实测**登录会轮换 ploykit_csrf cookie**，登录前取的 token 必失效（403）。正确顺序：
   ① GET /config（jar 播种）→ ② POST /auth/login（用①的 token）→ ③ **再 GET /config 取新 token**
   → 后续写请求全部用③。gorilla 语义：cookie=真值，X-CSRF-Token=masked 值，/config 的
   `csrf_token` 字段即 masked 值可直接用。
4. 冒烟期间 dev 环境真实 agent（gpt-4o-mini → api.openai.com）网络不通，任务 run 失败
   （`engine execute failed … dial tcp …api.openai.com`）——与流程引擎无关，不影响本波次验收。

## 6. 看板/列表角标：明确降级（follow-up）

`GET /api/work-items` 列表投影（`WorkItem` 接口 + 后端 ListQuery）**不含 workflow state**；
逐行调 `GET /work-items/{id}/workflow` 不可行（端点当前 404，挂载后也是 N+1）。
`WorkItemRow`（web/src/components/work-item/work-item-list.tsx）表格列结构清晰、加角标本体容易，
但数据源缺失，硬塞 = 每行打一个 404。**降级为 follow-up**：后端 ListQuery 加
`workflow_stage_position/workflow_stage_total` 投影（独立小任务）→ 前端在 title 列追加
`S2/4` 小徽标（i18n 键已备好：`workflow.panel.stageProgress`）。

## 7. Follow-up 清单（后波次认领）

1. **后端**：挂 `GET /api/work-items/{id}/workflow`（StateOf 已实现，挂载 + routeTable 登记即可）；
   前端 api 层/组件零改动自动升级为可刷新读面。
2. **后端**：dispatcher 补 §5 stage context 注入（§5.1，引擎侧）。
3. **后端 + 契约**：PUT name 字段裁决（§5.2）；前端把编辑器 name 输入接进 WorkflowUpsertInput。
4. **后端**：ListQuery 加 workflow_state 投影 → 前端做看板角标（§6）。
5. **前端**：transition 时间线折叠列表（等 state 投影带 log）。
6. **前端**：executor 名反查（agents/squads 缓存已有；squad→leader 语义确认后接）。

## 8. 门禁输出（2026-10-13）

```
pnpm -C web build  → ✓ built in 2.49s（仅既有 chunk>500kB 警告=已知噪音）
pnpm -C web test   → Test Files 25 passed (25) · Tests 237 passed (237)
npx eslint src/api/workflow.ts src/components/workflow/ src/routes/_app.work-items.$id.tsx
                   → 零输出零 finding（exit 0）
```

dev server（我起的，PID 24812）已 taskkill；端口 8080 空出（残留在跑的 `go run ./cmd/full-server`
是其他会话的进程，未动）。冒烟产生的工单数据已删；workflow 定义已 DELETE（204）。
