# FP-13 审计：automation 域（triggers / autopilots / wakeups）

> 日期：2026-10-12 · 审计人：FP-13 agent · 方法：七步审计法（全链梳理→契约逐字段→curl 实测→代码质量→后端正确性→测试覆盖→P0/P1/P2）
> 范围：`internal/automation/{domain,app,adapters}` + `cmd/orkome-server/wiring_automation.go` + `workers.go` + `migrations/1006|1013|1017|1020|1026` + 前端 `web/src/routes/_app.automation.tsx` / `web/src/api/automation.ts`
> 环境：后端 :8080（dev，日志 `/tmp/orkome-server.log`，PG `orkome-pg`）· 测试账号 inventory-test@orkome.dev / `inventory-ws`
> 边界：只读代码，只写本文档；未改动任何代码。FP-06 已审过的 Preview/OnComment 链路不重复。

---

## 一、一句话结论

**调度内核（多副本安全、熔断、链防环）是全仓质量最高的部分之一，但"接线层"漏电严重：三个用户可达的主链路（手动触发、API 唤醒 fire、autopilot 删除）100% 必败且全部静默，前端 automation 页实际处于"能看不能动"状态——P0 全部是十几行以内的修复。**

**P0×3 · P1×4 · P2×11**（另 P3 级提示 3 条，附文内）。

---

## 二、三条执行链全景

### 2.1 triggers（autopilot 子资源 + webhook 入站）

```
CRUD: POST/GET/PATCH/DELETE /api/autopilots/{id}/triggers (+signing-secret / rotate-webhook-token)
  handlers: adapters/http/routes.go:496-548 → app/service_autopilot.go:167-252
调度: TickWorker("autopilot_cron", 1s) workers/jobs.go:41 → CronTickOnce (service_autopilot.go:277-301)
  → ListDueScheduleTriggers(now) [repo.go:653] → ClaimDueTrigger CAS [repo.go:671] → StartRun("schedule")
执行: dispatchRun [service_autopilot.go:394-445] = ReserveQuota → InsertRun → (create_work_item: 建工单 | task_only: 直接用 template.work_item_id) → EnqueueForWakeup → agent_task_queue
webhook 入站: MountPublic [routes.go:29-48] POST /webhooks/autopilots/{token}
  → WebhookService.Ingest [service_webhook.go:27-60]（HMAC-SHA256(timestamp+"."+body) + ±5min 时间戳 + 三层限流 ratelimit.go）
  → InsertDelivery（dedupe_key 去重）→ TickWorker("automation_webhook_delivery") ConsumeOnce → StartRun("webhook")
```

### 2.2 wakeups（工单级唤醒）

```
CRUD: /api/work-items/{id}/wakeups×12 + 工作区视图×3 + system-wakeups×6（routes.go:55-76）
创建: kind=time(cron|interval) / kind=event(event_types+filters) / kind=condition(谓词) / kind=system(children_done 平台管理)
事件捕获: bus.On(PhaseCascade) → RecordEvent [service_wakeup.go:359-388] → receipt 表（UNIQUE(wakeup,revision,event_key) 幂等）
调度: TickWorker("wakeup_dispatch", 1s) → TickOnce [service_wakeup.go:390-422] 四路 union 扫描（due/expire/condition/receipt, pgrepo dueScanSQL:183-223）
fire: fireOneTx [service_wakeup.go:521-546] = SET LOCAL lock_timeout=50ms → LockTaskOwner(advisory+FOR UPDATE) → LockWakeup(FOR UPDATE) → 门槛检查 → 链防环(LatestWakeupChain+ChainContains) → MergeIntoPending/InsertTask → FireWakeup(rev CAS)
补偿: expires_at 过期 → 'expire' 路 MarkWakeupExpired（实测 ✓）；burst 熔断（10 fires/min → 暂停 10min，实测有单测）；system children_done 由事件驱动 NotifyChildrenChanged 即时评估
```

### 2.3 autopilots（两种 exec_mode 的定时链）

