# FP-17 onboarding 完整旅程 审计报告（新用户首登 → 建工作区 → 进入产品）

> 日期：2026-10-12 · 方法：七步审计法（全链梳理→契约逐字段对齐→curl 实测→代码质量→后端正确性→测试覆盖→P0/P1/P2 清单）
> 环境：后端 :8080（迁移 1036）+ 前端 :5173；测试账号 `fp17-fresh@orkome.dev`（JIT 建号）、`fp17-fresh2@orkome.dev`（分享码加入），dev 验证码 888888
> 前置引用：FP-01 定论"验证码 JIT 建号是设计"（`docs/audit-fp01-auth-2026-10-12.md`）；FP-02 已审 join 断链 P0 与 slug 校验 P1（`docs/audit-fp02-workspace-2026-10-12.md`、`docs/feature-audit-batch1-2026-10-12.md` 根因 C）——本册只补旅程语境，不重复定罪

---

## 〇、结论速览

**P0×0 / P1×1（另有引用 FP-02 的 P1×1）/ P2×6。**

旅程主干（JIT 建号→零工作区分流→建区→落地→登出重登回放）**后端侧全链实测通过**，建区是原子事务、首个工作区默认 owner 角色正确、状态目录惰性播种健全；壳层"僵尸 state"高发区经实测+静态双查**未发现空白类僵尸**（`setCurrent`→refetch→navigate 链条完整，`_app` beforeLoad 有 workspaces[0] 自愈兜底）。最大问题在前端 onboarding 页的**旅程死路**：零工作区用户被 `/login`（authed 重定向）与 `/onboarding`（零工作区重定向）**双向弹弓困死**，页面自身无登出/换账号出口——邮箱误输即触发。

## 一、全链梳理（旅程九站）

```
① /                          index.tsx:3-5 裸 redirect → /work-items
② /work-items (_app)         _app.tsx:32-49 beforeLoad 三态分流：
     401 ────────────────→ /login
     workspaces.length===0 → /onboarding          ← 新用户首站
     else                  → store 自愈 setCurrent(workspaces[0].id)（:45-48）
③ /login                     login.tsx:13-20 beforeLoad：authed 用户反向弹回 /work-items
     send-code → verify-code（dev 888888）→ JIT 建号（FP-01 已定性为设计）
     verify 成功 → navigate /work-items（login.tsx:55）→ ②再次分流 → /onboarding
④ /onboarding                onboarding.tsx —— 仅两条路：
     创建卡（name+slug，:85-118）：suggestSlug 首个 name blur 自动填（:97-103）
     加入卡（分享码，:126-142）：joinByShareCode →【FP-02 断链，见 §四】
     ※ 没有"跳过"——这是 _app 工作区 fence（FP-02"进不去"侧）的必然设计，非缺陷
⑤ 建区 POST /api/workspaces  ploykit workspace service Create（service.go:80-126）
     RunInTx{ workspace 行 + member(role=owner) 行 }（pgrepo/workspaces.go:30-44）——原子 ✓
     slug 归一化→regex→保留字→重名 409；MaxPerUser=-1 不限（wiring_identity.go:94）
⑥ onSuccess（onboarding.tsx:47-51）
     setCurrent(ws.id) → await refetchQueries(['me']) → navigate /work-items
⑦ /work-items (_app) 落地    beforeLoad 复跑：me 已刷新含新区 → 放行
     页面全家桶查询：statuses/projects/members/agents/work-items + 壳层 ui-entries/notifications
⑧ 分享码加入（另一新用户）   POST /api/share-links/redeem → 200 Workspace（无 workspace_id/role）
⑨ 登出→重登回放              signOut（_app.tsx:79-85）：logout + store 置空 + queryClient.clear + ws close
     再 verify-code → 会话重建 → workspaces 原样保留 ✓
```

**答案备查（任务问句逐条）**
- 注册后落哪：verify-code JIT 建号后 `/work-items`，被 `_app.tsx:42-44` 弹到 `/onboarding` ✓
- onboarding 三条路：只有两条（创建/加入），无跳过——由工作区 fence 决定，合理
- slug 生成辅助：**有**（suggestSlug，name blur 且 slug 为空时自动填）
- 建完怎么进：setCurrent→refetch me→navigate，**壳层不空白**（§三 T8-T12 全家桶 2xx）
- 重复 onboarding：有工作区用户手动访问**不重定向、可重复建区**（无 beforeLoad 守卫，inventory-pages-live:203 已录现象，本册定性）
- 旅程回放：登出清 store+缓存，重登 memberships 完整回放 ✓
- i18n/空态：`onboarding.*` 11 键 zh/en 齐全（zh.ts:572-585 / en.ts:607-620）；落地空态兜 null 全对（除 members，根因 A）

