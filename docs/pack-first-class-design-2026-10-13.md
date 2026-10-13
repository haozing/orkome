# Pack 一等公民化 —— 信息架构升级与开发者接入设计

> 日期：2026-10-13
> 状态：设计提案（待用户裁决后实施）
> 关系：`pack-tool-contract-design-2026-10-11.md`（契约边界，本文全盘继承其裁决）、`pack-frontend-design-2026-10-11.md`（slots/job-handle 前端面，已落地）、`pack-rollout-plan-2026-10-11.md`（rollout 步骤）。本文不推翻三者，只解决它们都没回答的问题：**Pack 在产品导航里算什么、用户怎么看见和管理它、开发者怎么接进来**。

---

## 0. 问题定性：Pack 被当成了配置项，实际是生态单元

### 0.1 领域事实（代码实证，2026-10-13）

一个 pack 经 `pack-provision install` 安装时发生 **6 件事**：

| # | 动作 | 落点 | MCP 服务器有没有 |
|---|---|---|---|
| 1 | 注册 MCP 服务器（凭证 sealx 密封） | `workspace_mcp_server` | 有（这就是全部） |
| 2 | 三重契约闸门 + 工具 schema 存档（B1） | `pack_tool_schema` | 无 |
| 3 | 创建智能体 | agents API | 无 |
| 4 | 创建技能并绑定智能体 | skills API | 无 |
| 5 | 事件订阅真注册（HMAC 签名 / 五档重试 / 死信暂停） | `webhook_subscription` | 无 |
| 6 | UI 进驻：侧栏入口 + 工单 tab 插槽 + ui_token 票证 | `pack_ui_entry` / `ui_token` | 无 |

**MCP 只有第 1 行；Pack 是其余 5 行 + 第 1 行的打包编排。**

### 0.2 产品现状的错位

- **没有 pack 实体**：平台库无 `pack` 表。"pack" 是散落在 5 张表（`workspace_mcp_server` / `pack_tool_schema` / `webhook_subscription` / `pack_ui_entry` / `ui_token.plugin`）里的一个字符串键，唯一编排者是仓库 CLI（`cmd/pack-provision`）。
- **前端触点已渗出配置页**：侧栏 PackEntries（`_app.tsx`）、工单详情页动态 tab（`PackSlotFrame`）、任务消息 job_handle 深链（`task-tool-renderers.tsx`）——pack 的存在感遍布产品，但它的"家"只是 `/tools` 页一个只读小节（pack 名 + label + 安装日期，无健康/版本/工具数/订阅数）。
- **生命周期全在 CLI**：安装/卸载/（无升级/无健康检查）都走 `pack-provision`，前端零参与。装完一个 pack，用户在产品里"看不见装了什么"，装对装错全靠猜。

### 0.3 与 MCP 的边界（一句话钉死）

> **MCP 服务器是协议层的接线（工具单向 pull 进来）；Pack 是产品层的应用（谁在提供能力、以什么身份、界面长在哪、事件怎么流、怎么装卸）。** 类比：MCP 服务器是数据库连接串，Pack 是装进来的 App。

方向性差异：

| 方向 | MCP | Pack |
|---|---|---|
| 工具（平台→pack pull） | ✓ 唯一能力 | ✓（第 1 行） |
| 事件（平台→pack push） | ✗ | ✓（第 5 行；"events inform, never orchestrate" 边界已冻结） |
| UI（pack→平台进驻） | ✗ | ✓（第 6 行：侧栏入口 + 工单 tab） |
| 回调（pack→平台 API） | ✗ | 设计有、D4 未落地（pack 服务账号 PAT） |

事件接收端现状诚实标注：agentblog 侧 `/hooks/orkome` 接收端未实现（README 自认 D3 前预期状态）；pack→平台 API 回调方向未落地。当前"双向"= 工具 pull + 事件 push + UI 进驻。

---

## 1. 信息架构裁决案

### 1.1 导航：Pack 升一等公民，进 AI团队组

