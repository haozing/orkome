# 交接笔记：B2 / F1+F5 —— 任务流通用渲染器三卡 + 渲染器注册表 + {jobId} 深链变量

> 日期：2026-10-11
> 范围：仅 `web/`（Go 零改动；A3 的 _app.mcp.tsx / packs-sections.tsx / api/packs.ts 只读复用未动；task-panel.tsx 与 _app.run-logs.index.tsx 未动，自动获益）
> 需求基线：`pack-frontend-design-2026-10-11.md` §2（v1.1 审计修订版）；字段名唯一权威来源：`pack-tool-contract-design-2026-10-11.md` §2.1；包装串形状：`interfaces-output-validator-2026-10-11.md`

---

## 一、改了什么 / 为什么

### 1. TS 类型（`web/src/api/orchestration.ts`，与 TaskMessage 同居）

- `JobHandle`（`{job_id}` 形状）、`JobState`（五态枚举）、`JobStatusResult` / `JobStatusError` / `JobStatus`（§2.1 全字段，`stage_failed: 'pre_publish' | 'post_publish' | null`）。字段名严格照抄 §2.1，零发明。

### 2. 渲染器注册表 + 三卡 + 违规态（`web/src/components/task-tool-renderers.tsx`，新文件）

- **注册表是纯组件映射**：`ToolResultRenderer` 为入口，`renderShapeCard` 按序短路（JobStatus → JobHandle → Metrics），全不中落 `DefaultToolResult`（现状灰框逐字保留，作为注册表 `default` 与零回归基线）。无进程内插件机制；pack 自定义渲染器（按工具名 keyed）是 v1+ 扩展位，文件内有注释留位。
- **三卡全部按 result 形状触发，不依赖工具名**（绕开 TaskMessage 无工具名字段的事实）：
  - `JobHandleCard`：job_id（可复制，clipboard 不可用时静默降级）+ state 徽章（若有）+ "在 pack 控制台打开"深链按钮；
  - `JobStatusCard`：state 徽章 + stage 文本 + 四段式生命周期进度条（queued→running→awaiting_review→published；failed 不在轨道上，失败详情在 error 块）+ 失败时 error.code + stage_failed 标签（**post_publish 红框红字警示 / pre_publish 中性 + 提示文案**）+ result.summary + result.url 可点（target=_blank）+ 内嵌 metrics 网格（JobStatus 自带 metrics 字段，顺手画）；
  - `MetricsCard`：键值对 `<dl>` 网格，原始值 string/number/boolean 直出，对象/数组 JSON.stringify。
- **违规态（轻量，非三卡之一）** `ContractViolationCard`：对 A1 冻结格式（`tool output contract violation: <reason>` + JSON 行，`code === TOOL_OUTPUT_CONTRACT_VIOLATION`）逐行扫描 JSON 行判中，展示 title + 工具名 + reason + 原文摘录；判不中落默认灰框。

### 3. `TaskMessageRow` 改造（`web/src/components/task-messages.tsx`，改造点收敛处）

- 非 text 消息统一走 `ToolResultRenderer`；text 分支与 li/tone/label 标记逐字保留。
- **call_id 配对**：`TaskMessageList`（同文件）扫 tool_use 消息 content 首 token（后端 emit `name + " " + args`，engine.go:334）建 `call_id → 工具名` map，按 call_id 下发给各 Row。工具名只喂给 JobHandleCard 做深链 pack 匹配；形状卡不依赖它。
- 两个消费面（work-item 面板 `task-panel.tsx`、run-logs 抽屉 `_app.run-logs.index.tsx`）**零改动自动获益**。

### 4. F5：`{jobId}` 深链模板变量（JobHandleCard 内）

- `renderConsoleUrl` 与 pack-entries.tsx 的 `renderURL` 同风格（replaceAll + encodeURIComponent），追加 `{jobId}`。
- pack 匹配：工具名 `startsWith(pack + '_')`，多 pack 前缀同时命中取**最长**（防 `a` / `a_b` 二义）；查 ui-entries 复用 A3 的 `listPackEntries`，与侧栏 PackEntries 共用 queryKey `['pack-ui-entries', workspaceId]`（同端点同形状，缓存互通）。
- **ui-token 每次点击现铸**（POST /api/ui-tokens），零缓存——60s 过期设计使然；onSuccess `window.open(url, '_blank', 'noopener')`。
- workspaceId 取自 `useWorkspaceStore`（消费面不透传，故未动它们）。
- 按钮隐藏条件（三选一即隐）：无配对工具名 / 无 workspaceId / ui-entries 查不到匹配 pack。job_id 始终显示。

### 5. i18n（zh.ts / en.ts，`taskMsg.*` 命名空间）

- 新增：`copy/copied/openConsole/openConsoleFailed/stage/metrics/stageFailed/postPublishWarning/prePublishNote/violationTitle/violationExcerpt/state.{queued,running,awaiting_review,published,failed}`，中英双份。

### 6. 测试（`web/test/components/task-messages.test.tsx`，16 用例）

- ①JobHandleCard：{jobId}/{uiToken}/{workspaceId} 三变量展开 + POST body 断言（plugin/workspace_id）+ window.open 断言；copy chip；无 pack entry 时按钮隐藏且 job_id 仍在；无配对 tool_use 时按钮隐藏；可选 state 徽章。②JobStatus：failed post_publish（红框断言 border-red-200 + error.code + post_publish 标签 + url href + 内嵌 metrics）与 pre_publish（中性 border-zinc-200）；running 无 error 块。③Metrics 网格含嵌套值 JSON 化。④默认灰框零回归：未匹配形状 / truncated / 非 JSON / 包装内层非 JSON（structuredProbe 语义）/ tool_use 与 approval 现状渲染 / 带额外键的 handle 不认（契约严格性）。⑤违规态四要素 + 原文两行不被整段渲染。栈：msw + QueryClientProvider + testing-library，i18n 钉 en，全沿 A3 先例。

