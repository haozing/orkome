# FP-04 审计：工单详情页 · 信息面板与状态流转（2026-10-12）

> 范围：`/work-items/$id` 详情页整页组装、info 面板（状态/负责人/优先级/日期/父项）、property-rows、labels、子工单、时间线、描述编辑器、动态 slot tab（四波 C2 交付物静态复核）、URL search 状态。
> 方法：七步审计法——全链梳理 → 契约逐字段对齐 → curl 实测（登录态 inventory-test@orkome.dev / 工作区 inventory-ws，数据 fp04 前缀，测后清理）→ 代码质量 → 后端正确性 → 测试覆盖 → 缺陷清单。
> 环境：后端 :8080 实测 + web/src 静态阅读。未开浏览器（复用 inventory 活体结论：详情页 100% 崩溃，见 `docs/inventory-pages-live-2026-10-12.md` §5.2）。

---

## 结论速览

| 维度 | 结论 |
|---|---|
| P0 / P1 / P2 | **1 / 3 / 10** |
| P0 | members envelope 契约不匹配 → 详情页整页崩溃（本页 3 个断点，1 处最小修复可解） |
| 整页崩根因 | 前端 `listMembers` 不解 `{items,total}` envelope；错误边界只有 router 级，无路由级/面板级降级 |
| 状态流转 | 无白名单（任意↔任意，仅校验 key 存在 + triage 锁）；乐观锁 409 实测有效；非法 key 400 实测有效 |
| 子工单防环 | self / 直环 / 二级环 curl 全部 400 拦截，实现正确 |
| slot tab 四触点 | 静态复核**通过**（zod 放行 `pack:*`、类型、patchSearch、条件渲染 + ghost 兜底均有测试在案）；一处多 slot/pack 的 key 设计隐患（P1-3） |
| 测试盲区 | 前端三层测试全部按**错误契约** mock members（裸数组），P0 因此全绿漏网 |

---

## 1. 全链梳理：详情页组装与错误边界粒度

### 1.1 查询装配图（`web/src/routes/_app.work-items.$id.tsx`）

```
WorkItemDetailPage
├─ useQuery(['work-item', id]) → GET /api/work-items/{id}     ← 全页门闩：pending→PageLoading，error→PageError（行 48-56，这两态处理正确）
├─ usePackSlots(id)            → GET /api/ui-entries（staleTime 60s，select 展平 slot tab）
│
├─ 头部：StatusToggle（自query ['work-item-statuses']）+ MoreMenu
├─ Tabs：静态 4 tab + slotTabs（pack:<pack>）
├─ 内容区（按 search.tab 条件渲染）
│   ├─ detail    → DescriptionEditor（自有 PUT + 冲突框）
│   ├─ sub       → SubWorkItemsList（['work-item',id,'children']）
│   ├─ tasks     → TaskPanel（['work-item',id,'tasks']，展开卡另拉 messages）
│   ├─ timeline  → TimelineList（['work-item',id,'timeline']）
│   └─ pack:<p>  → PackSlotFrame（现铸 ui-token，不缓存）
├─ LabelsBlock（['work-item',id,'labels'] + ['labels']）       ← 常驻，不随 tab
├─ CommentThread（['work-item',id,'comments'] + members + agents）← 常驻，不随 tab
└─ InfoPanel（members + ['work-item-statuses'] + agents + ParentRow 内嵌列表查询）← 常驻 ★崩溃源
```

**依赖关系判定**：唯一的全页门闩是 `getWorkItem`——它失败时整页 `PageError`（合理，无工单无页面）。其余 9+ 个查询（members/statuses/agents/labels/comments/children/tasks/timeline/ui-entries）都是**面板级**依赖，任何一个失败在当前实现下都可能拖垮整页（见 1.2），这是本页最大的结构性问题。

### 1.2 错误边界现状（一个 query 失败 = 整页崩）

实测代码路径，本页存在**三层边界全部缺位**：

