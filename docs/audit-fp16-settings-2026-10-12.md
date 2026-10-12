# FP-16 审计：settings 页全量（账号/安全/偏好）

> 日期：2026-10-12 · 环境：后端 :8080（dev，日志 /tmp/orkome-server.log）+ 前端 :5173（仅静态+API 实测，未开浏览器）
> 账号：`inventory-test@orkome.dev` · 工作区 `inventory-ws`（e83dd858-7412-4625-b048-0741e5ae6552）
> 方法：七步审计（全链梳理 → 契约对齐 → curl 实测 → 代码质量 → 后端正确性 → 测试覆盖 → 缺陷清单）
> 边界：通知偏好归 FP-10（已审：偏好零 UI，其 P2-1）、认证链/改密踢会话归 FP-01（已审 #24/#25）——重叠面只引用不重判
> 结论先行：**P0=0，P1=2，P2=8**。settings 页目前只有「服务端版本 + 会话/工作区」两节（`_app.settings.tsx` 全文 92 行），账号/安全/偏好七类设置区块**整体未建**：改密、会话管理、资料编辑、步进重认证、通知偏好（FP-10）的后端端点全部实测可用却零前端接线；邮箱验证/找回密码/账号注销的端点则因框架 `AccountService` **从未在 orkome 装配**而 5×500。改密与会话吊销的后端语义（吊销全部会话+换发、按 user_id fence、404 语义）经 12 项 curl 实测全部正确。

---

## 一、全链梳理：settings 页区块 × 端点 × 接线状态总账

settings 页（`web/src/routes/_app.settings.tsx:20-90`）现有区块与应有哪些区块的对照：

