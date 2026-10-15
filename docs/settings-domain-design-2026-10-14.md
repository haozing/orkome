# 设置域补全设计（Settings Domain, v1）

> 2026-10-14。前置分析：settings 页差距盘点（本会话）。本方案把 /settings 从「服务器版本 + 会话/工作区 + 通知静音」三个区块，补成账户与工作区管理的正式归宿。
>
> **核心结论：除 MFA 一行装配外，本方案是纯前端工程**——成员/邀请/分享链接/改密/会话/资料/审计/webhook 订阅全部由 ploykit 0.6.1 现成提供且已在 orkome 挂载可用；唯一未装配的是 MFA（`IdentityMountDeps` 未设 `MFA` 字段，服务构造/repo/sealx 全部现成，见 §6.1）。

## 一、产品定义

### 1.1 /settings 的角色定位

settings 是**账户级 + 工作区级管理的统一归宿**，按「我的账户 → 当前工作区 → 服务端」三层组织：

- **我的账户**（跨工作区）：资料、密码、MFA、会话、通知偏好、PAT、语言/主题说明。
- **当前工作区**（随 X-Workspace-Id 切换）：概览（改名）、成员与邀请、分享链接、Webhook 订阅、审计日志。admin 门槛的区块对 member 显示「需要管理员权限」空态。
- **服务端**：版本信息。

### 1.2 与既有页面的边界（不重复建设）

| 能力 | 归宿 | 理由 |
|---|---|---|
| PAT 创建/列表/撤销 | **settings 安全区块**（从 /access 迁入并共享组件） | 「管理我的所有令牌」是账户管理；/access 专注接入引导，引用 settings 链接 |
| 凭证库（sealx 密封 key） | 暂不动（仍只在 agent 表单 credential-field） | 凭证与 agent 强绑定是 v0 语义；集中管理面另行裁决 |
| Webhook 订阅管理 | **settings 工作区区块**（新建） | 修复悬空引用：pack 详情页文案「订阅本体在工作区 Webhook 管理」指向的页面现在不存在 |
| 通知偏好 | 留在 settings（现状不动） | 已落地（迁移 1034 三组静音） |
| 用量 /usage、SSO、角色权限编辑 roles | **本期不做**，页面预留占位 | 见 §7 缓办清单 |

### 1.3 信息架构：单页分区 vs 多子路由

**单页 + 左侧分区锚点导航**（同页滚动定位），不做 /settings/security、/settings/members 子路由。理由：区块总量 9 个但每个都是单屏表单/列表，无嵌套导航诉求；子路由会强迫用户点两次才能到达任何区块；现有页面（/tools、/packs 详情）都是分区制先例。

## 二、页面结构（终态蓝图）

```
/settings
├── 分区锚点导航（sticky 左栏：账户 / 工作区 / 服务端 三组锚点）
├── 【账户】
│   ├── 1. 资料编辑        PATCH /auth/me
│   ├── 2. 密码与安全       change-password + MFA + 会话管理（三卡片合一区块）
│   ├── 3. 通知偏好         （现状组件原样迁入，无改动）
│   └── 4. 访问令牌 PAT     （从 /access 迁入的完整管理面）
├── 【工作区】（admin 门槛区块对 member 显只读或空态）
│   ├── 5. 工作区概览       名称编辑 + slug 只读展示 + plan 展示
│   ├── 6. 成员与邀请       成员列表/改角色/移除 + 邀请管理 + 分享链接
│   ├── 7. Webhook 订阅     （新页面主体，pack 详情页事件区块链接至此）
│   └── 8. 审计日志         列表 + 过滤 + CSV 导出
└── 【服务端】
    └── 9. 版本信息          （现状保留；version 修复见 §6.2）
```

i18n：全部新文案 zh/en 双语齐备（沿用 settings.* / members.* / webhooks.* / audit.* 命名空间）。

## 三、逐区块设计（含 wire 契约冻结）

