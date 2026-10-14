# Project Workflow —— 项目级流程：让工作项真正流转起来

> 日期：2026-10-13
> 状态：设计定稿（全文断言经代码逐条验证，验证记录见 §10）
> 原则：**无兼容包袱、数据架构干净；流程是配置出来的推进，不是评论现场即兴的响应。**

---

## 0. 问题与定位

### 0.1 现状的三个真空（代码事实）

1. **项目是空壳**。`project` 表只有 name/description + `project_resource`（repo/目录挂载，`migrations/1001_work_core.up.sql`）。它没有状态集、没有执行者、没有阶段——工作项挂不挂项目不影响任何行为。
2. **状态是标签不是环节**。`work_item_status` 按 workspace 全局配置；`WorkItemService.Update`（`internal/work/app/service_workitem.go:168`）对 StatusKey 只做"存在性校验"（`WritableCategory`），backlog 直跳 done 完全合法。状态之间没有转移规则。
3. **"谁执行"是现场决定的**。执行分派全部走评论触发决策树（`internal/orchestration/domain/trigger.go` DecisionTree）：@谁谁干活、回复 agent 回原 agent、assignee 是 agent/squad 派给本人或 leader（规则 7c，`trigger.go:237-244`）。不存在"到这一环自动换人"的配置位。

结论：系统有很强的**被动响应**能力（评论/定时/事件唤醒/批量扇出），但没有任何东西**主动推着工作项往前走**。

### 0.2 定位

**Workflow = 项目的推进协议**：一串有序阶段（stage），每个阶段绑定一个执行者（agent 或 squad）和进入指令。工作项进入某阶段 → 执行者被自动派任务 → 干完把工单推进到下一阶段 → 下一阶段执行者被唤醒。循环往复直到终态。

一句话：**现在"assignee"是一个静态字段；Workflow 把它变成一个随进度移动的光标。**

### 0.3 与既有机制的关系（不造第二套）

| 既有机制 | Workflow 复用它做什么 |
|---|---|
| `agent_task_queue` + EnqueueService | 阶段执行者拿到的就是标准任务（全工具/审批/结算/Outcome 全继承） |
| `update_work_item_status` agent 工具 | 执行者"干完推单"的标准动作（`internal/orchestration/adapters/engine/tools_work.go:29`） |
| `uq_one_pending_task_per_work_item_agent` 唯一索引 | 流程派发与人工触发并发时的最终仲裁（DB 层，无需新锁） |
| `work_item.updated` 事件 | 流程引擎的推进信号源（outbox 保证不丢） |
| status catalog（workspace 级） | 阶段的"位置"语义直接复用 status_key，不新造位置概念 |
| 小队（squad）/批量（batch） | 某一阶段的执行者可以是 squad（派 leader）、阶段内扇出可以 batch——Workflow 只管"下一棒是谁" |

**Workflow 不新增执行机制，只新增一个"谁在什么位置、下一棒是谁"的裁决层。**

---

## 1. 数据模型（干净版）

迁移号 `1044_project_workflow.up.sql`。三张表，全部归属 **work 域**（推进协议是工作项生命周期的一部分；orchestration 只消费信号建任务）。

