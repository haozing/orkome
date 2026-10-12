# FP-01 认证与会话 审计报告（登录/验证码/注册/登出/会话生命周期）

> 日期：2026-10-12 · 审计人：FP-01 专项 agent · 方法：代码审计（前端 web/src + ploykit identity v0.4.0 + orkome wiring）+ curl 活体实测 26 项
> 环境：后端 :8080（dev，日志 /tmp/orkome-server.log，email=dev log 通道）；测试账号 `inventory-test@orkome.dev`；dev code `888888` 已启用
> 范围：登录两条流、注册、登出、CSRF 三段式、会话生命周期、AUTH_TOKEN_TTL、send-code 限流、注册开关
> 结论速览：**P0×0 / P1×2 / P2×9** —— 核心链路（验证码登录/注册/登出/会话吊销/CSRF/限流/枚举防护）活体全通；两个 P1 都是"长时会话下的静默失效"类（CSRF 凭据 12h 早于会话 7d 过期且前端无恢复；ALLOW_SIGNUP=false 单独设置不生效）。

---

## 一、全链梳理（七步法 · 步骤 1）

### 1.1 登录（验证码流，主）

| 层 | 文件:行 | 职责 | 备注 |
|---|---|---|---|
| 路由守卫 | `web/src/routes/login.tsx:12-22` | 已登录访问 /login → redirect /work-items | `ensureQueryData(meQueryOptions)`，失败则留在登录页 |
| 表单 | `web/src/routes/login.tsx:37-65` | 两步：email → code；zod（6 位数字 `login.codeInvalid`）；mutation 错误 `role=alert` 展示 | `submitting` 合并两个 mutation 的 pending，按钮 loading ✓ |
| API 封装 | `web/src/api/identity.ts:39-55` | `sendLoginCode→POST /auth/send-code`、`verifyLoginCode→POST /auth/verify-code`、`logout→POST /auth/logout` | 注释明确 verify-code 响应不再回带 csrf_token（v0.4.1 契约） |
| HTTP client | ploykit `client/src/api.ts:23-49,168-252` | CSRF 三段式：首个写请求前 `ensureCsrfToken()` GET /config 取 `csrf_token` → 写请求带 `X-CSRF-Token`；`/api/*` 自动注 `X-Workspace-Id`；15s 超时；非 2xx 抛 `ApiError` | **token 模块级缓存永不失效、403 无恢复逻辑（P1-1 前端半边）** |
| Handler | ploykit `identity/adapters/http/handlers.go:75-104` | DecodeJSON → `SessionsSvc.SendCode/VerifyCode` → 成功 `SetSessionCookie` + `{user, expires_at}` | |
| 路由挂载 | ploykit `identity/adapters/http/routes.go:39-42,55-56` | send-code/verify-code/register/login 公开；logout/me 走 `requireAuthFunc` | `Authenticate` 中间件由 orkome 挂在全链 |
| App 服务 | ploykit `identity/app/service_session.go:131-157,159-245` | SendCode（限流+冷却+HMAC 哈希入库+发信）；VerifyCode（dev code 旁路/挑战校验/5 次上限/JIT 建号/会话创建） | |
| 存储 | ploykit `identity/adapters/pgrepo/challenges.go:10-64`、`sessions.go:15-114` | challenge：HMAC-SHA256 哈希、attempts 计数、consume；session：token SHA256 落库、滑动+绝对双 TTL | |
| 装配 | `cmd/orkome-server/wiring_identity.go:44-112` | cookie `orkome_auth`、SessionTTL 7d、AbsoluteTTL 30d、DevCode/AllowSignup/AllowedEmails 注入、发信通道三级回落（Resend→SMTP→DevLog） | **cfg.TokenTTL 未消费（P2-1）** |
| 中间件链 | `cmd/orkome-server/main.go:300-308` | `/config`=webx.ConfigHandler；链序 Recover→RequestID→ClientMetadata→AccessLog→SecurityHeaders→CORS→**CSRFConditional**→Authenticate | **TrustLocalhostAnyPort=true 硬编码（P2-6）** |

### 1.2 登录（密码流，辅 / API-only）

