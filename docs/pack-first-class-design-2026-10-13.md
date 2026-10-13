# Pack 一等公民化 —— 信息架构升级与开发者接入设计

> 日期：2026-10-13（v2：代码审计后重写——修正 3 处事实错误，架构从"字符串键+补丁列"升级为"pack 实体表聚合根"；审计发现以 ★ 标注）
> 状态：设计提案（待用户裁决后实施）
> 原则：**不考虑兼容、不背旧数据**（v0 无兼容原则）——迁移一次性建干净结构，装过的 pack 重装即迁入（agentblog 是唯一活例，重跑 provision 即可）。
> 关系：`pack-tool-contract-design-2026-10-11.md`（契约边界全盘继承）、`pack-frontend-design-2026-10-11.md`（slots/job-handle 已落地）、`pack-rollout-plan-2026-10-11.md`。本文解决三者未回答的问题：**Pack 在导航里算什么、用户怎么看见管理它、开发者怎么接进来、数据模型怎么建才干净**。

---

## 0. 问题定性：Pack 被当成了配置项，实际是生态单元

### 0.1 领域事实（代码实证）

一个 pack 经 `pack-provision install` 安装时发生 **6 件事**（`cmd/pack-provision/main.go:420-629`）：

| # | 动作 | 落点 | MCP 服务器有没有 |
|---|---|---|---|
| 1 | 注册 MCP 服务器（凭证 sealx 密封） | `workspace_mcp_server` | 有（这就是它的全部） |
| 2 | 三重契约闸门 + 工具 schema 存档（B1） | `pack_tool_schema` | 无 |
| 3 | 创建智能体 | `agent`（经 POST /api/agents） | 无 |
| 4 | 创建技能并绑定智能体 | `skill`（经 POST /api/skills） | 无 |
| 5 | 事件订阅真注册（HMAC / 五档重试 / 死信暂停） | ploykit `webhook_subscription` | 无 |
| 6 | UI 进驻：侧栏入口 + 工单 tab + ui_token 票证 | `pack_ui_entry` / `ui_token` | 无 |

**MCP 只有第 1 行；Pack 是 6 行的打包编排。** 边界一句话：**MCP 服务器是协议层的接线（工具单向 pull）；Pack 是产品层的应用（谁提供能力、以什么身份、界面长在哪、事件怎么流、怎么装卸）。**

### 0.2 ★ 审计发现：数据完整性三缺陷（v1 设计文档未察觉）

| # | 缺陷 | 证据 | 后果 |
|---|---|---|---|
| D1 | **技能来源错标**：skills HTTP 层强制 `in.Origin = "manual"`，provision 装的技能也被记成手工 | `internal/skills/adapters/http/routes.go:121` | pack 详情页无法列出"带来的技能"；卸载时按名误删用户手工技能的风险面 |
| D2 | **智能体无来源**：`Agent` 结构（`ports.go:41-56`）与 INSERT（`pgrepo/repo.go:50`）均无 origin/pack 列；provision 的 POST body 也不带 | 同左 | pack 资产不可枚举；uninstall 的 agents 归档按名匹配，同名手工 agent 会被误伤 |
| D3 | **订阅识别靠字符串**：pack 订阅以 `description="pack <name>"` 约定识别，webhook Subscription 结构无 pack 字段 | `vendor .../webhooks/app/model.go:15-23`（vendor 表，不可加列） | 详情页事件区块只能前缀匹配，脆且无法外键约束 |

**结论：v1 文档"资产区块现有 API 可查"的断言不成立**（技能错标、agent 无标记）。干净架构必须先修数据模型，前端页面才有干净数据可读。

### 0.3 根因：pack 是"散落 5 张表的字符串键"，没有聚合根

`workspace_mcp_server.pack`❌（无列）/ `pack_tool_schema.pack`（text）/ `pack_ui_entry.pack`（text）/ `webhook_subscription.description`（约定）/ `ui_token.plugin`（text）。v1 设计的 R1（加 `origin text` 列）只是第六处字符串补丁。**干净解法：建 `pack` 表做一等实体，全部关联走 FK。**

---

## 1. 数据模型（迁移 1042，一次性建干净）

### 1.1 pack 聚合根

