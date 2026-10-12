# FP-18 功能点审计：work 域高级端点（batch / query / table / views / metadata / properties / labels / statuses / projects，~49 条无前端消费面）

> 日期：2026-10-12 · 审计人：FP-18 专项 agent · 仓库 D:\code\orkome
> 范围：`docs/inventory-api-2026-10-12.md` §3 work 域 78 条中，扣除 FP-03（创建/列表/quick-create）、FP-04（详情/labels 挂载/子工单/时间线）、FP-06（评论全家族）已审部分后的"后端先行"高级面，共 **49 条**（清单见 §1）。
> 环境：后端 :8080（真实启动）+ 前端 :5173（未开浏览器）；账号 inventory-test@orkome.dev · 工作区 inventory-ws（e83dd858-7412-4625-b048-0741e5ae6552）；cookie jar /tmp/fp18-cookies.txt；测试数据 fp18 前缀（已清理，残留 4 个自定义状态——状态目录无删除端点，见 D-13）。
> 方法：七步审计法（全链梳理 → 契约对齐 → curl 实测 55 组用例 → 代码质量 → 后端正确性 → 测试覆盖 → 缺陷清单）。
> 前置：`docs/feature-audit-batch1-2026-10-12.md`（引用其 FP-03 D-02 脏输入 500 / FP-03 D-06 通配符 / 根因 D NULL-scan 家族编号，不重复展开）。

---

## 0. 一句话结论

高级面的**安全底座是好的**（wsGuard 全覆盖越权 403、SQL 全参数化注入面干净、metadata/properties 类型系统完整、outbox 事件一致性有测试），但**"查询/表格/搜索/批次/预览"五个面向前端的门面端点全是坏的**：table/rows 自产游标自己回放必 500 且 id 返回字节数组（P0）、batch 静默部分成功且不支持字段被无声丢弃、query 面的 labels/priority_min 过滤器是静默 no-op、preview-trigger 内容路径 100% 500、labels PUT 完全忽略路径 id。**结论：读改写类小端点（metadata/properties/labels/statuses/projects/views）可接前端；类数据库查询面（table 三件套 + search 非 federated）需返工；batch/preview-trigger 契约需重定。**

**缺陷计数：P0×1 · P1×7 · P2×11**

---

## 1. 全链梳理（49 条端点 × 分层 × 前端消费 × 实测判定 × 就绪度）

> 认证一律 routes.go:76-92 wsGuard（X-Workspace-Id + MemberCheck），除 client-usage 叠加 RequireHuman+Session（routes.go:172）。
> "前端"列 = web/src 是否有调用点（依据 inventory §14 B 档 + 本次 grep 复核）。

### 1.1 查询面（类数据库）

| 端点 | handler | service | repo | 前端 | 实测 | 就绪度 |
|---|---|---|---|---|---|---|
| POST /api/work-items/query | handlers.go:680 | ViewService.Compile（service_query.go:176）+ CompileParsed（:60） | ListStatuses/ListPropertyDefs 只读 | 否 | 200/400 正确；labels/priority_min 被回显但不过滤（D-04） | 可接（校验面） |
| POST /api/work-items/table/rows | handlers.go:693 | ViewService.Rows（service_catalog.go:425） | QueryRows（pgrepo:1064，scanMaps） | 否 | 200 但 id=字节数组、列名 `coalesce`、**任何 cursor（含自产）→500**（D-01/D-11） | **需返工** |
| POST /api/work-items/table/groups | handlers.go:710 | ViewService.Groups（:466） | QueryRows | 否 | 4 个 group_by 全 200，聚合正确 | 可接 |
| POST /api/work-items/table/facets | handlers.go:726 | ViewService.Facets（:499） | QueryRows | 否 | 200；仅 status/assignee 两维（D-18） | 可接（能力缺口） |
| GET /api/work-items/search（plain） | handlers.go:741 | SearchService.Search（service_catalog.go:691） | SearchRows（8s statement_timeout） | 否 | 200 但 id 字节数组 + snippet 恒为标题前 80 字符（D-09） | 需返工 |
| GET /api/work-items/search?mode=federated | 同上 | SearchService.SearchFederated（:712，RRF） | SearchRows | 否 | 200，SearchHit 结构正确（title/body/comment 三路 RRF） | 可接 |
| GET /api/work-items/grouped | handlers.go:193 | Views.Groups("assignee") | QueryRows | 否 | 200 `[{k,n}]` | 可接 |
| GET /api/work-items/assignee-frequency | handlers.go:202 | **与 grouped 完全同参同实现** | 同上 | 否 | 响应逐字节相同（D-10） | 建议裁决 |
| GET /api/projects/search | handlers_catalog.go:71 | CatalogService.SearchProjects（:216） | SearchProjects（repo.go:549） | 否 | 200/400 正确 | 可接 |
| POST /api/work-items/preview-trigger | handlers.go:765 | WriteTriggerProbe / PreviewTrigger（service_comment.go:75）→ orchestration TriggerService.Preview | HistoryAgentIDs / ListSquadFacts | 否 | change_kind 探针 200；**带 content 必 500**（D-07，双因） | 需返工 |
| POST /api/work-items/{id}/comments/trigger-preview | handlers.go:120 同 handler | 同上 | 同上 | **是**（work.ts previewCommentTrigger） | 500（无 leader 小队数据态，D-07②） | 需返工（连带前端） |

