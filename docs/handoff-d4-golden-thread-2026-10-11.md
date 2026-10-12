# D4 交接笔记：端到端金线——一条链穿起三份设计文档的所有交付物

> 日期：2026-10-11 · 工作流：D4（波 4 收官）· 任务书：`docs/pack-rollout-plan-2026-10-11.md` §4 D4
> 上游：D1+D3（`D:\code\agentblog\doc\pack-mcp-jiaojie.md` / `pack-hooks-jiaojie.md`，兄弟仓只读使用）、D2（`handoff-d2-install-bridge-2026-10-11.md`）、C1（`handoff-c1-registry-2026-10-11.md` + `interfaces-tool-registry-2026-10-11.md`）、B2（`handoff-b2-renderers-2026-10-11.md` 冻结表）
> 结论先行：**金线八步真跑全通（连续 3/3 PASS，约 47s/次）**——真 PG 真路由的 orkome stack + 真实 D1 fake full-server（MCP 六工具 + D3 webhook 接收端同进程），从 pack 安装到工单终态归档一条链闭环；门禁 `go build` / `go test ./...`（金线默认 skip）/ `golangci-lint` 全绿。

## 一、交付清单

| 文件 | 内容 |
|---|---|
| `e2e/golden_thread_test.go`（新） | `TestD4_GoldenThread`：env 门控（`ORKOME_AGENTBLOG_DIR` 未设默认 skip），八步全断言，一步一注释对应任务书金线定义；含 scripted 模型（两回合工具调用序列）与增量路由挂载 |
| `e2e/golden_thread_helpers_test.go`（新） | 辅助函数：agentblog server 启动/杀树、MCP 会话、HMAC 签名/直发探针、PAT 铸造、install 四写面、task_message 三卡形状判定（B2 语义对齐的解包装器） |
| `e2e/golden_test.go`（既有文件，4 处最小改动） | ①stack 增 `bus` 字段（newStack 局部变量回填）；②engine 增 `TaskMessages: orepo`（task_message 落库 sink）；③work 服务 `SetOutbox`（EventID 填充）；④TRUNCATE 清单补 `pack_tool_schema`（B1 迁移 1035 引入后清单漏更）——全部与生产 wiring 同构，既有测试零行为改动（全量回归绿） |
| `docs/handoff-d4-golden-thread-2026-10-11.md`（本笔记） | — |

## 二、八步实测结果表（2026-10-11 Windows + Git Bash，docker orkome-test-pg @ localhost:54329）

```
$ ORKOME_AGENTBLOG_DIR=D:/code/agentblog TEST_DATABASE_URL=postgres://postgres:orkome_e2e@localhost:54329/orkome?sslmode=disable \
  go test ./e2e/ -run TestD4_GoldenThread -count=1 -v -timeout 600s
--- PASS: TestD4_GoldenThread (47.14s)    # 连续 3/3 稳定
```

| 步 | 断言 | 结果 |
|---|---|---|
| ① 安装 | 真握手过三闸门（六工具前缀 / outputSchema 非空 object / 三件套齐全）+ schema 存档落库 6 条 source=protocol（job_status 存档 required 含 job_id/state）+ ui entry（内容工厂，{uiToken}）与 slots 落库 + webhook 订阅创建（url=agentblog /hooks/orkome） | ✅ 全过（Bearer PAT 走 stack 真鉴权链——webx.Authenticate PAT 分支 + memberMW） |
| ② 任务派发 | work item + agent + 任务入队（既有 goldens 模式），dispatcher 认领 | ✅ |
| ③ 引擎组装 | 任务真实执行时 `task.context.tools` 快照：agentblog 六条全在，`origin=mcp / server=agentblog / has_output_schema=true / concurrency_safe=false`（C1 数据面，经生产同构接线 MCPResolver+PackToolSchemas+TaskContextPatcher） | ✅ 六条全对 |
| ④ 员工回合 | scripted 模型依序调 evaluate_seed（tool_result 为结构化对象，verdict∈{go,marginal,avoid}，无 `TOOL_OUTPUT_CONTRACT_VIOLATION` 串——A1 放行）→ submit_generation（秒回，tool_result **恰 `{job_id}`**——JobHandleCard 严格形状） | ✅（MetricsCard 断言语义修正，见 §四-3） |
| ⑤ 管线真实跑 | D1 侧 job 经真实生成链（fakegen + 事件循环）推进到停进态：`awaiting_review`，result={post_id, summary}, metrics={publish_state:none} | ✅ 停进 awaiting_review（manual 站点最深可达态——D1 checklist ④） |
| ⑥ 收割回合 | 下一任务模型调 job_status：tool_result = job_id + state∈五态 + stage 非空（JobStatusCard 必填三件套）+ result 块有可渲染内容（awaiting_review=summary；published=url+summary）+ metrics 非空 | ✅ |
| ⑦ slot 数据面 | `GET /api/ui-entries` 响应含 agentblog：slot=`workitem.detail.tab`、label=文章预览、url 模板含 `{workItemId}` 与 `{uiToken}`、height=480 | ✅ |
| ⑧ 收尾归档 | work item 置 done → work_item.updated 事件（bus PhaseProject 扇出，EventID 经 outbox 填充）→ 进 delivery 队列 → `DeliverPending` 按框架 HMAC 签名 POST → agentblog 验签过 + 终态过滤过 → 归档；断言 delivery=`delivered`、payload=WorkItemPayload(status=done)、归档后 job_status 仍可查（归档只影响活跃集语义）；直发探针：新 event_id 终态事件→202、非终态→200、错签→401 | ✅ 全过（真投递路径 + HMAC 契约双向证明） |