```sql
CREATE TABLE pack (
  id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  workspace_id uuid NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
  name         text NOT NULL,                -- 如 agentblog
  version      text NOT NULL DEFAULT '',     -- pack.yaml version，重装覆盖
  installed_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE (workspace_id, name)
);
```

### 1.2 关联改造（改列 + FK，无回填——重装迁入）

| 表 | 改造 | 说明 |
|---|---|---|
| `workspace_mcp_server` | **加 `pack_id uuid NULL REFERENCES pack(id) ON DELETE SET NULL`** | NULL=手工注册。pack 删则置空（行保留，/tools 显示"来源已失联"），不 CASCADE——MCP 注册表行有独立生命周期 |
| `agent` | **加 `pack_id uuid NULL REFERENCES pack(id) ON DELETE SET NULL`** | 同上；archived 软删由 uninstall 显式做 |
| `skill` | **不加列**——已有 `origin`/`origin_ref`（`pgrepo/repo.go:24`），修 D1：provision 写 `origin='pack', origin_ref=<pack_id>` | HTTP 层放行 admin 调用者的 origin（§4.3） |
| `pack_ui_entry` | **`pack` text → `pack_id uuid FK`**（`UNIQUE(workspace_id, pack_id)`） | 入口随 pack 一一对应 |
| `pack_tool_schema` | **`pack` text → `pack_id uuid FK`，`ON DELETE CASCADE`** | 存档是 pack 的派生物，随删 |
| `webhook_subscription`（vendor 表，**不可改**） | orkome 侧建连接表 `pack_subscription(pack_id uuid FK CASCADE, subscription_id text, UNIQUE(pack_id, subscription_id))` | D3 的干净解；订阅本体删除仍走 vendor API |
| `ui_token.plugin` | **保持 text** | 60 秒单次消费票，嵌入式短令牌不值得 FK |

要点：**pack 行先建，后建资产**（provision 第 0 步 upsert pack，拿 pack_id 传给后续创建）。FK 方向统一"资产指向 pack"，删除语义分层：派生物 CASCADE（schema 存档）、独立生命体 SET NULL（MCP server / agent）、跨系统显式清理（webhook 订阅 + 连接表）。

### 1.3 聚合读取：`internal/pack` 新域模块

新模块 `internal/pack`（对齐 notification/skills 的模块划分），`PackService` 经端口聚合读（组合根接线，模式同 `MCPServerService.Resolved` / notification 的 `SubscriberReader`）：

```
GET /api/packs                            → 列表：name/version/installed_at + 资产计数
GET /api/packs/{packId}                   → 详情聚合：servers(含 pack_id) + tool_schemas
                                             + ui_entry/slots + subscriptions + agents + skills
PUT  /api/packs                           → provision upsert（admin；name+version+workspace）
DELETE /api/packs/{packId}                → P2：server 端卸载（显式清订阅/归档 agent/删 skill）
```

跨域读端口：MCP 注册表（orchestration）、agents（orchestration）、skills（skills 模块）、webhooks（ploykit admin repo 只读）。**不做跨模块 SQL 直查**——模块边界经端口保持（仓内既有纪律）。

---

## 2. 信息架构裁决案

### 2.1 导航：Pack 升一等公民，进 AI团队组

```
AI团队组：智能体 / 小队 / Skills / 能力包(Packs) / 工具集成 / 运行日志
底部段：  开放接入 / 自动化 / 设置（不动）
```

心智：**能力包是能力的来源，工具集成是接线板**。nav `nav.packs`=「能力包」（en: Packs）；路由 `/packs`、`/packs/$packId`。

### 2.2 两页分工与去重

- **`/packs`**：已装 pack 列表（名/版本/安装日期/资产计数）→ 详情；空态给 provision 命令引导。
- **`/tools`**：MCP 注册表每行加**来源**：`pack_id` 非空的行只读 + 「来自能力包 →」链接详情；NULL 行现有 CRUD 不变。

### 2.3 Pack 详情页（/packs/$packId，五区块）

```
agentblog  0.1.0                                  [探活] [卸载…(P2)]
① 概览   版本 / 安装时间 / 健康态（探针聚合，P1 手动触发）
② 工具   pack_tool_schema 存档清单（含 source: protocol|pack_yaml）
         + 逐服务器「实时探针」（复用 fe80ac8 probe-tools）
③ 事件   pack_subscription → 订阅 URL + types + is_active
         投递健康（成功/重试/死信计数）＝P2（R2 依赖 vendor 只读聚合查询）
④ 界面   控制台入口（label/URL 模板）+ 工单 tab slots（标识/高度）
⑤ 资产   agents × N（pack_id 命中）+ skills × N（origin='pack' AND origin_ref=pack_id）
```

