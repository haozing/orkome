package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"orkome/internal/contract/apierr"
	"orkome/internal/contract/events"
	"orkome/internal/work/domain"
)

type CatalogService struct {
	repo Repo
	emit Emitter

	outbox OutboxRecorder
	uow    Within
}

func NewCatalogService(repo Repo, emit Emitter) *CatalogService {
	return &CatalogService{repo: repo, emit: emit}
}

func (s *CatalogService) SetOutbox(rec OutboxRecorder, uow Within) {
	s.outbox, s.uow = rec, uow
}

func (s *CatalogService) emitCommitted(ctx context.Context, env *events.Envelope, write func(context.Context) error) error {
	return emitCommitted(ctx, s.emit, s.outbox, s.uow, env, write)
}

func (s *CatalogService) ListStatuses(ctx context.Context, workspaceID string) ([]StatusDef, error) {
	if err := s.repo.EnsureStatusSeed(ctx, workspaceID); err != nil {
		return nil, err
	}
	defs, err := s.repo.ListStatuses(ctx, workspaceID)
	if err != nil {
		return nil, err
	}
	sort.SliceStable(defs, func(i, j int) bool { return defs[i].Position < defs[j].Position })
	return defs, nil
}

func (s *CatalogService) CreateStatus(ctx context.Context, workspaceID, name, category string) (*StatusDef, error) {
	if name == "" || len(name) > 50 {
		return nil, apierr.NewValidation("invalid status name")
	}
	switch category {
	case domain.CatUnstarted, domain.CatStarted, domain.CatDone, domain.CatClosed:
	default:
		return nil, apierr.NewValidation("category must be unstarted|started|done|closed")
	}
	existing, err := s.repo.ListStatuses(ctx, workspaceID)
	if err != nil {
		return nil, err
	}
	taken := map[string]bool{}
	for _, d := range existing {
		taken[d.Key] = true
	}
	key := domain.DeriveStatusKey(name, category, taken, 1)
	def, err := s.repo.CreateStatus(ctx, workspaceID, key, name, category, len(existing))
	if err != nil {
		return nil, err
	}
	return &def, nil
}

func (s *CatalogService) ReorderStatuses(ctx context.Context, workspaceID string, keys []string) error {
	return s.repo.ReorderStatuses(ctx, workspaceID, keys)
}

func (s *CatalogService) UpdateStatus(ctx context.Context, workspaceID, key string, p StatusDefPatch) (*StatusDef, error) {
	if p.Name != nil {
		n := strings.TrimSpace(*p.Name)
		if n == "" || len(n) > 50 {
			return nil, apierr.NewValidation("invalid status name",
				apierr.FieldError{Field: "name", Problem: "1-50 chars"})
		}
		p.Name = &n
	}
	if p.Icon != nil && (*p.Icon == "" || len([]rune(*p.Icon)) > 32) {
		return nil, apierr.NewValidation("invalid icon",
			apierr.FieldError{Field: "icon", Problem: "1-32 runes"})
	}
	if p.Category != nil && !domain.IsCategory(*p.Category) {
		return nil, apierr.NewValidation("category must be unstarted|started|done|closed")
	}
	if err := s.repo.EnsureStatusSeed(ctx, workspaceID); err != nil {
		return nil, err
	}
	def, ok, err := s.repo.GetStatus(ctx, workspaceID, key)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, apierr.New(apierr.NotFound, "status not found")
	}
	if def.IsBuiltin && p.Category != nil && *p.Category != def.Category {
		return nil, apierr.NewValidation("builtin status category cannot be changed",
			apierr.FieldError{Field: "category", Problem: "locked for builtin statuses"})
	}
	out, ok, err := s.repo.UpdateStatus(ctx, workspaceID, key, p)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, apierr.New(apierr.NotFound, "status not found")
	}
	return &out, nil
}

