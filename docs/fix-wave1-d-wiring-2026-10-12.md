# FixD 交接笔记：cmd 装配层接线修复（wave1-D）

> 日期：2026-10-12 · 范围：`cmd/orkome-server/**`（独占）+ `internal/orchestration/app/batch.go`（3 处 payload 增行，见 §6 说明）
> 依据：feature-audit-batch1（CSRF 两缝、FP-06 P1-1、根因 C）+ batch2（FP-20 F1/F2/F3、FP-19 A2/A5、FP-16 P1-1）+ audit-fp01/09/13/19/20 分册 + FixF 移交（§8.5）
> 状态：8 项修复 + FixF 移交激活项全部落地；门禁结果见 §10（同树有其他 fix agent 并行 WIP，见 §10 说明）

---

## 1. CSRF 三缝（main.go handler 链 + 新文件 csrf_session.go）

**结论：`webx.CSRFConfig`（ploykit v0.4.1）没有 MaxAge 旋钮**，gorilla/csrf 默认 12h 且在
`csrf.Protect` 构造期固化，无法在链外补救（令牌寿命同时编码进 cookie 与服务端校验窗口，
仅改写响应头无效；两曾 Protect 叠加会双重校验 + 双 Set-Cookie 竞争，不可取）。

- ①**MaxAge 对齐会话 TTL**：新增 `cmd/orkome-server/csrf_session.go`——`webx.CSRFConditional`
  的产品侧镜像 `csrfChain(cfg, secure, ttl)`，唯一增量是 `csrf.MaxAge(ttl)`，ttl 传
  `idDeps.Auth.SessionTTL`（7d）。loopback 归一与 403 响应形状是框架未导出逻辑的逐行镜像
  （文件头注明删除条件：**ploykit 暴露 `CSRFConfig.MaxAge` 后整体删除并接回框架实现**，
  届时只需保留 ExemptPrefixes/PATPrefix 两个配置值）。
- ②**`PATPrefix: "ork_"`**：进 CSRFConfig。此前回落 `tk_` → `isPATRequest` 恒 false →
  所有 PAT 写请求被系统级 403（FP-09 P1-1 实测复现的缺陷）。
- ③**`/webhooks/` 豁免**：`ExemptPrefixes: []string{"/ws", "/webhooks/"}`
  （常量 `wsPathPrefix` / `webhookInboundPrefix`，带尾斜杠防误伤）。automation 公开入站
  `POST /webhooks/autopilots/{token}` 自带 token+HMAC+时间戳+限流四重防护、无 cookie
  语义，CSRF 前置拦截使正确签名的外部触发也 403（FP-13 P1-4）。

## 2. WS workspace scope 复活（wiring_ws.go，FP-06 P1-1）

`hub.WorkspaceMember = fwwsx.MemberGate(memberCheck)`——框架自带的 principal→成员校验
适配器（nil principal fail-closed、平台管理员直通，沿用框架语义）。此前从未设置 →
握手 auto-join 与显式 workspace 订阅全部 rejected，comment:/work_item:/label: 帧无人能收。

## 3. 订阅越权 fence（wiring_ws.go，FP-20 F1）

`hub.Authorize` 从丢弃 scope/id 两参改为消费：

- 先验 connWS 成员资格（沿旧链），再按 scope 分派：
  - `task`：`orchRepo.GetTask(ctx, connWS, taskID)` 存在性 = 任务在授权工作区内；
  - `chat`：`convRepo.GetSession(ctx, connWS, sessionID)` 存在性 = 会话在工作区内；
  - 其他/未授权 scope 一律 fail-closed（能到达 Authorize 的 scope 都已注册——hub 侧对
    未注册 scope 静默丢弃；orkome 注册的只有 task/chat，见 `registerRealtimeVocab`）。
- 注入方式：`wiringWS` 新增 `subScopeFence{task, chat}` 两参（函数注入，测试可 fake）；
  main.go 用 `orchpgrepo.New(db).GetTask` / `convpgrepo.New(db).GetSession` 构造。

