# Orkome 活体验证报告（2026-10-12）

> 采集方式：真实浏览器（Playwright headless Chromium，1440x900，zh-CN）逐页访问 localhost:5173，登录态为测试账号 `inventory-test@orkome.dev`（验证码 888888 dev 流程；任务说明中提到的"密码登录"实际不存在——login 页只有验证码流程，已如实记录）。逐页采集：页面存在性、核心区块、可交互功能点、渲染错误、console error，并对代表功能点做真实交互。
> 截图目录：`C:\Users\bzlih\AppData\Local\Temp\inv-shots\`（Git Bash 下即 `/tmp/inv-shots/`）。

## 0. 登录页 /login

| 项 | 结果 |
|---|---|
| 路由 | `/login`（未登录访问任意 `_app` 页会被重定向至此） |
| 核心区块 | 标题"登录 Orkome"、邮箱输入框、发送验证码按钮 |
| 功能点 | 邮箱输入、发送验证码、验证码输入、登录、换个邮箱（返回上一步）、前端校验错误提示 |
| 交互结果 | 输入 inventory-test@orkome.dev → 发送验证码 → 出现验证码输入框 → 输入 888888 → 登录成功，跳转 `/work-items` |
| console | 登录前探测 /api/me 的 401 资源加载 error 2 条（未登录时的预期行为） |
| 问题备注 | 登录页只有"验证码"一种方式，**没有密码登录入口**（与交接说明不符，已如实记录） |
| 截图 | `C:\Users\bzlih\AppData\Local\Temp\inv-shots\login.png` |

## 1. 全部工作 /work-items

| 项 | 结果 |
|---|---|
| 路由 | `/work-items`（登录后默认落地页，`/` 也重定向至此） |
| 核心区块 | 侧栏（10 个导航链接 + 新建项目按钮）、"全部工作"标题、搜索框、项目/状态/负责人 3 个筛选 select、"新建"按钮、"加载更多"按钮；当前工作区无工作项 → 空态"还没有工作项" |
| 功能点 | 搜索框(placeholder"搜索标题或描述…")、项目筛选、状态筛选、负责人筛选、新建、加载更多、新建项目(侧栏+)、顶栏：工作区切换器/WS状态徽标/通知铃铛(0条未读)/暗色切换/EN语言切换/退出登录 |
| 交互结果 | 页面正常渲染，无 console error |
| 截图 | `C:\Users\bzlih\AppData\Local\Temp\inv-shots\work-items.png`、`work-items-after-login.png` |

## 2. 我的工作 /my-work

| 项 | 结果 |
|---|---|
| 路由 | `/my-work` |
| 核心区块 | "我的工作"标题、4 个 tab（待办/已办/已创建/已订阅）、"创建工作项"按钮、分组排序 select；空态"没有待处理的工作" |
| 功能点 | 待办/已办/已创建/已订阅 tab、创建工作项按钮、分组排序下拉 |
| 交互结果 | 页面正常渲染，无 console error |
| 截图 | `C:\Users\bzlih\AppData\Local\Temp\inv-shots\my-work.png` |

## 3. 通知 /notifications

| 项 | 结果 |
|---|---|
| 路由 | `/notifications` |
| 核心区块 | "通知"标题、3 个筛选 tab（全部/未读/已归档）、"全部已读"按钮（**空列表下正确置灰 disabled**）、"归档全部已读"、"加载更多"；空态"暂无通知" |
| 功能点 | 全部/未读/已归档 tab、全部已读、归档全部已读、加载更多、顶栏通知铃铛（带未读数徽标） |
| 交互结果 | 页面正常渲染，无 console error |
| 截图 | `C:\Users\bzlih\AppData\Local\Temp\inv-shots\notifications.png` |

## 4. 侧栏 PackEntries（深链区）

| 项 | 结果 |
|---|---|
| 证据 | `/api/ui-entries` 返回 `{"items":[]}` → 侧栏 pack-entries 区块**不渲染任何按钮**（组件对空列表 return null，属预期）。当前无真实 pack 可点，记录为"无数据，非缺陷"；后续 mcp 页若能装 pack 再回来补测 |

## 5. 工作项新建弹窗 + 工单详情 /work-items/$id ⚠️ 发现崩溃级缺陷

### 5.1 新建工单弹窗（列表页"新建"按钮）
| 项 | 结果 |
|---|---|
| 核心区块 | "创建工作项"对话框：标题输入框、富文本 Markdown 编辑器（contenteditable + 工具栏：加粗/斜体/删除线/行内代码/标题/小标题/无序有序列表/引用/撤销/重做）、项目/状态(Backlog/To do/In progress/In review/Blocked/Done/Cancelled)/优先级(None/Low/Medium/High)/负责人 下拉、"创建"、"提交并继续创建"、"取消" |
| 交互结果 | 填标题"盘点验证工单-20261012"+正文 → 点"创建" → **成功**，列表出现条目 `#1 盘点验证工单-20261012 未分配 To do`，链接 `/work-items/3b8bc342-d79a-4a3f-be4e-5868ca976465` |
| 截图 | `work-items-new-dialog.png`、`work-items-with-item.png` |

