package e2e

import "testing"

func TestCoverageReport(t *testing.T) {
	all := allIfaces()
	hasPos := map[string]bool{}
	for _, r := range all {
		if r.WantStatus != 0 {
			hasPos[r.Method+" "+r.Path] = true
		}
	}
	noPos := 0
	for k := range hasPos {
		_ = k
	}
	seen := map[string]bool{}
	for _, r := range all {
		key := r.Method + " " + r.Path
		if seen[key] || hasPos[key] {
			seen[key] = true
			continue
		}
		seen[key] = true
		noPos++
		t.Logf("no positive: %s %s", r.Method, r.Path)
	}
	t.Logf("registered %d (deduped %d), positive recipes %d, no positives %d", len(all), len(seen), len(hasPos), noPos)
}
