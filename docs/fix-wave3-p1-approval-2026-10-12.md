# 修复交接笔记 · Wave3-P1（直聊任务审批入口，run-logs 抽屉）· 2026-10-12

> 范围：FP-12 P2-5 前半（直聊任务待审批无处可批）——审批入口落在 run-logs 任务转写抽屉（已裁决）。
> 涉及审计：docs/audit-fp12-run-logs-2026-10-12.md P2-5（本笔记只交审批半；running 取消动作与顶层 cancel 死端点仍在 P2-5 名下未动）。
> 门禁：`pnpm build` ✅ exit 0 · `pnpm test` ✅ 22 文件 / 135 用例全绿 · `pnpm exec eslint <5 个改动文件>` ✅ 零发现。
> 未执行任何 git add/commit；未开浏览器；未重启共享服务。

---

## 一、端点与请求形状（从 task-panel.tsx:129 回读，全部复用）

- `POST /api/tasks/{taskId}/approve` | `POST /api/tasks/{taskId}/deny`，body `{note}`（routes.go:99 + handleApprove :386-404，note 可缺省）。
- 前端封装 **`settleTask(taskId, approved, note?)` 已存在**（orchestration.ts:322，FixB 波次引入，本波次无人占用）——**orchestration.ts 零改动**，抽屉直接复用，与工单面板同一函数同一端点。
- 后端语义（service.go SettlementService.Approve :778）：存储态 status 必须为 `running`（awaiting_input 是 running+awaiting_input_since 的派生态，repo SELECT CASE 派生），同步 resume 引擎后三种落点：complete（发 `task.completed` 事件，:821 实测源码确认）/ 再 park（发 `task.awaiting` 事件，:776）/ fail。前端不在等待时点按钮 → 409 E_CONFLICT。
- **失效链确认覆盖**：抽屉已有 `useWsScope('task', task.id)`（FixB P2-1），main.tsx:16 `/^task:/` 规则失效 `['work-item']`/`['task']`/`['workspace-tasks']`——complete 与再 park 两条路径都有 task: 帧；消息 paged 键 `['task', id, 'messages', 'paged']` 与列表键 `['workspace-tasks', …]` 均被前缀命中。

## 二、修复清单

### 1. 抽屉审批操作区 — `web/src/components/run-logs/task-transcript-drawer.tsx`
- `task.status === 'awaiting_input'` 时在标题下渲染 amber 操作区（amber 与 TaskStatusBadge 的 warning tone 同色系，"这任务在等我批"一眼可见）：提示行 `taskMsg.awaitingDecision` + 「批准」（default 按钮，Check 图标）+「驳回」（danger 按钮，X 图标）+ pending 旋转指示；**其余状态一律不渲染**（`docked` 派生，无多余分支）。
- mutation 成功 → 主动失效 `['workspace-tasks']` 与 `['task', task.id]`（与 task-panel 的 invalidate 同款双保险，不依赖 WS 帧到达时序）。
- **成功即藏**：`settledDone`（mutation isSuccess）成立后操作区隐藏——审批是同步 resume，成功后任务不再 awaiting；即使该行已掉出当前筛选 tab（如批准后完成、掉出 active）、父级快照仍停在旧状态，也不会出现"再点一次吃 409"的困惑态。
- **再 park 复位**：审批后 agent 可能再次 interrupt → awaiting_input（后端 park 路径实测存在）。加一个小 effect：状态回到 `awaiting_input` 即 `settle.reset()`，失效链把列表刷新、覆盖状态翻转回来后操作区重新出现，避免按钮一次性永久消失。
- 失败路径：操作区内联红字 `taskMsg.settleFailed`（409/网络等一视同仁），按钮保持可重试；未误判成功。全站无全局 toast 系统，内联提示与 task-messages 渲染器的错误提示（`taskMsg.openConsoleFailed`）风格一致。

### 2. 抽屉任务快照跟随刷新 — `web/src/routes/_app.run-logs.index.tsx`
- 原实现 `openTask` useState 持有打开那一刻的行快照，列表失效刷新后抽屉徽章/操作区仍冻结在旧状态。改为 `openTaskRow = rows.find(r => r.id === openTask.id) ?? openTask`：优先取最新列表行覆盖（批准后徽章即时从"待审批"翻成"运行中/已完成"），行掉出当前筛选时回退快照——**抽屉不闪关**（直接改存 id + find 会 在任务移 tab 时把抽屉硬关）。
- 抽屉头部状态徽章：`TaskStatusBadge` 已在（FixB P2-1 波次渲染，覆盖全部八态含 awaiting_input），本波次未重复添加；其"实时性"正是由本条 overlay 补齐。

