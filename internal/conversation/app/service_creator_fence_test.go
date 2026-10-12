package app

// W2-1 creator 私有口径回归：
//  1. 写面统一 fence——发消息/改名/置顶/归档/删除/取消/读会话对非 creator
//     一律 404（与不存在不可区分，不泄露存在性），creator 全通；
//  2. chat:done 帧覆盖矩阵的 cancel 格——queued 直取消（Cancel→Finalize）与
//     TaskCancelled 漏斗（HandleChatTaskTerminal→Finalize）都发收尾帧、都只
//     达 creator；completed/approve/fail 三格在 orchestration 侧
//     （service_chatdone_frame_test.go）。

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type recordingCanceller struct {
	calls []string
	status string
	taskID string
	ok     bool
}

func (f *recordingCanceller) CancelChatTask(_ context.Context, _, sessionID string) (string, string, bool, error) {
	f.calls = append(f.calls, sessionID)
	return f.status, f.taskID, f.ok, nil
}

type recordingFrames struct {
	pushed []pushedFrame
}

type pushedFrame struct {
	user      string
	frameType string
	payload   map[string]any
}

func (f *recordingFrames) Push(_ context.Context, userID, frameType string, payload map[string]any) {
	f.pushed = append(f.pushed, pushedFrame{user: userID, frameType: frameType, payload: payload})
}

func TestCreatorOnly_WriteFence(t *testing.T) {
	repo := newFakeChatRepo()
	repo.sessions["ws1/sess1"] = activeSession("ws1", "u1")
	enq := &fakeEnqueuer{}
	s := NewChatService(repo, enq)

	// 非 creator：写面一律 404 且不产生任何副作用。
	_, _, err := s.SendMessage(context.Background(), "ws1", "sess1", "u2", "hi")
	require.Error(t, err)
	assert.Equal(t, "E_NOT_FOUND", errCode(t, err))
	assert.Empty(t, enq.calls, "非 creator 不得入队任务")
	assert.Empty(t, repo.inserted, "非 creator 不得落消息")

	_, err = s.UpdateSession(context.Background(), "ws1", "sess1", "u2", strPtr("hack"))
	require.Error(t, err)
	assert.Equal(t, "E_NOT_FOUND", errCode(t, err))
	assert.Empty(t, repo.titleUpdated, "非 creator 不得改名")

	_, err = s.SetPinned(context.Background(), "ws1", "sess1", "u2", true)
	require.Error(t, err)
	assert.Equal(t, "E_NOT_FOUND", errCode(t, err))
	assert.Empty(t, repo.pinSet, "非 creator 不得置顶")

	require.Error(t, s.Archive(context.Background(), "ws1", "sess1", "u2", true))
	assert.Equal(t, "E_NOT_FOUND", errCode(t, s.Archive(context.Background(), "ws1", "sess1", "u2", true)))

	require.Error(t, s.DeleteSession(context.Background(), "ws1", "sess1", "u2"))
	assert.Contains(t, repo.sessions, "ws1/sess1", "非 creator 删除不生效")

	cancelled := &recordingCanceller{status: "cancelled", taskID: "t1", ok: true}
	_, err = s.Cancel(context.Background(), "ws1", "sess1", "u2", cancelled)
	require.Error(t, err)
	assert.Equal(t, "E_NOT_FOUND", errCode(t, err))
	assert.Empty(t, cancelled.calls, "非 creator 不得触发取消")

	_, err = s.GetSession(context.Background(), "ws1", "sess1", "u2")
	require.Error(t, err)
	assert.Equal(t, "E_NOT_FOUND", errCode(t, err), "读会话同样不泄露存在性")

	// 不存在的会话对 creator 也 404（口径一致性）。
	_, err = s.GetSession(context.Background(), "ws1", "nope", "u1")
	assert.Equal(t, "E_NOT_FOUND", errCode(t, err))

	// creator：全通。
	_, err = s.UpdateSession(context.Background(), "ws1", "sess1", "u1", strPtr("renamed"))
	require.NoError(t, err)
	_, err = s.SetPinned(context.Background(), "ws1", "sess1", "u1", true)
	require.NoError(t, err)
	require.NoError(t, s.Archive(context.Background(), "ws1", "sess1", "u1", true))
	_, _, err = s.SendMessage(context.Background(), "ws1", "sess1", "u1", "hello")
	require.NoError(t, err)
	_, err = s.GetSession(context.Background(), "ws1", "sess1", "u1")
	require.NoError(t, err)
	require.NoError(t, s.DeleteSession(context.Background(), "ws1", "sess1", "u1"))
	assert.NotContains(t, repo.sessions, "ws1/sess1")
}