### 1.2 批量与排位

| 端点 | handler | service | repo | 前端 | 实测 | 就绪度 |
|---|---|---|---|---|---|---|
| POST /api/work-items/batch-update | handlers.go:127 | CheckBatchTriageLock（service_workitem.go:478）+ 循环 Update(rev=0) | UpdateWorkItem | 否 | 200 `{updated:N}`；部分失败静默、不支持字段静默丢弃、无效 status 200+0（D-03） | 契约需返工 |
| POST /api/work-items/batch-delete | handlers.go:150 | 循环 Delete | DeleteWorkItem（FK 级联） | 否 | 200 `{deleted:1}` 幽灵跳过 | 同 D-03 |
| POST /api/work-items/{id}/move | handlers.go:110 | WorkItemService.Move（service_workitem.go:312，四分支锚点语义） | MovePosition/NextPositionInStatus | 否 | 204；**非法 JSON 仍执行写**（D-08）；垃圾锚点 500（D-15） | 可接（修 D-08 后） |
| GET /api/work-items/child-progress | handlers.go:183 | ChildProgress（service_workitem.go:826） | repo.go:1341（parent 分组 count） | 否 | 200/400/404 全对 | 可接 |

### 1.3 目录面：labels / statuses / projects / properties 定义

| 端点 | handler | service | repo | 前端 | 实测 | 就绪度 |
|---|---|---|---|---|---|---|
| GET /api/labels/{id} | handlers_catalog.go:10 | GetLabel（service_catalog.go:134） | GetLabel | 否 | 200/404 ✓ | 可接 |
| PUT /api/labels/{id} | handlers.go:527 **= handleCreateLabel** | UpsertLabel（:118） | UpsertLabel ON CONFLICT(workspace_id,name)（repo.go:384） | 否 | **忽略 labelId：PUT 改名 → 201 全新标签**（D-02） | 契约需返工 |
| DELETE /api/labels/{id} | handlers.go:529 | DeleteLabel（:145） | 三张关联表事务清理（repo.go:393-408） | 否 | 204/204 幂等（幽灵也 204，观察项） | 可接 |
| POST /api/work-item-statuses | handlers.go:583 | CreateStatus（service_catalog.go:49，category 白名单 + DeriveStatusKey） | CreateStatus（repo.go:55） | 否 | 201 派生 key；空名/非法 category 400 ✓ | 可接 |
| POST /api/work-item-statuses/reorder | handlers.go:599 | ReorderStatuses（:74） | 逐 key UPDATE（repo.go:98） | 否 | 204；幽灵 key/部分 keys 静默（D-13） | 可接（守卫缺） |
| PATCH /api/work-item-statuses/{key} | handlers_catalog.go:19 | UpdateStatus（:78，builtin category 锁） | UpdateStatus（repo.go:73） | 否 | 改名/图标/自定义 category 200；builtin 改 category 400；未知 key 404 ✓ | 可接 |
| GET/PUT/DELETE /api/projects/{id} | handlers.go:145/146/147 | GetProject/UpdateProject/DeleteProject（service_catalog.go:174-214，非空 409） | repo.go:509-547 | 否 | 全环 ✓；非空删除错误码误用 E_REVISION_CONFLICT（D-14）；重名 PUT 400、重名 **POST 500**（D-06） | 可接（修 D-06 后） |
| GET/POST/PUT/DELETE /api/projects/{id}/resources | handlers.go:148-151 | 4 方法（service_catalog.go:236-308，kind/uri 白名单校验 :224-234） | project_resource.go | 否 | 全环 ✓；kind=banana 400、ftp 400 | 可接 |
| GET/POST /api/properties | handlers.go:613/622 | ListPropertyDefs/CreatePropertyDef（:316-334，9 类型白名单、select 必带 options、key 自动 slug） | repo.go:577-611 | 否 | 创建/校验 ✓；**归档后重建同名 key 500**（D-05） | 可接（修 D-05 后） |
| GET/PATCH/DELETE /api/properties/{key} | handlers_catalog.go:131/140/161 | Get/Update/ArchivePropertyDef（:340-391，归档软删） | repo.go:602-667 | 否 | 200/404 ✓ | 可接 |

### 1.4 工单级键值面（metadata / properties 值 / 订阅 / 表情 / 重复）

