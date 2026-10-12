package app

// W2-1 chat:done 帧覆盖矩阵（结算侧）：completed（Settle 直完成）/ approve
// （Approve 恢复后完成）/ fail（Settle 携带 execErr）三条路径都发收尾帧、
// 都只达 creator。cancel 路径由 conversation 域 Finalize 发射（queued 直取
// 消不过 orchestration 事件，TaskCancelled 漏斗终结在会话投影侧），矩阵的
// 第四格见 internal/conversation/app/service_creator_fence_test.go。
//
// 回归锁定三件事：
//  1. 终态语义：帧 payload.Status 恒携带 completed|failed（cancel 在会话侧）；
//  2. creator 定向：bus 事件 WorkspaceID 置空（带全文的帧禁止再进
//     workspace/chat scope 广播），push 端口只打 OriginatorUserID；
//  3. 无 chat_session_id 的任务零帧；OriginatorUserID 为空只发 bus 不 push。

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"orkome/internal/contract/events"
)

type framePushRecorder struct {
	pushed []framePush
}

type framePush struct {
	user      string
	frameType string
	payload   map[string]any
}

func (f *framePushRecorder) Push(_ context.Context, userID, frameType string, payload map[string]any) {
	f.pushed = append(f.pushed, framePush{user: userID, frameType: frameType, payload: payload})
}

func chatDoneFrames(emit *recorderEmitter) []ChatDoneFrame {
	var out []ChatDoneFrame
	for _, e := range emit.emitted {
		if e.Event != events.ChatDone {
			continue
		}
		if p, ok := e.Payload.(ChatDoneFrame); ok {
			out = append(out, p)
		}
	}
	return out
}

// chatDoneFakeRepo 覆盖 complete/fail 两条结算路径所需的 Repo 面。
type chatDoneFakeRepo struct {
	Repo
	failCalled bool
}

func (f *chatDoneFakeRepo) LastTaskText(context.Context, string) (string, error) { return "", nil }

func (f *chatDoneFakeRepo) CompleteRun(context.Context, string, string, string, string, string, UsageReport) error {
	return nil
}

func (f *chatDoneFakeRepo) FailRun(context.Context, string, string, string, string) (bool, error) {
	f.failCalled = true
	return false, nil
}

func (f *chatDoneFakeRepo) SettlePendingSupplements(context.Context, string) error { return nil }

func (f *chatDoneFakeRepo) PromoteDeferred(context.Context) ([]Task, error) { return nil, nil }

func chatTask() Task {
	return Task{
		ID: "task-1", WorkspaceID: "ws-1", AgentID: "ag-1",
		OriginatorUserID: "u-creator", Status: "running",
		Context: map[string]any{"chat_session_id": "sess-1"},
	}
}

func newChatDoneSettle(repo *chatDoneFakeRepo) (*SettlementService, *recorderEmitter, *framePushRecorder) {
	emit := &recorderEmitter{}
	push := &framePushRecorder{}
	settle := NewSettlementService(repo, emit, nil, nil, nil)
	settle.SetRuntimeInstance("i-1")
	settle.SetChatDonePush(push)
	return settle, emit, push
}

func TestChatDoneFrame_CompletedPath(t *testing.T) {
	repo := &chatDoneFakeRepo{}
	settle, emit, push := newChatDoneSettle(repo)

	require.NoError(t, settle.Settle(context.Background(), chatTask(),
		Agent{ID: "ag-1", WorkspaceID: "ws-1"}, EngineOutput{Output: "the reply"}, nil))

	frames := chatDoneFrames(emit)
	require.Len(t, frames, 1, "completed 路径恰好一条 chat:done")
	f := frames[0]
	assert.Equal(t, "sess-1", f.SessionID)
	assert.Equal(t, "task-1", f.TaskID)
	assert.Equal(t, "assistant", f.Role)
	assert.Equal(t, "message", f.Kind)
	assert.Equal(t, "completed", f.Status, "终态语义字段必须携带")
	assert.Equal(t, "the reply", f.Content, "帧内容保留完整回复（与 REST 读一致）")

	require.Len(t, push.pushed, 1, "creator 定向 push 恰好一次")
	p := push.pushed[0]
	assert.Equal(t, "u-creator", p.user, "只投给会话 creator")
	assert.Equal(t, "chat:done", p.frameType)
	assert.Equal(t, "completed", p.payload["status"])
	assert.Equal(t, "the reply", p.payload["content"])

	// bus 事件脱工作区：任何订阅 workspace scope 的成员都不得再收到全文帧
	for _, e := range emit.emitted {
		if e.Event == events.ChatDone {
			assert.Empty(t, e.WorkspaceID, "chat:done 事件禁止携带 WorkspaceID（workspace 广播漏全文）")
			assert.Equal(t, "u-creator", e.ActorUserID)
		}
	}
}

