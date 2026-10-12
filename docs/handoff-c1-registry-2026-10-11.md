# C1 交接笔记：统一 Tool 注册表 + middleware 管线 + ConcurrencySafe + task.context.tools（2026-10-11）

> 工作流：C1（波 3）｜需求：`pack-tool-contract-design-2026-10-11.md` §3｜任务书：`pack-rollout-plan-2026-10-11.md` §3 C1
> 上游：A1（`handoff-a1-output-contract-2026-10-11.md`）、B1（`handoff-b1-job-gate-2026-10-11.md`）
> **接口冻结文档：`docs/interfaces-tool-registry-2026-10-11.md`（下游只读它，不读实现）**

## 一、改了什么

### 新增文件（3 个，均 `internal/orchestration/adapters/engine/`）

| 文件 | 内容 |
|---|---|
| `registry.go`（扩展） | `ContractTool` 可选接口（OutputSchema/ConcurrencySafe）+ `toolRegistry` 统一注册表（builtin/mcp 同表、同名后写覆盖、快照输出）——与 toolsearch 阈值分桶共存 |
| `registry_middleware.go` | `toolContractMiddleware`（实现 adk `ChatModelAgentMiddleware.WrapInvokableToolCall`）：输出校验（builtin origin）+ ConcurrencySafe fail-closed 串行闸；违规复用 A1 golden 格式 + 审计日志 |
| `registry_assembly.go` | `buildBuiltinTools`（engine.go:221-260 装配段**纯搬移**）+ `registerMCPBridge`（B1 存档补位登记）+ `patchToolSnapshot`（task.context 双键回写） |
| `registry_c1_test.go` | 新增 14 个测试（见冻结文档 §7） |

### 修改文件

| 文件 | 改动 |
|---|---|
| `engine.go` | ① 结构体增 `PackToolSchemas`（B1 存档端口）、`TaskContextPatcher`（快照回写端口）两字段；② 装配段替换为 `buildBuiltinTools` + `registerMCPBridge` + `newToolContractMiddleware` 三行（diff 为纯搬移+新增挂载） |
| `tools.go` / `tools_work.go` / `tools_squad.go` | 9 个内置工具各加 `OutputSchema()`/`ConcurrencySafe()` 两方法（零行为改动，纯元数据） |
| `tools_mcp.go` | ① `mcpBridge` 增 `schemas` 字段 + `schemaOf`；② A1 装饰器的 schema 选择升级两级：协议自描述 → B1 存档（`archivedToolSchemas` helper，端口 nil/失败回落空映射）；③ 最终生效 schema 落 `br.schemas` |
| `ports.go`（app，独占） | `ToolSnapshotEntry` 类型 + `ToolContextSnapshot`（双键写入载荷）+ `TaskContextTools`（新键优先/旧键回落/双形态兼容）+ `MCPContextSnapshot` 保留（dispatcher 兼容） |
| `wiring_orchestration.go` | 引擎接线：`PackToolSchemas: orchapp.NewPackToolSchemaService(repo)`（B1 §3 口径，不走 HTTP）、`TaskContextPatcher: repo`、`AuditLog: slog.New(log.Handler()).With("channel","audit")`——**A1 遗留坑 #7（AuditLog 未接线）就此收口** |

## 二、为何这么改（关键决策）

1. **ContractTool 可选接口而非契约文档草图的完整签名**：内置工具已是 eino `tool.BaseTool`（Info=说明书、InvokableRun=执行），重造 `Name()/Schema()/Execute()` 会让装饰层双轨分叉、迁移风险陡增。行为不变优先：只叠加两个元数据方法，eino 执行面零改动，模型可见 ToolInfo 零变化。审计作废的"chi 切片"同样未采用——middleware 走 adk 原生 `WrapInvokableToolCall`，与 steer/toolsearch 同构。
2. **两个校验执行位、同一个 validator**：MCP 工具输出是 eino-ext 的 CallToolResult 包装串（解包规则特殊），保留 A1 装饰器位；内置工具输出是平台自己的文本（整体按 JSON 解析、失败按字符串文档），在 middleware 位校验。两位喂同一个 `OutputValidator`，违规错误格式同一 golden——A1 红利闭环而不重写管线。
3. **ConcurrencySafe 串行闸在 middleware、不用 ToolsNode 的 ExecuteSequentially**：全局开关会把安全工具也串行（过度保守）；middleware 按注册表逐工具判定——不安全工具共一把 run 级互斥锁（同轮绝不重叠），安全工具走 eino 原生并行。语义"安全可与非安全并行"是特性不是漏洞（只读/幂等与副作用并发无风险）。全链路测试用重叠度探针（80ms 执行窗口）断言 max 并发度。
4. **快照语义理顺为"引擎权威、dispatcher 占位"**：dispatcher 派发期只写 resolved 占位（workers/ 非 C1 独占文件，未动）；引擎组装完注册表后一次性覆盖——`tools` 键是全量工具面（一次写全），`mcp_servers` 键被桥接细化态（ok/ssrf/connect_failed）覆盖。A1 前的 `br.snapshot` 死数据就此有了消费者。**不搬死数据**：旧 resolved 快照在读端兼容（`TaskContextTools` 回落），不回写。
5. **B1 存档两级优先级**：协议自描述（实时握手）优先，存档只补未自描述工具；最终生效值同时供装饰器/注册表/快照三处。端口失败不阻塞任务（快照是观测面不是控制面）。
6. **内置工具契约锚定形状不锚定文案**（`{"type":"string"}` 而非 const）：toolFail/拒绝串与成功串同为 string，锚定文案会在改文案时误伤；改结构（升级为 JSON 输出）才被拦——这正是契约演进闸门的用意。豁免（nil schema）只给了 request_approval（人类决定转述）与 search_workspace（动态截断文本）两个纯文本答复类工具。