以下契约全部从 ploykit 0.6.1 vendor 源码提取（file:line 已核对），**并行任务书必须逐字段照抄，实现者不得自创形状**（多 agent 接缝教训 #2）。

### 3.1 资料编辑

- `PATCH /auth/me`，body `{display_name: string, avatar_url: string}`（两字段**都必须传**，严格解码多传/少传字段 → 400；无 language/timezone 字段）。
- 校验：display_name trim 后 1–100 字符；avatar_url 空串合法（**全量覆盖语义：空串=清空头像**），非空须 http(s) 且 ≤2048。
- 响应 200：`{id, email, display_name, avatar_url?, status, is_platform_admin, email_verified, created_at, updated_by?}`（app.User）。
- UI：display_name 输入框 + avatar_url 输入框 + 保存按钮；成功后写回 `me` query 缓存。
- 权限：session 或 PAT 均可。

### 3.2 密码与安全

三张卡片：

**a. 修改密码**（仅 session 用户有意义；PAT 调用 → 403，页面无需特判——settings 本身在 session 内）
- `POST /auth/change-password`，body `{old_password, new_password}`。
- 校验：新密码 10–128 字符且 ≥3/4 类（数字/小写/大写/符号），不得与旧密码相同。
- 响应 200 `{"expires_at": RFC3339}` + **Set-Cookie 新 session；其他设备全部登出**——UI 必须在提交前明确警示「修改密码将退出所有其他设备」。
- 无密码用户（验证码 JIT 建号）：old_password 无从填起，显示「未设置密码」+ 指向设置密码入口（见 d）。

**b. MFA（TOTP）**——依赖 §6.1 的一行装配
- `GET /auth/mfa/status` → `{enabled: bool, pending: bool}`。
- `POST /auth/mfa/enroll` → `{otpauth_uri, secret, issuer}`（secret 一次性明文）；已启用者再 enroll → 409。
- `POST /auth/mfa/enable`，body `{code}`（6 位 TOTP，±1 步漂移）→ `{backup_codes: [10×"XXXXX-XXXXX"]}`（**一次性明文，必须立即展示+复制，离开页面不可再取**）。
- `POST /auth/mfa/disable`（无 body）→ `{disabled: true}`；**吊销全部 session**。
- UI：未启用 → 「启用」按钮 → 展示 otpauth_uri 二维码（前端 qrcode 库）+ secret 手输回退 → 输码确认 → 备份码一次性展示 Dialog；已启用 → 「关闭」按钮 + 警示全 session 吊销。

**c. 会话管理**
- `GET /auth/sessions` → **裸数组**（无信封）`[{id, ip_hash, user_agent, last_seen_at, expires_at, created_at, current: bool, impersonated_by?}]`。
- `DELETE /auth/sessions/{id}` → 204（不存在/非本人 → 404）；`DELETE /auth/sessions` → 200 + 清 cookie（全部登出，含当前——**UI 放最后并二次确认**）。
- UI：当前会话标记「本设备」置顶，逐行「退出」按钮。

**d. 设置密码（无密码账户）**——ploykit 无专用端点，v0 不做服务端开发，此卡片仅当 old_password 校验失败路径展示引导文案（记入 §7 缓办）。

### 3.3 访问令牌（PAT）

- 从 /access 迁入管理面（`api/identity.ts` 的 listTokens/createToken/revokeToken 原样复用）：
  - `GET /api/tokens` → **裸数组** `[{id, name, prefix, expires_at?, last_used_at?, created_at, scope?}]`（scope 含 workspace_ids/permissions，v0 仅展示不编辑）。
  - `POST /api/tokens` body `{name, ttl_hours}` → 201 `{pat: {...}, token}`（token 明文仅此一次）。
  - `DELETE /api/tokens/{id}` → 204。
- UI：列表（前缀/最后使用/过期）+ 创建 Dialog（name + TTL 选择）+ 一次性 token 展示 + 撤销；/access 页改为链接到 `/settings#tokens` 并保留最小内联创建（接入语境下不想跳页）。
- 权限：**RequireHuman**（PAT 调用 → 403），页面在 session 内无需特判。