```sql
-- 1) 流程定义：1:1 绑定 project（v0 一项目一流程，够用且干净）
CREATE TABLE project_workflow (
  id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  workspace_id uuid NOT NULL REFERENCES workspace(id),
  project_id   uuid NOT NULL UNIQUE REFERENCES project(id),  -- UNIQUE = 1:1
  name         text NOT NULL,
  trigger_mode text NOT NULL DEFAULT 'auto' CHECK (trigger_mode IN ('auto','manual')),
  -- auto: 进入项目的工作项自动进入流程; manual: 需显式 start
  entry_stage_position int NOT NULL DEFAULT 1,              -- 入口阶段序号（从 1 起）
  enabled      boolean NOT NULL DEFAULT true,
  created_by   uuid NOT NULL REFERENCES "user"(id),
  created_at   timestamptz NOT NULL DEFAULT now(),
  updated_at   timestamptz NOT NULL DEFAULT now()
);

-- 2) 阶段定义：有序，position 唯一
CREATE TABLE workflow_stage (
  id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  workspace_id uuid NOT NULL REFERENCES workspace(id),
  workflow_id  uuid NOT NULL REFERENCES project_workflow(id) ON DELETE CASCADE,
  position     int NOT NULL CHECK (position >= 1),
  name         text NOT NULL,
  -- 阶段的"位置"锚点：该阶段结束时执行者应把工单移到的 status
  exit_status_key text NOT NULL REFERENCES ... -- 见 §1.1：软引用 work_item_status.key
  executor_type   text NOT NULL CHECK (executor_type IN ('agent','squad','none')),
  executor_id     uuid,          -- agent 表或 squad 表的 id，none 时为 NULL（人工环节）
  -- 进入该阶段时下发给执行者的指令模板（支持 {{title}}/{{number}} 占位符）
  instruction  text NOT NULL DEFAULT '',
  UNIQUE (workflow_id, position),
  UNIQUE (workflow_id, name)
);

-- 3) 工作项的流程位置（推进光标 + 推进历史）
CREATE TABLE work_item_workflow_state (
  id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  workspace_id uuid NOT NULL REFERENCES workspace(id),
  work_item_id uuid NOT NULL UNIQUE REFERENCES work_item(id) ON DELETE CASCADE,  -- UNIQUE = 一单一流程
  workflow_id  uuid NOT NULL REFERENCES project_workflow(id),
  stage_id     uuid NOT NULL REFERENCES workflow_stage(id),
  entered_at   timestamptz NOT NULL DEFAULT now(),
  -- 防失控计数：本阶段已派发轮数（超 MaxDispatchPerStage 停派，见 §4.5）
  dispatch_count int NOT NULL DEFAULT 0,
  -- 防失控总量：累计 advance 次数（超 阶段数×3 视为异常循环，见 §5.2）
  advance_count  int NOT NULL DEFAULT 0,
  finished_at  timestamptz,     -- 全流程走完的时间；NULL = 在途
  UNIQUE (work_item_id)
);
CREATE INDEX idx_wiws_active ON work_item_workflow_state (workflow_id, stage_id)
  WHERE finished_at IS NULL;

-- 推进流水（谁在什么时候把单子推到哪一棒，人类可读的时间线）
CREATE TABLE workflow_transition_log (
  id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  workspace_id uuid NOT NULL REFERENCES workspace(id),
  state_id     uuid NOT NULL REFERENCES work_item_workflow_state(id) ON DELETE CASCADE,
  from_stage_id uuid,
  to_stage_id   uuid NOT NULL REFERENCES workflow_stage(id),
  actor_type   text NOT NULL,   -- 'agent' | 'user' | 'system'
  actor_id     text NOT NULL DEFAULT '',
  task_id      uuid,            -- 引发本次推进的 agent 任务（若有）
  note         text NOT NULL DEFAULT '',
  created_at   timestamptz NOT NULL DEFAULT now()
);
```

### 1.1 设计裁决：阶段位置锚定 `exit_status_key`，软引用

不建 FK 到 `work_item_status`（status 可被改名/归档，FK 会让流程定义变脆）。改为应用层校验：保存 stage 时检查 `work_item_status` 里 key 存在且 category ∈ {started, done}（closed 类不能作为阶段出口，那是流程之外的废弃态）。`status_key` 是全系统通用的稳定标识（builtin 七态 + 自定义态，key 不可变、name 可改——`service_catalog.go` 的 UpdateStatus 不允许改 key 本身）。

**为什么阶段要锚 status 而不是自造 stage 列**：`work_item.stage` 列（1001 迁移遗留）已被 automation 域的子工单分阶段收敛（`ChildStageFacts` 读 `coalesce(stage,'')`，`wiring_automation.go:184-196`）占用为"子工单阶段标签"语义。流程阶段若也写 stage 会污染那个语义。**流程的光标只活在 `work_item_workflow_state`，status_key 只作单向投影**（流程改状态，状态不反推流程——除推进信号外）。

### 1.2 状态与阶段的关系（一图）

```
workflow_stage.position:   1          2           3          4
stage.name:              "需求澄清"  "方案设计"   "实现"     "验收"
exit_status_key:          "clarified"(自定义) "designed"  "done"    "accepted"
executor:                 A-agent    B-squad     C-agent    D-agent(或 none=人工)

工作项视角：status 在阶段推进时被流程移动；两阶段之间 status 停留在出口态
（可被人工自由改，不影响流程光标——光标只听流程自己的推进信号，见 §4.1）
```