- `create_work_item`：每次触发创建工单（title 支持 `{{date}}` 插值，Interpolate 仅此一变量）+ 派 agent 任务，工单订阅者带 autopilot_template 来源。
- `task_only`：直接对 `template.work_item_id` 派任务——**不校验工单存在性/归属**（service_autopilot.go:421-423），工单 id 可为任意值（失败则在 tx 内回滚，无越权后果，但错误表现是 500/静默，见 P1-7）。
- 两种 mode 最终都派 agent 任务（与既往审计结论一致）；run 状态由 TaskCompleted/Failed/Cancelled 事件回填（wiring_automation.go:46-53 → BackfillFromTask → SetRunStatusByTask，同时做 quota reserved→committed 结转，pgrepo:794-810）。
- 护栏：日配额（ReserveQuota upsert CAS，UTC 日窗）、失败率熔断（7d/≥50 runs/≥90% 失败 → paused_reason=failure_rate + 通知创建人，main.go:258-268 已接线）。

---

## 三、缺陷清单

### P0-1 手动触发 autopilot 100% 必败（前端「立即触发」按钮全灭）

- **位置**：`internal/automation/adapters/pgrepo/repo.go:771-777` — `InsertRun` 的 `trigger_id`（$2）直接入参，未像 $6/$7 一样包 `nullif(...)::uuid`；手动触发时 `routes.go:461` 传 `triggerID=""` → PG `22P02 invalid input syntax for type uuid: ""` → 整个 dispatchRun 事务回滚 → `routes.go:468` 把一切错误压成 400 `"trigger failed"` 且**不打任何日志**。
- **实测**：POST `/api/autopilots/{id}/trigger` 两次均 400；PG 日志错误时间戳与请求精确吻合（04:13:35.139 vs 请求 .134）；`autopilot_run`/`autopilot_quota_reservation` 零残留（tx 回滚）；psql 逐步复放同链路四条 INSERT 全部成功，唯一差异即空串 trigger_id。
- **波及**：前端 automation 页唯一可用的执行入口就是「立即触发」（`_app.automation.tsx:110-117`）→ 页面核心交互全灭。schedule/webhook 路径传非空 trigger_id 不受影响——**唯一炸的是前端唯一暴露的那条**。
- **修复**：`repo.go:774` `VALUES ($1, nullif($2,'')::uuid, ...)`（一行）；同时 `routes.go:468` 补 `log.Warn` 带根因（本批"错误三无"家族）。

### P0-2 API 创建的唤醒 100% 无法 fire，且每秒刷一条 PG 错误（永不自愈）

- **位置**：`adapters/http/routes.go:148-151` — `createWakeup` 只回填 `WorkspaceID/WorkItemID`，**从不设置 `CreatedBy`**（对照 createAutopilot:400 有 `CreatedBy: p.UserID`）→ DB `created_by=NULL` → fire 时 `wiring_automation.go:141` `orZeroUUID("")` 补零 UUID `00000000-…` → 撞 `agent_task_queue_originator_user_id_fkey` → InsertTask 失败 → `fireOneSteps` 返回 false → tx 回滚，**next_fire_at 永不推进**。
- **实测**：interval=2s、max_fires=1 的 fp13 唤醒等 6 秒后 `fire_count=0`、`last_task_id` 空、tasks=[]；PG 日志 04:15:11/12/13/14 **每秒一条** `agent_task_queue` FK 违例——因 next_fire_at 停在过期时刻，due 扫描每秒重试同一失败，形成错误洪水 + due 队列永占。手动 `/trigger` 同样失败（"wakeup could not fire" 400，真实根因被吞）。停用后错误即停（止血验证 ✓）。
- **修复**：`routes.go` createWakeup 补 `in.CreatedBy = webx.PrincipalFrom(r.Context()).UserID`（一行）。可加：fire 连续失败 N 次写 `last_error` 字段（已建列、代码从未写过，1027 dead column）+ 熔断，避免同类问题再次静默洪水。

### P0-3 autopilot 删除被自己的默认 trigger 100% 挡死（前端删除闭环必败）

- **位置**：`app/service_autopilot.go:76`（无 cron 时兜底 trigger `Enabled: true`）与 `:93`（有 cron 时 `CreateTrigger(..., enabled=true)`）→ **每个 autopilot 创建即带 ≥1 条 enabled trigger**；而删除守卫 `pgrepo/repo.go:590` `has_enabled_triggers` 不区分"会不会真正触发"→ DELETE 必 409。
- **实测**：DELETE `fp13-ap-cron`（带 enabled schedule trigger）→ 409 `"autopilot still has enabled triggers"`；逐条 DELETE 三个 trigger 后再删 → 204。前端 `_app.automation.tsx:126-149` 只有删除按钮，**无任何 trigger 管理入口** → 用户永远删不掉。
- **修复**（三选一，推荐 ①）：① 守卫只挡会真正触发的 trigger：`t.enabled AND (t.kind='webhook' OR (t.kind='schedule' AND cron_expression IS NOT NULL))`（兜底 trigger cron 为空、next_run_at 为空，永不触发，不应阻塞删除）；② 创建时兜底 trigger 置 `enabled=false`；③ DeleteAutopilot 自动级联删除其 triggers（删的是整个 autopilot，trigger 无独立存在意义）。

