package http

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"orkome/internal/platform/webx"
	"orkome/internal/work/app"
)

type fakeUsageRepo struct {
	app.Repo
	got []app.ClientUsageReport
}

func (f *fakeUsageRepo) ClientUsageUpsert(_ context.Context, in app.ClientUsageReport) error {
	f.got = append(f.got, in)
	return nil
}

func newUsageRouter(t *testing.T, source webx.Source) (http.Handler, *fakeUsageRepo) {
	t.Helper()
	repo := &fakeUsageRepo{}
	deps := Deps{
		ClientUsage: app.NewClientUsageService(repo),
		MemberCheck: func(context.Context, string, string) (bool, error) { return true, nil },
	}
	auth := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, r.WithContext(webx.WithPrincipal(r.Context(),
				&webx.Principal{UserID: "u-1", Source: source})))
		})
	}
	mux := http.NewServeMux()
	Mount(mux, deps, auth)
	return webx.ClientMetadata(mux), repo
}

func doClientUsage(r http.Handler, platform, version, os, installID string) *httptest.ResponseRecorder {

	req := httptest.NewRequest("POST", "/api/client-usage", strings.NewReader(`{"install_id":"`+installID+`"}`))
	req.Header.Set("X-Workspace-Id", "ws-1")
	if platform != "" {
		req.Header.Set("X-Client-Platform", platform)
	}
	if version != "" {
		req.Header.Set("X-Client-Version", version)
	}
	if os != "" {
		req.Header.Set("X-Client-OS", os)
	}
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

func TestClientUsage_PATForbidden(t *testing.T) {
	r, repo := newUsageRouter(t, webx.SourcePAT)
	rec := doClientUsage(r, "web", "1.0.0", "windows", "11111111-1111-1111-1111-111111111111")
	assert.Equal(t, http.StatusForbidden, rec.Code)
	assert.Contains(t, rec.Body.String(), "E_FORBIDDEN")
	assert.Empty(t, repo.got, "machine credential requests not persisted")
}

func TestClientUsage_SessionReports(t *testing.T) {
	r, repo := newUsageRouter(t, webx.SourceSession)
	rec := doClientUsage(r, "Web", "1.2.3", "Windows", "11111111-1111-1111-1111-111111111111")
	require.Equal(t, http.StatusNoContent, rec.Code, "body=%s", rec.Body.String())
	require.Len(t, repo.got, 1)
	in := repo.got[0]
	assert.Equal(t, "ws-1", in.WorkspaceID)
	assert.Equal(t, "u-1", in.UserID)
	assert.Equal(t, "web", in.ClientType, "platform headers normalized to lowercase")
	assert.Equal(t, "1.2.3", in.ClientVersion)
	assert.Equal(t, "windows", in.OS)
	assert.Equal(t, "11111111-1111-1111-1111-111111111111", in.InstallID)
}

func TestClientUsage_RejectsUnknownPlatform(t *testing.T) {
	r, repo := newUsageRouter(t, webx.SourceSession)
	assert.Equal(t, http.StatusBadRequest, doClientUsage(r, "", "1.0.0", "windows", "i").Code)
	assert.Equal(t, http.StatusBadRequest, doClientUsage(r, "mobile", "1.0.0", "windows", "i").Code)
	assert.Empty(t, repo.got)
}
