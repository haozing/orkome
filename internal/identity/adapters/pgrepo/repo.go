package pgrepo

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"

	"orkome/internal/identity/app"
	"github.com/haozing/ploykit/platform/pg"
)

type Repo struct{ DB *pg.DB }

var _ app.Repo = (*Repo)(nil)

func (r *Repo) q(ctx context.Context) pg.DBTX { return r.DB.Tx(ctx) }

func errNoRow(err error) bool { return errors.Is(err, pgx.ErrNoRows) }

func (r *Repo) UpsertUserByEmail(ctx context.Context, email string) (app.User, error) {
	row := r.q(ctx).QueryRow(ctx, `
INSERT INTO "user" (email, display_name) VALUES ($1, $2)
ON CONFLICT (email) DO UPDATE SET updated_at = now()
RETURNING id, email, display_name, coalesce(avatar_url,''), language, timezone, created_at`,
		email, friendlyName(email))
	var u app.User
	err := row.Scan(&u.ID, &u.Email, &u.DisplayName, &u.AvatarURL, &u.Language, &u.Timezone, &u.CreatedAt)
	return u, err
}

func (r *Repo) GetUser(ctx context.Context, id string) (app.User, bool, error) {
	var u app.User
	err := r.q(ctx).QueryRow(ctx,
		`SELECT id, email, display_name, coalesce(avatar_url,''), language, timezone, created_at FROM "user" WHERE id=$1`, id).
		Scan(&u.ID, &u.Email, &u.DisplayName, &u.AvatarURL, &u.Language, &u.Timezone, &u.CreatedAt)
	if errNoRow(err) {
		return u, false, nil
	}
	return u, err == nil, err
}

func (r *Repo) GetUserByEmail(ctx context.Context, email string) (app.User, bool, error) {
	var u app.User
	err := r.q(ctx).QueryRow(ctx,
		`SELECT id, email, display_name, coalesce(avatar_url,''), language, timezone, created_at FROM "user" WHERE email=$1`, email).
		Scan(&u.ID, &u.Email, &u.DisplayName, &u.AvatarURL, &u.Language, &u.Timezone, &u.CreatedAt)
	if errNoRow(err) {
		return u, false, nil
	}
	return u, err == nil, err
}

func (r *Repo) UpdateUser(ctx context.Context, id, name, lang, tz string) (app.User, error) {
	var u app.User
	err := r.q(ctx).QueryRow(ctx, `
UPDATE "user" SET
  display_name = CASE WHEN $2 <> '' THEN $2 ELSE display_name END,
  language     = CASE WHEN $3 <> '' THEN $3 ELSE language END,
  timezone     = CASE WHEN $4 <> '' THEN $4 ELSE timezone END,
  updated_at   = now()
WHERE id=$1
RETURNING id, email, display_name, coalesce(avatar_url,''), language, timezone, created_at`,
		id, name, lang, tz).
		Scan(&u.ID, &u.Email, &u.DisplayName, &u.AvatarURL, &u.Language, &u.Timezone, &u.CreatedAt)
	return u, err
}

func (r *Repo) CreateWorkspace(ctx context.Context, name, slug, owner string) (app.Workspace, app.Member, error) {
	var ws app.Workspace
	var m app.Member
	err := r.DB.Within(ctx, func(ctx context.Context) error {
		if err := r.q(ctx).QueryRow(ctx,
			`INSERT INTO workspace (name, slug) VALUES ($1,$2) RETURNING id, name, slug, work_item_counter, created_at`,
			name, slug).
			Scan(&ws.ID, &ws.Name, &ws.Slug, &ws.WorkItemCounter, &ws.CreatedAt); err != nil {
			return err
		}
		return r.q(ctx).QueryRow(ctx,
			`INSERT INTO member (workspace_id, user_id, role) VALUES ($1,$2,'owner') RETURNING id, workspace_id, user_id, role, created_at`,
			ws.ID, owner).
			Scan(&m.ID, &m.WorkspaceID, &m.UserID, &m.Role, &m.CreatedAt)
	})
	return ws, m, err
}

