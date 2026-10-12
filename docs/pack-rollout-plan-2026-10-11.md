# Pack 工具契约 + 前端面 + dsh 纪律 —— 多 Agent 排期实施方案

> 日期：2026-10-11
> 输入：`pack-tool-contract-design-2026-10-11.md`（后端契约）、`pack-frontend-design-2026-10-11.md`（前端面）、`dsh-borrowing-table-v2-2026-10-11.md`（dsh 纪律 P0-P3）
> 形态：单人多会话（每个工作流一个独立 agent 会话），按依赖分四波；每波内可并行，波间有硬依赖。
> 迁移号段现状：产品段已到 1034；本方案占用 **1035-1037**。消息 kind 现为 5 种（text/tool_use/tool_result/error/approval，`service_external.go:68`）。

---

## 0. 总览：波次与依赖图

```
波 1（地基，三线并行，无相互依赖）
  A1 输出契约强校验（后端核心）
  A2 dsh 七段 summary 提示词（独立，纯增量）
  A3 前端 F4：MCP 页改"Packs 与集成"三段式（只读，零后端依赖）

波 2（依赖 A1 的形状判定；两线并行）
  B1 job-handle 三件套契约（pack-provision 校验 + 桥接层配合）
  B2 前端 F1：三个通用渲染器（形状判定依赖 A1 的 schema 判定语义）
      ＋ F5 深链 {jobId} 变量

波 3（依赖 A1+B1 的注册表接口定型；两线并行）
  C1 统一 Tool 注册表 + middleware 管线迁移（engine.go 221-252 段抽出）
  C2 slots 通道（F2 后端 + F3 前端 PackSlotFrame + work-item Tabs 接线）

波 4（首个 pack 上板，串行收官）
  D1 agentblog MCP server（六工具，遵守 A1/B1 契约）→ D2 pack-provision 安装 + 联调
  → D3 事件订阅 + 归档联动 → D4 端到端金线：评论触发→员工评估→submit→job 卡片→slot 预览

随手项（不占波次，随手 agent 或自己做）：副作用安全文案（P2）、prompt 落日志可重建（P1）、
ConcurrencySafe ★已升 P1 并入 C1（见波 3）——三者互不依赖，见 §6。
```

时长口径：一个"工作流"= 一个 agent 会话一次连续执行（数小时级，含测试），不是人日。串行链最长为 A1→B1→C1→D2，是全案关键路径。

---

## 1. 波 1：地基（三线并行）

### A1 输出契约强校验【关键路径起点】

**给 agent 的任务书**（自包含要点）：

- 目标：MCP 工具的输出从"插件自觉"变为"平台强制"，非法输出不进 task_message。
- 落点：
  1. `internal/orchestration/adapters/engine/tools_mcp.go`：MCP 桥接工具执行后按 schema 校验；无 schema 的工具——v0 桥接层用**结构化 JSON 探测**（能 `json.Unmarshal` 到 map/struct 即放行，纯文本/畸形 JSON → 判 `TOOL_OUTPUT_CONTRACT_VIOLATION`），因为 pack 侧 schema 声明要到 B1 才进 provision——A1 先落"运行期校验框架 + 违规路径"，B1 补"安装期声明闸门"。
  2. 违规路径：错误作为 tool_result（kind 不变）落 task_message，但 content 是平台生成的标准错误（含违规原因 + 原文前 500 字符），模型看到的是明确的失败而非幻觉结构。
  3. 校验器接口设计成可插拔（`OutputValidator`），B1 的 schema 校验是其实现之一。
- 验收（agent 会话内自证）：单测覆盖合法/非法/纯文本三类；违规结果的标准错误格式有 golden 测试；`make test` + `make lint` 全绿。
- 边界：不动 registry.go 的 toolsearch 逻辑；不动 engine.go 装配段（那是 C1）。

### A2 dsh 七段 summary 提示词搬运

