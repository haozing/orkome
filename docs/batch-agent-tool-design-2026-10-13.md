# Batch as Agent Tool —— 批量任务 v2.0：agent 的扇出执行通道

> 日期：2026-10-13
> 状态：设计定稿（用户裁决后实施）
> 原则：**无兼容包袱、数据架构干净；能 agent 完成的不麻烦人类。**
> 取代：v1 的"批量 tab 面板"定位（624d721，保留为用户入口之一）。本文把 Batch 的第一身份定为 **agent 的内部异步扇出工具**，绑定工作项执行，面板降级为同一实体的旁观察视图。

---

## 0. 问题与定位

### 0.1 驱动场景（用户原始需求）

500 张图片要 OCR。现实约束：

- OCR 能力是**已注册的 MCP 工具**（或提示词约定的 API）；
- 不能建 500 个子工作项——子工单是给人看管的（状态流转/评论/通知各一套生命周期），500 次机械调用没有一次值得单独对话；
- 不能让 agent 在对话里循环调 500 次——每轮都烧 token、上下文爆掉、失败恢复无从谈起；
- agent 需要在批次完成后**拿到结构化结果继续处理**（汇总、失败重试、写产出）。

### 0.2 定位

**Batch = agent 的异步扇出执行通道**：一次工具调用创建 N 条轻量执行单元（batch_item），后台并发跑，完成自动唤醒 agent 收割结果。token 账：500 条 = 1 次创建 + 1 次唤醒 + 1 次结果读取。

**三入口一实体**（同一 Batch 实体，不造第二套）：

| 入口 | 触发者 | 界面 |
|---|---|---|
| agent 工具 `batch_create` | 智能体（主路径） | 工具调用 |
| 工单批量 tab 面板 | 用户手动 | 已有（624d721），保留 |
| API（开放接入，远期） | 外部客户端 | PAT 通道 |

### 0.3 执行体（kind）目录

| kind | 执行体 | 版本 |
|---|---|---|
| `llm_comment` | 内置：真模型生成+回帖（已有，演示性质） | v2.0 保留 |
| **`mcp_tool`** | 逐条调用指定 MCP 工具（params 指定 server+tool） | **v2.0 核心**——OCR 等 MCP 能力的批量化就靠它 |
| `http_call` | 逐条调用 HTTP API（提示词约定的外部接口） | v2.1 |
| 自定义 | pack 经 BatchProcessor 注册的任意 kind | 机制已有（Registry） |

---

## 1. 核心设计一：双通道 payloads（数据怎么进批次）

### 1.1 问题的本质

大批量场景下，"agent 生成/转述 500 条入参"有两个致命伤：

1. **token 成本**：15k token 纯 JSON 挤进一轮对话，且这是纯转发零增值；
2. **对应性无保证**：LLM 复述长列表丢条目/改字段是统计规律——500 条丢 3 条，批次显示全成功，3 张图没被处理，**沉默失败**。

### 1.2 双通道设计

`batch_create` 的 payloads 支持两种互斥形态：

**通道 A：inline（小批量，≤20 条）**

```json
{ "kind": "llm_comment", "payloads": [{"seed": "主题一"}, {"seed": "主题二"}] }
```

数据已在对话上下文中（用户粘贴、agent 自产），agent 直接写。量小，LLM 转述可靠，server 原样落表。

**通道 B：source + template（大批量，数据不流经 LLM）**

```json
{
  "kind": "mcp_tool",
  "tool": "agentblog_ocr",                       // mcp_tool 专属：要扇出的 MCP 工具
  "source": {
    "tool": "list_attachments",                  // 数据源：一个 agent 工具面上已注册的工具
    "args": {"work_item_id": "{work_item_id}"},  // 参数，{work_item_id} 等占位符 server 代填
    "pick": "$.attachments[*]"                   // JSONPath：从返回中抽取元素数组（缺省=结果本身是数组）
  },
  "payload_template": {
    "image_ref": "{$.file_id}",                  // ← 从 source 元素的 file_id 字段取
    "lang": "zh"                                 // ← 字面量
  },
  "required": ["image_ref"]                      // 物化校验：这些字段缺一不可
}
```

**物化流水线（全部 server 侧机械执行，零 LLM 参与）：**

