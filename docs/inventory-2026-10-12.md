# orkome 三维盘点总览 —— 页面 × 功能点 × API

> 日期：2026-10-12 · 环境：后端 :8080（迁移至 1036）+ 前端 :5173（vite dev）真实启动，测试账号 `inventory-test@orkome.dev` / 工作区 `inventory-ws`
> 方法：三 agent 并行三维盘点，交叉互证——**V 活体**（真实浏览器遍历 17 页，44 张截图，真实写操作）+ **R 静态**（web/src 代码面）+ **A API**（后端路由×前端调用面×契约测试对照）
> 分册（细节都在分册，本文是总账与交叉发现）：
> - `docs/inventory-pages-live-2026-10-12.md`（活体，21 章）· 截图 `/tmp/inv-shots/`（44 张）
> - `docs/inventory-frontend-static-2026-10-12.md`（静态，5 部分含页面→API 交叉表）
> - `docs/inventory-api-2026-10-12.md`（API，16 节 807 行，含四波新端点形状 §13.6）

---

## 一、总账

| 维度 | 数量 | 备注 |
|---|---|---|
| 页面 | **17**（21 个路由文件） | login / onboarding / my-work / notifications / work-items 列表+详情 / chat(占位) / agents 列表+详情(6 tab) / squads 列表+详情 / skills 列表+详情 / mcp / automation / run-logs / settings；活体 17/17 全可达，无 404 |
| 功能点 | 静态 **~150**（含 9 个派生子视图）；活体确认 **~70** 个可交互控件 | 差值 = 需特定数据态才渲染的面（空态未渲染的表单/筛选等），非矛盾 |
| API 端点 | **≈316** HTTP | 产品域 ~200 + ploykit 框架域 ~108 + 平台面 8；另有 MCP StreamableHTTP 1 组（9 工具） |
| WS | 1 入口（`GET /ws`），4 scope，**27 类业务帧** + 控制帧 | 前端按前缀正则失效 react-query |
| 前端调用面 | 9 个 api 封装文件 75 函数 → ~70 唯一 path + 4 处组件裸调用 | **无前端消费面的端点 ≈155 条**（见 §四） |
| i18n | zh 30 命名空间 ≈635 键 / en 33 ≈646 键 | 四波新增 packs.*/slots.*/taskMsg.* 均双语 |

## 二、活体验证结论（V）

- **写操作 6 成 1 败**：创建工作项 ✓ / Agent ✓ / 小队 ✓ / 小队加成员 ✓ / 技能 ✓ / 技能内新建 SKILL.md ✓；**创建 PAT 100% 失败**（bug #3）。
- 正常亮点：壳层 6 控件（铃铛/主题/语言/WS 徽标/登出）、work-items 搜索与状态筛选、my-work 4 tab、run-logs 4 tab、agent 详情其余 5 tab、mcp 三段式（Packs 空态/新增服务器弹窗/三客户端配置复制）全部可用；除下述问题外无 console error。
- 登录页仅有验证码流程，**无密码登录入口**（低，与开发惯例不符，已记录）。

## 三、交叉确认的缺陷（三面互证后钉死根因，P0→P2）

### P0（崩溃/核心流程必败，4 项）

| # | 缺陷 | 根因（file:line） | 证据面 |
|---|---|---|---|
| 1 | **工单详情页整页崩溃**（members 相关五处连坐：info-panel/property-rows/comment-thread/列表指派/agent 权限 tab） | 后端 members 返回 `{items,total}` envelope，前端 `web/src/api/identity.ts:72` 当数组用（`.find/.filter is not a function`） | V（崩溃截图+console）+ A（契约形状）互证 |
| 2 | **任意页面点工作区切换器 → 崩进错误边界** | `workspace-switcher.tsx:23` `<Menu.GroupLabel>` 未包在 `<Menu.Group>` 内（Base UI 上下文缺失） | V（截图实证） |
| 3 | **MCP 页创建 PAT 100% 失败** | 前端发 `ttl_seconds`（`identity.ts:95`），后端只收 `ttl_hours`（ploykit handlers.go:269）→ 400；页面仅显示 "invalid request body" | V（实测必败）+ A（两端字段对照） |
| 4 | **分享码加入工作区必 404** | 前端调 `POST /api/share-links/join`（`identity.ts` joinByShareCode），ploykit 实际路由 `/api/share-links/redeem` | A（契约对照） |

