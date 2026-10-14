-- 1046 逆向：收回 stalled 语义与 assistant_proxy 枚举（v4 §7 第二刀前的
-- 完整回退面）。注意：若已有 'stalled' 任务行或 assistant_proxy 行存在，
-- 约束回加会失败——回退前须先清理数据（设计纪律：不考虑旧数据迁移）。
DROP INDEX IF EXISTS uq_agent_assistant_proxy;

ALTER TABLE agent_task_queue DROP CONSTRAINT agent_task_queue_status_check;
ALTER TABLE agent_task_queue ADD CONSTRAINT agent_task_queue_status_check
  CHECK (status IN ('queued','dispatched','running','completed','failed','cancelled','deferred'));

ALTER TABLE agent DROP CONSTRAINT agent_runtime_kind_check;
ALTER TABLE agent ADD CONSTRAINT agent_runtime_kind_check
  CHECK (runtime_kind IN ('engine','external'));
