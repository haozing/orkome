# FP-20 审计：WS realtime 横切专项（hub / 帧全表 / scope 矩阵 / 断线重连 / 多端一致性）

> 日期：2026-10-12 · 环境：后端 :8080（`relayx: realtime mode=memory`，`ws origins configured n=0`，见 /tmp/orkome-server.log:4-5）+ 前端 :5173（vite 代理）
> 方法：全链代码走读（wiring_ws.go + ploykit v0.4.1 vendor{wsx,wswire,relayx} + @ploykit v0.4.0{client/ws.ts, hooks/ws-query-bridge.ts}）+ **真实 WS 客户端实测**（node `ws`，PAT 鉴权，见 §五）
> 前置：已知发现 batch1（FP-06 workspace scope 死 / FP-05 interim 不截断+失效无 debounce / FP-10 通知创建零帧）**不重复申报**，本文是横切全貌与补充。
> 边界：只读代码；临时测试脚本在 `/tmp/fp20-ws/`（不入仓）；测试数据前缀 `fp20-`。

---

## 一、一句话结论

**实时链只有一条窄路是活的：`user:{uid}` 定向通知帧（6 类 mutation 帧）；workspace scope 死（FP-06）叠加 11 类死帧词汇、4 类 batch 帧被空 WorkspaceID 门禁静默丢弃、task:message/progress scope 错位，导致 35 类帧词汇中实际可达前端的只有 13 类——且 task/chat scope 的订阅授权形同虚设（跨工作区订阅 IDOR，实测 confirmed），Redis relay 接线违反框架契约（≥2 副本即自我转发死循环）。**

### 发现计数（本文新增，不含 batch1 已申报项）

| 级别 | 数量 | 编号 |
|---|---|---|
| P0 | 0（FP-06 workspace 死仍是最高级缺陷，已申报） | — |
| P1 | 3 | F1 订阅越权 IDOR · F2 relay 回环（潜伏）· F3 batch 帧全灭 |
| P2 | 9 | F4 scope 错位 · F5 死帧×11 · F6 握手静默失败 · F7 origin 部署雷 · F8 缺 event_id · F9 帧载荷状态滞后 · F10 chat 失败无终态帧 · F11 帧序/限流 · F12 文档与清单偏差 |
| P3 | 2 | F13 测试缺口清单 · F14 卫生项 |

---

## 二、全链图（写入路径 → 帧 → 前端）

```
[产品域写操作]                    [引擎流]                        [通知 mutation]
work/orchestration/conv/batch    eventpipe(interim 节流500ms)     notification service
   | emit / outbox                    | InterimPush/ProgressPush       | s.emit (service.go:179-215)
   v                                  v                                v
eventbus.Bus.Emit（三阶段：cascade→project→broadcast，同步内联）         notifUserFrames.Push
   |                                                                   | SendToUser
   | PhaseBroadcast 订阅者 = wiring_ws.go:61-79                        v
   |   ├─ e.WorkspaceID=="" → 静默 return（batch 四帧死在这，F3）      hub.Broadcast
   |   ├─ frame = WSName(e.Event)  点→冒号                            （user:{uid} scope）
   |   ├─ Broadcast → workspace:{wsid}（scope 死，FP-06）
   |   └─ scopedTarget：task.*→task:{id}；chat.done→chat:{sid}
   v
hub.Broadcast（wsx/hub.go:109）
   ├─ broadcastLocal：房间内逐客户端 deliver（event_id 去重深度128；send 缓冲16满即踢）
   └─ Relay.PublishOut → relayx Redis pub/sub（无 Redis=memory 模式时无此路）
                                        ▲ wiring_ws.go:54-56 回调用了 Broadcast（应为 DeliverRemote，F2）
前端 main.tsx：bridgeWsToQuery(wsClient, queryClient, 4 条规则) → 按帧类型前缀失效 react-query
前端直听帧的 useWsFrame 消费者：0 个；useWsScope：仅 task-panel.tsx:119（task:{id}）
```

关键不对称：**后端帧的全部意义在前端是"失效缓存"**——没有任何组件直接消费帧 payload（无 `useWsFrame` 调用方）。所以"帧到没到"等价于"页面新不新"，而 payload 里的数据（task result、chat content）纯粹是泄漏面（见 F1）。

---

## 三、帧类型三方矩阵（后端发送点 × scope × 前端规则 × 实测）

