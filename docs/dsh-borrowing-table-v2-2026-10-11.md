# dsh 借鉴表 v2 —— 落地到 orkome 的裁决清单

> 日期：2026-10-11（v1.1 修订：按 `pack-design-audit-2026-10-11.md` 修正 4 处，含 1 处优先级上调——ConcurrencySafe 从 P3 升 P1，修订处以 ★ 标注）
> 状态：定稿（审计后修订版）
> 背景：本表取代对话中的口头 v1（"Eino 员工工具系统抄 dsh 什么"）。orkome 是我们自己的项目——板/任务运行时/插件契约已有实现与设计文档（plugin-system-design、agent-client-contract-design、context-rehydration-design、ADR 0001-0013）。本表回答：**在这些既有资产之上，dsh（DeepSeek Harness，MIT，源码本地有 clone）还能补什么纪律进来**，逐条裁决并给落点。
> 配套：`pack-tool-contract-design-2026-10-11.md`（工具层补篇）

---

## 0. 总原则

**orkome 的骨架（板、双运行时、五件套、再水合）不动——它们已被自己的设计文档和实现锁定，且恰好是与 dsh 不同的正确形态（任务粒度 vs 会话粒度）。dsh 只补纪律，不换骨架。**

三个判断依据（沿用对话结论）：

1. dsh 是"会话粒度"的 harness（黑板+解释器）：一轮 turn 内多 step、请求冻结、surface 投影。orkome 是"任务粒度"的板：一任务一执行、supplement steer、结算摘要。粒度不同，协议不可平移。
2. dsh 可抄的精华分两类：**内容层**（提示词、预算参数——MIT 白拿）与**纪律层**（契约、fail-closed、可重建——翻译成 Go）。
3. 明确不抄：Cordis 本体（进程内插件运行时）——orkome 的扩展走进程外 MCP（pack）与编译期装配（内置工具），双轨禁令（ADR 0008）与 Cordis 哲学互斥，且这是有意选择。

---

## 1. 逐条裁决表

### 1.1 工具系统 → 落点 `pack-tool-contract-design` §2/§3

| dsh 的东西 | 裁决 | 落点与备注 |
|---|---|---|
| ToolDefinition（name/schema/execute） | **已在**（eino tool.BaseTool + MCP 桥接双轨已实现）；**升级为统一 Tool 接口** | 注册表统一内置+MCP（补篇 §3）；对模型无差别 |
| output.schema 结果强契约 | ✅ **抄（最高优先级）** | dsh `packages/core/tools/src/index.ts:223-299` 强制 output 契约。落点：pack 工具输出 schema 安装期校验 + 运行期桥接层校验，非法=工具失败不进 task_message（补篇 §2.2） |
| fail-closed 并发（isConcurrencySafe 仅显式 true 并行） | ✅ ★**升 P1 实装（原"预留"判断作废）** | 审计发现 eino ToolsNode 对同消息多 tool_calls **默认并行**（`compose/tool_node.go:276` 零值 false + :1076-1079 并行分流，engine 未关闭）——并行已经开着、护栏缺着。`ConcurrencySafe() bool` 默认 false 在注册表落地时同步实装（有副作用工具未声明即不并行），不是将来才需要的东西 |
| pre/execute/post 三相管线 | ✅ 抄形状（★实现走 eino 原生协议） | 审计→限流→审批门→execute→输出校验→脱敏，实现 `ChatModelAgentMiddleware` 的 `WrapInvokableToolCall`（`adk/handler.go:193`），与现有 steerMiddleware/toolsearch 同构共存；初稿"chi 风格自研切片"作废 |
| preset（per-agent 工具集） | **已在**（workspace+agent 两级 MCP 合并 = 同语义）；扩展 | preset 概念扩到内置工具 allowlist（补篇 §3） |
| timeoutMs 与模型可见 schema 分离 | ✅ 顺手抄 | 执行参数不进 schema |
| 每任务组装+快照审计 | **已在**（`task.context.mcp_servers`） | 扩为 `task.context.tools` 纳入内置工具（补篇 §3） |
| 工具过载→toolsearch | **已在**（24 阈值） | 不动 |
| 命名前缀纪律 | **已在**（★安装期硬卡；运行期只有同名去重，无前缀检查——"运行期软丢"为初稿误述，已修正） | 不动；运行期前缀护栏如需要属新增 |
| 后台任务 job 模式（ctx.jobs + job_* 工具） | ✅ **抄协议形状** | dsh 的后台任务=句柄+查询，正是 job-handle 三件套的原型（补篇 §2.1） |
| PTC、presentation meta、呈现模式 | ❌ 无场景 | 维持 |
| 服务键 DI/插件生命周期/HMR/五模式事件 | ❌ 与双轨禁令互斥 | 维持 |

### 1.2 上下文与日志 → 落点：engine 与 rehydration 的增强