---

## 二、形状判定最终语义表（fail-safe：任一步不中 → 默认灰框）

入口顺序（`ToolResultRenderer`，仅 `kind === 'tool_result'` 且 `truncated !== true` 进入）：

| # | 判定 | 命中条件 | 产出 |
|---|---|---|---|
| 0 | 截断硬规则 | `truncated === true` 或 kind ≠ tool_result | 默认灰框（tool_use/approval/error 保持现状渲染） |
| 1 | 违规态 | 逐行找 `{` 开头可解析行，`code === 'TOOL_OUTPUT_CONTRACT_VIOLATION'` 且 `reason` 为 string | ViolationCard |
| 2 | JSON.parse | 整串 parse 失败 | 默认灰框 |
| 3 | 解包装 | 解析值为对象且 `content` 为数组（CallToolResult 包装）：`structuredContent` 为对象 → 用之；否则 `content[0].text` 再 parse，失败 → 灰框；`content` 非数组的对象 → 视为裸文档（防御未包装结构化输出） | 内层文档 |
| 4 | JobStatus | 对象 + `job_id` 非空 string + `state` ∈ 五态 + `stage` 为 string（必填三件套）；result/metrics/error 可选，内层字段异常按缺省处理（url/summary 非 string 忽略；stage_failed 非法值归 null） | JobStatusCard |
| 5 | JobHandle | 对象 + `job_id` 非空 string + **除 job_id/state 外无其他键** + state（若有）∈ 五态 | JobHandleCard |
| 6 | Metrics | 对象 + `metrics` 为**非空**普通对象（`{metrics:{}}` 不算——没有可渲染内容） | MetricsCard |
| 7 | 兜底 | 以上全不中 | 默认灰框（pre 原文 + truncated 注脚，与现状零差异） |

优先级说明：JobStatus 字段最全先判（其 metrics 会被 JobStatusCard 内嵌渲染）；`{job_id, state, stage}` 全的走 JobStatus，只有 `{job_id}`/`{job_id, state}` 才是 handle；带额外键的疑似 handle 一律灰框（契约严格性，测试有钉）。

## 三、验收实测

- `pnpm build`（vite build + tsc -b）：✓ built，tsc 零错误（另跑 `pnpm typecheck` exit 0 复核）
- `pnpm test`：18 files / 103 tests 全绿（新增 16）
- `pnpm lint`：仅剩预存发现（project-nav.tsx:58 error + 7 个 react-refresh warning，分布于 task-status-badge.tsx / ui/badge|button|tabs.tsx）；**本批新增/改动文件零发现**
- 触碰文件全部过 `prettier --check`（与仓库基线一致）

---

## 四、留了什么坑 / 后续注意

1. **深链按钮出现的时机**：ui-entries 是异步查询（entries 加载完成前按钮不渲染）。工具名无 `_`（内置工具）或 store 无 workspaceId 时查询根本不发（enabled=false）。若将来任务流需要在无 workspace 上下文的地方渲染，深链会静默消失——by design，非 bug。
2. **ui-token 现铸 → 连点两次会开两个 token**：每次点击一个新 POST + 新窗口。60s 过期下这是正确形态；若将来要防抖，在 JobHandleCard 的 jump mutation 前加 pending 态判断即可（现已 disabled=isPending，连点被拦，但两次独立点击各铸各的）。
3. **`{jobId}` 在 pack.yaml 模板里没有也不会报错**：replaceAll 静默无操作。agentblog 的 entry urlTemplate 若想带 job 定位，需要 pack 侧在模板里自己写 `{jobId}`——平台不注入、不校验变量存在性（与 {workItemId} 的 slot 设计一致）。
4. **JobStatus 内层字段宽松化**：schema 必填三件套（job_id/state/stage）严格，但 result/metrics/error 内层键值异常时按缺省渲染而非整体灰框（如 error.code 缺失则整个 error 块不显示）。若后端 B1 存档校验更严，前端比后端宽一档是安全的方向（不会出现前端崩、只会少显示）。
5. **state 徽章颜色映射**是平台主观选择（queued=neutral/running=default(pendingReview=warning/published=success/failed=danger)，将来若 UI 评审要换色，改 `stateBadgeVariant` 一处。
6. **工具名首 token 解析**依赖后端 emit 格式 `name + " " + args`（engine.go:334）。若后端某天把 tool_use content 改为纯 JSON 对象，配对会静默失效（深链按钮消失，三卡不受影响——它们不看名字）。配对逻辑集中在 task-messages.tsx 的 `toolNameByCallId`，一处可改。
7. **违规态依赖 A1 的文本格式**（前缀 + JSON 行逐行扫描）。若 A1 将来改错误格式（不该改——golden 已冻结），只需改 `parseContractViolation`。
8. **run-logs 抽屉的 job_status 消息流盲区**（设计 §2.2）：v0 深链出去看是既定方案；"追踪"开关轮询是 v1，本批未做（按设计延后）。
9. `test/components/task-messages.test.tsx` 里 `Object.defineProperty(navigator, 'clipboard', ...)` 是 jsdom 无 clipboard 的 workaround，真实浏览器不受影响。