### P1-4 webhook 公开入站被全局 CSRF 拦死（外部触发链 0% 可用）

- **位置**：`cmd/orkome-server/main.go:308` — `webx.CSRFConfig{ExemptPrefixes: []string{"/ws"}}` 未豁免 `/webhooks/`；`autohttp.MountPublic(root, …)`（main.go:307）挂在 CSRF 之下。
- **实测**：正确 HMAC 签名 + 新鲜时间戳 POST `/webhooks/autopilots/{token}` → 403 `"CSRF token missing or invalid"`（错误签名/过期时间戳也全 403，但那应是 403 bad signature——实际全被 CSRF 前置拦截，签名逻辑从未被执行）。deliveries/runs 零增长。
- **说明**：该端点自有 token+HMAC+时间戳+限流四重防护，不需要 CSRF（无 cookie 会话语义）。与 batch1 已记录的 main.go:308 CSRF 配置缝（MaxAge/PATPrefix）同一行代码的第三处遗漏，**独立新缺陷**。
- **修复**：`ExemptPrefixes` 增加 `"/webhooks/"`（一行）。

### P1-5 autopilot 非原子创建：坏 cron → 400 但 autopilot 已落库（孤儿堆积）

- **位置**：`app/service_autopilot.go:66-97` — 先 `s.uow(steps)` 提交 autopilot+rule_version，**之后**才 `CreateTrigger`；trigger 失败（坏 cron 400 / token 撞车 500）时 autopilot 已持久化且无任何触发器。
- **实测**：POST `{"name":"fp13-ap-badcron2","cron_expression":"not a cron",...}` → 400（cronx 报错信息友好 ✓），但列表中出现已落库的 `fp13-ap-badcron2`（无 trigger）。用户重试 N 次堆积 N 个孤儿。
- **修复**：把 trigger 构造移进同一 `steps`（trigger 的 Mint/next 计算不依赖 DB，可在 uow 内做）；或失败路径补偿 `DeleteAutopilot`。

### P1-6 做过 checkin / 收过事件的 wakeup 删除必 500

- **位置**：`migrations/1006_automation.up.sql:29-39` — `work_item_wakeup_receipt.wakeup_id` FK **NO ACTION**（同表 workspace_id FK 却是 CASCADE，不一致）；`DeleteWakeup`（pgrepo:120-123）不先清 receipts。
- **实测**：对 W6 checkin 一次后 DELETE → 500；PG 日志 04:17:34.447 `update or delete on table "work_item_wakeup" violates foreign key constraint "work_item_wakeup_receipt_wakeup_id_fkey"`；两次复现。工单删除链路因 1029 对 wakeup CASCADE 而间接解锁（receipt 仍会卡 work_item 级联，需一并核对）。
- **修复**：FK 改 `ON DELETE CASCADE`（一条迁移），或 DeleteWakeup 先 `DELETE FROM work_item_wakeup_receipt WHERE wakeup_id=$1`。

### P1-7 创建侧校验三连：无 template → 500；非法 exec_mode → 500；错误全部无日志

- **位置/实测**：
  - 请求不带 `template` 字段 → nil map → `template` jsonb NOT NULL 违反（23502）→ 500 `E_INTERNAL`（实测 fp13-ap-notpl；PG 日志 04:08:00.175 实证）。`service_autopilot.go:39-48` 未初始化 `a.Template`。
  - `exec_mode:"bogus_mode"` → DB CHECK `autopilot_exec_mode_check`（23514）裸抛 → 500。Create（service_autopilot.go:43-45）只补默认值**不做枚举校验**，Update 有校验（:138-140）——同一资源两个入口一严一松。
  - `triggerAutopilot` handler（routes.go:460-476）把配额以外的一切错误压成 400 `"trigger failed"`，无日志（P0-1 的根因因此藏了三轮排查才定位）。
