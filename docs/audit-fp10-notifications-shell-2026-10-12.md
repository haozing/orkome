# FP-10 审计：通知系统与全局壳层（铃铛/已读未读/偏好/主题语言/WS 徽标）

> 日期：2026-10-12 · 环境：后端 :8080（dev，日志 /tmp/orkome-server.log）+ 前端 :5173（仅静态+API 实测，未开浏览器）
> 账号：`inventory-test@orkome.dev` · 工作区 `inventory-ws`（e83dd858-7412-4625-b048-0741e5ae6552）
> 方法：七步审计（全链梳理 → 契约对齐 → curl 实测 → 代码质量 → 后端正确性 → 测试覆盖 → 缺陷清单）
> 结论先行：**P0=0，P1=5，P2=10**。壳层六控件与通知主流程（列表/未读/已读/归档/偏好 API/WS 已读同步）全部实测可用、隔离正确、幂等可靠；但「新通知到达」环节是重灾区：任务完成事件双发致通知重复、评论创建必 500（评论实际已落库）、作者会收到自己评论的通知、新通知无 WS 帧只靠 60s 轮询、通知页"加载更多"因空串游标恒显示且点击即重复列表。另有通知偏好**零 UI**（三组静音只能裸调 API）。

---

## 一、全链梳理

### 1.1 壳层（web/src/routes/_app.tsx）

```
header 右侧六控件：WsStatusBadge → NotificationBell → ThemeToggle → LanguageToggle → 登出(图标按钮)
左侧 nav：my-work / notifications(侧栏也有通知入口) / work-items + ProjectNav + PackEntries + AI 团队组 + mcp/automation/settings
```

- WS 连接：`_app.tsx:78` 挂载时 `wsClient.connect()`；登出 `signOut`（:79-85）= logout API → 清 workspace store → `queryClient.clear()` → `wsClient.close()` → 跳 /login，顺序正确。
- 登出按钮 aria-label 有、无文字；`logout()` → `POST /auth/logout`（api/identity.ts:53）。无 try/finally——logout 网络失败时不跳转（P2-10 级，此处并入 P2 清单第 8 条备注）。

### 1.2 铃铛链（components/notification-bell.tsx → api/notification.ts → 产品投影端点）

```
NotificationBell
├─ useQuery ['notifications','unread-count'] → GET /api/notifications/unread-count（60s 轮询，:81）
├─ useQuery ['notifications','recent']     → GET /api/notifications?limit=8（无轮询）
├─ 全部已读 → POST /api/notifications/mark-all-read（:87，无本地 invalidate，靠 WS 回声帧）
└─ PreviewRow 点击 → POST /{id}/read →（有 work_item_id 才跳转详情页）
```

- WS 失效：`main.tsx:21` `{ match: /^notification:/, invalidate: [['notifications']] }` → 六帧全部打中铃铛两查询与通知页。
- 实测徽标：`99+` 封顶（:103）、0 时隐藏、`aria-label` 带计数。设计合理。

### 1.3 通知生成链（谁写入 notification 表）

| 事件源 | 写入路径 | type 值 | 接收人 | WS 帧 |
|---|---|---|---|---|
| 评论创建（work 域 CommentCreated） | bus PhaseProject 投影 `wiring_orchestration.go:196-220` → `NotifyService.Handle`（notification/app/service.go:80-121） | `comment.created` | 工单订阅者 + @提及（ParseMentionedUsers） | **无** |
| 任务完成/失败（TaskCompleted/TaskFailed） | 同上投影 | `task.completed` / `task.failed` | 订阅者 + 提及 | **无** |
| 任务完成（结算直写） | `orchestration/app/service.go:758` `WriteTaskCompleted` | `task_completed`（下划线） | 仅 OriginatorUserID | **无** |
| 审批请求（park） | `service.go:771` `WriteApprovalRequest` | `approval_request` | OriginatorUserID | **无** |
| 评估报告 | `service_eval.go:95` `WriteEvalReport` | `eval_report` | OriginatorUserID | **无** |
| Autopilot 熔断 | `cmd/orkome-server/main.go:259` `WriteNotice` | `autopilot_paused` | ws creator | **无** |
| Wakeup 成员 | `wiring_automation.go:39` `WriteNotice` | `wakeup_member` | 指定 uid | **无** |