---

## 2. 核心循环：推进链

### 2.1 状态机（每工作项一条，`work_item_workflow_state` 为唯一事实）

```
[无状态] --start--> [stage_1] --advance--> [stage_2] --…--> [stage_N] --complete--> [finished]
    ^                   |                                    (exit_status→终态 category=done)
    +---- restart ------+（人工：回到 entry stage，dispatch_count 清零）
```

推进条件（advance 的判定，全部满足才动）：

1. 工作项当前 `status_key` **等于当前阶段的 `exit_status_key`**（执行者用 `update_work_item_status` 工具打到出口态，或人工拖到出口态）；
2. 本阶段的执行者任务已到**终态**（completed/failed/cancelled——failed 也放行：失败但打了出口态，说明执行者自觉收尾了；若人工不认可，拖回非出口态即可，§4.5 返工）；
3. `dispatch_count ≤ MaxDispatchPerStage`（默认 3，防死循环）。

> ★ 二次验证修订：初稿曾要求"出口态的写入时间必须晚于任务终态时间"——**方向反了**，会卡死快乐路径。真实时序是 agent 在任务**运行中**调用 `update_work_item_status` 打出口态（工具直写，`service_workbench.go:32`），任务随后才结算（`Settle` 写 completed_at）。时间比较永远不成立。修订后条件 2 只做**存在性**检查（存在 context 带 stage_id 的终态任务），不做时序比较——竞态场景（出口态先打、任务后终）由"两个信号都触发裁决"天然覆盖（见 §4.1 双门铃）。

advance 动作（单事务）：

```
tx {
  1. UPDATE work_item_workflow_state SET stage_id = 下一阶段, dispatch_count = 0, entered_at = now()
  2. INSERT workflow_transition_log (from, to, actor, task_id)
  3. 将工作项 assignee 改为下一阶段执行者（executor_type/id → assignee_type/id）   ← 光标移动的投影
  4. 为下一阶段执行者入队任务（见 §3 派发矩阵）
}
```

**status 与推进解耦的关键**：advance 的信号是 `work_item.updated` / `work_item:updated_by_agent` 事件（哪个都收），但**判定不看事件、看库**——事件只当门铃，收到后读 `work_item_workflow_state` + `work_item.status_key` 现值裁决。事件丢失/乱序不致错（下一声门铃会再裁决；outbox relay 保底重投）。

### 2.2 为什么推进要有两个信号源（status 事件 + 任务终态事件）

任务终态（TaskCompleted/TaskFailed）只说明"这一棒跑完了"，不说明"结果被接受了"——执行者可能完成任务但没打出口态（人工判断不通过、要返工）。以 status 达到出口态为推进的**判定条件**，把"是否过关"的裁决权留给执行者的收尾动作（或人工拖单）。但只听 status 事件有缺口：若执行者**先**打出口态、任务**后**结算（正常顺序就是如此），status 事件到达时条件 2 尚不满足，裁决空转后就再无信号。因此监听器订两个门铃：

- `work_item.updated` / `work_item:updated_by_agent`（status 可能达出口态）
- `TaskCompleted` / `TaskFailed` / `TaskCancelled`（条件 2 可能刚满足）

任一门铃响都做**同一套全量裁决**（读库现值，§4.1），缺哪个信号都不会漏推进。任务结算只是前置条件之一；**把人留在环上的最自然位置**：人工可以永远不拖到出口态来否决推进，也可以手动拖过去强制放行。

---

## 3. 派发矩阵：阶段执行者怎么拿任务（与现有通道的精确接缝）

流程引擎调 **EnqueueService 现有入口**，不绕过、不新造入队路径：

| executor_type | 派发调用 | 说明 |
|---|---|---|
| `agent` | `EnqueueAssignment(ctx, ws, wi, agentID, actorUserID)`（`service.go:576`） | 标准指派任务；返回 `already_active` 时并入 MergeIntoPending 或转 deferred（引擎直接采纳返回值语义，见 §4.4） |
| `squad` | `EnqueueSquadTask(ctx, ws, wi, squadID, actorUserID)`（`service.go:1093`） | 落 leader 任务（is_leader_task=true），leader 再 consult/dispatch 成员——小队编排全继承 |
| `none` | 不派发 | 人工环节：改完 assignee 投影后给订阅者发通知（走通知域 WriteNotice），等人工拖单到出口态 |

