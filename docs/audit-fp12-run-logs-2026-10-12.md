# FP-12 功能点审计：run-logs 页与任务转写抽屉（TaskTranscriptDrawer）

> 日期：2026-10-12 · 审计人：FP-12 agent · 方法：七步审计法（全链梳理→契约对齐→curl 实测→代码质量→后端正确性→测试覆盖→缺陷清单）
> 范围：`web/src/routes/_app.run-logs.index.tsx` 全部（4 个状态 tab、运行列表、TaskTranscriptDrawer、与 work-item 任务面板的边界）；后端 `GET /api/tasks`、`GET /api/tasks/{taskId}/messages`、`GET /api/tasks/{taskId}`、`GET /api/work-items/{id}/usage`
> 环境：后端 :8080（迁移 1036）真实登录实测；220 条消息复现样本 = 工单 #1（盘点验证工单-20261012，3b8bc342）下的任务 `11111111-1111-4111-8111-111111111102`
> 已知缺陷引用不重复申报：FP-05 P1-1（消息流最旧 200 盲区）、P3-1/P3-3（渲染不一致/无虚拟化）、batch1 根因 A（members envelope，本页不消费 members，不受影响）

---

## 一、一句话结论

**runs 列表链路质量高（keyset 游标/状态白名单/fence/ awaiting_input 派生全对，实测零漂移），抽屉渲染管线与工单面板完全同源（三卡照常触发）；但抽屉是"死回放"——打开期间无任何实时/重取机制，且 usage/token/结算信息与任务动作（审批/取消）在 run-logs 侧零呈现，usage 端点全站零消费。**

**计数：P0 × 0（新发现）；P1 × 0（新发现，FP-05 P1-1 对抽屉同样命中，实测确认）；P2 × 6；P3 × 6。**

---

## 二、全链梳理（前端 → API → 后端 → repo）

```
run-logs 页（_app.run-logs.index.tsx:45-167）
  ├─ useQuery(['agents']) listAgents ──────────► GET /api/agents（routes.go:231，envelope {agents} 正确解包 orchestration.ts:120-125）
  ├─ useInfiniteQuery(['workspace-tasks',{status,agent_id}])
  │    listWorkspaceTasks（orchestration.ts:227-241，不传 limit/fields）──► GET /api/tasks（handlers_taskview.go:116-149）
  │    ──► parseTaskStatusFilter（:76-114 白名单）──► Repo.ListWorkspaceTasks（pgrepo/repo.go:1537-1612，keyset 游标）
  ├─ 4 tab：active/failed/completed/all（:30-35），status 写进 URL search（:18-29）
  └─ TaskTranscriptDrawer（:227-306，openTask useState 挂载，:164）
       ├─ useQuery(['task',id,'messages']) listTaskMessages（orchestration.ts:248-253，恒 limit=200 无 order 无游标）
       │    ──► GET /api/tasks/{taskId}/messages（routes.go:317-362）──► Repo.ListTaskMessages（repo.go:748-783）
       ├─ TaskMessageList（task-messages.tsx:62-75）──► ToolResultRenderer（task-tool-renderers.tsx 注册表）★与面板同源
       └─ useAgentNameMap（task-status-badge.tsx:34-41，与页共享 ['agents'] 缓存）
```

### 与 work-item 任务面板（task-panel.tsx）的管线一致性

| 维度 | 面板 TaskDetail | run-logs 抽屉 | 一致？ |
|---|---|---|---|
| 消息查询 | `['task',id,'messages']` + listTaskMessages（task-panel.tsx:120-123） | 同 key 同 fn（run-logs:237-240） | ✅ 同管线、react-query 缓存互通 |
| 渲染器 | TaskMessageList → ToolResultRenderer 三卡 | 同一组件（run-logs:301） | ✅ 三卡在抽屉同样触发，job 深链/ui-entries/metrics 全生效 |
| 实时订阅 | `useWsScope('task', id)`（task-panel.tsx:119） | **无**（全仓唯一 task scope 订阅点就是面板） | ❌ P2-1 |
| 失效联动 | settle/cancel onSuccess invalidate + WS 帧→main.tsx:17 规则 | 无动作可失效；无订阅则帧也不来 | ❌ |
| 审批/取消 | approve/deny/cancel 按钮（task-panel.tsx:140-173） | **只读，无任何动作** | ❌ P2-5 |
| result 渲染 | Markdown（task-panel.tsx:179） | `<pre>`（run-logs:285） | ❌（FP-05 P3-1 已报） |
| failure_reason | 不显示 | 红块显示（run-logs:267-280） | 互补（FP-05 P3-1 已报） |

