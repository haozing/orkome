package http

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/haozing/ploykit/platform/webx"
	"orkome/internal/contract/apierr"
	"orkome/internal/skills/app"
)

func newSkillLabelsRouter(t *testing.T, mounted []string, notFoundFor string) *http.ServeMux {
	t.Helper()
	labels := func(ids ...string) []SkillLabelView {
		out := make([]SkillLabelView, 0, len(ids))
		for _, id := range ids {
			out = append(out, SkillLabelView{ID: id, Name: "L" + id})
		}
		return out
	}
	deps := Deps{
		Skills: &app.SkillService{},
		SkillLabels: SkillLabelEndpoints{
			List: func(context.Context, string, string) ([]SkillLabelView, error) {
				return labels(mounted...), nil
			},
			Attach: func(_ context.Context, _, skillID, labelID string) ([]SkillLabelView, error) {
				if skillID == notFoundFor {
					return nil, apierr.New(apierr.NotFound, "skill not found")
				}
				return labels(append(append([]string{}, mounted...), labelID)...), nil
			},
			Detach: func(context.Context, string, string, string) ([]SkillLabelView, error) {
				return labels(), nil
			},
		},
		MemberCheck: func(context.Context, string, string) (bool, error) { return true, nil },
	}
	auth := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, r.WithContext(webx.WithPrincipal(r.Context(),
				&webx.Principal{UserID: "u-1", Source: webx.SourceSession})))
		})
	}
	r := http.NewServeMux()
	Mount(r, deps, auth)
	return r
}

func skillLabelsReq(r http.Handler, method, target, body string) *httptest.ResponseRecorder {
	var req *http.Request
	if body == "" {
		req = httptest.NewRequest(method, "/api"+target, nil)
	} else {
		req = httptest.NewRequest(method, "/api"+target, strings.NewReader(body))
	}
	req.Header.Set("X-Workspace-Id", "ws-1")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

func TestSkillLabelsEndpoints_MountedAndValidation(t *testing.T) {
	r := newSkillLabelsRouter(t, []string{"lab-1"}, "sk-404")

	rec := skillLabelsReq(r, "GET", "/skills/sk-1/labels", "")
	require.Equal(t, http.StatusOK, rec.Code)
	var got []SkillLabelView
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	require.Len(t, got, 1)

	rec = skillLabelsReq(r, "POST", "/skills/sk-1/labels", `{"label_id":"lab-2"}`)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	assert.Len(t, got, 2, "attach response equals updated catalog")

	rec = skillLabelsReq(r, "DELETE", "/skills/sk-1/labels/lab-2", "")
	require.Equal(t, http.StatusOK, rec.Code)
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	assert.Empty(t, got)

	assert.Equal(t, http.StatusBadRequest, skillLabelsReq(r, "POST", "/skills/sk-1/labels", `{}`).Code)
	assert.Equal(t, http.StatusMethodNotAllowed, skillLabelsReq(r, "DELETE", "/skills/sk-1/labels", "").Code)
	assert.Equal(t, http.StatusNotFound, skillLabelsReq(r, "POST", "/skills/sk-404/labels", `{"label_id":"x"}`).Code)

	bare := http.NewServeMux()
	Mount(bare, Deps{Skills: &app.SkillService{},
		MemberCheck: func(context.Context, string, string) (bool, error) { return true, nil }},
		func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r2 *http.Request) {
				next.ServeHTTP(w, r2.WithContext(webx.WithPrincipal(r2.Context(),
					&webx.Principal{UserID: "u-1", Source: webx.SourceSession})))
			})
		})
	assert.Equal(t, http.StatusNotFound, skillLabelsReq(bare, "GET", "/skills/sk-1/labels", "").Code,
		"SkillLabels zero value = routes not registered")
}
