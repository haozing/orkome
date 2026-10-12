# FP-03 功能点审计：工单创建与工单列表（新建弹窗 / 成员指派 / 搜索 / 筛选 / 分页）

> 日期：2026-10-12 · 审计人：FP-03 专项 agent · 仓库 D:\code\orkome
> 环境：后端 :8080（dev，`orkome-server.exe` Oct 11 23:45 构建，日志 /tmp/orkome-server.log）+ 前端 :5173（未开浏览器，静态 + curl 双面验证）
> 账号：inventory-test@orkome.dev · 工作区 inventory-ws（e83dd858-7412-4625-b048-0741e5ae6552）
> 方法：七步审计法（全链梳理 → 契约对齐 → curl 实测 → 代码质量 → 后端正确性 → 测试覆盖 → 缺陷清单）
> 关联：`docs/inventory-2026-10-12.md`（总账 P0-1 即本文 D-01）· 测试数据以 `fp03` 前缀留存在 inventory-ws（约 12 条，含边界用例）

---

## 0. 一句话结论

创建与列表的**主链路（创建 201/409/400、keyset 翻页、搜索、状态筛选、工作区 fence、rune 语义 title 校验）后端全部实测通过**；功能点的可用性被一个前端契约断点击穿——**members envelope 断点使"指派给成员"与"按成员筛选"两个入口双双静默消失**（P0-1）；后端存在 2 个正确性级缺陷（脏输入 500 裸穿、防重锁形同虚设）与 1 组 i18n 缺键（zh 11 键回退英文）。

**缺陷计数：P0×1 · P1×3 · P2×10**

---

## 1. 全链梳理（新建弹窗 + 列表 双链）

### 1.1 创建链

| 层 | 文件:行 | 内容 |
|---|---|---|
| 弹窗组件 | `web/src/components/work-item/create-work-item-dialog.tsx` | 项目/状态/优先级/指派人 4 个下拉 + title Input(maxLength=500) + RichEditor 描述；Enter 提交；"创建"+"创建并继续"双按钮；error `role=alert` |
| API 封装 | `web/src/api/work.ts:64-80` | `createWorkItem` POST `/api/work-items`，补默认 `assignee_type:'none'`、`assignee_id:''`、`project_id:''` |
| 成员源 | `web/src/api/identity.ts:71-74,126-139` | `listMembers` → GET `/api/workspaces/{id}/members`；`membersQueryOptions` key=`['workspace',ws,'members']` |
| 路由 | `internal/work/adapters/http/routes.go:98` | `reg("POST /api/work-items", d.handleCreate)`，经 `auth → RequireAuth → wsGuard`（routes.go:76-92：强制 X-Workspace-Id + MemberCheck） |
| Handler | `internal/work/adapters/http/handlers.go:18-46` | 解码 8 字段 → `WorkItems.Create` → 201 回 `WorkItem` |
| Service | `internal/work/app/service_workitem.go:42-104` | TrimSpace → title rune 校验(1-500) → EnsureStatusSeed → 空状态默认 `backlog`(:51) → 状态白名单 `WritableCategory`(:57) → AdvisoryLockTitle → 归一化标题重复检查(409) → 事务插入 + 订阅者 + 事件 |
| Repo | `internal/work/adapters/pgrepo/repo.go:124-156` | 事务内 `UPDATE workspace SET work_item_counter+1` 取号 → NextPositionInStatus → INSERT（`nullif($n,'')::uuid` 处理空串） |

### 1.2 列表链

| 层 | 文件:行 | 内容 |
|---|---|---|
| 页面 | `web/src/routes/_app.work-items.index.tsx` | URL search schema `{q,project,status,assignee}`（zod，:22-27）；q 300ms 防抖同步 URL(:45-57)；4 筛选器 + 重置 + "共 N 项" + 加载/错误/空态 + 加载更多 |
| API 封装 | `web/src/api/work.ts:41-60,211-229` | `listWorkItems` GET `/api/work-items?limit&cursor&project&search&status*&assignee&creator&subscriber`；`workItemsInfiniteQuery(limit=30, filters)` useInfiniteQuery，queryKey=`['work-items',filters]` |
| 路由/Handler | routes.go:97 + handlers.go:57-65 | `handleList` → `{items, next_cursor}` |
| Service | service_workitem.go:115-141 | limit clamp（`<=0 || >200 → 50`）→ involves 校验 → prop 过滤解析 → repo.List → fillWireCategories |
| Repo | pgrepo/repo.go:229-326 | 动态条件拼装（status ANY / assignee / creator / subscriber EXISTS / project / search ILIKE / parent / cursor / label / involves / props）→ `ORDER BY created_at DESC, id DESC LIMIT n+1` → keyset cursor |

