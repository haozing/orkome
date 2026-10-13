package pgrepo

// Pack 聚合读端口（pack 一等公民化 P1，W1-2）：按 pack 维度读 skill 行。
// 命中条件 origin='pack' AND origin_ref=pack_id（D1 修复的数据面——provision
// R3 起 POST /api/skills 带 origin='pack', origin_ref=<pack_id>）。返回 skills
// 本域类型（arch iron law rule 3）——pack 域的引用形状由组合根适配器转换。

import (
	"context"

	"orkome/internal/skills/app"
)

// ListByOriginRef 返回 origin='pack' AND origin_ref=packID 的技能行
//（详情页"资产"区块的 skills 计数与清单）。
func (r *Repo) ListByOriginRef(ctx context.Context, ws, packID string) ([]app.Skill, error) {
	rows, err := r.q(ctx).Query(ctx,
		`SELECT `+skillCols+` FROM skill WHERE workspace_id=$1 AND origin='pack' AND origin_ref=$2 ORDER BY name`, ws, packID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []app.Skill
	for rows.Next() {
		s, err := scanSkill(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}