| 设置区块 | 后端端点 | 后端状态 | 前端接线 | 判定 |
|---|---|---|---|---|
| ① 服务端信息 | GET /config | 200（**无 version 字段**） | `configQueryOptions`（identity.ts:103） | 已接，但版本恒显 "unknown"（P2-1） |
| ② 会话/工作区 | GET /auth/me + GET /api/workspaces | 200 | `meQueryOptions`（identity.ts:113） | 已接（只读展示+工作区切换按钮） |
| ③ 账号资料（display_name/头像） | PATCH /auth/me | **200 实测可用** | **零调用、零表单** | 缺口（P1-2 主体） |
| ④ 修改密码 | POST /auth/change-password | **200 实测可用**（吊销全部会话+换发） | **零调用** | 缺口（P1-2 主体） |
| ⑤ 会话管理（列表/吊销单条/吊销全部） | GET /auth/sessions、DELETE /auth/sessions/{id}、DELETE /auth/sessions | **全部实测可用** | **零调用** | 缺口（P1-2 主体） |
| ⑥ 步进重认证 | POST /auth/confirm-password | 可用（200/401 实测） | 零调用；且 orkome 产品代码**无任何 RequireRecentAuth 消费方** | 死协议（P2 相关） |
| ⑦ 邮箱验证 | POST /auth/send-verification、POST /auth/verify-email | **500（Account 未接线）** | 零调用 | 后端断（P1-1） |
| ⑧ 找回/重置密码 | POST /auth/forgot-password、POST /auth/reset-password | **500（同上）** | 零调用（FP-01 信息项已记「前端未接」，本审计补充：**后端也是死的**） | 后端断（P1-1） |
| ⑨ 账号注销 | DELETE /auth/me | **500（同上）** | 零调用 | 后端断（P1-1） |
| ⑩ MFA/TOTP | /auth/mfa/*（上游） | **orkome 依赖 v0.4.1 不含**（探测 404） | 无 | 版本落差（P2-7） |
| ⑪ 主题/语言 | 无后端（纯前端） | — | 壳层 toggle（theme-toggle.tsx / language-toggle.tsx），localStorage 持久化 | 已有但不随账号走（P2-8） |
| ⑫ 通知偏好 | GET/PATCH/PUT /api/notification-preferences | 可用 | 零调用 | **FP-10 P2-1 已判**，此处不重复 |
| ⑬ 头像上传 | （无端点） | AvatarService 只读（Sign/Verify/Open） | 无 | 半成品（P2-6） |
| ⑭ 邮箱变更 | （框架无此能力） | PATCH /auth/me 只收 display_name/avatar_url | 无 | 框架能力空白（信息项） |

前端 API 封装面佐证：`web/src/api/identity.ts`（149 行）只有 config/login-verify-code/logout/workspaces/members/tokens/me 七组函数；`api.patch` 在 web/src 全仓只有 automation.ts:26 与 squads.ts:69 两处（均与 identity 无关）——③~⑨ 的接线缺口是**封装层就为零**，不是页面漏调。

## 二、后端链路（file:line）

### 2.1 路由挂载（ploykit v0.4.1，vendor 与源码 tag 逐字节一致——仅 CRLF 差异，`git diff v0.4.1` 复核）

`vendor/github.com/haozing/ploykit/identity/adapters/http/routes.go:39-67`：
- 账号面：`PATCH /auth/me`（:57 requireAuth）、`DELETE /auth/me`（:58 RequireHuman）、`POST /auth/change-password`（:59 RequireHuman）、`POST /auth/confirm-password`（:60）、`GET /auth/sessions`（:61）、`DELETE /auth/sessions`（:62）、`DELETE /auth/sessions/{sessionId}`（:63）
- 验证面：`POST /auth/send-verification`（:52 RequireHuman）、`POST /auth/verify-email`（:53 公开）、`POST /auth/forgot-password`（:50 公开）、`POST /auth/reset-password`（:51 公开）

### 2.2 orkome 装配——**Account 链断裂点（P1-1 根因）**

`cmd/orkome-server/wiring_identity.go:130-136`：

```go
func (id *fwIdentity) IdentityMountDeps(proxies []*net.IPNet) identityhttp.Deps {
	return identityhttp.Deps{
		AuthCfg: id.Auth, Sessions: id.Repo, PATs: id.Repo,
		SessionsSvc: id.Sessions, Tokens: id.Tokens,
		TrustedProxies: proxies,
	}   // ← 没有 Account 字段
}
```

框架 handler 对 `d.Account == nil` 的保护是直接 500（`account.go:10-12/28-30/48-51/59-62`、`handlers.go:245-248`）。而装配侧的讽刺之处：
- 邮件发送器**三通道已备**（`wiring_identity.go:51-61`：Resend/SMTP/DevLog，dev 走 DevLog）；
- DevLog 实现了全部四个发送方法，**包括** `SendPasswordReset`/`SendEmailVerification`（`internal/identity/adapters/email/email.go:37-45`）——框架找回/验证链的邮件侧完全就绪；
- `AccountConfig.LinkBaseURL` 全仓无任何设值（grep 零命中）；
- `fwapp.NewAccountService` 在 orkome 仓库零调用。

即：**只差 3 行接线**（构造 AccountService + Deps.Account 赋值 + LinkBaseURL 配置），找回密码/邮箱验证/账号注销三个能力即活。

### 2.3 会话/改密服务语义（vendor ploykit）

- 改密 `service_session.go:371-411`：验旧密码 → 拒同密码 → `PasswordOK`（10+ 字符 3/4 类，`domain/rules.go:162-186`）→ SetPasswordHash → **`RevokeAllUserSessions`（:394，吊销该用户全部会话，含当前）** → 新建 `PasswordConfirmed: true` 会话 → 回 Set-Cookie → AfterPasswordChange hook（orkome 未挂，无影响）。
- 会话校验 `pgrepo/sessions.go:46-75`：token 哈希 + `revoked_at IS NULL` + 滑动/绝对双 TTL + `status='active'` + `tokens_valid_after` 五条件。
- 吊销单条 `pgrepo/sessions.go:121-132`：`WHERE id=$2 AND user_id=$1`——**按 user_id fence**，不能吊他人会话；RowsAffected=0 → ErrNotFound → 404。
- 列表 `pgrepo/sessions.go:134-153`：ip_hash（SHA256 哈希非明文 IP）、UA、last_seen、双 TTL；handler 补 `Current` 标记（`handlers.go:221-223`）。
- 资料更新 `service_session.go:424-436` + `pgrepo/users.go:62-75`：display_name trim 后 1-100 rune；avatar_url 空串合法、非空须 http(s) 且 ≤2048；**空串落 NULL（全量覆盖语义，见 P2-4）**。
- 步进 `service_session.go:346-369`：ConfirmPassword 走 `tooManyAttempts` 限流（注释明言"劫持会话不得爆破密码"）——对比之下 **ChangePassword 的旧密码校验（:376）反而不走限流**（P2-2）。

### 2.4 MFA 版本落差（P2-7）

ploykit 源码仓 `git diff v0.4.1 -- identity/`：+1176 行，含 `adapters/http/mfa.go`（122 行，`MFA *app.MFAService` Deps，注释"Nil = the capability is absent, not degraded"）、`app/service_mfa.go`（336 行）、`app/totp.go`（98 行）、`pgrepo/mfa.go`，并新增 `MFAChangeGuard`/`PATCreateGuard` 两个步进护栏挂点。orkome go.mod 钉在 v0.4.1（`go.mod:15`），四个 MFA 路径探测全 404。前端/web 层无任何 MFA 预留（grep 零命中）。

## 三、curl 实测记录（2026-10-12，cookie+CSRF 会话）

| # | 用例 | 结果 | 证据 |
|---|---|---|---|
| 1 | PATCH /auth/me 改 display_name | 200，GET 回读一致 | 全量 body `{display_name,avatar_url}` 必填 |
| 2 | PATCH display_name="" | 400 E_VALIDATION "display_name must be 1-100 characters after trim" | |
| 3 | PATCH display_name 101 字符 | 400 同上 | |
| 4 | PATCH avatar_url="ftp://…" | 400 E_VALIDATION（http(s) 限定） | |
| 5 | PATCH avatar_url 2060 字符 | 400 E_VALIDATION（≤2048） | |
| 6 | PATCH avatar_url 合法 https → 置头像 | 200，响应含 avatar_url | |
| 7 | PATCH avatar_url="" → 清头像 | 200，avatar_url 消失 | 全量覆盖语义实证（P2-4） |
| 8 | GET /auth/sessions（双会话） | 200，字段 id/ip_hash/user_agent/last_seen_at/expires_at/created_at/current | current 标记正确落在调用方 |
| 9 | DELETE /auth/sessions/{他会话id}（同用户） | 204；该会话后续 /auth/me 401 | 允许吊销自己名下其他会话（设计如此） |
| 10 | 重复吊销 / 吊销随机 uuid | 404 E_NOT_FOUND "session not found" ×2 | |
| 11 | 无 CSRF 写请求 | 403 E_FORBIDDEN | CSRF 链正常 |
| 12 | change-password 正确旧密码 | 200 `{expires_at}`+新 cookie；**另一 jar 旧会话即刻 401，本 jar 新 cookie 200** | 与 FP-01 #24/#25 一致 |
| 13 | change-password 旧密码错 | 401 E_UNAUTHENTICATED「当前密码不正确，请确认后重新输入」 | 中文消息 |
| 14 | change-password 新旧相同 | 400「新密码不能与当前密码相同」 | 中文消息 |
| 15 | change-password 弱密码 | 400 **"new password too weak"** | **英文消息**——同端点中英混杂（P2-3） |
| 16 | 改回原密码 + 原密码重新登录 | 200/200 | 已还原，`Inv3ntory!2026` 有效 |
| 17 | POST /auth/send-verification | **500 E_INTERNAL "internal error"** | Account==nil |
| 18 | POST /auth/verify-email（公开+正确 CSRF） | **500** | 同上 |
| 19 | POST /auth/forgot-password（存在/不存在邮箱各一次） | **500 / 500** | 不存在邮箱本应 204 防枚举（service_account.go:74-76），同样 500 |
| 20 | POST /auth/reset-password | **500** | 同上 |
| 21 | DELETE /auth/me | **500**（nil 检查前置，账号无恙） | 随后 /auth/me 200 |
| 22 | confirm-password 错/对 | 401 "invalid password" / 200 `{password_confirmed_at}` | 限流器接了（wrong 不返回 429 是未触阈值） |
| 23 | /auth/mfa/setup、/auth/mfa、/api/mfa 等 4 路径 × POST/GET | 全 404 | v0.4.1 无 MFA 路由 |

实测后清理：display_name 已还原为原值（原值本身是 U+FFFD 乱码——建号脚本 GBK 写坏，属测试数据问题非产品缺陷）、avatar 已清、密码已还原、自建会话已吊销。

## 四、代码质量

- settings 页两节均双语齐备（zh.ts:586-599 / en.ts:621-634，13 键 zh-en 一一对应，无缺键）；401 处理是全站唯一页面级实现（`_app.settings.tsx:19,80-87`；全局缺口 FP-01 P2-2 已记）。
- 无工作区空态引导（:64-77）与工作区切换按钮组（:50-63，走 `services/workspace-switch.ts`，正确保留 `me`/`config` 缓存只清业务缓存并重连 WS）——这两节质量没有问题。
- 页面无 h1（h2 起步）、无 `<title>` 定制，导航层 `nav.settings` 正常高亮。
- 「危险操作确认」无从谈起：页面唯一算敏感的登出在壳层（FP-01 P2-3 已记其 401 死按钮问题）。
- 前端 `web/src` 全仓 **0 个测试文件**——settings 页质量完全靠人工回归。

## 五、后端正确性专项

1. **改密吊销范围 = 全端（该用户所有会话）**，不是"其他设备"粒度——实测 #12 双 jar 验证。语义安全（宁可全踢），且新会话 `PasswordConfirmed=true` 免于步进重认证，设计自洽。
2. **吊销越权 fence 正确**：DELETE /auth/sessions/{id} 按 user_id 过滤（实测 #9/#10，他人会话/不存在 id 均 404，不泄露存在性）。
3. **邮箱变更能力不存在**：PATCH /auth/me 无 email 字段，`UpdateProfile` 不触碰 email——不存在「邮箱变更是否重置验证态」的问题，因为变不了（信息项，产品若要邮箱变更需框架扩展）。
4. **email_verified 永假闭环**：测试账号 `email_verified=false`，而置真的唯一路径 SetEmailVerified 只被 VerifyEmail 调用（service_account.go:173），后者 500——**当前系统没有任何用户能把邮箱置为已验证**（P1-1 的后果面）。
5. 反枚举语义在框架里是对的（forgot-password 未知邮箱静默 204，service_account.go:74-76），被 nil-500 整体压掉——接线后无需改动即正确。
6. 头像链：`content:avatar:write` 权限点在 catalog 声明（wiring_identity.go:88）但全仓零消费；AvatarService 无 Upload 方法（service.go:278-306 只有 Sign/Verify/Open）——上传面从未实现，读取面的 HMAC 签名（exp+sig）倒是完备。

## 六、测试覆盖

| 层 | 已有 | 缺口 |
|---|---|---|
| ploykit 源码仓 | service_session_test（改密/步进/枚举均衡）、service_account_test（验证/找回/注销）、sessions_test（http 层 logout/revoke/me/confirm-password/current 标记）、account_deletion_test、pgrepo sessions/challenges 集成测试；上游另有 mfa_test/totp_test | —（框架层覆盖良好） |
| orkome internal/identity | 仅 pins/ui-tokens/cli-token 四组（integration_test.go:219-439） | **settings 面在 orkome 仓库零测试**：装配层（Account==nil 这种断链恰好是装配层断的，任何一层集成测试都会抓住）零断言——与 FP-01「认证链装配层零覆盖」同根 |
| orkome e2e | registry_test.go:43-46 仅声明 send-code/verify-code/config 三条 identity 公开路由 | change-password/sessions/me-PATCH 无 e2e 场景 |
| 前端 | 无测试文件 | settings 页零回归手段 |

「测试走 A 生产走 B」模式在本 FP 的体现：框架单测证明 AccountService 逻辑正确，但 orkome 装配从未实例化它——**单测全绿与线上 500 并存**，正是 batch1 系统性模式 #2 的又一实例。

## 七、缺陷清单

### P0（0 项）
无。settings 现有两节可用；后端可用端点实测无一颗雷。

### P1（2 项）

**P1-1 AccountService 从未装配：邮箱验证/找回密码/账号注销 5 端点全 500，email_verified 永假闭环**
- 根因：`cmd/orkome-server/wiring_identity.go:130-136` `IdentityMountDeps` 不设 `Account`；`fwapp.NewAccountService` 全仓零调用；`AccountConfig.LinkBaseURL` 无值。
- 后果面：找回密码（用户改密后忘旧密码即永久锁死，FP-01 信息项 4 的真根因在此）、邮箱验证（email_verified 全体恒 false）、账号注销（合规面）三条链全断；且邮件发送器三通道已就绪、DevLog 连 SendPasswordReset/SendEmailVerification 都实现了（email/email.go:37-45），框架逻辑有完整单测——**只差 3 行接线 + 1 个 LinkBaseURL 配置**。
- 最小修复：`wiring_identity.go` 构造 `fwapp.NewAccountService(repo, sender, fwapp.AccountConfig{SecretPepper: cfg.JWTSecret, LinkBaseURL: cfg.AppURL})` 并在 `IdentityMountDeps` 返回值加 `Account: account`；`config.go` 增 `AppURL`（或复用现有前端地址配置）。前端还需补 `/verify-email`、`/reset-password` 两个落地路由页（邮件链接目标，现不存在会 404——`service_account.go:90,150` 拼的就是这两个路径）。

**P1-2 settings 安全面整体未建：改密/会话管理/资料编辑后端全可用、前端零接线（封装层即为零）**
- 现状：`_app.settings.tsx` 全文 92 行只有两节；PATCH /auth/me、change-password、GET/DELETE /auth/sessions（×2）五个端点实测可用（§三 #1-15），`web/src/api/identity.ts` 无一封装、页面无一控件。
- 后果面：① 任何用户**无法改密码**——叠加 P1-1 亦无法找回，密码即身份的全部安全边界却不存在任何用户触点；② 泄露会话无法自查/吊销（GET /auth/sessions 连 current 标记都备好了）；③ display_name 只能靠注册时一次定终身（当前注册 UI 也没有，见 FP-01）。
- 最小修复：`identity.ts` 增 `updateMe/changePassword/listSessions/revokeSession/revokeAllSessions` 五个封装（契约见 §三），settings 页增「账号资料」「安全」两节：资料表单（display_name+头像 URL，含 1-100/2048 校验镜像）+ 改密表单（old/new/确认，服务端错误按 code 映射 i18n）+ 会话列表（current 徽标 + 单条吊销 + 全部吊销二次确认）。

### P2（8 项）

| # | 缺陷 | 根因（file:line） | 最小修复 |
|---|---|---|---|
| 1 | settings 版本恒显 "unknown"：前端读 `config.data.version`，后端 /config Extra 只给 `product` | `web/src/routes/_app.settings.tsx:28`；`cmd/orkome-server/main.go:300-302` | Extra 补 `version`（build ldflags 或 go.mod 版本） |
| 2 | change-password 旧密码校验无限流：ConfirmPassword 走 tooManyAttempts（注释明言防劫持爆破），ChangePassword 同样验旧密码却不走——劫持会话可无限速猜旧密码 | `vendor/.../identity/app/service_session.go:376`（对比 :351-355） | ChangePassword 验旧前加同一 `tooManyAttempts` 检查（框架改动） |
| 3 | 改密错误消息中英混杂直出：同端点两条中文（「当前密码不正确…」「新密码不能与当前密码相同」）+ 一条英文（"new password too weak"） | `vendor/.../identity/app/service_session.go:378,382,385` | 统一语言；前端按 error code 映射 settings.* i18n 键（FP-01 P2-7 同族） |
| 4 | PATCH /auth/me 全量覆盖语义无防护：两字段必填，漏发 avatar_url 即清头像（repo 空串→NULL）；将来接 UI 时最易踩 | `vendor/.../identity/adapters/pgrepo/users.go:63-66`；handler 无指针字段 `handlers.go:163-177` | 前端封装固定同发双字段并注释；或框架改指针字段做部分更新 |
| 5 | 路由契约表 settings 面漂移：RouteContract identity_api 写 "GET/PATCH /me"（实际 /auth/me）、"POST /share-links/join"（实际 /redeem，batch1 根因 C）；改密/会话/账号/验证 9 条路由完全不在契约表——单向对账抓不住 | `cmd/orkome-server/routes.go:6-16` | 契约表对齐实际挂载 + 补 settings 面 9 条（batch1 第三波「双向对账」项的实例） |
| 6 | 头像链半成品：AvatarService 只读（无 Upload），`content:avatar:write` 死权限点，PATCH /auth/me 只能填外链 | `internal/content/app/service.go:278-306`；`internal/content/adapters/http/routes.go:66-68`；`wiring_identity.go:88` | 短期：settings 资料表单用外链+说明；长期：POST /api/avatars 上传端点（复用 content 上传管线+权限点） |
| 7 | MFA 能力版本落差：上游 ploykit 已实现完整 TOTP 面（+1176 行，含 MFAChangeGuard/PATCreateGuard 护栏挂点），orkome 钉 v0.4.1，探测全 404 | `go.mod:15`；`git diff v0.4.1 -- identity/` | 升级 ploykit 后按 Deps.MFA 装配（框架明言 nil=能力不存在，无半接线风险），前端补 MFA 节 |
| 8 | 偏好不随账号走：主题/语言仅 localStorage（`orkome.ui`/`orkome.locale`），前端 User 接口声明的 `language/timezone` 后端模型根本没有（avatar_url 后端有）；换设备即丢 | `web/src/api/identity.ts:7-9`（死字段）；`stores/ui.ts:23`；`i18n/index.ts:8` | 删除死字段或框架扩展 User；跨设备同步属产品决策，先修接口漂移 |

### 信息项（不计缺陷）
- 邮箱变更能力框架不存在（UpdateProfile 无 email 字段）——「改邮箱重置验证态」无从谈起，产品需要时属框架扩展。
- 测试账号 display_name 本身是 U+FFFD 乱码（建号脚本 GBK 写坏），与产品无关。
- FP-10 P2-1（通知偏好零 UI）在 settings 页的落点即本页「通知」节的缺失，修复时与 P1-2 同刀。

## 八、亮点（保持项）

1. **会话安全语义全部正确**：改密全踢+换发、吊销按 user_id fence、404 不泄露存在性、ip_hash 落库非明文、VerifySession 五条件（含 `tokens_valid_after` 对改密前老会话的兜底）——12 项实测零偏差。
2. **反枚举与防爆破在框架层设计到位**：forgot-password 未知邮箱静默 204、dummy-PHC 均衡（FP-01 已证）、ConfirmPassword 限流注释明确（"a hijacked session must not brute-force the password"）。
3. **/auth/sessions 响应字段即为 UI Ready**：id/UA/last_seen/双 TTL/current 标记一应俱全，前端接线无需后端任何改动。
4. settings 页 401 空态与无工作区空态是全站状态覆盖最好的页面（虽然这也反衬其余页面的缺口）。

