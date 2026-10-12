# FP-19 审计：webhooks 订阅面 + admin 运营面（双"零前端消费"域）

> 日期：2026-10-12 · 审计人：FP-19 agent · 方法：七步审计法（全链梳理→契约对齐→curl 实测→代码质量→后端正确性→测试覆盖→P0/P1/P2 清单）
> 环境：后端 :8080（dev）；账号 inventory-test@orkome.dev（is_platform_admin=false，inventory-ws 中 role=**owner**）；测试数据 fp19 前缀，已清理（订阅×2、探针工单×1 均删除；webhook_delivery 落表行保留作证据）
> 前置：docs/inventory-api-2026-10-12.md §10/§11 · docs/feature-audit-batch1-2026-10-12.md（已知缺陷只引用不重复）· docs/pack-tool-contract-design-2026-10-11.md §4.3

---

## 一、一句话结论

**webhooks 订阅面是本次全仓审计中质量最高的域之一：9 条端点全环实测通过、SSRF 三层防御实测全拦、secret 封存/一次性回显/轮换双签名窗齐备，可安全暴露；admin 面"39 条"实际只挂载 10 条（userops/wsops/billingops 三个 Mount 全部未装配），挂载面守卫链硬但审计链断（平台管理员授予/撤销零审计），建议封存待补。**

| 域 | P0 | P1 | P2 | 就绪度评级 |
|---|---|---|---|---|
| webhooks 订阅面（9 条） | 0 | 2 | 4 | **可安全暴露**（后端就绪；补审计+查询面后可对 pack 生态开放） |
| admin 运营面（名义 39 / 实挂 10） | 0 | 2 | 3 | **建议封存**（暴露前必须补 WithAuditor + 装配决策 + 文档对齐） |
| 合计 | **0** | **4** | **7** | |

---

## 二、webhooks 订阅面全链梳理

```
事件源（internal/contract/events，eventbus PhaseProject）
  → cmd/orkome-server/wiring_webhooks.go:53-62  fanout 守卫（ws/event_id 非空 + catalog.Has）
  → ploykit/webhooks/app/service.go:179-184     Emit（catalog 二次校验）
  → ploykit/webhooks/adapters/pgrepo/repo.go:139-155  EmitForEvent
      INSERT ... SELECT FROM webhook_subscription WHERE is_active AND event_types ? $3
      ON CONFLICT (subscription_id,event_id) WHERE status='pending' DO NOTHING   ← pending 幂等
  → webhook_delivery 表（pending, next_attempt_at）
  → adapters/workers/delivery.go               DeliveryWorker 每 5s / 批 20（wiring_webhooks.go:65 注册）
  → service.go:186-211 DeliverPending → deliverOne :257-305
      egressx 校验（:260）→ HMAC 签名头（X-Signature/X-Signature-Old/X-Timestamp/X-Event-Id/X-Event-Type/X-Delivery-Id :270-279）
      2xx → MarkDelivered；Webhook-Delivery: abort-message → 立即死信；否则 retryPlan
  → retryPlan :213-216：1m/5m/30m/2h/6h + ±20% jitter，第 6 次失败 → dead
  → 死信后 maybeAutoDisable :238-255：first_failure_at 起 5 天（AutoDisableWindow）连续失败 → 自动暂停订阅
```

- 表结构：ploykit migrations 011/020/027/031/032（`webhook_subscription` 含 `secret/old_secret/old_secret_expires_at/first_failure_at`；`webhook_delivery` 含 pending 部分唯一索引 `uq_webhook_delivery`，subscription FK ON DELETE SET NULL，workspace FK CASCADE）。
- secret 静态封存：wiring_webhooks.go:43 `sealx.NewSecrets(sealx.DeriveKey(cfg.JWTSecret,"webhook"))`（HMAC-SHA256 派生 32 字节，sealx/derive.go:12-18）；启动日志无 "webhook secrets sealing disabled" → 封存生效（noneSecrets 下 create 会 500 拒绝明文，secrets.go:29-40，fail-closed 设计正确）。
- HTTP 面：webhooks/adapters/http/routes.go:50-58 恰好 9 条，与盘点 §10 一一对应。member 门（:61-81）= 认证 + MemberLookup（wiring 组装 membership+role，wiring_webhooks.go:70-80）+ authz `webhooks:read`；admin 门（:91-99）再叠加 `webhooks:manage`。authz defaults.go：member 只读、admin/owner 读写——与盘点"成员可读、admin 可管"声明一致。
- 订阅 CRUD 全环 curl 实测（owner）：create 201（secret 一次性回显）→ list 200（**secret 不回显**，model.go Subscription 结构无 secret 字段）→ ping 204 → PATCH is_active:false 200 → ping 复得 409 E_CONFLICT "subscription is paused" → PATCH true 200 → rotate-secret 200 `{secret}` ×2（每次新 `whk_` 32字节随机）→ redeliver 200（pending 存在时返回原行不重复入队——repo.go:267-316 语义正确）→ DELETE 204 → 再 DELETE 404。参数校验：unknown event 400、空 url/events 400、PATCH 缺 is_active 400。
- fanout 实测：创建工单 → 2 个订阅各落 1 条 `work_item.created` delivery（event_id 同源）→ worker 拾取 → 目标返回 HTTP 405 → `last_status_code=405, last_error="HTTP 405"` 落表 → 约 1 分钟后 attempts 1→2（重试节奏与 RetryBackoff 吻合）。
- 孤儿语义实测：删除订阅后，其 pending deliveries 被 ClaimPending 孤儿清扫（repo.go:158-163）标记 `status=dead, last_error="orphaned: subscription deleted", subscription_id=""`——投递历史保留、审计不断。