func TestCreatorOnly_CancelGateDoesNotTouchCanceller(t *testing.T) {
	repo := newFakeChatRepo()
	repo.sessions["ws1/sess1"] = activeSession("ws1", "u1")
	s := NewChatService(repo, nil)
	cancelled := &recordingCanceller{status: "cancelled", taskID: "t1", ok: true}

	_, err := s.Cancel(context.Background(), "ws1", "sess1", "u2", cancelled)
	require.Error(t, err)
	assert.Empty(t, cancelled.calls, "fence 拦截必须在触达取消端口之前")
}

func strPtr(s string) *string { return &s }

func TestCancelFrame_QueuedCancelReachesCreatorOnly(t *testing.T) {
	repo := newFakeChatRepo()
	repo.sessions["ws1/sess1"] = activeSession("ws1", "u1")
	repo.listMsg = func(string, int, int64) ([]Message, error) {
		return []Message{{Role: "user", Content: "x", Seq: 1}}, nil
	}
	frames := &recordingFrames{}
	s := NewChatService(repo, nil)
	s.SetUserFrames(frames)

	outcome, err := s.Cancel(context.Background(), "ws1", "sess1", "u1",
		&recordingCanceller{status: "cancelled", taskID: "t-q", ok: true})
	require.NoError(t, err)
	assert.Equal(t, "restored", outcome)

	require.Len(t, frames.pushed, 1, "queued 直取消恰好一条收尾帧")
	f := frames.pushed[0]
	assert.Equal(t, "u1", f.user, "只达 creator")
	assert.Equal(t, "chat:done", f.frameType)
	assert.Equal(t, "sess1", f.payload["session_id"])
	assert.Equal(t, "t-q", f.payload["task_id"])
	assert.Equal(t, "assistant", f.payload["role"])
	assert.Equal(t, "cancelled", f.payload["status"], "终态语义字段必须携带")
	assert.Equal(t, "no_response", f.payload["kind"], "取消无回复，kind 走空内容规则")
	assert.Empty(t, f.payload["content"])
}

func TestCancelFrame_TaskCancelledFunnelBothOutcomes(t *testing.T) {
	for _, tc := range []struct {
		name           string
		assistantCount int
	}{
		{"restored", 0},
		{"stopped", 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := newFakeChatRepo()
			repo.sessions["ws1/sess1"] = activeSession("ws1", "u1")
			repo.assistantCount = tc.assistantCount
			repo.cancelPending["t-run"] = "u1"
			frames := &recordingFrames{}
			s := NewChatService(repo, nil)
			s.SetUserFrames(frames)

			outcome, err := s.HandleChatTaskTerminal(context.Background(), "ws1", "sess1", "u1", "t-run", "cancelled")
			require.NoError(t, err)
			assert.Equal(t, tc.name, outcome)

			require.Len(t, frames.pushed, 1, "TaskCancelled 漏斗终态恰好一条收尾帧")
			f := frames.pushed[0]
			assert.Equal(t, "u1", f.user)
			assert.Equal(t, "cancelled", f.payload["status"])
			assert.Equal(t, "t-run", f.payload["task_id"])
		})
	}
}

func TestCancelFrame_NilPortAndNonVerdictNoFrame(t *testing.T) {
	repo := newFakeChatRepo()
	repo.sessions["ws1/sess1"] = activeSession("ws1", "u1")
	s := NewChatService(repo, nil)

	// 未接线：不 panic、REST 语义不受影响。
	outcome, err := s.Cancel(context.Background(), "ws1", "sess1", "u1",
		&recordingCanceller{status: "cancelled", taskID: "t-q", ok: true})
	require.NoError(t, err)
	assert.Equal(t, "restored", outcome)

	// 非 cancelled 终态（completed/failed）由 orchestration 结算侧发帧，
	// 会话侧漏斗必须零帧（否则双帧）。
	frames := &recordingFrames{}
	s.SetUserFrames(frames)
	for _, status := range []string{"completed", "failed"} {
		out, err := s.HandleChatTaskTerminal(context.Background(), "ws1", "sess1", "u1", "t-x", status)
		require.NoError(t, err)
		assert.Empty(t, out)
	}
	assert.Empty(t, frames.pushed, "会话侧只对 cancel 终态发帧，避免与结算侧双发")
}