**⚠ 待产品裁决（标注项）**：conversation 域读写面现行走 **creator-only**
（`mustOwnSession`：`sess.CreatorID != userID` 即 404），而 chat 帧的现有广播口径是
workspace + chat:{sid} 双投。本修复按指示采用「**工作区成员 + 会话存在**」语义——
同区其他成员可订阅 chat:{sid} 收到会话帧（含 Content）。若产品裁定会话为创建者私有，
把 `fence.chat` 换成 `GetSession + CreatorID==p.UserID` 一处即可（wiring_ws.go 的
subScopeFence 注释已写明）。**该裁决需要产品输入，不是接线缺陷。**

## 4. relay 回环（wiring_ws.go，FP-20 F2）

relay 回调从 `broadcaster.Broadcast(...)`（= 本地投递 + PublishOut 再发布）改为
`hub.DeliverRemote(env.Scope, env.Frame)`，提取为 `relayDeliver(hub)` 便于测试。
启用 REDIS_URL（≥2 副本）前的必修项：原写法 origin 抑制无法终止，2 副本无限空转、
3+ 副本指数倍增。附带：`relayx.NewRelay("")` 随机 origin 告警仍在（FP-14a，未动，
属卫生项非缺陷）。

## 5. batch 帧补 WorkspaceID（wiring_batch.go + orchestration/app/batch.go，FP-20 F3）

- 装配层：`batchEmitter.Emit` 构造 Envelope 时 `WorkspaceID: payloadString(payload, "workspace_id")`。
- **⚠ 越界说明**：payload 本身不含 workspace_id，数据只能来自 batch 实体——故在
  `internal/orchestration/app/batch.go` 三个 `s.bus.Emit` 位点（BatchCreated /
  BatchCompleted|Failed / BatchProgress）的 payload map 各加一行
  `"workspace_id": out/b.WorkspaceID`（从 batch 实体取，正是修复清单的表述）。该文件
  不在 FixD 独占清单内，改动为纯增量三行，若与其他 agent 冲突以他们的为准、只需保留
  payload 带 workspace_id 这一契约。

## 6. admin 审计 + 会话吊销接线（wiring_commercial.go，FP-19 A2/A5）

```go
rec := audit.NewRecorder(pool, log)
adminSvc := adminapp.NewAdminService(adminpg.New(pool), time.Now).
    WithAuditor(rec).         // *audit.Recorder 满足 adminapp.Auditor（Record 签名逐参一致）
    WithSessionRevoker(idRepo) // identitypg.Repo.RevokeAllUserSessions
```

- `wiringCommercial` 签名加 `idRepo *identitypg.Repo`（main.go 传 `idDeps.Repo`）。
- 效果：平台管理员授予/撤销、用户封禁/解封进 audit 表（此前 `admin.user_disable /
  user_enable / user_role_change` 静默跳过）；封禁即吊销全部活跃会话，消除
  「封禁→解封后旧会话原样复活」的残留面。

## 7. AccountService 接线（wiring_identity.go + config.go，FP-16 P1-1）

- `fwapp.NewAccountService(repo, sender, fwapp.AccountConfig{SecretPepper: cfg.JWTSecret,
  LinkBaseURL: cfg.AccountLinkBaseURL}, time.Now)`——mailer 三通道（Resend/SMTP/DevLog，
  均已实现 SendPasswordReset/SendEmailVerification）复用 wiringIdentity 既有 sender。
- `fwIdentity` 加 `Account` 字段；`IdentityMountDeps` 补 `Account: id.Account` →
  forgot-password / reset-password / send-verification / verify-email / DELETE /auth/me
  五端点活化（框架对 Deps.Account==nil 的五端点直接 500）。
- **新配置**：`ORKOME_ACCOUNT_LINK_BASE_URL`（config.go `AccountLinkBaseURL`）——邮件链接
  前端基地址，框架拼 `LinkBaseURL + /reset-password?email=..&token=..` 与
  `/verify-email?...`。**部署前必须配置**，否则链接是相对路径（DevLog 通道可辨识、
  真实邮件通道不可点）。
