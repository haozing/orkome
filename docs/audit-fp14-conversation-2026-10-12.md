# FP-14 审计：conversation/chat 域（30 条端点"后端全就绪、前端零接入"就绪度评估）

> 日期：2026-10-12 · 审计人：FP-14 agent · 方法：七步审计法（全链梳理→契约对齐→curl 实测→代码质量→后端正确性→测试覆盖→缺陷清单+前端接入成本）
> 环境：后端 :8080（binary 2026-10-11 23:45，源码 9f574ed）+ PG 容器 orkome-pg(:55432) 直查取证；测试账号 inventory-test@orkome.dev / 工作区 inventory-ws
> 结论速览：**P0×0 · P1×4 · P2×13**。主链（建会话→发消息→任务→取消→恢复→删除）活体全通且事务语义正确；但"agent 已归档"数据态下发消息 500 且消息丢失、`/read` 零 fence、会话写面无双标 fence、`chat:done` 帧只覆盖 completed 一条路径——前端接入前需先修 4 个 P1。
> 前置引用：`docs/inventory-api-2026-10-12.md` §5（30 条端点表）、`docs/inventory-frontend-static-2026-10-12.md` §2.18/§5-7（/chat 占位页、chat:* 帧无失效规则——已盘点项本文不重复计缺陷）

---

## 一、全链梳理

### 1.1 代码拓扑（internal/conversation 三层 + wiring）

| 层 | 文件 | 内容 |
|---|---|---|
| http | `internal/conversation/adapters/http/routes.go`（547 行） | 32 条路由注册（26 chat + 6 quick-actions）；memberMW（X-Workspace-Id + 成员门）；writeErr（apierr/coder 双映射，:29-65） |
| app | `app/service.go`（771 行）、`app/service_pin.go`、`app/service_quickaction.go` | ChatService（会话/消息/取消三路径/draft/read）、PinService、QuickActionService + QuickActionGenerator（挂在 chat.done 上的建议生成器）、PointersService（orchestration 回写端口） |
| domain | `domain/rules.go`（39 行） | kind/outcome 常量、ShouldVerdictTerminal（仅 cancelled）、FinalizeOutcome |
| pgrepo | `adapters/pgrepo/repo.go`（431 行） | chat_session/chat_message/chat_draft_restore/chat_cancel_pending/chat_pinned_agent/quick_action 六表 SQL |
| wiring | `cmd/orkome-server/wiring_conversation.go`（250 行） | enqTx/ChatTaskOps/PendingChatReader/Prioritizer/Canceller/AgentStarters 全部接线；TaskCancelled→HandleChatTaskTerminal 投影；chat.done→QuickActionGenerator 挂钩 |
| 迁移 | `migrations/1004_conversation`、`1007/1011/1018/1025/1028/1030` | 六表 + msg_seq 计数器 + parts jsonb + kind 扩 agent_intro + cancel_pending 表 + 跨域删除级联 |

### 1.2 端点挂载实况（实测清点 = 32 条注册；盘点表写 30 条——QA run/render 两行挂在 /api/work-items 前缀下，计数口径差 ±2，属盘点漂移，非缺陷）

- 有条件挂载：`Pins`（3 条 pinned-agents）与 `QuickActions`（4 条 CRUD + run/render）为 nil 时不注册——生产均非 nil，全部挂载。
- 条件端口 `TaskStatus`/`Canceller` 生产已接线（settle.ChatTaskStatus / chatCanceller），GET/DELETE pending-task、DELETE queued-tasks 均活。
- `ChannelHistoryReader`（`app/service.go:142-145`）**生产未接线**（全仓无 `SetChannelHistoryReader` 调用）→ `/api/chat/history`、`/api/chat/thread` 永远走 transcript 兜底实现（`service.go:281-289, 305-313`）。

### 1.3 与前端占位页的差距清单（接入要补的件）

