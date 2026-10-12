-- v0 无兼容：直接删列。存量 outcome 数据不迁移不保留（裁决：无兼容包袱）。
ALTER TABLE work_item DROP COLUMN outcome;
