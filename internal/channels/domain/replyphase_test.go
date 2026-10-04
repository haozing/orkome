package domain

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestReplyPhaseTransitions(t *testing.T) {
	assert.True(t, ReplyPhaseMayOpenPlaceholder(""), "no rows to open a placeholder")
	assert.True(t, ReplyPhaseMayOpenPlaceholder(ReplyPhaseStreaming), "streaming placeholder resumable (takeover retry after a previous send failure)")
	assert.False(t, ReplyPhaseMayOpenPlaceholder(ReplyPhaseTerminal), "final draft took over: placeholder must not reopen")
	assert.False(t, ReplyPhaseMayOpenPlaceholder(ReplyPhaseSettled), "round already finished: cannot reopen")

	assert.True(t, ReplyPhaseMayDeliverTerminal(""), "no-row direct final draft delivery (single-phase channel)")
	assert.True(t, ReplyPhaseMayDeliverTerminal(ReplyPhaseStreaming), "streaming → terminal two-phase convergence")
	assert.True(t, ReplyPhaseMayDeliverTerminal(ReplyPhaseTerminal), "terminal → terminal idempotent redelivery (retry chain continues the same round)")
	assert.False(t, ReplyPhaseMayDeliverTerminal(ReplyPhaseSettled), "settled re-entry is a no-op")
}

func TestCardStatusFinish(t *testing.T) {
	assert.True(t, CardStatusMayFinish(CardStatusPending))
	assert.True(t, CardStatusMayFinish(CardStatusStreaming))
	assert.True(t, CardStatusMayFinish(CardStatusFinal), "repeated finish idempotent")
	assert.False(t, CardStatusMayFinish(CardStatusError), "error terminal: failure path resends without rewrite")
}