func (s *CatalogService) UpsertLabel(ctx context.Context, workspaceID, name, color string) (*Label, error) {
	name = strings.TrimSpace(name)
	if name == "" || len(name) > 50 {
		return nil, apierr.NewValidation("invalid label name")
	}
	l, err := s.repo.UpsertLabel(ctx, workspaceID, name, color)
	if err != nil {
		return nil, err
	}
	return &l, nil
}

func (s *CatalogService) ListLabels(ctx context.Context, workspaceID string) ([]Label, error) {
	return s.repo.ListLabels(ctx, workspaceID)
}

func (s *CatalogService) GetLabel(ctx context.Context, workspaceID, id string) (*Label, error) {
	l, ok, err := s.repo.GetLabel(ctx, workspaceID, id)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, apierr.New(apierr.NotFound, "label not found")
	}
	return &l, nil
}

func (s *CatalogService) DeleteLabel(ctx context.Context, workspaceID, id string) error {
	return s.repo.DeleteLabel(ctx, workspaceID, id)
}

func (s *CatalogService) CreateProject(ctx context.Context, workspaceID, name, desc string) (*Project, error) {
	name = strings.TrimSpace(name)
	if name == "" || len(name) > 100 {
		return nil, apierr.NewValidation("invalid project name")
	}
	var p Project
	env := ev(events.ProjectCreated, workspaceID, "", nil)
	if err := s.emitCommitted(ctx, &env, func(ctx context.Context) error {
		row, err := s.repo.CreateProject(ctx, workspaceID, name, desc)
		if err != nil {
			return err
		}
		p = row
		env.Payload = map[string]any{"project_id": p.ID, "name": p.Name}
		return nil
	}); err != nil {
		return nil, err
	}
	return &p, nil
}

func (s *CatalogService) ListProjects(ctx context.Context, workspaceID string) ([]Project, error) {
	return s.repo.ListProjects(ctx, workspaceID)
}

func (s *CatalogService) GetProject(ctx context.Context, workspaceID, id string) (*Project, error) {
	p, ok, err := s.repo.GetProject(ctx, workspaceID, id)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, apierr.New(apierr.NotFound, "project not found")
	}
	return &p, nil
}

func (s *CatalogService) UpdateProject(ctx context.Context, workspaceID, id, name, desc string) (*Project, error) {
	name = strings.TrimSpace(name)
	if name == "" || len(name) > 100 {
		return nil, apierr.NewValidation("invalid project name",
			apierr.FieldError{Field: "name", Problem: "1-100 chars"})
	}
	p, ok, err := s.repo.UpdateProject(ctx, workspaceID, id, name, desc)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, apierr.New(apierr.NotFound, "project not found")
	}
	return &p, nil
}

func (s *CatalogService) DeleteProject(ctx context.Context, workspaceID, id string) error {
	if _, err := s.GetProject(ctx, workspaceID, id); err != nil {
		return err
	}
	n, err := s.repo.CountWorkItemsInProject(ctx, workspaceID, id)
	if err != nil {
		return err
	}
	if n > 0 {
		return &apierr.Error{Code: apierr.Conflict, Message: "project still has work items",
			Details: []apierr.FieldError{{Field: "project_id", Problem: "referenced by work items"}}}
	}
	return s.repo.DeleteProject(ctx, workspaceID, id)
}

func (s *CatalogService) SearchProjects(ctx context.Context, workspaceID, q string) ([]Project, error) {
	q = strings.TrimSpace(q)
	if q == "" || len(q) > 200 {
		return nil, apierr.NewValidation("empty or too long query")
	}
	return s.repo.SearchProjects(ctx, workspaceID, q, 20)
}

func validateResourceRef(kind, uri string) error {
	if err := domain.ValidateResourceKind(kind); err != nil {
		return apierr.NewValidation("invalid project resource",
			apierr.FieldError{Field: "kind", Problem: err.Error()})
	}
	if err := domain.ValidateResourceURI(kind, uri); err != nil {
		return apierr.NewValidation("invalid project resource",
			apierr.FieldError{Field: "uri", Problem: err.Error()})
	}
	return nil
}

