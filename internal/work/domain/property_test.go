package domain

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValidateWorkItemProperties(t *testing.T) {
	defs := []PropertyDef{
		{Key: "t", Type: PropText}, {Key: "u", Type: PropURL}, {Key: "n", Type: PropNumber},
		{Key: "c", Type: PropCheckbox}, {Key: "d", Type: PropDate}, {Key: "s", Type: PropSelect, Options: []string{"a", "b", "c"}},
		{Key: "ms", Type: PropMultiSelect, Options: []string{"a", "b", "c"}},
		{Key: "ac", Type: PropActor}, {Key: "mac", Type: PropMultiActor},
	}
	u1 := "00000000-0000-4000-8000-000000000001"
	u2 := "00000000-0000-4000-8000-000000000002"

	valid := map[string]any{
		"t":   "hello",
		"u":   "https://example.com/x",
		"n":   3.5,
		"c":   true,
		"d":   "2026-10-02",
		"s":   "b",
		"ms":  []any{"c", "a"},
		"ac":  "member:" + u1,
		"mac": []any{"member:" + u2, "member:" + u1},
	}
	out, err := ValidateWorkItemProperties(defs, valid)
	require.NoError(t, err)

	assert.Equal(t, []string{"a", "c"}, out["ms"])

	assert.Equal(t, []string{"member:" + u2, "member:" + u1}, out["mac"])
	assert.Equal(t, "member:"+u1, out["ac"])

	bad := []struct {
		key string
		val any
	}{

		{"d", "not-a-date"}, {"d", "2026-13-45"}, {"d", "2026-02-30"}, {"d", "2026-10-02T00:00:00Z"}, {"d", 20261002},

		{"ac", "user:" + u1}, {"ac", u1}, {"ac", "member:not-a-uuid"}, {"ac", "member:"}, {"ac", "agent:" + u1}, {"ac", 42},

		{"mac", []any{"user:" + u1}}, {"mac", []any{"member:zzz"}},
		{"mac", manyActors(21)}, {"mac", []any{"member:" + u1, 7}},

		{"t", stringOfRunes(2001)}, {"u", "example.com/no-scheme"}, {"s", "zz"}, {"ms", []any{"zz"}},

		{"unknown", "x"},
	}
	for _, tc := range bad {
		_, err := ValidateWorkItemProperties(defs, map[string]any{tc.key: tc.val})
		assert.Error(t, err, "key=%s val=%v should be rejected", tc.key, tc.val)
	}
}

func TestCanonicalJSONStable(t *testing.T) {
	defs := []PropertyDef{
		{Key: "d", Type: PropDate},
		{Key: "ms", Type: PropMultiSelect, Options: []string{"a", "b", "c"}},
	}
	a, err := ValidateWorkItemProperties(defs, map[string]any{
		"ms": []any{"b", "a"}, "d": "2026-01-31",
	})
	require.NoError(t, err)

	b, err := ValidateWorkItemProperties(defs, map[string]any{
		"d": "2026-01-31", "ms": []any{"b", "a"},
	})
	require.NoError(t, err)
	ja, err := json.Marshal(a)
	require.NoError(t, err)
	jb, err := json.Marshal(b)
	require.NoError(t, err)
	assert.Equal(t, string(ja), string(jb))
	assert.JSONEq(t, `{"d":"2026-01-31","ms":["a","b"]}`, string(ja))
}

func TestMultiActorDedup(t *testing.T) {
	defs := []PropertyDef{{Key: "mac", Type: PropMultiActor}}
	u1 := "00000000-0000-4000-8000-000000000001"
	out, err := ValidateWorkItemProperties(defs, map[string]any{
		"mac": []any{"member:" + u1, "member:" + upper(u1), "member:" + u1},
	})
	require.NoError(t, err)
	assert.Equal(t, []string{"member:" + u1}, out["mac"])

	ok20 := manyActors(20)
	_, err = ValidateWorkItemProperties(defs, map[string]any{"mac": ok20})
	assert.NoError(t, err)
	_, err = ValidateWorkItemProperties(defs, map[string]any{"mac": manyActors(21)})
	assert.Error(t, err)
}

func manyActors(n int) []any {
	out := make([]any, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, "member:"+testUUID(i))
	}
	return out
}

func testUUID(i int) string {
	const hexd = "0123456789abcdef"
	s := []byte("00000000-0000-4000-8000-000000000000")
	tail := s[24:36]
	for k := len(tail) - 1; k >= 0 && i > 0; k-- {
		tail[k] = hexd[i%16]
		i /= 16
	}
	return string(s)
}

func stringOfRunes(n int) string {
	b := make([]rune, n)
	for i := range b {
		b[i] = 'x'
	}
	return string(b)
}
