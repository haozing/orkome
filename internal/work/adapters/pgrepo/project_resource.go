package pgrepo

import (
	"context"

	"github.com/jackc/pgx/v5"

	"orkome/internal/work/app"
)

func (r *Repo) ListProjectResources(ctx context.Context, wsID, projectID string) ([]app.ProjectResource, error) {
	rows, err := r.q(ctx).Query(ctx, `
SELECT pr.id, pr.project_id, pr.kind, pr.uri, pr.created_at
FROM project_resource pr JOIN project p ON p.id = pr.project_id
WHERE p.workspace_id=$1 AND p.id=$2
ORDER BY pr.created_at, pr.id`, wsID, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []app.ProjectResource
	for rows.Next() {
		res, err := scanPR(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, res)
	}
	return out, rows.Err()
}

func (r *Repo) CreateProjectResource(ctx context.Context, wsID, projectID, kind, uri string) (app.ProjectResource, bool, error) {
	res, err := scanPR(r.q(ctx).QueryRow(ctx, `
INSERT INTO project_resource (project_id, kind, uri)
SELECT p.id, $3, $4 FROM project p WHERE p.workspace_id=$1 AND p.id=$2
RETURNING id, project_id, kind, uri, created_at`, wsID, projectID, kind, uri))
	if noRow(err) {
		return res, false, nil
	}
	if err != nil {
		return res, false, err
	}
	return res, true, nil
}

func (r *Repo) UpdateProjectResource(ctx context.Context, wsID, projectID, resourceID, kind, uri string) (app.ProjectResource, bool, error) {
	res, err := scanPR(r.q(ctx).QueryRow(ctx, `
UPDATE project_resource pr SET kind=$4, uri=$5
FROM project p
WHERE p.id = pr.project_id AND p.workspace_id=$1 AND p.id=$2 AND pr.id=$3
RETURNING pr.id, pr.project_id, pr.kind, pr.uri, pr.created_at`, wsID, projectID, resourceID, kind, uri))
	if noRow(err) {
		return res, false, nil
	}
	if err != nil {
		return res, false, err
	}
	return res, true, nil
}

func (r *Repo) DeleteProjectResource(ctx context.Context, wsID, projectID, resourceID string) error {
	_, err := r.q(ctx).Exec(ctx, `
DELETE FROM project_resource pr
USING project p
WHERE p.id = pr.project_id AND p.workspace_id=$1 AND p.id=$2 AND pr.id=$3`,
		wsID, projectID, resourceID)
	return err
}

func scanPR(row pgx.Row) (app.ProjectResource, error) {
	var res app.ProjectResource
	err := row.Scan(&res.ID, &res.ProjectID, &res.Kind, &res.URI, &res.CreatedAt)
	return res, err
}