| 端点 | handler | service | repo | 前端 | 实测 | 就绪度 |
|---|---|---|---|---|---|---|
| GET/PUT/DELETE /api/work-items/{id}/metadata/{key} | handlers.go:264-301 | GetMetadataKey/SetMetadataKey/DeleteMetadataKey（service_workitem.go:610-679，key≤128、值≤16KB、json.Valid） | repo.go:1169-1211（jsonb_build_object 参数化） | 否 | 全环 ✓（含特殊字符 key `we$ird"key`、超值 400、幽灵 404）；revision 联动+1 | 可接（质量最好的一组） |
| GET/PUT/DELETE /api/work-items/{id}/properties/{key} | handlers.go:303-343 | GetPropertyKey/SetPropertyKey/DeletePropertyKey（:685-786，走 domain.ValidateWorkItemProperties 9 类型系统） | repo.go:1213-1255 | 否 | 全环 ✓：number/select/unknown type/未知 key/空串清除路径全 400/200 正确 | 可接 |
| GET/POST /api/work-items/{id}/subscribers | handlers.go:484/493 | Subscribe（service_workitem.go:409，幂等 upsert） | UpsertManualSubscriber | 否 | 200/201 ✓（重复仍 201，D-17 观察项） | 可接 |
| POST /api/unsubscribe（/subtree） | handlers.go:885 | Unsubscribe / UnsubscribeSubtree（:528-581，RECURSIVE CTE + advisory lock） | repo.go:869-927 | 否 | 自身 200、缺 id 400、**非成员目标 403**（target 成员校验在，FP-03 D-09 的对照正面样本）、subtree 返回 removed 列表 | 可接 |
| POST/DELETE /api/work-items/{id}/reactions | handlers.go:211/227 | AddReaction/RemoveReaction（service_workitem.go:790-824，emoji≤32B） | repo.go:1257-1297（work_item JOIN fence） | 否 | 201/200 ✓；重复 add 幂等 201 | 可接 |
| GET /api/work-items/{id}/duplicates | handlers.go:255 | Duplicates（service_workitem.go:594） | ListDuplicates（repo.go:1147） | 否 | 200 `{items:[]}` ✓ | 可接 |
| POST /api/client-usage | routes.go:172（RequireHuman+Session） | ClientUsageService.Report（service_labels.go:80-105，install_id uuid、版本 ASCII≤64、OS 白名单） | ClientUsageUpsert（日粒度 upsert） | 否 | 204 ✓（需 X-Client-Platform: web/desktop header）；缺 install_id / 非法 uuid 400 ✓ | 可接 |

### 1.5 保存视图

| 端点 | handler | service | repo | 前端 | 实测 | 就绪度 |
|---|---|---|---|---|---|---|
| GET /api/work-item-views | handlers.go:837 | Views（service_catalog.go:625，**按 user 私有**） | ListWorkItemViews（repo.go:937） | 否 | 200 `{items:[...]}`（空→[] 兜底） | 可接 |
| PUT /api/work-item-views | handlers.go:849 | SaveView（:629，name 1-100 rune、id uuid 校验、ws+user fence） | UpsertWorkItemView（repo.go:956，INSERT/UPDATE 双分支） | 否 | 创建/改名/幽灵 404/非法 id 400/空名 400 全对；**无 DELETE 端点**、filters 无 schema 校验（D-12） | 可接（缺口项） |
| GET/PUT /api/work-item-view-preferences | handlers.go:862/871 | ViewPreference/SetViewPreference（:657-683，默认视图归属校验） | repo.go:977-1001 | 否 | 空→""、设置/清除/幽灵 400 全对 | 可接 |

---

## 2. 契约对齐要点（逐组核对结论）

1. **envelope 分裂**：query 面返回 `{fingerprint, items, next_cursor}`（rows）vs `{items}`（search/duplicates/subscribers/facets 的外层是裸数组）vs views `{items}` vs labels/statuses/projects 裸数组——同域三种形状。前端接入需逐一适配（inventory §3 的 `是` 列端点已各自适配，新接入端点无现成解包惯例可抄）。
2. **rows 响应字段名错乱**（D-01 的一部分）：`SELECT wi.id, …, coalesce(wi.assignee_id::text,'')`（service_catalog.go:451-452）没有 AS 别名 → 输出键为 `coalesce`；`wi.id` 是 uuid → pgx Values() 返回 [16]byte → JSON 变数字数组。`created_at` 是 time.Time → JSON 序列化为 RFC3339（此项幸存可用）。**id/assignee 两列不可用。**
3. **/query 的 parsed 回显与实际过滤能力不一致**（D-04）：FilterSummary（service_query.go:33-44）包含 Labels/PriorityMin，compileFilters（service_catalog.go:523-571）不消费——回显了"我不会执行"的过滤器。同理 rows/groups/facets 的 filters JSON 里传 `labels`/`priority_min` 静默无效（实测 priority_min:99 全量返回）。
4. **batch 响应无失败清单**（D-03）：`{updated:N}` 无法告知哪些 id 失败及原因；`patch` 是 map[string]any，mapToPatch（handlers.go:959-981）白名单外字段（description/due_date/assignee_type 单独出现等）无声丢弃——批量改描述这一最常见诉求实际不被支持且无报错。
5. **labels PUT 语义**（D-02）：REST 惯例 PUT /{id}=按 id 更新；实现是按 (ws,name) upsert（handlers.go:527 直接复用 handleCreateLabel，路径参数根本不进 service）。实测 PUT 用新名 → 201 新 id，原 id 不变。
6. **status catalog wire 折叠一致**（D3 关联核对结论）：自定义终态（category=closed）工单在创建响应与列表/详情响应中 `status_category` 均折叠为 `"cancelled"`（domain.WireCategory→wireFold，status_catalog.go:54-81，实测双路径一致）——HTTP 面没问题；D3 发现的"webhook payload 无 category"是 WS/webhook 投影缺字段，不是本端点组的问题，两者互补后结论：**折叠逻辑健在，缺的是把 category 放进 WorkItemPayload**。
7. **views fences 正确**：UpsertWorkItemView WHERE workspace_id+user_id+id（repo.go:968）；SetViewPreference 校验 default_view_id 属于本人（service_catalog.go:662-682，幽灵 400 实测）；View 结构体的 WorkspaceID/UserID 字段虽可由 body 注入但 repo 只用函数参数（args $1/$2），无越权写。
8. **错误码**：全程统一 `{"error":"E_*","message","details"}` envelope；但项目非空删除复用 E_REVISION_CONFLICT（D-14）、脏输入在 404/400/500 间漂移（D-15）。

