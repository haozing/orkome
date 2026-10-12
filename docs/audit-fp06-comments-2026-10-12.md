# FP-06 审计：工单评论线程（comment-thread / 评论 CRUD / @提及 / WS comment 帧）

> 日期：2026-10-12 · 环境：后端 :8080（dev，二进制 10-11 23:45 构建，与当前源码一致）+ 前端 :5173（未开浏览器，UI 结论来自代码 + 今日 V 盘点互证）
> 账号：`inventory-test@orkome.dev`（id `7c893147-…`）· 工作区 `inventory-ws`（`e83dd858-…`，工单 #1 = `3b8bc342-…`）· 对照区 `fp02-ws-a`（`5ebb1af1-…`，无 agent/小队）
> 方法：七步审计法（全链梳理 → 契约对齐 → curl 实测 → 代码质量 → 后端正确性 → 测试覆盖 → 缺陷清单）
> 测试数据：fp06 前缀（fp06-评论A、fp06 probe 系列评论、fp06-probe-wi 工单），均保留可复查

---

## 0. 结论

**评论的"写—存—读"主干是扎实的（校验/rune 边界/软删/乐观锁/事件 outbox 都是认真做的），但这个功能点当前处在三重断裂中：① 存在无 leader 小队的工作区里评论创建 100% 返回 500（评论实际落库，用户重试必产生重复）；② WS `comment:*` 四帧因 `hub.WorkspaceMember` 从未接线而永远到不了任何客户端，实时失效链是死的；③ 编辑/删除/解决端点没有工作区 fence，实测跨工作区改/删他人工作区评论全部成功（IDOR）。加上 members envelope（P0-1 继承）让 @提及一打就崩、前端根本不做编辑/删除 UI——这个功能点"能发一条评论"之外的部分基本都断着。**

| 级别 | 数量 |
|---|---|
| P0 | 2 |
| P1 | 4 |
| P2 | 8 |

三条最重要发现：P0-1 无 leader 小队 → 评论创建 500（评论已落库）；P1-1 WS workspace 帧投递全断（WorkspaceMember 未接线）；P1-2 跨工作区改/删/解决评论实测通过（无 fence）。

---

## 1. 全链梳理

