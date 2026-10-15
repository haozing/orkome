# Grok Bot 0.18.0 源码级分析与 orkome 借鉴清单

> 日期：2026-10-15
> 材料来源：`b-nnett/grok-bot-0.18-reconstructed`（面向源码的非官方重建，含 27.7 万行可读 TypeScript；本机克隆于 `tmp/grok-bot/`，`tmp/` 不入库）
> 方法：四路并行深读（agent 循环/提示词、记忆与持久化、沙箱工具权限、前端与重建产物），全部结论带 file:line 证据
> 立场：orkome 的骨架（工单/任务/workspace 多租户/归因）不动，本表只回答"补什么纪律进来"

---

## 0. 先说清楚 Grok Bot 是什么（据代码证据）

- **形态**：多 bot 常驻同事。roster/sidebar 管理具名 agent（`New Bot`、群组上限 6 人 `GROUP_MAX_MEMBERS=6`）、人机共享房间（typing/入群申请/邀请）、每 bot 一台"电脑"（forever box，可接管/交还）、routines 定时任务（触发器覆盖 Slack/GitHub/Linear/Sentry/PagerDuty）。落地页文案："Your team of always-on agents that you can give real work to."
- **分发方**：bundle ID `com.anysphere.sand`，Anysphere Incorporated 签名，下载域 `downloads.cursor.com`（社区口径为 xAI×Cursor 合作产品，签名证据指向 Anysphere 发布渠道）。
- **架构**：Electron 桌面 harness（`source/electron-main`：设置/机密/认证/插件生命周期 + 远端 box 连接器）→ coordinator + host（推理路由、turn 执行、工具、MCP、transcript、reactions）→ 渲染进程（React，协议 v1 三通道帧：lifecycle/request/reply/event）。
- **推理路由**：四 provider 可切（Cursor 第一方 / Claude Code / Codex / OpenRouter），工具定义跨家统一（MCP 形状转各家 schema），**工具循环归属差异全隔离在 adapter 内**（codex 自管 Direct Responses 循环 maxSteps=8；claude 走 CLI + 临时本地 MCP server 注入；openrouter 走 AI SDK tool loop）。

---

## 1. 机制清单（20 条，按四层）

### A. Agent 循环与提示词

| # | 机制 | 实现要点 | 证据 |
|---|---|---|---|
| A1 | **双层步数循环** | 外层 runner 兜底 5000 步，内层 agent step 循环 512 步；每步串行，`executeToolStream` 产出流交 interaction handler 消费 | `host/runner/sand-agent-runner.ts:1266`、`packages/agent/constants.ts:1` |
| A2 | **终止=本轮无 tool call** | `(!hasToolCall && !hasQueuedMessages) \|\| isLastIteration`；"回复用户"被建模为 `SendMessage` 工具而非最终文本 | `abstract-user-message-action-handler.ts:2702`、`send-message-tool.ts:16` |
| A3 | **三段式 tool-result 信封** | `{role:"tool", content:[{type:"tool-result",...}], providerOptions:{cursor:{highLevelToolCallResult:{output,isError,rawErrorMessages}}}}`——模型可见文本 + 结构化内容 + 高保真结果/错误标记 | `tool-stream-executor.ts:733-748` |
| A4 | **工具错误分类** | `ToolErrorClassification` 枚举（timeout/user_rejected/hook_denied/mcp_auth_error/aborted…）→ serializeError 回模型 + emitToolCallError 上报 UI | `packages/agent/tools/core.ts:128-155` |
| A5 | **prompt-jsx 组件化提示词** | 自研 JSX runtime；`System/User/Assistant/Tool` 组件产消息；`section` 渲染为 `<title-小写连字符>…</tag>` XML 段；条件挂载各 section（user_info/git_status/skills/mcp） | `packages/prompt-jsx/render.ts:56-282`、`prompts/user-info-component.ts:206` |
| A6 | **每段 prompt 带 token 预算与多级降级** | skill catalog 按上下文 2% 预算四级降级：under_budget → shortened → dropped → omitted | `prompts/skill-catalog-budget.ts:44-70` |
| A7 | **工具输出双阈值截断** | 单次硬限 100k 字符（frontAndBack 保留首尾）；错误消息 20k 预算 | `tools/common.ts:35-50`、`tool-stream-executor.ts:99` |
| A8 | **中断语义化 + 会话级串行队列** | abort reason 带 `{intentional, reason}`；每 agent 一队列前一 turn 完成才跑下一个；回合内排队消息只在 step 边界消费（不打断进行中 step） | `sand-agent-runner.ts:1127`、`inference-router.ts:215-227` |
| A9 | **thinking 显式状态机** | 首个 reasoning chunk 记时间，遇非 thinking chunk 即 seal 并发 `thinkingCompleted(duration)`；运行态 250ms 脉冲防刷新抹掉 | `interaction-handler.ts:171-239`、`inference-router.ts:80-101` |

