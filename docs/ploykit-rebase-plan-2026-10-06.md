# Orkome 底座切换实施文档（Ploykit Rebase）

| | |
|---|---|
| 日期 | 2026-10-06 |
| 状态 | Draft（待批准后按阶段执行） |
| 参与仓库 | `D:\code\miniCrew\orkome`（产品，本仓库）<br>`github.com/haozing/ploykit`（框架，**线上 module 消费**，钉住 **v0.3.0**；验证基线为 master 42386ff） |
| 关联文档 | `docs/adr/0005`（工作区解析）、`docs/adr/0012`（设计独立性）、`docs/route-compare/`、`cmd/orkome-server/router_contract_test.go` |
| 分支策略 | 长线分支 `rebase-ploykit`，每阶段完成即合入 `main`；框架侧改动先合入框架仓库并打 tag |

---

## 0. 基线修订 v2（2026-10-10，与后文冲突处以本节为准）

初稿与附录 D/E 均基于本地框架快照 `D:\code\ploykit`（9e85d12）。2026-10-10 新建 `D:\code\miniCrew\rebuild\` 并全新拉取 GitHub master（**42386ff**，v0.3.0=6e82084 之后又 6 个提交），核验发现框架已大幅演进。**框架基线换锚为 GitHub master 42386ff**，已验证：`go build ./...`、`go vet ./...`、`go test ./...`（**68 包全绿**）。本地 `D:\code\ploykit` 与消费无关（走线上 module），仅作框架开发副本自行维护；`D:\code\miniCrew\rebuild\ploykit` 亦只是验证/阅读用的参照副本，**不是依赖来源**。

**Phase 0 关闭**（框架 master 已完成，逐项核验）：

| 原任务 | 状态 | 证据（master 42386ff） |
|---|---|---|
| P0-1 metrics | ✅ | `platform/metrics/handler.go` 真实现（promhttp + subtle 常量时间 token 门） |
| P0-2 dispatchreason / failuretax | ✅ | `internal/contract/` 现含 apierr / dispatchreason / failuretax / wswire 四包 |
| P0-3 event_outbox 迁移 | ❌ 作废 | 框架已转向 **River** 事务事件（`platform/events`，ADR 0003）：river_job schema 由 `events.Migrate` 引导，不进框架 migrations；orkome 不消费 platform/events（见 D7），此任务无意义 |
| P0-4 非成员 403 | ✅ | `WorkspaceHeaderCtx` 非成员现返回 403 `not a workspace member`，且新增凭据 scope 校验（`p.Scope.AllowsWorkspace`）；缺失头仍 400——D2 双向满足，N1"不加开关"即现状 |
| P0-5 PATPrefix 配置化 | ✅ | `Hub.PATPrefix` 字段 + `defaultPATPrefix = "tk_"`（hub.go:33,75）；orkome 接线配 `ork_`；B6 关闭 |
| P0-8 Principal.AgentID | ✅ | `webx.Principal.AgentID`（principal.go:78） |
| P0-6 / P0-7 | ✅ | 附录 E 已核（code-audit §9 整改、identity 门槛 service_session.go:20-23） |

附录 E 引用的框架侧实施文档（`D:\code\ploykit\docs\framework-impl-2026-10-06.md`）现已不在磁盘上——其勘误内容已被本文档吸收，引用悬空无害；其遗留工作项（B6/B8 等）需按 master 现状复核：**B6 已完成**；**B8（events 词汇收窄）在 master 上已是终态**——框架 `internal/contract` 已无 events 词汇包，orkome 的 `contract/events` 全量本地自持即可（P2-8 的"仅框架域事件 import 框架"前提消失，简化为纯本地保留）。

**新裁定 D7（v2 核心变更）——事件底座分叉**：框架 platform 已删除 `eventbus` / `outboxx` / `schedx` / `flagsx` 四包，由 `platform/events`（River 事务事件：`events.Migrate` 引导 river_job schema、`EmitAt` 定时投递、指数退避 handler、MaxWorkers=10）与顶层 `schedule` 域取代。orkome 业务域深度依赖自家 eventbus 三相位 + outboxx + schedx。裁定：**这四包保留为 orkome 本地平台包**（本就是 orkome 原创平移出去的），本次 rebase 不迁移 River / schedule 域——与"业务域零改动"非目标一致；迁移 River events + schedule 域（自动化 wakeups/autopilots 天然适配 schedule）列为**后续独立项目**。

**连带修订**：Phase 2 替换清单由 15 包缩为 **11 包**（pg/pgmigrate/redisx/storagex/cronx/egressx/sealx/redactx/ids/logx/workers + metrics）；框架迁移已至 **043**（schedule/quota 幂等等新表），产品 1001 段起点更无冲突；D4 仓库与 tag（v0.2.1 / v0.3.0）**已存在**，orkome 直接线上消费（`go get github.com/haozing/ploykit`，无 replace）。**新可选消费面（仅备查，本次不接）**：`authorization`（规则/事实引擎，与 authz 并存）、`schedule`、`settings`、`contractx`（路由/OpenAPI 契约比对——未来可替代 router_contract_test + route-compare 手法）、`renderx`、`pgpart`。

**v3 执行反馈（2026-10-10，Phase 1 实操中确认的两件事）**：

1. **contract 六包全部本地保留（推翻 §4 原 apierr/wswire 换框架行与 D5 上移动作）**：框架 contract 词汇包位于 `ploykit/internal/contract/`——Go internal 可见性规则下**外部模块无法 import**（框架自己的 example 也从不 import 它）。因此 orkome 的 apierr / events / wswire / dispatchreason / failuretax / modelmeta **六包全部留产品侧**，P2-8 的 apierr 部分取消、P5 的 wswire 换框架取消（wsx/relayx 平台包照换）。
2. **依赖钉 v0.3.0 + sumdb 冲突说明**：`v0.3.0` tag 在 sum.golang.org 的记录哈希与当前 zip 不一致——该 tag 原打在 Next.js 历史上，Go 版重写仓库后复用了 tag 号，公共生态里**此版本已永久作废**。orkome 侧已用 `GONOSUMDB=github.com/haozing/ploykit` 完成首次拉取（go.sum 钉住下载哈希；后续机器按 go.sum/vendor 构建无需此变量）。**根治**：框架在 Go 线上打新 tag（如 v0.4.0），此后所有人正常 `go get`；在那之前每次升 ploykit 版本需带 GONOSUMDB。依赖按 tag 钉 v0.3.0（=6e82084；与验证基线 42386ff 的差异仅为文档/UI/一个框架侧特性，本计划不依赖）。

---

## 1. 背景与目标

ploykit 是从 runlynk / agentblog / **orkome** 三个项目蒸馏出的 Go 多租户 SaaS 框架，platform 层 18 包已平移 17 个，且 wsx / pgmigrate / schedx 在框架侧已有超越 orkome 现版的实现。本计划把 orkome 从"自带全套底座"改为**消费 ploykit 的产品仓库**，依赖方向回归框架契约：**产品 → 框架，单向**。

### 目标

1. 删除 orkome 中与 ploykit 重复的底座（platform 18 包、identity 中的身份/工作区部分、webhooks、notification），换为框架 import；
2. 获得 orkome 缺失的能力：billing / quota / admin / audit / analytics / OAuth / DB 可吊销会话 / 声明式 RBAC；
3. 前端收编 `@ploykit/client` + `@ploykit/ui`，删除同源的本地实现；
4. 全程绞杀者模式：**每个阶段结束时系统可构建、可测试、可发布**。

### 非目标

- 不改七个业务域（orchestration / work / conversation / channels / content / skills / automation）的任何业务逻辑；
- 不动 MCP 接入面与 `client-kit/`（外部 runtime 的 9 个工具、`ork_` 凭据语义保持不变）；
- 不引入 Next.js 版 ploykit（`D:\code\ploykit-react` 是另一条产品线，与本计划无关）；
- 不做数据库存量数据迁移（裁定 D1，备选路径见附录 A）。

---

## 2. 裁定记录

以下裁定是后续所有阶段的前提。执行中如推翻任一条，需先更新本文档并评估受影响阶段。

| # | 裁定 | 理由 | 备选 |
|---|---|---|---|
| D1 | **重建迁移基线**（greenfield）：丢弃 orkome 001–004 / 038 / 009 中身份类表，直接采用框架迁移打底（框架已至 021 `021_invitation_pending_unique` 且持续增长，占 001–999 段；产品迁移一律 1001 起，见 §7） | 仓库仅有一个 initial commit（2026-10-04），尚无必须保留的存量数据；越晚切换成本越高 | 附录 A 的一次性 cutover 迁移 |
| D2 | **工作区解析契约以 orkome 为准**：修改 ploykit `WorkspaceHeaderCtx` 的**非成员分支 404 → 403**（审计核实：缺失头分支框架已返回 400 `E_VALIDATION`，无缺口；身份域自身端点维持 404） | ADR 0005 + `docs/route-compare/` 是已验证的外部表面；框架还处于早期，消费者少，改框架代价最小 | 改 orkome 契约并重录 route-compare、修订 ADR 0005 |
| D3 | **路由器从 chi 迁到标准库 `http.ServeMux`**（Go 1.26 方法+路径模式） | 框架各域 `Mount(mux, Deps)` 绑定标准 mux；chi 与之不兼容；顺势砍掉一个依赖 | 保留 chi，身份域路由手工挂载（Mount 不可用，不推荐） |
| D4 | **依赖引入（v3 修订）**：纯线上 module 消费——`go get github.com/haozing/ploykit@<tag/commit>`，**无 replace、无本地路径耦合**；基线锚定已验证的 42386ff（68 包测试绿），框架更新走正常升版 + `go mod vendor` | 单一事实源在 GitHub；vendor / CI / 协作者零特判 | replace 指向本地副本——仅在调试框架本身时临时使用，不入库 |
| D5 | **contract 归属**：`dispatchreason`、`failuretax` 上移框架；`modelmeta`（带 eino 依赖）**留在 orkome** | 框架不背 LLM 引擎依赖；前两者是通用任务编排词汇 | 全部留产品侧（框架消费方复用性差） |
| D6 | **接受会话语义升级**：无状态 JWT cookie → 框架 DB 可吊销会话（滑动 7 天 / 绝对 30 天） | 可吊销、会话列表、改密全局失效是安全刚需；orkome.com 商业化前必须补上 | 框架侧支持双轨（不推荐，双轨即债务） |
| D7 | **事件底座四包（eventbus/outboxx/schedx/flagsx）保留为 orkome 本地平台包**，不迁框架 River events / schedule 域（v2 新增，见 §0） | 框架已用 River 取代这四包；orkome 业务域深度耦合三相位总线 + outbox + schedx，迁移即触碰业务逻辑，违反"业务域零改动"非目标 | 迁移 River events + schedule 域（自动化 wakeups/autopilots 天然适配），列为后续独立项目 |

**既有事实澄清**（框架 README 多处过时表述——不止两处，含钩子计数行 16→19 已接线 + webhooks(2) 规划中——由框架实施文档批次 A 一并修正）：框架 `relayx` 已是真实现、`pgmigrate` 已有全套 Up/Down/Status；真正的缺口只有 `platform/metrics` 占位与 `contract` 缺三个词汇包（其中 modelmeta 按本计划留产品侧）。

---

## 3. 目标架构

### 3.1 依赖方向（与框架 example 消费模式一致）

```
github.com/haozing/ploykit          ← 框架（identity/authz/workspace/billing/quota/
  │                                   admin/notify/audit/analytics/webhooks/platform/contract）
  ▼  单向依赖，框架禁止 import 产品
