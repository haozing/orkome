# Orkome 前端静态盘点（2026-10-12）

> 范围：`web/src/`（React 19 + TanStack Router file-based routes + TanStack Query + zustand + i18next + @ploykit/client WS）。
> 方法：纯静态源码阅读。API 面以 `web/src/api/*.ts` 实际调用为准；后端契约事实源为 `cmd/orkome-server/router_contract_test.go` 与 `e2e/`（另一 agent 负责）。
> 用途：与 API 盘点 agent 交叉对照的接缝文档。

---

## ① 路由总表

### 1.1 独立路由（登录/工作区壳之外）

| 路由路径 | 文件 | 页面名 | nav 入口 | 说明 |
|---|---|---|---|---|
| `/` | `routes/index.tsx` | （无组件） | — | beforeLoad 直接 redirect → `/work-items` |
| `/login` | `routes/login.tsx` | 登录页 | 无（未登录态） | 已登录访问则 redirect → `/work-items` |
| `/onboarding` | `routes/onboarding.tsx` | 工作区引导页 | 无（`_app` 无 workspace 时 redirect 来） | 创建/加入工作区双栏 |

### 1.2 壳路由

| 路由 | 文件 | 说明 |
|---|---|---|
| （root） | `routes/__root.tsx` | 仅 Outlet + ReactQueryDevtools(DEV) |
| `_app` | `routes/_app.tsx` | 登录+工作区壳。beforeLoad：`GET me` 401→`/login`；无 workspace→`/onboarding`。挂 WS 连接、顶栏（WorkspaceSwitcher/WsStatusBadge/NotificationBell/ThemeToggle/LanguageToggle/登出）、左侧 nav |

### 1.3 `_app.*` 页面路由（登录+工作区内）

| 路由路径 | 文件 | 页面名 | nav 入口（`nav.*`） | 分组 |
|---|---|---|---|---|
| `/my-work` | `_app.my-work.tsx` | 我的工作 | nav.myWork（顶部第 1） | 主导航 |
| `/notifications` | `_app.notifications.tsx` | 通知中心 | nav.notifications（顶部第 2） | 主导航 |
| `/work-items` | `_app.work-items.tsx`（纯布局壳，无 UI） | — | — | 布局节点 |
| `/work-items/` | `_app.work-items.index.tsx` | 全部工作 | nav.allWork（顶部第 3） | 主导航 |
| `/work-items/$id` | `_app.work-items.$id.tsx` | 工作项详情 | 列表/通知/任务卡片跳入 | 动态段 `$id` |
| `/agents` | `_app.agents.index.tsx` | Agent 列表 | nav.agents（AI TEAM 组） | AI 团队 |
| `/agents/$agentId` | `_app.agents.$agentId.tsx` | Agent 详情（6 tab） | 列表跳入 | 动态段 `$agentId` |
| `/squads` | `_app.squads.index.tsx` | Squad 列表 | nav.squads（AI TEAM 组） | AI 团队 |
| `/squads/$squadId` | `_app.squads.$squadId.tsx` | Squad 详情 | 列表跳入 | 动态段 `$squadId` |
| `/skills` | `_app.skills.index.tsx` | Skills 列表 | nav.skills（AI TEAM 组） | AI 团队 |
| `/skills/$skillId` | `_app.skills.$skillId.tsx` | Skill 详情（文件编辑器） | 列表跳入 | 动态段 `$skillId` |
| `/run-logs/` | `_app.run-logs.index.tsx` | 运行日志 | nav.runLogs（AI TEAM 组） | AI 团队 |
| `/mcp` | `_app.mcp.tsx` | Packs 与集成（三段式） | nav.mcp（底部） | 底部组 |
| `/automation` | `_app.automation.tsx` | 自动化（Autopilot） | nav.automation（底部） | 底部组 |
| `/settings` | `_app.settings.tsx` | 设置 | nav.settings（底部） | 底部组 |
| `/chat` | `_app.chat.tsx` | 聊天（占位页） | **无 nav 入口** | 占位 |

侧栏 nav 汇总（`_app.tsx` NAV_ITEMS/BOTTOM_NAV_ITEMS/AI_TEAM_NAV_ITEMS + ProjectNav + PackEntries）：
- 顶部组：my-work / notifications / work-items
- ProjectNav：项目列表（点击跳 `/work-items?project=<id>`）
- PackEntries：已安装 pack 的外部跳转按钮（新窗口）
- AI TEAM 组：agents / squads / skills / run-logs
- 底部组：mcp / automation / settings

**静态盘点发现：`/chat` 路由存在但侧栏无任何入口**（PagePlaceholder 占位，i18n 注释提到规划中 WS 作用域 `chat:<sessionId>`）。

---

## ② 逐页功能点明细

统计口径：数据加载 1 个 useQuery/useInfiniteQuery = 1 功能点；表单/弹窗/交互控件/子视图各按粒度计 1。

### 2.1 `/login`（登录页，`routes/login.tsx`）

- 数据：无查询（`meQueryOptions` 仅 beforeLoad 守卫用）
- 表单：两步邮箱验证码登录（react-hook-form + zod）
  - step=email：邮箱 → `sendLoginCode`（POST `/auth/send-code`）
  - step=code：6 位验证码 → `verifyLoginCode`（POST `/auth/verify-code`），成功→`/work-items`
  - 交互：返回改邮箱、ApiError 展示
- 功能点 ≈ 4

### 2.2 `/onboarding`（引导页，`routes/onboarding.tsx`）