### B. 记忆与持久化

| # | 机制 | 实现要点 | 证据 |
|---|---|---|---|
| B1 | **会话=内容寻址 blob DAG** | 状态结构只存 turn blob ID，逐层 hydrate；blob ID = SHA-256；每 agent 一把 32 字节 blob 加密密钥 | `agent-kv/agent-store.ts:195-256,350`、`blob-store.ts:21-26` |
| B2 | **manifest CAS 同步** | 本地 SQLite 索引（files/pending_deletes/tombstone）+ ETag 乐观并发（412=冲突，内容寻址写 412 当成功）+ 单写者锁（pid/心跳/stale 判定）+ 近似两阶段提交（先传对象、后 CAS 提交 manifest） | `agent-store-sync/local-index.ts:29-46`、`sand-box-store-files.ts:398-451`、`box-store-manifest.ts:56-72` |
| B3 | **摘要=固定 9 小节 + 显式标记 + 持久块** | 摘要提示词要求"绝口不提总结"、9 段固定结构；回注时作为 user 消息带 `isSummary=true` 标记；另有 durable blocks（plan/todos/skills/transcript 位置）与摘要同级重注入 | `summarization-handler.ts:797-1122`、`durable-blocks.ts:20-144` |
| B4 | **阈值触发 + 异步预生成** | `threshold = maxTokens - unused`；接近即后台摘要不阻塞主循环；严重超限才阻塞等待 | `background-summarization.ts:29-56`、`abstract…ts:2031-2470` |
| B5 | **转写"存全量、读窗口"** | JSONL 全量 + SQLite 索引（seq/kind）；上下文只放"文件夹+检索指引"（先关键词搜索、读附近窗口、不要线性通读）——把回忆变成工具调用而非 prompt 预算 | `agent-transcript/index.ts`、`transcript-location.ts:28-38` |
| B6 | **记忆=文件而非黑箱** | `memory/profile.md`（永久偏好，每轮进系统提示，上限 100 条）+ `log/YYYY-MM.md`（历史，4000 字符预算）+ note；事实行 `- (YYYY-MM-DD) <fact>`；删除须逐字匹配；**渲染结果冻结快照**只在 compactionEpoch 变化时重算（保 KV cache） | `memory-service.ts:9-152`、`sand-memory.ts:2-38,104-125` |
| B7 | **blob 引用投影** | 跨进程只传 `blobId` 引用（`{case:"blobId"} \| {case:"blobIdWithData"}`），接收端按需 hydrate，>512KB 或 >100ms 慢加载有埋点 | `context-processing-hydration.ts:9-81`、`agent-kv/reference.ts:95-142` |
| B8 | **脱敏进类型系统** | `RedactedString.unwrap(purpose)` 不满足用途即抛错；toString/toJSON 全走 display 值；CREDENTIALS/UNSPECIFIED 无条件脱敏；脱敏贴在 proto 边界同时服务日志/遥测/UI | `redaction/types.ts:93-170`、`shouldRedact.ts:5-46` |

### C. 沙箱、工具与权限