`actorUserID` 的取值：**流程创建者**（project_workflow.created_by）——任务有稳定的人类归属（originator/accountable），审批通知、quota 计费都有主。这沿用了 autopilot 的归属裁决先例（`wiring_automation.go` wakeupEnqueuer 注释：无主任务 fail-closed 落 unattributed，流程不走这条路——创建流程必须是人）。

任务的 `context` 注入（引擎在调用前包装）：

```json
{
  "trigger": "workflow_stage",
  "workflow_id": "...", "stage_id": "...", "stage_name": "方案设计",
  "stage_position": 2, "instruction": "渲染后的指令文本",
  "work_item_title": "..."
}
```

`trigger: workflow_stage` 是**新值**（现有值域：assignment/quick_action/chat/wakeup/leader_dispatch）。engine 侧按 `context.trigger` 分支注入阶段简报（同 squadBrief 模式）：把 instruction + 出口态期望（"完成后调用 update_work_item_status 将状态移至 X 即进入下一阶段"）写进系统提示。**这是 engine 侧唯一的改动点**（一个 prompt 分支 + 一个 trigger 值识别），不改工具面。

### 3.1 已验证的接缝事实（为什么不会撞车——逐条对应代码）

| # | 撞车场景 | 代码事实 | 裁决 |
|---|---|---|---|
| 1 | 流程派发 vs 人工指派/Quick Action 同时入队 | `uq_one_pending_task_per_work_item_agent`（`1003_orchestration.up.sql`）按 (work_item_id, agent_id) 唯一，queued/dispatched/特定 deferred 只允许一条；InsertTask 撞 23505 → `asFenceErr`（`repo.go:291`） | **DB 仲裁，无需新逻辑**：撞索引即"该 agent 已有在途"，流程引擎按 §4.4 处理 |
| 2 | 流程改 assignee 触发 main.go 的 assignee 自动派发器（`main.go:96-104`：PhaseCascade 监听 `work_item.updated`，**仅 AssigneeType=agent 时** EnqueueAssignment） | 派发器不看"是谁改的"（ActorUserID 非空即派） | ★ 经推导**无需让位**（初稿的 metadata 标记方案已删除）：流程的派发发生在 advance 事务内、且在先；若派发器随后也对同一 agent 入队，`EnqueueAssignment` 的 CountPendingFence（`service.go:594`）返回 already_active，空手而归。squad 型执行者的投影（assignee_type=squad）派发器根本不认（只认 agent 型）。仅剩的杂音是个别场景下 trigger_evidence 变 assignment 而非 workflow_stage——语义无害，W2 加一条集成测试锚定即可 |
| 3 | 执行者完成任务 → resetIdleWorkItem（`main.go:435-446`：TaskCompleted 且 status=in_progress 且无活跃任务 → 打回 todo） | 出口态恰为 in_progress 的流程（如"实现"阶段），任务完成瞬间 reset 把出口态打回 todo——若发生在 status 门铃裁决之后，把已满足的条件 1 又拆了 | ★ 真机 E2E 实证（2026-10-14）：**初版"两序皆安全"论证错误**——resetIdle 与推进监听器同在 PhaseCascade，reset handler 先注册先执行，TaskCompleted 门铃到达推进监听器时 status 已被打回 todo，条件 1 不成立，流程卡死在当前阶段。修正：resetIdleWorkItem 对存在在途流程光标（finished_at IS NULL）的工单跳过（`HasActiveWorkflowState` 守卫）；流程内工单的状态回落由流程返工语义（§4.5）与人工拖单负责 |
| 4 | 流程内评论还走 DecisionTree（用户 @ 别的 agent、assignee 是当前阶段执行者 → 7c 规则照常派活） | DecisionTree 是评论通道的规则，与流程通道正交 | **保留，不拦**：评论介入是流程内协调的合法方式（比如用户 @ 专家 agent 补个意见）。它派的任务同样受唯一索引约束；流程 advance 只认"阶段执行者任务终态后 status 达出口态"，别的 agent 跑什么都不影响光标 |
| 5 | 用户手动拖 status 到下一阶段出口态（跳过当前阶段） | status 无转移规则，任何跳转合法（`service_workitem.go:168` 只验存在性） | **不禁止跳**：advance 裁决读的是"当前 stage 的出口态"，跳到别的态不触发 advance；人工想把单子推快，就连续拖到当前出口态再拖下一阶段出口态——每次 advance 只走一格（stage 间连续推进靠下一阶段的派发链自己继续）。v0 不做"跳阶段"配置 |
| 6 | wakeup（定时/事件/条件）仍会对流程内工作项派活 | wakeup 是工作项级规则，与流程并存 | 保留。二者最终都汇入 agent_task_queue，唯一索引 + MergeIntoPending 收敛。流程不接管 wakeup 的生命周期 |
| 7 | autopilot 建单挂项目 → 自动进流程（trigger_mode=auto）又自动派第一棒 | autopilot 的 dispatchRun 用 EnqueueForWakeup 派 target_agent（`service_autopilot.go:428`） | 允许共存但有序：进流程的 start 动作（§4.6）本身就是入队第一棒；若 autopilot 同时也派了 target_agent，唯一索引会拦住重复（同 agent），不同 agent 则两张任务单并存——语义可接受（autopilot 的单子是"启动上下文"，流程的单子是"第一棒"）。v0 文档建议：autopilot 模板若 target_agent = 流程第一棒执行者，唯一索引自然合并；不等则并存 |