- 表单：创建工作区（name+slug，slug 自动联想 suggestSlug）→ `createWorkspace`（POST `/api/workspaces`）
- 表单：按共享码加入 → `joinByShareCode`（POST `/api/share-links/join`）
- 成功后：写 workspace store → refetch `me` → `/work-items`
- 功能点 ≈ 3

### 2.3 `_app` 壳（`routes/_app.tsx`）

- 数据：`meQueryOptions`（beforeLoad ensureQueryData + WorkspaceSwitcher 内 useQuery）
- 交互：登出（POST `/auth/logout` + 清 query cache + 关 WS）、WorkspaceSwitcher 切工作区、ThemeToggle、LanguageToggle
- 侧栏组件：
  - `ProjectNav`：`['projects']`→GET `/api/projects`；新建项目弹窗（POST `/api/projects`，name+description）；星标置顶（localStorage `orkome.starred-projects:<wsId>`）；>5 折叠展开；active 高亮（router search `project`）
  - `PackEntries`：`['pack-ui-entries', wsId]`→GET `/api/ui-entries`；点击 → POST `/api/ui-tokens`（现铸票）→ 拼 `{uiToken}/{workspaceId}` 模板 → `window.open` 新窗
  - `NotificationBell`：`['notifications','unread-count']`→GET `/api/notifications/unread-count`（60s 轮询）+ `['notifications','recent']`→GET `/api/notifications?limit=8`；下拉预览 8 条；单条点击=标已读+跳工作项；全部已读按钮
  - `WsStatusBadge`：`useWsStatus()`（idle/connecting/open/reconnecting/stopped 五态徽章）
- 功能点 ≈ 12

### 2.4 `/my-work`（我的工作，`_app.my-work.tsx`）

- URL search（zod 校验）：`scope`(todo/done/created/subscribed) / `project` / `group`(time/project/status)
- 数据：`['me']`、`['projects']`、`['work-item-statuses']`、`['work-items', filters]`（useInfiniteQuery，30/页）
  - scope=todo：assignee=我 + statuses∈open 类；done：assignee=我 + closed 类；created：creator=我；subscribed：subscriber=我
- 交互：4 个 scope tab（写 URL search）、分组模式 select（time/project/status）、项目筛选 select、分组折叠/展开、加载更多、计数显示
- 派生视图：project 分组 / status 分组（前端分组，非独立路由）
- 弹窗：CreateWorkItemDialog（见 2.13），创建成功且 scope∈{todo,done} 时自动切到 created
- 空态：`myWork.empty_<scope>_*` 四套文案
- 功能点 ≈ 10

### 2.5 `/work-items/`（全部工作，`_app.work-items.index.tsx`）

- URL search：`q`/`project`/`status`/`assignee`（assignee 值形如 `user:xxx`/`agent:xxx`）
- 数据：`['projects']`、`['work-item-statuses']`、`['workspace',ws,'members']`、`['agents']`、`['work-items',filters]`（useInfiniteQuery）
- 交互：搜索框（300ms 防抖写 URL）、项目/状态/负责人（成员+agent 双 optgroup）三个筛选 select、重置筛选、加载更多、计数
- 列表：WorkItemList 表格行（编号/标题/优先级/负责人/状态/起止日/最后活跃）
- 功能点 ≈ 9

### 2.6 `/work-items/$id`（工作项详情，`_app.work-items.$id.tsx`）★四波改动

- URL search：`tab` ∈ 静态四值 `detail|sub|tasks|timeline` **或** `/^pack:/` 动态值；`task`（tasks tab 内展开的任务卡 id）
- 数据：
  - `['work-item', id]`→GET `/api/work-items/{id}`
  - `usePackSlots(id)`：`['pack-ui-entries', wsId]`→GET `/api/ui-entries`，select 展平出 `{pack, slot, key:'pack:<pack>'}` tab 列表（无 slots 的 pack 不出 tab；staleTime 60s）
- 交互/子视图（tab 即 search 状态，刷新可还原）：
  - detail → `DescriptionEditor`（富文本 1.5s 防抖自动保存 PUT；`E_REVISION_CONFLICT` 时拉服务端版本弹"服务端 vs 我的"双栏冲突框：用服务端 / 保留我的重提）
  - sub → `SubWorkItemsList`（`['work-item',id,'children']`→GET `/api/work-items/children?parent=`）
  - tasks → `TaskPanel`（见下）
  - timeline → `TimelineList`（`['work-item',id,'timeline']`→GET `/api/work-items/{id}/timeline?limit=50`）
  - `pack:<pack>` → `PackSlotFrame`（见下）
  - 内容区兜底：tab 值不属于任何已知 tab（pack 卸载后 URL 残留）→ 按 detail 呈现
- 头部：`StatusToggle`（状态胶囊下拉）、`MoreMenu`（新窗打开/复制链接/删除工作项 DELETE+确认框）
- 底部固定区：`LabelsBlock`（标签芯片+增删+新建标签，见下）、`CommentThread`（见下）
- 右栏：`InfoPanel`（StatusRow/PriorityRow/AssigneeRow 三个下拉 + DateRow×2 日期 + ParentRow 父项搜索选择 + project/creator/时间只读行；全部走 `useFieldUpdate`→PUT 带 `expected_revision` 乐观锁，冲突时仅失效重拉）
- 功能点 ≈ 20（含 7 个子组件各自独立功能）

#### TaskPanel（tasks tab，`components/work-item/task-panel.tsx`）

