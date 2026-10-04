package app

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"orkome/internal/contract/apierr"
	"orkome/internal/identity/domain"
	"orkome/internal/platform/webx"
)

type fakeRepo struct {
	latestPending func(email string) (PendingCode, bool)
	insertCode    func(email, hash string, exp time.Time) error
	incAttempts   func(id string) error
	consumeCode   func(id string) error
	attemptsCalls int

	upsertUser func(email string) (User, error)
	getByEmail func(email string) (User, bool)

	shareByCode  func(code string) (ShareLink, bool)
	consumeShare func(code string) (ShareLink, bool)
	shareCalls   int

	getMember     func(wsID, userID string) (Member, bool)
	getMemberByID func(memberID string) (Member, bool)
	countOwners   func(wsID string) (int, error)
	removeMember  func(memberID string) error
	removeCalls   []string
	addMember     func(wsID, userID, role string) (Member, error)
	addCalls      int

	deleteWorkspace func(id string, teardown WorkspaceTeardown) error
	deletedWS       []string

	getPATByHash   func(hash string) (PAT, string, bool)
	insertPAT      func(userID, name, hash, prefix string, exp *time.Time) (PAT, error)
	revokePAT      func(id, userID string) error
	revokeAllPATs  func(userID string) error
	revokeAllCalls []string
	touchPAT       func(id string) error

	listPins     func(wsID, userID string) []Pin
	addPin       func(wsID, userID, itemType, itemID string) (Pin, bool)
	reorderPins  func(order []string) error
	reorderCalls int
}

func (f *fakeRepo) UpsertUserByEmail(_ context.Context, email string) (User, error) {
	if f.upsertUser != nil {
		return f.upsertUser(email)
	}
	return User{ID: "u1", Email: email}, nil
}
func (f *fakeRepo) GetUser(_ context.Context, id string) (User, bool, error) {
	return User{ID: id, Email: "u@example.com", DisplayName: "U"}, true, nil
}
func (f *fakeRepo) GetUserByEmail(_ context.Context, email string) (User, bool, error) {
	if f.getByEmail != nil {
		u, ok := f.getByEmail(email)
		return u, ok, nil
	}
	return User{}, true, nil
}
func (f *fakeRepo) UpdateUser(_ context.Context, _ string, _, _, _ string) (User, error) {
	return User{}, nil
}

func (f *fakeRepo) CreateWorkspace(_ context.Context, _, _, _ string) (Workspace, Member, error) {
	return Workspace{}, Member{}, nil
}
func (f *fakeRepo) GetWorkspace(_ context.Context, _ string) (Workspace, bool, error) {
	return Workspace{}, false, nil
}
func (f *fakeRepo) GetWorkspaceBySlug(_ context.Context, _ string) (Workspace, bool, error) {
	return Workspace{}, false, nil
}
func (f *fakeRepo) UpdateWorkspace(_ context.Context, _ string, _, _ string) (Workspace, error) {
	return Workspace{}, nil
}
func (f *fakeRepo) DeleteWorkspace(_ context.Context, id string, teardown WorkspaceTeardown) error {
	f.deletedWS = append(f.deletedWS, id)
	if f.deleteWorkspace != nil {
		return f.deleteWorkspace(id, teardown)
	}

	if teardown != nil {
		return teardown(context.Background(), id)
	}
	return nil
}
func (f *fakeRepo) ListMyWorkspaces(_ context.Context, _ string) ([]Workspace, error) {
	return nil, nil
}
func (f *fakeRepo) GetMember(_ context.Context, wsID, userID string) (Member, bool, error) {
	if f.getMember != nil {
		m, ok := f.getMember(wsID, userID)
		return m, ok, nil
	}
	return Member{}, false, nil
}
func (f *fakeRepo) GetMemberByID(_ context.Context, memberID string) (Member, bool, error) {
	if f.getMemberByID != nil {
		m, ok := f.getMemberByID(memberID)
		return m, ok, nil
	}
	return Member{}, false, nil
}
func (f *fakeRepo) ListMembers(_ context.Context, _ string) ([]Membership, error) {
	return nil, nil
}
func (f *fakeRepo) UpdateMemberRole(_ context.Context, _, _ string) (Member, error) {
	return Member{}, nil
}
func (f *fakeRepo) RemoveMember(_ context.Context, memberID string) error {
	f.removeCalls = append(f.removeCalls, memberID)
	if f.removeMember != nil {
		return f.removeMember(memberID)
	}
	return nil
}
func (f *fakeRepo) CountOwners(_ context.Context, wsID string) (int, error) {
	if f.countOwners != nil {
		return f.countOwners(wsID)
	}
	return 0, nil
}
func (f *fakeRepo) AddMember(_ context.Context, wsID, userID, role string) (Member, error) {
	f.addCalls++
	if f.addMember != nil {
		return f.addMember(wsID, userID, role)
	}
	return Member{ID: "m1", WorkspaceID: wsID, UserID: userID, Role: role}, nil
}

