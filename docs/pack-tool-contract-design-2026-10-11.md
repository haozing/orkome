# Pack 工具契约与首批 Pack 设计（补篇）

> 日期：2026-10-11（v1.1 修订：按 `pack-design-audit-2026-10-11.md` 修正 6 处事实性错误，修订处以 ★ 标注）
> 状态：设计定稿（审计后修订版，可作实施基线）
> 关系：本篇是 `plugin-system-design-2026-10-03.md`（五件套契约、pack.yaml、域零规则——总纲不变）的**工具层补篇**，吸收 dsh（DeepSeek Harness，MIT）的工具纪律，回答三件事：①pack 工具的执行契约怎么定才可靠；②平台内置工具与 MCP 工具怎么统一；③首批两个 pack（agentblog / dify）长什么样。
> 前提：不考虑兼容性与旧数据；v0 干净设计。

---

## 1. 问题：五件套定了"怎么接入"，没定"工具本身的质量契约"

现有实现（`internal/orchestration/adapters/engine/`）已解决接入层：MCP 注册/合并（`MergeMCP`）、SSRF 门、前缀校验（安装期硬卡；★运行期仅有同名去重 `tools_mcp.go:160-163`，无前缀检查——如需运行期前缀护栏属新增项）、每任务组装 + `task.context.mcp_servers` 快照（★注意：该快照是 dispatcher 派发时写入的 resolved 态；engine 侧 `br.snapshot` 的细化状态当前无消费者）、>24 工具降级 toolsearch。这些**保持不动**。

★审计发现的降本事实：MCP 协议层的 `outputSchema` 与 `structuredContent` 其实已经到手——mcp-go `ListTools` 原样返回（`mcp-go/mcp/tools.go:569`），是 eino-ext 包装时丢弃的（`eino-ext/components/tool/mcp/mcp.go:94-101` 只取 Name/Desc/InputSchema）。因此 §2.2 的输出校验优先走"协议层自描述"，pack.yaml 声明只作为补充。

缺的是工具层的四类纪律（dsh 用整套类型系统强制的东西）：

| 缺口 | 现状 | 后果 |
|---|---|---|
| 输出无契约 | MCP 工具返回什么全凭插件自觉 | 脏结果直接进 task_message，模型基于幻觉结构做决策，事后无法归因 |
| 长任务无协议 | 工具调用是同步 RPC | 分钟级工作流（生成管线/大工作流）阻塞回合，超时即灾难 |
| 副作用无分型 | 失败=一个字符串 | 重试策略只能靠模型猜，"发布到一半失败"可能被盲目重试 |
| 内置工具无统一注册表 | engine.go 逐个硬编码装配（`engine.go:221-252`） | 审批/日志/限流逻辑散在各工具内部，加工具=改 engine |

## 2. Pack 工具契约 v1（三条硬规则 + 一条建议）

### 2.1 异步 job-handle 协议（长任务强制）

**任何预期 >30s 的能力必须实现为 job 三件套，禁止同步阻塞**：

```
<pack>_submit_xxx(args) → {job_id}                    # 秒回，绝不等待完成
<pack>_job_status(job_id) → JobStatus                  # 随时可查
<pack>_cancel_job(job_id) → {cancelled: bool}          # 尽力取消
```

`JobStatus` 固定 schema（平台侧校验，见 2.2）：

```json
{
  "job_id": "string",
  "state": "queued | running | awaiting_review | published | failed",
  "stage": "string（自由文本：当前管线阶段）",
  "result": { "url": "string?", "summary": "string?" },
  "metrics": "object（各 pack 自定义，如质检指标）",
  "error": { "code": "string", "retryable": "bool", "stage_failed": "pre_publish | post_publish | null" }?
}
```

员工典型回合：`evaluate`（同步便宜手）→ 决策 → `submit`（秒回 job_id）→ 把 job_id 写进 task_message 结束本轮；后续任务（或事件唤醒的新任务）查 `job_status` 收割。**分钟级管线永不占用引擎并发槽。**

### 2.2 输出契约强校验（脏结果进不了日志）

- ★优先级倒置（较初稿）：**第一优先是回收 MCP 协议层自描述**——桥接层从 mcp-go 的 `ListTools` 结果取 `outputSchema`（已在手，被 eino-ext 丢弃），pack.yaml 的 `output` 声明仅作未自描述工具的补充与安装期闸门。
- pack-provision 安装时校验：工具要么 MCP 自描述 outputSchema、要么 pack.yaml 声明，两者皆缺 = fail the install（与前缀校验同级别闸门）。
- 平台 MCP 桥接层（`tools_mcp.go`）执行后按 schema 校验：合法 → 结构化结果进 task_message；非法 → 工具失败（错误码 `TOOL_OUTPUT_CONTRACT_VIOLATION`），原文截断后仅入审计日志，**模型永不看到未验证的结构**。
- ★实现注意：前端/校验层面对的 content 是 eino-ext 序列化的 `CallToolResult` 包装（content 数组 + structuredContent + isError 整体成串，`eino-ext mcp.go:155-160`），形状判定按包装层写，实现时先真跑抓样例。
- 这是 dsh "output.schema 是强制契约" 的直译（dsh `packages/core/tools/src/index.ts:223-299`），落点在桥接层而非依赖插件自觉。