- 数据：`['work-item',id,'tasks']`→GET `/api/work-items/{id}/tasks`；展开卡 `['task',taskId,'messages']`→GET `/api/tasks/{taskId}/messages?limit=200`
- WS：展开时 `useWsScope('task', taskId)` 订阅任务作用域（失效由 main.tsx 中央桥规则驱动：`task:*` 帧失效 `['work-item']/['task']/['workspace-tasks']`）
- 交互：任务卡折叠/展开（写 URL `?task=`）、**审批停靠**（status=awaiting_input → 批准/拒绝按钮 → POST `/api/tasks/{taskId}/approve|deny`）、活跃任务取消按钮（POST `/api/work-items/{wid}/tasks/{tid}/cancel`）、result Markdown 渲染
- 功能点 ≈ 7

#### PackSlotFrame（动态 slot tab 宿主，`components/pack-slot-frame.tsx`）★四波改动

- 铸票：`['pack-slot-token', pack, slot, wsId, workItemId]`→POST `/api/ui-tokens`（60s 短票，**staleTime:0+gcTime:0 不缓存**，切 tab 重挂载必重铸，retry:false）
- URL 模板变量：`{uiToken}/{workspaceId}/{workItemId}` 前端替换（encodeURIComponent）
- iframe `sandbox="allow-scripts allow-same-origin allow-popups"`
- resize 协议：只认同源 `postMessage({type:'pack.resize', height})`，3s 超时回落 `slot.height`，高度上限 8192
- 兜底：铸票失败显示 `slots.mintFailed` 错误框；加载中按声明高度占位；右上角常驻"在控制台打开"外链（iframe 永远不是唯一路径）
- data-testid：`pack-slot-frame`/`pack-slot-error`/`pack-slot-loading`/`pack-slot-external`

#### LabelsBlock（`components/work-item/label-chips.tsx`）

- 数据：`['work-item',id,'labels']`→GET `/api/work-items/{id}/labels`；`['labels']`→GET `/api/labels`（5min）
- 交互：attach（POST `…/labels`）/detach（DELETE `…/labels/{labelId}`，成功后 setQueryData 直写）/新建标签（POST `/api/labels`，6 色轮转）+自动 attach；下拉菜单内联表单
- 功能点 ≈ 4

#### CommentThread（`components/work-item/comment-thread.tsx`）

- 数据：`['work-item',id,'comments']`→GET `…/comments`；`['workspace',ws,'members']`、`['agents']`（mention 候选）
- 触发预览：500ms 防抖 → POST `/api/work-items/{id}/comments/trigger-preview`；`would_trigger`→"将唤醒 @agent"芯片；`blocked`→显示抑制原因
- 发帖：POST `…/comments`（lazy RichEditor，@mention 成员+agent）；两层楼（root+replies 按 parent_id）；Markdown 渲染
- 功能点 ≈ 5

### 2.7 `/agents`（Agent 列表，`_app.agents.index.tsx`）

- 数据：`['agents']`→GET `/api/agents`；`['working-agents']`→GET `/api/working-agents`（活跃任务数徽章）
- 交互：all/active/archived 三过滤 tab（本地 state）；行点击进详情
- 弹窗：CreateAgentDialog（name/description/provider(仅 openai)/model/baseUrl/credential）→ POST `/api/agents`，成功跳详情
- 功能点 ≈ 5

### 2.8 `/agents/$agentId`（Agent 详情，`_app.agents.$agentId.tsx`）

- URL search：`tab` ∈ base/model/skills/mcp/perm/tasks
- 数据：`['agent',id]`→GET `/api/agents/{id}`
- 头部：归档/恢复（POST `…/archive|restore`，两段确认）、取消全部任务（POST `…/cancel-tasks`，两段确认）
- 6 个 tab：
  - **base**：name/description/对话开场 starters（≤3 条增删）→ PUT `/api/agents/{id}`
  - **model**：provider/model/baseUrl/credential（CredentialField）/max_concurrent_tasks → PUT；runtime_config.params 只读 JSON 展示；**EnvEditor**（`['agent-env',id]`→GET/PUT `/api/agents/{id}/env`，KV 行编辑）
  - **skills**：`['agent-skills',id]`→GET `/api/agents/{id}/skills`；启用开关（PUT `…/skills/{skillId}/enabled`）、解绑（DELETE `…/skills/{skillId}`）、SkillPickerDialog（`['skills',q]`→GET `/api/skills[/search?q=]`，400ms 防抖；绑定 POST `…/skills/add`）
  - **mcp**：自有服务器 `['agent-mcp',id]`→GET `…/mcp-servers`；**已解析视图** `['agent-mcp-resolved',id]`→GET `…/mcp-servers?resolved=1`（只读列表）；启用 toggle（PUT `…/mcp-servers/{serverId}`）、删除（DELETE）、McpServerDialog（name/url/apiKey/enabled → POST/PUT）
  - **perm**：visibility 三选（public/public_to/private radio）+ 调用目标 allowlist 编辑（+全体成员 / +指定成员 select）→ PUT `/api/agents/{id}`
  - **tasks**：`['agent-tasks',id]`→GET `/api/agents/{id}/tasks`；状态徽章/触发类型/重试次数/失败原因/跳工作项链接/手动刷新
- 功能点 ≈ 18

### 2.9 `/squads`（Squad 列表，`_app.squads.index.tsx`）

- 数据：`['squads']`→GET `/api/squads`；`['agents']`（leader 名解析）
- 交互：行点击进详情（leader 名/编舞模式徽章/成员数）
- 弹窗：CreateSquadDialog（name/instructions/choreography 复选）→ POST `/api/squads`
- 功能点 ≈ 4

