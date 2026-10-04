package domain

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestSquadMemberStatus(t *testing.T) {
	cases := []struct {
		name  string
		tasks []string
		want  string
	}{
		{"no tasks = idle", nil, SquadMemberIdle},
		{"terminal history only (JOIN face won't provide, defensive) = idle", []string{"completed", "failed", "cancelled"}, SquadMemberIdle},
		{"queued = queued", []string{"queued"}, SquadMemberQueued},
		{"deferred (channel media pending) = queued", []string{"deferred"}, SquadMemberQueued},
		{"queued + running = running (priority tier)", []string{"queued", "running"}, SquadMemberRunning},
		{"dispatched=running", []string{"dispatched"}, SquadMemberRunning},
		{"running = in progress", []string{"running"}, SquadMemberRunning},
		{"multi-task mix = running", []string{"queued", "deferred", "dispatched"}, SquadMemberRunning},
		{"unknown status fail-closed = idle", []string{"weird_state"}, SquadMemberIdle},
		{"unknown + queued = queued", []string{"weird_state", "queued"}, SquadMemberQueued},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, SquadMemberStatus(tc.tasks))
		})
	}
}