图例：发=后端有发送点｜规则=main.tsx 有匹配的失效规则｜达=当前接线实测/推定可达真实客户端。
**"27 类"实为 35 类有明确词汇的帧 + 1 类清单外帧（chat:session_deleted）；其中 11 类是死帧（零发送点或发出即丢）。**

### A. eventbus→workspace 广播族（workspace scope 死 → 本族当前**全灭**）

| # | 帧类型 | 发送点（file:line） | 前端规则 | 达 | 备注 |
|---|---|---|---|---|---|
| 1 | work_item:created | work/app/service_workitem.go:84 | ✓ | ✗ | payload 经闭包回填 wiEvent |
| 2 | work_item:updated | service_workitem.go:271,652,675,759,782 | ✓ | ✗ | |
| 3 | work_item:updated_by_agent | work/app/service_workbench.go:58 | ✓ | ✗ | |
| 4 | work_item:deleted | service_workitem.go:305 | ✓ | ✗ | |
| 5 | comment:created | work/app/service_comment.go:150 | ✓ | ✗ | 实测评论创建（另撞 FP-06 家族 500，落库成功） |
| 6 | comment:updated | service_comment.go:301 | ✓ | ✗ | |
| 7 | comment:deleted | service_comment.go:331 | ✓ | ✗ | |
| 8 | comment:resolved | service_comment.go:355 | ✓ | ✗ | |
| 9 | project:created | work/app/service_catalog.go:155 | **✗** | ✗ | 白听：规则表无 `^project:`，即使修活 workspace，ProjectNav 也不刷新 |
| 10 | label:created | —（常量 events.go:71，零发送点） | ✗ | 死帧 | |
| 11-19 | workspace:created/updated/deleted、member:added/updated/removed、invitation:created/accepted/declined | —（**无桥接**：ploykit wsapp.WorkspaceService 无事件出口，orkome 仅接 OnMemberRemoved/OnTeardown 两个副作用钩子 main.go:222-225；常量仅用于 webhooks 映射 wiring_webhooks.go:99-107） | ✗ | 死帧×9 | inventory §12.3 标注触发源"ploykit workspace 钩子"不成立 |
| 20 | chat:session_deleted | conversation/app/service.go:391 | ✗ | ✗ | **inventory 27 类清单之外的第 28 类**；chat 占位无消费方 |

### B. task 生命周期族（task:{id} scope **实测活**；workspace 副本死）

| # | 帧类型 | 发送点 | 前端规则 | 达 | 备注 |
|---|---|---|---|---|---|
| 21 | task:queued | orch/app/ports.go:777、service.go:509,559,717,947,961,992、service_trigger.go:260,358、workers/sweeper.go:85 | ✓ | ✓ task scope | **实测**（event_id ✓）；注意 sweeper 异步补发导致帧序倒挂（§六-T6） |
| 22 | task:dispatch | workers/dispatcher.go:165 | ✓ | ✓ task scope | **实测**；**无 event_id**（F8） |
| 23 | task:running | dispatcher.go:172、service_external.go:92 | ✓ | ✓ task scope | **实测**；**无 event_id**（F8） |
| 24 | task:awaiting_input | service.go:764（park） | ✓ | ✓ 同链路 | |
| 25 | task:completed | service.go:756,818（**payload 含 Result 全文** service.go:1087） | ✓ | ✓ 同链路 | F1 泄漏面 |
| 26 | task:failed | service.go（fail 路径） | ✓ | ✓ task scope | **实测**；payload.status 滞后为 "running"（F9） |
| 27 | task:cancelled | service.go:891,1053,1065 | ✓ | ✓ 同链路 | |
| 28 | task:resumed | —（常量 events.go:105，零发送点；decodeOutboxPayload main.go:355 仍保留解码分支） | ✓（空听） | 死帧 | |
| 29 | task:message | cmd/orkome-server/main.go:131-135（InterimPush 直拼） | ✓ | ✗ | **scope 错位：只发 workspace，不进 task:{id}**（F4）——与 inventory §12.3 表矛盾；engine 侧有 500ms/32chunk 节流（eventpipe.go:14-16）但 flush 内容不截断（FP-05 已知） |
| 30 | task:progress | main.go:137-141（ProgressPush 直拼） | ✓ | ✗ | 同 F4；**无节流**，每条工具消息 1 帧 → 前端每帧 3 组失效（§七 量化） |