- 目标：跨任务 session summary 从"500 字截断"升级为"LLM 七段 checkpoint"。dsh 原文在本地 clone `C:\Users\bzlih\.zcode\workspace\default\deepseek-harness\packages\compaction\compaction-basic\src\summarizer.ts`（七段结构 + Rules + CHECKPOINT_PREAMBLE，MIT）。
- 落点：`internal/orchestration/` 新增 summarizer（prompt 搬运 + 调用走既有 `modelmeta` 工厂）；结算路径可选启用（配置开关，默认关——先上代码不改变为，观察后再切）。注意 **v0 不做暖前缀缓存复用**（orkome 的 summary 调用是独立小调用，没有 dsh 那种"复刻上次请求"的请求管线，抄了也不生效——这是借鉴表 P1 里"配套结构一起搬"判断的反例应用）。
- 验收：prompt 文本有 golden 测试（防手滑改坏七段）；开关关闭时行为与现状 byte-identical；换模型跑一次真调用冒烟。

### A3 前端 F4：Packs 与集成页

- 目标：`_app.mcp.tsx` 改三段式（Packs 只读列表 / ws 级 MCP 注册表 / 客户端接入）。★审计修正两点：①**ws 级注册表 UI 是新写**（后端端点在 routes.go:162-165 但前端零调用；agent 级 UI 已有、留在 agent 详情页不动）——复用 `orchestration.ts:140-179` 函数签名模式；②**健康列 v0 不做**（数据源 task.context.tools 快照不存在）——Packs 列表降级为 pack 名 + label + installed_at（`pack_ui_entry` 现有列）。i18n：动 `nav.mcp`（zh+en）+ 新增 `packs.*` 命名空间。
- 验收：三个 workspace 状态（零 pack/一个 pack/多 pack）截图；ws 级注册表增删有 e2e；类型检查过；i18n 键补齐（中英）。

---

## 2. 波 2：契约与可见性（依赖 A1）

### B1 job-handle 三件套安装期闸门

- 目标：`pack.yaml` 的工具契约声明进 provision 解析（`cmd/pack-provision/main.go`，现 538 行）；★**优先回收 MCP 协议层自描述**——mcp-go `ListTools` 自带 `outputSchema`（被 eino-ext 丢弃），安装期闸门改为"工具要么协议自描述、要么 pack.yaml 声明，两者皆缺 = fail the install"；★版本双放行 `pack/v0` 与 `pack/v1`（现只收 v0，main.go:127-129）；★ui 段最小路径：保留 `entry` 单数、仅追加 `slots`（entries 数组化不做）；job-handle 形状（submit/status/cancel 命名约定）三件齐全校验，缺 = fail the install。schema 存档（落新表或 JSON 列，**迁移 1035**）。
- 同时把 A1 的 `OutputValidator` 补上"协议自描述 schema 优先，未声明回落结构化探测"两级。
- 验收：provision 测试夹具（合法 pack / 缺 status 的 job pack / 无前缀 pack / v0 格式旧 pack）四态覆盖。

### B2 前端 F1+F5：通用渲染器

- 目标：`task-messages.tsx` 升级为渲染器注册表 + 三卡片（JobHandleCard/JobStatusCard/MetricsCard）；形状判定语义与 A1/B1 对齐（JobStatus 的 schema 字段名以 B1 存档的为准——**这个 agent 开工前需要 A1、B1 的接口文档先冻结**，见 §5 协作纪律）。深链模板 `{jobId}` 变量支持。
- 验收：Storybook 式快照（合法 job 结果/失败 post_publish/metrics/未注册工具灰框兜底四态）；灰框路径保留现状样式不回归。

---

## 3. 波 3：注册表与 slots（依赖 A1+B1）

### C1 统一 Tool 注册表 + middleware 管线

- 目标：engine.go:221-260 装配段抽出为注册表 + ★middleware 走 eino 原生协议（实现 `ChatModelAgentMiddleware.WrapInvokableToolCall`，`adk/handler.go:193`，与 steerMiddleware/toolsearch 同构——不自研切片，见 §8 修正 2）；内置工具逐个实现统一 `Tool` 接口（含 `OutputSchema()`——内置工具的输出从此也受 A1 校验，**A1 的红利闭环**；OutputSchema 存注册表侧不进 ToolInfo）；★`ConcurrencySafe()` 默认 false **同步实装判定**（审计发现 eino ToolsNode 默认并行已开着，tool_node.go:276/1076-1079——有副作用工具未声明即不并行，这不是预留是补护栏，自 P3 升入本工作流）；`task.context.mcp_servers` 扩展为 `task.context.tools`（兼容读旧键；★理顺 dispatcher resolved 态快照与 engine br.snapshot 死数据两处语义，勿直接搬死数据）。
- 风险提示（给 agent）：这段是 engine 热路径，迁移必须**行为不变优先**——每个内置工具先加接口实现、装配段整体搬移，最后才动 handler 链；全程跑既有 engine 测试 + smoke。
- 验收：既有 engine/adk 测试全绿；新增"输出校验 middleware 对内置工具生效"的单测；快照字段双写期间新旧读路径都有测试。