func (r *Repo) GetWorkspace(ctx context.Context, id string) (app.Workspace, bool, error) {
	var ws app.Workspace
	err := r.q(ctx).QueryRow(ctx,
		`SELECT id, name, slug, work_item_counter, created_at FROM workspace WHERE id=$1`, id).
		Scan(&ws.ID, &ws.Name, &ws.Slug, &ws.WorkItemCounter, &ws.CreatedAt)
	if errNoRow(err) {
		return ws, false, nil
	}
	return ws, err == nil, err
}

func (r *Repo) GetWorkspaceBySlug(ctx context.Context, slug string) (app.Workspace, bool, error) {
	var ws app.Workspace
	err := r.q(ctx).QueryRow(ctx,
		`SELECT id, name, slug, work_item_counter, created_at FROM workspace WHERE slug=$1`, slug).
		Scan(&ws.ID, &ws.Name, &ws.Slug, &ws.WorkItemCounter, &ws.CreatedAt)
	if errNoRow(err) {
		return ws, false, nil
	}
	return ws, err == nil, err
}

func (r *Repo) UpdateWorkspace(ctx context.Context, id, name, slug string) (app.Workspace, error) {
	var ws app.Workspace
	err := r.q(ctx).QueryRow(ctx, `
UPDATE workspace SET
  name = CASE WHEN $2 <> '' THEN $2 ELSE name END,
  slug = CASE WHEN $3 <> '' THEN $3 ELSE slug END,
  updated_at = now()
WHERE id=$1 RETURNING id, name, slug, work_item_counter, created_at`,
		id, name, slug).
		Scan(&ws.ID, &ws.Name, &ws.Slug, &ws.WorkItemCounter, &ws.CreatedAt)
	return ws, err
}

func (r *Repo) DeleteWorkspace(ctx context.Context, id string, teardown app.WorkspaceTeardown) error {
	return r.DB.Within(ctx, func(ctx context.Context) error {

		if _, err := r.q(ctx).Exec(ctx, `SET LOCAL lock_timeout = '10s'`); err != nil {
			return err
		}
		var locked string
		err := r.q(ctx).QueryRow(ctx,
			`SELECT id FROM workspace WHERE id=$1 FOR UPDATE`, id).Scan(&locked)
		if errNoRow(err) {
			return nil
		}
		if err != nil {
			return err
		}
		if _, err := r.q(ctx).Exec(ctx,
			`SELECT pg_advisory_xact_lock(hashtext('ws-delete:' || $1))`, id); err != nil {
			return err
		}

		rows, err := r.q(ctx).Query(ctx,
			`SELECT user_id FROM member WHERE workspace_id=$1`, id)
		if err != nil {
			return err
		}
		var members []string
		for rows.Next() {
			var uid string
			if err := rows.Scan(&uid); err != nil {
				rows.Close()
				return err
			}
			members = append(members, uid)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return err
		}
		rows.Close()
		for _, uid := range members {
			if err := r.RevokeAllPATs(ctx, uid); err != nil {
				return err
			}
		}

		if teardown != nil {
			if err := teardown(ctx, id); err != nil {
				return err
			}
		}

		for _, stmt := range []string{
			`DELETE FROM workspace_invitation WHERE workspace_id=$1`,
			`DELETE FROM workspace_share_link WHERE workspace_id=$1`,
			`DELETE FROM member WHERE workspace_id=$1`,
			`DELETE FROM workspace WHERE id=$1`,
		} {
			if _, err := r.q(ctx).Exec(ctx, stmt, id); err != nil {
				return err
			}
		}
		return nil
	})
}

