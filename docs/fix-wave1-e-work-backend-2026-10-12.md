# Fix Wave-1 交接笔记 —— FixE：work 域后端修复（游标 / 活跃时间 / 契约欺诈 / 表视图）

> 日期：2026-10-12 · 执行 agent：FixE · 仓库 D:\code\orkome
> 修复依据：`docs/feature-audit-batch2-2026-10-12.md`（FP-11/FP-18）+ `docs/audit-fp11-my-work-2026-10-12.md`、`docs/audit-fp18-work-advanced-2026-10-12.md`
> 独占范围：`internal/work/**`（除 FixF 的 `service_comment.go`/`routes.go`）；测试补在 `internal/work/**` 与 `e2e/`（goldens/registry 既有断言同步更新）；未触碰 `cmd/**`、`web/**`、`internal/orchestration|automation|skills|conversation/**`

---

## 一、修复清单（7/7 完成）

### 修复 1：列表端点耗尽游标省略（FP-11 P1-1 后端半）
- `internal/work/adapters/http/handlers.go`：新增 `writeListPage(w, items, next)` —— `next == ""` 时整个省略 `next_cursor` 键（不再发 `""`），非耗尽页形状不变。`handleList` 与 `handleChildren`（children 端点同病）都改走它。
- `internal/work/app/service_catalog.go`（Rows）：table/rows 的 service 层 envelope 同规则——耗尽时不带 `next_cursor`。
- repo 层哨兵不变（`""` = 耗尽），wire 形状由 handler 决定；前端 `?? undefined` 拦不到的空串从此不再产生。
- **附带加固（同族实测命中）**：`pgrepo/repo.go` ListWorkItems 的游标时间戳从 `+08:00` 本地偏移改为 **UTC `Z` 形**——集成测试实测：`+08:00` 走 URL query 时 `+` 被解码成空格 → 回放 PG 22007 → 500（守规矩编码的客户端不踩，裸拼的必炸）。UTC 形天然 URL-safe。

### 修复 2：last_activity_at 语义（FP-11 P1-2）
- `pgrepo/repo.go` `UpdateWorkItem`：`sets` 初始加 `last_activity_at = now()`——状态流转/改派/优先级/标题等**所有核心 PUT 更新**都 bump，与评论（TouchWorkItem）、metadata、properties 的既有 bump 行为对齐。
- 注意：这也会让 batch-update 每个成功项 bump（合理——批量本身就是工作动作）；空 patch 已被修复 4 的 400 拦住，不会产生零变化假活跃。

### 修复 3：labels PUT 尊重路径 id（FP-18 契约欺诈① / D-02）
- `pgrepo/repo.go`：新增 `UpdateLabel(ctx, wsID, id, name, color) (Label, ok, err)` —— `UPDATE … WHERE workspace_id AND id`，`ok=false` 即 404；重名撞 `UNIQUE(workspace_id,name)` 走 `mapUniqueViolation` → E_VALIDATION。
- `app/ports.go`：Repo 接口加 `UpdateLabel`。
- `app/service_catalog.go`：新增 `CatalogService.UpdateLabel`——非 uuid 路径 id 直接 404（避免 PG 22P02 500），name 校验与 create 同规（1-50）。
- `handlers.go`：`handleUpsertLabel` 不再转发 `handleCreateLabel`；按路径 id 更新，**命中返回 200**（原来 upsert 返回 201）。
- 影响的既有断言：`e2e/registry_pos_test.go` 该条从 `MissStatus:201, WantStatus:201` 改为 `404 / 200`（这正是审计 D-02 的欺诈签名）。fake 的 label id 改为 uuid 形（真实层会拒非 uuid）。

### 修复 4：batch 部分失败报告（FP-18 契约欺诈② / D-03）
- `handlers.go` `handleBatchUpdate`：
  - **白名单外字段 → 400**：`mapToPatch` 现在返回 `(patch, rejected []string)`，白名单 = `title/status/priority/assignee_id/assignee_type/properties`；拒绝时 400 E_VALIDATION，`details` 逐字段列出（`patch.description` 等），**零副作用**。
  - **空 patch → 400**（裁决：选 400 而非 no-op 幂等）。理由：空 patch 若放行，repo 会无条件 `revision+1`+touch 时间戳——正是 D-03 实测的"revision 1→2 内容零变化"幽灵跳变，破坏前端乐观锁；而空 patch 几乎必然是客户端 bug，快速失败且零副作用比假装成功更安全。`ids=[]` + 合法 patch 保持 `200 {updated:0}`（合法退化批处理）。
  - **部分失败清单**：响应改 `{updated: N, failed: [{id, reason}]}`，`reason` 带 apierr 码（如 `E_NOT_FOUND: work item not found`）；无失败时 `failed: []`。
  - `assignee_type` 不再要求与 `assignee_id` 捆绑出现（原来单独出现被无声丢弃——现在独立映射，repo 本来就支持单改 type）。
