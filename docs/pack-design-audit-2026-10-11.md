# 设计文档审计报告（pack-tool-contract / pack-frontend / dsh-borrowing-v2）

> 日期：2026-10-11
> 审计对象：三份 2026-10-11 设计文档，对照 D:\code\orkome 当前代码（含 vendor）逐条四态判定（成立/部分成立/不成立/无法静态判定）
> 结论先行：**主体判断与代码吻合度高，四缺口判定与 P0 落点全部准确；但抓出 8 处事实性错误（含 2 处高危），已列"必修清单"——本报告是开工前的修文档依据，修正后三份文档可作为实施基线。**

---

## 一、必修清单（8 处，开工前改文档）

### 高危（不修则对应工作流必然失败/做错）

| # | 错误 | 证据 | 修正 | 波及 |
|---|---|---|---|---|
| 1 | **`work_item.completed` 事件不存在**。契约文档 §4.1/§4.2 与排期 D3 都让 agentblog 订阅它——事件目录里没有（`internal/contract/events/events.go:61-73` 只有 created/updated/deleted），webhook catalog 白名单（`cmd/orkome-server/wiring_webhooks.go:95-141`）也没有，未注册类型根本到不了 delivery | events.go:61-73；wiring_webhooks.go:53-55 | 二选一：①改订阅 `work_item.updated`（payload 含 `WorkItemPayload.status`，agentblog 侧过滤终态）；②立项新增 `work_item.completed` 并登记进 `orkomeEventCatalog()`。**推荐 ①**（零后端改动） | 契约文档 §4.1/§4.2、排期 D3 |
| 2 | **"现有 ws 级 MCP 注册表 UI 平移"不存在**。前端文档 §3.1 中段说"现有能力平移：列表/新增/删除/凭证"——后端端点在（`routes.go:162-165`），但前端 `api/` **零调用**（全仓 grep 无命中）；现存的注册表 UI 只有 agent 级（agent 详情页 MCP tab，`_app.agents.$agentId.tsx:766+`） | routes.go:162-165；web/src 零命中 | 措辞改为"agent 级 UI 已有（留在原地）；ws 级列表/新增/删除为**新写**（复用 `orchestration.ts:140-179` 的函数签名模式）"——F4 规模相应上调 | 前端文档 §3、排期 A3 |

### 中危（不修则实现者踩坑/做多余功）

| # | 错误 | 证据 | 修正 |
|---|---|---|---|
| 3 | **"运行期前缀软丢"不存在**（契约文档 §1 与借鉴表 §1.1 同款措辞）。运行期只有**同名去重**（`tools_mcp.go:160-163` `duplicate_name_dropped`），与前缀无关 | tools_mcp.go:94,160-163 | 删除该措辞或降级为"待实现项"；两份文档同修 |
| 4 | **ConcurrencySafe 优先级判断的前提错误**。借鉴表写"现在 engine 串行 dispatch 用不上，并行组启用前白拿"——实际上 eino ToolsNode **默认并行**执行同消息的多个 tool_calls（`compose/tool_node.go:276` executeSequentially 零值 false + :1076-1079 分流 parallelRun；engine.go:221 未设 true），**并行已经开着，护栏已经缺着** | tool_node.go:276,1076-1079 | ConcurrencySafe 从 P3"预留"上调为 P1/P2 实装——至少给有副作用的内置工具和 MCP 工具 fail-closed |
| 5 | **autopilot"直调 Dify"能力不存在**。契约文档 §5 说纯机械触发"由 autopilot 直调（automation 域）"——automation 两种 exec_mode 最终都派 agent 任务（`service_autopilot.go:413-427`），域内无出站直调执行器 | service_autopilot.go:413-427 | 改为"待建能力（autopilot 出站动作）"或 v0 用 cron 触发 Dify 自己的 webhook；当前表述像既有路径 |
| 6 | **健康聚合数据源不存在**。前端文档 §3 说"从 task.context.tools 快照聚合，不新增探活"——全后端无人向 task.context 写 tools 快照（引擎工具清单 `engine.go:221-252` 本地装配不落盘），也无聚合端点 | grep `context["tools"]` 零命中 | 二选一：①v0 降级为只显示 pack 名/label/installed_at（`pack_ui_entry` 现有列）；②把快照落库（并入 C1 的 task.context.tools 扩展）列为 F4 前端依赖。同页的版本号/工具数/订阅数同样不可得（表无此列）——一并处理 |
| 7 | **pack.yaml 示例格式与现解析器不匹配**。契约文档 §4.1 示例用 `pack/v1` + `ui.entries` 数组 + `mcp.tools` 段——现 provision 只收 `pack/v0`（`main.go:127-129`）、`ui.entry` 单数指针（:78-83）、无 tools 字段（非 strict yaml 静默忽略）；且 `pack_ui_entry` 表 `UNIQUE(workspace_id, pack)` 一 pack 一行，entries 数组是 schema 重构不是增补 | main.go:66-96,127-129；migrations/1033:12-20 | 示例标注"v1 目标格式"；B1/C2 任务书明确两个决策：版本双放行、entry→entries 迁移 vs 只追加 slots（**推荐最小路径：保留 entry 单数，仅追加 ui.slots**） |
| 8 | **pack credential 与 webhook 验签链路未打通**。契约文档 §4.3 的 `AGENTBLOG_CALLBACK_TOKEN` 想当回调验签密钥——provision 安装 events 只传 url+types（`main.go:382-391`），框架订阅用自己的 signing secret（sealx 封存，`wiring_webhooks.go:41-47`） | main.go:382-391 | 明确交付路径：回调验签走框架 subscription signing secret（rotate-secret API 交付给 agentblog），pack credential 只用于 agentblog 验证平台的 MCP 调用——两个方向的凭证分开写清 |

