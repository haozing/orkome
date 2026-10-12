# ploykit 框架层改进需求 —— orkome pack 四波实施反馈

> 日期：2026-10-12 · 来源：`pack-rollout-plan-2026-10-11.md` 四波实施（A1-D4 全收官，终局门禁全绿，见 `handoff-master-implementation-2026-10-11.md`）
> 取证基线：orkome @ 9f574ed + 未提交实施改动；ploykit v0.4.1 本地 clone（D:\code\ploykit）现场读码
> 每条按"现象 → 根因 → 证据 → 建议"组织；P0/P1 是 pack 生态扩张前值得先动的，P2 是顺手项，文末列了**明确不归 ploykit** 的项防止归属漂移。

---

## P0-1【接收端零依赖验签包】首个 pack 集成方被迫手写 HMAC 验签

**现象**：D3 给 agentblog 实现 `/hooks/orkome` 接收端时，验签（HMAC-SHA256、`unixSeconds + "." + body` 拼接、常量时间比较、rotate 双头、±5min 防重放窗）全部**照着 ploykit 源码手工复刻**了一遍（agentblog `mcp/pack/hooks.go`，14 个回放测试）。

**根因**：签名函数其实**已经导出**——`webhooks/app/model.go:150-175` 有 `Sign` / `VerifySign` / `VerifySignWithWindow` / `VerifySignAny`，`OldSecretTTL`（model.go:66）也是导出常量。但接收方用不了它们：`webhooks/app` 这个包带着整个 webhook 服务域（model.go:16 起的 Subscription 等领域类型 + service/repo），一个**外部接收端**（pack 供应商的独立服务）为了四个函数要 import 整个域，还要把 ploykit 拉进自己的 go.mod——agentblog 仓 ploykit 引用数为 0，我们刻意没引入，宁可手写。

**证据**：
- 已导出的签名 API：`D:\code\ploykit\webhooks\app\model.go:150,158,163,170`；rotate 双头投递 `service.go:271-276`（`X-Signature` + `X-Signature-Old` + `X-Timestamp` RFC3339Nano）
- 接收端手写复刻：`D:\code\agentblog\mcp\pack\hooks.go`（验签/防重放/双头全自实现）
- ploykit webhooks 域文档缺位：`webhooks/doc.go` 仅 `package webhooks` 一行，签名协议无 markdown 文档

**建议**：
1. 把签名协议抽成**零第三方依赖的叶子包** `webhooks/verifier`（或 `platform/whsign`，与 platform 层定位更合）：`Sign` / `VerifyWithWindow` / `VerifyAny(secrets...)` + `Protocol` 常量（header 名、时间戳格式、拼接规则、推荐防重放窗）+ 完整的协议文档（含给**非 Go 接收方**的算法描述——pack 生态的接收端语言不限）。
2. `webhooks/app` 反向引用该叶子包（服务端与接收端共享同一实现，协议永不漂移）。
3. 发布为独立 go module（`go.mod` 只剩 stdlib），让 pack 供应商 `go get` 时零传递依赖。这不是过度设计——每一个订阅 orkome 事件的 pack 都要实现同一套验签，第二个 pack 出现时手写就会写出第二份微妙不同的 bug。

**验收参考**：agentblog 的 hooks.go 可整体替换为对该包的调用，14 个回放测试不改断言全绿。

---

## P0-2【事件无 EventID 被静默丢弃】框架应拒绝发射残缺事件而不是让下游静默吞

**现象**：D4 金线第⑧步发现 `work_item.updated` 事件永远到不了 webhook 投递——orkome work 服务没接 outbox，`EventID` 为空，wiring 侧扇出 handler `wiring_webhooks.go:53-61` 的守卫 `e.EventID == "" → return` 直接静默跳过。产品侧修法是补 `SetOutbox` 接线（已修），但**这个 bug 在产品里潜伏到端到端联调才暴露**——单元/集成测试全绿，因为守卫丢弃时不打任何日志、不计数。

**根因**：契约（事件必须有稳定 ID）与执行（缺失时静默丢弃）分居两仓两侧：发射侧 `platform/events/emit.go:44` 只校验 `Kind == ""`，不校验 `EventID`/`IDempotencyKey`；接收侧 orkome wiring 守卫静默 return。

**证据**：
- 发射侧唯一校验：`platform/events/emit.go:44-46`（`empty kind` 报错；EventID/幂等键无检查）
- 静默丢弃点：`cmd/orkome-server/wiring_webhooks.go:54`（`e.WorkspaceID == "" || e.EventID == "" || !catalog.Has(e.Event) → return`，无日志无指标）
- 潜伏期佐证：D4 之前全量测试绿（49 包 + DB 套件），只有八步金线串起真实 bus→delivery 链路才暴露