**关键结论：八种写入路径零一种在写入时推 WS 帧。** `notification:*` 六帧只在读/未读/归档操作时由 `NotificationService.emit`（notification/app/service.go:309-313，SetUserFrames ← wiring_ws.go:97-105 notifUserFrames → hub.SendToUser）发出。→ 见 P1-4。

幂等：投影 dedup key = `<eventID>:<uid>`，直写 = `task_completed:<uid>:<wi>` / `approval:<task>` / `eval:<task>`，全部落框架部分唯一索引 `uq_notification_dedup(user_id, dedup_key)` + `ON CONFLICT DO NOTHING`（pgrepo/repo.go:33-42）。outbox relay 重放同 envelope 因 EventID 相同被 dedup 吸收——**重放安全，但双发事件挡不住**（不同 EventID）→ P1-2。

### 1.4 WS 帧链（六帧核对）

`contract/events.go:10-15`：`notification:read|unread|batch_read|archived|unarchived|batch_archived`，与 inventory-api §12.3 完全一致。发射点五处：MarkRead→read/unread、MarkAllRead→batch_read、Archive→archived/unarchived、ArchiveAll→batch_archived、ArchiveAllRead→batch_archived（n>0 才发，service.go:214-217）。payload `{id}` 或 `{}`。

- 定位：`hub.SendToUser`（vendor ploykit platform/wsx/hub.go:149-151）→ room `user:{id}` 广播；cookie 握手自动 join user scope（hub.go:271）→ **多设备全收**，A 设备标记已读，B 设备铃铛同步刷新。实测链路静态核实（帧内容无法 curl 观察，wiring_ws.go:97-105 接线确认）。
- 前端去重：WsClient event_id 环形去重（web/test/lib/ws.test.ts 覆盖），bridge 重连后全量 invalidate（resync，ws-query-bridge.test.ts 覆盖）——亮点。

### 1.5 双面重叠设计核实（badge vs unread-count 等 3 组）

| ploykit 幸存 3 条 | 产品对应 | 语义差异（核实属实） |
|---|---|---|
| GET /api/notifications/badge（user 全局，可带 ws 过滤，bell/pgrepo.go:68） | GET /api/notifications/unread-count（ws+user 双过滤，repo.go:82-89） | 跨工作区 vs 工作区内 |
| POST /api/notifications/read-all（user 全局，**无 WS 帧**，notifyhttp routes.go:99-105） | POST /api/notifications/mark-all-read（ws 内、排除已归档、发 batch_read 帧） | 范围+副作用不同 |
| PUT /api/notification-preferences/{type}（框架 per-type 行，(user_id,notification_type) PK） | GET/PATCH/PUT /api/notification-preferences（'mute' 哨兵行，1034 扩列） | 行形状不同，部分唯一索引隔离 |

被顶替 4 条（`wiring_notification.go` supersededNotifyPatterns）经 filtered router 剔除防 ServeMux panic——机制、注释、契约 walk（router_contract_test.go:169-179 通知 19 条全在表）三面齐备。**重叠为刻意设计成立，无缺口**。但注意：框架 `PUT prefs/{type}` 写的 per-type 行（in_app_enabled/email_enabled）在产品写路径**无任何读方**（产品只读 mute 行）——是个死开关，见 P2-1。

### 1.6 偏好模型（1034 迁移）

`1034_notification_preference_ext.up.sql`：框架表扩 3 列 `workspace_id` / `muted_types text[]` / `muted_work_item_ids text[]`，静音组存 `notification_type='mute'` 哨兵行（部分唯一索引 `uq_notification_preference_mute(user_id, workspace_id)`）；会话静音以 `session:<id>` 前缀编码进 muted_types（pgrepo splitMutedTypes/mergeMutedTypes，:185-203）。三组谓词 domain/mute.go（`*` 通配支持）。校验：>50 types / >200×2 拒绝、`session:` 前缀保留字拒绝（app/service.go:237-247）。

**UI 露出：0%。** settings 页只有 Server/Session 两节（_app.settings.tsx:22-88），无任何通知偏好控件；GET/PATCH/PUT 三端点前端零调用。→ P2-1。

## 二、契约对齐（envelope 逐字段）