| 件 | 现状 | 缺口 |
|---|---|---|
| 路由页 | `web/src/routes/_app.chat.tsx` 12 行 PagePlaceholder | 整页 |
| API 封装 | `web/src/api/` 无 chat.ts（9 个文件零 chat 函数） | ~15 个函数 |
| WS 失效规则 | `web/src/main.tsx:15-21` 规则表只有 work_item/task/comment/notification | 缺 `chat:` 条目 |
| WS 订阅 | 实时桥无打开会话时 subscribe `chat:{sessionId}` 的调用方 | 需会话页挂载时订阅 |
| 侧栏入口 | 无（盘点已记录，不重复计数） | nav 项 |
| i18n | zh/en 仅 `chat.title/desc` 两键（zh.ts:712-715）；desc 提到的 `chat:message` 帧**后端不存在**（见 P2-9） | 列表/输入/状态/错误约 20+ 键 |

---

## 二、契约对齐（CRUD/消息/历史逐字段核对）

| 端点 | 请求→响应（实测） | 判定 |
|---|---|---|
| POST /api/chat/sessions | `{agent_id,title}` → 201 Session 全量 JSON；title 空缺省 "new session"；无 agent_id → 400 E_VALIDATION | ✓（但 agent 存在性/归档不校验 → P1-2） |
| GET /api/chat/sessions | → `{sessions:[...]}`，creator-only + status='active'，按 pinned_at/updated_at 排序，硬 LIMIT 100 无分页 | ✓ |
| GET/PATCH/DELETE session | 200 Session / 200 / 200 `{result:"deleted"}`；PATCH title 空串 → 400，`{}`（null title）→ 原样返回 | ✓ |
| pin/unpin/archive/unarchive | 200 `{result}`；归档后从列表消失，unarchive 恢复 | ✓ |
| POST messages | `{content}` → 201 `{message, task_id}`；TrimSpace 空 → 400；>50000 rune → 400；非 JSON → 400 | ✓（archived agent → 500，P1-1） |
| GET messages | limit（默认 50）/before_seq（默认 2^62）→ `{messages:[...]}` seq 升序返回 | ✓（limit>200 静默回落 50，P2-7） |
| GET/DELETE pending-task | `{task:{id,status}}` / `{outcome}` | ✓（终态也返回，P2-4） |
| DELETE queued-tasks | 与单任务取消同一 handler | ✓ |
| POST queued-tasks/{taskId}/prioritize | → `{priority}`；非队列任务 → 404 | ✓ |
| POST read | → 200 `{result:"read"}` | ⚠ 零 fence（P1-3） |
| GET/DELETE draft-restores | `{draft:null}` / `{result:"discarded"}` | ✓ |
| GET history | `session_id` 必填 400；`before` 游标非法 → 400；limit 1~200 clamp 50/200；`next_cursor`=本页最旧 seq；返回 `channel_type:"transcript"` | ✓ |
| GET thread | thread_id≠session_id → 404 | ✓ |
| GET pending-tasks / has-any | `{tasks:[...]}`（**空时 null**，P2-3）/ `{has_any}` | ⚠ |
| pinned-agents CRUD | POST 201 / GET `{agents:[{agent_id,name,position}]}` / DELETE 支持 query 或 body 传 agent_id | ✓（假 agent_id → 500，P1-2 同族） |
| quick-actions CRUD | 201/`{quick_actions}`/200/200；label≤80 rune、prompt 必填 400；`is_private` 入参被静默强制 true（service_quickaction.go:87） | ✓（死旋钮 P2-10） |
| QA run / render | run → 201 `{task_id}`（work item 有 agent 指派时实测通过）；render → 200 `{prompt}`；不存在 work item → 400 "no agent assignee"（错误归因误导，P2-10） | ✓ |

**与 orchestration 的边界**：chat_message 与 task_message 是两套表两个上下文——task_message(task_id) 面向任务轨迹（FP-05 面），chat_message(session_id, seq) 面向会话流；assistant 最终回复由 `SettlementService.complete`（orchestration/app/service.go:687-701）经 chats 端口 AppendAssistantMessage/Parts 写入 chat_message，中间轨迹不进会话。用户消息双写：chat_message 一行 + `agent_task_queue.context->>'message'`（引擎取输入）。数据模型无冲突，边界干净。**缺 participants**：会话是 creator 单主模型（无成员表），chat_message 无发送者归因列（role='user' 不分人）——多成员共写一个会话时无法区分谁说的（P1-4）。

---

## 三、curl 实测记录（摘要；全部用 fp14 前缀数据，测后已清理）

### 3.1 成功态主干（全通）

