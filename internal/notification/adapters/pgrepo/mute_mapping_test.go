package pgrepo

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"orkome/internal/notification/app"
)

func TestSplitMergeMutedTypes_RoundTrip(t *testing.T) {
	raw := []string{"task_completed", app.SessionMutePrefix + "sess-1", "comment_created", app.SessionMutePrefix + "sess-2"}
	types, sessions := splitMutedTypes(raw)
	assert.Equal(t, []string{"task_completed", "comment_created"}, types)
	assert.Equal(t, []string{"sess-1", "sess-2"}, sessions)

	assert.ElementsMatch(t, raw, mergeMutedTypes(types, sessions))
}

func TestSplitMutedTypes_EmptyAndPlain(t *testing.T) {
	types, sessions := splitMutedTypes(nil)
	assert.Nil(t, types)
	assert.Nil(t, sessions)

	types, sessions = splitMutedTypes([]string{"eval_report"})
	assert.Equal(t, []string{"eval_report"}, types)
	assert.Nil(t, sessions)

	merged := mergeMutedTypes(nil, nil)
	assert.NotNil(t, merged, "write side must persist empty arrays not NULL (emptyIfNil semantics upfront)")
	assert.Empty(t, merged)
}