- **前端欠账（非本 wave）**：`/reset-password`、`/verify-email` 两个落地页不存在会 404
  （audit-fp16 §P1-1 修复建议的后端半边已闭环）。

## 8. 契约清单修正（routes.go + router_contract_test.go）

- `identity_api`：`POST /share-links/join` → **`POST /share-links/redeem`**（框架只有
  redeem，batch1 根因 C）；顺带删除同组幽灵条目 `GET /share-links/{code}`（v0.4.0/v0.4.1
  的 workspacehttp.Mount 均无此路由）；`GET /invitations` → `GET /invitations/mine`
  （框架实际注册形态）。
- `identity_root`：补 AccountService 活化后真实挂载的五个账号端点。
- 新增 `TestShareLinksContractRedeem`：真实 mount workspacehttp.Mount 断言 redeem 可解析、
  `/join` 与 `GET /share-links/{code}` 为幽灵路由——防止幽灵契约回流。

## 8.5 追加接线：EventIn 补 ActorUserID（FixF 移交激活项）

`wiring_orchestration.go` PhaseProject 投影回调的 `notifapp.EventIn` 构造补
`ActorUserID: e.ActorUserID` 一行——投影侧（notification/app/service.go）的
自通知过滤（`delete(recipients, in.ActorUserID)`）依赖该字段，空串不过滤，
不接线则过滤在生产恒不生效。同治 FP-10 P2-2（任务完成自通知）与 FP-06 P2-2
（评论作者自通知）。依据：docs/fix-wave1-f-security-notify-2026-10-12.md 移交要点。

## 9. 新增测试（cmd/orkome-server/wiring_ws_test.go）

真实 fwwsx.Hub（内存模式）+ gorilla websocket 客户端的帧级断言：

| 用例 | 覆盖 |
|---|---|
| `TestWiringWS_WorkspaceMemberGate` | WorkspaceMember 已设 + 成员/非成员/nil principal 三态 + 帧投递（成员收到 workspace 帧非成员收不到） |
| `TestWiringWS_AuthorizeScopeFence` | 9 组 Authorize 真值表（区内/跨区/空 id/未知 scope/非成员/nil）+ rejected:subscribe / confirm:subscribe 帧路径 + task scope 帧投递 |
| `TestWiringWS_RelayDeliverDoesNotRepublish` | DeliverRemote 本地投递且 **PublishOut 零调用**（探针 relayRecorder 挂 hub.Relay），并对照 Broadcast 会 PublishOut 一次——证明零发布是语义不是巧合 |
| `TestBatchEmitter_CarriesWorkspaceID` | Envelope.WorkspaceID 从 payload 提取 / 缺省为空（门禁丢弃路径不变） |

## 10. 门禁结果与同树并行 WIP 说明

见最终回复的输出摘要。**注意**：本仓库同一工作树上还有其他 fix agent 并行作业
（观测到 internal/skills、internal/work/app 先后处于接口改签名/未完成调用的中间态，
`go build ./...` 会被他们的 WIP 瞬时阻断）。FixD 自身文件（cmd/orkome-server + orchestration/app）
单独构建通过；全树门禁以最终重试记录为准，任何残留失败均不在 FixD 文件内。

## 11. 遗留与建议（未修，属其他波次/裁决）

1. **chat 订阅 creator-only 裁决**（§3 待裁决项）——产品输入后是一行改动。
2. `relayx.NewRelay("")` 应传实例标识（FP-20 F14a 卫生项）。
3. CSRF 镜像文件的删除条件：ploykit 升级暴露 `CSRFConfig.MaxAge` 时收口回框架
   （与 batch2 §五 "v0.4.1 版本钉死的落差"的升级评估合并做）。
4. 前端 `/reset-password`、`/verify-email` 落地页 + api.ts 的 403 恢复逻辑
   （audit-fp01 建议的后端半边已由 MaxAge 对齐解决大半）。
5. RouteContract 其余分册（identity_root 全量、workspaces 子路由）仍未与框架逐一
   对账（walk 测试看不穿框架 StripPrefix 子 mux，需独立清单对账专项）。
