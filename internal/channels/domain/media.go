package domain

import "time"

const (
	MediaPending    = "pending"
	MediaDeleting   = "deleting"
	MediaTombstoned = "tombstoned"
)

const (
	ReconcileInterval = time.Minute

	MediaSettleDelay = 15 * time.Minute

	MediaReconcileLease = 2 * time.Minute

	mediaBackoffBase = time.Minute
	mediaBackoffCap  = time.Hour
)

var tombstonePassDelays = []time.Duration{
	15 * time.Minute,
	time.Hour,
	6 * time.Hour,
	24 * time.Hour,
}

func MediaAbandoned(createdAt, now time.Time) bool {
	return now.Sub(createdAt) >= MediaSettleDelay
}

func MediaRetryDelay(attempt int) time.Duration {
	d := mediaBackoffBase
	for i := 1; i < attempt && d < mediaBackoffCap; i++ {
		d *= 2
	}
	if d > mediaBackoffCap {
		d = mediaBackoffCap
	}
	return d
}

func TombstoneNextDelay(pass int) time.Duration {
	if pass < 0 {
		pass = 0
	}
	if pass >= len(tombstonePassDelays) {
		pass = len(tombstonePassDelays) - 1
	}
	return tombstonePassDelays[pass]
}

func MediaIntentRefBindEligible(state string) bool { return state == MediaPending }
