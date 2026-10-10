package http

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"orkome/internal/channels/app"
	"orkome/internal/channels/domain"
)

type cbRepo struct {
	app.Repo
	inst      app.Installation
	titles    map[string]string
	sources   map[string]string
	locked    map[string]bool
	missingUI bool
}

func (f *cbRepo) GetInstallation(_ context.Context, id string) (app.Installation, error) {
	if f.missingUI || id != f.inst.ID {
		return app.Installation{}, app.ErrInstallationNotFound
	}
	return f.inst, nil
}

func (f *cbRepo) UpdateRouteTitle(_ context.Context, _, externalChatID, title, source string) (bool, error) {
	if _, ok := f.titles[externalChatID]; !ok {
		return false, nil
	}
	if f.locked[externalChatID] {
		return false, nil
	}
	f.titles[externalChatID] = title
	f.sources[externalChatID] = source
	return true, nil
}

func newCBHandler(t *testing.T, instCfg string) (*http.ServeMux, *cbRepo) {
	t.Helper()
	inst := app.Installation{
		ID: "inst-1", WorkspaceID: "ws-1", Kind: "fake", Status: "active",
		Fingerprint: domain.CredentialFingerprint("fake", []byte(instCfg)),
		Config:      []byte(instCfg),
	}
	repo := &cbRepo{
		inst:    inst,
		titles:  map[string]string{"chat-1": ""},
		sources: map[string]string{},
		locked:  map[string]bool{},
	}
	r := http.NewServeMux()
	Mount(r, Deps{Titles: app.NewTitleCallbackService(repo), Repo: repo})
	return r, repo
}

func doPost(t *testing.T, h http.Handler, cfg, instID, chatID, title string) (int, map[string]any) {
	t.Helper()
	body, _ := json.Marshal(map[string]string{
		"installation_id": instID, "external_chat_id": chatID, "title": title,
	})
	req := httptest.NewRequest(http.MethodPost, "/api/channels/title-callback", bytes.NewReader(body))
	if cfg != "" {
		req.Header.Set("X-Channel-Config", cfg)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	out := map[string]any{}
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	return w.Code, out
}

func TestTitleCallbackHappyPath(t *testing.T) {
	const cfg = `{"routing_key":"rk-1","app_secret":"s3cr3t"}`
	h, repo := newCBHandler(t, cfg)

	code, out := doPost(t, h, cfg, "inst-1", "chat-1", "LLM polished title")
	require.Equal(t, 200, code, "out=%v", out)
	assert.Equal(t, true, out["applied"])
	assert.Equal(t, "LLM polished title", repo.titles["chat-1"])
	assert.Equal(t, domain.TitleSourceLLM, repo.sources["chat-1"])
}

func TestTitleCallbackAuth(t *testing.T) {
	const cfg = `{"routing_key":"rk-1","app_secret":"s3cr3t"}`
	h, _ := newCBHandler(t, cfg)

	code, _ := doPost(t, h, "", "inst-1", "chat-1", "t")
	assert.Equal(t, 401, code, "missing credential header 401")

	code, _ = doPost(t, h, `{"routing_key":"rk-1","app_secret":"wrong"}`, "inst-1", "chat-1", "t")
	assert.Equal(t, 401, code, "credential fingerprint mismatch 401")

	code, _ = doPost(t, h, cfg, "inst-other", "chat-1", "t")
	assert.Equal(t, 401, code, "unknown install 401 (same shape)")
}

func TestTitleCallbackRejects(t *testing.T) {
	const cfg = `{"routing_key":"rk-1"}`
	h, repo := newCBHandler(t, cfg)

	code, out := doPost(t, h, cfg, "inst-1", "chat-1", "   ")
	assert.Equal(t, 400, code, "whitespace-only title 400: %v", out)

	code, out = doPost(t, h, cfg, "inst-1", "chat-none", "t")
	require.Equal(t, 200, code)
	assert.Equal(t, false, out["applied"], "route does not exist = non-error landing")

	repo.locked["chat-1"] = true
	code, out = doPost(t, h, cfg, "inst-1", "chat-1", "late title")
	require.Equal(t, 200, code)
	assert.Equal(t, false, out["applied"], "explicit lock does not overwrite")
	assert.Empty(t, repo.titles["chat-1"])
}