结论：**互补大于重复**（run-logs 覆盖全工作区含直聊任务/工单已删任务；面板只覆盖本工单），但抽屉是面板的"功能子集 + 只读"，见 P2-1/P2-5。

---

## 三、契约逐字段对齐

### 3.1 GET /api/tasks（runs 列表，handlers_taskview.go:116-149）

| 字段/参数 | 后端行为 | 前端消费 | 判定 |
|---|---|---|---|
| `status` | 白名单校验（:80-86），`active`→queued/dispatched/running/awaiting_input（:96-104），`all`→不过滤；非法值 400 E_VALIDATION | 4 tab 映射正确（run-logs:59，all→undefined） | ✅ |
| `agent_id` | trim 后进 WHERE（repo.go:1557-1560）；归档 agent 仍在列表（ListAgents 不滤 archived，repo.go:81-83） | select 下拉（run-logs:101-113） | ✅ 实测 5/0 |
| `cursor` | `(created_at,id)` keyset DESC（repo.go:1561-1568,1584）；坏游标 400 | `getNextPageParam: next_cursor`（run-logs:64）+ load more（:151-162） | ✅ 实测翻页衔接正确 |
| `limit` | ≤0 或 >100 → 50（repo.go:1538-1541） | **不传** → 每页 50 | ✅（>100 静默回落 50，非钳到 100，小疵） |
| `fields=slim` | 清空 result/error/context（:137-143） | **从不使用** | ❌ P2-3 |
| 响应 `tasks[]` | 24 字段含 agent_name/work_item_title（LEFT JOIN，repo.go:1571-1579），work_item 删除→title 空→前端显示"（工作项已删除）"（run-logs:188-193） | WorkspaceTaskRow | ✅ |
| `next_cursor` | 空串→JSON null（:144-148） | `?? undefined` | ✅ |

### 3.2 GET /api/tasks/{taskId}/messages（routes.go:317-362）

| 参数 | 后端 | 前端 | 判定 |
|---|---|---|---|
| `limit` | 默认 200（:323）；>500 在 repo 静默回落 200（repo.go:749-751） | 恒 200（orchestration.ts:248） | ✅ 但见 P3-5 |
| `order=desc` | 支持（:327-329） | **不用** | ❌（FP-05 P1-1 修复面） |
| `before_seq` | 支持+400 校验（:330-337）；`next_before_seq` 仅 desc 且满页返回（:358-360） | **不用**（orchestration.ts 无分页形参） | ❌ 同上 |
| `kind` | 逗号分隔子集校验 400（:338-348） | 不用 | 后端能力闲置 |
| ws fence | memberMW 校验 header 并 `p.WorkspaceID=ws`（routes.go:200-224）；repo `WHERE workspace_id=$1 AND task_id=$2`（repo.go:768） | — | ✅ 双层，实测 403 |

### 3.3 run 详情 / usage（审计范围问项"usage/token 统计？结算信息？"）

