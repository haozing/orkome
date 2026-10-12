# 接口冻结：job-handle 三件套安装期闸门 + pack 工具 schema 存档（B1）

> 日期：2026-10-11
> 状态：**接口冻结**（B2/C1/D1 只读本文档，不读 B1 实现）
> 归属工作流：B1（波 2）；下游消费者：B2（前端 JobHandleCard/JobStatusCard 形状判定）、C1（运行期把存档 schema 接进桥接层）、D1（agentblog MCP server 按此契约实现六工具）
> 需求来源：`pack-tool-contract-design-2026-10-11.md` §2.1/§2.2/§4.1；`pack-rollout-plan-2026-10-11.md` §2 B1；运行期接口见 A1 冻结文档 `interfaces-output-validator-2026-10-11.md`

---

## 0. 一句话语义

pack-provision 安装期新增两类硬闸门（与前缀校验同级别，violation = fail the install）：**output 闸门**（每个 MCP 工具要么协议自描述 outputSchema、要么 pack.yaml `mcp.tools` 声明，两者皆缺即拒装）与 **job-handle 三件套闸门**（pack 含 submit 形状工具则 `<pack>_job_status` 与 `<pack>_cancel_job` 必须齐全）；安装时把每个工具最终生效的 output schema 存档进库（迁移 1035），供波 3 C1 运行期消费。

## 1. JobStatus 固定 schema（存档形状，字段名冻结）

`<pack>_job_status` 工具的输出必须符合以下 JSON Schema（**字段名一个都不许改**，B2 的 JobStatusCard 形状判定以本节为准；顶层必须 `type: object`）。本节是三方的公共基准：D1（agentblog）实现 `*_job_status` 时照此自描述；B2 据此渲染判定；库表 `source` 列的预留值 `builtin_default` 未来注入的也是这份文档（B1 本身不写入它，见 §3.4）。

```json
{
  "type": "object",
  "required": ["job_id", "state"],
  "properties": {
    "job_id": { "type": "string", "minLength": 1 },
    "state": { "type": "string", "enum": ["queued", "running", "awaiting_review", "published", "failed"] },
    "stage": { "type": "string" },
    "result": {
      "type": "object",
      "properties": {
        "url":     { "type": ["string", "null"] },
        "summary": { "type": ["string", "null"] }
      },
      "additionalProperties": true
    },
    "metrics": { "type": "object", "additionalProperties": true },
    "error": {
      "type": ["object", "null"],
      "required": ["code", "retryable", "stage_failed"],
      "properties": {
        "code":        { "type": "string" },
        "retryable":   { "type": "boolean" },
        "stage_failed": { "type": ["string", "null"], "enum": ["pre_publish", "post_publish", null] }
      }
    }
  },
  "additionalProperties": true
}
```

要点（与契约文档 §2.1 逐字对齐）：

- 必填：`job_id`、`state`。`state` 枚举五值：`queued | running | awaiting_review | published | failed`。
- `stage` 自由文本（当前管线阶段），可选。
- `result` 为对象（可为 null 的 url/summary 字符串；`additionalProperties: true` 允许 pack 扩展字段）。
- `metrics` 对象，各 pack 自定义（质检指标等），可选。
- `error` 存在时必填三键：`code`（string）、`retryable`（bool）、`stage_failed`（`pre_publish | post_publish | null`，失败时必填于 pre/post 语义——`post_publish` 禁自动重试）。
- `additionalProperties: true`（顶层与 result/metrics）——存档 schema 用于"最小形状判定"，pack 可加自己的扩展键，不算违规。

## 2. pack.yaml v1：`mcp.tools` output 声明段（yaml 形状冻结）

`apiVersion: pack/v1` 在 v0 全部字段基础上，仅新增 `mcp[].tools` 段（v1 其余字段与 v0 完全一致；`ui.slots` 允许出现、B1 静默忽略——slots 解析落库是 C2/迁移 1036 的归属）：

```yaml
apiVersion: pack/v1
name: agentblog
version: 0.1.0
mcp:
  - name: agentblog
    transport: streamable_http
    url: https://agentblog.internal/mcp
    tools:                        # v1 新增段；可省略（省略 = 全依赖协议自描述）
      - name: evaluate_seed       # 工具名，必须带 pack 前缀（与前缀闸门同规则）
        output: |                 # 该工具的输出 JSON Schema（YAML 块标量，解析为 JSON 对象）
          {"type":"object","properties":{"score":{"type":"number"}},"required":["score"]}
      - name: agentblog_job_status
        # 省略 output = 该工具依赖协议自描述（自描述也缺 = 拒装，见 §4.4）
```

