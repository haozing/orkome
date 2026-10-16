-- 集成页终态 W1（docs/integrations-page-design-2026-10-15.md §2.3）：手动页面入口
-- 的 pack 名唯一键。迁移 1042 已 DROP 旧的 UNIQUE(workspace_id, pack)，只剩
-- UNIQUE(workspace_id, pack_id)；手动入口 pack_id 为 NULL，同工作区同名 PUT
-- 会失去冲突目标——以部分唯一索引补上：手动入口按 pack 名唯一（幂等覆盖依据），
-- provision 行有 pack_id 不落入谓词，仍由 1042 的 UNIQUE(workspace_id, pack_id) 管。
CREATE UNIQUE INDEX pack_ui_entry_ws_manual_pack_key
  ON pack_ui_entry (workspace_id, pack)
  WHERE pack_id IS NULL;