| # | 机制 | 实现要点 | 证据 |
|---|---|---|---|
| C1 | **VM 供给三件套** | 供给描述五元组（gatewayUrl/token/networkToken/vncUrl/forkVncBaseUrl）单结构返回 + 加密缓存 7 天 + 迁移进度流 7 阶段；**不是每 bot 一 VM**——共享 VM + 每 agent 窗口（display :N + owner token + 独立 VNC） | `box-host-connector.ts:81-100`、`shared-desktop-sand-box.ts:24-36` |
| C2 | **路径双重校验** | box-exec-daemon 每次 exec 做 resolvePath + realpath 双校验，符号链接直接拒，逃出 workspaceRoot 即 `PathRejectedError` | `box-exec-daemon/server.ts:191-217` |
| C3 | **权限三档 + 审批 id 绑定** | `never\|ask\|always`（默认 ask，管理员 ceiling 取较严者）；`ask` 必须带有效 approvalId 且与动作匹配；本地面 daemon **反向拨号**（用户机不开端口） | `local-tool-permission.ts:2-22`、`local-exec-daemon.ts:23-59` |
| C4 | **审批=可回传消息对象** | 审批事件转写为 transcript 消息（auto-review-approval）；前端经 RPC `resolveAutoReviewApproval` 回传裁决；分类器按 surface 分级（hostShell/boxShell/mcp/computer/automationWrite/cloudAgent/subagent），每档 off/shadow/enforce，TTL 10min、每 agent 最多 4 个待审 | `auto-review-service.ts:50-53`、`shared/rpc/coordinator.ts:100`、`sand-auto-review.ts:7-63` |
| C5 | **凭证不过模型上下文** | secret-request：值直写连接器凭证文件，模型只收到"已设置"回执；1Password 桥铸短期 token 只交 sink 消费者，调用方仅拿 consumerReference | `sand-secret-request.ts:3-5`、`onepassword-provisioning-bridge.ts:24-37` |
| C6 | **接管=工具调用+挂起+快照+交还** | 模型调 `request_box_help(instruction, reason=auth/captcha/payment)` 结束回合；服务记录 pending 抓快照；用户操作后交还恢复；系统提示硬性规定"永不索取/输入密码与 2FA" | `box-help-tool.ts:16-79`、`box-handoff-service.ts:27-38` |
| C7 | **MCP 元工具模式** | 模型侧不是每工具一个名字，而是元工具 `CallMcpTool(providerIdentifier+toolName)` + `GetMcpTools` 发现；传输层才构造 `${server}-${toolName}` | `tools/mcp/mcp.ts:219-447` |
| C8 | **hooks 23 个生命周期点** | JSON 配置（兼容 Claude Code schema 自动转换）；6 个权限类可拦截 + 5 个可追加 context；**fail-closed 缺省**；拦截文案强制附"不要建议绕过" | `hooks/hook-step.ts:1-7`、`hooks-exec/hook-error-handling.ts:1-70` |
| C9 | **shell 输出限速归档 + 进程组取消** | 每流 1MB 截断（stdout_trimmed 事件）、合并超 50MB 落盘归档；50ms/256KB 限速合并；取消=SIGTERM 进程组 1s 后 SIGKILL；前台默认 30s 超时 | `local-exec/shell-core.ts:74-77`、`shell-exec/core.ts:32-101` |
| C10 | **git 安全用环境变量钉** | `safe.bareRepository=explicit`、`core.hooksPath=/dev/null`、`core.fsmonitor=false` 等经 process env 注入（对手写 `-c` 也生效）；错误统一附诊断并脱敏 URL userinfo | `git-core/process-env.ts:3-56` |

### D. 前端与协议

| # | 机制 | 实现要点 | 证据 |
|---|---|---|---|
| D1 | **单订阅 + 基线增量事件** | transcript 事件四态 `snapshot/appended/updated/cleared` 带作用域隔离；流式=连续 updated（含 before/after）；不断言"停止生成"（shipped 契约没有） | `transcript-feed-source.ts:87-147`、`submission.ts:6,44-56` |
| D2 | **发送=离线队列** | 状态机 `pending\|queued\|failed\|sent\|cancelled`：乐观发送、离线排队、重连 flush、按 agent 的 stale-generation 栅栏 | `workspace/submission.ts`、`acknowledgement.ts:12` |
| D3 | **reactions 乐观+静默失败** | 本地先赢（乐观更新），失败静默，权威数据用 projection 回灌；计数去重+首现排序 | `reaction-actions.ts:79-115` |
| D4 | **审批卡片显式化"在哪执行"** | "Runs on your local computer" vs "Runs on Grok Bot's computer"；待批准规则脱敏（URL 去 userinfo/query、sk-/gh-/xox 等）；**store/RPC 缺失时 fail-closed 渲染 null 而非假按钮** | `auto-review-approval.tsx:12-60`、`local-tool-permission.tsx:20-25` |

---

## 2. 与 orkome 的对比（逐条裁决）