| # | 操作 | 结果 |
|---|---|---|
| 1 | POST sessions（正常 agent，带/不带 title） | 201 ×2 |
| 2 | GET/PATCH session、pin→列表置顶→unpin、archive→列表消失→unarchive | 全 200 |
| 3 | POST messages（空会话首条，正常 agent 0284b43f） | 201 `{message(seq=1), task_id}` |
| 4 | 空会话首条 + 会话有历史后再发 | 201（intro 只在首条前注入） |
| 5 | GET messages limit=1/1000、before_seq=2/abc | seq 窗口正确；非法 before_seq 静默按最新 |
| 6 | GET history limit=2 → `next_cursor:"2"` 翻页语义正确；thread 冒充 id → 404 | 200 |
| 7 | DELETE pending-task（queued 任务）→ 再 DELETE session | 409 E_SESSION_IN_USE → 取消后 200 deleted |
| 8 | DELETE pending-task（无 assistant 回复的会话） | `{outcome:"restored"}`（恢复判定活体生效） |
| 9 | DELETE pending-task（running 任务） | `{outcome:"stop_requested"}`（异步停止，符合设计） |
| 10 | POST read / draft-restores GET/DELETE | 200 |
| 11 | pinned-agents add/list/dup-add/delete(query) | 201/200/201/200 |
| 12 | QA create(400 空label)/list/patch/render/run（work item 指派 agent 后） | 全通，run 201 `{task_id}` |
| 13 | 外层 fence：无 X-Workspace-Id → 400；非成员工作区 → 403 | 全 200 语义正确 |

### 3.2 缺陷态实测（关键证据）

| # | 复现 | 结果 | 根因定位 |
|---|---|---|---|
| A | 对已归档 agent（fp07-audit-agent，archived_at 非空）的**空会话**发首条消息 | **500** `{"error":"E_INTERNAL","message":"internal error"}`，**消息不落库**（tx 回滚，事务语义正确但用户输入丢失），服务端**无任何错误日志** | `EnqueueChatInTx` 返回裸 `errors.New("agent unavailable")`（orchestration/app/service.go:936）→ writeErr 兜底 500（conversation/adapters/http/routes.go:64） |
| B | POST sessions 传**不存在的 agent_id** | 500 E_INTERNAL | CreateSession 无 agent 校验，FK 23503 裸抛（pgrepo/repo.go:32-40 + routes.go:64） |
| C | POST sessions 传**已归档 agent** | **201 成功创建** → 用户进入会话后首条消息必撞 A | CreateSession 不查 archived（service.go:230-242） |
| D | POST /sessions/{随机合法UUID}/read | **200 `{"result":"read"}`**（不存在的会话也 200） | MarkRead 只 `WHERE id=$1`（pgrepo/repo.go:253-256），无 workspace/creator/存在性条件；routes.go:325-331 只传 sid |
| E | POST pinned-agents 传不存在的 agent_id | 500 E_INTERNAL | chat_pinned_agent FK 23503 裸抛（pgrepo/repo.go:397-403） |
| F | 对**已 failed** 的任务 DELETE pending-task | 200 `{outcome:"stop_requested"}` + 写入 chat_cancel_pending 行 | service.go:567-584 不筛终态；该行无事件再触发 ClearCancelPending → 永久孤儿行 |
| G | GET pending-tasks（空） | `{"tasks":null}` | routes.go:399-407 不归一 nil（同文件 ：179-181 的 sessions 却归一了）——前端接入即 `.map` TypeError |
| H | GET pending-task（任务已 failed） | `{task:{...,status:"failed"}}` | GetTaskByChatSession 无 status 过滤（orchestration pgrepo/repo.go:814-822）——"pending"名不符实 |
| I | 消息内容 `<script>…</script>`/emoji/CRLF | 201 原样存储、JSON 正确转义 | 存储无注入问题（渲染层 XSS 是前端接入时的责任） |

> 取证过程中的对照实验：同库直插 InsertChatTask 同构 SQL 成功、pgx 直连 event_outbox 结构体 payload 编码成功、隔离测试（预置消息跳过 intro 路径）201——最终把 500 钉死在"空会话 intro 路径 + GetAgent(archived) → agent unavailable"这条链，非环境噪声。

---

## 四、代码质量

