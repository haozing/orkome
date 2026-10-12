# FP-05 功能点审计：工单详情·任务面板与消息流（task-panel / TaskMessageList / 三卡渲染器 / realtime）

> 日期：2026-10-12
> 范围：只审任务面板组装、消息流渲染管线、三卡渲染器注册表（B2 四波交付物）、WS task:* 失效链、两个消费面一致性。
> 契约基线：`docs/pack-tool-contract-design-2026-10-11.md` §2.1（JobStatus schema 唯一权威）+ `docs/handoff-b2-renderers-2026-10-11.md`（形状判定语义表 #0-#7）+ `docs/inventory-api-2026-10-12.md` §13.6。
> 方法：全链代码梳理（前端两消费面 → api 层 → HTTP handler → pgrepo → eventpipe/engine emit → WS hub）+ 契约逐字段复核 + dev 库实测（curl 造数 + 端点行为验证）+ 前端 16 用例复跑。
> 结论一句话：**三卡渲染与契约对齐质量高（逐字段零漂移、fail-safe 兜底严密），但消息流拉取存在一个长任务尾段不可见的 P1 分页盲区，且 realtime 链有三处一致性缺口（interim 帧不截断、外部回写路径无帧、失效链无节流）。**

---

## 1. 全链梳理（只读对照，证据链）

### 1.1 写入侧（engine → DB → WS）

```
engine.go:330-345 事件循环
  ├─ Tool 角色消息 → pipe.emit("tool_result", toolCallID(msg), msg.Content)   (engine.go:333)
  └─ Assistant 消息 → 每个 tool_call: pipe.emit("tool_use", tc.ID, name+" "+args) (engine.go:338)
                      + text: pipe.emit("text", "", msg.Content)              (engine.go:342)
eventpipe.go:102-125 emit()
  ├─ tool_use/tool_result 先 flushBoundary() 冲刷缓冲 text（interimThrottle）
  ├─ p.interim.add(kind, content)        ← ⚠ 全文未截断进 WS interim 路径（P1-2）
  ├─ >2000 rune 截断（rune 语义正确）+ truncated 标记（eventpipe.go:113-116）
  ├─ AppendTaskMessage 落库，max(seq)+1（repo.go:738-746）
  └─ 成功 → ProgressPush → main.go:137-141 广播 task:progress {stage,call_id,seq,task_id}（工作区 scope）
interimThrottle（32 块/500ms 窗口）→ main.go:131-136 广播 task:message {kind,content,task_id}
```

- `events.TaskMessage`（"task.message"，contract/events/events.go:106）**无任何 emit 方**，仅存在于 decodeOutboxPayload（main.go:354-356）与 webhook catalog——死契约条目（P3-13）。
- 外部 agent 回写路径（MCP `report_progress`/`complete_task`）走 `ExternalTaskService.ReportMessage`（service_external.go:107-110）**直写 repo，不经过 eventPipe**：无 task:progress 帧、恒 `truncated=false`、无 2000 rune 上限（P2-3）。

### 1.2 读取侧（两个消费面 → 同一端点）

| 消费面 | 文件 | 任务列表 query | 消息 query |
|---|---|---|---|
| work-item 面板 | web/src/components/work-item/task-panel.tsx:40-43,120-123 | `['work-item', wi, 'tasks']` | `['task', taskId, 'messages']` |
| run-logs 抽屉 | web/src/routes/_app.run-logs.index.tsx:237-240 | `['workspace-tasks', …]` | `['task', taskId, 'messages']` |

两消费面共用 `listTaskMessages`（api/orchestration.ts:248-252）→ `GET /api/tasks/{taskId}/messages?limit=200`（routes.go:88,317-357）→ `ListTaskMessages`（repo.go:748-781）。

### 1.3 realtime 失效链

