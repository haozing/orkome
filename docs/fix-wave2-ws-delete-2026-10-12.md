# Fix Wave-2 交接笔记 —— W2-5：工作区删除硬删接线（FP-02 P1-1）

> 日期：2026-10-12 · 执行 agent：W2-5 · 仓库 D:\code\orkome
> 裁决依据：工作区删除 = **硬删**（v0 不做软删）——teardown 接 FixE 已交付的
> `CleanupWorkspaceTables`（work 域 21 表），再走框架 `DeleteWorkspaceCascade`。
> 修复依据：`docs/audit-fp02-workspace-2026-10-12.md` P1-1（52 处 FK 零级联、
> 首个撞墙表 `work_item_status` 23503、任何用过的工作区删不掉）+ FixE 交付笔记
> `docs/fix-wave1-e-work-backend-2026-10-12.md` §给 FixD 的接线签名。
> 独占范围：`cmd/orkome-server/main.go`（hook 注册区）、`cmd/orkome-server/wiring_workspace.go`（新）、
> `cmd/orkome-server/wiring_workspace_test.go`（新）。**未触碰** `internal/work/**`、`migrations/**`。

---

## 一、修复内容

### 1. 接线（cmd/orkome-server/wiring_workspace.go 新文件 + main.go 注册区替换）