### 3. i18n — `web/src/i18n/locales/zh.ts` / `en.ts`
- `taskMsg.*` 新增四键，中英双份（只加键，未动他人键）：

| 键 | zh | en |
|---|---|---|
| `taskMsg.approve` | 批准 | Approve |
| `taskMsg.reject` | 驳回 | Reject |
| `taskMsg.awaitingDecision` | 该任务在等待你的审批，批准后将继续执行 | This task is waiting for your approval; approving resumes it |
| `taskMsg.settleFailed` | 审批操作失败，请重试 | Failed to record the decision, please retry |

- 有意未复用 `work.approve`/`work.reject`：本组件文案属任务域（taskMsg），且任务书指定 `taskMsg.*`。

### 4. 测试 — `web/test/components/run-logs-drawer.test.tsx`（2 → 9 用例）
- `renderDrawer` 升级为返回 `{ ...rendered, invalidateSpy }`（spy 在 QueryClient 上，供失效链断言）+ 可传入任务夹具；新增 `AWAITING_TASK`（status: awaiting_input）。
- ① **awaiting_input 显示按钮并成功调用端点**：msw 拦 `POST /api/tasks/t-page/approve|deny`，点击 Approve → approve 恰一次、deny 零次 → `invalidateQueries` 收到 `['workspace-tasks']` 与 `['task','t-page']` 两次调用 → 操作区消失（快照仍 awaiting 也不残留按钮）。
- ② **非 awaiting 不显示**：running 夹具下 Approve/Reject/提示文案三查皆 null。
- ③ **失败路径**：approve mock 409 E_CONFLICT → 内联 `taskMsg.settleFailed` 英文文案出现，Approve 按钮仍 enabled（可重试，未误判成功）。
- ④ **i18n 键存在**：it.each 四键 × 双语（`lng: 'en'`/`'zh'`），断言 `t(key)` 不回退为键名本身。
- 既有 2 用例（task scope 订阅、desc 分页升序渲染）原样全绿。

## 三、文件清单（本 agent 触碰）

| 文件 | 变更 |
|---|---|
| `web/src/components/run-logs/task-transcript-drawer.tsx` | 审批操作区（awaiting_input only）+ settle mutation（复用 settleTask）+ 失效链 + 成功藏/再 park 复位 + 失败 i18n 提示 |
| `web/src/routes/_app.run-logs.index.tsx` | `openTaskRow` 快照覆盖（最新列表行优先、掉列回退），抽屉渲染改挂 `openTaskRow` |
| `web/src/i18n/locales/zh.ts` | taskMsg.approve/reject/awaitingDecision/settleFailed |
| `web/src/i18n/locales/en.ts` | 同上英文 |
| `web/test/components/run-logs-drawer.test.tsx` | 审批 3 用例 + i18n 键 4 例 + renderDrawer 升级 |
| `web/src/api/orchestration.ts` | **零改动**（settleTask 既有，直接复用） |

## 四、边界与遗留（不在本任务范围，留给后续）

1. **P2-5 后半**：running 任务的抽屉侧取消动作、顶层 `POST /api/tasks/{taskId}/cancel` 死端点（handleTaskCancelTop 全站零消费）——审计修复路线第 4 条建议与 chat 排期合并验收；本任务书只裁决了审批入口。
2. **note 形参未接 UI**：settleTask 支持 note，面板与抽屉均未提供备注输入（后端可缺省）；如需"驳回理由"输入框属产品增强。
3. **行掉出筛选的极端态**：批准后任务完成且用户停在 active tab 时，抽屉回退快照仍显示"待审批"徽章但操作区已隐藏（settledDone），文案自洽；关开抽屉即见新状态。
4. FP-12 P2-5 原文同时点名 chat 页 PagePlaceholder——chat 排期时审批入口应在 chat 流内收口，本抽屉入口与其不冲突。

## 五、门禁凭证

- `pnpm build`：✓ built in 2.09s（rolldown chunk 体积警告为既有现象，与本改动无关）。
- `pnpm test`：Test Files 22 passed / Tests 135 passed（FixB 交接时的 128 + 本波次新增 7：审批 3 + i18n 4）。
- `pnpm exec eslint` 对 5 个改动文件：零发现（ESLINT_CLEAN）。