- 后端：`task:progress`（每条落库消息一帧）+ `task:message`（interim 节流帧）均广播到 **workspace scope**（main.go:131-141）；envelope 事件 `task.completed` 等经 wiring_ws.go:61-78 同时投 workspace + task scope（scopedTarget，wiring_ws.go:107-118）。
- 前端：main.tsx:16 规则 `/^task:/` → invalidate `['work-item']` `['task']` `['workspace-tasks']` 三前缀 → refetch。桥接实现（ploykit ws-query-bridge.ts）逐帧同步 invalidate，**无 debounce**（P2-4）。
- 客户端经 `/ws?workspace_id=` 握手自动加入 workspace scope（realtime/client.ts）——消息帧可达；`useWsScope('task', task.id)`（task-panel.tsx:119）只对 task:completed 等状态帧有意义，对消息帧冗余（无害，P3-14）。

---

## 2. 契约对齐：三卡判定 vs 契约 §2.1 与 B2 语义表逐条复核

对照物：task-tool-renderers.tsx 与 handoff-b2-renderers §二 语义表（#0-#7）。**逐条核对结论：全部一致，零漂移。**

| # | 语义表条目 | 实现 | 判定 |
|---|---|---|---|
| 0 | `truncated===true` 或 kind≠tool_result → 默认灰框 | task-tool-renderers.tsx:452-454 | ✓（硬规则在入口短路，任何卡不得越界） |
| 1 | 违规态：逐行找 `{` 开头可解析行，code 匹配 + reason string | parseContractViolation（:50-68） | ✓；且与 A1 冻结格式源头核对一致：output_validator.go:87 `"tool output contract violation: " + reason + "\n" + JSON`，excerpt 500 rune 经 json.Marshal 转义保证单行（逐行扫描可靠） |
| 2 | JSON.parse 失败 → 灰框 | :457-462 | ✓ |
| 3 | CallToolResult 解包装：structuredContent 对象优先 → content[0].text 再 parse → 失败灰框；content 非数组视为裸文档 | unwrapToolResult（:74-87） | ✓；与后端校验器 unwrapCallToolResultDocument（output_validator_pipeline.go:109-135）语义同向，前端比后端宽一档（`first.text` 只查 string 不查 type==='text'），方向安全（只多显示不崩） |
| 4 | JobStatus：job_id 非空 string + state ∈ 五态 + stage string 三件套；result/metrics/error 内层宽松缺省 | parseJobStatus（:95-118） | ✓ 逐字段对照 §2.1 schema：五态枚举一致；stage_failed 合法值白名单 + 非法归 null；error 无 code 时整个 error 块缺省（handoff 坑#4 预告的宽松化） |
| 5 | JobHandle：恰好 {job_id} + 可选 state ∈ 五态；带额外键不认（契约严格性） | parseJobHandle（:122-133） | ✓（测试钉死："handle with extra keys → 灰框"） |
| 6 | Metrics：metrics 非空普通对象 | parseMetrics（:136-140） | ✓（空对象不算，无可渲染内容） |
| 7 | 全不中 → 灰框（pre 原文 + truncated 注脚），零回归基线 | DefaultToolResult（:209-219） | ✓ |

**优先级顺序**（JobStatus → JobHandle → Metrics，:435-440）符合语义表说明（字段最全先判；带 metrics 的 JobStatus 由 JobStatusCard 内嵌渲染）。

### 2.1 首 token 解析（call_id ↔ tool_use 配对）鲁棒性

- 后端 emit 格式 `name + " " + args`（engine.go:338）。
- 前端 `content.trim().split(/\s+/, 1)[0]`（task-messages.tsx:14）。核查边界：
  - 工具名含空格：OpenAI 风格函数名约定无空格（`^[a-zA-Z0-9_-]+$`），现网 pack 前缀工具名不可能含空格——**安全**；
  - args 为空：content = `"name "` → trim → 首 token = name——**安全**；
  - name 为空（异常）：首 token 变 args 首段（如 `{"a"...`），map 存垃圾名 → findPackEntry 前缀匹配失败 → 深链按钮隐藏，三卡不受影响（形状卡不看名字）——**安全降级**；
  - 无 call_id / 重复 call_id：map 后写覆盖前写，配对取最后一次 tool_use；eino 单轮 tool_call id 唯一，无实际影响。
- 配对 map 构建 O(n) 单次遍历 + useMemo（task-messages.tsx:10-18,63）——**无性能问题**。

### 2.2 {jobId} 深链与 ui-token 现铸

