package app

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeChatRepo struct {
	sessions map[string]Session

	seq            int64
	inserted       []Message
	listMsg        func(sessionID string, limit int, beforeSeq int64) ([]Message, error)
	assistantCount int

	draftUpserted map[string]string
	draftUsers    []string
	deleteSince   []int64
	finalized     []string

	pointerSet     string
	pointerCleared bool

	titleUpdated []struct{ ws, id, title string }
	pinSet       []struct {
		ws, id string
		pinned bool
	}
	deleteFail bool

	unreadSet bool

	cancelPending   map[string]string
	markedPending   []string
	clearedPending  []string
	stoppedByTaskID map[string]bool

	getMessage     []string
	archivedByUser []struct{ ws, uid string }

	// FP-14：agent 可用性注册表（ws/agentID → archived）与已读标记记录
	agentStates map[string]bool
	readMarks   []string

	txActive     bool
	rolledBack   bool
	withinOpened int
}

func newFakeChatRepo() *fakeChatRepo {
	return &fakeChatRepo{sessions: map[string]Session{}, draftUpserted: map[string]string{},
		cancelPending: map[string]string{}, stoppedByTaskID: map[string]bool{},
		agentStates: map[string]bool{}}
}

func (f *fakeChatRepo) Within(_ context.Context, fn func(ctx context.Context) error) error {
	if f.txActive {
		return fn(context.Background())
	}
	f.txActive = true
	f.withinOpened++
	nIns, seq, unread := len(f.inserted), f.seq, f.unreadSet
	err := fn(context.Background())
	f.txActive = false
	if err != nil {
		f.inserted = f.inserted[:nIns]
		f.seq = seq
		f.unreadSet = unread
		f.rolledBack = true
	}
	return err
}

func (f *fakeChatRepo) CreateSession(_ context.Context, s Session) (Session, error) {
	s.ID = "sess1"
	f.sessions[s.WorkspaceID+"/"+s.ID] = s
	return s, nil
}
func (f *fakeChatRepo) GetSession(_ context.Context, wsID, id string) (Session, bool, error) {
	s, ok := f.sessions[wsID+"/"+id]
	return s, ok, nil
}
func (f *fakeChatRepo) ListSessions(_ context.Context, _, _ string) ([]Session, error) {
	return nil, nil
}
func (f *fakeChatRepo) ArchiveSession(_ context.Context, _, _ string, _ bool) error { return nil }
func (f *fakeChatRepo) UpdateSessionTitle(_ context.Context, wsID, id, title string) (Session, bool, error) {
	f.titleUpdated = append(f.titleUpdated, struct{ ws, id, title string }{wsID, id, title})
	s, ok := f.sessions[wsID+"/"+id]
	if !ok {
		return Session{}, false, nil
	}
	s.Title = title
	f.sessions[wsID+"/"+id] = s
	return s, true, nil
}
func (f *fakeChatRepo) SetSessionPin(_ context.Context, wsID, id string, pinned bool) (Session, bool, error) {
	s, ok := f.sessions[wsID+"/"+id]
	if !ok {
		return Session{}, false, nil
	}
	f.pinSet = append(f.pinSet, struct {
		ws, id string
		pinned bool
	}{wsID, id, pinned})
	s.Pinned = pinned
	f.sessions[wsID+"/"+id] = s
	return s, true, nil
}
func (f *fakeChatRepo) DeleteSessionCascade(_ context.Context, wsID, id string) (bool, error) {
	if _, ok := f.sessions[wsID+"/"+id]; !ok || f.deleteFail {
		return false, nil
	}
	delete(f.sessions, wsID+"/"+id)
	return true, nil
}
func (f *fakeChatRepo) NextSeq(_ context.Context, _ string) (int64, error) {
	f.seq++
	return f.seq, nil
}
func (f *fakeChatRepo) InsertMessage(_ context.Context, m Message) (Message, error) {
	if m.Kind == "system_stopped" && m.TaskID != "" {
		f.stoppedByTaskID[m.TaskID] = true
	}
	f.inserted = append(f.inserted, m)
	return m, nil
}
func (f *fakeChatRepo) InsertMessageParts(_ context.Context, m Message, _ []map[string]any) (Message, error) {
	f.inserted = append(f.inserted, m)
	return m, nil
}
func (f *fakeChatRepo) ListMessages(_ context.Context, sessionID string, limit int, beforeSeq int64) ([]Message, error) {
	if f.listMsg != nil {
		return f.listMsg(sessionID, limit, beforeSeq)
	}
	return nil, nil
}
func (f *fakeChatRepo) CountAssistant(_ context.Context, _ string) (int, error) {
	return f.assistantCount, nil
}
func (f *fakeChatRepo) DeleteUserMessagesSince(_ context.Context, _ string, sinceSeq int64) error {
	f.deleteSince = append(f.deleteSince, sinceSeq)
	return nil
}
func (f *fakeChatRepo) UpsertDraftRestore(_ context.Context, _, sessionID, userID, content string) error {
	f.draftUpserted[sessionID] = content
	f.draftUsers = append(f.draftUsers, userID)
	return nil
}
func (f *fakeChatRepo) FinalizeDraft(_ context.Context, sessionID, outcome string) error {
	f.finalized = append(f.finalized, sessionID+":"+outcome)
	return nil
}
func (f *fakeChatRepo) GetPendingDraft(_ context.Context, sessionID, _ string) (string, bool, error) {
	if c, ok := f.draftUpserted[sessionID]; ok {
		return c, true, nil
	}
	return "", false, nil
}
func (f *fakeChatRepo) UpdateResumePointer(_ context.Context, _, ref string) error {
	f.pointerSet = ref
	return nil
}
func (f *fakeChatRepo) ClearResumePointer(_ context.Context, _ string) error {
	f.pointerCleared = true
	return nil
}
func (f *fakeChatRepo) SetUnread(_ context.Context, _ string, _ time.Time) error {
	f.unreadSet = true
	return nil
}
func (f *fakeChatRepo) AgentAvailability(_ context.Context, wsID, agentID string) (bool, bool, error) {
	// 显式注册过（含 archived=false）才视为存在；未注册 = 不存在
	a, ok := f.agentStates[wsID+"/"+agentID]
	return ok, a, nil
}

