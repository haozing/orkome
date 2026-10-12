# W2-1 交接笔记：chat 域收口（creator 私有统一 + chat:done 补全/定向）

> 日期：2026-10-12 · 范围：`internal/conversation/**` + `internal/orchestration/app/service.go`（仅 chat:done 发射点位与 fail/cancel 路径）+ `internal/contract/events/events.go`（1 常量）+ `cmd/orkome-server/main.go`（两行 setter 接线，见 §6 越界说明）
> 依据：audit-fp14-conversation-2026-10-12（P1-4 双标 fence、P2-6 fail/cancel 不发帧、P2-8 workspace 广播漏全文）+ 产品裁决（/chat 独立页不做、conversation 域 dormant 保留；会话口径 **creator 私有**；chat:done 改 **creator 定向 + 补 fail/cancel**；v0 不考虑兼容与旧数据）
> 关联：FixD（docs/fix-wave1-d-wiring-2026-10-12.md §3 待裁决项）——**本笔记 §7 即该项的裁决落地，他侧需同步**

---

## 1. 写面统一 creator-only（audit P1-4，裁决口径 a"会话彻底私有"）

conversation 域此前"读 creator-only、写工作区成员即可"的双标，统一为 **creator 私有**：非 creator 与不存在同响应 **404 E_NOT_FOUND**（不泄露存在性），跨工作区仍被外层 memberMW 403 拦截。实现核心是新助手 `ownedSession`（app/service.go）：

- `ownedSession(ctx, ws, sid, userID)`：`repo.GetSession` + `CreatorID != userID → 404`；**DB 错误独立返回**（不再与未命中合并吞成 404，顺带修掉 FP-14 P2-12 的 GetSession 合并吞错）。
- `mustOwnSession` 改为 `ownedSession` 的薄包装（语义不变，history/thread/draft/prioritize 四个原有读面自动继承新实现）。
- 收口点位（全部 service 层，http 层只负责传 `p.UserID`）：

| 操作 | 改动 |
|---|---|
| GET session（handleGetSession） | service.GetSession 签名 +userID，creator-only |
| 发消息 SendMessage | 内部改走 ownedSession（此前只查工作区成员） |
| 改名 UpdateSession | 签名 +userID，非 nil title 走 mustOwnSession |
| 置顶 SetPinned | 签名 +userID，mustOwnSession |
| 归档/恢复 Archive | 签名 +userID，mustOwnSession；handleArchive 的前置 GetSession 双查删除（fence 在 service 内） |
| 删除 DeleteSession | 签名 +userID，mustOwnSession（routes 的 GetSession 前置保留，为 E_SESSION_IN_USE 的 409/404 次序） |
| 取消 Cancel | mustOwnSession 置于触达 Canceller **之前**（同区他人不得取消他人任务，此前无校验） |
| GET messages（handleListMessages） | 前置 GetSession 改传 userID → 消息流 creator-only |
| GET pending-task（handlePendingTask） | 新增 GetSession 门（此前裸查任务，泄露他人会话任务态） |
| 读态 MarkRead | FP-14 P1-3 已修（repo 级 creator 双条件），本次未动 |

- **`chat_message` 不加 sender 列**（裁决）：creator-only 下无代发者，role='user' 恒等于 creator 本人，v0 无归因需求。
- 未收口的相邻面（维持原状，与本裁决不冲突）：ListSessions/ListPendingTasks/HasPendingTask 本就 creator-scoped；PinService/QuickAction 本就 user-scoped；`ArchiveSessionsByUser`（吊销清理）本就 creator-scoped。

## 2. chat:done 帧形状：+Status 终态语义（最小差异）

`orchapp.ChatDoneFrame` 增一字段，其余键与旧广播帧完全一致：

```go
type ChatDoneFrame struct {
    SessionID string `json:"session_id"`
    TaskID    string `json:"task_id"`
    Role      string `json:"role"`
    Kind      string `json:"kind"`
    Status    string `json:"status"`   // 新增：completed | failed | cancelled
    Content   string `json:"content"`
}
```

- `chatDonePayload` 增 status 参数；kind 规则不变（空内容 → no_response）。
- **fail 帧**：content 恒空、kind=no_response、status=failed——与 fail 落库的空 assistant 行（`AppendAssistantMessage("")` → no_response 行）REST/帧一致；失败详情不进帧（前端经 pending-task/任务面重取）。
- **cancel 帧**：kind=no_response、content 空、status=cancelled；restored/stopped 两种裁决都发（对前端轮次均已终结，裁决差异经 draft-restores/messages 重取可见）。
- 常量：`orchestration/domain.Completed/Failed`、`conversation/domain.TaskTerminalCancelled`（既有常量，无新造）。

