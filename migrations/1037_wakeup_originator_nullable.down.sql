-- W2-2 down：恢复策略（写明，与直觉的"NULL→哨兵→SET NOT NULL"不同）——
--
-- 1) 不做 SET NOT NULL：基线（1003:89）中该列本就可空，从未存在过 NOT NULL
--    约束；且基线代码本就有合法写 NULL 的路径（orchestration InsertDeferredTask
--    空 originator 走 nullif($7,'')::uuid 等）。在 down 里收紧会把 schema 搞得
--    比 up 之前更严，属过度回滚。
-- 2) up 的数据清洗（哨兵零 UUID → NULL）物理上不可逆：零 UUID 不满足
--    agent_task_queue_originator_user_id_fkey（"user" 表无此行）——这正是当年
--    orZeroUUID 补零必炸 23503 的同一堵墙，无法把 NULL 行回写成哨兵。
-- 3) 因此 down 只做守卫：存在 NULL originator 行（系统发起任务，无法归属）时
--    显式报错中止，要求先为这些行做归属决策（指定真实用户或删除对应任务行）
--    再回滚本迁移；无 NULL 行时本 down 为 no-op（schema 与基线一致）。
DO $$
BEGIN
  IF EXISTS (SELECT 1 FROM agent_task_queue WHERE originator_user_id IS NULL) THEN
    RAISE EXCEPTION 'migration 1037 down: agent_task_queue has NULL originator_user_id rows (system-initiated tasks, unattributable by design); attribute them to a real user or delete those rows before rolling back this migration';
  END IF;
END
$$;
