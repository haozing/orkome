package pgrepo

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"orkome/internal/platform/pg"
	"orkome/internal/skills/app"
)

type Repo struct{ DB *pg.DB }

func New(db *pg.DB) *Repo { return &Repo{DB: db} }

var _ app.Repo = (*Repo)(nil)

func (r *Repo) q(ctx context.Context) pg.DBTX { return r.DB.Tx(ctx) }

const skillCols = `id::text, workspace_id::text, name, description, origin, coalesce(origin_ref,''), coalesce(created_by::text,''), created_at`

func scanSkill(row pgx.Row) (app.Skill, error) {
	var s app.Skill
	err := row.Scan(&s.ID, &s.WorkspaceID, &s.Name, &s.Description, &s.Origin,
		&s.OriginRef, &s.CreatedBy, &s.CreatedAt)
	return s, err
}

func (r *Repo) InsertSkill(ctx context.Context, s app.Skill) (app.Skill, error) {
	return scanSkill(r.q(ctx).QueryRow(ctx, `
INSERT INTO skill (workspace_id, name, description, origin, origin_ref, created_by)
VALUES ($1,$2,$3,$4,$5,nullif($6,'')::uuid)
ON CONFLICT (workspace_id, name) DO UPDATE SET description=EXCLUDED.description,
  origin=EXCLUDED.origin, origin_ref=EXCLUDED.origin_ref
RETURNING `+skillCols,
		s.WorkspaceID, s.Name, s.Description, s.Origin, s.OriginRef, s.CreatedBy))
}

