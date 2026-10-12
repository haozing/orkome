# FP-11 审计：my-work 页（我的工作聚合视图）

> 日期：2026-10-12 · 审计对象：`web/src/routes/_app.my-work.tsx`（325 行）全链 · 方法：七步审计法（全链梳理→契约对齐→curl 实测→代码质量→后端正确性→测试覆盖→缺陷清单）
> 环境：后端 :8080 + 前端 :5173；测试账号 `inventory-test@orkome.dev`（uid `7c893147-c061-426e-a7f8-c8f011c73502`）、工作区 `inventory-ws`（`e83dd858-7412-4625-b048-0741e5ae6552`）、对照工作区 `FP02 Audit WS A2`（`5ebb1af1-cd06-44ad-b116-b7b62897adc2`）
> 已知缺陷不重复申报：members envelope（根因 A，batch1）、WS 实时链死亡（batch1 P1 主题 1）、zh 缺 status.*/priority.* 键（batch1）——本域触点以"引用"方式登记

---

## 一、一句话结论

my-work 四个 tab 的**语义、数据源与工作区 fence 全部正确**（四 tab 归属、退订联动、跨工作区隔离、keyset 分页均 curl 实测通过），页面自身无 P0；但**"加载更多"永不消失且点击后重复整页**（`next_cursor:""` × `??` × TanStack v5 `!= null` 三点合断，P1）与**"按时间"排序双断**（服务端按 created_at 分页 + 状态流转不更新 last_activity_at，P1）让这个以"最近活跃"为卖点的个人视图在数据量上来后系统性失真。

**计数：P0 × 0（另有根因 A 新增触点 2 处，引用 batch1 P0-A）· P1 × 2 · P2 × 6**

---

## 二、全链梳理（组件 → api → 端点 → service → repo）

```
_app.my-work.tsx
  ├─ meQueryOptions                     → GET /auth/me + GET /api/workspaces（identity.ts:113-125）
  ├─ listProjects                       → GET /api/projects          （work.ts:236-239）
  ├─ listStatuses                       → GET /api/work-item-statuses（work.ts:98-101）
  └─ workItemsInfiniteQuery(30, filters) → listWorkItems             （work.ts:41-60, 211-229）
       → GET /api/work-items?limit=30&cursor&project&status×N&assignee&creator&subscriber
           → workhttp.Mount wsGuard（routes.go:76-92：要求 X-Workspace-Id 头 + MemberCheck 成员检查 + p.WorkspaceID=ws）
             → handleList（handlers.go:57-65）→ urlQuery（handlers.go:922-939）
               → WorkItemService.List（service_workitem.go:115-141：limit 钳制 ≤200、fillWireCategories 补 status_category）
                 → Repo.ListWorkItems（pgrepo/repo.go:229-326：workspace_id=$1 fence + 过滤拼接 + created_at,id keyset 分页）
```

- 工作区上下文：前端 `setWorkspaceIdProvider`（main.tsx:12）从 zustand 持久化 store（`orkome.workspace`）取 currentId，ploykit client 对 `/api` 前缀路径自动加 `X-Workspace-Id` 头（ploykit api.ts `apiFetch`）；后端 wsGuard 逐路由验成员身份。
- **裁决：my-work 是"单工作区视图"而非跨工作区聚合**——所有查询都被当前工作区 fence 住，"我的工作"= "我在**当前工作区**的工作"。切工作区后列表正确切换（`switchWorkspace` removeQueries 全清 + WS 重连，workspace-switch.ts:4-15，弥补了 queryKey 无工作区维度的隐患）。

## 三、四 tab 语义与数据源

| tab（scope） | 前端 filter（_app.my-work.tsx:115-127） | 后端 SQL 条件（repo.go:230-299） | 实测归属 |
|---|---|---|---|
| 待办 todo（默认） | `assignee=me` + `statuses=openKeys`（category∈unstarted/started：backlog,todo,in_progress,in_review,blocked） | `workspace_id=$1 AND status_key=ANY(..) AND assignee_id::text=$2` | ✅ 指派我+todo 的 #17 命中；done 的 #18 不命中 |
| 已办 done | `assignee=me` + `statuses=closedKeys`（done,cancelled） | 同上，status 集换 closed | ✅ 仅 #18 |
| 已创建 created | `creator=me` | `workspace_id=$1 AND creator_id::text=$2` | ✅ 19 条全量含 4 条 fp11 |
| 已订阅 subscribed | `subscriber=me` | `EXISTS(SELECT 1 FROM work_item_subscriber WHERE work_item_id=wi.id AND user_id::text=$2)` | ✅ 创建自动订阅（source=creator）；退订 #19 后从本 tab 消失、仍留在"已创建" → **由真实订阅关系驱动而非创建关系** |