---

## 3. curl 实测记录（55 组用例，全部 :8080 真实后端）

### 3.1 query/table 面为重点（摘录）

| # | 用例 | 结果 | 判定 |
|---|---|---|---|
| 1 | POST /query 合法（category 展开 unstarted→[backlog,todo]、sort 白名单、limit clamp 1-200） | 200 fingerprint+parsed | ✅ |
| 2 | /query group_by=banana + sort="evil; DROP TABLE x:asc" | 400 双 field details | ✅ 白名单生效 |
| 3 | /query labels=["nonexistent"]+priority_min=99 | 200，parsed 回显两过滤器 | ❌ 静默 no-op（D-04） |
| 5 | rows filters.priority_min=99 | 200 全量（priority=1 也在） | ❌ 同上 |
| 6 | rows limit=2 取 next_cursor | 200；cursor=`2026-10-12 12:05:18.628791 +0800 CST|[222 201 …]`（Go %v 原样） | ❌ 自产游标非法（D-01） |
| 7 | **rows 回放自产 cursor** | **500 E_INTERNAL** | ❌ **P0** |
| 8/9 | rows cursor="a|b" / "notatime|<合法uuid>" | 均 500 | ❌ cursor 校验只查"|"（D-11） |
| 10 | rows limit=9999/-5 | 200 clamp 200/50 | ✅ |
| 11 | groups group_by=status/assignee/label/project + 非法 | 4×200 + 400 | ✅ 聚合正确 |
| 12 | facets search=fp18 | 200 `{assignee:[…],status:[…]}` | ✅（维度缺口 D-18） |
| 13 | GET /grouped vs /assignee-frequency 同参 | **响应逐字节相同** `[{k:"none",n:3}]` | ❌ 别名复制品（D-10） |
| 14 | /search plain：id 字节数组；federated：SearchHit 字符串正确；`%' OR 1=1 --` → 0 条；空 q 400 | 参数化安全 | ⚠️ plain 模式 D-09 |

### 3.2 batch / move

| # | 用例 | 结果 | 判定 |
|---|---|---|---|
| 15 | batch-update 2 合法 id status→todo | 200 `{updated:2}`，逐项生效+事件 | ✅ |
| 16 | batch-update [合法, "not-a-uuid"] | 200 `{updated:1}`，无失败清单 | ❌ 静默部分成功（D-03） |
| 17 | batch-update patch={"description":…} | 200 `{updated:1}`，**description 未变、revision 1→2** | ❌ 字段丢弃+幽灵 revision 跳变（D-03） |
| 18 | batch-update patch={"status":"no_such"} | 200 `{updated:0}` | ❌ 全军覆没静默（D-03；对比单条 PUT 同输入是 400） |
| 19 | ids=[] → 200 {updated:0}；ids="banana" → 400 | | ✅/✅ |
| 20 | batch-delete [合法,幽灵] | 200 `{deleted:1}` | ⚠️ 同 D-03 语义 |
| 21 | triage 锁：set triage_state 后批量改 status | 400 `batch update blocked: work item in triage` | ✅ 批量前置检查在 |
| 22 | move {} → 204 置顶 | | ✅ |
| 23 | **move body=`{invalid json!!`** | 响应 400，**updated_at 12:09:01→12:09:20（写已执行）** | ❌ D-08 |
| 24 | move before="zzz" | 500 | ❌ D-15 家族 |
| 25 | move 锚点异列 | 400 `anchor must be … same status column` | ✅ |

### 3.3 metadata / properties / labels / statuses / projects / views（健康组摘录）

| # | 用例 | 结果 |
|---|---|---|
| 30 | reactions ASCII 环：add 201 count1 → DELETE query param 200 count0 | ✅（初次 👍 用例失败系控制台 GBK 编码假象，ASCII 复测通过） |
| 31 | metadata：put 标量/嵌套对象 → get → 非法 JSON 400 → 空体 400 → 20KB 400（≤16KB）→ 129 字符 key 400 → `we$ird"key` 原样存取 → DELETE 204 → get 404 → 幽灵工单 404 | ✅ 全对 |
| 32-35 | property defs：number/select/text 201、banana 400、select 无 options 400、key 自动 slug；工单级：8→200、"eight"→400 want number、"ultra"→400 not in definition、unknown key→400、""→200 清除路径 | ✅ 类型系统完整 |
| 36 | PATCH 改名/加 option 200、未知 key 404；**DELETE 后重建同名 → 500** | ❌ D-05 |
| 37 | GET /api/work-items/not-a-uuid → 404；同 id 访问 metadata → 500 | ⚠️ D-15 不一致 |
| 38 | **PUT /api/labels/{A} 改名 → 201 新 id 554cc5da…，A 原样** | ❌ D-02 |
| 39 | DELETE label 204×2（幂等）、GET 404 | ✅（风格观察项） |
| 40-43 | statuses：自定义 started/closed 201 派生 key；空名/非法 category 400；PATCH 改名+图标 200、builtin 改 category 400、自定义改 category 200；reorder 204 按给定 keys 重写 position（幽灵 key 静默） | ✅/⚠️ D-13 |
| 44 | projects：CRUD 200/201/400/404/409 全对；**POST 重名 500**（D-06）；search 200/400 | ❌/✅ |
| 45 | resources：CRUD ✓、kind=banana 400、ftp:// 400 | ✅ |
| 46-47 | views：创建/列表/更新/幽灵 404/非法 id 400/空名 400；preferences：GET ""、设置/幽灵 400/清 null | ✅ |
| 48-50 | preview-trigger：change_kind 探针 200 blocked；**带 content（含 mention 格式、含空 content）→ 500**（两条路由全中，前端在用的 comments/trigger-preview 也 500） | ❌ D-07 |
| 51 | 越权：伪造 ws 403、缺 header 400、无 CSRF 403、metadata 跨 ws 403 | ✅ fence 硬 |
| 52-54 | 注入：props contains `%'-- ; DROP TABLE work_item;--` → 200 空结果；search 注入 → 200 空结果；metadata key `x');--DROP` 原样存取；props>20 → 400；未知 prop key → 400 | ✅ 参数化无死伤 |
| 55 | client-usage 带 X-Client-Platform: web → 204 | ✅ |