### 2.1 SSRF：三层防御，13 向量全拦（重点结论）

订阅 URL 同时被三道闸拦截（全部在投递路径上，egressx 在场）：

| 层 | 位置 | 机制 |
|---|---|---|
| ① 创建时 | service.go:83 → target.go:21-24 → egressx `ValidateURL` | scheme 白名单（**仅 HTTPS**；http 仅当 WEBHOOK_ALLOW_PRIVATE_TARGET=1 且目标在 PrivateAllowCIDRs 内）、DNS 解析后逐 IP 查私网/保留段黑名单（127/8、10/8、169.254/16、172.16/12、192.168/16、0/8、224/4、::1、fc00::/7、NAT64 等 30+ 段） |
| ② 请求时 | egressx client.go guardTransport.RoundTrip | 每次出站重跑 ValidateURL |
| ③ 拨号时 | egressx client.go `Dialer.Control` → checkDialAddress | **dial-time 对真实连接 IP 复查**——DNS 重绑定/解析漂移的最后兜底 |

实测（POST /webhooks/subscriptions，全部 400 且错误信息可读）：
`http://127.0.0.1:8080`、`http://169.254.169.254/latest/meta-data`、`http://10.1.2.3`、`http://[fd00::1]` → "plaintext http target … not in the egress-policy exception ranges"；`https://127.0.0.1`、`https://localhost`、`https://[::1]`、`https://0.0.0.0`、`https://192.168.1.1` → "resolves to a private/reserved address … blocked by the SSRF guard"；`ftp://` → "must be HTTPS"。公网 HTTPS（example.com）正常放行创建。**egressx 确在投递路径上，SSRF 面合格。**

### 2.2 secret/rotate 24h 窗与 D3 对照

- 算法（model.go:150-156）：`X-Signature: sha256=HMAC-SHA256(secret, "<unix秒>.<body>")`，`X-Timestamp` RFC3339Nano 同源。与 pack-tool-contract §4.3 声明的"框架 subscription signing secret（sealx 封存、创建一次性回显、rotate-secret 轮换）、不是 pack credential"语义一致。
- 轮换（service.go:140-155 + repo.go:110-124）：新 secret 封存写 `secret`，旧封存值平移 `old_secret`、`old_secret_expires_at = now()+24h`（OldSecretTTL，model.go:66）。投递时（service.go:201-208、273-275）窗口内同时带 `X-Signature`（新）+ `X-Signature-Old`（旧），接收端双验平滑换钥——与 agentblog D3 接收端约定吻合（本仓无法直接读 agentblog 代码，算法对照基于契约文档级）。
- 一次性回显实测成立：create 201 与 rotate 200 各回显一次明文；list/PATCH 响应均无 secret。
- 验证边界：本机无接收端可控的 HTTP 靶（私网被禁、e2e 才有 loopback 放行客户端），`X-Signature-Old` 双头未能 live 抓包，为代码路径验证（e2e golden_thread_test.go:344-356 已对主签名做过 delivered 端到端断言）。

---

## 三、admin 运营面：名义 39，实际挂载 10（本次最重要发现）

### 3.1 装配核对

orkome 只调了 `adminhttp.Mount`（wiring_commercial.go:55-59 → main.go:242）。ploykit admin 的四组 Mount 中**仅核心 routes.go 被装配**：

