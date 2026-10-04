package pgrepo

import (
	"context"
	"time"
)

func (r *Repo) InsertUIToken(ctx context.Context, jti, ws, user, plugin string, expiresAt time.Time) error {
	_, err := r.DB.Tx(ctx).Exec(ctx, `
INSERT INTO ui_token (jti, workspace_id, user_id, plugin, expires_at)
VALUES ($1,$2,$3,$4,$5) ON CONFLICT (jti) DO NOTHING`, jti, ws, user, plugin, expiresAt)
	return err
}

func (r *Repo) ConsumeUIToken(ctx context.Context, jti string, now time.Time) (ws, user, plugin string, ok bool, err error) {
	err = r.DB.Tx(ctx).QueryRow(ctx, `
UPDATE ui_token SET consumed_at = $2
WHERE jti = $1 AND consumed_at IS NULL AND expires_at > $2
RETURNING workspace_id::text, user_id::text, plugin`, jti, now).Scan(&ws, &user, &plugin)
	if errNoRow(err) {
		return "", "", "", false, nil
	}
	if err != nil {
		return "", "", "", false, err
	}
	return ws, user, plugin, true, nil
}
