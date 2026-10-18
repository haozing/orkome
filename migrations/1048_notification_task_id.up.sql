-- W1-1 通知内联裁决：通知行直挂 task_id。
--
-- 动机：审批停车（orchestration park）写 approval_request 通知时此前不带
-- task_id，通知弹窗的裁决按钮拿不到任务 id，只能跳工单页再找任务——裁决
-- 要 3 跳。本列让通知行直接携带任务 id，前端弹窗内联调
-- POST /api/tasks/{taskId}/approve|deny 一步完成。
--
-- 形状：uuid，可空。存量通知（及所有非审批类通知）保持 NULL，前端 optional
-- 消费（task_id 缺省时不渲染内联按钮），不写回填迁移。
-- 无 FK：通知是投递副产物，任务行删除不应阻塞/级联通知清理（归档清扫链路
-- DeleteArchivedBefore 独立管理生命周期）。

ALTER TABLE notification ADD COLUMN IF NOT EXISTS task_id uuid;