### 3.2 判定矩阵总表（谁在什么时候动 assignee / 派任务）

| 动作 | 流程引擎 | main.go assignee 派发器 | DecisionTree（评论） | wakeup | autopilot |
|---|---|---|---|---|---|
| 改 assignee | ✅ advance 时投影下一棒 | —（只读） | — | — | — |
| 派任务 | ✅ 阶段进入时 | assignee 变更即派（#2 让位后跳过流程投影） | 评论规则 3/6/7 | 定时/事件到点 | 建单/到点 |
| 移 status | advance 时不动 status（status 是推进的**因**不是果） | — | — | — | — |

关键澄清：**advance 不改 status**。下一阶段的入口 status 就是上一阶段的出口 status（单子在两个阶段之间停留于出口态，直到下一棒执行者把它打到下一个出口态）。这样"阶段 ↔ status"是单向因果：status 达出口态 → advance → 换执行者派任务 → 执行者打下一个出口态 → ……。status 永远是执行者或人工打的，流程只消费它。人工（`none`）阶段不动画布，等人工拖单（少一条隐式动作，规则更直）。

---

## 4. 撞车防御协议（本章是设计的核心，逐条可验证）

### 4.1 幂等推进：门铃不裁决，裁决在读库

- 推进监听器订阅两对门铃（§2.2）：`work_item.updated` + `work_item:updated_by_agent`，以及 `TaskCompleted` + `TaskFailed` + `TaskCancelled`（均经 outbox relay → eventbus）。
- 回调只做三件事：查 `work_item_workflow_state`（无则非流程单，返回）；比 status_key 与 stage.exit_status_key、查阶段任务终态；满足 §2.1 三条件则 advance。
- advance 是**单事务 + 乐观锁**：`UPDATE work_item_workflow_state SET stage_id=$next, advance_count=advance_count+1 WHERE id=$id AND stage_id=$current`（affected=0 即别人已推进，放弃）。并发门铃（人工改 + agent 改几乎同时）只有一个赢，另一个空转。
- transition_log 的 (state_id, to_stage_id) 不加唯一——返工回退会产生重复 to（合法），幂等由 state 行的 CAS 保证。

> ★ relay 解码注记：`decodeOutboxPayload`（`main.go:387-403`）目前只解码 Task*/Comment* 事件，work_item 系事件经 relay 重放时 Payload 为 nil——监听器**不得依赖 payload 字段取 work_item_id，必须从事件路由参数或直接读库取**。W2 落地时给 decodeOutboxPayload 补 work_item 系（`WorkItemCreated/Updated/UpdatedByAgent` → `WorkItemPayload`）是顺手修正，一行 case 的事。

### 4.2 投影协议：流程动 assignee 与 assignee 派发器共存

