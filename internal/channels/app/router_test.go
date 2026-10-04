package app

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"orkome/internal/channels/domain"
	"orkome/internal/channels/wire"
)

type fakeRepo struct {
	mu sync.Mutex

	installations map[string]Installation
	dedup         map[string]*fakeDedupRow
	audits        []DropAudit
	bindings      map[string]string
	tokens        map[string]string
	routes        map[string]Route
	generations   map[string]int64
	genRows       int
	media         map[string]*fakeMediaRow
	taskStamps    map[string]TaskDelivery
	outbound      []OutboundRecord
	settled       []string

	armed    map[string]*fakeArmedRow
	armedSeq int

	replyPhase   map[string]string
	placeholder  map[string]string
	terminalIDs  []string
	cardRows     map[string]CardMessage
	finishedCard []string

	titleUpdates []string

	nextRouteConflict bool
}

type fakeDedupRow struct {
	state, token string
}

type fakeMediaRow struct {
	state       string
	leaseToken  string
	leaseExpiry time.Time
	attempt     int
	pass        int
	intent      MediaIntent
}

type fakeArmedRow struct {
	snap  ArmedPendingContext
	token string
}

func newFakeRepo() *fakeRepo {
	return &fakeRepo{
		installations: map[string]Installation{},
		dedup:         map[string]*fakeDedupRow{},
		bindings:      map[string]string{},
		tokens:        map[string]string{},
		routes:        map[string]Route{},
		generations:   map[string]int64{},
		media:         map[string]*fakeMediaRow{},
		taskStamps:    map[string]TaskDelivery{},
		armed:         map[string]*fakeArmedRow{},
		replyPhase:    map[string]string{},
		placeholder:   map[string]string{},
		cardRows:      map[string]CardMessage{},
	}
}

func (f *fakeRepo) ResolveInstallation(_ context.Context, kind wire.Kind, routingKey string) (Installation, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, inst := range f.installations {
		if inst.Kind == kind && routingKeyOf(inst) == routingKey {
			return inst, nil
		}
	}
	return Installation{}, ErrInstallationNotFound
}

func routingKeyOf(inst Installation) string {
	var cfg map[string]any
	_ = json.Unmarshal(inst.Config, &cfg)
	s, _ := cfg["routing_key"].(string)
	return s
}

func (f *fakeRepo) ListActiveInstallations(context.Context) ([]Installation, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []Installation
	for _, inst := range f.installations {
		if inst.Status == "active" {
			out = append(out, inst)
		}
	}
	return out, nil
}

func (f *fakeRepo) ClaimInbound(_ context.Context, instID, msgID string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	provider := string(f.installations[instID].Kind)
	key := provider + "|" + msgID
	row, ok := f.dedup[key]
	if !ok {
		row = &fakeDedupRow{state: domain.DedupClaimed, token: "tok-" + msgID}
		f.dedup[key] = row
		return row.token, nil
	}
	if row.state == domain.DedupSeen {
		row.state = domain.DedupClaimed
		row.token = "tok2-" + msgID
		return row.token, nil
	}
	return "", ErrDuplicateInbound
}

func (f *fakeRepo) MarkInboundProcessed(_ context.Context, instID, msgID, tok string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if row, ok := f.dedup[string(f.installations[instID].Kind)+"|"+msgID]; ok && row.token == tok {
		row.state = domain.DedupProcessed
	}
	return nil
}

func (f *fakeRepo) ReleaseInbound(_ context.Context, instID, msgID, tok string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if row, ok := f.dedup[string(f.installations[instID].Kind)+"|"+msgID]; ok && row.token == tok {
		row.state = domain.DedupSeen
	}
	return nil
}

func (f *fakeRepo) dedupState(instID, msgID string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.dedup[string(f.installations[instID].Kind)+"|"+msgID].state
}

func (f *fakeRepo) CleanStaleInbound(context.Context, time.Time) (int64, error) { return 0, nil }

func (f *fakeRepo) RecordDrop(_ context.Context, rec DropAudit) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.audits = append(f.audits, rec)
	return nil
}

func (f *fakeRepo) ResolveSender(_ context.Context, instID, extUser string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if uid, ok := f.bindings[instID+"|"+extUser]; ok {
		return uid, nil
	}
	return "", ErrSenderUnbound
}