| 层 | 文件:行 | 状态 |
|---|---|---|
| Handler/Service | `handlers.go:124-139` → `service_session.go:298-339` | 完整可用：dummy-PHC 枚举均衡（I8 有专项测试）、限流 10 次/15min/(ip,email)、成功 `PasswordConfirmed` 会话 |
| 前端 | 无任何消费（`grep password/register` 仅 packs-sections MCP key 输入与 react-hook-form API 名） | **判定：设计而非漏接**（见 §二） |
| 改密 | `handlers.go:179-194` → `service_session.go:371-411` | 活体可用；改密吊销全部会话并换发新会话（实测 §三 #24-26） |

### 1.3 注册

| 层 | 文件:行 | 状态 |
|---|---|---|
| Handler/Service | `handlers.go:106-122` → `service_session.go:247-296` | 密码规则 10+ 字符 3/4 类；display_name 1-100；signupBlocked 门；重复 409；AfterRegister hook 事务内（orkome 未挂 hook → 注册后无工作区 → onboarding） |
| 前端 | 无注册页（新用户走验证码 JIT 路径：`login.tsx` → verify-code → UpsertUserByEmail） | API-only 面 |

### 1.4 登出/会话生命周期

| 环节 | 实现 | 活体证据 |
|---|---|---|
| 创建 | verify-code/register/login/change-password → `pgrepo/sessions.go:25-44`：MintToken 32B hex，SHA256 落库，`expires_at`=now+7d，`absolute_expires_at`=now+30d | #12/#21/#24 |
| 滑动续期 | `webx/auth.go:157-160` 每请求 `RenewSession`；`pgrepo/sessions.go:91-109`：剩余 < 3.5d 时续到 now+7d（不超绝对），Set-Cookie 回写 | 代码审读 |
| 校验 | `pgrepo/sessions.go:46-75`：token 哈希 + revoked_at + 双 TTL + `u.status='active'` + `u.tokens_valid_after < s.created_at` | #9（logout 后旧 cookie 401） |
| 吊销 | logout 单会话（`handlers.go:141-147`）；change-password/revoke-all 全量（`service_session.go:394`、`pgrepo/sessions.go:116-119`） | #8/#9/#24/#25 |
| 前端登出 | `_app.tsx:79-85`：`await logout()` → 清 workspace store → `queryClient.clear()` → ws close → navigate /login | #3 缺陷见 P2-3 |

## 二、契约对齐（步骤 2）

**"登录页无密码入口"判定：设计选择，不是漏接。** 证据链：
1. i18n zh `web/src/i18n/locales/zh.ts:561-` 与 en `en.ts:596-` 的 `login.*` 命名空间只有验证码流键（title/emailStepDesc/codeSentTo/codeLabel/…），**无任何 password/register 键**——若是漏接应存在半成品键；
2. 前端零 `auth/register`、`auth/login`（密码）调用；后端两者由 ploykit 框架挂载，服务于外部契约（MCP / client-kit / PAT 客户端）；
3. 产品语义自洽：新用户验证码 JIT 建号（`email_verified=true`），首次无工作区 → `_app.tsx:42-44` redirect /onboarding 建工作区。无密码流不阻碍任何用户旅程。
4. 残留影响：改密（`POST /auth/change-password`）后用户若忘记"当前密码"，前端无找回入口（forgot-password 端点存在但前端未接、Resend 未配时也发不出信）。记录为信息项。

**字段对照**（前端 identity.ts vs 后端 handlers.go）：

| 端点 | 后端返回 | 前端消费 | 对齐 |
|---|---|---|---|
| POST /auth/send-code | `{"status":"sent"}` | `{status}` | ✓ |
| POST /auth/verify-code | `{user, expires_at}` + Set-Cookie | `{user, expires_at}`（cookie 由浏览器接） | ✓（不再回带 csrf_token，`identity.ts:46-47` 注释与 e2e `golden_test.go:578-583` 注释一致） |
| POST /auth/logout | `{"status":"ok"}` | void | ✓ |
| GET /auth/me | meResponse{User…, impersonated_by?} | `User`（多余字段无害） | ✓ |
| GET /config | `{csrf_token, oauth_providers, billing_channels, product}` | `{version, csrf_token}`（version 由 Extra 补充？orkome Extra 只给 product——`configQueryOptions.version` 实际恒空，settings 页显示 "unknown"， Cosmetic） | ✓（功能），version 字段前端永不非空（微小，未计缺陷） |

