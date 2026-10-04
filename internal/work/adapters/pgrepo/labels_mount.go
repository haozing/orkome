package pgrepo

import (
	"context"
	"orkome/internal/contract/apierr"

	"orkome/internal/work/app"
)

func (r *Repo) GetLabel(ctx context.Context, wsID, id string) (app.Label, bool, error) {
	var l app.Label
	err := r.q(ctx).QueryRow(ctx,
		`SELECT id, name, coalesce(color,''), 0 FROM label WHERE workspace_id=$1 AND id=$2`, wsID, id).
		Scan(&l.ID, &l.Name, &l.Color, &l.Count)
	if noRow(err) {
		return l, false, nil
	}
	return l, err == nil, err
}

func (r *Repo) AgentLabelTarget(ctx context.Context, wsID, agentID string) (bool, error) {
	var ok bool
	err := r.q(ctx).QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM agent WHERE workspace_id=$1 AND id=$2)`, wsID, agentID).Scan(&ok)
	return ok, err
}

func (r *Repo) SkillLabelTarget(ctx context.Context, wsID, skillID string) (bool, error) {
	var ok bool
	err := r.q(ctx).QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM skill WHERE workspace_id=$1 AND id=$2)`, wsID, skillID).Scan(&ok)
	return ok, err
}

func (r *Repo) AttachAgentLabel(ctx context.Context, wsID, agentID, labelID string) error {
	tag, err := r.q(ctx).Exec(ctx, `
INSERT INTO agent_to_label (agent_id, label_id)
SELECT a.id, l.id FROM agent a, label l
WHERE a.workspace_id=$1 AND a.id=$2 AND l.workspace_id=$1 AND l.id=$3
ON CONFLICT (agent_id, label_id) DO NOTHING`, wsID, agentID, labelID)
	if err != nil {
		return err
	}
	return r.errIfMountAbsent(ctx, "agent_to_label", "agent_id", agentID, "label_id", labelID, tag,
		"agent or label not found")
}

func (r *Repo) DetachAgentLabel(ctx context.Context, wsID, agentID, labelID string) error {
	_, err := r.q(ctx).Exec(ctx, `
DELETE FROM agent_to_label al
USING agent a
WHERE a.id = al.agent_id AND a.workspace_id=$1 AND al.agent_id=$2 AND al.label_id=$3`,
		wsID, agentID, labelID)
	return err
}

func (r *Repo) ListAgentLabels(ctx context.Context, wsID, agentID string) ([]app.Label, error) {
	rows, err := r.q(ctx).Query(ctx, `
SELECT l.id, l.name, coalesce(l.color,''), 0 FROM label l
JOIN agent_to_label al ON al.label_id = l.id
WHERE l.workspace_id=$1 AND al.agent_id=$2 ORDER BY l.name`, wsID, agentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []app.Label
	for rows.Next() {
		var l app.Label
		if err := rows.Scan(&l.ID, &l.Name, &l.Color, &l.Count); err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

func (r *Repo) AttachSkillLabel(ctx context.Context, wsID, skillID, labelID string) error {
	tag, err := r.q(ctx).Exec(ctx, `
INSERT INTO skill_to_label (skill_id, label_id)
SELECT s.id, l.id FROM skill s, label l
WHERE s.workspace_id=$1 AND s.id=$2 AND l.workspace_id=$1 AND l.id=$3
ON CONFLICT (skill_id, label_id) DO NOTHING`, wsID, skillID, labelID)
	if err != nil {
		return err
	}
	return r.errIfMountAbsent(ctx, "skill_to_label", "skill_id", skillID, "label_id", labelID, tag,
		"skill or label not found")
}

func (r *Repo) DetachSkillLabel(ctx context.Context, wsID, skillID, labelID string) error {
	_, err := r.q(ctx).Exec(ctx, `
DELETE FROM skill_to_label sl
USING skill s
WHERE s.id = sl.skill_id AND s.workspace_id=$1 AND sl.skill_id=$2 AND sl.label_id=$3`,
		wsID, skillID, labelID)
	return err
}

func (r *Repo) ListSkillLabels(ctx context.Context, wsID, skillID string) ([]app.Label, error) {
	rows, err := r.q(ctx).Query(ctx, `
SELECT l.id, l.name, coalesce(l.color,''), 0 FROM label l
JOIN skill_to_label sl ON sl.label_id = l.id
WHERE l.workspace_id=$1 AND sl.skill_id=$2 ORDER BY l.name`, wsID, skillID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []app.Label
	for rows.Next() {
		var l app.Label
		if err := rows.Scan(&l.ID, &l.Name, &l.Color, &l.Count); err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

func (r *Repo) errIfMountAbsent(ctx context.Context, table, colA, valA, colB, valB string,
	tag interface{ RowsAffected() int64 }, notFoundMsg string) error {
	if tag.RowsAffected() > 0 {
		return nil
	}
	var exists bool
	err := r.q(ctx).QueryRow(ctx,
		"SELECT EXISTS(SELECT 1 FROM "+table+" WHERE "+colA+"=$1 AND "+colB+"=$2)", valA, valB).Scan(&exists)
	if err != nil {
		return err
	}
	if !exists {
		return apierr.New(apierr.NotFound, notFoundMsg)
	}
	return nil
}
