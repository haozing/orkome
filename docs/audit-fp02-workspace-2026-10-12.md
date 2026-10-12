# FP-02 功能点审计 · 工作区生命周期与切换器（创建/切换/成员视角/onboarding）

- 日期：2026-10-12
- 审计人：FP-02 audit agent（七步审计法：全链梳理 → 契约对齐 → curl 实测 → 代码质量 → 后端正确性 → 测试覆盖 → 缺陷清单）
- 范围：仅工作区生命周期与切换器。代码只读，无改动；测试数据一律 `fp02` 前缀。
- 环境：后端 http://localhost:8080（dev）；测试账号 inventory-test@orkome.dev（owner of inventory-ws）+ 实测中现建的 fp02-second@orkome.dev。
- 关联文档：docs/inventory-2026-10-12.md（P0-1/P0-2 已知问题，本文实证并扩面）、docs/adr/0005-workspace-resolution-id-header-only.md

---

## 0. 一句话结论

工作区**运行时 fence 是硬的**（越权矩阵 11/11 通过、slug 并发唯一性完美），但**生命周期两端都断了**：入口 onboarding 的"邀请码加入"前端打在不存在的端点上（404，且响应形状再错一层），出口"删除工作区"对任何用过的工作区必然 500（52 处 FK 零级联）；加上切换器 GroupLabel 崩溃与 members envelope 连坐 7 处，工作区功能"进不去、出不来、切不动、成员不可见"。

**计数：P0 ×3 · P1 ×3 · P2 ×5**

---

## 1. 全链梳理

### 1.1 前端链路

```
workspace-switcher.tsx (全局 header, _app.tsx:91)
  └─ meQueryOptions (api/identity.ts:113) = GET /auth/me + GET /api/workspaces 拼装
  └─ switchWorkspace (services/workspace-switch.ts)
       ├─ useWorkspaceStore.setCurrent (stores/workspace.ts, zustand persist → localStorage 'orkome.workspace')
       ├─ queryClient.removeQueries(保留 me/config)   ← 切换缓存失效
       └─ wsClient.reconnect() (realtime/client.ts:4-19, url 函数实时读 store)

onboarding.tsx (路由 /onboarding)
  ├─ createWorkspace → POST /api/workspaces (identity.ts:56)
  └─ joinByShareCode → POST /api/share-links/join (identity.ts:59)  ← 【P0-2 端点不存在】

X-Workspace-Id 附加：main.tsx:12 setWorkspaceIdProvider(() => store.currentId)
  → @ploykit/client api.ts:仅 path.startsWith('/api') 的请求附加
```

### 1.2 后端链路（vendor/github.com/haozing/ploykit/workspace/）

```
adapters/http/routes.go:53 Mount
  ├─ GET/POST /api/workspaces            （仅认证，无 workspace 上下文）
  ├─ PATCH/DELETE /api/workspaces/{id}   （workspaceCtx: path 参数 → GetMember → 非成员 404）
  └─ /api/workspaces/{id}/... 子路由     （同 workspaceCtx 包裹：members/roles/transfer/leave/invitations/share-links）
adapters/http/handlers.go → app/service.go → adapters/pgrepo/
cmd/orkome-server: wiring_identity.go:94 (WorkspaceConfig{MaxPerUser: -1} = 无限)
  main.go:223-225 WorkspaceHooks{OnMemberRemoved: _oldRevocation, OnTeardown: _oldTeardown}

Header 通道（其他域）：WorkspaceHeaderCtx (handlers.go:138-170)
  缺头 400 → scope 403 → 非成员 403 （ADR 0005）
```

### 1.3 当前工作区状态管理（审计项：zustand/localStorage/URL？刷新后保持？）

- **zustand + persist → localStorage `orkome.workspace`**（stores/workspace.ts:7-15）。URL 不承载工作区身份（ADR 0005：拒绝 slug 进 URL）。
- 刷新保持：persist 同步 rehydrate；`_app.tsx:45-48` beforeLoad 校验 `currentId ∈ me.workspaces`，非法/缺失回落 `workspaces[0]`——**localStorage 伪造 id、被移出工作区、工作区被删三种场景均自愈**。实测代码路径推演成立。
- 伪 id 自愈后首个请求仍可能带旧头发一次 403（beforeLoad 校验发生在请求前，实际影响仅限极端竞态）。

---

## 2. 契约对齐（逐字段）

### 2.1 创建工作区 POST /api/workspaces