---

## 4. 代码质量与一致性

| 检查项 | 结论 |
|---|---|
| 错误处理 | writeErr（routes.go:30-54）统一 apierr→HTTP 映射；writeErr 对非 apierr 一律 500 且**不打日志**（batch1 模式 4 的又一实例：D-01/D-05/D-06/D-07 服务端均无根因痕迹，本次全靠日志 grep + DB 直查定位） |
| decode 惯例 | `decode[T]` 统一 1MB 上限+400；**3 处 `in, _ :=` 忽略 ok**（handlers.go:111 move 实害、:231/:459 reactions DELETE 无害）——一致性破坏点 |
| handler 复制 | handleAssigneeFreq ≡ handleGroupedByAssignee（:193-209）；handleUpsertLabel ≡ handleCreateLabel（:527）——两处都是"语义没做完就挂路由"的形状 |
| service 分层 | CatalogService/ViewService/SearchService 职责清晰；QueryFilters.Fingerprint（sha256 8 字节）给前端做缓存键——设计意图好，但输入过滤器未实现导致 fingerprint 稳定地指纹着错误结果 |
| 遗产一致性 | CreateProject 无 unique 映射而 UpdateProject 有（repo.go:501 vs 531）；GetWorkItem 吞一切 DB 错误为 404（service_workitem.go:106-113）而 GetMetadataKey 不吞——同域两套哲学 |

## 5. 后端正确性（注入 / 事务 / fence / 性能）

| 项 | 结论 |
|---|---|
| SQL 注入 | **干净**。动态 SQL 只拼参数占位符（compileFilters/filter_conds.go 全 `$n`）；group_by/sort 经白名单 map；limit 走 %d 整数；jsonb 键经 jsonb_build_object($3::text,…) 参数化。注入实测零死伤（§3.3 #52-54） |
| 越权 | **硬**。wsGuard 全覆盖；跨 ws 实测 403；订阅面 target 成员校验（handlers.go:900-906）；views/labels/projects/metadata 均带 ws 条件。未发现 FP-06/FP-08 式 IDOR |
| 事务边界 | batch 是**有意**的逐条自治（部分成功语义）+ 前置 triage 检查——语义可用但契约未显式化（D-03）；ReorderStatuses/DeleteLabel/RemoveSubscriberSubtree 事务内多语句 ✓；AdvisoryLockTitle no-op 问题归 FP-03 D-03 不重复 |
| 性能 | groups/facets 单查询 GROUP BY 无 N+1；rows 单查询+keyset；search 8s statement_timeout+50 上限（MapSearchTimeout→503）✓；**隐患**：rows/groups/facets 的 search ILIKE 全表扫（无 trgm 使用，1001 索引是 trgm 但 ILIKE '%x%' 能吃到）；props 过滤 `properties->>$n` 逐行解析无索引（表小无碍，预留）；ReorderStatuses 逐行 UPDATE（状态目录 ≤ 十几行，无碍） |
| fence 细节 | UpsertReaction/DeleteReaction/ListReactionCounts 均 JOIN work_item 带 ws 条件 ✓；RemoveSubscriberSubtree RECURSIVE CTE 限定 workspace ✓ + advisory xact lock **在事务内**（与 FP-03 D-03 的裸锁形成对照，这里是正确写法） |

## 6. 测试覆盖映射

**有测试**（层：http=httptest fake / integ=真实 DB / e2e=全栈）：

- query compile：service_query_test.go ×5 + http TestQueryCompileRichDimensions（resources_routes_test.go:319）
- prop 过滤算子：filter_conds_test.go + http TestListPropOperators（只测 GET /api/work-items 链路，rows 链路无）
- metadata/reactions/duplicates/child-progress/usage：subresource_integration_test.go M2 系列 ×5（integ）
- properties key/def：routes_catalog_test.go TestPropertyKeyEndpoints/TestPropertyDefManagement（fake）
- labels：TestLabelsIncrementalMount/TestGetLabel（fake；**PUT-by-id 语义零断言**——D-02 漏网原因）
- statuses：TestStatusPatch（fake，含 builtin 锁/图标 rune 上限）+ service_catalog_test.go CreateStatus 白名单
- projects：TestProjectCRUD（含 search 400）+ TestProjectResourcesEndpoints（含跨 ws 404）
- client-usage：handlers_clientusage_test.go ×3
- outbox：outbox_tail_test.go 对 metadata/property/reaction/subscribe/unsubscribe 全部有同事务断言
- e2e：goldens_test.go:45 一条 `table/rows → 200`（**只断言状态码，不查字段名、不翻页**——D-01 全绿漏网的直接原因）

