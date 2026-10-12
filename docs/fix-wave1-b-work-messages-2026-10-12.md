# 修复交接笔记 · FixB（前端 work / 消息流）· 2026-10-12

> 范围：FP-11 P1-1（空串游标）、FP-05 P1-1（消息流分页盲区）、FP-12 P2-1（抽屉死回放）、FP-20 F4 配套（失效风暴 debounce）、FP-11 P1-2 前端半（排序显示对齐）、测试修正。
> 涉及审计：feature-audit-batch1/2 + audit-fp05 / fp11 / fp12（2026-10-12）。
> 门禁：`pnpm build` ✅ exit 0 · `pnpm test` ✅ 22 文件 / 128 用例全绿 · `pnpm exec eslint <新改文件>` ✅ 零发现。
> 未执行任何 git add/commit；未开浏览器；未触碰共享服务。

---

## 一、修复清单（逐条对账）

### 1. 空串游标（FP-11 P1-1）— `web/src/api/work.ts:228`
- `getNextPageParam: lastPage.next_cursor ?? undefined` → `|| undefined`：后端 keyset 耗尽返回 `next_cursor:""`，`??` 拦不住 → TanStack v5 `hasNextPage` 恒真 + 点击后重复整页。空串现在归为"无更多页"，my-work 四 tab 与 work-items 列表页的"加载更多"在耗尽后正确消失。
- 同款兜底同步加到 `_app.run-logs.index.tsx` 的 runs 列表 `getNextPageParam`（该端点耗尽返回 null，此处纯防御）。
- 双保险：FixE 会把耗尽时的 cursor 置空为省略字段；前端兜底保留。
- 回归钉子：`test/api/work.test.ts` 新增 "exhausted empty-string cursor "" → undefined"。

### 2. 消息流分页盲区（FP-05 P1-1）— `web/src/api/orchestration.ts`
后端契约（routes.go `handleTaskMessages`，已核对源码）：`order=desc` 返回最新 200（新→旧）；`next_before_seq` **仅在"desc 且满页"时携带**，缺失即更早历史耗尽。
- 新增 `listTaskMessagesPage(taskId, {limit?, beforeSeq?})`：恒 `order=desc` + `before_seq` 游标，返回前把页内消息**反转为 seq 升序**，`next_before_seq` 归一化为 `number | null`。
- `listTaskMessages(taskId, limit=200)` 保留原签名，内部改走 desc 拉最新 200 + 反转升序——**task-panel（不在本 agent 文件权限内）零改动即治愈盲区**：面板与摘要服务现在看同一窗口（最新 200），尾段含最终 tool_result 可见。
- 新增 `taskMessagesInfiniteQuery(taskId, limit=200)`：`useInfiniteQuery` 选项，`initialPageParam: 0`（首拉不带 before_seq），`getNextPageParam ← next_before_seq ?? undefined`。
  - **queryKey 用 `['task', id, 'messages', 'paged']`（尾段刻意不同）**：task-panel 的普通 `useQuery` 同用 `['task', id, 'messages']` 但数据形状是数组；共享同键会让两种 observer 互相覆写 queryFn 导致形状串台（面板 fresh 缓存 + 抽屉 infinite observer 读到数组 → `data.pages` 崩溃）。WS 失效链走 `['task']` 前缀，两个键都命中。
- 新增 `taskMessagesAscending(data)`：页（新→旧）倒序拼接成 seq 全局升序，无需排序。
- run-logs 抽屉接入：`useInfiniteQuery` + 升序渲染 + 顶部"加载更多"（`fetchNextPage` 翻更早历史，`runLogs.loadMore` 复用既有 i18n 键，未动 i18n 文件）。
- **16 个既有渲染器用例复跑全绿**（task-messages.test.tsx，全套 128 用例亦绿）。已知取舍：消息总数恰为 limit 整数倍时会多出一次"点击后空页"才能看到按钮消失（后端满页才给游标所致，空页后即收敛）。

### 3. 抽屉死回放（FP-12 P2-1）— 组件提取 + 订阅
- **`TaskTranscriptDrawer` 从 `_app.run-logs.index.tsx` 提取为 `web/src/components/run-logs/task-transcript-drawer.tsx`**。原因：@tanstack/router-plugin 的 autoCodeSplitting 会把路由文件的非 Route 导出拆进 chunk，路由模块运行时只导出 `Route`/`TSRFastRefreshAnchor`，测试（以及任何直接引用）拿到的组件是 undefined——实测踩坑后提取。逻辑逐行照搬，无行为变化。
- 抽屉挂 `useWsScope('task', task.id)`（照 task-panel.tsx:119 用法）：task:completed 等状态帧 → main.tsx `/^task:/` 失效规则 → refetch 抽屉查询；消息帧走 workspace scope，全局失效链本就覆盖。运行中任务的转写不再是打开时刻的冻结快照。
- 回归钉子：`test/components/run-logs-drawer.test.tsx`（新文件）——①挂载即 subscribe('task', id)、卸载经返回的清理函数退订；②升序渲染 + 向上翻页 + 耗尽后按钮消失（msw 按 `before_seq` 分页供数）。

### 4. 失效风暴 debounce（FP-20 F4 配套）— `web/src/main.tsx`
- 桥（ploykit ws-query-bridge）只消费 `queryClient.invalidateQueries`，故注入一个仅覆写该方法的 debounce 视图（其余成员要求上不经过）：**同 key 突发 300ms trailing 合并**为最后一次 invalidate。
- 四条规则（work_item:/task:/comment:/notification:）键语义不变；断线重连的全量 resync 走同一路径，仅延迟一拍同 key 合并。ploykit 包在 Go module cache 中不可改，规则层注入是最小侵入点。

