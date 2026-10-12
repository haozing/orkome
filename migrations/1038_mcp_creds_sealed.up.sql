-- W2-3（wave2）/ FP-09 P1-3：MCP 服务器凭证接入 sealx 密封链——存量明文一次性清空。
--
-- 背景：workspace_mcp_server.credentials / agent_mcp_server.credentials（迁移 1003:32/40
-- 建 jsonb 列）此前由 pgrepo credJSON() 直接 json.Marshal 明文落库，绕过同仓模型凭证
-- 的 Seal（sealx AES-GCM）+ fail-closed 先例（agent_model_credential.encrypted_secret）。
-- 第三方 API Key（Bearer 头）明文躺在 DB。
--
-- 配套代码改动（同波）：pgrepo/repo_mcp.go 写入经 sealCreds() 密封为
-- {"sealed":"<base64>"} 信封（Seal 未接线写非空凭证直接报错，fail-closed）；
-- 读取 unsealCreds() 只接受信封，其余形状（含本迁移清空前的存量明文残留）
-- 一律报错拒绝，绝不回落明文。
--
-- v0 无兼容裁决：存量明文直接清空，不做明文→密文的就地转换——当前库无生产
-- 凭证，重装即重填；带着明文跑转换器反而把"短暂可读的明文"固化进迁移历史。
-- 语义随 Wave 2 统一为三态：PUT 缺 credentials 字段=保留、显式 null/{}=清空。

UPDATE workspace_mcp_server SET credentials = '{}'::jsonb WHERE credentials::text <> '{}';
UPDATE agent_mcp_server SET credentials = '{}'::jsonb WHERE credentials::text <> '{}';
