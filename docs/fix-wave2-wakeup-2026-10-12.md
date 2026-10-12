# W2-2 交接笔记：system wakeup 归属设计（可空 originator）

> 日期：2026-10-12 · agent：W2-2 · 依据：FP-13 P0-2 同族遗留（fix-wave1-c §四.4 FixC 上报）+ 主会话裁决（originator 改可空 / 存量零 UUID 置 NULL / fire 去 orZeroUUID；v0 不考虑兼容）
> 独占文件域：`internal/automation/**`、`migrations/1037_*`、`cmd/orkome-server/wiring_automation.go`
> 未做 git add/commit（按任务书要求）；共享 dev server 未重启。

---

## 一、修复清单（4 项）

### 1. 迁移 1037：`agent_task_queue.originator_user_id` 可空固化 + 哨兵清洗
- **文件**：`migrations/1037_wakeup_originator_nullable.up.sql` / `.down.sql`（产品号段下一个可用号，已确认 1036 为此前最大）。
- **up**：
  1. 仿 1017 惯例的条件 `DROP NOT NULL`（information_schema 判断）。**事实核查**：1003:89 建列时本就可空（`uuid REFERENCES "user"(id)`，无 NOT NULL），此语句当前是 no-op——保留是为防未来环境误收紧，并显式把"系统任务允许无主"固化进迁移史；
  2. 存量哨兵清洗：`UPDATE ... SET originator_user_id=NULL WHERE originator_user_id='00000000-…-0000'::uuid`。**该 UPDATE 预期恒匹配 0 行**——零 UUID 从第一天起就被该 FK 拦截（`"user"` 表无零 UUID 行，这正是 FP-13 P0-2 炸 23503 的机理），不可能落库；按裁决仍防御性保留（幂等）。
- **down 策略（与任务书直觉不同，已写明在文件头）**：**不做 SET NOT NULL**。两个原因：① 基线（1003）从未有过 NOT NULL，收紧属过度回滚，且基线代码本就有合法写 NULL 的路径（`InsertDeferredTask` 空 originator 走 `nullif($7,'')::uuid` 等）；② up 的数据清洗物理不可逆——NULL 行无法回写零 UUID 哨兵（撞同一堵 FK 墙）。因此 down 用 DO 块守卫：存在 NULL 行（系统发起任务）时**显式报错中止**，要求先做归属决策（指定真实用户或删除对应行）；无 NULL 行时为 no-op（schema 与基线一致）。

### 2. 写入侧：`EnsureSystemWakeup` 显式不写 created_by
- **文件**：`internal/automation/adapters/pgrepo/repo.go`（EnsureSystemWakeup）。
- INSERT 列清单本就不含 created_by（行为未变），补注释把"有意不写"固化为显式设计，防止后续被当遗漏"补上"。
- **API 创建路径 400 语义不变**：`internal/automation/adapters/http/routes.go` createWakeup 回填 principal + `internal/automation/app/service_wakeup.go` Create 空 CreatedBy → `E_VALIDATION` 400（FixC 双层栅栏原样保留，本波未动）。

### 3. fire 侧：去 `orZeroUUID` + unattributed 分支
- **文件**：`cmd/orkome-server/wiring_automation.go`（EnqueueForWakeupWithChain）。
- 删除 `orZeroUUID`（全仓唯一调用点就在本函数，函数一并删除）；`OriginatorUserID/AccountableUserID` 直传 `userID`（空串经 orchestration InsertTask 的 `nullif($7,'')::uuid` 落 NULL，两列同一参数绑定）。
- `OriginatorSource`：空归属时由硬编码 `"trigger_owner"` 改为 **`unattributed`**（`orchdomain.SrcUnattributed`，域内已定义的状态：无授权轨道 fail-closed、仅审计语义）；非空保持 `trigger_owner` 不变。
- **fire 链 originator 实际消费方核查（任务书要求的笔记）**——全部对空值有语义，NULL=系统发起是最小正确处理：
  | 消费方 | 位置 | NULL（空串）语义 |
  |---|---|---|
  | 审批请求通知 | `internal/orchestration/app/service.go:772`（`task.OriginatorUserID != ""` 守卫） | **跳过——无主即无审批通知对象**（"NULL=不发用户通知"成立） |
  | 委派链根归因 | `internal/orchestration/adapters/engine/tools_squad.go:27`（chainRootOf：`ChainRootDirectHuman: root != ""`） | 链根标记为非直连人类——系统发起的正确归因 |
  | 领队派发透传 | `tools_squad.go:71-72` → `ports.go:764 ResolveAttribution` | 空 originator 走 source 分支，unattributed → `(角色轨道空, 审计轨道=accountable)`，domain/rules_test.go 已有该状态用例 |
  | settlement/batch 读侧 | `queries/settlement.sql`、`batch.go:24`（`coalesce(...::text,'')`） | 回读空串，无影响 |
  - 注：`InsertTask` 把 originator 与 accountable 绑定同一参数（`$7`），因此系统任务的 accountable 同为 NULL——无法在不越界改 orchestration pgrepo 的前提下只置空其一。审计可追溯性由 `context.wakeup_id` 承担，见 §四.3。