已知越界问题（inventory 总账已记，本审计不重复计）：onboarding `joinByShareCode` 调 `/api/share-links/join` 而实际路由 `/redeem`（P0-4）；`createToken` 发 `ttl_seconds` vs 后端 `ttl_hours`（P0-3）——均属 MCP/PAT 面，仅在本链路表标注位置。

## 三、curl 活体实测（步骤 3，26 项全记录）

| # | 场景 | 期望 | 实际 | 判定 |
|---|---|---|---|---|
| 1 | 密码登录（inventory-test） | 200+Set-Cookie | 200，`expires_at`=+7d | ✓ |
| 2 | 错误密码 | 401 | 401 `invalid credentials` | ✓ |
| 3 | 不存在邮箱+密码 | 与 #2 完全一致 | 401 同文同码 | ✓ 枚举均衡 |
| 4 | 伪造 X-CSRF-Token | 403 | 403 `CSRF token missing or invalid` | ✓ |
| 5 | 缺 X-CSRF-Token | 403 | 403 同上 | ✓ |
| 6 | 有 CSRF 无会话 logout | 401 | 401 `authentication required` | ✓（同时证明前端 P2-3：过期会话点登出必 401） |
| 7 | 无 cookie GET /auth/me | 401 | 401 | ✓ |
| 8 | 有效会话 logout | 200 ok | 200 | ✓ |
| 9 | logout 后旧 cookie /auth/me | 401 | 401 | ✓ 服务端真吊销 |
| 10 | send-code | 200 sent | 200；DevLog 落 `code=638878` | ✓ |
| 11 | 错误验证码 | 401 | 401 `code mismatch` | ✓ |
| 12 | 正确验证码 | 200+JIT 建号 | 200，`fp01-codetest` 创建，`email_verified=true` | ✓ |
| 13 | 成功登录后立即重发 | — | 200（challenge 已 consume，冷却自然不适用） | ✓ 行为合理，记录 |
| 14 | send-code 非法邮箱 | 400 | 400 `invalid email` | ✓ |
| 15 | pending 未消费时连发两次 | 第二次 429 | 429 `please wait before requesting another code` | ✓ 1min 冷却 |
| 16 | 错误码 ×5 | 5×401 | 5×401 `code mismatch` | ✓ |
| 17 | 第 6 次提交正确码 | 拒绝 | 401 `too many attempts` | ✓ CodeMaxTries=5 生效 |
| 18 | dev code 888888 免 send-code 直登 | 200（dev） | 200 JIT `fp01-devbypass` | ✓（prod 双保险：`config.go:98-100` 拒启 + `service_session.go:165-169` I15 belt + 专项测试） |
| 19 | 密码爆破 10+ 次 | 触顶 429 | 第 10 次 429 `too many attempts, retry later` | ✓ MaxAttemptsPerIP=10/15min |
| 20 | 弱密码注册 | 400 | 400 文案含规则（10+ 字符 3/4 类） | ✓ |
| 21 | 合法注册 | 200+会话 | 200，`email_verified=false` | ✓ |
| 22 | 重复注册 | 409 | 409 `email already registered` | ✓ |
| 23 | 注册后密码回登 | 200 | 200 | ✓ |
| 24 | 改密 | 200+换发会话 | 200 `expires_at` | ✓ |
| 25 | 改密后另一 jar 的旧会话 | 401 | 401 | ✓ RevokeAllUserSessions 生效 |
| 26 | 新密码登录 | 200 | 200 | ✓ |

## 四、代码质量（步骤 4）

