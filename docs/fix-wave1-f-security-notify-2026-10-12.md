# Fix Wave-1 F 组交接笔记：安全 fence + 通知去重 + 错误分类

> 日期：2026-10-12 · 执行：FixF agent · 依据：`feature-audit-batch1`（FP-05/06/08/10）+ `feature-audit-batch2`（FP-14）及四份分册
> 门禁：`go build ./...` 全绿；`go test ./... -count=1` 除 FixE 在途用例外全绿（见 §六）；`golangci-lint run` **0 issues**（goarch export-data 报错为环境噪音，未触碰的包同样出现）；未重启共享 dev server；未 git add/commit

---

## 一、修复清单（7 项全落地）

### 1. 评论写面工作区 fence（FP-06 P1-2，IDOR）

`internal/work/app/service_comment.go` Update / Delete / SetResolve：`GetComment`（只按 id 查）之后补 `c.WorkspaceID != workspaceID → 404`，与 `commentVisible` 同口径——跨区改/删/解决一律 404 不泄露存在性。此前实测"他区改/删/解决全 2xx"。

- 回归：`service_comment_test.go::TestCommentWrite_CrossWorkspaceFence`（跨区三连 404 + 零副作用 + 同区合法路径不回归）；既有 4 测的 fake 评论补了 `WorkspaceID`（fake 数据对齐现实）。

### 2. agent-skill 绑定 fence（FP-08 P1-4，跨区越权写）

五个绑定方法（Set/Add/SetEnabled/Remove/List 同族）此前收 `ws` 不用、repo 写面无 `ag.workspace_id` 限定。修复双管齐下：

- `internal/skills/app/service.go`：五个方法消费 `ws`——先 `AgentInWorkspace`（新 repo 端点）校验 agent ∈ ws，再 `GetSkill(ws, id)` 校验 skill ∈ ws（堵住 `SetAgentSkills` 对外来 skill uuid 仅靠 FK 存在性放行的洞），全部 404 不泄露存在性（对照 squads `GetAgent(ws, …)` 同款链）。
- `internal/skills/adapters/pgrepo/repo.go`：四个写方法签名带 `ws`，SQL 全部 `ag.workspace_id / s.workspace_id` 限定（service 校验 + SQL 收口纵深防御）；`SetAgentSkillEnabled` 未命中绑定 → 404。附带硬化：非法 uuid 输入 22P02 → 400（此前裸 500，审计 S17/S20）。
- 回归：`service_binding_fence_test.go::TestAgentSkillBinding_CrossWorkspaceFence`（跨区 agent 四写 404 + 零 repo 写调用；外来 skill 404；同区 add/enable/set/remove 全通且带 ws 到 repo）。`http/routes_test.go` 的 fake 内嵌接口无需改；`b18_integration_test.go` 走 service 层签名不变。

### 3. 任务完成通知双发/三发治理（FP-10 P1-2 + P2-2 自通知）

`internal/orchestration/app/service.go`：

- **Settle**：删 ：755 的重复 `Emit(TaskCompleted)`（新 EventID 绕过 `uq_notification_dedup` → 订阅者双通知）与 ：757-759 的起源者直写第三条（`WriteTaskCompleted`，type `task_completed` 下划线、前端 i18n 未映射显示原始串）。保留 `complete()` 在事务内经 outbox 落的唯一一条——**事件种类与 payload 形状不变**（`task.completed` + TaskPayload）。
- **Approve**：删 ：823 的重复 Emit（同因）；actor=byUser 保留透传。
- `SettlementService.notification` 字段与构造签名保留（注释标明占位），cmd 装配零改动。

投影侧（`internal/notification/app/service.go`）：`EventIn` 新增 `ActorUserID`，`Handle` 对收件集合（订阅 ∪ 提及 ∪ 直发）`delete(recipients, in.ActorUserID)`——**起源者自通知过滤**。

