# FixC 交接笔记：Go 后端 P0 快修（orchestration/automation + 错误日志）

> 日期：2026-10-12 · agent：FixC · 修复依据：feature-audit-batch1（根因 D）+ feature-audit-batch2（FP-13×3、FP-18 preview-trigger）+ 分册 audit-fp13 / audit-fp18 / audit-fp06
> 独占文件域：`internal/orchestration/adapters/pgrepo/repo.go`、`internal/orchestration/adapters/http/routes.go`、`internal/automation/**`
> 未做 git add/commit（按任务书要求）；主会话统一重启 dev server 验证。

---

## 一、修复清单（6 项任务书内 + 1 项测试中发现的多余层）

### 1. 根因 D：ListSquadFacts 可空 leader_agent_id（评论 500 / trigger-preview 500 因 B）
- **位置**：`internal/orchestration/adapters/pgrepo/repo.go` ListSquadFacts
- **修法**：`s.leader_agent_id::text` → `coalesce(s.leader_agent_id::text,'')`（与同文件 agentCols 对 owner_user_id 同款）。
- **附带修（重要，测试中新发现的第二层断点）**：第三列 `coalesce((SELECT array_agg(sm.agent_id::text) ...), '{}')` 产出 **text[]（OID 1009）**，pgx v5 默认二进制协议（ploykit pg 封装无 QueryExecMode 覆写）**扫不进 `*[]byte`** —— 即便修好 leader 列，只要工作区有 ≥1 个小队（不管 leader 是否为 NULL）查询仍在 scan 阶段报错。已改为 `jsonb_agg(...)` + `'[]'::jsonb` 兜底（jsonb 可扫 []byte）。**收益**：`jsonUnmarshalStrings` 从此真正解析出成员——旧值是 PG 数组字面量 `{a,b}` 不是 JSON，`SquadFacts.MemberIDs` 在生产中恒为空（`_ =` 静默吞错）。
- **给 FixF 的提示**：`service_trigger.go:106` 消费 `sq.MemberIDs`，该字段从"恒空"变为真实回填，squad 成员相关的 trigger 评估行为会按代码原意生效；如 FixF 正在动 trigger 评估逻辑，请知悉此数据源已变化。

### 2. preview-trigger 孪生断点：HistoryAgentIDs 空 workItemId
- **位置**：`internal/orchestration/adapters/pgrepo/repo.go` HistoryAgentIDs
- **修法**：`workItemID == ""` 直接返回 `(nil, nil)`，不再把空串送进 `$1::uuid`（22P02）。顶层 `POST /api/work-items/preview-trigger`（无 workItemId）由此从必 500 变为合法空历史。
- 该守卫在触库前短路，不依赖 DB，有免环境单测锁定。

### 3. FP-13 P0-1：InsertRun trigger_id 空串
- **位置**：`internal/automation/adapters/pgrepo/repo.go` InsertRun
- **修法**：`VALUES ($1, nullif($2,'')::uuid, ...)`（列本身可空，migration 1006:77）。手动触发（`StartRun(apID, "", "manual")`）不再撞 `22P02 invalid input syntax for type uuid: ""`。

### 4. FP-13 P0-2：createWakeup 缺 CreatedBy（零 UUID 撞 FK 死循环）
- **位置**：`internal/automation/adapters/http/routes.go` createWakeup + `internal/automation/app/service_wakeup.go` Create
- **修法（双层栅栏）**：
  1. handler 从认证 principal 回填 `in.CreatedBy`（`PrincipalFrom` 判 nil，照 handlers_collaborators.go 惯例）；
  2. service 层 `Create` 入口校验 `CreatedBy` 非空，空则 `E_VALIDATION` → **400**（而非放行让 fire 阶段补零 UUID 撞 `agent_task_queue_originator_user_id` FK，形成"永不 fire + due 扫描每秒重试"的错误洪水）。
- `WakeupService.Create` 全仓仅此一个 HTTP 调用方（已核实），system wakeup 走 `EnsureChildDoneRule` 不受影响。

### 5. FP-13 P0-3：autopilot 删除被空 cron 兜底 trigger 挡死
- **位置**：`internal/automation/adapters/pgrepo/repo.go` DeleteAutopilot 的 `has_enabled_triggers` 子查询
- **修法**（审计推荐方案①）：守卫只挡"会真正触发的 trigger"：
  `t.enabled AND (t.kind='webhook' OR (t.kind='schedule' AND coalesce(t.cron_expression,'') <> ''))`
  —— 空 cron 的默认兜底 trigger（无 next_run_at，`ListDueScheduleTriggers` 永不认领）与 manual trigger 不再阻塞 DELETE；enabled webhook / 带 cron schedule 仍挡。
