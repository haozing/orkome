package pgrepo

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/haozing/ploykit/platform/pg"
	"orkome/internal/contract/apierr"
	"orkome/internal/work/app"
	workdomain "orkome/internal/work/domain"
)

type Repo struct{ DB *pg.DB }

var _ app.Repo = (*Repo)(nil)

func (r *Repo) q(ctx context.Context) pg.DBTX { return r.DB.Tx(ctx) }
func noRow(err error) bool                    { return errors.Is(err, pgx.ErrNoRows) }

const statusSeed = `INSERT INTO work_item_status (workspace_id, key, name, category, is_builtin, position)
SELECT $1, s.key, s.name, s.category, true, s.pos FROM (VALUES
 ('backlog','Backlog','unstarted',0),('todo','Todo','unstarted',1),('in_progress','In Progress','started',2),
 ('in_review','In Review','started',3),('blocked','Blocked','started',4),('done','Done','done',5),('cancelled','Cancelled','closed',6)
) AS s(key,name,category,pos) ON CONFLICT (workspace_id, key) DO NOTHING`

func (r *Repo) EnsureStatusSeed(ctx context.Context, wsID string) error {
	_, err := r.q(ctx).Exec(ctx, statusSeed, wsID)
	return err
}

func (r *Repo) ListStatuses(ctx context.Context, wsID string) ([]app.StatusDef, error) {
	rows, err := r.q(ctx).Query(ctx,
		`SELECT key, name, category, is_builtin, position, icon FROM work_item_status WHERE workspace_id=$1 AND archived_at IS NULL ORDER BY position`, wsID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []app.StatusDef
	for rows.Next() {
		var d app.StatusDef
		if err := rows.Scan(&d.Key, &d.Name, &d.Category, &d.IsBuiltin, &d.Position, &d.Icon); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

func (r *Repo) CreateStatus(ctx context.Context, wsID, key, name, category string, pos int) (app.StatusDef, error) {
	_, err := r.q(ctx).Exec(ctx,
		`INSERT INTO work_item_status (workspace_id, key, name, category, position) VALUES ($1,$2,$3,$4,$5)`,
		wsID, key, name, category, pos)
	return app.StatusDef{Key: key, Name: name, Category: category, Position: pos}, err
}

func (r *Repo) GetStatus(ctx context.Context, wsID, key string) (app.StatusDef, bool, error) {
	var d app.StatusDef
	err := r.q(ctx).QueryRow(ctx,
		`SELECT key, name, category, is_builtin, position, icon FROM work_item_status WHERE workspace_id=$1 AND key=$2 AND archived_at IS NULL`,
		wsID, key).Scan(&d.Key, &d.Name, &d.Category, &d.IsBuiltin, &d.Position, &d.Icon)
	if noRow(err) {
		return d, false, nil
	}
	return d, err == nil, err
}

func (r *Repo) UpdateStatus(ctx context.Context, wsID, key string, p app.StatusDefPatch) (app.StatusDef, bool, error) {
	sets := []string{"updated_at = now()"}
	args := []any{wsID, key}
	n := 2
	add := func(sql string, v any) { n++; sets = append(sets, fmt.Sprintf(sql, n)); args = append(args, v) }
	if p.Name != nil {
		add("name = $%d", *p.Name)
	}
	if p.Icon != nil {
		add("icon = $%d", *p.Icon)
	}
	if p.Category != nil {
		add("category = $%d", *p.Category)
	}
	var d app.StatusDef
	err := r.q(ctx).QueryRow(ctx, fmt.Sprintf(
		`UPDATE work_item_status SET %s WHERE workspace_id=$1 AND key=$2 AND archived_at IS NULL
RETURNING key, name, category, is_builtin, position, icon`, strings.Join(sets, ", ")),
		args...).Scan(&d.Key, &d.Name, &d.Category, &d.IsBuiltin, &d.Position, &d.Icon)
	if noRow(err) {
		return d, false, nil
	}
	return d, err == nil, err
}

func (r *Repo) ReorderStatuses(ctx context.Context, wsID string, keys []string) error {
	return r.DB.Within(ctx, func(ctx context.Context) error {
		for i, k := range keys {
			if _, err := r.q(ctx).Exec(ctx,
				`UPDATE work_item_status SET position=$3, updated_at=now() WHERE workspace_id=$1 AND key=$2`, wsID, k, i); err != nil {
				return err
			}
		}
		return nil
	})
}

const wiCols = `id, workspace_id, number, title, description, acceptance_criteria, status_key, priority,
assignee_type, coalesce(assignee_id::text,''), creator_type, creator_id, coalesce(parent_work_item_id::text,''),
coalesce(project_id::text,''), position, coalesce(due_date::text,''), coalesce(start_date::text,''), metadata, properties, revision,
triage_state, coalesce(duplicate_of_work_item_id::text,''), created_at, updated_at, last_activity_at, outcome`

func scanWI(row pgx.Row) (app.WorkItem, error) {
	var w app.WorkItem
	err := row.Scan(&w.ID, &w.WorkspaceID, &w.Number, &w.Title, &w.Description, &w.AcceptanceCriteria,
		&w.StatusKey, &w.Priority, &w.AssigneeType, &w.AssigneeID, &w.CreatorType, &w.CreatorID,
		&w.ParentID, &w.ProjectID, &w.Position, &w.DueDate, &w.StartDate, &w.Metadata, &w.Properties, &w.Revision,
		&w.TriageState, &w.DuplicateOf, &w.CreatedAt, &w.UpdatedAt, &w.LastActivityAt, &w.Outcome)
	return w, err
}

func (r *Repo) CreateWorkItem(ctx context.Context, in app.NewWorkItem) (app.WorkItem, error) {
	var w app.WorkItem
	err := r.DB.Within(ctx, func(ctx context.Context) error {
		var num int64
		if err := r.q(ctx).QueryRow(ctx,
			`UPDATE workspace SET work_item_counter = work_item_counter + 1 WHERE id=$1 RETURNING work_item_counter`,
			in.WorkspaceID).Scan(&num); err != nil {
			return err
		}
		pos, err := r.NextPositionInStatus(ctx, in.WorkspaceID, in.StatusKey)
		if err != nil {
			return err
		}
		assigneeType := in.AssigneeType
		if assigneeType == "" {
			assigneeType = "none"
		}
		return r.q(ctx).QueryRow(ctx, fmt.Sprintf(`
INSERT INTO work_item (workspace_id, number, title, description, status_key, priority,
  assignee_type, assignee_id, creator_type, creator_id, parent_work_item_id, project_id, position,
  origin_type, origin_id, triage_state)
VALUES ($1,$2,$3,$4,$5,$6,$7,nullif($8,'')::uuid,$9,$10,nullif($11,'')::uuid,nullif($12,'')::uuid,$13,$14,nullif($15,'')::uuid,nullif($16,''))
RETURNING %s`, wiCols),
			in.WorkspaceID, num, in.Title, in.Description, in.StatusKey, in.Priority,
			assigneeType, in.AssigneeID, in.CreatorType, in.CreatorID, in.ParentID, in.ProjectID,
			pos, in.OriginType, in.OriginID, in.TriageState).
			Scan(&w.ID, &w.WorkspaceID, &w.Number, &w.Title, &w.Description, &w.AcceptanceCriteria,
				&w.StatusKey, &w.Priority, &w.AssigneeType, &w.AssigneeID, &w.CreatorType, &w.CreatorID,
				&w.ParentID, &w.ProjectID, &w.Position, &w.DueDate, &w.StartDate, &w.Metadata, &w.Properties, &w.Revision,
				&w.TriageState, &w.DuplicateOf, &w.CreatedAt, &w.UpdatedAt, &w.LastActivityAt, &w.Outcome)
	})
	return w, err
}

func (r *Repo) GetWorkItem(ctx context.Context, wsID, id string) (app.WorkItem, bool, error) {
	w, err := scanWI(r.q(ctx).QueryRow(ctx,
		fmt.Sprintf(`SELECT %s FROM work_item WHERE workspace_id=$1 AND id=$2`, wiCols), wsID, id))
	if noRow(err) {
		return w, false, nil
	}
	return w, err == nil, err
}

// WriteOutcome 后写覆盖工单产出（"需求→产出"一等公民，迁移 1039）。
// 由任务结算链路调用（orchapp.OutcomeWriter 适配）；工单不存在时返回错误
// 由调用方裁决（结算侧吞错降级为仅评论兜底）。
func (r *Repo) WriteOutcome(ctx context.Context, wsID, wiID string, o app.Outcome) error {
	tag, err := r.q(ctx).Exec(ctx,
		`UPDATE work_item SET outcome = $3 WHERE workspace_id=$1 AND id=$2`,
		wsID, wiID, o)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("work item %s not found in workspace %s", wiID, wsID)
	}
	return nil
}

func (r *Repo) UpdateWorkItem(ctx context.Context, wsID, id string, p app.WorkItemPatch, rev int64) (app.WorkItem, error) {
	// last_activity_at bumps on every core-field PUT (status transition, reassign,
	// priority, title, ...) — same semantics the comment/metadata/property paths
	// already had, so "recently active" views do not go stale on the most common
	// work actions (FP-11 P1-2).
	sets := []string{"updated_at = now()", "last_activity_at = now()"}
	args := []any{wsID, id, rev}
	n := 3
	add := func(sql string, v any) { n++; sets = append(sets, fmt.Sprintf(sql, n)); args = append(args, v) }
	if p.Title != nil {
		add("title = $%d", *p.Title)
	}
	if p.Description != nil {
		add("description = $%d", *p.Description)
	}
	if p.StatusKey != nil {
		add("status_key = $%d", *p.StatusKey)
	}
	if p.Priority != nil {
		add("priority = $%d", *p.Priority)
	}
	if p.AssigneeType != nil {
		add("assignee_type = $%d", *p.AssigneeType)
	}
	if p.Outcome != nil {
		add("outcome = $%d", *p.Outcome)
	}
	if p.AssigneeID != nil {
		add("assignee_id = nullif($%d,'')::uuid", *p.AssigneeID)
	}
	if p.ProjectID != nil {
		add("project_id = nullif($%d,'')::uuid", *p.ProjectID)
	}
	if p.DueDate != nil {
		add("due_date = nullif($%d,'')::date", *p.DueDate)
	}
	if p.StartDate != nil {
		add("start_date = nullif($%d,'')::date", *p.StartDate)
	}
	if p.ParentID != nil {
		add("parent_work_item_id = nullif($%d,'')::uuid", *p.ParentID)
	}
	if p.TriageState != nil {
		add("triage_state = nullif($%d,'')", *p.TriageState)
	}
	if p.DuplicateOf != nil {
		add("duplicate_of_work_item_id = nullif($%d,'')::uuid", *p.DuplicateOf)
	}
	if p.Properties != nil {
		add("properties = $%d::jsonb", p.Properties)
	}

	n++
	w, err := scanWI(r.q(ctx).QueryRow(ctx, fmt.Sprintf(
		`UPDATE work_item SET %s, revision = revision + 1 WHERE workspace_id=$1 AND id=$2 AND ($3::bigint <= 0 OR revision = $3) RETURNING %s`,
		strings.Join(sets, ", "), wiCols), args...))
	if noRow(err) {
		return w, apierr.New(apierr.Conflict, "revision conflict")
	}
	return w, err
}

func (r *Repo) DeleteWorkItem(ctx context.Context, wsID, id string) error {
	return r.DB.Within(ctx, func(ctx context.Context) error {
		_, err := r.q(ctx).Exec(ctx, `DELETE FROM work_item WHERE workspace_id=$1 AND id=$2`, wsID, id)
		return err
	})
}

func (r *Repo) ListWorkItems(ctx context.Context, q app.ListQuery) ([]app.WorkItem, string, error) {
	conds := []string{"wi.workspace_id = $1"}
	args := []any{q.WorkspaceID}
	n := 1
	if len(q.StatusKeys) > 0 {
		n++
		conds = append(conds, fmt.Sprintf("wi.status_key = ANY($%d)", n))
		args = append(args, q.StatusKeys)
	}
	if q.AssigneeID != "" {
		n++
		conds = append(conds, fmt.Sprintf("wi.assignee_id::text = $%d", n))
		args = append(args, q.AssigneeID)
	}
	if q.CreatorID != "" {
		n++
		conds = append(conds, fmt.Sprintf("wi.creator_id::text = $%d", n))
		args = append(args, q.CreatorID)
	}
	if q.SubscriberID != "" {
		n++
		conds = append(conds, fmt.Sprintf("EXISTS(SELECT 1 FROM work_item_subscriber sub WHERE sub.work_item_id = wi.id AND sub.user_id::text = $%d)", n))
		args = append(args, q.SubscriberID)
	}
	if q.ProjectID != "" {
		n++
		conds = append(conds, fmt.Sprintf("wi.project_id::text = $%d", n))
		args = append(args, q.ProjectID)
	}
	if q.Search != "" {
		n++
		conds = append(conds, fmt.Sprintf("(wi.title ILIKE $%d OR wi.description ILIKE $%d)", n, n))
		args = append(args, "%"+q.Search+"%")
	}
	if q.ParentID != nil {
		if *q.ParentID == "" {
			conds = append(conds, "wi.parent_work_item_id IS NULL")
		} else {
			n++
			conds = append(conds, fmt.Sprintf("wi.parent_work_item_id::text = $%d", n))
			args = append(args, *q.ParentID)
		}
	}
	if q.Cursor != "" {
		parts := strings.SplitN(q.Cursor, "|", 2)
		if len(parts) == 2 {
			n += 2
			conds = append(conds, fmt.Sprintf("(wi.created_at, wi.id) < ($%d::timestamptz, $%d::uuid)", n-1, n))
			args = append(args, parts[0], parts[1])
		}
	}
	if q.LabelID != "" {
		n++
		conds = append(conds, fmt.Sprintf(`EXISTS(SELECT 1 FROM work_item_to_label wl WHERE wl.work_item_id = wi.id AND wl.label_id::text = $%d)`, n))
		args = append(args, q.LabelID)
	}

	if q.Involves != "" {
		n++
		conds = append(conds, app.InvolvesCondition(n))
		args = append(args, q.Involves)
	}
	for _, pf := range q.PropFilters {
		cond, used := app.PropCondition(pf, n+1)
		conds = append(conds, cond)
		args = append(args, pf.Key)
		if used > 1 {
			args = append(args, pf.Value)
		}
		n += used
	}

	sqlq := fmt.Sprintf(`SELECT %s FROM work_item wi WHERE %s ORDER BY wi.created_at DESC, wi.id DESC LIMIT %d`,
		wiCols, strings.Join(conds, " AND "), q.Limit+1)
	rows, err := r.q(ctx).Query(ctx, sqlq, args...)
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()
	var out []app.WorkItem
	for rows.Next() {
		var w app.WorkItem
		if err := rows.Scan(&w.ID, &w.WorkspaceID, &w.Number, &w.Title, &w.Description, &w.AcceptanceCriteria,
			&w.StatusKey, &w.Priority, &w.AssigneeType, &w.AssigneeID, &w.CreatorType, &w.CreatorID,
			&w.ParentID, &w.ProjectID, &w.Position, &w.DueDate, &w.StartDate, &w.Metadata, &w.Properties, &w.Revision,
			&w.TriageState, &w.DuplicateOf, &w.CreatedAt, &w.UpdatedAt, &w.LastActivityAt, &w.Outcome); err != nil {
			return nil, "", err
		}
		out = append(out, w)
	}
	next := ""
	if len(out) > q.Limit {
		last := out[q.Limit-1]
		// UTC "Z" form keeps the cursor URL-safe: a "+08:00" offset inside a
		// query param decodes "+" as a space and the replay dies with PG 22007
		// (same cursor-robustness family as FP-11 P1-1 / FP-18 D-01).
		next = last.CreatedAt.UTC().Format("2006-01-02T15:04:05.999999Z") + "|" + last.ID
		out = out[:q.Limit]
	}
	return out, next, rows.Err()
}

func (r *Repo) MovePosition(ctx context.Context, wsID, id string, pos int64) error {
	_, err := r.q(ctx).Exec(ctx,
		`UPDATE work_item SET position=$3, updated_at=now() WHERE workspace_id=$1 AND id=$2`, wsID, id, pos)
	return err
}

func (r *Repo) NextPositionInStatus(ctx context.Context, wsID, statusKey string) (int64, error) {
	var max *int64
	err := r.q(ctx).QueryRow(ctx,
		`SELECT max(position) FROM work_item WHERE workspace_id=$1 AND status_key=$2`, wsID, statusKey).Scan(&max)
	if err != nil {
		return 0, err
	}
	if max == nil {
		return 0, nil
	}
	return *max, nil
}

func (r *Repo) CountActiveDuplicate(ctx context.Context, wsID, normTitle string, projectID, parentID *string, activeKeys []string, excludeID string) (int, error) {
	var n int
	err := r.q(ctx).QueryRow(ctx, `
SELECT count(*) FROM work_item WHERE workspace_id=$1 AND lower(regexp_replace(trim(title), '\s+', ' ', 'g')) = $2
  AND status_key = ANY($3)
  AND ($4::uuid IS NULL OR project_id = $4::uuid)
  AND ($5::uuid IS NULL OR parent_work_item_id = $5::uuid)
  AND ($6::uuid IS NULL OR id <> $6::uuid)`,
		wsID, normTitle, activeKeys, projectID, parentID, nilIfEmpty(excludeID)).Scan(&n)
	return n, err
}

func (r *Repo) AdvisoryLockTitle(ctx context.Context, wsID, normTitle string) (func(), error) {
	conn, err := r.DB.Acquire(ctx)
	if err != nil {
		return nil, err
	}
	_, err = conn.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext($1 || ':' || $2))`, wsID, normTitle)
	if err != nil {
		conn.Release()
		return nil, err
	}
	return func() { conn.Release() }, nil
}

func (r *Repo) WorkItemAssignee(ctx context.Context, workItemID string) (string, string) {
	return r.GetAssignee(ctx, workItemID)
}

func (r *Repo) GetAssignee(ctx context.Context, workItemID string) (string, string) {
	var at, aid string
	_ = r.q(ctx).QueryRow(ctx,
		`SELECT assignee_type, coalesce(assignee_id::text,'') FROM work_item WHERE id=$1`, workItemID).
		Scan(&at, &aid)
	return at, aid
}

func (r *Repo) UpsertLabel(ctx context.Context, wsID, name, color string) (app.Label, error) {
	var l app.Label
	err := r.q(ctx).QueryRow(ctx, `
INSERT INTO label (workspace_id, name, color) VALUES ($1,$2,nullif($3,''))
ON CONFLICT (workspace_id, name) DO UPDATE SET color = coalesce(nullif($3,''), label.color)
RETURNING id, name, coalesce(color,'')`, wsID, name, color).Scan(&l.ID, &l.Name, &l.Color)
	return l, err
}

// UpdateLabel renames/recolors an existing label addressed by id (REST PUT
// /labels/{id} semantics, FP-18 D-02). ok=false when the id does not exist in
// the workspace; a name collision with another label maps to E_VALIDATION.
func (r *Repo) UpdateLabel(ctx context.Context, wsID, id, name, color string) (app.Label, bool, error) {
	var l app.Label
	err := r.q(ctx).QueryRow(ctx, `
UPDATE label SET name=$3, color=coalesce(nullif($4,''), label.color)
WHERE workspace_id=$1 AND id=$2
RETURNING id, name, coalesce(color,'')`, wsID, id, name, color).Scan(&l.ID, &l.Name, &l.Color)
	if noRow(err) {
		return l, false, nil
	}
	if err != nil {
		return l, false, mapUniqueViolation(err, "label name already exists")
	}
	return l, true, nil
}

func (r *Repo) DeleteLabel(ctx context.Context, wsID, id string) error {
	return r.DB.Within(ctx, func(ctx context.Context) error {

		if _, err := r.q(ctx).Exec(ctx, `DELETE FROM work_item_to_label WHERE label_id=$1`, id); err != nil {
			return err
		}
		if _, err := r.q(ctx).Exec(ctx, `DELETE FROM agent_to_label WHERE label_id=$1`, id); err != nil {
			return err
		}
		if _, err := r.q(ctx).Exec(ctx, `DELETE FROM skill_to_label WHERE label_id=$1`, id); err != nil {
			return err
		}
		_, err := r.q(ctx).Exec(ctx, `DELETE FROM label WHERE workspace_id=$1 AND id=$2`, wsID, id)
		return err
	})
}

func (r *Repo) ListLabels(ctx context.Context, wsID string) ([]app.Label, error) {
	rows, err := r.q(ctx).Query(ctx, `
SELECT l.id, l.name, coalesce(l.color,''), count(wl.work_item_id)
FROM label l LEFT JOIN work_item_to_label wl ON wl.label_id = l.id
WHERE l.workspace_id=$1 GROUP BY l.id ORDER BY l.name`, wsID)
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

func (r *Repo) SetWorkItemLabels(ctx context.Context, wiID string, labelIDs []string) error {
	return r.DB.Within(ctx, func(ctx context.Context) error {
		if _, err := r.q(ctx).Exec(ctx, `DELETE FROM work_item_to_label WHERE work_item_id=$1`, wiID); err != nil {
			return err
		}
		for _, id := range labelIDs {
			if _, err := r.q(ctx).Exec(ctx,
				`INSERT INTO work_item_to_label (work_item_id, label_id) VALUES ($1,$2) ON CONFLICT DO NOTHING`,
				wiID, id); err != nil {
				return err
			}
		}
		return nil
	})
}

func (r *Repo) AttachWorkItemLabel(ctx context.Context, wsID, wiID, labelID string) error {
	_, err := r.q(ctx).Exec(ctx, `
INSERT INTO work_item_to_label (work_item_id, label_id)
SELECT w.id, l.id FROM work_item w, label l
WHERE w.workspace_id=$1 AND w.id=$2 AND l.workspace_id=$1 AND l.id=$3
ON CONFLICT (work_item_id, label_id) DO NOTHING`, wsID, wiID, labelID)
	return err
}

func (r *Repo) DetachWorkItemLabel(ctx context.Context, wsID, wiID, labelID string) error {
	_, err := r.q(ctx).Exec(ctx, `
DELETE FROM work_item_to_label wl
USING work_item w
WHERE w.id = wl.work_item_id AND w.workspace_id=$1 AND wl.work_item_id=$2 AND wl.label_id=$3`,
		wsID, wiID, labelID)
	return err
}

func (r *Repo) GetWorkItemLabels(ctx context.Context, wiID string) ([]app.Label, error) {
	rows, err := r.q(ctx).Query(ctx, `
SELECT l.id, l.name, coalesce(l.color,''), 0 FROM label l
JOIN work_item_to_label wl ON wl.label_id = l.id WHERE wl.work_item_id=$1 ORDER BY l.name`, wiID)
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

func (r *Repo) ListProjects(ctx context.Context, wsID string) ([]app.Project, error) {
	rows, err := r.q(ctx).Query(ctx,
		`SELECT id, name, description, created_at FROM project WHERE workspace_id=$1 ORDER BY name`, wsID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []app.Project
	for rows.Next() {
		var p app.Project
		if err := rows.Scan(&p.ID, &p.Name, &p.Description, &p.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (r *Repo) CreateProject(ctx context.Context, wsID, name, desc string) (app.Project, error) {
	var p app.Project
	err := r.q(ctx).QueryRow(ctx,
		`INSERT INTO project (workspace_id, name, description) VALUES ($1,$2,$3) RETURNING id, name, description, created_at`,
		wsID, name, desc).Scan(&p.ID, &p.Name, &p.Description, &p.CreatedAt)
	return p, err
}

func (r *Repo) GetProject(ctx context.Context, wsID, id string) (app.Project, bool, error) {
	var p app.Project
	err := r.q(ctx).QueryRow(ctx,
		`SELECT id, name, description, created_at FROM project WHERE workspace_id=$1 AND id=$2`,
		wsID, id).Scan(&p.ID, &p.Name, &p.Description, &p.CreatedAt)
	if noRow(err) {
		return p, false, nil
	}
	return p, err == nil, err
}

func (r *Repo) UpdateProject(ctx context.Context, wsID, id, name, desc string) (app.Project, bool, error) {
	var p app.Project
	err := r.q(ctx).QueryRow(ctx, `
UPDATE project SET name=$3, description=$4, updated_at=now()
WHERE workspace_id=$1 AND id=$2
RETURNING id, name, description, created_at`, wsID, id, name, desc).
		Scan(&p.ID, &p.Name, &p.Description, &p.CreatedAt)
	if noRow(err) {
		return p, false, nil
	}
	if err != nil {
		return p, false, mapUniqueViolation(err, "project name already exists")
	}
	return p, true, nil
}

func (r *Repo) DeleteProject(ctx context.Context, wsID, id string) error {
	_, err := r.q(ctx).Exec(ctx, `DELETE FROM project WHERE workspace_id=$1 AND id=$2`, wsID, id)
	return err
}

func (r *Repo) CountWorkItemsInProject(ctx context.Context, wsID, projectID string) (int, error) {
	var n int
	err := r.q(ctx).QueryRow(ctx,
		`SELECT count(*) FROM work_item WHERE workspace_id=$1 AND project_id=$2::uuid`,
		wsID, projectID).Scan(&n)
	return n, err
}

func (r *Repo) SearchProjects(ctx context.Context, wsID, q string, limit int) ([]app.Project, error) {
	rows, err := r.q(ctx).Query(ctx, `
SELECT id, name, description, created_at FROM project
WHERE workspace_id=$1 AND (name ILIKE $2 OR description ILIKE $2)
ORDER BY name LIMIT $3`, wsID, "%"+q+"%", limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []app.Project
	for rows.Next() {
		var p app.Project
		if err := rows.Scan(&p.ID, &p.Name, &p.Description, &p.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func mapUniqueViolation(err error, msg string) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return apierr.NewValidation(msg)
	}
	return err
}

func (r *Repo) ListPropertyDefs(ctx context.Context, wsID string) ([]app.PropertyDef, error) {
	rows, err := r.q(ctx).Query(ctx,
		`SELECT key, name, type, options, description FROM work_item_property_def WHERE workspace_id=$1 AND archived_at IS NULL ORDER BY key`, wsID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []app.PropertyDef
	for rows.Next() {
		var d app.PropertyDef
		if err := rows.Scan(&d.Key, &d.Name, &d.Type, &d.Options, &d.Description); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

func (r *Repo) CreatePropertyDef(ctx context.Context, d app.PropertyDef, wsID string) (app.PropertyDef, error) {
	_, err := r.q(ctx).Exec(ctx,
		`INSERT INTO work_item_property_def (workspace_id, key, name, type, options, description) VALUES ($1,$2,$3,$4,$5::jsonb,$6)`,
		wsID, d.Key, d.Name, d.Type, mustJSON(d.Options), d.Description)
	return d, err
}

func (r *Repo) GetPropertyDef(ctx context.Context, wsID, key string) (app.PropertyDef, bool, error) {
	var d app.PropertyDef
	err := r.q(ctx).QueryRow(ctx,
		`SELECT key, name, type, options, description FROM work_item_property_def WHERE workspace_id=$1 AND key=$2 AND archived_at IS NULL`,
		wsID, key).Scan(&d.Key, &d.Name, &d.Type, &d.Options, &d.Description)
	if noRow(err) {
		return d, false, nil
	}
	return d, err == nil, err
}

func (r *Repo) UpdatePropertyDef(ctx context.Context, wsID, key string, p app.PropertyDefPatch) (app.PropertyDef, bool, error) {
	sets := []string{}
	args := []any{wsID, key}
	n := 2
	if p.Name != nil {
		n++
		sets = append(sets, fmt.Sprintf("name = $%d", n))
		args = append(args, *p.Name)
	}
	if p.Description != nil {
		n++
		sets = append(sets, fmt.Sprintf("description = $%d", n))
		args = append(args, *p.Description)
	}
	if p.Options != nil {
		n++
		sets = append(sets, fmt.Sprintf("options = $%d::jsonb", n))
		args = append(args, mustJSON(p.Options))
	}
	if len(sets) == 0 {
		return r.GetPropertyDef(ctx, wsID, key)
	}
	var d app.PropertyDef
	err := r.q(ctx).QueryRow(ctx, fmt.Sprintf(
		`UPDATE work_item_property_def SET %s WHERE workspace_id=$1 AND key=$2 AND archived_at IS NULL
RETURNING key, name, type, options, description`, strings.Join(sets, ", ")),
		args...).Scan(&d.Key, &d.Name, &d.Type, &d.Options, &d.Description)
	if noRow(err) {
		return d, false, nil
	}
	return d, err == nil, err
}

func (r *Repo) ArchivePropertyDef(ctx context.Context, wsID, key string) (bool, bool, error) {
	tag, err := r.q(ctx).Exec(ctx,
		`UPDATE work_item_property_def SET archived_at=now() WHERE workspace_id=$1 AND key=$2 AND archived_at IS NULL`,
		wsID, key)
	if err != nil {
		return false, false, err
	}
	if tag.RowsAffected() > 0 {
		return true, true, nil
	}

	var exists bool
	if err := r.q(ctx).QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM work_item_property_def WHERE workspace_id=$1 AND key=$2)`,
		wsID, key).Scan(&exists); err != nil {
		return false, false, err
	}
	return false, exists, nil
}