```
代调 source.tool（复用现有工具执行器，不经模型）
  → JSONPath 抽取元素数组（路径写错 → 立即报错，绝不沉默空数组）
  → 模板逐条代入（$.field 取值 + 字面量）
  → required 校验：缺失条目 → status=skipped + 跳过原因落 batch_item
  → 计数对账：batch.source_materialization = {source_count: 500, materialized: 497, skipped: 3}
```

### 1.3 对应性保证的机制来源

| 错位场景 | inline（通道A） | source（通道B） |
|---|---|---|
| 字段名不匹配 | agent 自己保证 | **显式映射**（`$.file_id → image_ref` 写在模板里，声明式、可审计） |
| 嵌套层级抄错 | 同上 | **JSONPath 精确定义**，错路径=报错非静默 |
| 条目丢失/换序 | ≤20 条 LLM 可靠 | **不存在**——server 代调直取，数据不过模型 |

对应关系存档于 `batch.source`（原文），事后可复盘"这批数据哪来的、怎么映射的"。

### 1.4 防呆：物化预览，先看后跑

`batch_create`（通道 B）**创建即物化但不开跑**（status=pending），返回：

```json
{ "batch_id": "...", "source_count": 500, "materialized": 497, "skipped": 3,
  "sample": [ /* 前 3 条成品 payload_ref */ ] }
```

agent 检查 sample（选择器/映射是否抽对）后 `batch_start(batch_id)`；面板通道（人类）同样先见预览再点开始。**沉默的错误扇出被结构上排除。**

> 实施注：现有 BatchWorker 已消费 pending 批次——改为「通道 B 创建后 status=pending 且不自动 claim，显式 start 才转 running」；通道 A 保持现状（inline 小批量自动开跑，省一步）。

---

## 2. 核心设计二：三个 agent 工具（batch 的工具面）

### 2.1 工具注册

引擎统一工具注册表（`registry_assembly.go` 的 `registerBuiltin` 既有通道），每任务注入三个：

| 工具 | 入参 | 返回 | 说明 |
|---|---|---|---|
| **`batch_create`** | kind, tool?(mcp_tool 必填), payloads? \| source+payload_template+required?, concurrency? | batch_id, source_count/materialized/skipped, sample | 绑定当前 task 的 work_item；通道 A 自动开跑，B 返回预览等 start |
| **`batch_status`** | batch_id | status, 四态计数, source_materialization 对账, last_error | 轻量查询，适合轮询前的先看 |
| **`batch_results`** | batch_id, status?(succeeded/failed/skipped), cursor?, limit(默认 20，上限 100) | 分页条目（payload_ref + result + error + attempts） | **结果收割的唯一通道**，分页设计防止 500 条结果灌爆上下文 |

`batch_results` 的结果形态按 kind 分层：`mcp_tool` 条目带工具完整返回（已在 batch_item.result）；`llm_comment` 带回帖评论 id。**不给"一次返回全部 500 条"的口子**——agent 需要全量汇总时自己翻页聚合，上下文始终可控。

### 2.2 完成唤醒（闭环的最后一环）

现状：`batch_done` 事件 + `NotifyBatchDone` 端口已存在（wiring_batch.go）。

v2.0 接线裁决：批次终态（completed/failed）→ **唤醒创建该批次的 agent**（originator task），唤醒 payload 携带 `{batch_id, done, failed, skipped}` 摘要——agent 醒来第一件事 `batch_status`/`batch_results` 收割。agent 若已不在运行态，唤醒建立新任务（复用现有回环机制，同 squad_harvest 模式）。

### 2.3 权限与绑定

- batch 强绑定 work_item（现有外键），agent 只能对**自己 task 的 work_item** 创建/查询（工具层取 task 上下文，不暴露跨单操作）；
- `mcp_tool` kind 的 tool 白名单 = 该任务生效的 MCP 工具面（复用 ResolveForEngine 的合并结果）——agent 不能扇出它看不见的工具；
- 大小限制沿用：内联 payload ≤4KB/条、批次 ≤100k 条、并发 ≤64。

---

## 3. 数据模型（干净版，无兼容负担）

现有 batch/batch_item 表**结构不变**（v0 设计即为此场景预留），仅两处增补：