**零测试端点清单**（HTTP 层无任何用例）：

`POST /move`、`batch-update`（仅 service 层 TestCheckBatchTriageLock）、`batch-delete`、`table/rows`（e2e 一条 200 除外）、`table/groups`、`table/facets`、`GET /search` plain 模式、`GET /grouped`、`GET /assignee-frequency`、`POST /preview-trigger` 两条路由、`GET /{id}/subscribers`、`POST /unsubscribe` 两条、`GET/PUT /work-item-views`、`GET/PUT /view-preferences`、`POST /work-item-statuses`、`POST /work-item-statuses/reorder`、`PUT/DELETE /labels/{id}`。

> 规律：P0（rows cursor）与 5 个 P1（batch 契约/labels PUT/preview-trigger/props 过滤器/labels 重建）全部落在零测试或"只断言 200"的端点上——**这批端点的缺陷不是漏检，是从未检过**。

---

## 7. 缺陷清单

### P0（1 项）

**D-01 table/rows 端到端损坏：自产 next_cursor 回放必 500 + id/assignee 字段不可用**
- 三处根因：①`SELECT wi.id, …` 无 `::text`（service_catalog.go:451-452）→ pgx Values() 把 uuid 还原为 [16]byte → JSON 输出 `[205,204,…]` 数字数组；`coalesce(wi.assignee_id::text,'')` 无 AS 别名 → 字段名是字面量 `"coalesce"`；②next_cursor 用 `fmt.Sprintf("%v|%v", last["created_at"], last["id"])`（service_catalog.go:458-462）拼出 Go time.Time/byte-slice 的 %v 文本（`2026-10-12 … +0800 CST|[222 201 …]`）；③回放时 `(wi.created_at, wi.id) < ($n::timestamptz, $n)`（:448）解析失败 → pgx 22P02 → 500。cursor 结构校验只查"|"（parseCursor :617-623），垃圾 cursor 同 500（D-11）。
- 实测：#6→#7 两次调用自证；e2e goldens 只断言首页 200 因此全绿。
- **修复**（一处 SELECT + 一处 cursor 格式化即可，参照 ListWorkItems 已正确实现 repo.go:322）：`wi.id::text AS id, coalesce(wi.assignee_id::text,'') AS assignee_id, to_char(wi.created_at,'YYYY-MM-DD"T"HH24:MI:SS.USOF') AS created_at`（或查询后格式化）；next_cursor 改 `last.CreatedAt.Format("2006-01-02T15:04:05.999999Z0700")` 同款；parseCursor 增加时间戳/uuid 预校验失败返 400。
- 就绪度：**需返工**（这是表格视图的主链路，翻第二页即死）。

### P1（7 项）

**D-02 PUT /api/labels/{labelId} 完全忽略 labelId（按 name upsert 冒充按 id 更新）**
- handlers.go:527 `handleUpsertLabel` 直接转发 handleCreateLabel，路径参数不进 service；repo UpsertLabel ON CONFLICT (workspace_id,name)（repo.go:384-391）。
- 实测：PUT {A} + 新名 → 201 全新标签，A 原样（§3.3 #38）。前端按 REST 语义接标签管理页=改名必凭空多标签。
- 修复：service 加 UpdateLabelByID(ctx, ws, id, name, color)（UPDATE … WHERE workspace_id AND id RETURNING；404 语义现成）；或如保留 upsert 语义则删掉 PUT /{id} 路由避免误导。

**D-03 batch-update/batch-delete 静默部分成功 + 不支持字段静默丢弃 + 幽灵 revision 跳变**
- handlers.go:127-164 循环吞错只回 `{updated:N}`；mapToPatch（:959-981）白名单仅 title/status/priority/assignee/properties，`description`/`due_date`/`labels` 等批量最常见字段被丢弃；UpdateWorkItem 无条件 `revision=revision+1`（repo.go:214）——**空 patch 也跳 revision**（实测 #17 revision 1→2 而内容零变化），会误伤前端乐观锁。
- 修复：①mapToPatch 返回未知字段清单→400 E_VALIDATION；②响应改 `{updated, failed:[{id,reason}]}`；③patch 全空/全被丢弃时 400 或零副作用返回。

**D-04 query/table 三件套的 `labels` 与 `priority_min` 过滤器静默无效**
- QueryFilters（service_catalog.go:403-414）与 FilterSummary（service_query.go:33-44）声明并回显，compileFilters（service_catalog.go:523-571）不消费。实测 priority_min:99 → 全量返回（§3.1 #3/#5）。
- 危害：保存视图/表格 UI 一旦照声明的过滤器渲染，用户看到"已过滤"假象。
- 修复：compileFilters 补 `EXISTS(SELECT 1 FROM work_item_to_label …)` + `wi.priority >= $n` 两条件（各 3 行）；或 CompileParsed 阶段对未实现字段报错。