### 1.3 附带面

- **行组件** `web/src/components/work-item/work-item-list.tsx`：8 列；指派人列 `useAssigneeLabel`(:39-57) 在 `assignee_type==='user'` 时 `members.data?.find(...)`（:46，envelope 崩点，见 D-01 因果链）；状态徽章 `statusMetaOf`+i18n。
- **quick-create**：routes.go:189-191 **仅当 `d.SourceCtx != nil` 才注册**；wiring 已挂（`cmd/orkome-server/wiring_work.go:96`），实测 201 且默认 status=todo（handlers_source.go:67）。前端封装 `quickCreateWorkItem`（work.ts:61-63）零调用方（裁决见 §6）。

---

## 2. 契约对齐（逐字段核对）

### 2.1 创建请求

| 前端 `WorkItemCreateInput`（work.ts:64-72） | 后端 handleCreate 解码 struct（handlers.go:20-29） | 判定 |
|---|---|---|
| title: string | Title string | ✅ |
| description?: string | Description string | ✅ |
| status?: string | Status string | ✅ 空→backlog(service:51)；非法→400 `unknown status key`（实测✓） |
| priority?: number | Priority int | ⚠️ 无范围校验（D-05） |
| assignee_type?: 'none'\|'user'\|'agent' | AssigneeType string（空→none） | ⚠️ 前端枚举比 DB CHECK 少 `squad`；非法值后端 500（D-02） |
| assignee_id?: string | AssigneeID string | ⚠️ 不校验存在性/成员资格，幽灵 uuid → 201（D-09） |
| project_id?: string | ProjectID string | ⚠️ 非 uuid → 500（D-02） |

前端补默认值的三件套（work.ts:76-78）与后端 `nullif('')::uuid`（repo.go:145）吻合 ✅。

### 2.2 列表响应 / 分页游标

- 后端 `{items, next_cursor}`（handlers.go:64）↔ 前端 `{items: WorkItem[]|null, next_cursor: string|null}` + `?? []` 兜底（work.ts:55-59）✅。实测空结果时 `items` 为 0 条正常渲染空态。
- 游标形状：`"<created_at RFC3339>|<uuid>"`（repo.go:322），前端 opaque 透传 ✅；实测 `limit=2` 取 cursor 翻页第 2 页首条 number 递减、keyset 生效 ✅。
- URL 参数名：前端搜索框状态 `q`（路由 search schema）→ 请求参数 `search`（work.ts:49）映射正确，与后端 `q.Get("search")`（handlers.go:935）对齐 ✅。状态筛选：前端单选 `status` → 数组追加 `status=key`（work.ts:50）→ 后端 `q["status"]` → `ANY($n)`（repo.go:233-237）✅。

### 2.3 members envelope 断点（D-01 精确定位）

实测后端返回：

```json
{"items":[{"user_id":"7c89…","email":"…","display_name":"…","role":"owner","created_at":"…"}],"total":1}
```

- **断点在 `web/src/api/identity.ts:72`**：`api.get<Member[] | null>` 把 `{items,total}` **对象**当成 `Member[]`；对象 truthy，`?? []` 不触发 → `listMembers` 实际返回一个被伪装成数组的对象。
- 消费点 A `create-work-item-dialog.tsx:140`：`members.data && members.data.length > 0` → `.length` 为 undefined → **成员 `<optgroup>` 不渲染** → 新建弹窗无法指派给成员（活体证据 `docs/inventory-pages-live-2026-10-12.md:75`：下拉只有"未分配"）。
- 消费点 B `_app.work-items.index.tsx:147`：同模式 → **列表页"按成员筛选"分组同样消失**。
- 消费点 C `work-item-list.tsx:46`：`members.data?.find(...)` → 一旦出现 user 指派行将 TypeError。当前被 A 掩盖（指派不出去，列表无 user 指派行）——**修 A 后 C 自然恢复**，但若按"后端改数组"方案外的任何局部修复（例如只在弹窗里适配），C 的雷仍在。
- 附加：identity.ts 里 `Member` 接口声明了 `id`/`workspace_id`（:22-27 与 :62-70 重复声明两份），而实测成员对象**没有这两个字段**——即使修好 envelope，依赖 `m.id` 的代码也是 undefined。
- 对照组：`listAgents`（orchestration.ts:120-125）正确解包 `{agents}` 包裹 → agent 分组正常，证明这是单点遗漏而非惯例。

