-- 能力包平台化（pack 平台化，承接 1042 的 pack 一等公民化）：
-- skill 加 pack_id 可空归属列。此前 skill 的 pack 来源只有
-- origin='pack' AND origin_ref=<pack_id> 一条路径（1042 §4 的 D1 口径）；
-- 平台化后技能允许"装后挂载/换挂/卸下"（POST /api/skills/{skillId}/pack），
-- 字符串键 origin_ref 无法承载 FK 级联语义，补一列与 agent.pack_id（1042 §3）
-- 对齐：pack 删则置空（行保留，技能有独立生命周期）。
ALTER TABLE skill ADD COLUMN pack_id uuid REFERENCES pack(id) ON DELETE SET NULL;

-- 详情页/列表页按 pack 维度取技能资产的高频读面；部分索引过滤掉
-- 手工/导入来源的绝大多数行。
CREATE INDEX idx_skill_pack_id ON skill(pack_id) WHERE pack_id IS NOT NULL;
