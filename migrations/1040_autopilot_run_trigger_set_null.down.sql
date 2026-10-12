-- down：恢复 NO ACTION（需先手工清理引用行，原始行为即如此）。
ALTER TABLE autopilot_run DROP CONSTRAINT IF EXISTS autopilot_run_trigger_id_fkey;
ALTER TABLE autopilot_run
  ADD CONSTRAINT autopilot_run_trigger_id_fkey
  FOREIGN KEY (trigger_id) REFERENCES autopilot_trigger(id);