func (f *fakeChatRepo) MarkRead(_ context.Context, wsID, sessionID, userID string) (bool, error) {
	f.readMarks = append(f.readMarks, wsID+"/"+sessionID+"/"+userID)
	s, ok := f.sessions[wsID+"/"+sessionID]
	return ok && s.CreatorID == userID, nil
}

func (f *fakeChatRepo) MarkCancelPending(_ context.Context, _, taskID, userID string) error {
	if taskID == "" {
		return nil
	}
	if _, ok := f.cancelPending[taskID]; !ok {
		f.cancelPending[taskID] = userID
		f.markedPending = append(f.markedPending, taskID)
	}
	return nil
}

func (f *fakeChatRepo) GetCancelPending(_ context.Context, _, taskID string) (string, bool, error) {
	u, ok := f.cancelPending[taskID]
	return u, ok, nil
}

func (f *fakeChatRepo) ClearCancelPending(_ context.Context, _, taskID string) error {
	delete(f.cancelPending, taskID)
	f.clearedPending = append(f.clearedPending, taskID)
	return nil
}

func (f *fakeChatRepo) HasStoppedMessage(_ context.Context, _, taskID string) (bool, error) {
	return f.stoppedByTaskID[taskID], nil
}

func (f *fakeChatRepo) AppendMessageContent(_ context.Context, messageID, appendage, dedupMarker string) (bool, error) {
	for i := range f.inserted {
		m := &f.inserted[i]
		if m.ID != messageID {
			continue
		}
		if strings.Contains(m.Content, dedupMarker) {
			return false, nil
		}
		m.Content += appendage
		return true, nil
	}
	return false, nil
}

func (f *fakeChatRepo) GetMessage(_ context.Context, id string) (bool, error) {
	f.getMessage = append(f.getMessage, id)
	for i := range f.inserted {
		if f.inserted[i].ID == id {
			return true, nil
		}
	}
	return false, nil
}

