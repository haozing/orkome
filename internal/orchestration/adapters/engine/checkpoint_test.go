package engine

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestCheckpointFingerprintRoundTrip(t *testing.T) {
	payload := []byte(`{"messages":[1,2,3]}`)
	framed := withFingerprint(payload)
	assert.Equal(t, payload, append([]byte(nil), framed[len(checkpointFingerprint)+1:]...),
		"framing = fingerprint headers + original payload")

	got, ok := stripFingerprint(framed)
	assert.True(t, ok)
	assert.Equal(t, payload, got, "Get side should strip fingerprint headers and restore the payload")
}

func TestCheckpointFingerprintMismatchDegrades(t *testing.T) {

	_, ok := stripFingerprint([]byte("eino/0\nlegacy-state"))
	assert.False(t, ok, "old schema version fingerprints should be judged poison")
	_, ok = stripFingerprint([]byte("other-engine/1\nforeign"))
	assert.False(t, ok, "foreign engine fingerprints judged poison")

	_, ok = stripFingerprint([]byte("no header at all"))
	assert.False(t, ok)
	_, ok = stripFingerprint(nil)
	assert.False(t, ok, "empty-byte flow poison check")
}
