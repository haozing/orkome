# A3 / F4 交接笔记 —— MCP 页改造为"Packs 与集成"三段式

> 日期：2026-10-11
> 任务书：`docs/pack-rollout-plan-2026-10-11.md` §1 A3
> 需求：`docs/pack-frontend-design-2026-10-11.md` §3（v1.1 审计修订版，★ 处已全部遵守）
> 边界遵守：只动 `web/`（本轮工作区里的 Go 改动来自并行的 A1/C1 会话，非本会话所改）；未碰 `_app.agents.$agentId.tsx`；无任何 git add/commit。

---

## 一、改了什么 / 为何

### 1. `web/src/routes/_app.mcp.tsx` —— 改造为三段式

- 页面组件改名 `McpPage` → `PacksAndIntegrationsPage`（路由文件与路径 `/mcp` 不动，路由 = 深链兼容）。
- 页首标题从硬编码 `MCP` 改为 `t('packs.pageTitle')`，intro 从 `mcp.intro` 换为 `packs.pageIntro`（原 `mcp.intro` 文案描述"客户端接入"，与三段式定位不符；`mcp.*` 命名空间其余键全部保留在用）。
- **删除了原"服务地址"独立卡片**（endpointTitle/endpointDesc/comingSoon）。原因：三段式里没有它的位置，且端点 URL（`{origin}/mcp`）在第 3 段的 Codex/Claude Code/通用客户端三个配置片段里本来就完整出现，信息零丢失。这是对"现有内容原样保留"的唯一一处收紧，如需恢复可从 git 历史取回。
- 第 3 段（连接你的客户端三片段 + 工具清单 + PAT 管理）逐行未动。

### 2. `web/src/components/packs-sections.tsx` —— 新增（第 1/2 段组件）

- **`PacksInstalledSection`（第 1 段）**：`GET /api/ui-entries` 只读列表，每行 `pack 名（mono）+ label + installed_at`（本地化日期）。★v0 不做健康列/版本号/工具数/订阅数——审计确认数据源不存在，列名未出现它们。零 pack 空态带引导文案（说明安装走 pack-provision CLI、装完后侧栏会有入口）。
- **`WorkspaceMcpSection`（第 2 段）**：★新写（非平移）。工作区级 MCP 服务器的 列表/新增/编辑/删除。交互模式参考 agent 详情页 MCP tab（确认式删除、Dialog 表单），但代码独立。凭证管理一并做了（见下"端点考察"——端点存在，凭证随 create/update 的 body 走）。
- 拆成独立组件而非内联在 route 文件里：route 文件不便做组件测试（路由树依赖），独立组件可直接 RTL 渲染。

### 3. `web/src/api/packs.ts` —— 新增（API 封装，签名模式照 `orchestration.ts:140-179`）

- `listPackEntries()` → `GET /api/ui-entries`，解包 `{ items }`，null 兜底 `[]`。
- `listWorkspaceMcp(wsId)` / `createWorkspaceMcp(wsId, input)` / `updateWorkspaceMcp(wsId, serverId, input)` / `deleteWorkspaceMcp(wsId, serverId)`。
- 审计事实复核属实：全仓 grep 确认此前 `web/src/api/` 对 `/api/workspaces/{id}/mcp-servers` **零调用**（唯二消费方是 `cmd/pack-provision/main.go:264,288,502,511`），本文件是前端首次封装。

### 4. i18n（zh + en 双份）

- `nav.mcp`：`'MCP'` → `'Packs 与集成'` / `'Packs & Integrations'`（侧栏导航入口改名，两份 locale 都改）。
- 新增 `packs.*` 命名空间（zh/en 各 25 键）：页标题/两段标题描述/空态/安装时间/注册表增删改文案/表单标签/凭证清空警示。所有新 UI 文案走 i18n，零硬编码（表单 placeholder 里的 `web-search`、`https://…`、`sk-…` 是示例值非文案，与 agent 页惯例一致）。

### 5. 测试（新增 13 个，全绿）

- `web/test/api/packs.test.ts`（5 个）：msw 验证 5 个封装的 URL 路径、wire 形状（POST body `{name,transport,url,credentials}`）、`{servers}`/`{items}` 解包与 null 兜底、DELETE 返回 `{result}`。
- `web/test/components/packs-sections.test.tsx`（8 个）：
  - Packs 三态：零 pack（空态文案 + 无行）/ 一个 pack（名+label+日期）/ 多个 pack（两行，断言无健康列等）；
  - 注册表交互：空态、列表渲染、**新增**（Dialog 表单 → POST → 列表 refetch 出新行）、**删除**（aria-label 定位删除钮 → 确认 → DELETE → 行消失）、**编辑**（表单预填 → PUT 到 `/{serverId}`）。
- 组件测试基建仓内已具备（vitest + RTL + msw + setup.ts，测试语言固定 en），无缺口；未做的是路由级测试（整页 `_app.mcp.tsx` 未直接测，其新增逻辑全部在两个 section 组件里被覆盖）。