func (f *fakeChatRepo) ArchiveSessionsByUser(_ context.Context, wsID, userID string) (int64, error) {
	f.archivedByUser = append(f.archivedByUser, struct{ ws, uid string }{wsID, userID})
	var n int64
	for k, sess := range f.sessions {
		if sess.WorkspaceID == wsID && sess.CreatorID == userID {
			sess.Status = "archived"
			f.sessions[k] = sess
			n++
		}
	}
	return n, nil
}

type fakeEnqueuer struct{ calls []string }

func (f *fakeEnqueuer) EnqueueChat(_ context.Context, _, _, _, _, content string) (string, error) {
	f.calls = append(f.calls, content)
	return "task-new", nil
}

type fakeEnqTx struct {
	repo   *fakeChatRepo
	calls  []string
	taskID string
	err    error
	inTx   int
	outTx  int
}

func newFakeEnqTx(repo *fakeChatRepo) *fakeEnqTx {
	return &fakeEnqTx{repo: repo, taskID: "task-tx"}
}

func (f *fakeEnqTx) fn() EnqueueChatTxFunc {
	return func(_ context.Context, _, _, _, _, content string) (string, error) {
		if f.repo != nil && f.repo.txActive {
			f.inTx++
		} else {
			f.outTx++
		}
		f.calls = append(f.calls, content)
		if f.err != nil {
			return "", f.err
		}
		return f.taskID, nil
	}
}

type fakeTaskOps struct {
	latestStatus   string
	latestTaskID   string
	latestOK       bool
	cancelled      []string
	cancelInTx     int
	supplementOK   bool
	supplemented   []string
	supplementInTx int
	repo           *fakeChatRepo
}

func (f *fakeTaskOps) LatestChatTask(_ context.Context, _, _ string) (string, string, bool, error) {
	return f.latestStatus, f.latestTaskID, f.latestOK, nil
}
func (f *fakeTaskOps) CancelQueued(_ context.Context, _, taskID string) error {
	if f.repo != nil && f.repo.txActive {
		f.cancelInTx++
	}
	f.cancelled = append(f.cancelled, taskID)
	return nil
}
func (f *fakeTaskOps) SupplementRunning(_ context.Context, _, taskID, _, _ string) (bool, error) {
	if f.repo != nil && f.repo.txActive {
		f.supplementInTx++
	}
	f.supplemented = append(f.supplemented, taskID)
	return f.supplementOK, nil
}

type fakeCanceller struct {
	status string
	taskID string
	ok     bool
}

func (f *fakeCanceller) CancelChatTask(_ context.Context, _, _ string) (string, string, bool, error) {
	return f.status, f.taskID, f.ok, nil
}

func errCode(t *testing.T, err error) string {
	t.Helper()
	if c, ok := err.(interface{ Code() string }); ok {
		return c.Code()
	}
	if c, ok := err.(interface{ ErrorCode() string }); ok {
		return c.ErrorCode()
	}
	require.FailNow(t, "errors must expose error codes", "err=%v", err)
	return ""
}

func activeSession(ws, creator string) Session {
	return Session{ID: "sess1", WorkspaceID: ws, AgentID: "ag1", CreatorID: creator,
		Status: "active", Title: "t"}
}

func TestFinalize_EmptyTranscriptRestoresDraft(t *testing.T) {
	repo := newFakeChatRepo()
	repo.sessions["ws1/sess1"] = activeSession("ws1", "u1")
	repo.assistantCount = 0

	repo.listMsg = func(string, int, int64) ([]Message, error) {
		return []Message{{SessionID: "sess1", Role: "user", Content: "write a weekly report for me", Seq: 7}}, nil
	}
	s := NewChatService(repo, nil)

	outcome, err := s.Finalize(context.Background(), "ws1", "sess1", "u1", "task-1")
	require.NoError(t, err)
	assert.Equal(t, "restored", outcome)
	assert.Equal(t, "write a weekly report for me", repo.draftUpserted["sess1"], "user content moved into draft restore")
	assert.Equal(t, []int64{7}, repo.deleteSince, "deleted from the last user row on (content not kept in the transcript)")
	assert.Empty(t, repo.finalized, "restored path keeps the draft pending, does not finalize")
	assert.Empty(t, repo.inserted, "no broadcast rows inserted")
}

