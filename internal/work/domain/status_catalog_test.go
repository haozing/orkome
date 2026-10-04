package domain

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func wireTestDefs() []StatusDef {
	defs := append([]StatusDef{}, BuiltinStatuses...)
	return append(defs,
		StatusDef{Key: "done_1", Name: "Shipped", Category: CatDone, Position: 7},
		StatusDef{Key: "closed_1", Name: "Abandoned", Category: CatClosed, Position: 8},
		StatusDef{Key: "review_2", Name: "Gate Review", Category: CatStarted, Position: 9},
	)
}

func TestWireCategory(t *testing.T) {
	defs := wireTestDefs()
	cases := []struct {
		key  string
		want string
	}{

		{"backlog", "backlog"},
		{"todo", "todo"},
		{"in_progress", "in_progress"},
		{"in_review", "in_review"},
		{"blocked", "blocked"},
		{"done", "done"},
		{"cancelled", "cancelled"},

		{"done_1", "done"},
		{"closed_1", "cancelled"},
		{"review_2", "in_progress"},

		{"ghost_status", "ghost_status"},
	}
	for _, tc := range cases {
		assert.Equal(t, tc.want, WireCategory(defs, tc.key), "key=%s", tc.key)
	}

	assert.Equal(t, "todo", WireCategory(nil, "todo"))

	assert.Equal(t, "ghost_status", WireCategory(nil, "ghost_status"))
}

func TestWireCategoryUnstartedFold(t *testing.T) {
	defs := append([]StatusDef{}, BuiltinStatuses...)
	defs = append(defs, StatusDef{Key: "intake_1", Name: "Intake", Category: CatUnstarted, Position: 7})
	assert.Equal(t, "todo", WireCategory(defs, "intake_1"))
}

func TestExpandCategories(t *testing.T) {
	defs := []StatusDef{
		{Key: "backlog", Category: CatUnstarted, Position: 0},
		{Key: "todo", Category: CatUnstarted, Position: 1},
		{Key: "in_progress", Category: CatStarted, Position: 2},
		{Key: "done", Category: CatDone, Position: 3},
		{Key: "done_1", Category: CatDone, Position: 4},
		{Key: "cancelled", Category: CatClosed, Position: 5},
	}

	assert.Equal(t, []string{"backlog", "todo"}, ExpandCategories(defs, []string{"unstarted"}))
	assert.Equal(t, []string{"done", "done_1"}, ExpandCategories(defs, []string{"done"}))

	got := ExpandCategories(defs, []string{"todo", "done", "todo"})
	assert.Equal(t, []string{"todo", "done", "done_1"}, got)

	assert.Empty(t, ExpandCategories(defs, nil))
}
