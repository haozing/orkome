package domain

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

const (
	SystemRuleChildrenDone = "children_done"

	CheckinNoteMax = 500

	InstructionMax = 4000

	MaxEvidenceBytes = 40 * 1024

	ReceiptRetryWindow = 10 * time.Minute

	PausedReasonBurst = "burst"

	BurstWindow = time.Minute

	BurstMaxFires = 10

	BurstQuietPeriod = 10 * time.Minute

	PausedReasonLoop = "loop"

	WakeupChainMax = 32
)

func ChainContains(chain []string, wakeupID string) bool {
	for _, id := range chain {
		if id == wakeupID {
			return true
		}
	}
	return false
}

func ExtendChain(chain []string, wakeupID string) []string {
	if len(chain) >= WakeupChainMax {
		chain = chain[len(chain)-WakeupChainMax+1:]
	}
	out := make([]string, 0, len(chain)+1)
	out = append(out, chain...)
	return append(out, wakeupID)
}

func BurstExceeded(fires []time.Time, now time.Time, window time.Duration, max int) bool {
	if max <= 0 {
		max = BurstMaxFires
	}
	lo := now.Add(-window)
	n := 0
	for _, t := range fires {
		if t.After(lo) && !t.After(now) {
			n++
		}
	}
	return n >= max
}

type EventFacts struct {
	EventType string
	ActorType string
	ActorID   string
	AgentID   string
	TaskID    string
}

type EventFilter struct {
	EventTypes []string `json:"event_types"`
	AgentID    string   `json:"filter_agent_id,omitempty"`
	TaskID     string   `json:"filter_task_id,omitempty"`
	ActorType  string   `json:"filter_actor_type,omitempty"`
	ActorID    string   `json:"filter_actor_id,omitempty"`
}

func (f EventFilter) Matches(ev EventFacts) bool {
	if len(f.EventTypes) == 0 {
		return false
	}
	hit := false
	for _, t := range f.EventTypes {
		if t == ev.EventType {
			hit = true
			break
		}
	}
	if !hit {
		return false
	}
	if f.AgentID != "" && f.AgentID != ev.AgentID {
		return false
	}
	if f.TaskID != "" && f.TaskID != ev.TaskID {
		return false
	}
	if f.ActorType != "" && f.ActorType != ev.ActorType {
		return false
	}
	if f.ActorID != "" && f.ActorID != ev.ActorID {
		return false
	}
	return true
}

func ExtractEventFacts(eventType string, payload map[string]any) EventFacts {
	f := EventFacts{EventType: eventType}
	if payload == nil {
		return f
	}
	str := func(k string) string {
		if v, ok := payload[k].(string); ok {
			return v
		}
		return ""
	}

	f.ActorType = str("author_type")
	if f.ActorType == "" {
		f.ActorType = str("creator_type")
	}
	f.ActorID = str("author_id")
	if f.ActorID == "" {
		f.ActorID = str("creator_id")
	}

	f.TaskID = str("id")
	f.AgentID = str("agent_id")

	if f.AgentID == "" && str("assignee_type") == "agent" {
		f.AgentID = str("assignee_id")
	}
	return f
}

type ChildStageFact struct {
	Stage    string
	Terminal bool
}

func ChildrenDone(children []ChildStageFact) (bool, string) {
	if len(children) == 0 {
		return false, ""
	}
	stages := map[string]*stageCount{}
	var order []string
	unstaged := &stageCount{}
	closed := 0
	for _, ch := range children {
		sc := unstaged
		if ch.Stage != "" {
			if stages[ch.Stage] == nil {
				stages[ch.Stage] = &stageCount{Stage: ch.Stage}
				order = append(order, ch.Stage)
			}
			sc = stages[ch.Stage]
		}
		sc.Total++
		if ch.Terminal {
			sc.Closed++
			closed++
		}
	}
	if closed == len(children) {
		return true, "all"
	}
	sort.Strings(order)
	var closedStage string
	frontier, waiting := false, false
	for _, st := range order {
		sc := stages[st]
		if waiting {
			break
		}
		if sc.Closed < sc.Total {
			waiting = true
			continue
		}
		closedStage, frontier = st, true
	}
	if frontier && waiting {
		return true, "stage:" + closedStage
	}
	return false, ""
}

type stageCount struct {
	Stage         string
	Total, Closed int
}

func CondFingerprint(met bool) string {
	if met {
		return "met"
	}
	return ""
}

func EdgeTriggered(prev *string, cur string) bool {
	return cur != "" && (prev == nil || *prev != cur)
}

func CapEvidence(s string) string {
	if len(s) <= MaxEvidenceBytes {
		return s
	}
	return s[:MaxEvidenceBytes] + "\n…(evidence truncated at the 40KB cap)"
}

func ValidateInstruction(s string) error {
	if strings.TrimSpace(s) == "" {
		return fmt.Errorf("instruction required")
	}
	if n := len([]rune(s)); n > InstructionMax {
		return fmt.Errorf("instruction must be <= %d runes, got %d", InstructionMax, n)
	}
	return nil
}

func ValidateCheckinNote(s string) error {
	if n := len([]rune(s)); n > CheckinNoteMax {
		return fmt.Errorf("checkin note must be <= %d runes, got %d", CheckinNoteMax, n)
	}
	return nil
}