### 3.4 工作区概览

- `PATCH /api/workspaces/{id}` body `{name}`（**只能改 name，slug 不可改**）→ 200 `{id, slug, name, plan_code, created_at}`。
- UI：name 编辑（admin）+ slug/plan_code/created_at 只读行；成功后同步 me 缓存与 WorkspaceSwitcher。

### 3.5 成员与邀请

**成员**（member 可读）：
- `GET /api/workspaces/{id}/members` → 信封 `{items: [{user_id, email, display_name, role, created_at}], total}`。
- `PATCH .../members/{userId}` body `{role}`，**role 仅 admin|member**（不可指派 owner；不能改自己；最后一个 owner 不可降级 → 409）→ 200 `{"status":"ok"}`。
- `DELETE .../members/{userId}` → **200（非 204）**；不能移除自己（403 "use leave instead"）；可移除非最后 owner。

**邀请**（admin 写，member 可读）：
- `POST .../invitations` body `{email, role}`（role 仅 admin|member）→ 201 `{id, workspace_id, email, role, status, expires_at, created_at}`；已 pending → 409；已是成员 → 409。**TTL 固定 7 天，不随请求传**。
- `GET .../invitations` → `{items, total}`，**wire 上 status 恒为 "pending"**（accepted/declined/revoked/过期被服务端过滤，前端不做状态列）。
- `DELETE .../invitations/{invId}` → **204**；非 pending → 404。

**分享链接**（admin 写，member 可读）：
- `POST .../share-links` body `{role, max_uses, ttl_hours}`（role 仅 admin|member；**max_uses 必填 1–1000；ttl_hours 必填 1–720**，0 不合法）→ 201 `{link: {id, code_prefix, role, max_uses, uses, expires_at, created_at}, code}`——**完整 code 仅此一次**（24 位 hex）。
- `GET .../share-links` → `{items, total}`（仅未撤销未过期）。
- `DELETE .../share-links/{linkId}` → **204**。
- UI：创建表单（角色下拉 + 次数 + 时长）→ 一次性 code 展示 Dialog（复制）；列表展示 code_prefix/uses/max_uses/过期时间/撤销。

**我的邀请（收件方视角）**：`GET /api/invitations/mine` → **裸数组**（Invitation + workspace_name/workspace_slug）；accept → 200 Workspace（刷新 me 缓存）；decline → 200 `{"status":"ok"}`。入口放 settings 账户区顶部条（有待处理邀请时显示）+ 铃铛通知已有直达链接，v0 不做独立页。

### 3.6 Webhook 订阅（工作区）

pack 详情页事件区块的「订阅本体」从此有了家。复用既有 `web/src/api/webhooks.ts`（ping/rotate/暂停恢复/重放/deliveries/events 已封装），**补三个未接的**：
- `GET /api/workspaces/{id}/webhooks/subscriptions` → `{items: [{id, workspace_id, event_types, url, description, is_active, created_at}]}`（**不含 secret**，nil 归一 []）。
- `POST 同路径` body `{url, events: [string], description}`（url+events 必填；**无 secret 入参，服务端生成**）→ 201 `{subscription, secret}`（`whk_`+64hex **仅此一次**）。
- `DELETE .../subscriptions/{subscriptionId}` → 204。
- UI：订阅列表（事件数/URL/状态/创建时间）+ 行操作（Ping/暂停恢复/轮换密钥[一次性明文 Dialog]/删除）+ 创建 Dialog（URL + 从 `GET .../webhooks/events` 目录多选事件，复用 pack 安装向导的事件多选组件）+ 底部投递记录（复用 pack 详情的 deliveries 组件，按订阅过滤下拉）。
- 权限：读=member（webhooks:read），写=admin（webhooks:manage）。
- pack 详情页事件区块加「在设置中管理订阅」链接（替换现有文案尾部）。

### 3.7 审计日志（工作区）

