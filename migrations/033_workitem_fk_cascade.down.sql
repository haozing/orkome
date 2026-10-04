ALTER TABLE comment DROP CONSTRAINT comment_work_item_id_fkey,
  ADD CONSTRAINT comment_work_item_id_fkey FOREIGN KEY (work_item_id) REFERENCES work_item(id);
ALTER TABLE comment DROP CONSTRAINT comment_parent_id_fkey,
  ADD CONSTRAINT comment_parent_id_fkey FOREIGN KEY (parent_id) REFERENCES comment(id);
ALTER TABLE comment_reaction DROP CONSTRAINT comment_reaction_comment_id_fkey,
  ADD CONSTRAINT comment_reaction_comment_id_fkey FOREIGN KEY (comment_id) REFERENCES comment(id);
ALTER TABLE work_item_to_label DROP CONSTRAINT work_item_to_label_work_item_id_fkey,
  ADD CONSTRAINT work_item_to_label_work_item_id_fkey FOREIGN KEY (work_item_id) REFERENCES work_item(id);
ALTER TABLE work_item_subscriber DROP CONSTRAINT work_item_subscriber_work_item_id_fkey,
  ADD CONSTRAINT work_item_subscriber_work_item_id_fkey FOREIGN KEY (work_item_id) REFERENCES work_item(id);
ALTER TABLE work_item_reaction DROP CONSTRAINT work_item_reaction_work_item_id_fkey,
  ADD CONSTRAINT work_item_reaction_work_item_id_fkey FOREIGN KEY (work_item_id) REFERENCES work_item(id);
ALTER TABLE work_item_child_event DROP CONSTRAINT work_item_child_event_parent_id_fkey,
  ADD CONSTRAINT work_item_child_event_parent_id_fkey FOREIGN KEY (parent_id) REFERENCES work_item(id);
ALTER TABLE work_item_child_event DROP CONSTRAINT work_item_child_event_child_id_fkey,
  ADD CONSTRAINT work_item_child_event_child_id_fkey FOREIGN KEY (child_id) REFERENCES work_item(id);
ALTER TABLE activity_log DROP CONSTRAINT activity_log_work_item_id_fkey,
  ADD CONSTRAINT activity_log_work_item_id_fkey FOREIGN KEY (work_item_id) REFERENCES work_item(id);
ALTER TABLE inbox_item DROP CONSTRAINT inbox_item_work_item_id_fkey,
  ADD CONSTRAINT inbox_item_work_item_id_fkey FOREIGN KEY (work_item_id) REFERENCES work_item(id);
ALTER TABLE work_item_wakeup DROP CONSTRAINT work_item_wakeup_work_item_id_fkey,
  ADD CONSTRAINT work_item_wakeup_work_item_id_fkey FOREIGN KEY (work_item_id) REFERENCES work_item(id);

ALTER TABLE skill_file DROP CONSTRAINT skill_file_skill_id_fkey,
  ADD CONSTRAINT skill_file_skill_id_fkey FOREIGN KEY (skill_id) REFERENCES skill(id);
ALTER TABLE agent_skill DROP CONSTRAINT agent_skill_skill_id_fkey,
  ADD CONSTRAINT agent_skill_skill_id_fkey FOREIGN KEY (skill_id) REFERENCES skill(id);
ALTER TABLE skill_to_label DROP CONSTRAINT skill_to_label_skill_id_fkey,
  ADD CONSTRAINT skill_to_label_skill_id_fkey FOREIGN KEY (skill_id) REFERENCES skill(id);