```
[前端]
routes/_app.work-items.$id.tsx:134  <CommentThread workItemId> （detail tab 底部）
  └ components/work-item/comment-thread.tsx
       ├ useQuery ['work-item', id, 'comments'] → api/work.ts listComments()  GET /api/work-items/{id}/comments → res.items ?? []
       ├ useMutation createComment()            POST 同路径 {content, parent_id:''}（parentId 参数存在但永不传 → 无回复入口）
       ├ useQuery membersQueryOptions(ws)  → api/identity.ts listMembers()  GET /api/workspaces/{ws}/members   ← envelope bug 波及点
       ├ useQuery ['agents'] listAgents()  → api/orchestration.ts           GET /api/agents（{agents:[…]} 形状解析正确）
       ├ RichEditor(lazy) + mention 扩展（tiptap，`[@label](mention://type/id)` 序列化，与后端正则一致）
       ├ previewCommentTrigger() 500ms 防抖 → POST …/comments/trigger-preview（would_trigger/blocked 预检提示）
       └ CommentRow：author_type→"用户"/"Agent" + author_id 前 6 位 + Markdown 渲染；无编辑/删除/回复/解决/回应 UI
[WS 失效] main.tsx:17  { match: /^comment:/, invalidate: [['work-item'],['work-items']] }（规则正确，但见 P1-1：帧到不了）

[后端 HTTP] internal/work/adapters/http/routes.go（全部经 wsGuard：校验 X-Workspace-Id 成员资格并设 Principal.WorkspaceID）
  118 GET  /api/work-items/{workItemId}/comments            → handleListComments（handlers.go:365，?limit/before 分页可选）
  119 POST /api/work-items/{workItemId}/comments            → handleCreateComment（handlers.go:345 → {comment, admission} 201）
  120 POST /api/work-items/{workItemId}/comments/trigger-preview → handleTriggerPreview（handlers.go:765）
  175 PUT    /api/comments/{commentId}                      → handleUpdateComment（content + expected_revision）
  176 DELETE /api/comments/{commentId}                      → handleDeleteComment（软删）
  178 DELETE /api/comments/{commentId}/keep-replies         → 同一 handler（纯别名）
  179/180 POST|DELETE /api/comments/{commentId}/resolve     → SetResolve(true/false)
  182/183 POST|DELETE /api/comments/{commentId}/reactions   → 表情回应（+count 返回）
  185/186 GET|POST /api/comments/{commentId}/sub-work-item-preview|sub-work-items（RequireHuman，B 线评论转子工单）
[service] internal/work/app/service_comment.go
  Create(:123)：trimSpace → redactx.Sanitize → 空或 >50000 rune 拒 → GetWorkItem(ws,wi) 工作区 fence ✓ → parent 存在/未删/同工单 →
    emitCommitted（outbox 同事务：CreateComment + TouchWorkItem + 作者/被提及者 UpsertSubscriber + CommentPayload 填充）→
    提交后 triggerEngine.OnComment（triage 门控 → 触发/抑制/coalesce/排队唤醒 agent）→ admission
  Update(:286)：GetComment（无 ws 校验!）→ 空 content 拒 → user 评论仅作者可改（agent 评论无门槛）→ revision CAS → CommentUpdated → cancelTasksOfComment
  Delete(:323)：GetComment（无 ws 校验!）→ user 评论仅作者（agent 评论无门槛）→ TombstoneComment 软删 → CommentDeleted → cancelTasks
  SetResolve(:346)：仅 root 可 resolve；resolve→CommentResolved 帧，unresolve→CommentUpdated 帧（无 ws 校验!）
  回应(:389/:406)：走 commentVisible（有 ws fence + 未删校验，是全文件唯一有 fence 的写路径）
  ListThread(:231)：ListComments(work_item_id) → domain.FoldThreads 折叠（删根连子一起隐藏）
[repo] internal/work/adapters/pgrepo/repo.go:676-795  cCols 含 `deleted_at IS NOT NULL AS deleted`；UpdateComment 带乐观锁（`revision=$3` 不符→Conflict）；ListCommentPage 递归 CTE 按 root 分页
[event] comment.created/updated/deleted/resolved（contract/events/events.go:66-69，CommentPayload{id,workspace_id,work_item_id,author_type,author_id,content}）
  → work/app/outbox.go emitCommittedEnvs（同事务落 outbox，提交后 emit + MarkPublished）
[WS] cmd/orkome-server/wiring_ws.go:61 PhaseBroadcast → Frame{Type: WSName(event)=comment:created…, Payload} → Broadcast("workspace:<wsId>")   ← 投递断点见 P1-1
[通知] cmd/orkome-server/wiring_orchestration.go:200-218 PhaseProject：CommentCreated → NotifyService.Handle（订阅者 + ParseMentionedUsers 直发；mute 偏好生效）
[触发] workapp.SetTriggerEngine(triggerPort) → orchapp.TriggerService.Preview/OnComment（service_trigger.go）→ TrySteer/MergeIntoPending/InsertTask/InsertDeferredTask
[agent 侧] post_comment 内置工具（orchestration/adapters/engine/tools.go:70-91）→ workBenchAdapter.PostAgentComment（wiring_orchestration.go:401）→ **直调 pgrepo.CreateComment，绕过 CommentService 全部逻辑**（见 P1-3）
```

## 2. 契约对齐

| 项 | 后端 | 前端 | 判定 |
|---|---|---|---|
| 评论列表 | `{items:[Comment]}`（分页时 +`next_before`） | `res.items ?? []` | ✓ 一致 |
| 评论对象 | `{id, workspace_id, work_item_id, author_type, author_id?, content, type, parent_id?, resolved, deleted, revision, created_at, updated_at}` | `Comment` 接口（work.ts:122-136）逐字段一致 | ✓ |
| 创建响应 | 201 `{comment, admission:{result, reason_code?}}` | `{comment, admission}` | ✓ |
| 预检响应 | `{result: would_trigger\|blocked, triggers?, suppressions?, reason_code?}` | `TriggerPreview` | ✓ |
| WS comment 帧 | `comment:created/updated/deleted/resolved` + CommentPayload（wswire.WSName 点转冒号；payload 与 A 分册 §12.3 一致） | main.tsx `^comment:` → 失效 `['work-item'],['work-items']`，评论查询键 `['work-item', id, 'comments']` 前缀可命中 | 构造 ✓ / **投递 ✗（P1-1）** |
| **members** | GET /api/workspaces/{ws}/members → **`{items:[…], total}`**（curl 实证） | `listMembers` 把 body 整体当 `Member[]` | **✗ P0-2 继承**：comment-thread.tsx:45 `(members.data ?? []).map` 对 envelope 对象调 `.map` → 输入 `@` 弹出提及列表即 TypeError（页面已被详情页其它 members 用点压垮，评论框的 @ 是雪上加霜的第五处） |
| agents | `{agents:[…]}` | `res.agents ?? []` | ✓ |
| 提及语法 | 前端 `[@label](mention://type/uuid36)`（mention.tsx:19/214）| 后端两套正则：work 域 `{0,36}` 允许空 id（status_catalog.go:182）、notification 域同型但不过滤 type（service.go:53）| 前后端语法一致 ✓；通知侧语义缺陷见 P2-1 |

作者信息解析结论：**评论列表渲染完全不用 members**（CommentRow 显示 `用户 7c8931…` 即 author_id 前 6 位，comment-thread.tsx:176-198）——所以"评论列表作者名解析"没有再踩 members envelope，但代价是谁都不显示名字（P2-3）。members 只被 `mentionItems()`（@提及候选）使用，那一处踩了（P0-2）。

## 3. curl 实测记录（全部现场复现）

| # | 用例 | 结果 |
|---|---|---|
| 1 | inventory-ws 工单#1 创建评论（普通文本） | **500 E_INTERNAL**（76ms）；**GET 列表证实评论已落库** → 用户重试即重复评论。今日 11:11 已有他组审计留下同症状记录（fp10 cursor B 两次 500） |
| 2 | fp02-ws-a（无小队）同请求 | **201**，`admission:{result:"blocked", reason_code:"no_trigger_target"}` → 定位到小队数据 |
| 3 | 纯空白 content | 400 `invalid content` ✓ |
| 4 | 50000 个汉字（rune 边界） | **201** ✓（rune 语义正确） |
| 5 | 50001 个汉字 | 400 ✓ 边界精确 |
| 6 | 回复已删除评论（parent_id=已删 id） | 400 `parent comment not found` ✓ |
| 7 | trigger-preview（inventory-ws，@user 提及） | **500**（同 P0-1 根因）；preview-trigger 端点同样 500 |
| 8 | 改自己评论（expected_revision=0） | 200 ✓ |
| 9 | 过期 revision（=1） | 409 `E_REVISION_CONFLICT` ✓ 乐观锁生效 |
| 10 | **跨工作区改**：X-Workspace-Id=inventory-ws 改 fp02-ws-a 的评论 | **200，内容真的被改成 "fp06 CROSS-WS EDIT"**（IDOR 实锤） |
| 11 | **跨工作区 resolve** | **204** |
| 12 | **跨工作区 DELETE** | **204**（软删生效，列表中该评论消失） |
| 13 | 加回应 👍 / 删回应 | 201 `{reactions:[{emoji,count:1}]}` / 200（count 仍 1 → 删除静默 no-op，见 P2-6） |
| 14 | resolve root / unresolve | 204 / 204 ✓（service 测试另证仅 root 可 resolve） |
| 15 | 通知联动 | 创建评论后 GET /api/notifications 出现 `type:"comment.created", title:"New comment"` 给**作者本人**（自通知，P2-2 实锤） |
| 16 | WS 实连（python websockets + cookie，`?workspace_id=` 握手） | `connected` 帧（capabilities 含 comment）✓；随后 HTTP 创建评论 201 → **6s 内 0 帧**；同连接先 PUT 工单（200）→ 亦无 `work_item:*` 帧 |
| 17 | 显式发 `{"type":"subscribe","scope":"workspace","id":<ws>}` | **`rejected:subscribe`**（hub.go:403-406 → joinWorkspace → WorkspaceMember==nil → false）|

## 4. 缺陷清单

### P0

**P0-1 存在"无 leader 小队"的工作区时，评论创建/预检 100% 返回 500，且评论已落库（重复提交陷阱）**
- 根因：`internal/orchestration/adapters/pgrepo/repo.go:1054-1061` `ListSquadFacts` 把可空的 `squad.leader_agent_id::text` 直接 `Scan` 进非空 `string`（`internal/orchestration/domain/trigger.go:37 LeaderID`）；squad 表 `leader_agent_id` nullable（无约束强制）。pgx 扫 NULL 进 string → error → `TriggerService.Preview/OnComment` 报错 → `writeErr`（work routes.go:30-35 对非 apierr 一律 500 且**不打日志**，故服务端日志无根因）。
- 影响：inventory-ws 4 个小队中 3 个 leader 为 NULL → POST 评论 500（评论已持久化，admission/agent 触发/前端 onSuccess 清草稿全部不发生）；trigger-preview、preview-trigger 同 500；该工作区的"评论唤醒 agent"整体不可用。无小队的工作区不受影响。
- 修复：`SELECT s.id::text, coalesce(s.leader_agent_id::text,''), …`（与 `agentCols` 对 `owner_user_id` 的处理同款，repo.go:54）；顺手在 `writeErr` 非 apierr 分支补 `log.Error`。
- 测试缺口：既有测试的 fake repo 不经 pgx 扫描，schema 层面 NULL 不可见 → 建议补一条 pgrepo 集成测试（leaderless squad + OnComment）。

**P0-2（继承总盘点 P0-1）members envelope 波及 @提及：输入 `@` 即 TypeError**
- `web/src/components/work-item/comment-thread.tsx:42-45`：`membersQueryOptions` → `listMembers`（identity.ts:69-72）把 `{items,total}` envelope 当 `Member[]` 返回 → `mentionItems()` 内 `members.data.map` 抛错。触发点是 tiptap mention 的 `items()` 回调（mention.tsx:181-185），即用户在评论框输入 `@` 的瞬间。
- 修复：与总盘点一致，一处适配（`listMembers` 取 `res.items ?? []`）修全部五处，评论 @提及随之恢复。

### P1

**P1-1 WS `comment:*` 四帧永不达客户端——workspace scope 投递链整体死亡**
- 证据链：a) `cmd/orkome-server/wiring_ws.go:23-81` 只设置了 `hub.Authorize`，**从未设置 `hub.WorkspaceMember`**；b) vendor `platform/wsx/hub.go:238-248` `joinWorkspace` 首行 `h.WorkspaceMember == nil → return false`——握手 `?workspace_id=` auto-join（hub.go:290）与显式 `subscribe workspace`（hub.go:403-406）走的都是它 → 必然 rejected；c) 前端只对 `task` scope 用 `useWsScope` 订阅（hooks/ws.ts:27、task-panel.tsx:119），workspace scope 无任何订阅者，`realtime/client.ts:9-14` 的注释（"hub 读 ?workspace_id= auto-join"）在 WorkspaceMember 未接线时是空想；d) 实测：connected 帧正常、创建评论 201 后 0 帧、PUT 工单亦无帧、显式 subscribe 被拒。
- 影响：`main.tsx:15-17` 的 `^comment:/^work_item:/^task:(workspace 部分)` 失效规则全部永不触发 → 多标签/多用户下评论列表、工单详情都是陈旧数据，直到手动刷新。（task scope 订阅走 hub.go default 分支 + Authorize，反而可用——正好解释了为何单看 task-panel 时实时是好的。）
- 修复：wiring_ws.go 给 `hub.WorkspaceMember` 接 `idMemberCheck(db)` 同款校验（p != nil && GetMember(ws, p.UserID)）；补一条"WS 帧投递"集成测试。
- 附带：帧构造与 payload 本身正确（§2），修一处接线即通。