func (s *CatalogService) ListProjectResources(ctx context.Context, workspaceID, projectID string) ([]ProjectResource, error) {
	if _, err := s.GetProject(ctx, workspaceID, projectID); err != nil {
		return nil, err
	}
	return s.repo.ListProjectResources(ctx, workspaceID, projectID)
}

func (s *CatalogService) CreateProjectResource(ctx context.Context, workspaceID, projectID, kind, uri string) (*ProjectResource, error) {
	if _, err := s.GetProject(ctx, workspaceID, projectID); err != nil {
		return nil, err
	}
	if err := validateResourceRef(kind, uri); err != nil {
		return nil, err
	}
	var res ProjectResource
	env := ev("project_resource.created", workspaceID, "", nil)
	if err := s.emitCommitted(ctx, &env, func(ctx context.Context) error {
		r, ok, err := s.repo.CreateProjectResource(ctx, workspaceID, projectID, kind, uri)
		if err != nil {
			return err
		}
		if !ok {
			return apierr.New(apierr.NotFound, "project not found")
		}
		res = r
		env.Payload = map[string]any{
			"project_id": projectID, "resource_id": res.ID, "kind": kind,
		}
		return nil
	}); err != nil {
		return nil, err
	}
	return &res, nil
}

func (s *CatalogService) UpdateProjectResource(ctx context.Context, workspaceID, projectID, resourceID, kind, uri string) (*ProjectResource, error) {
	if _, err := s.GetProject(ctx, workspaceID, projectID); err != nil {
		return nil, err
	}
	if err := validateResourceRef(kind, uri); err != nil {
		return nil, err
	}
	var res ProjectResource
	env := ev("project_resource.updated", workspaceID, "", map[string]any{
		"project_id": projectID, "resource_id": resourceID, "kind": kind,
	})
	if err := s.emitCommitted(ctx, &env, func(ctx context.Context) error {
		r, ok, err := s.repo.UpdateProjectResource(ctx, workspaceID, projectID, resourceID, kind, uri)
		if err != nil {
			return err
		}
		if !ok {
			return apierr.New(apierr.NotFound, "project resource not found")
		}
		res = r
		return nil
	}); err != nil {
		return nil, err
	}
	return &res, nil
}

func (s *CatalogService) DeleteProjectResource(ctx context.Context, workspaceID, projectID, resourceID string) error {
	if _, err := s.GetProject(ctx, workspaceID, projectID); err != nil {
		return err
	}
	env := ev("project_resource.deleted", workspaceID, "", map[string]any{
		"project_id": projectID, "resource_id": resourceID,
	})
	return s.emitCommitted(ctx, &env, func(ctx context.Context) error {
		return s.repo.DeleteProjectResource(ctx, workspaceID, projectID, resourceID)
	})
}

var validPropTypes = map[string]bool{
	domain.PropText: true, domain.PropURL: true, domain.PropNumber: true, domain.PropCheckbox: true,
	domain.PropDate: true, domain.PropSelect: true, domain.PropMultiSelect: true,
	domain.PropActor: true, domain.PropMultiActor: true,
}

func (s *CatalogService) CreatePropertyDef(ctx context.Context, def PropertyDef, workspaceID string) (*PropertyDef, error) {
	if !validPropTypes[def.Type] {
		return nil, apierr.NewValidation("unknown property type")
	}
	if def.Key == "" {
		def.Key = domainSlug(def.Name)
	}
	if def.Key == "" || len(def.Key) > 60 {
		return nil, apierr.NewValidation("invalid property key")
	}
	if (def.Type == domain.PropSelect || def.Type == domain.PropMultiSelect) && len(def.Options) == 0 {
		return nil, apierr.NewValidation("select property requires options")
	}
	out, err := s.repo.CreatePropertyDef(ctx, def, workspaceID)
	if err != nil {
		return nil, err
	}
	return &out, nil
}

func (s *CatalogService) ListPropertyDefs(ctx context.Context, workspaceID string) ([]PropertyDef, error) {
	return s.repo.ListPropertyDefs(ctx, workspaceID)
}