- `renderConsoleUrl`（:143-148）：{uiToken}/{workspaceId}/{jobId} 三变量 replaceAll + encodeURIComponent，与 pack-entries.tsx renderURL 同风格。**单测级核查通过**（task-messages.test.tsx:90-97 断言三变量展开 + POST body `{plugin:'agentblog', workspace_id:'ws-1'}` + window.open `'_blank','noopener'`）。
- pack 匹配最长前缀（:151-158，防 `a`/`a_b` 二义）✓；queryKey `['pack-ui-entries', workspaceId]` 与侧栏共享缓存 ✓。
- ui-token 每点击现铸（mutation 内 POST，零缓存）✓ 实测：POST /api/ui-tokens `{plugin,workspace_id}` → 201 `{token, expires_in:60}`（JWT，60s）。
- **文档漂移（P2-7）**：inventory-api §13.6 写 body 为 `{"pack": …}`，后端实际收 `plugin`（handlers_uitoken.go:18-25）——按文档对接 400 实测复现。前端（JobHandleCard + PackEntries）都用 `plugin`，代码侧自洽，**是文档错**。

### 2.3 i18n

`taskMsg.*` 18 键 + `state.*` 5 子键，zh.ts:680-704 / en.ts:716-741 **完全对称，无缺键**；组件实际使用的全部键（含 runLogs.loadingMessages/noMessages）均存在。

---

## 3. 实测记录（dev 环境，curl + 直查 orkome-pg）

测试数据：work item `盘点验证工单-20261012`（3b8bc342…）下造两个任务（fp05 前缀）：
- `11111111-…-1101`：9 条消息覆盖 JobHandle（含配对 tool_use）/ JobStatus failed post_publish / Metrics / 违规文本 / truncated / 纯文本非 JSON；
- `11111111-…-1102`：220 条 text 消息（分页盲区验证）。

| 验证点 | 结果 |
|---|---|
| GET messages 默认形状返回 | ✓ 字段形状与前端 TaskMessage 接口一致（call_id omitempty 正确） |
| kind 过滤 `?kind=tool_result` | ✓ 6 条全 tool_result |
| 非法 kind / before_seq=abc | ✓ 均 400 Validation |
| 跨工作区隔离（伪造 X-Workspace-Id） | ✓ E_FORBIDDEN |
| ui-tokens `plugin` body | ✓ 201 + 60s token |
| ui-tokens `pack` body（按 API 文档） | ✗ 400 "plugin and workspace_id required"（文档漂移实锤） |
| **默认分页（limit=200 无 order，即前端实际调用形状）** | **✗ 返回 seq 1..200（最旧 200），seq 201-220 不可见**（P1-1 实锤） |
| `order=desc` + `next_before_seq` | ✓ 正常（220→21 + 游标），但前端从未使用 |
| 前端 16 用例复跑 | ✓ 16 passed |

数据面到渲染面的映射（灰框/截断/违规的呈现）由 16 用例钉死并复跑通过；DB 侧截断语义由 eventpipe_test.go:170-171 钉死（>summaryMax → truncated=true 且内容恰为 2000 rune）。

---

## 4. 缺陷清单

> 计数：P0 × 0，P1 × 2，P2 × 6，P3 × 6。无阻断级缺陷；P1-1 对"重任务"场景是用户可感知的功能缺失。

### P1

**P1-1 消息流分页盲区：>200 条任务尾段（含最终输出）在两个消费面均不可见**
- 证据链：web/src/api/orchestration.ts:248-252（`listTaskMessages` 恒 `limit=200`，不传 order/游标）→ routes.go:323（默认 `TaskMessagesQuery{Limit:200}`，仅 `order=desc` 时 Desc）→ repo.go:761-766（ASC `LIMIT 200`）。实测：220 条任务返回 seq 1..200，seq 201-220（收尾消息）永不可见。摘要服务读的是 `Desc:true` 最新 200（service_summary.go:129-130）——**摘要看得到结尾、UI 看不到**，同一数据两个窗口。
- 影响：长 agent run（工具调用频繁时 200 条并不难达到）在工单面板和 run-logs 抽屉都看不到执行尾段与最终 tool_result；`task.result` 字段仍有兜底显示（TaskCard 面板），故定 P1 而非 P0。
- 修复建议（前端为主，零后端改动）：`listTaskMessages` 改为 `order=desc&limit=200` 拉最新 200 后在 UI 层反转为升序渲染；后续需要翻历史时用 `next_before_seq` 游标上翻。或后端把默认 order 改 desc（对齐摘要服务），前端同样反转。