| 字段 | 后端（实测 201 响应） | 前端 identity.ts `Workspace` | 判定 |
|---|---|---|---|
| id/slug/name/plan_code/created_at | ✅ | ✅ | 一致 |
| role | 201 响应**无**（ListMine 才带，inventory 实测 list 响应含 `role:"owner"`） | `role?: string` 可选 | 一致 |

slug 规则对齐（重点缺陷，见 P1-3）：
- 后端 domain/rules.go:70 `^[a-z0-9][a-z0-9-]{2,38}[a-z0-9]$` + 归一化（lowercase+trim, service.go:76-78）+ 保留字表（rules.go:74-77：api/www/app/admin/mail/static/assets/auth/support/billing）
- 前端 onboarding.tsx:15 `/^[a-z0-9-]{4,40}$/` —— **允许首尾 `-`，不查保留字**
- i18n zh.ts:575 文案「小写字母/数字/短横线，4-40 位」——**未提首尾限制**

### 2.2 成员 GET /api/workspaces/{id}/members（重点）

- 后端真实形状（curl 抓样）：`{"items":[{"user_id","email","display_name","role","created_at"}],"total":2}` —— `app.Page[T]`（model.go:52-55）
- 前端 identity.ts:71-74 `api.get<Member[] | null>` 当裸数组；且 `Member` 接口声明的 `id`、`workspace_id` 字段**后端不存在**（model.go:18-24）
- 波及面全量清点（已知"五处连坐"，实为 **7 个消费方 = 5 处崩溃 + 2 处静默**）：

| # | 位置 | 用法 | 后果 |
|---|---|---|---|
| 1 | web/src/components/work-item/work-item-list.tsx:46 | `members.data?.find(...)` | **TypeError 崩溃** |
| 2 | web/src/components/work-item/info-panel.tsx:28 | `.find(...)` | **崩溃**（工单详情页整页） |
| 3 | web/src/components/work-item/property-rows.tsx:43 | `.find(...)` | **崩溃** |
| 4 | web/src/components/work-item/comment-thread.tsx:45 | `(members.data ?? []).map(...)` | **崩溃**（对象无 .map） |
| 5 | web/src/routes/_app.agents.$agentId.tsx:1038, 1063 | `.find` + `(?? []).filter` | **崩溃**（agent 权限 tab） |
| 6 | web/src/routes/_app.work-items.index.tsx:147,149 | `.length > 0` 守卫后 `.map` | **静默空**：`undefined > 0` 恒 false，指派筛选条永不渲染，无报错 |
| 7 | web/src/components/work-item/create-work-item-dialog.tsx:140,142 | 同上模式 | **静默空**：指派人下拉永远为空 |

- 第 6、7 处为新发现（inventory-2026-10-12.md 的五处清单之外）——不崩溃但**无任何错误暴露**，比崩溃更隐蔽。
- 修复：identity.ts:72 改 `const page = await api.get<{items: Member[] | null}>(...); return page.items ?? []` —— 一处适配修全 7 处。`id`/`workspace_id` 两个字段声明应删除（消费方恰好全用 `user_id`，未踩雷）。

### 2.3 加入 POST /api/share-links/join（前端）vs /api/share-links/redeem（后端）

- 后端仅挂 `POST /api/share-links/redeem`（vendor routes.go:68）；`/api/share-links/join` 实测 **404 纯文本 "page not found"**（非 JSON envelope → 前端 ApiError code=E_MALFORMED_RESPONSE，onboarding 只显示通用"请求失败"）
- **cmd/orkome-server/routes.go:14 路由契约清单写着 `POST /share-links/join`** —— 契约文档与实现脱节（契约测试 router_contract_test.go 未覆盖 share-links 子路由，所以没拦住）
- 第二层错位：即使端点对了，`RedeemShareLink` 返回 **Workspace** `{id,slug,name,plan_code,created_at}`（service.go:504-532，curl 实证），前端 identity.ts:59 声明为 `Member`，onboarding.tsx:64 取 `member.workspace_id` → **undefined** → `setCurrent(undefined)` → beforeLoad 回落 `workspaces[0]`——多工作区用户会**加入成功却切到错误的工作区**。

### 2.4 其余 envelope 快查