**P1-2 评论 Update/Delete/SetResolve 无工作区 fence——跨工作区越权写实测通过（IDOR）**
- 根因：`internal/work/app/service_comment.go:286-299（Update）/:323-330（Delete）/:346-350（SetResolve）` 的 `GetComment(ctx, id)`（repo.go:684，`WHERE id=$1` 无 ws 条件）后不再比对 `c.WorkspaceID` 与入参 `workspaceID`；handler 传入的 `wsID(r)` 只进了事件 envelope。对照：`commentVisible`(:378-387) 有 `c.WorkspaceID != workspaceID → NotFound`，reactions 是唯一有 fence 的写路径。
- 实测：见 §3 #10-12——用自己另一个工作区的 header 改/解决/删了 fp02-ws-a 的评论，全部成功。
- 前置条件：攻击者需知道目标评论 UUID 且自身是任一工作区成员（wsGuard 只验证"你是 header 工作区的成员"，不验证评论归属）。非可枚举，但完全越权。
- 修复：三条路径统一改用 `commentVisible`（或 GetComment 后比对 WorkspaceID）；`handleListComments` 同样不校验 work item 归属（ListThread 不经 wsID），一并补。补跨区 403 测试。

**P1-3 agent 评论（post_comment 工具）绕过 CommentService——无校验、无事件、无通知、无订阅**
- `cmd/orkome-server/wiring_orchestration.go:401-405` `PostAgentComment` 直调 `wRepo.CreateComment`（pgrepo），绕过了：50000 rune 校验、`redactx.Sanitize`、`UpsertSubscriber`（作者/被提及者）、`TouchWorkItem`、**`comment.created` 事件（outbox）**。
- 后果：agent 经 post_comment 发的评论没有 WS 帧（其它人看不到实时更新）、不触发通知（订阅者收不到"New comment"）、不进触发链；与人类评论在 comment-thread 里仅以 `author_type:"agent"` → 显示 "Agent 0284b4…" 区分（comment-thread.tsx:178-183），无样式/分组差异。
- 修复：PostAgentComment 改走 `CommentService.Create`（AuthorType:"agent"，Create 已兼容 agent 作者并自然跳过 trigger user 门控）或最小化：补校验 + emit CommentCreated。

