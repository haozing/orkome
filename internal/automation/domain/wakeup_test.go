package domain

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestEventFilter_Matches(t *testing.T) {
	base := EventFacts{EventType: "comment.created", ActorType: "agent", ActorID: "ag-1", AgentID: "ag-1", TaskID: "t-9"}

	require.True(t, EventFilter{EventTypes: []string{"comment.created"}}.Matches(base))

	require.False(t, EventFilter{EventTypes: []string{"task.completed"}}.Matches(base))

	require.False(t, EventFilter{}.Matches(base))

	require.True(t, EventFilter{EventTypes: []string{"task.failed", "comment.created"}}.Matches(base))

	require.True(t, EventFilter{EventTypes: []string{"comment.created"}, AgentID: "ag-1"}.Matches(base))
	require.False(t, EventFilter{EventTypes: []string{"comment.created"}, AgentID: "ag-2"}.Matches(base))

	require.True(t, EventFilter{EventTypes: []string{"comment.created"}, TaskID: "t-9"}.Matches(base))
	require.False(t, EventFilter{EventTypes: []string{"comment.created"}, TaskID: "t-8"}.Matches(base))
	require.True(t, EventFilter{EventTypes: []string{"comment.created"}, ActorType: "agent", ActorID: "ag-1"}.Matches(base))
	require.False(t, EventFilter{EventTypes: []string{"comment.created"}, ActorType: "member"}.Matches(base))

	require.False(t, EventFilter{EventTypes: []string{"work_item.updated"}, AgentID: "ag-1"}.
		Matches(EventFacts{EventType: "work_item.updated"}))
}

func TestExtractEventFacts(t *testing.T) {

	c := ExtractEventFacts("comment.created", map[string]any{
		"author_type": "agent", "author_id": "ag-7"})
	require.Equal(t, EventFacts{EventType: "comment.created", ActorType: "agent", ActorID: "ag-7"}, c)

	k := ExtractEventFacts("task.completed", map[string]any{"id": "tk-1", "agent_id": "ag-3"})
	require.Equal(t, "tk-1", k.TaskID)
	require.Equal(t, "ag-3", k.AgentID)

	w := ExtractEventFacts("work_item.created", map[string]any{
		"creator_type": "user", "creator_id": "u-1", "assignee_type": "agent", "assignee_id": "ag-9"})
	require.Equal(t, "user", w.ActorType)
	require.Equal(t, "u-1", w.ActorID)
	require.Equal(t, "ag-9", w.AgentID)

	require.Equal(t, EventFacts{EventType: "x"}, ExtractEventFacts("x", nil))
}

func child(stage string, terminal bool) ChildStageFact {
	return ChildStageFact{Stage: stage, Terminal: terminal}
}

func TestChildrenDone_AllTerminal(t *testing.T) {
	met, fp := ChildrenDone([]ChildStageFact{child("1", true), child("1", true), child("2", true)})
	require.True(t, met)
	require.Equal(t, "all", fp)
}

func TestChildrenDone_StageClosedWithLaterWaiting(t *testing.T) {

	met, fp := ChildrenDone([]ChildStageFact{child("1", true), child("1", true), child("2", false)})
	require.True(t, met)
	require.Equal(t, "stage:1", fp)
}

func TestChildrenDone_FirstStageOpen_NotMet(t *testing.T) {

	met, fp := ChildrenDone([]ChildStageFact{child("1", false), child("2", true)})
	require.False(t, met)
	require.Empty(t, fp)
}

func TestChildrenDone_CrossStageIndependence(t *testing.T) {

	met, fp := ChildrenDone([]ChildStageFact{child("0", true), child("1", false), child("2", false)})
	require.True(t, met)
	require.Equal(t, "stage:0", fp)

	met, fp = ChildrenDone([]ChildStageFact{child("0", true), child("0", false), child("1", false)})
	require.False(t, met)
}

func TestChildrenDone_StableStageOrdering(t *testing.T) {

	_, a := ChildrenDone([]ChildStageFact{child("2", false), child("1", true)})
	_, b := ChildrenDone([]ChildStageFact{child("1", true), child("2", false)})
	require.Equal(t, "stage:1", a)
	require.Equal(t, a, b)
}

func TestChildrenDone_EmptyAndUnstaged(t *testing.T) {
	met, fp := ChildrenDone(nil)
	require.False(t, met)
	require.Empty(t, fp, "no trigger without sub-items")
	met, fp = ChildrenDone([]ChildStageFact{})
	require.False(t, met)

	met, fp = ChildrenDone([]ChildStageFact{child("", true), child("", false), child("1", true)})
	require.False(t, met)

	met, fp = ChildrenDone([]ChildStageFact{child("", true), child("1", true)})
	require.True(t, met)
	require.Equal(t, "all", fp)
}