### 5.2 工单详情页 —— 崩溃 ❌
| 项 | 结果 |
|---|---|
| URL | `/work-items/3b8bc342-d79a-4a3f-be4e-5868ca976465` |
| 现象 | 整页落入错误边界："页面出错了"，只有 重试/返回上页/回工作项 三个按钮，详情内容完全无法渲染 |
| console 错误原文 | `TypeError: members.data?.find is not a function at resolveUser (src/components/work-item/info-panel.tsx:25:27) at InfoPanel ... The above error occurred in the <InfoPanel> component. React will try to recreate this component tree from scratch using the error boundary you provided, CatchBoundary.` |
| 根因（代码定位） | 后端 `GET /api/workspaces/{id}/members` 实际返回分页 envelope `{"items":[...],"total":1}`；前端 `web/src/api/identity.ts:72` `listMembers` 把响应当 `Member[]` 直接返回（`items ?? []` 拿到的是 envelope 本身），`info-panel.tsx:25` 对 envelope 调 `.find` → 崩。属前后端契约不匹配。 |
| 连带影响 | 详情页内"建任务"等一切详情功能（任务列表、属性编辑、评论等）因崩溃**全部无法验证**；详情页有 通知订阅/刷新 等按钮也无法盘点 |
| 截图 | `work-item-detail.png`（错误边界画面） |

**缺陷影响面（members envelope 契约不匹配，共 7 处消费 `membersQueryOptions`）**：
- `info-panel.tsx:25`、`property-rows.tsx:38`、`comment-thread.tsx:42`、`work-item-list.tsx:46`、`_app.agents.$agentId.tsx:1034` 用 `.find` → 有 userId 时崩溃路径
- `create-work-item-dialog.tsx:140`、`_app.work-items.index.tsx:147` 用 `.length>0` 判断 → 静默降级：**新建弹窗/列表筛选的成员分组不渲染，无法把工单指派给成员**（本次实测弹窗负责人下拉只有"未分配"，与代码吻合）
- work-items 列表页未崩是因为空 assignee 时提前 return，绕过了 `.find`
## 6. 聊天 /chat

| 项 | 结果 |
|---|---|
| 路由 | `/chat`（不在侧栏，深链可达） |
| 核心区块 | 占位页（PagePlaceholder）：标题"聊天"+ 描述"与 agent 的会话、流式消息与待办任务…"，**无任何可交互控件** |
| 交互结果 | 正常渲染，无 console error。属有意的骨架占位页 |
| 截图 | `chat.png` |

## 7. 智能体列表 /agents