### 2.4 不动清单

侧栏 PackEntries（用）、工单 slots tab、任务 job_handle 深链、agent 级 MCP tab、`/access` 页。契约边界全继承：events inform never orchestrate；域零（pack 数据表不进平台库）；slot 目录 v0 冻结。

---

## 3. 开发者接入旅程

### 3.1 现状（全手工）

读契约文档 → 手写 pack.yaml → 自建 MCP server（心中默背三闸门）→ 搞 admin PAT → 跑 CLI → 盲验。痛点：闸门只在安装时校验、装完无反馈面、无版本概念。

### 3.2 分阶段

```
P1  干净地基+可见   迁移 1042 + pack 服务 + provision 改造 + /packs 两页 + /tools 来源列
P2  可管           投递统计（R2）；server 端卸载/安装向导（闸门逻辑进 server，R4）
P3  好写           pack-provision init（脚手架）/ validate（本地预检三闸门）
P4  生态           市场/注册表——不排期
```

### 3.3 边界裁决（不变）

**不做事件消费**（事件→自动建工单/派任务）。"events inform, never orchestrate" 已冻结，P4 也不破，除非重新裁决契约。

---

## 4. 实施需求

### 4.1 R1：迁移 1042（§1 全部，P1）

无回填。迁移后 agentblog 重跑 provision（uninstall→install 或直接 install 幂等升级）迁入新结构。

### 4.2 R2：事件投递统计（P2）

ploykit `webhook_delivery` 加只读聚合查询（vendor 侧 PR 或 orkome 侧 admin repo 只读扩展），orkome 暴露 `GET /api/packs/{packId}/deliveries`。不碰投递逻辑。

### 4.3 R3：provision 改造（P1，修 D1/D2/D3）

- 第 0 步 `PUT /api/packs` upsert pack 行（admin PAT），拿 pack_id；
- POST mcp-servers / agents 的 body 带 `pack_id`（服务端落列）；
- POST skills 带 `origin='pack', origin_ref=pack_id`——skills HTTP 层修改：**仅 admin 调用者可写 origin**（非 admin 恒 'manual'，防伪造）；
- 订阅创建后写 `pack_subscription` 连接表（新端点或随 PUT /api/packs 提交）；
- uninstall 改按 pack_id 定向清理（不再按名猜）。

### 4.4 R4：安装闸门进 server（P2 前置）

`POST /api/packs/install`（上传 pack.yaml + 连接参数，三闸门在 server 执行）；CLI 变同一端点的自动化通道。

### 4.5 R5：升级语义（P3+）

`pack-provision upgrade` = 事务化 uninstall+install，版本比对提示；P1 重装即覆盖（version 列已记录）。

---

## 5. 验收清单（P1）

- [ ] 迁移 1042 落库；agentblog 重跑 provision 后 pack/关联列/连接表数据齐
- [ ] AI团队组出现「能力包」（Skills 与工具集成之间）；/packs 列表显示版本+资产计数
- [ ] /packs/$packId 五区块：②存档工具清单+实时探针；④入口+slots；⑤agents/skills 按 pack_id/origin 精确命中（D1/D2 修复的验收即在此）
- [ ] /tools 每行显示来源；pack 行只读+链接详情；手工行 CRUD 不变
- [ ] ③事件区块列订阅（URL/types/active），投递统计 P2 占位
- [ ] zh/en i18n；go test / tsc / vitest 全绿

## 6. 风险与开放问题

- **provision 与 server 的 API 契约同步**：R3 改 5 个 body 形状，CLI 与 server 同 PR 发（无兼容包袱，一步到位）。
- **技能同名碰撞**：pack 技能与手工技能同名时（现按名 skip），修 D1 后应 fail 提示而非静默 skip——裁决点。
- **vendor 边界**：webhook 投递统计（R2）若 vendor 不收只读扩展 PR，则在 orkome 侧直接查 vendor 表（越模块但只读，可接受的妥协，需裁决）。