### C2 slots 通道（F2+F3）

- 后端半：provision 解析 `ui.slots`（迁移 1036，`pack_ui_entry` 扩列或新表）；`GET /api/ui-entries` 响应带 slots；frame-ancestors 探测函数就位（第一方豁免开关）。
- 前端半：`PackSlotFrame`（ui-token 铸票复用、postMessage resize 3s 兜底、失败呈现深链；★票 60 秒过期，每次挂载现铸现用勿缓存）；`_app.work-items.$id.tsx` Tabs 动态化（★含 zod search schema 扩展四触点：schema/DetailTab 类型/patchSearch/条件渲染，见前端文档 §1.3 修订）。
- 验收：本地起假 pack（静态页带 frame-ancestors 头）走通 slot 渲染 + resize；无 slots 的 pack 详情页 Tabs 与现状一致。

---

## 4. 波 4：agentblog-pack 上板（串行收官，关键路径末段）

依赖顺序固定，每步验收通过才进下一步：

1. **D1 agentblog MCP server**（六工具按 A1/B1 契约实现；`submit_generation` 的 `stage_failed` 按发布动作前后如实标记；同 seed+workspace 幂等去重）。验收：对 contract 夹具跑全工具 golden；post_publish 失败拒绝自动重试有测试。
2. **D2 pack-provision 安装 + 桥接联调**：真实握手过前缀/output/job 三闸门；引擎侧组装快照含 agentblog 工具。
3. **D3 事件 + 归档**（★审计修正：`work_item.completed` 事件不存在——events.go:61-73 与 webhook catalog 均无）：改订 **`work_item.updated`**，agentblog 侧按 `payload.WorkItemPayload.status` 过滤终态后归档。验收：HMAC 验签、重试、死信路径用回放测试；终态过滤逻辑有单测。
4. **D4 端到端金线**（最终验收，人也要看一眼）：评论 @内容员工 → 员工 `evaluate_seed`（渲染器正确呈现 metrics 卡）→ `submit_generation`（JobHandleCard 出现，job_id 可复制）→ agentblog 管线真实跑 → 下轮任务 `job_status`（JobStatusCard 呈现产物 URL 可点）→ work-item 详情页"文章预览"slot tab 显示真实预览 → 工单完结触发 agentblog 归档。**一条链穿三份文档的所有交付物**。

之后（不排入本方案）：dify-pack 迁移（v1.1）、审批管线门（v2）。

---

## 5. 多 agent 协作纪律（为什么这么切波）

1. **每会话一个工作流，任务书自包含**：agent 会话无跨会话记忆，上面的"任务书要点"必须是它能独立完成的全部——包括验收命令和边界（"不要动 X"）。
2. **接口冻结先于并行**：B2 依赖 A1/B1 的"形状判定语义"，C1 依赖 A1 的 `OutputValidator` 接口——所以波 1 的 A1 任务书里要求**先写接口 + 接口文档再写实现**，波 2/3 的 agent 拿冻结的接口开工，不读对方实现。
3. **共享文件是并行杀手，已主动拆开**：engine.go 只有 C1 碰（A1 明令不碰装配段）；task-messages.tsx 只有 B2 碰；provision main.go 只有 B1 碰；迁移号分配死（1035=B1、1036=C2、1037 留给波 4 若需）避免合并冲突。
4. **关键路径保护**：A1→B1→C1→D2 是串行链，A2/A3/随手项全是填充物——如果只有零散时间，优先推关键路径，填充项随时可做。
5. **每个 agent 会话的收尾动作固定**：`make test && make lint` 全绿 + 一段"改了什么/为何这么改/留了什么坑"的交接笔记（落 `docs/` 或 PR 描述）——下一个会话靠它热启动，人靠它验收。

---

## 6. 随手项清单（不占波次）