- **修复**：Create 里 `if a.Template == nil { a.Template = map[string]any{} }` + exec_mode 白名单校验（与 Update 同款）+ pgxerr 映射（23514/23502 → 400）。

### P2 清单（11 条）

| # | 缺陷 | 位置 | 修复建议 |
|---|---|---|---|
| P2-1 | condition/system 型唤醒每秒被空写：`advanceCondition` 未命中时无条件 `FireWakeup`（revision+1），写放大 ~86400 次/天/条，并使该类唤醒的乐观锁/过期 CAS 抖动 | service_wakeup.go:506-512 | fp 与现状相同则跳过写 |
| P2-2 | `max_fires` 耗尽的 time 唤醒永远留在 due 扫描（enabled+过期+非 paused），每秒空转：advisory lock + FOR UPDATE + 4 路扫描占位 | service_wakeup.go:559-561 | fire 时到达上限置 enabled=false（同过期语义） |
| P2-3 | delivery 租约无回收：ClaimDelivery 只取 `status='queued'`，worker 崩溃后 'processing' 永卡；lease_token/lease_expires_at 字段存在但全仓无 reaper（对照 batch.go:174-175 有 reclaim） | pgrepo/repo.go:859-866 | claim 条件加 `OR (status='processing' AND lease_expires_at<now())` |
| P2-4 | webhook dedupe_key 无时间窗：identical body 永远 202 不触发（`dedupe_key` 全局 UNIQUE 无 TTL），且 delivery 表无清理任务无限增长 | pgrepo:836-848 + migration 1006:102 | dedupe key 掺入时间片，或定期清理历史 delivery |
| P2-5 | 创建 webhook trigger 时铸造的 signing secret 只回指纹不回明文（routes.go:513-518）→ 铸造即废，必须再调 PUT signing-secret 才可用；单测 `TestIngest_MintedSecret_VerifiesAndRejects` 甚至验证了"铸造的 secret 能通过校验"但用户永远拿不到它 | routes.go:514-518 | 一次性明文返回或干脆不铸造 |
| P2-6 | `ListWakeupsByWorkItem` 只按 work_item_id 无 workspace fence（IDOR-lite 读面；对照同文件 system 版有 ws 条件）；`NotifyBatchDone` 全表扫描所有工作区 condition 唤醒再内存过滤 | pgrepo:83-86, 249-252 | 补 workspace_id 条件 |
| P2-7 | collaborators 可添加任意 uuid（不校验目标用户存在/是否本工作区成员），非法 uuid → 500；且 collaborator 当前无任何权限效果（死特性） | pgrepo:996-1011（`$3::uuid` 裸转换）、routes.go:98-100 | 校验 + 错误映射；或暂缓该特性 |
| P2-8 | 契约漂移：inventory-api 文档写 trigger `kind=cron|webhook`，实际 `schedule|webhook|manual`（实测 kind=cron → 400）；`GET /api/system-wakeups` 空时 `rules:null` 非 `[]` | docs/inventory-api-2026-10-12.md:489、pgrepo collectWakeups | 修文档 + 空数组归一 |
| P2-9 | 非法 uuid path（如 GET /api/work-items/not-a-uuid/wakeups）→ 22P02 裸抛 500 `internal` | pgrepo 各处 `$x::uuid` | 路径参数统一预校验或 pgxerr 映射 400 |
| P2-10 | wakeups PUT 语义无法清空可选字段（`patch.X != ""` / `pickStr`），cron↔interval 切换、解除 filter 均不可表达；PATCH kind=time 每次重置调度相位（from now） | service_wakeup.go:128-189 | 用指针语义或显式 clear 标记 |
| P2-11 | 前端 i18n：`automation.triggered` 括号不闭合（无 reason 时文案以「（run 状态：xx」结尾），zh/en 同病；触发反馈直接向用户裸露英文枚举（run status 原文） | web/src/i18n/locales/zh.ts:463-464、en.ts 同键、_app.automation.tsx:76-81 | 括号并入 reason 键；状态枚举映射 i18n |

### P3（提示，不计入总数）

- 配额窗口为 UTC 日（`QuotaWindow`，domain/rules.go:164-167），与用户时区的"每日"直觉不一致，未在文档/API 注明。
- `POST /api/work-items/{id}/wakeups` 与 `POST /api/autopilots` 的 JSON 解码错误被 `_ =` 吞掉（routes.go:150/399），坏 JSON 报"instruction required"这类误导性错误（对照 putWakeup:186 有正确处理）。
- `task_only` 模式的 `template.work_item_id` 不校验存在性/归属（service_autopilot.go:421-423），坏值在 tx 内回滚成静默失败。