- **风格与 work/orchestration 域一致**：三层目录、窄端口（Repo/PinRepo/QuickActionRepo 接口在 app 层定义、pgrepo `var _ app.Repo = (*Repo)(nil)` 断言）、wiring setter 注入，一致性良好；**零 TODO/FIXME 残留**。
- 错误处理沿用全仓"三无"模式中的两无（非 apierr 一律 500、不打日志），但**事务语义是对的**：Send 失败消息不落库（对照 batch1"500+已落库"族，本域无此病）。
- 死代码/死旋钮：`ChannelHistoryReader` 端口无生产实现；`quick_action.is_private` 入参被覆盖；`chat_session.engine_tag/is_agent_intro/project_id` 三列 Go 代码零引用（schema 遗留）；`SetUnread` 的 time 参数被忽略（repo.go:248 硬用 now()）；`domain.ShouldDeferCancel` 无生产调用方。
- `handleArchive` 先 GetSession 再 Archive 双查询（routes.go:194-207）——无害的小冗余。

## 五、后端正确性

- **消息校验**：TrimSpace 空/50000 rune 上限/1MB body 上限（decode MaxBytesReader）三重防线 ✓。
- **会话 fence**：外层工作区成员门硬（实测 400/403）；内层双标见 P1-4。`mustOwnSession` 只罩住 History/Thread/RestoreDraft/DiscardDraft/Prioritize 五个读面。
- **seq 原子性**：NextSeq 用 `UPDATE chat_session SET msg_seq=msg_seq+1 RETURNING` 行锁计数 + `idx_chat_msg_session(session_id, seq)` 索引，并发插入不会重号 ✓。
- **取消三路径**：queued→直取消、running→stop_requested 异步 + TaskCancelled 投影→Finalize（restored：回删用户消息+存 draft / stopped：补 system_stopped 行），活体验证 restored/stop_requested 两态 ✓。
- **chat:done 触发条件**（审计问题正面回答）：仅当 `task.Context.chat_session_id` 非空且任务 **completed**（`Settle`，orchestration/app/service.go:750-753）或 **Approve 恢复后 completed**（:814-816）时发 `chat.done` → WSName 转 `chat:done` → 广播 `workspace:{wsId}` + `chat:{sessionId}` 双 scope（wiring_ws.go:73-78, 107-118）。**fail 与 cancel 路径不发**（:861 只发 task:failed；TaskCancelled 只进会话投影不 发帧）。载荷 `ChatDoneFrame{session_id, task_id, role:"assistant", kind:message|no_response, content=完整输出}`。前端 main.tsx 无 `chat:` 失效规则（盘点已知）——意味着这 27 类帧里唯一带完整聊天内容的帧目前无人消费，且**任务失败/停止时聊天页将永远等不到收尾帧**（P2-8）。
- **与 task_message 表边界**：见 §二，无冲突；assistant 回复同内容落 task_message 轨迹 + chat_message 会话流双份（设计如此）。

## 六、测试覆盖

71 个测试函数（app 53 / http 集成 15 / pgrepo 3）：生命周期、取消两态、draft 归属负例、QA run 成败映射、非成员 403、pin 排序、分页均有。**盲区恰好全部落在本次实测翻车的数据态上**：

1. archived/不存在 agent 的 create-session 与 send-message 路径（P1-1/P1-2 全军覆没式漏测——集成测试全用正常 agent）；
2. `/read` 无 fence 负例（P1-3）；
3. 同工作区非 creator 写他人会话（P1-4）；
4. pending-task 终态语义 / pending-tasks null 归一（P2-3/4）；
5. chat:done 在 fail/cancel 路径不发帧无断言（P2-8）。

---

## 七、缺陷清单

### P1（4 项，接入前必修）