### 2.10 `/squads/$squadId`（Squad 详情，`_app.squads.$squadId.tsx`）

- 数据：`['squad',id]`→GET `/api/squads/{id}`；`['squad-members',id]`→GET `…/members`；`['squad-members-status',id]`→GET `…/members/status`（**10s 轮询**，运行状态圆点+活跃任务数）
- 表单：基本信息（name/instructions/leader 下拉/编舞 A·B radio）→ PUT `/api/squads/{id}`
- 成员管理：添加成员弹窗（agent 下拉+role 输入→POST `…/members`）、role 行内编辑（PATCH `…/members/{agentId}`）、移除（DELETE，两段确认）、删除 Squad（DELETE，两段确认）
- 功能点 ≈ 8

### 2.11 `/skills`（Skills 列表，`_app.skills.index.tsx`）

- 数据：`['skills', q]`→GET `/api/skills` 或 `/api/skills/search?q=`（400ms 防抖）
- 弹窗 1：CreateSkillDialog（name/description→POST `/api/skills`）
- 弹窗 2：ImportSkillDialog（url/zip 双模式 tab；URL→POST `/api/skills/import` {url}；zip→FormData POST `/api/skills/import`）——**文件上传入口**
- 功能点 ≈ 5

### 2.12 `/skills/$skillId`（Skill 详情，`_app.skills.$skillId.tsx`）

- 数据：`['skill',id]`→GET `/api/skills/{id}`；`['skill-files',id]`→GET `…/files`；`['skill-agents',id]`→GET `/api/skills/{id}/agents`（BoundAgentsBar 使用方徽章）
- 文件浏览：左列表（path+大小，SKILL.md 不可删）+右编辑器（textarea）
- 交互：新建文件（path 校验禁 `..`/`/` 开头→PUT `…/files`）、编辑保存（PUT `…/files`）、删除文件（DELETE `…/files/{fileId}`，hover 两段确认）、SKILL.md frontmatter 缺失提示、URL 来源技能刷新（POST `…/refresh`）、删除技能（DELETE `/api/skills/{id}`，两段确认）
- 功能点 ≈ 9

### 2.13 `/run-logs`（运行日志，`_app.run-logs.index.tsx`）

- URL search：`status`(active/failed/completed/all) / `agent`
- 数据：`['agents']`；`['workspace-tasks',{status,agent_id}]`→GET `/api/tasks?status=&agent_id=&cursor=`（useInfiniteQuery）
- 交互：4 状态 tab、agent 筛选 select、手动刷新、加载更多
- 行：状态徽章/agent 名/工作项标题（或"已删除"/"直接任务"）/触发类型/重试次数/失败原因/跳工作项
- **派生视图：TaskTranscriptDrawer**（点击行开右侧抽屉）：
  - `['task',taskId,'messages']`→GET `/api/tasks/{taskId}/messages`
  - `useAgentNameMap()`（`['agents']`）解析 agent 名
  - 展示：状态徽章、失败块（failure_reason+error pre）、result pre、**TaskMessageList 消息流**（走 2.6 的渲染器注册表）
- 功能点 ≈ 8

### 2.14 `/mcp`（Packs 与集成，`_app.mcp.tsx`）★四波改动·三段式

- **第 1 段 PacksInstalledSection**（`components/packs-sections.tsx`）：
  - `['pack-ui-entries-page']`→GET `/api/ui-entries`（只读：pack 名·label·安装日期；v0 明确不做健康列/版本/工具数）
  - 三态：loading/错误/空态（虚线框）；data-testid：`packs-empty`/`packs-list`/`packs-row`
- **第 2 段 WorkspaceMcpSection**（同文件）：
  - `['workspace-mcp', wsId]`→GET `/api/workspaces/{id}/mcp-servers`
  - 列表：name+transport 徽章+`has_credentials` 徽章+url
  - 新建/编辑 WorkspaceMcpDialog（name/url/transport http|sse 下拉/apiKey 密码框→POST/PUT）
  - 编辑留空 apiKey 有"清空凭证"琥珀警示（PUT 全量覆盖语义）
  - 删除（DELETE `…/mcp-servers/{serverId}`，两段确认）；data-testid：`ws-mcp-empty`/`ws-mcp-list`/`ws-mcp-row`
- **第 3 段 客户端接入（原 MCP 内容平移）**：
  - PAT 凭证：`['tokens']`→GET `/api/tokens`；创建（name+TTL 下拉 never/30/90/180 天→POST `/api/tokens`）→**一次性 secret 弹示+复制**；吊销（DELETE `/api/tokens/{id}`，两段确认）；状态徽章（active/expired/revoked）
  - 接入片段：Codex toml / Claude Code .mcp.json / 通用 HTTP 三段 Snippet，均带复制按钮（`${origin}/mcp` 端点）
  - 工具清单卡（list_work_items 等 5 个，coming soon 徽章）
- 功能点 ≈ 12

### 2.15 `/automation`（自动化，`_app.automation.tsx`）

- 数据：`['autopilots']`→GET `/api/autopilots`
- 交互：立即触发（POST `…/{id}/trigger`，反馈条显示 run.status/reason_code）、启停 toggle（PATCH `…/{id}` {enabled}）、删除（DELETE，两段确认）、熔断说明（paused_reason 红字）
- 状态徽章：熔断(fused)/禁用/启用；exec_mode 映射（create_work_item/task_only）
- 功能点 ≈ 6

### 2.16 `/notifications`（通知中心，`_app.notifications.tsx`）