| Mount | 端点数 | orkome 是否调用 | 实测 |
|---|---|---|---|
| adminhttp.Mount（routes.go:44-54） | 10 | **是** | 全部 403（非平台管理员）/ 401（未认证） |
| MountUserOps（routes_userops.go:17-30） | 10 | **否** | 404 page not found |
| MountWsOps（routes_wsops.go:26-35） | 9 | **否** | 404 |
| MountBillingOps（routes_billingops.go:26-42） | 12 | **否** | 404 |

即盘点 §11.2 的 40 行表里只有 10 行真实存在：`GET stats / users / workspaces / audit / audit/export.csv / analytics`、`PATCH users/{id}/status / users/{id}/admin / workspaces/{id}/plan`、`POST users/{id}/impersonate`。用户运维 10 条（detail/export/kick/session revoke/password-reset/mark-verified/PAT 管理/删除用户）、工作区运维 9 条（detail/成员角色/成员移除/删库/配额授予/转移所有权/notify/sso/analytics-recent）、计费运维 12 条（plans/orders/mark-paid/payment-events/run-expiry/run-overage/**webhook-deliveries×2**/cancel-subscription 等）全部未挂载。附带后果：ploykit webhooks 的 adminops（`ListAllDeliveries/AdminRedeliver`，service_adminops.go）在 orkome 是**不可达死代码**。`inventory-api-2026-10-12.md` §11.2 与"零前端消费 39 条"表述需按 10/39 修正。

### 3.2 挂载面拦截矩阵（非平台管理员实测 10 条，全部 403）

| 端点 | 实测 | 守卫点 |
|---|---|---|
| GET /api/admin/stats | 403 platform admin required | requireAdmin routes.go:56-77 |
| GET /api/admin/users | 403 | 同上 |
| GET /api/admin/workspaces | 403 | 同上 |
| GET /api/admin/audit | 403 | 同上 |
| GET /api/admin/analytics | 403 | 同上 |
| POST /api/admin/users/{id}/impersonate | 403 | 同上 |
| PATCH /api/admin/users/{id}/status | 403 | 同上 |
| PATCH /api/admin/users/{id}/admin | 403 | 同上 |
| GET /api/admin/users/{id}（未挂载） | 404 纯文本 | — |
| DELETE /api/admin/workspaces/{id}（未挂载） | 404 纯文本 | — |

守卫链四层（代码级，全部在位）：①全局 `authMw(root)`（main.go:311 链尾包裹，root mux 的 /api/admin 亦过认证）→ ②requireAdmin：未认证 401、`IsPlatformAdmin=false` 403、**impersonated 会话 403**（routes.go:67-70，防链式冒充）、**PAT 来源 403**（:71-74）→ ③服务层自我保护：不可 disable/demote 自己（service.go:64-65、118-119）、impersonate 拒绝自 impersonate + 链式（:127-131）→ ④impersonate 落审计（routes.go:299-303，Rec 已接线）+ SetSessionCookie 换发框架会话。

### 3.3 admin 能力清单与两个未接线的框架钩子

现挂载 10 条的能力面 = 平台统计 / 用户列表分页搜索 / 启停用户 / 授予·撤销平台管理员 / **impersonation（可用）** / 工作区列表·改 plan / 平台审计查询+CSV 导出（上限 50000 行+截断头）/ 7-90 天分析汇总。两个框架能力因未调 With* 而静默缺失（wiring_commercial.go:39 只 `NewAdminService(...)`）：

1. `WithAuditor` 未调 → AdminService.auditor=nil → `admin.user_disable / user_enable / user_role_change`（service.go:79-81、96-99、130-133）**审计记录全部静默跳过**——平台管理员授予/撤销、封禁/解封在 audit 表零痕迹（impersonation 反而有，因为 route 层直接调 Rec）。WebhookRoute 的 `admin.user_kick` 等在 userops 里，本来就未挂载。
2. `WithSessionRevoker` 未调 → DisableUser 不吊销活跃 session 行（service.go:73 nil 跳过）。**实际访问被即刻切断**——identity `VerifySession` SQL 带 `u.status='active'`（identity/adapters/pgrepo/sessions.go:69），封禁后下一个请求即 401，所以降级为 P2；残留风险是"封禁→解封"后旧 session 原样复活（`tokens_valid_after` 未动），语义上"禁用期"会话未被清算。

### 3.4 就绪度评级：建议封存

