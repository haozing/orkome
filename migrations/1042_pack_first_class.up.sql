-- Pack 一等公民化（P1，docs/pack-first-class-design-2026-10-13.md §1）。
-- v0 无兼容原则：不回填，装过的 pack 重跑 provision 即迁入新结构。
-- 核心变化：pack 从"散落 5 张表的字符串键"升级为 pack 实体表聚合根，
-- 全部关联走 FK，删除语义分层：
--   派生物     CASCADE  （pack_tool_schema / pack_ui_entry / pack_subscription）
--   独立生命体 SET NULL （workspace_mcp_server / agent——行保留，来源失联）
--   跨系统     显式清理 （webhook 订阅本体走 vendor API，orkome 侧只留连接表）

-- 1. pack 聚合根。
CREATE TABLE pack (
  id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  workspace_id uuid NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
  name         text NOT NULL,                -- 如 agentblog
  version      text NOT NULL DEFAULT '',     -- pack.yaml version，重装覆盖
  installed_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE (workspace_id, name)
);

-- 2. MCP 注册表行加来源（NULL=手工注册；pack 删则置空，行保留独立生命周期）。
ALTER TABLE workspace_mcp_server ADD COLUMN pack_id uuid REFERENCES pack(id) ON DELETE SET NULL;

-- 3. agent 加来源（同上；archived 软删由 uninstall 显式做，不在本迁移内）。
ALTER TABLE agent ADD COLUMN pack_id uuid REFERENCES pack(id) ON DELETE SET NULL;

-- 4. pack_ui_entry：pack text 键升级为 pack_id FK（一 workspace 一 pack 一行）。
--    旧 pack text 列保留（存量读面 GET /api/ui-entries 仍按它返回；v0 无兼容
--    包袱，provision 重装后两侧同写）。UNIQUE(workspace_id, pack) 换成
--    UNIQUE(workspace_id, pack_id)；pack_id 加 UNIQUE 保证一 pack 一入口。
ALTER TABLE pack_ui_entry ADD COLUMN pack_id uuid REFERENCES pack(id) ON DELETE CASCADE;
ALTER TABLE pack_ui_entry DROP CONSTRAINT IF EXISTS pack_ui_entry_workspace_id_pack_key;
ALTER TABLE pack_ui_entry ADD CONSTRAINT pack_ui_entry_ws_pack_unique UNIQUE (workspace_id, pack_id);

-- 5. pack_tool_schema：pack text 键升级为 pack_id FK，存档随 pack 删（CASCADE）。
--    1035 的 UNIQUE 约束是匿名表级约束，PostgreSQL 默认命名为
--    pack_tool_schema_workspace_id_pack_server_tool_name_key。
ALTER TABLE pack_tool_schema ADD COLUMN pack_id uuid REFERENCES pack(id) ON DELETE CASCADE;
ALTER TABLE pack_tool_schema DROP CONSTRAINT IF EXISTS pack_tool_schema_workspace_id_pack_server_tool_name_key;
ALTER TABLE pack_tool_schema ADD CONSTRAINT pack_tool_schema_ws_pack_server_tool_unique UNIQUE (workspace_id, pack_id, server, tool_name);

-- 6. 连接表：pack × vendor webhook_subscription（vendor 表不可加列，D3 的
--    干净解——订阅本体删除仍走 vendor API，orkome 侧只管关联）。
CREATE TABLE pack_subscription (
  pack_id         uuid NOT NULL REFERENCES pack(id) ON DELETE CASCADE,
  subscription_id text NOT NULL,
  UNIQUE (pack_id, subscription_id)
);
