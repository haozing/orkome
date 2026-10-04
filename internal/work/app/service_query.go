package app

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"orkome/internal/contract/apierr"
	"orkome/internal/work/domain"
)

type CompileRequest struct {
	Filters QueryFilters `json:"filters"`
	GroupBy string       `json:"group_by"`
	Sort    []string     `json:"sort"`
	Limit   int          `json:"limit"`
}

type CompiledQuery struct {
	Fingerprint string      `json:"fingerprint"`
	Parsed      ParsedQuery `json:"parsed"`
	Errors      []string    `json:"errors"`
}

type ParsedQuery struct {
	Filters FilterSummary `json:"filters"`
	GroupBy string        `json:"group_by,omitempty"`
	Sort    []string      `json:"sort"`
	Limit   int           `json:"limit"`
}

type FilterSummary struct {
	Status      []string `json:"status,omitempty"`
	Assignee    string   `json:"assignee,omitempty"`
	Labels      []string `json:"labels,omitempty"`
	Project     string   `json:"project,omitempty"`
	PriorityMin *int     `json:"priority_min,omitempty"`
	Search      string   `json:"search,omitempty"`

	Involves string `json:"involves,omitempty"`

	Props map[string]string `json:"props,omitempty"`
}

var sortFields = map[string]bool{
	"created_at": true, "updated_at": true, "priority": true, "number": true, "title": true,
}

var assigneeRe = regexp.MustCompile(`^(user|agent|squad):(.+)$`)

func groupByOK(v string) bool {
	switch v {
	case "", "status", "assignee", "label", "project":
		return true
	}
	return false
}

func CompileParsed(defs []domain.StatusDef, propDefs []domain.PropertyDef, req CompileRequest) (CompiledQuery, []apierr.FieldError) {
	var errs []apierr.FieldError

	status := req.Filters.Status
	if len(status) > 0 {
		for _, s := range status {
			if domain.IsCategory(s) || domain.IsBuiltinKey(s) {
				continue
			}
			if _, ok := domain.WritableCategory(defs, s); !ok {
				errs = append(errs, apierr.FieldError{Field: "filters.status",
					Problem: fmt.Sprintf("unknown status %q", s)})
			}
		}
		status = domain.ExpandCategories(defs, status)
	}

	if a := req.Filters.Assignee; a != "" && a != "none" {
		m := assigneeRe.FindStringSubmatch(a)
		if m == nil {
			errs = append(errs, apierr.FieldError{Field: "filters.assignee",
				Problem: `must be "none" or "user|agent|squad:<uuid>"`})
		} else if _, ok := domain.ParseUUID(m[2]); !ok {
			errs = append(errs, apierr.FieldError{Field: "filters.assignee",
				Problem: fmt.Sprintf("invalid uuid in %q", a)})
		}
	}

	if v := req.Filters.Involves; v != "" {
		if _, ok := domain.ParseUUID(v); !ok {
			errs = append(errs, apierr.FieldError{Field: "filters.involves",
				Problem: "must be a user uuid"})
		}
	}

	if raw := req.Filters.Props; len(raw) > 0 {
		if len(raw) > maxPropFilters {
			errs = append(errs, apierr.FieldError{Field: "filters.props",
				Problem: fmt.Sprintf("at most %d property filters", maxPropFilters)})
		} else {
			for _, key := range domain.SortedPropKeys(raw) {
				pf, err := domain.ParsePropFilterExpr(key, raw[key])
				if err == nil {
					_, err = domain.ResolvePropFilter(propDefs, pf)
				}
				if err != nil {
					errs = append(errs, apierr.FieldError{Field: "filters.props." + key,
						Problem: err.Error()})
				}
			}
		}
	}

	if !groupByOK(req.GroupBy) {
		errs = append(errs, apierr.FieldError{Field: "group_by",
			Problem: "must be status|assignee|label|project"})
	}
	sort := normalizeSort(req.Sort)
	for _, s := range req.Sort {
		field := s
		if i := strings.IndexByte(s, ':'); i >= 0 {
			field = s[:i]
		}
		if !sortFields[field] {
			errs = append(errs, apierr.FieldError{Field: "sort",
				Problem: fmt.Sprintf("unknown sort field %q", field)})
		}
	}

	limit := req.Limit
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}

	if len(errs) > 0 {
		return CompiledQuery{}, errs
	}
	return CompiledQuery{
		Fingerprint: req.Filters.Fingerprint(),
		Parsed: ParsedQuery{
			Filters: FilterSummary{
				Status:      status,
				Assignee:    req.Filters.Assignee,
				Labels:      req.Filters.Labels,
				Project:     req.Filters.Project,
				PriorityMin: req.Filters.PriorityMin,
				Search:      req.Filters.Search,
				Involves:    req.Filters.Involves,
				Props:       req.Filters.Props,
			},
			GroupBy: req.GroupBy,
			Sort:    sort,
			Limit:   limit,
		},
		Errors: []string{},
	}, nil
}

func normalizeSort(in []string) []string {
	if len(in) == 0 {
		return []string{"created_at:desc"}
	}
	out := make([]string, 0, len(in))
	for _, s := range in {
		field, dir, found := strings.Cut(s, ":")
		if !found {
			dir = "asc"
		}
		out = append(out, field+":"+dir)
	}
	return out
}

func (s *ViewService) Compile(ctx context.Context, workspaceID string, req CompileRequest) (*CompiledQuery, error) {
	if err := s.repo.EnsureStatusSeed(ctx, workspaceID); err != nil {
		return nil, err
	}
	defs, err := s.repo.ListStatuses(ctx, workspaceID)
	if err != nil {
		return nil, err
	}

	var propDefs []domain.PropertyDef
	if len(req.Filters.Props) > 0 {
		pd, err := s.repo.ListPropertyDefs(ctx, workspaceID)
		if err != nil {
			return nil, err
		}
		propDefs = toDomainPropDefs(pd)
	}
	out, errs := CompileParsed(toDomainDefs(defs), propDefs, req)
	if len(errs) > 0 {
		return nil, apierr.NewValidation("invalid query filters", errs...)
	}
	return &out, nil
}

func (s *ViewService) expandStatusFilter(ctx context.Context, workspaceID string, f QueryFilters) QueryFilters {
	if len(f.Status) == 0 {
		return f
	}
	if err := s.repo.EnsureStatusSeed(ctx, workspaceID); err != nil {
		return f
	}
	defs, err := s.repo.ListStatuses(ctx, workspaceID)
	if err != nil {
		return f
	}
	for _, v := range f.Status {
		if domain.IsCategory(v) {
			f.Status = domain.ExpandCategories(toDomainDefs(defs), f.Status)
			break
		}
	}
	return f
}