- **任务详情** `GET /api/tasks/{taskId}`（handlers_taskview.go:231-242）：存在、fence 硬（实测他区 header 403）、**前端零消费**（抽屉只用列表行快照，不回源刷新）。
- **usage**：数据模型完整——`task_usage`（prompt/completion_tokens、cost_usd_ticks、provider/model、model_retries/failovers，repo.go:398/1277/1397 写入）+ `task_usage_hourly` rollup（repo.go:1406-1434）+ 聚合端点 `GET /api/work-items/{id}/usage`（handlers_usage.go:11-22，实测返回四段结构）。**但：① 无 per-task usage 端点；② 全站前端零消费（grep 仅 i18n 命中）；③ run-logs 列表/抽屉不展示任何 token/费用**。→ P2-2。
- **结算（settle）信息**：抽屉显示 awaiting_input 徽标（TaskStatusBadge）但无 approve/deny 动作；`POST /api/tasks/{taskId}/approve|deny`（routes.go:84-85）全站唯一消费方是 task-panel.tsx:129——直聊任务（无 work_item）无法到达面板 → 直聊任务待审批**无处可批** → P2-5。
- **失败原因**：列表行红字 failure_reason（run-logs:210-214）+ 抽屉红块 failure_reason+error（:267-280），chat 任务实测（`agent_provider.network`）展示正确。✅

### 3.4 触发类型映射（TRIGGER_KEYS，run-logs:36-44）

后端实际写入的 evidence kind 全集：`assignment`（service.go:517/566）、`chat`（:941/967）、`squad`（:999）、`member_comment`（service_trigger.go:251）、`rerun`（handlers_taskview.go:300）、`leader_dispatch`（ports.go:766/784）、`rule`（wiring_automation.go:142）、`delegated`（InsertRecoveryTask，repo.go:1020）。前端映射缺 **`delegated`** → 恢复任务行内裸显英文 kind。→ P3-1。

---

## 四、curl 实测记录（:8080，inventory-test@orkome.dev @ inventory-ws）

```
1. 登录 → GET /api/tasks
2. 4 tab 实测（当日计数）：active=1（running）、failed=2、completed=2、all=5 —— 加总一致 ✅
   （注：测试期间他 FP agent 并发写同一工作区，数字随时间 4→11 漂移，各 tab 口径始终自洽）
3. 非法 status=bogus → 400 {"error":"E_VALIDATION","message":"unknown task status: bogus"} ✅
4. cursor=garbage → 400 ✅；无 X-Workspace-Id → 400 ✅；他区 header（本区任务）→ 403 ✅
5. keyset 翻页 limit=2：p1=[ba6b335e,a4a45a0e] cursor=2026-10-12T04:08:27.943734Z|a4a45a0e…
   p2=[6918631c,11111111…] —— 时间序衔接正确、无重无漏 ✅
6. agent_id 过滤：真实 agent → 5 行；bogus uuid → 0 行 ✅
7. limit=101 → 实际按 50 钳（返回当时全量 11 行，未越界）✅
8. 220 条消息任务（…1102）抽屉实拉形状 ?limit=200（无 order）：
   → 返回 seq 1..200，无 next_before_seq 字段；seq 201-220（含尾段/最终输出）不可见
   —— FP-05 P1-1 在抽屉侧实锤复现 ✅（引用不重报）
9. desc+before_seq 人工走链：200+20 两页走完 220 条，next_before_seq=21 → 末页 20 条后为 null ✅
   —— 后端分页能力完好，纯前端未接（修复零后端改动）
10. before_seq=abc → 400 ✅；kind=bogus → 400 ✅；kind=text,error → 只回该两类 ✅
11. limit=0 → 200 条（默认）；limit=600 → 200 条（repo >500 回落 200，非钳 500）⚠ P3-5
12. GET /api/work-items/3b8bc342…/usage → {"tasks":0,"total_input_tokens":0,…,"by_provider":[]}
    （dev fake 引擎不产 usage，数据链路通、数值为零）✅；不存在工单 → 404 ✅
13. GET /api/tasks/{taskId}：本区 200；他区 header 403 ✅
```

---

## 五、后端正确性评估