func (f *fakeRepo) InsertCode(_ context.Context, email, hash string, exp time.Time) error {
	if f.insertCode != nil {
		return f.insertCode(email, hash, exp)
	}
	return nil
}
func (f *fakeRepo) LatestPendingCode(_ context.Context, email string) (PendingCode, bool, error) {
	if f.latestPending != nil {
		c, ok := f.latestPending(email)
		return c, ok, nil
	}
	return PendingCode{}, false, nil
}
func (f *fakeRepo) ConsumeCode(_ context.Context, id string) error {
	if f.consumeCode != nil {
		return f.consumeCode(id)
	}
	return nil
}
func (f *fakeRepo) IncCodeAttempts(_ context.Context, id string) error {
	f.attemptsCalls++
	if f.incAttempts != nil {
		return f.incAttempts(id)
	}
	return nil
}

func (f *fakeRepo) CreateInvitation(_ context.Context, _ string, _, _, _ string, _ time.Time) (Invitation, error) {
	return Invitation{}, nil
}
func (f *fakeRepo) HasPendingInvitation(_ context.Context, _, _ string) (bool, error) {
	return false, nil
}
func (f *fakeRepo) ListWorkspaceInvitations(_ context.Context, _ string) ([]Invitation, error) {
	return nil, nil
}
func (f *fakeRepo) ListMyInvitations(_ context.Context, _ string) ([]Invitation, error) {
	return nil, nil
}
func (f *fakeRepo) GetInvitation(_ context.Context, _ string) (Invitation, bool, error) {
	return Invitation{}, false, nil
}
func (f *fakeRepo) SetInvitationStatus(_ context.Context, _, _ string) error { return nil }
func (f *fakeRepo) DeleteInvitation(_ context.Context, _ string) error       { return nil }

func (f *fakeRepo) CreateShareLink(_ context.Context, _ string, _, _, _ string, _ int, _ time.Time) (ShareLink, error) {
	return ShareLink{}, nil
}
func (f *fakeRepo) ListShareLinks(_ context.Context, _ string) ([]ShareLink, error) {
	return nil, nil
}
func (f *fakeRepo) GetShareLinkByCode(_ context.Context, code string) (ShareLink, bool, error) {
	if f.shareByCode != nil {
		sl, ok := f.shareByCode(code)
		return sl, ok, nil
	}
	return ShareLink{}, false, nil
}
func (f *fakeRepo) DeleteShareLink(_ context.Context, _ string) error { return nil }
func (f *fakeRepo) ConsumeShareUse(_ context.Context, code string) (ShareLink, bool, error) {
	f.shareCalls++
	if f.consumeShare != nil {
		sl, ok := f.consumeShare(code)
		return sl, ok, nil
	}
	return ShareLink{}, false, nil
}

func (f *fakeRepo) InsertPAT(_ context.Context, userID, name, hash, prefix string, exp *time.Time) (PAT, error) {
	if f.insertPAT != nil {
		return f.insertPAT(userID, name, hash, prefix, exp)
	}
	return PAT{ID: "pat1", Name: name, Prefix: prefix}, nil
}
func (f *fakeRepo) ListPATs(_ context.Context, _ string) ([]PAT, error) { return nil, nil }
func (f *fakeRepo) RevokePAT(_ context.Context, id, _ string) error {
	if f.revokePAT != nil {
		return f.revokePAT(id, id)
	}
	return nil
}
func (f *fakeRepo) RevokeAllPATs(_ context.Context, userID string) error {
	f.revokeAllCalls = append(f.revokeAllCalls, userID)
	if f.revokeAllPATs != nil {
		return f.revokeAllPATs(userID)
	}
	return nil
}
func (f *fakeRepo) GetPATByHash(_ context.Context, hash string) (PAT, string, bool, error) {
	if f.getPATByHash != nil {
		p, uid, ok := f.getPATByHash(hash)
		return p, uid, ok, nil
	}
	return PAT{}, "", false, nil
}
func (f *fakeRepo) TouchPAT(_ context.Context, id string) error {
	if f.touchPAT != nil {
		return f.touchPAT(id)
	}
	return nil
}

