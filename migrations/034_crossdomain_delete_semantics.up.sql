ALTER TABLE agent_task_queue DROP CONSTRAINT agent_task_queue_work_item_id_fkey,
  ADD CONSTRAINT agent_task_queue_work_item_id_fkey FOREIGN KEY (work_item_id) REFERENCES work_item(id) ON DELETE CASCADE;

ALTER TABLE agent_task_queue DROP CONSTRAINT agent_task_queue_retry_of_task_id_fkey,
  ADD CONSTRAINT agent_task_queue_retry_of_task_id_fkey FOREIGN KEY (retry_of_task_id) REFERENCES agent_task_queue(id) ON DELETE CASCADE;
ALTER TABLE task_supplement DROP CONSTRAINT task_supplement_task_id_fkey,
  ADD CONSTRAINT task_supplement_task_id_fkey FOREIGN KEY (task_id) REFERENCES agent_task_queue(id) ON DELETE CASCADE;
ALTER TABLE task_usage DROP CONSTRAINT task_usage_task_id_fkey,
  ADD CONSTRAINT task_usage_task_id_fkey FOREIGN KEY (task_id) REFERENCES agent_task_queue(id) ON DELETE CASCADE;
ALTER TABLE task_message DROP CONSTRAINT task_message_task_id_fkey,
  ADD CONSTRAINT task_message_task_id_fkey FOREIGN KEY (task_id) REFERENCES agent_task_queue(id) ON DELETE CASCADE;

ALTER TABLE autopilot_run DROP CONSTRAINT autopilot_run_work_item_id_fkey,
  ADD CONSTRAINT autopilot_run_work_item_id_fkey FOREIGN KEY (work_item_id) REFERENCES work_item(id) ON DELETE SET NULL;

ALTER TABLE work_item DROP CONSTRAINT work_item_parent_work_item_id_fkey,
  ADD CONSTRAINT work_item_parent_work_item_id_fkey FOREIGN KEY (parent_work_item_id) REFERENCES work_item(id) ON DELETE SET NULL;

ALTER TABLE work_item DROP CONSTRAINT work_item_duplicate_of_work_item_id_fkey,
  ADD CONSTRAINT work_item_duplicate_of_work_item_id_fkey FOREIGN KEY (duplicate_of_work_item_id) REFERENCES work_item(id) ON DELETE SET NULL;

ALTER TABLE work_item_batch DROP CONSTRAINT work_item_batch_work_item_id_fkey,
  ADD CONSTRAINT work_item_batch_work_item_id_fkey FOREIGN KEY (work_item_id) REFERENCES work_item(id) ON DELETE CASCADE;
ALTER TABLE work_item_batch_item DROP CONSTRAINT work_item_batch_item_batch_id_fkey,
  ADD CONSTRAINT work_item_batch_item_batch_id_fkey FOREIGN KEY (batch_id) REFERENCES work_item_batch(id) ON DELETE CASCADE;