- **401 拦截**：仅两处——`_app.tsx:36-41`（路由 beforeLoad，导航时兜住）与 `_app.settings.tsx:19`（页面级提示）。**无全局 QueryCache/MutationCache onError、无 client 级 401 处理**（`web/src/lib/query.ts` 只有 retry 策略）：会话在页内过期时所有查询失败只显示"请求失败"，直到下一次路由跳转才被 beforeLoad 弹去 /login（P2-2）。
- **CSRF 403 无恢复**：`client/src/api.ts` 的 `csrfToken` 模块级缓存只在成功响应 `maybeRefreshCsrf` 时更新；403 时不重引导、不清缓存。叠加后端 CSRF cookie 12h（见 P1-1）形成死锁。
- **i18n**：表单校验与按钮文案双语齐全；但服务端错误直出——`login.tsx:59-61` `rawError instanceof ApiError ? rawError.message : t(...)`，中文界面会看到 "code mismatch" / "too many attempts, retry later" 英文（P2-7）。
- **loading 态**：登录两步均有 pending 禁用 + 文案切换 ✓；onboarding 两表单同 ✓。

## 五、后端正确性（步骤 5）

- **CSRF 校验链**：gorilla/csrf，key=`DeriveCSRFKey(JWTSecret)`（`webx/csrf.go:40-47`，重启稳定、多副本一致）；cookie `ploykit_csrf` HttpOnly+Lax，Secure 随 prod；`/ws` 豁免（首帧自行鉴权）；PAT 请求豁免（`isPATRequest`）；/config 空 token 熔断（`webx/config.go:23-30` F20 防装配遗漏）。伪造/缺失 403 实测通过。
- **会话存储与轮换**：token 仅哈希落库；滑动+绝对双 TTL；`tokens_valid_after` 兜底改密前的老会话。**缺口**：RenewSession 只续期不换 token——30 天内同一 token 值（P2-9）；TTL 双份来源（`wiring_identity.go:46-49` pgrepo.Config 与 :63-67 AuthConfig 各写一份 7d/30d，另有 cfg.TokenTTL 死旋钮，P2-1）。
- **send-code 限流**：per-email pending 冷却 1min ✓（实测 #15）+ per-(ip,email) 失败计数 10/15min ✓。**缺口**：无 per-IP 发送量上限——攻击者可对不同邮箱各发 1 封/分钟、总量无界（prod 配 Resend 后是成本/骚扰面）（P2-5）。
- **verify-code 限流**：仅 per-challenge 5 次 + 冷却间接约束；`tooManyAttempts` 未接进 VerifyCode——爆破预算 ≈5 码/分钟/邮箱 ≈ 0.7%/天（P2-4）。
- **枚举防护**：密码流 dummy-PHC 均衡 ✓（实测 #2/#3，且有 `TestLoginWithPassword_UnknownEmailEqualized_I8` 专项）；send-code 恒 `sent` ✓。残留：`AllowSignup=false` 时 verify-code 对不存在邮箱返回 403 "signup is not open for this email" 而存在邮箱继续走（`service_session.go:202-208`）——closed-signup 模式下的存在性 oracle（信息项，dev 默认 AllowSignup=true 不触发）。
- **注册开关语义陷阱**：`signupgate.go:14-23`：AllowSignup=false 时 `SignupAllowed(email, lists...)` 在两表全空时返回 true（`rules.go:112-117` 短路）→ **ALLOW_SIGNUP=false 单独设置完全不设防**（P1-2）。
- **AUTH_TOKEN_TTL**：`config.go:19` 定义、`:95-97` 校验，全仓无消费点（grep 实证）；实际 TTL 硬编码 7d/30d（P2-1）。
- **login_attempt/auth_challenge 清理**：`CleanupAttempts`（`pgrepo/challenges.go:66`）在 ploykit v0.4.0 与 orkome 全仓**均无调用方**，两张表无界增长（P2-8）。

## 六、测试覆盖（步骤 6）