1. **无面板级 ErrorBoundary**：`web/src` 全仓 grep `componentDidCatch|getDerivedStateFromError|ErrorBoundary` 零命中——应用没有任何可复用错误边界组件。
2. **无路由级 errorComponent**：`_app.work-items.$id.tsx` 的 `createFileRoute` 只配了 `validateSearch` + `component`；全部 21 个路由文件无一声明 `errorComponent`。
3. **只有 router 级兜底**：`web/src/router.tsx:10` `defaultErrorComponent: RouteErrorComponent`——任何渲染期异常（如 TypeError）都替换整页内容为"页面出错了 + 重试/返回/回工作项"。

**崩溃链（P0-1，逐断点）**：

| # | 断点 | file:line | 说明 |
|---|---|---|---|
| ① | 契约源 | `vendor/github.com/haozing/ploykit/workspace/adapters/pgrepo/workspaces.go:140-165` + `app/service.go:227-230` | `GET /api/workspaces/{id}/members` 返回 `Page[Member]` = `{"items":[...],"total":N}`（curl 实证），item 形状 `{user_id,email,display_name,role,created_at}` |
| ② | 前端解包缺失 | `web/src/api/identity.ts:71-74` | `listMembers` 声明返回 `Member[]`，实现 `api.get<Member[]|null>(...)` 后 `items ?? []`——拿到的是 envelope 对象本身（truthy），原样上抛 |
| ③ | 渲染期取数崩 | `web/src/components/work-item/info-panel.tsx:26-34` | `creator_type==='user'` 时 render 期同步调 `resolveUser(creator_id)` → `members.data?.find(...)`：envelope 无 `.find` → TypeError。**用户创建的工单 100% 触发** |
| ④ | 边界缺位 | `_app.work-items.$id.tsx:138` | `<InfoPanel item={wi}/>` 无边界直挂 → 异常上抛 |
| ⑤ | 全页替换 | `web/src/router.tsx:10` + `components/route-error.tsx` | RouteErrorComponent 接管，详情内容全失（与活体截图 `work-item-detail.png` 一致） |

**本页同根因波及点**（修好 ② 后全部自愈）：
- `property-rows.tsx:41-45` AssigneeRow `resolveName`：`assignee_type==='user'` 时同款 `.find` 崩；
- `property-rows.tsx:74-84` AssigneeRow 菜单：`members.data?.length`/`members.data!.map`（envelope.length=undefined→菜单分组静默消失，`.map` 崩）；
- `comment-thread.tsx:44-53` mentionItems：`(members.data ?? []).map`——envelope truthy → 输入 `@` 弹 mention 时崩（不在 render 主路径，故活着体里先由 ③ 挡住）。

**错误边界改造建议（P2-2，方案）**：
- **最小修复（必做）**：`identity.ts:71-74` 改为解包——`const page = await api.get<{items: Member[]|null}>(...); return page.items ?? []`。一行修 7 个消费方（info-panel / property-rows / comment-thread / work-item-list / create-dialog / work-items.index / agents 页 perm tab）。
- **防御加固**：InfoPanel/AssigneeRow 的 `resolveUser` 里 `Array.isArray(members.data) ? members.data : []`——即使未来契约再漂移也只是名字退化为 id 前缀而非崩溃。
- **边界下沉（结构性）**：新建 `<QueryBoundary>`（class 组件，getDerivedStateFromError + 重试 prop），包裹 InfoPanel / CommentThread / LabelsBlock / TaskPanel 等面板级组件——单面板崩只显示面板内"加载失败+重试"，头部/标签/评论/Tabs 其余区域照常工作；再给 `$id` 路由配 `errorComponent` 保壳（侧栏/顶栏不丢）。

---

## 2. 契约对齐：本页各端点 envelope 逐字段核对（curl 实证）

