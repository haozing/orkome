# 插件前端面设计 —— 深链之外的三个缺口

> 日期：2026-10-11（v1.1 修订：按 `pack-design-audit-2026-10-11.md` 修正 4 处事实性错误与 2 处实现触点，修订处以 ★ 标注）
> 状态：设计定稿（审计后修订版，可作实施基线）
> 关系：`plugin-system-design-2026-10-03.md` §2.4/§2.5 的前端落地篇；`pack-tool-contract-design-2026-10-11.md` 的 UI 侧配套。
> 前端栈现状（实证）：TanStack Router（file-based routes）+ React Query + Zustand + Tailwind + Base UI/shadcn + i18next；深链入口 `PackEntries` 已在侧栏（`_app.tsx:126`），ui-token 三段式已通。

---

## 0. 现状盘点与问题

已落地的插件前端面**只有一件**：侧栏深链按钮（`pack-entries.tsx`：拉 `/api/ui-entries` → 铸 ui-token → 新窗口跳 pack 控制台）。

设计文档 §2.5 的第 5 件（slots）在代码里**零实现**：pack-provision 不解析 slots 声明（grep 无命中）、无 slot 渲染组件、`workspace.dashboard` 宿主页不存在。同时后端补篇（pack-tool-contract）新增的 job-handle 协议在前端也没有消费面。

本篇回答三个问题：

1. **slots 到底建不建、怎么建**（§1）
2. **pack 工具的执行痕迹在任务流里怎么呈现**（§2）——这是被两份既有设计文档都漏掉的、但用户每天都会看的面
3. **pack 的管理面**（安装/卸载/凭据/健康）放哪（§3）

---

## 1. Slots（页面插槽）：启动判定 + 最小实现

### 1.1 启动判定：**agentblog-pack 落地即启动 slot 通道**（修订原"条件启动"）

原设计说"运营真实产生深链跳太烦的痛点才建"。修订理由：agentblog 是第一方 pack，两个 slot 场景当场就是真实需求而非假设——

- `workitem.detail.tab`：内容运营的 work item 详情页想直接看"这篇文章的预览/质检指标"，跳出去再跳回来确实是烦的（这是 v0 最有价值的 slot）；
- `workspace.dashboard.card`：**宿主页（workspace dashboard）本身不存在**——这条 slot 连带宿主一起延后（见 1.4），不因它拖累前者。

判定结果：**只做 `workitem.detail.tab` 一个 slot**，dashboard 卡片及其宿主页延后到出现第二个 pack。

### 1.2 数据与 API（后端小改，无新城）

pack.yaml v1 增补（provision 解析 + 存进既有 `pack_ui_entry` 表）：

★**格式决策（审计修订）**：现状 `ui.entry` 是**单数**（`pack-provision/main.go:78-82`），表 UNIQUE(workspace_id, pack) 一 pack 一行。**取最小路径：保留 `entry` 单数不动，仅追加 `slots` 段**——entries 数组化是不必要的 schema 重构，取消初稿示例的列表写法。

```yaml
ui:
  entry:                                # 单数（现状保持）
    label: "内容工厂"
    urlTemplate: https://agentblog.internal/console?ws={workspaceId}&t={uiToken}
  slots:                                # ★新增（列表）
    - slot: workitem.detail.tab
      label: "文章预览"
      urlTemplate: https://agentblog.internal/slot/preview?item={workItemId}&t={uiToken}
      height: 480                       # 声明高度（resize 协议兜底前的默认）
```

- 模板变量扩一个 `{workItemId}`（铸票时平台不注入——**票据仍只有 workspace 级 claims**，workItemId 是 URL 参数，由前端渲染时替换）；★另支持 `{jobId}`（§2.1 JobHandleCard 的控制台跳转用）。
- 读取 API：`GET /api/ui-entries` 响应里 `slots` 随行返回（一个端点两用，不加新路由，契约测试更新）。

### 1.3 前端组件：`PackSlotFrame` + work-item 详情页接线