## 二、契约对齐（涉及端点逐字段）

| 端点 | 后端实现 | 响应形状 | 前端消费 | 判定 |
|---|---|---|---|---|
| POST /auth/send-code | ploykit identity routes.go | `{status:"sent"}` | login.tsx:39-44 | ✓ |
| POST /auth/verify-code | 同上（JIT 建号+会话 cookie） | `{user,expires_at}` | login.tsx:45-52 | ✓ |
| POST /auth/register | handlers.go:106-123，**要求强密码**（实测 400 "password too weak"） | `{user,expires_at}` | **前端零调用**（FP-01 已定性无注册页是设计；API-only 面） | ✓（记录） |
| GET /auth/me + GET /api/workspaces | identity me / ploykit listMine | User / `WorkspaceMembership[]{id,slug,name,plan_code,created_at,role}` | meQueryOptions 拼装 `{user,workspaces}`（identity.ts:113-125） | ✓（role 字段在，自愈与 switcher 可用） |
| POST /api/workspaces | ploykit create（handlers.go:25-49） | 201 `Workspace{id,slug,name,plan_code,created_at}` | createWorkspace→`ws.id`（onboarding.tsx:48） | ✓ |
| POST /api/share-links/redeem | redeemShareLink（handlers.go:242-261） | 200 **Workspace**（无 workspace_id/role） | 前端打 **/api/share-links/join** 且取 `member.workspace_id`（identity.ts:60 / onboarding.tsx:64） | ✗ 双重断链＝FP-02 根因 C（引用），本册实测坐实形状 |
| GET /api/workspaces/{id}/members | listMembers（handlers.go:52-60） | **`{items,total}` envelope** | listMembers 按 `Member[]` 返回（identity.ts:71-74，`?? []` 只兜 null 不解包） | ✗ batch1 根因 A（引用）——新用户首屏指派筛选/下拉静默缺失 |
| GET /api/work-item-statuses | EnsureStatusSeed 惰性播种（service_catalog.go:37-39，ON CONFLICT DO NOTHING 并发安全） | 7 内建状态 | listStatuses | ✓ |
| GET /api/projects /api/agents /api/work-items /api/ui-entries /api/notifications/unread-count | 各域 | null/`{agents:[]}`/`{items:null}`/`{items:[]}`/`{count:0}` | 四处封装全部正确兜 null（work.ts:236-238、orchestration.ts:120-125、work.ts:56-59、pack-entries.tsx:19-24） | ✓ |

slug 规则契约漂移见缺陷 3。

## 三、curl 实测记录（全部复现于 2026-10-12）

**主干（fp17-fresh@orkome.dev，JIT）**
| # | 操作 | 结果 |
|---|---|---|
| T1/T2 | POST /auth/register 无密码 | 400 `password too weak (need 10+ chars, 3 of 4 kinds)`——register 与前端旅程无关 |
| T3/T4 | send-code + verify-code 888888 | 200；JIT 建号 `display_name:"fp17-fresh"`（邮箱前缀），`email_verified:true` |
| T5 | 零工作区态 | `/auth/me` 200；`/api/workspaces` → `[]` |
| T6 | 建区 `{name:"FP17 Fresh WS",slug:"fp17-fresh-ws"}` | **201** `{id:7677bf0f…,slug,name,plan_code:"free",created_at}` |
| T7 | 再查 workspaces | 1 项，`role:"owner"` ✓ |
| T8-T12 | 落地全家桶（statuses/projects/members/agents/work-items，带 X-Workspace-Id） | statuses **7 内建惰性播种** ✓；projects `null`→前端兜 []；members `{items:[…owner],total:1}` envelope；agents `{agents:[]}`；work-items `{items:null,next_cursor:""}`——**全 2xx，无 500，落地不空白** |
| T24/T25 | fresh2 redeem 后 | fresh2 memberships=`[{…,role:"member"}]`；owner 侧成员列表 2 人 ✓ |
| T26-T29 | 登出→me 401→重登 888888 | `{"status":"ok"}`→`E_UNAUTHENTICATED`→200 重登；workspaces 3 区原样回放 ✓ |