1. **ListWorkspaceTasks（repo.go:1537-1612）**：keyset 游标 `(created_at,id) < (…)` 与 `ORDER BY created_at DESC,id DESC` 严格一致，稳定无重；`awaiting_input` 派生（running+awaiting_input_since，:1550 与 SELECT CASE :1572 两处口径一致）；agent JOIN 保证 agent_name 恒有值。**正确**。
2. **状态过滤 SQL 拼接**（repo.go:1552 `"t.status='"+s+"'"`）：安全性完全依赖 handler 白名单（handlers_taskview.go:80-86）——当前唯一调用方有闸，纵深防御缺失 → P3-5。
3. **fence**：读面双层（memberMW + repo WHERE），实测 403；messages 403 由成员闸先拦。无 IDOR。
4. **usage 口径**：按 task 聚合 join work_item，fence 在内层 `t.workspace_id=$1`（repo.go:1453/1462）✅；`count(DISTINCT u.task_id)` 口径正确。
5. **handleTaskMessages 裸读 header**（routes.go:318-322）而非 `d.ws(r)`：memberMW 已校验同一 header 并写回 principal（:224），行为等价，仅一致性小疵（并入 P3-5 观察项）。

---

## 六、代码质量 / 生命周期 / i18n / a11y

- **抽屉查询生命周期**：`{openTask && <Drawer/>}`（:164）关闭即卸载；staleTime 15s（lib/query.ts:7）内重开吃缓存，>15s 重开才回源。**打开期间无 refetchInterval、无订阅、无手动刷新按钮**（P2-1）。
- **tab 状态持久化**：status/agent 走 URL search（可分享/可回退）✅；坏值 safeParse 失败静默回 `{}`→active（:24-27），可接受。抽屉打开状态不进 URL，刷新即丢（P3-4）。
- **切换筛选**：无 `placeholderData: keepPreviousData`，每次切 tab 整页 PageLoading 闪烁（P3-4）。
- **大转写渲染**：TaskMessageList 一次渲染 200 条全量 Markdown，无虚拟化（FP-05 P3-3 已报，抽屉 max-w-lg 窄容器下更紧）。
- **i18n**：runLogs.*（zh.ts:335-360 / en.ts:465-490）、taskStatus.*（zh:661-670）、taskMsg.*（zh:680+）**双语全齐** ✅；唯一漏网是 delegated 裸显（P3-1）。
- **a11y**：tablist/tab/aria-selected（:77-99）、select aria-label、Dialog.Title 齐备 ✅。
- **抽屉标题**用 useAgentNameMap（含归档 agent，ListAgents 不滤 archived）不会退化成裸 id ✅；但不显示 attempt/max_attempts（列表行显示），重试上下文在抽屉里丢失（P3-2）。

---

## 七、测试覆盖

| 层 | 现状 | 缺口 |
|---|---|---|
| repo：ListWorkspaceTasks | `workspace_tasks_list_test.go:13-69`：字段投影/状态过滤（含 awaiting_input 派生）/两页游标全覆盖，质量好 | agent_id 过滤、坏游标 400、limit 钳制未测 |
| HTTP：GET /api/tasks | **全仓零测试**（grep 仅 trace_test 误命中 POST）——e2e registry_test.go 路由清单里也没有该端点 | **P2-6**：本页主端点在 HTTP 层（成员闸/envelope/游标形参）无任何守卫，与 batch1 "GET /api/work-items 零测试"同款系统性盲区 |
| HTTP：messages | `b2_integration_test.go:213-248`：基础读写+他区 403 | desc/before_seq/kind 三参数**零测试**（next_before_seq 无一字节测试覆盖） |
| HTTP：usage | `handlers_usage_snapshot_test.go:39` shape+404 | 够用 |
| 前端组件 | `task-messages.test.tsx` 16 用例（三卡/兜底/深链）复用良好 | RunLogsPage/TaskTranscriptDrawer **零组件测试**；`web/test/api/` 无 orchestration.test.ts（listWorkspaceTasks/listTaskMessages 封装零测试） |

---

## 八、缺陷清单

### P0（新发现）
无。