func (f *fakeRepo) ListPins(_ context.Context, wsID, userID string) ([]Pin, error) {
	if f.listPins != nil {
		return f.listPins(wsID, userID), nil
	}
	return nil, nil
}
func (f *fakeRepo) AddPin(_ context.Context, wsID, userID, itemType, itemID string) (Pin, bool, error) {
	if f.addPin != nil {
		p, created := f.addPin(wsID, userID, itemType, itemID)
		return p, created, nil
	}
	return Pin{}, true, nil
}
func (f *fakeRepo) ReorderPins(_ context.Context, _, _ string, order []string) error {
	f.reorderCalls++
	if f.reorderPins != nil {
		return f.reorderPins(order)
	}
	return nil
}
func (f *fakeRepo) RemovePin(_ context.Context, _, _, _, _ string) error { return nil }

type fakeCache struct {
	store   map[string]string
	ttls    map[string]time.Duration
	setKeys []string
	delKeys []string
}

func newFakeCache() *fakeCache {
	return &fakeCache{store: map[string]string{}, ttls: map[string]time.Duration{}}
}

func (c *fakeCache) Get(_ context.Context, key string) (string, bool) {
	v, ok := c.store[key]
	return v, ok
}
func (c *fakeCache) Set(_ context.Context, key, val string, ttl time.Duration) {
	c.store[key] = val
	c.ttls[key] = ttl
	c.setKeys = append(c.setKeys, key)
}
func (c *fakeCache) Del(_ context.Context, keys ...string) { c.delKeys = append(c.delKeys, keys...) }

var validShareCode = "0123456789abcdef01234567"

func TestJoinByCode_NewMemberAddedWithLinkRole(t *testing.T) {
	repo := &fakeRepo{
		shareByCode: func(code string) (ShareLink, bool) {
			return ShareLink{ID: "sl1", WorkspaceID: "ws1", Code: code, Role: "member",
				MaxUses: 5, ExpiresAt: time.Now().Add(time.Hour)}, true
		},
		consumeShare: func(code string) (ShareLink, bool) {
			return ShareLink{WorkspaceID: "ws1", Role: "member", Uses: 1}, true
		},
	}
	s := NewWorkspaceService(repo, nil, nil, time.Now)

	m, err := s.JoinByCode(context.Background(), validShareCode, &webx.Principal{UserID: "u9"})
	require.NoError(t, err)
	assert.Equal(t, "ws1", m.WorkspaceID)
	assert.Equal(t, "u9", m.UserID)
	assert.Equal(t, "member", m.Role, "member role taken from the link (CAS returned row)")
	assert.Equal(t, 1, repo.shareCalls)
	assert.Equal(t, 1, repo.addCalls)
}

func TestJoinByCode_AlreadyMemberDoesNotBurnQuota(t *testing.T) {
	repo := &fakeRepo{
		shareByCode: func(string) (ShareLink, bool) {
			return ShareLink{WorkspaceID: "ws1", Role: "member", ExpiresAt: time.Now().Add(time.Hour)}, true
		},
		getMember: func(wsID, userID string) (Member, bool) {
			return Member{WorkspaceID: wsID, UserID: userID, Role: "admin"}, true
		},
	}
	s := NewWorkspaceService(repo, nil, nil, time.Now)

	m, err := s.JoinByCode(context.Background(), validShareCode, &webx.Principal{UserID: "u1"})
	require.Error(t, err)
	assert.Nil(t, m)
	assert.Equal(t, apierr.Validation, err.(*apierr.Error).Code, "already a member→E_VALIDATION (same AcceptInvitation convention)")
	assert.Zero(t, repo.shareCalls, "idempotent: existing members do not consume quota (uses counts real joins only)")
	assert.Zero(t, repo.addCalls)
}