辅助面：分组模式 time（客户端按 last_activity_at 重排）/ project（按项目分段，无项目归"未分组"）/ status（按 status position 排序，未知 key 兜底原样显示）；项目筛选（search.project）与 tab 正交叠加；分页 limit=30 "加载更多"。四套空态文案（`myWork.empty_<scope>_title/desc`）zh/en 双语齐全。

## 四、契约对齐（逐字段）

- 响应 envelope `{items: WorkItem[]|null, next_cursor: string}`，`listWorkItems` 显式 `page.items ?? []`（work.ts:55-59）——**这正是 identity.ts listMembers 该写而没写的防御**，work 域自身无 envelope 问题。
- 排序/游标：`ORDER BY created_at DESC, id DESC` + keyset `(created_at,id) < (cursor)`（repo.go:272-279,301），`next_cursor` 形如 `2026-10-12T11:10:39.661906+08:00|<uuid>`；无更多页时返回**空串** `""`（repo.go:319-324）→ 见 P1-1。
- 参数核对：limit（0→50，>200→50，service_workitem.go:115-118；前端恒 30 不触界）、status 重复键 `q["status"]` ✓、assignee/creator/subscriber/project/search ✓。前端 `WorkItemPageInput` 与后端 `urlQuery` 字段一一对应，无幽灵字段。
- `status_category` 由 `fillWireCategories` 每次列表查询补齐（service_workitem.go:139,151-166）——前端 todo/done 的 open/closed 集合来自 `/api/work-item-statuses`（同一 category 词表），两端一致。

## 五、curl 实测记录

测试数据（fp11 前缀，事后已清理）：
- #17 `fp11-指派-待办`（assignee=me, todo）、#18 `fp11-指派-已完成`（assignee=me, done）、#19 `fp11-订阅归属`（无指派）、#20 `fp11-项目内工单`（project=fp11-project）、B 区 #3 `fp11-B区指派`。

| # | 场景 | 结果 |
|---|---|---|
| 1 | 四 tab 归属（见 §三表） | 4/4 通过 |
| 2 | project 筛选：created+project=fp11-project | 仅 #20 ✅ |
| 3 | 订阅联动：`POST /api/unsubscribe {work_item_id}` 后 subscriber 查询 | #19 从 subscribed 消失（18 条），仍在 created ✅ |
| 4 | 跨工作区：B 区指派我的工单 | 不出现在 inventory-ws 任何 tab 查询 ✅；B 区自己的查询命中 ✅ → **无跨区泄漏** |
| 5 | fence：无 `X-Workspace-Id` 头 → 400；非成员工作区 → 403 | ✅ 硬 fence |
| 6 | keyset 分页：created tab limit=5 走 5 页 | 23 条无重复无遗漏、number 严格降序 ✅ |
| 7 | **空游标复取**（P1-1 证据）：`cursor=`（空串） | 返回与第一页**完全相同**的 10 条 ✅ 证实重复取页路径 |
| 8 | 空态/边界：`status=nonexistent_key` → `{items:null}`；全新空工作区 statuses 自动 seed 7 内置（todo/done tab 不会因空状态集卡死）| ✅ |
| 9 | last_activity_at：PUT 改 priority 200 后 | `updated_at` 变、`last_activity_at` **不变**（P1-2b 证据）|
| 10 | 附带：DELETE 空工作区 fp11-ws → 500 E_INTERNAL，日志 `23503 work_item_status_workspace_id_fkey` | 印证 batch1 FP-02 P1（**连空工作区也删不掉**——内置状态 seed 行即触发 FK），残留 `eeeb2d2e-…`（fp11-ws）留作活体证据 |

## 六、缺陷清单

### P0 × 0（自有）＋ 根因 A 触点扩展 2 处【引用 batch1 P0-A / 总账 P0-1】

| 触点 | 位置 | 症状 |
|---|---|---|
| my-work 行内指派列 | `web/src/components/work-item/work-item-list.tsx:46` `members.data?.find(...)` | 与实测崩溃点 `info-panel.tsx:28` 完全同构；members 查询以 envelope 落地后渲染任意行即抛 TypeError。活体盘点当日未见列表页崩溃截图（可能因查询时序），但代码路径与已证实崩溃点一致 |
| "创建工单"弹窗成员组 | `web/src/components/work-item/create-work-item-dialog.tsx:140` `members.data && members.data.length > 0` | envelope 无 `.length` → **成员指派下拉组永不渲染**（静默），batch1"指派下拉永不渲染"家族的第 8 处消费方 |