| 端点 | 后端形状 | 前端消费 | 判定 |
|---|---|---|---|
| GET /api/workspaces | 裸数组 `[...{role}]` | `Workspace[] ?? []` | ✅ |
| GET invitations/share-links/members | `{items,total}` | 仅 members 有前端消费（其余无 UI） | members ❌（P1-2），其余 N/A |
| POST share-links 201 | `{link, code}` | 无 UI | N/A |

---

## 3. curl 实测记录（2026-10-12，均复现）

### 3.1 创建与 slug 校验

| 用例 | 结果 |
|---|---|
| POST create `fp02-ws-a` | **201** `{id,slug,name,plan_code,created_at}` |
| slug `"ab"`（<4）| 400 `E_VALIDATION` |
| slug `"-abcd"` / `"abcd-"`（首尾横线）| 400 —— **前端校验会放行**（P1-3） |
| slug `"abc def"` / 中文 | 400 |
| slug `"api"`（保留字但 <4 位）| 400 格式信息（保留字检查在格式之后） |
| slug `"admin"`（保留字 5 位）| 400 `"slug is reserved"`（前端无此检查，用户见英文报错） |
| slug `"FP02-UPPER "`（大小写+空格）| **201 且归一化为 `fp02-upper`**（service.go:76-78） |
| 重名 `fp02-ws-a` | 409 `E_CONFLICT` "slug already taken" |
| name 空 | 400 `"name required"` |
| **8 并发同 slug `fp02-race`** | **1×201 + 7×409** —— `UNIQUE(slug)`（ploykit 003_workspace.up.sql）+ `pg_advisory_xact_lock` 计数锁（pgrepo/workspaces.go:99-111），并发正确性 ✅ |

### 3.2 X-Workspace-Id 越权矩阵（ADR 0005 对齐）

| 用例 | 实测 | ADR 预期 |
|---|---|---|
| GET /api/work-items 不带头 | **400** `E_VALIDATION "X-Workspace-Id header required"` | ✅ |
| 带不存在的 workspace id | **403** `"not a member of this workspace"` | ✅（403=不存在=非成员，防枚举） |
| 带 slug（`inventory-ws`）当 id | **403** | ✅（不做格式检查） |
| 第二用户带第一用户 ws id | **403** | ✅ |
| GET 不存在的 ws 的 members（path 通道）| **404** `"workspace not found"` | ✅（ADR 0005 记录的 identity 唯一偏差） |
| 非成员 PATCH rename ws | **404**（path 通道 404 语义） | ✅ |
| 非成员 DELETE ws | 404 | ✅ |

### 3.3 成员视角权限矩阵（fp02-second 加入 fp02-ws-a 后，role=member）

| 操作 | 实测 | 依据 |
|---|---|---|
| GET members | **200**（`members:read`，envelope 实锤 `{items,total}`） | authz/defaults.go:53-61 |
| PATCH rename | **403** `insufficient permission: workspace:update` | ✅ member 只读 |
| POST invitation | **403** `members:invite` | ✅ |
| DELETE workspace | **403** `workspace:delete` | ✅ |
| GET share-links | **200**（`invites:read`） | ✅ |
| GET /api/work-items | **200** | ✅ |
| POST /api/work-items | **201**（见 P2-4：work 域不做角色检查） | ⚠️ |
| POST leave | **200** | ✅（workspace:read） |
| owner 删 share-link 前后 uses 计数 | 0→1 ✅ | — |

share-link 闭环：owner 创建（`{link, code}` 201）→ 第二用户 redeem 200 → uses=1 → members 出现 role=member 行。**后端链路完好，断点纯在前端（P0-2/P0-3）**。

### 3.4 删除工作区（新缺陷 P1-1）

| 用例 | 结果 |
|---|---|
| DELETE 空工作区 ×4（abcdef/abcd/fp02-upper/fp02-ws-b）| **204** 全部成功 |
| DELETE `fp02-ws-a`（含 1 工单 + status 种子 + share-link + 成员进出史）| **500**，工作区**残留删不掉** |
| 服务端日志 | `internal error (unmapped) err="...violates foreign key constraint \"work_item_status_workspace_id_fkey\" ... (SQLSTATE 23503)"` |

根因链：ploykit `DeleteWorkspaceCascade`（vendor pgrepo/repo.go:58-67）= 裸 `DELETE FROM workspace`，完全依赖 DB 级联；但 orkome 迁移中 **52 处 `REFERENCES workspace(id)` 无一处 `ON DELETE CASCADE`**（grep 计数，1001~1035 全周期）；orkome teardown hook（main.go:182-193）只取消活跃任务不清业务表。首个撞墙的必是 `work_item_status`（1001_work_core.up.sql:6 无级联；internal/work/adapters/pgrepo/repo.go:26 `statusSeed` 会对每个用过的工作区播种）→ **任何真实使用过的工作区都不可删除**。