func TestJoinByCode_ExpiredOrUnknownCodeRejected(t *testing.T) {
	past := time.Now().Add(-time.Minute)
	repo := &fakeRepo{
		shareByCode: func(string) (ShareLink, bool) {
			return ShareLink{WorkspaceID: "ws1", Role: "member", ExpiresAt: past}, true
		},
	}
	s := NewWorkspaceService(repo, nil, nil, time.Now)

	_, err := s.JoinByCode(context.Background(), validShareCode, &webx.Principal{UserID: "u9"})
	require.Error(t, err)
	assert.Equal(t, apierr.NotFound, err.(*apierr.Error).Code, "expired code → 404 (invisible same code as does-not-exist, I6)")
	assert.Zero(t, repo.shareCalls, "expired codes must not enter the quota CAS")

	repo.shareByCode = func(string) (ShareLink, bool) { return ShareLink{}, false }
	_, err = s.JoinByCode(context.Background(), validShareCode, &webx.Principal{UserID: "u9"})
	assert.Equal(t, apierr.NotFound, err.(*apierr.Error).Code)
	assert.Zero(t, repo.shareCalls)
}

func TestJoinByCode_MalformedCodeRejectedWithoutLookup(t *testing.T) {
	repo := &fakeRepo{shareByCode: func(string) (ShareLink, bool) {
		t.Fatal("malformed codes must not reach the repository")
		return ShareLink{}, false
	}}
	s := NewWorkspaceService(repo, nil, nil, time.Now)

	_, err := s.JoinByCode(context.Background(), "not-a-code", &webx.Principal{UserID: "u9"})
	require.Error(t, err)
	assert.Equal(t, apierr.NotFound, err.(*apierr.Error).Code)
}

func TestJoinByCode_ExhaustedQuotaRejected(t *testing.T) {
	repo := &fakeRepo{
		shareByCode: func(string) (ShareLink, bool) {
			return ShareLink{WorkspaceID: "ws1", Role: "member", ExpiresAt: time.Now().Add(time.Hour)}, true
		},
		consumeShare: func(string) (ShareLink, bool) {
			return ShareLink{}, false
		},
	}
	s := NewWorkspaceService(repo, nil, nil, time.Now)

	_, err := s.JoinByCode(context.Background(), validShareCode, &webx.Principal{UserID: "u9"})
	require.Error(t, err)
	assert.Equal(t, apierr.NotFound, err.(*apierr.Error).Code)
	assert.Equal(t, 1, repo.shareCalls)
	assert.Zero(t, repo.addCalls, "member row must not land when CAS fails")
}

func removeMemberFixture() (*fakeRepo, *WorkspaceService, *[]string) {
	var seq []string
	repo := &fakeRepo{
		getMember: func(ws, uid string) (Member, bool) {
			return Member{ID: "m-actor", WorkspaceID: ws, UserID: uid, Role: "owner"}, true
		},
		getMemberByID: func(id string) (Member, bool) {
			return Member{ID: id, WorkspaceID: "ws1", UserID: "u-target", Role: "member"}, true
		},
		countOwners: func(string) (int, error) { return 2, nil },
		removeMember: func(id string) error {
			seq = append(seq, "member-deleted")
			return nil
		},
	}
	tokens := NewTokenService(repo, nil, time.Now)
	s := NewWorkspaceService(repo, nil, tokens, time.Now)
	return repo, s, &seq
}

func TestRemoveMember_NilRevocationPortSkipped(t *testing.T) {
	repo, s, _ := removeMemberFixture()

	err := s.RemoveMember(context.Background(), "ws1", "m-target", "u-actor")
	require.NoError(t, err)
	assert.Equal(t, []string{"m-target"}, repo.removeCalls)
	assert.Equal(t, []string{"u-target"}, repo.revokeAllCalls, "PAT face still revokes all instantly")
}