★ 初稿的 metadata 让位标记已删除（§3.1 #2 修订记录）：`EnqueueAssignment` 自带 pending fence（`service.go:594`），流程先入队则派发器空手而归；且派发器只认 agent 型 assignee（`main.go:100`），squad 型投影不触发。**零改动共存**，唯一代价是任务 context 的 trigger 字段在极端交错下可能记为 assignment——W2 用集成测试锚定实际行为即可。

### 4.3 防提前推进：出口态必须有终态任务背书

条件 2 的实现：advance 裁决时查 `agent_task_queue`：该 work_item 存在 `context->>'stage_id' = 当前 stage_id` 且 status ∈ {completed, failed, cancelled} 的任务（存在即可，不做时序比较——时序反了会卡死快乐路径，见 §2.1 ★ 修订注）。没有该阶段任务（none 阶段或人工代打）则退化为条件 1+3。

"任务终态**之前**就打了出口态、且之后任务一直不结算"的窗口不存在——终态本身是第二个门铃（§2.2），结算即再裁决。真正的提前推进只剩一种：人工在任务运行中拖到出口态且任务最终 failed——按条件 2 放行（failed 也是终态）。这是**有意的裁决**：人工强拖 = 人工背书。

### 4.4 派发失败的降级序列

EnqueueAssignment 返回值处理：

| 返回 | 含义 | 引擎动作 |
|---|---|---|
| `queued` | 入队成功 | 正常 |
| `already_active` | 该 agent 已有在途任务（pending fence，`service.go:546` CountPendingFence） | **合并**：调 MergeIntoPending（把 stage 指令并入在途任务的 coalesced 上下文）；并在 transition_log 记 note=coalesced |
| `blocked` | agent 不存在/归档 | transition_log 记 note=executor_unavailable；**通知流程创建者**（WriteNotice），流程停在当前阶段（不自动跳过——缺人问人，不静默改道） |
| `model_not_configured` | 模型没配 | 同 blocked，通知创建者 |

流程停在原地等人 = 安全侧。派发 count 不增加（没派出去）。

### 4.5 返工与回退

- **阶段内返工**：执行者任务 failed → 出口态没打 → 流程不动。dispatch_count 在**下一次该阶段成功派发**时递增；同阶段反复派发（人工 Trigger 重派/评论再触发）累计，超 MaxDispatchPerStage（默认 3）后不再派发，通知创建者。
- **跨阶段回退**：v0 只有一个人工端点 `POST /api/work-items/{id}/workflow/restart`（回 entry stage，清 dispatch_count/finished_at）。回退到任意中间阶段不做（YAGNI，等真需求）。

### 4.6 进入流程（start）

- **auto 模式**：`work_item.created` 事件（或挂载项目的 PATCH）→ 引擎查该项目 enabled workflow → 建 `work_item_workflow_state`（entry stage）→ 投影 assignee → 派第一棒。
- **manual 模式**：`POST /api/work-items/{id}/workflow/start`（body 可选 override entry）。
- 建单路径不限制：手工建、autopilot 建（§3.1 #7）、agent 建子项（CreateChildByAgent 挂了项目同样进流程——**注意**：子工单若挂流程项目，它有自己的 state；父子各走各的流程，互不干扰。子单的 children-done 收敛（ChildDone 系统唤醒）照旧工作——那是父单 assignee 的唤醒，与父单流程光标正交）。
- 工作项移出项目（project_id 清空）→ state 保留但 `finished_at = now()` + note=detached（单子脱离流程，光标冻结，不再推进）。

### 4.7 终态与关闭

- advance 到最后一阶段：完成条件仍是最后一阶段出口态（通常是 category=done 的 status）。达成 → `finished_at = now()`，transition_log 记 complete。**不再派任何任务**。
- 流程中途 `work_item` 被删：state 级联删除（ON DELETE CASCADE）。status 被移到 closed 类（cancelled 等）→ advance 不触发（closed 不在出口态候选，§1.1），state 冻结（detached 语义同 4.6，记 note=cancelled）。人工 restart 可复活。

### 4.8 关停与暂停

- workflow.enabled=false：立即生效——推进监听器查 workflow 时带 enabled 检查，停了就不 advance、不派发；在途任务不撤（已派出的一棒跑完就停在原地）。
- 这覆盖了"流程配置改错"的逃生门：停流程 → 单子停在当前阶段 → 修正配置 → 重启（state 未动，从当前阶段继续）。