| Grok 机制 | orkome 现状 | 裁决 |
|---|---|---|
| A1/A2 双层步数 + SendMessage 工具化 | 引擎已有 maxSteps 语义与四道寿命闸；回复走 outcome/汇报 | **已在**（形态不同：任务粒度 vs 会话粒度） |
| A3 三段式 tool-result 信封 | task_message 单一 content + 2000 rune 截断 | **可借鉴**：错误/结构化结果分层（P2，配合工具契约） |
| A4 工具错误分类枚举 | 引擎有 failure_reason 但工具级分类粗 | **可借鉴**：pack 工具契约加 error taxonomy（P2） |
| A5 prompt 组件化 | 提示词靠 Go 字符串拼接（engine.go squadBrief 等） | **可借鉴**：条件段 + 可单测渲染（P3，Go 里做 template 化） |
| A6/A7 预算降级 + 双阈值截断 | token 预算散落（skill catalog 有 24 阈值类机制），截断只有单一 2000 rune | **可借鉴**：截断分"流内截断+归档"两级（P2，与 spill 同族） |
| A8 中断语义 + 串行队列 | task_supplement steer + 取消；单任务单执行天然串行 | **已在**（且任务粒度更简单） |
| A9 thinking 状态机 | task:progress 帧 + 执行 tab | **已在**（脉冲防抹除的小技巧可留意） |
| B1 blob DAG 会话 | task_message 单调 seq + task.context | **不抄**：会话粒度存储，任务粒度用不上 |
| B2 manifest CAS 同步 | 无本地优先需求（平台侧单一权威库） | **不抄**（无场景） |
| B3 摘要固定小节+标记+持久块 | **已被 dsh 借鉴表吸收**：七段 summary（A2 已实现，跟随任务语言）；durable blocks 尚未有对应 | **部分已在**；"压缩后仍必须存在的块清单"思想可参考（P3） |
| B4 阈值触发+异步预生成 | summary 在结算时生成（非 in-flight 压缩） | **不抄**（无长会话） |
| B5 转写存全量读窗口 | 时间线/汇报 tab 已是"按需水合"哲学（同构） | **已在**（orkome 的再水合设计即此思想） |
| B6 记忆=文件+冻结快照 | skill 即知识库（用户裁决）；无 per-bot 自动记忆 | **语义反转借鉴**：人沉淀、模型消费（见 §3 P1-3）；冻结快照技巧对提示词缓存有参考 |
| B7 blob 引用投影 | attachments 已是"ID 引用+REST 拉取" | **已在** |
| B8 脱敏类型系统 | sealx 密封 + 接口层脱敏；无编译期类型包装 | **可借鉴**（P3）：密钥/PII 的显式包装类型（Go 里用专用类型 + 构造校验） |
| C1 VM 供给 | 无 VM；执行外包（外部运行时/助理模式） | **不抄**（分层不同：orkome 不做常驻 VM） |
| C2 路径双重校验 | 无沙箱面 | 若未来做本地执行器可抄（挂起） |
| C3/C4 权限三档+审批 id 绑定+审批对象化 | 任务级审批停车/恢复 + runtimeAgentWriteGuard + 通知面 approval_request | **可借鉴**（P1-1）：审批数据流对象化 + 前端内联裁决；**动作分级 classifier 不作 v0**（有审批停车已覆盖） |
| C5 凭证不过模型上下文 | sealx 密封凭证 + runtime credential 双轨 + `submitSecret` 类语义无 | **已在**（且 orkome 的 sealx 域分离更严） |
| C6 接管=工具+挂起+快照+交还 | 无（无 VM 无接管面） | **不抄** |
| C7 MCP 元工具模式 | 双轨（workspace+agent 两级）；工具名前缀闸；**v0 每工具一个名字** | **可借鉴**（P2）：工具数爆炸时元工具是现成退路（toolsearch 阈值已有，元工具是另一形态） |
| C8 hooks 生命周期 | 无用户可写 hook；有事件总线+outbox | **缓办**：pack 事件订阅已覆盖"外部响应"面；进程内 hook 点（before-tool 拦截）对平台化有吸引力但 v0 不需要 |
| C9/C10 shell/git 加固 | 无 shell 执行面（引擎沙箱另有设计） | **可借鉴**（P3）：若做本地执行器直接抄环境变量钉法 |
| D1-D4 前端协议 | 单 WS + 订阅框架 + query invalidation；**无乐观发送队列**（REST 直接） | **可借鉴**（P2）：D2 离线队列思想对"评论提交/审批"有价值；D4 **fail-closed 渲染**（P1-2） |

---

## 3. 借鉴清单（按优先级，全部映射到 orkome 落点）

### P0 —— 无（Grok 的核心机制 orkome 要么已在、要么不适用）

### P1 —— 高价值、低成本

1. **审批数据流对象化 + 通知内联裁决**（对应 C3/C4/D4）
   - 现状：`approval_request` 通知只能跳工单，审批按钮在任务面板。
   - 落点：通知详情弹窗加内联批准/拒绝（`settleTask(id, approved)` 已存在，纯前端）；审批事件补 transcript 痕迹（task_message 加 approval kind——或直接用现有时间线事件）。
   - 收益：把"AI 卡住了等你"从两跳变一跳——这正是 Grok 审批卡"Allow/Deny 在流里"的好用之处。