- **fake 同步**：`routes_test.go` 的 `autopilotRepoFake.DeleteAutopilot` 增加 `triggerWouldFire()` 与 SQL 同语义（测试注释里写明"fake 与真库语义漂移正是 P0 漏网原因，改一处必须同步另一处"）。

### 6. writeErr 补日志（消灭"500 零痕"）
- **orchestration**：`internal/orchestration/adapters/http/routes.go` writeErr 拆为 `writeErr(w, err)`（旧签名兼容，handlers_*.go 等非本波文件无需改动）+ `writeErrReq(r, w, err)`；非 apierr 分支 `slog.Error("orchestration http: internal error", "path", reqPath(r), "err", err.Error())`。routes.go 内全部调用点已切 `writeErrReq`（带路径）；包内其他文件的调用点仍走旧入口，**同样有错误串日志**（只是无 path）。
- **automation**：`internal/automation/adapters/http/routes.go` + `handlers_collaborators.go`（本域独占，全量切换 `writeErrReq`）；另 `triggerAutopilot` 的 400 `"trigger failed"` 吞根因处补 `slog.Error` 带原始 err（审计 FP-13 P0-1 的"根因藏了三轮"就藏在这行）。

---

## 二、回归测试（每修一条）

> 设计说明：三个 automation P0 与根因 D 的漏网原因一致——**fake repo 不经 pgx/PG 约束**（22P02、NULL scan、FK 全不可见）。因此 PG 级缺陷一律补 `pgrepo` 集成用例，门控 `TEST_DATABASE_URL`（无则 skip，沿用仓库既有惯例 `newQuotaFixture` / `summaryEnv`；本地已用 `orkome-test-pg:54329` 实跑验证全绿）。CI 建议按 audit-fp13 §六注入该变量。

| 缺陷 | 测试 | 文件 | 类型 |
|---|---|---|---|
| 根因 D（leader NULL scan）+ 第二层（text[] scan / MemberIDs 恒空） | `TestTriggerFactsP0Regression_DB/ListSquadFacts_LeaderlessSquadScansAsEmpty`（无 leader 小队扫描不报错、LeaderID 空、成员按 JSON 数组解析正确） | `internal/orchestration/adapters/pgrepo/trigger_facts_integration_test.go` | 真 PG（门控） |
| HistoryAgentIDs 空串 | `TestHistoryAgentIDs_EmptyWorkItemIDShortCircuits`（`&Repo{}` 无 DB 即可跑，证明短路在触库前） | 同上 | 纯单测（恒跑） |
| HistoryAgentIDs 正常路径 | `TestTriggerFactsP0Regression_DB/HistoryAgentIDs_DistinctAgentsForWorkItem`（completed+pending 去重；注：受 `uq_one_pending_task_per_work_item_agent` 约束，同 (wi,agent) 第二条需先置 completed） | 同上 | 真 PG（门控） |
| FP-13 P0-1 | `TestInsertRun_EmptyTriggerIDStoredAsNull`（空 trigger_id 落 NULL，修复前 22P02）+ `TestInsertRun_RealTriggerIDRoundTrips`（非空不受影响） | `internal/automation/adapters/pgrepo/p0_regression_integration_test.go` | 真 PG（门控） |
| FP-13 P0-2（回填） | `TestCreateWakeup_BackfillsCreatedByFromPrincipal`（principal 的 UserID 落到存储层） | `internal/automation/adapters/http/routes_test.go` | fake 端点测试 |
| FP-13 P0-2（400 栅栏） | `TestCreateWakeup_MissingPrincipalUserID_Returns400` + service 层 `TestWakeup_CreateValidation` 新增缺 created_by 断言 | 同上 / `internal/automation/app/service_test.go` | fake 端点 + service |
| FP-13 P0-3 | `TestDeleteAutopilot_InertFallbackTriggerDoesNotBlock`（走 service.Create 空 cron 真实生产路径产出兜底 trigger，DELETE 放行）+ `TestDeleteAutopilot_ActiveTriggerStillBlocks`（webhook / 带 cron 仍挡）+ `TestAutopilotDeleteEndpoint` 扩展 trig-3 空 cron 用例 | `p0_regression_integration_test.go` / `routes_test.go` | 真 PG（门控）+ fake |

**夹具成本纪律**：`summaryEnv` 每调用一次就全量跑一遍迁移（~100s），orchestration pgrepo 包在 TEST_DATABASE_URL 下整包时长本已逼近 go test 默认 600s 包级超时（既有 M85 万条级批处理测试很重）——因此 orchestration 侧两个 DB 用例合并为 `TestTriggerFactsP0Regression_DB` 单夹具子测试（实测 75s）。**后续在该包加门控用例请共享夹具，勿逐用例起库**；带 PG 跑整包建议 `-timeout 20m`。automation pgrepo 整包带 PG 实测 319s，正常。

