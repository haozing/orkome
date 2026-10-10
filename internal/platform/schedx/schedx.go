package schedx

import (
	"fmt"
	"time"

	"github.com/haozing/ploykit/platform/cronx"
)

const LateForgive = 5 * time.Minute

const maxPlans = 10000

type Schedule struct {
	Expr string
	TZ   string
}

func PlansForScope(s Schedule, anchor, now time.Time) ([]time.Time, error) {
	if !now.After(anchor) {
		return nil, nil
	}
	var plans []time.Time
	cur := anchor
	for len(plans) < maxPlans {
		t, err := cronx.Next(s.Expr, s.TZ, cur)
		if err != nil {
			return nil, fmt.Errorf("schedx: %w", err)
		}
		if t.After(now) {
			break
		}
		plans = append(plans, t)
		cur = t
	}
	if len(plans) == maxPlans {
		return nil, fmt.Errorf("schedx: scan window too large (>=%d plans between %s and %s)", maxPlans, anchor, now)
	}
	return plans, nil
}

func LatestDue(s Schedule, anchor, now time.Time) (time.Time, bool, error) {
	plans, err := PlansForScope(s, anchor, now)
	if err != nil {
		return time.Time{}, false, err
	}
	if len(plans) == 0 {
		return time.Time{}, false, nil
	}
	latest := plans[len(plans)-1]
	if now.Sub(latest) > LateForgive {
		return time.Time{}, false, nil
	}
	return latest, true, nil
}