门禁：

```
$ go build ./...                 # 0 错误
$ go test ./... -count=1         # 全绿（TestD4_GoldenThread 未设 ORKOME_AGENTBLOG_DIR → skip）
$ golangci-lint run              # 0 issues（internal/goarch export data typechecking error 为任务书已知环境噪音）
```

## 三、金线测试形态（与 D2 联调的差异）

D2 联调（`cmd/pack-provision/agentblog_e2e_test.go`）用 fake capture API 承接 install、直调 `buildMCPTools` 组装桥接——验证的是**安装器与引擎组装件**。D4 金线是**平台全栈**：

- **真库真路由 stack**：`newStack`（e2e 既有基建——真 PG 迁移、真 HTTP 路由、真 dispatcher 轮询认领、真 settle 结算）+ D4 补的生产同构接线（见 §四-2）；
- **install 走真 API 面**：Bearer PAT（真 `personal_access_token` 表 + webx 鉴权链）对 stack 的真路由落四个写面（MCP 注册/schema 存档/订阅/ui-entries）；三闸门判定与 provision `handshakeAndGate` 逐条对齐（不经 pack-provision 进程——避免 e2e 依赖 main 包与 CLI 形态，判定语义同源）；
- **引擎真跑**：任务由 dispatcher 认领 → `EinoEngine.Execute` → `ResolveForEngine` 经 `workspace_mcp_server` 表解析 → 桥接组装 → script 模型调真 MCP 工具 → tool_use/tool_result 落 `task_message` 表；
- **scripted 模型**：`modelmeta.Register("fake")` 覆写（engine 测试既有 fake 模式的 e2e 等价物）。回合一 evaluate→submit→收尾（模型从历史 Tool 消息解 CallToolResult 包装取 job_id——与 B2 前端解包同语义）；回合二 job_status（测试侧 `armHarvest(jobID)` 注入真实 job_id）→收尾。

## 四、修了什么（orkome 侧改动全记录）

### 1. 测试新文件（主体交付）
两个新测试文件，见 §一。无生产代码改动。

### 2. `e2e/golden_test.go` 的 4 处最小接线（既有文件，全量回归绿）

| 改动 | 根因 | 生产对应 |
|---|---|---|
| stack 增 `bus` 字段并回填 | newStack 的 eventbus 是局部变量，金线的 webhook 扇出 handler 需要 `On(PhaseProject)` 追加 | —（测试基建缺口，非生产缺陷） |
| engine 增 `TaskMessages: orepo` | stack 从未接 task_message sink，engine 的 tool_use/tool_result/text 全部不落库——金线三卡形状断言（B2 数据面）读不到 | `wiring_orchestration.go:88 TaskMessages: repo` |
| `workDeps.WorkItems/Comments.SetOutbox(rec, db.Within)` | **真缺口**：`emitCommittedEnvs` 只在 outbox 分支给 Envelope 填 EventID；未接 outbox 时 work_item.updated 事件 EventID 为空，webhook 扇出 handler 按 `EventID==""` 丢弃 → delivery 永不生成。生产 wiring_work.go 有此接线，stack 漏了 | `wiring_work.go:90-91` |
| TRUNCATE 清单补 `pack_tool_schema` | B1 迁移 1035 引入该表后 newStack 的清库清单未同步（跨测试脏数据风险，顺带修正） | — |

> 判定：这 4 处均为 **stack 测试基建与生产 wiring 的偏差**（生产代码本身正确），不是产品缺陷。D4 按"接线方式与生产同构"逐项补上，既有 e2e 全量回归绿（行为不变）。

