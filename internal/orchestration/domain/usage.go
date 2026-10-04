package domain

import "time"

func ClosedHourBoundary(now time.Time) time.Time {
	return now.Truncate(time.Hour)
}