---

## 5. 防失控（循环与风暴防御）

1. **dispatch_count 上限**（每阶段 3 次派发，§4.5）。
2. **推进总量上限**：`advance_count`（§1 表结构）每次 advance +1，超 `position 数 × 3` 视为异常循环 → 自动 enabled=false 该 workflow + 通知创建者（防御性，正常流程到不了这个数：每阶段最多 advance 一次）。
3. **事件不递归**：advance 事务里改 assignee 只发一次 `work_item.updated`，推进监听器收到后读库发现 status ≠ 新 stage 出口态 → 空转返回。TaskCompleted 门铃同理（任务已在终态，条件已消费，CAS 兜底）。**事件链天然无环**：advance 的因是 status/任务终态变化，果是 assignee 变化——果不再成因。这是本设计最重要的不变量：**流程从不改 status、从不结算任务（§3.2），所以流程动作不会触发自己的推进判定**。
4. 唯一索引兜底（§3.1 #1）保证物理上不会任务洪水。

> 注：wakeup 的 wakeup_chain 防环机制不适用这里（那是 wakeup→task→event→wakeup 的环）；流程的环是 advance→assignee 事件→advance，由不变量 3 根除。

---

## 6. API 与 UI（v0 最小面）

### 6.1 API（work 域路由挂载）

```
# 流程 CRUD（项目管理员）
POST   /api/projects/{projectId}/workflow          # 创建（含 stages 数组，一次成型）
GET    /api/projects/{projectId}/workflow
PUT    /api/projects/{projectId}/workflow          # 整体替换 stages（v0 不做增量）
DELETE /api/projects/{projectId}/workflow          # 删定义；在途 state 全部 detached
POST   /api/projects/{projectId}/workflow/enable|disable

# 工作项侧
GET    /api/work-items/{id}/workflow               # state + transition 历史 + 当前阶段定义
POST   /api/work-items/{id}/workflow/start         # manual 模式入口（auto 模式也可显式重进）
POST   /api/work-items/{id}/workflow/restart       # 回 entry（§4.5）
DELETE /api/work-items/{id}/workflow               # 脱离流程（= detached）
```

权限：流程 CRUD = workspace admin（项目归属的资源）；start/restart/detach = 工作项可写成员。

### 6.2 UI（v0 三个触点）

1. **项目详情页**：流程编辑器（阶段列表：拖序、每阶段配 执行者选择器 + 出口态下拉 + 指令文本框）。复用现有 agent/squad 选择器组件。
2. **工作项详情页**：侧栏"流程"区块——当前阶段名 + 执行者 + 进度点（●●○○）+ transition 时间线折叠列表 + restart/detach 按钮。
3. **看板/列表**：项目分组视图下，卡片角标显示当前阶段号（"S2/4"）。

不做：流程模板库、跨项目共享流程、阶段并行、条件分支（§8）。

---

## 7. 实施切分（三个可独立合入的 PR）

| PR | 内容 | 验收 |
|---|---|---|
| **W1 数据模型 + 定义面** | 1044 迁移 + workflow CRUD API + sql_allowlist(work 域加 4 表) + 项目页流程编辑器（只读联调后端） | 建流程/改阶段/启停走通；work 域表归属检查过 trace-guard |
| **W2 引擎 + 撞车防御** | 推进监听器（双门铃订阅 + CAS advance + decodeOutboxPayload 补 work_item 解码）+ §3 派发矩阵 + §4 全部协议 + start/restart/detach 端点 | 集成测试覆盖 §3.1 矩阵 7 场景 + §5 防御 4 条（含双事件并发 advance 幂等、resetIdleWorkItem 两序安全、assignee 派发器共存语义锚定） |
| **W3 工具面 + UI** | engine 阶段简报注入（context.trigger=workflow_stage 分支）+ 工作项侧栏流程区块 + 看板角标 + 真模型 E2E（三阶段流程：A 派 B 派 C 全自动走完） | E2E：工作项挂项目 → auto 进流程 → 三阶段无人干预流转到 done，transition_log 三条、任务三个、无重复派发 |

## 8. 边界与不做（v0 明确出圈）