### 3. 任务书断言语义的两处对齐（非代码缺陷，记录裁决依据）

- **"evaluate_seed 触发 MetricsCard"**：D1 契约的 `evaluate_seed` 输出是评估对象（seed/verdict/volume/difficulty/competitors），**无 metrics 键**——B2 冻结表 #6 的 MetricsCard 判定（`metrics` 非空普通对象）对它不成立。metrics 载体是 `job_status`/`review_result`（质检指标）。金线断言改为：evaluate_seed 断言结构化 + verdict 枚举 + A1 放行；**MetricsCard 形状锚放 ⑥ 的 job_status**（其 metrics 非空实测过）。任务书该句应理解为"评估结果以卡形式可见"——按 B2 实际语义对齐。
- **"result.url 存在"**：D1 停进态语义——`awaiting_review` 的 result={post_id, summary}（产物停在人审门，**url 要 published 部署完成后才有**，`packports.go:317-332`）；B2 冻结表 #4 也把 result/url 当可选块。断言改为产物态 result 块有可渲染内容（summary 或 url），published 态 url 才点亮。**金线 fake 形态最深可达 awaiting_review——url 可点是需真部署验证的演示项**（见 §六）。

### 4. agentblog 侧发现的问题（只读仓，最小修复建议）

- **【bug·中文种子词生成失败】** `fakegen` 伪模型对非 ASCII 种子词产出损坏的 JSON（outline 节点 title 首字节变 `\x87` 乱码 → "output is not valid JSON after repair" → job failed，实测中文种子词 100% failed / ASCII 种子词正常到 awaiting_review）。复现：`AGENTBLOG_MCPACK_TOKEN=t go run ./cmd/full-server` 后 submit `{"seed":"中文词","workspace":"w"}`。怀疑点：fakegen 按 byte 截断/取首字母逻辑（`cmd/full-server/adapters.go` 的 fake LLM 或 `agent/tools/llm.go` 的 repair 路径）对多字节 UTF-8 不安全。**最小修复建议：fakegen 的 title 生成对 rune 而非 byte 操作**。金线已规避（种子词用 ASCII），中文种子的真实生成需真 LLM key 验证（见 §六）。
- 【噪音] `preview secret 未配置` 等 fake 形态启动警告——已知现状，非问题。

## 五、关键实现细节（给复现/排障）

1. **env 门控**：`ORKOME_AGENTBLOG_DIR`（agentblog 仓根）+ `TEST_DATABASE_URL`（docker PG）。前者未设 → skip（默认 `go test ./...` 不依赖兄弟仓）；`make test-e2e` 的 docker 自起逻辑复用。
2. **DisableKeepAlives**：沿 D2 坑 1 的规避（vendored mcp-go v0.43.0 连接池竞态，测试内 `http.DefaultTransport.DisableKeepAlives=true`，t.Cleanup 还原）。
3. **订阅 secret 对齐**：`CreateSubscription` 随机铸造 secret 并一次性回显（生产运维把它配到 agentblog 的 `AGENTBLOG_HOOK_SECRET`——D3 文档 §一）。金线 agentblog server 先于订阅启动（MCP 握手依赖），env 已固定 secret → 把库里的 sealed secret 重写为 `Seal(hookSecret)` 等价完成这步运维动作——unseal→Sign→验签全程真实，不绕 HMAC。
4. **投递驱动**：生产是 DeliveryWorker 5s 轮询；测试直接 `whSvc.DeliverPending(ctx, 20)`（同一 `deliverOne` 路径：loopback 放行的 egressx client + sealx unseal + `Sign(secret, now, payload)` + 五 header）。
5. **三卡形状判定**：`goldenUnwrapToolResult` 按 B2 冻结表 #2/#3 解 eino-ext 的 CallToolResult 包装串（structuredContent 优先 → content[0].text 再 parse）；tool_use/tool_result 按 call_id 配对（前端 `toolNameByCallId` 同语义）。
6. **增量路由挂载**：stack 的 api mux 在 newStack 内封口，金线把 ui-entries / pack-tool-schemas / ws 级 mcp-servers / webhook 订阅四个面挂独立 mux 并前缀拦截并入 handler 链（与生产同一条 authMw/CSRF/member 链）。注意 **ServeMux 的 `r.PathValue` 只在 `ServeHTTP` 分发时填充**——手动 `api.Handler(r)` 取 handler 分发会拿到空 PathValue（实测踩过）。
7. **loopback egress**：引擎 MCP 桥与 webhook 投递 client 均 `egressx` 放行 `127.0.0.0/8`（生产对应 `ENGINE_EGRESS_EXTRA_ALLOW_CIDRS` / `WEBHOOK_ALLOW_PRIVATE_TARGET`）。

