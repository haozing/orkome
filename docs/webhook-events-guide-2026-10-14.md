# Webhook 事件接收集成指南

> 日期：2026-10-14。面向 pack（能力包）作者与厂商接收端运维。
> 范围：orkome 平台把工作区业务事件通过出站 webhook 推送到 pack 声明的接收端 URL；本文覆盖声明方式、投递协议、验签、重试/死信语义、可订阅事件目录与运维接口。
> 实现依据：ploykit v0.6.1 `webhooks/` 域（`webhooks/app/service.go`、`platform/whsign/whsign.go`、`webhooks/adapters/http/routes.go`、`webhooks/adapters/pgrepo/repo.go`）与 orkome 侧 `cmd/pack-provision/main.go`、`cmd/orkome-server/wiring_webhooks.go`、`internal/contract/events/events.go`。

---

## 1. 方向与归属：先分清"谁拥有订阅"

- **方向**：这是**出站**投递——orkome 是发送方，pack 厂商的接收端（你的服务）是 HTTP POST 的被调方。pack 不需要向 orkome 暴露任何入站 API（pack 的 MCP 工具是另一条 pull 通道，与事件无关）。
- **订阅本体是工作区级资产**：订阅存储在 ploykit 框架的 `webhook_subscription` 表（工作区维度，一个 URL 一行，附 sealed 签名密钥），投递日志在 `webhook_delivery` 表。**pack 不拥有订阅**，只是通过 `pack_subscription` 连接表把自己关联到订阅上（`UNIQUE(pack_id, subscription_id)`，`internal/pack/app/service.go:140`）。
- **一份订阅可被多个 pack 共享**：`pack-provision install` 按 URL 去重——目标 URL 已存在订阅时不再创建，只把已有订阅 link 到本 pack（幂等，`cmd/pack-provision/main.go:585-597`）。因此同一接收端 URL 只会收到一份投递，不会因安装了两个 pack 而双发。
- **卸载语义分层**：服务端 `DELETE /api/packs/{packId}` 只删 pack 行，`pack_subscription` 连接表随 FK CASCADE 跟删，**订阅本体不受影响**（"订阅本体的清理是 P2 uninstall 的显式动作，不在本服务内"，`internal/pack/app/service.go:250-252`）。注意：当前 `pack-provision uninstall` CLI 在清理流程里会**显式**调 webhooks API 删掉本 pack 名下的订阅（`cmd/pack-provision/main.go:977-985`，跨系统显式清理）。如果多 pack 共享同一订阅，卸载其中一个 pack 前请先评估——共享场景建议先用 `PATCH` 暂停而非删除。

## 2. 快速上手：pack.yaml 的 events 段

### 2.1 声明

```yaml
apiVersion: pack/v1
name: acme-insights
version: 1.0.0
events:
  - url: https://hooks.acme.example.com/orkome
    types:
      - work_item.created
      - work_item.updated
      - task.completed
```

- `url`：接收端完整 URL（http/https）。provision 时与投递前都会做 SSRF 校验（见 §7）。
- `types`：要订阅的事件类型清单，**必须全部在目录内**（§5）。含未知类型的安装请求会被服务端以 400 拒绝（`unknown event type: <type>`，`webhooks/app/service.go:110-114` + `webx.NewValidation` → HTTP 400）。

### 2.2 安装与签名密钥的一次性交付

`pack-provision install` 逐条处理 events 段：URL 不存在则 `POST /api/workspaces/{ws}/webhooks/subscriptions` 创建订阅，然后 `PUT /api/packs/{packId}/subscriptions` 关联到 pack。

**签名 secret 只在创建响应里回显一次**（`{"subscription":{...},"secret":"..."}`）。服务端落库的是 sealx 封存版，此后任何接口都不再返回它。CLI 安装时会完整打印：

```
- subscription https://hooks.acme.example.com/orkome created (3 event types)
  signing secret (shown once, give it to the receiver operator of ...):

    whk_<64 hex chars>

```