| 端点 | 后端实际（curl 抓样） | 前端封装 | 判定 |
|---|---|---|---|
| `GET /api/work-items/{id}` | 裸对象（含 revision/status_category 等；`creator_type:'user'`） | `work.ts:87-89` 裸对象 | ✅ 匹配 |
| `PUT /api/work-items/{id}` | 200 裸对象；非法 status→400 `E_VALIDATION "unknown status key"`；旧 revision→409 `E_REVISION_CONFLICT` | `work.ts:113-118` + `use-field-update.ts:12-17`/`description-editor.tsx:42-54` 按 `err.code` 分流 | ✅ 匹配（但注意 PUT 响应 `status_category:""`，见 P2-8） |
| `GET /api/work-item-statuses` | 裸数组 7 内置项 | `work.ts:98-101` `?? []` | ✅ 匹配 |
| `GET /api/workspaces/{ws}/members` | **`{"items":[…],"total":1}`**，item 无 `id`/`workspace_id` 字段 | `identity.ts:71-74` 当裸数组 | ❌ **P0-1**；且 `Member` 接口（identity.ts:62-70）声明了后端不返回的 `id`/`workspace_id`（:21-27 还有重复声明，TS 后者生效） |
| `GET /api/work-items/{id}/labels` | 裸数组 | `work.ts:177-180` | ✅ |
| `POST …/labels`（attach） | 200 裸数组（attach 后全量） | `work.ts:181-183` `Label[]` | ✅ |
| `DELETE …/labels/{lid}`（detach） | **200 + body `null`**（空列表 nil slice 序列化） | `work.ts:184-186` 声明 `Label[]`；`label-chips.tsx:51-54` `applyList(null)` 后 `attached.data ?? []` 自愈 | ⚠️ 契约不一致但自愈（P2-8 附注） |
| `POST /api/labels` / `GET /api/labels` | 裸对象/裸数组 | `work.ts:170-176` | ✅ |
| `GET /api/work-items/children?parent=` | `{"items":[…],"next_cursor":null}` | `work.ts:81-86` 解 `items ?? []` | ✅ |
| `GET /api/work-items/{id}/timeline?limit=` | `{"items":[…]}`，DESC，activity 已由服务端渲染 content | `work.ts:196-201` 解 `items ?? []` | ✅（内容缺陷见 P2-1/P2-10） |
| `GET /api/ui-entries` | `{"items":[]}`（当前无 pack） | `packs.ts:35-38` 解 `items ?? []` | ✅（slot tab 无法活体验证，静态复核见 §4） |
| `GET /api/work-items/{id}/tasks` | `{tasks}`（经 `work-detail.test.ts:21-24` 与 orchestration 封装归一） | `api/orchestration.ts` listWorkItemTasks | ✅ |

---

## 3. curl 实测记录（:8080，cookie jar /tmp/fp04-cookies.txt）

| 用例 | 请求 | 结果 |
|---|---|---|
| 登录 | POST /auth/login | 200，user/workspace 就位 |
| members 抓样 | GET /api/workspaces/{ws}/members | `{"items":[{user_id,email,display_name,role,created_at}],"total":1}` → **P0-1 铁证** |
| 详情抓样 | GET /api/work-items/3b8bc342…（#1 工单） | 裸对象，`creator_type:"user"`、`revision:6` → 崩溃路径必触发 |
| 合法流转 | PUT {status:"in_progress", expected_revision:6} | 200，revision 6→7 |
| 非法 status | PUT {status:"no_such_status"} | 400 `E_VALIDATION unknown status key` ✅ |
| 乐观锁 | PUT {expected_revision: rev-1} | 409 `E_REVISION_CONFLICT` ✅（前端两处按 code 分流正确） |
| 任意↔任意流转 | started→unstarted（in_progress→todo） | 200——**无流转白名单**，后端只验 key 存在（`service_workitem.go:189-197` → `domain.WritableCategory`，status_catalog.go:108-115）；done/cancelled→todo 同样放行（未单测但代码路径相同） |
| 标签全链 | POST /api/labels → attach → list → detach → attach 不存在 id | 全通；detach 返回 200+`null`；attach 幽灵 id → 404 `E_NOT_FOUND` ✅ |
| 建子工单 | POST /api/work-items {parent_work_item_id} | 201（number=15，status_category 有值） |
| 查子工单 | GET /children?parent= | `{"items":[child]}` ✅ |
| 防环①自引用 | PUT {parent: 自身} | 400 `cannot set parent to itself` ✅ |
| 防环②直环 | PUT {parent: 自己的子} | 400 `parent chain forms a cycle` ✅ |
| 防环③二级环 | 孙→祖父（WI.parent=gc） | 400 `cycle` ✅（`service_workitem.go:239-253` 沿父链上溯 64 层） |
| 时间线 | GET timeline?limit=50 / limit=200 | 13 条，DESC 混排 comment/activity；activity content 全是 `"updated work item"`——**payload 里明明有 data.status='todo'**（→ P2-1 实锤） |
| ui-entries | GET /api/ui-entries | `{"items":[]}`（slot tab 无活体数据，静态复核） |