**P1-4 编辑/删除权限模型：agent 评论任何成员都可改/删，且无管理员豁免、无编辑窗口**
- `service_comment.go:297-299/:328-330`：仅当 `c.AuthorType == "user" && c.AuthorID != actorID` 才 403。agent 评论（author_type="agent"）对任何认证成员的 PUT/DELETE 直接放行；user 评论则连管理员也不能改（无 role 检查）。
- 与 P1-2 是同一函数的两类问题：P1-2 是跨工作区，本条是同工作区内 agent 内容的完整性。处置：至少禁止编辑 agent 评论（或审计日志），并把"管理员可删"显式建模或文档化为"仅作者"。

### P2

**P2-1 提及通知不区分 mention 类型**：`internal/notification/app/service.go:144-149` `ParseMentionedUsers` 对 `mention://agent|squad|work_item|all` 的 ID 一律当用户 ID 直发通知（InsertNotification 不校验用户存在）→ 提及 agent/work_item 的评论产生收件人根本不存在的脏通知行。work 域 `service_comment.go:165-169` 只对 `type=="user"` 建订阅——两域语义不一致。修复：notification 侧复用 work 域 ParseMentions 并过滤 `Type=="user"`。

**P2-2 自通知**：作者即订阅者（SubSourceCommenter，service_comment.go:162-164），`NotifyService.Handle`（notification/app/service.go:80-121）无 actor 排除 → 实测作者收到自己评论的 "New comment"。修复：Handle 增加 `ExcludeUserID`（wiring 处传 `e.ActorID`）。