---

## 四、后端正确性评估（值得肯定的部分）

| 维度 | 结论 | 证据 |
|---|---|---|
| 多副本安全 | **硬**。trigger 认领 CAS（`next_run_at IS NOT DISTINCT FROM`，pgrepo:671-679 + 单测 MultiReplicaClaimDedup）；wakeup fire 全链单 tx：`SET LOCAL lock_timeout=50ms` → advisory xact lock(work_item) → `FOR UPDATE` 行锁 → revision CAS（fireOneTx, service_wakeup.go:531-546）；quota upsert 条件 CAS | 代码 + pgrepo 集成测试 |
| 坏规则隔离 | **好**。tick worker 错误只记日志不退出（workers/jobs.go:18-34，Workers.Start 有 panic recover + AllHealthy 判死）；单条坏 trigger/wakeup 不拖死 tick——实测未知 field 的 condition 规则运行 3 秒无 5xx；但注意"隔离"的代价是**静默**（P0-1/P0-2 正是靠这个特性藏了这么久） | 实测 + workers.go:83-91 |
| 防失控 | **四层**：自环/跨环 pause（wakeup_chain 32 深度 + PauseWakeupLoop，有单测）、burst 熔断（10/min → 停 10min）、autopilot 失败率熔断（7d/50runs/90% → 置停 + 通知，main.go:258 已接线非死代码）、日配额 | 代码 + 45+ 单测 |
| 时钟/时区 | cronx 拒绝 `Local` 时区强制显式 IANA（实测 Not/AZone → 400 友好报错 ✓）；cron 预览时区正确（Asia/Shanghai ✓）。宕机补偿语义：**time 型唤醒错过会补 fire 一次**（next 从 now 重算），**autopilot cron `PlanLate>5min` 放弃过期 tick**——两者语义不同且都合理，建议写进文档。注意 app 时钟与 DB `now()` 混用（ClaimDelivery 用 DB 时钟，due 扫描用 app 时钟），单实例无碍，多机时钟漂移时 expiry 判定会抖 | cronx.go:12-29、service_autopilot.go:285-294 |
| 契约错误码 | 校验类报错质量高（cron/timezone/secret 的 400 信息全部可操作，实测 ✓）；败笔集中在"DB 约束裸 500 + handler 吞错"（见 P1-7/P0-1） | 实测记录 |

---

## 五、前端差距地图（后端先行 42 条 vs 前端已接 4 条）

`_app.automation.tsx`（165 行）实际**不是纯空态页**：有列表 + 三按钮（立即触发/启停/删除）+ 熔断提示 + 空态文案。但后端面共 42 条（webhook 入站 1 + wakeups 21 + autopilots 20），前端仅接 4 条（list/patch-enabled/trigger/delete），且其中两条（trigger/delete）因 P0-1/P0-3 必败——**页面当前是"能看不能动"**。

接入建议（按成本/收益排序，均以修复 P0 为前提）：

1. **第一波（接上就能用）**：autopilot 创建表单 → `POST /api/autopilots` + `cron-preview`（预览未来执行时间，端点现成、报错友好）；runs 视图 → `GET runs`/`GET run`；trigger 管理 → 创建/删除/启停 + webhook token 展示/rotate/signing-secret（同时解开 P0-3 删除死锁的用户路径）。
2. **第二波（wakeups 域 21 条）**：工单详情侧栏——wakeups 列表/创建（time|event|condition 三态表单）/启停/手动触发/checkin/tasks；工作区三视图（work-item-wakeups / paused / summaries）；system-wakeups（children_done 默认指令自定义，PUT /api/system-wakeups）。
3. **第三波（观测面）**：deliveries 列表/详情/replay。
4. i18n：`automation.*` 15 键 zh/en 双语已齐（质量好），新增 UI 沿用该命名空间；顺手修 P2-11 括号病。

---

## 六、测试覆盖评估