| 层 | 现有 | 缺口 |
|---|---|---|
| ploykit app 层 | `service_session_test.go`（hooks/change-password/step-up/枚举均衡/出生即确认）、`service_session_prod_test.go`（I15 双保险）、`signupgate_test.go`（gate 覆盖 config、JIT 路径）、`service_oauth_gate_hooks_test.go:173`（登录 429） | verify-code 自身无 IP 限流属设计缺口，无测试可言 |
| ploykit http 层 | `sessions_test.go`（logout/revoke/me/confirm-password/当前会话标记） | — |
| ploykit pgrepo | sessions/challenges/pats 均有集成测试 | — |
| orkome internal/identity | pins/ui-tokens 集成测试 | — |
| orkome e2e | `golden_test.go:553-568` `login()` helper（dev code 654321）驱动全链测试；`registry_test.go:43-45` 声明 send-code/verify-code 公开路由 | **无认证行为专项**：logout 后旧 cookie 失效、错误验证码、CSRF 403、限流 429、register、改密吊销均只在 ploykit 单测覆盖，orkome 装配层（cookie 名/中间件链序/TrustLocalhostAnyPort）零断言。建议补一条 e2e 场景（复用 golden harness 即可） |

## 七、缺陷清单（步骤 7）

### P0（0 项）
无。核心链路 26 项活体全通。

### P1（2 项）

**P1-1 CSRF 凭据 12h 过期早于会话 7d，前端无恢复 → 长闲置 tab 所有写操作死锁**
- 现象推演：gorilla/csrf cookie 默认 MaxAge=12h（`vendor/github.com/gorilla/csrf/csrf.go:39` `defaultAge=3600*12`；`webx/csrf.go:127-136` 未设 `csrf.MaxAge`），而会话 cookie 7d 滑动。用户闲置 >12h 后回来：GET 正常，首个写请求带的是内存里的旧 token → 403；此后每次写仍用同一缓存 token（`client/src/api.ts:23-49` `csrfToken` 仅成功时经 `maybeRefreshCsrf` 更新、403 无清缓存重试逻辑）→ 写操作全部 403，唯一恢复手段是 F5。SPA 常驻 tab 场景必现。
- 根因：后端 `cmd/orkome-server/main.go:308`（CSRFConditional 未配 MaxAge）+ 前端 `client/src/api.ts`（无 403 恢复）。
- 最小修复（二选一即可，建议都做）：后端 `csrf.MaxAge(7*24*3600)` 与会话对齐；前端在 `ApiError(403, E_FORBIDDEN)` 时清空 csrfToken 并重引导一次 /config 后单次重试。

**P1-2 `ALLOW_SIGNUP=false` 单独设置不生效（空白名单 = 全开放注册）**
- 根因：`vendor/.../identity/app/signupgate.go:14-23` → `domain.SignupAllowed`（`rules.go:112-117`）在 `AllowedEmails` 与 `AllowedDomains` 均空时短路返回 true；orkome `config.go:23-25` 三者默认全空。运营设 `ALLOW_SIGNUP=false` 期望关闭注册，实际 JIT/注册照常放行，无任何日志提示。
- 最小修复：`signupBlocked` 中 `!allowSignup && len(allowedEmails)==0 && len(domains)==0 → return true`（收紧）；或 `ParseConfig` 启动校验：`!AllowSignup && 两表空 → 报错拒启`。

### P2（9 项）