| 项 | 结果 |
|---|---|
| 路由 | `/agents` |
| 核心区块 | "智能体"标题、状态筛选 tab（全部/活跃/已归档）、"新建智能体"按钮；空态"还没有智能体" |
| 新建弹窗 | 名称（必填）、描述/System Prompt textarea、模型供应商 select（默认 openai 兼容端点）、模型（必填，placeholder gpt-4o-mini）、base_url（可选，注明 SSRF 校验）、凭据 select（"不使用凭据"）+"＋ 凭据"按钮；"创建"在必填项为空时正确置灰 |
| 交互结果 | 填名称+模型+描述 → 创建**成功**，自动跳转详情 `/agents/0284b43f-be65-4e84-99d6-48ca3f0b427c`，无 console error |
| 截图 | `agents.png`、`agent-create-dialog.png`、`agents-with-agent.png` |

## 8. 智能体详情 /agents/$agentId ⚠️ 权限 tab 崩溃

| 项 | 结果 |
|---|---|
| 路由 | `/agents/0284b43f-be65-4e84-99d6-48ca3f0b427c` |
| 核心区块 | 头部（名称、openai·gpt-4o-mini、公开徽标、归档、取消全部任务）+ 6 个 tab：基本信息/模型/技能/MCP/权限/任务 |
| 基本信息 tab | 名称/描述可编辑、"＋ 添加开场白"（≤3 条）、保存基本信息（未修改时置灰）— 正常 |
| 模型 tab | 供应商/模型/base_url/凭据 select/＋凭据/并发任务上限(数字，当前 6)/保存模型配置、环境变量区（＋添加变量/保存环境变量，值掩码说明）— 正常 |
| 技能 tab | "＋ 绑定技能"按钮、空态"未绑定技能"说明 — 正常 |
| MCP tab | 两个区块："专属服务"（＋添加服务，注明覆盖工作区库）+ "生效配置（合并视图）"（合并视图空态提示）— 正常 |
| **权限 tab** | **崩溃 ❌** 错误边界"页面出错了"，console：`TypeError: (members.data ?? []).filter is not a function at PermForm (src/routes/_app.agents.$agentId.tsx:1848)`。与工单详情同根因：`listMembers` 返回 envelope 而非数组。崩溃后导航按钮被错误边界吞掉，"任务"tab 无法从权限 tab 到达 |
| 任务 tab | 另开新会话直接点击验证（见 §8.1） |
| 截图 | `agent-detail-basic.png`、`agent-detail-model.png`、`agent-detail-skills.png`、`agent-detail-mcp.png`、`agent-detail-perms.png`(崩溃画面) |

### 8.1 任务 tab（另开会话直接点）
| 项 | 结果 |
|---|---|
| 内容 | "还没有执行记录" + 引导文案"指派工作项给它、在评论 @ 它或配置唤醒规则后，任务会出现在这里。" — 正常渲染，无 console error |
| 截图 | `agent-detail-tasks.png` |

## 9. 小队列表 /squads

| 项 | 结果 |
|---|---|
| 路由 | `/squads` |
| 核心区块 | "小队"标题、说明文案（领队编排派工/@小队触发）、"新建小队"按钮 |
| 新建弹窗 | 名称（必填，placeholder"例如：晨报小队"）、编排指令 textarea、一个 checkbox（空成员小队相关选项）；名称为空时"创建"正确置灰 |
| 交互结果 | 填"盘点验证小队" → 创建**成功**，自动跳转详情 `/squads/afd05f4f-9297-4cfd-8e67-12d1a1be39e9` |
| 有数据后 | 列表出现卡片"盘点验证小队 领队：未设置 Mode A 派工 0 成员"，链接到详情 |
| 截图 | `squads.png`、`squad-create-dialog.png`、`squads-list.png` |

## 10. 小队详情 /squads/$squadId

| 项 | 结果 |
|---|---|
| 路由 | `/squads/afd05f4f-9297-4cfd-8e67-12d1a1be39e9` |
| 核心区块 | 名称/编排指令编辑、"删除小队"、"保存基本信息"（未修改置灰）、领队 select（未设置/盘点验证Agent）、协同模式 radio×2（Mode A·异步派工 / Mode B·同轮编队，各带说明）、成员区（添加成员/成员列表/移除） |
| 交互结果 | "添加成员" → 弹对话框（select"选择智能体…"）→ 选"盘点验证Agent" → "添加" → **成功**，成员数 0→1，列表出现"盘点验证Agent member 移除" |
| console | 无 error |
| 截图 | `squad-addmember-dialog.png`、`squad-after-addmember.png` |