func TestRemoveMember_RevocationPortCalledAfterRemoval(t *testing.T) {
	repo, s, seq := removeMemberFixture()
	var calls [][2]string
	s.UserRevocation = func(_ context.Context, wsID, userID string) error {
		calls = append(calls, [2]string{wsID, userID})
		*seq = append(*seq, "port-revoked")
		return nil
	}

	err := s.RemoveMember(context.Background(), "ws1", "m-target", "u-actor")
	require.NoError(t, err)
	require.Len(t, calls, 1)
	assert.Equal(t, [2]string{"ws1", "u-target"}, calls[0], "port params = workspace + removed user")
	assert.Equal(t, []string{"member-deleted", "port-revoked"}, *seq,
		"cross-domain revocation must be called after commit of the member row deletion")
	assert.Equal(t, []string{"m-target"}, repo.removeCalls)
}

func TestRemoveMember_PortErrorDoesNotRollbackRemoval(t *testing.T) {
	repo, s, _ := removeMemberFixture()
	s.UserRevocation = func(context.Context, string, string) error {
		return errors.New("cross-domain revocation failed")
	}

	err := s.RemoveMember(context.Background(), "ws1", "m-target", "u-actor")
	require.NoError(t, err, "port failure only logs, does not roll back deleted members")
	assert.Equal(t, []string{"m-target"}, repo.removeCalls)
	assert.Equal(t, []string{"u-target"}, repo.revokeAllCalls)
}

func ownerRepo() *fakeRepo {
	return &fakeRepo{
		getMember: func(ws, uid string) (Member, bool) {
			return Member{ID: "m-actor", WorkspaceID: ws, UserID: uid, Role: "owner"}, true
		},
	}
}

func TestWorkspaceDelete_TeardownPortInvokedPerWorkspace(t *testing.T) {
	repo := ownerRepo()
	s := NewWorkspaceService(repo, nil, nil, time.Now)
	var got []string
	s.WorkspaceTeardown = func(_ context.Context, wsID string) error {
		got = append(got, wsID)
		return nil
	}

	err := s.Delete(context.Background(), "ws1", "u-actor")
	require.NoError(t, err)
	assert.Equal(t, []string{"ws1"}, repo.deletedWS)
	assert.Equal(t, []string{"ws1"}, got)
}

func TestWorkspaceDelete_NilTeardownSkipped(t *testing.T) {
	repo := ownerRepo()
	s := NewWorkspaceService(repo, nil, nil, time.Now)

	err := s.Delete(context.Background(), "ws1", "u-actor")
	require.NoError(t, err)
	assert.Equal(t, []string{"ws1"}, repo.deletedWS)
}

func TestWorkspaceDelete_TeardownErrorRollsBackWholeDelete(t *testing.T) {
	repo := ownerRepo()
	s := NewWorkspaceService(repo, nil, nil, time.Now)
	boom := errors.New("orchestration teardown failed")
	s.WorkspaceTeardown = func(context.Context, string) error { return boom }

	err := s.Delete(context.Background(), "ws1", "u-actor")
	require.ErrorIs(t, err, boom, "teardown failure must roll back wholesale (internal archive 04 §2.2 two-phase)")
}

func TestWorkspaceDelete_NonOwnerRejectedBeforeRepo(t *testing.T) {
	repo := &fakeRepo{
		getMember: func(ws, uid string) (Member, bool) {
			return Member{ID: "m-actor", WorkspaceID: ws, UserID: uid, Role: "admin"}, true
		},
	}
	s := NewWorkspaceService(repo, nil, nil, time.Now)
	s.WorkspaceTeardown = func(context.Context, string) error {
		t.Fatal("must not enter the delete transaction without passing the role gate")
		return nil
	}

	err := s.Delete(context.Background(), "ws1", "u-actor")
	require.Error(t, err)
	assert.Equal(t, apierr.Forbidden, err.(*apierr.Error).Code)
	assert.Empty(t, repo.deletedWS)
}

func pinFixture(pins ...Pin) (*fakeRepo, *PinService) {
	repo := &fakeRepo{
		getMember: func(ws, uid string) (Member, bool) {
			return Member{WorkspaceID: ws, UserID: uid, Role: "member"}, true
		},
		listPins: func(string, string) []Pin { return pins },
	}
	return repo, NewPinService(repo)
}