```tsx
// web/src/components/pack-slot-frame.tsx（新增，~80 行）
// 职责：铸 ui-token（复用既有 POST /api/ui-tokens）→ 拼 URL → iframe 渲染
//      → 监听 postMessage({type:"pack.resize", height}) 调整高度（3s 超时用声明高度）
//      → sandbox="allow-scripts allow-same-origin allow-popups"
//      → 加载失败/超时呈现"在控制台打开"深链兜底（iframe 永远不是唯一路径）
```

work-item 详情页（`_app.work-items.$id.tsx`）接线：现有 `<Tabs>` 的 TabsList 是静态数组驱动——改为**静态 tab + 动态 slot tab 拼接**：

```tsx
const slots = useQuery(['pack-slots', wsId, itemId], ...)   // entries 响应里的 slots
const tabs = [...staticTabs, ...slots.map(s => ({ key: `pack:${s.pack}`, label: s.label }))]
// 渲染 slot tab 时挂 <PackSlotFrame slot={s} workItemId={itemId} />
```

★**实现触点修正（审计）**：tab 选中态不在组件 state，而在 **URL search params + zod 枚举**（`searchSchema = z.enum(['detail','sub','tasks','timeline'])`，`_app.work-items.$id.tsx:18-27`；validateSearch 失败即丢弃）——动态 `pack:*` 值会被 zod 拒掉。接线需同步改四个触点：zod schema（如 `z.enum([...]).or(z.string().regex(/^pack:/))`）、`DetailTab` 类型、`patchSearch` 强转（:69）、内容条件渲染分支（:88-99）。仍是小改动，但比上面的伪代码多出 search-schema 这一个触点，实现者勿照抄伪代码漏掉。

★**第二个候选宿主（已识别，v0 不做）**：agent 详情页是同款 URL 驱动 Tabs 架构（`_app.agents.$agentId.tsx:43-46` zod 枚举六 tab、:178-198 静态数组）——将来 slot 扩到 agent 面时接线模式完全同构。

### 1.4 反滥用与冻结纪律（继承原设计 §2.5）

- frame-ancestors 探测：**slot 注册时**（provision 或 server 侧）GET slot URL 验 `Content-Security-Policy: frame-ancestors` 必须含 orkome 域名，不合规拒绝该 slot（但 pack 其余件照常装）；v0 第一方 pack 可豁免探测、代码到位后开启。
- slot = 只读摘要视图，重交互永远走"打开完整控制台"深链（Frame 右上角固定一个 external-link 按钮）。
- **目录冻结**：首个第三方 pack 使用 `workitem.detail.tab` 时，该 slot 的模板变量/协议/高度语义冻结为 v1 契约。
- dashboard 宿主页（`workspace.dashboard.card` 的前提）：与 dashboard 产品页一起设计，本篇不预建。

---

## 2. 任务流里的 pack 工具呈现（两份既有设计都漏掉的面）

用户看一个 agent 任务的执行过程（`task-messages` 流），pack 工具的调用长什么样？现状：`tool_use`/`tool_result` 一律灰框 + `pre` 原文（`task-messages.tsx`）——对内置工具够用，对 pack 工具是三重缺陷：

1. **认不出**：不知道这条调用来自哪个 pack、哪个工具（名字只有 `agentblog_submit_generation`）；
2. **job 状态不可视**：补篇的 job-handle 协议下，`submit` 的结果只有 `{job_id}`——用户看到 job_id 字符串，不知道跑到哪了，得自己去 agentblog 控制台查；
3. **产物不可达**：`review_result` 返回的 URL 躺在 JSON 里，不可点。

### 2.1 结构化渲染规则（`task-messages.tsx` 升级）

按工具名前缀路由到 pack 渲染器，**平台内置默认渲染器 + pack 注册自定义渲染器**两层：

```tsx
// 渲染器注册表（前端侧，非进程内插件机制——只是组件映射表）
type ToolResultRenderer = (props: { args: unknown; result: unknown }) => ReactNode

const renderers: Record<string, ToolResultRenderer> = {
  // 平台默认：识别 job-handle 契约的通用渲染
  default: DefaultToolResult,               // 现状灰框（保持，兜底一切未注册工具）
  'agentblog:submit_generation': SubmitGenRenderer,
  'agentblog:job_status': JobStatusRenderer,
  ...
}
```

