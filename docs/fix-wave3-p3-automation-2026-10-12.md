# P3 交接笔记：automation 页前端 MVP（空态页 → 可管理）

> 日期：2026-10-12 · agent：P3 · 契约事实源：`internal/automation/adapters/http/routes.go`（逐字核对）+ curl 实测（登录配方见任务书，工作区 e83dd858-7412-4625-b048-0741e5ae6552）
> 独占文件域：`web/src/routes/_app.automation.tsx`、`web/src/api/automation.ts`、`web/test/components/automation-page.test.tsx`（新）、`web/src/i18n/locales/{zh,en}.ts`（只加 `automation.*` 键）
> 未做 git add/commit（按任务书要求）；未开浏览器、未重启共享服务。

---

## 一、交付清单

| 项 | 内容 | 位置 |
|---|---|---|
| Autopilots 列表 | 名称 / exec_mode（i18n 标签）/ 目标 agent（`['agents']` 查询解析名字，退化显示短 ID）/ 启用态徽章（启用·停用·熔断置停）/ paused_reason 提示；空态保留引导文案；失败显示服务端 message | `_app.automation.tsx` AutomationPage |
| 新建 Autopilot 弹窗 | name、exec_mode 下拉（create_work_item/task_only）、prompt 文本域（非空时映射 `template.{title,description}`）、target_agent_id 下拉（GET /api/agents）、cron 文本框（可空=仅手动）、timezone 下拉（常用 IANA 8 值 + 空=UTC） | 同文件 CreateAutopilotDialog |
| 前端校验对齐后端 | name 必填（后端 `name required` 400）；cron 5 段格式客户端先拦 + **提交前先打 `POST /api/autopilots/cron-preview` 做真源校验**——因为后端 createAutopilot 对坏 cron 返回的是 **500 E_INTERNAL**（cronx 错误未包成 apierr），预检能把坏 cron 拦成友好 400 提示；task_only 模式必选工作项（映射 `template.work_item_id`，后端 dispatchRun 从模板取该值） | 同文件 |
| 每行操作 | 「立即触发」POST trigger，成功横幅显示 run 状态 + **任务已派发（task_id 短码）**（quota 超限时后端也回 200，run.status=skipped + reason_code 会在横幅自然呈现）；启用/停用 toggle（沿用）；删除行内二次确认 | 同文件 |
| 删除 409 分流 | 后端两种 409（启用中触发器 / pending-running 运行）按 server message 分类，分别显示「请先删除或停用其触发器」/「有等待/执行中的运行，请稍后重试」；行保留不消失 | 同文件 `deleteBlockedKey` |
| Wakeups 区块 | 列表（GET /api/work-item-wakeups）：kind 徽章（定时/事件/条件/系统）、指令、目标工作项标题（work-items 列表解析）、cron/interval、下次触发时间、停用态；**系统规则行无删除按钮**（后端 DELETE 会 400）；新建：工作项下拉 + 指令（≤4000）+ **datetime-local 时间选择器 → 就地转一次性 5 段 cron（`分 时 日 月 *`）+ max_fires=1 + 浏览器 IANA 时区**；删除走工作项域端点 | 同文件 WakeupsSection / CreateWakeupDialog |
| API 层 | 类型（Autopilot/Wakeup/AutopilotTrigger/AutopilotRun 补 task_id 等实测字段）+ 全部端点封装 + `cronPreview` | `web/src/api/automation.ts` |
| i18n | `automation.*` 新增 39 键 × 中英双份 + 测试锁定双语键存在 | `zh.ts` / `en.ts` |
| 测试 | 53 例：列表三态（空/有数据/加载失败）、创建往返（契约形状断言 + 坏 cron 双层拦截）、手动触发反馈、删除 409 两分支、wakeups 渲染/新建（cron 转换断言）/删除/过去时刻拦截、`datetimeLocalToCron` 单测、i18n 双语键 | `web/test/components/automation-page.test.tsx` |

## 二、端点形状速查表（routes.go 逐字 + curl 实测）