**P1-2 WS `task:message` interim 帧载荷不截断：MB 级工具结果整包广播**
- 证据链：eventpipe.go:109 `p.interim.add(kind, content)` 传**全文**（DB 路径在 :113-116 才截 2000 rune）→ main.go:131-136 `json.Marshal` 原样 → Broadcast 到工作区**全部成员**的客户端。eventpipe_test.go:178 甚至钉死了该行为（interim 帧含 `summaryMax+5` 全文）。
- 影响：大结果（如整页 HTML/长 JSON）下 WS 帧可达 MB 级，多成员工作区放大 N 倍带宽；而前端根本不消费该 payload（只按帧类型 invalidate），纯属浪费。
- 修复建议：interim 路径复用同一 summaryMax 截断（或干脆 interim 帧不带 content——前端不读它；若为未来"流式预览"保留，则截 2000 rune 与 DB 对齐）。

### P2

**P2-1 外部回写路径（MCP report_progress / complete_task）绕过 realtime 与截断纪律**
- service_external.go:107-110 直写 `AppendTaskMessage(…, truncated=false)`：无 2000 rune 上限（外部 agent 可写任意长消息入库，前端灰框全量渲染）；写完**不推任何 task:* 帧**——前端要等 task:completed/failed 事件帧才 refetch，运行中 `report_progress` 的消息在 UI 上不实时出现。realtime 链与 DB 的一致性缺口。
- 修复：ReportMessage/Complete 复用截断常量（2000 rune），并在落库成功后走与 ProgressPush 同型的广播（把 ProgressPush 下沉为共享 sink 装饰器，而非 engine 私有字段）。

**P2-2 WS 失效链无节流，运行期 refetch 风暴**
- main.tsx:16 每帧 invalidate 三个前缀；task:progress 逐消息一帧 + interim 500ms 一帧 → 高频 run 下每秒多次全量 refetch（每次 GET 拉 200 条全量 content）。桥接层（ploykit ws-query-bridge.ts）逐帧同步执行、无 debounce/合并。
- 修复：桥接或规则层加 ~300-500ms trailing debounce；或对 `['task', id, 'messages']` 单独用 fetchQuery 节流。

**P2-3 emit() 持久化失败静默吞掉**
- eventpipe.go:119-121 `if err != nil { return }`：无日志、无 metrics、无 progress 帧。消息静默丢失无任何观测点（seq 唯一约束冲突、DB 抖动都归于此）。配合 P2-4 的 max(seq)+1 并发窗口（repo.go:738-746，引擎 emit 与外部 ReportMessage 并发时 UNIQUE(task_id,seq) 冲突），是真实可达路径。
- 修复：失败时至少 `slog.Warn`（带 task_id/kind/err）；seq 改用 DB sequence 或 `INSERT … ON CONFLICT DO NOTHING` 重试。

**P2-4 深链 `window.open` 在异步 mutation onSuccess 中调用，可能被弹窗拦截**
- task-tool-renderers.tsx:400（onSuccess → window.open）与 pack-entries.tsx:37（同款既有模式）：POST /api/ui-tokens 回来后才 open，已脱离用户手势调用栈，Chrome/Safari 弹窗拦截器可拦——用户点按钮无反应且无提示（jump.isError 只在请求失败时出现，open 被拦截不算 error）。
- 修复：onClick 同步先 `const w = window.open('', '_blank')`，mutation onSuccess 后 `w.location.href = url`（失败则 w.close()）。

**P2-5 API 文档漂移：ui-tokens body 字段名**
- inventory-api §13.6 写 `{"pack": …}`；后端 handlers_uitoken.go:19 实收 `plugin`。按文档对接 400（实测复现）。改文档即可。