### 3.5 前端行为静态验证（不开浏览器，代码+依赖源码实证）

- **P0-1 根因实证**：`@base-ui/react@^1.0.0` node_modules 实源 `menu/group/MenuGroupContext.js`：`useMenuGroupRootContext()` 在 context undefined 时 `throw new Error('Base UI: MenuGroupContext is missing. Menu group parts must be used within <Menu.Group> or <Menu.RadioGroup>.')`；`workspace-switcher.tsx:23` 的 `<Menu.GroupLabel>` 直接放在 `<Menu.Popup>` 下 → 渲染即 throw → 整页进 RouteErrorComponent（router.tsx defaultErrorComponent）。组件挂在全局 header（_app.tsx:91）→ **任意 _app 页面点开即崩**。
  - **最小修复**：GroupLabel 与 items 外包一层 `<Menu.Group>`（3 行 JSX）。旁证：ploykit 自己的 UsersPage.tsx:265 注释「GroupLabel 必须包在 Group 内（Base UI Menu.GroupLabel 渲染即读组上下文」。
- **缓存失效彻底性**：switchWorkspace `removeQueries` 只保留 `me`/`config`（均为工作区无关数据），其余全清 → 无老 ws 数据泄漏；`wsClient.reconnect()` 在 `setCurrent` **之后**调用，realtime/client.ts:4-15 的 url 是函数、连接时实时读 store → 时序正确 ✅。
- **持久化**：localStorage `orkome.workspace`，刷新保持 ✅；伪 id 由 _app.tsx:45-48 自愈 ✅。
- 设置页第二套切换器（_app.settings.tsx:52-62，按钮式）不受 P0-1 影响，可作降级路径。

---

## 4. 后端正确性补充

- **默认角色/配额**：`WorkspaceConfig{MaxPerUser: -1}`（wiring_identity.go:94）= 无限（零值约定，service.go:95-106 注释明确）；创建即插 owner 行（pgrepo CreateWorkspaceWithOwnerTx，同事务原子）；`workspace.slug UNIQUE` + advisory lock 双保险（实测 8 并发）。
- **last-owner 保护**：demote/remove/leave 三路径均有 `CountActiveOwners` + `lastOwnerGuardSQL` SQL 级兜底（service.go:248-261, 288-294, 326-330；pgrepo:175-179）——未实测但代码路径完整。
- **成员软删**：`removed_at IS NULL` 过滤贯穿所有查询，`uq_member_active` 唯一索引支持重新加入 ✅。
- 审计埋点：create/rename/delete/role_change/invite/redeem 全部走 `s.audit`（service.go）✅。

---

## 5. 测试覆盖

