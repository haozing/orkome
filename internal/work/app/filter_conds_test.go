package app

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"orkome/internal/work/domain"
)

func TestInvolvesConditionThreeForms(t *testing.T) {
	cond := InvolvesCondition(4)
	for _, want := range []string{
		"wi.assignee_id = $4::uuid",
		"wi.creator_id = $4::uuid",
		"EXISTS(SELECT 1 FROM work_item_subscriber wis",
		"wis.work_item_id = wi.id AND wis.user_id = $4::uuid",
	} {
		assert.Contains(t, cond, want)
	}
	assert.Equal(t, 3, strings.Count(cond, "$4"), "three predicates reuse the same placeholder")
}

func TestPropCondition(t *testing.T) {
	cases := []struct {
		f        domain.PropFilter
		want     string
		usedArgs int
	}{
		{domain.PropFilter{Op: domain.PropOpEq, Value: "5", Numeric: true},
			"(wi.properties->>$1)::numeric = $2::numeric", 2},
		{domain.PropFilter{Op: domain.PropOpNe, Value: "5", Numeric: true},
			"(wi.properties->>$1)::numeric <> $2::numeric", 2},
		{domain.PropFilter{Op: domain.PropOpGt, Value: "5", Numeric: true},
			"(wi.properties->>$1)::numeric > $2::numeric", 2},
		{domain.PropFilter{Op: domain.PropOpLt, Value: "10", Numeric: true},
			"(wi.properties->>$1)::numeric < $2::numeric", 2},
		{domain.PropFilter{Op: domain.PropOpEq, Value: "high"},
			"wi.properties->>$1 = $2", 2},
		{domain.PropFilter{Op: domain.PropOpNe, Value: "high"},
			"wi.properties->>$1 <> $2", 2},
		{domain.PropFilter{Op: domain.PropOpContains, Value: "hel"},
			"wi.properties->>$1 ILIKE '%' || $2 || '%'", 2},
		{domain.PropFilter{Op: domain.PropOpEmpty},
			"(wi.properties->>$1 IS NULL OR wi.properties->>$1 = '')", 1},
	}
	for _, tc := range cases {
		got, used := PropCondition(tc.f, 1)
		assert.Equal(t, tc.want, got)
		assert.Equal(t, tc.usedArgs, used)
	}
}

func TestCompileFiltersRichDimensions(t *testing.T) {
	where, args := compileFilters("ws-1", QueryFilters{
		Status:   []string{"todo"},
		Involves: "00000000-0000-4000-8000-00000000000A",
		Props:    map[string]string{"notes": "contains:bug", "estimate": "gt:5"},
	}, []domain.PropFilter{
		{Key: "estimate", Op: domain.PropOpGt, Value: "5", Numeric: true},
		{Key: "notes", Op: domain.PropOpContains, Value: "bug"},
	})
	require.NotNil(t, args)

	assert.Equal(t, []any{
		"ws-1", []string{"todo"}, "00000000-0000-4000-8000-00000000000A", "estimate", "5", "notes", "bug",
	}, args)
	assert.Contains(t, where, "wi.status_key = ANY($2)")
	assert.Contains(t, where, "wi.assignee_id = $3::uuid")
	assert.Contains(t, where, "wi.creator_id = $3::uuid")
	assert.Contains(t, where, "(wi.properties->>$4)::numeric > $5::numeric")
	assert.Contains(t, where, "wi.properties->>$6 ILIKE '%' || $7 || '%'")
	assert.True(t, strings.HasPrefix(where, " WHERE wi.workspace_id = $1 AND "), "the first condition is always the tenant predicate")

	where2, args2 := compileFilters("ws-1", QueryFilters{}, nil)
	assert.Equal(t, []any{"ws-1"}, args2)
	assert.Equal(t, " WHERE wi.workspace_id = $1", where2)
}