| 端点 | 实测 envelope | 前端消费 | 判定 |
|---|---|---|---|
| GET /api/notifications | `{items, next_cursor}`；items 空时 `null`（非 `[]`）、耗尽时 `next_cursor:""`（非 null） | `?? []` 兜底；**空串游标引发 P1-3** | ⚠ |
| GET /api/notifications/unread-count | `{count}` | ✓ | ✓ |
| GET /api/notifications/summary | `{unread, today_new}` | **无调用方**（已知 dead code） | ✓ |
| GET /api/notifications/facets | `{facets:[]}`（空态返回数组非 null，routes.go:134-136 显式兜底） | 无调用方 | ✓ |
| GET …/archived | 同列表 | 前端用 `?archived=true` 而非此端点（双入口皆通） | ✓ |
| POST /{id}/read\|unread\|archive\|unarchive | 204 空体 | ✓ | ✓ |
| POST mark-all-read / archive-all | 204 空体 | ✓ | ✓ |
| POST archive-all-read | `{archived: n}` | ✓ | ✓ |
| GET/PATCH/PUT /api/notification-preferences | `{muted_types, muted_work_item_ids, muted_conversation_ids}`；PATCH 只改提供键、PUT 全量替换、合并后校验 | **零调用** | ⚠（UI 缺） |
| GET /api/notifications/badge（框架） | `{count}` user 全局 | 无（产品用 unread-count） | ✓ |
| POST /api/notifications/read-all（框架） | 204 | 无 | ✓ |
| PUT /api/notification-preferences/{type}（框架） | `{notification_type, email_enabled, in_app_enabled, updated_at}` | 无 | ⚠（死开关） |

错误面：缺 X-Workspace-Id → 400 E_VALIDATION；非成员工作区 → 403 E_FORBIDDEN（实测）；**非法 UUID / 非法游标 → 500 E_INTERNAL**（应 400）→ P2-4。

## 三、curl 实测记录（2026-10-12，全通过 cookie+CSRF 会话）

1. 空态：unread-count=0 / summary={0,0} / facets={[]} / list=`{items:null,next_cursor:""}` / prefs 三键全 null ✓
2. 制造通知：向工单 #1 POST 评论 → **HTTP 500 E_INTERNAL**，但评论落库 ✓、未读 +1 ✓（通知投影成功）。第二次在另一工单复测，同样 500 → 确定性。日志仅 access 行，无错误详情（可观测性缺口）。
3. **自通知实证**：唯一动作人是自己，通知仍生成给自己（"New comment"，body=评论原文）。
4. 已读/未读往返：read→204、count 2→1；unread→204、count→2；mark-all-read→204、count→0 ✓
5. 归档族：archive→主列表减 1、archived 列表 +1；unarchive→回主列表；archive-all-read→`{archived:2}`；archive-all→主列表空 ✓
6. 静音过滤：muted_types=["comment.created"] 后评论 → count 保持 0 ✓（三组静音谓词生效）
7. 偏好往返：PATCH 三键 → GET 一致 → PATCH 单键（其余保留）→ PUT 清空 → 非法 JSON/`session:` 前缀/>50 项 全部 400 E_VALIDATION ✓
8. 游标分页：limit=1 → `next_cursor:"2026-10-12T11:11:57.65878+08:00"`；**原样回传（`+` 未编码）→ 500**；URLSearchParams 编码后（`%2B`）→ 第二页正确返回 ✓（前端安全，API 直连方踩坑）
9. 守卫：无 workspace 头 → 400；随机 workspace → 403；不存在的 UUID 标已读 → 204（幂等静默）；非 UUID 路径参数 → 500
10. 框架三端点：badge `{count:0}` ✓；PUT prefs/task.completed → 200 回显 ✓（但见 P2-1 死开关）；read-all 未测写（避免污染跨工作区数据，语义已静态核实）

## 四、代码质量要点

