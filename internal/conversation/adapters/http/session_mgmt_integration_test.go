package http

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"orkome/internal/contract/apierr"
	convapp "orkome/internal/conversation/app"
)

func errCodeOf(t *testing.T, out map[string]any) string {
	t.Helper()
	c, ok := out["error"].(string)
	require.True(t, ok, "response should contain an error code: %v", out)
	return c
}

func (h *b4h) doRaw(method, path, rawBody string) (int, map[string]any) {
	h.t.Helper()
	rd := httptest.NewRequest(method, "/api"+path, bytes.NewReader([]byte(rawBody)))
	rd.Header.Set("X-Workspace-Id", h.wsID)
	w := httptest.NewRecorder()
	h.handler.ServeHTTP(w, rd)
	var out map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	return w.Code, out
}

func TestSessionMgmt_RenameSession(t *testing.T) {
	h := newB4H(t)

	code, out := h.do("POST", "/chat/sessions", map[string]any{"agent_id": h.agentID, "title": "old name"})
	require.Equal(t, 201, code, "create: %v", out)
	sid, _ := out["id"].(string)

	code, out = h.do("PATCH", "/chat/sessions/"+sid, map[string]any{"title": "new name  "})
	require.Equal(t, 200, code, "rename: %v", out)
	assert.Equal(t, "new name", out["title"])
	assert.Equal(t, sid, out["id"], "returns the full session row")

	code, out = h.do("GET", "/chat/sessions/"+sid, nil)
	require.Equal(t, 200, code)
	assert.Equal(t, "new name", out["title"])

	code, out = h.do("PATCH", "/chat/sessions/"+sid, map[string]any{})
	require.Equal(t, 200, code, "empty patch: %v", out)
	assert.Equal(t, "new name", out["title"])

	code, out = h.do("PATCH", "/chat/sessions/"+sid, map[string]any{"title": "   "})
	assert.Equal(t, 400, code)
	assert.Equal(t, apierr.Validation, errCodeOf(t, out))

	code, out = h.do("PATCH", "/chat/sessions/"+sid, map[string]any{"title": strings.Repeat("mark", 201)})
	assert.Equal(t, 400, code)
	assert.Equal(t, apierr.Validation, errCodeOf(t, out))

	code, out = h.doRaw("PATCH", "/chat/sessions/"+sid, "{bad json")
	assert.Equal(t, 400, code)
	assert.Equal(t, apierr.Validation, errCodeOf(t, out))

	title := "x"
	code, out = h.do("PATCH", "/chat/sessions/00000000-0000-0000-0000-000000000009", map[string]any{"title": title})
	assert.Equal(t, 404, code)
	assert.Equal(t, apierr.NotFound, errCodeOf(t, out))
}

func TestSessionMgmt_PinUnpinAndListOrdering(t *testing.T) {
	h := newB4H(t)

	mk := func(title string) string {
		code, out := h.do("POST", "/chat/sessions", map[string]any{"agent_id": h.agentID, "title": title})
		require.Equal(t, 201, code, "create: %v", out)
		sid, _ := out["id"].(string)
		require.NotEmpty(t, sid)
		return sid
	}
	s1 := mk("one")
	s2 := mk("two")

	first := func() string {
		code, out := h.do("GET", "/chat/sessions", nil)
		require.Equal(t, 200, code)
		list, _ := out["sessions"].([]any)
		require.Len(t, list, 2)
		m, _ := list[0].(map[string]any)
		return m["id"].(string)
	}

	assert.Equal(t, s2, first(), "unpinned: updated_at DESC (new sessions first)")

	code, out := h.do("POST", "/chat/sessions/"+s1+"/pin", nil)
	require.Equal(t, 200, code, "pin: %v", out)
	assert.Equal(t, "pinned", out["result"])
	assert.Equal(t, s1, first(), "pinned sessions should sort first")
	code, out = h.do("GET", "/chat/sessions/"+s1, nil)
	require.Equal(t, 200, code)
	assert.Equal(t, true, out["pinned"])

	code, _ = h.do("POST", "/chat/sessions/"+s1+"/pin", nil)
	require.Equal(t, 200, code)
	assert.Equal(t, s1, first())

	code, out = h.do("POST", "/chat/sessions/"+s1+"/unpin", nil)
	require.Equal(t, 200, code, "unpin: %v", out)
	assert.Equal(t, "unpinned", out["result"])
	code, out = h.do("GET", "/chat/sessions/"+s1, nil)
	require.Equal(t, 200, code)
	assert.Equal(t, false, out["pinned"])
	assert.Equal(t, s2, first(), "updated_at order restored after cancel")

	code, out = h.do("POST", "/chat/sessions/00000000-0000-0000-0000-000000000009/pin", nil)
	assert.Equal(t, 404, code)
	assert.Equal(t, apierr.NotFound, errCodeOf(t, out))
}