func TestPinService_NonMemberInvisible(t *testing.T) {
	repo, s := pinFixture()
	repo.getMember = func(string, string) (Member, bool) { return Member{}, false }

	_, err := s.List(context.Background(), "ws1", "u1")
	assert.Equal(t, apierr.NotFound, err.(*apierr.Error).Code)
	_, _, err = s.Add(context.Background(), "ws1", "u1", "work_item", "11111111-1111-1111-1111-111111111111")
	assert.Equal(t, apierr.NotFound, err.(*apierr.Error).Code)
}

func TestPinService_AddValidatesTypeAndUUID(t *testing.T) {
	repo, s := pinFixture()
	repo.addPin = func(string, string, string, string) (Pin, bool) {
		t.Fatal("invalid inputs must not reach the repository")
		return Pin{}, true
	}

	_, _, err := s.Add(context.Background(), "ws1", "u1", "agent", "11111111-1111-1111-1111-111111111111")
	require.Error(t, err)
	assert.Equal(t, apierr.Validation, err.(*apierr.Error).Code)

	_, _, err = s.Add(context.Background(), "ws1", "u1", "work_item", "not-a-uuid")
	require.Error(t, err)
	assert.Equal(t, apierr.Validation, err.(*apierr.Error).Code)

	err = s.Remove(context.Background(), "ws1", "u1", "agent", "11111111-1111-1111-1111-111111111111")
	assert.Equal(t, apierr.Validation, err.(*apierr.Error).Code)
}

func TestPinService_ReorderRequiresExactFullSet(t *testing.T) {
	pins := []Pin{{ID: "p1"}, {ID: "p2"}, {ID: "p3"}}
	repo, s := pinFixture(pins...)

	for _, order := range [][]string{
		{"p1", "p2"},
		{"p1", "p2", "p3", "p4"},
		{"p1", "p1", "p3"},
		{},
	} {
		err := s.Reorder(context.Background(), "ws1", "u1", order)
		require.Error(t, err, "order=%v should be rejected", order)
		assert.Equal(t, apierr.Validation, err.(*apierr.Error).Code)
	}
	assert.Zero(t, repo.reorderCalls, "invalid shape must not reach the repository")

	require.NoError(t, s.Reorder(context.Background(), "ws1", "u1", []string{"p3", "p1", "p2"}))
	assert.Equal(t, 1, repo.reorderCalls, "allowed only when it is a full reordering")
}

func TestTokenRenew_RotatesOldAndDropsCache(t *testing.T) {
	tok, hash, _, err := domain.MintPAT()
	require.NoError(t, err)

	revoked := ""
	repo := &fakeRepo{
		getPATByHash: func(h string) (PAT, string, bool) {
			require.Equal(t, hash, h)
			return PAT{ID: "pat-old", Name: "cli"}, "u1", true
		},
		insertPAT: func(userID, name, _, pref string, _ *time.Time) (PAT, error) {
			return PAT{ID: "pat-new", Name: name, Prefix: pref}, nil
		},
		revokePAT: func(id, _ string) error { revoked = id; return nil },
	}
	cache := newFakeCache()
	s := NewTokenService(repo, cache, time.Now)

	res, err := s.Renew(context.Background(), tok, "rotated")
	require.NoError(t, err)
	assert.Equal(t, "pat-new", res.PAT.ID)
	assert.NotEqual(t, tok, res.Plaintext, "rotation must issue new plaintext")
	assert.True(t, domain.IsPATFormat(res.Plaintext))
	assert.Equal(t, "pat-old", revoked, "old PAT rows must be invalidated")
	assert.Equal(t, []string{"pat:" + hash}, cache.delKeys, "old hash cache invalidated immediately")
}

func TestTokenRenew_RejectsMalformedToken(t *testing.T) {
	repo := &fakeRepo{getPATByHash: func(string) (PAT, string, bool) {
		t.Fatal("non-ork_+40hex forms must not query the DB")
		return PAT{}, "", false
	}}
	s := NewTokenService(repo, newFakeCache(), time.Now)

	_, err := s.Renew(context.Background(), "not-a-pat", "x")
	require.Error(t, err)
	assert.Equal(t, apierr.Unauthenticated, err.(*apierr.Error).Code)
}

