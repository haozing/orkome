package domain

import "orkome/internal/contract/dispatchreason"

type TriageGateFacts struct {
	TriageState *string

	Mentions []Mention

	AuthorType string

	ParentAuthorType string
}

func NamedExecutorPresent(f TriageGateFacts) bool {
	for _, m := range f.Mentions {
		if m.Type == "agent" || m.Type == "squad" {
			return true
		}
	}
	return f.AuthorType == "user" && f.ParentAuthorType == "agent"
}

func InTriage(triageState *string) bool {
	return triageState != nil && *triageState != ""
}

func TriageTriggerBlocked(f TriageGateFacts) bool {
	return InTriage(f.TriageState) && !NamedExecutorPresent(f)
}

func TriageSuppression(agentID string) (string, bool) {
	return dispatchreason.WorkItemInTriage, agentID != ""
}

type WriteTriggerFacts struct {
	ChangeKind string

	AssigneeType string
	AssigneeID   string

	CreatorType string
	CreatorID   string

	TriageState *string

	InvocationBlocked bool

	NoNewHumanInput bool

	HasPendingExecutor bool

	ExecutorArchived bool
}

type ProbeDecision struct {
	Trigger    bool
	ReasonCode string
}

const (
	ProbeChangeCreate       = "create"
	ProbeChangeAssignee     = "assignee"
	ProbeChangeStatusActive = "status_promote"
)

const ProbeNoTarget = "no_trigger_target"

func WriteTriggerProbe(f WriteTriggerFacts) ProbeDecision {

	if f.AssigneeType != "agent" || f.AssigneeID == "" {
		return ProbeDecision{ReasonCode: ProbeNoTarget}
	}
	switch f.ChangeKind {
	case ProbeChangeCreate, ProbeChangeAssignee, ProbeChangeStatusActive:
	default:
		return ProbeDecision{ReasonCode: ProbeNoTarget}
	}
	if f.InvocationBlocked {
		return ProbeDecision{ReasonCode: dispatchreason.InvocationNotAllowed}
	}
	if f.CreatorType == "agent" && f.CreatorID == f.AssigneeID && f.NoNewHumanInput {
		return ProbeDecision{ReasonCode: dispatchreason.SelfTriggerSuppressed}
	}
	if f.HasPendingExecutor {
		return ProbeDecision{ReasonCode: dispatchreason.AlreadyActive}
	}

	if InTriage(f.TriageState) {
		return ProbeDecision{ReasonCode: dispatchreason.WorkItemInTriage}
	}
	if f.ExecutorArchived {
		return ProbeDecision{ReasonCode: dispatchreason.AgentArchived}
	}
	return ProbeDecision{Trigger: true}
}

type TriagePatchLock struct {
	AssigneeChanged bool
	StatusChanged   bool
	ParentChanged   bool
}

func (l TriagePatchLock) Field() string {
	switch {
	case l.AssigneeChanged:
		return "assignee"
	case l.StatusChanged:
		return "status"
	case l.ParentChanged:
		return "parent_work_item_id"
	default:
		return ""
	}
}

func (l TriagePatchLock) Locked() bool { return l.Field() != "" }

func PatchClearsTriage(triageState *string) bool {
	return triageState != nil && *triageState == ""
}

func TriageUpdateLocked(inTriage bool, lock TriagePatchLock, clearsTriage bool) bool {
	return inTriage && lock.Locked() && !clearsTriage
}
