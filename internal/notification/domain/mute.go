package domain

type MuteGroups struct {
	Types []string

	WorkItemIDs []string

	SessionIDs []string
}

type MuteTarget struct {
	Type       string
	WorkItemID string
	SessionID  string
}

func Muted(g MuteGroups, t MuteTarget) bool {
	if t.Type != "" {
		for _, typ := range g.Types {
			if typ == "*" || typ == t.Type {
				return true
			}
		}
	}
	if t.WorkItemID != "" {
		for _, id := range g.WorkItemIDs {
			if id == t.WorkItemID {
				return true
			}
		}
	}
	if t.SessionID != "" {
		for _, id := range g.SessionIDs {
			if id == t.SessionID {
				return true
			}
		}
	}
	return false
}
