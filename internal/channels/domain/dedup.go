package domain

const (
	DedupSeen      = "seen"
	DedupClaimed   = "claimed"
	DedupProcessed = "processed"
)

func DedupCanClaim(state string) bool { return state == DedupSeen }

func DedupIsDuplicate(state string) bool {
	return state == DedupClaimed || state == DedupProcessed
}