### C. batch 族（**发出即被门禁丢弃**）

| # | 帧类型 | 发送点 | 前端规则 | 达 | 备注 |
|---|---|---|---|---|---|
| 31 | batch:created | orch/app/batch.go:120 | ✗ | 死帧 | batchEmitter（cmd/orkome-server/wiring_batch.go:34-36）构造 Envelope **不带 WorkspaceID** → wiring_ws.go:62-64 空 ID 静默 return（F3） |
| 32 | batch:progress | batch.go:229 | ✗ | 死帧 | 同上 |
| 33 | batch:completed | batch.go:207-211 | ✗ | 死帧 | 同上 |
| 34 | batch:failed | batch.go:207-211 | ✗ | 死帧 | 同上 |

### D. chat 会话族 / E. user 定向族

| # | 帧类型 | 发送点 | 前端规则 | 达 | 备注 |
|---|---|---|---|---|---|
| 35 | chat:done | service.go:752,815（**仅成功路径**） | ✗（chat 占位，inventory 已注明） | ✓ chat:{sid}（订阅可成，未观测到——失败路径不发，F10） | payload 含 Content 全文（F1 泄漏面） |
| 36-41 | notification:read/unread/batch_read/archived/unarchived/batch_archived | notification/app/service.go:179,188,196,205,215 → wiring_ws.go:99-105 SendToUser | ✓ | **✓ 实测活**（唯一族） | 仅 6 个 mutation 端点发帧；创建类 8 条写路径零帧（FP-10 已知，此处确认接线本身是通的） |

**规则表差集（main.tsx 4 条 vs 后端帧）**：漏听 `project:created`、`chat:done`、`chat:session_deleted`、`batch:*4`（后两者兼死帧）；`workspace/member/invitation ×9` 死帧无妨。`^task:` 一条规则同时覆盖 8 类状态帧+message+progress——状态帧风暴即失效风暴（§七）。

---

## 四、WS 入口安全（实测）

| 项 | 实测/代码证据 | 判定 |
|---|---|---|
| 握手鉴权 | `GET /ws` 被 authMw 包裹（wiring_ws.go:93-95）；无凭证请求被放行为匿名连接（webx/auth.go:135-140 无 cookie → next），**升级成功**、无 connected 帧，authGrace 30s 后 read deadline 关闭（wsx/client.go:136-139） | 匿名连接可存活 30s（连接占用面，低危） |
| 伪造 PAT | 首帧 `auth` 假 token → `rejected:auth`（hub.go:370-377，PATPrefix ork_ + ResolvePAT） | ✓ 正确 |
| Origin 校验 | 实测：`Origin: https://evil.example` → **403**；`Origin: http://localhost:5173` 直连 :8080 → **也 403**（allowlist 空 n=0，非 same-origin 一律拒，hub.go:311-312）。**dev 前端能用纯靠 vite 代理 `/ws` 未开 changeOrigin、Host 保持 localhost:5173 才使 sameOrigin 通过**（vite.config.ts:51） | F7：生产反代一旦改写 Host 且未配 WS_ALLOWED_ORIGINS → 全部浏览器 WS 403；启动日志仅 INFO 无告警 |
| `?token=` 查询参数 | **不存在**。Authenticate 只认 `Authorization: Bearer ork_` 与 cookie（webx/auth.go:121-141）；WS 的 PAT 通路是首帧 `auth` 控制帧（hub.go:366-381） | inventory-api §12.1"支持 ?token=PAT"记载错误（F12） |
| 订阅越权 | 见 F1（下） | **P1** |

### F1（P1）：task/chat scope 订阅越权——跨工作区帧泄漏

`wiring_ws.go:35-41`：

```go
hub.Authorize = func(ctx context.Context, p *webx.Principal, connWS, _, _ string) bool {
    if p == nil || connWS == "" { return false }
    ok, err := memberCheck(ctx, connWS, p.UserID)   // 只查握手工作区成员资格
    return err == nil && ok
}
```