| # | 缺陷 | file:line | 修复建议 |
|---|---|---|---|
| 1 | **已归档 agent 会话发消息 500 且用户输入丢失、无日志**（实测 A；且 C 使其必然被踩到：archived agent 可建会话） | orchestration/app/service.go:936、conversation/adapters/http/routes.go:64、conversation/app/service.go:230-242 | ①EnqueueChatInTx 改返回 `apierr.New(apierr.Conflict, "agent unavailable")`（writeErr 已映射 409）；②CreateSession 校验 agent 存在且未归档（不存在→404、归档→409）；③writeErr 兜底分支补 `log.Warn` |
| 2 | **FK 裸 500 族**：不存在 agent 建会话（实测 B）/不存在 agent 加 pin（实测 E） | conversation/adapters/pgrepo/repo.go:32-40, 397-403 + routes.go:64 | 服务层先 GetAgent 存在性校验→400/404；或 asSessionInUse 同款把 23503 映射为 E_VALIDATION |
| 3 | **POST /read 零 fence 跨工作区写 IDOR**（实测 D：任意 UUID 200；他工作区会话可标记已读） | conversation/adapters/http/routes.go:325-331、app/service.go:396-398、adapters/pgrepo/repo.go:253-256 | MarkRead 加 workspace_id 条件（service 传 ws）+ 行数为 0 时 404。与 batch1 P1-4（评论 IDOR）同根因家族，修复模式一致 |
| 4 | **会话 fence 双标 + 无参与者归因**：读（history/thread/draft/prioritize）creator-only，写（发消息/改名/pin/archive/delete/GET messages/GET session）任何同工作区成员可对他人的会话执行；chat_message 无 sender 列，多人共写无法归因 | conversation/app/service.go:665-674（mustOwnSession 只罩读）、repo.go:151-159（InsertMessage 无 author） | 产品裁决二选一：a) 会话彻底私有——写面也加 mustOwnSession（~6 处一行）；b) 会话成员共享——补 chat_message.sender_user_id 列 + 参与者语义。现状（写比读更开放）几乎不可能是本意 |

### P2（13 项）

| # | 缺陷 | file:line | 建议 |
|---|---|---|---|
| 1 | GET pending-tasks 空返回 `{"tasks":null}` 非数组 | routes.go:399-407 | 仿 ：179-181 归一 `[]`（一行） |
| 2 | CreateSession 错误路径 500 无日志（同 batch1"三无"模式） | routes.go:64 | writeErr 兜底补日志（与 P1-1③ 同一处改） |
| 3 | DELETE pending-task 对终态任务返回 stop_requested 并永久孤儿 chat_cancel_pending 行 | app/service.go:567-584、repo.go:287-296 | Cancel 前查 LatestChatTask 状态，终态直接返回 none/409 |
| 4 | pending-task 返回终态任务，语义名不符实 | orchestration/app/service.go:1035-1038、pgrepo/repo.go:814-822 | 查询加 `status IN ('queued','dispatched','running')` 过滤（或在响应里带 terminal 标记） |
| 5 | DeleteSessionCascade 不清 agent_task_queue 引用（列无 FK 无索引）→ 孤儿任务行；已删会话仍可被 GetTaskByChatSession 命中 | conversation/adapters/pgrepo/repo.go:110-134、migrations/1003_orchestration.up.sql:79 | 迁移补 `ON DELETE SET NULL` + 索引；或 cascade 内一并取消/解引用 |
| 6 | chat:done 仅 completed/approve 发，fail/cancel 不发——聊天页失败/停止场景无收尾帧 | orchestration/app/service.go:750-753, 814-816（对照 ：861） | fail 路径补发 chat:done（kind 可加 "failed"），或前端以 task:failed 兜底失效 |
| 7 | GET messages limit>200 静默回落 50（请求更大给默认值而非 clamp 200）；before_seq 非法静默忽略 | app/service.go:256-259、routes.go:281-290 | clamp 到 200；非法 before_seq → 400 |
| 8 | chat:done 全量广播 workspace scope：任何成员收到他人会话完整回复（REST 读 creator-only，两侧隐私口径相反）；且 WS subscribe chat:{sessionId} 只验工作区成员（Authorize 忽略 scope/id 参数）→ 可订阅他工作区会话收帧 | cmd/orkome-server/wiring_ws.go:35-41, 73-78 | ①workspace 广播是否含聊天内容需产品裁决（至少脱敏为 {session_id,task_id}）；②Authorize 对 chat scope 校验 session 归属工作区 |
| 9 | i18n 描述宣称 `chat:message` 帧，后端不存在该帧类型；任务运行中无任何流式帧，聊天页只能轮询 pending-task | web/src/i18n/locales/zh.ts:714、internal/contract/events/events.go:5,113 | 接入前对齐规格：删帧名或实现 interim 帧（chat 域规格 06 conversation/ 待回读） |
| 10 | quick_action 无数量上限（MaxQuickActions=3 仅约束生成器）、is_private 入参被静默覆盖；QA run 对不存在 work item 报"no agent assignee"误导 | app/service_quickaction.go:83-93, 87、cmd/orkome-server/wiring_conversation.go:98-107（GetWorkItem 的 ok 被忽略） | Create 加 ≤3 上限；wiring 里 !ok → 404 |
| 11 | UpsertDraftRestore 的 ON CONFLICT DO NOTHING 无可冲突唯一约束（无 (session_id,user_id) 唯一键）→ 恢复周期累积行，靠 created_at DESC 兜底 | adapters/pgrepo/repo.go:210-216、migrations/1004_conversation.up.sql:38-46 | 补部分唯一索引 `(session_id,user_id) WHERE finalized='pending'` |
| 12 | GetSession 服务层 `err != nil || !ok` 合并吞错：DB 故障也返回 404（invalid uuid 实测 404 即此因） | app/service.go:248-254 | err 单独分支返回 500（保守：仅对 22P02 类输入错归 404） |
| 13 | ChannelHistoryReader 生产未接线，/history /thread 永远 transcript 兜底；ListSessions 硬 LIMIT 100 无分页 | app/service.go:142-145、cmd/orkome-server/wiring_conversation.go（无 SetChannelHistoryReader）、repo.go:54-58 | 接入 channels 域 reader 或删端口；列表分页待接入时一并做 |

