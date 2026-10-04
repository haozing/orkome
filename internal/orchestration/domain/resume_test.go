package domain

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestEngineTagCompatible(t *testing.T) {
	assert.True(t, EngineTagCompatible("", ""), "neither side marked: compatible (historical behavior)")
	assert.True(t, EngineTagCompatible("", "eino"), "output side empty (historical rows before migration 019): allowed")
	assert.True(t, EngineTagCompatible("eino", "eino"), "same engine: compatible")
	assert.False(t, EngineTagCompatible("eino", "other"), "foreign engine: handled as poison (internal archive 05 §2.9)")
	assert.True(t, EngineTagCompatible("eino", ""),
		"query side unwired (empty tag) = no gate, allow all (GetLastSessionRef port contract; symmetric unknowability does not block)")
}