框架签名是 `(ctx, principal, connWorkspace, scopePrefix, id)`——orkome 把 **scopePrefix 和 id 两个参数丢弃**，只校验"你是否是握手 URL 里那个工作区的成员"，完全不校验目标 scope/id 归属。实测（§五-T4）：以 inventory-ws 成员身份 `subscribe scope=task id=00000000-0000-7000-8000-ffffffffffff`（**不存在的任务**）与 `scope=chat id=任意会话` → 双双 `confirmed:subscribe`。即：**任何登录用户只要是其任意一个工作区的成员，即可订阅全系统任意 `task:{taskId}` / `chat:{sessionId}`**，持续接收：
- `task:completed` payload 的 `Result`（agent 最终回答全文，service.go:1087）
- `chat:done` payload 的 `Content`（会话回复全文，service.go:1104-1110）
- 任务状态/agent_id/work_item_id 等元数据

UUIDv7 不可枚举是唯一缓解。修复（一行语义 + 两个分支）：

```go
hub.Authorize = func(ctx context.Context, p *webx.Principal, connWS, scope, id string) bool {
    if p == nil { return false }
    switch scope {
    case "task":  // 查 task.WorkspaceID 的成员资格（需注入 orch repo 查询）
    case "chat":  // 查 session.WorkspaceID 的成员资格
    default:      // 现 connWS 成员检查
    }
}
```

---

## 五、实测记录（node ws 客户端，PAT=`ork_4064…`，工作区 inventory-ws）

| # | 场景 | 结果 |
|---|---|---|
| T1 | 无 `?workspace_id` 连接 + auth 帧 → `confirmed:auth`（含 10 项 capabilities）→ 订阅矩阵 | workspace 拒、task/chat **全拒**（connWS 空 → Authorize false）——task/chat 订阅**依赖 URL 带工作区**，不带即全瞎 |
| T2 | Bearer 升级 + `?workspace_id=` | `connected` 立即下发，**无任何 workspace 加入成功/失败反馈**（F6 静默失败） |
| T3 | 恶意 Origin → 403；dev Origin 直连 → 403 | 见 §四 F7 |
| T4 | 带 `?workspace_id=` 订阅矩阵 | `workspace:inventory-ws` → **rejected**（FP-06 复证）；`task:{真实id}` / `task:{不存在id}` / `chat:{任意id}` → **三条全 confirmed（F1 实锤）**；`workspace:{他区}` → rejected（此路正确） |
| T5 | 挂机监听（user+task+chat scope）期间 HTTP 写：评论×2、通知 read/unread、工单 create/update | 收帧：`notification:read`、`notification:unread`（user scope 活）；work_item create/update（201/200）**零帧达**（workspace 死复证）；评论两连 500（FP-06 家族，落库成功） |
| T6 | 建 chat session → 订阅 chat:{sid} → 发消息 → 订阅 task:{tid} | `task:dispatch`(04:18:47) → `task:running`(04:18:47) → **`task:queued`(04:18:48，倒挂)** → 90s 后 `task:failed`(04:20:12，payload.status="running")；`chat:done` 未发（失败路径无终态帧，F10） |
| T7 | 显式订阅 `scope=user` | **静默无响应**（hub.go:407-409 未注册 scope 直接 return，不给 rejected） |

---

## 六、断线重连与多端一致性

**前端客户端（@ploykit v0.4.0 client/src/ws.ts，vite 别名直连模块缓存）**：
- 重连：指数退避 500ms→30s 封顶 + 30% 抖动（ws.ts:219-230）✓；服务端下线→徽标 `reconnecting`（真实状态机，`useSyncExternalStore` 订阅 onStatus，ws-status-badge.tsx 无造假）✓
- 重连恢复：onopen 把 `scopeRefs` 全量重发 subscribe（ws.ts:157-165）✓；workspace ridealong 在 URL 里随重连自动重试 ✓（client.ts:12-14）
- **断线窗口丢帧：无 backfill/序号/replay**。唯一弥补是 ws-query-bridge.ts:28-42 的 `sawOpen` 逻辑——第二次及以后的 `open` 触发**全部规则键** invalidate。即重连补齐仅覆盖 4 条规则的键（work-items/work-item/task/workspace-tasks/notifications）；project/chat/batch 相关状态在断线期间的变化**永不补齐**（后两类本来就是死帧，暂无实际损失——修 F3/F4 时必须同步补规则，否则丢帧面扩大）。
- 生命周期：`_app.tsx:78` 挂载即 connect、登出显式 close ✓；切换工作区 `setCurrent → removeQueries → reconnect()`（workspace-switch.ts）顺序正确，URL 重算 ✓（但切换器 UI 本身 P0-2 崩溃，路径不可达——盘点已报）。
- 心跳：客户端 heartbeatMs 默认 0（关闭）；靠服务端 25s ping/浏览器自动 pong 维持（60s 读超时）✓。半开链路由服务端写失败→drop 兜底，客户端发失败→close→重连 ✓。
- **版本错位隐患**：Go 依赖 ploykit v0.4.1，前端 vite 别名钉死 `ploykit@v0.4.0`（vite.config.ts:16）。v0.4.1 的 F16 修复在 hub 侧兼容两种形状所以未爆——但客户端任何 v0.4.1 协议演进（client.ts:17-18 注释暗示的修复）不会到达前端。建议别名与 go.mod 同步升级。