### P1（新发现）
无。**继承引用**：FP-05 P1-1（消息流恒最旧 200、尾段含最终输出不可见）对抽屉同样命中——本次以抽屉实际请求形状（`?limit=200` 无 order）实测复现（seq 1..200，无游标字段，201-220 不可见）；后端 desc+before_seq 能力完好（实测两页走链 220 条），修复面在前端约 15 行（orchestration.ts:248-253 加游标形参 + 抽屉/面板接 useInfiniteQuery desc 反转渲染），**与 FP-05 修复为同一处代码，勿分开修**。

### P2（6 项）

**P2-1 抽屉是"死回放"：打开期间转写冻结，无订阅、无轮询、无刷新，且无任何陈旧提示**
- `web/src/routes/_app.run-logs.index.tsx:237-240`（查询）、:227-306（整个抽屉无 useWsScope / refetchInterval / 刷新按钮）。
- 全仓唯一 task scope 订阅点在 task-panel.tsx:119；main.tsx:17 的全局失效规则只在帧到达时起作用，抽屉不订阅则帧永不到达。默认 tab 是"进行中"——主场景恰是看运行中任务，打开抽屉后转写永远停在那一次 fetch；关重开在 15s staleTime 内还吃缓存（lib/query.ts:7）。
- 对直聊任务（无 work_item）抽屉是全站唯一转写入口，冻结问题无处可绕。
- 修复：抽屉挂 `useWsScope('task', task.id)`（一行，桥/失效链全部现成）；对 running 状态加 5-10s refetchInterval 兜底；标题区加"陈旧"提示或刷新按钮。

**P2-2 usage/token/费用全链存在、全站零消费；run-logs 不做 per-run 呈现，且无 per-task usage 端点**
- 数据：task_usage + hourly rollup（repo.go:398/1277/1397/1406-1434）；聚合端点 GET /api/work-items/{id}/usage（handlers_usage.go:11-22，实测形状正确）。
- 缺口：① `grep usage web/src` 仅 i18n 命中——**端点零消费方**；② 抽屉对有 work_item 的任务也不展示 usage（数据就在一次请求内可得）；③ 无 `GET /api/tasks/{id}/usage`（repo 有 InsertMemberUsage 按 task 落数，读面却只能按工单聚合）。
- 修复：抽屉加 usage 条（有 work_item 则复用现端点）；补 per-task usage 读端点（repo 聚合 SQL 改 WHERE u.task_id 即可）。

**P2-3 列表下载全量 result/error/context，后端 slim 旋钮闲置**
- `web/src/api/orchestration.ts:227-241` 不传 `fields=slim`；handlers_taskview.go:137-143 明明支持（清空 result/error/context）。
- runs 列表每页 50 行全量携带 result 与 error 正文（任务 result 即模型最终输出，规模上限与 FP-05 P1-2 的"MB 级 interim"同源），页面只显示 failure_reason 与截断摘要——纯运输浪费，长列表放大。
- 修复：listWorkspaceTasks 默认带 `fields=slim`（行内需要 result 的只有抽屉，抽屉走 GET /api/tasks/{taskId} 回源即可，顺带治 P2-1 的快照陈旧）。

**P2-4 "进行中"口径漂移：deferred 被后端 active 过滤排除、被前端活跃集合包含**
- 后端 active=queued/dispatched/running/awaiting_input（handlers_taskview.go:96-104），**不含 deferred**；前端 ACTIVE_TASK_STATUSES 含 deferred（task-status-badge.tsx:22-28），工单面板视其为活跃（可取消）。
- 后果：deferred（延后，如 channel_task_media_pending）任务在 run-logs 默认"进行中" tab 隐身，只能去"全部"找；与工单面板活跃口径互相矛盾。
- 修复：handlers_taskview.go:97 的 active 数组加 `domain.Deferred`（或两侧统一排除，取一即可，需产品定夺）。