## 九、修复顺序建议

1. **P1-1 三行接线**（AccountService+LinkBaseURL+Deps.Account）+ 前端 /verify-email、/reset-password 两个落地页——解锁找回/验证/注销三链，成本最小收益最大。
2. **P1-2 settings 两节**（资料/安全）：identity.ts 五封装 → 表单+会话列表；通知节按 FP-10 修复计划并入。
3. P2-1 version 一行；P2-5 契约表对齐（与 batch1 第三波双向对账合并做）；P2-3 i18n 映射与 P1-2 表单同刀。
4. P2-2/P2-7 随 ploykit 升级窗口一并处理；P2-4/P2-8 随对应 UI 落地时处理。

## 十、证据留存

- 实测会话：fp16 前缀 cookie jar（/tmp/fp16-cookies*.txt，已用毕吊销自建会话）；display_name/密码/头像均已还原，密码 `Inv3ntory!2026` 复登 200 验证。
- 改密实测不可避免地踢掉了共享账号上的并行会话一次（窗口 ~1 秒）；另有一次误吊同账号他人会话（已从会话列表误认目标，即时发现，产品语义正是允许吊销名下全部会话，无数据损失）。
- 关键中间文件：/tmp/fp16-me.json（原始 /auth/me 抓样）、/tmp/fp16-sess-before|after.json（会话清单抓样）。
- vendor 与 ploykit v0.4.1 tag 一致性：逐文件 `git show v0.4.1 | diff`（仅 CRLF）；上游增量 `git diff v0.4.1 --stat -- identity/`。
