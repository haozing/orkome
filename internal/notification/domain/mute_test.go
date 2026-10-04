package domain

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestMuted_ThreeGroups(t *testing.T) {
	g := MuteGroups{
		Types:       []string{"task_completed"},
		WorkItemIDs: []string{"wi-1"},
		SessionIDs:  []string{"sess-1"},
	}
	assert.True(t, Muted(g, MuteTarget{Type: "task_completed"}), "source group hit")
	assert.True(t, Muted(g, MuteTarget{WorkItemID: "wi-1"}), "task group hit")
	assert.True(t, Muted(g, MuteTarget{SessionID: "sess-1"}), "session group hit")

	assert.False(t, Muted(g, MuteTarget{Type: "comment_created"}), "different categories not muted")
	assert.False(t, Muted(g, MuteTarget{WorkItemID: "wi-2"}), "different tasks not muted")
	assert.False(t, Muted(g, MuteTarget{SessionID: "sess-2"}), "different session not muted")
	assert.False(t, Muted(g, MuteTarget{}), "keys without ownership are never muted")
}

func TestMuted_WildcardAndCrossGroup(t *testing.T) {
	all := MuteGroups{Types: []string{"*"}}
	assert.True(t, Muted(all, MuteTarget{Type: "eval_report"}), "  * mute all categories")
	assert.False(t, Muted(all, MuteTarget{WorkItemID: "wi-1"}), "  * applies to the source group only")

	wi := MuteGroups{WorkItemIDs: []string{"wi-1"}}
	assert.False(t, Muted(wi, MuteTarget{Type: "task_completed"}))

	multi := MuteGroups{SessionIDs: []string{"s-9"}}
	assert.True(t, Muted(multi, MuteTarget{Type: "task_completed", WorkItemID: "wi-1", SessionID: "s-9"}))
}
