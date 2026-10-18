-- 1048 逆向：收回通知的任务挂载列（已写入的 task_id 一并丢弃）。
ALTER TABLE notification DROP COLUMN IF EXISTS task_id;
