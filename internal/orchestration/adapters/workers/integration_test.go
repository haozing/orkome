package workers

import (
	"context"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cloudwego/eino/schema"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/haozing/ploykit/platform/logx"
	"github.com/haozing/ploykit/platform/pg"
	pgmigrate "github.com/haozing/ploykit/platform/pgmigrate"
	"orkome/internal/contract/dispatchreason"
	convpgrepo "orkome/internal/conversation/adapters/pgrepo"
	convapp "orkome/internal/conversation/app"
	idpgrepo "orkome/internal/identity/adapters/pgrepo"
	notifpgrepo "orkome/internal/notification/adapters/pgrepo"
	"orkome/internal/orchestration/adapters/engine"
	orchpgrepo "orkome/internal/orchestration/adapters/pgrepo"
	orchapp "orkome/internal/orchestration/app"
	"orkome/internal/orchestration/domain"
	"orkome/internal/platform/eventbus"
	workpgrepo "orkome/internal/work/adapters/pgrepo"
	workapp "orkome/internal/work/app"
	"orkome/migrations"
)

type env struct {
	conv      *convapp.ChatService
	convRepo  *convpgrepo.Repo
	pointers  *convapp.PointersService
	t         *testing.T
	pool      *pgxpool.Pool
	db        *pg.DB
	bus       *eventbus.Bus
	repo      *orchpgrepo.Repo
	agents    *orchapp.AgentService
	enq       *orchapp.EnqueueService
	settle    *orchapp.SettlementService
	wRepo     *workpgrepo.Repo
	comments  *workapp.CommentService
	engine    *fakeOrRealEngine
	mu        sync.Mutex
	fakeReply atomic.Value
	toolsMode bool
}

type triggerAdapter struct{ trig *orchapp.TriggerService }

func (a triggerAdapter) Preview(ctx context.Context, in workapp.TriggerInput) ([]workapp.PreviewHit, []workapp.PreviewHit, error) {
	out, err := a.trig.Preview(ctx, orchapp.CommentInput{
		WorkspaceID: in.WorkspaceID, WorkItemID: in.WorkItemID,
		AuthorType: in.AuthorType, AuthorID: in.AuthorID, Content: in.Content,
		ParentAuthorType: in.ParentAuthorType, ParentAuthorID: in.ParentAuthorID,
		RootAuthorType: in.RootAuthorType, RootAuthorID: in.RootAuthorID,
		AssigneeType: in.AssigneeType, AssigneeID: in.AssigneeID,
	})
	if err != nil {
		return nil, nil, err
	}
	var tg, sp []workapp.PreviewHit
	for _, h := range out.Triggers {
		tg = append(tg, workapp.PreviewHit{AgentID: h.AgentID, Reason: h.Reason})
	}
	for _, h := range out.Suppressions {
		sp = append(sp, workapp.PreviewHit{AgentID: h.AgentID, Reason: h.Reason})
	}
	return tg, sp, nil
}

func (a triggerAdapter) OnComment(ctx context.Context, in workapp.TriggerInput, commentID string) (string, string, error) {
	return a.trig.OnComment(ctx, orchapp.CommentInput{
		WorkspaceID: in.WorkspaceID, WorkItemID: in.WorkItemID,
		AuthorType: in.AuthorType, AuthorID: in.AuthorID, Content: in.Content,
		ParentAuthorType: in.ParentAuthorType, ParentAuthorID: in.ParentAuthorID,
		RootAuthorType: in.RootAuthorType, RootAuthorID: in.RootAuthorID,
		AssigneeType: in.AssigneeType, AssigneeID: in.AssigneeID,
	}, commentID)
}

type fakeOrRealEngine struct {
	inner *engine.EinoEngine
	env   *env
}

func (f *fakeOrRealEngine) Name() string { return f.inner.Name() }

func (f *fakeOrRealEngine) Resume(ctx context.Context, task orchapp.Task, agent orchapp.Agent, secret, targetID string, decision orchapp.ApprovalDecision) (orchapp.EngineOutput, error) {
	if !f.env.toolsMode {
		f.env.registerFake()
	}
	return f.inner.Resume(ctx, task, agent, secret, targetID, decision)
}

func (f *fakeOrRealEngine) Generate(ctx context.Context, agent orchapp.Agent, secret, system, input string) (string, orchapp.UsageReport, error) {
	if !f.env.toolsMode {
		f.env.registerFake()
	}
	return f.inner.Generate(ctx, agent, secret, system, input)
}

