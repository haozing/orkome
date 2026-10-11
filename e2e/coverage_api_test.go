package e2e

import (
	"net/http"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/haozing/ploykit/platform/ids"
)

var pathParamRe = regexp.MustCompile(`\{[a-zA-Z]+\}`)

var posWorld *world

func TestL1SixCaseMatrix(t *testing.T) {
	w := newWorld(t)
	w.loginAs("owner", "owner@l1.e2e")
	w.vars["wsID"] = w.createWSAs("owner", "l1six")
	posWorld = w
	w.loginAs("stranger", "stranger@l1.e2e")
	w.bootstrapCSRF("anon")

	for _, r := range allIfaces() {
		r := r
		t.Run(r.Domain+": "+r.Method+" "+r.Path, func(t *testing.T) {
			negPath := pathParamRe.ReplaceAllString(r.Path, "00000000-0000-0000-0000-000000000000")
			var body any
			if r.Body != nil {
				body = r.Body()
			}
			isWrite := r.Method == http.MethodPost || r.Method == http.MethodPut || r.Method == http.MethodPatch

			if r.Auth != authPublic {
				want := http.StatusUnauthorized
				if r.AnonExpect != 0 {
					want = r.AnonExpect
				}
				rec := w.st.do("anon", r.Method, negPath, body, nil)
				require.Equal(t, want, rec.Code,
					"unauthenticated should be %d, got %d: %s", want, rec.Code, rec.Body.String())
			}

			if r.Auth == authMember {
				rec := w.st.do("stranger", r.Method, negPath, body, nil)
				require.GreaterOrEqual(t, rec.Code, http.StatusBadRequest,
					"non-members should be rejected, got %d: %s", rec.Code, rec.Body.String())
				require.Less(t, rec.Code, http.StatusInternalServerError, "non-server error")
			}

			if r.Body != nil && isWrite && !r.SkipBadBody {
				okPath := negPath
				if r.Setup != nil {
					okPath = r.Setup(w)
				} else if !strings.Contains(r.Path, "{") {
					okPath = r.Path
				}
				rec := w.st.do("owner", r.Method, okPath, map[string]any{}, nil)
				require.GreaterOrEqual(t, rec.Code, http.StatusBadRequest,
					"missing required fields should be 4xx, got %d: %s", rec.Code, rec.Body.String())
				require.Less(t, rec.Code, http.StatusInternalServerError)
			}

			if strings.Contains(r.Path, "{") {
				if r.ListLike {
					rec := w.st.do("owner", r.Method, negPath, body, nil)
					require.Equal(t, http.StatusOK, rec.Code,
						"list type should be a 200 empty set, got %d: %s", rec.Code, rec.Body.String())
				} else if r.Method == http.MethodDelete {
					rec := w.st.do("owner", r.Method, negPath, body, nil)
					want := []int{http.StatusOK, http.StatusNoContent, http.StatusBadRequest, http.StatusNotFound}
					if r.MissStatus != 0 {
						want = []int{r.MissStatus}
					}
					require.Contains(t, want, rec.Code,
						"DELETE idempotent semantics anomaly for missing resource, got %d: %s", rec.Code, rec.Body.String())
				} else if !(isWrite && r.Body == nil) {
					want := http.StatusNotFound
					if r.MissStatus != 0 {
						want = r.MissStatus
					}
					rec := w.st.do("owner", r.Method, negPath, body, nil)
					require.Equal(t, want, rec.Code,
						"missing resource should be %d, got %d: %s", want, rec.Code, rec.Body.String())
				}
			}

			_ = ids.NewV4
		})
	}
}

func TestL1Positive(t *testing.T) {
	byDomain := map[string][]iface{}
	var order []string
	for _, r := range allIfaces() {
		if r.WantStatus == 0 {
			continue
		}
		if _, ok := byDomain[r.Domain]; !ok {
			order = append(order, r.Domain)
		}
		byDomain[r.Domain] = append(byDomain[r.Domain], r)
	}
	for _, dom := range order {
		dom := dom
		t.Run(dom, func(t *testing.T) {
			w := newWorld(t)
			posWorld = w
			w.loginAs("owner", "owner@"+dom+".e2e")
			w.vars["wsID"] = w.createWSAs("owner", "pos-"+dom)
			for _, r := range byDomain[dom] {
				var body any
				if r.Body != nil {
					body = r.Body()
				}
				path := r.Path
				if r.Setup != nil {
					path = r.Setup(w)
				}
				client := r.Client
				if client == "" {
					client = "owner"
				}
				rec := w.st.do(client, r.Method, path, body, r.Hdr)
				require.Equal(t, r.WantStatus, rec.Code,
					"[%s %s] normal path want %d, got %d: %s", r.Method, r.Path, r.WantStatus, rec.Code, rec.Body.String())
			}
		})
	}
}