const cCols = `id, workspace_id, work_item_id, author_type, coalesce(author_id::text,''), content, type,
coalesce(parent_id::text,''), resolved_at IS NOT NULL, deleted_at IS NOT NULL, revision, created_at, updated_at`

func scanC(row pgx.Row) (app.Comment, error) {
	var c app.Comment
	err := row.Scan(&c.ID, &c.WorkspaceID, &c.WorkItemID, &c.AuthorType, &c.AuthorID, &c.Content, &c.Type,
		&c.ParentID, &c.Resolved, &c.Deleted, &c.Revision, &c.CreatedAt, &c.UpdatedAt)
	return c, err
}

func (r *Repo) CreateComment(ctx context.Context, in app.NewComment) (app.Comment, error) {
	c, err := scanC(r.q(ctx).QueryRow(ctx, fmt.Sprintf(`
INSERT INTO comment (work_item_id, workspace_id, author_type, author_id, content, parent_id)
VALUES ($1,$2,$3,nullif($4,'')::uuid,$5,nullif($6,'')::uuid) RETURNING %s`, cCols),
		in.WorkItemID, in.WorkspaceID, in.AuthorType, in.AuthorID, in.Content, in.ParentID))
	return c, err
}

func (r *Repo) GetComment(ctx context.Context, id string) (app.Comment, bool, error) {
	c, err := scanC(r.q(ctx).QueryRow(ctx,
		fmt.Sprintf(`SELECT %s FROM comment WHERE id=$1`, cCols), id))
	if noRow(err) {
		return c, false, nil
	}
	return c, err == nil, err
}