删除链真实路径：`DELETE /api/workspaces/{id}` → vendor
`workspace/adapters/http/handlers.go deleteWorkspace` → `app/service.go DeleteWorkspace`
（owner 校验 → GetWorkspace → **BeforeDelete** → `RunInTx{`**OnTeardown**` → DELETE FROM workspace`}`）。

`workspaceHardDeleteHooks(db, cancelActiveTasks)` 返回实现硬删裁决的钩子组，main.go 里
`idDeps.WS.WithHooks(workspaceHardDeleteHooks(db, _oldTeardown))` 一行接线（原内联 hook 字面量删除，
`pgx` import 随之移除）。三段式编排：

1. **BeforeDelete 预清**（框架在 RunInTx 之前调用；错误 → 409 Conflict，**不吞**）：
   `precleanWorkspaceLabelRefs` 用独立小事务清 `agent_to_label` / `skill_to_label`。
   这两张跨域 join 表**没有 workspace_id 列**、对 label 又**无 ON DELETE CASCADE**，
   若不先提交清掉，`CleanupWorkspaceTables` 的 `DELETE FROM label` 必被 FK 挡死
   （FixE 原语未覆盖、其集成测试也未播种的盲区——技能/agent 打标签的工作区正是本波测试播种的"技能绑定"）。
2. **OnTeardown**（框架事务内；框架会**吞掉**钩子错误并继续 DELETE workspace，见 vendor service.go:161-165）：
   a) `_oldTeardown`：取消活跃任务（既有行为，防引擎删除途中再写行）；
   b) `workpgrepo.CleanupWorkspaceTables(ctx, db, wsID)`：FixE work 域 21 表原语，**直接调用、未重复实现**
      （自带独立事务并提交；事务内已 `SET LOCAL orkome.workspace_teardown='on'` 静默 1020 的 search-index 触发器）；
   c) `cleanupWorkspaceCrossDomainTables(ctx, tx, wsID)`：**在框架事务上**按 FK 拓扑序执行
      58 条 DELETE（`workspaceCrossDomainCleanup`），清掉 FixE 清单之外**全部**无级联
      workspace-FK 业务表 + 引用它们的无 workspace_id 子表（orchestration / conversation /
      channels / automation / skills / notification / 产品杂项面 / event_outbox）。
3. 框架 `DELETE FROM workspace`：framework 自有表（member / workspace_invitation /
   workspace_role / workspace_share_link / workspace_oidc_provider）与商用面（`"order"` /
   quota_\* / schedule_plan / subscription_event / webhook_\*）自带 CASCADE，无需清理。

### 2. 跨域清理语句清单（拓扑序，实测自 55432 dev 库 pg_constraint 目录）

关键无级联引用边（全部按"子/引用方在前"排布，单测逐对断言顺序）：

- `notification.recipient_agent_id` → agent：通知必须先于 agent 删（workspace 级 CASCADE 在
  DELETE workspace 时才触发，救不了 agent 这一步）；
- agent 被引用面：`agent_credential` / `agent_mcp_server`（同时挡 workspace_mcp_server）/
  `agent_invocation_target` / `squad_member`（同时挡 squad）/ `squad.leader_agent_id` /
  `chat_session.agent_id`（NOT NULL）/ `chat_pinned_agent` / `task_usage.agent_id` /
  `agent_skill` / `agent_to_label` / `work_item_wakeup`（FixE 已清）→ 全部先于 agent；
- `agent_task_queue` 对 agent / squad 均无级联（work_item 绑定的任务已随 FixE 的
  work_item 级联消失，清单兜住 chat 触发等无单任务）；
- autopilot 家族：`autopilot_run`（引用 trigger / quota_reservation，先于二者）→
  `autopilot_quota_reservation` → subscriber / collaborator / rule_version（后三者有 CASCADE，
  显式语句固化意图）→ `autopilot_quota_period` → `autopilot_trigger` → `autopilot`
  （`target_agent_id` 无级联，先于 agent）；
- chat 家族对 `chat_session` 无级联；channel 家族对 `channel_installation` 无级联，其中
  `channel_inbound_audit` / `channel_outbound_message` / `channel_reply_delivery` /
  `channel_task_delivery` / `channel_chat_context_generation` / `channel_outbound_card_message`
  连 FK 约束都没有（裸 uuid 列），只能按 installation/session **子选**清孤儿；
- `event_outbox` 未发布行随工作区消亡——防止删除后 outbox relay 把死工作区的事件再放出去、
  在 orchestration 等表复活孤儿行；
- `work_item_batch(_item)` 正常已随 work_item 级联，显式语句固化意图。

**有意不清（retention-by-design）**：`audit_event`（审计留存，含 workspace.delete 这条记录本身）、
`analytics_event`（可观测留存；无 FK、永不挡删除）。`agent_runtime` 是全局实例注册表，与工作区无关。

### 3. 事务边界与失败语义（"任一步失败、工作区保留、不留半删状态"）

| 失败点 | 结果 |
|---|---|
| BeforeDelete 预清失败 | 独立事务回滚，什么都没删 → 框架折成 **409** `workspace cannot be deleted: …` |
| FixE 清理（b）失败 | b 自身回滚（work 域行原样）、c 未执行；框架吞错后继续 DELETE workspace，必被残留的 `work_item_status` 种子行 23503 挡下 → 框架事务整体回滚（c 已执行的前几条一并回滚）→ **500**，工作区全须全尾。任何真实用过的工作区都有 status 种子，因此 b 失败不可能滑成"清了一半却 204"的假成功 |
| 跨域清理（c）失败 | 框架事务回滚（跨域行原样），但 b 已独立提交——work 域行先没了、工作区壳仍在。这是 FixE 原语"自带事务"签名的**既定接缝**（其导出签名只收 `*pg.DB`，ploykit `pg.DB.Within` 只认自家未导出 txKey，框架事务无法注入），语句全部幂等、重试删除即自愈 |

已知残留（超出本波裁决，未动）：若未来某工作区只有 c 段数据、无任何 work 域行且 c 失败，
DELETE workspace 可能成功——当前不可能发生（status 种子兜底），记录在案。

### 4. 遗留活体证据处置

`fp02-ws-a`（5ebb1af1-cd06-44ad-b116-b7b62897adc2：2 工单 + 7 条 status 种子 + share-link + 成员史）
与 `fp17-fresh-ws`（7677bf0f-47df-46a3-933c-01d05a14e1eb：7 条 status 种子）在 dev 库 orkome@55432
实测仅含 work 域 + 框架表数据——**本波接线后均可直接删除**。活体回访（重启 dev server 后走
DELETE → 204）由主会话执行；本 agent 未触碰共享 dev server 与 dev 库数据。

---

## 二、测试

### `cmd/orkome-server/wiring_workspace_test.go`（新）

1. **`TestWorkspaceHardDeleteStatementHygiene`**（无库单测）：
   - 58 条跨域语句逐条：单一 `$1`（不得出现 `$2+`）、目标表与声明一致、不碰 workspace 本表、表不重复；
   - 43 对无级联引用边的拓扑序断言（子表语句必须先于父表）；
   - 覆盖面断言：库目录实测的 79 张业务表中，除 FixE 21 表与纯 CASCADE 子表外，
     每张都必须出现在清单里——未来迁移新增无级联 workspace-FK 表而漏接时此处先红；
   - BeforeDelete 预清必须覆盖 agent_to_label / skill_to_label。
2. **`TestWorkspaceHardDeleteIntegration`**（TEST_DATABASE_URL 门控，独立空库 `orkome_wsdel`，
   与 e2e 包的 `orkome_e2e` 并行互扰）：
   - 走**真实 HTTP 链**（identityhttp DevCode 登录 → workspacehttp DELETE），钩子即生产接线
     `workspaceHardDeleteHooks(db, nil)`；
   - **全型数据播种**（~80 表各 ≥1 行，含 nullable 无级联 FK 的行：squad.leader_agent_id、
     autopilot.target_agent_id、task_usage.agent_id、notification.recipient_agent_id、
     chat_session.agent_id、agent_mcp_server.workspace_server_id、agent_credential.pat_id、
     channel_chat_session_binding.chat_session_id、chat_cancel_pending.task_id、
     autopilot_run.quota_reservation_id 等）→ DELETE → **204** → 全表零残留（一次性空库上全表
     计数即工作区残留计数，无 workspace_id 的 join 表同样可断言）→ 重复 DELETE → **404**；
   - **失败注入**（DROP TABLE autopilot CASCADE）：DELETE → **500** 且工作区保留、
     跨域行原样（框架事务回滚）、work 域行已随 FixE 独立事务先清（§一.3 既定接缝实证）、
     BeforeDelete 预清的 join 表已先行消失。

## 三、门禁（最终实测输出）

> 环境：Windows + Git Bash；`TEST_DATABASE_URL=postgres://postgres:orkome_e2e@localhost:54329/orkome?sslmode=disable`（本机 docker `orkome-test-pg`，集成测试自建一次性空库 `orkome_wsdel`）。