- **轮询 vs WS 互补**：设计意图是"WS 推已读状态、轮询兜新通知"，但 recent 列表连轮询都没有（staleTime 15s + 全局 `refetchOnWindowFocus:false`），WS 断线时铃铛下拉内容**永不更新**（徽标至少 60s 一次）。操作反馈同样依赖 WS 回声：铃铛"全部已读"点击后无本地 invalidate（notification-bell.tsx:87），WS 断开时点了没反应直到 60s 轮询。
- **键盘导航**：铃铛为 Base UI DropdownMenu，行内是原生 `<button>` 可 Tab 聚焦；但行不是 Menu.Item，点击后弹层不关闭（跳转在弹层后面发生）；通知页 tab 用 `role="tablist/tab"` 却只有原生 focus、无方向键语义（a11y 形式化，P2）。
- **主题/语言持久化**：主题 zustand persist `orkome.ui`（localStorage，stores/ui.ts），`index.html` 内联脚本防暗色 FOUC——**亮点**；语言 `orkome.locale`（i18n/index.ts），探测链 localStorage → navigator.language → en。两者均**纯设备本地**，不随账号走（多设备不同步，与通知已读状态的跨设备同步形成反差）。
- **i18n 完整性**：zh 639 键 vs en 650 键，zh 缺 11 键 = `status.*`×7 + `priority.*`×4（en.ts 有、meta.ts 引用；中文下状态/优先级徽章显示原始键路径）——语言切换的全局波及，inventory 已录 P1，FP-10 的语言切换是其触发面。壳层自身命名空间（notif.*/ws.*/common.*/nav.*/settings.*）双语齐全 ✓。
- **通知文案不可国际化**：标题/正文硬编码英文（"New comment"、"Task completed"、"Agent X completed the task"、"needs your approval"）且无参数（谁评论的？哪个工单？）——中文用户收到英文通知；类型徽章 8 种落库值只映射 5 种（`task_completed`/`autopilot_paused`/`wakeup_member` 原样显示）→ P2-2。

## 五、后端正确性

| 维度 | 判定 | 证据 |
|---|---|---|
| 投影幂等（同事件不双发） | **半成立** | 同 EventID 重放被 `uq_notification_dedup` 吸收 ✓；但 TaskCompleted 事件本身双发（两个 EventID）→ P1-2 |
| 已读按用户隔离 | ✓ | 全部 UPDATE/SELECT 带 `workspace_id=$1 AND recipient_user_id=$2`（pgrepo/repo.go 各方法）；非本用户通知 id 静默 0 行 |
| 跨工作区隔离 | ✓ | wsFromHeader 成员校验（routes.go:55-76），实测 403 |
| 偏好校验 | ✓ | 容量上限 + 保留前缀 + 合并后整体校验，实测 400 |
| SendToUser 定位 | ✓ | user scope 房间广播，cookie 握手自动 join（hub.go:271），多设备可达；PAT auth 路径同样 join（hub.go:380） |
| 静音 fail-open | 可接受 | GetPreference 出错时通知照发（service.go:109-110）——宁可多勿丢，有测试覆盖 |
| 归档清理 | ✓ | ArchiveSweeper 每小时、30 天保留、批量 500，DeleteArchivedBefore 有测试 |
| 截断 | ✓ | title 200 / body 2000|4000 rune-safe（text.go），TruncateRunes 边界手推无越界 |

## 六、测试覆盖

- **后端 notification 5 包全绿**（`go test ./internal/notification/...`）：集成 5 测（prefs patch 合并、summary/facets/archived、直写静音过滤、截断统一、归档清理）+ app 3 测（三组静音、直写路径、第三组 patch）+ domain 2 + pgrepo 映射 2 + workers 4。dedup 幂等在集成 seed 中验证（重复 dedup 断言单行）。
- **缺口**：无 cursor 分页行为测试（空串耗尽语义/非法游标 500 都没测）；无 NotifyService.Handle 的 订阅者+提及合并、自通知排除（无此逻辑可测）测试；无"事件双发时投影不重复"护栏（本来就没有）。
- **1034 迁移**：映射函数有测试；集成测试跑在已应用 1034 的库上（全绿间接验证 DDL），无 down 迁移测试。
- **前端**：WS 三层（client/bridge/hooks）+ workspace-switch 有测试且质量好；**notification-bell / notifications 页面 / settings / api/notification.ts 零组件测试**（web/test 全目录核对）。

## 七、缺陷清单

### P1（5 项，按修复收益排序）