func TestFinalize_WithAssistantInsertsStopped(t *testing.T) {
	repo := newFakeChatRepo()
	repo.sessions["ws1/sess1"] = activeSession("ws1", "u1")
	repo.assistantCount = 2
	s := NewChatService(repo, nil)

	outcome, err := s.Finalize(context.Background(), "ws1", "sess1", "u1", "task-1")
	require.NoError(t, err)
	assert.Equal(t, "stopped", outcome)
	require.Len(t, repo.inserted, 1, "exactly one system row")
	assert.Equal(t, "assistant", repo.inserted[0].Role)
	assert.Equal(t, "system_stopped", repo.inserted[0].Kind)
	assert.Empty(t, repo.draftUpserted, "stopped path does not create draft restore")
	assert.Equal(t, []string{"sess1:discarded"}, repo.finalized, "in-flight drafts invalidated together")
}

func TestCancel_ThreePaths(t *testing.T) {
	repo := newFakeChatRepo()
	repo.sessions["ws1/sess1"] = activeSession("ws1", "u1")
	repo.listMsg = func(string, int, int64) ([]Message, error) {
		return []Message{{Role: "user", Content: "x", Seq: 1}}, nil
	}
	s := NewChatService(repo, nil)

	out, err := s.Cancel(context.Background(), "ws1", "sess1", "u1", &fakeCanceller{ok: false})
	require.NoError(t, err)
	assert.Equal(t, "none", out, "no tasks: cancel is a no-op")

	out, err = s.Cancel(context.Background(), "ws1", "sess1", "u1",
		&fakeCanceller{status: "cancelled", taskID: "t1", ok: true})
	require.NoError(t, err)
	assert.Equal(t, "restored", out, "not started: immediate verdict (empty transcript takes restored)")

	out, err = s.Cancel(context.Background(), "ws1", "sess1", "u1",
		&fakeCanceller{status: "running", taskID: "t1", ok: true})
	require.NoError(t, err)
	assert.Equal(t, "stop_requested", out, "running: stop requested only, verdict deferred")
	assert.Equal(t, "u1", repo.cancelPending["t1"], "running-state cancellation lands a deferred mark first (internal archive 05 §2.5)")
}

func TestCancel_RunningTaskMarksPendingForVerdict(t *testing.T) {
	repo := newFakeChatRepo()
	repo.sessions["ws1/sess1"] = activeSession("ws1", "u1")
	s := NewChatService(repo, nil)

	out, err := s.Cancel(context.Background(), "ws1", "sess1", "u1",
		&fakeCanceller{status: "running", taskID: "t-run", ok: true})
	require.NoError(t, err)
	assert.Equal(t, "stop_requested", out)
	assert.Equal(t, []string{"t-run"}, repo.markedPending, "deferred marks persisted with the stop request")
	assert.Equal(t, "u1", repo.cancelPending["t-run"])
	assert.Empty(t, repo.clearedPending, "marks wait for terminal consumption, not cleared on the spot")

	out, err = s.Cancel(context.Background(), "ws1", "sess1", "u1",
		&fakeCanceller{status: "cancelled", taskID: "t-q", ok: true})
	require.NoError(t, err)
	assert.Equal(t, "restored", out)
	assert.Equal(t, []string{"t-run"}, repo.markedPending, "synchronous verdict path does not write marks")
}

func TestHandleChatTaskTerminal_CancelledRestored(t *testing.T) {
	repo := newFakeChatRepo()
	repo.sessions["ws1/sess1"] = activeSession("ws1", "u1")
	repo.assistantCount = 0
	repo.listMsg = func(string, int, int64) ([]Message, error) {
		return []Message{{SessionID: "sess1", Role: "user", Content: "write a weekly report for me", Seq: 7}}, nil
	}
	repo.cancelPending["t-run"] = "u-marker"
	s := NewChatService(repo, nil)

	outcome, err := s.HandleChatTaskTerminal(context.Background(), "ws1", "sess1", "u-fallback", "t-run", "cancelled")
	require.NoError(t, err)
	assert.Equal(t, "restored", outcome)
	assert.Equal(t, "write a weekly report for me", repo.draftUpserted["sess1"])
	assert.Equal(t, []string{"u-marker"}, repo.draftUsers, "draft belongs to the initiator inside the marks")
	assert.Equal(t, []int64{7}, repo.deleteSince)
	assert.Equal(t, []string{"t-run"}, repo.clearedPending, "marks cleared after verdict")
	assert.NotContains(t, repo.cancelPending, "t-run")
}