func (r *Repo) UpdateComment(ctx context.Context, id, content string, expectedRevision int64) (app.Comment, error) {
	c, err := scanC(r.q(ctx).QueryRow(ctx, fmt.Sprintf(`
UPDATE comment SET content=$2, revision=revision+1, updated_at=now()
WHERE id=$1 AND ($3::bigint <= 0 OR revision = $3) RETURNING %s`, cCols), id, content, expectedRevision))
	if noRow(err) {
		return c, apierr.New(apierr.Conflict, "revision conflict")
	}
	return c, err
}

func (r *Repo) TombstoneComment(ctx context.Context, id string) error {
	_, err := r.q(ctx).Exec(ctx, `UPDATE comment SET deleted_at=now(), updated_at=now() WHERE id=$1`, id)
	return err
}

func (r *Repo) SetResolved(ctx context.Context, id string, resolved bool, byType, byID string) error {
	if resolved {
		_, err := r.q(ctx).Exec(ctx,
			`UPDATE comment SET resolved_at=now(), resolved_by_type=$2, resolved_by_id=nullif($3,'')::uuid WHERE id=$1`, id, byType, byID)
		return err
	}
	_, err := r.q(ctx).Exec(ctx, `UPDATE comment SET resolved_at=NULL WHERE id=$1`, id)
	return err
}

