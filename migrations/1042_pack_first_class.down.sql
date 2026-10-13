-- 完全逆序回滚迁移 1042（pack 一等公民化）。

-- 6. 连接表。
DROP TABLE IF EXISTS pack_subscription;

-- 5. pack_tool_schema：去掉 pack_id 列，恢复 1035 的匿名 UNIQUE 形状。
ALTER TABLE pack_tool_schema DROP CONSTRAINT IF EXISTS pack_tool_schema_ws_pack_server_tool_unique;
ALTER TABLE pack_tool_schema ADD CONSTRAINT pack_tool_schema_workspace_id_pack_server_tool_name_key UNIQUE (workspace_id, pack, server, tool_name);
ALTER TABLE pack_tool_schema DROP COLUMN IF EXISTS pack_id;

-- 4. pack_ui_entry：恢复 (workspace_id, pack) UNIQUE，去掉 pack_id 列。
ALTER TABLE pack_ui_entry DROP CONSTRAINT IF EXISTS pack_ui_entry_ws_pack_unique;
ALTER TABLE pack_ui_entry ADD CONSTRAINT pack_ui_entry_workspace_id_pack_key UNIQUE (workspace_id, pack);
ALTER TABLE pack_ui_entry DROP COLUMN IF EXISTS pack_id;

-- 3. agent 来源列。
ALTER TABLE agent DROP COLUMN IF EXISTS pack_id;

-- 2. MCP 来源列。
ALTER TABLE workspace_mcp_server DROP COLUMN IF EXISTS pack_id;

-- 1. pack 聚合根（最后删——上面全部 FK 指向它）。
DROP TABLE IF EXISTS pack;