| # | 缺陷 | 根因（file:line） | 修复建议 |
|---|---|---|---|
| 1 | **评论创建必 500，但评论/订阅/通知已全部落库**——用户见错重试 → 重复评论+重复通知，污染通知链事件源 | `internal/work/app/service_comment.go:219-221`：triggerEngine.OnComment 错误直通 500（触发派发失败不应使已提交的评论报错）；且 http 中间件只记 access 行，错误详情不可观测 | OnComment 失败降级为 admission 结果（如 `dispatch_error`）而非 500；或把触发派发移出请求路径。顺带给 writeErr 补 err 日志字段 |
| 2 | **任务完成通知双发+三发**：complete() 已 emit TaskCompleted 一次（:728 经 outbox），:746 又直接 Emit 一次（新 EventID，dedup 按 `<eventID>:<uid>` 挡不住），:758 起源者再收第三条直写（type `task_completed`，i18n 未映射显示原始串） | `internal/orchestration/app/service.go:728` + `:746` + `:758`；dedup 语义见 `internal/notification/app/service.go:114-118` | 删 :746 的重复 Emit；或投影侧 dedup key 改业务键（如 `task_completed_event:<taskID>:<uid>`）；长期应统一"投影一条直写一条"为单路径 |
| 3 | **通知页"加载更多"恒显示，点击即整页重复**：后端耗尽返回 `next_cursor:""`，前端 `?? undefined` 把空串当有效游标 → hasNextPage 恒真；fetchNextPage 的空 pageParam 被 `if (cursor)` 吞掉 → 重取第一页追加 | `internal/notification/adapters/pgrepo/repo.go:76-78`（返回 ""）+ `web/src/api/notification.ts:46`（`next_cursor ?? null` 不处理 ""）+ `web/src/routes/_app.notifications.tsx:140`（`?? undefined`） | 后端耗尽返回 `null`（或前端三处任一改为 `next_cursor ? … : undefined`）；加一条"len(items)<limit 则无下页"守卫 |
| 4 | **新通知到达零 WS 帧，实时性只剩 60s 轮询兜底**：八种写入路径均不推帧，notification:* 六帧只覆盖已读/归档操作；recent 列表无轮询+全局关闭窗口聚焦刷新，WS 断线时铃铛列表永不更新 | 写入侧：`internal/notification/app/service.go:80-121`（Handle 无 emit）、pgrepo 直写族；消费侧：`web/src/components/notification-bell.tsx:81`（仅 unread-count 有 refetchInterval）、`web/src/lib/query.ts:8` | 投影/直写落库后按 recipient SendToUser 一帧 `notification:created`（契约需新增第七帧并在 main.tsx 加失效规则——同前缀已覆盖）；过渡方案给 recent 列表也加 60s refetchInterval |
| 5 | **自通知**：作者评论自己的工单 → 自己收 "New comment"（实测：我的评论使我未读 +1）；任务起源者同样收到自己的完成/审批通知。Envelope.ActorUserID 字段存在但投影不用 | `cmd/orkome-server/wiring_orchestration.go:196-220`（handler 未透传/排除 `e.ActorUserID`）；作者自动订阅见 `internal/work/app/service_comment.go:161-166` | `NotifyService.Handle` 增加 `ExcludeUserIDs []string`，投影侧传 `e.ActorUserID`（仅 user 作者类型时） |

### P2（10 项）

