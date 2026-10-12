-- 删除闭环修复（真模型 E2E 复现）：autopilot_run.trigger_id 的 FK 无
-- ON DELETE 行为，默认 NO ACTION——autopilot 一旦跑过，其 trigger 永远删不掉
--（DELETE 撞 23503 → 500），进而 autopilot 自身的删除守卫（enabled trigger
-- 检查）也无法满足，删除闭环全断。
-- 裁决：trigger 删除时 run 的 trigger_id 置 NULL（run 是历史台账，保留；
-- trigger_id 仅溯源用，可空化不破坏任何读取路径——run 列表/详情均不依赖它）。

ALTER TABLE autopilot_run DROP CONSTRAINT IF EXISTS autopilot_run_trigger_id_fkey;
ALTER TABLE autopilot_run
  ADD CONSTRAINT autopilot_run_trigger_id_fkey
  FOREIGN KEY (trigger_id) REFERENCES autopilot_trigger(id) ON DELETE SET NULL;
