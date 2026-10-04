package e2e

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

func TestGuardTruncateCoversAllTables(t *testing.T) {
	migTables := migrateCreatedTables(t)
	truncTables := goldenTruncateTables(t)

	var missing []string
	for _, m := range migTables {
		if !truncTables[m] {
			missing = append(missing, m)
		}
	}
	if len(missing) > 0 {
		t.Errorf("%d tables exist in migrations but are not in the TRUNCATE list (cross-test dirty-data risk); add them to golden_test.go's TRUNCATE statement:\n  %s",
			len(missing), strings.Join(missing, "\n  "))
	}
	t.Logf("migrations tables %d, TRUNCATE covers %d", len(migTables), len(truncTables))
}

var createTableRe = regexp.MustCompile(`(?i)CREATE\s+TABLE\s+(?:IF\s+NOT\s+EXISTS\s+)?("?[a-z_]+"?)`)

func migrateCreatedTables(t *testing.T) []string {
	t.Helper()
	ents, err := os.ReadDir("../migrations")
	if err != nil {
		t.Fatalf("read migrations directory failed: %v", err)
	}
	seen := map[string]bool{}
	var renames [][2]string
	for _, e := range ents {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".up.sql") {
			continue
		}
		b, err := os.ReadFile("../migrations/" + e.Name())
		if err != nil {
			t.Fatalf("read %s failed: %v", e.Name(), err)
		}
		for _, m := range createTableRe.FindAllString(string(b), -1) {
			name := strings.TrimSpace(createTableRe.FindStringSubmatch(m)[1])
			name = strings.Trim(name, `"`)
			seen[name] = true
		}

		for _, m := range renameTableRe.FindAllStringSubmatch(string(b), -1) {
			renames = append(renames, [2]string{
				strings.Trim(strings.TrimSpace(m[1]), `"`),
				strings.Trim(strings.TrimSpace(m[3]), `"`),
			})
		}
	}
	for _, r := range renames {
		if seen[r[0]] {
			delete(seen, r[0])
			seen[r[1]] = true
		}
	}
	out := make([]string, 0, len(seen))
	for k := range seen {
		out = append(out, k)
	}
	return out
}

var renameTableRe = regexp.MustCompile(`(?i)ALTER\s+TABLE\s+("?([a-z_]+)"?)\s+RENAME\s+TO\s+("?([a-z_]+)"?)`)

var truncateRe = regexp.MustCompile(`(?s)TRUNCATE\s+(.+?)\s+RESTART\s+IDENTITY`)

func goldenTruncateTables(t *testing.T) map[string]bool {
	t.Helper()
	b, err := os.ReadFile("golden_test.go")
	if err != nil {
		t.Fatalf("read golden_test.go failed: %v", err)
	}
	m := truncateRe.FindStringSubmatch(string(b))
	if m == nil {
		t.Fatal("golden_test.go did not find the TRUNCATE ... RESTART IDENTITY statement")
	}
	out := map[string]bool{}
	for _, tok := range strings.Split(m[1], ",") {
		name := strings.TrimSpace(tok)
		name = strings.Trim(name, `"`)
		if name != "" {
			out[name] = true
		}
	}
	return out
}