func (r *Repo) GetSkill(ctx context.Context, ws, id string) (app.Skill, bool, error) {
	out, err := scanSkill(r.q(ctx).QueryRow(ctx,
		`SELECT `+skillCols+` FROM skill WHERE workspace_id=$1 AND id=$2`, ws, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return out, false, nil
	}
	return out, err == nil, err
}

func (r *Repo) ListSkills(ctx context.Context, ws string) ([]app.Skill, error) {
	rows, err := r.q(ctx).Query(ctx,
		`SELECT `+skillCols+` FROM skill WHERE workspace_id=$1 ORDER BY name`, ws)
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

func (r *Repo) SearchSkills(ctx context.Context, ws, q string, limit int) ([]app.Skill, error) {
	if limit <= 0 || limit > 50 {
		limit = 20
	}
	rows, err := r.q(ctx).Query(ctx, fmt.Sprintf(`
SELECT %s FROM skill WHERE workspace_id=$1 AND (name ILIKE $2 OR description ILIKE $2)
ORDER BY name LIMIT %d`, skillCols, limit), ws, "%"+q+"%")
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

func (r *Repo) DeleteSkill(ctx context.Context, ws, id string) error {
	_, err := r.q(ctx).Exec(ctx, `DELETE FROM skill WHERE workspace_id=$1 AND id=$2`, ws, id)
	return err
}

func (r *Repo) UpdateSkill(ctx context.Context, ws, id string, name, description *string) (app.Skill, bool, error) {
	out, err := scanSkill(r.q(ctx).QueryRow(ctx, `
UPDATE skill SET name=coalesce($3,name), description=coalesce($4,description)
WHERE workspace_id=$1 AND id=$2
RETURNING `+skillCols, ws, id, name, description))
	if errors.Is(err, pgx.ErrNoRows) {
		return out, false, nil
	}
	return out, err == nil, err
}

func (r *Repo) ReplaceFiles(ctx context.Context, skillID string, files []app.SkillFile) error {
	return r.DB.Within(ctx, func(ctx context.Context) error {
		if _, err := r.q(ctx).Exec(ctx, `DELETE FROM skill_file WHERE skill_id=$1`, skillID); err != nil {
			return err
		}
		for _, f := range files {
			if _, err := r.q(ctx).Exec(ctx, `
INSERT INTO skill_file (skill_id, path, content, content_sha256, size_bytes)
VALUES ($1,$2,$3,$4,$5)`, skillID, f.Path, f.Content, f.SHA256, f.Size); err != nil {
				return err
			}
		}
		return nil
	})
}

func (r *Repo) ListFiles(ctx context.Context, ws, skillID string) ([]app.SkillFile, error) {
	rows, err := r.q(ctx).Query(ctx, `
SELECT f.id::text, f.path, f.content, f.content_sha256, f.size_bytes
FROM skill_file f JOIN skill s ON s.id=f.skill_id
WHERE s.workspace_id=$1 AND f.skill_id=$2 ORDER BY f.path`, ws, skillID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []app.SkillFile
	for rows.Next() {
		var f app.SkillFile
		if err := rows.Scan(&f.ID, &f.Path, &f.Content, &f.SHA256, &f.Size); err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

func (r *Repo) UpsertFile(ctx context.Context, skillID string, f app.SkillFile) error {
	_, err := r.q(ctx).Exec(ctx, `
INSERT INTO skill_file (skill_id, path, content, content_sha256, size_bytes)
VALUES ($1,$2,$3,$4,$5)
ON CONFLICT (skill_id, path) DO UPDATE SET
  content=EXCLUDED.content, content_sha256=EXCLUDED.content_sha256,
  size_bytes=EXCLUDED.size_bytes`, skillID, f.Path, f.Content, f.SHA256, f.Size)
	return err
}

func (r *Repo) GetFile(ctx context.Context, ws, skillID, fileID string) (app.SkillFile, bool, error) {
	var f app.SkillFile
	err := r.q(ctx).QueryRow(ctx, `
SELECT f.id::text, f.path, f.content, f.content_sha256, f.size_bytes
FROM skill_file f JOIN skill s ON s.id=f.skill_id
WHERE s.workspace_id=$1 AND f.skill_id=$2 AND f.id=$3`, ws, skillID, fileID).
		Scan(&f.ID, &f.Path, &f.Content, &f.SHA256, &f.Size)
	if errors.Is(err, pgx.ErrNoRows) {
		return f, false, nil
	}
	return f, err == nil, err
}

func (r *Repo) DeleteFile(ctx context.Context, ws, skillID, fileID string) error {
	_, err := r.q(ctx).Exec(ctx, `
DELETE FROM skill_file f USING skill s
WHERE s.id=f.skill_id AND s.workspace_id=$1 AND f.skill_id=$2 AND f.id=$3`, ws, skillID, fileID)
	return err
}

func (r *Repo) GetSkillByName(ctx context.Context, ws, name string) (app.Skill, bool, error) {
	out, err := scanSkill(r.q(ctx).QueryRow(ctx,
		`SELECT `+skillCols+` FROM skill WHERE workspace_id=$1 AND name=$2`, ws, name))
	if errors.Is(err, pgx.ErrNoRows) {
		return out, false, nil
	}
	return out, err == nil, err
}

func (r *Repo) SetAgentSkills(ctx context.Context, agentID string, skillIDs []string, enabled bool) error {
	return r.DB.Within(ctx, func(ctx context.Context) error {
		if _, err := r.q(ctx).Exec(ctx, `DELETE FROM agent_skill WHERE agent_id=$1`, agentID); err != nil {
			return err
		}
		for _, sid := range skillIDs {
			if _, err := r.q(ctx).Exec(ctx, `
INSERT INTO agent_skill (agent_id, skill_id, enabled) VALUES ($1,$2,$3)`, agentID, sid, enabled); err != nil {
				return err
			}
		}
		return nil
	})
}

func (r *Repo) AddAgentSkill(ctx context.Context, agentID, skillID string) error {
	_, err := r.q(ctx).Exec(ctx, `
INSERT INTO agent_skill (agent_id, skill_id, enabled) VALUES ($1,$2,true)
ON CONFLICT (agent_id, skill_id) DO NOTHING`, agentID, skillID)
	return pg.AsNotFound(err, "agent or skill not found")
}

func (r *Repo) SetAgentSkillEnabled(ctx context.Context, agentID, skillID string, en bool) error {
	_, err := r.q(ctx).Exec(ctx,
		`UPDATE agent_skill SET enabled=$3 WHERE agent_id=$1 AND skill_id=$2`, agentID, skillID, en)
	return err
}

func (r *Repo) RemoveAgentSkill(ctx context.Context, agentID, skillID string) error {
	_, err := r.q(ctx).Exec(ctx,
		`DELETE FROM agent_skill WHERE agent_id=$1 AND skill_id=$2`, agentID, skillID)
	return err
}

func (r *Repo) ListAgentSkills(ctx context.Context, ws, agentID string) ([]app.AgentSkill, error) {
	rows, err := r.q(ctx).Query(ctx, `
SELECT s.id::text, s.name, s.description, a.enabled
FROM agent_skill a JOIN skill s ON s.id=a.skill_id
JOIN agent ag ON ag.id=a.agent_id
WHERE ag.workspace_id=$1 AND a.agent_id=$2 ORDER BY s.name`, ws, agentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []app.AgentSkill
	for rows.Next() {
		var x app.AgentSkill
		if err := rows.Scan(&x.SkillID, &x.Name, &x.Description, &x.Enabled); err != nil {
			return nil, err
		}
		out = append(out, x)
	}
	return out, rows.Err()
}

func (r *Repo) ListSkillAgents(ctx context.Context, ws, skillID string) ([]app.SkillAgent, error) {
	rows, err := r.q(ctx).Query(ctx, `
SELECT ag.id::text, ag.name, a.enabled
FROM agent_skill a JOIN agent ag ON ag.id=a.agent_id
WHERE ag.workspace_id=$1 AND a.skill_id=$2 ORDER BY ag.name`, ws, skillID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []app.SkillAgent
	for rows.Next() {
		var x app.SkillAgent
		if err := rows.Scan(&x.AgentID, &x.Name, &x.Enabled); err != nil {
			return nil, err
		}
		out = append(out, x)
	}
	return out, rows.Err()
}