- 守卫本身无可指摘（10 条抽样全 403/401，PAT/impersonation 双拒）；
- 但：29/39 端点不存在、无任何 web 控制台、关键治理动作零审计、`admin.role` 变更不可追溯。在补齐 A1/A2 并对盘点文档纠偏之前，不应引导任何运营流程依赖此面；现状"恰好只有 10 条可用"不构成攻击面，封存成本为零。

---

## 四、缺陷清单

### P1（4 条）

**FP19-A1 admin 面 29/39 条端点未装配，盘点契约与实现漂移且无守卫报警**
`wiring_commercial.go:55-59`（MountAdmin 只调 adminhttp.Mount）；未调用 `adminhttp.MountUserOps/MountWsOps/MountBillingOps`（vendor/…/admin/adapters/http/routes_userops.go:17、routes_wsops.go:26、routes_billingops.go:26）。影响：盘点 §11.2 的 40 行表 29 行实为 404 纯文本；依赖文档接入的运营/计费/webhook 死信管理面全部不存在；root-mux 无契约对账（盘点 §13 已知"挂了未声明"单向缺陷在此表现为"声明未挂"）。修复：二选一——①补装配三组 Mount（userops 还需构造 UserOpsService/WsOpsService/BillingOpsService）；②修订盘点文档为"10 条挂载 + 29 条框架未装配"，并在 router_contract_test 增加"文档声明 → 实际挂载"双向对账。

**FP19-A2 平台管理员授予/撤销、用户封禁/解封零审计（WithAuditor 未接线）**
`wiring_commercial.go:39`（`adminapp.NewAdminService(adminpg.New(pool), time.Now)` 后无 `.WithAuditor(c.Audit)`）；审计点在 `vendor/…/admin/app/service.go:79、96、130` 因 auditor=nil 被跳过。影响：`PATCH /api/admin/users/{id}/admin`（授予平台管理员！）、`PATCH users/{id}/status` 在 audit 表无痕，治理面不可追责（impersonation 有审计，保护水平不一致）。修复：一行 `.WithAuditor(c.Audit)`（AuditRecorder 接口已满足，Rec 同一 *audit.Recorder）。

**FP19-W1 fanout Emit 错误与计数被完全吞掉：投递静默丢失且无痕**
`wiring_webhooks.go:57` `_, _ = svc.Emit(...)`——DB 抖动/临时错误时该事件对所有订阅静默丢失，无日志、无重试、无死信（EmitForEvent 失败直接返回 error 被丢弃）；返回的"入队条数"亦无人观测。（相邻已知问题：同函数 ：54 对空 EventID 事件静默过滤，属盘点已收录的 wiring 层问题，此处不重复计数。）修复：Emit err 时 `log.Error("webhook fanout failed", "event", e.Event, "event_id", e.EventID, "err", err)`；进阶可加 outboxx 重试或至少 metrics 计数。ploykit 侧语义备注：pending 幂等靠 `uq_webhook_delivery (subscription_id,event_id) WHERE status='pending'`（repo.go:149），事件在 delivered 后重放同一 EventID 会再发一次——orkome 事件源每次 emit 新 EventID，现实中不触发，记录为语义边界。

**FP19-W2 webhooks 域全域零审计 + 自动熔断零通知**
订阅的创建/删除/轮换/启停是工作区级高价值操作，但 ploykit webhooks 域不接 audit.Recorder（routes.go/service.go 全文无 audit）；orkome 也未装配 `webhooks.WebhookHooks`（wiring_webhooks.go 未调 WithHooks，hooks.go:5-9 的 OnDeliveryFailed/OnSubscriptionDisabled 双双为 nil）→ 死信累积、5 天窗自动暂停（service.go:238-255）对工作区**零通知零审计**，用户只会发现"webhook 不来了"而平台侧无任何痕迹。修复：①orkome 给 svc.WithHooks 接 notifRepo.WriteNotice（参照 autopilot fuse 的现有模式 main.go:256-266）；②订阅写操作补 audit.Record（owner/admin 动作）。

### P2（7 条）

**FP19-W3 订阅数无上限**：`webhooks/app/service.go:82-112` CreateSubscription 无 per-workspace 配额；owner/admin 可无限建订阅，每事件 fanout 行数线性放大（webhook_delivery 无保留期清理作业在 orkome 侧运行，020 迁移名带 retention 但未见 worker）。建议：创建时 count 封顶（如 20/工作区）+ 投递表定期清理。