## 3. chat:done 补 fail/cancel（audit P2-6）

| 路径 | 发射点 | status |
|---|---|---|
| completed（Settle 直完成） | orchestration/app/service.go `Settle`（原 :753-757，改走 emitChatDone） | completed |
| approve（恢复后完成） | 同文件 `Approve`（原 :816-818） | completed |
| fail（Settle 携带 execErr → `fail()`） | 同文件 `fail`，**新增**：emitCommitted 成功后、promoteAfterTerminal 前 | failed |
| cancel | conversation/app/service.go `Finalize` 尾部 **新增** push（restored/stopped 两个 return 前各一处，经 `pushChatDoneCancelled`） | cancelled |

cancel 选在会话域 Finalize 的原因：queued 直取消（REST → CancelChatTask → CancelRun）**不产生任何 orchestration 事件**，TaskCancelled 漏斗也终结在 `HandleChatTaskTerminal → Finalize`——Finalize 是两条 cancel 子路径的唯一汇合点，一处发射天然免双帧。会话侧对 completed/failed 终态（HandleChatTaskTerminal 非 cancelled 分支）**不发帧**，与结算侧分工互斥不双发。

已知豁口（未修，记录备查）：
- `SweepExpiredAwaiting`（interrupt_timeout 判负）与 liveness sweep 判死路径直调 `FailRun`、不过 settle 事件面，无帧——与"该路径也不写 chat_message 行"的现状一致，属恢复级降级，接入后如需可统一。
- trigger-comment 级联取消（`CancelByTriggerComment`→TaskCancelled）命中的 chat 任务：`HandleChatTaskTerminal` 因无 chat_cancel_pending 行返回 OutcomeNone，投影与帧都不发——**此前投影同样不发**，非本次引入，chat 接入时一并裁决。

## 4. chat:done 改 creator 定向（audit P2-8，隐私矛盾消解）

旧链：`s.emit.Emit(chat.done, WorkspaceID=ws)` → wiring_ws PhaseBroadcast → `workspace:{ws}` + `chat:{sid}` 双 scope 广播——任何同区成员订阅即收到他人会话**完整回复**，与 REST 读 creator-only 直接矛盾。

新链（`emitChatDone`，结算侧唯一出口；cancel 侧 pushChatDoneCancelled 同构）：

1. **bus 事件 WorkspaceID 置空**：wiring_ws PhaseBroadcast 对空 WorkspaceID 短路，全文帧从此进不了 workspace/chat 任何 scope。bus 事件保留的唯一消费方 QuickActionGenerator（wiring_conversation.go）按 payload 取数、不读 WorkspaceID，不受影响；webhook 目录里的 chat.done 只是描述注册表，webhook 投递走 outbox（chat.done 从不入 outbox），均不受影响。ActorUserID 带 OriginatorUserID。
2. **push 端口**（notification×6 帧的现成发送路径同构）：

```go
// orchestration/app/service.go（conversation/app/service.go 的 UserFrames 同构）
type UserFramePusher interface {
    Push(ctx context.Context, userID, frameType string, payload map[string]any)
}
settle.SetChatDonePush(p)      // completed/failed
chat.SetUserFrames(f)          // cancelled
```

cmd 侧用**既有** `notifUserFrames{cast: wsCast}`（wiring_ws.go，notification 帧的同款适配器）接线，帧型 `events.ChatDoneFrameType = "chat:done"`（contract/events 新常量，colon 形态与 notification:×6 一致，且等于 WSName("chat.done")，前端失效规则键不变）。帧内容保留**完整回复**——creator 本人可见与 REST GET messages 口径一致，"广播漏全文"的隐私矛盾就此消解而不是脱敏回避。

- 边界行为：OriginatorUserID 为空（理论外）只发 bus 不 push；端口未接线只发 bus（QA 生成器照常，creator 无 WS 帧，REST 不受影响）。
- `chat:{sessionId}` scope 从此无帧可订；FixD 的 Authorize chat fence（工作区成员口径）失效于"无帧"而非"放行"，仍建议按 §7 收紧（纵深防御 + 防未来 scope 复用）。

## 5. 测试（新增 3 文件，全绿）