密钥形如 `whk_` + 32 字节随机数的 hex（`webhooks/app/model.go MintSecret`）。**安装完成时必须把这段 secret 转交给接收端运维妥善保管**——它是接收端验签的唯一依据。丢了没有找回通道，只能对该订阅调 rotate-secret 重铸一枚新密钥；重铸后旧密钥还有一个 24 小时的双签名过渡窗（§3.3、§6）。

## 3. 投递协议

### 3.1 请求形状

每次投递是一次 `POST`，`Content-Type: application/json`，HTTP 客户端超时 10 秒（`webhooks/app/service.go:48`）。

**body 是业务事件 payload 的 JSON 对象本身**——不是再包一层的信封。事件上下文（id / type 等）全部在请求头里，不在 body 里：

```json
{
  "id": "wi_01J9...",
  "workspace_id": "ws_01J8...",
  "number": 42,
  "title": "Fix login redirect",
  "status": "in_progress",
  "revision": 7,
  "creator_type": "user",
  "creator_id": "usr_01J7..."
}
```

上例是 `work_item.updated` 的 payload（字段清单见 §5 各 payload 速查）。两类特殊 body：

- **ping**（连通性测试，§6）：body 为 `{"message":"webhooks.ping"}`，`X-Event-Type` 为字面量 `ping`，`X-Event-Id` 以 `ping-` 开头。接收端对 ping 也应验签并返回 2xx。
- **空/非对象 payload**：个别事件 payload 为空时 body 为 JSON `null`。

> 澄清：orkome 内部事件总线上的 `events.Envelope`（`internal/contract/events/events.go`，字段 `EventID/Event/WorkspaceID/ActorUserID/OccurredAt/Payload`）是**内部总线形状，没有 json tag，从不出网**。投递时只有其中的 Payload 部分经 `webhookPayloadMap` 规范化为 JSON 对象后作为 body 发出（`wiring_webhooks.go:57-61`、`webhooks/adapters/pgrepo/repo.go:139-155`、`service.go:297`）。**不要**在接收端按信封结构解析 body。

### 3.2 请求头

| 头 | 含义 |
|---|---|
| `X-Signature` | 当前密钥的签名，**必有**。值 = `"sha256=" + hex(HMAC-SHA256(secret, "<unix秒>.<body>"))` |
| `X-Signature-Old` | rotate-secret 后 24 小时过渡窗内，旧密钥的同公式签名；与 `X-Signature` 并存 |
| `X-Timestamp` | 签名时刻，RFC3339Nano（如 `2026-10-14T09:30:00.123456789Z`）。其 Unix 秒值即签名输入里的 `<unix秒>` |
| `X-Event-Id` | **幂等键**。同一事件的历次投递（含重试）此值不变，**接收端必须按它去重** |
| `X-Event-Type` | 目录事件类型（如 `task.completed`；ping 为 `ping`） |
| `X-Delivery-Id` | 本次投递尝试的记录 id。**重试会产生新的 delivery id**——去重请认 `X-Event-Id`，不要认它 |

签名公式（`platform/whsign/whsign.go:56-62`，即 `whsign.Sign`）：

```
mac    := HMAC_SHA256(key = subscription_secret, msg = unix_seconds + "." + raw_body)
header := "sha256=" + hex_lowercase(mac)
```

注意：签名覆盖的是**原始请求 body 字节**（未经任何解码/re-serialize），key 是字符串 secret 的 UTF-8 字节，msg 是十进制 Unix 秒字符串 + 一个 `.` + body。

### 3.3 验签示例

Go（可直接 import `github.com/haozing/ploykit/platform/whsign`，零依赖叶子包；不引它就照下面的手写版）：

