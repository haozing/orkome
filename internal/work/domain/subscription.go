package domain

import "fmt"

const (
	SubSourceCreator           = "creator"
	SubSourceAssignee          = "assignee"
	SubSourceCommenter         = "commenter"
	SubSourceMentionee         = "mentionee"
	SubSourceAutopilotTemplate = "autopilot_template"
	SubSourceDelegation        = "delegation"
)

const SubSourceManual = "manual"

const (
	NotifyLevelAll       = "all"
	NotifyLevelDelegated = "delegated"
)

func SubscriberLevel(source string) string {
	if source == SubSourceDelegation {
		return NotifyLevelDelegated
	}
	return NotifyLevelAll
}

var externalSubSources = map[string]bool{SubSourceAutopilotTemplate: true}

func ValidateSubscriberSpec(source, userID string) (string, string, error) {
	if !externalSubSources[source] {
		return "", "", fmt.Errorf("subscriber source %q not carryable at creation", source)
	}
	uid, ok := ParseUUID(userID)
	if !ok {
		return "", "", fmt.Errorf("subscriber user id %q must be a canonical uuid", userID)
	}
	return uid, source, nil
}

type DelegationChain struct {
	OriginType           string
	ChainRootUserID      string
	ChainRootDirectHuman bool
}

func DelegationChainSubscriber(c DelegationChain) (string, bool) {
	if c.OriginType != "agent_create" || !c.ChainRootDirectHuman {
		return "", false
	}
	root, ok := ParseUUID(c.ChainRootUserID)
	if !ok {
		return "", false
	}
	return root, true
}