> ⚠ **cmd 一行激活项（本波禁碰 cmd/**，移交）**：`cmd/orkome-server/wiring_orchestration.go:196` PhaseProject 投影构造 `EventIn{…}` 需补 `ActorUserID: e.ActorUserID` 一行，过滤器才在生产生效。同一行同时治 FP-06 P2-2（评论作者自通知，审计原文即"Handle 增加 ExcludeUserID（wiring 处传 e.ActorID）"）。未接线时零风险（空串不过滤）。

- 行为变化说明（产品可见）：起源者的任务完成通知从"直写保底"改为**订阅口径**——评论触发链的起源者天然是订阅者（SubSourceCommenter）不受影响；纯指派/直聊任务（无 work item 订阅）起源者不再收到完成通知。chat 任务场景本就无 work item，旧行为只是发一条 i18n 显示为原始串 `task_completed` 的坏通知，删除即净改善；若产品要"起源者必达"，属新裁决项（见 §四-3）。
- 回归：`service_settle_notify_test.go::TestSettle_TaskCompletedSingleEmit`（同任务完成只产 **1** 条 TaskCompleted + **0** 次直写）、`TestApprove_TaskCompletedSingleEmit`（Approve 路径同样单条 + actor 透传）；`notification/app/service_test.go::TestNotify_Handle_ActorSelfNotificationFiltered`（actor 经提及/直发/订阅进入收件集一律跳过，他人不受影响，actor 为空不过滤）。

### 4. agent unavailable 错误分类（FP-14 P1-1）

`internal/orchestration/app/service.go` EnqueueChatInTx（:936）与 EnqueueChat（:959 同病同治）：裸 `errors.New("agent unavailable")`（writeErr 兜底 500、无日志、用户输入随事务回滚丢失）改为分类：

- **agent 不存在 → 400 `E_VALIDATION`**（"agent not found in this workspace"）；
- **agent 已归档 → 409 `apierr.Conflict`（`E_REVISION_CONFLICT`，本仓 409 通用码）**，语义选择说明：请求本身合法（会话存在、内容合法），是 agent 生命周期状态与之**冲突**，409 比 400 贴切；writeErr 双域均已映射 409。协调单允许"409 agent_archived 或 400"，二分后各归其位。

`internal/conversation` CreateSession 补 agent 存在性 + 归档前置校验（堵住实测 C"归档 agent 可建会话 → 首条消息必撞 500"的必然踩雷链）：

- `app/service.go`：CreateSession 先查 `repo.AgentAvailability`（新 Repo 端点，pgrepo 实现按 `agent.workspace_id + archived_at` 判定，非法 uuid 归"不存在"）→ 不存在 400 / 已归档 409。
- 回归：`service_enqueue_agent_state_test.go`（orchestration 双路径分类 + 不落任务断言）；`service_agent_fence_test.go::TestCreateSession_AgentStateGate`（400/409/成功三态 + 不落会话断言）。

### 5. chat /read fence（FP-14 P1-3，IDOR）

`MarkRead` 加会话归属条件：`WHERE workspace_id=$1 AND id=$2 AND creator_id=$3`，未命中 → service 层 404（此前任意 UUID 一律 200、他区可标他人会话已读）。链路：`pgrepo/repo.go` → `app/service.go` → `http/routes.go`（补传 ws + userID）。

- **口径说明（creator vs 工作区成员）**：取 **creator 口径**（与该域 `mustOwnSession` 一致的严格者）。理由：① 会话是 creator 单主模型（无参与者表），history/thread/draft 读面全部 creator-only；② `unread_since/last_read_at` 是会话级单列，非 creator 翻转只会污染他人的未读指示。若产品日后裁决"会话成员共享"（FP-14 P1-4b 需补参与者列），此处随参与者语义一并放宽。
- 回归：`service_agent_fence_test.go::TestMarkRead_OwnershipFence`（creator 通过 + 同区非 creator 404 + 随机 UUID 404 + 跨区 404 + repo 调用带 ws+creator 口径）。

### 6. WS interim 帧截断（batch1 FP-05 P1-2）

`internal/orchestration/adapters/engine/eventpipe.go`：截断上移到 `emit()` 入口——interim 路径与 DB 路径消费同一份截到 `summaryMax`（2000 rune）的内容，MB 级结果不再整包广播 WS（truncated 标志语义不变）。

- 同步修正钉死旧行为的断言 `eventpipe_test.go:178`：`sink.frames` 期望值由 `"ok"+"x"×2005` 改 `"ok"+"x"×2000`（注释标明修正依据）。

### 7. work 域 writeErr 补日志

`internal/work/adapters/http/routes.go` writeErr 非 apierr 分支补 `slog.Error("work: unclassified error on write path", "err", err)`——止住"用户见 internal error、服务端无根因痕迹"（batch1 根因 D 的服务端半边；pgrepo 侧 `pgxerr` 映射归 FixE）。

---

## 二、文件清单（本波触碰）

| 文件 | 动作 |
|---|---|
| `internal/work/app/service_comment.go` | 写面 fence ×3 |
| `internal/work/app/service_comment_test.go` | fake 补 ws + 越权回归测试 |
| `internal/work/adapters/http/routes.go` | writeErr 补日志 |
| `internal/skills/app/service.go` | Repo 接口（+AgentInWorkspace、4 写法带 ws）+ 五方法消费 ws |
| `internal/skills/app/service_binding_fence_test.go` | 新增回归 |
| `internal/skills/app/service_test.go` | fake 对齐新签名 + 记录写调用 |
| `internal/skills/adapters/pgrepo/repo.go` | 绑定写 SQL 带工作区限定 + AgentInWorkspace + 22P02→400 |
| `internal/orchestration/app/service.go` | 通知三处收敛一处 + EnqueueChat(InTx) 错误分类 |
| `internal/orchestration/app/service_settle_notify_test.go` | 新增（通知条数断言） |
| `internal/orchestration/app/service_enqueue_agent_state_test.go` | 新增（错误分类回归） |
| `internal/orchestration/adapters/engine/eventpipe.go` | interim 截断 |
| `internal/orchestration/adapters/engine/eventpipe_test.go` | 钉死断言更新 |
| `internal/notification/app/service.go` | EventIn.ActorUserID + Handle 自通知过滤 |
| `internal/notification/app/service_test.go` | 自通知过滤回归 |
| `internal/conversation/app/service.go` | CreateSession agent 门 + MarkRead fence + Repo 接口两方法 |
| `internal/conversation/app/service_agent_fence_test.go` | 新增回归 |
| `internal/conversation/app/service_test.go` | fake 对齐（AgentAvailability/MarkRead） |
| `internal/conversation/adapters/pgrepo/repo.go` | AgentAvailability + MarkRead 归属 SQL |
| `internal/conversation/adapters/http/routes.go` | handleRead 传 ws+user |
| `docs/fix-wave1-f-security-notify-2026-10-12.md` | 本笔记 |

**未触碰**（遵守边界）：`cmd/**`、`web/**`、FixE 三件（`internal/work/adapters/pgrepo/repo.go`/`handlers.go`/`service_catalog.go`）、FixC 两域（`internal/orchestration/adapters/pgrepo|http`、`internal/automation/**`）。`internal/notification/app/service.go` 为通知治理唯一落点（无其他 wave 认领）。

---

## 三、门禁实测输出摘要

```
go build ./...                      → 通过（无输出）
go test ./... -count=1              → 唯一失败：internal/work/adapters/http
                                      TestListPageExhaustedCursorOmitted（FixE 在途，
                                      其测试文件 wave1_fixes_test.go 引用的
                                      fix-wave1-e 交接笔记尚未落盘——其独占文件
                                      service_catalog.go/handlers.go 修复进行中；
                                      与本波零交集，本波文件全部通过）
golangci-lint run（全仓）            → 0 issues（exit 0）
golangci-lint run（本波各包）        → 0 issues（exit 7 系 goarch export-data
                                      typechecking 噪音；对未触碰包同样出现，
                                      属环境噪音，非代码问题）
```

---

## 四、待产品裁决（本波均未实现，仅标注）

1. **chat:done 帧覆盖 fail/cancel 路径**（FP-14 P2-6）：现状仅 completed / approve-后-completed 发 `chat:done`，fail（:861 只发 `task:failed`）与 cancel（TaskCancelled 只进会话投影不发帧）不发——聊天页失败/停止场景永远等不到收尾帧。修复方向二选一：fail 路径补发 `chat:done`（kind 扩 `"failed"`）或前端以 `task:failed` 兜底失效。涉及 `orchestration/app/service.go` fail 路径 + 帧契约 + 前端规则，需先定 kind 词汇。
2. **chat:done 广播口径**（FP-14 P2-8）：`chat:done` 目前全量广播 `workspace:{wsId}` + `chat:{sessionId}` 双 scope 且载荷含**完整聊天回复**，而 REST 读面 creator-only——两侧隐私口径相反；叠加 WS Authorize 忽略 scope/id 参数（FP-20 P1），任何成员可订阅他人会话收全文。需裁决：workspace 广播是否脱敏为 `{session_id, task_id}`，或只发 `chat:{sessionId}`。
3. （附带产生）**起源者完成通知保底口径**：见 §一-3 行为变化说明——如需"起源者必达"，建议以 `DirectUserIDs` 进投影（payload 已含 originator 不涉及 envelope 变更）而非恢复直写。

---

## 五、给下一个 wave 的接线/回归要点

1. **cmd 一行**：`wiring_orchestration.go` PhaseProject 的 `EventIn` 补 `ActorUserID: e.AgentUserID`（激活自通知过滤；同治评论自通知 P2-2）。验证：作者评论/审批自己的动作不再收到通知。
2. **验收 FP-14 回放**：用 archived agent 重放审计 §3.2 A/B/C——建会话 409、发消息 409、不存在 agent 建会话 400；随机 UUID POST /read 404。
3. **验收 FP-08**：A 区成员持 B 区 agent uuid 调五绑定端点 → 404；非法 uuid → 400（不再 500）。
4. **验收 FP-10**：同一任务完成，通知列表仅 1 条 `task.completed`；`task_completed`（下划线）坏类型不再新增。
5. **验收 FP-06**：他区评论 PUT/DELETE/resolve → 404 且服务端无副作用。
6. **eventpipe**：跑 `go test ./internal/orchestration/adapters/engine/ -run TestEventPipe` 已锁定 interim ≤2000 rune；多标签实测可观察大结果不再整包到帧。