1. **`go build ./...`** → 通过。
2. **`go test ./... -count=1`**（无 TEST_DATABASE_URL，集成按惯例 skip）→ 退出码 0，无 FAIL（全绿，15:42 实测）。
   门禁收尾时并行 agent 又在 `internal/orchestration/adapters/http/handlers_mcp_test.go`（其 MCP 凭据
   改动的新测试，TestMCPCredsThreeState_WSLevel 组）落了未完成用例致该域 4 子测试红——与本波文件
   无交集（本波只动 `cmd/orkome-server` 接线层），由该域 agent 自行收敛。
3. **`golangci-lint run`**（全仓）→ **`0 issues.`**（stderr 的 `internal/goarch … export data version 4` 为任务书预告的工具链环境噪音，不影响退出码）。
4. **带库定向回归**（真实树、无 overlay）：
   `TEST_DATABASE_URL=… go test ./cmd/orkome-server/ -run TestWorkspaceHardDelete -count=1 -v` →
   - `TestWorkspaceHardDeleteStatementHygiene` PASS；
   - `TestWorkspaceHardDeleteIntegration/full_type_data_hard_delete_then_404` PASS（~80 表全型播种 → HTTP DELETE 204 → 全表零残留 → 重复 DELETE 404）；
   - `TestWorkspaceHardDeleteIntegration/failure_injection_keeps_workspace_and_propagates` PASS——
     日志实录即为 §一.3 的文档语义：框架 `WARN workspace teardown hook failed; continuing cascade delete … 42P01`
     → `DELETE workspace` 撞上 `25P02 current transaction is aborted` → 500 回滚、工作区保留。
5. 未重启共享 dev server（:8080 未动）；未执行任何 `git add/commit`；dev 库只读（pg_constraint 拓扑实测 + 两块证据工作区行数盘点）。
6. 时序备注：门禁期间并行 agent 正在改 `internal/orchestration/adapters/pgrepo`（MCP 抽迁）与
   `cmd/orkome-server/router_contract_test.go`/`wiring_automation_integration_test.go`，全仓门禁曾短暂被其
   编译中间态阻塞；其收敛后上述 1-4 为最终态实测。

## 四、给后续 agent 的接口与注意事项

```go
// main.go（唯一接线点）：
idDeps.WS = idDeps.WS.WithHooks(workspaceHardDeleteHooks(db, _oldTeardown))
// _oldTeardown 为原"取消活跃任务"逻辑；集成测试传 nil。
```

1. **改 vendor/workspace 域删除语义前先看 wiring_workspace.go 头注**——三段式事务边界
   （BeforeDelete 预清 / OnTeardown=取消任务→FixE→跨域 / 框架 DELETE）全部写在那里。
2. **新增带 `workspace_id` 的业务表（迁移）时**：若无 ON DELETE CASCADE，必须在
   `workspaceCrossDomainCleanup` 增补语句——`TestWorkspaceHardDeleteStatementHygiene`
   的 expected 覆盖面清单需要同步扩（清单以 2026-10-12 库目录实测为准）。
3. **新增"引用业务表但无 workspace_id/无级联"的 join 表**：无 workspace_id 的进
   BeforeDelete 预清（若挡 label/跨 FixE 域）或按父表子选加进跨域清单；两侧都是本清单表的，
   按拓扑序插入即可，单测拓扑对会拦顺序错误。
4. `webx.WriteErr` 语义：预清失败 → 409（框架 Conflict 包装）；teardown 失败 → 500
   （框架吞钩子错误 + 23503 兜底）；未知工作区 → 404。前端无需区分，统一"失败即保留"。
5. 跨域清单对大表（notification / chat_message / event_outbox）是按 workspace 全量 DELETE，
   v0 数据量下无性能问题；将来工作区规模上来再考虑分批。
6. `event_outbox` 清理放在 OnTeardown（框架事务）内：与 b 段不同事务，失败时 outbox 行
   随框架事务回滚保留——死工作区的未发布事件最多多广播一轮，无害。
