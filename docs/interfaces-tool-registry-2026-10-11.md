# 接口冻结：统一 Tool 注册表 + middleware 管线 + ConcurrencySafe + task.context.tools（C1）

> 日期：2026-10-11
> 状态：**接口冻结**（下游 D1/D2/D4 与前端只读本文档，不读 C1 实现）
> 归属工作流：C1（波 3）；上游：A1 `OutputValidator`（`interfaces-output-validator-2026-10-11.md`）、B1 存档读取 API（`interfaces-job-handle-2026-10-11.md` §6）
> 需求来源：`pack-tool-contract-design-2026-10-11.md` §3；`pack-rollout-plan-2026-10-11.md` §3 C1

---

## 0. 一句话语义

内置工具与 MCP 桥接工具进同一个注册表、同一套管线：内置工具的输出从此也受 A1 校验（注册表侧 OutputSchema），有副作用而未显式声明 `ConcurrencySafe=true` 的工具在 eino 默认并行的 ToolsNode 上不再并行（fail-closed 串行闸），`task.context` 新增 `tools` 全量清单键（兼容读旧键 `mcp_servers`）。

## 1. ContractTool 接口（Go 签名冻结）

包：`internal/orchestration/adapters/engine`（registry.go）。

```go
// ContractTool 是内置工具的可选契约接口：在 eino tool.BaseTool 之上
// 声明输出契约与并发安全分型。实现方只提供元数据，执行路径仍是原
// InvokableRun，行为不变。
type ContractTool interface {
    // OutputSchema 返回该工具输出的 JSON Schema（按实际返回形状写）。
    // 返回 nil 表示该工具显式豁免输出契约（纯文本答复类工具的有意识
    // 决定，豁免会在注册表快照里如实标记 has_output_schema=false）。
    OutputSchema() json.RawMessage

    // ConcurrencySafe 声明该工具是否可与同消息其他 tool_call 并行执行。
    // fail-closed：默认（未实现本接口或返回 false）不并行。
    ConcurrencySafe() bool
}
```

**与契约文档 §3 草图的差异（有意为之，行为不变优先）**：草图里的 `Name()/Schema()/Execute()` 由 eino 执行面承担——`Info()` 即模型可见说明书（输入 schema 载体），`InvokableRun` 即执行路径；orkome 侧只叠加两个元数据方法，不重造签名、不给装饰层留双重分叉。执行期 eino ToolsNode 消费的仍是 `tool.BaseTool`，模型可见 ToolInfo 无任何变化（OutputSchema 存注册表侧，eino `schema.ToolInfo` 本就没有输出 schema 字段）。

注册表条目（进程内，每次任务组装重建）：

```go
// toolRegistry：一次任务组装期的统一注册表（engine 包内非导出类型）。
// 条目来源：registerBuiltin（内置，经 ContractTool 回收元数据）与
// registerMCP（MCP 桥接，schema 传"协议自描述→B1 存档"的最终生效值，
// 并发一律按不安全处理）。
// 消费方：① 输出校验+并发闸 middleware（§2）② task.context.tools
// 快照（§3）③ B1 schema 优先级（bridge.schemaOf）。
```

同名登记后写覆盖先写（与装配段的覆盖顺序一致：内置先登记、MCP 后登记）。

## 2. middleware 管线（eino 原生协议，不自研切片）

挂点：`adk.ChatModelAgentMiddleware.WrapInvokableToolCall`（vendor `adk/handler.go:193`），与既有 steerMiddleware（engine.go）/ toolsearch（registry.go）同构共存——adk 构造 ChatModelAgent 时自动把 Handlers 转成 ToolsNode 的 ToolCallMiddlewares。toolsearch 动态注入的工具同样被管线覆盖（中间件在 ToolsNode 层，按工具名判定）。

管线职责与判定语义（冻结）：

1. **输出校验**：`origin=builtin` 的工具执行成功后，输出按注册表 OutputSchema 过 `OutputValidator`（与 MCP 同一个校验器、同一套违规错误格式——A1 接口文档 §4 的 golden 原样复用）。文档选取规则（与 MCP 路径不同，冻结）：内置工具输出是平台自己的文本，**整体按 JSON 解析，解析失败按字符串文档处理**——`{"type":"string"}` 契约即为此形状。工具失败（error / eino Interrupt）原样透传不走校验。MCP 工具**不在 middleware 校验**（其输出是 eino-ext CallToolResult 包装串），仍由 A1 的 `wrapWithValidator` 装饰器承担——两个执行位、同一个 validator。
2. **ConcurrencySafe fail-closed 串行闸**：每个工具调用按注册表判定——未显式 `ConcurrencySafe=true` 的工具（含未登记的成员工具、全部 MCP 工具）共用本 run 的互斥锁逐个执行；显式 true 的只读/幂等工具直接放行（走 eino 原生并行路径）。语义：**不安全工具之间绝不并行；安全工具可彼此并行、也可与不安全工具并行**（只读/幂等与副作用并行无风险）。判定是纯注册表查询，无竞态。

## 3. task.context.tools（JSON 形状冻结，前端将来要读）

引擎组装完注册表后经 `TaskContextPatcher` 一次性回写（写入失败只记日志不阻塞任务）。**双键语义理顺**：

- `tools`（新键）：统一注册表全量清单——任务的**真实工具面**（内置 + MCP，含契约与并发分型标记），一次写入即完整；
- `mcp_servers`（旧键）：引擎用桥接细化态（`ok / ssrf_rejected / connect_failed / list_failed / duplicate_name_dropped` + tool_count）覆盖派发期 dispatcher 写入的 `resolved` 占位粗快照——**引擎侧为权威写入方**（离执行最近、状态最细）；任务若从未执行（排队中取消），保留 dispatcher 的 resolved 占位。