- `handleBatchDelete`：同样附 `{deleted: N, failed: [{id, reason}]}`（原"幽灵跳过"）。

### 修复 5：query/table 过滤器落地（FP-18 契约欺诈③ / D-04）
- `app/service_catalog.go` `compileFilters`：补齐两个已回显却从不执行的过滤器：
  - `priority_min` → `wi.priority >= $n`；
  - `labels` → `EXISTS(SELECT 1 FROM work_item_to_label wl JOIN label l … AND (l.id::text = ANY($n) OR l.name = ANY($n)))`——条目同时匹配**标签 id 或名称**（名称工作区内唯一；未知值过滤为空集，与 status=未知 key 的行为一致）。
- **不支持的过滤器 → 400**：`handlers.go` 新增 `decodeFilters` + `knownFilterKeys`（8 个已知键）；`handleQuery/handleRows/handleGroups/handleFacets` 改为先解码 `json.RawMessage` 校验键集合，未知键 400 E_VALIDATION 并在 details 列出 `filters.<key>`（原来被结构化解码无声吞掉）。
- 影响面：保存视图 filters 里的未声明键一旦接到这三件套会开始报错——这是**期望中的定型**（D-12 建议的视图侧校验后续可在 SaveView 复用 `knownFilterKeys`）。

### 修复 6：table/rows 游标返工（FP-18 P0 / D-01+D-11）
- **游标结构化**：`service_catalog.go` 新增 `rowsCursor{created_at,id}` JSON + base64url 编解码（`encodeRowsCursor`/`decodeRowsCursor`）；解码失败/零时间戳/空 id/非 uuid 一律拒绝 → handler 400（消灭"垃圾 cursor → pgx 22P02 → 500"，D-11）。旧 `%v|%v` 文本游标天然不再可解（本来也回放必炸）。
- **SELECT 修名**：全部列加 `::text`/显式别名——`wi.id::text AS id`、`coalesce(wi.assignee_id::text,'') AS assignee_id` 等，消灭"id 是数字数组、字段名叫 coalesce"。
- **翻页 off-by-one**：`LIMIT limit+1` + 截断（对齐 ListWorkItems）——恰好整页的最后一页不再伪造 next_cursor（原来会引导前端多点一次空页）。
- **同族 D-09**（同文件、同一根因，顺手修）：`SearchService.Search` plain 模式 `wi.id::text AS id` + snippet 改锚定裸查询参数 `$3`（原来用带 `%` 的 `$2`，strpos 恒 miss，snippet 恒为标题前 80 字符）。
- **e2e 翻页断言**（漏网根因修补）：`e2e/goldens_test.go` `TestE2E_G1_BrowserGolden`——造 4 条 todo，limit=2 取第一页（断言 id 是 JSON 字符串、无 `coalesce` 键、有 `assignee_id` 键、cursor 非空）→ **回放第二页**（原 P0 现场：必 500）→ 断言不重复、末页无 cursor；垃圾 cursor → 400。

### 修复 7：工作区清理助手（FP-02 P1-1 的 orkome 半，给 FixD 铺垫）
- 新文件 `internal/work/adapters/pgrepo/workspace_cleanup.go`，导出：

```go
// 按 FK 拓扑序删除工作区下全部 work 域业务行；单事务，全成或全败。
func CleanupWorkspaceTables(ctx context.Context, db *pg.DB, workspaceID string) error
```