- `GET /api/audit` query：`actor_id/action/resource_type/from/to(RFC3339)/limit(默认100,上限500)/offset` → `{items: [{id, workspace_id, actor_type("user"|"pat"|"system"), actor_id?, actor_snapshot{email,role?,workspace_id?}, action, resource_type, resource_id?, metadata, created_at}], total}`，按 created_at DESC。
- `GET /api/audit/export.csv` → 文件下载（同过滤参数；CSV 有公式注入防护）。
- UI：过滤栏（action/resource_type 下拉 + 时间范围）+ 分页（limit/offset）+ 表格 + 「导出 CSV」按钮（`window.open` 带 query 直下）。metadata 展开行按需 JSON 折叠。
- 权限：owner/admin（member 无 audit:read → 403，区块显示权限空态）。

### 3.8 版本信息（服务端）

现状保留，但修复 version 恒显 unknown：见 §6.2。

## 四、前端工程结构

### 4.1 API 模块拆分

```
web/src/api/
├── identity.ts        # +updateProfile/getMfaStatus/mfaEnroll/mfaEnable/mfaDisable
│                      #  +listSessions/revokeSession/revokeAllSessions
│                      #  +listMyInvitations/acceptInvitation/declineInvitation
├── workspace-admin.ts # 新文件：renameWorkspace/listInvitations/createInvitation/
│                      #   revokeInvitation/listShareLinks/createShareLink/revokeShareLink
│                      #   updateMemberRole/removeMember（listMembers 留在 identity.ts）
└── webhooks.ts        # +listSubscriptions/createSubscription/deleteSubscription
└── audit.ts           # 新文件：listAudit(query) / auditCsvUrl(query)
```

### 4.2 组件拆分（settings 页目录化）

```
web/src/routes/_app.settings.tsx        # 壳：锚点导航 + 分区组装（不再单文件 460 行）
web/src/components/settings/
├── profile-section.tsx
├── security-section.tsx                # 密码+MFA+会话三卡片
├── mfa-card.tsx                        # 含二维码（qrcode.react 或同等轻量库）
├── sessions-card.tsx
├── tokens-section.tsx                  # 从 access 页抽出的共享组件
├── invitations-banner.tsx              # 我的邀请条
├── workspace-overview-section.tsx
├── members-section.tsx                 # 成员+邀请+分享链接三段
├── share-links-card.tsx
├── webhook-subscriptions-section.tsx   # +投递记录复用
└── audit-section.tsx
```

### 4.3 权限可见性

- 区块自身不做路由级守卫（settings 页本身在 session 内）。
- 工作区区块按 me.workspaces 里的 `role` 条件渲染写操作；member 看到的写区块显示「需要管理员权限」+ 只读部分（成员列表可读）。
- 服务端按 403 ApiError 兜底（审计对 member 是权限拒绝）。

### 4.4 状态与缓存

- 既有 query key 约定延续：`['tokens']`、`['workspace', id, 'members']`、`['notification-preferences']`；新增 `['sessions']`、`['mfa-status']`、`['ws-invitations', id]`、`['ws-share-links', id]`、`['ws-subscriptions', id]`、`['audit', id, filters]`。
- 一次性明文（PAT token / webhook secret / share-link code / MFA backup codes）**只存组件局部 state**，不进 query 缓存不进 localStorage。

## 五、导航与入口调整

1. 侧栏 BOTTOM_NAV 不动（/settings 已在）。
2. /access 页 PAT 区块改为「管理入口在设置」链接 + 保留最小内联创建。
3. pack 详情页事件区块尾部加「在设置中管理订阅 →」链接（指向 `/settings#webhooks`）。
4. 顶栏 ThemeToggle/LanguageToggle **不动**（即时切换的合理位置是壳层；settings 内不放重复开关——若未来做「偏好随账号走」再迁）。

## 六、后端改动（仅两处，都很小）

### 6.1 MFA 装配（唯一必做）