### 低危（表述偏差，顺手修）

- 契约文档 §3 的 chi 风格 middleware 示意与排期 §8 修正（adk 原生 Wrap）矛盾——统一为 adk 原生协议（`adk/handler.go:193` + `compose.ToolCallMiddlewares`）。
- 借鉴表 P1 七段提示词成本"极低（复制粘贴）"→ 改"低（含 summary 生成从 repo 层上移 service 层的一次分层）"（`repo.go:377,403` 现状）。
- 借鉴表 §1.2 "组装材料在 task.context 有记录"表述偏宽——history/session 转写/squadBrief 是组装时拉取不在 context（`engine.go:178-209`），"部分材料在 context"才准确。

---

## 二、好消息（审计中发现的降本机会）

1. **MCP 协议层 outputSchema 已经到手被丢**：mcp-go `ListTools` 自带 `outputSchema` 与 `structuredContent`（`mcp-go/mcp/tools.go:569`、`:46`），eino-ext 包装只取 Name/Desc/**Input**Schema 就丢了（`eino-ext/components/tool/mcp/mcp.go:94-101`）——A1/B1 的输出校验可直接回收该字段，比"结构化 JSON 探测"回落方案更准、成本更低。
2. **realtime 层零新增依赖**：`task:*` WS 帧 → `main.tsx:16` 失效规则 → React Query refetch 已闭环，F1 三卡不需要任何推送改造。
3. **一处升级两页受益**：`TaskMessageList` 有两个消费面（work-item 面板 `task-panel.tsx:191` + run-logs 抽屉 `_app.run-logs.index.tsx:301`），注册表做进 `TaskMessageRow` 即全覆盖。
4. **新消息 kind 比预想轻**：engine 侧 eventPipe 直写 repo，不经 `TaskMessageKinds` 白名单（那只 gate external API，`service_external.go:68-75`）——但注意 DB 有 `CHECK (kind IN 5 值)`（`migrations/1009:6`），新 kind 需要迁移放行。
5. **agent 详情页是第二 slot 宿主**（同款 URL 驱动 tabs 架构），将来扩展模式同构。

---

## 三、开工注意（实现者须知，不改文档结构）

1. **slot tab 接线**：work-item 详情页 tab 值走 URL search + zod 枚举（`_app.work-items.$id.tsx:18-27`），动态 `pack:*` 值需同步扩 zod schema/类型/patchSearch/条件渲染四个触点——比伪代码多一个 schema 触点。
2. **工具名解析规则**：TaskMessage 无工具名字段；tool_use 的 content 首 token 是工具名（`engine.go:334` emit `name+" "+args`），tool_result 靠 call_id 回溯配对。三个内置卡按 result 形状触发可绕开此依赖。
3. **截断兜底**：content 超 2000 runes 被截（`eventpipe.go:113-116`），渲染器必须 `truncated || JSON.parse 失败 → 默认灰框`，否则大 metrics 卡直接崩。
4. **ui-token 60 秒过期**：PackSlotFrame 每次挂载现铸现用，勿缓存。
5. **B2 形状判定面对的是 CallToolResult 包装**：eino-ext 把 content 数组+structuredContent+isError 整体序列化为字符串（`eino-ext mcp.go:155-160`），前端按包装层写形状匹配，先真跑抓样例。
6. **两处快照语义别混**：dispatcher 写的 `mcp_servers`（resolved 态）与 engine `br.snapshot`（ok/ssrf 细化态，当前死数据无消费者，`tools_mcp.go:100`）——C1 扩展时理顺，别把死数据直接搬进新键。
7. **OutputSchema 存注册表侧**：eino `schema.ToolInfo` 无输出 schema 字段，OutputSchema 不能进模型可见 ToolInfo——存 orkome 自己的注册表。
8. **i18n**：Packs 页改名单动 `nav.mcp`（zh+en），新增 `packs.*` 命名空间，三卡文案落 `taskMsg.*`（zh.ts:651-657 已有空位）。
9. **job-handle 过渡期**：引擎槽超时 30 分钟（`dispatcher.go:87-88`），B1 落地前 pack 若提供分钟级同步工具仍会占槽——D1 第一天就按 job-handle 形状实现（排期已如此要求）。

---

## 四、审计方法与边界

- 方法：两路并行审计（后端契约+借鉴表 / 前端面），每条主张四态判定 + file:line 证据；文档与代码矛盾时以代码为准。
- 判定统计：契约文档 15 项主张——11 成立 / 3 部分成立 / 1 不成立（事件）+1 不成立（autopilot）；前端文档 18 项——10 成立 / 5 部分成立 / 2 不成立（均 §3 管理面）+1 无法静态判定；借鉴表 17 项——12 成立 / 4 部分成立 / 1 不成立（并行前提）。
- 边界：pack 侧（agentblog 控制台验票行为）、运行时行为（checkpoint 对新 kind 兼容、frame-ancestors 实测）不在本仓可证范围，已在各工作流任务书标注开工自证。