func (f *fakeRepo) IssueBindingToken(_ context.Context, instID, extUser string, _ time.Duration) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	tok := "bind-" + extUser
	f.tokens[tok] = instID + "|" + extUser
	return tok, nil
}

func (f *fakeRepo) ConsumeBindingToken(_ context.Context, _, token, userID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	key, ok := f.tokens[token]
	if !ok {
		return ErrBindingTokenInvalid
	}
	f.bindings[key] = userID
	delete(f.tokens, token)
	return nil
}

func (f *fakeRepo) GetRoute(_ context.Context, instID, extChat string) (Route, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	rt, ok := f.routes[instID+"|"+extChat]
	return rt, ok, nil
}

func (f *fakeRepo) InsertRoute(_ context.Context, rt Route) (Route, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	key := rt.InstallationID + "|" + rt.ExternalChatID
	if _, exists := f.routes[key]; exists {
		if f.nextRouteConflict {
			f.nextRouteConflict = false
			return Route{}, ErrRouteConflict
		}
		return Route{}, ErrRouteConflict
	}
	rt.ID = "route-" + key
	rt.Generation, rt.ContextRevision = 1, 1
	f.routes[key] = rt
	f.generations[rt.ChatSessionID] = 1
	f.genRows++
	return rt, nil
}

func (f *fakeRepo) RotateRoute(_ context.Context, instID, extChat, newSession string) (Route, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	key := instID + "|" + extChat
	rt, ok := f.routes[key]
	if !ok {
		rt = Route{ID: "route-" + key, InstallationID: instID, ExternalChatID: extChat}
	}
	rt.ChatSessionID = newSession
	rt.Generation++
	rt.ContextRevision = 1
	f.routes[key] = rt
	f.generations[newSession] = 1
	f.genRows++
	return rt, nil
}

func (f *fakeRepo) TouchRouteMessage(_ context.Context, instID, extChat, msgID, threadID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if rt, ok := f.routes[instID+"|"+extChat]; ok {
		rt.LastMessageID, rt.LastThreadID = msgID, threadID
		f.routes[instID+"|"+extChat] = rt
	}
	return nil
}

func (f *fakeRepo) CurrentGeneration(_ context.Context, sessionID string) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if rev, ok := f.generations[sessionID]; ok {
		return rev, nil
	}
	return 1, nil
}

func (f *fakeRepo) NextGeneration(_ context.Context, sessionID, _ string) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.generations[sessionID]++
	f.genRows++

	for k, rt := range f.routes {
		if rt.ChatSessionID == sessionID {
			rt.ContextRevision = f.generations[sessionID]
			f.routes[k] = rt
		}
	}
	return f.generations[sessionID], nil
}

func armedKey(sessionID string, revision int64) string {
	return sessionID + "|" + strconv.FormatInt(revision, 10)
}

func (f *fakeRepo) ArmPendingRun(_ context.Context, snap ArmedPendingContext) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.armedSeq++
	token := "arm-" + strconv.Itoa(f.armedSeq)
	snap.Token = ""
	f.armed[armedKey(snap.SessionID, snap.ContextRevision)] = &fakeArmedRow{snap: snap, token: token}
	return token, nil
}

func (f *fakeRepo) ClearPendingRun(_ context.Context, sessionID string, revision int64, token string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if row, ok := f.armed[armedKey(sessionID, revision)]; ok && row.token == token {
		delete(f.armed, armedKey(sessionID, revision))
	}
	return nil
}

func (f *fakeRepo) ListArmedPendingContexts(_ context.Context) ([]ArmedPendingContext, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	keys := make([]string, 0, len(f.armed))
	for k := range f.armed {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]ArmedPendingContext, 0, len(keys))
	for _, k := range keys {
		snap := f.armed[k].snap
		snap.Token = f.armed[k].token
		out = append(out, snap)
	}
	return out, nil
}

func (f *fakeRepo) armedCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.armed)
}

func (f *fakeRepo) InsertMediaIntents(_ context.Context, intents []MediaIntent) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, in := range intents {
		if _, ok := f.media[in.StorageKey]; !ok {
			f.media[in.StorageKey] = &fakeMediaRow{state: in.State, intent: in}
		}
	}
	return nil
}

func (f *fakeRepo) BindMediaToMessage(_ context.Context, _ string, keys []string) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, k := range keys {
		if row, ok := f.media[k]; ok && row.state == domain.MediaPending {
			delete(f.media, k)
			n++
		}
	}
	return n, nil
}