## 三、测试结果（实际命令输出摘要，2026-10-11 Windows + Git Bash）

```
$ go build ./...
（无输出 = 全仓编译通过）

$ go test ./internal/orchestration/adapters/engine/ -count=1 -v | grep -cE "^--- PASS"
81          # engine 包 81 个测试全部 PASS（既有 67 + C1 新增 14），0 FAIL

$ go test ./... -count=1 | grep -c "^ok"
48          # 全仓 48 包 ok；唯一 FAIL 见坑 #1（C2 未跟踪测试夹具 bug，非 C1）

$ golangci-lint run
0 issues.  # 仅 stdlib goarch typechecking error（任务书注明的环境噪音）
```

新增 14 测试四组：①校验管线五态（含全链路 `TestEngineRun_BuiltinOutputContractEnforced_EndToEnd`：合法透传 + 违规 validator 注入下模型看到 A1 标准违规串）；②fail-closed（注册表分型 + middleware 层三态 + 全链路零重叠/并行保留双态）；③context.tools（双键形状/新键优先/旧键回落/引擎回写）；④B1 存档（两级优先级真跑 fake MCP server + 未接线回落）。

## 四、留了什么坑（诚实清单）

1. **`internal/orchestration/adapters/http/handlers_uientry_test.go` 的 `TestUIEntrySlotsContract` FAIL 非 C1 造成**：该文件是 C2 的未跟踪新文件（`?? ` 状态），其夹具在 "legacy PUT" 子测试断言 `repo.stored` 长度 2，但同测试内更早的 "PUT without slots" 子测试已把 plainpack 入库（长度 3）——C2 自己的夹具 bug，C1 未触碰 http 包任何文件（`git diff` 可证）。C2 修夹具或重排子测试即绿。
2. **toolsearch 检索工具未进注册表 → 被串行闸过度保守处理**：>24 工具降级路径下模型经 toolsearch 注入的检索工具未登记（fail-closed 按不安全串行）。只读检索被串行是过度保守非风险；后续把它显式登记为 safe 即可（注册表已有通道）。
3. **MCP 工具的 ConcurrencySafe 声明通道未建**：v0 一律串行（含全部只读 MCP 工具）。pack.yaml 声明通道（如 `mcp.tools[].concurrency_safe`）留给 v1.1+；届时只改 `registerMCP` 的入参来源。
4. **`mcpServerToolOwners` 用前缀最长匹配归属工具→服务器**：MCP 快照按服务器聚合（无逐工具清单），归属只靠 `<server>_` 前缀推断（server 名含下划线时取最长命中）。归属仅用于快照展示不参与判定；根治需把桥接快照升级为逐工具清单（届时 `br.snapshot` 结构重构）。
5. **AuditLog 复用主 logger handler**（`With("channel","audit")`）而非独立文件：v0 够用（结构化字段可区分）；若审计需要独立留存周期/出口，在 wiring 换 handler 即可，engine 侧零改动。
6. **B1 存档每任务组装拉一次全量**（`ListPackToolSchemas`）：当前无缓存。workspace 存档行数 = pack×工具数（个位数量级），pg 单查询可忽略；若 D2 上板后组装延迟可测地涨，加进程内短 TTL 缓存（模式照抄 mcpCatalog）。
7. **dispatcher 的 resolved 占位快照仍写旧单键**（`MCPContextSnapshot`）：workers/dispatcher.go 不在 C1 独占文件清单内未动。语义上无冲突（引擎执行后覆盖），但"排队中取消"的任务 context 里只有占位无 tools 键——前端读端已兼容。后续若 workers 归属空窗可顺手把 dispatcher 也改双键（一行）。

## 五、context.tools 最终形状（一句话）

`task.context.tools` 是引擎组装完统一注册表后一次性回写的全量工具清单数组，每元素 `{name, origin: builtin|mcp, server?, has_output_schema, concurrency_safe}`；读端新键优先、老任务回落 `mcp_servers` 服务器级聚合；引擎同时用桥接细化态覆盖旧键 `mcp_servers`（dispatcher 的 resolved 只作未执行任务的占位）——形状冻结见 `docs/interfaces-tool-registry-2026-10-11.md` §3。

## 六、边界确认

- 独占文件内完成：`internal/orchestration/adapters/engine/`（全部）、`ports.go`、`wiring_orchestration.go`、docs 新文件 2 个。
- 未动：`cmd/pack-provision/`、`migrations/`、`handlers_uientry.go`/`repo_uientry.go`/`service_uientry.go`（C2）、`web/`、`service_summary.go`/`service.go`（A2，只读）、B1 的 `service_packtoolschema.go`/`repo_packtoolschema.go`（只调用）、`workers/dispatcher.go`（非归属，见坑 #7）。
- 既有 engine/adk 测试全绿（81/81，未删任何既有测试）；零新增依赖；未做任何 git add/commit。