```sql
-- 1043: 批量 v2.0
ALTER TABLE batch ADD COLUMN started_at timestamptz;                     -- 预览→start 的显式起点
ALTER TABLE batch ADD COLUMN source_materialization jsonb;               -- {source_count, materialized, skipped}
ALTER TABLE batch_item ADD COLUMN skip_reason text;                      -- 物化校验失败原因（status=skipped 时）
```

- `batch.source`（已有 jsonb）存通道 B 的 source+template 原文——审计与重放依据；
- `batch_item.payload_ref`（已有 jsonb）存物化后的成品入参——**物化发生在创建期**，执行期 worker 拿到的就是成品，worker 不感知双通道；
- status 枚举增补：batch_item 增加 `skipped`（物化跳过，区别于执行失败的 failed）——查 domain/batch.go 的 CHECK 约束同步。

**刻意不做**：source 型的二次引用（call_ref）、payload 模板继承、跨批次数据管道。有真实场景再加，v0 不预支抽象。

---

## 4. `mcp_tool` kind 执行器

```
worker claim 条目 → 从 batch.params.tool 定位 MCP 工具（解析当前 ws+agent 的
生效工具面）→ 以 payload_ref 为入参调用（复用 buildMCPTools 桥接链：SSRF/
缓存/输出 schema 校验全继承）→ 结果落 batch_item.result
```

- **并发安全**：MCP 工具默认非并发安全（registry 的 concurrencySafe=false 口径），扇出并发由 batch 的 concurrency 参数控制（默认 4）；
- **失败语义**：工具报错 → 条目 failed + attempts++（max 3 重试，现有机制）；工具返回业务错误码 → 同样 failed（result 里带错误体）；
- **限速**：继承 batch 的 pace_interval_seconds（现有列），对 agentblog 这类带速率的外部服务友好。

## 5. 面板（人类入口）同步

批量 tab 增补（量小，纯前端）：

- 创建对话框加"从数据源生成"模式：选 source 工具（下拉=该工单 agent 工具面的只读枚举）+ JSONPath + 模板编辑器 + required 勾选；创建后同样先见物化预览 + 「开始执行」按钮；
- 批次卡片显示 source_materialization 对账（500→497+3）；
- skipped 条目在展开列表可见跳过原因。

## 6. 实施切分

| 阶段 | 内容 | 规模 |
|---|---|---|
| **P1** | 三工具注册（引擎 builtin 通道）+ 迁移 1043 + 通道 A（inline）+ batch_status/results 分页 + 预览→start 状态机 | 后端为主，2 天级 |
| **P2** | source 物化器（代调工具+JSONPath+模板+校验）+ `mcp_tool` 执行器 + batch_done 唤醒接线 | 后端核心，2-3 天级 |
| **P3** | 面板"从数据源生成"模式 + 对账展示 + `http_call` kind | 前端+执行器，1-2 天级 |

P1 完成即可用 `llm_comment` 验证全链（agent 创建→唤醒→收割）；P2 完成即支撑 OCR 场景。

## 7. 验收清单（P2 全量）

- [ ] agent 调 `batch_create`（source 通道）创建 500 条 OCR 批次：创建返回 source_count=500/预览 sample，条目落表，LLM 上下文未出现 500 条列表（token 断言）
- [ ] `batch_start` 后 worker 按 concurrency=4 并发调 MCP 工具，结果逐条落 batch_item.result
- [ ] required 字段缺失的条目 → skipped + skip_reason，对账计数 500=497+3
- [ ] 批次终态 → 原 agent 被唤醒且 payload 带 batch_id → `batch_results` 分页收割 → 继续处理（真模型 E2E）
- [ ] 人类经面板创建同一批次 → 同一实体、同工具面可查（三入口一实体）
- [ ] 无指派 agent / 工具不在白名单 / JSONPath 无命中 → 创建期 fail-fast（带原因），不产生半物化批次

## 8. 边界与不做

- Batch 不做跨工作项复用（绑定即终身，跟随工单删除策略）；
- 不做批次间依赖编排（"A 完成触发 B"是小队/autopilot 的地盘，batch 只做单层扇出——编排权纪律同 events inform never orchestrate）；
- 不做实时进度推送 agent（agent 靠唤醒+主动查，推送是面板 WS 的事，已有）；
- 500 条结果不提供一次性全量返回（分页上限 100，保护上下文）。