func (f *fakeRepo) ListDueMedia(_ context.Context, now time.Time, _ int) ([]MediaIntent, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []MediaIntent
	for _, row := range f.media {
		if row.intent.NextAttemptAt.After(now) {
			continue
		}
		if row.state == domain.MediaDeleting && row.leaseExpiry.After(now) {
			continue
		}
		out = append(out, row.intent)
	}
	return out, nil
}

func (f *fakeRepo) ClaimMediaIntent(_ context.Context, key, lease string, exp time.Time) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	row, ok := f.media[key]
	if !ok {
		return false, nil
	}
	if row.state == domain.MediaPending ||
		(row.leaseToken != "" && row.leaseExpiry.Before(time.Now())) ||
		(row.state == domain.MediaTombstoned && row.intent.NextAttemptAt.Before(time.Now())) {
		row.state = domain.MediaDeleting
		row.leaseToken, row.leaseExpiry = lease, exp
		row.attempt++
		return true, nil
	}
	return false, nil
}

func (f *fakeRepo) SettleMediaIntent(_ context.Context, key, lease string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if row, ok := f.media[key]; ok && row.leaseToken == lease {
		delete(f.media, key)
	}
	return nil
}

func (f *fakeRepo) MarkMediaTombstoned(_ context.Context, key, lease string, pass int, next time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if row, ok := f.media[key]; ok && row.leaseToken == lease {
		row.state = domain.MediaTombstoned
		row.pass = pass
		row.intent.NextAttemptAt = next
		row.leaseToken = ""
	}
	return nil
}

func (f *fakeRepo) ReleaseMediaIntent(_ context.Context, key, lease string, attempt int, next time.Time, lastErr string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if row, ok := f.media[key]; ok && row.leaseToken == lease {
		row.state = domain.MediaPending
		row.attempt = attempt
		row.intent.NextAttemptAt = next
		row.leaseToken = ""
		_ = lastErr
	}
	return nil
}

func (f *fakeRepo) InsertTaskDelivery(_ context.Context, d TaskDelivery) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.taskStamps[d.TaskID]; !ok {
		f.taskStamps[d.TaskID] = d
	}
	return nil
}

func (f *fakeRepo) GetTaskDelivery(_ context.Context, taskID string) (TaskDelivery, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	d, ok := f.taskStamps[taskID]
	return d, ok, nil
}

func (f *fakeRepo) RecordOutboundMessage(_ context.Context, rec OutboundRecord) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.outbound = append(f.outbound, rec)
	return nil
}

func (f *fakeRepo) SettleReplyDelivery(_ context.Context, turnID, _, _, _ string, _ wire.Kind, _, _, _ string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.settled = append(f.settled, turnID)
	f.replyPhase[turnID] = "settled"
	return nil
}

func (f *fakeRepo) BeginReplyStreaming(_ context.Context, turnID, taskID, _, _ string, _ wire.Kind, _ string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	_ = taskID
	phase, ok := f.replyPhase[turnID]
	if ok && !domain.ReplyPhaseMayOpenPlaceholder(phase) {
		return false, nil
	}
	f.replyPhase[turnID] = domain.ReplyPhaseStreaming
	return true, nil
}

func (f *fakeRepo) RecordReplyPlaceholder(_ context.Context, turnID, externalMessageID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.placeholder[turnID] = externalMessageID
	return nil
}

func (f *fakeRepo) MarkReplyTerminal(_ context.Context, turnID, externalMessageID string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	phase, ok := f.replyPhase[turnID]
	if !ok || !domain.ReplyPhaseMayDeliverTerminal(phase) {
		return false, nil
	}
	f.replyPhase[turnID] = domain.ReplyPhaseTerminal
	f.terminalIDs = append(f.terminalIDs, turnID)
	_ = externalMessageID
	return true, nil
}

func (f *fakeRepo) InsertCardMessage(_ context.Context, rec CardMessage) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.cardRows[rec.TaskID]; !ok {
		f.cardRows[rec.TaskID] = rec
	}
	return nil
}

func (f *fakeRepo) FinishCardMessage(_ context.Context, taskID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if row, ok := f.cardRows[taskID]; ok && domain.CardStatusMayFinish(row.Status) {
		row.Status = domain.CardStatusFinal
		f.cardRows[taskID] = row
	}
	f.finishedCard = append(f.finishedCard, taskID)
	return nil
}

