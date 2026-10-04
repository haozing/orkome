package domain

import (
	"regexp"
	"strings"

	"orkome/internal/contract/dispatchreason"
)

var mentionRe = regexp.MustCompile(`\[@([^\]]+)\]\(mention://(user|agent|squad|work_item|all)/([0-9a-fA-F-]{0,36})\)`)

type Mention struct {
	Type string
	ID   string
}

func ParseMentions(content string) []Mention {
	var out []Mention
	for _, m := range mentionRe.FindAllStringSubmatch(content, -1) {
		out = append(out, Mention{Type: m[2], ID: strings.ToLower(m[3])})
	}
	return out
}

type AgentFacts struct {
	ID       string
	Name     string
	Archived bool

	LeaderOfSquads []string

	MemberOfSquads []string
}

type SquadFacts struct {
	ID        string
	LeaderID  string
	MemberIDs []string
}

type CommentFacts struct {
	WorkspaceID string
	WorkItemID  string

	AuthorType string
	AuthorID   string

	ParentAuthorType string
	ParentAuthorID   string

	RootAuthorType string
	RootAuthorID   string

	AssigneeType string
	AssigneeID   string

	HistoryAgentIDs []string

	Content string
}

type Trigger struct {
	Kind    string
	AgentID string
	Reason  string
}

const TriggerKindAgent = "agent"

const TriggerDeferredFallback = "deferred_fallback"

const (
	EvidenceAssignment    = "assignment"
	EvidenceChat          = "chat"
	EvidenceSquad         = "squad"
	EvidenceMemberComment = "member_comment"
	EvidenceRerun         = "rerun"
)

type Suppression struct {
	AgentID string
	Reason  string
}

type Decision struct {
	Triggers     []Trigger
	Suppressions []Suppression
}

func Eligibility(a AgentFacts, invocable bool) (ok bool, reason string) {
	if !invocable {
		return false, dispatchreason.InvocationNotAllowed
	}
	if a.Archived {
		return false, dispatchreason.TargetUnavailable
	}
	return true, ""
}

func DecisionTree(f CommentFacts, agents []AgentFacts, squads []SquadFacts, invocable func(AgentFacts) bool) Decision {

	if strings.HasPrefix(strings.TrimSpace(f.Content), "/note") {
		return Decision{}
	}

	mentions := ParseMentions(f.Content)
	hasAll := false
	var mentionedAgents, mentionedSquads []string
	for _, m := range mentions {
		switch m.Type {
		case "all":
			hasAll = true
		case "agent":
			mentionedAgents = append(mentionedAgents, m.ID)
		case "squad":
			mentionedSquads = append(mentionedSquads, m.ID)
		}
	}

	if len(mentionedAgents)+len(mentionedSquads) > 0 {
		var d Decision
		byID := map[string]AgentFacts{}
		for _, a := range agents {
			byID[a.ID] = a
		}
		emit := func(id string, reason string) {
			a, ok := byID[id]
			if !ok {
				return
			}
			if ok, why := Eligibility(a, invocable(a)); !ok {
				d.Suppressions = append(d.Suppressions, Suppression{AgentID: id, Reason: why})
				return
			}
			d.Triggers = append(d.Triggers, Trigger{Kind: TriggerKindAgent, AgentID: id, Reason: reason})
		}
		for _, id := range mentionedAgents {
			emit(id, "3.explicit_mention")
		}
		for _, sid := range mentionedSquads {
			for _, sq := range squads {
				if sq.ID == sid {
					emit(sq.LeaderID, "3.explicit_squad_mention")
				}
			}
		}
		return d
	}

	if hasAll {
		return Decision{}
	}

	userMention := false
	for _, m := range mentions {
		if m.Type == "user" {
			userMention = true
		}
	}
	if userMention {
		return Decision{}
	}

	switch f.AuthorType {
	case "agent":

		if f.AuthorID != "" {
			for _, a := range agents {
				if a.ID == f.AuthorID {
					for _, sid := range a.MemberOfSquads {
						for _, sq := range squads {
							if sq.ID == sid && sq.LeaderID != f.AuthorID {
								return emitOne(agents, sq.LeaderID, "6a.agent_author_squad_leader", invocable)
							}
						}
					}
				}
			}
		}

		if f.AssigneeType == "squad" && f.AssigneeID != "" {
			for _, sq := range squads {
				if sq.ID == f.AssigneeID {
					return emitOne(agents, sq.LeaderID, "6b.work_item_squad_leader", invocable)
				}
			}
		}

		return Decision{}
	case "system":
		return Decision{}
	}

	if f.ParentAuthorType == "agent" && f.ParentAuthorID != "" {
		return emitOne(agents, f.ParentAuthorID, "7a.reply_to_agent", invocable)
	}

	if f.ParentAuthorType != "" {
		if f.RootAuthorType == "agent" && f.RootAuthorID != "" {

			return Decision{}
		}

		var d Decision
		for _, id := range f.HistoryAgentIDs {
			d = appendTrigger(d, agents, id, "7b.thread_root_owner", invocable)
		}
		return d
	}

	switch f.AssigneeType {
	case "agent":
		return emitOne(agents, f.AssigneeID, "7c.work_item_assignee", invocable)
	case "squad":
		for _, sq := range squads {
			if sq.ID == f.AssigneeID {
				return emitOne(agents, sq.LeaderID, "7c.work_item_squad_leader", invocable)
			}
		}
	}
	return Decision{}
}

func emitOne(agents []AgentFacts, id, reason string, invocable func(AgentFacts) bool) Decision {
	var d Decision
	return appendTrigger(d, agents, id, reason, invocable)
}

func appendTrigger(d Decision, agents []AgentFacts, id, reason string, invocable func(AgentFacts) bool) Decision {
	for _, a := range agents {
		if a.ID != id {
			continue
		}
		if ok, why := Eligibility(a, invocable(a)); !ok {
			d.Suppressions = append(d.Suppressions, Suppression{AgentID: id, Reason: why})
			return d
		}
		d.Triggers = append(d.Triggers, Trigger{Kind: TriggerKindAgent, AgentID: id, Reason: reason})
		return d
	}
	return d
}

type MissType int

const (
	MissNone MissType = iota

	MissActive

	MissNew
)

func ClassifyMiss(hasPending, hasActive bool) MissType {
	switch {
	case hasPending:
		return MissNone
	case hasActive:
		return MissActive
	default:
		return MissNew
	}
}

const MaxProtocolRounds = 4
