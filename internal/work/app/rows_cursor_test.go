package app

// Unit coverage for the structured table/rows cursor (FP-18 D-01/D-11):
// encode/decode round-trip, and every malformed shape rejected so a garbage
// cursor is a 400 at the handler instead of a pgx 22P02 500.

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRowsCursorRoundTrip(t *testing.T) {
	ct := time.Date(2026, 10, 12, 4, 5, 6, 789000000, time.UTC)
	id := "0b9c4c86-9f4c-4a74-8ed0-5f0bc9a7e111"

	enc := encodeRowsCursor(ct, id)
	require.NotEmpty(t, enc)
	assert.NotContains(t, enc, "|", "cursor must be a single opaque token")
	assert.NotContains(t, enc, "+0800 CST", "no Go %v debug text may leak into the cursor")

	gotCT, gotID, ok := decodeRowsCursor(enc)
	require.True(t, ok)
	assert.True(t, gotCT.Equal(ct))
	assert.Equal(t, id, gotID)
}

func TestDecodeRowsCursorRejectsGarbage(t *testing.T) {
	bad := []string{
		"",
		"not base64!!",
		"aGVsbG8=",          // valid base64, not JSON
		"eyJhIjoxfQ",        // {"a":1} — wrong keys
		"e30",               // {} — missing fields
		`{"created_at":"not-a-time","id":"x"}`, // not decodable to the struct
	}
	for _, c := range bad {
		_, _, ok := decodeRowsCursor(c)
		assert.False(t, ok, "cursor %q must be rejected", c)
	}

	// zero timestamp / empty id / non-uuid id
	ct := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	_, _, ok := decodeRowsCursor(encodeRowsCursor(time.Time{}, "0b9c4c86-9f4c-4a74-8ed0-5f0bc9a7e111"))
	assert.False(t, ok, "zero timestamp rejected")
	_, _, ok = decodeRowsCursor(encodeRowsCursor(ct, ""))
	assert.False(t, ok, "empty id rejected")
	_, _, ok = decodeRowsCursor(encodeRowsCursor(ct, "not-a-uuid"))
	assert.False(t, ok, "non-uuid id rejected — must never reach PG as an uuid param")
}