## 11. Skills 列表 /skills

| 项 | 结果 |
|---|---|
| 路由 | `/skills` |
| 核心区块 | "Skills"标题、"导入"按钮（URL/zip 导入）、"新建技能"按钮、搜索框（placeholder"搜索技能名称或描述…"） |
| 新建弹窗 | 名称（必填）+ 描述 textarea；必填为空时创建置灰 |
| 交互结果 | 填"盘点验证技能" → 创建**成功**跳转详情 `/skills/b49df100-279e-4944-87f7-f4f7af88c056`；列表出现卡片（手工 2026/10/12） |
| 截图 | `skills.png`、`skill-create-dialog.png`、`skills-list.png` |

## 12. 技能详情 /skills/$skillId

| 项 | 结果 |
|---|---|
| 路由 | `/skills/b49df100-279e-4944-87f7-f4f7af88c056` |
| 核心区块 | 元信息（来源 手工/创建日期）、"删除技能"、左侧文件列表 + "新建文件"、右侧文件编辑器（未选文件时提示"选择左侧文件查看与编辑"） |
| 交互结果 | "新建文件" → 内联表单（文件路径 placeholder"例如 reference.md（SKILL.md 为主文件）" + 文件内容 textarea）→ 填 SKILL.md + 内容 → 创建**成功**：文件列表出现 `SKILL.md 109B`，编辑器打开该文件，出现"保存"按钮（未修改置灰） |
| console | 无 error |
| 截图 | `skill-detail.png`、`skill-after-newfile.png` |

## 13. Packs 与集成 /mcp（三段式）

| 项 | 结果 |
|---|---|
| 路由 | `/mcp` |
| 区块 1：Packs（已安装） | 空态"还没有安装任何 pack"+ 说明（pack-provision CLI 由管理员安装，安装后侧栏出现快捷入口）。与侧栏 PackEntries 空态互相印证 |
| 区块 2：工作区级 MCP 服务器 | "新增服务器"按钮 → 弹窗：名称/类型 select(http/sse)/URL placeholder `https://mcp.example.com/mcp`/API Key password 框（sk-…）/保存（必填为空置灰）。**按钮存在且能打开，未真实提交（无真实外部 MCP 服务可连）** |
| 区块 3：连接你的客户端 | Codex config.toml / Claude Code .mcp.json / 通用 MCP 客户端 三段代码示例，各带"复制"按钮（3 个）；"工具清单（首批）"标注"即将上线"，列出 list_work_items/get_work_item/create_work_item/add_comment/my_work 5 个工具及描述 |
| 访问凭证（PAT） | 名称输入框 + 有效期 select（永不过期/30/90/180 天）+"创建令牌" |
| **交互结果** | 填名称点"创建令牌" → **失败 ❌** `POST /api/tokens` 400：`{"error":"E_BAD_JSON","message":"invalid request body","details":"json: unknown field \"ttl_seconds\""}`。前端 `web/src/api/identity.ts:95` 发 `ttl_seconds`，后端（vendor ploykit `identity/adapters/http/handlers.go:269`）只接受 `ttl_hours` → **PAT 创建 100% 失败**。页面上仅显示 "invalid request body"，无字段级提示 |
| console | 400 资源加载 error（对应上述请求） |
| 截图 | `mcp.png`、`mcp-after-expand.png`、`mcp-pat-retry.png` |

## 14. 自动化 /automation

| 项 | 结果 |
|---|---|
| 路由 | `/automation` |
| 核心区块 | "自动化"标题 + Autopilot 说明（定时/Webhook 触发）、"我的自动化流程"区、空态"暂无自动化流程。" |
| 功能点 | 当前**没有任何可交互控件**（无新建按钮，纯空态展示页） |
| 交互结果 | 正常渲染，无 console error |
| 截图 | `automation.png` |