- **memberNotifier 通知分支不受影响**：`fireOneSteps` 无 agent 目标时的成员通知走 `WakeupMemberTarget`（工单 assignee），与 originator 无关。

### 4. 回归测试（3 组）
| 覆盖 | 测试 | 文件 | 类型 |
|---|---|---|---|
| 迁移 up/down 往返（可空断言 + 哨兵清零 + down 对 NULL 行拒绝 + down 不收紧 + 再 up 幂等） | `TestMigration1037_OriginatorNullableRoundTrip_DB` | `internal/automation/adapters/pgrepo/w2_originator_integration_test.go` | 真 PG（门控） |
| 系统 wakeup 全链：播种（created_by 落 NULL、Ensure 幂等）→ 子工单未终态不 fire → 终态后 fire 落任务（originator/accountable NULL、source=unattributed）→ fire_count/revision/last_task_id/condition_state 推进 | `TestSystemWakeup_FireChain_NullOriginator_DB` | `cmd/orkome-server/wiring_automation_integration_test.go`（真 wakeupEnqueuer + 真 factsReader，与 wiringAutomation 同款装配） | 真 PG（门控） |
| 有主对照：API 形态（created_by=真实用户）fire 后 originator 保持真实用户 + source=trigger_owner（去补零不回归归属链） | `TestWakeup_OwnedWakeup_KeepsTriggerOwnerAttribution_DB` | 同上 | 真 PG（门控） |
| 遗留 NULL created_by 的 time 唤醒：due → TickOnce fire、任务 unattributed、**next_fire_at 按间隔推进**（P0-2"永不推进"症状回归锁） | `TestWakeup_LegacyNullCreatedBy_TimeKind_FiresAndAdvances_DB` | 同上 | 真 PG（门控） |
| API 创建空 created_by 仍 400（FixC 既有回归，本波未动、随包全绿） | `TestCreateWakeup_MissingPrincipalUserID_Returns400` + service `TestWakeup_CreateValidation` 负例 | `internal/automation/adapters/http/routes_test.go` / `internal/automation/app/service_test.go` | fake，恒跑 |

**为什么全链测试必须放 cmd 包**：补零逻辑在装配层 `wakeupEnqueuer`（package main），internal 包测试够不着；fake repo 不经 pgx/PG 约束（batch1 系统性模式 2）。夹具沿用 `newQuotaFixture` 手法（独立库名 `orkome_wake2`，双迁移框架 + TRUNCATE + 最小 seed：user/workspace/agent/`work_item_status('done')`/父子工单）。迁移往返测试放 automation pgrepo 包，与既有夹具共享（无新增逐用例起库）。

---

## 二、门禁摘要（最终）