测试数据清理：fp04 子工单×2、孙工单、标签均 DELETE（204）。#1 工单状态/优先级已还原（todo/0）。

---

## 4. 代码质量

### 4.1 动态 slot tab 四触点静态复核（四波 C2 交付物验收）

| 触点 | 位置 | 结论 |
|---|---|---|
| ① zod schema | `_app.work-items.$id.tsx:25-28`：`z.enum(['detail','sub','tasks','timeline']).or(z.string().regex(/^pack:/)).optional()` | ✅ 放行 `pack:*`；garbage 触发 safeParse 失败 → 返回 `{}` 整组丢弃——**"整组丢弃"是注释明示的设计**（行 21-23），副作用是 `?tab=非法值&task=x` 会连 `task` 一起丢（低危，记录在案） |
| ② 类型 | 行 29 `DetailTab = z.infer<…>['tab']`，`use-pack-slots.ts:14` tab key 模板串 `` `pack:${string}` `` | ✅ 一致 |
| ③ patchSearch | 行 58-62：`navigate({search: prev=>({...prev,…patch}), replace:true})`；Tabs onValueChange 行 84 `String(v) as DetailTab` | ✅ 强转值域恰为 ①放行的值域；`replace:true` 不污染历史；toggleTask 折叠时 `task:undefined` 正确擦除 |
| ④ 条件渲染 + ghost 兜底 | 行 63-68 `knownStatic`/`activeSlot`/`showDetail`；行 100-121 slotTabs 渲染 | ✅ 逻辑正确；`work-item-detail-tabs.test.tsx:181-187` 有 `pack:ghost` 回退 detail 的专项测试。**残留瑕疵**：ghost 态下 `Tabs value='pack:ghost'` 无匹配 trigger → tab 栏无任何选中态而内容区显示 detail（P2-7，仅视觉） |

四触点逻辑正确、有测试在案（5 个用例覆盖无 slots/空响应/有 slots 切换/ghost 回退）。**PackSlotFrame 宿主**（铸票不缓存、sandbox、resize 同源校验+3s 回落、外链兜底、8 个用例）复核无逻辑问题。

### 4.2 编辑器保存链与防丢字（DescriptionEditor）

- 主链健康：1.5s 防抖自动保存 → `E_REVISION_CONFLICT` 时 fetchQuery 服务端版本 → 双栏冲突框（用服务端/保留我的重提，重提取 fresh revision）——设计完整，`description-editor.test.tsx` 3 用例覆盖冲突流。
- **P2-3 假冲突**：`description-editor.tsx:61-67` `flushSave` 在 `saveState==='dirty' && save.isPending`（首防抖已发出、未返回的窗口期）时再 `save.mutate(draft)`——两个并发 mutation 带**同一个 revision**，后者必 409 → 弹"服务端 vs 我的"冲突框且两栏内容相同。触发方式：打字后 >1.5s、保存返回前点击编辑器外（blur→stopEditing）。修法：`flushSave` 里 `if (save.isPending) return`（或挂起标记待返回后补发）。
- **P2-4 丢字面**：① Escape（:112-117）清防抖+退出编辑+saveState 复位——**草稿静默丢弃**，无确认无 toast；② 无 `beforeunload` 守卫，`saveState==='dirty'` 时关标签页直接丢；③ `saveState==='error'` 只有一行"保存失败"文案（:133），无重试按钮（下次输入会重排程，但用户未必知道）。建议：Escape 改为确认或"退出即保存"；dirty 态挂 beforeunload；error 态提供显式重试。

### 4.3 其余组件