**多端一致性**：
- 双标签页（同用户）：各自握手、各自 auto-join `user:{uid}` → SendToUser 帧两 tab 都收（通知徽标一致 ✓）；workspace 帧（修活后）按房间广播，天然多端一致 ✓。
- 双端并发写同一工单：HTTP 层有 revision 乐观锁 + 帧只做 invalidate（refetch 幂等），无帧级状态覆盖问题 ✓。
- 慢消费者：send 缓冲 16 满 → 直接踢连接（wsx/client.go:50-58，`ws_evictions` 指标）→ 客户端退避重连 → 重连触发全键 invalidate → **风暴时踢连接反而放大 refetch 压力**（与 §七 量化叠加，FP-05 修复时应一并考虑）。

---

## 七、负载面量化（FP-05 的定量补充）

- **一次评论创建** = 1 帧 `comment:created` → 命中 `^comment:` → invalidate 2 组键（work-item、work-items）。
- **一次工单更新** = 1 帧 → 2 组键。一次任务全程（queued+dispatch+running+completed）≥ 4 帧 × 3 组键 = ≥12 次 invalidate。
- **一个 N 次工具调用的任务**：task:progress 无节流每工具消息 1 帧（eventpipe.go:131-134）≈ 2N 帧（tool_use+tool_result 各一），task:message 节流上限 2 帧/秒。N=100 的任务在数分钟内产生 ≈ 200-500 帧，**每帧 invalidate 3 组键**（`main.tsx:16`），每组键在详情页激活着 1-6 个 query → **千次级 refetch 请求**。当前因 workspace 死 + message/progress 不进 task scope（F4）而无人挨打；**FP-06 修复瞬间该风暴会命中所有 workspace 成员**——修 F4/F6 必须同步给失效桥加 debounce/coalesce（FP-05 已给方向，此处给出量级依据）。
- hub 侧：每帧每房间 1 次 JSON marshal（非每客户端，wsx/hub.go:128-147）✓；deliver 非阻塞、慢者踢 ✓；bus 同步内联广播在写请求路径上（房间大时 O(n) channel 投递，非阻塞）——千级连接下写延迟可控（框架 perf 测试曾量 1000 连接扇出，见 F13-6）。

---

## 八、relayx / 部署形态

- 无 REDIS_URL → memory 模式（实测启动日志 mode=memory），单实例 hub 直播，正确（但意味着**多标签一致性仅限单实例**——单机 dev 语义下无问题）。
- 有 REDIS_URL → 每帧 `PublishOut` 到固定 channel `ploykit:relay:v1`；订阅断线 500ms→30s 退避重订阅（relayx/redis.go:33-49）；发布失败仅 Warn 丢帧（at-most-once）。
- **F2（P1，潜伏）**：`wiring_ws.go:53-57` relay 回调调用 `broadcaster.Broadcast(...)`（= hub.Broadcast = 本地投递 + **再 PublishOut**）。框架契约是回调里只调 `hub.DeliverRemote`（本地投递、不再发布）——hub_relay_equiv_test.go:47 钉死该用法并断言 "no loop"。orkome 的写法在 **≥2 副本 + Redis** 时形成自激：A 发帧 → B 收到（origin≠B）→ B 本地投递**并**以 origin=B 再发布 → A 收到（origin≠A）→ 再以 origin=A 发布 → …… origin 抑制永远无法终止循环；2 副本线性空转烧 Redis/CPU，**3+ 副本每跳 (N-1) 倍增指数爆炸**。客户端 event_id 去重只能压制重复投递，压不住信道流量。修复一行：

```go
reg.AddFunc("ws-relay", func(ctx context.Context) error {
    return rel.Run(ctx, func(env relayx.Envelope) {
        hub.DeliverRemote(env.Scope, env.Frame)   // wiring_ws.go:55
    })
})
```

