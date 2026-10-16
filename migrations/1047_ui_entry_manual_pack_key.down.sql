-- 1047 逆向：收回手动入口的 pack 名唯一键（手动入口行本身保留——
-- 该索引只是 1042 丢掉 UNIQUE(workspace_id, pack) 后手动路径的冲突目标）。
DROP INDEX IF EXISTS pack_ui_entry_ws_manual_pack_key;