- **LabelsBlock**：attach/detach 成功 `setQueryData` 直写 + create 后自动 attach + 6 色轮转 + 内联新建表单；错误经 `menuError` 展示。逻辑健壮（`?? []` 兜住了 detach 的 null 返回体）。
- **TaskPanel**：折叠/展开写 URL（可深链）、审批停靠（approve/deny）、活跃取消、WS scope 订阅。错误态有 `PageEmpty` 降级——**这是本页面板级降级的正确范本**，InfoPanel 应照此办理。
- **MoreMenu**：删除两段确认 + 错误展示 + 成功 invalidate 后跳列表。无问题。
- **ParentRow**（P2-6）：`doSearch` 只拉 `listWorkItems({limit:50})` 首页做**客户端过滤**——工单 >50 时候选池残缺且不透传 search 参数；且无 abort/序号守卫，快速输入时慢响应可覆盖新结果（300ms 防抖缓解不消除）。
- **SubWorkItemsList**（P2-9）：纯只读列表（链接跳子详情 + 状态徽章 + 时间）。**详情页无任何"创建子工单"入口**——创建弹窗无 parent 字段，后端 `POST /comments/{id}/sub-work-items`（评论转子工单）前端也未接。"子工作" tab 目前只能看不能建。
- **TimelineList**（P2-5/P2-10）：`limit=50` 硬编码无"加载更多"（后端 `before` 游标参数前端从未传）→ >50 条不可达；actor 显示 `t('comment.user') + id 前 6 位`，不解析成员名。

---

## 5. 后端正确性

| 项 | 结论 | 证据 |
|---|---|---|
| 状态机流转白名单 | **无白名单（设计如此）**：Update 仅校验 status key 存在于目录（`service_workitem.go:189-197`），任意 category 间自由流转（含 done→todo）。triage 中工单有字段锁（`TriageUpdateLocked`）。如需"取消的工单不能复活"类约束需另立规则——建议产品确认这是有意的扁平模型 | curl：in_progress→todo 200 |
| 乐观锁 | `repo.go:218` `($3::bigint <= 0 OR revision = $3)`：rev>0 强校验（409），rev=0 跳过——batch-update 用 0（免锁批量，有意） | curl 409 实证 |
| 子工单防环 | self / 直环 / 二级环全部 400；沿父链上溯上限 64 层——**边界注意**：合法链深 ≥64 时防环失效（理论边界，现网难触发） | curl 三例 400 |
| 时间线排序 | `repo.go:1028-1034` UNION ALL comments+activity，`ORDER BY created_at DESC LIMIT`——**无次级排序键**：同秒事件顺序不稳定，`before` 游标翻页时同 ts 行可能重/漏 | 静态 |
| 时间线 task_settled | `wiring_work.go:39-66`：outcome/summary/agent_name/failure_reason **扁平**落库 → `RenderActivityLine`（activity.go:26-37）顶层取值**匹配**，task 完成/失败事件渲染正确 | 代码对齐 |
| **时间线 work_item.updated 内容退化** | `wiring_work.go:129-134` `payloadMap` 把 WorkItemPayload 包成 `{"data":{…}}`，而 `activity.go:49-61 updateDetail` 顶层读 `payload["status"]/["assignee_type"]/["title"]` → **永远 miss**，所有 updated 事件退化成光杆 "updated work item"（curl 实证：payload.data.status='todo' 摆着，content 却无细节）。修法二选一：payloadMap 扁平化 WorkItemPayload 字段，或 updateDetail 读 `payload["data"]` | curl + 代码 |
| activity 单测脱节 | `domain/activity_test.go:16` 用**扁平 payload** 断言 `updated work item (status doing / assigned agent) (add RSS)`——单测绿、生产退化，接线处（wiring）无集成测试覆盖渲染文本 | 静态 |
| labels/子工单/时间线 envelope | 全部与前端解包一致（§2 表） | curl |

---

## 6. 测试覆盖

### 6.1 前端（web/test/）

- `work-item-detail-tabs.test.tsx`（5 用例）：四触点主流程覆盖良好——schema 放行/丢弃、无 slots 零回归、空响应、有 slots 切换挂载 PackSlotFrame、ghost 回退。**盲区**：InfoPanel 等 6 个子组件全 stub（:59-66），面板崩溃路径零覆盖；未测 `task` 参数与 tab 的联动（toggleTask）。
- `work-detail.test.ts:120-138`：**listMembers 的 msw mock 是裸数组**——按前端错误假设写测试而非后端真实 envelope，P0 因此在测试全绿下漏网。**修 P0 时必须同步把此 mock 改为 `{"items":[…],"total":1}`**，让测试锁住真实契约。
- `assignee-row.test.tsx:16`：整个 `membersQueryOptions` 被 vi.mock 掉——同样绕开契约。建议：api 层测试一律 mock HTTP（msw handler 返回**真实后端抓样**），组件层不 mock queryOptions。
- `pack-slot-frame.test.tsx`（8 用例）/ `description-editor.test.tsx`（3 用例）：覆盖良好；编辑器盲区=flushSave 并发窗口、Escape 丢弃、beforeunload。

