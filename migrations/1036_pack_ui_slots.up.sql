-- orkome pack slots 通道（C2 / F2 后端半，docs/pack-frontend-design-2026-10-11.md §1.2）。
-- pack.yaml v1 的 ui.slots 段（slot 标识 v0 目录冻结：仅 workitem.detail.tab）
-- 由 pack-provision 解析后随 ui.entry 走同一个 PUT /api/ui-entries/{pack} 落库：
-- entry 表一 workspace 一 pack 一行（UNIQUE(workspace_id,pack)，迁移 1033），
-- slots 是 pack 级列表，扩一个 jsonb 列即最小路径——不建新表、不加 JOIN。
-- 元素形状：{"slot":"workitem.detail.tab","label":"文章预览",
--           "url_template":"https://…/{workItemId}?t={uiToken}","height":480}
-- NULL = 该 pack 无 slots（v0 pack 与未声明 slots 的 v1 pack 均如此）。
-- GET /api/ui-entries 响应 items[].slots 随行返回（一个端点两用，无新路由）。
ALTER TABLE pack_ui_entry ADD COLUMN slots jsonb;