func (f *fakeRepo) GetInstallation(_ context.Context, installationID string) (Installation, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	inst, ok := f.installations[installationID]
	if !ok {
		return Installation{}, ErrInstallationNotFound
	}
	return inst, nil
}

func (f *fakeRepo) UpdateRouteTitle(_ context.Context, installationID, externalChatID, title, source string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	key := installationID + "|" + externalChatID
	rt, ok := f.routes[key]
	if !ok {
		return false, nil
	}
	if !domain.TitleCallbackMayReplace(rt.TitleSource) {
		return false, nil
	}
	rt.Title, rt.TitleSource = title, source
	f.routes[key] = rt
	f.titleUpdates = append(f.titleUpdates, key+"|"+title+"|"+source)
	return true, nil
}

type fakeConvo struct {
	mu        sync.Mutex
	ensured   []EnsureSessionParams
	appended  []AppendParams
	markCalls []string
	seq       int64
	failMark  bool
}

func (f *fakeConvo) EnsureChannelSession(_ context.Context, p EnsureSessionParams) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.ensured = append(f.ensured, p)
	return "sess-" + p.ExternalChatID + "-" + string(p.ChatType), nil
}

func (f *fakeConvo) AppendChannelMessage(_ context.Context, p AppendParams, markInTx func(context.Context) error) (AppendResult, error) {
	f.mu.Lock()
	f.seq++
	seq := f.seq
	f.appended = append(f.appended, p)
	f.mu.Unlock()
	if markInTx != nil {
		_ = markInTx(context.Background())
		f.mu.Lock()
		f.markCalls = append(f.markCalls, p.ExternalMessageID)
		f.mu.Unlock()
	}
	return AppendResult{MessageID: "cm-" + p.ExternalMessageID, Seq: seq}, nil
}

type fakeInvoke struct {
	denied map[string]bool
	err    error
	calls  []string
}

func (f *fakeInvoke) MemberMayInvokeAgent(_ context.Context, agentID, userID string) (bool, error) {
	f.calls = append(f.calls, agentID+"/"+userID)
	if f.err != nil {
		return false, f.err
	}
	return !f.denied[userID], nil
}

type fakeRuns struct {
	mu     sync.Mutex
	calls  []EnqueueChatRunParams
	fail   bool
	nextID int
}

func (f *fakeRuns) EnqueueChannelChatRun(_ context.Context, p EnqueueChatRunParams) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.fail {
		return "", errors.New("enqueue failed")
	}
	f.nextID++
	f.calls = append(f.calls, p)
	return "task-" + p.SessionID + "-" + time.Now().Format("150405.000000000"), nil
}

func (f *fakeRuns) snapshot() []EnqueueChatRunParams {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]EnqueueChatRunParams(nil), f.calls...)
}

type fakeIssues struct {
	mu    sync.Mutex
	calls []CreateWorkItemParams
}

func (f *fakeIssues) CreateFromChannel(_ context.Context, p CreateWorkItemParams) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, p)
	return "wi-1", nil
}

type fakeMembers struct{ revoked map[string]bool }

func (f *fakeMembers) IsWorkspaceMember(_ context.Context, ws, userID string) (bool, error) {
	return !f.revoked[userID], nil
}

type fakeGuard struct{}

func (fakeGuard) BreakLinks(s string) string {
	return strings.ReplaceAll(s, "](", "] (")
}

func testInstallation() Installation {
	return Installation{
		ID: "inst-1", WorkspaceID: "ws-1", AgentID: "agent-1", InstallerUserID: "installer-1",
		Kind: "fake", Status: "active",
		Fingerprint: "fp-1",
		Config:      json.RawMessage(`{"routing_key":"rk-1"}`),
	}
}

func boundRepo() *fakeRepo {
	r := newFakeRepo()
	r.installations["inst-1"] = testInstallation()
	r.bindings["inst-1|extu-1"] = "user-1"
	return r
}

func newTestRouter(repo *fakeRepo, invoke InvokePolicy, cfg RouterConfig) (*Router, *fakeConvo, *fakeRuns, *fakeIssues) {
	convo := &fakeConvo{}
	runs := &fakeRuns{}
	issues := &fakeIssues{}
	return NewRouter(repo, convo, invoke, runs, issues, &fakeMembers{}, cfg), convo, runs, issues
}