- `db` 参数类型是 ploykit 包装的 `*pg.DB`（`pgrepo.Repo.DB` 同型；`pg.WrapPool(pgxpool)` 可得）。
- **事务内先 `SET LOCAL orkome.workspace_teardown = 'on'`**——这是 migration 1020 预留的 schema 级 teardown 开关：`work_item`/`comment`/`project` 上的 search-index AFTER DELETE 触发器在清理期间静默，不会把行重新塞回 `search_index_change`（测试实证：不开则清理自身的 DELETE 会经触发器复活队列行）。
- 删除顺序（21 条语句，全部 `$1 = workspaceID`）：`work_item_wakeup_receipt`（唯一无 CASCADE 的跨域孙表，必须先于 work_item）→ `work_item_wakeup` → `comment_reaction` → `comment` → `activity_log` → `work_item_reaction` → `work_item_subscriber` → `work_item_to_label` → `work_item_dependency` → `work_item_child_event` → `work_item` → `work_item_view_preference` → `work_item_view` → `work_item_source_context` → `work_item_property_def` → `label` → `project_resource` → `project` → `search_index_change` → `client_usage_daily`（本域 observability 面，`pgrepo/observability.go` 所有）→ `work_item_status`。
- 跨域说明（写进了代码注释）：`agent_task_queue`/`work_item_batch`/`work_item_wakeup`/`notification` 随 work_item CASCADE；`autopilot_run` 与 work_item 自引用（parent/duplicate）SET NULL——均已由 1029/1030 保证。**agent/squad/chat 等其他域行不在本函数范围**。
- **未改任何 wiring**（main.go / workspace 删除路径原样）——FixD 接线时调用该函数后再 `DELETE workspace` 即可治愈 fp11 §五#10 的 `23503 work_item_status_workspace_id_fkey`。
- 门控测试：`workspace_cleanup_integration_test.go`（TEST_DATABASE_URL）——在 21 张表各播一行（含显式播 `work_item_status` 与 `search_index_change`/`client_usage_daily`）→ 清理 → 全表归零（同时证明 teardown GUC 生效：清理自身的 DELETE 没有经触发器复活 search_index_change）→ 删掉测试自播的 agent（他域范围）后 `DELETE workspace` 成功 → 重复调用幂等。

---

## 二、回归测试矩阵（全部落地）

| 修复 | 测试 | 位置 | 门控 |
|---|---|---|---|
| 1 | `TestListPageExhaustedCursorOmitted`（含 children 端点、非耗尽形状不变） | http/wave1_fixes_test.go | 无 |
| 1 | `list_exhausted_cursor_omitted` 子测试（真库翻到耗尽页，断言无空串） | http/wave1_integration_test.go | TEST_DATABASE_URL |
| 1 | `list_work_items_exhausted_cursor_is_empty_sentinel` + UTC 游标无 `+` 断言 | pgrepo/wave1_pgrepo_integration_test.go | TEST_DATABASE_URL |
| 2 | `update_bumps_last_activity_at`（回拨 2h 后 PUT priority，断言 bump） | http/wave1_integration_test.go | TEST_DATABASE_URL |
| 2 | `update_work_item_bumps_last_activity_at`（repo 层） | pgrepo/wave1_pgrepo_integration_test.go | TEST_DATABASE_URL |
| 3 | `TestLabelsPutByID`（原地改名无幻影/重名 400/幽灵 404/非 uuid 404） | http/wave1_fixes_test.go | 无 |
| 3 | `label_put_by_id` 子测试（真库改名 + 全表计数=1） | http/wave1_integration_test.go | TEST_DATABASE_URL |
| 4 | `TestBatchUpdateRejectsUnsupportedFields`（逐字段列举、零副作用）、`TestBatchUpdateEmptyPatchRejected`（revision 不动）、`TestBatchUpdatePartialFailureReported`、`TestBatchDeletePartialFailureReported` | http/wave1_fixes_test.go | 无 |
| 4 | `batch_partial_failure` 子测试（真库） | http/wave1_integration_test.go | TEST_DATABASE_URL |
| 5 | `TestUnknownFilterKeysRejected`（query/rows/groups/facets 四路 400 + 已知键放行） | http/wave1_fixes_test.go | 无 |
| 5 | `filters_execute` 子测试（priority_min 5/99、labels 按名/按 id、未知键 400）——**含审计冒烟用例 priority_min=99 不再全量返回** | http/wave1_integration_test.go | TEST_DATABASE_URL |
| 6 | `TestRowsCursorRoundTrip` + `TestDecodeRowsCursorRejectsGarbage`（编解码往返 + 8 种垃圾形态拒绝） | app/rows_cursor_test.go | 无 |
| 6 | `rows_cursor_round_trip` 子测试（id 字符串、无 coalesce 键、连续翻页无重复、垃圾 cursor 400、search plain id 字符串） | http/wave1_integration_test.go | TEST_DATABASE_URL |
| 6 | G1 golden 扩展：首页字段名/类型 + **翻第二页** + 垃圾 cursor 400 | e2e/goldens_test.go | TEST_DATABASE_URL |
| 7 | `TestPG_CleanupWorkspaceTables`（19 表播种→清空→workspace 可删→幂等） | pgrepo/workspace_cleanup_integration_test.go | TEST_DATABASE_URL |
| — | 既有断言同步：fake `ListWorkItems` 支持 nextCursor 注入、fake label id uuid 化、e2e registry label PUT 201/201→200/404 | resources_routes_test.go / routes_catalog_test.go / registry_pos_test.go | 无 |