func (f *fakeOrRealEngine) Execute(ctx context.Context, task orchapp.Task, agent orchapp.Agent, secret string) (orchapp.EngineOutput, error) {
	if !f.env.toolsMode {
		f.env.registerFake()
	}
	return f.inner.Execute(ctx, task, agent, secret)
}

func (e *env) SetReply(fn func(prompt string) (content string, promptTok, completionTok int, err error)) {
	e.fakeReply.Store(fn)
}

func (e *env) registerFake() {
	v := e.fakeReply.Load()
	fn, _ := v.(func(string) (string, int, int, error))
	engine.RegisterFakeProviderForTest(func(prompt string) (string, schema.TokenUsage, error) {
		c, p, cm, err := fn(prompt)
		return c, schema.TokenUsage{PromptTokens: p, CompletionTokens: cm}, err
	})
}

func newEnv(t *testing.T) *env {
	base := os.Getenv("TEST_DATABASE_URL")
	if base == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	admin, err := pgx.Connect(context.Background(), base)
	require.NoError(t, err)
	_, _ = admin.Exec(context.Background(), "DROP DATABASE IF EXISTS orkome_m3")
	_, _ = admin.Exec(context.Background(), "CREATE DATABASE orkome_m3")
	admin.Close(context.Background())
	dsn := strings.Replace(base, "/orkome", "/orkome_m3", 1)

	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	require.NoError(t, err)
	t.Cleanup(pool.Close)
	m := pgmigrate.New(pool, migrations.FS, ".")
	_, err = m.Up(ctx, 0)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `TRUNCATE channel_chat_session_binding, channel_inbound_message_dedup, channel_installation, attachment, agent_skill, skill_file, skill, notification_preference, event_outbox, notification, agent_task_checkpoint, agent_model_credential, task_usage,
		task_supplement, agent_task_queue, agent_runtime, squad_member, squad, agent_mcp_server,
		workspace_mcp_server, agent_invocation_target, agent,
		activity_log, comment_reaction, comment, work_item_source_context, work_item_reaction,
		work_item_child_event, work_item_dependency, work_item_subscriber, work_item_view_preference,
		work_item_view, work_item_to_label, label, work_item_property_def, work_item, project_resource,
		project, work_item_status, pinned_item, personal_access_token, workspace_share_link,
		workspace_invitation, verification_code, member, workspace, "user" RESTART IDENTITY CASCADE`)
	require.NoError(t, err)

	db := pg.WrapPool(pool)
	log := logx.New(logx.Options{Level: "error"})
	bus := eventbus.New(log)

	e := &env{t: t, pool: pool, db: db, bus: bus}
	e.repo = orchpgrepo.New(db)
	e.engine = &fakeOrRealEngine{inner: &engine.EinoEngine{}, env: e}
	e.engine.inner.ChatHistory = func(ctx context.Context, sessionID string, limit int) ([]engine.ChatTurn, error) {
		ms, err := e.convRepo.ListMessagesForTest(sessionID)
		if err != nil {
			return nil, err
		}

		var out []engine.ChatTurn
		for _, m := range ms {
			if m.Kind == "message" || m.Kind == "no_response" {
				out = append(out, engine.ChatTurn{Role: m.Role, Content: m.Content})
			}
		}
		if len(out) > limit {
			out = out[len(out)-limit:]
		}
		return out, nil
	}
	e.SetReply(func(string) (string, int, int, error) { return "Default conclusion.", 100, 50, nil })
	e.registerFake()

	e.agents = orchapp.NewAgentService(e.repo, bus)
	e.enq = orchapp.NewEnqueueService(e.repo, bus)
	notification := notifpgrepo.New(db)
	e.wRepo = &workpgrepo.Repo{DB: db}
	convRepo := convpgrepo.New(db)
	e.convRepo = convRepo
	e.settle = orchapp.NewSettlementService(e.repo, bus, notification, workCommentWriter{wRepo: e.wRepo}, chatWriterAdapter{repo: convRepo})

	e.comments = workapp.NewCommentService(e.wRepo, bus, time.Now)
	workapp.SetTriggerEngine(triggerAdapter{trig: orchapp.NewTriggerService(e.repo, bus)})
	e.conv = convapp.NewChatService(convRepo, chatEnqueuerAdapter{enq: e.enq, repo: e.repo})
	e.pointers = convapp.NewPointersService(convRepo)
	orchapp.SetWorkItemReader(workReader{wRepo: e.wRepo})
	return e
}

