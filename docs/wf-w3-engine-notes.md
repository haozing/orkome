# W3 引擎侧实施注记（阶段简报注入 + 事件驱动 E2E）

> 日期：2026-10-13。波次契约：docs/wf-interfaces-frozen.md（§ 编号均指该文）。
> 设计：docs/project-workflow-design-2026-10-13.md（下称"设计"）。
> W2 交接：docs/wf-w2-notes.md（"给 W3 的交接"段为直接输入）。

## 做了什么

### 1. 阶段简报注入（engine 侧，设计 §3"engine 侧唯一的改动点"）

- **internal/orchestration/adapters/engine/workflow.go（新文件）**：
  `stageBriefing(task orchapp.Task) string`——
  - `task.Context["trigger"] != "workflow_stage"` → 空串（非流程任务零影响）；
  - `stage_exit_status` 空/空白 → 空串（无出口态的简报是残缺指令，收尾义务
    无法表述——宁可简报缺席，不给执行者错误暗示）；
  - 完整 context → 英文骨架简报（与 squadBriefing 同语言策略）：
    `[Workflow Stage Briefing]` + 阶段名/序号 + Assignment（instruction 原文
    嵌入不翻译，空指令渲染 `—` 占位）+ Closing duty（`update_work_item_status`
    打出口态 → 自动进下一阶段）+ 不建跟踪子单的克制条款。
  - context 字段名逐字对齐契约 §5（消费锚：workapp 单测
    TestWorkflowDispatch_ContextShapeContract + W2 集成 Test 1 SQL 断言）。
- **engine.go：一处拼接（原 squadBrief 行原地扩展）**：
  ```go
  stageBrief := stageBriefing(task)
  user := strings.TrimSpace(assembleUserPrompt(task, history, session) + "\n" + squadBrief + "\n" + stageBrief)
  ```
  无新 import（strings 已有）。工具面/模型调用/注册表零改动。
  squad 执行者场景两个简报共存（leader 既看小队简报也看阶段简报），
  顺序：base prompt → squadBrief → stageBrief。
- **workflow_test.go（新）**：表驱动 7 例（完整注入逐句断言 / 缺 trigger /
  非 workflow trigger / 缺 exit_status / 空白 exit_status / nil context /
  空指令占位符）+ 拼接顺序 1 例（squadBrief 与 stageBrief 共存次序）。

### 2. 真链路 E2E（机制层；cmd/orkome-server/wiring_workflow_e2e_test.go，新）

`TestWorkflowE2E`，TEST_DATABASE_URL 门禁 skip 模式（建库 orkome_w3e2e，
全量迁移 + 逐表 TRUNCATE）。与 W2 集成测试（直调 TryAdvance）的分工：
**补 eventbus 事件驱动路径的装配证据**。

- 装配：真 `wiringWorkflow` 一行调用（真实 eventbus 双门铃订阅）+
  真 WorkflowService（emit=bus、outbox/uow 与 wiring_work.go 同款）+
  EnqueueService 同构构造。
- 链路：Start → 断言第一棒任务落库且 context 逐字段 == §5（trigger/
  workflow_id/stage_id/stage_name/stage_position/stage_exit_status/
  instruction/work_item_title）+ assignee 投影 → `decodeOutboxPayload`
  对 work_item.updated 出 WorkItemPayload 的解码面证据（§4.1 注记）→
  任务置 completed + 工单打到出口态 → **`bus.Emit(work_item.updated)`**
  → 断言 stage 2 光标（StagePosition=2/AdvanceCount=1/无 finished_at）+
  agent2 新任务（context §5、position=2）+ assignee 换人 + status 不被
  改写（§3.2 不变量）。
- outbox 用空转假体（Append/MarkPublished no-op）：relay 只是 bus.Emit 的
  重试投递器，监听器消费的 Envelope/Payload 形状一致，事件驱动路径等价。
- dev 环境：dev server 未起（任务书明示 E2E 走 DB 层）；实跑用
  orkome-test-pg（54329）；容器 orkome-pg（55432）在跑未动。

## 装配缺口上报（W2 遗留，超出 W3 文件清单，未修）

**生产派发路径不产生 §5 context**：

- `wiring_workflow.go` agent 分支 = `enq.EnqueueAssignment(...)`；
- `internal/orchestration/app/service.go:598` 组装 `{"trigger":"assignment",
  "actor":...}`（+ 可选 work_item_title），**无 workflow_stage 任何字段**；