修复同根因 A 一行解包（`identity.ts:71-74` 改 `res.items ?? []`），全站含上述两触点同时治愈。

### P1 × 2

**P1-1 "加载更多"永不消失，点击后整页重复（波及 my-work 全部 4 tab + work-items 列表页）**
三点合断，证据链完整：
1. 后端无更多页时 `next_cursor:""`（pgrepo/repo.go:319-324，实测 `next:` 为空串）；
2. 前端 `getNextPageParam: (lastPage) => lastPage.next_cursor ?? undefined`（web/src/api/work.ts:228）——`""` 非 nullish，`??` 拦不住，返回 `""`；
3. TanStack v5 判定 `hasNextPage = getNextPageParam(...) != null`（query-core@5.104.1 infiniteQueryBehavior.js:85）→ `""` 判真 → 按钮**每页恒显**（_app.my-work.tsx:309-320）。
点击后 `cursor: pageParam || undefined`（work.ts:226）把 `""` 变 `undefined` → 重取第一页（实测 #7 同数据）→ 追加重复行，且新末页 `next_cursor` 仍为 `""` → 无限循环，"共 N 项"计数（my-work:191-193）随之膨胀。
与 batch1 FP-10 通知域"空串游标 hasNextPage 恒真"同型同病（batch1 §三.6），这是 **work.ts 实例**的首报。
**修复（一行）**：work.ts:228 改 `lastPage.next_cursor || undefined`；根修可让后端 exhausted 时返回 `null`（合同改动大，不推荐先行）。

**P1-2 "按时间"默认排序双断，视图系统性失真**
- (a) **分页序与展示序不一致**：服务端按 `created_at DESC` 出页（repo.go:301），前端 time 模式在已加载窗口内按 `last_activity_at` 重排（_app.my-work.tsx:141-143）。指派给我的 open 工单 >30 条时，"老创建但刚有动静"的条目根本不在前 30 条加载窗口里——my-work 待办 tab 直接看不见它；窗口内重排还会让"加载更多"接出来的条目插到页首，顺序错乱。
- (b) **last_activity_at 语义缺口**：只有评论（service_comment.go:158,308,338 TouchWorkItem）与 metadata/properties（repo.go:1190-1249）会 bump；**核心 PUT 更新路径（状态流转/改派/优先级/标题，repo.go UpdateWorkItem :193-219）不 bump**——实测改优先级后 last_activity_at 不变。最常见的工作动作"把待办挪到进行中"不计入活跃度，"按时间"排序与 work-items 列表"最近活跃"列共同失真；metadata 更新反而 bump，同一域内语义自相矛盾。
**修复**：(b) UpdateWorkItem 补 `last_activity_at=now()`（一行，与列更新合并）；(a) 中期把 my-work 的排序/游标下沉服务端（按 last_activity_at keyset），短期至少在 UI 上注明排序口径。

### P2 × 6

| # | 缺陷 | 位置 | 建议 |
|---|---|---|---|
| 1 | 上游查询（me/statuses/projects）失败时页面**永久转圈无错误面**：`filters=null` → `enabled:false` → `list.isPending` 恒真渲染 PageLoading；页面只消费 `list.isError`（:256），三个上游查询的 error 无人呈现 | _app.my-work.tsx:105-133,254 | 上游 `isError` 时渲染 PageError+重试 |
| 2 | todo/done 下创建成功后强制跳 created tab，且 `navigate({search:{scope:'created'}})` **全量替换** search，丢掉 `project`/`group` 参数 | _app.my-work.tsx:247-251 | 透传既有参数；或仅在新建项不属于当前 tab 时才跳 |
| 3 | 计数徽标"共 N 项"实为**已加载条数**（首屏 30），有下一页时误导；与 P1-1 叠加后把重复页也计入 | _app.my-work.tsx:191-193 | 改文案"已加载 N 项"或等 P1-1 修复 |
| 4 | tab 无键盘可达性：`role="tablist"/"tab"` 无 roving tabindex/方向键、无 `aria-controls`/tabpanel 关联 | _app.my-work.tsx:160-187 | 补键盘导航或改用 Base UI Tabs |
| 5 | 索引缺口：`assignee_id::text`/`creator_id::text` 的 cast 使 `idx_wi_ws_assignee`（uuid 列）失效、creator 无任何索引（migrations/1001:83-89），created tab 退化为工作区内扫描；叠加 `fillWireCategories` 每次 List 多 2 条查询（EnsureStatusSeed+ListStatuses，service_workitem.go:151-166） | repo.go:240-247 | 函数索引 `((assignee_id)::text)`/`((creator_id)::text)`，或参数按 uuid 传；statuses 定义加会话级缓存 |
| 6 | "已办"把 `cancelled` 一并计入（closedKeys=done+closed 两类）——取消≠完成，语义需产品裁决；同理 empty_done 文案只提"完成" | _app.my-work.tsx:112-114 | 确认口径；若需区分拆 status 集或加筛选 |