| 文件 | 覆盖 |
|---|---|
| internal/orchestration/app/service_chatdone_frame_test.go | 帧矩阵 completed/approve/fail 三格：都发、status 语义、push 只达 OriginatorUserID（approve 场景审批人≠creator 也不外泄全文）、bus 事件 WorkspaceID 空、无会话任务零帧、originator 空不 push |
| internal/conversation/app/service_creator_fence_test.go | 帧矩阵 cancel 格（queued 直取消 + TaskCancelled 漏斗 ×restored/stopped：都发、只达 creator、status/kind 断言）+ 非 verdict（completed/failed）会话侧零帧（防双发）+ nil 端口不 panic；写面 fence 矩阵：非 creator 发消息/改名/置顶/归档/删除/取消/读会话全 404 且零副作用、取消 fence 先于 Canceller 触达，creator 全通 |
| internal/conversation/adapters/http/creator_fence_integration_test.go（PG，TEST_DATABASE_URL） | HTTP 面回归：同区 member 角色的 userB 对他人会话 12 探针（GET/PATCH/pin/archive/messages×2/pending-task GET/cancel×2/read/draft/DELETE）全 404 E_NOT_FOUND 且零留痕，creator 同端点全通 |

既有测试同步：service_session_test.go 的 UpdateSession/SetPinned/DeleteSession 调用补 userID 参数；Settle/Approve 既有单_emit 回归（FP-10）不受影响照常通过；workers b5/b9/invariants 的 ChatDone bus 断言不受影响（事件仍在，仅 WorkspaceID 置空）。

## 6. 越界说明：cmd/orkome-server/main.go 两行接线（FixD 波次文件）

投递端点在装配层，hub 只在 cmd 存在——不动 cmd 则帧无人可收、修复半途。故在 main.go 的 wiringWS 之后加**最小增量**（与 FixD 当初在 batch.go 加三行 payload 同性质的越界，以文件现状为准、保留契约即可）：

```go
if settle := orchHTTPDeps.Settle; settle != nil {
    settle.SetChatDonePush(notifUserFrames{cast: wsCast})
}
convDeps.Chat.SetUserFrames(notifUserFrames{cast: wsCast})
```

未新增任何 cmd 类型/文件；`notifUserFrames`、`wsCast` 全是既有件。FixD 后续整理 wiring 时可挪可并，只需保住两条 setter 的语义。

## 7. ⚠ FixD 同步项：WS chat 订阅 fence 收紧为 creator-only

wave1-D §3 的"待产品裁决"已落：**会话=creator 私有**。请把 `wiring_ws.go` 的 `subScopeFence.chat`（以及 Authorize 对 chat scope 的判定）从「工作区成员 + GetSession 存在」收紧为「`GetSession` 后校验 `CreatorID == p.UserID`」（subScopeFence 注释里预留的方案，一处改动）。当前 chat scope 虽已无帧（§4），但 fence 收紧仍是必修：防未来 scope 复用/帧回归时重新打开"同区成员订阅他人会话"的口子。同理建议顺手移除 `scopedTarget` 的 ChatDone 分支（永久死代码）。

## 8. 门禁（2026-10-12 15:36 最终记录）

- **W2-1 自身范围全绿**：`go build`（conversation 全部 + orchestration/app + contract/events，plain）✓；`go test` conversation/app + orchestration/app ✓；conversation/adapters/http **PG 集成 14 例含 TestCreatorOnly_HTTPFence 全 PASS**（TEST_DATABASE_URL=orkome-test-pg:54329）✓；`golangci-lint run` 全树 **0 issues**（goarch export-data 一行为环境噪音，按波次惯例豁免）✓。
- **全树门禁残留失败均不在 W2-1 文件内**（同 wave1-D §10 先例，同树并行 fix agent WIP）：
  1. `internal/orchestration/adapters/pgrepo/repo_mcp.go`（未跟踪新文件）与 `repo.go` 重复声明 wsMCPCols/scanWSMCP/List/Create/Update/DeleteWorkspaceMCP/ListAgentMCP/agentMCPCols/scanAgentMCP/UpdateAgentMCP 等——并行 agent 的 MCP 凭证封存重构中间态（两版签名不同：新版带 CredentialSealer），**需该 agent 自行收敛**；
  2. `cmd/orkome-server/router_contract_test.go` routeEntry 结构体字面量与 `routes.go` 不匹配——路由契约测试改版中间态。
  -佐证：以上两处以外，`go test ./... -count=1`（把 repo_mcp.go 以空文件 overlay 遮蔽后）**全树全绿**；遮蔽仅为取证，真实文件未动。
- 共享 dev server 未重启；测试用 orkome-test-pg 容器（54329）。

## 9. 未尽事项（不属本波）

- audit-fp14 P2-1/2/3/4/7/10/11/12/13 未在本波清单内（P2-12 的 GetSession 吞错已顺带修掉一半：ownedSession 分离 err）。
- 前端 /chat 页、api/chat.ts、main.tsx `chat:` 失效规则——产品裁决不做独立页；dormant 保留，接入时帧契约以本文 §2/§4 为准（status 必有、只达 creator）。