## 15. 运行日志 /run-logs

| 项 | 结果 |
|---|---|
| 路由 | `/run-logs` |
| 核心区块 | "运行日志"标题、"刷新"按钮、状态 tab（进行中/失败/已完成/全部）、"按智能体筛选" select（已出现"盘点验证Agent"选项，说明数据联通）、空态"当前没有进行中的任务"+引导文案 |
| 交互结果 | 4 个 tab 全部点击切换正常，无报错；无执行记录（未跑过任务，符合预期） |
| 截图 | `run-logs.png`、`run-logs-tabs.png` |

## 16. 设置 /settings

| 项 | 结果 |
|---|---|
| 路由 | `/settings` |
| 核心区块 | "服务端"（版本 unknown）、"会话"（当前账号 inventory-test@orkome.dev、工作区 inventory-ws、退出会话按钮） |
| 功能点 | 会话卡片 + 登出按钮 |
| 交互结果 | 正常渲染，无 console error |
| 截图 | `settings.png` |

## 17. Onboarding /onboarding

| 项 | 结果 |
|---|---|
| 路由 | `/onboarding`（有工作区时直接访问不重定向，可正常到达） |
| 核心区块 | 两个面板："创建工作区"（名称输入 + 创建按钮）、"加入已有工作区"（输入 + 加入按钮，对应邀请码 join） |
| 交互结果 | 正常渲染，无 console error。未真实提交（避免新建多余工作区） |
| 截图 | `onboarding.png` |

## 18. 壳层交互（全页共用）⚠️ 工作区切换器崩溃

| 功能点 | 结果 |
|---|---|
| 通知铃铛 | 点击弹出下拉："通知 / 全部已读 / 暂无通知 / 查看全部通知" — 正常 |
| **工作区切换器** | 点击后**整页崩溃 ❌** 错误边界画面"页面出错了：Base UI: MenuGroupContext is missing. Menu group parts must be used within `<Menu.Group>` or `<Menu.RadioGroup>`"。根因：`workspace-switcher.tsx:23` 的 `<Menu.GroupLabel>` 未包在 `<Menu.Group>` 内。**这是全局性 bug：任何页面点工作区切换器都会白屏到错误边界**（截图 `ws-switcher.png`） |
| 主题切换 | 点击"切换到暗色"→ `<html>` class 变 `dark`，再切回亮色 — 正常（截图 `theme-dark.png`） |
| 语言切换 | EN ↔ 中文：`<html lang>` en/zh 交替、导航文案 My Work ↔ 我的工作，localStorage 持久化 — 正常（截图 `lang-en.png`） |
| 退出登录按钮 | 存在（aria-label"退出登录"），未真实点击以免销毁登录态 |
| WS 状态徽标 | 显示 open（绿色 emerald badge，`data-ws-status="open"`）— websocket 连接正常 |

## 19. 列表页交互补充（work-items / my-work）