```go
import (
    "crypto/hmac"
    "crypto/sha256"
    "encoding/hex"
    "io"
    "net/http"
    "strconv"
    "time"
)

// replayWindow 建议 5 分钟（ploykit 接收端默认口径 whsign.RecommendedReplayWindow）。
func verifyWebhook(w http.ResponseWriter, r *http.Request, secret string, replayWindow time.Duration) ([]byte, bool) {
    body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
    if err != nil {
        return nil, false
    }
    ts, err := time.Parse(time.RFC3339Nano, r.Header.Get("X-Timestamp"))
    if err != nil || time.Since(ts) > replayWindow || time.Since(ts) < -replayWindow {
        return nil, false // 时间戳缺失/超窗：拒绝，防重放
    }
    mac := hmac.New(sha256.New, []byte(secret))
    mac.Write([]byte(strconv.FormatInt(ts.Unix(), 10)))
    mac.Write([]byte("."))
    mac.Write(body)
    expected := "sha256=" + hex.EncodeToString(mac.Sum(nil))
    // hmac.Equal 常数时间比较；过渡窗内可再对 X-Signature-Old 用旧密钥比一次
    if !hmac.Equal([]byte(expected), []byte(r.Header.Get("X-Signature"))) {
        return nil, false
    }
    return body, true
}
```

Python（伪代码级，任何语言同构）：

```python
import hmac, hashlib, time
from datetime import datetime

REPLAY_WINDOW = 300  # seconds

def verify(secret: str, headers, raw_body: bytes) -> bool:
    ts = datetime.fromisoformat(headers["X-Timestamp"])          # RFC3339Nano
    if abs(time.time() - ts.timestamp()) > REPLAY_WINDOW:
        return False                                              # 1. 防重放
    msg = f"{int(ts.timestamp())}.".encode() + raw_body           # 2. "<unix秒>.<raw body>"
    want = "sha256=" + hmac.new(secret.encode(), msg, hashlib.sha256).hexdigest()
    got  = headers.get("X-Signature", "")
    if not hmac.compare_digest(want, got):                        # 3. 常数时间比较
        old = headers.get("X-Signature-Old", "")                  # 4. 过渡窗兜底（旧密钥签名）
        if not old or not hmac.compare_digest(want, old):
            return False
    return True
```

四个步骤缺一不可：① 校验 `X-Timestamp` 在容忍窗内（防重放）；② 用**原始 body 字节**按公式计算；③ 常数时间比较 `X-Signature`（过渡窗内失败再比 `X-Signature-Old`，两枚签名基于同一 timestamp 与 body，仅 key 不同）；④ 验签通过后按 `X-Event-Id` 去重（同一 event id 重复到达直接返回 2xx、不重复入队业务处理）。

### 3.4 响应语义

| 接收端响应 | orkome 侧行为 |
|---|---|
| **2xx** | 投递成功，记录 `delivered`，不再重试。**响应 body 不被解析** |
| **非 2xx + 响应头 `Webhook-Delivery: abort-message`** | 此事件**立即放弃**，直接进死信（`dead`），不再重试（`service.go:331-334`）。适用于"这份数据我方无法处理、重试也无意义"的场景，如 schema 不认识的旧版 payload |
| **非 2xx（其他）** | 进入重试队列（§4） |
| 连接失败/超时（10s） | 同上，进入重试队列 |

接收端最佳实践：**先验签、去重，然后立即返回 2xx，把业务处理放到异步队列**。处理链路长于 10 秒必然被 orkome 判为超时而重试，造成同一事件反复投递。确实无法处理的事件，返回非 2xx 并带上 `Webhook-Delivery: abort-message` 头，让 orkome 立刻放弃它。

## 4. 重试、死信与订阅自动暂停

- **退避序列**：1m → 5m → 30m → 2h → 6h，各档叠加 **±20% jitter**（实际值 = 档位 × [0.8, 1.2] 均匀随机，`service.go:251-253`）。
- **尝试次数**：共 6 次。第 1~5 次失败分别落 1m/5m/30m/2h/6h 档；**第 6 次失败该投递进死信**（`status='dead'`，`service.go retryPlan`：`attempts > len(RetryBackoff)`）。
- **死信不会自动重放**：需要人工经 §6 的 redeliver 接口重放（产生一条新的投递记录，同一 event id）。
- **订阅自动暂停（熔断）**：一条投递死信时检查订阅的 `first_failure_at`（首次失败时间）。若从首次失败起已持续失败满 **5 天**（`AutoDisableWindow = 5*24h`），订阅被自动置为 `is_active=false` 并停发（`service.go:271-288`）。期间任何一次投递成功都会清零计时（`repo.go MarkDelivered`）。暂停后可在管理端 PATCH 恢复（§6）。
- 对接收端的推论：短时间可自愈的故障（发版、扩容）靠快速返回 2xx 或坦然接受几次重试即可；**持续性故障 5 天不恢复会被平台摘除投递**，恢复接收端后记得检查订阅是否被自动暂停。