func (s *CatalogService) GetPropertyDef(ctx context.Context, workspaceID, key string) (*PropertyDef, error) {
	d, ok, err := s.repo.GetPropertyDef(ctx, workspaceID, key)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, apierr.New(apierr.NotFound, "property definition not found")
	}
	return &d, nil
}

func (s *CatalogService) UpdatePropertyDef(ctx context.Context, workspaceID, key string, p PropertyDefPatch) (*PropertyDef, error) {
	if p.Name != nil {
		n := strings.TrimSpace(*p.Name)
		if n == "" || len(n) > 100 {
			return nil, apierr.NewValidation("invalid property name",
				apierr.FieldError{Field: "name", Problem: "1-100 chars"})
		}
		p.Name = &n
	}
	if p.Options != nil {
		cur, ok, err := s.repo.GetPropertyDef(ctx, workspaceID, key)
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, apierr.New(apierr.NotFound, "property definition not found")
		}
		if (cur.Type == domain.PropSelect || cur.Type == domain.PropMultiSelect) && len(p.Options) == 0 {
			return nil, apierr.NewValidation("select property requires options")
		}
	}
	out, ok, err := s.repo.UpdatePropertyDef(ctx, workspaceID, key, p)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, apierr.New(apierr.NotFound, "property definition not found")
	}
	return &out, nil
}

func (s *CatalogService) DeletePropertyDef(ctx context.Context, workspaceID, key string) error {
	_, exists, err := s.repo.ArchivePropertyDef(ctx, workspaceID, key)
	if err != nil {
		return err
	}
	if !exists {
		return apierr.New(apierr.NotFound, "property definition not found")
	}
	return nil
}

func domainSlug(s string) string {
	return strings.ToLower(strings.ReplaceAll(strings.TrimSpace(s), " ", "_"))
}

type ViewService struct {
	repo Repo
}

func NewViewService(repo Repo) *ViewService { return &ViewService{repo: repo} }

type QueryFilters struct {
	Status      []string `json:"status"`
	Assignee    string   `json:"assignee"`
	Labels      []string `json:"labels"`
	Project     string   `json:"project"`
	PriorityMin *int     `json:"priority_min"`
	Search      string   `json:"search"`

	Involves string `json:"involves"`

	Props map[string]string `json:"props,omitempty"`
}

func (q QueryFilters) Fingerprint() string {
	b, _ := json.Marshal(struct {
		QueryFilters
		V int `json:"v"`
	}{q, 2})
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:8])
}

func (s *ViewService) Rows(ctx context.Context, workspaceID string, f QueryFilters, cursor string, limit int) (map[string]any, error) {
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}
	if err := validateInvolves(f.Involves); err != nil {
		return nil, err
	}
	props, err := s.resolvePropFilters(ctx, workspaceID, f)
	if err != nil {
		return nil, err
	}
	f = s.expandStatusFilter(ctx, workspaceID, f)
	where, args := compileFilters(workspaceID, f, props)
	if cursor != "" {
		ct, cid, ok := parseCursor(cursor)
		if !ok {
			return nil, apierr.NewValidation("bad cursor")
		}

		n := len(args)
		where += fmt.Sprintf(" AND (wi.created_at, wi.id) < ($%d::timestamptz, $%d)", n+1, n+2)
		args = append(args, ct, cid)
	}
	sqlq := fmt.Sprintf(`SELECT wi.id, wi.number, wi.title, wi.status_key, wi.priority, wi.assignee_type,
		coalesce(wi.assignee_id::text,''), wi.revision, wi.created_at
		FROM work_item wi %s ORDER BY wi.created_at DESC, wi.id DESC LIMIT %d`, where, limit)
	rows, err := s.repo.QueryRows(ctx, sqlq, args)
	if err != nil {
		return nil, err
	}
	var next string
	if len(rows) == limit {
		last := rows[len(rows)-1]
		next = fmt.Sprintf("%v|%v", last["created_at"], last["id"])
	}
	return map[string]any{"items": rows, "next_cursor": next, "fingerprint": f.Fingerprint()}, nil
}

