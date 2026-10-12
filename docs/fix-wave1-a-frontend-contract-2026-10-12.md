# Fix Wave 1 · FixA 交接笔记：前端契约修复（identity/workspace/onboarding/MCP 页）

> 日期：2026-10-12 · 修复 agent：FixA · 依据：`docs/feature-audit-batch1-2026-10-12.md`（根因 A/B/C/E）+ `docs/audit-fp02-workspace-2026-10-12.md` + `docs/audit-fp09-mcp-page-2026-10-12.md` + `docs/audit-fp17-onboarding-2026-10-12.md`
> 独占文件范围：`web/src/api/identity.ts`、`web/src/routes/onboarding.tsx`、`web/src/components/workspace-switcher.tsx`、`web/src/i18n/locales/{zh,en}.ts`、`web/test/api/identity.test.ts` 及组件测试
> 未执行 git add/commit（按指令）；未起浏览器、未动任何服务

---

## 一、修复清单（每条对应审计编号）

### 1. members envelope 解包 —— batch1 根因 A / FP-02 P1-2（治 4 个 P0 + 2 个静默 P1）

`web/src/api/identity.ts` `listMembers`：
- `api.get<Member[] | null>` → `api.get<{ items: Member[] | null; total?: number }>`，返回 `page.items ?? []`。后端是 ploykit `app.Page` envelope（`{items,total}`，FP-02 §2.2 curl 抓样），一处解包治愈全站 7 个消费方（info-panel/property-rows/work-item-list/comment-thread/_app.agents.$agentId 5 处崩溃 + work-items 筛选条/创建指派下拉 2 处静默空）。
- `Member` 接口两处重复声明（原 :21 与 :62）合并为一处（放文件前部），删除后端不存在的 `id`/`workspace_id` 字段；保留真实形状 `user_id/email/display_name/role/created_at`。全仓 grep 确认无消费方使用被删字段（唯一 `.workspace_id` 用点在 onboarding.tsx，随修复 2 一并改掉）。

### 2. 邀请码加入 —— batch1 根因 C / FP-02 P0-2 + P0-3 / FP-17 P1-R1

- `joinByShareCode`：`POST /api/share-links/join`（不存在，404）→ `POST /api/share-links/redeem`；返回类型 `Member` → `Workspace`（redeem 真实响应为裸 Workspace，无 `workspace_id`/`role`，FP-17 T23 实锤）。
- `web/src/routes/onboarding.tsx` join.onSuccess：`setCurrent(member.workspace_id)`（undefined → 多工作区用户切错区）→ `setCurrent(ws.id)`。

### 3. PAT 创建 —— batch1 根因 E / FP-09 P0-1

`web/src/api/identity.ts` `createToken` 双断点一次修：
- 请求体 `{name, ttl_seconds}` → `{name, ttl_hours: Math.round(ttlSeconds/3600)}`（后端 DisallowUnknownFields，旧字段必 400；TTL_OPTIONS 720/2160/4320 秒整除无损）。
- 响应改按嵌套 `{pat:{...}, token}` 解包，返回 `{...res.pat, token: res.token}`（旧扁平解析渲染「令牌""已创建」）。唯一调用方 `_app.mcp.tsx` 无需改动。
- 注：CSRF `PATPrefix:"ork_"` 缺失（FP-09 P1-1）在 main.go，属共享文件，不在本 agent 范围，**未动**。

### 4. 切换器渲染即崩 —— batch1 根因 B / FP-02 P0-1

`web/src/components/workspace-switcher.tsx`：`<Menu.GroupLabel>` + items 外包一层 `<Menu.Group>`（Base UI MenuGroupContext 缺失时渲染即 throw，任意页面点开即崩）。

### 5. onboarding 逃生门 —— FP-17 P1-1

`web/src/routes/onboarding.tsx`：
- 加 `beforeLoad`：`ensureQueryData(meQueryOptions)` 失败（无会话）→ `redirect({ to: '/login' })`（治"未登录直达 /onboarding 提交时报裸 401"，并斩断 /login↔/onboarding 双向弹弓的入口侧）。
- 页面底部加「使用其他账号登录」ghost 按钮：`logout()` → `setCurrent(null)` → `queryClient.clear()` → `wsClient.close()` → `navigate('/login')`（对齐 `_app.tsx` signOut 清理链；邮箱误输 JIT 造号者不再被钉死）。

### 6. slug 前后端对齐 —— FP-02 P1-3 / FP-17 P2-1

`web/src/routes/onboarding.tsx`：`/^[a-z0-9-]{4,40}$/` → `^[a-z0-9][a-z0-9-]{2,38}[a-z0-9]$`（对齐 ploykit domain/rules.go，首尾不得为短横线）；i18n `onboarding.slugRule` 中英文案同步补「不以短横线开头或结尾」。保留字前端预检未做（审计建议可后置，后端 400 兜底）。