**建议**（两层，发射侧为主）：
1. `Emitter.Emit` 对 `EventID == ""` 返回错误（与 `empty kind` 同级）——事件缺稳定 ID 意味着下游幂等/重试全部失效，这是契约级残缺，fail-fast 比静默丢弃便宜得多。若担心破坏存量调用方，可加 `EmitStrict` 或先打 WARN + 指标过渡一版。
2. ploykit 提供扇出守卫的标准姿势：导出 `webhooks/app` 的 `FanoutFilter`（或文档化的守卫模板），丢弃时**必须** `log.Warn` + 计数器（`metrics` 包已有基建）——orkome wiring 这段守卫逻辑几乎肯定会在每个用 ploykit webhooks 的产品里复刻一遍，让它长在框架里，下家不用再踩一次"静默丢"。

**验收参考**：拔掉 orkome work 服务的 outbox 接线重跑金线，第⑧步应得到明确错误（发射侧）或 WARN 日志+指标（守卫侧），而不是静默无投递。

---

## P1-1【pgmigrate 新表必须手工同步 TRUNCATE 清单】迁移守卫互相脱节

**现象**：B1 落迁移 1035（新表 `pack_tool_schema`）后 e2e 守卫测试红——TRUNCATE 清单（orkome 侧 `e2e/golden_test.go` + `arch/sql_allowlist.yml`）漏了新表。D4 又补了一次。每个用 ploykit 测试栈的产品新增表都要**记住**同步两个清单，忘记的代价是测试期数据残留导致的诡异 flaky。

**根因**：pgmigrate 只管 DDL 版本，不知道"测试隔离需要全表清单"这回事；清单一分为二（框架表 vs 产品表）且分散在产品仓，无单一事实源。

**证据**：orkome `cmd/migrate/main.go:45-46` 双 FS 挂载模式（`pkgmigrations.FS` 框架 94 个迁移 + `migrations.FS` 产品 1034+）；TRUNCATE 守卫在产品侧 e2e 里手写。

**建议**：pgmigrate 增加 `Tables(pool)` 自省 API——迁移 up 完后从 `information_schema` / pgmigrate 自身记录导出"当前库全表清单"，框架表与产品表一并返回。产品侧 TRUNCATE 守卫改为消费该 API：新表自动进清单，守卫测试只剩"确认清单函数被调用"一件事。顺手提供 `pgmigrate.TruncateIsolation(pool)`（事务内 TRUNCATE ... RESTART IDENTITY CASCADE）把测试隔离姿势也标准化掉。

**验收参考**：orkome 再落一个新表迁移（不动任何清单文件），TRUNCATE 守卫测试应保持绿。

---

## P1-2【webhook 重试/死信节奏无接收方可依据的契约文档】

**现象**：D3 任务书要求"5xx → orkome 侧重试"的语义对接收端透明，agentblog 侧要保证幂等。但重试节奏（几次、什么 backoff、何时判死）接收方只能读 `service.go:213` 的 `retryPlan` 源码反推——`RetryBackoff` 数组与"attempts > len(RetryBackoff) 判死"是**实现细节不是契约**，现在却是对接方唯一的信息源。

**证据**：`webhooks/app/service.go:213-216`（retryPlan / RetryBackoff / dead 判定）；接收方交接文档 `agentblog/doc/pack-hooks-jiaojie.md` 只能以"读码+注释说明"的方式确认格式（D3 交付物原话）。

**建议**：webhooks 域补一份 `doc/signing-and-delivery.md`（或充实 `doc.go`）：签名算法（联动 P0-1）、header 全表、时间戳窗、重试次数与 backoff 表、死信条件与查询 API、`X-Event-Id` 幂等语义。交付方契约一旦写进文档，接收端实现就有了对照验收的依据，也倒逼 `RetryBackoff` 等常量将来若有调整必须走版本化说明。

---

## P1-3【egressx loopback 放行是裸 CIDR API，缺"本地开发模式"语义】

**现象**：D2/D4 联调测试要连 `http://127.0.0.1:随机端口` 的本地 pack server，egressx 默认 SSRF 门拦 loopback。测试被迫**自建一套 loopback guard** 复刻放行逻辑；生产侧则靠环境变量 `ENGINE_EGRESS_EXTRA_ALLOW_CIDRS` 传裸 CIDR 字符串。

**根因**：`NewGuard(extraAllowCIDRs []string)` 是唯一放行面（egressx.go:75），`PrivateAllowCIDRs()`（:59）返回的是**全部**私网段——"只想放 loopback 不想放整个 RFC1918"没有现成表达。

**证据**：`platform/egressx/egressx.go:59,75`；D2 交接笔记遗留坑 #3（真机部署要配 env CIDR）；D4 测试内自建 loopback guard。

**建议**：egressx 增加语义化预设：`LoopbackOnlyAllowCIDRs()`（127.0.0.0/8 + ::1，明确不含 RFC1918/链路本地）与 `NewGuardForLocalDev()`（或 `GuardOption` 变体）——本地集成测试是每个 pack 集成方都要走的路，让"安全地放开 loopback"成为一等公民而不是各自拼 CIDR。生产多域名/内网 pack 的正式放行另议（那涉及凭据与审计，不该是个 flag）。

---

## P2（顺手项）