func (r *Repo) ListMyWorkspaces(ctx context.Context, userID string) ([]app.Workspace, error) {
	rows, err := r.q(ctx).Query(ctx, `
SELECT w.id, w.name, w.slug, w.work_item_counter, w.created_at
FROM workspace w JOIN member m ON m.workspace_id = w.id
WHERE m.user_id = $1 ORDER BY w.created_at`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []app.Workspace
	for rows.Next() {
		var ws app.Workspace
		if err := rows.Scan(&ws.ID, &ws.Name, &ws.Slug, &ws.WorkItemCounter, &ws.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, ws)
	}
	return out, rows.Err()
}

func (r *Repo) GetMember(ctx context.Context, wsID, userID string) (app.Member, bool, error) {
	var m app.Member
	err := r.q(ctx).QueryRow(ctx,
		`SELECT id, workspace_id, user_id, role, created_at FROM member WHERE workspace_id=$1 AND user_id=$2`,
		wsID, userID).
		Scan(&m.ID, &m.WorkspaceID, &m.UserID, &m.Role, &m.CreatedAt)
	if errNoRow(err) {
		return m, false, nil
	}
	return m, err == nil, err
}

func (r *Repo) GetMemberByID(ctx context.Context, memberID string) (app.Member, bool, error) {
	var m app.Member
	err := r.q(ctx).QueryRow(ctx,
		`SELECT id, workspace_id, user_id, role, created_at FROM member WHERE id=$1`, memberID).
		Scan(&m.ID, &m.WorkspaceID, &m.UserID, &m.Role, &m.CreatedAt)
	if errNoRow(err) {
		return m, false, nil
	}
	return m, err == nil, err
}

func (r *Repo) ListMembers(ctx context.Context, wsID string) ([]app.Membership, error) {
	rows, err := r.q(ctx).Query(ctx, `
SELECT m.id, m.workspace_id, m.user_id, m.role, m.created_at, u.email, u.display_name
FROM member m JOIN "user" u ON u.id = m.user_id
WHERE m.workspace_id=$1 ORDER BY m.created_at`, wsID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []app.Membership
	for rows.Next() {
		var x app.Membership
		if err := rows.Scan(&x.ID, &x.WorkspaceID, &x.UserID, &x.Role, &x.CreatedAt, &x.Email, &x.DisplayName); err != nil {
			return nil, err
		}
		out = append(out, x)
	}
	return out, rows.Err()
}

func (r *Repo) UpdateMemberRole(ctx context.Context, memberID, role string) (app.Member, error) {
	var m app.Member
	err := r.q(ctx).QueryRow(ctx,
		`UPDATE member SET role=$2 WHERE id=$1 RETURNING id, workspace_id, user_id, role, created_at`,
		memberID, role).
		Scan(&m.ID, &m.WorkspaceID, &m.UserID, &m.Role, &m.CreatedAt)
	return m, err
}

func (r *Repo) RemoveMember(ctx context.Context, memberID string) error {
	_, err := r.q(ctx).Exec(ctx, `DELETE FROM member WHERE id=$1`, memberID)
	return err
}

func (r *Repo) CountOwners(ctx context.Context, wsID string) (int, error) {
	var n int
	err := r.q(ctx).QueryRow(ctx,
		`SELECT count(*) FROM member WHERE workspace_id=$1 AND role='owner'`, wsID).Scan(&n)
	return n, err
}

func (r *Repo) AddMember(ctx context.Context, wsID, userID, role string) (app.Member, error) {
	var m app.Member
	err := r.q(ctx).QueryRow(ctx,
		`INSERT INTO member (workspace_id, user_id, role) VALUES ($1,$2,$3)
		 ON CONFLICT (workspace_id, user_id) DO UPDATE SET role=EXCLUDED.role
		 RETURNING id, workspace_id, user_id, role, created_at`,
		wsID, userID, role).
		Scan(&m.ID, &m.WorkspaceID, &m.UserID, &m.Role, &m.CreatedAt)
	return m, err
}

func (r *Repo) InsertCode(ctx context.Context, email, hash string, exp time.Time) error {
	_, err := r.q(ctx).Exec(ctx,
		`INSERT INTO verification_code (email, code_hash, expires_at) VALUES ($1,$2,$3)`, email, hash, exp)
	return err
}

func (r *Repo) LatestPendingCode(ctx context.Context, email string) (app.PendingCode, bool, error) {
	var c app.PendingCode
	err := r.q(ctx).QueryRow(ctx, `
SELECT id, code_hash, attempts, expires_at, created_at FROM verification_code
WHERE email=$1 AND consumed_at IS NULL
ORDER BY created_at DESC LIMIT 1`, email).
		Scan(&c.ID, &c.CodeHash, &c.Attempts, &c.ExpiresAt, &c.CreatedAt)
	if errNoRow(err) {
		return c, false, nil
	}
	return c, err == nil, err
}

func (r *Repo) ConsumeCode(ctx context.Context, id string) error {
	_, err := r.q(ctx).Exec(ctx,
		`UPDATE verification_code SET consumed_at=now() WHERE id=$1 AND consumed_at IS NULL`, id)
	return err
}

func (r *Repo) IncCodeAttempts(ctx context.Context, id string) error {
	_, err := r.q(ctx).Exec(ctx,
		`UPDATE verification_code SET attempts=attempts+1 WHERE id=$1`, id)
	return err
}

func (r *Repo) DeleteExpiredCodes(ctx context.Context, cutoff time.Time, limit int) (int, error) {
	tag, err := r.q(ctx).Exec(ctx, `
DELETE FROM verification_code WHERE id IN (
  SELECT id FROM verification_code WHERE expires_at < $1 LIMIT $2
)`, cutoff, limit)
	if err != nil {
		return 0, err
	}
	return int(tag.RowsAffected()), nil
}

func (r *Repo) CreateInvitation(ctx context.Context, wsID, email, role, by string, exp time.Time) (app.Invitation, error) {
	var inv app.Invitation
	err := r.q(ctx).QueryRow(ctx, `
INSERT INTO workspace_invitation (workspace_id, email, role, expires_at, created_by)
VALUES ($1,$2,$3,$4,$5)
RETURNING id, workspace_id, email, role, status, expires_at, created_by, created_at`,
		wsID, email, role, exp, by).
		Scan(&inv.ID, &inv.WorkspaceID, &inv.Email, &inv.Role, &inv.Status, &inv.ExpiresAt, &inv.CreatedBy, &inv.CreatedAt)
	return inv, err
}

func (r *Repo) HasPendingInvitation(ctx context.Context, wsID, email string) (bool, error) {
	var exists bool
	err := r.q(ctx).QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM workspace_invitation WHERE workspace_id=$1 AND email=$2 AND status='pending')`,
		wsID, email).Scan(&exists)
	return exists, err
}

func invitationCols(alias string) string {
	return alias + `.id, ` + alias + `.workspace_id, w.name, ` + alias + `.email, ` + alias + `.role, ` + alias + `.status, ` + alias + `.expires_at, ` + alias + `.created_by, ` + alias + `.created_at`
}

func (r *Repo) ListWorkspaceInvitations(ctx context.Context, wsID string) ([]app.Invitation, error) {
	rows, err := r.q(ctx).Query(ctx, `
SELECT `+invitationCols("i")+`
FROM workspace_invitation i JOIN workspace w ON w.id=i.workspace_id
WHERE i.workspace_id=$1 ORDER BY i.created_at DESC`, wsID)
	if err != nil {
		return nil, err
	}
	return scanInvitations(rows)
}

func (r *Repo) ListMyInvitations(ctx context.Context, email string) ([]app.Invitation, error) {
	rows, err := r.q(ctx).Query(ctx, `
SELECT `+invitationCols("i")+`
FROM workspace_invitation i JOIN workspace w ON w.id=i.workspace_id
WHERE i.email=$1 AND i.status='pending' AND i.expires_at > now()
ORDER BY i.created_at DESC`, email)
	if err != nil {
		return nil, err
	}
	return scanInvitations(rows)
}

func scanInvitations(rows pgx.Rows) ([]app.Invitation, error) {
	defer rows.Close()
	var out []app.Invitation
	for rows.Next() {
		var inv app.Invitation
		if err := rows.Scan(&inv.ID, &inv.WorkspaceID, &inv.WorkspaceName, &inv.Email, &inv.Role,
			&inv.Status, &inv.ExpiresAt, &inv.CreatedBy, &inv.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, inv)
	}
	return out, rows.Err()
}

func (r *Repo) GetInvitation(ctx context.Context, id string) (app.Invitation, bool, error) {
	var inv app.Invitation
	err := r.q(ctx).QueryRow(ctx, `
SELECT `+invitationCols("i")+`
FROM workspace_invitation i JOIN workspace w ON w.id=i.workspace_id
WHERE i.id=$1`, id).
		Scan(&inv.ID, &inv.WorkspaceID, &inv.WorkspaceName, &inv.Email, &inv.Role,
			&inv.Status, &inv.ExpiresAt, &inv.CreatedBy, &inv.CreatedAt)
	if errNoRow(err) {
		return inv, false, nil
	}
	return inv, err == nil, err
}

func (r *Repo) SetInvitationStatus(ctx context.Context, id, status string) error {
	_, err := r.q(ctx).Exec(ctx,
		`UPDATE workspace_invitation SET status=$2, updated_at=now() WHERE id=$1 AND status='pending'`, id, status)
	return err
}

func (r *Repo) DeleteInvitation(ctx context.Context, id string) error {
	_, err := r.q(ctx).Exec(ctx, `DELETE FROM workspace_invitation WHERE id=$1`, id)
	return err
}

func (r *Repo) CreateShareLink(ctx context.Context, wsID, code, role, by string, maxUses int, exp time.Time) (app.ShareLink, error) {
	var sl app.ShareLink
	err := r.q(ctx).QueryRow(ctx, `
INSERT INTO workspace_share_link (workspace_id, code, role, max_uses, expires_at, created_by)
VALUES ($1,$2,$3,$4,$5,$6)
RETURNING id, workspace_id, ''::text, code, role, max_uses, uses, expires_at, created_at`,
		wsID, code, role, maxUses, exp, by).
		Scan(&sl.ID, &sl.WorkspaceID, &sl.WorkspaceName, &sl.Code, &sl.Role, &sl.MaxUses, &sl.Uses, &sl.ExpiresAt, &sl.CreatedAt)
	return sl, err
}

func (r *Repo) ListShareLinks(ctx context.Context, wsID string) ([]app.ShareLink, error) {
	rows, err := r.q(ctx).Query(ctx, `
SELECT l.id, l.workspace_id, w.name, l.code, l.role, l.max_uses, l.uses, l.expires_at, l.created_at
FROM workspace_share_link l JOIN workspace w ON w.id=l.workspace_id
WHERE l.workspace_id=$1 ORDER BY l.created_at DESC`, wsID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []app.ShareLink
	for rows.Next() {
		var sl app.ShareLink
		if err := rows.Scan(&sl.ID, &sl.WorkspaceID, &sl.WorkspaceName, &sl.Code, &sl.Role,
			&sl.MaxUses, &sl.Uses, &sl.ExpiresAt, &sl.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, sl)
	}
	return out, rows.Err()
}

func (r *Repo) GetShareLinkByCode(ctx context.Context, code string) (app.ShareLink, bool, error) {
	var sl app.ShareLink
	err := r.q(ctx).QueryRow(ctx, `
SELECT l.id, l.workspace_id, w.name, l.code, l.role, l.max_uses, l.uses, l.expires_at, l.created_at
FROM workspace_share_link l JOIN workspace w ON w.id=l.workspace_id
WHERE l.code=$1`, code).
		Scan(&sl.ID, &sl.WorkspaceID, &sl.WorkspaceName, &sl.Code, &sl.Role,
			&sl.MaxUses, &sl.Uses, &sl.ExpiresAt, &sl.CreatedAt)
	if errNoRow(err) {
		return sl, false, nil
	}
	return sl, err == nil, err
}

func (r *Repo) DeleteShareLink(ctx context.Context, id string) error {
	_, err := r.q(ctx).Exec(ctx, `DELETE FROM workspace_share_link WHERE id=$1`, id)
	return err
}

func (r *Repo) ConsumeShareUse(ctx context.Context, code string) (app.ShareLink, bool, error) {
	var sl app.ShareLink
	err := r.q(ctx).QueryRow(ctx, `
UPDATE workspace_share_link l SET uses = l.uses + 1
FROM workspace w
WHERE w.id = l.workspace_id AND l.code=$1
  AND l.expires_at > now()
  AND (l.max_uses = 0 OR l.uses < l.max_uses)
RETURNING l.id, l.workspace_id, w.name, l.code, l.role, l.max_uses, l.uses, l.expires_at, l.created_at`,
		code).
		Scan(&sl.ID, &sl.WorkspaceID, &sl.WorkspaceName, &sl.Code, &sl.Role,
			&sl.MaxUses, &sl.Uses, &sl.ExpiresAt, &sl.CreatedAt)
	if errNoRow(err) {
		return sl, false, nil
	}
	return sl, err == nil, err
}

func (r *Repo) InsertPAT(ctx context.Context, userID, name, hash, prefix string, exp *time.Time) (app.PAT, error) {
	var p app.PAT
	err := r.q(ctx).QueryRow(ctx, `
INSERT INTO personal_access_token (user_id, name, token_hash, prefix, expires_at)
VALUES ($1,$2,$3,$4,$5)
RETURNING id, name, prefix, last_used_at, expires_at, revoked_at, created_at`,
		userID, name, hash, prefix, exp).
		Scan(&p.ID, &p.Name, &p.Prefix, &p.LastUsedAt, &p.ExpiresAt, &p.RevokedAt, &p.CreatedAt)
	return p, err
}

func (r *Repo) ListPATs(ctx context.Context, userID string) ([]app.PAT, error) {
	rows, err := r.q(ctx).Query(ctx, `
SELECT id, name, prefix, last_used_at, expires_at, revoked_at, created_at
FROM personal_access_token WHERE user_id=$1 ORDER BY created_at DESC`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []app.PAT
	for rows.Next() {
		var p app.PAT
		if err := rows.Scan(&p.ID, &p.Name, &p.Prefix, &p.LastUsedAt, &p.ExpiresAt, &p.RevokedAt, &p.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (r *Repo) RevokePAT(ctx context.Context, id, userID string) error {
	_, err := r.q(ctx).Exec(ctx,
		`UPDATE personal_access_token SET revoked_at=now() WHERE id=$1 AND user_id=$2 AND revoked_at IS NULL`, id, userID)
	return err
}

func (r *Repo) RevokeAllPATs(ctx context.Context, userID string) error {
	_, err := r.q(ctx).Exec(ctx,
		`UPDATE personal_access_token SET revoked_at=now() WHERE user_id=$1 AND revoked_at IS NULL`, userID)
	return err
}

func (r *Repo) GetPATByHash(ctx context.Context, hash string) (app.PAT, string, bool, error) {
	var p app.PAT
	var userID string
	err := r.q(ctx).QueryRow(ctx, `
SELECT id, name, prefix, last_used_at, expires_at, revoked_at, created_at, user_id
FROM personal_access_token WHERE token_hash=$1`, hash).
		Scan(&p.ID, &p.Name, &p.Prefix, &p.LastUsedAt, &p.ExpiresAt, &p.RevokedAt, &p.CreatedAt, &userID)
	if errNoRow(err) {
		return p, "", false, nil
	}
	return p, userID, err == nil, err
}

func (r *Repo) TouchPAT(ctx context.Context, id string) error {
	_, err := r.q(ctx).Exec(ctx, `UPDATE personal_access_token SET last_used_at=now() WHERE id=$1`, id)
	return err
}

func (r *Repo) ListPins(ctx context.Context, wsID, userID string) ([]app.Pin, error) {
	rows, err := r.q(ctx).Query(ctx, `
SELECT p.id, p.workspace_id, p.item_type, p.item_id, p.position, p.created_at
FROM pinned_item p JOIN workspace w ON w.id = p.workspace_id
WHERE p.workspace_id=$1 AND p.user_id=$2
ORDER BY p.position, p.created_at`, wsID, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []app.Pin
	for rows.Next() {
		var p app.Pin
		if err := rows.Scan(&p.ID, &p.WorkspaceID, &p.ItemType, &p.ItemID, &p.Position, &p.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (r *Repo) AddPin(ctx context.Context, wsID, userID, itemType, itemID string) (app.Pin, bool, error) {
	var p app.Pin
	err := r.q(ctx).QueryRow(ctx, `
WITH next AS (
  SELECT coalesce(max(position), 0) + 1 AS pos FROM pinned_item
  WHERE workspace_id=$1 AND user_id=$2
)
INSERT INTO pinned_item (workspace_id, user_id, item_type, item_id, position)
SELECT $1, $2, $3, $4, pos FROM next
ON CONFLICT (workspace_id, user_id, item_type, item_id) DO NOTHING
RETURNING id, workspace_id, item_type, item_id, position, created_at`,
		wsID, userID, itemType, itemID).
		Scan(&p.ID, &p.WorkspaceID, &p.ItemType, &p.ItemID, &p.Position, &p.CreatedAt)
	if errNoRow(err) {
		err = r.q(ctx).QueryRow(ctx, `
SELECT id, workspace_id, item_type, item_id, position, created_at
FROM pinned_item
WHERE workspace_id=$1 AND user_id=$2 AND item_type=$3 AND item_id=$4`,
			wsID, userID, itemType, itemID).
			Scan(&p.ID, &p.WorkspaceID, &p.ItemType, &p.ItemID, &p.Position, &p.CreatedAt)
		return p, false, err
	}
	return p, err == nil, err
}

func (r *Repo) ReorderPins(ctx context.Context, wsID, userID string, order []string) error {
	return r.DB.Within(ctx, func(ctx context.Context) error {
		for i, id := range order {
			tag, err := r.q(ctx).Exec(ctx, `
UPDATE pinned_item SET position=$3
WHERE workspace_id=$1 AND user_id=$2 AND id=$4`, wsID, userID, i+1, id)
			if err != nil {
				return err
			}
			if tag.RowsAffected() != 1 {
				return errors.New("pgrepo: pin row not owned by user during reorder")
			}
		}
		return nil
	})
}

func (r *Repo) RemovePin(ctx context.Context, wsID, userID, itemType, itemID string) error {
	_, err := r.q(ctx).Exec(ctx, `
DELETE FROM pinned_item
WHERE workspace_id=$1 AND user_id=$2 AND item_type=$3 AND item_id=$4`,
		wsID, userID, itemType, itemID)
	return err
}

func friendlyName(email string) string {
	local := email
	for i := 0; i < len(email); i++ {
		if email[i] == '@' {
			local = email[:i]
			break
		}
	}
	out := make([]rune, 0, len(local))
	upper := true
	for _, c := range local {
		if c == '.' || c == '_' || c == '-' {
			out = append(out, ' ')
			upper = true
			continue
		}
		if upper && c >= 'a' && c <= 'z' {
			c -= 'a' - 'A'
		}
		upper = false
		out = append(out, c)
	}
	return string(out)
}

func New(db *pg.DB) *Repo { return &Repo{DB: db} }