**平台内置三个通用渲染器**（所有遵守 pack 工具契约的 pack 免费获得，这本身就是契约的 UI 红利）：

| 渲染器 | 触发条件 | 呈现 |
|---|---|---|
| `JobHandleCard` | result 匹配 `{job_id}` 形状 | 卡片：job_id（可复制）+ state 徽章 + "在 pack 控制台打开"深链（带 `job_id` query 参数，深链模板支持 `{jobId}` 变量） |
| `JobStatusCard` | result 匹配 JobStatus schema（补篇 §2.1） | 阶段进度条形 + state 徽章 + 失败时展示 `error.code` 与 `stage_failed` 标签（`post_publish` 红色警示）+ 产物 URL 可点 |
| `MetricsCard` | result 含 `metrics` 对象 | 键值对网格（质检指标一类） |

★**实现事实（审计补充，实现者必读）**：

1. **工具名解析规则**：`TaskMessage` 类型**没有工具名字段**（`api/orchestration.ts:24-33`，仅 kind/call_id/content/truncated）。工具名只在 tool_use 的 content 首 token（后端 emit 的是 `name + " " + args`，`engine.go:334`）；tool_result 的 content 是纯结果文本，靠 `call_id` 回溯配对到前面的 tool_use。好消息：三个内置卡都按 **result 形状**触发，不依赖工具名——工具名路由只有 pack 自定义渲染器（v1+）才需要。
2. **截断兜底**：content 超 2000 runes 被 engine 截断（`eventpipe.go:113-116`），截断后 JSON 解析必失败——渲染器入口统一规则：`truncated === true || JSON.parse 失败 → 默认灰框`（与"非契约退化"激励自洽）。
3. **content 是 CallToolResult 包装**：MCP 工具结果经 eino-ext 序列化为 content 数组 + structuredContent + isError 的包装串（`eino-ext mcp.go:155-160`），形状匹配按包装层写，先真跑抓样例再写类型。
4. **类型落点**：JobStatus/job-handle TS 类型放 `api/orchestration.ts`（与 TaskMessage 同居）；渲染器注册表 + 三卡做进 `TaskMessageRow`（`components/task-messages.tsx`）——其两个消费面（work-item 面板 `task-panel.tsx:191` + run-logs 抽屉 `_app.run-logs.index.tsx:301`）**自动获得新渲染，无需各自接线**。
5. **realtime 零新增依赖**：`task:*` WS 帧 → `main.tsx:16` 失效规则 → React Query refetch 已闭环，三卡不需要任何推送改造。

pack 自定义渲染器（如 agentblog 想要文章首图卡）**不进平台仓库**——属于 pack 自己前端工程的事，平台只认注册表协议。v0 不做自定义注入通道（那是把前端插件化，踩双轨禁令的红线）；第一方 pack 需要自定义卡时先提 PR 进平台默认渲染器表，等第三个 pack 出现同样诉求再立协议。

### 2.2 消息流内联 job 状态（v1，可选）

`submit` 之后用户留在任务页，`job_status` 要等 agent 下一次查询才有新消息——中间是盲区。v1 方案：`JobHandleCard` 上加"追踪"开关，开启后前端轮询 pack 的 job_status 端点（经平台 MCP 桥接代理，**不经模型**，纯 UI 刷新，不产生 task_message）。v0 先不做（盲区可接受：深链出去看），列在此处防止将来把它做成"平台轮询写回 task_message"的错误形态。

### 2.3 契约联动

§2.1 的形状识别依赖 pack 工具契约（输出 schema）落地——**输出契约先行，UI 免费跟读**。若 pack 未遵守契约（无 schema、返回自由 JSON），一律落回默认灰框：契约的可见红利 + 非契约的可见退化，形成正向激励。

---

## 3. Pack 管理面（安装/卸载/凭据/健康）

现状：安装 = SSH 到服务器跑 `pack-provision` CLI（开发者操作）；前端**没有任何 pack 管理页面**。