| 门禁 | 结果 |
|---|---|
| `go build ./...` | OK |
| `go test ./... -count=1`（无 TEST_DATABASE_URL，全仓） | 全绿（见最终回复摘要） |
| `go test ./internal/automation/... -count=1`（无 PG） | 全绿（含 API 400 回归 + service 校验回归） |
| `go test ./internal/automation/... -count=1`（带 TEST_DATABASE_URL） | **全绿**（pgrepo 455s，含新增迁移往返用例） |
| `go test ./cmd/orkome-server/ -run '…Wakeup…' -count=1`（带 TEST_DATABASE_URL） | **全绿**：3 条全链回归（系统 fire / 有主对照 / 遗留 NULL created_by time 推进） |
| `golangci-lint run` | **0 issues**（`internal/goarch` export-data 报错为任务书已声明的环境噪音） |
| `gofmt -l` 提示 | 仓内 .go 工作副本为 CRLF（autocrlf=true 检出态），gofmt 全量标黄属既有状态；.golangci.yml（standard + depguard）不含 gofmt linter，非门禁项 |

TEST_DATABASE_URL 实跑配置：`postgres://postgres:orkome_e2e@localhost:54329/orkome_e2e?sslmode=disable`（容器 orkome-test-pg）。

**过程记录（并行协作）**：本波执行期间全仓 build 两度被 W2 并行 agent 的中间态阻塞（orchestration pgrepo 的 repo.go ↔ repo_mcp.go MCP 块重复声明；cmd 包 router_contract_test / main.go↔wiring_workspace.go 签名过渡态），均由对应 agent 自行收敛至绿，本波未触碰任何非独占文件。cmd 包测试迭代期曾用 `go test -overlay`（临时 stub，仅本地验证用、未改工作树）预验证，最终门禁全部在真实树上跑通。

---

## 三、验证根因修复的现成载体（主会话用）

1. 建 system wakeup（工单带 agent assignee + 一个子工单）→ 子工单置 done → PG `agent_task_queue` 应出现 `originator_user_id IS NULL AND originator_source='unattributed'` 的 queued 任务，wakeup `fire_count=1`、`last_task_id` 指向该任务。
2. 修复前的症状对照：`docker logs orkome-pg` 中每秒一条 `agent_task_queue_originator_user_id_fkey` 违例应绝迹。
3. API 建 time 唤醒（带登录态）→ 正常 fire，任务 `originator_source='trigger_owner'`、originator=登录用户（归属链不回归）。

## 四、给主会话 / 后续 agent 的提示

1. **同族遗留（未修，防丢失）**：`internal/automation/app/service_autopilot.go:407-410` 的 `dispatchRun` 仍保留 `creator == "" → 零 UUID` 兜底，喂给 `work_item.created_by`（**NOT NULL + FK**，1001:177——与 originator 不同，该列是真 NOT NULL）。触发条件：autopilot 行 created_by 为 NULL（HTTP 路径必带 principal，理论边界），届时 createWorkItem 照样 FK 炸。若后续统一"去哨兵"，此处应一并去掉兜底并让失败显式化。
2. **down 迁移的行为变化**：1037 down 在存在系统任务（NULL originator）时会报错——这不是 bug 而是设计（归属决策不可自动逆）。若主会话希望"可静默回滚"，需要产品裁决 down 时删除无主任务行（破坏性，本波未采纳）。
3. **审计线索**：系统任务的 `accountable_user_id` 也是 NULL（InsertTask 参数绑定所限）；人肉排查一条任务是否系统发起，看 `originator_source='unattributed'` + `context->>'wakeup_id'`。
4. **实证核查记录（纠正一个直觉）**：探针实证 `FireWakeup` 把指纹串（如 `"all"`）写进 `condition_state` **是正常路径**——1017 已把该列 boolean→text（1006 建列时的 boolean 形态只是历史），不需要修。
5. **并行阻塞交接**：orchestration pgrepo 的 `repo_mcp.go` 与 `repo.go` 重复声明属 W2 其他 agent 的中间态，本笔记写作时未恢复；本波全部改动收敛在 §一 文件清单内，合并时无冲突面。
6. **本波实际 touched 文件**：
   - `migrations/1037_wakeup_originator_nullable.up.sql` / `.down.sql`（新）
   - `cmd/orkome-server/wiring_automation.go`（去 orZeroUUID + unattributed 分支 + import）
   - `internal/automation/adapters/pgrepo/repo.go`（EnsureSystemWakeup 注释固化设计）
   - `internal/automation/adapters/pgrepo/w2_originator_integration_test.go`（新）
   - `cmd/orkome-server/wiring_automation_integration_test.go`（新）