```
AI团队组：智能体 / 小队 / Skills / 能力包(Packs) / 工具集成 / 运行日志
底部段：  开放接入 / 自动化 / 设置（不动）
```

心智模型：**能力包是能力的来源，工具集成是接线板**。装一个 pack，团队长出智能体、技能、工具、界面；工具集成页管理手动注册的散装 MCP 服务器。顺序即"来源 → 接线"。

命名：nav `nav.packs` =「能力包」（en: Packs）。路由 `/packs`；pack 详情 `/packs/:pack`。

### 1.2 两页分工与去重

- **`/packs`**：pack 列表（卡片/行式：名、版本、安装日期、健康态、工具数）→ 点进详情。
- **`/tools`**：保持 MCP 注册表职能，每行加**来源列**：
  - `pack:agentblog` 来源的行 → 只读（名称/URL/凭证受 pack 生命周期管），操作位换成「来自能力包 agentblog →」链接到 `/packs/agentblog`；
  - 手工注册的行 → 现有编辑/删除/查看工具不变。
- 判定依据：安装期 provision 写入的 `workspace_mcp_server` 行目前无来源标记，**需加一列**（见 §3.1 R1）。

### 1.3 Pack 详情页（/packs/:pack，五区块）

```
agentblog  0.1.0                                  [探活] [卸载…]
──────────────────────────────────────────────────────────────
① 概览   安装时间 / 版本 / 健康态（探针结果聚合）
② 工具   N 个（实时 probe 清单，复用 fe80ac8 的 probe-tools；
         schema 来源标注：协议自描述 / pack_yaml 存档）
③ 事件   订阅: work_item.updated → http://…/hooks/orkome
         投递健康：成功 / 重试中 / 死信 计数（依赖 R2）
④ 界面   控制台入口（label + URL 模板）
         工单 tab：文章预览（slot 标识 + 高度）
⑤ 资产   智能体 × N / 技能 × N（pack 带来的，现有 API 可查）
```

- ②③④⑤ 的数据源全部已存在（`pack_tool_schema`、webhook 订阅 API、`pack_ui_entry`、agents/skills 列表）；唯一缺口是③的投递统计查询（R2）。
- 「探活」= 对该 pack 全部 MCP 服务器跑 probe-tools（现成端点），聚合成健康/部分健康/失联三态。
- 「卸载」P1 只做只读提示（引导 CLI 命令）；UI 卸载放 P2（依赖安装闸门逻辑进 server，见 §3.2 R4）。

### 1.4 不动清单

- 侧栏 PackEntries 快捷入口（留在 AI团队组上方原位——它是"用"，/packs 是"管"）。
- 工单详情页 slots tab、任务消息 job_handle 深链（已落地，不在本设计范围）。
- agent 级 MCP tab（agent 私有服务器遮蔽配置）。
- `/access` 开放接入页（对外 PAT，与 pack 无关）。
- 契约边界全部继承：事件 inform 不 orchestrate；域零（pack 数据表不进平台库）；slot 目录 v0 冻结 `workitem.detail.tab`。

---

## 2. 开发者接入旅程设计

### 2.1 现状旅程（全手工）

```
读契约文档 → 手写 pack.yaml → 自建 MCP server（心中默背三闸门）
→ 搞 owner/admin PAT → 跑 pack-provision install → 出错改了重装 → 盲验
```

痛点排序：①三闸门只有装的时候才知道过不过；②装完无反馈面；③无版本/升级概念；④PAT 手工管理。

### 2.2 目标旅程（分阶段）

```
P1  可见    装完 → /packs 详情页立刻看见装了什么、健康吗、带了谁
P2  可管    UI 卸载 + 健康面 +（依赖 R4）UI 安装向导
P3  好写    pack-provision init（脚手架）/ validate（本地预检三闸门）
P4  生态    市场/注册表（上传、版本、审核）——文档 v2 规划，agentblog/dify 阶段不做
```

**P3 的两个 CLI 子命令是开发者体验的真门槛**：

