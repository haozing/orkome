# 下一波实施计划：把"缝"接上（2026-10-15）

> 依据：`docs/grokbot-code-analysis-2026-10-15.md`（Grok 0.18 源码级分析的 P1/P2 清单）+ chat 退役（`ef950d0`）清空的历史包袱
> 状态：待实施（W1 双件可并行）
> 边界：integrations 页重构在途（另一路工作），文件避让清单见 §5

---

## 0. 为什么是这几件

判据链（来自源码分析结论）：**orkome 与 Grok Bot 的机制层大体同代，差距不在能力、在"缝"**——已有能力没有缝成零摩擦线。选最短的两条缝 + 一条冷启动缝：

1. **审批的缝**：AI 卡住等你 → 今天要 3 跳（铃铛 → 详情弹窗 → 工单 → 执行 tab → 展开任务展开批准）。更糟：审批通知连"打开工作项"都点不动——`work_item_id` 压根没写入（见 §2 证据）。Grok 的审批卡是流内 Allow/Deny，一跳。
2. **派活的缝**：Quick Actions 后端 6 端点全绿、Web 零消费——"说一次、反复用"的零门槛入口躺在仓库里没人接。
3. **冷启动的缝**：新工作区建完只有空板（`onboarding.tsx:66` → `/work-items`），无引导；`quickCreateWorkItem` 有 API 无 UI（`api/work.ts:68`）。

---

## 1. 波次总览

| 波 | 内容 | 尺寸 | 依赖 | 与在途冲突 |
|---|---|---|---|---|
| W1-1 | 通知内联裁决（含 work_item_id/task_id 补写） | M（后端小迁移 + 前端弹窗） | 无 | 无 |
| W1-2 | 快捷动作 Web 面（建/管/跑） | M（纯前端 + 新 API 封装） | 无 | 无 |
| W2 | 起步清单（空态即第二幕） | S | 无 | i18n `work.*` 与 W1-2 相邻，分前缀 |
| W3 | 引擎纪律（工具错误分类 / 两级截断） | L | 各需先出设计文档 | 无 |

**W1-1 与 W1-2 文件零交集，可双 agent 并行。** W2 可在任意时点插入。

---

## 2. W1-1 通知内联裁决（P1，最高优先）

### 现状与证据

- 写入口：`internal/orchestration/app/service.go:889` `park()` 调 `WriteApprovalRequest(ctx, ws, userID, taskID, payload)`；实现 `internal/notification/adapters/pgrepo/repo.go:260-271` 的 INSERT **不带 `work_item_id` 与 `task_id`**。
- 后果：通知详情弹窗（`web/src/components/notification-detail-dialog.tsx:82`）「打开工作项」按钮对审批通知恒不导航（`work_item_id` 空）；裁决只能去 工单 → 执行 tab → 展开任务。
- 裁决 API 与前端函数**都已存在**：`POST /api/tasks/{taskId}/approve|deny`（`orchestration/adapters/http/routes.go:102`）、`settleTask(taskId, approved, note?)`（`web/src/api/orchestration.ts:346`）——缺的只是把 task_id 送到通知弹窗。

### 改动

1. **迁移 1048**（号段避开并行在途的 1047）：
   `ALTER TABLE notification ADD COLUMN IF NOT EXISTS task_id uuid;`
2. **写入口签名**：`ports.go:930` 补 `workItemID` → `WriteApprovalRequest(ctx, ws, userID, workItemID, taskID, payload)`；repo 实现写 `work_item_id` + `task_id` 两列；caller `service.go:889` 传 `task.WorkItemID`。
3. **投影**：产品列表查询 `ListNotification`（`internal/notification/adapters/pgrepo/repo.go:61`）SELECT 加 `coalesce(task_id::text,'')` → `app.Item`（`internal/notification/app/service.go:13`）加 `TaskID` 字段 → handler DTO 与 wire 序列化同步。
4. **前端**：
   - `api/notification.ts` `NotificationItem` 加 `task_id?: string`；
   - `notification-detail-dialog.tsx`：`type === 'approval_request' && task_id` 时在 footer 渲染「批准」（primary）「拒绝」（destructive outline）——调 `settleTask` → 成功后就地置完成态（禁用 + 「已批准/已拒绝」）→ 失效 notifications 与 `['work-item', wi, 'tasks']` 查询；
   - i18n `notif.*` 加 4 键（批准/拒绝/已批准/已拒绝）。
5. **测试**：Go——repo 写读回环（写 approval 通知后 List 带 task_id/work_item_id）；前端——dialog 测试（msw mock approve + 断言按钮出现/点击/完成态）。

### 验收

- 单测全绿；
- 真机闭环：让任务触发引擎 `request_approval`（`orchestration/adapters/engine/tools.go:38`）park → 铃铛点开 → 弹窗直接批准 → 任务续跑；「打开工作项」按钮现在能跳；
- 无真模型兜底：SQL 插一条 `type='approval_request'` 带 task_id 的通知验证 UI。

### 注意

- 存量通知该列为 NULL——前端按 optional 处理，**不回填**；
- approve 要求任务 running（`SettlementService.Approve` 前置校验），parked 任务保持 running，无需动；
- 24h 审批超时上限不变。

---

## 3. W1-2 快捷动作 Web 面（P1）

### 现状与语义

- 后端 6 端点全绿零消费：`conversation/adapters/http/routes.go:117-125`；
- 语义（照抄勿猜）：workspace+user 私有（Create 强制 `IsPrivate=true`，`service_quickaction.go:88`）；每用户上限 3（`MaxQuickActions`）；Run 解析**工单负责人**为执行 agent——负责人不是 agent（含小队）时 400 `"work item has no agent assignee"`（`service_quickaction.go:71`）。