---

## 三、门禁实际输出

> 环境：Windows + Git Bash；`TEST_DATABASE_URL=postgres://postgres:orkome_e2e@localhost:54329/orkome?sslmode=disable`（本机 docker `orkome-test-pg`）。

1. **`go build ./...`** → 通过（`BUILD_OK`）。
2. **`go test ./... -count=1`**（无 TEST_DATABASE_URL，集成/e2e 按惯例 skip）→ 退出码 0，无 FAIL 行（全绿）。
3. **`golangci-lint run`**（全仓，v2.6.2）→ **`0 issues.`**（stderr 的 `internal/goarch … export data version 4 is greater than maximum supported version 2` 为任务书预告的工具链版本环境噪音，不影响退出码与结论）；`go vet ./internal/work/... ./e2e/` 干净。
4. **带库定向回归**（门禁之外的真实 DB 验证）：
   - `go test ./internal/work/adapters/http/ -run 'TestM2' -v` → **全部 7 个 PASS**（TestM2_GoldenPath / Usage / Duplicates / Metadata / Reactions / ChildProgress / Wave1WorkContractFixes，261s 总耗时）。
   - `go test ./internal/work/adapters/pgrepo/ -run 'TestPG_Wave1RepoFixes|TestPG_CleanupWorkspaceTables' -v` → 两个都 PASS（单个用例 ~1-2.5 分钟，主要耗时在 batchEnv 的整库重建+全量迁移）。
   - **e2e**（`go test ./e2e/ -run 'TestE2E_G1_BrowserGolden|TestL1Positive|TestL1SixCaseMatrix|TestGuardTruncate'`）：
     - `TestE2E_G1_BrowserGolden` **PASS**（44s，含新的 rows 翻页回放/字段名/垃圾 cursor 400 断言）；
     - `TestL1Positive` **PASS**（505s，全路由正向走查，含 label PUT 新契约 200 期望）；
     - `TestGuardTruncateCoversAllTables` PASS；
     - `TestL1SixCaseMatrix` 唯一失败子测试是 **skills 域** `PUT /api/agents/{agentId}/skills/{skillId}/enabled`（期望 miss→200，实得 404 "agent not found in this workspace"）——这是**并行 agent 对 `internal/skills/**` 的绑定 fence 改动**（工作区有未提交的 skills/app/service.go 修改 + 新增 service_binding_fence_test.go）与 e2e 六例矩阵旧期望的碰撞，**不在 FixE 范围**，请 skills 域 agent 同步该 e2e 期望；work 域子测试全 PASS。
   - 基线对照：改动前 `go test ./internal/work/... -timeout 40m` 同环境全绿（http 256s / pgrepo 1070s）——本机 pgrepo 集成套件本来就需要 >10min（每个用例 DROP/CREATE 数据库并全量迁移），默认 10 分钟包超时在改动前就会 panic，属环境慢、非本次引入。
5. **未重启共享 dev server**（:8080 全程未动）；未执行任何 `git add/commit`。

### e2e（goldens/registry）执行说明
本轮已实际执行（见上）。两点 harness 提醒给后续写 e2e 的人：
1. `newStack` 的 `DROP DATABASE IF EXISTS orkome_e2e` 失败会被静默吞掉（`_, _ =`）——前一个用例的连接未放干净时会继承脏数据，**翻页类断言不要假设工作区总数**（G1 已按"走查全部页 + 无重复 + 收敛"写法免疫）。
2. `json.Unmarshal` 到**复用的 struct** 时，响应里被省略的键不会清零字段（耗尽的 `next_cursor` 缺键会让上一页的值滞留）——循环翻页时必须每轮用新变量（m2 集成测试即此写法，G1 初版踩过并已修正）。

---

## 四、给后续 agent 的接口与注意事项

### 给 FixD（workspace teardown 接线）
```go
import workpgrepo "orkome/internal/work/adapters/pgrepo"

// 在 workspace 删除事务/路径中、DELETE workspace 之前：
if err := workpgrepo.CleanupWorkspaceTables(ctx, db, workspaceID); err != nil { … }
// db: *pg.DB（pg.WrapPool(pgxpool.Pool) 可得；与 pgrepo.Repo.DB 同型）
```
- 该函数只清 **work 域** 21 张表（含本域 observability 面 `search_index_change`/`client_usage_daily`，且事务内已静默 1020 的 search-index 触发器）；orchestration（agent/squad/agent_task_queue/autopilot_*）、conversation（chat_*）、notification、channels、pins 等域仍需各自域的等价物（测试实证：只清 work 域时 `DELETE workspace` 仍会被 `agent_workspace_id_fkey` 挡住）。
- work 域内 FK 阻塞全部解除，含"空工作区也 23503"的 `work_item_status` 种子行（fp11 §五#10 直接治愈）。
- 建议接线时套自己的事务或在函数外层统一 uow（函数本身单事务，嵌套 Within 语义见 ploykit pg.Tx(ctx) 约定）。