- `pack-provision init <name>`：生成 pack.yaml 骨架（含三闸门合规示例：`<name>_` 前缀工具、自描述 outputSchema、job 三件套齐备）+ MCP server 骨架 + README。
- `pack-provision validate ./pack.yaml`：本地预检——起不起服务都行，能查 yaml 结构、工具名前缀、schema 声明完整性、job 三件套、slot 标识合法性；**不写平台任何数据**。

### 2.3 要抵住的诱惑（边界裁决）

**不做事件消费**（收到 pack 事件 → 自动建工单/派任务）。契约已冻结 "events inform, never orchestrate"：事件只做收尾同步（如 agentblog 归档），不做编排触发。破了这条，pack 从能力提供方变成平台内另一个大脑，编排权失控。P4 之前不碰；P4 也不碰，除非重新裁决契约。

---

## 3. 实施需求清单

### 3.1 R1：MCP 服务器来源标记（小，P1 必须）

`workspace_mcp_server` 加列 `origin`（迁移 10xx）：`manual`（默认，存量回填）/ `pack:<name>`。provision install 写入时带 `origin=pack:<name>`；uninstall 已按名删除不受影响。前端 /tools 页读此列渲染来源列。手动删除 pack 来源的行 → 允许但详情页健康态变「失联」（v0 不做联动恢复）。

### 3.2 R2：事件投递统计查询（中，P2）

webhook_delivery 数据在 ploykit 表里，缺工作区级查询 API。新增 `GET /api/workspaces/{ws}/webhooks/subscriptions/{id}/stats`（成功/重试中/死信计数 + 最近 N 条投递结果），权限走现有 member 校验。ploykit 侧若缺聚合查询则加只读方法（不碰投递逻辑）。

### 3.3 R3：pack 级健康聚合（小，P2）

详情页「探活」即逐台跑现有 probe-tools，前端聚合即可，无新端点。可选优化：后端合并端点 `POST /api/packs/{pack}/probe`（P3 再说）。

### 3.4 R4：安装闸门进 server（大，P2 前置）

UI 安装向导的前提是把 provision 的三闸门（前缀/输出契约/job 三件套）从 CLI 搬进 server（`POST /api/packs/install`，multipart 上传 pack.yaml + 连接参数）。CLI 保留为自动化通道（同一端点），server 侧成为唯一实现。**P1 不做**——CLI 已可用，向导是锦上添花。

### 3.5 R5：版本记录（小，P2）

`pack_ui_entry` 加 `version` 列，provision 写入。升级 = uninstall + install 的 UI 提示在 P2 一并提供（真 upgrade 命令放 P3+）。

---

## 4. 实施切分

| 阶段 | 内容 | 性质 | 规模 |
|---|---|---|---|
| **P1** | `/packs` 页 + 详情页（①②④⑤ 区块，③留占位）；nav 加入 AI团队组；R1 来源列 + /tools 来源列渲染；i18n | 纯前端 + 一条小迁移 | 半天级 |
| **P2** | ③事件投递统计（R2）；健康聚合探活按钮（R3）；UI 卸载 + 版本列（R5）；R4 安装闸门进 server + UI 安装向导 | 后端中量 + 前端 | 2-3 天级 |
| **P3** | `init` / `validate` CLI 子命令；接入文档（以 agentblog 为活例） | 纯 CLI | 1-2 天级 |
| **P4** | 市场/注册表 | 大 | 不排期 |

---

## 5. 验收清单（P1）

- [ ] AI团队组出现「能力包」，位于 Skills 与工具集成之间；`/packs` 可达
- [ ] `/packs` 列出已装 pack（名/版本/安装日期）；空态引导 pack-provision 命令
- [ ] `/packs/agentblog` 详情页：工具区块实时 probe 出 6 工具（含 schema 来源）；界面区块显示控制台入口与工单 tab；资产区块列出 agents/skills
- [ ] `/tools` MCP 注册表每行显示来源；`pack:agentblog` 行只读 + 链接详情页；手工行可编辑
- [ ] 事件区块显示订阅声明，投递统计位置留「P2」占位
- [ ] zh/en i18n 齐全；`tsc -b` / vitest 全绿