| 层 | 现状 | 评价 |
|---|---|---|
| vendor workspace 域 | 无 *_test.go（vendor 裁剪） | 依赖上游 |
| orkome Go | e2e/scenario 有成员 guard 覆盖；router_contract_test.go 做路由契约对比 | **契约清单本身写错**（routes.go:14 `share-links/join`）且未覆盖 share-links 子路由，P0-2 漏网 |
| 前端 | web/src **0 个** *.test.*/*.spec.* 文件 | 切换器/onboarding/workspace-switch/缓存失效全裸奔 |

建议：① 契约测试补 SubrouteInfos() 全量对账（routes.go:105 就是为此准备的）；② 修复 members envelope 后加一条 fetch mock 类型测试；③ 删除工作区加一条"含数据工作区"集成测试（现挂 500 正是空缺）。

---

## 6. 缺陷清单

### P0（3）

| # | 缺陷 | 位置 | 最小修复 |
|---|---|---|---|
| P0-1 | 任意页面点工作区切换器整页崩溃（Menu.GroupLabel 脱离 Menu.Group，Base UI 渲染即 throw） | web/src/components/workspace-switcher.tsx:23 | GroupLabel+items 外包 `<Menu.Group>` |
| P0-2 | onboarding 邀请码加入必 404：前端打 `/api/share-links/join`，后端只有 `/api/share-links/redeem`；契约文档 routes.go:14 同错 | web/src/api/identity.ts:60 ↔ cmd/orkome-server/routes.go:14 | 前端改调 `/api/share-links/redeem`（1 行）+ 修契约清单 |
| P0-3 | join 响应形状错位（与 P0-2 堆叠）：redeem 返回 Workspace 无 `workspace_id`，onboarding `setCurrent(undefined)` → 多工作区用户加入后切错工作区 | web/src/routes/onboarding.tsx:64（identity.ts:59 类型连带） | onSuccess 改用响应 `id`，`joinByShareCode` 返回类型改 `Workspace` |

### P1（3）

| # | 缺陷 | 位置 | 修复 |
|---|---|---|---|
| P1-1 | 删除任何用过的工作区必 500 且删不掉：52 处 FK 零级联，teardown 只取消任务（curl 实证 23503） | vendor pgrepo/repo.go:58-67 + orkome main.go:182-193 + migrations 全部 | teardown hook 内按拓扑序清 orkome 业务表（推荐，不动 52 个迁移）；或迁移统一补 ON DELETE CASCADE |
| P1-2 | members envelope 连坐 7 处（5 崩溃 + 2 静默，新发现 work-items 筛选条与创建指派下拉两处静默空） | web/src/api/identity.ts:71-74 | listMembers 解包 `.items ?? []`（一处修 7 处）；顺删 Member 接口错误的 `id`/`workspace_id` 字段 |
| P1-3 | slug 前后端规则不一致：前端放行首尾 `-`、不查保留字；i18n 文案缺首尾说明；用户过前端校验后吃 400 英文报错 | web/src/routes/onboarding.tsx:15、web/src/i18n/locales/zh.ts:575 | 前端 regex 对齐 `^[a-z0-9][a-z0-9-]{2,38}[a-z0-9]$`，文案补「不以 - 开头/结尾」，保留字提示可后置 |

### P2（5）

| # | 缺陷/观察 | 位置 | 建议 |
|---|---|---|---|
| P2-1 | 切换工作区后 URL search 参数残留：`?assignee=user:<旧ws成员id>` 静默失效/空结果 | web/src/routes/_app.work-items.index.tsx:26 + services/workspace-switch.ts | switchWorkspace 内 `router.invalidate()` 或清除工作区语义参数 |
| P2-2 | onboarding 创建成功不清 react-query 缓存（不走 switchWorkspace 的 removeQueries），"删光重建"场景旧缓存带新头重放 | web/src/routes/onboarding.tsx:47-51 | create.onSuccess 改调 `switchWorkspace(queryClient, ws.id)` |
| P2-3 | 成员/邀请管理面零 UI：invitation、share-link 管理、角色管理、重命名、删除、离开、转移所有权 7 组端点（后端全通、权限齐备、curl 验证 ✅）前端无入口 | web/src/routes/_app.settings.tsx（现状仅切换按钮） | 产品排期；短期至少给 settings 加"离开工作区/邀请"入口 |
| P2-4 | work 域写操作未接 authz 角色矩阵：wsGuard 只查成员身份（internal/work/adapters/http/routes.go:76-88），实测 member 可创建工单 201；authz.DefaultRoles 的 member 并无 work:write（vendor authz/defaults.go:53-61）——两套权限模型漂移 | internal/work/adapters/http/routes.go:76 | 产品拍板"成员可写工单"是否刻意；若是，从 catalog 注释固化，防止未来补 authz 时全员突然失去写权限 |
| P2-5 | header 403 vs path 404 语义不一致（不存在/非成员）——ADR 0005 已背书为 identity 唯一偏差，记录备契约对照 | vendor handlers.go:129 vs 161-164 | 无需改动 |

### 实测通过项（无缺陷）

创建 201/归一化/重名 409/8 并发竞态、越权矩阵 11/11、成员角色读权限、share-link 闭环与 uses 计数、last-owner 三重保护（代码路径）、当前 ws localStorage 持久化与伪 id 自愈、切换缓存失效与 realtime 重连时序、X-Workspace-Id 仅对 /api 附加。

---

## 7. 遗留测试数据

- `inventory-ws`（e83dd858）与测试账号未动。
- fp02 空工作区已全部删除（204）。
- **`fp02-ws-a`（5ebb1af1-cd06-44ad-b116-b7b62897adc2, name "FP02 Audit WS A2"）无法删除——即 P1-1 的活体证据**，保留作复现样本，修复 P1-1 后可清理。
- 第二用户 fp02-second@orkome.dev 保留（其空工作区 fp02-ws-b 已删），供后续成员视角回归。
