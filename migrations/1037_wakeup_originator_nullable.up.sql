-- W2-2（wave2）：system wakeup 归属设计——系统唤醒无主，originator 允许 NULL。
--
-- 裁决（2026-10-12 主会话）：system wakeup（children_done）由 EnsureSystemWakeup
-- 平台自动播种，created_by 恒 NULL（1006:6 建列即可空）；fire 链曾用 orZeroUUID
-- 把空归属补成零 UUID '00000000-…-0000' → 撞
-- agent_task_queue_originator_user_id_fkey（"user" 表无零 UUID 行，23503）→
-- 任务插不进、next_fire_at 永不推进、due 扫描每 tick 重试同一失败（错误洪水，
-- FP-13 P0-2 的同族遗留）。本次固化数据形态：NULL = 系统发起、无主。
-- 配套代码改动：cmd/orkome-server/wiring_automation.go 去 orZeroUUID、
-- EnsureSystemWakeup 显式不写 created_by。

-- 1) 固化可空：1003:89 建列时本就可空（uuid REFERENCES "user"(id)，无 NOT NULL），
--    仿 1017 惯例做条件防御——当前可空时为 no-op，防未来环境误收紧。
DO $$
BEGIN
  IF EXISTS (
    SELECT 1 FROM information_schema.columns
    WHERE table_name = 'agent_task_queue'
      AND column_name = 'originator_user_id'
      AND is_nullable = 'NO'
  ) THEN
    ALTER TABLE agent_task_queue ALTER COLUMN originator_user_id DROP NOT NULL;
  END IF;
END
$$;

-- 2) 存量哨兵清洗：零 UUID 一直被 FK 拦截、历史上从未成功落库，本语句预期
--    匹配 0 行；仍按裁决防御性清洗一次（幂等，无匹配即 no-op）。
UPDATE agent_task_queue
SET originator_user_id = NULL
WHERE originator_user_id = '00000000-0000-0000-0000-000000000000'::uuid;