### 2.4 错误响应契约

后端错误体 `{"error":"E_*","message":"…","details":[{field,problem}]}`。前端 `@ploykit/client` `ApiError extends Error`、`message=后端原文`（api.ts:3-16）→ 弹窗 `create.error.message` 直显英文原文（如 `active duplicate exists`、`invalid title`）——**无 i18n 映射**（D-08）。

---

## 3. curl 实测记录（全部跑通，:8080 真实后端）

### 3.1 创建

| 用例 | 结果 | 判定 |
|---|---|---|
| 最小字段 `{"title":"fp03-最小创建-20261012"}` | **201**，number 递增，status=`backlog`，status_category=`backlog` | ✅ |
| 同标题重复创建 | **409** `E_ACTIVE_DUPLICATE "active duplicate exists"` | ✅（归一化去重生效，测试 M2 亦覆盖 Alpha/alpha） |
| 空标题 `"   "` | **400** `invalid title` + `details:[{field:"title",problem:"1-500 chars"}]` | ✅ |
| 纯 500 个汉字 | **201** | ✅ **rune 语义正确** |
| 纯 501 个汉字 | **400** `invalid title` | ✅ |
| 非法 status `"doing_x"` | **400** `unknown status key` | ✅ 枚举白名单生效 |
| priority=999 / priority=-5 | **201** | ❌ 无范围校验（D-05） |
| assignee_type="banana" | **500 E_INTERNAL** | ❌ DB CHECK 裸穿（D-02） |
| assignee_id=不存在 uuid | **201** | ❌ 幽灵指派（D-09） |
| project_id="not-a-uuid" | **500 E_INTERNAL** | ❌（D-02） |
| parent_work_item_id="abc" | **500 E_INTERNAL** | ❌（D-02） |

> 排错备注：初测 499/500 汉字曾误报 400，系测试标题带了 9-11 字符后缀越过 500 rune；纯长度重测后确认边界精确在 500/501。500 全部是**运行中二进制行为正确**，与当前源码一致。

### 3.2 列表 / 搜索 / 筛选 / 分页

| 用例 | 结果 | 判定 |
|---|---|---|
| `limit=100` 全量 | 200，13 条，next_cursor=""，`created_at` 严格降序 | ✅ 排序稳定（created_at DESC, id DESC 兜底） |
| `search=fp03` | 200，10/13 条命中且标题全含 fp03 | ✅ ILIKE 大小写不敏感 |
| `search=fp03%25`（带 % 通配） | 与 `search=fp03` **完全相同 10 条** | ❌ `%` 当通配符，未转义（D-06） |
| `status=todo` | 200，仅 todo 条目 | ✅ |
| `status=nonsense` | 200，0 条（空结果非报错，可接受） | ✅ |
| `limit=2` → 取 cursor 翻页 | 200，第 2 页首条 number=11，keyset 无重叠 | ✅ |
| `cursor=garbage` | 200，**静默返回第 1 页** | ❌ 畸形游标不报错（D-07） |
| `limit=0 / -1 / 201 / 999 / abc` | 全部 200 且 clamp（0→50、-1→50、>200→50、非数字→50） | ✅ 静默 clamp，无崩溃 |

### 3.3 fence 与认证

| 用例 | 结果 |
|---|---|
| 未登录 GET /api/work-items | 401 ✅ |
| 无 X-Workspace-Id | 400 "X-Workspace-Id header required" ✅ |
| 伪造工作区 uuid | 403 "not a member of this workspace" ✅ |
| `POST /api/work-items/quick-create` | 201，默认 status=`todo` ✅（与常规创建默认 backlog 不一致，见 D-10 观察） |
| GET /api/work-item-statuses | 7 个内置状态按 position 有序 ✅ |

---

## 4. 代码质量（前端）