冻结约束：

- `tools[].name` 必填且必须以 `<mcp.name>_` 前缀开头（否则安装失败：声明了不存在的形状即契约错误）。
- `tools[].output` 可省略；提供时必须是**JSON Schema 对象**（YAML 块标量或映射均可，unmarshal 后顶层为 JSON object 且非空，否则安装失败）。
- `mcp.tools` 段仅 v1 生效；v0 pack 写了 `tools:` 段会被非 strict 解析静默忽略（v0 行为完全不变）。
- 协议自描述与 pack.yaml 声明**同时存在时不冲突、不合并**：协议自描述优先存档（§4.4 优先级）。

## 3. 三闸门精确判定语义（安装期，全部 fail the install）

三闸门搭在既有前缀握手路径上：install 时 provision 用 mcp-go 客户端直连每个 `mcp[]` server，完成 Initialize + ListTools，一次握手依次判定。

### 3.1 前缀闸门（既有，v0/v1 不变）

每个工具名必须以 `<mcp-name>_` 开头。`--no-prefix-check` 豁免（legacy server）。**注意：豁免仅免前缀检查，不免 §3.2/§3.3 新闸门。**

### 3.2 output 闸门（v1 新增；v0 免检）

对 ListTools 返回的**每个**工具 `t`，判定"是否有输出契约"：

```
自描述 = t.OutputSchema.Type != ""（mcp-go Tool 结构的 outputSchema 字段，非空即算）
声明   = pack.yaml mcp[].tools[] 中存在 name == t.Name 的条目 且 该条目 output 非空
有契约 = 自描述 OR 声明
任一工具 有契约 == false → fail the install（错误信息含工具名与两级来源提示）
```

- 边界：server 暴露了工具但 pack.yaml 未声明该工具名 → 该工具仍按上式判定（只要自描述即过）；pack.yaml 声明了 server 不存在的工具名 → §2 的前缀约束之外，属 pack 契约错误，**fail the install**（拼写防呆）。
- `--no-prefix-check` **不豁免** output 闸门（output 契约与前缀纪律正交）。

### 3.3 job-handle 三件套闸门（v1 新增；v0 免检）

按契约 §2.1 命名约定，对每个 `mcp[]` server（前缀 `P = <mcp-name>_`）：

```
submit 工具集 S = ListTools 中所有匹配 P + "submit_" + 任意后缀 的工具
（如 agentblog_submit_generation；P+"submit_" 本身也算形状工具）
三件套另两件：
  J = P + "job_status"    （如 agentblog_job_status）
  C = P + "cancel_job"    （如 agentblog_cancel_job）
判定：
  |S| >= 1 且 (J 不在 ListTools 或 C 不在 ListTools) → fail the install
  |S| == 0 → 无长任务形状，不要求 J/C（同步 pack 不被迫造空壳）
```

- 判定只看 **server 实际暴露的工具名**（ListTools 结果），不看 pack.yaml 声明——三件套是 server 侧能力齐不齐，不是声明齐不齐。
- J/C 与 S 可分布在不同 `mcp[]` server 吗？**不可**——三件套按单 server 前缀判定，`P` 由该 server 自己的名字决定（pack 纪律：前缀 = server 名）。
- `--no-prefix-check` **不豁免**本闸门。

### 3.4 存档写入（闸门全过后）

闸门通过后，对每个工具计算**最终生效 schema** 并存档（迁移 1035，表 `pack_tool_schema`）：

```
最终生效 schema = 协议自描述 outputSchema（优先，原样存档，source='protocol'）
              OR pack.yaml mcp.tools[].output（未自描述时补位，source='pack_yaml'）
```

说明：存档 source 仅有以上两值会被 B1 写入；表 CHECK 里的 `builtin_default` 是**预留值**（B1 永不写入——§3.2 闸门已保证"两者皆缺"的工具根本装不进来，v0 又不存档；留给后续版本在运行期兜底注入 §1 标准文档时使用）。

存档为幂等 upsert（重装覆盖，以最新握手为准）；uninstall 删除该 pack 全部存档行。

## 4. 版本双放行与 v0 不变量