func TestHandleChatTaskTerminal_CancelledStopped(t *testing.T) {
	repo := newFakeChatRepo()
	repo.sessions["ws1/sess1"] = activeSession("ws1", "u1")
	repo.assistantCount = 2
	repo.cancelPending["t-run"] = "u1"
	s := NewChatService(repo, nil)

	outcome, err := s.HandleChatTaskTerminal(context.Background(), "ws1", "sess1", "u1", "t-run", "cancelled")
	require.NoError(t, err)
	assert.Equal(t, "stopped", outcome)
	require.Len(t, repo.inserted, 1)
	assert.Equal(t, "system_stopped", repo.inserted[0].Kind)
	assert.Equal(t, "t-run", repo.inserted[0].TaskID, "stopped rows carry task_id (idempotent dedup anchor)")
	assert.True(t, repo.stoppedByTaskID["t-run"])
	assert.Equal(t, []string{"t-run"}, repo.clearedPending)
}

func TestHandleChatTaskTerminal_CompletedAndFailedNoOp(t *testing.T) {
	repo := newFakeChatRepo()
	repo.sessions["ws1/sess1"] = activeSession("ws1", "u1")
	repo.assistantCount = 1
	repo.cancelPending["t-run"] = "u1"
	s := NewChatService(repo, nil)

	for _, status := range []string{"completed", "failed"} {
		outcome, err := s.HandleChatTaskTerminal(context.Background(), "ws1", "sess1", "u1", "t-run", status)
		require.NoError(t, err)
		assert.Equal(t, "", outcome, "%s terminal state without verdict", status)
	}
	assert.Empty(t, repo.inserted, "no rows inserted")
	assert.Empty(t, repo.finalized, "draft untouched")
	assert.Empty(t, repo.clearedPending)
}

func TestHandleChatTaskTerminal_IdempotentSecondCall(t *testing.T) {
	repo := newFakeChatRepo()
	repo.sessions["ws1/sess1"] = activeSession("ws1", "u1")
	repo.assistantCount = 2
	repo.cancelPending["t-run"] = "u1"
	s := NewChatService(repo, nil)

	first, err := s.HandleChatTaskTerminal(context.Background(), "ws1", "sess1", "u1", "t-run", "cancelled")
	require.NoError(t, err)
	assert.Equal(t, "stopped", first)
	require.Len(t, repo.inserted, 1)

	second, err := s.HandleChatTaskTerminal(context.Background(), "ws1", "sess1", "u1", "t-run", "cancelled")
	require.NoError(t, err)
	assert.Equal(t, "", second, "marks cleared: second call yields no verdict")
	assert.Len(t, repo.inserted, 1, "no duplicate inserts of stopped rows")

	repo.cancelPending["t-run"] = "u1"
	third, err := s.HandleChatTaskTerminal(context.Background(), "ws1", "sess1", "u1", "t-run", "cancelled")
	require.NoError(t, err)
	assert.Equal(t, "stopped", third)
	assert.Len(t, repo.inserted, 1, "skip if a stopped row with the same taskID exists (idempotent dedup check)")
}

func TestHandleChatTaskTerminal_NoMarkerNoVerdict(t *testing.T) {
	repo := newFakeChatRepo()
	repo.sessions["ws1/sess1"] = activeSession("ws1", "u1")
	repo.assistantCount = 0
	s := NewChatService(repo, nil)

	outcome, err := s.HandleChatTaskTerminal(context.Background(), "ws1", "sess1", "u1", "t-run", "cancelled")
	require.NoError(t, err)
	assert.Equal(t, "", outcome)
	assert.Empty(t, repo.inserted, "no verdict written")
	assert.Empty(t, repo.draftUpserted)
	assert.Empty(t, repo.deleteSince, "user message rows untouched")
}

