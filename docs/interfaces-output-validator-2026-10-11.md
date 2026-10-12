# 接口冻结：OutputValidator —— MCP 工具输出契约强校验（A1）

> 日期：2026-10-11
> 状态：**接口冻结**（B2/C1 只读本文档，不读 A1 实现）
> 归属工作流：A1（波 1）；下游消费者：B1（provision 声明 schema 接入）、B2（前端形状判定语义对齐）、C1（内置工具注册表接入校验管线）
> 需求来源：`pack-tool-contract-design-2026-10-11.md` §2.2；`pack-rollout-plan-2026-10-11.md` §1 A1

---

## 0. 一句话语义

MCP 工具的输出从"插件自觉"变为"平台强制"：桥接层在工具执行后按 schema 校验输出，**非法输出不进 task_message**，模型看到的是平台生成的标准失败（错误码 `TOOL_OUTPUT_CONTRACT_VIOLATION`），而非幻觉结构；原文全文只进审计日志。

## 1. 接口签名与所在包

包：`internal/orchestration/adapters/engine`（与 MCP 桥接层同包，文件 `output_validator.go`）。

```go
// OutputValidator 校验单个 MCP 工具调用的输出是否符合其输出契约。
//
// 管线位置：装饰器 wrapping eino tool.BaseTool 的 InvokableRun —— 工具执行
// 成功之后、结果进入模型上下文/task_message 之前。校验失败时返回标准违规
// 错误串（见 §4），模型看到的是失败而非未验证的结构。
//
// Input 是校验输入的自描述快照：已经过 MCP 桥接层解包的待验 JSON 文档、
// 该工具注册的输出 schema（nil 表示未注册）与工具名。
type OutputValidator interface {
    Validate(vi Input) error
}

// Input 是一次校验的输入。
type Input struct {
    ToolName string          // 工具名（含 pack 前缀，用于违规报告与审计）
    Schema   json.RawMessage // 工具的输出 JSON Schema；nil/空 = 未注册 schema
    Document any             // 待验文档：结构化结果（通常为 map[string]any）
}
```

- `Validate` 返回 `nil` = 放行；返回 `*ViolationError` = 判违规（标准错误格式见 §4）；其他错误 = 校验器自身故障（同样按违规处理，fail-closed）。
- **Go 惯例**：接口单方法 + 输入结构体，B1/C1 可提供自己的实现替换或叠加，无需改动调用方。

### v0 默认实现（两级）

```go
// DefaultValidator 是 v0 的两级实现：
//   第一级 schemaValidator —— 注册了 schema 的工具：JSON Schema 校验
//     （v0 自研子集校验器，见 §5；零新增依赖）；
//   第二级 structuredProbe —— 未注册 schema 的工具：结构化 JSON 探测，
//     Document 必须是合法 JSON 解析出的 object/array（map/slice），
//     纯文本（string）或畸形 JSON → 违规。
// schema 存在即只走第一级（schema 本身已蕴含"必须是结构化 JSON"）。
func NewDefaultValidator() OutputValidator
```

## 2. 管线位置（运行期全景）

```
buildMCPTools（每任务组装）
  ├─ newMCPClient → mcpConnect：
  │    ① cli.ListTools → mcp-go 原样返回每个工具的 outputSchema
  │       （★协议层自描述——mcp-go Tool 结构自带，eino-ext GetTools 丢弃，
  │         orkome 在自己的桥接层回收）
  │    ② 建 toolName → outputSchema(RawMessage) 注册表，随 mcpCatalog 缓存
  │    ③ einoextmcp.GetTools 得到 eino BaseTool
  └─ 每个工具包一层 validatingTool（装饰器，实现 eino tool.BaseTool）：
       Info() 原样透传（ToolInfo 无输出 schema 字段，不塞）
       InvokableRun() = 原工具执行 → 解包 CallToolResult 包装串 → 取
         structuredContent（无则 content[0].text 解析 JSON）→ validator
         .Validate → 通过：原结果返回；违规：返回标准违规错误串 + 审计日志
```

关键点（B2 对齐用）：

- **进入模型的 MCP 工具成功结果是 eino-ext 序列化的 CallToolResult 包装串**，实测样例（2026-10-11，fake server 真跑）：
  - 带结构化输出：`{"content":[{"type":"text","text":"{\"job_id\":\"j-1\"}"}],"structuredContent":{"job_id":"j-1"}}`
  - 纯文本：`{"content":[{"type":"text","text":"pong"}]}`
  - （`isError:true` 时 eino-ext 直接走 error 路径，不产出成功结果串）