func p2pMsg(text string) wire.InboundMessage {
	return wire.InboundMessage{
		EventID: "e-1", MessageID: "m-" + text,
		Source: wire.Source{Kind: "fake", RoutingKey: "rk-1", ChatID: "chat-1", ChatType: wire.ChatTypeP2P, SenderID: "extu-1"},
		Type:   wire.MsgTypeText, Text: text,
	}
}

func TestInboundPipelineHappyPath(t *testing.T) {
	repo := boundRepo()
	router, convo, runs, _ := newTestRouter(repo, &fakeInvoke{}, RouterConfig{})

	res, err := router.Handle(context.Background(), p2pMsg("check the progress for me"))
	require.NoError(t, err)

	assert.Equal(t, "inst-1", res.InstallationID)

	assert.Equal(t, domain.DedupProcessed, repo.dedupState("inst-1", "m-check the progress for me"))

	assert.Equal(t, "user-1", res.UserID)

	assert.NotEqual(t, OutcomeInvokeDenied, res.Outcome)

	require.Len(t, convo.ensured, 1)
	assert.Equal(t, "sess-chat-1-p2p", res.SessionID)
	assert.Equal(t, "check the progress for me", convo.ensured[0].Title, "deterministic title = first line of the body")

	require.Len(t, convo.appended, 1)
	assert.Equal(t, "user-1", convo.appended[0].UserID)
	assert.Equal(t, res.SessionID, convo.appended[0].SessionID)
	assert.Contains(t, convo.markCalls, "m-check the progress for me", "dedup terminal marks in the same transaction as append (fake sequential execution)")

	require.Len(t, runs.calls, 1)
	assert.Equal(t, "user-1", runs.calls[0].UserID, "run initiator = the sender of this message")
	assert.Equal(t, int64(1), runs.calls[0].ContextRevision)

	repo.mu.Lock()
	stamps := len(repo.taskStamps)
	repo.mu.Unlock()
	assert.Equal(t, 1, stamps, "exactly one task delivery stamp")
	assert.Equal(t, OutcomeIngested, res.Outcome)
}

func TestInboundInvokeDeniedPersistsNothing(t *testing.T) {
	repo := boundRepo()
	router, convo, runs, issues := newTestRouter(repo, &fakeInvoke{denied: map[string]bool{"user-1": true}}, RouterConfig{})

	res, err := router.Handle(context.Background(), p2pMsg("should not be processed"))
	require.NoError(t, err)

	assert.Equal(t, OutcomeInvokeDenied, res.Outcome)

	assert.Empty(t, convo.ensured, "invoke rejected: must not find-or-create a session")
	assert.Empty(t, convo.appended, "invoke rejected: must not append messages")
	assert.Empty(t, runs.calls, "invoke rejected: must not enqueue a run")
	assert.Empty(t, issues.calls, "invoke rejected: must not create a work item")
	repo.mu.Lock()
	stamps := len(repo.taskStamps)
	repo.mu.Unlock()
	assert.Zero(t, stamps, "invoke rejected: must not land task delivery stamps")

	require.Len(t, repo.audits, 1)
	assert.Equal(t, string(DropInvokeDenied), repo.audits[0].DropReason)
	assert.Equal(t, domain.DedupProcessed, repo.dedupState("inst-1", "m-should not be processed"))
}

func TestInboundDuplicate(t *testing.T) {
	repo := boundRepo()
	router, convo, runs, _ := newTestRouter(repo, &fakeInvoke{}, RouterConfig{})

	msg := p2pMsg("dup")
	_, err := router.Handle(context.Background(), msg)
	require.NoError(t, err)
	res, err := router.Handle(context.Background(), msg)
	require.NoError(t, err)
	assert.Equal(t, OutcomeDropped, res.Outcome)
	assert.Equal(t, DropDuplicate, res.DropReason)
	assert.Len(t, convo.appended, 1, "redelivery must not append twice")
	assert.Len(t, runs.calls, 1)
}

