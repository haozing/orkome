# A1 交接笔记：MCP 工具输出契约强校验（2026-10-11）

> 工作流：A1（波 1 · 关键路径起点）｜需求：`pack-tool-contract-design-2026-10-11.md` §2.2｜任务书：`pack-rollout-plan-2026-10-11.md` §1
> 下游：B1（provision schema 闸门）、B2（前端形状判定）、C1（内置工具注册表接入）
> **接口冻结文档：`docs/interfaces-output-validator-2026-10-11.md`（B2/C1 只读它，不读实现）**

---

## 一、改了什么

### 新增文件（3 个，均在 `internal/orchestration/adapters/engine/`）

| 文件 | 内容 |
|---|---|
| `output_validator.go` | 冻结接口 `OutputValidator`/`Input`/`ViolationError` + v0 默认两级实现（`NewDefaultValidator`：协议自描述 schema 优先 → 结构化探测回落）+ 自研 JSON Schema 子集校验器（type/properties/required/items/长度/数值区间/pattern/enum/const/anyOf/oneOf/allOf/`#/$defs` 内部 $ref）+ 标准违规错误格式（`ModelFacingText`，golden 冻结） |
| `output_validator_pipeline.go` | 执行管线：`wrapWithValidator` 装饰器 wrapping eino `tool.BaseTool` 的 `InvokableRun`；`unwrapCallToolResultDocument` 解包 eino-ext 序列化的 CallToolResult 包装串；违规 → 标准错误串给模型 + 结构化审计日志（slog，含包装串全文） |
| `output_validator_test.go` | 五态覆盖 + golden + 边界用例 + **真跑 fake MCP server 的端到端**（六工具夹具过 buildMCPTools 全路径） |

### 修改文件（3 个）

| 文件 | 改动 |
|---|---|
| `tools_mcp.go` | ① `mcpConnect` 在 eino-ext `GetTools` 之外**自己再发一次 `cli.ListTools`** 回收每个工具的 `outputSchema`（`RawOutputSchema` 原样 / 结构化形态重序列化，`collectToolOutputSchemas` + `toolOutputSchemaRaw`）→ toolName→schema 注册表映射；② `mcpCacheEntry` 增 `schemas` 字段随缓存复用；③ `buildMCPTools` 装配处每个工具包 `wrapWithValidator` 装饰器；④ 新增 `outputValidator()`/`auditLogger()` 装配读取 helper |
| `engine.go` | 仅两处：`EinoEngine` 结构体加 `OutputValidator OutputValidator` 与 `AuditLog *slog.Logger` 字段（+`log/slog` import）。**装配段（221-260）一行未动**（C1 归属） |
| `registry_test.go` | 两处 `mcpConnect` 调用适配新返回值签名（`tools, _, err :=`） |

### 文档（2 个）

- `docs/interfaces-output-validator-2026-10-11.md` —— 接口冻结（签名/管线位置/两级实现/违规错误格式精确示例/B1 接入方式/schema 子集与不支持关键字清单）
- 本笔记

## 二、为何这么改（关键决策）

1. **装饰器而非 middleware**：C1 的结论是审批/审计管线走 adk 原生 `ChatModelAgentMiddleware`，但 A1 落点在桥接层且**不能动 engine.go 装配段**——在 `buildMCPTools` 里 wrap 每个工具是唯一零侵入的挂点，且天然覆盖两条工具路径（≤24 静态合并、>24 toolsearch 动态注入——toolsearch 把 `DynamicTools` 原样交给 ToolsNode，装饰不丢）。
2. **自研 JSON Schema 子集校验器**：任务书预期零新增依赖，但审计核实 vendor 里**没有**任何校验器（`eino-contrib/jsonschema` 与 `invopop/jsonschema` 都只是结构定义/反射包，无 Validate 功能）。自研覆盖 MCP outputSchema 生态最常用关键字；**不支持的关键字（not/if-then-else/patternProperties/外部 $ref 等）fail-closed 判违规，绝不静默放行**。升级为完整校验器时只换 `output_validator.go` 的校验器内部，接口与错误格式不变。
3. **校验对象与 excerpt 语义**（真跑样例驱动，探针已删）：eino-ext 把 CallToolResult 整体序列化成串。待验文档选取顺序 = `structuredContent` 存在直接用 → 否则 `content[0].text` 解析 JSON。违规串的 `output_excerpt` 取**业务原文**（structuredContent 的 JSON 或 content 首段 text）而非包装串——模型要读懂的是"工具输出了什么"。
4. **违规以非 error 的 tool_result 返回**：装饰器返回 `(标准错误串, nil)`，eino ToolsNode 把它作为正常 tool 结果递给模型（kind 不变）——模型看到明确失败而非异常中断回合，符合任务书"错误以 tool_result 落 task_message"。
5. **工具自身失败（`isError:true` / 传输错误）不走校验**：eino-ext 已把它们变成 error 路径，失败本身已是模型可见的明确结果，校验只管"成功输出是否守约"。

### 实测协议样例（2026-10-11 fake server 真跑，供 B2 对齐）