| 功能点 | 结果 |
|---|---|
| work-items 搜索"盘点" | 实时过滤：共 1 项（#1 盘点验证工单-20261012）— 正常 |
| work-items 搜索乱串 | "共 0 项 无匹配工作项 换个条件试试" 空态 — 正常 |
| 状态筛选 select | 选项 全部/Backlog/To do/In progress/In review/Blocked/Done/Cancelled，选择后列表刷新 — 正常 |
| my-work 4 tab | 待办(0)/已办(0)/已创建(1 #1 工单)/已订阅(1) 均正常切换，数据与创建动作一致 |
| "重置"筛选按钮 | 搜索后有"重置"按钮出现 — 正常 |

---

## 汇总

### 页面覆盖（17/17）
login、onboarding、my-work、notifications、work-items 列表、work-items 详情、chat、agents 列表、agents 详情（6 tab）、squads 列表、squads 详情、skills 列表、skills 详情、mcp、automation、run-logs、settings。全部可达、无 404、无空白页（chat/automation 为有意的占位/空态页）。

### 功能点统计
共记录约 **70 个**可交互功能点（10 侧栏导航 + 6 顶栏控件 + 各页面按钮/表单/tab/筛选器），真实执行写操作 6 项全部成功：创建工作项、创建 Agent、创建小队、小队添加成员、创建技能、技能内新建 SKILL.md。失败写操作 1 项：创建 PAT（bug #3）。

### 问题清单（按严重度）
| # | 严重度 | 问题 | 根因定位 |
|---|---|---|---|
| 1 | 高 | 工单详情页整页崩溃，无法查看/编辑任何工单 | `web/src/api/identity.ts:72` listMembers 把 `{items:[],total}` envelope 当数组；`info-panel.tsx:25` 调 `.find` 抛 TypeError。同根因波及 property-rows/comment-thread/work-item-list/agent 详情权限 tab（`.filter`），并使新建弹窗成员分组消失（无法指派成员） |
| 2 | 高 | 任意页面点工作区切换器 → 整页崩溃到错误边界 | `web/src/components/workspace-switcher.tsx:23` `<Menu.GroupLabel>` 未包在 `<Menu.Group>` 内（Base UI MenuGroupContext missing） |
| 3 | 高 | MCP 页创建 PAT 100% 失败（400） | 前端发 `ttl_seconds`（`identity.ts:95`），后端只收 `ttl_hours`（ploykit handlers.go:269）→ unknown field 400。页面仅显示 "invalid request body" |
| 4 | 中 | Agent 详情"权限"tab 崩溃（问题 1 同根因，独立入口） | `_app.agents.$agentId.tsx:1848` `(members.data ?? []).filter is not a function` |
| 5 | 低 | 登录页无密码登录入口（与交接说明不符），仅验证码流程 | `web/src/routes/login.tsx`（只有 sendLoginCode/verifyLoginCode） |
| 6 | 信息 | 侧栏 PackEntries 无按钮渲染 | `/api/ui-entries` 返回空 items，组件空列表 return null，预期行为 |
| 7 | 信息 | chat 页为占位页、automation 页无新建入口 | PagePlaceholder / 空态文案，设计如此 |

### 截图清单（`C:\Users\bzlih\AppData\Local\Temp\inv-shots\`，Git Bash `/tmp/inv-shots/`）
login.png、work-items-after-login.png、work-items.png、work-items-new-dialog.png、work-items-with-item.png、work-item-detail.png（崩溃画面）、work-items-search.png、work-items-filtered.png、my-work.png、notifications.png、bell-popover.png、chat.png、agents.png、agent-create-dialog.png、agents-with-agent.png、agent-detail.png、agent-detail-basic.png、agent-detail-model.png、agent-detail-skills.png、agent-detail-mcp.png、agent-detail-perms.png（崩溃画面）、agent-detail-tasks.png、squads.png、squad-create-dialog.png、squads-list.png、squad-addmember-dialog.png、squad-after-addmember.png、skills.png、skill-create-dialog.png、skills-list.png、skill-detail.png、skill-after-newfile.png、mcp.png、mcp-after-expand.png、mcp-pat-retry.png、automation.png、run-logs.png、run-logs-tabs.png、settings.png、onboarding.png、ws-switcher.png（崩溃画面）、theme-dark.png、lang-en.png

### 测试遗留数据（盘点证据，保留）
- 工作项 `#1 盘点验证工单-20261012`（3b8bc342-d79a-4a3f-be4e-5868ca976465）
- Agent `盘点验证Agent`（0284b43f-be65-4e84-99d6-48ca3f0b427c）
- 小队 `盘点验证小队`（afd05f4f-9297-4cfd-8e67-12d1a1be39e9，含 1 成员）
- 技能 `盘点验证技能`（b49df100-279e-4944-87f7-f4f7af88c056，含 SKILL.md 109B）
