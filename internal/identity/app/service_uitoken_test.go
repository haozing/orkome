package app

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"orkome/internal/contract/apierr"
)

// ---- fakes ----

type fakeUITokenRepo struct {
	// store 模拟 ui_token 表：jti → 行；ConsumeUIToken 的单次原子语义在
	// 服务层契约里由 ok=false 表达，这里用 consumed 标记模拟。
	store    map[string]uitRow
	failRead bool
}

type uitRow struct {
	ws, user, plugin string
	expiresAt        time.Time
	consumed         bool
}

func (f *fakeUITokenRepo) InsertUIToken(_ context.Context, jti, ws, user, plugin string, expiresAt time.Time) error {
	if f.store == nil {
		f.store = map[string]uitRow{}
	}
	f.store[jti] = uitRow{ws: ws, user: user, plugin: plugin, expiresAt: expiresAt}
	return nil
}

func (f *fakeUITokenRepo) ConsumeUIToken(_ context.Context, jti string, now time.Time) (ws, user, plugin string, ok bool, err error) {
	if f.failRead {
		return "", "", "", false, errors.New("db down")
	}
	row, found := f.store[jti]
	if !found || row.consumed || !row.expiresAt.After(now) {
		return "", "", "", false, nil
	}
	row.consumed = true
	f.store[jti] = row
	return row.ws, row.user, row.plugin, true, nil
}

type fakeLocator struct {
	wsItems map[string][]string // workspace → work item ids
	calls   int
	fail    bool
}

func (f *fakeLocator) WorkItemInWorkspace(_ context.Context, ws, id string) (bool, error) {
	f.calls++
	if f.fail {
		return false, errors.New("locator down")
	}
	for _, v := range f.wsItems[ws] {
		if v == id {
			return true, nil
		}
	}
	return false, nil
}

func uitFixture(now time.Time) (*fakeUITokenRepo, *UITokenService) {
	repo := &fakeUITokenRepo{}
	svc := NewUITokenService(repo, []byte("uit-unit-secret-0123456789abcdef"), func() time.Time { return now })
	return repo, svc
}

// ---- consume 四态（FP-15 P1-1 语义锚：凭证=票本身，与调用方会话无关） ----

func TestUITokenConsume_FourStates(t *testing.T) {
	// 相对真实时间（截秒）：JWT exp 校验在 s.now 同钟化后虽以夹具时钟为准，
	// 但固定历史日期是隐患（任何走真实时钟的路径都会判过期）。
	now := time.Now().UTC().Truncate(time.Second)
	repo, svc := uitFixture(now)
	ctx := context.Background()

	// ① 匿名场景等价物：持有有效票即可换出 claims（路由层摘会话门后，
	// handler 不读 Principal——服务层只认票）。
	tok, err := svc.Mint(ctx, "ws-1", "u-1", "fakeblog", "")
	require.NoError(t, err)
	claims, err := svc.Consume(ctx, tok)
	require.NoError(t, err)
	assert.Equal(t, "ws-1", claims.WorkspaceID)
	assert.Equal(t, "u-1", claims.Sub)
	assert.Equal(t, "fakeblog", claims.Plugin)
	assert.Empty(t, claims.WorkItemID, "未绑定票的 work_item_id 为空")

	// ② 二次使用拒绝（jti 单次）。
	_, err = svc.Consume(ctx, tok)
	require.Error(t, err)
	assert.Equal(t, apierr.Unauthenticated, err.(*apierr.Error).Code)

	// ③ 过期票拒绝（把行级 expires_at 推进到过期时钟之前——DB 层 TTL 门槛）。
	tok2, err := svc.Mint(ctx, "ws-1", "u-1", "fakeblog", "")
	require.NoError(t, err)
	jti2 := jtiOf(t, svc, tok2)
	expiredRow := repo.store[jti2]
	expiredRow.expiresAt = now.Add(time.Second)
	repo.store[jti2] = expiredRow
	expiredSvc := NewUITokenService(repo, []byte("uit-unit-secret-0123456789abcdef"),
		func() time.Time { return now.Add(UITokenTTL + time.Second) })
	_, err = expiredSvc.Consume(ctx, tok2)
	require.Error(t, err, "expired ticket must be rejected")
	assert.Equal(t, apierr.Unauthenticated, err.(*apierr.Error).Code)

	// ④ 无票/伪造票拒绝（验签失败、alg 混淆同路）。
	_, err = svc.Consume(ctx, "not-a-token")
	require.Error(t, err)
	assert.Equal(t, apierr.Unauthenticated, err.(*apierr.Error).Code)

	other := NewUITokenService(repo, []byte("another-secret-entirely-x"), func() time.Time { return now })
	tampered, err := other.Mint(ctx, "ws-1", "u-1", "fakeblog", "")
	require.NoError(t, err)
	_, err = svc.Consume(ctx, tampered)
	require.Error(t, err, "wrong-key ticket must not verify")
	assert.Equal(t, apierr.Unauthenticated, err.(*apierr.Error).Code)
}

