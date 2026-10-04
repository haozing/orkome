package domain

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func propDefsFixture() []PropertyDef {
	return []PropertyDef{
		{Key: "estimate", Name: "Estimate", Type: PropNumber},
		{Key: "notes", Name: "Notes", Type: PropText},
		{Key: "severity", Name: "Severity", Type: PropMultiSelect, Options: []string{"low", "high"}},
		{Key: "done_flag", Name: "Done", Type: PropCheckbox},
		{Key: "due", Name: "Due", Type: PropDate},
	}
}

func TestParsePropFilterExpr(t *testing.T) {
	f, err := ParsePropFilterExpr("estimate", "gt:5")
	require.NoError(t, err)
	assert.Equal(t, PropFilter{Key: "estimate", Op: PropOpGt, Value: "5"}, f)

	f, err = ParsePropFilterExpr("notes", "contains:hello world")
	require.NoError(t, err)
	assert.Equal(t, "hello world", f.Value, "after a colon inside the value, everything belongs to the value")

	f, err = ParsePropFilterExpr("notes", "empty:")
	require.NoError(t, err)
	assert.Equal(t, PropOpEmpty, f.Op)

	_, err = ParsePropFilterExpr("notes", "empty")
	require.NoError(t, err, "bare empty form parseable")

	for _, expr := range []string{"gte:5", "in:1,2", "=5", ":x", "5"} {
		_, err := ParsePropFilterExpr("k", expr)
		assert.Error(t, err, "expression %q should be rejected", expr)
	}
}

func TestResolvePropFilter(t *testing.T) {
	defs := propDefsFixture()

	for _, expr := range []string{"eq:5", "ne:5", "gt:5", "lt:10"} {
		f, err := ResolvePropFilter(defs, PropFilter{Key: "estimate", Op: expr[:2], Value: expr[3:]})
		require.NoError(t, err, "estimate %s should pass", expr)
		assert.True(t, f.Numeric, "number-type ordered comparisons must be numeric")
	}

	f, err := ResolvePropFilter(defs, PropFilter{Key: "estimate", Op: PropOpGt, Value: "5.0"})
	require.NoError(t, err)
	assert.True(t, f.Numeric)

	for _, tc := range []PropFilter{
		{Key: "notes", Op: PropOpContains, Value: "hello"},
		{Key: "notes", Op: PropOpEq, Value: "hello"},
		{Key: "due", Op: PropOpGt, Value: "2026-01-01"},
	} {
		out, err := ResolvePropFilter(defs, tc)
		require.NoError(t, err)
		assert.False(t, out.Numeric)
	}

	for _, key := range []string{"estimate", "notes", "severity", "done_flag"} {
		out, err := ResolvePropFilter(defs, PropFilter{Key: key, Op: PropOpEmpty, Value: "ignored"})
		require.NoError(t, err)
		assert.Empty(t, out.Value, "empty carries no value")
	}

	for _, tc := range []PropFilter{
		{Key: "severity", Op: PropOpGt, Value: "low"},
		{Key: "severity", Op: PropOpLt, Value: "low"},
		{Key: "done_flag", Op: PropOpGt, Value: "true"},
	} {
		_, err := ResolvePropFilter(defs, tc)
		assert.Error(t, err, "%s %s should be rejected", tc.Key, tc.Op)
	}

	_, err = ResolvePropFilter(defs, PropFilter{Key: "estimate", Op: PropOpGt, Value: "abc"})
	assert.Error(t, err)

	_, err = ResolvePropFilter(defs, PropFilter{Key: "ghost", Op: PropOpEq, Value: "x"})
	assert.Error(t, err)
}
