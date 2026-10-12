package http

// W2-1 creator 私有口径（HTTP 面）：同一工作区的另一成员（userB，member 角色）
// 对他人会话的读/写/取消/读态一律 404（与不存在不可区分），creator 全通。
// 服务层矩阵见 internal/conversation/app/service_creator_fence_test.go；
// chat:done 帧矩阵见同目录 service_chatdone_frame_test.go（orchestration 侧）。

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCreatorOnly_HTTPFence(t *testing.T) {
	h := newB4H(t)

	code, out := h.do("POST", "/chat/sessions", map[string]any{"agent_id": h.agentID, "title": "mine"})
	require.Equal(t, 201, code, "create: %v", out)
	sid, _ := out["id"].(string)
	require.NotEmpty(t, sid)

	// ── 同区非 creator（userB 是 member，过的了外层工作区门）：全 404 ──
	type req struct{ method, path string }
	nonCreatorProbe := []req{
		{"GET", "/chat/sessions/" + sid},
		{"PATCH", "/chat/sessions/" + sid},
		{"POST", "/chat/sessions/" + sid + "/pin"},
		{"POST", "/chat/sessions/" + sid + "/archive"},
		{"POST", "/chat/sessions/" + sid + "/messages"},
		{"GET", "/chat/sessions/" + sid + "/messages"},
		{"GET", "/chat/sessions/" + sid + "/pending-task"},
		{"DELETE", "/chat/sessions/" + sid + "/pending-task"},
		{"DELETE", "/chat/sessions/" + sid + "/queued-tasks"},
		{"POST", "/chat/sessions/" + sid + "/read"},
		{"GET", "/chat/sessions/" + sid + "/draft-restores"},
		{"DELETE", "/chat/sessions/" + sid},
	}
	for _, p := range nonCreatorProbe {
		code, out := h.doAs(h.userB, p.method, p.path, map[string]any{"content": "intrude", "title": "hijack"})
		assert.Equal(t, 404, code, "%s %s 应对非 creator 404: %v", p.method, p.path, out)
		if code == 404 {
			assert.Equal(t, "E_NOT_FOUND", errCodeOf(t, out), "%s %s 错误码形态与不存在一致", p.method, p.path)
		}
	}

	// 非 creator 的尝试不留痕：会话原样健在，creator 视角不受影响。
	code, out = h.do("GET", "/chat/sessions/"+sid, nil)
	require.Equal(t, 200, code)
	assert.Equal(t, "mine", out["title"], "非 creator 改名未生效")

	// ── creator：全通 ──
	code, _ = h.do("PATCH", "/chat/sessions/"+sid, map[string]any{"title": "renamed"})
	assert.Equal(t, 200, code)
	code, _ = h.do("POST", "/chat/sessions/"+sid+"/pin", nil)
	assert.Equal(t, 200, code)

	code, out = h.do("POST", "/chat/sessions/"+sid+"/messages", map[string]any{"content": "hello"})
	require.Equal(t, 201, code, "send: %v", out)
	code, out = h.do("GET", "/chat/sessions/"+sid+"/messages", nil)
	require.Equal(t, 200, code)
	msgs, _ := out["messages"].([]any)
	require.Len(t, msgs, 1)

	code, out = h.do("GET", "/chat/sessions/"+sid+"/pending-task", nil)
	require.Equal(t, 200, code)
	require.NotNil(t, out["task"], "queued task visible to creator")

	code, out = h.do("DELETE", "/chat/sessions/"+sid+"/pending-task", nil)
	require.Equal(t, 200, code, "cancel: %v", out)

	code, _ = h.do("POST", "/chat/sessions/"+sid+"/read", nil)
	assert.Equal(t, 200, code)
	code, _ = h.do("GET", "/chat/sessions/"+sid+"/draft-restores", nil)
	assert.Equal(t, 200, code)
	code, _ = h.do("DELETE", "/chat/sessions/"+sid+"/draft-restores", nil)
	assert.Equal(t, 200, code)

	code, _ = h.do("POST", "/chat/sessions/"+sid+"/archive", nil)
	assert.Equal(t, 200, code)
	code, _ = h.do("POST", "/chat/sessions/"+sid+"/unarchive", nil)
	assert.Equal(t, 200, code)

	code, _ = h.do("DELETE", "/chat/sessions/"+sid, nil)
	assert.Equal(t, 200, code)
}