- 分支/并行阶段、条件路由（"验收不过回实现"只能人工 restart）。
- 跨项目流程模板/复制。
- 阶段级 SLA/超时自动升级（wakeup 可以手工配在单子上补位，天然不冲突——§3.1 #6）。
- WorkItemPayload 加 prev_assignee（无消费者需求前不动契约；assignee 撞车已由 pending fence 解决，§4.2）。
- stage 写 work_item.stage 列（§1.1 已裁决：那列语义归子工单收敛）。
- agent 工具新增（不给 agent "advance workflow" 工具——推进只能靠打出口态，保持 §5.3 不变量唯一因）。

---

## 9. 开放问题（实施前需拍板，不阻塞 W1）

1. **executor 归档/删除时**：v0 走 blocked+通知（§4.4）。要不要自动暂停流程而不是等人？（当前裁决：等人，缺人问人。）
2. **squad 执行者的 leader 变更**：阶段定义存 squad_id，派发时实时取 leader——leader 换人下一棒自动跟新，无需迁移。确认此语义即可。
3. **同一 agent 连任多阶段**（A 负责阶段 1 和 3）：第二棒派发时 A 若仍在阶段 1 任务在途中 → already_active → MergeIntoPending 合并指令。行为正确但体验微妙（一条任务两个阶段的指令）——v0 接受，文档注明。

## 10. 验证记录（设计断言 ↔ 代码事实对照，二次验证 ★ 修订）

| 断言 | 代码证据 |
|---|---|
| status 变更无转移规则，仅存在性校验 | `internal/work/app/service_workitem.go:168-195`（Update 只做 WritableCategory 存在性检查） |
| agent 改状态发独立事件名 | `internal/contract/events/events.go:7`（`work_item:updated_by_agent` 冒号形态）vs `events.go:63`（`work_item.updated` 点形态）；产生于 `service_workbench.go:58` |
| WorkItemPayload 无 prev 字段 | `events.go:75-84`（只有后值） |
| ★ agent 打出口态在任务运行中、先于结算 | `tools_work.go:41`（工具直写 UpdateStatusByAgent）→ 结算在 `service.go:746`（complete）；时序=先出口态后终态，故 §2.1 条件 2 不做时序比较 |
| 任务唯一索引 + 23505 转 fence 错 | `1003_orchestration.up.sql` uq_one_pending；`repo.go:291 asFenceErr` |
| already_active 判定 = pending fence（assignment/quick_action 双入口都有） | `service.go:546 / 594` CountPendingFence（queued/dispatched/特定 deferred） |
| MergeIntoPending 合并不追加任务 | `repo.go:737-751`（UPDATE 在途任务 context） |
| assignee 自动派发器只认 agent 型、无来源过滤 | `cmd/orkome-server/main.go:96-104`（PhaseCascade，AssigneeType=agent 才派） |
| resetIdleWorkItem 打回 in_progress | `main.go:435-446`（TaskCompleted → 无活跃任务 → todo） |
| ★ relay 不解码 work_item 系事件载荷 | `main.go:387-403`（decodeOutboxPayload 只认 Task*/Comment*；重放路径 Payload=nil） |
| ★ EnqueueAssignment 经 outbox+uow 落库（事务内入队可行） | `service.go:615-634`（emitCommitted 包裹 InsertTask）；`pg.go:423`（Within 嵌套时复用外层 tx） |
| wakeup 防环 chain 机制存在（不适用于本设计） | `service_wakeup.go:637`（ChainContains → PauseWakeupLoop） |
| work_item.stage 列被子工单收敛占用 | `wiring_automation.go:184-196`（ChildStageFacts 读 stage） |
| 评论触发树与流程正交 | `internal/orchestration/domain/trigger.go`（全树无 workflow 概念） |
| 事件经 outbox 落库再 relay | `internal/work/app/outbox.go` + `main.go:169`（NewRelay → bus.Emit） |
| engine 按 context.trigger 分支有先例 | `service.go:549`（quick_action）、`repo.go:671`（chat trigger）、wiring_automation.go:132（wakeup） |
| EnqueueAssignment/SquadTask 入口现成（squad 落 leader 任务带 IsLeaderTask） | `service.go:576 / 1093-1118` |
| 迁移号段 1044 可用 | migrations 最新为 1043_batch_agent_tool |