**边界态**
| # | 操作 | 结果 |
|---|---|---|
| T13 | slug 重复 fp17-fresh-ws | 409 `E_CONFLICT "slug already taken"` |
| T14/T19 | 保留字 api(3字命中长度)/mail(4字) | 400 `slug is reserved`（保留字表 rules.go:73-77） |
| T15/T20 | 2 位 slug；**尾连字符 `fp17-trail-`** | 400；后者**前端 zod 放行、后端拒绝**（缺陷 3 实锤） |
| T16 | 大写 slug `FP17-Mixed` | 201 自动归一 `fp17-mixed`（normalizeSlug service.go:76-78） |
| T17 | 空 name | 400 `name required` |
| T18 | 同账号第二/第三工作区 | 201×2（MaxPerUser=-1 不限）→ 有工作区用户可从 /onboarding 无限重复建区 |
| T23 | fresh2 redeem 正确端点 | 200 **Workspace 形状**（无 workspace_id）——前端取 `member.workspace_id` 必 undefined |
| T30-T31 | 未登录建区→403 CSRF（链序在 auth 前）；零工作区用户无 header 打 scoped 端点→400 `X-Workspace-Id header required` | 行为正确 |
| T33/T35 | 新区 unread-count `{count:0}`、ui-entries `{items:[]}` | 壳层组件新区全绿 |
| T36-T38 | 清理：删两个空区 204×2；**删含 1 名成员的 fp17-fresh-ws → 500 `E_INTERNAL`** | FP-02 删除 500（52 FK 零级联）在"仅多一个 member 行"即复现——**fp17-fresh-ws 留作第二活体证据**（与 fp02-ws-a 同因） |

## 四、缺陷清单

### P1（本功能点自有 ×1）

**P1-1 零工作区用户被登录页/引导页双向弹弓困死，onboarding 页无登出/换账号出口**
- 链条：`login.tsx:13-20` beforeLoad 对 authed 用户 `throw redirect /work-items` → `_app.tsx:42-44` 对 0 workspaces `throw redirect /onboarding` → `onboarding.tsx:12-14` **无 beforeLoad、无 logout、无任何回登录链接**（全文无 logout import，logout 按钮只存在于 `_app` 壳，而壳恰是零工作区用户进不去的）。
- 触发面不小：验证码 JIT 设计下**任何邮箱误输都会即时造号**（本审计的 fp17-fresh 就是这么来的）；误输者被钉死在 onboarding，唯一出路是手清 cookie。
- 顺带覆盖：①未登录直达 /onboarding 无守卫，页面照常渲染、提交时报裸 401（`E_UNAUTHENTICATED authentication required`）；②有工作区用户手动访问 /onboarding 不重定向、可无限重复建区（T18，inventory-pages-live:203 已录现象）。
- 修复（约 15 行）：`onboarding.tsx` 加 beforeLoad（无会话→redirect /login）+ 页面角落加"使用其他账号登录"次级按钮（调 `logout()` 后 navigate /login）；可选：有工作区访问时顶部给"返回工作台"链接。

### P1（引用，不计入本册口径）

**P1-R1 onboarding 邀请码加入双重断链**＝batch1 根因 C / FP-02 P0（前端打 `/api/share-links/join` 404 + redeem 返回 Workspace 无 `workspace_id`）。本册旅程语境补充两点实测： redeem 端点本身旅程级全通（T21-T25：建链→redeem→member 角色→双向成员可见），修前端两处即可通； 修好后零工作区用户的 `setCurrent(undefined)` 会被 `_app.tsx:45-48` beforeLoad 自愈兜成 `workspaces[0]`（恰好就是刚加入的唯一区），**多工作区用户仍会切错区**——FP-02 已录，修 `res.id` 一处同时治愈。

### P2 ×6

| # | 缺陷 | 证据 | 修复建议 |
|---|---|---|---|
| 1 | slug 正则前后端漂移：前端放行首/尾连字符，后端 400 | `onboarding.tsx:15` `^[a-z0-9-]{4,40}$` vs ploykit `rules.go:71` `^[a-z0-9][a-z0-9-]{2,38}[a-z0-9]$`；T20 实测 | 前端正则对齐后端（一处） |
| 2 | 后端校验消息裸英文直出中文界面 | `onboarding.tsx:71-76` 直接渲染 `err.message`（"slug already taken"/"slug is reserved"，T13/T19）；zh.ts 无对应映射 | mutationError 按 ApiError code 映射 `onboarding.*` i18n 键 |
| 3 | suggestSlug 对中文/非拉丁名产出**空串** | `onboarding.tsx:29-35`（`[^a-z0-9]+→'-'` 再剥首尾）——纯中文名（本产品 zh 一等公民）得 ""，提交才报错 | 空/过短时 fallback `ws-<随机6位>`，或输入即联动而非仅 blur |
| 4 | slug 冲突无消歧：409 后仅裸错误，用户手改全名 | T13；无实时可用性预检、无 `-2` 后缀建议 | 409 时自动 `suggestSlug+"-2"` 回填并聚焦 |
| 5 | 被移出唯一工作区后的 30s 幽灵窗口 + 无被动弹回 | `identity.ts:124` me staleTime=30s；`_app.tsx:42` 分流只在导航时跑，壳内无 me 订阅。被移出后≤30s+一次导航内留在幽灵区，期间请求 403/404 | 壳层 `useQuery(me)` + useEffect：`workspaces.length===0 → navigate('/onboarding')`（顺带治多标签） |
| 6 | 前端测试按错误契约 mock 的实锤标本（batch1 §四.1 模式） | `web/test/api/identity.test.ts:24-37` mock 了**不存在的** `/api/share-links/join` 与后端**从不返回**的 Member 形状（含 workspace_id）——测试绿、契约错 | 修 FP-02 时同步改为 mock `/redeem` + 真实 Workspace 抓样 |