type workCommentWriter struct{ wRepo *workpgrepo.Repo }

func (w workCommentWriter) WriteAgentComment(ctx context.Context, wsID, wiID, agentID, content string) error {
	_, err := w.wRepo.CreateComment(ctx, workapp.NewComment{
		WorkItemID: wiID, WorkspaceID: wsID, AuthorType: "agent", AuthorID: agentID, Content: content,
	})
	return err
}

func (w workCommentWriter) HasAgentComment(ctx context.Context, wiID string) (bool, error) {

	rows, err := w.wRepo.ListComments(ctx, wiID)
	if err != nil {
		return false, err
	}
	for _, c := range rows {
		if c.AuthorType == "agent" {
			return true, nil
		}
	}
	return false, nil
}

type workReader struct{ wRepo *workpgrepo.Repo }

func (r workReader) GetWorkItem(ctx context.Context, ws, id string) (orchapp.WorkItemInfo, bool, error) {
	wi, ok, err := r.wRepo.GetWorkItem(ctx, ws, id)
	return orchapp.WorkItemInfo{ID: wi.ID, Title: wi.Title, Desc: wi.Description}, ok, err
}

func (e *env) createUser(t *testing.T, email string) string {
	ctx := context.Background()
	u, err := idpgrepo.New(e.db).UpsertUserByEmail(ctx, email)
	require.NoError(t, err)
	return u.ID
}

func (e *env) createWS(t *testing.T, ownerUserID, slug string) string {
	ctx := context.Background()
	ws, _, err := idpgrepo.New(e.db).CreateWorkspace(ctx, "WS-"+slug, slug, ownerUserID)
	require.NoError(t, err)
	return ws.ID
}

func (e *env) createWorkItem(t *testing.T, wsID, creatorID, title string) string {
	wi, err := e.wRepo.CreateWorkItem(ctx0(), workapp.NewWorkItem{
		WorkspaceID: wsID, Title: title, CreatorType: "user", CreatorID: creatorID,
	})
	require.NoError(t, err)
	return wi.ID
}

func ctx0() context.Context { return context.Background() }

func (e *env) agentWithRuntime(t *testing.T, wsID string, rc map[string]any) orchapp.Agent {
	a, err := e.agents.Create(ctx0(), orchapp.Agent{
		WorkspaceID: wsID, Name: "worker-" + rand4(), RuntimeConfig: rc,
	})
	require.NoError(t, err)
	return *a
}

func rand4() string { return time.Now().Format("050000.000000000")[7:] }

func waitFor(t *testing.T, cond func() bool, what string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("wait timeout: %s", what)
}

func TestM3_GoldenPath_AssignToAgentRunsToCompletion(t *testing.T) {
	e := newEnv(t)
	ctx := ctx0()
	user := e.createUser(t, "m3@example.com")
	ws := e.createWS(t, user, "m3-golden")
	wi := e.createWorkItem(t, ws, user, "help me research X")
	agent := e.agentWithRuntime(t, ws, map[string]any{"provider": "fake", "model": "fake-1"})

	task, admission, err := e.enq.EnqueueAssignment(ctx, ws, wi, agent.ID, user)
	require.NoError(t, err)
	require.Equal(t, dispatchreason.Queued, admission)
	require.Equal(t, "queued", task.Status)

	assert.Equal(t, user, task.OriginatorUserID)
	assert.Equal(t, user, task.AccountableUserID)

	d := &Dispatcher{
		Repo: e.repo, Claim: orchapp.NewClaimService(e.repo), Settle: e.settle,
		Engine: e.engine, Log: logx.New(logx.Options{Level: "error"}), Emit: e.bus,
		PollEvery: 20 * time.Millisecond, Semaphore: 2, Timeout: 10 * time.Second,
		CredFn: func(ctx context.Context, wsID, credID string) (string, error) { return "", nil },
	}
	dctx, cancel := context.WithCancel(ctx)
	go d.Run(dctx)
	defer cancel()

	waitFor(t, func() bool {
		tt, ok, _ := e.repo.GetTask(ctx, ws, task.ID)
		return ok && tt.Status == "completed"
	}, "task should reach completed")

	tt, _, _ := e.repo.GetTask(ctx, ws, task.ID)
	assert.Equal(t, "Default conclusion.", tt.Result)

	waitFor(t, func() bool {
		comments, _ := e.wRepo.ListComments(ctx, wi)
		for _, c := range comments {
			if c.AuthorType == "agent" && c.Content == "Default conclusion." {
				return true
			}
		}
		return false
	}, "an agent fallback comment should be synthesized")

	var pt, ct int64
	err = e.pool.QueryRow(ctx,
		`SELECT prompt_tokens, completion_tokens FROM task_usage WHERE task_id=$1`, task.ID).Scan(&pt, &ct)
	require.NoError(t, err, "usage row should exist")
	assert.EqualValues(t, 100, pt)
	assert.EqualValues(t, 50, ct)

	waitFor(t, func() bool {
		var n int
		err := e.pool.QueryRow(ctx,
			`SELECT count(*) FROM notification WHERE recipient_user_id=$1 AND work_item_id=$2`, user, wi).Scan(&n)
		return err == nil && n >= 1
	}, "one done notification should be written")

}