func TestRestoreDraft_PrivateToCreator(t *testing.T) {
	repo := newFakeChatRepo()
	repo.sessions["ws1/sess1"] = activeSession("ws1", "u1")
	repo.draftUpserted["sess1"] = "draft content"
	s := NewChatService(repo, nil)

	_, ok, err := s.RestoreDraft(context.Background(), "ws1", "sess1", "u2")
	require.Error(t, err)
	assert.False(t, ok)
	assert.Equal(t, "E_NOT_FOUND", errCode(t, err), "non-creator indistinguishable from does-not-exist")

	_, ok, err = s.RestoreDraft(context.Background(), "ws1", "nope", "u1")
	require.Error(t, err)
	assert.False(t, ok)
	assert.Equal(t, "E_NOT_FOUND", errCode(t, err))

	content, ok, err := s.RestoreDraft(context.Background(), "ws1", "sess1", "u1")
	require.NoError(t, err)
	assert.True(t, ok)
	assert.Equal(t, "draft content", content)
}

func TestDiscardDraft_PrivateToCreator(t *testing.T) {
	repo := newFakeChatRepo()
	repo.sessions["ws1/sess1"] = activeSession("ws1", "u1")
	s := NewChatService(repo, nil)

	err := s.DiscardDraft(context.Background(), "ws1", "sess1", "u2")
	require.Error(t, err)
	assert.Equal(t, "E_NOT_FOUND", errCode(t, err))
	assert.Empty(t, repo.finalized)

	err = s.DiscardDraft(context.Background(), "ws1", "sess1", "u1")
	require.NoError(t, err)
	assert.Equal(t, []string{"sess1:discarded"}, repo.finalized)
}

func newChatWith(repo *fakeChatRepo, ops *fakeTaskOps, enq *fakeEnqueuer) *ChatService {
	s := NewChatService(repo, enq)
	s.SetChatTaskOps(ops)
	return s
}

func TestSendMessage_RunningTaskSteeredViaSupplement(t *testing.T) {
	repo := newFakeChatRepo()
	repo.sessions["ws1/sess1"] = activeSession("ws1", "u1")
	ops := &fakeTaskOps{latestStatus: "running", latestTaskID: "t-run", latestOK: true, supplementOK: true}
	enq := &fakeEnqueuer{}
	s := newChatWith(repo, ops, enq)

	m, taskID, err := s.SendMessage(context.Background(), "ws1", "sess1", "u1", "one more thing")
	require.NoError(t, err)
	assert.Equal(t, "t-run", taskID, "steering hit: the task is the original running task")
	assert.Equal(t, []string{"t-run"}, ops.supplemented)
	assert.Empty(t, enq.calls, "must not enqueue duplicate new tasks")
	require.Len(t, repo.inserted, 1)
	assert.Equal(t, "user", m.Role)
	assert.True(t, repo.unreadSet)
}

func TestSendMessage_QueuedTaskCancelledAndRequeued(t *testing.T) {
	repo := newFakeChatRepo()
	repo.sessions["ws1/sess1"] = activeSession("ws1", "u1")
	ops := &fakeTaskOps{latestStatus: "queued", latestTaskID: "t-q", latestOK: true}
	enq := &fakeEnqueuer{}
	s := newChatWith(repo, ops, enq)

	_, taskID, err := s.SendMessage(context.Background(), "ws1", "sess1", "u1", "changed my mind")
	require.NoError(t, err)
	assert.Equal(t, []string{"t-q"}, ops.cancelled, "queued tasks cancelled first")
	assert.Equal(t, "task-new", taskID)
	assert.Len(t, enq.calls, 1, "new task re-enqueued after cancel")
}

func TestSendMessage_SupplementRaceFallsBackToEnqueue(t *testing.T) {
	repo := newFakeChatRepo()
	repo.sessions["ws1/sess1"] = activeSession("ws1", "u1")
	ops := &fakeTaskOps{latestStatus: "running", latestTaskID: "t-run", latestOK: true, supplementOK: false}
	enq := &fakeEnqueuer{}
	s := newChatWith(repo, ops, enq)

	_, taskID, err := s.SendMessage(context.Background(), "ws1", "sess1", "u1", "ask again")
	require.NoError(t, err)
	assert.Equal(t, []string{"t-run"}, ops.supplemented)
	assert.Equal(t, "task-new", taskID, "race fallback: take the normal enqueue")
	assert.Len(t, enq.calls, 1)
}