---

## 二、第 2 段端点形状的实际考察结果（交接重点）

全部从 Go 源码实证（`internal/orchestration/adapters/http/routes.go`、`app/service.go`、`adapters/pgrepo/repo.go`）：

| 项 | 事实 |
|---|---|
| 路径 | `GET/POST /api/workspaces/{workspaceId}/mcp-servers`、`PUT/DELETE .../{serverId}`（routes.go:162-165） |
| 鉴权 | 与其他端点不同：workspace id 从 **URL path**（`r.PathValue("workspaceId")`，memberPathMW）取，**不是** `X-Workspace-Id` 头；成员校验失败 403。前端 api 封装因此必须把 wsId 拼进 URL（packs.ts 已如此，测试有断言） |
| 请求体 | `mcpWire{ name, transport, url, credentials(map[string]any) }`（routes.go:463-468）；name/url 必填（service.go:329），URL 过 egress guard；transport 空串落库时默认 `"http"`（repo.go:543 orDefault） |
| 列表响应 | `{ "servers": [{ id, workspace_id, name, transport, url, has_credentials, created_at }] }`，按 name 排序；**credentials 不回传**（`json:"-"`），只有 `has_credentials` 布尔 |
| create 响应 | 201 + 单个 server 对象（不在数组里）；重名 409 Conflict（pg 23505） |
| update 响应 | 200 + 单个 server 对象；**全量覆盖语义**：repo `SET name/transport/url/credentials`，credentials 不传 = 清空凭证（credJSON 空 map 落 `{}`） |
| delete 响应 | `{ "result": "deleted" }` |
| 凭证形状 | pack-provision 的先例（main.go:281-285）：`{ "headers": { "Authorization": "Bearer <key>" } }`。前端 Dialog 沿用同一形状（单个 API Key 输入 → Bearer 头）；后端无独立"凭证管理"子端点，凭证随 create/update 走——**TODO 已闭环：不存在缺失端点，凭证管理已实现** |
| transport 合法值 | 引擎侧接受 `""/http/streamable_http/sse`（tools_mcp.go:187-189）；UI 下拉只给 `http`/`sse` 两项（库里的存量值都会正常回显） |

给下一会话的两个坑：

1. **编辑时留空 API Key 会清空凭证**（后端 PUT 全量覆盖）。UI 已做缓解：编辑已存凭证的服务器且 Key 留空时显示琥珀色警示文案（`packs.apiKeyClearWarning`）。如果后端将来想做 PATCH 语义（credentials 省略 = 保持），前端去掉警示即可。
2. **编辑已存 transport 为 `streamable_http` 的服务器**时，下拉只有 http/sse 两项，选了 http 再保存会把 transport 改掉。当前库里 transport 都来自 provision CLI（pack.yaml 可写任意值），v0 接受这个限制；要消除的话把 select 换成带三项的（或在检测到未知值时动态加一项）。

---

## 三、验收命令实际输出摘要

| 命令 | 结果 |
|---|---|
| `cd web && pnpm build`（含 tsc 类型检查） | ✅ `✓ built in 3.44s`，类型零错误 |
| `make web-test`（本机 Git Bash 无 make，等价执行 `cd web && pnpm test`） | ✅ `Test Files 17 passed (17) / Tests 87 passed (87)`（含新增 13 个） |
| `make web-lint`（等价执行 `cd web && pnpm lint`） | ⚠️ 退出码 1，但唯一 error 在 `src/components/project-nav.tsx:58`（react-hooks/set-state-in-effect）——**预存问题**：已用 `git stash` 验证在基线 commit 9f574ed 上同样报错，与本任务无关（该文件本会话未触碰）；7 个 warning 也全在预存文件。本次新增/修改的 4+2 个文件零 lint 发现 |
| 无后端起 dev server 截图 | 未做（dev server 依赖后端 8080 提供登录态，本地无库；组件三态/交互已由 msw 测试覆盖） |

## 四、遗留坑清单

1. `project-nav.tsx` 预存 lint error（见上表，非本任务引入，需要单独小修）。
2. 编辑流的全量覆盖语义依赖 UI 警示缓解，根修在后端 PATCH（见二-1）。
3. transport 下拉不含 `streamable_http`（见二-2）。
4. 第 1 段 Packs 无"打开控制台"深链按钮——任务书明确 v0 只读列表（pack 名 + label + installed_at），侧栏 `PackEntries` 已承载深链入口；若要行内加入口，复用 `pack-entries.tsx` 的 ui-token 铸票逻辑即可。
5. `WorkspaceMcpSection` 在 `workspaceId === null` 时禁用"新增"按钮且列表查询不发；当前产品登录后必有 workspace，此防御分支实际不可达。
6. 健康/版本/工具数列：等 C1 的 task.context.tools 快照落库后再补（设计文档 §3.1 出路②）。