### 给前端接入（FP-18 契约定型后的行为变化）
1. **耗尽页**：`next_cursor` 键整体缺失（不再是 `""`）。`?? undefined` 现在语义正确，但若代码里有 `cursor: pageParam || undefined` 依旧兼容。
2. **batch-update/delete**：`{updated|deleted, failed:[{id,reason}]}`；白名单外字段/空 patch 现在是 400（原静默成功）。前端批量改 description/due_date 的诉求仍不被支持——需要先扩 `WorkItemPatch`/`batchPatchKeys`，不要绕过。
3. **PUT /api/labels/{id}**：200 按路径 id 改名；重名 400；未知 id 404。不要再按"PUT=upsert"理解。
4. **rows/query filters**：只认 `status/assignee/labels/project/priority_min/search/involves/props`；labels 条目 id 或名称均可；未知键 400。保存视图 UI 按 FilterSummary 渲染的假设现在成立。
5. **rows 游标**：不透明 base64url token，原样回传即可；跨版本不兼容旧格式（旧格式本来也不能用）。
6. **list 游标**：时间戳已改 UTC `Z` 形，URL 拼接不再需要担心 `+`；仍建议 `encodeURIComponent`。

### 已知残留（本波范围外，未修）
- **给并行 agent 的提醒**：`TestL1SixCaseMatrix` 现唯一失败子测试 `skills:_PUT_/api/agents/{agentId}/skills/{skillId}/enabled`（miss 期望 200、实得 404 "agent not found in this workspace"）——工作区里 skills 域有未提交的 fence 改动（`internal/skills/app/service.go` 等），属该域 agent 的期望同步工作；FixE 未触碰 `internal/skills/**`。
- FP-18 D-05（property def 归档后重建同名 500）、D-06（POST project 重名 500）、D-07（preview-trigger 内容路径 500，根因在 orchestration `HistoryAgentIDs`/`ListSquadFacts`，禁碰域）、D-08（move 解码失败仍执行写）、D-13/D-14/D-16/D-18 及 `assignee-frequency ≡ grouped`（D-10 裁决）——均按审计 §7 修复顺序留给后续批次。
- batch-update 仍不支持 `description/due_date/labels` 字段（现会 400 明示，而非静默丢弃）；扩展属契约演进，需产品/前端同批。
- `GET /api/work-items/{id}` 把一切 repo 错误吞成 404（D-15 家族）未动。
- 本机 pgrepo 集成套件总时长 ~18 分钟（每用例重建库+迁移），是"带库测试几乎没人跑"的结构性原因之一，建议后续给集成测试引入 once-per-package 的共享迁移 fixture。

---

## 五、改动文件清单

**生产代码**
- `internal/work/adapters/pgrepo/repo.go`（修复 1 加固：UTC 游标；修复 2：bump；修复 3：UpdateLabel）
- `internal/work/adapters/pgrepo/workspace_cleanup.go`（新，修复 7）
- `internal/work/adapters/http/handlers.go`（修复 1：writeListPage；修复 4：batch 契约；修复 5：decodeFilters+四 handler；修复 3：handleUpsertLabel）
- `internal/work/app/service_catalog.go`（修复 3：UpdateLabel；修复 5：compileFilters；修复 6：rows SELECT/游标/搜索 plain）
- `internal/work/app/ports.go`（Repo 接口 +UpdateLabel）

**测试**
- `internal/work/adapters/http/wave1_fixes_test.go`（新）、`wave1_integration_test.go`（新）
- `internal/work/adapters/pgrepo/wave1_pgrepo_integration_test.go`（新）、`workspace_cleanup_integration_test.go`（新）
- `internal/work/app/rows_cursor_test.go`（新）
- `internal/work/adapters/http/routes_catalog_test.go`、`resources_routes_test.go`（fake 扩展 + label id uuid 化）
- `e2e/goldens_test.go`（G1 翻页断言）、`e2e/registry_pos_test.go`（labels PUT 期望 201/201→200/404）