**存量（质量整体较高）**：app 层 45+ 单测覆盖 loop/burst/lock-timeout/quota 循环/回滚/receipt 闸门/熔断；domain 纯函数表驱动（ChildrenDone 五种阶段形态等）；HTTP 层 fake-repo 契约测试（409 守卫、replay、rate-limit 先于 lookup）；pgrepo 集成测试（quota 并发串行化、四路 union、lock timeout tx-local）。`go test ./internal/automation/...` 全绿。

**盲区（本批 3 个 P0 全部从网眼里漏过）**：

1. **fake repo 不经 PG 约束**——`TestAutopilotDeleteEndpoint` 手工摆放 triggers，"Create 必产 enabled trigger × 删除守卫"的真实组合无人覆盖；CreatedBy 缺失与 InsertRun 空串 trigger_id 这类 FK/类型错误在 fake 层不可见。这是 batch1 系统性模式 2（"测试走 A 路径，生产走 B"）的又一实证。
2. **集成测试可静默跳过**——pgrepo 集成测试无 `TEST_DATABASE_URL` 即 `t.Skip`（repo_integration_test.go:35-37），CI/本地默认跑的全是 fake；建议 CI 强制注入（容器已有：orkome-test-pg:54329）。
3. **MountPublic 无穿越 CSRF 层的测试**——`TestMountPublic_RateLimitPrecedesLookup` 直接挂 mux，不经过 main.go 全局链；webhook 入站 403 全断因此零告警。
4. 缺"手动触发 happy path 打真库"集成用例、缺 condition/system 空写量的断言。

---

## 七、修复路线图（按收益/成本）

**第一波（6 处一行级修改，清掉全部 3 个 P0 + P1-4）**
1. `pgrepo/repo.go:774` InsertRun `trigger_id` 加 `nullif($2,'')::uuid`（P0-1）
2. `routes.go` createWakeup 补 `in.CreatedBy = Principal.UserID`（P0-2）
3. `pgrepo/repo.go:590` 删除守卫排除"cron 为空的 schedule trigger"（P0-3）
4. `main.go:308` ExemptPrefixes 加 `"/webhooks/"`（P1-4）
5. 上述四处各补一条回归测试（真实 PG 或带约束的 sqlite 不现实——至少补 fake 之外的 pgrepo 集成用例并让 CI 带 TEST_DATABASE_URL）

**第二波（契约与数据完整性）**
6. Create 补 template 默认值 + exec_mode 白名单 + trigger 构造并入 uow（P1-5/P1-7）
7. receipt FK 改 CASCADE（P1-6）
8. triggerAutopilot 等吞错处补日志 + pgxerr 映射（全批次共性）

**第三波（正确性打磨）**
9. P2-1/2/3：空写跳过、max_fires 燃尽退出 due、delivery 租约回收
10. P2-4/5：dedupe 时间窗、credential 明文一次性下发
11. P2-6/7/9：读面 ws fence、collaborator 校验、uuid 预校验

**第四波（前端接入）**：按 §五 波次推进。

---

## 八、实测证据留存

- 登录/CSRF 配方见任务书；cookie jar `/tmp/fp13-cookies.txt`。
- 关键实测：A1 autopilot 201；A2 非法 exec_mode 500；A3b 坏 cron 400+孤儿落库；A3c 无 template 500；DELETE 有 enabled trigger → 409 / 无 → 204；T1 kind=cron 400（文档漂移）；T3 webhook credential 无 secret 明文；T7/T8 signing-secret 校验 ✓；T9/T9' 手动触发 400×2（PG 22P02 时间戳吻合）；W1-W5 校验 400 全对；W6 fire 失败 + 每秒 FK 错误洪水（04:15:11-14）；W7 过期置停 ✓；乐观锁 409 ✓；checkin 204 ✓；坏 condition 规则不炸 tick ✓；WH1-4 全 403（CSRF 拦截）；receipt FK 删除 500 ×2；collaborator/system-wakeup/cron-preview/deliveries/runs 全部按上文记录。
- 测试数据：`fp13-wi-automation-20261012`（工单 #26b34e78，保留作修复回访载体，其上留有 1 条 disabled 的过期唤醒 + 1 条 disabled 的 children_done 系统规则——前者同时是 P1-6"删不掉"的活样本）；autopilots/triggers 已全部清理（列表为空）；**无 agent 任务残留**（所有 fire 都失败在入库前，webhook 被 CSRF 拦截未产生任务）。
- 服务端日志：`/tmp/orkome-server.log`；PG 错误：`docker logs orkome-pg`（04:07-04:17 段）。