**P2-5 直聊任务的动作真空：待审批无处可批、运行中无处可取消、顶層 cancel 端点是死端点**
- `POST /api/tasks/{taskId}/approve|deny`（routes.go:84-85）唯一消费方 task-panel.tsx:129/149-157；`POST /api/tasks/{taskId}/cancel`（handleTaskCancelTop，handlers_taskview.go:61,248-259）**全站零消费**。
- run-logs 抽屉对 awaiting_input 任务只渲染"待审批"徽标（无按钮），对 running 无取消；直聊任务没有 work_item → 面板不可达；chat 页是 PagePlaceholder（_app.chat.tsx:9）→ **直聊任务一旦 await input 永久卡死**。
- 注：chat 流程整体未完成可能是有意分期待办；本条把抽屉侧缺口与死端点记在 FP-12 名下，chat 排期时合并修。
- 修复：抽屉按 task-panel.tsx:140-173 同款加 approve/deny（awaiting_input）与 cancel（active）动作条，settleTask/cancel 复用现成 API。

**P2-6 测试缺口：主端点 HTTP 层零守卫 + messages 分页参数零测试 + 页面/抽屉零组件测试**
- GET /api/tasks 无 HTTP 级测试，e2e/registry_test.go 路由清单亦未收录（系统性"契约测试单向对账"又一实证）；messages 的 order=desc/before_seq/kind/next_before_seq 零测试——恰是 FP-05 P1-1 能藏到 220 条才被发现的原因；web/test 无 RunLogsPage/TaskTranscriptDrawer 测试、无 orchestration.ts API 封装测试。
- 修复：① HTTP 测试补 status 非法 400/游标续页/slim 形状；② messages 补 desc+before_seq 走链与 next_before_seq 断言（用 220 条样本形状）；③ 抽屉组件测试至少锁"渲染管线三卡触发 + await_input 呈现"。

### P3（6 项）

1. **TRIGGER_KEYS 缺 `delegated`**（run-logs:36-44 vs repo.go:1020）：恢复任务行内裸显英文 kind，补一个 i18n 键即可。
2. **抽屉标题不显示 attempt/max_attempts**（run-logs:246-253）：列表行有 retry 徽标（:201-205），抽屉丢重试上下文。
3. **筛选维度不足**：无时间范围筛选器（确认不存在，也未见规格要求）、无按 work-item 检索；长历史只能逐页 load more（游标指向过去）。
4. **抽屉状态不进 URL**（openTask useState，run-logs:51,164）：刷新即丢、无深链；切筛选无 keepPreviousData 整页闪烁（:55-65）。
5. **后端小疵**：状态过滤 SQL 字符串拼接依赖 handler 白名单、repo 层无二次防御（repo.go:1552）；messages `limit>500` 静默回落 200 而非钳 500（repo.go:749-751，600→200 反直觉）；handleTaskMessages 裸读 header 与 principal 并存（routes.go:318 vs :224，行为等价）。
6. **重试链行噪音**：failed tab 同时显示同一逻辑 run 的 attempt=1（已败）与 attempt=2（成功）两行，无 retry_of_task_id 关联/折叠，用户难分辨"最终态"。

---

## 九、修复路线建议（收益/成本序）

1. **P2-1（一行订阅 + 兜底轮询）**——抽屉活性复活，桥/失效链全部现成。
2. **FP-05 P1-1 同一处修**（前端 desc 游标 ~15 行）——抽屉与面板同时治愈，后端零改动。
3. **P2-3 + 抽屉回源**（slim + GET /api/tasks/{taskId}）——运输减负且治快照陈旧。
4. **P2-5 抽屉动作条**——顺手消灭死端点；chat 排期时统一验收。
5. **P2-2 usage 条 + per-task 端点**——数据链已在，纯读面工作。
6. **P2-4 一行口径对齐**；**P2-6 三段测试补齐**（HTTP 守卫最优先）。

## 十、证据留存

- 220 条样本：任务 `11111111-1111-4111-8111-111111111102`（全部 text kind，dev fixture），工单 3b8bc342-d79a-4a3f-be4e-5868ca976465（盘点验证工单-20261012）——**保留供 FP-05 P1-1 与本册 P2-1 修复验证**，不需要时按 task_id 删（task_message 级联）。
- 会话 cookie：/tmp/fp12-cookies.txt（临时）。本册未新增任何 fp12 前缀持久数据（纯只读实测）。