### 6.2 后端（internal/work）

- 在案：revision 409（integration_test.go:293）、标题去重 409、状态 key 校验、`RenderActivityLine` 单测、timeline hydration（timeline_integration_test.go）。
- 盲区：① 防环三态（self/直环/多级）无 Go 测试（本次以 curl 补验）；② 时间线渲染文本的**接线级**测试（wiring payloadMap → repo ListTimeline → RenderActivityLine 全链）缺失——正是 P2-1 溜进来的缝；③ 无流转语义用例（如 closed→active 是否放行，当前无约束，测试也无从表达）。

---

## 7. 缺陷清单

### P0（1 项）

**P0-1 工单详情页整页崩溃：members envelope 契约不匹配**
- 根因：`web/src/api/identity.ts:71-74` `listMembers` 把 `{"items":[…],"total":N}` envelope（vendor ploykit `workspace/adapters/pgrepo/workspaces.go:140-165`）当 `Member[]` 返回。
- 本页断点链：`_app.work-items.$id.tsx:138`（无边界挂载）→ `info-panel.tsx:28` `members.data?.find` render 期 TypeError（creator_type='user' 必触发）→ 无路由级 errorComponent → `router.tsx:10` 全页 RouteErrorComponent。同页波及 `property-rows.tsx:43,74-84`、`comment-thread.tsx:45`。
- **最小修复**：`listMembers` 解包 `page.items ?? []`（一处修全部 7 个消费方：本页 3 处 + work-item-list + create-dialog + work-items.index + agents perm tab）。
- 顺手项：合并 `Member` 重复接口（identity.ts:21-27 与 62-70），删掉后端不存在的 `id`/`workspace_id` 字段。

### P1（3 项）

**P1-1 zh 缺 `status.*`（7 键）/`priority.*`（4 键）**：`en.ts:450-463` 有、`zh.ts` 无；`meta.ts` STATUS_META/PRIORITY_META 引用。详情页状态胶囊（StatusToggle/StatusRow/BadgeDot）与优先级行中文界面回退英文。补 11 键即修。

**P1-2 前端测试按错误契约 mock，P0 全绿漏网**：`work-detail.test.ts:120-138` mock members 为裸数组、`assignee-row.test.tsx:16` 整体 mock queryOptions、`work-item-detail-tabs.test.tsx:60` stub InfoPanel——三层全绕开真实 envelope。整改：msw handler 以真实后端抓样为准；为 InfoPanel 加一条"members 返回 envelope 形状时不崩"的回归用例。

**P1-3 slot tab key 与多 slot/pack 契约不匹配（潜伏）**：`use-pack-slots.ts:24-33` 每个 (pack,slot) 生成一个 tab 但 key 一律 `pack:<pack>`——pack 声明 ≥2 个 slot 时：React duplicate key、Tabs 两个同 value trigger、`slotTabs.find` 永远只命中第一个（第二个 slot 不可达）。后端 `service_uientry.go:69-82` validateSlots 明确允许 N slot/pack 且不查重复。修法：前端 key 用 `pack:<pack>:<slot.slot>`（schema regex 同步放宽），或后端收口"每 pack 至多 1 个 workitem.detail.tab"。当前无多 slot pack 数据，未爆发。

### P2（10 项）

