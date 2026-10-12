# Pack 三文档实施总交接（2026-10-11 · 全四波收官）

> 输入：`pack-design-audit-2026-10-11.md`（审计）→ `pack-tool-contract-design` / `pack-frontend-design` / `dsh-borrowing-table-v2`（v1.1 修订基线）→ `pack-rollout-plan-2026-10-11.md`（四波排期）
> 执行方式：多 agent 按"波内并行、波间串行；接口先行冻结；共享文件唯一归属"纪律推进，波间与终局均由主会话合并复核。
> 状态：**四波全部完成，终局门禁全绿**（见 §4）。工作区未提交——89 个改动文件等待人工 review 后 commit。

---

## 1. 波次执行一览

| 波 | 工作流 | 交付 | 交接笔记 |
|---|---|---|---|
| 1 | A1 输出契约强校验 | `OutputValidator` 冻结接口 + 两级实现（协议自描述 schema → 结构化 JSON 探测）+ 违规路径（`TOOL_OUTPUT_CONTRACT_VIOLATION` 标准错误 + 审计日志）+ fake MCP server 端到端测试 | handoff-a1-output-contract |
| 1 | A2 七段 summary | summary 生成从 repo 层上移 service 层；dsh 七段 checkpoint 提示词搬运（domain/checkpoint.go，MIT 署名）；开关 `ORKOME_SUMMARY_LLM_ENABLED` 默认关，关=byte-identical | handoff-a2-summarizer |
| 1 | A3 Packs 页（F4） | `_app.mcp.tsx` 三段式：Packs 只读列表 / **ws 级 MCP 注册表（新写）** / 客户端接入；`packs.*` i18n 中英 | handoff-a3-packs-page |
| 2 | B1 安装闸门 + 1035 | provision 双版本放行 v0/v1；三闸门（前缀 / output 自描述或声明 / job 三件套）；`pack_tool_schema` 存档表 + 读取 API | handoff-b1-job-gate |
| 2 | B2 渲染器三卡（F1+F5） | TaskMessageRow 渲染器注册表 + JobHandleCard / JobStatusCard / MetricsCard + 违规态卡；`{jobId}` 深链；call_id↔tool_use 配对；两消费面零改动自动获益 | handoff-b2-renderers |
| 3 | C1 统一注册表 | engine 装配段纯搬移进注册表；9 内置工具补 OutputSchema 受 A1 校验；middleware 走 eino 原生 `WrapInvokableToolCall`；**ConcurrencySafe fail-closed 实装**（未声明不并行，run 级串行闸）；`task.context.tools` 快照（兼容读旧键） | handoff-c1-registry |
| 3 | C2 slots 通道（F2+F3）+ 1036 | `pack_ui_entry` 扩 `slots jsonb`；provision 解析 ui.slots（v0 显式丢弃保不变）；frame-ancestors 探测（--frame-origin 豁免开关）；PackSlotFrame（现铸票/resize/深链兜底）+ work-item tabs zod 四触点 | handoff-c2-slots |
| 4 | D1 agentblog MCP server | 六工具全对接真实管线（serp 评估/知识检索/CreatePlan→PollOnce 生成循环/cms 审核）；outputSchema 全协议自描述；job 五态状态机 + 副作用分型 + seed+workspace 幂等 | agentblog 仓 doc/pack-mcp-jiaojie |
| 4 | D2 安装联调 | `packs/agentblog/pack.yaml`（首个真实 v1 pack）；真实握手过三闸门 + 存档 source=protocol×6 + 引擎桥接快照 ok/6 工具 | handoff-d2-install-bridge |
| 4 | D3 事件归档 | agentblog `/hooks/orkome`：HMAC 验签（rotate 双头）+ ±5min 防重放 + 终态过滤（done/cancelled→归档）+ event_id 幂等 | agentblog 仓 doc/pack-hooks-jiaojie |
| 4 | D4 端到端金线 | 八步金线测试（安装→派发→组装→员工回合→管线→收割→slot 数据面→归档）；3/3 稳定，主会话亲验 46s PASS | handoff-d4-golden-thread |

冻结接口文档（后续开发据此协作，不读实现）：`interfaces-output-validator` / `interfaces-job-handle` / `interfaces-tool-registry`（均 2026-10-11）。

## 2. 关键修正（实施中发现并修复的真缺口）