- URL search：`archived`/`unread` 布尔 → 三视图 tab（all/unread/archived）
- 数据：`['notifications','list',view]`→GET `/api/notifications[?archived=true]`（useInfiniteQuery，30/页）；unread 视图为前端过滤
- 行交互：点击=未读则标已读（POST `…/{id}/read|unread`）、归档/取消归档（POST `…/{id}/archive|unarchive`）、类型徽章（5 类映射 i18n）
- 批量：全部已读（POST `…/mark-all-read`）、归档全部已读（POST `…/archive-all-read`，仅 all 视图）
- 实时：无本页监听——`notification:*` 帧由 main.tsx 中央桥失效 `['notifications']` 前缀
- 功能点 ≈ 7

### 2.17 `/settings`（设置，`_app.settings.tsx`）

- 数据：`['config']`→GET `/config`（版本号展示）；`['me']`
- 交互：工作区切换按钮组（同 WorkspaceSwitcher 逻辑）；无工作区→引导链接；401→登录链接
- 功能点 ≈ 3

### 2.18 `/chat`（占位页）

- 仅 PagePlaceholder（标题+描述+skeletonNote），零数据、零交互
- 功能点 = 1

### 2.19 共享基础设施（非页面但承载功能）

| 组件/模块 | 功能 |
|---|---|
| `task-messages.tsx` ★四波改动 | TaskMessageList/Row：text→Markdown；非 text→`ToolResultRenderer`；toolName 由 call_id 回溯 tool_use content 首 token 配对 |
| `task-tool-renderers.tsx` ★四波改动 | **渲染器注册表**（纯组件映射非插件）：只有 `tool_result && !truncated` 才尝试；顺序 JobStatus→JobHandle→Metrics→默认灰框。① JobStatusCard：五态徽章+stage 进度条+error(code/stage_failed 徽章 post_publish 红/pre_publish 中性/retryable)+result.url 可点+内嵌 metrics 网格；② JobHandleCard：{job_id}+复制 chip+可选 state 徽章+「在 pack 控制台打开」深链（查 ui-entries 按 `<pack>_` 最长前缀匹配工具名；POST `/api/ui-tokens` 现铸票；模板含 `{jobId}`）；③ MetricsCard：metrics 键值网格；④ ContractViolationCard（轻量违规态，非三卡）：TOOL_OUTPUT_CONTRACT_VIOLATION 纯文本解析；⑤ DefaultToolResult：灰框兜底（截断/解析失败/未匹配）。硬规则：截断或 JSON.parse 失败一律灰框；先解 CallToolResult 包装（structuredContent 优先，否则 content[0].text 再解 JSON） |
| `use-pack-slots.ts` ★四波改动 | 动态 slot tab 数据源 hook：GET `/api/ui-entries` 展平 `(pack,slot)` 对，tab key=`pack:<pack>`；与 PackEntries/JobHandleCard 共用 `['pack-ui-entries', wsId]` 缓存键 |
| `markdown.tsx` | Markdown 渲染（md.image 占位） |
| `editor/rich-editor.tsx` + `mention.tsx` | 富文本编辑器（lazy 加载）+@mention 弹层 |
| `page-placeholder.tsx` / `route-error.tsx` | 占位页 / 全局错误页+404 页 |
| `theme-toggle.tsx` / `language-toggle.tsx` | 主题切换 / 中英切换 |
| `ui/*` | shadcn 风格基础件（button/badge/dialog/alert-dialog/dropdown-menu/input/tabs/states） |

---

## ③ i18n 命名空间

文件：`i18n/locales/zh.ts`（721 行）/ `en.ts`（758 行）；入肘 `i18n/index.ts`（localStorage `orkome.locale`，浏览器语言检测，fallback en）。扁平单 namespace=`translation`，二级即"命名空间"。

### 3.1 命名空间清单（zh.ts，30 个）

| 命名空间 | 大致键数 | 用途 |
|---|---|---|
| common | ~30 | 通用按钮/状态 |
| nav | ~11 | 侧栏导航 |
| **packs** | **~19** | ★本次新增：集成页第 1/2 段（installedTitle/installedDesc/emptyTitle/emptyDesc/installedAt/wsMcpTitle/wsMcpDesc/addServer/noServers/storedCred/editServer/confirmDelete/deleteServer/serverNameLabel/serverUrlLabel/transportLabel/apiKeyLabel/apiKeyKeepLabel/apiKeyClearWarning/pageTitle/pageIntro） |
| **slots** | **~2** | ★本次新增：slot 宿主（mintFailed/openExternal） |
| agents | ~100（最大） | Agent 详情 6 tab 全部文案 |
| squads | ~35 | Squad 详情 |
| mcp | ~35 | 客户端接入段+PAT |
| skills | ~40 | 技能两页 |
| runLogs | ~20 | 运行日志 |
| myWork | ~25 | 我的工作（含 empty_<scope>_* 动态键） |
| work | ~70 | 工作项列表明细 |
| automation | ~15 | Autopilot |
| desc | ~13 | 描述编辑器（冲突框等） |
| projects | ~12 | 侧栏项目 |
| agentsList | ~12 | Agent 列表 |
| notif | ~20 | 通知 |
| editor | ~8 | 富文本 |
| login | ~8 | 登录 |
| onboarding | ~10 | 引导 |
| settings | ~10 | 设置 |
| squadsList | ~14 | Squad 列表 |
| cred | ~10 | 凭证字段 |
| labels | ~12 | 标签 |
| comment | ~12 | 评论 |
| timeline | ~5 | 时间线 |
| taskStatus | 8 | 任务状态徽章 |
| ws | 7 | WS 状态/工作区切换 |
| **taskMsg** | **~22** | ★本次新增/扩展：消息流 kind 标签（toolUse/toolResult/approval/error）+ truncated/copy/copied + 三卡文案（openConsole/openConsoleFailed/stage/metrics/stageFailed/postPublishWarning/prePublishNote/violationTitle/violationExcerpt）+ `taskMsg.state.*`（queued/running/awaiting_review/published/failed 五态嵌套） |
| err | 5 | 错误页 |
| chat | 2 | 占位 |
| md | 1 | Markdown 占位 |