- F14a：`relayx.NewRelay("", ...)`（wiring_ws.go:51）空 self → 每次启动生成随机实例 ID 并告警，应传实例标识（主机名/POD 名）。

---

## 九、hub 并发安全与连接生命周期（代码核查）

- rooms 读写锁正确（join/leave/drop 全持写锁，broadcast 持读锁拷贝 targets 后再投递，hub.go:128-147）✓
- 每连接去重表独立互斥锁 + 深度 128 环形淘汰 ✓；`dropped` CAS 保证 drop 幂等（双 pump 竞争关闭安全）✓
- 连接计数：upgrade 成功 +1，drop -1（写失败/读失败/慢消费三路都会到 drop）✓；authMw 401 不产生连接 ✓
- 慢消费背压：select default 满即踢（16 帧），不阻塞 broadcast 方 ✓
- 订阅限流：30 次/分/连接（wsx/client.go:82-94）——前端 task-panel 每展开一个任务订阅一次（task-panel.tsx:119），快速展开 >30 个任务 → 静默 `rejected:subscribe`，客户端**不重试不提示**（F11）
- 泄漏面：`c.send` 从不 close，writePump 只能靠写错误/超时退出——drop 后 conn 已 Close，writePump 随即报错退出 ✓ 无泄漏

---

## 十、F4-F12 逐条（P2）

| # | 缺陷 | 证据（file:line） | 修复建议 |
|---|---|---|---|
| F4 | task:message / task:progress 只发 workspace scope，**不进 task:{id}**——task scope 订阅者（任务详情页）收不到流式消息/进度；与 inventory-api §12.3"workspace + task:{id}"记载矛盾 | main.go:131-141（仅 ScopeWorkspace 一次 Broadcast） | 与 scopedTarget 对齐：直拼帧同样补发 `task:{taskID}` scope；或并入 bus 走 scopedTarget 统一路径 |
| F5 | 死帧词汇 11 类：workspace×9（无桥接）、label:created、task:resumed；另 `events.TaskMessage` 常量与 decodeOutboxPayload 分支（main.go:356）也是死路径 | events.go:71,105、main.go:353-368、wiring_webhooks.go:99-107 | 要么在 ploykit workspace 服务挂事件桥补齐 9 帧，要么从 capabilities/文档中移除，防"协议腐烂"误导前端排期 |
| F6 | 握手期 workspace join 失败**零反馈**：`connected` 照发，前端徽标 open、误以为在房间（本轮 workspace 死能存活 3 周未被发现的体验根因） | wsx/hub.go:268-276（joinWorkspace 返回值被忽略） | join 失败时下发 `rejected:subscribe {scope:"workspace"}` 或在 connected payload 中带已加入 scope 列表 |
| F7 | WS_ALLOWED_ORIGINS 未配时：跨源 403 正确，但 **dev 源直连后端也 403**；当前 dev 全靠 vite 代理不改 Host 才成立；生产反代改 Host 即全灭；仅启动 INFO 一条 | 实测 T3/T3b；wiring_ws.go:42-43 | 未配且 IsProd() 时打 WARN/ERROR；README 部署节标注 |
| F8 | task:dispatch / task:running 无 event_id → 服务端/客户端去重全部失效（relay 重放、重连补发场景会重复投递） | dispatcher.go:165,172（Envelope 不带 EventID） | envelope() 工厂统一填 EventID（orchestration/app/service.go 的 envelope() 已有，dispatcher 绕过了它） |
| F9 | task:failed payload.status 滞后为 "running"（帧语义与载荷自相矛盾） | 实测 T6；service.go fail 路径取 task.Status 早于落库 | fail/park 构造 payload 时覆写目标状态 |
| F10 | chat 任务失败无任何 chat 侧终态帧（chat:done 仅 complete 路径 752/815）——未来 chat UI 将永远"输入中" | service.go:735-760, 800-825 | fail/cancel 路径对 chat_session_id 补发 `chat:done {kind:"error"}` 或新增 `chat:failed` |
| F11 | 帧序不可靠（实测 queued 晚于 dispatch/running，sweeper 异步补发；帧无 seq 字段）+ 订阅限流 30/min 前端无处理 | 实测 T6；wsx/client.go:43-44 | 前端不需要排序（invalidate 语义）记录为已知；task-panel 展开超过 30/分钟时应合并订阅或对 rejected 重试一次 |
| F12 | 文档/清单偏差：①inventory-api §12.1 `?token=PAT` 不存在；②§12.3 task:message/progress scope 列错；③"27 类"实为 35 类词汇 + 1 类清单外（chat:session_deleted）；④capabilities 宣称的 10 项中 5 项（workspace/member/invitation/label/batch 相关）无活帧 | 本文 §三 | 按 §三矩阵修订 inventory 分册，死帧从 capabilities 摘除 |

