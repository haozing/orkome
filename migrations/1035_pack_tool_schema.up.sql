-- orkome pack 工具输出 schema 存档（B1：job-handle 三件套安装期闸门）。
-- pack-provision 安装 pack/v1 pack 时，把每个 MCP 工具最终生效的 output
-- schema（协议自描述优先，pack.yaml mcp.tools[].output 声明补位）存档于此；
-- 波 3 C1 的运行期桥接层读取本表，为未自描述工具补输出契约。
-- v0 pack 不写入（存档语义是 v1 契约的一部分）。
CREATE TABLE pack_tool_schema (
  id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  workspace_id  uuid NOT NULL REFERENCES workspace(id),
  pack          text NOT NULL,
  server        text NOT NULL,
  tool_name     text NOT NULL,
  output_schema jsonb NOT NULL,
  source        text NOT NULL CHECK (source IN ('protocol', 'pack_yaml', 'builtin_default')),
  installed_at  timestamptz NOT NULL DEFAULT now(),
  UNIQUE (workspace_id, pack, server, tool_name)
);

CREATE INDEX idx_pack_tool_schema_ws_tool ON pack_tool_schema (workspace_id, tool_name);
