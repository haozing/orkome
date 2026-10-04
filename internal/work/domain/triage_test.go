package domain

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"orkome/internal/contract/dispatchreason"
)

func triagePtr(s string) *string { return &s }

func TestTriageTriggerBlocked(t *testing.T) {
	agentMention := "[@nova](mention://agent/00000000-0000-4000-8000-0000000000a1)"
	tests := []struct {
		name    string
		facts   TriageGateFacts
		blocked bool
	}{
		{
			name:    "triage+no mention(derived fallback)→denied",
			facts:   TriageGateFacts{TriageState: triagePtr("pending"), AuthorType: "user"},
			blocked: true,
		},
		{
			name: "triage + explicit @agent → allowed",
			facts: TriageGateFacts{
				TriageState: triagePtr("pending"), AuthorType: "user",
				Mentions: ParseMentions(agentMention),
			},
			blocked: false,
		},
		{
			name: "triage+explicit @squad→allowed",
			facts: TriageGateFacts{
				TriageState: triagePtr("pending"), AuthorType: "user",
				Mentions: []Mention{{Type: "squad", ID: "00000000-0000-4000-8000-0000000000b1"}},
			},
			blocked: false,
		},
		{
			name: "triage+@member(human) mention→still denied (not the named executor)",
			facts: TriageGateFacts{
				TriageState: triagePtr("pending"), AuthorType: "user",
				Mentions: []Mention{{Type: "user", ID: "00000000-0000-4000-8000-0000000000c1"}},
			},
			blocked: true,
		},
		{
			name: "triage + human reply agent comment → allowed (a reply names the target)",
			facts: TriageGateFacts{
				TriageState: triagePtr("pending"), AuthorType: "user", ParentAuthorType: "agent",
			},
			blocked: false,
		},
		{
			name: "triage + agent reply agent comment → denied (6a/6b fallback is derived)",
			facts: TriageGateFacts{
				TriageState: triagePtr("pending"), AuthorType: "agent", ParentAuthorType: "agent",
			},
			blocked: true,
		},
		{
			name:    "non-triage → allowed",
			facts:   TriageGateFacts{AuthorType: "user"},
			blocked: false,
		},
		{
			name:    "triage_state empty string → not triaged",
			facts:   TriageGateFacts{TriageState: triagePtr(""), AuthorType: "user"},
			blocked: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.blocked, TriageTriggerBlocked(tt.facts))
		})
	}
}

func TestTriageSuppressionReason(t *testing.T) {
	reason, hasAgent := TriageSuppression("00000000-0000-4000-8000-0000000000a1")
	assert.Equal(t, dispatchreason.WorkItemInTriage, reason)
	assert.True(t, hasAgent)
	_, hasAgent = TriageSuppression("")
	assert.False(t, hasAgent)
}

func TestWriteTriggerProbe(t *testing.T) {
	base := WriteTriggerFacts{
		ChangeKind:   ProbeChangeAssignee,
		AssigneeType: "agent",
		AssigneeID:   "00000000-0000-4000-8000-0000000000a1",
	}
	tests := []struct {
		name   string
		facts  WriteTriggerFacts
		want   bool
		reason string
	}{
		{"non-agent assignment → no trigger face", WriteTriggerFacts{ChangeKind: ProbeChangeAssignee, AssigneeType: "user", AssigneeID: "x"}, false, ProbeNoTarget},
		{"change type not derived (title edit)→no trigger face", WriteTriggerFacts{ChangeKind: "title", AssigneeType: "agent", AssigneeID: "a"}, false, ProbeNoTarget},
		{"passing the full chain → trigger", base, true, ""},
		{"visibility gate → invocation_not_allowed", func() WriteTriggerFacts { f := base; f.InvocationBlocked = true; return f }(), false, dispatchreason.InvocationNotAllowed},
		{"self-trigger loop (agent creator + no new human input)→self_trigger_suppressed", func() WriteTriggerFacts {
			f := base
			f.CreatorType, f.CreatorID, f.NoNewHumanInput = "agent", f.AssigneeID, true
			return f
		}(), false, dispatchreason.SelfTriggerSuppressed},
		{"pending exists→already_active", func() WriteTriggerFacts { f := base; f.HasPendingExecutor = true; return f }(), false, dispatchreason.AlreadyActive},
		{"triage + derived executor → work_item_in_triage", func() WriteTriggerFacts { f := base; f.TriageState = triagePtr("pending"); return f }(), false, dispatchreason.WorkItemInTriage},
		{"archive→agent_archived (readiness gate last)", func() WriteTriggerFacts { f := base; f.ExecutorArchived = true; return f }(), false, dispatchreason.AgentArchived},
		{"triage+archive→triage gate precedes readiness gate", func() WriteTriggerFacts {
			f := base
			f.TriageState, f.ExecutorArchived = triagePtr("pending"), true
			return f
		}(), false, dispatchreason.WorkItemInTriage},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := WriteTriggerProbe(tt.facts)
			assert.Equal(t, tt.want, got.Trigger)
			assert.Equal(t, tt.reason, got.ReasonCode)
		})
	}
}

func TestTriageUpdateLocked(t *testing.T) {
	lock := TriagePatchLock{AssigneeChanged: true}
	assert.Equal(t, "assignee", lock.Field())
	assert.True(t, TriageUpdateLocked(true, lock, false), "triage + change assignee → denied")
	assert.False(t, TriageUpdateLocked(true, lock, true), "same patch unlock(accept)→allowed")
	assert.False(t, TriageUpdateLocked(false, lock, false), "non-triage → allowed")
	assert.False(t, TriageUpdateLocked(true, TriagePatchLock{}, false), "triage + no locked field → allowed")

	assert.Equal(t, "status", TriagePatchLock{StatusChanged: true}.Field())
	assert.Equal(t, "parent_work_item_id", TriagePatchLock{ParentChanged: true}.Field())
	assert.Equal(t, "", TriagePatchLock{}.Field())
	assert.True(t, PatchClearsTriage(triagePtr("")))
	assert.False(t, PatchClearsTriage(triagePtr("pending")))
	assert.False(t, PatchClearsTriage(nil))
}
