-- Project Workflow（docs/project-workflow-design-2026-10-13.md §1）：
-- 项目级推进协议。流程定义 1:1 绑定 project（UNIQUE），阶段有序锚定
-- exit_status_key（软引用 work_item_status.key，§1.1 裁决：status 可改名/归档，
-- FK 会让定义变脆；存在性与 category ∈ started|done 由应用层校验）。
-- 状态面（W2 消费）：work_item_workflow_state 是推进光标的唯一事实，
-- workflow_transition_log 是人类可读时间线，均随 work_item/state 级联清理。

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
  -- 阶段的"位置"锚点：该阶段结束时执行者应把工单移到的 status（软引用，无 FK）
  exit_status_key text NOT NULL,
  executor_type   text NOT NULL CHECK (executor_type IN ('agent','squad','none')),
  executor_id     uuid,          -- agent 表或 squad 表的 id，none 时为 NULL（人工环节）
  -- 进入该阶段时下发给执行者的指令模板（支持 {{title}}/{{number}} 占位符）
  instruction  text NOT NULL DEFAULT '',
  UNIQUE (workflow_id, position),
  UNIQUE (workflow_id, name)
);

CREATE INDEX idx_wf_stage_workflow ON workflow_stage (workflow_id, position);

-- 3) 工作项的流程位置（推进光标，W2 消费；W1 只建表）
-- 裁决（W1 实施注记 §关键裁决）：workflow_id / stage_id 在这里是软引用（无 FK）——
-- Delete 定义要保留在途 state 做 detached 冻结（契约 §2），FK 会让定义删除
-- 撞上光标历史，与 §1.1 exit_status_key 软引用同一 brittle-FK 论证。
-- 本表对 work_item 的 FK ON DELETE CASCADE 是唯一的硬引用（单删光标亡）。
CREATE TABLE work_item_workflow_state (
  id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  workspace_id uuid NOT NULL REFERENCES workspace(id),
  work_item_id uuid NOT NULL UNIQUE REFERENCES work_item(id) ON DELETE CASCADE,  -- UNIQUE = 一单一流程
  workflow_id  uuid NOT NULL,
  stage_id     uuid NOT NULL,
  entered_at   timestamptz NOT NULL DEFAULT now(),
  -- 防失控计数：本阶段已派发轮数（超 MaxDispatchPerStage 停派，设计 §4.5）
  dispatch_count int NOT NULL DEFAULT 0,
  -- 防失控总量：累计 advance 次数（超 阶段数×3 视为异常循环，设计 §5.2）
  advance_count  int NOT NULL DEFAULT 0,
  finished_at  timestamptz,     -- 全流程走完/脱离流程的时间；NULL = 在途
  UNIQUE (work_item_id)
);

CREATE INDEX idx_wiws_active ON work_item_workflow_state (workflow_id, stage_id)
  WHERE finished_at IS NULL;

-- 4) 推进流水（谁在什么时候把单子推到哪一棒，人类可读的时间线）
-- to_stage_id 同为软引用（state 冻结后仍可读历史；阶段随定义删除不拦流水）。
CREATE TABLE workflow_transition_log (
  id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  workspace_id uuid NOT NULL REFERENCES workspace(id),
  state_id     uuid NOT NULL REFERENCES work_item_workflow_state(id) ON DELETE CASCADE,
  from_stage_id uuid,
  to_stage_id   uuid NOT NULL,
  actor_type   text NOT NULL CHECK (actor_type IN ('agent','user','system')),
  actor_id     text NOT NULL DEFAULT '',
  task_id      uuid,            -- 引发本次推进的 agent 任务（若有）
  note         text NOT NULL DEFAULT '',
  created_at   timestamptz NOT NULL DEFAULT now()
);