func (s *ViewService) Groups(ctx context.Context, workspaceID, groupBy string, f QueryFilters) ([]map[string]any, error) {
	var sel, join string
	switch groupBy {
	case "status":
		sel = "wi.status_key AS k"
	case "assignee":
		sel = "coalesce(wi.assignee_type || ':' || wi.assignee_id::text, 'none') AS k"
	case "project":
		sel = "coalesce(wi.project_id::text, 'none') AS k"
		join = ""
	case "label":
		sel = "coalesce(l.name, 'no label') AS k"
		join = "LEFT JOIN work_item_to_label wl ON wl.work_item_id = wi.id LEFT JOIN label l ON l.id = wl.label_id"
	default:
		return nil, apierr.NewValidation("group_by must be status|assignee|label|project")
	}
	if err := validateInvolves(f.Involves); err != nil {
		return nil, err
	}
	props, err := s.resolvePropFilters(ctx, workspaceID, f)
	if err != nil {
		return nil, err
	}
	f = s.expandStatusFilter(ctx, workspaceID, f)
	where, args := compileFilters(workspaceID, f, props)
	sqlq := fmt.Sprintf(`SELECT %s, count(*) AS n FROM work_item wi %s %s GROUP BY 1 ORDER BY 2 DESC`, sel, join, where)
	rows, err := s.repo.QueryRows(ctx, sqlq, args)
	if err != nil {
		return nil, err
	}
	return rows, nil
}

func (s *ViewService) Facets(ctx context.Context, workspaceID string, f QueryFilters) (map[string]any, error) {
	out := map[string]any{}
	if err := validateInvolves(f.Involves); err != nil {
		return nil, err
	}
	props, err := s.resolvePropFilters(ctx, workspaceID, f)
	if err != nil {
		return nil, err
	}
	f = s.expandStatusFilter(ctx, workspaceID, f)
	where, args := compileFilters(workspaceID, f, props)
	for _, dim := range []struct{ key, sql string }{
		{"status", "SELECT wi.status_key AS k, count(*) n FROM work_item wi %s GROUP BY 1"},
		{"assignee", "SELECT coalesce(wi.assignee_type || ':' || wi.assignee_id::text, 'none') AS k, count(*) n FROM work_item wi %s GROUP BY 1"},
	} {
		rows, err := s.repo.QueryRows(ctx, fmt.Sprintf(dim.sql, where), args)
		if err != nil {
			return nil, err
		}
		out[dim.key] = rows
	}
	return out, nil
}

func compileFilters(workspaceID string, f QueryFilters, props []domain.PropFilter) (string, []any) {
	conds := []string{"wi.workspace_id = $1"}
	args := []any{workspaceID}
	n := 1
	if len(f.Status) > 0 {
		n++
		conds = append(conds, fmt.Sprintf("wi.status_key = ANY($%d)", n))
		args = append(args, f.Status)
	}
	if f.Assignee != "" {
		n++
		if f.Assignee == "none" {
			conds = append(conds, "wi.assignee_type = 'none'")
		} else {
			parts := strings.SplitN(f.Assignee, ":", 2)
			if len(parts) == 2 {

				conds = append(conds, fmt.Sprintf("wi.assignee_type = $%d AND wi.assignee_id = $%d", n, n+1))
				args = append(args, parts[0], parts[1])
				n++
			}
		}
	}
	if f.Project != "" {
		n++
		conds = append(conds, fmt.Sprintf("wi.project_id::text = $%d", n))
		args = append(args, f.Project)
	}
	if f.Search != "" {
		n++
		conds = append(conds, fmt.Sprintf("(wi.title ILIKE $%d OR wi.description ILIKE $%d)", n, n))
		args = append(args, "%"+f.Search+"%")
	}
	if f.Involves != "" {
		n++
		conds = append(conds, InvolvesCondition(n))
		args = append(args, f.Involves)
	}
	for _, pf := range props {
		cond, used := PropCondition(pf, n+1)
		conds = append(conds, cond)
		args = append(args, pf.Key)
		if used > 1 {
			args = append(args, pf.Value)
		}
		n += used
	}
	return " WHERE " + strings.Join(conds, " AND "), args
}