func TestM3_Invariant_I1_PendingFence(t *testing.T) {
	e := newEnv(t)
	user := e.createUser(t, "i1@example.com")
	ws := e.createWS(t, user, "m3-i1")
	wi := e.createWorkItem(t, ws, user, "I1")
	a1 := e.agentWithRuntime(t, ws, map[string]any{"provider": "fake", "model": "m"})

	_, adm, err := e.enq.EnqueueAssignment(ctx0(), ws, wi, a1.ID, user)
	require.NoError(t, err)
	require.Equal(t, dispatchreason.Queued, adm)

	_, adm2, err := e.enq.EnqueueAssignment(ctx0(), ws, wi, a1.ID, user)
	require.NoError(t, err)
	assert.Equal(t, dispatchreason.AlreadyActive, adm2, "I1: a second pending on the same (work item,agent) should AlreadyActive")

	a2 := e.agentWithRuntime(t, ws, map[string]any{"provider": "fake", "model": "m"})
	_, adm3, err := e.enq.EnqueueAssignment(ctx0(), ws, wi, a2.ID, user)
	require.NoError(t, err)
	assert.Equal(t, dispatchreason.Queued, adm3)
}

func TestM3_Invariant_I2_SettlementCAS(t *testing.T) {
	e := newEnv(t)
	user := e.createUser(t, "i2@example.com")
	ws := e.createWS(t, user, "m3-i2")
	wi := e.createWorkItem(t, ws, user, "I2")
	a := e.agentWithRuntime(t, ws, map[string]any{"provider": "fake", "model": "m"})
	task, _, err := e.enq.EnqueueAssignment(ctx0(), ws, wi, a.ID, user)
	require.NoError(t, err)

	_, ok, _ := e.repo.ClaimNext(ctx0(), a.ID, 6)
	require.True(t, ok)
	_, err = e.repo.StartTask(ctx0(), task.ID, "", "")
	require.NoError(t, err)

	u := orchapp.UsageReport{Provider: "fake", Model: "m", PromptTokens: 1, CompletionTokens: 1}
	require.NoError(t, e.repo.CompleteRun(ctx0(), task.ID, "r1", "", "", u))
	err = e.repo.CompleteRun(ctx0(), task.ID, "r2", "", "", u)
	assert.Error(t, err, "I2: second CAS should affect 0 rows (idempotent rejection)")

	var result string
	_ = e.pool.QueryRow(ctx0(), `SELECT result FROM agent_task_queue WHERE id=$1`, task.ID).Scan(&result)
	assert.Equal(t, "r1", result, "terminal results cannot be rewritten by a second settlement")

	var usageRows int
	_ = e.pool.QueryRow(ctx0(), `SELECT count(*) FROM task_usage WHERE task_id=$1`, task.ID).Scan(&usageRows)
	assert.Equal(t, 1, usageRows, "exactly one usage row")
}

func TestM3_ModelNotConfigured_FailFast(t *testing.T) {
	e := newEnv(t)
	user := e.createUser(t, "nc@example.com")
	ws := e.createWS(t, user, "m3-nc")
	wi := e.createWorkItem(t, ws, user, "NC")
	a := e.agentWithRuntime(t, ws, map[string]any{"provider": "nope", "model": ""})

	_, adm, err := e.enq.EnqueueAssignment(ctx0(), ws, wi, a.ID, user)
	require.NoError(t, err)
	assert.Equal(t, dispatchreason.ModelNotConfigured, adm, "missing model should fail-fast at enqueue")

	a2 := e.agentWithRuntime(t, ws, map[string]any{"provider": "fake", "model": "m"})
	require.NoError(t, e.repo.ArchiveAgent(ctx0(), ws, a2.ID, true))
	wi2 := e.createWorkItem(t, ws, user, "NC2")
	_, adm2, err := e.enq.EnqueueAssignment(ctx0(), ws, wi2, a2.ID, user)
	require.NoError(t, err)
	assert.Equal(t, dispatchreason.Blocked, adm2)
}