`tools` 数组元素（`orchapp.ToolSnapshotEntry`，键名冻结）：

```json
{
  "name": "agentblog_job_status",   // 内置无前缀；MCP 自动带 <pack>_ 前缀
  "origin": "mcp",                  // "builtin" | "mcp"
  "server": "agentblog",            // 仅 origin=mcp 有值；内置为空（键省略）
  "has_output_schema": true,        // 是否声明了输出契约（协议自描述/B1 存档/内置 OutputSchema）
  "concurrency_safe": false         // 是否显式声明可并行（fail-closed 默认 false）
}
```

读取（服务端 helper，`orchapp.TaskContextTools(ctx map[string]any) []ToolSnapshotEntry`）：优先新键 `tools`；老任务（无新键）回落旧键 `mcp_servers` 合成服务器级聚合条目（`origin=mcp`、无逐工具明细）；两键皆无返回 nil。兼容 `[]any`（DB jsonb 回读）与 `[]map[string]any`（进程内构造）两种值形态。前端在 TS 侧应实现同语义读取（先 `context.tools` 后 `context.mcp_servers`）。

## 4. B1 存档接入（优先级冻结，与 A1/B1 文档一致）

`EinoEngine.PackToolSchemas`（结构性接口，装 `orchapp.NewPackToolSchemaService(orchpgrepo.New(db))`，勿走 HTTP）在桥接组装期一次拉全 workspace 存档，为协议未自描述的 MCP 工具补输出契约：

```
最终生效 schema = 协议自描述（mcp-go ListTools 实时回收，优先）
              OR B1 存档（pack_tool_schema，补位）
              → 均无 → A1 结构化探测回落
```

最终生效值同时供三处：A1 装饰器（执行校验）、注册表（middleware 判定/快照）、`bridge.schemaOf`。端口未接线或拉取失败 → 空映射，不阻塞任务（回落探测）。

## 5. 内置工具契约清单（v0 冻结基线）

| 工具 | OutputSchema | ConcurrencySafe | 说明 |
|---|---|---|---|
| request_approval | nil（豁免） | false | 人类审批决定转述（纯文本答复类）；中断型工具 |
| search_workspace | nil（豁免） | **true** | 命中清单动态文本（截断省略号），只读 |
| get_work_item | `{"type":"string"}` | **true** | Title/Description 两行文本，只读 |
| post_comment | `{"type":"string"}` | false | 写评论（成功串/拒绝串/toolFail 串同为 string） |
| update_work_item_status | `{"type":"string"}` | false | 状态机转移（有副作用） |
| checkin_wakeup | `{"type":"string"}` | false | 签到唤醒规则（有副作用） |
| dispatch_to_member | `{"type":"string"}` | false | 派发任务（有副作用） |
| create_child_work_item | `{"type":"string"}` | false | 建子工单（有副作用） |
| record_evaluation | `{"type":"string"}` | false | 评估台账（有副作用） |
| MCP 桥接工具（全部） | 协议自描述/B1 存档/无 | 一律 false | pack 侧尚无声明通道，声明机制留给后续版本 |

契约锚定形状不锚定文案：改返回文案不需动契约；改结构（如某工具升级为结构化 JSON 输出）会被校验拦下，须同步更新 OutputSchema——这是有意识的契约演进闸门。

## 6. 边界与不做（C1）

- 不动 registry.go 的 toolsearch 阈值分桶逻辑、不动 dispatcher 的派发期快照写入（workers/ 非 C1 归属；resolved 占位语义保留）。
- toolsearch 注入的检索工具本身未进注册表 → 按不安全串行（fail-closed 的保守面，只读检索被串行是过度保守，留待后续把它显式登记为 safe）。
- MCP 工具的 `ConcurrencySafe=true` 声明通道（如 pack.yaml 声明段）留给后续版本；v0 一律串行。
- eino ToolsNode 的 `ExecuteSequentially` 全局开关未启用（middleware 串行闸是更细粒度的等价实装：安全工具仍可并行）。

## 7. 测试锚点（回归时必须保持绿）

- `TestContractMiddleware_BuiltinOutputValidated` / `TestContractMiddleware_ObjectOutputAgainstStringSchema` / `TestContractMiddleware_UnregisteredAndErrorsPassThrough`：校验管线五态。
- `TestRegistry_FailClosedDefaults`：注册表分型 + 快照形状 + fail-closed 默认。
- `TestContractMiddleware_UnsafeToolsSerialize`：未声明不并行 / 显式 true 并行 / 未登记按不安全（middleware 层）。
- `TestEngineRun_UnsafeToolsSerialized_EndToEnd` / `TestEngineRun_SafeToolsParallel_EndToEnd`：同消息双 tool_calls 经 adk/eino 真跑的零重叠 / 并行保留。
- `TestEngineRun_BuiltinOutputContractEnforced_EndToEnd`：校验 middleware 真挂载（合法透传 + 违规替换全链路）。
- `TestToolContextSnapshot_Shape` / `TestTaskContextTools_NewKeyPreferred` / `TestTaskContextTools_LegacyFallback` / `TestEngineRun_PatchesToolSnapshot`：context.tools 新旧双读 + 引擎回写。
- `TestArchivedToolSchemas_BackfillPriority` / `TestArchivedToolSchemas_NilPort`：B1 存档两级优先级与未接线回落。