func TestSendMessage_InactiveSessionRejected(t *testing.T) {
	repo := newFakeChatRepo()
	sess := activeSession("ws1", "u1")
	sess.Status = "archived"
	repo.sessions["ws1/sess1"] = sess
	enq := &fakeEnqueuer{}
	s := NewChatService(repo, enq)

	_, _, err := s.SendMessage(context.Background(), "ws1", "sess1", "u1", "hi")
	require.Error(t, err)
	assert.Equal(t, "E_VALIDATION", errCode(t, err))
	assert.Empty(t, enq.calls)

	repo.sessions["ws1/sess1"] = activeSession("ws1", "u1")
	_, _, err = s.SendMessage(context.Background(), "ws1", "sess1", "u1", "   ")
	require.Error(t, err)
	assert.Equal(t, "E_VALIDATION", errCode(t, err))
	assert.Empty(t, repo.inserted)
}

func TestSendMessage_TxPortMessageAndTaskSameTransaction(t *testing.T) {
	repo := newFakeChatRepo()
	repo.sessions["ws1/sess1"] = activeSession("ws1", "u1")
	enq := &fakeEnqueuer{}
	enqTx := newFakeEnqTx(repo)
	s := NewChatService(repo, enq)
	s.SetEnqueueChatTx(enqTx.fn())

	m, taskID, err := s.SendMessage(context.Background(), "ws1", "sess1", "u1", "hello")
	require.NoError(t, err)
	assert.Equal(t, "task-tx", taskID)
	require.Len(t, repo.inserted, 1)
	assert.Equal(t, "user", m.Role)
	assert.Equal(t, "hello", m.Content)
	assert.True(t, repo.unreadSet, "SetUnread also inside the transaction")
	assert.Equal(t, 1, repo.withinOpened, "exactly one transaction")
	assert.Equal(t, 1, enqTx.inTx, "enqueue called inside the transaction")
	assert.Zero(t, enqTx.outTx)
	assert.Empty(t, enq.calls, "Tx port wired, two-step path no longer taken")
	assert.False(t, repo.rolledBack)
}

func TestSendMessage_TxRollbackOnEnqueueFailure(t *testing.T) {
	repo := newFakeChatRepo()
	repo.sessions["ws1/sess1"] = activeSession("ws1", "u1")
	enqTx := newFakeEnqTx(repo)
	enqTx.err = errors.New("enqueue boom")
	s := NewChatService(repo, &fakeEnqueuer{})
	s.SetEnqueueChatTx(enqTx.fn())

	_, _, err := s.SendMessage(context.Background(), "ws1", "sess1", "u1", "hello")
	require.Error(t, err)
	assert.True(t, repo.rolledBack, "transaction rolled back")
	assert.Empty(t, repo.inserted, "user message rows not landed (enqueue failure leaves no orphan rows)")
	assert.False(t, repo.unreadSet)
	assert.Equal(t, int64(0), repo.seq, "numbering allocation rolls back together")
}

func TestSendMessage_NilTxPortFallsBackToTwoStep(t *testing.T) {
	repo := newFakeChatRepo()
	repo.sessions["ws1/sess1"] = activeSession("ws1", "u1")
	enq := &fakeEnqueuer{}
	s := NewChatService(repo, enq)

	_, taskID, err := s.SendMessage(context.Background(), "ws1", "sess1", "u1", "hello")
	require.NoError(t, err)
	assert.Equal(t, "task-new", taskID)
	assert.Equal(t, []string{"hello"}, enq.calls)
	require.Len(t, repo.inserted, 1)
	assert.Zero(t, repo.withinOpened, "two-step path opens no transaction")
}