**D-05 属性定义删除后重建同名 key 必 500**
- ArchivePropertyDef 软删保留行（repo.go:646-667）+ `UNIQUE (workspace_id, key)`（migrations/1001_work_core.up.sql:100）→ CreatePropertyDef 裸 INSERT 撞 23505 → 500（repo.go:595-600 无映射；对照 UpdateProject 有 mapUniqueViolation）。
- 实测：DELETE fp18_note 204 → POST 同名 → 500（§3.3 #36）。删除-重建是属性管理的常规闭环，必踩。
- 修复：CreatePropertyDef 撞 23505 时改为复活归档行（`UPDATE work_item_property_def SET archived_at=NULL, name=?, type=?, options=? WHERE workspace_id=$1 AND key=$2`）。

**D-06 POST /api/projects 重名 500（Update 有映射、Create 没有）**
- service_catalog.go:149-168 + repo.go:501-507 无 unique 处理；UNIQUE (workspace_id, name)（1001:109）。实测 500（§3.3 #44）。
- 修复：repo.CreateProject 套用现成 mapUniqueViolation(err, "project name already exists")（repo.go:569）。

**D-07 preview-trigger 内容路径 100% 500（双因，其中之一是 batch1 根因 D 的 orchestration 孪生）**
- 因 A（无条件）：顶层 `POST /api/work-items/preview-trigger`（routes.go:110）无 workItemId → TriggerService.Preview → HistoryAgentIDs(ctx, "") → `WHERE work_item_id=$1::uuid` 空串（orchestration/adapters/pgrepo/repo.go:995-999）→ 22P02 → 500。
- 因 B（数据态）：`ListSquadFacts` 把可空 `s.leader_agent_id::text` 直接 Scan 进 string（orchestration/adapters/pgrepo/repo.go:1056,1067）——inventory-ws 4 个小队 3 个 leader 为 NULL（DB 直查实证）→ 含 content 的两条路由（含**前端在用**的 `POST /{id}/comments/trigger-preview`，work.ts previewCommentTrigger）在无 leader 小队的工作区 500。与 feature-audit-batch1 根因 D（work 域同型 NULL-scan）同族第二例。
- 实测：change_kind 探针 200 blocked；content/mention/空 content → 500×3（§3.3 #48/#50）。
- 修复：①HistoryAgentIDs 空 id 短路返回 nil；②ListSquadFacts leader 列 `coalesce(s.leader_agent_id::text,'')`。修复后必须补"带 content 的 preview"集成测试（当前零覆盖）。

**D-08 move 端点无视解码失败仍执行写操作**
- handlers.go:111 `in, _ := decode[...]`——非法 JSON 时 decode 已写 400，但 handler 继续执行 Move(ctx, ws, id, nil, nil)（置顶写）。实测 400 响应 + updated_at 跳变 19 秒（§3.2 #23）。同款忽略在 :231/:459（reactions DELETE 兜底，无害但同坏味道）。
- 修复：`in, ok :=`，`if !ok { return }`。

### P2（11 项）

| # | 缺陷 | 位置 | 修法 |
|---|---|---|---|
| D-09 | /search plain 模式 id 字节数组（同 D-01①）+ snippet 定位失效：strpos 用带 `%` 通配的 $2 永远 miss → 恒返回标题前 80 字符 | service_catalog.go:706-708；pgrepo scanMaps | id::text；snippet 用剥离通配的独立参数 |
| D-10 | assignee-frequency 与 grouped 逐字节相同的复制品，无独立语义（频率直方图/top-N/姓名解析均无） | handlers.go:202-209 vs :193-200 | 实现真频率面（join user/agent 取名）或删路由 |
| D-11 | rows/groups/facets cursor 解析无时间戳校验，垃圾 cursor → 500 而非 400（D-01③ 一部分，单列防修复遗漏） | service_catalog.go:441-449,617-623 | parseCursor 校验失败返回 E_VALIDATION |
| D-12 | 保存视图无 DELETE 端点（只能改名覆盖，列表只增不减）；View.Filters 存任意 JSONB 无 schema 校验（上限 1MB）——前端接入后视图列表将积压垃圾视图 | routes.go:163-166；repo.go:956-975 | 补 DELETE /api/work-item-views/{id}（owner fence 现成）；SaveView 校验 filters 键 ⊆ QueryFilters 已知键（顺带治 D-04 的回显欺诈） |
| D-13 | 状态目录只增不减（无 DELETE/archive status）；reorder 幽灵 key/部分 key 集合静默 204（可造出 position 空洞/重复）；自定义终态工单 wire 折叠为 cancelled 本身正确（D3 核对结论），但缺"删除前检查是否仍有工单引用"的对应闭环 | routes.go:152-155；pgrepo repo.go:98-108 | 补 archive 端点（仿 ArchivePropertyDef）+ reorder 校验键集合完整性 |
| D-14 | 项目非空删除复用 E_REVISION_CONFLICT（语义完全无关的码，前端按 revision 冲突处理会引导"刷新重试"死循环） | service_catalog.go:210-211 | 新增/复用 E_CONFLICT 类码（apierr.Conflict 裸用即可，writeErr 已映射 409） |
| D-15 | 脏输入错误映射三态漂移：同是 `not-a-uuid` 工单 id，GET 详情 404（service_workitem.go:106-113 把一切 repo 错误吞成 NotFound——DB 故障也报 404）、metadata 500、move 锚点 500 | service_workitem.go:106-113；handlers.go:264-271 | repo 层映射 pgconn 22P02 → E_VALIDATION（一次映射全族治愈，同 FP-03 D-02 修复方案）；GetWorkItem 保留 noRow→404、真实错误上抛 |
| D-16 | ILIKE 通配符未转义（FP-03 D-06 家族在 rows/groups/facets/search/projects-search 的 4 处新实例） | service_catalog.go:553-554,696-697,752-768；repo.go:549-553 | 转 `\%`/`\_` + ESCAPE '\'（一处 util 全覆盖） |
| D-17 | 小项：subscribe 重复调用仍 201（UpsertManualSubscriber created 标志未透传到状态码）；emoji 长度按字节（32B，11 个 emoji 即拒）与 icon 按 rune 校验不一致；DELETE label 幽灵 204 vs GET 404 风格不一 | handlers.go:493-500；service_workitem.go:788-793；repo.go:393-408 | created=false 回 200；emoji 改 rune；幂等删除可保留但契约文档注明 |
| D-18 | facets 仅 status/assigne**e** 两维，group_by 却支持 label/project——表格三件套能力不对齐 | service_catalog.go:510-513 | 补 label/project 两维 SQL（join 现成于 Groups:477-478） |
| D-19 | D-01/02/03/04/07 五个 P0/P1 全部位于零 HTTP 测试或"只断言 200"的端点（§6 清单） | — | 按 §6 零测试清单补契约用例（rows 必须含"自产 cursor 回放"回归用例） |