func TestInboundGroupFilter(t *testing.T) {
	repo := boundRepo()
	router, _, _, _ := newTestRouter(repo, &fakeInvoke{}, RouterConfig{})

	msg := p2pMsg("group small talk")
	msg.Source.ChatType = wire.ChatTypeGroup
	msg.AddressedToBot = false
	res, err := router.Handle(context.Background(), msg)
	require.NoError(t, err)
	assert.Equal(t, OutcomeDropped, res.Outcome)
	assert.Equal(t, DropNotAddressedInGroup, res.DropReason)

	msg2 := p2pMsg("@bot in a group")
	msg2.Source.ChatType = wire.ChatTypeGroup
	msg2.AddressedToBot = true
	res2, err := router.Handle(context.Background(), msg2)
	require.NoError(t, err)
	assert.Equal(t, OutcomeIngested, res2.Outcome)
}

func TestInboundNeedsBinding(t *testing.T) {
	repo := boundRepo()
	router, convo, _, _ := newTestRouter(repo, &fakeInvoke{}, RouterConfig{})

	msg := p2pMsg("I am not bound")
	msg.Source.SenderID = "extu-stranger"
	res, err := router.Handle(context.Background(), msg)
	require.NoError(t, err)
	assert.Equal(t, OutcomeNeedsBinding, res.Outcome)
	assert.NotEmpty(t, res.BindingToken)
	assert.Empty(t, convo.appended, "unbound cannot append")

	require.NoError(t, repo.ConsumeBindingToken(context.Background(), "ws-1", res.BindingToken, "user-9"))
	res2, err := router.Handle(context.Background(), func() wire.InboundMessage {
		m := p2pMsg("I am bound")
		m.Source.SenderID = "extu-stranger"
		return m
	}())
	require.NoError(t, err)
	assert.Equal(t, OutcomeIngested, res2.Outcome)

	assert.ErrorIs(t, repo.ConsumeBindingToken(context.Background(), "ws-1", res.BindingToken, "user-9"), ErrBindingTokenInvalid)
}

func TestInboundRevokedAndNotFound(t *testing.T) {
	repo := boundRepo()
	router, _, _, _ := newTestRouter(repo, &fakeInvoke{}, RouterConfig{})

	msg := p2pMsg("x")
	msg.Source.RoutingKey = "no-such"
	res, err := router.Handle(context.Background(), msg)
	require.NoError(t, err)
	assert.Equal(t, OutcomeDropped, res.Outcome)
	assert.Equal(t, DropInvalidEvent, res.DropReason)

	inst := testInstallation()
	inst.Status = "revoked"
	repo.mu.Lock()
	repo.installations["inst-1"] = inst
	repo.mu.Unlock()
	res2, err := router.Handle(context.Background(), p2pMsg("y"))
	require.NoError(t, err)
	assert.Equal(t, DropRevokedInstallation, res2.DropReason)
}

func TestInboundMemberRevoked(t *testing.T) {
	repo := boundRepo()
	convo := &fakeConvo{}
	router := NewRouter(repo, convo, &fakeInvoke{}, &fakeRuns{}, &fakeIssues{},
		&fakeMembers{revoked: map[string]bool{"user-1": true}}, RouterConfig{})

	res, err := router.Handle(context.Background(), p2pMsg("revoked member"))
	require.NoError(t, err)
	assert.Equal(t, OutcomeDropped, res.Outcome)
	assert.Equal(t, DropNonWorkspaceMember, res.DropReason)
	assert.Empty(t, convo.appended, "non-members cannot append")
}

