package pgrepo

import (
	"strings"
	"testing"

	"orkome/internal/contract/failuretax"
	"orkome/internal/orchestration/domain"
)

func TestResumeToxicitySQLMatchesFailuretax(t *testing.T) {
	const marker = "coalesce(failure_reason,'') NOT IN ("
	i := strings.Index(getLastSessionRefSQL, marker)
	if i < 0 {
		t.Fatal("GetLastSessionRef SQL missing the failure_reason poison predicate (internal archive 05 §2.9 sync break)")
	}
	rest := getLastSessionRefSQL[i+len(marker):]
	list := rest[:strings.Index(rest, ")")]
	for reason := range failuretax.ResumeUnsafe {
		lit := "'" + reason + "'"
		if !strings.Contains(list, lit) {
			t.Errorf("SQL poison word list missing %s (failuretax.ResumeUnsafe out of sync with SQL)", reason)
		}
	}

	for _, tok := range strings.Split(list, ",") {
		reason := strings.Trim(strings.TrimSpace(tok), "'")
		if reason == "" {
			t.Fatalf("SQL poison word list parse error: %q", list)
		}
		if !failuretax.ResumeUnsafe[reason] {
			t.Errorf("SQL poison word list contains a non-ResumeUnsafe item %q (two-sided drift)", reason)
		}
	}
}

func TestResumeEngineTagSQLMirrorsPredicate(t *testing.T) {
	const frag = "($3 = '' OR coalesce(engine_tag,'') IN ('', $3))"
	if !strings.Contains(getLastSessionRefSQL, frag) {
		t.Fatalf("GetLastSessionRef SQL missing the engine fingerprint predicate %q (D3b/F7 sync break)", frag)
	}

	cases := []struct{ produced, current string }{
		{"", ""}, {"", "eino"}, {"eino", "eino"}, {"eino", "other"}, {"eino", ""},
	}
	for _, c := range cases {
		inList := c.current == "" || c.produced == "" || c.produced == c.current
		if inList != domain.EngineTagCompatible(c.produced, c.current) {
			t.Errorf("SQL predicate out of sync with domain.EngineTagCompatible: %v", c)
		}
	}

	if !strings.Contains(getLastSessionRefSQL, "engine_tag") {
		t.Fatal("engine_tag missing on the read side (F7: grep engine_tag should show both read and write)")
	}
}