**既有测试同步更新**：`service_test.go` 的 `seedWakeup` 与 `TestWakeup_CreateValidation` 因 CreatedBy 校验补 `CreatedBy: "user-1"`（校验顺序：created_by 最先，故负例补齐后再触达 kind/event_types 校验；并新增一条缺 created_by 的负例）。

---

## 三、门禁实际输出摘要

| 门禁 | 结果 |
|---|---|
| `go build ./...` | OK |
| `go test ./... -count=1`（无 TEST_DATABASE_URL，全仓） | **全绿**（中途一次 `internal/work/adapters/http/wave1_fixes_test.go` FAIL 为 FixE 并行开发中的过渡态，复跑即绿，与本波改动无关） |
| `go test ./internal/automation/...`（带 TEST_DATABASE_URL，真 PG 整包） | 全绿（pgrepo 包 319s，含 4 条新增集成回归） |
| `go test ./internal/orchestration/adapters/pgrepo/`（带 TEST_DATABASE_URL） | 本波新增回归全绿（合并夹具后实测 75s）；**整包带 PG 超 20m 超时为既有问题**：首次 601s 超时的栈就在既有已提交的 `TestM85_TenThousandItems_EndToEnd`（batch_integration_test.go，git 已提交非本波新增），且该包 45 个用例沿用"每用例全量跑迁移"的夹具设计（30~110s/个）——600s/1200s 均远不够，与本波 ~75s 增量无关。建议后续专项：夹具改共享库 + 按域拆 -run 分包跑 |
| `golangci-lint run` | **0 issues**（`internal/goarch` export-data 报错为任务书已声明的环境噪音） |

---

## 四、给主会话 / 后续 agent 的提示

1. **验证根因 D 修复的现成载体**：`inventory-ws`（4 小队 3 个 leader 为 NULL）的工单发评论应 201；顶层 `POST /api/work-items/preview-trigger` 与 `POST /{id}/comments/trigger-preview` 带 content 应 200（此前 100% 500）。
2. **验证 FP-13 的载体**：automation 页「立即触发」应 200（此前 400）；创建 autopilot 后立即可 DELETE（此前 409）；API 建的 time/event 唤醒应能 fire（此前每秒 FK 错误洪水——重启后可 `docker logs orkome-pg` 确认洪水消失）。
3. **日志新痕**：非 apierr 500 现在会在服务端日志出现 `orchestration http: internal error` / `automation http: internal error`（含 path+err）；autopilot 手动触发失败会打 `automation http: autopilot manual trigger failed`。后续排查 500 先 grep 这两行。
4. **未修（不在我范围，防丢失）**：
   - work 域 `internal/work/adapters/http/routes.go:30` 的 writeErr 同样"非 apierr 一律 500 不打日志"（FixE 领地）；
   - FP-13 P1-4 webhook CSRF 豁免在 `cmd/orkome-server/main.go`（FixD 领地）；
   - **疑似 P0-2 同族（本波核查发现，未实测未修）**：system wakeup（children_done）的 `EnsureSystemWakeup`（`internal/automation/adapters/pgrepo/repo.go:422`）INSERT 根本不含 created_by 列 → 恒 NULL；fire 时 `service_wakeup.go:650-657` 把 `locked.CreatedBy`（空串）交给 `EnqueueForWakeupWith*`，经 `cmd/orkome-server/wiring_automation.go:141` 的 `orZeroUUID` 补零 UUID → 与 P0-2 同一个 FK 地雷。全链修好需要：①INSERT 补 created_by（service `EnsureChildDoneRule` 仅 routes.go:399 一个调用方，可透传 principal）；②fire 侧 `orZeroUUID` 兜底改为解析真实用户（如工单 creator）——②在 FixD 领地且 ON CONFLICT DO NOTHING 意味着存量行清不进修复，需主会话裁决归属后再动。
5. **修改文件清单**（本波 FixC 实际 touched）：
   - `internal/orchestration/adapters/pgrepo/repo.go`（ListSquadFacts、HistoryAgentIDs）
   - `internal/orchestration/adapters/http/routes.go`（writeErr/writeErrReq + 调用点切换）
   - `internal/automation/adapters/pgrepo/repo.go`（InsertRun、DeleteAutopilot 守卫）
   - `internal/automation/adapters/pgrepo/p0_regression_integration_test.go`（新）
   - `internal/automation/app/service_wakeup.go`（Create 校验）
   - `internal/automation/app/service_test.go`（断言同步）
   - `internal/automation/adapters/http/routes.go`、`handlers_collaborators.go`、`routes_test.go`（writeErrReq、createWakeup、triggerAutopilot 日志、fake 同步、新端点测试）
   - `internal/orchestration/adapters/pgrepo/trigger_facts_integration_test.go`（新）