### P3（信息项，不计数）

- 盘点计数漂移：本域实际挂载 32 条路由 vs 盘点表 30/31 条（QA run/render 归属口径）。
- `chat_session.engine_tag/is_agent_intro/project_id`、`domain.ShouldDeferCancel`、`SetUnread` 时间参数为死代码/死旋钮，建议接入时一并裁决（呼应盘点 §六.6"dead code 等 conversation 接入时一并裁决"）。
- archive/unpin 等幂等操作重复调用均 200/201，宽松但无害。

---

## 八、前端接入成本评估（占位页 → 真页面）

**前提**：先修 P1-1/2/3（后端约 6 处小改 + writeErr 日志，半天内），否则接入即踩 500/IDOR。

| 增量 | 内容 | 工作量 |
|---|---|---|
| ① 最小可用（MVP） | api/chat.ts 封装（sessions CRUD + messages + 发送 + pending-task 轮询）；会话列表 + 消息流 + 输入框（参照 task 面板模式）；i18n 键；侧栏入口 | **1 天**（前端） |
| ② 实时与收尾 | main.tsx 补 `chat:` 失效规则；打开会话时 subscribe `chat:{sessionId}`；失败/停止场景以 task:failed 或 P2-6 补帧兜底；读态（POST read）与未读标记（unread_since 字段已在 schema） | **0.5~1 天** |
| ③ 体验件 | draft-restores 恢复提示、pin/archive/改名、conversation starters 空态、chat.quick_actions 建议面板（后端生成器已在跑）、pinned-agents 栏 | **1 天** |
| ④ 后端配套 | P1×4 修复（0.5 天）+ P2-1/3/4/7 小改（0.5 天）+ 多人归因若做（P1-4b，另计 1 天含迁移） | **1~2 天** |

- **MVP 口径：1 个前端日**；**完整聊天页（①+②+③+后端 P1/P2 配套）：3~4 天**，其中纯前端约 2.5~3 天。
- 接入时的三个已知坑（务必写进任务单）：G（`tasks:null` 需 `?? []`）、H（pending-task 需按 status 过滤终态）、limit>200 回落 50（分页组件别传超限值）。
- 双分页协议注意：GET messages 用 `before_seq`（数值），/history 用 `before`（字符串游标）+`next_cursor`——列表页和会话页别混用。

## 九、验证与留存

- 后端日志 `/tmp/orkome-server.log`（500 样例 request_id `01a12e48-f866-77c2-a0cb-f6477b6b9493`，无错误详情——即 P2-2 实证）；PG 直查取证（agent 行 archived_at、event_outbox 零 task.* 行、chat_cancel_pending 生命周期）。
- fp14 测试数据已全部清理：4 个会话已删、quick_action 已删、work_item 指派已还原、outbox 探针行已删、孤儿任务行已清（`SELECT count(*) … LIKE 'fp14%'` = 0）。
- 修复回归脚本要点：用 fp07-audit-agent（archived）重放 §3.2 A/B/C 三例 + 随机 UUID /read 应 404。