func TestSessionMgmt_DeleteSession(t *testing.T) {
	h := newB4H(t)

	code, out := h.do("POST", "/chat/sessions", map[string]any{"agent_id": h.agentID, "title": "pending deletion"})
	require.Equal(t, 201, code, "create: %v", out)
	sid, _ := out["id"].(string)

	code, out = h.do("POST", "/chat/sessions/"+sid+"/messages", map[string]any{"content": "placeholder"})
	require.Equal(t, 201, code, "send: %v", out)
	code, out = h.do("DELETE", "/chat/sessions/"+sid, nil)
	require.Equal(t, 409, code, "queued task in flight should be 409: %v", out)
	assert.Equal(t, convapp.CodeSessionInUse, errCodeOf(t, out))

	code, out = h.do("DELETE", "/chat/sessions/"+sid+"/pending-task", nil)
	require.Equal(t, 200, code, "cancel: %v", out)

	var captured []string
	h.chat.SetSessionEventSink(func(_ context.Context, event, ws, s string) {
		captured = append(captured, event+"|"+ws+"|"+s)
	})

	code, out = h.do("DELETE", "/chat/sessions/"+sid, nil)
	require.Equal(t, 200, code, "delete: %v", out)
	assert.Equal(t, "deleted", out["result"])
	require.Len(t, captured, 1, "exactly one frame chat:session_deleted")
	assert.Equal(t, "chat:session_deleted|"+h.wsID+"|"+sid, captured[0])

	code, _ = h.do("GET", "/chat/sessions/"+sid, nil)
	assert.Equal(t, 404, code)
	code, _ = h.do("GET", "/chat/sessions/"+sid+"/messages", nil)
	assert.Equal(t, 404, code)
	code, out = h.do("GET", "/chat/sessions", nil)
	require.Equal(t, 200, code)
	sessions, _ := out["sessions"].([]any)
	assert.Empty(t, sessions)

	code, out = h.do("DELETE", "/chat/sessions/"+sid, nil)
	assert.Equal(t, 404, code)
	assert.Equal(t, apierr.NotFound, errCodeOf(t, out))
}

func TestSessionMgmt_DeleteAfterTerminalTaskAllowed(t *testing.T) {
	h := newB4H(t)

	code, out := h.do("POST", "/chat/sessions", map[string]any{"agent_id": h.agentID})
	require.Equal(t, 201, code, "create: %v", out)
	sid, _ := out["id"].(string)

	code, out = h.do("POST", "/chat/sessions/"+sid+"/messages", map[string]any{"content": "run once"})
	require.Equal(t, 201, code, "send: %v", out)
	code, out = h.do("DELETE", "/chat/sessions/"+sid+"/pending-task", nil)
	require.Equal(t, 200, code, "cancel: %v", out)

	code, out = h.do("DELETE", "/chat/sessions/"+sid, nil)
	require.Equal(t, 200, code, "terminal task should not block delete: %v", out)
	assert.Equal(t, "deleted", out["result"])
}