`cmd/orkome-server/wiring_identity.go`：
```go
// IdentityMountDeps 补：
secrets, err := sealx.NewSecrets(sealx.DeriveKey(cfg.JWTSecret, "mfa"))  // 或复用独立 env key
mfa := fwapp.NewMFAService(repo, sessions, secrets, "orkome", time.Now)
// Deps 增：MFA: mfa（MFAChangeGuard 可选：webx.RequireRecentAuth(15m) 首期不配=无 step-up）
```
- `repo`（identitypg.Repo）已实现全部 MFARepo 方法（mfa.go:17-53）；迁移 046 已随 ploykit 0.6.1 落库；seal key 缺失时 enroll 返回 500（fail-closed，符合既定纪律）。
- 注意 MFA seal key 与 `ENGINE_CREDENTIAL_MASTER_KEY` 分开派生（DeriveKey 域分离），不共用原始 JWT_SECRET。

### 6.2 /config 补 version（小改进，随波次捎带）

`cmd/orkome-server/main.go:362` 的 ConfigDeps.Extra 加 `"version": buildVersion()`（ldflags 注入，缺省 dev 标识）。FP-16 P2-1 的「恒显 unknown」随之修复。

### 6.3 明确不做

- 不新增产品侧 Go 端点（零产品后端开发）。
- 不做角色权限编辑 UI（roles 端点 owner 专属 + step-up，需求未成熟）。
- 不做 SSO/OIDC 联邦配置 UI（FedAdminGuard 已有，页面需求出现再做）。
- 不做账号注销/邮箱验证 UI（端点已活但产品语义需单独裁决——删除账号对多 workspace 的影响面）。
- 不做用量 /usage 呈现（沿「缓办」裁决）。

## 七、实施切分（两波，可并行）

### W1（后端一行 + 账户侧前端）
- 后端：MFA 装配（§6.1）+ /config version（§6.2）。
- 前端：settings 壳改造（锚点导航）+ 资料编辑 + 密码与安全（含 MFA 完整流程）+ 会话管理 + PAT 迁入 + 我的邀请条 + i18n 全量。
- 验收：真机 IAB 过一遍——改资料回显、改密码后旧 session 失效、MFA enroll→enable→disable 全程（备份码一次性展示）、会话列表踢一个旧会话、PAT 创建/撤销。

### W2（工作区侧前端）
- 工作区概览改名 + 成员与邀请 + 分享链接 + Webhook 订阅（三补全）+ 审计日志 + pack 详情页链接替换 + /access PAT 区块改造。
- 验收：admin/member 双角色走查（member 见权限空态）；邀请全链（创建→邮箱（devlog）→mine 列表→accept 后 me 刷新）；分享链接 code 一次性展示；webhook 订阅创建后 pack 页 Ping 通；审计过滤+CSV 下载。

两波文件不相交（W1: identity.ts + settings/ 账户侧组件；W2: workspace-admin.ts + webhooks.ts + audit.ts + settings/ 工作区侧组件），接口形状以 §3 契约表为准，无跨波依赖。

## 八、风险与已知边界

1. **严格解码**：所有 PATCH/POST body 字段必须精确（多传字段 → 400）。任务书写死字段表。
2. **一次性明文四件**（PAT token/webhook secret/share code/backup codes）：离开确认 Dialog 即不可再取，UI 文案必须警示。
3. **吊销全部 session 的三端点**（change-password/mfa-disable/DELETE all sessions）：提交前二次确认 + 明确后果文案。
4. **裸数组 vs 信封混布**：sessions/tokens/invitations-mine 是裸数组，members/invitations/share-links/subscriptions/audit 是信封——API 模块层逐个归一（`?? []`），组件不感知。
5. **member 403 兜底**：audit/subscriptions 写操作对 member 403，区块级 catch 显空态而非整页崩。
6. **MFA seal key 变更=已启用用户全部锁死在外**（sealed secret 解不开）——与 ENGINE_CREDENTIAL_MASTER_KEY 同级运维警示，写进部署文档。
7. **share-link 兑换**：onboarding 已有 redeem 方向，本方案只做创建侧，不改 onboarding。