（非缺陷备忘：`GET /api/workspaces/{id}` DELETE 对空区 204 正常、含成员即 500＝FP-02 已录 P1，fp17-fresh-ws 留证；`/auth/register` 强密码要求与前端零调用一致，无旅程影响。）

## 五、后端正确性专项

1. **首个工作区默认角色**：`CreateWorkspaceWithOwnerTx`（pgrepo/workspaces.go:30-44）单事务内建 workspace + `member(role='owner')`，**原子 ✓**；T6/T7 实测 owner。
2. **默认设置初始化**：orkome **未接 `AfterCreate` 钩子**（main.go:222-225 只接 OnMemberRemoved/OnTeardown）——刻意为之：状态目录走 `EnsureStatusSeed` 惰性播种（`INSERT … ON CONFLICT DO NOTHING`，并发首载安全，repo.go:32-35），T8 实测 7 内建状态即到即用。work_item_counter/labels/projects 无预置需求，空态兜底已验。
3. **JIT 建号与成员关系原子性**：建号（identity VerifyCode）与建区/加入（workspace RunInTx）是两次独立事务，但分属两次用户动作，无中间态暴露窗口；redeem 的成员插入 + AfterMemberJoin 同事务（service.go:504-531，orkome 未接钩子走 else 分支单写）✓。
4. **配额**：`MaxPerUser:-1`（wiring_identity.go:94）＝不限；零值安全注释明确"不得锁死用户第一个工作区"（service.go:92-97）——onboarding 主链无配额死锁风险 ✓。

## 六、测试覆盖评估

**已有**：
- e2e `scenario_s1_test.go`：建区→DB 断言 owner→邀请 bob→accept→成员列表→越权拒绝——**email 邀请分支后端旅程覆盖良好**（:24-60）。
- e2e `registry_test.go:63`：`POST /api/share-links/redeem` 仅路由走查级（authSelf 一发）；`scenario_s1:75` 只测未登录不可列 share-links。
- 前端 `identity.test.ts:15-23` createWorkspace 契约测试（这条是对的）。

**缺口（按价值排序）**：
1. **分享码 redeem 无端到端场景**：max_uses 耗尽、TTL 过期、重复 redeem、redeem 后多区用户当前区语义——全部零覆盖（正是 FP-02 P0 漏网原因）。
2. **"新用户首登→建区→落地"全旅程冒烟**：后端/前端均无（s1 从 invitation 侧进，不从 onboarding UI 侧进）。
3. onboarding 组件零测试：双表单校验、suggestSlug、mutationError 渲染。
4. identity.test.ts 反向锁死错误契约（缺陷 6）——修复时必须同步真实抓样锁定（batch1 §四.1 的教训）。

## 七、修复路线（本功能点增量，FP-01/02 项见原册）

1. `onboarding.tsx`：beforeLoad（unauth→/login）+ "使用其他账号登录"按钮（P1-1，~15 行）
2. `identity.ts:60` 改 `/redeem` + `onboarding.tsx:64` 取 `res.id`（FP-02 根因 C，~3 行）
3. `onboarding.tsx:15` slug 正则对齐后端（P2-1，1 行）
4. mutationError 按 code 走 i18n（P2-2）+ suggestSlug 中文 fallback（P2-3）
5. 壳层 me 订阅被动弹回（P2-5）
6. e2e 补 redeem 场景 + 全旅程冒烟（§六）

## 测试数据留存

- 已清理：`fp17-mixed`、`fp17-second-ws`（空区删除 204）
- 活体证据（删除 500 复现体，FP-02 同因）：工作区 `fp17-fresh-ws`（id `7677bf0f-47df-46a3-933c-01d05a14e1eb`，含 owner `fp17-fresh@orkome.dev` + member `fp17-fresh2@orkome.dev`）——修复 FK 级联后删除即可清场
