package e2e

import (
	"fmt"
	"net/http"
	"os"
	"sort"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
)

func TestRoutesWalkGuards(t *testing.T) {
	st := newStack(t)

	type walked struct {
		method, path string
		mws          []func(http.Handler) http.Handler
	}
	got := map[string]*walked{}
	err := chi.Walk(st.handler, func(method, route string, h http.Handler, mws ...func(http.Handler) http.Handler) error {

		key := strings.TrimSuffix(method+" "+route, "/")
		if key == "" {
			key = method + " /"
		}
		if _, dup := got[key]; dup {
			return nil
		}
		got[key] = &walked{method, route, mws}
		return nil
	})
	if err != nil {
		t.Fatalf("Walk failed: %v", err)
	}

	authPtr := fmt.Sprintf("%p", st.authMw)
	hasAuth := func(w *walked) bool {
		for _, mw := range w.mws {
			if fmt.Sprintf("%p", mw) == authPtr {
				return true
			}
		}
		return false
	}

	for _, r := range registry {
		w, ok := got[strings.TrimSuffix(r.Method+" "+r.Path, "/")]
		if !ok {
			t.Errorf("G1: registry-declared interface not mounted by composition root: %s %s", r.Method, r.Path)
			continue
		}

		if r.Auth == authSelf {
			continue
		}
		authed := hasAuth(w)
		switch r.Auth {
		case authPublic:
			if authed {
				t.Errorf("G4: %s %s marked public but authMw in the chain", r.Method, r.Path)
			}
		default:
			if !authed {
				t.Errorf("G4: %s %s marked %s but authMw not in the chain", r.Method, r.Path, r.Auth)
			}
		}
	}

	staticList := loadStaticRoutes(t)
	var notInStatic []string
	for key := range got {
		if !staticList[key] {
			notInStatic = append(notInStatic, key)
		}
	}
	sort.Strings(notInStatic)
	for _, k := range notInStatic {
		t.Errorf("G2: e2e composition root mounted routes outside the static catalog: %s", k)
	}
	var notMounted []string
	for key := range staticList {
		if _, ok := got[key]; !ok {
			notMounted = append(notMounted, key)
		}
	}
	sort.Strings(notMounted)
	t.Logf("Walk found %d routes; static catalog %d; e2e unmounted %d (mount gap below)", len(got), len(staticList), len(notMounted))
	for _, k := range notMounted {
		t.Logf("not mounted: %s", k)
	}

	if dir := os.Getenv("E2E_REPORT_DIR"); dir != "" {
		_ = os.MkdirAll(dir, 0o755)
		var b strings.Builder
		fmt.Fprintf(&b, "# Walk measured routes (%d, e2e composition root scope)\n", len(got))
		keys := make([]string, 0, len(got))
		for k := range got {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			fmt.Fprintln(&b, k)
		}
		fmt.Fprintf(&b, "\n# e2e not mounted (in static catalog, not in composition root, %d)\n", len(notMounted))
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
		parts := strings.SplitN(ln, " ", 2)
		if len(parts) != 2 {
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