orkome（本仓库，产品）
  ├── cmd/orkome-server/            ← 唯一组装点（main + wiring_*.go）
  ├── cmd/migrate/                  ← 迁移入口：框架 FS + 产品 FS 同队列执行
  ├── internal/                     ← 七个业务域 + 产品侧扩展域（见 3.2）
  ├── internal/platform/            ← 仅剩四包（D7）：eventbus/outboxx/schedx/flagsx，
  │                                   其余全删换框架 import
  ├── internal/contract/            ← 仅剩 modelmeta（D5）
  ├── migrations/                   ← 重编号后的产品迁移（1001 起，见 §7）
  └── web/                          ← Vite SPA，消费 @ploykit/client + @ploykit/ui
```

### 3.2 产品侧扩展域（保留在 orkome、叠在框架身份域之上的部分）

原 `internal/identity` 瘦身为**产品账户扩展域**，只保留框架没有的：

| 保留项 | 现位置 | 处理 |
|---|---|---|
| PAT → agent 绑定（`SetAgentBinding`，`main.go:222-231`） | cmd 接线 | 产品包装器：包装框架 `TokenService`，在 PAT 解析后追加 agent 映射查询 |
| runtime 写围栏（`guard_runtime.go`） | cmd 接线 | 原样保留，挂在框架 `webx.Authenticate` 之后；与 authz 角色体系正交（runtime 凭据不受工作区角色管辖） |
| `ui_token` / `pack_ui_entry`（插件嵌入，迁移 039） | identity 域 | 迁入产品账户扩展域，表保留 |
| `pinned_item`（侧栏钉选，迁移 004/027） | identity 域 | 迁入产品账户扩展域，表保留 |
| 邮箱验证码登录的 AllowSignup/AllowedEmails/AllowedDomains 门槛 | `wiring_identity.go:47-48` | 映射到框架 identity 对应配置项（Phase 3 核对，缺则在框架补配置面） |

### 3.3 实时层（WS）

审计核实：orkome 现版 wsx **已具备** CtrlAuth 控制帧（`hub.go:245`）、subscribe/scope 模型、capabilities 协商与 PATResolve——两边帧协议同源，差异比预想小，集中在两点：①**连接工作区来源**——orkome 从 `?workspace_id=` 查询参数取（`hub.go:212`）；框架 wsx 除 auth 控制帧外**亦支持 `?workspace_id=` 直入**（`platform/wsx/hub.go:206-207`），实际差异比原表述更小；②relayx 三态模型与双端 event_id 去重深度的语义差。WS 消费方只有浏览器前端（client-kit 走 MCP HTTP，不经 WS，已核实），因此 wsx/relayx 后端替换与前端 WsClient 替换仍安排在 **Phase 5 同一批次原子切换**（R3 风险等级据实下调）。

---

## 4. 替换对照表（全景）

| orkome 现有件 | 处理 | ploykit 对应 | 备注 |
|---|---|---|---|
| `internal/platform/pg`、`pgmigrate`、`redisx`、`storagex`、`cronx`、`egressx`、`sealx`、`redactx`、`ids`、`logx`、`workers` | **删除换 import（Phase 2，11 包 + metrics）** | `platform/*` 同名包 | 机械替换 |
| `internal/platform/eventbus`、`outboxx`、`schedx`、`flagsx` | **保留为本地平台包（D7）** | 框架已删（River events / schedule 域取代） | orkome 原创包回流自持；迁 River 是后续独立项目 |
| `internal/platform/webx` | **整包保留至 Phase 3** | `platform/webx` | 其 `Authenticate` 是 orkome JWT 实现，框架版依赖框架会话表（P3-1 才建）；Phase 2 仅补插框架新增的 RequestID/Timeout/BodyLimit 中间件 |
| `internal/platform/wsx`、`relayx` | **延迟到 Phase 5 原子切换** | `platform/wsx`、`platform/relayx` | 帧协议不兼容，见 §3.3 |
| `internal/platform/metrics` | **先上移框架**（Phase 0）再消费 | `platform/metrics` | OTel + promhttp，含 `/metrics` token 门 |
| `internal/identity` 身份部分（验证码登录/JWT 会话/PAT/邀请/分享链接/工作区 CRUD） | **删除换框架** | `identity` + `workspace` | 会话语义升级（D6）；PIN/ui-token 留产品（§3.2） |
| （无） | **新增消费** | `authz` | 声明式 `Require`，权限点目录见附录 B |
| （无） | **新增消费** | `billing`、`quota`、`admin`、`audit`、`analytics` | 商业化底座，Phase 6 仅接线不开发 |
| `internal/webhooks` | **删除换框架** | `webhooks` | 同源（HMAC + 同退避表）；事件词表 Register 进框架 EventCatalog；多出 ping/redeliver/启停/保留策略 |
| `internal/notification` | **删除换框架** | `notify` | 站内信去重三档 + SMTP；mention/静音组词汇做成 notify 之上的产品投影层 |
| `internal/contract/dispatchreason`、`failuretax` | **上移框架**（Phase 0） | `contract/*` | D5 |
| `internal/contract/apierr`、`events` | **apierr 删除换框架（Phase 2）；events 按裁定 N2 方向反转：保留本地** | `contract/apierr`；`contract/events`（仅框架域词汇） | 框架 events 将收窄为框架域词汇（Envelope + notification/workspace/member/invitation）；orkome 产品词汇（work_item/comment/chat/label/project/task/batch 等）留本地，仅框架域事件 import 框架 |
| `internal/contract/wswire` | **随 Phase 5 与 wsx 同批换框架** | `contract/wswire` | wiring_ws 同时 import 两者，绑定切换 |
| `internal/contract/modelmeta` | **保留** | — | eino 依赖留产品（D5） |
| 七个业务域 + `internal/arch`（重写）+ `e2e/` | **保留** | — | 业务零改动 |
| `web/src/lib/api.ts`、`ws.ts`、`realtime/client.ts`、`ws-query-bridge.ts` | **删除换包** | `@ploykit/client` | 同源设计：CSRF bootstrap、event_id 去重、scope 引用计数 |
| 自写 AppShell / 设置页 | **换框架页** | `@ploykit/ui` | 产品 15 页只改数据层 import |

---

## 5. 阶段实施计划

每阶段 = 一个可合并到 `main` 的增量。完成标准全部满足才进下一阶段。

### Phase 0 · 框架补齐（在框架仓库，对 orkome 零风险）

**v2 状态（2026-10-10）：本阶段整体关闭**——P0-1/2/4/5/8 已在框架 master 42386ff 完成，P0-3 作废（D7），P0-6/7 附录 E 已核。逐项证据见 §0。**执行从 Phase 1 开始。** 以下保留原始任务文本作历史记录：

**目标**：消灭"框架缺件"，让消费期不需要在产品仓库里绕行。

- [ ] P0-1 平移 `internal/platform/metrics`（OTel + promhttp + token 门）到 `platform/metrics`，替换 doc.go 占位；补表驱动测试
- [ ] P0-2 上移 `contract/dispatchreason`、`contract/failuretax`（从 orkome 拷语义、去业务引用，按框架 ADR 0012 的"零字面拷贝"原则重写表述）
- [ ] P0-3 框架 migrations 新增 `event_outbox` 建表迁移（当前 outboxx 代码有、迁移无；含 `idx_outbox_pending(workspace_id,id)`、`idx_outbox_pending(lease_expires_at)` 等同形索引）
- [ ] P0-4 `WorkspaceHeaderCtx` 错误语义对齐 D2：**仅非成员分支** 404→403（缺失头分支框架已返回 400，无缺口；按框架裁定 N1 **不加配置开关**，直接改 403——当前框架消费者仅 example，同批适配）
- [ ] P0-8 框架 `webx.Principal` 增加产品扩展位（`AgentID` 字段或通用 `Attrs`）：orkome 的 PAT→agent 绑定与 runtime 写围栏依赖 `Principal.AgentID`（`internal/platform/webx/principal.go:24`），框架现版 Principal 无此字段——**Phase 3 交换 webx 前必须先在框架就位**
- [ ] P0-5 框架 wsx PAT 前缀配置化：框架侧实况是 `tk_` 两处硬编码（`platform/wsx/hub.go:297` 判别、`identity/domain/rules.go:87` patPrefix 常量），改配置项 `PATPrefix`（默认 `tk_`）同源取值（框架实施文档 B6；原引 hub.go:270 的 `ork_` 系 **orkome 侧**代码——产品经配置 `ork_` 保持 MCP 外部契约不变）
- ~~P0-6 清理 `docs/code-audit.md` 所列 14 处死代码 / 6 处双轨；修正 README 状态节两处过时行（relayx、pgmigrate）~~ **无需执行（已完成）**：死代码/双轨已于 2026-10-05 全清（code-audit §9 整改记录）；README 状态节修正由框架实施文档批次 A 承接（实为多处，不止 relayx/pgmigrate 两行）
- ~~P0-7 框架侧补齐 orkome 身份门槛配置面核对：AllowSignup / AllowedEmails / AllowedDomains 是否已有等价物，缺则补~~ **无需执行（已完成）**：框架 `identity/app/service_session.go:20-23` 已有完整门槛逻辑

**完成标准**：框架仓库 `go test ./...` 全绿（含新增包）；README 状态节与代码一致；打 tag（如 `v0.3.0`）。

**回滚**：纯框架仓库改动，git revert 即可，不影响 orkome。

### Phase 1 · 依赖接入（orkome 仓库）

**目标**：建立产品 → 框架的编译期依赖与守门规则，不改任何运行行为。

- [x] P1-1 `go.mod`：module path 保持 `orkome`；`go get github.com/haozing/ploykit@v0.3.0`（D4：纯线上 module，无 replace；v3 执行记录：tag 有 sumdb 冲突，首次拉取带 `GONOSUMDB=github.com/haozing/ploykit`，见 §0 v3）；`go mod vendor` 同步（otel 1.46→1.47 等传递抬升随附）✅ 2026-10-10
- [x] P1-2 重写 `.golangci.yml` depguard：app-surface 增加 `github.com/haozing/ploykit` 前缀放行（其余规则不动）✅ 2026-10-10
- [ ] P1-3 重写 `internal/arch` 中 platform 纯净测试为"框架依赖白名单"测试；业务域依赖规则测试原样保留
- [x] P1-4 vendor 策略（v3 简化）：常规 `go mod vendor` 流程，无任何 replace/特判 ✅ 2026-10-10（实测：otel 升版需补一条 `go get go.opentelemetry.io/otel/internal/global@v1.47.0` sum 条目后 vendor 干净）

**完成标准**：`make build && make test && make lint` 全绿；除 go.mod/守门文件外无业务代码 diff。

**回滚**：revert 单 commit。

### Phase 2 · platform 层替换 + 路由器迁移（wsx/relayx 除外）

**目标**：底座换血，行为等价。

**执行记录（2026-10-10，M1+M5 完成并提交，P2-4/P2-6 待下批）**：
- P2-1 ✅：75 个文件换 import；11 个本地平台包（pg/pgmigrate/redisx/storagex/cronx/egressx/sealx/redactx/ids/logx/metrics）删除；保留 7 包（D7 四包 + webx/wsx/relayx），其中 outboxx→框架 pg、schedx→框架 cronx、relayx→框架 logx 的兄弟依赖已重指向；`sealx.DeriveKey`（框架无此 API）下沉为产品侧 `deriveSealKey`（wiring_orchestration.go）；arch 测试与 depguard 放行 `github.com/haozing/ploykit/platform/*`
- P2-2 顺延：框架 `platform/workers` 在 v0.3.0 之后才落地，orkome cmd 本地注册表保持至升版本
- P2-3 并入 Phase 3：框架 webx 的 RequestID/Timeout/BodyLimit 与本地 webx 整体交换时一起上，避免混插半个栈
- P2-5 ✅：新增 `migrations/embed.go`（`//go:embed *.sql`）；cmd/migrate 与 main.go readyz 检查、26 个集成/e2e 测试文件全部改 `pgmigrate.New(pool, migrations.FS, ".")`（框架包名为 `pgm`，需别名 import）；仅挂产品 FS，框架 FS 待 P3-1
- P2-7 ✅：eventbus/outboxx 本地保留，`event_outbox` 仍由产品迁移 009 创建
- P2-8 ✅（v3 已取消换框架动作）：六个 contract 包全本地
- **接受的行为差异**：框架 egressx 对无法解析的 https 域名 fail-closed（本地旧版尽力放行、连接期才失败）——MCP 快照分类从 `connect_failed` 变为 `ssrf_rejected`，终态一致（工具不可用），两个测试已按新语义重写（ADR 0002 血统的安全强化）
- 验证：`go build` / `go vet` / `go test ./...` 全绿（55 包）；`go mod tidy` 后 ploykit 转直接依赖（go.mod 第一 require 块）

- [ ] P2-1 逐包替换 import（**11 包 + metrics**，v2：eventbus/outboxx/schedx/flagsx 按 D7 保留本地）：全仓 grep `orkome/internal/platform/` → `github.com/haozing/ploykit/platform/`；删除对应本地包（**wsx/relayx/webx 及 D7 四包除外**，临时保留并标注 `// DEPRECATED: Phase 5（webx: Phase 3；D7 四包：长期保留）`。webx 延后原因：其 Authenticate 是 orkome JWT 实现，框架版依赖框架会话表，P3-1 才建，提前切换认证即断）
- [ ] P2-2 `cmd/orkome-server/workers.go` 换框架 `platform/workers`（同形状：Add/AddFunc/Start/Drain/AllHealthy；`/readyz` 联动核对）
- [ ] P2-3 中间件栈**补插框架新增件**（RequestID / Timeout / BodyLimit）进现有 orkome 栈；Authenticate、CSRF 等身份相关中间件**保持 orkome webx 版本至 Phase 3**；`/ws` 豁免 DB 超时层
- [ ] P2-4 **chi → 标准库 ServeMux**（D3）：全量路由注册迁移（`cmd/orkome-server/routes.go` 路由契约映射表 + 各域 `adapters/http/routes.go`）；URLGroup/中间件嵌套改写为模式前缀
- [ ] P2-5 `cmd/migrate` 改用框架 pgmigrate 作为执行器，但**仅挂产品 FS**——本阶段产品迁移仍含 001–004（user/workspace/member），与框架 002–005 表名冲突，**框架 FS 必须等 P3-1 基线重建时才入队**；down/status 子命令从框架获得
- [ ] P2-6 更新 `router_contract_test.go` + `docs/route-compare/`：本阶段仅因路由器迁移产生的表面变化（预期为零或极小，任何差异逐条记录裁定）
- [ ] P2-7 outbox relay / eventbus 接线核对（v2/D7）：两者**保留本地实现不动**，仅核对 `event_outbox` 表仍由产品迁移（原 009）创建——框架无此表（已转 River），产品侧同表 DDL **保留**（撤销初稿"删除"指令）
- [ ] P2-8 contract `apierr` 换框架 import（`wswire` 留待 Phase 5 随 wsx）；`events` 按裁定 N2 **保留本地**——v2 核验：框架 `internal/contract` 已无 events 词汇包，B8 收窄在 master 上已是终态，orkome 全量本地自持 `contract/events` 即可，无 import 框架词汇的动作

**完成标准**：`make build/test/lint` 全绿；`docker run postgres → cmd/migrate up → 起服 → curl /healthz /readyz` 通过；e2e 全量绿；route-compare 差异清单为空或已裁定。

**回滚**：阶段分支整体 revert；本地 platform 包在 P2-1 删除前留存于 git 历史。

### Phase 3 · 身份 / 工作区 / authz 切换（最大里程碑，独立验收）

**目标**：身份底座换为框架，工作区模型对齐，声明式权限上线。

- [ ] P3-1 **迁移队列重建**（D1）：产品 `migrations/` 删除 001–004、038 及 009 中身份类表（`notification_preference`），其余按原相对顺序重编号为 **1001 起**（§7；原定 021 起与框架已用的 `021_invitation_pending_unique` 冲突，按裁定 N3 产品仓库一律 1001 段）；`event_outbox` 已由框架迁移覆盖（P0-3），产品侧同表迁移删除
- [ ] P3-2 表兼容核对清单：产品表外键目标 `workspace(id)`/`"user"(id)`/`member` 与框架表 id 类型（uuid）、member 角色值域（owner/admin/member）逐一比对；不一致处改产品迁移
- [ ] P3-3 `cmd/orkome-server/wiring_identity.go` 重写：框架 `identitypg.New` + `SessionService`/`AccountService`/`TokenService` + `identityhttp.Mount`；删 orkome JWT 会话（`webx/auth.go` 旧实现已被框架 Authenticate 取代）；`POST /auth/refresh`、`/auth/google` 死路由清理
- [ ] P3-4 workspace 换框架 `workspace` 域（CRUD/成员/邀请/分享链接/last-owner 保护语义比对：框架已修复"Leave 不触发清理"的母本 bug，确认 orkome 行为跟随）
- [ ] P3-5 产品账户扩展域落地（§3.2）：pins、ui-tokens、cli-token、PAT→agent 绑定包装器
- [ ] P3-6 authz 上线：`authz.New(catalog, roles, WithProvider(authzpg.New(pool)))`；产品权限点 Register（附录 B）；逐域把 `routes.go` 中手写角色判断替换为 `Require(a, "域:动作")` / `RequireOwn`；owner⊇admin⊃member 显式映射与 orkome 现行中间件行为逐路由比对
- [ ] P3-7 前端登录/会话流最小改造（仅为保持可用：cookie 名、`/config`、登出、PAT 页），完整收编留 Phase 5
- [ ] P3-8 route-compare 重录身份/工作区表面（`/auth/*`、`/api/workspaces*`、`/api/tokens*`、`/api/invitations*`、`/api/share-links*`）
- [ ] P3-9 按 D4：rebase 收尾时从框架 master 打新 tag（如 v0.4.0），orkome 升版本 + `go mod vendor`（全程无 replace）

**完成标准**：全新 DB `migrate up` → 注册/验证码登录 → 建工作区 → 邀请/加入 → PAT 签发/吊销 → runtime 凭据（`ork_`）走通 client-kit 三步接入全流程；e2e 全绿；`X-Workspace-Id` 缺失 400 / 非成员 403 契约测试通过。

**回滚**：阶段分支 revert + DB 重建（D1 下无数据保留义务）。

### Phase 4 · webhooks / notification 替换

- [ ] P4-1 删 `internal/webhooks`，换框架 `webhooks` 域：orkome 事件词表（产品词汇留本地 `contract/events`，见 N2/P2-8）Register 进 `EventCatalog`；`wiring_webhooks.go` 重写；`event_delivery` 表由框架 011/020 覆盖
- [ ] P4-2 删 `internal/notification`，换框架 `notify`：bell 收件箱 + 偏好（框架 009/018 表）；mention/静音组/订阅投影做成 `internal/notification`（重生为产品投影层，监听 eventbus Broadcast 相位，写框架 notify 端口）；实时推送待 Phase 5 接 wsx `SendToUser`
- [ ] P4-3 automation 入站 webhook 管线**不动**（产品域），仅核对其公开端点路由在 ServeMux 迁移后无回归
- [ ] P4-4 route-compare 重录 webhooks/notifications 表面；e2e 补 ping/redeliver/启停用例

**完成标准**：webhook 投递 e2e（签名头、退避、重放）绿；通知角标/偏好/已读流绿。

### Phase 5 · 实时层原子切换 + 前端收编

**目标**：WS 前后端同批切换 + 前端底座收编（§3.3）。

- [ ] P5-1 后端：删本地 `platform/wsx`、`relayx`；`wiring_ws.go` 重写为框架 Hub（AllowedOrigins 同源回退 + `PATPrefix=ork_`）；`/ws` 挂框架 `ServeWS`；Broadcast 相位 → wswire Frame 的映射逻辑移植到新接线；relay 模式（REDIS_URL 驱动 memory/sharded/mirrored）按框架语义重配
- [ ] P5-2 前端：删 `web/src/lib/api.ts`、`ws.ts`、`realtime/client.ts`、`ws-query-bridge.ts`，换 `@ploykit/client`（vite alias 源码引用，同框架 example）；工作区 id 注入改 `setWorkspaceIdProvider`
- [ ] P5-3 `@ploykit/ui` 接入：AppShell + 登录/找回密码/邮箱验证 + SettingsShell（成员/概况/审计/用量/计费/Webhooks/邀请）换框架页；产品 15 页（work-items/agents/squads/skills/chat/automation/my-work/notifications/mcp/run-logs/settings）数据层 import 切换，路由与页面结构不动
- [ ] P5-4 queryKeys 约定迁移：产品查询键对齐框架层级元组约定，WS 失效桥按前缀失效
- [ ] P5-5 通知实时推送接通：产品投影层 → wsx SendToUser → 前端 NotificationBell
- [ ] P5-6 浏览器端双开冒烟：多标签页同工作区实时同步、task/chat scope 订阅、断线重连退避

**完成标准**：`pnpm test`（web vitest + MSW）绿；GUI 冒烟清单通过；旧 WS 代码全删。

### Phase 6 · 契约收尾与白赚能力接线

- [ ] P6-1 `router_contract_test.go` + `docs/route-compare/` 全量终录（本计划验收门）
- [ ] P6-2 billing/quota/admin/audit/analytics 接线（仅 Mount + 种子套餐 + usage 配额项，不做商业化开发）：quota 键建议 `tasks:dispatch`、`storage:bytes`、`agents:count`；`workspace.OnTeardown`/`OnMemberRemoved` 钩子接 orkome 的级联清理（UserRevocation 范本）
- [ ] P6-3 e2e 全量重跑（含 TRUNCATE 守卫）
- [ ] P6-4 新 ADR `0013-ploykit-rebase.md`：记录裁定 D1–D6 与本计划执行结果；修订 ADR 0012 中"platform 自持"表述
- [ ] P6-5 README/Layout 节、各域 README、`Makefile` 更新；删除全部死代码（含 `orkome-server.exe` 构建产物入 `.gitignore`）

**完成标准**：全量测试绿；`make build` 产物在干净环境（新 DB + 新机器）按 README Quick Start 走通。

---

## 6. 测试与验收策略

| 层级 | 手段 | 阶段门 |
|---|---|---|
| 单元 | 既有 `make test`（框架依赖后架构规则测试必跑） | 每阶段 |
| 集成 | `make test-int`（TEST_DATABASE_URL） | 每阶段 |
| 路由契约 | `router_contract_test.go` + `docs/route-compare/`（本计划的核心回归网：任何表面变化必须显式重录并裁定） | P2/P3/P4/P6 |
| e2e | `e2e/` 全量 + client-kit 三步接入手工回归 | P3/P6 |
| 前端 | web vitest + MSW；P5 加浏览器双开实时冒烟 | P5 |
| 架构守门 | depguard（业务域互斥 + 框架白名单） | 每阶段 |

原则：**契约先行**——每个阶段动手前，先把该阶段会触碰的路由表面在 route-compare 里做"预期差异清单"，实现后逐条对账。

---

## 7. 迁移重编号方案（D1 基线重建）

### 丢弃（由框架迁移覆盖）

| orkome 迁移 | 表 | 框架对应 |
|---|---|---|
| 001_extensions | pgcrypto 等 | 001 |
| 002_workspace | workspace | 003 |
| 003_users_members | "user"、member | 002 + 003 |
| 004_identity | verification_code、workspace_invitation、workspace_share_link、personal_access_token、pinned_item | 004（auth_challenge/session/login_attempt/PAT）+ 005；pinned_item 移入产品迁移 |
| 009（部分） | notification_preference | 018 |
| 007（仅剥离 `inbox_item` 的 DDL，其余 orchestration 表**保留**） | inbox_item | 009（框架 `notification` 表直接顶替，含 035 改名后的最终形态） |
| 035_notification_rename_inbox | inbox_item → notification 改名 | 009（改名后表名恰与框架一致，本迁移整体作废） |
| 038_webhooks | webhook_subscription、event_delivery | 011 + 020 |

另（v2/D7 修订）：`event_outbox`（009）**保留产品侧**——框架已转 River 事务事件、无此表，orkome 本地 outboxx 依赖它；产品重编号时 009 的该部分 DDL 随迁（不删除）。

### 保留重编号（1001 起，保持原相对顺序）

> 起点由原定的 021 改为 1001：框架自身迁移 v2 实测已用至 **043**（schedule/quota 幂等等）且持续增长，001–999 段保留给框架；产品仓库迁移一律 1001 起（example 已确立 1001–1004 先例）。

005 work_core → 006 discussion → 007 orchestration（**剥离 inbox_item DDL 后保留**，见丢弃表）→ 008 conversation → 010 automation → 011 channels_support → 012 batch → 013 task_message_liveness → 014 agent_builder → 015 chat_parts → 016 usage_hourly → 017 automation_quota → 018 work_task_only_rename → 019 task_engine_tag_fire_at → 020 source_context_object_intent → 021 autopilot_subscriber → 022 conversation_seq → 023 channels → 024 observability_tail → 025 channel_route_title → 026 work_catalog_crud → 027 pins（含 pinned_item 表定义随此迁移保留）→ 028 agent_access → 029 conversation_cancel_pending → 030 automation_hardening → 031 channel_debounce_recovery → 032 chat_intro_kind → 033 workitem_fk_cascade → 034 crossdomain_delete_semantics → 036 task_summary → 037 external_runtime → 039 ui_tokens。

> 重编号执行方式：一次性脚本改文件名 + `cmd/migrate` 状态表按新编号清零（基线重建，无在途状态）。执行前用 schema 快照 diff 验证：新基线 `migrate up` 的最终 schema 与旧基线（减去丢弃表）一致。

---

## 8. 风险登记册

| # | 风险 | 概率 | 影响 | 缓解 |
|---|---|---|---|---|
| R1 | 会话语义切换（JWT→DB session）破坏前端/e2e/契约 | 高 | 高 | P3 独立里程碑；契约预期差异清单先行；前端最小改造 P3-7 保可用 |
| R2 | 迁移重编号漏表/错序，产品表 FK 悬空 | 中 | 高 | P3-2 兼容核对清单 + schema 快照 diff（§7） |
| R3 | wsx 帧协议差异导致实时功能断档 | 低 | 中 | 审计下调：双方帧协议同源（均已有 CtrlAuth/subscribe/capabilities），差异仅在 connWS 来源与 relayx 语义；仍按 P5 前后端原子切换执行 |
| R4 | 框架身份域接缝不够（PAT→agent 绑定、注册门槛配置） | 中 | 中 | P0-7 先核对配置面；产品包装层 §3.2 兜底 |
| R5 | 双仓库并行改动漂移（框架改一半产品开始消费） | 中 | 中 | 每阶段以框架 tag 为锚（D4）；CI 两仓联动 |
| R6 | member 角色语义差异（隐式继承 vs 显式集合）导致权限回归 | 中 | 高 | P3-6 逐路由比对表；authz 测试表驱动覆盖三档×权限点 |
| R7 | `ork_` 凭据/MCP 面 accidentally 变化，破坏外部 runtime | 低 | 高 | client-kit 三步接入列入 P3/P6 手工回归；MCP 面零改动为非目标约束 |
| R8 | ~~vendor×replace 组合坑~~（v3：无 replace，风险消除） | 低 | 低 | 线上 module + 常规 vendor 流 |

---

## 9. 附录

### 附录 A · 存量数据 cutover 备选路径（若 D1 被推翻）

一次性切换迁移（框架迁移之后、产品迁移之前执行）：

1. `user`/`workspace`/`member` 列映射插入框架表（id 保持，角色值域校验）；
2. `verification_code` → `auth_challenge`（kind=login_code）或直接作废（用户重新登录，会话必然全部失效——语义切换本身强制登出一次）;
3. `personal_access_token` 结构对齐（hash 算法一致则原样搬，prefix 补齐）；
4. `workspace_invitation`/`workspace_share_link` 直搬；
5. cutover 后 `ANALYZE` 全库 + 抽样账号手工验证。

### 附录 B · authz 权限点初始目录（草案，P3-6 定稿）

```
work:read  work:write  work:delete  work:move  work:catalog   （工作项/项目/目录）
comment:write  comment:read_own
orch:agent:read  orch:agent:write  orch:agent:credentials     （credentials 建议 admin 档）
orch:squad:read  orch:squad:write  orch:batch:write  orch:approve:decide  orch:usage:read
conv:read  conv:write
channel:read  channel:write  channel:connection:write
content:upload  content:avatar:write
skill:read  skill:write  skill:import
auto:wakeup:write  auto:autopilot:read  auto:autopilot:write
```

框架默认目录（workspace/members/invites/billing/webhooks/notifications/audit/usage 共 15 点）直接可用。runtime 凭据不受角色体系管辖（写围栏正交，§3.2）。

### 附录 C · 关键文件索引（orkome 侧）

- 组装：`cmd/orkome-server/main.go`、`wiring_{identity,ws,notification,automation,webhooks,channels,batch,content,skills,mcp,work,orchestration,conversation}.go`、`workers.go`、`guard_runtime.go`、`routes.go`
- 契约守门：`cmd/orkome-server/router_contract_test.go`、`docs/route-compare/`
- 架构守门：`internal/arch/`、`.golangci.yml`（depguard）
- 平台层：`internal/platform/`（Phase 2/5 分批删除）
- 前端底座：`web/src/lib/{api.ts,ws.ts}`、`web/src/realtime/client.ts`、`web/src/lib/ws-query-bridge.ts`

### 附录 D · 审计修订记录（2026-10-06）

对本文档做过一次双仓库事实核验 + 阶段逻辑审计，修订如下（核验通过的关键声明不再列举：模块名/Go 版本/chi 依赖、18 个 platform 包、JWT-HS256、`main.go` 三处行号引用、`routes.go:7` 死路由、附录 C 文件清单、框架 metrics 占位、pgmigrate 全套、`wsx/hub.go:270` 的 `ork_` 硬编码、`Mount(mux *http.ServeMux)` 签名、框架迁移 001–020、event_outbox 缺迁移、contract 三缺、client-kit 零 WS 使用、027 为 DROP+重建 pinned_item）：

| # | 严重度 | 问题 | 修订 |
|---|---|---|---|
| A1 | 高 | P2-5 原写法"框架 FS 先入队"：Phase 2 产品侧仍含 001–004（user/workspace/member），与框架 002–005 重复建表，全新 DB 上 `migrate up` 必炸 | P2-5 改为仅挂产品 FS，框架 FS 延后至 P3-1 基线重建时入队 |
| A2 | 高 | P2-1/P2-3 原写法 Phase 2 删 webx 并换框架 Authenticate：框架认证依赖会话表（P3-1 才建），提前一阶段切换认证即断 | webx 与 wsx/relayx 同待遇整包保留至 Phase 3；Phase 2 仅补插 RequestID/Timeout/BodyLimit |
| A3 | 中 | §7 原保留 035：其源表 `inbox_item` 由 007_orchestration 创建，035 将其改名为 `notification`，与框架 009 的 `notification` 表名冲突且改名目标悬空 | 035 整体作废入丢弃表；007 剥离 inbox_item DDL 后保留 |
| A4 | 中 | `apierr`/`events`/`wswire` 三包替换原无阶段归属 | apierr/events 归 Phase 2（新增 P2-8，含词表 diff 前置）；wswire 归 Phase 5 |
| A5 | 中 | 框架 `webx.Principal` 无 `AgentID` 字段，orkome 的 PAT→agent 绑定与 runtime 写围栏依赖它，Phase 3 交换 webx 会编译期断裂 | 新增 P0-8：先在框架加扩展位 |
| A6 | 低 | §3.3 原表述"帧协议不兼容"过重：orkome 现版已有 CtrlAuth（hub.go:245）/subscribe/capabilities/PATResolve | 改写为精确差异（connWS 来源 + relayx 语义）；R3 概率中→低 |
| A7 | 低 | D2/P0-4 原写"缺失头 422→400"：实测框架 `ErrValidation` 已返回 400，真实缺口仅非成员 404 vs 403 | D2/P0-4 收窄为仅改非成员分支 |
| A8 | 低 | `wiring_identity.go:43-46` 行号漂移（实际 47-48） | 已更正 |

### 附录 E · 二次审计修订（2026-10-06，ploykit 实施文档联动）

ploykit 框架侧实施文档（`D:\code\ploykit\docs\framework-impl-2026-10-06.md`，§6 勘误表）对框架当前 HEAD 逐条核实后回写的勘误，正文相应位置已同步修正：

| # | 位置 | 原文/原状 | 修订 |
|---|---|---|---|
| E1 | §2 事实澄清 | "框架 README 两处过时表述" | 实为多处：含钩子计数行（已接线数修正 + webhooks(2) 规划中）；由框架批次 A 一并修正。注：框架实施文档记"18 个已接线"，经对框架代码复核 billing 实为 5 个钩子（含 `BeforeCheckout`，`billing/app/hooks.go:14`、接线于 `billing/app/service.go:58`），总数 **19**（identity 4 / workspace 6 / billing 5 / quota 3 / admin 1），README 按实测值落笔 |
| E2 | P0-5 | "wsx PAT 前缀 `ork_` 硬编码（hub.go:270）" | 该行号/值为 **orkome 侧**；框架侧实况是 `tk_` 两处硬编码（`hub.go:297`、`identity/domain/rules.go:87`），工作项为框架 B6 |
| E3 | P0-6 | 清理 14 处死代码 / 6 处双轨 | 已于 2026-10-05 完成（code-audit §9），标记无需执行 |
| E4 | P0-7 | 框架补身份门槛配置面 | 框架 `identity/app/service_session.go:20-23` 已有，标记无需执行 |
| E5 | P0-4 | "加配置开关供其他消费者回退" | 按框架裁定 N1 删除开关，直接改 403 |
| E6 | §2 D1 / §7 | "框架迁移 001–020" | 已至 021（`021_invitation_pending_unique`），且持续增长 |
| E7 | §7 重编号 | "产品迁移重编号为 021 起" | 与框架 021 冲突；按 N3 改为 **1001 起**（example 已确立 1000+ 段） |
| E8 | §4 替换对照表 events 行 / P2-8 / P4-1 | "框架 events 已含 orkome 事件词表，核对无缺后删本地" | 按 N2 反转：框架 events 收窄为框架域词汇，orkome **保留**本地产品词汇，仅框架域事件 import 框架 |
| E9 | §3.3 | "框架从 auth 控制帧取工作区" | 框架 wsx 亦支持 `?workspace_id=` 直入（`platform/wsx/hub.go:206-207`），差异比原文更小 |