### P1

- **Agent 详情"权限" tab 崩溃**——与 P0-1 同根因（members envelope），崩溃后该页其余 tab 不可达。
- **zh 包缺 `status.*`（7 键）/`priority.*`（4 键）**——中文 UI 状态/优先级徽章回退英文（R：en.ts:450-463 有、meta.ts 引用）。
- **新表缺"挂了未声明"守卫**——`TestRouteContractWalk` 只做表→mux 单向对账，pins/cli-token/usage/admin×39 等实际挂载路由不在 routeTable（A）。

### P2（卫生/体验）

- 4 个 API 封装无调用方：`quickCreateWorkItem` / `getNotificationSummary` / `archiveAllNotifications` / `deleteCredential`（最后者=凭证创建后前端删不掉，功能缺口）。
- `/api/ui-entries` 双缓存键（`pack-ui-entries` vs `pack-ui-entries-page`）失效不联动。
- `identity.ts` Member 接口重复声明（:21 与 :62）。
- `/chat` 无侧栏入口 + 其 `chat:*` WS 帧在 main.tsx 失效规则表无条目（占位页，待 chat 域接入）。
- workspace MCP 编辑留空 Key 清凭证（UI 已警示；agent 级对话框无警示）。
- `docs/route-compare/orkome_routes.txt` 幽灵条目 `GET /api/agents/{id}/runtime-credentials`（实际只注册 POST）。
- `/uploads/*` 仅 LOCAL 存储模式注册，环境依赖未标注。

## 四、无前端消费面的端点（≈155 条，规划对照信息，非缺陷）

整域零消费：**conversation 全部 30 条**（chat 页占位）、**webhooks 订阅面 9 条**、**quota/audit 3 条**、**admin 39 条**；分散未接：work ~48 条（table/query/batch/metadata/views 等高级面）、automation ~33 条（wakeups 全部+autopilots 大半）、orchestration ~31 条（batches/agent-builder/usage）、identity 框架面大半（SSO/OAuth/MFA/邀请）。

> 读法：这些是"后端先行、前端未接"的规划面——conversation/automation/工作项高级视图是明确的下一批前端工作；admin 面是运营后台入口未建。完整清单见 A 分册 §各域表。

## 五、四波实施交付物在盘点中的位置

- MCP 页三段式 ✓（活体：Packs 空态/新增服务器弹窗/配置复制全可用）
- task-messages 渲染器三卡 ✓（静态确认注册表+灰框兜底硬规则；活体未触发——需真实 pack 任务流，属预期）
- work-item 详情动态 slot tab ✓（静态确认 zod 四触点；活体被 P0-1 崩溃遮蔽，**修复 P0-1 后需回访验证**）
- pack-tool-schema 三端点 / ui-entries slots 扩展 / MCP 端点+9 工具 → A 分册 §13.6 有完整 JSON 形状

## 六、修复建议顺序

1. P0-1 members envelope（一处前端适配修五处崩溃，收益最大）
2. P0-2 workspace-switcher（一行 JSX 归位）
3. P0-3 PAT ttl 字段名（前端改 `ttl_hours`，或后端双收）
4. P0-4 share-links 路由名（前端改 `/redeem`）
5. P1 i18n 补键 + 契约测试补"挂了未声明"反向守卫
6. P2 逐条顺手修（dead code 先不动，等 conversation 接入时一并裁决）

## 七、环境与证据留存

- 后端日志 `/tmp/orkome-server.log`，前端日志 `/tmp/orkome-web.log`；PG 容器 `orkome-pg`（:55432）。
- 盘点测试数据保留在 `inventory-ws`：工单 #1、Agent/小队/技能各 1，均可复查。
- 44 张截图在 `C:\Users\bzlih\AppData\Local\Temp\inv-shots\`。