func TestTokenRenew_RevokedTokenRejected(t *testing.T) {
	tok, hash, _, err := domain.MintPAT()
	require.NoError(t, err)
	now := time.Now()
	revokedAt := now.Add(-time.Minute)
	repo := &fakeRepo{
		getPATByHash: func(string) (PAT, string, bool) {
			return PAT{ID: "pat-old", RevokedAt: &revokedAt}, "u1", true
		},
		insertPAT: func(string, string, string, string, *time.Time) (PAT, error) {
			t.Fatal("revoked tokens must not rotate out new PATs")
			return PAT{}, nil
		},
	}
	_ = hash
	s := NewTokenService(repo, newFakeCache(), func() time.Time { return now })

	_, err = s.Renew(context.Background(), tok, "x")
	require.Error(t, err)
	assert.Equal(t, apierr.Unauthenticated, err.(*apierr.Error).Code)
}

func TestResolvePAT_CacheTTLClampedToRemainingLife(t *testing.T) {
	tok, hash, _, err := domain.MintPAT()
	require.NoError(t, err)
	now := time.Now()
	expires := now.Add(3 * time.Minute)
	repo := &fakeRepo{
		getPATByHash: func(h string) (PAT, string, bool) {
			return PAT{ID: "pat1", ExpiresAt: &expires}, "u1", true
		},
	}
	cache := newFakeCache()
	s := NewTokenService(repo, cache, func() time.Time { return now })

	p, err := s.ResolvePAT(context.Background(), tok)
	require.NoError(t, err)
	assert.Equal(t, "u1", p.UserID)
	assert.Equal(t, webx.SourcePAT, p.Source)

	key := "pat:" + hash
	require.Contains(t, cache.ttls, key)
	ttl := cache.ttls[key]
	assert.InDelta(t, 3*time.Minute, ttl, float64(time.Second),
		"cache TTL must clamp to remaining lifetime (expired PATs not delayed by the cache window)")
}

func TestResolvePAT_RevokedRejected(t *testing.T) {
	tok, _, _, err := domain.MintPAT()
	require.NoError(t, err)
	now := time.Now()
	revokedAt := now.Add(-time.Second)
	repo := &fakeRepo{
		getPATByHash: func(string) (PAT, string, bool) {
			return PAT{ID: "pat1", RevokedAt: &revokedAt}, "u1", true
		},
	}
	cache := newFakeCache()
	s := NewTokenService(repo, cache, func() time.Time { return now })

	_, err = s.ResolvePAT(context.Background(), tok)
	assert.Error(t, err, "revoked PAT resolution must fail")
	assert.Empty(t, cache.setKeys, "no cache writes after revocation")
}

func TestResolvePAT_CacheHitSkipsRepo(t *testing.T) {
	tok, hash, _, err := domain.MintPAT()
	require.NoError(t, err)
	repo := &fakeRepo{
		getPATByHash: func(string) (PAT, string, bool) {
			t.Fatal("cache hits must not query the DB")
			return PAT{}, "", false
		},
	}
	cache := newFakeCache()
	cache.store["pat:"+hash] = "u1|u@example.com|U|pat1|pat"
	s := NewTokenService(repo, cache, time.Now)

	p, err := s.ResolvePAT(context.Background(), tok)
	require.NoError(t, err)
	assert.Equal(t, "u1", p.UserID)
	assert.Equal(t, "pat1", p.PATID)
}

func testAuth() *webx.AuthConfig {
	return &webx.AuthConfig{Secret: []byte("test-secret-test-secret-test-32"), SessionTTL: 30 * 24 * time.Hour}
}

func TestVerifyCode_MismatchIncrementsAttempts(t *testing.T) {
	sent := domain.HashCode("111111")
	repo := &fakeRepo{
		latestPending: func(string) (PendingCode, bool) {
			return PendingCode{ID: "c1", CodeHash: sent, ExpiresAt: time.Now().Add(9 * time.Minute)}, true
		},
		consumeCode: func(string) error {
			t.Fatal("code mismatch must not consume")
			return nil
		},
	}
	s := NewSessionService(repo, nil, testAuth(), SessionConfig{AllowSignup: true}, time.Now)

	_, err := s.VerifyCode(context.Background(), "u@example.com", "222222")
	require.Error(t, err)
	assert.Equal(t, apierr.Unauthenticated, err.(*apierr.Error).Code)
	assert.Equal(t, 1, repo.attemptsCalls, "wrong codes must count attempts")
}