| 检查项 | 结论 |
|---|---|
| 弹窗校验反馈 | title 空禁用双按钮 + Enter 拦截（:71,206,214）；服务端错误 `role=alert` 展示（:191-195）；"创建并继续"成功后 `createdContinue` 提示（:198-201）——**结构良好** |
| 列表 loading/空态/错误态 | `PageLoading` / `PageError(onRetry)` / `PageEmpty`（有筛选用 noMatch 文案、无筛选用 emptyTitle 文案区分，:179-194）——**完备** |
| 搜索防抖 | 300ms 防抖 + `replace:true` 写 URL（:45-57），刷新/分享保持状态——**良好** |
| queryKey 设计 | `['work-items', filters]` 结构化 hash，filters 每渲染新建对象无碍；弹窗创建成功 `invalidateQueries({queryKey:['work-items']})` 前缀失效覆盖所有筛选变体（dialog:56）——**正确** |
| 可访问性 | 4 个筛选器/4 个下拉均有 `aria-label`；列表头 `aria-hidden`；行是语义 `<li>+<Link>`——**良好** |
| 缺口 | ①"共 N 项"（`work.totalN`，:174）实为**已加载条数** `items.length`，翻页中数字跳变、语义误导（D-11）；②弹窗的 members/agents 查询不受 `open` 门控（:39-40），页面加载即发请求（轻微，agents 有 staleTime 60s）；③错误文案英文直显（D-08） |

---

## 5. 后端正确性

| 检查项 | 结论 |
|---|---|
| title 长度 rune 语义 | ✅ 精确 500/501 边界，中文无碍（§3.1） |
| status 枚举白名单 | ✅ `WritableCategory` 对工作区状态目录（含自定义）校验，未知名 400 |
| **AdvisoryLockTitle 有效性** | ❌ **pg_advisory_xact_lock 在 autocommit 裸连接上执行**（repo.go:359-370：`r.DB.Acquire` 取池连接直接 `Exec`，无事务包裹）→ xact 级咨询锁在语句结束即释放，**锁形同虚设**；`CountActiveDuplicate`(SELECT) 与 `CreateWorkItem`(INSERT 事务) 之间存在 TOCTOU 窗口，并发同标题可双双越过 409（D-03） |
| 重复检查语义 | 归一化（lowercase+空白折叠，domain NormalizeTitle）+ 限同 project/parent + 排除终态 —— 单线程语义 ✅（且 M2 测试覆盖"终态后可重建"） |
| 列表 N+1 | ✅ 无。单查询 + `fillWireCategories`（每请求固定 2 条：seed+ListStatuses，非按行） |
| 排序稳定性 | ✅ `ORDER BY created_at DESC, id DESC` 确定性；但无 `(workspace_id, created_at DESC, id DESC)` 索引（1001 只有 ws_status/ws_updated/trgm），大表 LIMIT 翻页走 sort（D-12 性能预留） |
| 工作区 fence | ✅ 每路由 wsGuard（MemberCheck）+ 查询恒带 `workspace_id=$1`（repo.go:230），伪造 ws 实测 403 |
| 读路径隐含写 | ⚠️ 每次 List/Create 都跑 `EnsureStatusSeed`（INSERT…ON CONFLICT DO NOTHING，repo.go:26-35）——幂等但读路径带写（D-13） |
| 错误分类 | ❌ pgx 层错误（uuid cast、CHECK violation）未被映射为 400，全部 500 E_INTERNAL（D-02） |

---

## 6. quickCreateWorkItem 死代码裁决

- `web/src/api/work.ts:61-63` 定义，全 web/src **零调用方**（grep 仅定义处）；后端端点活着且带 `RequireHuman`，语义是"评论捕获/客户端快捷建单"面（`OriginQuickCreate`、`source_digest` 走 CreateFromDigest）。
- **裁决：属"后端先行、前端未接"规划面**（与盘点总账 §四一致），前端封装为预留死代码。**建议删除或加注释标记预留**，暂不接线——列表页已有完整新建弹窗，quick-create 的 UI 场景（命令面板/评论流）不存在；待 conversation 域接入评论捕获时一并裁决（与总账 P2 处置策略一致）。

---

## 7. i18n 键清单核实（本功能点全部 57 键）

脚本批量核对（`web/src/i18n/locales/{zh,en}.ts`）：

- **46 个 `work.* / common.* / nav.* / myWork.* / agents.* / runLogs.loadMore` 键：zh/en 双语全齐** ✅（弹窗 20 + 列表 21 + 行组件 5 组全部命中）。
- **zh 缺 11 键**：`status.backlog/todo/inProgress/inReview/blocked/done/cancelled`（7）+ `priority.none/low/medium/high`（4）；en.ts:450-464 齐。`i18n/index.ts:28` `fallbackLng:'en'` → **中文界面下状态徽章、优先级徽章、两处下拉选项回退英文**（"To do / In progress / None…"）。已核实影响面：`work-item-list.tsx:22,94`（徽章）、`create-work-item-dialog.tsx:117-121`（优先级下拉）、两处状态筛选/状态下拉经 `statusName`（内置键走 `t(labelKey)`）。