func TestEdgeTriggered(t *testing.T) {
	prev := "met"
	require.True(t, EdgeTriggered(nil, "met"), "first non-empty fingerprint observed = edge")
	require.True(t, EdgeTriggered(&prev, "stage:2"), "fingerprint change = edge")
	require.False(t, EdgeTriggered(&prev, "met"), "unchanged fingerprint does not trigger")
	require.False(t, EdgeTriggered(&prev, ""), "negative edge does not trigger (only turning true)")
	empty := ""
	require.False(t, EdgeTriggered(&empty, ""), "staying false does not trigger")
}

func TestCondFingerprint(t *testing.T) {
	require.Equal(t, "met", CondFingerprint(true))
	require.Equal(t, "", CondFingerprint(false))
}

func TestCapEvidence(t *testing.T) {
	small := strings.Repeat("a", 1000)
	require.Equal(t, small, CapEvidence(small))
	big := strings.Repeat("a", MaxEvidenceBytes+100)
	out := CapEvidence(big)
	require.LessOrEqual(t, len(out), MaxEvidenceBytes+64, "truncate + ellipsis hint")
	require.Contains(t, out, "truncate")
}

func TestValidateInstruction(t *testing.T) {
	require.Error(t, ValidateInstruction(""))
	require.Error(t, ValidateInstruction("   "))
	require.NoError(t, ValidateInstruction("check it"))
	require.NoError(t, ValidateInstruction(strings.Repeat("x", InstructionMax)))
	require.Error(t, ValidateInstruction(strings.Repeat("x", InstructionMax+1)))
}

func TestValidateCheckinNote(t *testing.T) {
	require.NoError(t, ValidateCheckinNote(""))
	require.NoError(t, ValidateCheckinNote(strings.Repeat("x", CheckinNoteMax)))
	require.Error(t, ValidateCheckinNote(strings.Repeat("x", CheckinNoteMax+1)), "over 500 runes rejected")
}

func TestBurstExceeded(t *testing.T) {
	now := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)
	in := func(offsets ...time.Duration) []time.Time {
		var out []time.Time
		for _, d := range offsets {
			out = append(out, now.Add(d))
		}
		return out
	}
	cases := []struct {
		name  string
		fires []time.Time
		max   int
		want  bool
	}{
		{"no trigger", nil, 3, false},
		{"  2 in window (max=3) not reached", in(-50*time.Second, -30*time.Second), 3, false},
		{"  3 in window (max=3) reaches the fuse threshold", in(-50*time.Second, -30*time.Second, 0), 3, true},
		{"window-edge old trigger outside (==now-window not counted)", in(-time.Minute, -30*time.Second), 3, false},
		{"window-edge new trigger inside the window (==now counted)", in(-30*time.Second, -20*time.Second, 0), 3, true},
		{"future timestamps not counted (defensive)", in(-30*time.Second, -20*time.Second, time.Second), 3, false},
		{"max<=0 falls back to default 10", in(-10*time.Second, -5*time.Second, 0), 0, false},
		{"default 10 reaches the threshold", func() []time.Time {
			var ts []time.Time
			for i := 0; i < 10; i++ {
				ts = append(ts, now.Add(-time.Duration(i)*time.Second))
			}
			return ts
		}(), 0, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, BurstExceeded(tc.fires, now, BurstWindow, tc.max))
		})
	}
}

func TestExtendChain_CapAndPropagation(t *testing.T) {
	require.Equal(t, []string{"a"}, ExtendChain(nil, "a"), "empty parent chain = current id only")
	require.Equal(t, []string{"a", "b", "c"}, ExtendChain([]string{"a", "b"}, "c"))

	p31 := make([]string, 31)
	for i := range p31 {
		p31[i] = fmt.Sprintf("w%d", i)
	}
	out := ExtendChain(p31, "self")
	require.Len(t, out, 32)
	require.Equal(t, "w0", out[0], "no truncation under capacity")
	require.Equal(t, "self", out[len(out)-1], "current id appended at the chain tail (latest causality)")

	p32 := append(append([]string(nil), p31...), "w31")
	out = ExtendChain(p32, "self")
	require.Len(t, out, 32)
	require.Equal(t, "w1", out[0], "oldest entry falls out of window")
	require.Equal(t, "self", out[31])

	src := []string{"a", "b"}
	got := ExtendChain(src, "c")
	got[0] = "mutated"
	require.Equal(t, []string{"a", "b"}, src)
}

func TestChainContains(t *testing.T) {
	require.False(t, ChainContains(nil, "w1"))
	require.False(t, ChainContains([]string{"w2", "w3"}, "w1"))
	require.True(t, ChainContains([]string{"w2", "w1", "w3"}, "w1"))
	require.False(t, ChainContains([]string{"w2"}, ""), "empty id not misjudged as a hit")
}