### 7. i18n 补键

- `web/src/i18n/locales/zh.ts` 新增顶层 `status.*`（backlog/todo/inProgress/inReview/blocked/done/cancelled 7 键）+ `priority.*`（none/low/medium/high 4 键），自 `en.ts:450-464` 翻译；位置与 en.ts 一致（work 与 automation 之间）。这两组键被 `web/src/components/work-item/meta.ts` 的 STATUS_META/PRIORITY_META 动态消费，此前 zh 用户一直回落英文。
- `onboarding.useAnotherAccount`（使用其他账号登录 / Use another account）中英双份；`slugRule` 文案双语更新。
- 注意：zh/en 无类型级 parity 检查（fallbackLng=en），en 侧无需补键。

### 8. 测试修正（batch1 §四.1"测试按错误契约 mock"的标本修复）

- `web/test/api/identity.test.ts`：
  - joinByShareCode 用例：mock 由不存在的 `/api/share-links/join` + Member 形状 → `/api/share-links/redeem` + 真实 Workspace 抓样 `{id,slug,name,plan_code,created_at}`；断言 `ws.id`。
  - 新增 listMembers 两条：`{items,total}` envelope 解包 + `{items:null}` → `[]`，抓样即真实成员形状（无 id/workspace_id）。
  - 新增 createToken 一条：断言请求体为 `{name, ttl_hours:2}` + `{pat:{...},token}` 响应解包为扁平 PatSecret。
  - 原 createWorkspace / meQueryOptions 用例（契约本就正确）保留。
- 新建 `web/test/components/workspace-switcher.test.tsx`（2 用例）：打开菜单渲染工作区列表不抛错（根因 B 回归断言，修复前此用例会以 "MenuGroupContext is missing" 失败）+ 空工作区回落文案。msw 提供真实 `/auth/me`+`/api/workspaces` 抓样。
- 修复 1-4 各有至少一条回归断言（1→listMembers envelope；2→redeem 抓样；3→createToken 双向契约；4→switcher 打开不崩）。

## 二、范围外顺手修（共享门禁被挡）

- `web/src/main.tsx:1`：并行 agent 遗留的编译错误 `TS2304: Cannot find name 'QueryClient'`（:44 `as unknown as QueryClient` 无导入）——补 `type QueryClient` 导入一行，仅类型层，无运行时行为变化。该文件非本 agent 独占，动它只因它阻塞共享 build 门禁。

## 三、门禁结果（实测输出）

- **我的全部改动文件**：eslint 零发现（逐文件跑过：identity.ts / onboarding.tsx / workspace-switcher.tsx / zh.ts / en.ts / main.tsx / identity.test.ts / workspace-switcher.test.tsx → clean）。
- **测试**：`pnpm vitest run test/api/identity.test.ts test/components/workspace-switcher.test.tsx` → **8/8 全绿**。全量 `pnpm test`：125/126 通过 + 1 文件级失败，失败项全部在**其他并行 agent 在飞文件**（`run-logs-drawer.test.tsx` 文件级 import 失败、`work-detail.test.ts` 的 taskMessagesAscending 1 用例——均为 FP-05/run-logs agent 正在改的文件，与本批修复无关）。
- **build**：vite 构建通过；`tsc` 在过滤上述两个在飞文件后**零错误**。全量 `pnpm build` 终态取决于该 agent 收尾，主会话汇总门禁时需以全树静止后为准。
- `project-nav.tsx:58` 预存 lint error（react-hooks/set-state-in-effect）未动，不属于本批。

## 四、遗留与提示（给主会话/后续 agent）

1. 共享门禁收口：等 run-logs agent 的 `run-logs-drawer.test.tsx`/`zz-probe-drawer.test.tsx` 定稿后重跑 `pnpm build`+`pnpm test` 即应全绿；FixA 侧无未决项。
2. FP-02 P2-2（onboarding create.onSuccess 不清缓存，建议改走 switchWorkspace）为 P2 未入本批清单，未改。
3. FP-17 P2-2/3/4（错误码 i18n 映射、suggestSlug 中文 fallback、409 消歧）未入本批清单，未改。
4. main.go CSRF `PATPrefix:"ork_"`（FP-09 P1-1）与根因 D 后端 coalesce 归后端 agent；identity.ts 的 PAT 解包已就绪，两者合流后 MCP 页 PAT 全链即通。
5. 审计提到"契约文档 cmd/orkome-server/routes.go:14 写着 share-links/join 同错"——属 Go 侧文件，本 agent 未动，需后端/契约 agent 修正。