const maxPropFilters = 20

func validateInvolves(v string) error {
	if v == "" {
		return nil
	}
	if _, ok := domain.ParseUUID(v); !ok {
		return apierr.NewValidation("invalid involves filter",
			apierr.FieldError{Field: "involves", Problem: "must be a user uuid"})
	}
	return nil
}

func (s *ViewService) resolvePropFilters(ctx context.Context, workspaceID string, f QueryFilters) ([]domain.PropFilter, error) {
	if len(f.Props) == 0 {
		return nil, nil
	}
	defs, err := s.repo.ListPropertyDefs(ctx, workspaceID)
	if err != nil {
		return nil, err
	}
	return resolvePropFilters(toDomainPropDefs(defs), f.Props)
}

func resolvePropFilters(defs []domain.PropertyDef, raw map[string]string) ([]domain.PropFilter, error) {
	if len(raw) > maxPropFilters {
		return nil, apierr.NewValidation("too many property filters",
			apierr.FieldError{Field: "props", Problem: fmt.Sprintf("at most %d", maxPropFilters)})
	}
	out := make([]domain.PropFilter, 0, len(raw))
	for _, key := range domain.SortedPropKeys(raw) {
		pf, err := domain.ParsePropFilterExpr(key, raw[key])
		if err == nil {
			pf, err = domain.ResolvePropFilter(defs, pf)
		}
		if err != nil {
			return nil, apierr.NewValidation(err.Error(),
				apierr.FieldError{Field: "prop_" + key, Problem: err.Error()})
		}
		out = append(out, pf)
	}
	return out, nil
}

func parseCursor(c string) (string, string, bool) {
	parts := strings.SplitN(c, "|", 2)
	if len(parts) != 2 {
		return "", "", false
	}
	return parts[0], parts[1], true
}

func (s *ViewService) Views(ctx context.Context, workspaceID, userID string) ([]View, error) {
	return s.repo.ListWorkItemViews(ctx, workspaceID, userID)
}

func (s *ViewService) SaveView(ctx context.Context, workspaceID, userID string, v View) (*View, error) {
	v.Name = strings.TrimSpace(v.Name)
	if v.Name == "" || len([]rune(v.Name)) > 100 {
		return nil, apierr.NewValidation("invalid view name",
			apierr.FieldError{Field: "name", Problem: "1-100 chars"})
	}
	if v.Filters == nil {
		v.Filters = map[string]any{}
	}
	if v.Sort == nil {
		v.Sort = []map[string]any{}
	}
	if v.ID != "" {
		if _, ok := domain.ParseUUID(v.ID); !ok {
			return nil, apierr.NewValidation("invalid view id",
				apierr.FieldError{Field: "id", Problem: "must be a uuid"})
		}
	}
	out, ok, err := s.repo.UpsertWorkItemView(ctx, workspaceID, userID, v)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, apierr.New(apierr.NotFound, "view not found")
	}
	return &out, nil
}

func (s *ViewService) ViewPreference(ctx context.Context, workspaceID, userID string) (string, error) {
	viewID, _, err := s.repo.GetViewPreference(ctx, workspaceID, userID)
	return viewID, err
}

func (s *ViewService) SetViewPreference(ctx context.Context, workspaceID, userID string, viewID *string) error {
	if viewID != nil && *viewID != "" {
		views, err := s.repo.ListWorkItemViews(ctx, workspaceID, userID)
		if err != nil {
			return err
		}
		own := false
		for _, v := range views {
			if v.ID == *viewID {
				own = true
				break
			}
		}
		if !own {
			return apierr.NewValidation("default view not found",
				apierr.FieldError{Field: "default_view_id", Problem: "not found"})
		}
	} else {
		viewID = nil
	}
	return s.repo.SetViewPreference(ctx, workspaceID, userID, viewID)
}

