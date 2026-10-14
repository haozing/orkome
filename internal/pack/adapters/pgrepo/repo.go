// Package pgrepo 是 pack 域的 Postgres 存储实现（pack 表 + pack_subscription
// 连接表，迁移 1042）。写法对齐 internal/notification/adapters/pgrepo。
package pgrepo

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/haozing/ploykit/platform/pg"
	"orkome/internal/contract/apierr"
	"orkome/internal/pack/app"
)

func jsonUnmarshal(b []byte, v any) error { return json.Unmarshal(b, v) }

type Repo struct{ DB *pg.DB }

func New(db *pg.DB) *Repo { return &Repo{DB: db} }

var _ app.Repo = (*Repo)(nil)

func (r *Repo) q(ctx context.Context) pg.DBTX { return r.DB.Tx(ctx) }
func noRow(err error) bool                    { return errors.Is(err, pgx.ErrNoRows) }

const packCols = `id::text, workspace_id::text, name, version, installed_at`

func scanPack(row pgx.Row) (app.Pack, error) {
	var p app.Pack
	err := row.Scan(&p.ID, &p.WorkspaceID, &p.Name, &p.Version, &p.InstalledAt)
	return p, err
}

// Upsert 按 (workspace_id, name) 幂等写入：重装覆盖 version，installed_at
// 保留首装时刻（升级不是重装）。返回含 ID 的完整行（provision 第 0 步拿 ID）。
func (r *Repo) Upsert(ctx context.Context, ws, name, version string) (app.Pack, error) {
	return scanPack(r.q(ctx).QueryRow(ctx, `
INSERT INTO pack (workspace_id, name, version)
VALUES ($1,$2,$3)
ON CONFLICT (workspace_id, name) DO UPDATE SET version=EXCLUDED.version
RETURNING `+packCols, ws, name, version))
}

func (r *Repo) GetByID(ctx context.Context, ws, packID string) (app.Pack, bool, error) {
	p, err := scanPack(r.q(ctx).QueryRow(ctx,
		`SELECT `+packCols+` FROM pack WHERE workspace_id=$1 AND id=$2`, ws, packID))
	if noRow(err) {
		return app.Pack{}, false, nil
	}
	if err != nil {
		// 非法 uuid 输入（22P02）→ 400，与 skills pgrepo 的 AgentInWorkspace 同策略。
		if invalidUUID(err) {
			return app.Pack{}, false, apierr.NewValidation("invalid pack id")
		}
		return app.Pack{}, false, err
	}
	return p, true, nil
}

func (r *Repo) Delete(ctx context.Context, ws, packID string) error {
	tag, err := r.q(ctx).Exec(ctx,
		`DELETE FROM pack WHERE workspace_id=$1 AND id=$2`, ws, packID)
	if err != nil {
		if invalidUUID(err) {
			return apierr.NewValidation("invalid pack id")
		}
		return err
	}
	if tag.RowsAffected() == 0 {
		return apierr.New(apierr.NotFound, "pack not found")
	}
	return nil
}

func (r *Repo) List(ctx context.Context, ws string) ([]app.Pack, error) {
	rows, err := r.q(ctx).Query(ctx,
		`SELECT `+packCols+` FROM pack WHERE workspace_id=$1 ORDER BY installed_at, name`, ws)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []app.Pack
	for rows.Next() {
		p, err := scanPack(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// ---- pack_subscription 连接表 ----

// Link 建一条 pack × vendor 订阅的关联（幂等；订阅本体已由 provision 经
// vendor API 创建——本方法只动 orkome 侧连接表）。
func (r *Repo) Link(ctx context.Context, packID, subscriptionID string) error {
	_, err := r.q(ctx).Exec(ctx, `
INSERT INTO pack_subscription (pack_id, subscription_id)
VALUES ($1,$2)
ON CONFLICT (pack_id, subscription_id) DO NOTHING`, packID, subscriptionID)
	return err
}

// UnlinkAll 解绑一个 pack 的全部订阅关联（uninstall 用；订阅本体的删除仍走
// vendor API，不在此处）。
func (r *Repo) UnlinkAll(ctx context.Context, packID string) error {
	_, err := r.q(ctx).Exec(ctx,
		`DELETE FROM pack_subscription WHERE pack_id=$1`, packID)
	return err
}

// ListByPack 聚合读：连接表 JOIN vendor webhook_subscription，返回详情页
// 事件区块的订阅引用（URL/types/active/description/created_at）。本体行已被
// vendor 删除时（连接表残留）给零值行——DELETE 残留由下次 uninstall 清理。
func (r *Repo) ListByPack(ctx context.Context, ws, packID string) ([]app.SubRef, error) {
	rows, err := r.q(ctx).Query(ctx, `
SELECT ps.subscription_id, coalesce(s.url,''), coalesce(s.event_types,'[]'::jsonb), coalesce(s.is_active,false),
       coalesce(s.description,''), coalesce(s.created_at::text,'')
FROM pack_subscription ps
JOIN pack p ON p.id=ps.pack_id AND p.workspace_id=$1
LEFT JOIN webhook_subscription s ON s.id::text=ps.subscription_id
WHERE ps.pack_id=$2 ORDER BY ps.subscription_id`, ws, packID)
	if err != nil {
		if invalidUUID(err) {
			return nil, apierr.NewValidation("invalid pack id")
		}
		return nil, err
	}
	defer rows.Close()
	var out []app.SubRef
	for rows.Next() {
		var ref app.SubRef
		var types []byte
		if err := rows.Scan(&ref.SubscriptionID, &ref.URL, &types, &ref.IsActive, &ref.Description, &ref.CreatedAt); err != nil {
			return nil, err
		}
		if len(types) > 0 {
			if err := jsonUnmarshal(types, &ref.EventTypes); err != nil {
				return nil, err
			}
		}
		out = append(out, ref)
	}
	return out, rows.Err()
}

// invalidUUID 把 Postgres 的 invalid_text_representation（22P02，非法 uuid
// 字面量）识别出来——GetByID/Delete 的字符串入参直接进 ::uuid 比较时命中。
func invalidUUID(err error) bool {
	var pgErr *pgconn.PgError
	return err != nil && errors.As(err, &pgErr) && pgErr.Code == "22P02"
}