| # | 缺陷 | 位置 | 修法 |
|---|---|---|---|
| P2-1 | 时间线 updated 事件内容退化（payload `{"data":…}` 嵌套 vs `RenderActivityLine` 顶层取值），丢 status/assignee/title 细节 | `cmd/orkome-server/wiring_work.go:129-134` ↔ `internal/work/domain/activity.go:49-61` | payloadMap 扁平化或 updateDetail 读 `data`；补接线级测试 |
| P2-2 | 错误边界只有 router 级——面板级查询异常即整页崩 | `router.tsx:10`（全仓无 ErrorBoundary 组件、路由无 errorComponent） | 新建 QueryBoundary 包 InfoPanel/CommentThread/LabelsBlock/TaskPanel；$id 路由配 errorComponent 保壳（详见 §1.2） |
| P2-3 | flushSave 在保存飞行中重复 mutate（同 revision 并发）→ 必然自冲突 → 假冲突弹窗（两栏内容相同） | `description-editor.tsx:61-67` | `if (save.isPending) return` 或挂起补发 |
| P2-4 | Escape 静默丢弃草稿；无 beforeunload 守卫；error 态无重试按钮 | `description-editor.tsx:112-117,133` | Escape 确认或退出即存；dirty 挂 beforeunload；error 提供重试 |
| P2-5 | 时间线 limit=50 硬编码无分页 UI（后端 `before` 游标未接）→ >50 条不可达；`ORDER BY created_at` 无次级键，同秒翻页可重/漏 | `work.ts:196-201`、`timeline-list.tsx:16-19`、`repo.go:1028-1034` | 前端接 before 游标+加载更多；SQL 加 `id DESC` 次级键 |
| P2-6 | ParentRow 只拉首页 50 条客户端过滤 + 竞态无守卫 | `property-rows.tsx:250-263` | 透传 search 参数到服务端；seq/AbortController 守卫 |
| P2-7 | ghost tab 兜底只兜内容不兜高亮：`?tab=pack:ghost` 时 tab 栏无选中态 | `_app.work-items.$id.tsx:83 vs 68` | Tabs value 改 `showDetail && !activeSlot ? 'detail' : search.tab`，或检测到 ghost 时 patchSearch 清 tab |
| P2-8 | PUT 响应 `status_category:""`（Update 不 fill）且被 `setQueryData` 写入缓存；labels detach 返回体 `null`（与 attach 数组不一致） | `service_workitem.go:168-289`（对照 Create:102）；`repo.go` GetWorkItemLabels nil slice；`work.ts:184-186` | Update 返回前 `fillWireCategories`；labels 空列表回 `[]`（前端 `?? []` 已自愈，属契约卫生） |
| P2-9 | 子工单 tab 只读：详情页无创建子工单入口（创建弹窗无 parent 字段；后端 `POST /comments/{id}/sub-work-items` 前端未接） | `sub-work-items.tsx`、`create-work-item-dialog.tsx` | sub tab 加"新建子工单"（复用创建弹窗注入 parent）；或接评论转子工单链路 |
| P2-10 | 时间线 activity 文案硬编码英文（`RenderActivityLine`），zh 用户看到英文；actor 显示 `用户 7c8931…` 不解析姓名 | `internal/work/domain/activity.go`、`timeline-list.tsx:38-45` | 服务端出 event key + 参数、前端 i18n 渲染；actor 接 members/agents 解析 |

### 修复顺序建议

1. **P0-1**（identity.ts 一处解包 + 同步修正 work-detail.test.ts mock）→ 详情页立活，五处连坐全解。
2. **P2-2**（QueryBoundary + 路由 errorComponent）→ 把"一崩全崩"降为"一崩降一格"，此后任何单点契约漂移不再有整页爆炸半径。
3. **P1-1**（补 zh 11 键）、**P1-2**（契约测试整改）。
4. **P2-1**（时间线接线）→ 修复后回访验证 slot tab（装一个真 pack）与时间线 task_settled 渲染。
5. 其余 P2 逐条顺手修。

---

## 附：证据留存

- curl 会话：/tmp/fp04-cookies.txt（登录态 inventory-test@orkome.dev）；后端日志 /tmp/orkome-server.log。
- 实测工单：#1 `3b8bc342-d79a-4a3f-be4e-5868ca976465`（状态/优先级已还原）；测试子工单、标签已清理（204）。
- 关联文档：`docs/inventory-pages-live-2026-10-12.md` §5.2（活体崩溃截图与 console 原文）、`docs/inventory-2026-10-12.md` P0-1、`docs/inventory-frontend-static-2026-10-12.md` §2.6。