2. **前端 fail-closed 渲染纪律**（对应 D4）
   - 现状：部分组件在数据缺失时可能渲染假状态（如 loading 空转/空按钮）。
   - 落点：写进前端规范——store/RPC 缺失时渲染 null/error 态而非假按钮；审计一遍现有 pack slot/权限卡类组件。

3. **"steering 沉淀"入口（记忆语义反转版）**（对应 B6，用户已讨论过的方案 B）
   - 现状：任务级 supplement 纠正随任务消亡；"记住这条"无出口。
   - 落点：评论/纠正旁加动作 → 沉淀到 agent 提示词或技能（带出处行）；forget=编辑/删工件。**不做自动记忆**（拒绝黑箱）。
   - 触发信号：同一 agent 被反复纠同一件事。

### P2 —— 有场景、需设计

4. **工具结果两级截断（流内截断+归档）**（对应 A7/C9）
   - 现状：task_message 2000 rune 截断即丢失。
   - 落点：大结果存平台对象存储/附件，task_message 留"定位符+摘要+按需检索工具"（与 dsh spill 同族，已在账）。
5. **pack 工具契约补 error taxonomy**（对应 A4）
   - 落点：pack.yaml `mcp.tools` schema 加 `errors` 声明；引擎按分类决定重试/上报/回模型。
6. **工具元模式预案**（对应 C7）
   - 落点：MCP 工具超阈值（如单 server >20）时启用 `call_mcp_tool + list_mcp_tools` 双工具（toolsearch 已证此路有效，元工具是其泛化）。
7. **评论/审批提交的离线队列**（对应 D2）
   - 落点：REST 提交失败时 queued 状态 + 重连 flush（对移动端/弱网有用；v0 可只做"失败可重试"可见化）。
8. **prompt 组件化（Go 侧 template 化）**（对应 A5）
   - 落点：engine 的 briefing/系统提示拼装改为具名 section + 条件渲染 + 单测（现在散在字符串拼接里）。

### P3 —— 记录在案、缓办

9. 密钥/PII 显式类型包装（B8）——Go 用 `type Secret string` + 日志层强制 unwrap 声明；等有 PII 场景。
10. 提示词冻结快照（B6）——保 KV cache 的提示词稳定性策略；接入缓存计费后评估。
11. 本地执行器加固（C2/C9/C10）——路径双校验/进程组取消/环境变量钉 git；做本地执行器时直接抄。
12. 用户可写 hooks（C8）——进程内 hook 点；与 pack 事件订阅边界需先裁决。

### 明确不抄

- 会话 blob DAG / manifest CAS 同步（B1/B2）——无本地优先场景。
- 常驻 VM / VNC 接管（C1/C6）——分层不兼容，orkome 执行外包。
- per-bot 自动记忆/合成（B6 自动抽取路径）——黑箱、不可归因，违反 orkome 信息架构（用户已裁）。
- in-flight 摘要压缩（B4）——无长会话。
- 语音/桌面全局快捷键/通知 tray（D 层边缘）——桌面端形态差异。

---

## 4. 对"更好用"的结论（回应"我们能比它更好用吗"）

Grok Bot 的"好用"= 出厂预置（打开就有一个带云电脑的同事 + 模板市场 + 连接器）。orkome 的"好用"= **零配置得到一支出力可见的 AI 团队**——两战场不重叠。代码级对比后的结论：

1. **orkome 缺的不是机制，是"缝"**。Grok 的机制层（循环/信封/记忆/沙箱）与 orkome 已有资产（任务生命周期/事件总线/pack/审批停车）大体同代；差距在把已有能力缝成一条零摩擦线（Quick Actions 后端全绿但 Web 无 UI、onboarding 无第二幕、审批要两跳）。
2. **P1-1（通知内联审批）是当前最短的桥**：两跳变一跳，直接对齐 Grok 审批卡体验，纯前端。
3. **P1-3（steering 沉淀）把 Grok 的"模型记、人审"反转为"人沉淀、模型消费"**——保留归属与可评审，避开黑箱。
4. **P2-4/5/6 是引擎纪律补强**：与已记录的 dsh spill/工具契约同族，一起排期。

> 配套：`docs/dsh-borrowing-table-v2-2026-10-11.md`（内容层借鉴）、`docs/assistant-access-design-2026-10-14.md`（助理接入）。