### 2.3 副作用分型与重试纪律

- `error.stage_failed` 必填于失败时：`pre_publish`（产物未落地，**幂等可重试**：同参数重提=重跑）与 `post_publish`（已对外生效，**禁自动重试**，只能人工处置）。
- `error.retryable` 由 pack 侧按副作用语义声明，平台与员工尊重它；与 `failuretax` 对齐——`provider_network/timeout` 类可走任务级重试，`quality_failed` 类上报人类而非重试。
- dsh 的教训原文（合成收尾错误码 `TOOL_OUTCOME_UNKNOWN` 的提示文案）值得照抄进产品哲学：**"retry only if the operation is read-only or idempotent; if it may have side effects, first verify external state"**——副作用安全学写给模型看。

### 2.4 建议：同步便宜手与能力分层

每个 pack 宜提供"便宜手"（秒级、只读、可批量），让员工先花小钱评估再花大钱执行——这是工具组合产生智能的关键（dsh 的 `evaluate_seed` 模式）。

## 3. 平台内置工具注册表（统一双轨）

现状：内置工具（`request_approval`、`search_workspace`、`post_comment`、`get_work_item`、`update_work_item_status`、wakeup 签到、领队三件套）在 `engine.go:221-252` 硬编码逐个装配。

目标：内置工具与 MCP 工具进**同一个注册表、同一套管线**，对模型无差别：

```go
// internal/orchestration/adapters/engine/registry.go 扩展
type Tool interface {
    Name() string                                        // 内置无前缀；MCP 桥接自动带 <pack>_ 前缀
    Schema() InputSchema                                 // 模型可见说明书
    OutputSchema() JSONSchema                            // 结果契约（2.2 对内置工具同样强制）
    Execute(ctx context.Context, args json.RawMessage) (Result, error)
    ConcurrencySafe() bool                               // fail-closed：默认 false，显式 true 才进并行组
}
```

- ★**ConcurrencySafe 是现实缺失的护栏，不是预留**：审计发现 eino ToolsNode 对同一消息的多个 tool_calls **默认并行执行**（`compose/tool_node.go:276` executeSequentially 零值 false + :1076-1079 并行分流；engine 未关闭）——即并行已经开着、护栏还没有。注册表落地时同步实装 fail-closed 判定（有副作用的内置工具与 MCP 工具在未声明前一律按不安全处理：串行或拒并行），不延后。
- ★**管线实现走 eino 原生协议**，不自研 chi 风格切片：审批/审计/限流/输出校验实现 `ChatModelAgentMiddleware` 的 `WrapInvokableToolCall`（`adk/handler.go:193`），与现有 handlers 数组（steerMiddleware engine.go:213、toolsearch registry.go:38）同构共存；`ToolsNodeConfig.ToolCallMiddlewares`（`compose/tool_node.go:224-228`）为另一挂点。审计中"chi 风格 middleware 切片"的初稿表述作废。
- ★OutputSchema 存 orkome 注册表侧，**不进模型可见 ToolInfo**（eino `schema.ToolInfo` 无输出 schema 字段）。
- `timeoutMs` 等执行参数属于 Tool 实现细节，**永不进模型可见 schema**。
- 组装快照（`task.context.mcp_servers`）扩展为 `task.context.tools`（★理顺两处快照：dispatcher 的 resolved 态与 engine `br.snapshot` 的细化态当前语义分叉且后者无消费者，扩展时统一写入方与形状，勿直接搬运死数据）。

**明确不抄 dsh 的部分**：PTC（代码执行传输）、presentation meta、工具呈现模式（native/ptc/both）——无场景；服务键 DI / 插件生命周期 / 五模式事件——内置工具是编译期装配，MCP 工具是 pack 动态接入，两者都已各得其所，不需要 Cordis。

## 4. agentblog-pack（首个 pack）

### 4.1 pack.yaml

★**格式注意（审计修订）**：以下示例是 **v1 目标格式**。现 provision 只接受 `pack/v0`（`main.go:127-129`）、`ui.entry` 为单数指针（:78-83）、`packMCP` 无 tools 字段（非 strict yaml 会被静默忽略）。落地决策：B1 时 provision 放行 v0/v1 双版本；**ui 段取最小路径——保留 `entry` 单数、仅追加 `slots` 段**（`pack_ui_entry` 表 UNIQUE(workspace_id, pack) 一 pack 一行，entries 数组化是不必要的 schema 重构）。工具的 output 声明（若 pack.yaml 需要写）挂 `mcp.tools`，但优先依赖 MCP 协议层自描述（§2.2）。