粗略总键数：zh ≈ 635 行级键、en ≈ 646（含嵌套 taskMsg.state 等）。zh/en 命名空间基本对齐。

### 3.2 本次四波实施新增/扩展（按代码注释与使用点定位）

1. `packs.*`（~21 键）：`_app.mcp.tsx` 三段式的第 1/2 段 + `packs-sections.tsx`
2. `slots.*`（2 键）：`pack-slot-frame.tsx`（铸票失败/外链）
3. `taskMsg.*`（~22 键，含嵌套 `state.*`）：`task-messages.tsx`（kind 标签）+ `task-tool-renderers.tsx`（三卡+违规卡+复制 chip）
4. 工作项详情动态 slot tab 复用 `work.tab*` 既有键，无新增

### 3.3 i18n 问题

- **zh.ts 缺 `status.*`（7 键）与 `priority.*`（4 键）命名空间**——`en.ts` 有（450-463 行），`meta.ts` 的 STATUS_META/PRIORITY_META 引用 `status.todo`/`priority.high` 等键。中文界面下这些键 fallback 到英文（内置状态徽章、优先级列在中文显示英文）。建议补齐 zh。

---

## ④ 页面→API 交叉表

前端实际调用（`web/src/api/*.ts` 封装 → HTTP）。`api.get/post/put/patch/delete` 基址约定：`/auth/*`、`/config` 为 ploykit 框架路径；`/api/*` 为业务路径（workspace 经 `X-Workspace-Id` 头或 URL path，由 setWorkspaceIdProvider 注入）。

### 4.1 端点总表（按封装文件）

**identity.ts**
| 封装函数 | HTTP | 使用页面/组件 |
|---|---|---|
| getServerConfig | GET `/config` | settings |
| sendLoginCode | POST `/auth/send-code` | login |
| verifyLoginCode | POST `/auth/verify-code` | login |
| logout | POST `/auth/logout` | _app 壳 |
| meQueryOptions（=GET `/auth/me` + GET `/api/workspaces`） | GET×2 | _app 壳/login/onboarding/settings/workspace-switcher/各处 |
| createWorkspace | POST `/api/workspaces` | onboarding |
| joinByShareCode | POST `/api/share-links/join` | onboarding |
| listMembers | GET `/api/workspaces/{id}/members` | work-items 筛选/详情 InfoPanel/agent perm/settings(隐式) |
| listTokens / createToken / revokeToken | GET/POST `/api/tokens`、DELETE `/api/tokens/{id}` | mcp 第 3 段 |

**work.ts**
| 封装函数 | HTTP | 使用页面/组件 |
|---|---|---|
| listWorkItems | GET `/api/work-items?limit&cursor&project&search&status*&assignee&creator&subscriber` | my-work / work-items 列表 / ParentRow |
| quickCreateWorkItem | POST `/api/work-items/quick-create` | **无调用方（死代码）** |
| createWorkItem | POST `/api/work-items` | CreateWorkItemDialog |
| listChildWorkItems | GET `/api/work-items/children?parent=` | 详情 sub tab |
| getWorkItem | GET `/api/work-items/{id}` | 详情页 / ParentRow / DescriptionEditor 冲突恢复 |
| listStatuses | GET `/api/work-item-statuses` | 列表筛选/my-work 分组/详情 InfoPanel/创建弹窗 |
| updateWorkItem | PUT `/api/work-items/{id}`（含 expected_revision） | useFieldUpdate/DescriptionEditor |
| deleteWorkItem | DELETE `/api/work-items/{id}` | MoreMenu |
| listComments | GET `/api/work-items/{id}/comments` | CommentThread |
| previewCommentTrigger | POST `/api/work-items/{id}/comments/trigger-preview` | CommentThread |
| createComment | POST `/api/work-items/{id}/comments` | CommentThread |
| listLabels / createLabel | GET/POST `/api/labels` | LabelsBlock |
| listWorkItemLabels / attachLabel / detachLabel | GET/POST `/api/work-items/{id}/labels`、DELETE `…/labels/{labelId}` | LabelsBlock |
| listTimeline | GET `/api/work-items/{id}/timeline?limit=` | 详情 timeline tab |
| listProjects / createProject | GET/POST `/api/projects` | ProjectNav/my-work/work-items 筛选/创建弹窗 |