**P2-6 16 用例缺"真实 eino-ext CallToolResult 样例"钉子**
- A1 契约文档明确要求"形状判定按包装层写，实现时先真跑抓样例"（pack-tool-contract §2.2 ★注）；仓内测试的 `wrap()` 是理想化构造（content 单块 + structuredContent 齐全）。真实 mcp-go 序列化可能含 `isError`、多 content 块（前端只取 content[0]）、meta 等额外键——未钉。其余盲区：JobStatus 缺 stage 落灰框、content 数组多块、200 条长列表渲染、配对 map 大量 call_id。
- 修复：真跑一次 MCP 工具抓原始包装串存为 fixture（含 isError:true 与多块 content 变体），补 3-4 个用例。

### P3

**P3-1 两消费面渲染不一致**：`task.result` 面板用 Markdown（task-panel.tsx:179）而抽屉用 `<pre>`（run-logs:285）；failure_reason 抽屉显示（run-logs:267-280）面板不显示；面板 messages 查询无 isError 分支（task-panel.tsx:185-191），加载失败时静默空白（抽屉有 messagesFailed 提示）。

**P3-2 每次渲染重复 parse**：renderShapeCard 判定后卡片内部再 parse 一遍（task-tool-renderers.tsx:435-440 vs :289/:380/:274），一条消息两次 JSON.parse × 判定。量大时可感知，建议 parse 一次传结果。

**P3-3 长列表无虚拟化**：TaskMessageList 一次渲染 200 条（task-messages.tsx:62-75），text 走 Markdown 渲染开销高；配合 P2-2 的风暴，重 run 时面板明显卡顿。

**P3-4 `limit` 参数静默钳位**：HTTP 层收任意 n>0（routes.go:324-327），repo 层 >500 一律钳回 200（repo.go:750-752）——`limit=500` 生效、`limit=501` 得 200，无文档无提示。

**P3-5 `events.TaskMessage` 死契约**：常量 + decodeOutboxPayload 分支（main.go:356）+ webhook catalog 存在，但全仓无 emit 方。误导对接方以为有逐消息 outbox 事件；实际 realtime 走 task:message 直推帧。

**P3-6 `useWsScope('task', task.id)` 对消息帧无效**：task:message/task:progress 只广播 workspace scope（main.go:131-141），task scope 只收状态帧（wiring_ws.go:107-118）。订阅无害但注释/语义易让人误以为它承载消息实时。

---

## 5. 测试覆盖评估

- **前端 16 用例（web/test/components/task-messages.test.tsx）**：结构好——三卡正例、post_publish 红/pre_publish 中性、契约严格性（额外键 handle 不认）、四类灰框兜底、违规四要素、深链三变量展开 + POST body 断言。复跑 16/16 绿。盲区见 P2-6。
- **后端 eventpipe（eventpipe_test.go）**：throttle 表驱动（首帧直发/32 块/500ms/边界冲刷/tail）+ 落库截断钉子 + 失败不推 progress——质量高。缺口：①DB 写失败时 interim 帧仍已广播（emit 顺序 add→append，:109-117），无组合断言；②emit 吞错路径无日志断言。
- **HTTP 端点（b2_integration_test.go:213-248）**：覆盖基本读写；缺 >limit 分页/游标行为与 500 钳位断言（正是 P1-1 藏身处——后端测试只测了 ≤200 的世界）。

## 6. 修复优先级建议

1. **先修 P1-1**（前端 ~15 行：desc 拉取 + 反转渲染，后端零改动即可收敛）——这是唯一用户可感知的功能缺失。
2. **P1-2 + P2-3 一并修**（interim 截断一行 + 吞错加日志），都在 eventpipe.go 同一函数。
3. P2-1/P2-2 属 realtime 打磨，建议与"追踪轮询 v1"（handoff-b2 坑#8）一并设计。
4. P2-5 改文档五分钟；P2-4 与 PackEntries 同款模式统一修。
5. P2-6 抓一次真实样例固化 fixture。

## 7. 遗留测试数据（fp05 前缀，未清理）

dev 库 `orkome-pg`：任务 `11111111-1111-4111-8111-111111111101`（9 条消息，三卡/违规/截断样本）与 `11111111-…-1102`（220 条，P1-1 复现样本），挂在工作项 `盘点验证工单-20261012`（3b8bc342-d79a-4a3f-be4e-5868ca976465）下。保留供修复验证；不需要时按 task_id 删除即可（task_message 级联）。