```yaml
apiVersion: pack/v1        # v1 目标格式；provision 双版本放行见上
name: agentblog
version: 0.1.0
credentials:
  - key: AGENTBLOG_CALLBACK_TOKEN      # 仅用于 agentblog 验证平台的 MCP 调用方向（见 4.3 ★凭证分离）
mcp:
  - name: agentblog
    transport: streamable_http
    url: https://agentblog.internal/mcp
events:
  - types: ["work_item.updated"]       # ★审计修正：work_item.completed 事件不存在（events.go:61-73 与
                                       #   webhook catalog 均无）——订阅 updated，agentblog 侧按
  #   payload.WorkItemPayload.status 过滤终态后归档
ui:
  entry:                                # ★单数（最小路径，不数组化）
    label: "内容工厂"
    urlTemplate: https://agentblog.internal/console?ws={workspaceId}&t={uiToken}
  # slots 段见 pack-frontend-design §1.2
```

### 4.2 关键语义

- **域零**：文章/种子词/质检指标全在 agentblog 库；平台只有工单与 task_message。`job_id` 是两侧的关联键。
- **副作用分型落地**：生成管线失败时 `stage_failed` 由 agentblog 按发布动作前后如实标记；`post_publish` 失败（如 CDN 刷新失败）永不自动重试。
- **幂等**：`submit_generation(seed)` 同 seed 的 failed(pre_publish) 重提 = 重跑（agentblog 侧按 seed+workspace 幂等键去重）；员工侧无需感知。
- **事件只收尾不编排**：★订阅 `work_item.updated` 并按 status 过滤终态后触发归档——不写任何"事件→新任务"链（events inform, never orchestrate）。

### 4.3 与 agentblog 侧的对接要求（★凭证方向分离）

- MCP server 端点（streamable HTTP）+ 六工具（工具清单：`evaluate_seed`/`query_knowledge` 同步秒级；`submit_generation`/`job_status`/`cancel_job` job-handle 三件套；`review_result` 同步——原 §4.1 示例中的 mcp.tools 段已按审计移除，output 优先走 MCP 协议层自描述）；
- 管线入口可远程触发、产物带稳定 ID（对应 agentblog 侧"入口留缝"的既定要求）；
- ★**两个凭证方向严格分开**（审计修正：pack credential 不会自动进入 webhook 验签链路——provision 安装 events 只传 url+types，`main.go:382-391`）：
  - **平台 → agentblog（MCP 调用）**：`AGENTBLOG_CALLBACK_TOKEN` 类 pack credential，注入 MCP 请求头；
  - **agentblog → 平台（回调 API）**：PAT（专用服务账号 `svc-agentblog-pack`）；
  - **平台 → agentblog（事件推送验签）**：框架 subscription 的 signing secret（sealx 封存，创建订阅时一次性回显、rotate-secret API 可轮换）——**不是** pack credential。

## 5. dify-pack（第二个 pack）

同一模具，三工具：`dify_list_workflows`（同步）/ `dify_run_workflow`（异步 job-handle，映射 Dify 的异步执行 API）/ `dify_job_status`。

- ★**"纯机械触发不过模型"当前是待建能力**（审计修正）：automation 的 autopilot 两种 exec_mode（create_work_item / task_only）最终都会派 agent 任务（`service_autopilot.go:413-427`），域内**没有出站直调执行器**。v0 的零判断定时场景用 **cron 直接触发 Dify 自己的 workflow webhook**（Dify 侧原生支持），不经平台；将来若要"平台内直调"需给 autopilot 立项加出站动作类型——列 v2，不阻塞本设计。
- Dify 工作流的输出变量声明天然映射 `output` 契约（2.2），`stage_failed` 由"difyd 桥"按工作流副作用节点位置标记。

## 6. 边界声明（防止概念漂移）

- **插件（pack）≠ 外部运行时**：pack 给所有员工提供手（MCP 工具）；external agent（Claude Code 类）是另一种员工，走任务回环契约（ADR 0009），**不是插件**。
- **插件 ≠ 内置工具**：内置手编译期装配（§3）；插件手经 MCP 每任务组装。对模型统一，对实现分轨。
- **域零不可破**：任何"让插件把数据表放进平台库"的需求都是重新立项级决策（ADR 0008 双轨禁令）。
- **页面插槽（slots）维持条件启动**：无受益者不建。

## 7. 阶段

| 阶段 | 内容 | 门槛 |
|---|---|---|
| v0.9 | §3 注册表统一（内置工具迁移）+ 输出校验（先内置工具） | 无前置 |
| v1.0 | pack 工具契约三硬规则（job-handle/schema/分型）进 pack-provision 校验 + **agentblog-pack 上线** | agentblog MCP 端点就绪 |
| v1.1 | dify-pack | v1.0 打通 |
| v2 | 审批管线门（从工具演进为 middleware）、PAT scopes、permissions 执行面 | 第二 workspace / 首个第三方意向 |