**FP19-W4 投递记录查询面弱**：`GET …/webhooks/deliveries`（routes.go:201-212 + repo.go:232-258）仅 `limit`（>200 静默截断为 50，0/负数同），无 status/subscription_id/event_type 过滤、无游标分页、无单条 delivery 详情、payload 不外露（排障必须进 DB）；`limit` 非数字静默取 50 不报错。另 events 列表（routes.go:214-221）map 遍历顺序不稳定。

**FP19-W6 created_by 死列**：`webhook_subscription.created_by`（011 迁移）从不写入——repo.go:26-38 INSERT 无该列，service 不传创建者；归属信息永久缺失。

**FP19-A3 未挂载路由返回纯文本 404**：`/api/admin/*` 未注册路径返回 Go 默认 `404 page not found`（非 webx JSON envelope），与全站错误契约不一致（root-mux 面既有已知问题，此处新增 admin 实例证据）。

**FP19-A4 sealx 错误文案与 orkome 装配漂移**：ploykit noneSecrets 提示 "set PLOYKIT_SEAL_KEY"（webhooks/app/secrets.go:31、39），orkome 实际从 `cfg.JWTSecret` 派生（wiring_webhooks.go:43），且 orkome config.go 从不读 PLOYKIT_SEAL_KEY——若未来派生失败，按提示配环境变量无效，误导排障。

**FP19-A5 封禁→解封后旧会话复活**：WithSessionRevoker 未接（wiring_commercial.go:39），DisableUser 不动 session 行与 `user.tokens_valid_after`；封禁期访问被 VerifySession `u.status='active'` 即时切断（identity/adapters/pgrepo/sessions.go:69，兜底有效），但解封后封禁前的全部活跃会话原样有效。建议接线 revoker 或 DisableUser 时顺带 `tokens_valid_after=now()`。

**FP19-T1 orkome 侧 webhooks HTTP 面零测试**：router_contract_test.go:105-112 仅声明 9 条路由存在；e2e 只有 golden_thread（D4）覆盖"投递+主签名"一段；CRUD 全环/权限分界（member 只读 vs admin 可管）/SSRF 创建期拦截/rotate/ping/redeliver 无任何测试。ploykit 自身测试未随 vendor 入仓，仓内不可见。本次 curl 记录可作为回补集成测试的用例蓝本。

### 测试覆盖小结

| 面 | 仓内覆盖 | 结论 |
|---|---|---|
| 投递+HMAC 主签名 | e2e golden_thread_test.go:104-135、330-356（fake agentblog 接收端，断言 delivered+payload 形状） | 有（质量好） |
| HTTP CRUD/权限/rotate/ping/redeliver | 无 | 缺（本次 curl 全环补测全通过） |
| SSRF 守卫 | 无（egressx 单测在 ploykit 仓，不在本仓） | 缺 |
| admin 面 | 无（403 矩阵本次首测） | 缺 |

---

## 五、实测记录索引（关键证据）

- 订阅 CRUD：201 create（`4b265753…`/`620d07c3…`，secret 回显）→ list 无 secret → ping 204 → pause 200 → ping 409 → resume 200 → rotate×2 各出新 secret → redeliver 200 复用 pending 行 / 404 未知 id → DELETE 204 → 再删 404。
- fanout：创建工单 `452f2fdc…`（已删）→ 2 订阅各 1 条 work_item.created；HTTP 405 落表，attempts 1→2（1m 档）；并行 agent 的工单事件同样入队（目录门工作正常）。
- 孤儿：删订阅后原 pending → `dead / orphaned: subscription deleted / subscription_id=""`。
- SSRF：13 向量全 400（消息原文见 §2.1）。
- admin：10 条挂载端点全 403 "platform admin required"、未认证 401、9 条未挂载端点 404 纯文本。
- 会话中断插曲：测试中途 cookie 会话 401 一次，重登恢复（与 FP-01 长会话问题方向一致，未深入）。
- 清理：两订阅 + 探针工单均 204 删除；webhook_delivery 历史行留存（16 条，workspace 内，无订阅引用后不再增长）。

## 六、给下批的建议（按收益/成本）

1. `wiring_commercial.go:39` 补 `.WithAuditor(c.Audit)`（一行，清 A2）。
2. `wiring_webhooks.go:57` Emit 错误补日志 + hooks 接通知（清 W1/W2）。
3. 决策 A1：装配或除名 29 条 admin 端点，盘点文档同步纠偏为 10/39。
4. 把本次 curl 矩阵固化为集成测试（webhooks CRUD + SSRF + admin 403），root-mux 补"挂载↔文档"双向对账。