### 引用登记（非新增，本域触点）

- **根因 A members envelope**（batch1 P0-A）：上文触点扩展 2 处。
- **WS 实时链死亡**（batch1 P1 主题 1）：my-work 依赖 `work_item:`/`comment:` 帧失效 `['work-items']`（main.tsx:15-17），workspace scope 帧全拒 → 多标签场景 my-work 无自愈路径（staleTime 15s + `refetchOnWindowFocus:false`，query.ts:7-8），他人改派/完卡不刷新。
- **zh 缺 status.*/priority.* 键**（batch1 P1）：my-work 行内状态/优先级徽章中文回退英文。
- **空工作区删除 500**（batch1 FP-02 P1）：新增实证——空工作区（仅 seed 状态行）也 23503，见 §五#10。

## 七、测试覆盖

- **组件/页面零测试**：`_app.my-work.tsx`、`work-item-list.tsx` 在 `web/test/` 下均无对应测试；`workItemsInfiniteQuery` 的分页终止语义零测试——任一存在即可抓住 P1-1（现有兄弟测试 `assignee-row.test.tsx` 还把 listMembers mock 成裸数组，属于 batch1 §四.1"按错误契约 mock"模式）。
- **后端列表端点零测试**：`GET /api/work-items` 的 assignee/creator/subscriber/status×N/cursor 过滤在全仓无集成测试（integration_test.go 仅 POST/PUT/子资源）——复核确认 batch1 FP-03"列表端点全仓零测试"结论，且四 tab 归属语义正踩在这块零覆盖区上。
- 相关存在但不及面：service_subscriber_test.go（订阅服务单测）、routes_walk_test.go（路由单向对账）、service_query_test.go（table/query 高级面编译，非本端点）。

## 八、与 work-items 列表页的功能重叠裁决

| | my-work | work-items（全部工作） |
|---|---|---|
| 视角 | 人视角预设：待我办（open 状态集合）/我已办/我创建/我订阅 | 全量临时筛选：q 搜索 / 项目 / 单状态 / 指派人 |
| 独有能力 | open 状态集合一键过滤、订阅视角、三种分组（时间/项目/状态） | 搜索（trgm 索引）、任意指派人（含 agent）、单状态精筛 |
| 无法做到 | 无搜索、无"看别人的" | 表达不了"待我处理"（要手选 5 个状态）、无订阅/创建者视角 |

**裁决：互补关系，保留两者，无需合并。** 同一端点两套预设属正常复用；真正需要收敛的是共享层——`workItemsInfiniteQuery` 的分页终止（P1-1 一处修复两页同愈）与 `WorkItemRow` 的 members 依赖（根因 A 一处修复全站同愈）。

## 九、修复建议（按收益/成本）

1. **work.ts:228 一字符级修复**（`??` → `||`）清掉 P1-1 全部症状（两页面四 tab）。
2. **repo.go UpdateWorkItem 补 last_activity_at bump**，P1-2b 一行；P1-2a 随下批"排序游标服务端化"处理。
3. identity.ts 根因 A 修复后回访本页：行内指派列、创建弹窗成员下拉、详情页互通。
4. P2 逐条顺手修（上游错误面、onCreated 透传、计数文案、tab 键盘、函数索引）。

## 十、证据留存

- fp11 测试数据已清理：4 个工单 + B 区 1 工单 + fp11-project 全部 204 删除；inventory-ws 复查无 fp11 残留。
- 遗留活体证据：`fp11-ws`（`eeeb2d2e-bac7-4b69-af82-988c22ee8a34`，空工作区删除 500 样本，服务 FP-02 修复验证）；服务端日志 `/tmp/orkome-server.log` 12:16:07 `23503 work_item_status_workspace_id_fkey`。
- 复现脚本要点：登录→造指派/完成/订阅三件套→四 filter 查询核对→`cursor=`（空）复取第一页对比。
