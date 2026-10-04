package domain

const (
	SquadMemberRunning = "running"
	SquadMemberQueued  = "queued"
	SquadMemberIdle    = "idle"
)

func SquadMemberStatus(taskStatuses []string) string {
	queued := false
	for _, st := range taskStatuses {
		switch st {
		case "dispatched", "running":
			return SquadMemberRunning
		case "queued", "deferred":
			queued = true
		}
	}
	if queued {
		return SquadMemberQueued
	}
	return SquadMemberIdle
}