**P2-3 作者不显示名字**：CommentRow 显示 `用户 7c8931…`/`Agent 0284b4…`（author_id 前 6 位，comment-thread.tsx:187-190）。members/agents 两个查询明明就在组件里，补一个 id→display_name 映射即可（修完 P0-2 数据就是对的）。

**P2-4 编辑/删除/解决/回应/回复零前端 UI**：`PUT/DELETE /api/comments/{id}`、`resolve×2`、`reactions×2`、`keep-replies`（work routes.go:175-183）前端零消费（与 A 分册 §"无前端消费面"一致）；回复链路后端完整（parent_id 校验/嵌套渲染 comment-thread.tsx:94-95/163-169），但 `createComment` 调用永不传 parentId（:82）→ 用户无法回复。建议按批补 UI（编辑窗口/删除确认/回复按钮/resolve 切换）。

**P2-5 删除语义**：`DELETE /comments/{id}` 与 `/keep-replies` 是同一 handler（routes.go:176/178），语义别名；`FoldThreads`（domain/status_catalog.go:209-255）把已删 root 及其整棵子树从 API 输出中剔除（未删的回复依然存在于库中但永远不再返回）→ "删除根评论=静默删除整串回复"，与 keep-replies 命名暗示的语义冲突。另外被删节点的更深层未删子孙会被 walk 输出但前端挂不上树（父已删）→ 彻底不可见。

**P2-6 回应删除静默 no-op**：`DELETE /api/comments/{id}/reactions?emoji=…` emoji 不匹配（编码差异）时 0 行删除仍 200 并原样返回 count（handlers.go:455-472 无 rows affected 检查）。建议 404 或返回实际计数。

**P2-7 调试残留**：`web/src/components/editor/mention.tsx:34-39` `console.log('[SL] render items.length =', …)` 每次提及弹层渲染都打印。删除。