- `apiVersion` 接受 `pack/v0` 与 `pack/v1`，其余值拒装（错误信息列出两个合法值）。
- **v0 行为完全不变**：无 output 闸门、无三件套闸门、无存档写入（v0 pack 工具 schema 不进 1035 表——存档语义是 v1 契约的一部分）。这保证既有 e2e（`TestProvisionE2E`）零改动照常绿。
- v1 pack 的 `ui.entry` 语义与 v0 一致（单数，不数组化）；`ui.slots` 段出现即可、B1 静默忽略。

## 5. 迁移 1035：`pack_tool_schema` 表（形状冻结）

```sql
CREATE TABLE pack_tool_schema (
  id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  workspace_id  uuid NOT NULL REFERENCES workspace(id),
  pack          text NOT NULL,            -- pack 名（= mcp server 前缀基名）
  server        text NOT NULL,            -- mcp[] 条目名
  tool_name     text NOT NULL,            -- 含前缀全名（= 运行期桥接层 toolName 键）
  output_schema jsonb NOT NULL,           -- 最终生效 schema（§3.4 优先级）
  source        text NOT NULL,            -- 'protocol' | 'pack_yaml' | 'builtin_default'
  installed_at  timestamptz NOT NULL DEFAULT now(),
  UNIQUE (workspace_id, pack, server, tool_name)
);
```

- `source` 两值由 B1 写入：`protocol`（协议自描述）/ `pack_yaml`（声明补位）；`builtin_default` 为预留值（见 §3.4 说明，B1 永不写入）。
- down 成对：`DROP TABLE IF EXISTS pack_tool_schema;`。

## 6. C1 读取 API（波 3 运行期桥接层接入点，签名冻结）

C1 **只调以下读取函数，不碰 provision**（详细交接见 `docs/handoff-b1-job-gate-2026-10-11.md`）：

```go
// 包：orkome/internal/orchestration/app（service_packtoolschema.go）
// 实现在 internal/orchestration/adapters/pgrepo/repo_packtoolschema.go

type PackToolSchema struct {
    WorkspaceID  string
    Pack         string
    Server       string
    ToolName     string          // 含前缀全名 = 桥接层 toolName
    OutputSchema map[string]any  // JSON Schema 对象
    Source       string          // protocol | pack_yaml | builtin_default
    InstalledAt  time.Time
}

// ListPackToolSchemas 返回 workspace 下全部 pack 工具 schema 存档
// （C1 组装期一次拉全量，自建 toolName→schema map；无存档返回空切片不报错）。
func (s *PackToolSchemaService) ListPackToolSchemas(ctx context.Context, ws string) ([]PackToolSchema, error)

// GetPackToolSchema 按 toolName 精确取一条（ok=false = 未存档，C1 回落协议自描述/结构化探测）。
func (s *PackToolSchemaService) GetPackToolSchema(ctx context.Context, ws, toolName string) (PackToolSchema, bool, error)
```

优先级（C1 侧执行顺序，与 A1 冻结文档 §3 一致）：协议自描述（ListTools 实时）→ 本存档（未自描述工具补位）→ A1 结构化探测兜底。

## 7. B2 对齐要点（前端形状判定）

- JobStatus 卡片判定键：`job_id` + `state`（两键同在即认为是 JobStatus 形状）；state 五枚举值渲染（queued/running/awaiting_review/published/failed）。
- JobHandle（submit 返回）判定键：`job_id` 单键（或 `job_id` + 可选其他键、无 `state`）。
- Metrics 卡：`metrics` 对象存在即渲染（键值自定义）。
- 存档 schema 可经 HTTP `GET /api/workspaces/{ws}/pack-tool-schemas`（members 只读；响应 `{"items":[...]}`，item 形状＝`PackToolSchema` JSON）获取，供调试/前端兜底；写路径仅 provision 走 `PUT`（workspace admin）。
- 渲染器**不得**假设 `result/metrics/error` 必在（schema 中均为可选键）。

## 8. 边界与不做（B1）

- 不动 `internal/orchestration/adapters/engine/`（A1/C1 归属）、不改 `internal/orchestration/app|domain` 既有文件（A2 刚改过；只加新文件 `service_packtoolschema.go`）、不动 `web/`（B2/A3）。
- `ui.slots` 只保证不炸（非 strict yaml 静默忽略），解析落库是 C2 + 迁移 1036。
- 运行期校验（schema 怎么用）是 A1 框架 + C1 接线；B1 只负责安装期闸门与存档。
- 迁移只用 1035；不跑 migrate-up 到任何真实库。