| 项 | 落点 | 规模 |
|---|---|---|
| 副作用安全文案（P2） | engine 结算路径：存在未决 job/工具时 summary 附"仅幂等才重试"提示 | 半会话 |
| 请求可重建（P1） | engine 每次模型调用前把最终 user prompt 全文 append 进 task_message（新 kind `prompt`；engine 侧 eventPipe 直写不经 external 白名单，但 **DB 有 `CHECK (kind IN 5 值)` 约束（migrations/1009:6）需迁移放行**，顺带扩前端灰框映射） | 半会话 |
| ConcurrencySafe | ★已升 P1 并入 C1（eino 默认并行已开着，非预留项），见波 3 | — |

---

## 7. 一页执行摘要

| 波 | 工作流 | 依赖 | 关键交付 |
|---|---|---|---|
| 1 | A1 输出契约 / A2 七段提示词 / A3 Packs 页 | 无 | 校验框架+违规路径 / summarizer（开关默认关）/ 前端三段式 |
| 2 | B1 job 闸门+迁移1035 / B2 渲染器三卡 | A1 | provision 三闸门 / JobHandle·JobStatus·Metrics 卡 |
| 3 | C1 注册表+管线 / C2 slots+迁移1036 | A1+B1 | engine 装配段迁移 / PackSlotFrame+详情页 slot tab |
| 4 | D1→D2→D3→D4 agentblog-pack 上板 | 全部 | 六工具 pack / 端到端金线（四文档总验收） |

**总纪律：波内并行、波间串行；接口先行冻结；共享文件唯一归属；关键路径 A1→B1→C1→D2 优先。**

---

## 8. 实施级审计结论（2026-10-11 复核，排期据此修正）

排期初稿只做了落点验证（迁移号段、行号、文件归属）；本节为逐假设复核的结果——**四项可行性假设全部成立，但发现两处初稿没预料到的实现细节**，已修正：

| 假设 | 复核证据 | 结论 |
|---|---|---|
| B2 渲染器拿得到结构化结果？ | tool_result 事件在 `engine.go:329` 直接 emit `mv.Message.Content`（模型侧消息原文，MCP 工具的 JSON 输出原样在此）；tool_use 在 `:334` emit `name + arguments` | ✅ 成立，且**无需后端改动**——content 已是原始 JSON 字符串，前端形状判定可行 |
| A2 summary 开关落点存在？ | 结算在 `pgrepo/repo.go:377,403` 调 `domain.TaskSummary`（纯截断） | ✅ 成立，但 summary 生成在 **repo 层**（不在 app service）——初稿漏了这个分层：A2 任务书补一条**把 summary 生成上移到 service 层**（repo 只存），LLM 调用不进 repo |
| C1 middleware 能包住工具执行？ | `adk/handler.go:193` `WrapInvokableToolCall(ctx, endpoint, *ToolContext{Name, CallID})` + 基类 `TypedBaseChatModelAgentMiddleware` 提供全部 no-op 默认 | ✅ **比初稿预想更顺**——eino 原生就有工具 wrap 钩子，C1 实现 `ChatModelAgentMiddleware` 即可（与现有 steerMiddleware engine.go:213 同构），不自研切片，工作量下调一档 |
| B1 provision 能扩 pack.yaml 新段？ | `packSpec`（main.go:66-94）纯 yaml tag 结构体 | ✅ 成立；`apiVersion: pack/v1` 需在版本校验处（:127-129）放行 v0/v1 双格式 |

**两处修正（回写进工作流任务书）**：

1. **A3 健康聚合口径修正**：task_message 的 `call_id` 只是模型侧 call ID（`engine.go:334`：`tc.ID`），**不含 pack/服务器名**——初稿"前端按服务器名统计成功率"不可行。改为：tool_use 的 content 首段含工具名，按 `agentblog_` 前缀切分统计（与 B2 渲染器同一前缀路由键，口径统一）。
2. **C1 实现路径修正**：初稿写"chi 风格 middleware 切片"——复核发现 adk 已有原生中间件协议（Wrap* 系列 + Handlers 数组 engine.go:211-271），C1 走原生协议，与 steerMiddleware/toolsearch 天然共存。

**审计边界声明**：以上覆盖四项可行性假设与两处任务书修正；未覆盖、留给各 agent 会话开工自证的：tools_mcp.go 结果序列化的具体字段形状（B2 前端类型需真跑抓样例）、checkpoint 对新 kind 的兼容（C1 迁移时验证）、frame-ancestors 探测对 agentblog 实际响应头的匹配（D3 联调时验证）。
