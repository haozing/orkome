package webx

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"strings"
	"time"
)

// SessionStore 由 pgrepo 实现：DB 不透明令牌会话（可吊销，三家共识方案）。
type SessionStore interface {
	// CreateSession 签发新会话，返回明文令牌（只此一次出现）与过期时间。
	CreateSession(ctx context.Context, userID, ipHash, userAgent string, now time.Time) (token string, exp time.Time, err error)
	// VerifySession 校验令牌：存在、未吊销、未过期、用户未禁用、未早于 tokens_valid_after。
	VerifySession(ctx context.Context, token string, now time.Time) (*Principal, error)
	// RenewSession 条件滑动续期（W1）：剩余寿命不足半 TTL 时才写库——节流
	// 与"是否真续期"的判定由实现用单条 SQL 一步完成，消除每请求 UPDATE
	// 写放大。renewed=false 表示未到节流阈值未写库（exp 为零值，调用方不
	// 得使用）；错误如实上抛——旧签名让实现吞错返回合成 exp，是审计点名
	// 的死分支根源。续期值仍受绝对上限约束。
	RenewSession(ctx context.Context, token string, now time.Time) (exp time.Time, renewed bool, err error)
	// RevokeSession 登出。
	RevokeSession(ctx context.Context, token string) error
}

// PATLookup 把 Bearer 令牌解析为 Principal（pgrepo 实现，查 personal_access_token）。
type PATLookup interface {
	ResolvePAT(ctx context.Context, token string, now time.Time) (*Principal, error)
}

// DefaultPATPrefix 是 PAT 令牌的默认前缀（I2 单一真源：此前 "tk_" 字面量
// 散落在 auth.go 与 csrf.go 两处硬编码）。产品改前缀时须与 identity
// TokenService 的 WithPATPrefix、pgrepo Config.PATPrefix 同值注入。
const DefaultPATPrefix = "tk_"

// AuthConfig 汇总认证相关的不可变配置。
type AuthConfig struct {
	// CookieName 浏览器会话 Cookie 名。
	CookieName string
	// CookieDomain 留空 = 仅当前主机。
	CookieDomain string
	Secure       bool
	// SessionTTL 滑动窗口；AbsoluteTTL 是会话自创建起的最长寿命。
	SessionTTL  time.Duration
	AbsoluteTTL time.Duration
	// IPHashSecret 为登录限速/会话记录中的 IP 哈希加盐（不必与业务密钥相同）。
	IPHashSecret string
	// PATPrefix Bearer PAT 令牌前缀（I2）；空 = DefaultPATPrefix "tk_"。
	// 零配置行为不变；配置后须与 identity 侧 PAT 前缀同值。
	PATPrefix string
}

// patPrefix 返回生效的 PAT 前缀（空配置回退默认，单一真源入口）。
func (c *AuthConfig) patPrefix() string {
	if c.PATPrefix == "" {
		return DefaultPATPrefix
	}
	return c.PATPrefix
}

func DefaultAuthConfig() AuthConfig {
	return AuthConfig{
		CookieName:  "tk_auth",
		SessionTTL:  7 * 24 * time.Hour,
		AbsoluteTTL: 30 * 24 * time.Hour,
		Secure:      true,
	}
}

// MintToken 生成 32 字节随机不透明令牌（hex 编码）。
func MintToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// HashToken 令牌入库摘要：sha256（令牌本身 256 位随机，无需加盐）。
func HashToken(token string) string {
	h := sha256.Sum256([]byte(token))
	return hex.EncodeToString(h[:])
}

// HashIP 登录限速与审计用的 IP 指纹（加盐，避免存明文 IP）。
func HashIP(ip, secret string) string {
	h := sha256.Sum256([]byte("tk-ip:" + secret + ":" + ip))
	return hex.EncodeToString(h[:])
}

// Authenticate 是全局认证中间件（双通道：会话 Cookie + PAT）：
// Authorization: Bearer <PATPrefix>…（默认 tk_，I2 配置化）→ PAT；
// 会话 Cookie → DB 校验 + 条件滑动续期。
// 匿名请求放行（公开路由用），受保护路由再挂 RequireAuth。
func Authenticate(cfg *AuthConfig, sessions SessionStore, pats PATLookup) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := r.Context()
			now := time.Now()

			if h := r.Header.Get("Authorization"); strings.HasPrefix(h, "Bearer "+cfg.patPrefix()) {
				token := strings.TrimPrefix(h, "Bearer ")
				p, err := pats.ResolvePAT(ctx, token, now)
				if err != nil {
					// DB 故障不等于令牌无效：按 503 上抛，避免把后端故障伪装成认证失败。
					ErrUnavailable(w, "认证服务暂不可用")
					return
				}
				if p == nil {
					ErrUnauthenticated(w, "invalid token")
					return
				}
				fillPrincipalCell(ctx, p) // W11：外层 AccessLog 经 holder cell 读 user_id
				next.ServeHTTP(w, r.WithContext(WithPrincipal(ctx, p)))
				return
			}

			ck, err := r.Cookie(cfg.CookieName)
			if err != nil || ck.Value == "" {
				next.ServeHTTP(w, r.WithContext(ctx))
				return
			}
			p, err := sessions.VerifySession(ctx, ck.Value, now)
			if err != nil {
				ErrUnavailable(w, "认证服务暂不可用")
				return
			}
			if p == nil {
				// 无效/过期会话按匿名放行（公开路由继续可访问，受保护路由由 RequireAuth 拦截）。
				next.ServeHTTP(w, r.WithContext(ctx))
				return
			}
			// 条件滑动续期（W1）：节流判定在 RenewSession 的 SQL 里完成，
			// 只有真续期（renewed=true）才刷新浏览器 cookie——旧实现拿
			// 【续期后】的 exp 判 now+TTL/2 > exp 恒 false，cookie 永不滑动，
			// 活跃用户第 7 天被浏览器强制登出。
			// 续期失败不阻断请求（VerifySession 已过，尽力而为语义）。
			exp, renewed, err := sessions.RenewSession(ctx, ck.Value, now)
			if err == nil && renewed {
				cfg.SetSessionCookie(w, ck.Value, exp)
			}
			fillPrincipalCell(ctx, p) // W11：外层 AccessLog 经 holder cell 读 user_id
			next.ServeHTTP(w, r.WithContext(WithPrincipal(ctx, p)))
		})
	}
}

func (c *AuthConfig) SetSessionCookie(w http.ResponseWriter, token string, exp time.Time) {
	http.SetCookie(w, &http.Cookie{
		Name: c.CookieName, Value: token, Path: "/",
		Domain:   c.CookieDomain,
		Expires:  exp,
		HttpOnly: true, Secure: c.Secure, SameSite: http.SameSiteLaxMode,
	})
}

func (c *AuthConfig) ClearSessionCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name: c.CookieName, Value: "", Path: "/", Domain: c.CookieDomain,
		MaxAge: -1, HttpOnly: true, Secure: c.Secure, SameSite: http.SameSiteLaxMode,
	})
}