### 冻结 wire（实现时逐字段对照）

| 端点 | 请求 | 响应 |
|---|---|---|
| GET `/api/quick-actions` | — | `{quick_actions: [{id,label,prompt,is_private,created_at}]}` |
| POST `/api/quick-actions` | `{label,prompt}` | 201 单对象 QuickAction |
| PATCH `/api/quick-actions/{id}` | `{label,prompt}` | 200 单对象 |
| DELETE `/api/quick-actions/{id}` | — | 200 `{result:"deleted"}` |
| POST `/api/work-items/{wi}/quick-actions/{id}/run` | — | 201 `{task_id}` |
| POST `/api/work-items/{wi}/quick-actions/{id}/render` | — | 200 `{prompt}` |

### UI 落位

工单详情页编辑区（评论上方）新增「快捷动作」行：

- **跑**：chips=已存动作（≤3），点击 → run → toast「任务已派发」+ 切执行 tab（`patchSearch {tab:'tasks'}`）+ 失效 `['work-item', id, 'tasks']`；
- **管**：行尾「管理」popover——新建（label + prompt 两输入；label ≤80 字面前置校验）、编辑、删除；达 3 条禁用新建；
- **显示条件**：负责人非 agent 时整行隐藏（无动作面不占位）——避免造出"点了必 400"的按钮；
- **空态**：有 agent 负责人但无动作 → 仅显示「＋ 保存常用指令」引导（进管理 popover）。

### 文件

- 新 `web/src/api/quick-actions.ts`（6 函数 + 类型）；
- 新 `web/src/components/work-item/quick-actions-row.tsx`；
- 挂载 `web/src/routes/_app.work-items.$id.tsx`；
- i18n `work.qa.*`（约 12 键）。

### 验收

- msw 组件测试：列表渲染 / run 调用 + 切 tab / 管理 CRUD + 上限 3 禁用；
- 真机：工单指派 `deepseek-e2e` → 存一条动作 → 点击 → 执行 tab 出现新任务、真模型跑完 outcome 沉淀。

---

## 4. W2 起步清单（空态即第二幕）

现状：建/入工作区后 `navigate('/work-items')`（`onboarding.tsx:66,86`）→ 空板干瞪眼；空态只有文案。

改动（单文件为主）：`_app.work-items.index.tsx` 空态升级为三件套卡片：

1. **建第一个工单**——单字段（标题）快速创建（复用 `quickCreateWorkItem`），创建后直接进详情；
2. **接一个能力包**——链 `/tools`（集成页；在途重构完成后即为最顺入口）；
3. **邀请队友**——链 `/settings` 工作区段。

- i18n `work.start.*`；
- **不做独立 onboarding 页**——空态卡片=天然第二幕，零新路由。

验收：新账号建工作区 → 空态三件 → 点「建第一个工单」→ 单字段创建成功进详情。

---

## 5. W3 引擎纪律（排期，需先出设计文档）

| 项 | 内容 | 来源 |
|---|---|---|
| W3-1 pack 工具 error taxonomy | pack.yaml `mcp.tools` schema 加 `errors` 声明 + 安装期校验 + 引擎按分类决定重试/上报/回模型 | grokbot §P2-5 |
| W3-2 大结果两级截断 | task_message 2000 rune 截断升级为"流内截断 + 落盘附件 + 定位符"（与 dsh spill 同族） | grokbot §P2-4 |

两件各需独立设计文档（涉及 pack 契约冻结与附件域消费），本计划只排期。建议顺序：W3-2 先（首个真实大结果场景出现即可动）或 W3-1 先（pack 生态上量时）。

---

## 6. 并行纪律（integrations 在途避让）

在途文件（另一路，本轮**勿碰**）：
`web/src/components/integrations/**`、`packs-sections.tsx`、`_app.tools.tsx`、`_app.settings.tsx`、`pack-detail.tsx`、`api/packs.ts`、i18n `tools.*`/`settingsWs.*` 段、`migrations/1047_*`、`internal/orchestration/**/uientry*`

- W1 触及面与上述零交集；**迁移用 1048**；i18n 只动 `notif.*` 与 `work.qa.*`/`work.start.*`；
- W1-2 与 W2 都动 `work.*` i18n 与工作项路由——若并行，两批 key 分前缀互不重叠（`work.qa.*` / `work.start.*`）。

---

## 7. 验证门（每波）

- `go build ./cmd/... ./internal/...` + `go vet` 绿；`go test -count=1 ./cmd/... ./internal/...` 绿；
- `cd web && pnpm vitest run test/` 全绿（当前基线：28 文件 249 测试）；
- **e2e 契约表零变更**（三波均无新端点；W1-1 仅列投影变化，e2e registry 不动）；
- 真机：IAB 走查 + 截图自查（审批闭环 / 快捷动作跑通 / 起步三件）；
- commit 按域拆（W1-1、W1-2、W2 各一笔），push 前先 fetch。

---

## 8. 缓办（触发式，不排期）

| 项 | 触发信号 | 来源 |
|---|---|---|
| steering 沉淀（人沉淀/模型消费） | 同一 agent 反复纠同一件事 | grokbot §P1-3 |
| fail-closed 渲染审计 | 前端出现假状态事故 | grokbot §P1-2 |
| 元工具预案（call_mcp_tool） | 单 server 工具数破阈 | grokbot §P2-6 |
| 首页 AI 动态流 | 团队版上量 | 本会话"缝"分析 |
| 评论/审批提交离线队列 | 弱网/移动端场景 | grokbot §P2-7 |

---

## 9. 明确不做

chat 复活（已退役 `ef950d0`）、常驻 VM/屏幕接管、per-bot 自动记忆、in-flight 摘要压缩、桌面端形态（语音/托盘/全局快捷键）、pack 模板市场。
