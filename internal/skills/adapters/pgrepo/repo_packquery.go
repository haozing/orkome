package pgrepo

// Pack 聚合读端口（pack 一等公民化 P1，W1-2）：按 pack 维度读 skill 行。
// 返回 skills 本域类型（arch iron law rule 3）——pack 域的引用形状由
// 组合根适配器转换。

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5/pgconn"

	"orkome/internal/contract/apierr"
	"orkome/internal/skills/app"
)

// ListByOriginRef 返回 origin='pack' AND origin_ref=packID 的技能行
//（provision R3 装机即建的技能，1042 §4 D1 口径）。
// 保留原方法：provision 写面仍按 origin 键写入，语义不变。
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

// ListByPack 双源合并（能力包平台化，迁移 1045）：装机来源
//（origin='pack' AND origin_ref=pack_id）∪ 挂载来源（pack_id=pack_id）。
// 后者是"装后挂载"通道（POST /api/skills/{id}/pack 写列），两类都要出现在
// 详情页资产区块。UNION 去重：老数据换挂后 origin 键与 pack_id 键可能指向
// 同一行。非法 uuid 字面量映射 400（与 pack pgrepo 的 invalidUUID 同策略）。
func (r *Repo) ListByPack(ctx context.Context, ws, packID string) ([]app.Skill, error) {
	// 两分支参数分占（$2/$3）+ pack_id 显式 cast：同一参数若同时落在
	// text 列（origin_ref）与 uuid 列（pack_id）上下文，Postgres 会把
	// 未类型化参数统一解析成 text，uuid 分支报 42883——真机详情页 500
	// 实证（单表查询时 pgx 按列型推断没事，UNION 不行）。
	rows, err := r.q(ctx).Query(ctx, `
SELECT `+skillCols+` FROM skill WHERE workspace_id=$1 AND origin='pack' AND origin_ref=$2
UNION
SELECT `+skillCols+` FROM skill WHERE workspace_id=$1 AND pack_id=$3::uuid
ORDER BY name`, ws, packID, packID)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "22P02" {
			return nil, apierr.NewValidation("invalid pack id")
		}
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