// jtiOf 从票里解出 jti（测试辅助：过期分支要操纵行级 TTL）。
func jtiOf(t *testing.T, svc *UITokenService, token string) string {
	t.Helper()
	parsed, err := jwt.Parse(token, func(*jwt.Token) (any, error) { return svc.secret, nil },
		jwt.WithValidMethods([]string{"HS256"}))
	require.NoError(t, err)
	mc, ok := parsed.Claims.(jwt.MapClaims)
	require.True(t, ok)
	jti, _ := mc["jti"].(string)
	require.NotEmpty(t, jti)
	return jti
}

// ---- work_item_id 绑定（FP-15 P1-2） ----

func TestUITokenMint_WorkItemBinding(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	_, svc := uitFixture(now)
	loc := &fakeLocator{wsItems: map[string][]string{
		"ws-1": {"11111111-1111-1111-1111-111111111111"},
	}}
	svc.WithWorkItemLocator(loc)
	ctx := context.Background()

	t.Run("legal work item in minting workspace enters claims", func(t *testing.T) {
		const wi = "11111111-1111-1111-1111-111111111111"
		tok, err := svc.Mint(ctx, "ws-1", "u-1", "fakeblog", wi)
		require.NoError(t, err)
		claims, err := svc.Consume(ctx, tok)
		require.NoError(t, err)
		assert.Equal(t, wi, claims.WorkItemID, "工单 id 绑进票 claims")
	})

	t.Run("cross-workspace work item rejected with 400", func(t *testing.T) {
		_, err := svc.Mint(ctx, "ws-1", "u-1", "fakeblog", "22222222-2222-2222-2222-222222222222")
		require.Error(t, err)
		assert.Equal(t, apierr.Validation, err.(*apierr.Error).Code)
	})

	t.Run("non-uuid rejected before the locator", func(t *testing.T) {
		before := loc.calls
		_, err := svc.Mint(ctx, "ws-1", "u-1", "fakeblog", "not-a-uuid")
		require.Error(t, err)
		assert.Equal(t, apierr.Validation, err.(*apierr.Error).Code)
		assert.Equal(t, before, loc.calls, "非 UUID 不打 locator（PG uuid cast 会炸成 500）")
	})

	t.Run("locator outage surfaces as error not silent pass", func(t *testing.T) {
		loc.fail = true
		defer func() { loc.fail = false }()
		_, err := svc.Mint(ctx, "ws-1", "u-1", "fakeblog", "11111111-1111-1111-1111-111111111111")
		require.Error(t, err, "locator 故障必须失败而非放行")
	})

	t.Run("binding without locator configured fails closed", func(t *testing.T) {
		_, bare := uitFixture(now)
		_, err := bare.Mint(ctx, "ws-1", "u-1", "fakeblog", "11111111-1111-1111-1111-111111111111")
		require.Error(t, err, "未装配 locator 时携带 work_item_id 的铸票必须拒绝")
		_, err = bare.Mint(ctx, "ws-1", "u-1", "fakeblog", "")
		require.NoError(t, err, "不带 work_item_id 的铸票不受 locator 缺位影响")
	})
}