| dsh 的东西 | 裁决 | 落点与备注 |
|---|---|---|
| 日志即真相（append-only 执行真相） | **已在**（task_message 单调 seq） | 不动；以下三条是它缺的配套 |
| **七段压缩 checkpoint 提示词全文** | ✅ **抄内容（最划算的一笔）** | dsh `packages/compaction/compaction-basic/src/summarizer.ts`：七段结构（技术概念/文件与代码/错误与修复/未完成/当前工作/下一步/关键上下文）+ 保留规则（精确路径/命令/错误串/用户纠正）+ preamble（"既定背景直接续做"）。落点：①work item 任务链的 session summary 生成提示词（现 500 字 v0 无 LLM）；②长任务转写压缩。★配套结构（转写+摘要注入）已有，但 **summary 生成当前在 repo 层（repo.go:377,403），LLM 调用需先上移 service 层**——成本"低（含一次分层上移）"而非"极低" |
| 暖前缀缓存复用（摘要调用复刻上次请求前缀） | ✅ 抄技巧（★排期已裁剪：v0 不做） | summarizer 的 SummarizationInput 设计依赖"复刻上次请求"的请求管线——orkome summary 是独立小调用，抄了不生效；等 summary 生成接入与主请求同管线时再评估 |
| 请求可重建（"模型可见即已记录"） | ✅ 抄纪律（部分达成→补全） | ★现状精确化（审计）：prior_summary/prior_failure_reason/trigger 等在 task.context，但 history（engine.go:178-180）、session 转写（:184-185）、squadBrief（:209）是**派发时拉取、不在 context**——"材料有记录"只对一半。补全：engine 每次模型调用前把**最终 user prompt 全文**落 task_message（新 kind `prompt`；engine 侧 eventPipe 直写不经 external 白名单，但 DB 有 `CHECK (kind IN 5 值)` 约束需迁移放行）。成本低，审计价值高 |
| 失败也结算（assistant/attempt 思想） | **已在**（failuretax + task_settled 时间线） | 不动 |
| 失败重试防盲重（prior_summary 注入） | **已在**（retry 行 context 注入） | 不动 |
| Spill（大文本外溢为定位符） | ✅ **抄（适配版）** | task_message 已有 2000 rune 截断，但截断即丢失。dsh spill 思想：大工具结果（网页抓取/长文档）存平台对象存储或插件侧，task_message 只留"定位符+摘要+按需检索工具"。v1.5 列入，等首个真实大结果场景 |
| 会话 fork / surface replace 协议 / 助手流内嵌 | ❌ 会话粒度协议，任务粒度用不上 | 维持 |

### 1.3 板与任务运行时 → **几乎全部已在，orkome 版本更贴场景**

| dsh/orkome 的东西 | 裁决 | 备注 |
|---|---|---|
| inbox/steering | **已在**（supplement 注入 + steerMiddleware） | dsh 的 inbox 是会话粒度持久队列；orkome 的 task_supplement 是任务粒度等价物，已够用 |
| 审批停车/恢复 | **已在**（park/Interrupt/checkpoint 恢复 + 24h 超时） | 不动 |
| 租约/心跳判死 | **已在**（且双运行时分裂比 dsh 场景更细） | 不动 |
| 失败分类学 | **已在**（failuretax 15 类） | 不动 |
| 合成收尾错误码（TOOL_NOT_STARTED / TOOL_OUTCOME_UNKNOWN + 副作用安全文案） | ✅ **抄文案思想** | 任务失败时若存在"已派发但结果未知的工具/job"，结算摘要里带 dsh 式提示："该操作可能已部分生效；仅当只读或幂等才自动重试，否则先核实外部状态"——比裸失败字符串安全得多。落点：engine 结算 + job_status 轮询超时路径 |
| Turn/Step 协议、waterfall 全家桶 | ❌ 任务粒度不需要 | 维持 |

### 1.4 Cordis（进程内插件运行时）——终审 ❌

已有两轮论证，终审记录：Go 无进程内动态插件的可行路径；orkome 的扩展模型（编译期内置 + 进程外 MCP pack）已被 ADR 0008 锁定为互斥选择；重启即重载对单二进制部署足够。**唯一保留的 Cordis"思想"已被吸收**：接口+注册表（编译期）与 manifest+provision（进程外）正是它的 Go 翻译。

---

## 2. 优先级排序（工程视角）

| 序 | 项 | 成本 | 收益 |
|---|---|---|---|
| P0 | 输出契约强校验（补篇 §2.2） | 中（桥接层+安装校验） | 消灭"模型基于脏结构决策"这类最难查的故障 |
| P0 | job-handle 三件套协议（补篇 §2.1） | 低（协议设计+pack 遵守） | 长任务不再阻塞引擎槽，超时灾难消失 |
| P1 | 七段压缩提示词搬运 | ★低（含 summary 生成从 repo 上移 service 一次分层） | summary 质量跃升，MIT 白拿 |
| P1 | 请求可重建（prompt 全文落日志） | 低（新 kind 需迁移放行 DB CHECK 约束） | 审计/回放能力，事后归因 |
| P1 | ★fail-closed ConcurrencySafe（自 P3 上调） | 低（随注册表落地） | **eino 默认并行已开着**，副作用工具缺护栏是现实风险 |
| P2 | 统一 Tool 注册表 + middleware 管线 | 中 | 加工具不再改 engine；审批/限流集中 |
| P2 | 副作用安全文案（结算路径） | 极低 | 防盲重试事故 |
| P3 | Spill 适配版 | 中 | 等首个大结果场景再动 |

## 3. 一句话总账

**骨架是我们的（板/双运行时/五件套/再水合），dsh 只在三个地方落子：工具的"契约"（输出校验+job-handle+副作用分型）、上下文的"内容"（七段提示词+缓存复用+可重建）、失败的安全学（收尾文案）。Cordis 终审不抄——双轨禁令是我们对它的回答。**