### 修复顺序建议

1. **D-01**（rows 一处 SELECT + cursor 格式化，解锁整个表格视图面）
2. **D-07**（两处小改，其中 ListSquadFacts coalesce 与 batch1 根因 D 修法同款，可一个 PR 带上 work/orchestration 两处）
3. **D-02/D-03**（labels PUT、batch 契约——前端接入前必须定型，否则接完即返工）
4. **D-04/D-05/D-06**（过滤器补全、两处 23505 映射——各 ≤10 行）
5. **D-08/D-11~D-19** 顺手批；补 §6 零测试清单作为回归地基。

---

## 8. 就绪度评级与前端接入优先级

### 就绪度三档

| 档 | 端点组 | 说明 |
|---|---|---|
| **可接前端（31 条）** | metadata×3、properties/{key}×3、property defs×5、labels list/create/get/delete + attach/detach、statuses list/create/patch/reorder、projects 单查/更新/删除/search/resources×4、views×2、view-preferences×2、child-progress、duplicates、subscribers×2、unsubscribe×2、reactions×2、query（校验面）、search federated、groups、facets、client-usage | 本轮全环实测通过；修各自 P2 后更佳 |
| **需返工（7 条）** | table/rows（D-01 P0）、search plain（D-09）、preview-trigger 两条（D-07）、batch-update/batch-delete（D-03 契约）、PUT labels/{id}（D-02） | 主链路断或契约欺诈，接了即错 |
| **建议裁决（2 条）** | assignee-frequency（=grouped 别名）、grouped（二选一或差异化实现） | D-10 |

### 前端接入优先级建议（按产品价值/阻塞比）

1. **metadata + properties/{key}**：详情页 property-rows 前端壳已存在（FP-04），后端这组质量最好，直接可接自定义字段。
2. **table/rows + groups + facets + views + view-preferences**（修完 D-01 后）：这是"工单列表页升级表格视图/保存视图"的整套面，价值最高，注意按 §2.1 适配三种 envelope 形状。
3. **batch-update/delete**（修完 D-03 后）：列表多选批量操作；契约必须先落"失败清单"。
4. **statuses POST/reorder/PATCH**：设置页"自定义状态"管理；先补 D-13 的 archive 与 reorder 守卫。
5. **labels GET/{id}/PUT/DELETE**：标签管理页；D-02 必须先修。
6. **search federated + child-progress + duplicates + reactions + subscribers**：增量体验面，随时可接。
7. **preview-trigger**：等 D-07 修复（该面直接影响**已在生产路径**的评论触发预览）。

---

## 9. 证据留存

- cookie jar /tmp/fp18-cookies.txt；实测脚本 /tmp/fp18-req.sh、/tmp/fp18-post.sh；超大 payload /tmp/fp18-big.json、/tmp/fp18-manyprops.json。
- 测试数据清理：3 个 fp18 工单 DELETE 204、3 个标签 DELETE、2 个项目 DELETE、视图+偏好清空、property defs 与 client_usage_daily 经 psql 清除；**残留**：4 个自定义状态（fp18-dead-zone/fp18-uat-review/fp18/fp18-uat，状态目录无删除端点=D-13 实证），归档 property def 行 4 条已物理删除。
- 关键日志：/tmp/orkome-server.log（preview-trigger/projects 500 段 2026-10-12T04:13-04:14Z）；DB 直查：`squad.leader_agent_id IS NULL` 3/4 行（D-07② 实证）、`UNIQUE (workspace_id,key)` 1001:100/109。
- 关联：FP-03 D-02/D-06（脏输入 500、通配符家族）、FP-04 P2-8（status_category fill）、batch1 根因 D（NULL-scan 家族第二例=orchestration ListSquadFacts）、D3 webhook payload 缺 category（本审计核对其 HTTP 对偶面为正确）。