- **校验文档的选取顺序**：`structuredContent` 存在 → 直接用它（已是 any，通常 map）；否则取 `content[0]` 的 text 字段 `json.Unmarshal`——解析失败即违规（v0 无 schema 工具同此判定）。
- **违规后模型看到的不是包装串**，而是 §4 的标准错误文本（eino ToolsNode 会把工具 error 包装为 tool_result 递给模型，kind 不变）。
- 装饰器在两条工具路径都生效：≤24 工具的静态合并、>24 的 toolsearch 动态注入（toolsearch 把 `DynamicTools` 原样交给 ToolsNode，装饰不丢）。

## 3. B1 如何接 provision 声明的 schema

B1（pack.yaml `mcp.tools[].output` 声明 + 迁移 1035 存档）接入点有两处，均不改本接口：

1. **优先级**：协议自描述 schema 优先；pack.yaml 声明仅在工具未自描述时生效（与 `pack-tool-contract-design` §2.2 的倒置优先级一致）。B1 在其 provision/桥接侧完成"两者取一"，把最终生效 schema 交给注册表即可。
2. **接入方式（二选一，B1 定）**：
   - 复用注册表：B1 存档的 schema 在桥接组装时注入 toolName→schema 映射（对未自描述工具补条目）；`OutputValidator` 与管线零改动；
   - 或替换 `EinoEngine` 侧 validator 构造（`NewDefaultValidator` 换成 B1 的组合实现，如 `schema 校验 + provision 存档回落`）。
3. **安装期闸门**是 B1 自己的职责（"要么协议自描述、要么 pack.yaml 声明，皆缺 = fail the install"），与运行期框架正交。

C1（统一注册表）后续把内置工具的 `OutputSchema()` 经同一 `OutputValidator` 管线校验——接口不变，注册表成为 schema 的另一供给方。

## 4. 标准违规错误格式（golden 冻结）

违规时工具返回以下**纯文本**（一行 summary + JSON body；JSON 各键固定、顺序不保证；`output_excerpt` 为原文前 500 字符，原文长度不足 500 则全量）：

```
tool output contract violation: <原因短语>
{"code":"TOOL_OUTPUT_CONTRACT_VIOLATION","tool":"<工具名>","reason":"<原因短语>","output_excerpt":"<原文前 500 字符>"}
```

`reason` 枚举（v0 冻结，B2/C1 可依赖）：

| reason | 含义 |
|---|---|
| `structured content missing` | 工具注册了 schema 但结果无 structuredContent 且 content 不是合法 JSON |
| `schema mismatch: <详情>` | 有 schema，文档解析成功但不符合 schema（详情为违反的关键字/路径简述） |
| `malformed json` | 无 schema 工具，content 文本不是合法 JSON |
| `unstructured text` | 无 schema 工具，content 是合法 JSON 标量/字符串（非 object/array） |

精确示例（golden 测试锚定）：

```
tool output contract violation: malformed json
{"code":"TOOL_OUTPUT_CONTRACT_VIOLATION","tool":"demo_ping","reason":"malformed json","output_excerpt":"pong (not json)"}
```

审计日志（结构化，slog，模型永不可见）：工具名、reason、原文全文（键 `raw_output`）。

## 5. v0 schema 校验子集（自研，零新增依赖）

仓内 vendor 无通用 JSON Schema 校验器（eino-contrib/invopop 均只是结构定义包），A1 自研编译期校验器，支持 MCP outputSchema 生态最常用关键字子集：

- 类型：`type`（object/array/string/number/integer/boolean/null，含类型数组）
- 对象：`properties` / `required` / `additionalProperties`(bool)
- 数组：`items` / `minItems` / `maxItems`
- 字符串：`minLength` / `maxLength` / `pattern`（Go regexp）
- 数值：`minimum` / `maximum` / `exclusiveMinimum` / `exclusiveMaximum`
- 枚举/常量：`enum` / `const`
- 组合：`anyOf` / `oneOf` / `allOf`；`$ref` 仅支持同文档 `#/$defs/...` 内部引用
- 明确不支持（遇到即校验器报 `unsupported keyword` 按违规 fail-closed，不静默放行）：`not`、`if/then/else`、`patternProperties`、`propertyNames`、`contains`、外部 `$ref`、`format`

自研是 v0 权衡（依赖冻结 vs 覆盖面），升级为完整校验器时只需换 `schemaValidator` 内部实现，接口与错误格式不变。

## 6. 边界与不做

- A1 不动 engine.go 装配段（C1）、registry.go toolsearch 逻辑、cmd/pack-provision（B1）。
- 无 schema 工具的 v0 回落是**结构化 JSON 探测**（合法 object/array 放行）；B1 落地后未声明工具在安装期就被拦，运行期回落仅兜底。
- eino `schema.ToolInfo` 无输出 schema 字段，schema 一律存 orkome 注册表侧（桥接的 toolName→schema map），不进模型可见 ToolInfo。
