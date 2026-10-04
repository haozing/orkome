package cronx

import (
	"fmt"
	"time"

	"github.com/robfig/cron/v3"
)

func parser() cron.Parser {
	return cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)
}

func Next(expr, tz string, after time.Time) (time.Time, error) {
	loc := time.UTC
	if tz != "" {
		l, err := time.LoadLocation(tz)
		if err != nil {
			return time.Time{}, fmt.Errorf("bad timezone %q: %w", tz, err)
		}
		loc = l
	}
	sched, err := parser().Parse(expr)
	if err != nil {
		return time.Time{}, fmt.Errorf("bad cron %q: %w", expr, err)
	}
	return sched.Next(after.In(loc)), nil
}