---

## 十一、F13：测试覆盖缺口清单（orkome 装配层；框架层 v0.4.1 已有 hub_test / hub_audit_test / hub_relay_test / hub_relay_equiv_test / gate_test / register_test / relayx{relay,redis}_test）

1. **bus→帧 投递集成测试**：构造 Envelope → 断言 hub 房间收到帧（FP-06 已指出，此处补全）——当前 0 条。
2. **batchEmitter 空 WorkspaceID**（F3）：一条单测即可当场抓住（`batchEmitter.Emit` 后断言 wiring_ws 门禁放行）。
3. **scope 落点断言**：task:message/progress 应达 `task:{id}`（F4）——对 scopedTarget 的表驱动测试。
4. **Authorize 越权拒绝**（F1）：他区 task/chat 订阅必须 rejected——gate 语义测试 0 条（框架 gate_test 只测框架默认实现）。
5. **通知 6 帧 SendToUser**：无回归守卫（本实测证明是唯一活族——最脆也最值钱）。
6. **e2e perf WS 扇出**（e2e/perf_baseline_test.go:373+）：默认 skip（ORKOME_PERF_BASELINE!=1），且按 v0.4.1 语义已失效——测试 hub 未设 WorkspaceMember，workspace subscribe 必 rejected，一旦跑必 Fail。休眠的坏测试，应改用 PAT auth + user scope 或补 WorkspaceMember stub。
7. **前端规则表**：main.tsx 4 条规则无单测/快照（框架有 client/src/ws.test.ts 覆盖客户端本身）；规则表是前后端契约，应有一份"帧类型→规则命中"的表驱动测试（可从本文 §三矩阵生成）。
8. **relay 回环**：orkome 装配层无 hub_relay_equiv 的等价测试（框架测试测的是框架自己，绑不住 orkome 的错误接线）。

F14 卫生项：`NewRelay("")` 随机 origin 告警（wiring_ws.go:51）；显式订阅未注册 scope（如 user）静默无响应（hub.go:407-409 应回 rejected 保持协议可诊断）；前端别名 ploykit@v0.4.0 与 go.mod v0.4.1 版本错位（vite.config.ts:16）。

---

## 十二、修复优先级建议（与 batch1 合并视图）

1. **接线 `hub.WorkspaceMember`**（FP-06 的一行修复）——但注意：**修复会同时激活 F4 的 message/progress 风暴与 sendBuffer 踢连接放大**，建议同一变更集内带：F4 scope 修正 + 失效桥 debounce（FP-05）+ interim 截断（FP-05）。
2. **F2 relay 回环一行修**（DeliverRemote）——在启用 REDIS_URL 的部署之前必须落地。
3. **F1 Authorize 补 scope/id 校验**——需要 orch repo（task→workspace）与 conv repo（session→workspace）注入 wiring_ws；顺带修 F6（join 失败反馈）。
4. F3 batchEmitter 补 WorkspaceID（一行）+ 补 §十一-1/2 两条测试。
5. 其余 P2 按 §十 表逐条顺手修；死帧裁决（F5）建议与 conversation/chat 域接入同期处理。

## 十三、证据留存

- 实测脚本：`/tmp/fp20-ws/test1.js`（鉴权+订阅矩阵+origin）、`test2.js`（ridealong 订阅矩阵/IDOR）、`test3.js`（帧计数监听）、`test4.js`（chat→task 生命周期）；输出摘录见本文 §五。
- PAT：`ork_4064…`（2026-10-13 过期，可 DELETE /api/tokens/34b5f283-4f19-4f11-aa25-5b1dddc907df 回收）；测试数据：工作项 `fp20-wi-probe`（bfbe6a1e）、评论 `fp20-frame-count-probe`（fb997ef1，两条 500 但已落库）、chat session `fp20-frame-probe`（6b8bf9e0，task 8cfd256e failed——openai 网络不可达所致，与帧语义无关）。