- 后果链：① engine 的 stageBriefing 永远不命中（trigger 恒为 assignment）；
  ② `TryAdvance` §4.3 存在性谓词（`context->>'stage_id'`）永远不匹配——
  阶段任务无法背书推进，**流程卡死在第一棒之后**（仅 none 阶段靠人工背书
  可推）；③ 设计 §3"引擎在调用前包装 context"未在装配层落地。

W2 notes 说"生产实现 = wiring_workflow.go agent 分支（wiring_workflow.go:63-70
附近，实际行号搜 `workflow_stage`）"——该处实为通知 type 字符串与 dispatcher
分支，不是 context 组装；W2 集成 Test 1 是以同构 SQL 假体锚定形状的（其自身
注释也说明"派发走 wiring 同构 SQL"），生产路径未经集成验证。

**修复建议**（留给主会话裁决，任选其一，改动都在 cmd 层）：

1. wiring 的 dispatcher agent 分支在 `EnqueueAssignment` 返回 `queued` 后，
   按 `st.StageID` 回填任务 context（需 repo 追加一个窄 UPDATE）；
2. 或 EnqueueService 加带 context 参数的变体入口，wiring 传入 §5 map
   （`trigger:"workflow_stage"` + 7 字段）——语义干净，推荐。

本波 E2E 对该接缝的处理与 W2 一致：dispatcher 注入位以 §5 同构 SQL 落库
（测试内注释说明理由），锚定的是事件驱动推进链与 §5 形状，不让缺口被假体
掩盖——缺口本身在上报中独立陈述。

## 给后续的交接

- **stageBriefing 的消费前提**：上表缺口修复前，简报在真环境不会触发；
  修复后 engine 侧零改动即生效（trigger 判型已在位）。
- **instruction 占位符**：设计 §1 的 `{{title}}/{{number}}` 渲染归派发层
  （W2 集成 Test 1 断言原样透传"渲染归 engine 侧 W3，不在派发层"——该
  注释与设计原文矛盾，按契约执行：engine 原样嵌入 instruction，占位符
  渲染仍无人做，如需此能力在缺口修复时一并裁决）。
- **测试复跑**：`wiring_workflow_e2e_test.go` 每次重建 orkome_w3e2e（~45s）；
  与 W2 的 orkome_m85 建库互不冲突（库名隔离），可并行跑。
- **E2E fixture 复用**：newWfE2EFixture 是独立自包含的（不依赖
  wiring_automation_integration_test 的 wakeChainFixture）；若后续波次
  需要事件驱动链，直接挂这个 fixture。

## 门禁输出（全部绿）

```
go build ./...                          → BUILD_OK
go vet ./internal/orchestration/... ./cmd/... → VET_OK（无输出）
go test ./internal/orchestration/... ./internal/work/... ./cmd/... -count=1 →
  ok internal/orchestration/adapters/engine    3.607s（含 TestStageBriefing_* 新 8 例）
  ok internal/orchestration/adapters/http      2.450s
  ok internal/orchestration/adapters/pgrepo    1.901s
  ok internal/orchestration/adapters/workers   2.415s
  ok internal/orchestration/app                1.485s
  ok internal/orchestration/domain             0.968s
  ok internal/work/adapters/http               1.848s
  ok internal/work/adapters/pgrepo             1.320s
  ok internal/work/adapters/workers            1.127s
  ok internal/work/app                         0.866s
  ok internal/work/domain                      0.707s
  ok cmd/orkome-server                         3.141s（plain，无 TEST_DATABASE_URL）
  ok cmd/pack-provision                        2.289s
TEST_DATABASE_URL='postgres://postgres:orkome_e2e@localhost:54329/orkome?sslmode=disable'
  go test ./cmd/orkome-server/ -run TestWorkflowE2E -count=1 -timeout 15m →
  ok orkome/cmd/orkome-server 20.081s（TestWorkflowE2E PASS，~45s 首跑含迁移）
```

已知噪音未修：sonic go1.29 fallback 警告、CRLF gofmt 全量误报（任务书豁免）。

## 文件清单（W3 引擎侧全量）

- internal/orchestration/adapters/engine/workflow.go（新：stageBriefing）
- internal/orchestration/adapters/engine/workflow_test.go（新：表驱动单测）
- internal/orchestration/adapters/engine/engine.go（一处拼接，无新 import）
- cmd/orkome-server/wiring_workflow_e2e_test.go（新：事件驱动 E2E）
- docs/wf-w3-engine-notes.md（本文件）

禁碰清单遵守：tools_*.go / wiring_workflow.go / main.go / pack-provision / web/** 零改动；
internal/work/** 只读。
