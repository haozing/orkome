-- choreography 布尔退役（2026-10-12 产品裁决）：
-- "协同模式"不该是创建小队时由用户选择的静态属性——咨询（同轮秒回）与
-- 正式派发（独立生命周期）是 leader 按任务性质现场判断的两种执行方式。
-- 重构后 leader 双持：所有小队成员同时获得 consult 工具（进程内子 agent，
-- 无工具）与 dispatch_to_member（正式派发，独立任务全工具）。
-- v0 无兼容包袱：列直接删除。

ALTER TABLE squad DROP COLUMN choreography;