## 5. 事件目录

完整目录由 orkome 服务端 `orkomeEventCatalog()` 注册（`cmd/orkome-server/wiring_webhooks.go:95-141`），共 32 个类型；亦可运行时经 `GET /api/workspaces/{ws}/webhooks/events` 拉取（§6）。

### 5.1 workspace & membership

| type | 描述 | payload 要点 |
|---|---|---|
| `workspace.created` | workspace created | `WorkspacePayload`：`id`/`name`/`slug` |
| `workspace.updated` | workspace updated | 同上 |
| `workspace.deleted` | workspace deleted | 同上 |
| `member.added` | workspace member added | `MemberPayload`：`workspace_id`/`user_id`/`email`/`role` |
| `member.updated` | workspace member role updated | 同上 |
| `member.removed` | workspace member removed | 同上 |
| `invitation.created` | workspace invitation created | `InvitationPayload`：`id`/`workspace_id`/`email`/`role` |
| `invitation.accepted` | workspace invitation accepted | 同上 |
| `invitation.declined` | workspace invitation declined | 同上 |

### 5.2 work items, comments, catalog

| type | 描述 | payload 要点 |
|---|---|---|
| `work_item.created` | work item created | `WorkItemPayload`：`id`/`workspace_id`/`number`/`title`/`status`/`revision`/`creator_type`/`creator_id`，可选 `assignee_type`/`assignee_id` |
| `work_item.updated` | work item updated | 同上 |
| `work_item.deleted` | work item deleted | 同上 |
| `comment.created` | comment created | `CommentPayload`：`id`/`workspace_id`/`work_item_id`/`author_type`/`content`，可选 `author_id` |
| `comment.updated` | comment updated | 同上 |
| `comment.deleted` | comment deleted | 同上 |
| `comment.resolved` | comment resolved | 同上 |
| `label.created` | label created | — |
| `project.created` | project created | — |

### 5.3 orchestration: tasks & batches

| type | 描述 | payload 要点 |
|---|---|---|
| `task.queued` | agent task queued | `TaskPayload`：`id`/`agent_id`/`status`，可选 `workspace_id`/`work_item_id`/`result` |
| `task.dispatch` | agent task dispatched | 同上 |
| `task.running` | agent task started running | 同上 |
| `task.completed` | agent task completed | 同上（`result` 为产出摘要） |
| `task.failed` | agent task failed | 同上 |
| `task.cancelled` | agent task cancelled | 同上 |
| `task.awaiting_input` | agent task awaiting input | 同上 |
| `task.resumed` | agent task resumed | 同上 |
| `task.message` | agent task message appended | 同上 |
| `batch.created` | batch created | — |
| `batch.progress` | batch progress updated | — |
| `batch.completed` | batch completed | — |
| `batch.failed` | batch failed | — |

### 5.4 conversation

| type | 描述 | payload 要点 |
|---|---|---|
| `chat.done` | chat turn finished | chat 会话轮次结束（会话 id、任务 id、回复内容、状态） |

### 5.5 不对外投递的内部事件

**冒号命名空间**的事件（`notification:read`/`notification:unread`/`notification:batch_read`/`notification:archived`/`notification:unarchived`/`notification:batch_archived`、`chat:session_deleted`、`work_item:updated_by_agent`）以及 WS 帧 `chat:done` 是内部跨域管线事件，**不在目录内、永不对外投递**——目录闸门在入队前就会拦下它们（`wiring_webhooks.go:53-56`）。pack.yaml 的 types 里写了这些值会在 provision 时被 400 拒绝。

## 6. 运维接口速查

认证与授权：全部要求**工作区成员**身份——产品 API 走 `Authorization: Bearer <PAT>` + `X-Workspace-Id: <workspace id>` 头；读接口（GET）需 `webhooks:read` 权限（member 及以上默认具备），写接口需 `webhooks:manage` 权限（默认仅 owner/admin 具备）。路径中的 `{ws}` 为工作区 id。

