package app

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"orkome/internal/contract/apierr"
	"orkome/internal/work/domain"
)

func compileTestDefs() []domain.StatusDef {
	defs := append([]domain.StatusDef{}, domain.BuiltinStatuses...)
	return append(defs, domain.StatusDef{Key: "done_1", Name: "Shipped", Category: domain.CatDone, Position: 7})
}

func compileTestPropDefs() []domain.PropertyDef {
	return []domain.PropertyDef{
		{Key: "estimate", Name: "Estimate", Type: domain.PropNumber},
		{Key: "notes", Name: "Notes", Type: domain.PropText},
		{Key: "severity", Name: "Severity", Type: domain.PropMultiSelect, Options: []string{"low", "high"}},
	}
}

func TestCompileParsedValid(t *testing.T) {
	pmin := 2
	out, errs := CompileParsed(compileTestDefs(), compileTestPropDefs(), CompileRequest{
		Filters: QueryFilters{
			Status:      []string{"done", "backlog"},
			Assignee:    "user:00000000-0000-4000-8000-00000000000A",
			Project:     "p1",
			PriorityMin: &pmin,
			Search:      "kw",
		},
		GroupBy: "status",
		Sort:    []string{"priority", "created_at:desc"},
		Limit:   999,
	})
	require.Empty(t, errs)
	assert.NotEmpty(t, out.Fingerprint)
	assert.Equal(t, []string{}, out.Errors)

	assert.Equal(t, []string{"done", "done_1", "backlog"}, out.Parsed.Filters.Status)
	assert.Equal(t, "status", out.Parsed.GroupBy)
	assert.Equal(t, []string{"priority:asc", "created_at:desc"}, out.Parsed.Sort)
	assert.Equal(t, 200, out.Parsed.Limit)

	out, errs = CompileParsed(compileTestDefs(), compileTestPropDefs(), CompileRequest{})
	require.Empty(t, errs)
	assert.Equal(t, []string{"created_at:desc"}, out.Parsed.Sort)
	assert.Equal(t, 50, out.Parsed.Limit)
	assert.Empty(t, out.Parsed.Filters.Status)
}

func TestCompileParsedInvalid(t *testing.T) {
	_, errs := CompileParsed(compileTestDefs(), compileTestPropDefs(), CompileRequest{
		Filters: QueryFilters{
			Status:   []string{"ghost_status"},
			Assignee: "user:not-a-uuid",
		},
		GroupBy: "nonsense",
		Sort:    []string{"hacked_field:asc"},
	})
	require.NotEmpty(t, errs)
	byField := map[string]bool{}
	for _, e := range errs {
		byField[e.Field] = true
	}
	for _, f := range []string{"filters.status", "filters.assignee", "group_by", "sort"} {
		assert.True(t, byField[f], "missing-field error %s", f)
	}

	_, errs = CompileParsed(compileTestDefs(), compileTestPropDefs(), CompileRequest{Filters: QueryFilters{Assignee: "member:x"}})
	require.NotEmpty(t, errs)
	assert.Equal(t, "filters.assignee", errs[0].Field)

	ae := apierr.NewValidation("invalid query filters", errs...)
	assert.Equal(t, apierr.Validation, ae.Code)
}

func TestCompileParsedFingerprintStable(t *testing.T) {
	a, errs := CompileParsed(compileTestDefs(), compileTestPropDefs(), CompileRequest{Filters: QueryFilters{Status: []string{"done"}}})
	require.Empty(t, errs)
	b, errs := CompileParsed(compileTestDefs(), compileTestPropDefs(), CompileRequest{})
	require.Empty(t, errs)
	assert.Equal(t, a.Fingerprint, QueryFilters{Status: []string{"done"}}.Fingerprint())
	assert.Equal(t, b.Fingerprint, QueryFilters{}.Fingerprint())
	assert.NotEqual(t, a.Fingerprint, b.Fingerprint)
}

func TestCompileParsedInvolves(t *testing.T) {
	uid := "00000000-0000-4000-8000-00000000000B"
	out, errs := CompileParsed(compileTestDefs(), nil, CompileRequest{Filters: QueryFilters{Involves: uid}})
	require.Empty(t, errs)
	assert.Equal(t, uid, out.Parsed.Filters.Involves)
	assert.Equal(t, QueryFilters{Involves: uid}.Fingerprint(), out.Fingerprint)

	_, errs = CompileParsed(compileTestDefs(), nil, CompileRequest{Filters: QueryFilters{Involves: "not-a-uuid"}})
	require.Len(t, errs, 1)
	assert.Equal(t, "filters.involves", errs[0].Field)
}

func TestCompileParsedPropOperators(t *testing.T) {
	okCases := []map[string]string{
		{"estimate": "gt:5"},
		{"estimate": "lt:10"},
		{"estimate": "eq:5.5"},
		{"estimate": "ne:5"},
		{"notes": "contains:hello"},
		{"notes": "empty:"},
		{"severity": "empty"},
		{"severity": "contains:low"},
	}
	for _, props := range okCases {
		out, errs := CompileParsed(compileTestDefs(), compileTestPropDefs(),
			CompileRequest{Filters: QueryFilters{Props: props}})
		require.Empty(t, errs, "props=%v should pass", props)
		assert.Equal(t, props, out.Parsed.Filters.Props, "parsed summary carried as-is")
	}

	badCases := []struct {
		props map[string]string
		field string
	}{
		{map[string]string{"estimate": "gte:5"}, "filters.props.estimate"},
		{map[string]string{"ghost": "eq:x"}, "filters.props.ghost"},
		{map[string]string{"severity": "gt:low"}, "filters.props.severity"},
		{map[string]string{"severity": "lt:a"}, "filters.props.severity"},
		{map[string]string{"estimate": "gt:abc"}, "filters.props.estimate"},
		{map[string]string{"estimate": "eq:5,3"}, "filters.props.estimate"},
	}
	for _, tc := range badCases {
		_, errs := CompileParsed(compileTestDefs(), compileTestPropDefs(),
			CompileRequest{Filters: QueryFilters{Props: tc.props}})
		require.NotEmpty(t, errs, "props=%v should error", tc.props)
		assert.Equal(t, tc.field, errs[0].Field)
	}

	tooMany := map[string]string{}
	for i := 0; i < 21; i++ {
		tooMany[fmt.Sprintf("k%02d", i)] = "empty"
	}
	_, errs := CompileParsed(compileTestDefs(), compileTestPropDefs(), CompileRequest{Filters: QueryFilters{Props: tooMany}})
	require.NotEmpty(t, errs)
	assert.Equal(t, "filters.props", errs[0].Field)

	f1 := QueryFilters{Involves: "00000000-0000-4000-8000-00000000000C", Props: map[string]string{"estimate": "gt:5"}}
	f2 := QueryFilters{Involves: "00000000-0000-4000-8000-00000000000C", Props: map[string]string{"estimate": "gt:6"}}
	assert.NotEqual(t, f1.Fingerprint(), f2.Fingerprint(), "props value differences must change the fingerprint (anti page-drift)")
}