func TestVerifyCode_TooManyAttemptsRejected(t *testing.T) {
	sent := domain.HashCode("111111")
	repo := &fakeRepo{
		latestPending: func(string) (PendingCode, bool) {
			return PendingCode{ID: "c1", CodeHash: sent, Attempts: domain.CodeMaxTries,
				ExpiresAt: time.Now().Add(9 * time.Minute)}, true
		},
	}
	s := NewSessionService(repo, nil, testAuth(), SessionConfig{AllowSignup: true}, time.Now)

	_, err := s.VerifyCode(context.Background(), "u@example.com", "111111")
	require.Error(t, err)
	assert.Equal(t, apierr.Unauthenticated, err.(*apierr.Error).Code)
	assert.Zero(t, repo.attemptsCalls, "over-limit code: correct code still denied, attempts no longer counted")
}

func TestVerifyCode_SuccessSignsParseableSession(t *testing.T) {
	code := "111111"
	repo := &fakeRepo{
		latestPending: func(string) (PendingCode, bool) {
			return PendingCode{ID: "c1", CodeHash: domain.HashCode(code),
				ExpiresAt: time.Now().Add(9 * time.Minute)}, true
		},
		consumeCode: func(id string) error {
			assert.Equal(t, "c1", id)
			return nil
		},
		upsertUser: func(email string) (User, error) {
			return User{ID: "u1", Email: email, DisplayName: "U"}, nil
		},
	}
	auth := testAuth()
	s := NewSessionService(repo, nil, auth, SessionConfig{AllowSignup: true}, time.Now)

	res, err := s.VerifyCode(context.Background(), "U@Example.com", code)
	require.NoError(t, err)
	assert.Equal(t, "u1", res.User.ID)
	claims, err := auth.VerifySession(res.Token)
	require.NoError(t, err, "issued session JWTs must be verifiable")
	assert.Equal(t, "u1", claims.Sub)
	assert.Equal(t, "u@example.com", claims.Email, "emails normalized before entering the flow")
}

func TestVerifyCode_DevCodeBypassesPendingLookup(t *testing.T) {
	repo := &fakeRepo{
		latestPending: func(string) (PendingCode, bool) {
			t.Fatal("backdoor code takes the bypass, must not query the code row")
			return PendingCode{}, false
		},
		upsertUser: func(email string) (User, error) {
			return User{ID: "u1", Email: email}, nil
		},
	}
	s := NewSessionService(repo, nil, testAuth(), SessionConfig{AllowSignup: true, DevCode: "000000"}, time.Now)

	res, err := s.VerifyCode(context.Background(), "u@example.com", "000000")
	require.NoError(t, err)
	assert.NotEmpty(t, res.Token)
}

func TestVerifyCode_SignupDisabledUnknownEmail(t *testing.T) {
	repo := &fakeRepo{
		getByEmail: func(string) (User, bool) { return User{}, false },
		upsertUser: func(string) (User, error) {
			t.Fatal("unknown emails must not create users when signup is closed")
			return User{}, nil
		},
	}
	s := NewSessionService(repo, nil, testAuth(), SessionConfig{AllowSignup: false}, time.Now)

	_, err := s.VerifyCode(context.Background(), "newbie@example.com", "111111")
	require.Error(t, err)
	assert.Equal(t, apierr.Forbidden, err.(*apierr.Error).Code, "signup gate before code verification (internal archive 04 §2.1)")
}

func TestSendCode_ResendCooldown(t *testing.T) {
	repo := &fakeRepo{
		latestPending: func(string) (PendingCode, bool) {
			return PendingCode{CreatedAt: time.Now().Add(-30 * time.Second)}, true
		},
		insertCode: func(string, string, time.Time) error {
			t.Fatal("no more codes within the cooldown")
			return nil
		},
	}
	s := NewSessionService(repo, nil, testAuth(), SessionConfig{AllowSignup: true}, time.Now)

	err := s.SendCode(context.Background(), "u@example.com")
	require.Error(t, err)
	assert.Equal(t, apierr.RateLimited, err.(*apierr.Error).Code)
}