| 端点 | 请求 | 响应 | 实测备注 |
|---|---|---|---|
| `GET /api/autopilots` | `?limit=`（默认 100，≤500） | **裸数组** `Autopilot[]`（空=`[]`） | Autopilot 无 cron / 最近触发时间字段（见 §四） |
| `POST /api/autopilots` | `{name, exec_mode, template, target_agent_id, cron_expression, timezone}` | 201 `Autopilot` | 空 cron → 201 并建惰性兜底 trigger（wave1 修复，不挡删除）；**坏 cron → 500 E_INTERNAL**（非 400），故前端预检 |
| `GET /api/autopilots/{id}` | — | 200 `Autopilot` / 404 | |
| `PATCH /api/autopilots/{id}` | `{name?, exec_mode?, template?, target_agent_id?, enabled?}`（指针语义） | 200 `Autopilot` | exec_mode 仅认 `create_work_item\|task_only` |
| `DELETE /api/autopilots/{id}` | — | 204 | 409 两种：`autopilot still has enabled triggers; ...` / `autopilot has pending/running runs`；错误码均为 `E_REVISION_CONFLICT`，只能靠 message 分流 |
| `POST /api/autopilots/{id}/trigger` | 无 body | 200 `Run`（超 quota 也 200：`{status:"skipped", reason_code:"quota_exceeded"}`） | Run 形状实测：`{id, autopilot_id, source:"manual", status:"running", work_item_id, task_id, planned_at, triggered_at?}` |
| `POST /api/autopilots/cron-preview` | `{cron_expression, timezone, count}` | 200 `{next_runs: string[]}`；400 `E_VALIDATION`（坏 cron / 坏 tz，含 "Local" 拒绝） | 前端用作创建前真源校验 |
| `POST /api/autopilots/{id}/triggers` | `{kind, cron_expression, timezone, enabled}` | 201 `{trigger, credential?}` | 本 MVP 未用（UI 不做触发器管理） |
| `PATCH/DELETE /api/autopilots/{id}/triggers/{triggerId}` | 指针 patch / — | 200 `Trigger` / 204 | **无 GET 列表端点**（见 §四） |
| `GET /api/work-item-wakeups` | — | **裸数组** `Wakeup[]`（含 work_item_id、system_rule） | 删除唤醒所需的 work_item_id 从这里拿 |
| `POST /api/work-items/{workItemId}/wakeups` | `app.Wakeup` 形状（kind=time 时后端只认 `cron_expression` 或 `interval_seconds` 二选一；**不认裸 next_fire_at**） | 201 `Wakeup`（含后端算好的 `next_fire_at`） | 指令必填 ≤4000 runes；缺 created_by → 400（wave1 修复，principal 回填） |
| `DELETE /api/work-items/{workItemId}/wakeups/{wakeupId}` | — | 204 | system_rule 行 → 400 "system rule cannot be deleted"，UI 直接不渲染删除按钮 |

## 三、门禁实际输出

| 门禁 | 结果 |
|---|---|
| `cd web && pnpm build`（vite build + tsc -b） | **通过**（chunk >500kB 警告为既有噪音） |
| `pnpm test` | **23 文件 / 188 用例全绿**（含本波新增 53 例） |
| `pnpm exec tsc -b` | 0 错误 |
| `pnpm exec eslint <5 个改动文件>` | **0 发现**（修掉一处 `react-hooks/purity`：`Date.now()` 移入 onChange 事件处理器） |
| curl 实测 | 创建（空 cron）201 → trigger 200（running）→ 运行结束后 DELETE 204（工作区已清理回 `[]`）；一次性唤醒 201（next_fire_at 正确）→ DELETE 204；坏 cron create 复现 500、cron-preview 复现 400 |

## 四、给主会话 / 后续 agent 的提示

1. **契约缺口 A（阻塞项，需后端补端点）**：`routes.go` **没有 GET triggers 列表端点**。任务书设想的「删除 409 时列出该 autopilot 的 triggers 供删除」无法如实实现（前端不发明端点），当前降级为：409 时按 message 分流显示指引文案。建议后端补 `GET /api/autopilots/{id}/triggers` → `[]Trigger`，前端再把 409 分支升级成「触发器列表 + 单删」。
2. **契约缺口 B**：`Autopilot` 列表项**不含 cron 与最近触发时间**（cron 在 trigger 上，触发时间在 runs 里）。任务书「若有」故未渲染。若要展示，建议后端在列表里 join 返回 `cron_expression` / `last_run_at`，避免 N+1。
3. **后端小刺（非本域，防丢失）**：`createAutopilot` 对坏 cron 回 500（cronx 错误没包成 `apierr.NewValidation`），建议 FixC 同族处理改 400；前端已用 cron-preview 预检兜住，但直连 API 仍会 500。
4. **删除 409 的第二形态是暂态**：手动触发后立刻 DELETE 会 409 "pending/running runs"，运行结束即可删（实测复现并清理成功）。UI 文案已提示「稍后重试」。
5. **测试基础设施备忘**：msw 同一批 `server.use(...)` 内**先注册者先匹配**——具体覆盖要放在 `...defaultHandlers()` 前面；多行列表断言用 `findAllByTestId`（`findByTestId` 多匹配会抛错）。
6. **probe 数据已清理**：p3-probe-ap 与 p3 probe wakeup 均已删除，工作区 autopilots 恢复 `[]`。
7. 改动文件（本波 P3 实际 touched）：`web/src/api/automation.ts`、`web/src/routes/_app.automation.tsx`、`web/test/components/automation-page.test.tsx`（新）、`web/src/i18n/locales/zh.ts` / `en.ts`（仅 `automation.*` 块新增 39 键）。