func TestM3_ClaimRace_TwoDispatchers(t *testing.T) {
	e := newEnv(t)
	user := e.createUser(t, "race@example.com")
	ws := e.createWS(t, user, "m3-race")
	wi := e.createWorkItem(t, ws, user, "race")
	a := e.agentWithRuntime(t, ws, map[string]any{"provider": "fake", "model": "m"})
	task, _, err := e.enq.EnqueueAssignment(ctx0(), ws, wi, a.ID, user)
	require.NoError(t, err)

	var wg sync.WaitGroup
	var won atomic.Int32
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, ok, err := e.repo.ClaimNext(ctx0(), a.ID, 6); err == nil && ok {
				won.Add(1)
			}
		}()
	}
	wg.Wait()
	assert.Equal(t, int32(1), won.Load(), "dual-worker race wins exactly one")

	n, err := e.repo.RecoverDispatched(ctx0())
	require.NoError(t, err)
	assert.Equal(t, 1, n)
	_, ok, _ := e.repo.ClaimNext(ctx0(), a.ID, 6)
	assert.True(t, ok)
	_ = task.ID
}

func TestM3_FailRun_RetryChildSameTx(t *testing.T) {
	e := newEnv(t)
	user := e.createUser(t, "fail@example.com")
	ws := e.createWS(t, user, "m3-fail")
	wi := e.createWorkItem(t, ws, user, "fail")
	a := e.agentWithRuntime(t, ws, map[string]any{"provider": "fake", "model": "m"})
	task, _, err := e.enq.EnqueueAssignment(ctx0(), ws, wi, a.ID, user)
	require.NoError(t, err)
	_, ok, _ := e.repo.ClaimNext(ctx0(), a.ID, 6)
	require.True(t, ok)
	_, err = e.repo.StartTask(ctx0(), task.ID, "", "")
	require.NoError(t, err)

	retried, err := e.repo.FailRun(ctx0(), task.ID, "agent_provider.network", "dial tcp: refused")
	require.NoError(t, err)
	assert.True(t, retried, "allowlisted reasons should create a retry subtask")

	var child orchapp.Task
	err = e.pool.QueryRow(ctx0(), `
SELECT id, attempt, retry_of_task_id FROM agent_task_queue WHERE retry_of_task_id=$1`, task.ID).
		Scan(&child.ID, &child.Attempt, &child.RetryOfID)
	require.NoError(t, err)
	assert.Equal(t, 2, child.Attempt)

	var childFireAt *time.Time
	require.NoError(t, e.pool.QueryRow(ctx0(),
		`SELECT fire_at FROM agent_task_queue WHERE id=$1`, child.ID).Scan(&childFireAt))
	assert.Nil(t, childFireAt, "second segment retries immediately: fire_at is NULL")
	childTask, cok, _ := e.repo.ClaimNext(ctx0(), a.ID, 6)
	require.True(t, cok)
	assert.Equal(t, child.ID, childTask.ID, "retry subtask with NULL fire_at claimable immediately")
	_, err = e.repo.StartTask(ctx0(), childTask.ID, "", "")
	require.NoError(t, err)
	retried2nd, err := e.repo.FailRun(ctx0(), childTask.ID, "agent_provider.network", "dial tcp: refused again")
	require.NoError(t, err)
	assert.True(t, retried2nd, "provider family relaxed to 3 (internal archive 05 §2.8): attempt=2 still retryable")
	var grandchildID string
	var grandAttempt int
	var grandFireAt *time.Time
	err = e.pool.QueryRow(ctx0(), `
SELECT id, attempt, fire_at FROM agent_task_queue WHERE retry_of_task_id=$1`, childTask.ID).
		Scan(&grandchildID, &grandAttempt, &grandFireAt)
	require.NoError(t, err)
	assert.Equal(t, 3, grandAttempt)
	require.NotNil(t, grandFireAt, "third segment (last) created lazily: fire_at non-null")
	assert.WithinDuration(t, time.Now().Add(domain.RetryFinalDelay), *grandFireAt, 2*time.Second,
		"delay about 5s (domain.RetryFinalDelay)")
	_, delayed, _ := e.repo.ClaimNext(ctx0(), a.ID, 6)
	assert.False(t, delayed, "third segment with fire_at not due not claimable (ClaimNext gating)")

	_, err = e.pool.Exec(ctx0(), `UPDATE agent_task_queue SET fire_at=now() WHERE id=$1`, grandchildID)
	require.NoError(t, err)
	gc, gcok, _ := e.repo.ClaimNext(ctx0(), a.ID, 6)
	require.True(t, gcok)
	assert.Equal(t, grandchildID, gc.ID, "third segment claimable after fire_at due")

	wi2 := e.createWorkItem(t, ws, user, "fail2")
	t2, _, _ := e.enq.EnqueueAssignment(ctx0(), ws, wi2, a.ID, user)
	_, ok, _ = e.repo.ClaimNext(ctx0(), a.ID, 6)
	require.True(t, ok)
	_, _ = e.repo.StartTask(ctx0(), t2.ID, "", "")
	retried2, err := e.repo.FailRun(ctx0(), t2.ID, "interrupt_timeout", "24h due")
	require.NoError(t, err)
	assert.False(t, retried2, "interrupt_timeout never retries (internal archive 09 §10.4)")
}

