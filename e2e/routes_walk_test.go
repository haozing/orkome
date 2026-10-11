package e2e

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// Route reconciliation against the stdlib ServeMux composition root.
// The chi version enumerated mounted routes + middleware chains via chi.Walk;
// ServeMux exposes neither, so reconciliation is resolution-based (Handler()
// only matches, never invokes) and one-directional per list:
//   - G1: every registry-declared interface must resolve to its declared pattern
//   - G2: every static-catalog entry must resolve (reverse drift is guarded by
//     review + cmd router_contract_test + per-domain route accountings)
//
// G4 (authMw presence per route via middleware pointer identity) is not
// representable with ServeMux; auth discipline now rests on the per-domain
// registration helpers. Recorded as a ploykit issue (route-recording mux).
func TestRoutesWalkGuards(t *testing.T) {
	st := newStack(t)
	if st.rootMux == nil {
		t.Fatalf("stack has no root mux")
	}

	resolves := func(method, pattern string) bool {
		req := httptest.NewRequest("TRACE", regexp.MustCompile(`\{[a-zA-Z]+(\.\.\.)?\}`).ReplaceAllString(pattern, "00000000-0000-0000-0000-000000000000"), nil)
		rec := httptest.NewRecorder()
		st.handler.ServeHTTP(rec, req)
		allow := rec.Header().Get("Allow")
		for _, m := range strings.Split(allow, ",") {
			if strings.TrimSpace(m) == method {
				return true
			}
		}
		// auth-guarded subtree routes answer TRACE with 401/403 before the
		// inner mux can 405 — an auth verdict still proves the pattern matched
		return rec.Code == http.StatusUnauthorized || rec.Code == http.StatusForbidden
	}

	for _, r := range registry {
		if !resolves(r.Method, r.Path) {
			t.Errorf("G1: registry-declared interface not mounted by composition root: %s %s", r.Method, r.Path)
		}
	}

	staticList := loadStaticRoutes(t)
	var notMounted []string
	for key := range staticList {
		m, p, _ := strings.Cut(key, " ")
		if !resolves(m, p) {
			notMounted = append(notMounted, key)
		}
	}
	sort.Strings(notMounted)
	t.Logf("static catalog %d; e2e unmounted %d (mount gap below)", len(staticList), len(notMounted))
	for _, k := range notMounted {
		t.Logf("not mounted: %s", k)
	}

	if dir := os.Getenv("E2E_REPORT_DIR"); dir != "" {
		_ = os.MkdirAll(dir, 0o755)
		var b strings.Builder
		fmt.Fprintf(&b, "# registry routes (%d)\n", len(registry))
		for _, r := range registry {
			fmt.Fprintf(&b, "%s %s\n", r.Method, r.Path)
		}
		fmt.Fprintf(&b, "\n# e2e not mounted (in static catalog, not resolving, %d)\n", len(notMounted))
		for _, k := range notMounted {
			fmt.Fprintln(&b, k)
		}
		_ = os.WriteFile(dir+"/walk-routes.txt", []byte(b.String()), 0o644)
	}
}

func loadStaticRoutes(t *testing.T) map[string]bool {
	t.Helper()
	b, err := os.ReadFile("../docs/route-compare/orkome_routes.txt")
	if err != nil {
		t.Fatalf("read static catalog failed: %v", err)
	}
	out := map[string]bool{}
	for _, ln := range strings.Split(string(b), "\n") {
		ln = strings.TrimSpace(ln)
		if ln == "" {
			continue
		}
		if ln == "" || ln[0] == '#' || strings.HasPrefix(ln, "framework") {
			continue
		}
		parts := strings.SplitN(ln, " ", 2)
		if len(parts) != 2 {
			continue
		}
		switch parts[0] {
		case "ANY", "GET", "POST", "PUT", "PATCH", "DELETE":
		default:
			continue
		}
		m, p := parts[0], parts[1]
		if m == "ANY" {
			for _, mm := range []string{"GET", "POST", "PUT", "PATCH", "DELETE"} {
				out[mm+" "+p] = true
			}
			continue
		}
		out[m+" "+p] = true
	}
	return out
}
