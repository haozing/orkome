# 修复 wave3 · P2：通知偏好设置 UI（settings 页新区块）交接笔记

> 日期：2026-10-12 · 负责：P2 agent · 审计来源：docs/audit-fp10-notifications-shell-2026-10-12.md P2-1（偏好 UI 露出率 0%）
> 结论：**已交付**。settings 页新增「通知偏好」区块，迁移 1034 的三组静音偏好全露出，读-改-写 + 成功/失败反馈，中英 i18n，msw 测试 38 例。门禁全绿（build / test 229 passed / eslint 0 发现）。未碰任何禁碰文件。

---

## 一、契约要点（后端核实 + curl 活体复核）

### 1.1 端点与形状

| 端点 | 行为 | 备注 |
|---|---|---|
| `GET /api/notification-preferences` | 200，**裸 JSON 无 envelope**：`{muted_types, muted_work_item_ids, muted_conversation_ids}` | 空组可能是 `null`（pgrepo `splitMutedTypes` 空输入返 nil，实测 `muted_conversation_ids:null` 与 `muted_work_item_ids:[]` 同响应并存）——api 层统一归一为 `[]` |
| `PATCH /api/notification-preferences` | 请求体为 `PreferencePatch`（三个可选键，指针语义），**只更新提供的键**，服务端合并→校验→**返回合并后的完整 Preference** | 已活体核实：带 `muted_types:["*"]` 的行再 PATCH 单键 `muted_work_item_ids`，响应同时保留两者 |
| `PUT /api/notification-preferences` | 全量替换，本 UI 未用 | 框架 `PUT /api/notification-preferences/{type}`（per-type 行）是死开关（审计 P2-1 备注），本次未动 |

代码位置：`internal/notification/adapters/http/routes.go:197-234`（handler）、`internal/notification/app/service.go:25-30, 246-285`（Preference / PreferencePatch / Patch 合并 / validatePreference）、`internal/notification/adapters/pgrepo/repo.go:185-237`（`session:` 前缀编解码、'mute' 哨兵行 upsert）、`migrations/1034_notification_preference_ext.up.sql`。

### 1.2 字段全集（三组静音，工作区级，(user_id, workspace_id) 唯一的 'mute' 行）

| 字段 | 语义 | 校验（E_VALIDATION 400） | 通配 |
|---|---|---|---|
| `muted_types: string[]` | 按通知类别静音 | **≤50 项**；禁止 `session:` 前缀（保留字，实测 400 "muted type reserves session: prefix"） | 支持 `"*"`（只作用于类别组） |
| `muted_work_item_ids: string[]` | 按工作项静音 | **≤200 项** | 无（仅精确匹配） |
| `muted_conversation_ids: string[]` | 按会话（session）静音 | **≤200 项** | 无（仅精确匹配） |

- 存储层把 `session:<id>` 编进 `muted_types` 列，API 层已拆分——**前端永远只看三键裸数组**，不要写 `session:` 前缀。
- 静音判定在写入侧（`domain.Muted`，fail-open），类别值与落库 type 一致：`task.completed` / `task.failed` / `comment.created` / `approval_request` / `eval_report`（`api/notification.ts` `NOTIFICATION_TYPE_KEYS` 的 5 种；另有 `task_completed`/`autopilot_paused`/`wakeup_member` 落库值，审计 P2-2 归其他修复）。
- 认证/作用域：`webx.RequireAuth` + `X-Workspace-Id` 成员校验（缺头 400、非成员 403）。前端 `@ploykit/client` 的 `apiFetch` 自动注入 `X-Workspace-Id`（main.tsx `setWorkspaceIdProvider`）与 CSRF，无需手工处理。

### 1.3 curl 活体记录（inventory-test@orkome.dev @ inventory-ws）

1. GET 空态 → `{"muted_types":null,"muted_work_item_ids":[],"muted_conversation_ids":null}` ✓
2. PATCH `{"muted_types":["*"]}` → 200 回显 ✓；GET 一致 ✓
3. PATCH 单键 `{"muted_work_item_ids":["1111…"]}` → 合并保留 `muted_types:["*"]` ✓（**PATCH 只发改动键**的依据）
4. PATCH `{"muted_types":["session:x"]}` → 400 E_VALIDATION ✓
5. PATCH 三键 `[]` 复位（**已把共享 dev 服务器的偏好行还原为空**，未留测试残留）✓

## 二、交付清单