| # | 缺陷 | 位置 | 建议 |
|---|---|---|---|
| 1 | 通知偏好零 UI：三组静音只能裸调 API；settings 页无任何通知控件。框架 `PUT prefs/{type}` 为死开关（产品写路径不读 per-type 行） | `web/src/routes/_app.settings.tsx`（全页）；`internal/notification/README.md` 已述双轨 | settings 加"通知"节：muted_types 复选（5 种产品类型）+ 工单静音入口；死开关要么接通要么从路由摘除 |
| 2 | 通知类型徽章 8 种落库值仅 5 种有 i18n 映射；标题/正文硬编码英文无参数（中文用户收英文通知，无作者/工单上下文） | `web/src/api/notification.ts:20-26`；写入侧标题见 pgrepo/repo.go:249、service.go:201/207 | 补 `task_completed`/`autopilot_paused`/`wakeup_member` 三键（双语）；标题改带参数模板或改存 i18n key+params |
| 3 | zh 缺 `status.*`×7 + `priority.*`×4（语言切换全局波及：中文下状态/优先级徽章显示键名） | `web/src/i18n/locales/zh.ts`（en.ts:450-463 有） | 补 11 键即闭环（inventory P1 同源） |
| 4 | 非法 UUID 路径参数、非法/未编码游标 → 500 E_INTERNAL 而非 400；游标含 `+08:00` 裸传必炸（URLSearchParams 场景安全，API 直连踩坑） | `internal/notification/adapters/pgrepo/repo.go:57`（timestamptz cast）；uuid 由 pg 驱动 cast；`routes.go:78` writeErr 兜底 500 | 入口处 parse 校验失败返回 400 E_VALIDATION；游标建议改不可读的 opaque 串或 JSON base64 |
| 5 | envelope 卫生：`items:null`（应 []）、`next_cursor:""`（应 null）、prefs `muted_types:null` 与 `muted_work_item_ids:[]` 同响应不一致（splitMutedTypes 空输入返 nil） | `pgrepo/repo.go:44-80`、`:185-194`、`routes.go:104` | Go 侧统一 make 初始化 + 耗尽 next 返回 ""→null；顺手消掉 P1-3 的根 |
| 6 | 未读视图超过一页不可达：unread 视图前端过滤已载页、隐藏加载更多、服务端无 unread-only 参数 | `web/src/routes/_app.notifications.tsx:149`、`:244`；`api/notification.ts:30-47` | 后端加 `?unread=true`；或 unread 视图也允许翻页 |
| 7 | 通知页 Row 点击只标已读不跳转（aria-label 却承诺"打开「…」并标为已读"），与铃铛行为（跳转）不一致 | `web/src/routes/_app.notifications.tsx:56-59` vs `notification-bell.tsx:32-43` | Row 复用铃铛的 open 语义（有 work_item_id/link 即跳转+标读） |
| 8 | 铃铛操作全靠 WS 回声刷新（markAll/PreviewRow 无本地 invalidate）+ 弹层点击项不自动关闭；登出无 try/finally（logout 失败不跳转） | `notification-bell.tsx:38`、`:87`、`:109-138`；`routes/_app.tsx:79-85` | mutation onSuccess 本地 invalidate（乐观更新更佳）；行包 Menu.Item；signOut finally 跳转 |
| 9 | WsClient 未配心跳（heartbeatMs 默认 0）：半开连接时徽标滞留"实时已连"，仅靠服务端 25s ping 对端关闭兜底，网络切换场景徽标可能长时间失真 | `web/src/realtime/client.ts:16`（只传 url） | 传 `heartbeatMs: 30000` 一类；配合 reconnect 已有的全量 invalidate 即闭环 |
| 10 | dead code 与一致性小项：`getNotificationSummary`/`archiveAllNotifications` 无调用方（inventory 已录）；通知创建时间精度并列可跳行（`created_at <` 严格小于）；通知只按 created_at 排序无 id 决胜 | `web/src/api/notification.ts:53-67`；`pgrepo/repo.go:57-62` | dead code 等偏好 UI 落地时一并裁决；游标加 `, id` 决胜列 |

## 八、亮点（保持项）

1. **双面重叠有治理**：supersededNotifyPatterns + filtered router 防 ServeMux panic、README 决策记录、契约 walk 双向对账 19 条路由全在表——三面齐备，值得其他域复制。
2. **幂等与重放安全**：`(user_id, dedup_key)` 部分唯一索引 + ON CONFLICT DO NOTHING，outbox relay 重放同 EventID 不双发；直写与投影双列集（user_id + workspace/recipient/work_item）打通框架/产品两面。
3. **隔离正确**：全部读写 ws+recipient 双过滤，实测 403/400/静默幂等各就各位。
4. **WS 重连即自愈**：重连 open 后全量 invalidate（bridge 有测试），配合 SendToUser 多设备定向，已读状态跨设备/断线恢复体验成立。
5. **主题 FOUC 防护**（index.html 内联脚本）+ 壳层 i18n 命名空间双语齐全。

## 九、修复顺序建议

1. P1-1 评论 500（一处降级，止损通知链上游）
2. P1-3 加载更多空串游标（一处三元表达式，最便宜）
3. P1-2 TaskCompleted 双发（删一行 + 投影 dedup 键改业务键）
4. P1-5 自通知排除（Handle 加 ExcludeUserIDs + 透传 ActorUserID）
5. P1-4 notification:created 帧（契约第七帧 + main.tsx 规则，或过渡双轮询）
6. P2 按 2（i18n 映射）→ 3（zh 11 键）→ 5（envelope 卫生，与 P1-3 同刀）→ 1（偏好 UI）→ 其余顺手

## 十、证据留存

- 测试数据（fp10 前缀）：inventory-ws 工单 #1/#2 各若干评论（创建均 500 但落库）、通知若干（已读/归档状态混合）、偏好行（mute 行已清空、框架 task.completed 行 in_app=false 遗留）。
- 会话 cookie：/tmp/fp10-cookies.txt；后端日志 /tmp/orkome-server.log（ERROR 行只有 access 元数据，无堆栈——本身是 P1-1 的可观测性佐证）。