## 六、给"人看一眼"的演示清单（金线外需真人验证项）

| 项 | 怎么验 | 金线覆盖到哪 |
|---|---|---|
| **slot tab 真渲染** | 真浏览器开 work-item 详情页 → "文章预览" tab 出现 PackSlotFrame iframe（ui-token 铸票、postMessage resize、sandbox 三权限） | 只到 API 数据面（⑦）；C2 前端有组件测试，iframe 真渲染 + frame-ancestors 探测未覆盖（D2 坑同源） |
| **三卡真渲染** | 前端任务流看 MetricsCard/JobHandleCard/JobStatusCard 实际渲染 + job_id 复制 + 深链按钮 | 只到 tool_result 数据面形状（④⑥，与 B2 冻结判定逐条对齐）；B2 有组件测试 |
| **真模型中文任务** | 配真 LLM key，给 agent 下中文任务（评估→提交→收割） | 模型是 scripted；且 agentblog fakegen 有中文种子词 bug（§四-4）——真 LLM 下中文生成是否正常待验 |
| **JobStatusCard url 可点** | agentblog 配 auto 站点 + 质量闸门 → job 到 published → url 点开是真实文章 | fake 形态最深 awaiting_review（result 有 summary 无 url）；published 需真部署形态 |
| **webhook 控制台投递页** | orkome console 看 deliveries 页 delivered 记录 / Redeliver 重放 | 投递走的是真 `deliverOne`（同一 service），但控制台 UI 未看 |
| **人工审核闭环** | agentblog console 审核通过 awaiting_review 产物 → published | D1 坑 1：停进后快照不实时，需重新 submit 或 review_result 观察 |

## 七、遗留坑（诚实清单）

1. **agentblog fakegen 中文种子词 bug**（§四-4）：只读仓未修，金线以 ASCII 种子词规避。建议 D1 侧一行修复（rune 化截断）。
2. **金线的 install 是"判定同源"而非"进程同源"**：三闸门/schema 存档 PUT/订阅/ui-entries 四写面在测试内按 provision 语义逐条实现（Bearer PAT + 真路由），**没有起 pack-provision 进程**——避免 e2e 依赖 main 包。若 provision 行为漂移，金线的同构锚会滞后（D2 的 `assertPackYAMLShapeMatchesRepo` 模式可借鉴补锚）。
3. **scripted 模型覆写是进程级全局**：`modelmeta.Register("fake")` 后写胜出，同包并行测试若同时跑会互相干扰——e2e 包测试串行（go test 默认同包串行），当前无碍；若将来 e2e 开 `-parallel` 跨测试共用 fake provider 需注意。
4. **stack 的 4 处接线是"补齐到生产形状"而非新行为**：若后续生产 wiring 变（如 TaskMessages 换 sink），stack 不会自动跟随——已在 golden_test.go 注释标注对应生产行号。
5. **金线不覆盖 D2 联调的 provision 握手细节**（url 模板替换、credentialRef 解析等）：那是 D2 测试的职责，两测试互补。
6. **webhook 投递的重试/死信路径**未在金线覆盖（agentblog 侧 202/200 直接成功）；D3 的回放测试（14 例）已覆盖重试语义格式——金线只证"正常链路一轮投递成功"。
7. **agentblog JobStore 内存态**：金线每次起新 server 无影响；若复用长驻 server 跨测试，job_id 会失联（D1 坑 3 同源）。

## 八、复现命令

```bash
# 金线（本机有兄弟仓 + docker PG）
docker start orkome-test-pg   # 或 make test-e2e 的自起逻辑
ORKOME_AGENTBLOG_DIR=D:/code/agentblog \
TEST_DATABASE_URL=postgres://postgres:orkome_e2e@localhost:54329/orkome?sslmode=disable \
go test ./e2e/ -run TestD4_GoldenThread -count=1 -v -timeout 600s

# 默认门禁（不依赖兄弟仓；金线 skip）
go build ./... && go test ./... -count=1 && golangci-lint run
```

## 九、边界确认

- 新增文件：`e2e/golden_thread_test.go`、`e2e/golden_thread_helpers_test.go`、本笔记。
- 既有文件改动：仅 `e2e/golden_test.go`（4 处最小接线，§四-2 逐条记录；全量回归绿）。
- agentblog 仓只读（只 `go run` 它的 server）；未修其代码，bug 记最小修复建议（§四-4）。
- 未做任何 git add/commit。