| 文件 | 变更 |
|---|---|
| `web/src/api/notification.ts` | 追加 `MUTABLE_NOTIFICATION_TYPES`（5 种可静音类别）、`NotificationPreferences`（null→[] 归一）、`getNotificationPreferences()`、`patchNotificationPreferences(patch)`（部分键 PATCH，注释写明契约）；既有函数零改动 |
| `web/src/routes/_app.settings.tsx` | 新增第三区块「通知偏好」（登录后渲染）；`SettingsPage` 改为 `export function`（原因见 §4 坑 1）；内部组件 `PrefSwitch/PrefRow/MutedIdChips`（base-ui Switch，zinc 风，与既有区块同款 section 样式） |
| `web/src/i18n/locales/zh.ts` / `en.ts` | 新增 `notifications.*` 命名空间 31 键 ×2（只加不改） |
| `web/test/components/settings-notification-prefs.test.tsx` | 新建：msw 渲染当前值（含 null 组回退默认）、切换保存断言 PATCH body 形状（只发改动键）、工作项/会话增删往返、保存失败提示（服务器 message 透出 + 受控开关不落缓存）、加载失败 + 重试、31 个 i18n 双语键存在性。38 例全绿 |

## 三、UI 设计决策（笔记义务）

1. **PATCH 只发改动键**（非全量）：后端是按键合并语义（§1.1 实测 3），只发改动键可避免把 UI 未见过的组写回去（例如将来别的入口写入了工作项静音，settings 只切类别开关不会误清它）。PATCH 响应（合并后全量）直接 `setQueryData` 写缓存，免一次 GET。
2. **全露出**：
   - 类别组：`*`（全部类别）开关 + 5 种产品类别各一开关，均直接映射 `muted_types` 的增删（`*` 与具体类别可并存，后端谓词天然支持）。
   - 工作项组 / 会话组：组开关（反映 `length>0`；空组禁用；关闭 = 清空该组，PATCH `[]`）+ 输入框添加（trim 去重、maxLength 100、达 200 上限禁用添加）+ chip 删除。ID 列表目前没有其他管理入口，settings 是唯一露出口。
3. **空/未设置偏好**：GET `null` 组在 api 层归一为 `[]` → 全部开关关闭、空态文案「未静音任何…」，即后端默认（不静音）。
4. **反馈**：每次写入后 `aria-live=polite` 状态行——成功 `notifications.saved`；失败 `notifications.saveFailed` + ApiError 的服务器 message（如保留字 400 文案）。写入进行中全部开关禁用防交错丢失更新。
5. 区块仅在 `me.data` 存在（已登录）时渲染——偏好端点强制认证，未登录时挂一块常驻报错没有意义。

## 四、坑与实测发现（给后续 agent）

1. **路由文件的具名导出必须用 `export function`，不能在文件尾 `export { X }`**：`@tanstack/router-plugin`（vite，autoCodeSplitting）对 `src/routes/` 下文件的转换会把尾部导出列表形式破坏成 undefined，vitest 渲染直接 "Element type is invalid… got: undefined"。同一文件内容放到 routes 目录外渲染完全正常（已实证）。`_app.automation.tsx` 用 `export function` 所以没踩过。
2. base-ui v1.8 `Switch.Root/Thumb` 渲染为 `<span role="switch">`（forwardRef，`typeof === 'object'` 正常）；禁用态是 `aria-disabled="true"` 而非原生 disabled——测试断言用 `toHaveAttribute('aria-disabled','true')`，不能用 `toBeDisabled()`。
3. msw handler **先注册者先匹配**：`server.use(...defaultHandlers(), http.patch(...))` 时默认集里的同名 handler 会吃掉请求——本测试把记录型 PATCH handler 放在 `server.use()` 第一位。
4. `@ploykit/ui` 的 barrel 不可在 web 里 import（AppProviders → `@ploykit/hooks` 未别名、不可解析），Switch 需直接从 `@base-ui/react/switch` 引（web 已有该依赖，ploykit switch 的样式类已内联复制）。
5. prettier 全仓漂移（`_app.automation.tsx`、`api/identity.ts` 等存量文件 `--check` 也不过），门禁以 eslint 为准，本次未跑 prettier --write。

## 五、门禁摘要（2026-10-12 实测）

| 门禁 | 结果 |
|---|---|
| `cd web && pnpm build`（vite build + tsc -b） | ✅ 通过（chunk 体积 warning 为存量现象） |
| `cd web && pnpm test` | ✅ 229 passed (229)，含新增 38 例 |
| `pnpm exec eslint <5 个改动文件>` | ✅ 零发现 |

未开浏览器、未重启共享服务、共享 dev 服务器偏好行已复位为空。未执行任何 git add/commit。

## 六、遗留与建议（不阻塞本交付）

- 三种未映射落库类型（`task_completed`/`autopilot_paused`/`wakeup_member`）不在类别开关里——等审计 P2-2 的 i18n 映射落地后，往 `MUTABLE_NOTIFICATION_TYPES` + i18n 各加一组键即可。
- 框架 `PUT /api/notification-preferences/{type}` 死开关的接通/摘除（审计 P2-1 尾注）属后端决策，未动。
- `getNotificationSummary`/`archiveAllNotifications` dead code（审计 P2-10）未清理，留给对应修复批次。