| # | 缺陷 | 根因（file:line） | 最小修复 |
|---|---|---|---|
| 1 | `AUTH_TOKEN_TTL` 死旋钮：定义+校验但零消费，会话 TTL 实际硬编码 7d/30d（且 pgrepo.Config 与 AuthConfig 双份来源） | `cmd/orkome-server/config.go:19,95`；`wiring_identity.go:46-49,63-67` | wiring 改读 `cfg.TokenTTL`（滑动=TTL，绝对=3×TTL 或独立 env），文档标注 |
| 2 | 会话页内过期无全局 401 处理：查询失败只报错，不跳登录（仅路由切换兜底） | `web/src/lib/query.ts`（无 QueryCache onError）；仅 `_app.tsx:36-41`、`_app.settings.tsx:19` 两处 | createQueryClient 挂 `QueryCache.onError`：`ApiError(401)` → `queryClient.clear()` + redirect /login |
| 3 | 过期会话点登出是死按钮：`await logout()` 401 抛出 → navigate/queryClient.clear 全不执行，无提示（后端 401 已实测 #6） | `web/src/routes/_app.tsx:79-85` | `try { await logout() } catch {}` 后无条件清理+跳转 |
| 4 | verify-code 无 IP 级限流：仅 per-challenge 5 次 + 1min 重发冷却，爆破预算 ≈5 码/min/邮箱（≈0.7%/天） | `vendor/.../identity/app/service_session.go:159-200`（`tooManyAttempts` 未调用） | VerifyCode 入口加 `tooManyAttempts` 检查（与 SendCode/LoginWithPassword 同一计数器） |
| 5 | send-code 无 per-IP 发送量上限：不同邮箱各 1 封/min 总量无界，prod 邮件成本/骚扰面 | `service_session.go:131-157`（RecordAttempt 只记 verify/login，发送不计数） | 增加按 ip_hash 的发送计数（可记 `RecordAttempt(success=true)` 另立窗口） |
| 6 | `TrustLocalhostAnyPort=true` 硬编码进 prod，违背框架自述"production must enumerate exact origins"（loopback 源页面可免 Origin 校验） | `cmd/orkome-server/main.go:308` vs `vendor/.../webx/csrf.go:25-26` | 改为 `cfg.IsProd() == false` 时才置 true |
| 7 | 登录/验证码服务端错误英文直出（"code mismatch"/"too many attempts"），中文界面语言混杂 | `web/src/routes/login.tsx:59-61` | 按 `ApiError.code/message` 映射 login.* i18n 键，兜底再显示原文 |
| 8 | `login_attempt`/`auth_challenge` 表无界增长：`CleanupAttempts` 全仓无调用方 | `vendor/.../identity/adapters/pgrepo/challenges.go:66`（仅定义）；orkome workers.go 未挂 | orkome workers 加每日清理（两表均按 created_at + 30d） |
| 9 | 会话 token 30 天不轮换：RenewSession 只续期，同一 token 值长期有效（泄露窗口大） | `vendor/.../identity/adapters/pgrepo/sessions.go:91-109` | 续期时 MintToken 换值（UPDATE token_hash + Set-Cookie 新值）；属 ploykit 框架改动 |

### 信息项（不计缺陷）

- **无密码/注册 UI = 设计**（§二，i18n 无键 + 前端零消费 + JIT 旅程自洽）；代价：改密后无找回入口（forgot-password 前端未接）。
- **关闭注册模式的存在性 oracle**：`service_session.go:202-208`，仅 AllowSignup=false 时可区分邮箱是否存在；当前 dev 默认开放，不触发。
- **email_verified 语义差**：验证码登录 JIT `true`，密码注册 `false`（注册流不验证邮箱）——记录待产品裁决。
- **/config 的 `version` 字段恒空**：前端 `ServerConfig.version` 显示 "unknown"（orkome Extra 只回 product）——Cosmetic。
- onboarding 分享码加入 404 / PAT ttl 字段错位：已知 P0-4 / P0-3（inventory 总账），不重复计入。

## 八、测试数据留存

- 建号：`fp01-codetest@orkome.dev`（验证码 JIT）、`fp01-devbypass@orkome.dev`（dev code JIT）、`fp01-regtest@orkome.dev`（注册，最终密码 `Fp01Reg!2026x`）
- 未建号：`fp01-brute@orkome.dev`（challenge 触顶未消费，10min 自过期）；`fp01-nonexistent`（未存在）
- cookie jar：/tmp/fp01-c1~c4.txt、fp01-old.txt；inventory-test 会话已在 #8 登出
- 建议：验收后 `DELETE FROM "user" WHERE email LIKE 'fp01-%'`（级联 session）清场

## 九、修复建议顺序

1. P1-1 后端 `csrf.MaxAge` 一行 + 前端 403 恢复（用户可感知率最高的静默故障）
2. P1-2 注册开关收紧或启动校验（部署前必修，防"以为关了其实开着"）
3. P2-2 / P2-3（同属前端会话兜底，一起改 `_app.tsx` + `query.ts`）
4. P2-1 / P2-6（装配卫生，各一行）
5. P2-4 / P2-5 / P2-8（ploykit 框架加固，随下次 rebase 携带）
6. P2-7 / P2-9（体验与加固，排期）