1. **work 服务 outbox 未接线**（D4 发现）：`SetOutbox` 缺失导致 `work_item.updated` 无 EventID，webhook 扇出直接丢弃——已修，D3/D4 的归档链路因此才通。
2. **e2e stack bus 字段与 TRUNCATE 清单**：webhook 扇出 handler 需 `On(PhaseProject)`；1035 新表漏进 TRUNCATE 守卫——均已补。
3. **agentblog PG 形态双挂 panic**（D1 遗留，D3 修）：`/mcp/pack` 双挂 ServeMux panic，已幂等化。
4. **C2 会话超时但交付完整**：agent 汇报阶段中断，工作区文件与交接笔记齐全，主会话验收确认。
5. **审计时遗留的两处环境噪音**（非代码问题，验收时忽略）：golangci-lint 的 stdlib `internal/goarch` typechecking error（本机二进制旧于工具链）；web lint 的 `project-nav.tsx:58` 预存 error（基线 9f574ed 上已存在）。

## 3. 迁移与配置变更

- **迁移 1035** `pack_tool_schema`（B1）：UNIQUE(workspace_id,pack,server,tool_name)，source∈{protocol,pack_yaml,builtin_default}。
- **迁移 1036** `pack_ui_entry.slots jsonb`（C2）：NULL=无 slots，List 恒返回 `[]` 非 null。
- **新 env**：`ORKOME_SUMMARY_LLM_ENABLED`（A2，默认关）；agentblog 侧 `AGENTBLOG_MCPACK_TOKEN`（MCP 方向）与 `AGENTBLOG_HOOK_SECRET`（webhook 方向，凭证分离）。
- **provision 新 flag**：`--frame-origin`（空=第一方豁免探测）。

## 4. 终局门禁（主会话亲验，2026-10-11）

| 门禁 | 结果 |
|---|---|
| `go build ./...` | ✅ |
| `go test ./... -count=1` | ✅ 49 包 ok，0 FAIL |
| `golangci-lint run` | ✅ 0 issues（goarch 报错为环境噪音） |
| web `pnpm build` / `pnpm test` / `pnpm lint` | ✅ / ✅ 116/116 / ✅（仅预存 project-nav.tsx:58） |
| DB 套件（TEST_DATABASE_URL, `PG|Pgmigrate`） | ✅ 0 FAIL |
| 金线 `TestD4_GoldenThread`（ORKOME_AGENTBLOG_DIR + DB） | ✅ 46s，八步全断言 |
| `TestAgentblogE2E`（D2 联调） | ✅（同 env 门控） |

## 5. 留给下一步的坑（按优先级，详文见各 handoff）

1. **需真人验证**（金线只到数据面）：slot tab 浏览器真渲染、三卡真渲染、真模型中文任务、awaiting_review→published 审核闭环、webhook 控制台投递页。
2. **agentblog fakegen 中文种子词 bug**：非 ASCII 种子 100% 导致 outline JSON 损坏（按 byte 而非 rune 生成 title）——真模型中文演示前必须修（agentblog 仓，D4 笔记有最小修复建议）。
3. **mcp-go v0.43.0 vendored 版连接池竞态**：复用已关闭 SSE 空闲连接偶发 90s 挂起（D2 发现）；生产长会话缓存基本不受影响，建议升级 mcp-go 或独立 Transport（engine 包）。
4. **toolsearch 检索工具未登记 ConcurrencySafe** 被过度保守串行（只读无副作用，登记即并行）；MCP 工具 ConcurrencySafe 声明通道留 v1.1+。
5. **A2 真调用冒烟**：LLM 路径仅 fake 单测，开真 key 后核对中文 checkpoint 是否被 "English prose" 规则带偏；轨迹窗口 24k rune 是估值待校准。
6. **D1 快照停进**：awaiting_review 停进后人工审核结果不回流旧 job_status（需 Review 时实时 derive）。
7. **B1 存档读路径端到端未演练**（六工具全自描述，schema 全来自实时握手）——首个用 pack.yaml 声明 schema 的 pack 出现时补。
8. 自定义工单状态 key 若归终态类，webhook payload 无 category 不可识别（v0 取舍）。

## 6. 提交建议

两仓均未 commit（纪律要求）。建议分仓提交：
- **orkome**：89 个文件（Go 后端 + web 前端 + 8 份 handoff + 3 份冻结接口 + packs/agentblog + 2 迁移）。可按波拆 commit（A→B→C→D）或整体一个 feature commit；`git add` 前注意 CRLF 提示是仓内既有惯例。
- **agentblog**：mcp/pack 全套 + full-server 挂载 + 2 份交接文档。