func (r *Repo) InsertSourceContext(ctx context.Context, wsID, digest string, snapshot []byte, createdBy string) (string, bool, error) {
	var id string
	err := r.q(ctx).QueryRow(ctx, `
INSERT INTO work_item_source_context (workspace_id, source_digest, snapshot, created_by)
VALUES ($1,$2,$3,nullif($4,'')::uuid)
ON CONFLICT (workspace_id, source_digest) DO NOTHING
RETURNING id::text`, wsID, digest, snapshot, createdBy).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {

		err = r.q(ctx).QueryRow(ctx,
			`SELECT id::text FROM work_item_source_context WHERE workspace_id=$1 AND source_digest=$2`,
			wsID, digest).Scan(&id)
		return id, false, err
	}
	if err != nil {
		return "", false, err
	}
	return id, true, nil
}

func (r *Repo) GetSourceContext(ctx context.Context, wsID, digest string) (string, []byte, bool, error) {
	var id string
	var snap []byte
	err := r.q(ctx).QueryRow(ctx,
		`SELECT id::text, snapshot FROM work_item_source_context WHERE workspace_id=$1 AND source_digest=$2`,
		wsID, digest).Scan(&id, &snap)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil, false, nil
	}
	return id, snap, err == nil, err
}

func (r *Repo) ListComments(ctx context.Context, wiID string) ([]app.Comment, error) {
	rows, err := r.q(ctx).Query(ctx, fmt.Sprintf(
		`SELECT %s FROM comment WHERE work_item_id=$1 ORDER BY created_at, id`, cCols), wiID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanComments(rows)
}

func (r *Repo) ListCommentPage(ctx context.Context, wiID string, limit int, before string) ([]app.Comment, error) {
	cond := ""
	args := []any{wiID}
	if before != "" {
		args = append(args, before)
		cond = fmt.Sprintf(" AND c.created_at < $%d::timestamptz", len(args))
	}
	args = append(args, limit)
	rows, err := r.q(ctx).Query(ctx, fmt.Sprintf(`
WITH RECURSIVE roots AS (
  (SELECT c.* FROM comment c WHERE c.work_item_id=$1 AND c.parent_id IS NULL%s
   ORDER BY c.created_at DESC LIMIT $%d)
  UNION ALL
  SELECT c.* FROM comment c JOIN roots rt ON c.parent_id = rt.id
)
SELECT %s FROM roots`, cond, len(args), cCols), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanComments(rows)
}

func scanComments(rows pgx.Rows) ([]app.Comment, error) {
	var out []app.Comment
	for rows.Next() {
		var c app.Comment
		if err := rows.Scan(&c.ID, &c.WorkspaceID, &c.WorkItemID, &c.AuthorType, &c.AuthorID, &c.Content, &c.Type,
			&c.ParentID, &c.Resolved, &c.Deleted, &c.Revision, &c.CreatedAt, &c.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (r *Repo) TouchWorkItem(ctx context.Context, wiID string) error {
	_, err := r.q(ctx).Exec(ctx,
		`UPDATE work_item SET revision = revision + 1, last_activity_at = now(), updated_at = now() WHERE id=$1`, wiID)
	return err
}

func (r *Repo) UpsertSubscriber(ctx context.Context, wiID, userID, source string) error {
	_, err := r.q(ctx).Exec(ctx, `
INSERT INTO work_item_subscriber (work_item_id, user_id, source) VALUES ($1,$2,$3)
ON CONFLICT (work_item_id, user_id) DO NOTHING`, wiID, userID, source)
	return err
}

func (r *Repo) UpsertSubscribers(ctx context.Context, wiID string, subs []app.SubscriberWrite) error {
	for _, s := range subs {
		if _, err := r.q(ctx).Exec(ctx, `
INSERT INTO work_item_subscriber (work_item_id, user_id, source, level) VALUES ($1,$2,$3,$4)
ON CONFLICT (work_item_id, user_id) DO NOTHING`, wiID, s.UserID, s.Source, s.Level); err != nil {
			return err
		}
	}
	return nil
}

func (r *Repo) ListSubscribers(ctx context.Context, wiID string) ([]app.Subscriber, error) {
	rows, err := r.q(ctx).Query(ctx,
		`SELECT work_item_id, user_id::text, source, level, created_at
	FROM work_item_subscriber WHERE work_item_id=$1 ORDER BY created_at`, wiID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []app.Subscriber
	for rows.Next() {
		var s app.Subscriber
		if err := rows.Scan(&s.WorkItemID, &s.UserID, &s.Source, &s.Level, &s.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

func (r *Repo) UpsertManualSubscriber(ctx context.Context, wsID, wiID, userID string) (app.Subscriber, bool, error) {
	tag, err := r.q(ctx).Exec(ctx, `
INSERT INTO work_item_subscriber (work_item_id, user_id, source, level)
SELECT w.id, $3::uuid, 'manual', 'all' FROM work_item w
WHERE w.workspace_id=$1 AND w.id=$2
ON CONFLICT (work_item_id, user_id) DO NOTHING`, wsID, wiID, userID)
	if err != nil {
		return app.Subscriber{}, false, err
	}

	var s app.Subscriber
	err = r.q(ctx).QueryRow(ctx, `
SELECT s.work_item_id, s.user_id::text, s.source, s.level, s.created_at
FROM work_item_subscriber s JOIN work_item w ON w.id = s.work_item_id
WHERE w.workspace_id=$1 AND s.work_item_id=$2 AND s.user_id=$3::uuid`,
		wsID, wiID, userID).Scan(&s.WorkItemID, &s.UserID, &s.Source, &s.Level, &s.CreatedAt)
	if err != nil {
		return s, false, err
	}
	return s, tag.RowsAffected() > 0, nil
}

func (r *Repo) CountWorkItemsInTriage(ctx context.Context, wsID string, ids []string) (int, error) {
	var n int
	err := r.q(ctx).QueryRow(ctx,
		`SELECT count(*) FROM work_item WHERE workspace_id=$1 AND id = ANY($2) AND triage_state IS NOT NULL AND triage_state <> ''`,
		wsID, ids).Scan(&n)
	return n, err
}

func (r *Repo) RemoveSubscriber(ctx context.Context, wsID, wiID, userID string) (bool, error) {
	tag, err := r.q(ctx).Exec(ctx, `
DELETE FROM work_item_subscriber s
USING work_item w
WHERE w.id = s.work_item_id AND w.workspace_id=$1 AND s.work_item_id=$2 AND s.user_id=$3::uuid`,
		wsID, wiID, userID)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() > 0, nil
}

func (r *Repo) DeleteAllSubscriptionsByUser(ctx context.Context, wsID, userID string) (int64, error) {
	tag, err := r.q(ctx).Exec(ctx, `
DELETE FROM work_item_subscriber s
USING work_item w
WHERE w.id = s.work_item_id AND w.workspace_id=$1 AND s.user_id=$2::uuid`,
		wsID, userID)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

func (r *Repo) RemoveSubscriberSubtree(ctx context.Context, wsID, wiID, userID string) ([]string, error) {
	var removed []string
	err := r.DB.Within(ctx, func(ctx context.Context) error {
		if _, err := r.q(ctx).Exec(ctx,
			`SELECT pg_advisory_xact_lock(hashtext($1 || ':subscriber:' || $2))`, wsID, userID); err != nil {
			return err
		}
		rows, err := r.q(ctx).Query(ctx, `
WITH RECURSIVE sub AS (
  SELECT w.id FROM work_item w WHERE w.workspace_id=$1 AND w.id=$2::uuid
  UNION ALL
  SELECT c.id FROM work_item c JOIN sub ON c.parent_work_item_id = sub.id WHERE c.workspace_id=$1
)
DELETE FROM work_item_subscriber s
USING sub
WHERE s.work_item_id = sub.id AND s.user_id=$3::uuid
RETURNING s.work_item_id::text`, wsID, wiID, userID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				return err
			}
			removed = append(removed, id)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, err
	}
	return removed, nil
}

const viewCols = `id, workspace_id, user_id, name, filters, coalesce(group_by,''), sort, updated_at`

func scanView(row pgx.Row) (app.View, error) {
	var v app.View
	err := row.Scan(&v.ID, &v.WorkspaceID, &v.UserID, &v.Name, &v.Filters, &v.GroupBy, &v.Sort, &v.UpdatedAt)
	return v, err
}

func (r *Repo) ListWorkItemViews(ctx context.Context, wsID, userID string) ([]app.View, error) {
	rows, err := r.q(ctx).Query(ctx, fmt.Sprintf(
		`SELECT %s FROM work_item_view WHERE workspace_id=$1 AND user_id=$2 ORDER BY updated_at DESC`, viewCols),
		wsID, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []app.View
	for rows.Next() {
		v, err := scanView(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func (r *Repo) UpsertWorkItemView(ctx context.Context, wsID, userID string, v app.View) (app.View, bool, error) {
	if v.ID == "" {
		out, err := scanView(r.q(ctx).QueryRow(ctx, fmt.Sprintf(`
INSERT INTO work_item_view (workspace_id, user_id, name, filters, group_by, sort)
VALUES ($1,$2,$3,$4::jsonb,nullif($5,''),$6::jsonb)
RETURNING %s`, viewCols),
			wsID, userID, v.Name, mustJSON(v.Filters), v.GroupBy, mustJSON(v.Sort)))
		return out, err == nil, err
	}
	out, err := scanView(r.q(ctx).QueryRow(ctx, fmt.Sprintf(`
UPDATE work_item_view
SET name=$3, filters=$4::jsonb, group_by=nullif($5,''), sort=$6::jsonb, updated_at=now()
WHERE workspace_id=$1 AND user_id=$2 AND id=$7
RETURNING %s`, viewCols),
		wsID, userID, v.Name, mustJSON(v.Filters), v.GroupBy, mustJSON(v.Sort), v.ID))
	if noRow(err) {
		return out, false, nil
	}
	return out, err == nil, err
}

func (r *Repo) GetViewPreference(ctx context.Context, wsID, userID string) (string, bool, error) {
	var viewID *string
	err := r.q(ctx).QueryRow(ctx,
		`SELECT default_view_id::text FROM work_item_view_preference WHERE workspace_id=$1 AND user_id=$2`,
		wsID, userID).Scan(&viewID)
	if noRow(err) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	if viewID == nil {
		return "", true, nil
	}
	return *viewID, true, nil
}

func (r *Repo) SetViewPreference(ctx context.Context, wsID, userID string, viewID *string) error {
	_, err := r.q(ctx).Exec(ctx, `
INSERT INTO work_item_view_preference (workspace_id, user_id, default_view_id)
VALUES ($1,$2,$3::uuid)
ON CONFLICT (workspace_id, user_id) DO UPDATE SET default_view_id=$3::uuid`,
		wsID, userID, nilIfPtr(viewID))
	return err
}

func nilIfPtr(s *string) any {
	if s == nil || *s == "" {
		return nil
	}
	return *s
}

func (r *Repo) AppendActivity(ctx context.Context, eventID, wsID, wiID, actorType, actorID, kind string, payload map[string]any) error {
	_, err := r.q(ctx).Exec(ctx, `
INSERT INTO activity_log (event_id, workspace_id, work_item_id, actor_type, actor_id, kind, payload)
VALUES ($1,$2,nullif($3,'')::uuid,$4,nullif($5,'')::uuid,$6,$7::jsonb)
ON CONFLICT (event_id) DO NOTHING`, eventID, wsID, wiID, actorType, actorID, kind, mustJSON(payload))
	return err
}

func (r *Repo) ListTimeline(ctx context.Context, wiID string, limit int, before string) ([]app.TimelineEntry, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	args := []any{wiID, limit}
	cond := ""
	if before != "" {
		cond = " AND created_at < $3::timestamptz"
		args = append(args, before)
	}
	rows, err := r.q(ctx).Query(ctx, `
SELECT 'comment' AS kind, id::text, author_type, coalesce(author_id::text,''), content, '' AS event, NULL::jsonb AS payload, created_at FROM comment
 WHERE work_item_id=$1 AND deleted_at IS NULL`+cond+`
UNION ALL
SELECT 'activity', id::text, actor_type, coalesce(actor_id::text,''), '', kind, payload, created_at FROM activity_log
 WHERE work_item_id=$1`+cond+`
ORDER BY created_at DESC LIMIT $2`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []app.TimelineEntry
	for rows.Next() {
		var e app.TimelineEntry
		var event string
		var raw []byte
		if err := rows.Scan(&e.Kind, &e.ID, &e.ActorType, &e.ActorID, &e.Content, &event, &raw, &e.CreatedAt); err != nil {
			return nil, err
		}
		if e.Kind == "activity" {
			payload := map[string]any{}
			if len(raw) > 0 {
				_ = json.Unmarshal(raw, &payload)
			}
			if payload == nil {
				payload = map[string]any{}
			}
			e.Content = workdomain.RenderActivityLine(event, payload)
			payload["event"] = event
			e.Payload = payload
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func (r *Repo) QueryRows(ctx context.Context, sql string, args []any) ([]map[string]any, error) {
	rows, err := r.q(ctx).Query(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanMaps(rows)
}

func scanMaps(rows pgx.Rows) ([]map[string]any, error) {
	fds := rows.FieldDescriptions()
	var out []map[string]any
	for rows.Next() {
		vals, err := rows.Values()
		if err != nil {
			return nil, err
		}
		m := map[string]any{}
		for i, fd := range fds {
			m[string(fd.Name)] = vals[i]
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

const searchStatementTimeout = "8s"

func (r *Repo) SearchRows(ctx context.Context, sql string, args []any) ([]map[string]any, error) {
	var out []map[string]any
	err := r.DB.Within(ctx, func(ctx context.Context) error {
		if _, err := r.q(ctx).Exec(ctx,
			fmt.Sprintf("SET LOCAL statement_timeout = '%s'", searchStatementTimeout)); err != nil {
			return err
		}
		rows, err := r.q(ctx).Query(ctx, sql, args...)
		if err != nil {
			return err
		}
		defer rows.Close()
		out, err = scanMaps(rows)
		return err
	})
	if err != nil {
		return nil, MapSearchTimeout(err)
	}
	return out, nil
}

func MapSearchTimeout(err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "57014" {
		return apierr.New(apierr.SearchTimeout, "search timed out")
	}
	return err
}

func (r *Repo) ListWorkItemUsage(ctx context.Context, wsID, wiID string) ([]app.WorkItemUsage, error) {
	rows, err := r.q(ctx).Query(ctx, `
SELECT u.provider, u.model, count(DISTINCT u.task_id) AS task_count,
  sum(u.prompt_tokens) AS prompt_tokens, sum(u.completion_tokens) AS completion_tokens,
  sum(u.cost_usd_ticks) AS cost_usd_ticks
FROM task_usage u
JOIN agent_task_queue q ON q.id = u.task_id
WHERE q.workspace_id = $1 AND q.work_item_id = $2
GROUP BY u.provider, u.model
ORDER BY u.provider, u.model`, wsID, wiID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []app.WorkItemUsage
	for rows.Next() {
		var u app.WorkItemUsage
		if err := rows.Scan(&u.Provider, &u.Model, &u.TaskCount,
			&u.PromptTokens, &u.CompletionTokens, &u.CostUSDTicks); err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

func (r *Repo) ListDuplicates(ctx context.Context, wsID, wiID string) ([]app.WorkItem, error) {
	rows, err := r.q(ctx).Query(ctx, fmt.Sprintf(
		`SELECT %s FROM work_item wi WHERE wi.workspace_id=$1 AND wi.duplicate_of_work_item_id=$2 ORDER BY wi.created_at DESC`,
		wiCols), wsID, wiID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []app.WorkItem
	for rows.Next() {
		var w app.WorkItem
		if err := rows.Scan(&w.ID, &w.WorkspaceID, &w.Number, &w.Title, &w.Description, &w.AcceptanceCriteria,
			&w.StatusKey, &w.Priority, &w.AssigneeType, &w.AssigneeID, &w.CreatorType, &w.CreatorID,
			&w.ParentID, &w.ProjectID, &w.Position, &w.DueDate, &w.StartDate, &w.Metadata, &w.Properties, &w.Revision,
			&w.TriageState, &w.DuplicateOf, &w.CreatedAt, &w.UpdatedAt, &w.LastActivityAt, &w.Outcome); err != nil {
			return nil, err
		}
		out = append(out, w)
	}
	return out, rows.Err()
}

func (r *Repo) GetMetadataKey(ctx context.Context, wsID, wiID, key string) (json.RawMessage, bool, bool, error) {
	var raw []byte
	err := r.q(ctx).QueryRow(ctx,
		`SELECT metadata->$3 FROM work_item WHERE workspace_id=$1 AND id=$2`, wsID, wiID, key).Scan(&raw)
	if noRow(err) {
		return nil, false, false, nil
	}
	if err != nil {
		return nil, false, false, err
	}
	if raw == nil {
		return nil, true, false, nil
	}
	return json.RawMessage(raw), true, true, nil
}

func (r *Repo) UpdateMetadataKey(ctx context.Context, wsID, wiID, key string, value json.RawMessage) (map[string]any, bool, error) {
	var meta map[string]any
	err := r.q(ctx).QueryRow(ctx, `
UPDATE work_item
SET metadata = coalesce(metadata, '{}'::jsonb) || jsonb_build_object($3::text, $4::jsonb),
    revision = revision + 1, updated_at = now(), last_activity_at = now()
WHERE workspace_id=$1 AND id=$2
RETURNING metadata`, wsID, wiID, key, []byte(value)).Scan(&meta)
	if noRow(err) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return meta, true, nil
}

func (r *Repo) DeleteMetadataKey(ctx context.Context, wsID, wiID, key string) (bool, error) {
	tag, err := r.q(ctx).Exec(ctx, `
UPDATE work_item
SET metadata = metadata - $3, revision = revision + 1, updated_at = now(), last_activity_at = now()
WHERE workspace_id=$1 AND id=$2`, wsID, wiID, key)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() > 0, nil
}

func (r *Repo) GetPropertyKey(ctx context.Context, wsID, wiID, key string) (json.RawMessage, bool, bool, error) {
	var raw []byte
	err := r.q(ctx).QueryRow(ctx,
		`SELECT properties->$3 FROM work_item WHERE workspace_id=$1 AND id=$2`, wsID, wiID, key).Scan(&raw)
	if noRow(err) {
		return nil, false, false, nil
	}
	if err != nil {
		return nil, false, false, err
	}
	if raw == nil {
		return nil, true, false, nil
	}
	return json.RawMessage(raw), true, true, nil
}

func (r *Repo) SetPropertyKey(ctx context.Context, wsID, wiID, key string, value json.RawMessage) (map[string]any, bool, error) {
	var props map[string]any
	err := r.q(ctx).QueryRow(ctx, `
UPDATE work_item
SET properties = coalesce(properties, '{}'::jsonb) || jsonb_build_object($3::text, $4::jsonb),
    revision = revision + 1, updated_at = now(), last_activity_at = now()
WHERE workspace_id=$1 AND id=$2
RETURNING properties`, wsID, wiID, key, []byte(value)).Scan(&props)
	if noRow(err) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return props, true, nil
}

func (r *Repo) DeletePropertyKey(ctx context.Context, wsID, wiID, key string) (bool, error) {
	tag, err := r.q(ctx).Exec(ctx, `
UPDATE work_item
SET properties = properties - $3, revision = revision + 1, updated_at = now(), last_activity_at = now()
WHERE workspace_id=$1 AND id=$2`, wsID, wiID, key)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() > 0, nil
}

func (r *Repo) UpsertReaction(ctx context.Context, wsID, wiID, userID, emoji string) error {
	_, err := r.q(ctx).Exec(ctx, `
INSERT INTO work_item_reaction (work_item_id, user_id, emoji)
SELECT w.id, $3::uuid, $4 FROM work_item w
WHERE w.workspace_id=$1 AND w.id=$2
ON CONFLICT (work_item_id, user_id, emoji) DO NOTHING`, wsID, wiID, userID, emoji)
	return err
}

func (r *Repo) DeleteReaction(ctx context.Context, wsID, wiID, userID, emoji string) (bool, error) {
	tag, err := r.q(ctx).Exec(ctx, `
DELETE FROM work_item_reaction r
USING work_item w
WHERE w.id = r.work_item_id AND w.workspace_id=$1 AND r.work_item_id=$2
  AND r.user_id=$3::uuid AND r.emoji=$4`, wsID, wiID, userID, emoji)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() > 0, nil
}

func (r *Repo) ListReactionCounts(ctx context.Context, wsID, wiID string) ([]app.ReactionCount, error) {
	rows, err := r.q(ctx).Query(ctx, `
SELECT r.emoji, count(*) FROM work_item_reaction r
JOIN work_item w ON w.id = r.work_item_id
WHERE w.workspace_id=$1 AND r.work_item_id=$2
GROUP BY r.emoji ORDER BY 2 DESC, 1`, wsID, wiID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []app.ReactionCount
	for rows.Next() {
		var c app.ReactionCount
		if err := rows.Scan(&c.Emoji, &c.Count); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (r *Repo) UpsertCommentReaction(ctx context.Context, wsID, commentID, userID, emoji string) error {
	_, err := r.q(ctx).Exec(ctx, `
INSERT INTO comment_reaction (comment_id, user_id, emoji)
SELECT c.id, $3::uuid, $4 FROM comment c
WHERE c.workspace_id=$1 AND c.id=$2 AND c.deleted_at IS NULL
ON CONFLICT (comment_id, user_id, emoji) DO NOTHING`, wsID, commentID, userID, emoji)
	return err
}

func (r *Repo) DeleteCommentReaction(ctx context.Context, wsID, commentID, userID, emoji string) (bool, error) {
	tag, err := r.q(ctx).Exec(ctx, `
DELETE FROM comment_reaction r
USING comment c
WHERE c.id = r.comment_id AND c.workspace_id=$1 AND r.comment_id=$2
  AND r.user_id=$3::uuid AND r.emoji=$4`, wsID, commentID, userID, emoji)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() > 0, nil
}

func (r *Repo) ListCommentReactionCounts(ctx context.Context, wsID, commentID string) ([]app.ReactionCount, error) {
	rows, err := r.q(ctx).Query(ctx, `
SELECT r.emoji, count(*) FROM comment_reaction r
JOIN comment c ON c.id = r.comment_id
WHERE c.workspace_id=$1 AND r.comment_id=$2
GROUP BY r.emoji ORDER BY 2 DESC, 1`, wsID, commentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []app.ReactionCount
	for rows.Next() {
		var c app.ReactionCount
		if err := rows.Scan(&c.Emoji, &c.Count); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (r *Repo) ChildProgress(ctx context.Context, wsID, parentID string) ([]map[string]any, error) {
	rows, err := r.q(ctx).Query(ctx, `
SELECT c.status_key AS k, count(*) AS n
FROM work_item c
WHERE c.workspace_id=$1 AND c.parent_work_item_id=$2::uuid
GROUP BY c.status_key ORDER BY 2 DESC, 1`, wsID, parentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []map[string]any
	for rows.Next() {
		var k string
		var n int64
		if err := rows.Scan(&k, &n); err != nil {
			return nil, err
		}
		out = append(out, map[string]any{"k": k, "n": n})
	}
	return out, rows.Err()
}

func nilIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func mustJSON(v any) []byte {
	b, _ := json.Marshal(v)
	return b
}
