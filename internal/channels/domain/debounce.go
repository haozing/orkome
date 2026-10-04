package domain

import "time"

const DebounceWindow = 3 * time.Second

type DebounceKey struct {
	SessionID       string
	ContextRevision int64
}

func ShouldDebounce(lastInboundAt, now time.Time) bool {
	if lastInboundAt.IsZero() {
		return false
	}
	return now.Sub(lastInboundAt) < DebounceWindow
}