func TestChatDoneFrame_ApprovePath(t *testing.T) {
	task := chatTask()
	task.Context["interrupt"] = map[string]any{"target_id": "tgt-1"}
	repo := &approveFakeRepo{task: task}
	emit := &recorderEmitter{}
	push := &framePushRecorder{}
	settle := NewSettlementService(repo, emit, nil, nil, nil)
	settle.SetApprovalDeps(nil, &approveFakeEngine{out: "resumed result"},
		func(context.Context, string, string) (string, error) { return "", nil })
	settle.SetChatDonePush(push)

	require.NoError(t, settle.Approve(context.Background(), "ws-1", "task-1", "u-approver", true, ""))

	frames := chatDoneFrames(emit)
	require.Len(t, frames, 1, "approve 恢复后完成同样发收尾帧")
	assert.Equal(t, "completed", frames[0].Status)
	assert.Equal(t, "resumed result", frames[0].Content)

	require.Len(t, push.pushed, 1)
	assert.Equal(t, "u-creator", push.pushed[0].user,
		"帧只达 creator（originator），即使审批人是别人也不外泄回复全文")
	assert.Equal(t, "completed", push.pushed[0].payload["status"])
}

func TestChatDoneFrame_FailPath(t *testing.T) {
	repo := &chatDoneFakeRepo{}
	settle, emit, push := newChatDoneSettle(repo)

	require.NoError(t, settle.Settle(context.Background(), chatTask(),
		Agent{ID: "ag-1", WorkspaceID: "ws-1"}, EngineOutput{}, errors.New("engine boom")))

	require.True(t, repo.failCalled, "fail 结算已落库")
	frames := chatDoneFrames(emit)
	require.Len(t, frames, 1, "fail 路径补发收尾帧（此前只发 task:failed，聊天页等不到收尾）")
	f := frames[0]
	assert.Equal(t, "failed", f.Status)
	assert.Equal(t, "no_response", f.Kind, "失败无回复，kind 走空内容规则（与落库空 assistant 行一致）")
	assert.Empty(t, f.Content)

	require.Len(t, push.pushed, 1)
	assert.Equal(t, "u-creator", push.pushed[0].user)
	assert.Equal(t, "failed", push.pushed[0].payload["status"])
	assert.Equal(t, "no_response", push.pushed[0].payload["kind"])
}

func TestChatDoneFrame_NoChatSessionOrNoOriginator(t *testing.T) {
	// 无 chat_session_id：普通任务零帧、零 push。
	repo := &chatDoneFakeRepo{}
	settle, emit, push := newChatDoneSettle(repo)
	plain := Task{ID: "task-2", WorkspaceID: "ws-1", AgentID: "ag-1",
		OriginatorUserID: "u-creator", Status: "running"}
	require.NoError(t, settle.Settle(context.Background(), plain,
		Agent{ID: "ag-1", WorkspaceID: "ws-1"}, EngineOutput{Output: "x"}, nil))
	assert.Empty(t, chatDoneFrames(emit))
	assert.Empty(t, push.pushed)

	// originator 为空：bus 事件保住（QA 建议生成器按 payload 消费），push 跳过。
	repo2 := &chatDoneFakeRepo{}
	settle2, emit2, push2 := newChatDoneSettle(repo2)
	anon := chatTask()
	anon.OriginatorUserID = ""
	require.NoError(t, settle2.Settle(context.Background(), anon,
		Agent{ID: "ag-1", WorkspaceID: "ws-1"}, EngineOutput{Output: "y"}, nil))
	require.Len(t, chatDoneFrames(emit2), 1)
	assert.Empty(t, push2.pushed, "无收件人不 push")
}