func TestM3_Cancel(t *testing.T) {
	e := newEnv(t)
	user := e.createUser(t, "cx@example.com")
	ws := e.createWS(t, user, "m3-cx")
	wi := e.createWorkItem(t, ws, user, "cx")
	a := e.agentWithRuntime(t, ws, map[string]any{"provider": "fake", "model": "m"})
	task, _, _ := e.enq.EnqueueAssignment(ctx0(), ws, wi, a.ID, user)

	ok, err := e.settle.Cancel(ctx0(), ws, task.ID)
	require.NoError(t, err)
	assert.True(t, ok)

	ok2, _ := e.settle.Cancel(ctx0(), ws, task.ID)
	assert.False(t, ok2, "cancelling after terminal state should affect 0 rows")
}

func (e *env) listMessages(sessionID string) ([]convapp.Message, error) {
	return e.convRepo.ListMessagesForTest(sessionID)
}

type chatWriterAdapter struct{ repo *convpgrepo.Repo }

func (a chatWriterAdapter) AppendAssistantMessage(ctx context.Context, sessionID, taskID, content, kind string) error {
	ps := convapp.NewPointersService(a.repo)
	return ps.AppendAssistantMessage(ctx, sessionID, taskID, content, kind)
}

func (a chatWriterAdapter) AppendAssistantParts(ctx context.Context, sessionID, taskID, content, kind string, parts []orchapp.ContentPart) error {
	ps := convapp.NewPointersService(a.repo)
	pm := make([]map[string]any, len(parts))
	for i, p := range parts {
		pm[i] = map[string]any{"type": p.Type, "text": p.Text, "url": p.URL}
	}
	return ps.AppendAssistantParts(ctx, sessionID, taskID, content, kind, pm)
}
func (a chatWriterAdapter) UpdateResumePointer(ctx context.Context, sessionID, ref string) error {
	return a.repo.UpdateResumePointer(ctx, sessionID, ref)
}

func (a chatWriterAdapter) ClearResumePointer(ctx context.Context, sessionID string) error {
	_, err := a.repo.DB.Tx(ctx).Exec(ctx,
		`UPDATE chat_session SET session_ref=NULL, updated_at=now() WHERE id=$1`, sessionID)
	return err
}

type chatEnqueuerAdapter struct {
	enq  *orchapp.EnqueueService
	repo orchapp.Repo
}

func (a chatEnqueuerAdapter) EnqueueChat(ctx context.Context, wsID, sessionID, agentID, userID, content string) (string, error) {
	return a.enq.EnqueueChat(ctx, wsID, sessionID, agentID, userID, content)
}

type taskCancellerAdapter struct{ settle *orchapp.SettlementService }

func (a taskCancellerAdapter) CancelChatTask(ctx context.Context, wsID, sessionID string) (string, string, bool, error) {
	return a.settle.CancelChatTask(ctx, wsID, sessionID)
}

func (e *env) setToolsScript(step func(hist []*schema.Message) (engine.FakeStep, schema.TokenUsage, error)) {
	e.toolsMode = true
	engine.RegisterFakeProviderToolsForTest(step)
}