- ListTools 返回的 Tool JSON 自带 `outputSchema`（无 schema 的工具无此键）——eino-ext `GetTools` 会丢弃，A1 的 `collectToolOutputSchemas` 自己回收。
- 成功输出包装串：`{"content":[{"type":"text","text":"{\"job_id\":\"j-1\"}"}],"structuredContent":{"job_id":"j-1"}}`；纯文本：`{"content":[{"type":"text","text":"pong"}]}`。

## 三、测试结果（实际命令输出摘要）

```
$ go build ./...
（无输出 = 全仓编译通过）

$ go test ./internal/orchestration/adapters/engine/ -count=1 -v | grep -cE "^--- (PASS|FAIL)"
67          # engine 包 67 个测试全部 PASS，0 FAIL

$ go test ./... -count=1 | grep -c "^ok"
49          # 全仓 49 个包全绿，无 FAIL 行（make test 等价）

$ go vet ./internal/orchestration/adapters/engine/...
（无输出 = 通过）
```

五态覆盖（`TestMCPBridgeOutputContract_EndToEnd`，fake MCP server 真跑全路径）：
① 有 schema 合法 → 放行透传包装串；② 有 schema 违规（缺 required）→ `schema mismatch`；③ 无 schema 合法 JSON object（text 内 JSON）→ 放行；④ 无 schema 合法 JSON 标量 → `unstructured text`；⑤ 无 schema 纯文本/畸形 JSON → `malformed json`。golden：`TestViolationErrorModelFacingTextGolden`（精确串断言）+ 500 字符截断测试（`TestViolationErrorExcerptTruncation`）。审计断言：违规各一条、含包装串全文 `raw_output`、通过的工具不记。

## 四、留了什么坑

1. **golangci-lint 本机未安装**（`make lint` 不可执行；GOBIN 里只有 oapi-codegen/sqlc）。已用 `go vet`（绿）+ CI 的 `.golangci.yml` 兜底；**建议 CI 或装 lint 后复跑**。另注：本仓 Go 源文件普遍为 CRLF 行尾，`gofmt -l` 会全文误报（含未改动的既有文件），非 A1 引入。
2. **并行改动说明（非本包失败，未修）**：A1 会话期间队友（A2）持续改 `internal/orchestration/app`、`pgrepo`、`workers`、`summarizer`（工作区可见）。期间出现过短暂的 `arch` 依赖法失败（app import summarizer / domain 自引用）与编译失败（summarizer 包暂缺）——均为 A2 中间态，**A2 会话自行收敛后全仓测试转绿**，A1 未触碰这些包的逻辑（仅 app 包 3 处 `FailRun` 调用为恢复编译补了空串实参 `""`，与 A2 自己在测试桩里的补法一致，A2 落地真正 summary 参数时会被其测试覆盖）。若后续看到这几个文件的冲突，归属 A2。
3. **无 schema 工具的回落会拦掉"合法的纯文本工具"**：v0 设计即如此（结构化 JSON 探测，任务书明文）。存量 MCP 工具若只返回纯文本，升级后会被判违规——这是**故意的平台强制**（B1 之后未声明的工具在安装期就过不了），但升级公告要提醒 pack 作者给工具补 outputSchema 或结构化输出。
4. **schema 子集校验器不支持的 schema 形态按违规 fail-closed**（含 `additionalProperties` 为 schema 形态、外部 `$ref`、`not` 等）——遇到复杂 schema 的 pack 会假阴性（合法输出被判违规）。若真实 pack 出现，替换 `output_validator.go` 校验器内部为完整实现即可（接口冻结不受影响）。
5. **装饰器只包了 InvokableTool**：eino-ext MCP 工具全部可 Invokable，流式工具暂无校验路径（`wrapWithValidator` 对非 Invokable 原样返回）——当前无现实影响，C1 注册表迁移时留意。
6. **`structuredContent` 数组/对象之外的边角**：MCP 规范要求 structuredContent 与 schema 对应（object 形态），`unwrapCallToolResultDocument` 对非 object 的 structuredContent 也照单交给校验器（schema 会拦）；无 schema + structuredContent 为标量 → probe 拦。行为闭环，但依赖 server 端遵守规范。
7. **审计日志用 `slog.Default()` 兜底**（未装配 AuditLog 时）：违规会打默认日志而非丢失，但接线方（cmd/orkome-server）尚未显式装配专用 audit logger——C1/B1 接线时补。

## 五、冻结接口摘要（一句话版）

`OutputValidator`（单方法 `Validate(Input{ToolName, Schema json.RawMessage, Document any}) error`，`*ViolationError` 表示违规）+ `NewDefaultValidator()` 两级实现（协议自描述 schema 优先 → 结构化 JSON 探测回落）+ 标准违规错误 `tool output contract violation: <reason>\n{"code":"TOOL_OUTPUT_CONTRACT_VIOLATION","tool":...,"reason":...,"output_excerpt":<前500字符>}`，落在 `internal/orchestration/adapters/engine`，B1 经注册表注入 schema 或替换 validator 实现、C1 让内置工具走同一管线——详见 `docs/interfaces-output-validator-2026-10-11.md`。