type SearchService struct {
	repo Repo
}

func NewSearchService(repo Repo) *SearchService { return &SearchService{repo: repo} }

func (s *SearchService) Search(ctx context.Context, workspaceID, q string, includeTerminal bool, defs []StatusDef) ([]map[string]any, error) {
	q = strings.TrimSpace(q)
	if q == "" || len(q) > 200 {
		return nil, apierr.NewValidation("empty or too long query")
	}
	args := []any{workspaceID, "%" + q + "%"}
	conds := "wi.workspace_id = $1 AND (wi.title ILIKE $2 OR wi.description ILIKE $2)"
	if !includeTerminal {
		active := domain.ActiveStatusKeys(toDomainDefs(defs))
		if len(active) == 0 {
			return nil, nil
		}
		args = append(args, active)
		conds += fmt.Sprintf(" AND wi.status_key = ANY($%d)", len(args))
	}
	sqlq := fmt.Sprintf(`SELECT wi.id, wi.number, wi.title, wi.status_key,
		substring(wi.title from greatest(1, strpos(lower(wi.title), lower($2)) - 20) for 80) AS snippet
		FROM work_item wi WHERE %s ORDER BY wi.updated_at DESC LIMIT 50`, conds)
	return s.repo.SearchRows(ctx, sqlq, args)
}

func (s *SearchService) SearchFederated(ctx context.Context, workspaceID, q string) ([]domain.SearchHit, error) {
	q = strings.TrimSpace(q)
	if q == "" || len(q) > 200 {
		return nil, apierr.NewValidation("empty or too long query")
	}
	title, err := s.searchPath(ctx, workspaceID, q, "title")
	if err != nil {
		return nil, err
	}
	if len(title) <= 20 {
		return title, nil
	}
	body, err := s.searchPath(ctx, workspaceID, q, "body")
	if err != nil {
		return nil, err
	}
	comment, err := s.searchPath(ctx, workspaceID, q, "comment")
	if err != nil {
		return nil, err
	}
	return domain.RRFFuse([][]domain.SearchHit{title, body, comment}, 30), nil
}

func (s *SearchService) searchPath(ctx context.Context, wsID, q, path string) ([]domain.SearchHit, error) {
	sqlq, args := pathSQL(path, wsID, q)
	rows, err := s.repo.SearchRows(ctx, sqlq, args)
	if err != nil {
		return nil, err
	}
	out := make([]domain.SearchHit, 0, len(rows))
	for _, r := range rows {
		h := domain.SearchHit{Source: path}
		h.WorkItemID, _ = r["id"].(string)
		h.Title, _ = r["title"].(string)
		h.Snippet, _ = r["snippet"].(string)
		out = append(out, h)
	}
	return out, nil
}

func pathSQL(path, wsID, q string) (string, []any) {
	like := "%" + q + "%"
	switch path {
	case "body":
		return `SELECT wi.id::text AS id, wi.title, substring(wi.description from 1 for 120) AS snippet
FROM work_item wi WHERE wi.workspace_id = $1 AND wi.description ILIKE $2
ORDER BY wi.updated_at DESC LIMIT 50`, []any{wsID, like}
	case "comment":
		return `SELECT wi.id::text AS id, wi.title, substring(c.content from 1 for 120) AS snippet
FROM comment c JOIN work_item wi ON wi.id = c.work_item_id
WHERE wi.workspace_id = $1 AND c.content ILIKE $2 AND c.deleted_at IS NULL
ORDER BY wi.updated_at DESC LIMIT 50`, []any{wsID, like}
	default:
		return `SELECT wi.id::text AS id, wi.title, substring(wi.title from 1 for 120) AS snippet
FROM work_item wi WHERE wi.workspace_id = $1 AND wi.title ILIKE $2
ORDER BY wi.updated_at DESC LIMIT 50`, []any{wsID, like}
	}
}
