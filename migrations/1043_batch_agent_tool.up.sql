-- 批量任务 v2.0（docs/batch-agent-tool-design-2026-10-13.md §3）：
-- draft 预览状态（通道 B 物化预览→显式 start）、source 物化对账、
-- originator agent/task 维度（批次终态唤醒创建者 agent 收割）。
ALTER TABLE work_item_batch DROP CONSTRAINT work_item_batch_status_check;
ALTER TABLE work_item_batch ADD CONSTRAINT work_item_batch_status_check
  CHECK (status IN ('draft','pending','running','paused','completed','failed','cancelled'));

ALTER TABLE work_item_batch ADD COLUMN source_materialization jsonb;
ALTER TABLE work_item_batch ADD COLUMN originator_task_id text;
ALTER TABLE work_item_batch ADD COLUMN originator_agent_id uuid REFERENCES agent(id);

ALTER TABLE work_item_batch_item ADD COLUMN skip_reason text;