func TestInboundCommands(t *testing.T) {
	t.Run("/new generation rotation", func(t *testing.T) {
		repo := boundRepo()
		router, convo, _, _ := newTestRouter(repo, &fakeInvoke{}, RouterConfig{})
		_, err := router.Handle(context.Background(), p2pMsg("first sentence"))
		require.NoError(t, err)
		first := convSession(convo, 0)

		res, err := router.Handle(context.Background(), p2pMsg("/new new topic"))
		require.NoError(t, err)
		assert.Equal(t, OutcomeChatStarted, res.Outcome)
		assert.NotEqual(t, first, res.SessionID, "after /new the route points to the new session")
		repo.mu.Lock()
		rt := repo.routes["inst-1|chat-1"]
		repo.mu.Unlock()
		assert.Equal(t, int64(2), rt.Generation, "route generation+1")
	})

	t.Run("/clear new context generation (hard batch boundary)", func(t *testing.T) {
		repo := boundRepo()
		router, _, _, _ := newTestRouter(repo, &fakeInvoke{}, RouterConfig{})
		_, err := router.Handle(context.Background(), p2pMsg("old-generation first sentence"))
		require.NoError(t, err)
		res, err := router.Handle(context.Background(), p2pMsg("/clear"))
		require.NoError(t, err)
		assert.Equal(t, OutcomeContextRotated, res.Outcome)
		assert.Equal(t, int64(2), res.ContextRevision, "generation+1")
		repo.mu.Lock()
		genRows := repo.genRows
		repo.mu.Unlock()
		assert.GreaterOrEqual(t, genRows, 2, "context representative anchored into generated table")
	})

	t.Run("/task create work item", func(t *testing.T) {
		repo := boundRepo()
		router, _, _, issues := newTestRouter(repo, &fakeInvoke{}, RouterConfig{})
		res, err := router.Handle(context.Background(), p2pMsg("/task fix login\noccasional 401"))
		require.NoError(t, err)
		assert.Equal(t, OutcomeWorkItemCreated, res.Outcome)
		require.Len(t, issues.calls, 1)
		assert.Equal(t, "fix login", issues.calls[0].Title)
		assert.Equal(t, "occasional 401", issues.calls[0].Description)
		assert.Equal(t, "user-1", issues.calls[0].CreatorUserID)
	})

	t.Run("/task without title = usage hint", func(t *testing.T) {
		repo := boundRepo()
		router, _, _, issues := newTestRouter(repo, &fakeInvoke{}, RouterConfig{})
		res, err := router.Handle(context.Background(), p2pMsg("/task"))
		require.NoError(t, err)
		assert.Equal(t, OutcomeWorkItemUsage, res.Outcome)
		assert.Empty(t, issues.calls, "never create a work item without a title")
	})
}

func convSession(c *fakeConvo, i int) string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.ensured[i].ExternalChatID + "-" + string(c.ensured[i].ChatType)
}

func TestDebounceMergesBurst(t *testing.T) {
	repo := boundRepo()
	router, _, runs, _ := newTestRouter(repo, &fakeInvoke{}, RouterConfig{DebounceWindow: 80 * time.Millisecond})
	defer router.Drain()

	for i := 0; i < 3; i++ {
		_, err := router.Handle(context.Background(), p2pMsg("rapid sends"+string(rune('a'+i))))
		require.NoError(t, err)
	}
	assert.Empty(t, runs.calls, "no immediate trigger inside the window")
	assert.Equal(t, 1, router.debouncer.PendingCount(), "three consecutive sends on the same (session,generation) merge into one pending trigger")

	time.Sleep(200 * time.Millisecond)
	runs.mu.Lock()
	defer runs.mu.Unlock()
	assert.Len(t, runs.calls, 1, "exactly one run after the silent window")
}

func TestDebounceClearIsHardBoundary(t *testing.T) {
	repo := boundRepo()
	router, _, runs, _ := newTestRouter(repo, &fakeInvoke{}, RouterConfig{DebounceWindow: 80 * time.Millisecond})
	defer router.Drain()

	_, err := router.Handle(context.Background(), p2pMsg("old-generation message"))
	require.NoError(t, err)
	_, err = router.Handle(context.Background(), p2pMsg("/clear"))
	require.NoError(t, err)
	_, err = router.Handle(context.Background(), p2pMsg("new-generation message"))
	require.NoError(t, err)

	time.Sleep(250 * time.Millisecond)
	runs.mu.Lock()
	defer runs.mu.Unlock()
	assert.Len(t, runs.calls, 2, "old/new generations one run each (hard batch boundary no merge)")
	revisions := []int64{runs.calls[0].ContextRevision, runs.calls[1].ContextRevision}
	assert.ElementsMatch(t, []int64{1, 2}, revisions, "old generation 1 and new generation 2 each trigger (order irrelevant)")
}

func TestInfraErrorReleasesClaim(t *testing.T) {
	repo := boundRepo()
	router, _, _, _ := newTestRouter(repo, &fakeInvoke{err: errors.New("policy backend down")}, RouterConfig{})

	_, err := router.Handle(context.Background(), p2pMsg("transient failure"))
	require.Error(t, err)
	assert.Equal(t, domain.DedupSeen, repo.dedupState("inst-1", "m-transient failure"), "pending ≠ terminal: claim released")
}