### 3.1 v0：MCP 页扩展为"Packs"页（★规模较初稿上调——见下）

`_app.mcp.tsx` 现在是"MCP 客户端接入页"（Claude Code 配置片段 + PAT）。扩展为三段式，改名为"Packs 与集成"：

```
┌ Packs（已安装）─────────────────────────────┐
│ agentblog  [内容工厂 →]  安装于 2026-10-11  │  ← ★v0 降级版：pack 名 + label + installed_at
│ dify       [控制台 →]   安装于 2026-10-08   │     （pack_ui_entry 现有列；版本号/工具数/订阅数
└──────────────────────────────────────────────┘      /slot 数表里没有，provision 落库后 v1 再显示）
┌ MCP 服务器注册表 ───────────────────────────┐   ← ★审计修正：ws 级注册表 UI **不存在**（后端端点在
│ （★新写：工作区级列表/新增/删除/凭证）        │      routes.go:162-165，但前端 api/ 零调用）——
│  agent 级管理 UI 已有，留在 agent 详情页     │      "平移"实为新写；agent 级 UI 在 agent 详情页
│  MCP tab（_app.agents.$agentId.tsx:766+），  │      MCP tab，与本页并存不搬动。新写可直接复用
│  两者并存                                    │      orchestration.ts:140-179 的函数签名模式
└──────────────────────────────────────────────┘
┌ 客户端接入（现有内容不动）──────────────────┐
│ Claude Code / Codex 配置片段 + PAT 管理      │
└──────────────────────────────────────────────┘
```

- ★**健康列 v0 不做**（审计修正）：初稿"从 task.context.tools 快照聚合成功率"的数据源不存在——引擎工具清单本地装配不落盘，也无聚合端点。两个出路：①v0 降级为只显示 installed_at（如上图，**推荐**）；②把快照落库并入 C1 的 task.context.tools 扩展后，F4 再补健康列。前端先按 ① 交付。
- **不做前端安装向导**（v0）：安装仍走 CLI——安装需要填凭据、做前缀握手校验，CLI 是正确形态；前端只读展示。
- ★i18n 触点：改名动 `nav.mcp`（zh+en 双份）；新增 `packs.*` 命名空间；三卡文案落 `taskMsg.*`（zh.ts:651-657 已有空位）。

### 3.2 v1 前置条件

前端安装向导（凭据表单 + permissions 确认页）= permissions 执行面（原设计 Phase 5）的 UI 半张；与 PAT scopes 一起排 v1，触发条件不变（第二 workspace / 首个第三方意向）。

---

## 4. 汇总：前端任务清单（按优先级）

| 序 | 任务 | 规模 | 依赖 |
|---|---|---|---|
| F1 | `JobHandleCard`/`JobStatusCard`/`MetricsCard` 三个通用渲染器 + 渲染器注册表，接入 `TaskMessageRow` | 中 | 后端输出契约（P0）落地 |
| F2 | pack.yaml slots 解析（provision，entry 单数保持 + 追加 slots）+ `GET /api/ui-entries` 响应扩展 | 小 | 无 |
| F3 | `PackSlotFrame` 组件 + work-item 详情页动态 slot tab（含 zod search schema 扩展四触点） | 中 | F2 |
| F4 | MCP 页改"Packs 与集成"三段式（Packs 降级版只读列表 + ★ws 级注册表 UI **新写**） | ★中（初稿"小"上调：ws 级为新写） | 无（健康列等 C1 后补） |
| F5 | 深链模板 `{jobId}` 变量支持（JobHandleCard 的"控制台打开"） | 极小 | F1 |
| — | 消息流内联 job 追踪（轮询代理）、自定义渲染器注入协议、dashboard 卡片、健康列 | — | 分别列 v1/第三方出现/dashboard 立项/C1 快照落库 |

**设计原则一句话：pack 的 UI 面 = 深链（重交互的家）+ slot（轻摘要嵌入）+ 通用契约渲染器（任务流里的可见性）——三层全部"平台定义形状、pack 填内容"，前端不引入任何进程内插件机制，与后端双轨禁令对齐。**
