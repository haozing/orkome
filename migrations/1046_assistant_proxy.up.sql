-- 助理接入 W1（docs/assistant-access-design-2026-10-14.md v4 §4.1/§4.4）：
-- 一个迁移做三件事——扩 runtime_kind 枚举 / 扩任务 status 枚举 / 部分唯一索引。

-- ① runtime_kind CHECK 扩 'assistant_proxy'：agent 表上 1032 内联列 CHECK 是
-- unnamed 约束，自动名为 agent_runtime_kind_check（PostgreSQL 列级 CHECK
-- 未显式命名时按 {表}_{列}_check 生成），DROP/ADD 重建。down 逆向回
-- ('engine','external')。
ALTER TABLE agent DROP CONSTRAINT agent_runtime_kind_check;
ALTER TABLE agent ADD CONSTRAINT agent_runtime_kind_check
  CHECK (runtime_kind IN ('engine','external','assistant_proxy'));

-- ② 任务 status CHECK 扩 'stalled'：1003 的 status 列内联 CHECK 同为
-- unnamed 自动名 agent_task_queue_status_check。stalled 是新终态——
-- 有人值守模式下"超时挂起+交还"的唯一平台动作（v4 §4.3），不 failed、
-- 不重试、不进失败税。
ALTER TABLE agent_task_queue DROP CONSTRAINT agent_task_queue_status_check;
ALTER TABLE agent_task_queue ADD CONSTRAINT agent_task_queue_status_check
  CHECK (status IN ('queued','dispatched','running','completed','failed','cancelled','deferred','stalled'));

-- ③ 代理行唯一性：每工作区每用户至多一个 assistant_proxy 行。必须用部分
-- 唯一索引（二轮 N1，P0）——普通 UNIQUE(workspace_id, owner_user_id) 会
-- 让"一 owner 多 agent"（所有 API 建 agent 默认写 owner 的常态）即崩。
CREATE UNIQUE INDEX uq_agent_assistant_proxy
  ON agent (workspace_id, owner_user_id)
  WHERE runtime_kind = 'assistant_proxy';