## 8. 未接前端的高级面（清单标注，不深审）

work 域 ~48 条无前端消费端点中，与工单列表直接相邻的：`POST /query`（DSL 查询）、`table/rows|groups|facets`（表格视图三件套）、`GET /search`（federated RRF 搜索，B11）、`batch-update/batch-delete`、`grouped`、`assignee-frequency`、`work-item-views` + `view-preferences`（保存视图）、`child-progress`、`duplicates`、`move`（手动排序）、`reactions`、`subscribers`、`metadata/properties` 键级 CRUD。均为"后端先行"规划面，本文仅登记不判定。

---

## 9. 测试覆盖

| 层 | 已覆盖 | 缺口 |
|---|---|---|
| service（app 层，fake repo） | M2 golden（http/integration_test.go:212 起）：创建 201+取号、归一化重名 409、终态后可重建、revision 冲突 409、状态 seed=7 | Create 的 title 边界 400、unknown status 400、priority/assignee 越界（若 D-02/D-05 修复需同步补）；AdvisoryLock 并发路径**零测试**（假 repo 也测不出，需 pgrepo 集成） |
| pgrepo（集成） | b11 搜索（federated/RRF）、search_timeout | **`ListWorkItems` 零直接测试**——keyset 翻页、cursor 形状、畸形 cursor、limit clamp、ILIKE 通配全部无断言；`next_cursor` 全仓唯一断言在 conversation 域（service_history_test.go:83），work 域没有 |
| http 层 | resources_routes_test.go:263 唯一摸到 `GET /api/work-items`（involves 参数校验） | 列表端点契约（envelope/筛选/翻页）无任何 HTTP 级测试 |
| 前端 | **无任何测试文件**（web/src 下无 `*.test.*`/`*.spec.*`） | 弹窗校验逻辑、members 消费、防抖同步 URL——D-01 这类契约断裂正是无契约测试的直接后果 |
| e2e（Go） | golden/coverage 有 work-items 字样 | 未见列表翻页/搜索场景 |

---

## 10. 缺陷清单（P0×1 · P1×3 · P2×10）

### P0

**D-01 members envelope 断点：新建弹窗"成员"分组与列表页"按成员筛选"双双静默消失（无法指派给成员）**
- 根因：`web/src/api/identity.ts:72` `api.get<Member[] | null>` 收到 `{items,total}` 对象当数组返回；`.length===undefined` 使 `create-work-item-dialog.tsx:140`、`_app.work-items.index.tsx:147` 两处 `length > 0` 门为假；`work-item-list.tsx:46` 的 `.find` 是同根因的潜伏崩点（当前被前者掩盖）。
- **修复推荐：前端适配（改 `identity.ts` 一处），不动后端。** 理由：①`{items,total}` 是 ploykit workspace 域统一 envelope 惯例，work/comments/labels 等产品域同形状，改后端=破坏框架契约 + 连带 ploykit 契约测试与所有既有调用方；②一处修复同时治好本功能点 3 处 + 总账 P0-1 的详情页五处连坐 + Agent 权限 tab，收益最大（与总账 §六修复顺序 #1 一致）。改法：`listMembers` 改为 `api.get<{items: Member[] | null}>(...); return res.items ?? []`。
- 顺手项（同函数内）：合并 identity.ts:21 与 :62 重复的 `Member` 声明，并按实测字段改为 `{user_id,email,display_name,role,created_at}`（实测无 `id`/`workspace_id`）。

### P1

**D-02 创建端点脏输入 500 裸穿（3 类实测复现）**
- `assignee_type="banana"` → DB CHECK（migrations/1001:59）violation → 500；`project_id`/`parent_work_item_id` 非 uuid → pgx cast error → 500（`internal/work/adapters/http/handlers.go:18-46` 无白名单/格式校验；`pgrepo/repo.go:145` `nullif($n,'')::uuid`）。
- 修复：handleCreate 进 service 前校验 `assignee_type ∈ {none,user,agent,squad}`（或与前端枚举对齐）+ uuid 格式校验，返回 400 E_VALIDATION + field details；或在 repo 层把 pgconn.PgError(22007/23514/22P02) 映射为 apierr.Validation。