| 接口 | 角色 | 说明 |
|---|---|---|
| `GET /api/workspaces/{ws}/webhooks/events` | member | 事件目录，`{"items":[{"type","description"},...]}` |
| `GET /api/workspaces/{ws}/webhooks/subscriptions` | member | 订阅列表，`{"items":[Subscription,...]}` |
| `POST /api/workspaces/{ws}/webhooks/subscriptions` | admin | 创建订阅。body `{"url","events":["type",...],"description"}`；**201** 响应 `{"subscription":{...},"secret":"..."}`——secret 仅此一次回显。未知类型 400 |
| `POST .../subscriptions/{id}/ping` | admin | 连通性测试：入队一条 `ping` 投递到订阅 URL。订阅处于暂停态返回 409。响应 204 |
| `POST .../subscriptions/{id}/rotate-secret` | admin | 重铸签名密钥。响应 `{"secret":"..."}`，新密钥仅此一次。旧密钥保留 24 小时（`OldSecretTTL`），期间投递同时携带 `X-Signature`（新）与 `X-Signature-Old`（旧）；到期后只带新签名——**务必在 24h 内完成接收端换钥** |
| `PATCH .../subscriptions/{id}` | admin | 暂停/恢复。body `{"is_active":bool}`；返回更新后的 Subscription |
| `DELETE .../subscriptions/{id}` | admin | 删除订阅（停止投递并作废密钥；pending 投递标记 orphaned 死信） |
| `GET /api/workspaces/{ws}/webhooks/deliveries?limit=N` | member | 投递日志（最新在前），`{"items":[Delivery,...]}`；limit 缺省 50、上限 200 |
| `POST .../deliveries/{id}/redeliver` | admin | 重放：按原投递复制一条新的 pending 记录（同一 event id），返回新 Delivery。该订阅若已有同 event id 的 pending 记录则不重复入队 |

`Subscription` 字段：`id` / `workspace_id` / `event_types` / `url` / `description` / `is_active` / `created_at`。

`Delivery` 字段：

| 字段 | 说明 |
|---|---|
| `id` | 投递记录 id（= 每次尝试的 `X-Delivery-Id`） |
| `subscription_id` | 所属订阅 |
| `event_id` | 幂等键（= `X-Event-Id`） |
| `event_type` | 事件类型（或 `ping`） |
| `status` | `pending` / `delivered` / `dead` |
| `attempts` | 已尝试次数 |
| `last_status_code` | 最近一次 HTTP 状态码（网络失败为 0） |
| `last_error` | 最近一次失败原因 |
| `delivered_at` | 成功时间（未成功为 null） |
| `created_at` | 入队时间 |

运维节奏建议：安装完成后先 ping 一次确认接收端连通且验签通过；rotate-secret 时先更新接收端为"新旧密钥都能验"（§3.3 第 ④ 步），再触发 rotate，等 24h 窗过后去掉旧密钥。

## 7. 本地联调

- dev 环境下 webhook 投递目标**默认禁止私网地址**（SSRF 防线，`webhooks/app/target.go`：egress guard 拦 127.0.0.1、内网网段等）。本地联调要对接 `http://127.0.0.1:<port>`，需在 orkome-server 侧设置环境变量 **`WEBHOOK_ALLOW_PRIVATE_TARGET=true`**（`1`/`yes`/`on` 等价）。**生产环境务必保持关闭**——它放行的是对所有内网地址的出站请求。
- 联调接收端的最小闭环：起一个本地 HTTP 服务 → 写一份只含 events 段的 pack.yaml → `pack-provision install` → 记下 CLI 打印的 secret → 在管理端对该订阅 ping → 本地端按 §3.3 验签、按 §3.4 返回 2xx → `GET .../deliveries` 确认 `status=delivered`。
- 快速观察重试/死信：让接收端返回 500，可在 deliveries 里看到 attempts 递增与退避节奏；返回 500 + `Webhook-Delivery: abort-message` 头则立即可见 `status=dead`。