### 5. my-work 排序显示对齐（FP-11 P1-2 前端半）— `_app.my-work.tsx:141-144`
- 移除"按时间"分组的客户端 `last_activity_at` 重排，`flatItems = items` 直接跟随服务端 `created_at,id` keyset DESC 序。窗口内重排会让"加载更多"接出的条目插到页首，且排序键不在服务端游标里，窗口外活跃项本就不可见。
- **接口约定（留档，代码内有同文注释）**：FixE 把排序/游标切到 `last_activity_at`（配合 UpdateWorkItem bump 语义修复）后，本页继续跟随服务端序，前端无需再改；在那之前"按时间"排序口径 = 创建时间倒序（近期 UI 上未加注，如需口径提示文案归 FixA 的 i18n 范畴）。

### 6. 测试修正
- `test/api/work-detail.test.ts:120-138` members mock：裸数组 → 真实 ploykit app.Page 信封 `{items, total}` 抓样（身份字段按 FixA 后的 Member 接口：user_id/role/email/display_name/created_at），断言 `listMembers` 解包结果。**该用例依赖 identity.ts 的根因 A 解包修复（FixA 已落地，已核对 working tree）**。
- 修复 1 回归：work.test.ts 空串游标 → undefined。
- 修复 2 回归：work-detail.test.ts 四例——wire `order=desc`+升序归一化、`before_seq` 游标传递 + `next_before_seq` 透出、首页不带游标 + 缺游标 → null、`taskMessagesInfiniteQuery` 游标映射 + `taskMessagesAscending` 拼接。
- 修复 3 回归：run-logs-drawer.test.tsx 两例（见上）。

---

## 二、文件清单（本 agent 触碰）

| 文件 | 变更 |
|---|---|
| `web/src/api/work.ts` | P1-1 空串游标 + 注释 |
| `web/src/api/orchestration.ts` | messages desc 游标分页三件套（TaskMessagePage / listTaskMessagesPage / taskMessagesInfiniteQuery / taskMessagesAscending）+ listTaskMessages 语义升级 |
| `web/src/routes/_app.my-work.tsx` | 移除客户端 last_activity_at 重排 + 接口约定注释 |
| `web/src/routes/_app.run-logs.index.tsx` | 抽屉提取引用、runs 列表 `||` 兜底、清理 Dialog/cn 等不再使用的导入 |
| `web/src/main.tsx` | 桥 invalidateQueries 300ms trailing debounce |
| `web/src/components/run-logs/task-transcript-drawer.tsx` | **新文件**（抽屉组件，原样提取 + useWsScope + infinite messages + load-more） |
| `web/test/api/work.test.ts` | 空串游标回归 |
| `web/test/api/work-detail.test.ts` | members 信封抓样 + 消息分页 5 例 |
| `web/test/components/run-logs-drawer.test.tsx` | **新文件**（抽屉订阅 + 升序翻页） |

未触碰：i18n（zh/en，归 FixA）、identity.ts（FixA）、task-panel.tsx（非本 agent 权限）、后端任何文件。

---

## 三、给后续 agent 的接口约定 / 注意事项

1. **FixE（后端）**：messages 端点请保持"`next_before_seq` 仅 desc 且满页返回"或改为耗尽省略字段——前端 `?? undefined` 两种都兼容；work-items 的 `next_cursor` 耗尽改省略字段后，前端 `|| undefined` 兜底仍成立（双保险，不用回改）。
2. **task-panel 责任 agent**：面板当前经升级后的 `listTaskMessages` 自动获得"最新 200 升序"；若要在面板做向上翻历史，直接换 `useInfiniteQuery(taskMessagesInfiniteQuery(task.id))` + `taskMessagesAscending`，queryKey 保持 paged 尾段（勿与数组形 `['task', id, 'messages']` 混用同一 observer 面）。
3. **FixA（i18n）**：本波新增 UI 复用既有键（`runLogs.loadMore`、`common.loading`），零新增键，无需补翻译。
4. 抽屉组件路径 `@/components/run-logs/task-transcript-drawer`；勿再从路由模块 import 组件（router 插件会拆走非 Route 导出）。
5. 测试基建注意：vi.mock 路由相关模块时，router-plugin 变换后的路由文件还会引 `lazyRouteComponent`；base-ui Dialog 走 Portal，DOM 断言用 `document.body` 而非 render 的 container。

## 四、门禁输出摘要

```
pnpm build  → exit 0（vite build + tsc -b；chunk >500kB 警告为既有基线，非本次引入）
pnpm test   → Test Files 22 passed (22) · Tests 128 passed (128)
              含 task-messages.test.tsx 既有 16 渲染器用例全绿
pnpm exec eslint <9 个新改文件> → 0 problems（全仓 lint 未跑，避免吞入他 agent 在途文件）
```

## 五、遗留 / 未尽事项（如实记录）

- FP-05 P1-2（interim 帧不截断）、P2-1（外部回写无帧）、P2-3（emit 吞错）：后端 eventpipe 域，归 FixE。
- FP-12 P2-2/P2-3/P2-5（usage 呈现、slim、抽屉动作条）：本轮范围外，未动。
- my-work 上游查询错误面、tab 键盘可达性（FP-11 P2-1/P2-4）：本轮范围外。
- 消息总数为 limit 整数倍时，抽屉"加载更多"需多点一次空页才消失（后端满页才返回游标的固有代价），见 §一.2。