**D-03 防重复 AdvisoryLock 是 no-op，409 防重可被并发穿透**
- `pgrepo/repo.go:359-370`：`pg_advisory_xact_lock` 在**无事务的池连接 autocommit** 上执行，语句结束锁即释放；其后的取号+检查+插入各走各的事务。单发 409 正确（实测），但并发同标题可双写。
- 修复：把 lock+CountActiveDuplicate+CreateWorkItem 收进同一 `DB.Within` 事务（tx 内执行 `pg_advisory_xact_lock`），或改 session 级 `pg_advisory_lock` 并在 unlock 回调显式 unlock（同连接）。

**D-04 zh 包缺 `status.*`(7)/`priority.*`(4) 共 11 键，中文界面徽章与下拉回退英文**
- `web/src/i18n/locales/zh.ts`（无这两个命名空间）vs `en.ts:450-464`；fallback 链 `i18n/index.ts:28`。键清单见 §7。
- 修复：zh.ts 照 en 语义补 11 键（Backlog→待办池、Todo→待处理、In progress→进行中、In review→评审中、Blocked→受阻、Done→已完成、Cancelled→已取消、None→无、Low→低、Medium→中、High→高）。

### P2

**D-05 priority 无范围校验**：999/-5 → 201（service_workitem.go:42 不校验；migrations/1001:58 无 CHECK）。列表有 `PRIORITY_META[item.priority] ?? [0]` 兜底不崩，但脏数据入库。修复：service 校验 0-3 + 迁移加 CHECK。
**D-06 搜索通配符未转义**：`search=fp03%25` 与 `search=fp03` 同结果（repo.go:258-262 直接拼 `%q.Search%`）。用户搜含 `%`/`_` 的字面量语义错误。修复：转义 `\%`/`\_` 并加 `ESCAPE '\'`。
**D-07 畸形 cursor 静默回落第 1 页**：`cursor=garbage` → 无 `|` 分段即忽略条件（repo.go:272-279），客户端翻页 bug 会被掩盖成"数据重复"。修复：解析失败返回 400 E_VALIDATION。
**D-08 服务端错误文案英文直显**：ApiError.message 直显进弹窗 `role=alert`（create-work-item-dialog.tsx:191-195），如 "active duplicate exists"。修复：按 `err.code`（E_ACTIVE_DUPLICATE/E_VALIDATION）映射 i18n 文案，`isApiError` 已具备（api.ts:19）。
**D-09 幽灵指派**：assignee_id 不校验存在性/成员资格 → 201（对比 unsubscribe 有 target 成员校验，handlers.go:900-906）。列表行兜底显示 `id 前 8 位…`（work-item-list.tsx:47）不崩。修复：创建/更新时对 `assignee_type=user` 复用 MemberCheck。
**D-10 默认状态不一致（观察）**：常规创建空 status → `backlog`（service_workitem.go:51），quick-create → `todo`（handlers_source.go:67），前端弹窗默认 `'todo'`（create-work-item-dialog.tsx:29）。仅 API 直调可见分歧，建议统一。
**D-11 "共 N 项"语义误导**：`work.totalN` 实为已加载条数（_app.work-items.index.tsx:174），cursor 分页无 total。改文案为"已加载 N 条"或后端供 total（窗口 count）。
**D-12 列表排序无覆盖索引**：`(workspace_id, created_at DESC, id DESC)` 缺失（1001:83-89），大表翻页走 sort。建议随容量规划补索引。
**D-13 读路径隐含写**：每次 List/Create 跑 `EnsureStatusSeed`（repo.go:32-35）。建议 seed 移到工作区创建/状态目录写路径，读路径只查。
**D-14 quickCreateWorkItem 前端死代码**：work.ts:61-63 零调用方。建议删除或注释标记"客户端 API 预留"（后端端点保留，属评论捕获规划面）。

### 修复优先级建议

1. **D-01**（前端 identity.ts 一处，解锁指派+按成员筛选，连带治好详情页五处崩溃）→ 2. **D-04**（11 个静态键，一次提交）→ 3. **D-02**（handleCreate 前置校验）→ 4. **D-03**（事务内锁）→ 5. D-05~D-09 顺手批 → 6. D-06/D-07 随 pgrepo 补测试一起落（先补 §9 缺口再修，防回归）。

---

## 11. 证据留存

- cookie jar /tmp/fp03-cookies.txt；测试工单 12+ 条（fp03 前缀 + 499/500/501 汉字 + 纯 ascii 500）留在 inventory-ws，可直接复查。
- 关键日志：/tmp/orkome-server.log（400/201 序列，2026-10-12T03:09Z 段）。
- 活体侧证：`docs/inventory-pages-live-2026-10-12.md` §5（弹窗截图 work-items-new-dialog.png：负责人下拉只有"未分配"）。