**P2-8 交互卫生**：无乐观更新（发送成功才失效重查，弱网下按钮 pending 期间无即时反馈，可接受但可注明）；draft 存组件 state，切换 detail tab 即丢；trigger-preview 500（P0-1 工作区）在前端被静默吞掉（comment-thread.tsx:73-75 catch→setPreview(null)），用户对"唤醒预览失效"无感知。

## 5. 质量面确认（做对了的）

- **XSS**：渲染走 react-markdown（默认不执行原始 HTML）+ 链接强制 `target=_blank rel=noreferrer` + 图片替换占位（markdown.tsx:9-14）；入库前 `redactx.Sanitize`（service_comment.go:125/292）。双层防护 ✓。
- **长度/rune**：50000 rune 上限 rune 语义精确（§3 #4/5）；请求体 1MB MaxBytesReader（routes.go:58）。
- **乐观锁**：`expected_revision` CAS → 409 实测 ✓（repo.go:693-700）。
- **软删**：TombstoneComment（deleted_at）✓，删除后取消该评论触发的未启动任务（cancelTasksOfComment，service 测试覆盖 ✓）。
- **事务性**：评论写入 + TouchWorkItem + 订阅 + outbox 同一事务（emitCommittedEnvs），提交后才发事件 ✓（service_comment_test.go 断言 touched 顺序）。
- **WS 帧构造**：点转冒号命名 + CommentPayload 字段与契约一致；unresolve 复用 comment.updated（帧类型恰为契约四帧之一）✓。
- **i18n**：`comment.*` 13 键 zh/en 双语齐全（zh.ts:641-655 / en.ts:430-443 同段），`work.editorLoading` 两语言都有 ✓。
- **预检体验**：500ms 防抖 + 序号防竞态（previewSeq）+ would_trigger/blocked 徽标 ✓（受 P0-1 影响的区不可用）。
- **提及输入**：tiptap 原生扩展、键盘导航、chips 样式区分 user/agent、markdown 往返与后端正则同构 ✓。

## 6. 测试覆盖

| 层 | 既有 | 缺口 |
|---|---|---|
| service（work/app/service_comment_test.go，4 测） | 作者门+任务取消、仅 root 可 resolve、创建订阅+admission、revision CAS | 跨工作区 fence（正好漏掉 P1-2）；agent 评论编辑权限（P1-4） |
| HTTP（work/adapters/http/integration_test.go:295-330） | 创建/resolve/线程列表/timeline 含评论 | 预检端点；keep-replies；reactions；跨区 403 |
| pgrepo | ——（评论 repo 无专属集成测试） | **leaderless squad + OnComment（正好漏掉 P0-1）**；ListCommentPage 递归 CTE |
| 触发链（orchestration/app/service_trigger*_test.go） | 决策树/门控/reconcile 用 fake repo | 同上，fake repo 掩盖 pgx 扫描问题 |
| 前端 | work-detail.test.ts（listComments/createComment 归一化）、mention.test.tsx（扩展行为） | comment-thread 组件 0 测试；members envelope 形状回归测试 |
| WS | —— | 帧投递端到端测试 0（正好漏掉 P1-1） |

## 7. 修复建议顺序

1. **P0-1** 一行 `coalesce` 修 500（收益最大：恢复评论创建+触发+预检三端点）+ writeErr 补日志
2. **P0-2** members envelope 一处适配（总盘点已排第一，评论 @提及随之恢复）
3. **P1-1** hub.WorkspaceMember 接线（评论/工单/标签全部实时帧复活）
4. **P1-2** Update/Delete/SetResolve/ListThread 补 workspace fence + 403 测试
5. **P1-3** PostAgentComment 走 CommentService
6. **P1-4 / P2 逐条**（P2-4 前端 UI 建议与 conversation 域接入同一批做）

## 8. 测试数据留存

- inventory-ws 工单#1（`3b8bc342-…`）：评论 `3364f145…`（fp06-评论A 正文，P0-1 500 下落库的那条）
- fp02-ws-a 工单 `24ce56ca…`（fp06-probe-wi）：probe 2-7 评论（其中 `7adbc846…` 已被跨区删除实验软删；50000 汉字边界评论一条）
- 未动工单 #1 已有数据；未修改任何代码；cookie jar `/tmp/fp06-cookies.txt`，WS 测试脚本 `/tmp/fp06_ws_listen*.py`、`/tmp/fp06-ws-*.mjs`