1. **mcp-go vendored 客户端零值 `http.Client` 连接池竞态**（D2 发现，~1/4 概率 90s 挂起）：根因在 `mcp-go/client/transport/streamable_http.go:139` 与 `sse.go:93` 的 `&http.Client{}`（默认 `IdleConnTimeout=90s`，复用服务端已关闭的 SSE 空闲连接即挂）。**mcp-go 不在 ploykit**（orkome 直接 vendor），严格说不归这条 issue 线，但 ploykit 若来日收纳 MCP 客户端基建（pack 生态的框架半张），这条要在收录前修：显式独立 `Transport` + 可配 `DisableKeepAlives`。暂记于此防止丢失。
2. **`webhooks/app` 文档型 doc.go**：目前一行（`package webhooks`），域的职责/边界/与 events 平台的关系值得一段话——P1-2 的文档若做了可并入。
3. **`platform/events` 与 `webhooks` 的关系一页图**：Emit（事务内）→ bus → 扇出守卫 → delivery，这个链路是每个产品集成的必经之路，现在要拼读两个包的源码才能看全。

---

## 明确不归 ploykit（防止归属漂移，已核实）

| 项 | 归属 | 核实结果 |
|---|---|---|
| mcp-go 升级/Transport 定制 | orkome vendor 层 | `go.mod:17` 直接依赖 mark3labs/mcp-go v0.43.0，ploykit go.mod 零 mcp 引用 |
| TRUNCATE 清单文件本体 | orkome e2e 基建 | 清单文件在产品仓；P1-1 要的是 pgmigrate 提供自省 API 让清单自维护 |
| outbox 接线缺失 | orkome work 服务 | D4 已修（`SetOutbox`）；P0-2 要的是框架侧 fail-fast 防下家再踩 |
| ui-token 60s 过期/现铸现用 | orkome 产品契约 | 非 ploykit 能力 |
| agentblog fakegen 中文种子词 bug | agentblog 仓 | D4 笔记有最小修复建议 |
| golangci-lint stdlib goarch 噪音 | 本机环境 | lint 二进制旧于 Go 工具链，与两仓代码无关 |

---

## 优先级总览

| # | 项 | 价值 | 成本 | 触发条件 |
|---|---|---|---|---|
| P0-1 | 零依赖验签包（verifier 叶子包 + 协议文档 + 独立 module） | 每个 pack 接收端都受益，杜绝第二份手写 | 小（纯抽取+文档） | 第二个 pack 接入前 |
| P0-2 | Emit 对缺 EventID fail-fast + 守卫 WARN/指标 | 消灭"静默丢事件"这类最难查的故障 | 小 | 越早越好（防下家潜伏同 bug） |
| P1-1 | pgmigrate.Tables 自省 API | 新表不再手工同步清单 | 中 | 下次产品迁移前 |
| P1-2 | webhook 投递契约文档 | 接收方对接有据 | 小 | 随 P0-1 一起做 |
| P1-3 | egressx 本地开发预设 | pack 集成方联调体验 | 小 | 随手 |

---

## 增补（2026-10-12 功能点审计两批 + 修复波发现）

> 来源：`docs/feature-audit-batch1/batch2-2026-10-12.md` 与第一波修复实践。前三条为新产品侧证据，后三条为全新框架项。

| # | 项 | 证据 | 建议 |
|---|---|---|---|
| 增-1 | **webx.CSRFConfig 无 MaxAge 旋钮** | gorilla/csrf 默认 12h 在构造期固化；orkome 修 FP-01（CSRF 12h < 会话 7d 长闲置写死锁）时被迫做产品侧镜像 `csrfChain`（`cmd/orkome-server/csrf_session.go`，文件头标注了框架暴露旋钮后的删除条件） | webx.CSRFConfig 增加 `MaxAge` 透传 |
| 增-2 | **wsx hub：WorkspaceMember 未设置时静默 return false** | `hub.go:238-248`——orkome workspace 实时链死亡数周无人察觉（`wiring_ws.go` 漏接线），全因静默 | nil 依赖时构造期或首调用打 WARN（fail-loud） |
| 增-3 | **relayx 无回环防护** | orkome 误用 `Broadcast` 当 `DeliverRemote`（已修），但框架对消费者此类误用无防御——≥2 副本指数空转 | 远端帧带跳数/origin 环检测 |
| 增-4 | identity `CleanupAttempts` 全仓无调用方（与正文 FP-01 P2 呼应） | login_attempt/auth_challenge 无界增长 | 框架内建调度或文档强制 |
| 增-5 | webhooks 域零审计 + `WebhookHooks` 未装配即静默（orkome FP-19 实测） | 订阅连续失败熔断对工作区零通知 | hooks 装配缺失时启动期 WARN |
| 增-6 | admin 域 `Mount*` 三族 31 条端点装配与否全凭消费者（orkome 只挂了 10 条，盘点幻觉 39 条） | 框架可提供 MountAll + 能力清单声明，让"挂了什么"可对账 | 低优先 |