**orchestration.ts**
| 封装函数 | HTTP | 使用页面/组件 |
|---|---|---|
| listAgents / getAgent / createAgent / updateAgent | GET `/api/agents`、GET/POST/PUT `/api/agents/{id}` | 几乎全站（筛选/assignee/mention/leader 下拉等）+agent 两页 |
| archiveAgent | POST `/api/agents/{id}/archive\|restore` | agent 详情 |
| cancelAgentTasks | POST `/api/agents/{id}/cancel-tasks` | agent 详情 |
| getAgentEnv / putAgentEnv | GET/PUT `/api/agents/{id}/env` | agent model tab |
| listWorkingAgents | GET `/api/working-agents` | agent 列表 |
| listAgentMcp / resolvedAgentMcp | GET `/api/agents/{id}/mcp-servers[?resolved=1]` | agent mcp tab |
| upsertAgentMcp / updateAgentMcp / deleteAgentMcp | POST/PUT/DELETE `/api/agents/{id}/mcp-servers[/{serverId}]` | agent mcp tab |
| listAgentTasks | GET `/api/agents/{id}/tasks` | agent tasks tab |
| listWorkspaceTasks | GET `/api/tasks?status&agent_id&cursor` | run-logs |
| listWorkItemTasks | GET `/api/work-items/{id}/tasks` | TaskPanel |
| listTaskMessages | GET `/api/tasks/{taskId}/messages?limit=` | TaskPanel/run-logs 抽屉 |
| settleTask | POST `/api/tasks/{taskId}/approve\|deny` | TaskPanel 审批 |
| cancelWorkItemTask | POST `/api/work-items/{wid}/tasks/{tid}/cancel` | TaskPanel |

**packs.ts** ★四波
| 封装函数 | HTTP | 使用页面/组件 |
|---|---|---|
| listPackEntries | GET `/api/ui-entries` | usePackSlots / PacksInstalledSection（pack-entries.tsx 与 task-tool-renderers.tsx 另有裸 api.get 同端点） |
| listWorkspaceMcp / createWorkspaceMcp / updateWorkspaceMcp / deleteWorkspaceMcp | GET/POST `/api/workspaces/{id}/mcp-servers`、PUT/DELETE `…/{serverId}` | WorkspaceMcpSection |

**automation.ts**
| listAutopilots | GET `/api/autopilots` | automation |
| setAutopilotEnabled | PATCH `/api/autopilots/{id}` | automation |
| triggerAutopilot | POST `/api/autopilots/{id}/trigger` | automation |
| deleteAutopilot | DELETE `/api/autopilots/{id}` | automation |

**credentials.ts**
| listCredentials | GET `/api/credentials` | CredentialField（agent 创建/详情） |
| createCredential | POST `/api/credentials` | CredentialField |
| deleteCredential | DELETE `/api/credentials/{id}` | **无调用方（死代码）** |

**notification.ts**
| listNotifications | GET `/api/notifications?archived&limit&cursor` | 通知页/NotificationBell |
| getUnreadCount | GET `/api/notifications/unread-count` | NotificationBell |
| getNotificationSummary | GET `/api/notifications/summary` | **无调用方（死代码）** |
| markNotificationRead | POST `/api/notifications/{id}/read\|unread` | 通知页/Bell |
| markAllNotificationsRead | POST `/api/notifications/mark-all-read` | 通知页/Bell |
| archiveNotification | POST `/api/notifications/{id}/archive\|unarchive` | 通知页 |
| archiveAllNotifications | POST `/api/notifications/archive-all` | **无调用方（死代码）** |
| archiveAllReadNotifications | POST `/api/notifications/archive-all-read` | 通知页 |

**skills.ts**
| listSkills | GET `/api/skills` 或 `/api/skills/search?q=` | skills 列表/SkillPicker |
| createSkill | POST `/api/skills` | CreateSkillDialog |
| importSkillFromUrl / importSkillZip | POST `/api/skills/import`（JSON / FormData） | ImportSkillDialog |
| getSkill | GET `/api/skills/{id}` | skill 详情 |
| getSkillFiles / putSkillFile / deleteSkillFile | GET/PUT `/api/skills/{id}/files`、DELETE `…/files/{fileId}` | skill 详情 |
| refreshSkill | POST `/api/skills/{id}/refresh` | skill 详情（url 来源） |
| deleteSkill | DELETE `/api/skills/{id}` | skill 详情 |
| listAgentSkills / addAgentSkill / setAgentSkillEnabled / removeAgentSkill | GET `/api/agents/{id}/skills`、POST `…/skills/add`、PUT `…/skills/{skillId}/enabled`、DELETE `…/skills/{skillId}` | agent skills tab |
| listSkillAgents | GET `/api/skills/{skillId}/agents` | skill 详情 BoundAgentsBar |

**squads.ts**
| listSquads / getSquad / createSquad / updateSquad / deleteSquad | GET/POST `/api/squads`、GET/PUT/DELETE `/api/squads/{id}` | squads 两页 |
| listSquadMembers / addSquadMember | GET/POST `/api/squads/{id}/members` | squad 详情 |
| updateSquadMemberRole / removeSquadMember | PATCH/DELETE `/api/squads/{id}/members/{agentId}` | squad 详情 |
| squadMembersStatus | GET `/api/squads/{id}/members/status` | squad 详情（10s 轮询） |

**散落在组件里的裸调用**（不走 `web/src/api/` 封装）：
| 调用点 | HTTP | 组件 |
|---|---|---|
| PackEntries | GET `/api/ui-entries`、POST `/api/ui-tokens` | `pack-entries.tsx`（侧栏） |
| mintUiToken | POST `/api/ui-tokens` | `pack-slot-frame.tsx` ★ |
| JobHandleCard 深链 | GET `/api/ui-entries`（listPackEntries 封装）、POST `/api/ui-tokens` | `task-tool-renderers.tsx` ★ |

### 4.2 交叉对照备注（给 API 盘点 agent）