func TestSendMessage_SteerBranchInsideTransaction(t *testing.T) {
	repo := newFakeChatRepo()
	repo.sessions["ws1/sess1"] = activeSession("ws1", "u1")
	ops := &fakeTaskOps{latestStatus: "running", latestTaskID: "t-run", latestOK: true, supplementOK: true, repo: repo}
	enqTx := newFakeEnqTx(repo)
	enq := &fakeEnqueuer{}
	s := NewChatService(repo, enq)
	s.SetChatTaskOps(ops)
	s.SetEnqueueChatTx(enqTx.fn())

	_, taskID, err := s.SendMessage(context.Background(), "ws1", "sess1", "u1", "supplement")
	require.NoError(t, err)
	assert.Equal(t, "t-run", taskID)
	assert.Equal(t, 1, ops.supplementInTx, "supplement injection inside the transaction")
	assert.Empty(t, enqTx.calls, "steering hit: does not take the enqueue port")
	require.Len(t, repo.inserted, 1)
	assert.False(t, repo.rolledBack)
}

func TestListMessages_DescPageAscendingReturn(t *testing.T) {
	repo := newFakeChatRepo()
	var gotLimit int
	repo.listMsg = func(_ string, limit int, _ int64) ([]Message, error) {
		gotLimit = limit
		return []Message{{Seq: 30}, {Seq: 20}, {Seq: 10}}, nil
	}
	s := NewChatService(repo, nil)

	out, err := s.ListMessages(context.Background(), "sess1", 500, 0)
	require.NoError(t, err)
	assert.Equal(t, 50, gotLimit, "limit>200 clamped to 50")
	require.Len(t, out, 3)
	assert.Equal(t, int64(10), out[0].Seq, "returns ascending")
	assert.Equal(t, int64(30), out[2].Seq)
}

func TestResumePointer_SetAndClear(t *testing.T) {
	repo := newFakeChatRepo()
	p := NewPointersService(repo)

	require.NoError(t, p.UpdateResumePointer(context.Background(), "sess1", "gen-7:ckpt-2"))
	assert.Equal(t, "gen-7:ckpt-2", repo.pointerSet)

	require.NoError(t, p.ClearResumePointer(context.Background(), "sess1"))
	assert.True(t, repo.pointerCleared, "explicit retirement clears (internal archive 05 §2.9 /new path)")
}

func TestAppendAssistantMessage_EmptyContentBecomesNoResponse(t *testing.T) {
	repo := newFakeChatRepo()
	p := NewPointersService(repo)

	require.NoError(t, p.AppendAssistantMessage(context.Background(), "sess1", "t1", "   ", "message"))
	require.Len(t, repo.inserted, 1)
	assert.Equal(t, "no_response", repo.inserted[0].Kind)
	assert.Empty(t, repo.inserted[0].Content)
}

func TestParseSuggestions_NoiseTolerantAndCapped(t *testing.T) {
	raw := `  Sure, suggestions below:
[{"label":"write weekly report","prompt":"write weekly report based on this week's tasks"},
 {"label":"","prompt":"missing signature should be skipped"},
 {"label":"check risks","prompt":"check this week's delay risks"},
 {"label":"schedule a meeting","prompt":"schedule the retro meeting"},
 {"label":"over limit","prompt":"the fourth should be trimmed"}] for reference`

	got := ParseSuggestions(raw)
	require.Len(t, got, 3, "cap 3 items")
	assert.Equal(t, "write weekly report", got[0].Label)
	assert.Equal(t, "check risks", got[1].Label)
	assert.Equal(t, "schedule a meeting", got[2].Label, "invalid items skipped and collection continues; the fourth trimmed for being over the limit")
}

func TestParseSuggestions_GarbageReturnsNil(t *testing.T) {
	assert.Nil(t, ParseSuggestions("completely not JSON"))
	assert.Nil(t, ParseSuggestions(""))
}

func TestMessageExists_PassesIdToRepo(t *testing.T) {
	repo := newFakeChatRepo()
	repo.inserted = []Message{{ID: "m1", SessionID: "sess1", Role: "user", Seq: 1}}
	s := NewChatService(repo, nil)

	ok, err := s.MessageExists(context.Background(), "m1")
	require.NoError(t, err)
	assert.True(t, ok)

	ok, err = s.MessageExists(context.Background(), "m-nope")
	require.NoError(t, err)
	assert.False(t, ok)
	assert.Equal(t, []string{"m1", "m-nope"}, repo.getMessage, "id passed through as-is")
}