1. 前端未触达的 GET `/api/notifications/summary` 已封装但无调用方——后端如保留了 summary 端点属"前端未消费"；archive-all 同理。
2. POST `/api/work-items/quick-create`、DELETE `/api/credentials/{id}` 前端封装了但无调用方。
3. `POST /api/ui-tokens` 与 `GET /api/ui-entries` 是 pack 链路的前端核心端点，共 4 个调用点（侧栏跳转/详情 slot iframe 铸票/JobHandle 深链/两处只读列表）。
4. workspace 维权传递双轨：多数端点靠 `X-Workspace-Id` 头（setWorkspaceIdProvider 注入 zustand currentId）；workspace MCP 是 URL path 内嵌 `{id}`（packs.ts 注释确认后端 routes.go 差异）。
5. WS 握手：`GET /ws?workspace_id=`（http(s) 升级 ws(s)），workspace ridealong 在 URL 参数（realtime/client.ts 注释）。

---

## ⑤ 实时面（WS）汇总

- 客户端：`realtime/client.ts`——@ploykit/client `WsClient` 单例，URL 动态取 zustand 当前工作区；`_app` 挂载时 connect，登出 close，切工作区 reconnect（`services/workspace-switch.ts`：removeQueries 保留 `me`/`config`）。
- 订阅：`useWsScope('task', taskId)`（TaskPanel 展开卡）。其余依赖 hub 自动加入的 workspace 作用域。
- 失效桥（`main.tsx` `wsInvalidationRules`，ploykit `bridgeWsToQuery`）：
  | 帧前缀 | 失效 queryKey 前缀 |
  |---|---|
  | `work_item:*` | `['work-items']`, `['work-item']` |
  | `task:*` | `['work-item']`, `['task']`, `['workspace-tasks']` |
  | `comment:*` | `['work-item']`, `['work-items']` |
  | `notification:*` | `['notifications']` |
- 轮询兜底：NotificationBell unread-count 60s；squad 成员状态 10s。
- 状态可视化：WsStatusBadge（idle/connecting/open/reconnecting/stopped）。

---

## ⑥ 静态盘点发现的问题

1. **【i18n 缺口】zh.ts 缺 `status.*`（7 键）与 `priority.*`（4 键）**：en.ts 有、zh.ts 无。中文 UI 下内置工作项状态徽章（Backlog/To do/In progress…）与优先级（None/Low/Medium/High）回退显示英文。文件：`web/src/i18n/locales/zh.ts`（对照 `en.ts` 450-463 行；引用方 `components/work-item/meta.ts`）。
2. **【无 nav 入口】`/chat` 路由存在但侧栏无入口**：`_app.chat.tsx` 是 PagePlaceholder 占位（`chat.title/desc`），用户只能手输 URL 到达。属规划中页面，建议确认是否保留路由。
3. **【死代码·API 封装】4 个已封装无调用方**：
   - `work.ts quickCreateWorkItem`（POST `/api/work-items/quick-create`）
   - `notification.ts getNotificationSummary`（GET `/api/notifications/summary`）
   - `notification.ts archiveAllNotifications`（POST `/api/notifications/archive-all`）
   - `credentials.ts deleteCredential`（DELETE `/api/credentials/{id}`）
   前三者后端端点可能仍活着（供 API 盘点对照确认）；deleteCredential 意味着凭证创建后前端无法删除。
4. **【双查询键冗余】GET `/api/ui-entries` 有两个缓存键**：`['pack-ui-entries', wsId]`（PackEntries/usePackSlots/JobHandleCard）与 `['pack-ui-entries-page']`（PacksInstalledSection）。同端点不同键，失效不联动（当前无失效逻辑，影响小，但属约定不一致）。
5. **【全量覆盖陷阱·已缓解】workspace MCP PUT 全量覆盖 credentials**：`packs.ts` 注释明确"credentials 不传会清空已存凭证"；页面侧已用琥珀警示+Bearer 形状回传缓解，但 agent 级 MCP（`orchestration.ts` McpServerDialog）无对应警示（其 credentials 仅在输入非空时携带，语义不同，建议 API 对照后端行为确认）。
6. **【无 TODO/FIXME 标记】**：`web/src` 下 grep TODO/FIXME/HACK/XXX 无命中，代码卫生良好。
7. **【占位契约】`/chat` i18n 描述写明规划 WS 作用域 `chat:<sessionId>` 与 `chat:message/chat:done` 帧**：实时桥（main.tsx 规则表）尚无对应条目，实施时需补。
8. **【契约面提示】`Identity.ts` 内 `Member` 接口重复声明两次**（行 21-27 与 62-70，后者多 email/display_name），TS 下后者生效；建议合并以免漂移。

---

## 附：统计速览

- 路由文件 21 个 → **页面 17 个**（含 1 个占位、1 个纯布局壳、2 个独立路由），动态段 4 个（$id/$agentId/$squadId/$skillId）
- **派生子视图 9 个**：TaskTranscriptDrawer、TaskPanel、PackSlotFrame、DescriptionEditor、CommentThread、LabelsBlock、SubWorkItemsList、TimelineList、InfoPanel+ProjectNav/PackEntries/NotificationBell（壳级 3 个）
- 功能点总计约 **150**（明细见 ② 各页末行）
- 端点：9 个 API 文件共封装 **约 75 个函数**，覆盖 **约 70 个唯一 HTTP path**（含方法区分）；另有 2 个组件级裸调用端点（`/api/ui-tokens`、`/api/ui-entries` 裸用）
- i18n：**30 个命名空间（zh）/ 33（en）**，总键数 zh≈635 / en≈646
