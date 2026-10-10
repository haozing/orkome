package webx

import (
	"crypto/sha256"
	"net/http"
	"strings"

	"github.com/gorilla/csrf"
)

type CSRFConfig struct {
	Key []byte
	// TrustedOrigins 是允许跨域提交的来源，格式 host[:port]（gorilla/csrf 按 Origin 的
	// Host 字段精确匹配，不带 scheme），如开发模式 "localhost:5173"。
	// 同源部署无需配置；前后端分开部署/开发代理时必须显式列出。
	TrustedOrigins []string
	// ExemptPrefixes 命中路径前缀的请求整体豁免 CSRF——供无法携带浏览器 token 的
	// 服务器间回调入口（如计费渠道回调 /webhooks/billing/）。前缀全方法匹配。
	// 语义来自 SEC-V9；豁免入口的真伪校验由对应适配器自行承担（如签名验证）。
	ExemptPrefixes []string
	// PATPrefix PAT 令牌前缀（I2 单一真源）：PAT 豁免判定与 Authenticate 同口径。
	// 空 = DefaultPATPrefix "tk_"；配置后须与 AuthConfig.PATPrefix 同值。
	PATPrefix string
}

// patPrefix 返回生效的 PAT 前缀（空配置回退默认，与 AuthConfig 同语义）。
func (c *CSRFConfig) patPrefix() string {
	if c.PATPrefix == "" {
		return DefaultPATPrefix
	}
	return c.PATPrefix
}

// DeriveCSRFKey 从会话签名密钥派生独立的 CSRF 密钥，避免密钥复用。
// 参数名 signingSecret（W9 化石修正）：本仓会话是不透明令牌非 JWT，实参是
// 会话 pepper（example 装配处传 sessCfg.SecretPepper），旧名 jwtSecret 误导。
func DeriveCSRFKey(signingSecret []byte) []byte {
	prefix := []byte("ploykit-csrf-v1:")
	buf := make([]byte, 0, len(prefix)+len(signingSecret))
	buf = append(buf, prefix...)
	buf = append(buf, signingSecret...)
	h := sha256.Sum256(buf)
	return h[:]
}

// CSRFConditional 对浏览器会话请求强制 CSRF 校验，PAT 请求豁免
// （PAT 不受 CSRF 浏览器攻击面影响）。secure=false 供本地 http 开发。
func CSRFConditional(cfg *CSRFConfig, secure bool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		var inner http.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, r)
		})
		protected := csrf.Protect(cfg.Key,
			csrf.CookieName("ploykit_csrf"),
			csrf.Path("/"),
			csrf.Secure(secure), csrf.HttpOnly(true), csrf.SameSite(csrf.SameSiteLaxMode),
			csrf.TrustedOrigins(cfg.TrustedOrigins),
			// CSRF 拒绝统一走 {error,message} JSON 信封（契约见 docs/openapi.yaml）：
			// gorilla/csrf 默认 ErrorHandler 输出纯文本，破坏全站错误格式一致性。
			csrf.ErrorHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				WriteError(w, http.StatusForbidden, CodeForbidden, "CSRF token missing or invalid", nil)
			})),
		)(inner)
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			for _, prefix := range cfg.ExemptPrefixes {
				if strings.HasPrefix(r.URL.Path, prefix) {
					next.ServeHTTP(w, r)
					return
				}
			}
			if isPATRequest(r, cfg.patPrefix()) {
				next.ServeHTTP(w, r)
				return
			}
			if !secure {
				r = csrf.PlaintextHTTPRequest(r)
			}
			protected.ServeHTTP(w, r)
		})
	}
}

// isPATRequest 判定请求是否走 PAT 认证通道（豁免 CSRF）。
// 豁免判断不能依赖 Principal：装配上 csrfMW 包在 Authenticate 外层时
// Principal 尚未注入，此前的豁免分支是死代码（SEC-V7 / API-FT2-1）。
// 改为以 Authorization: Bearer <patPrefix> 头作为 PAT 认证的请求特征
// （与 Authenticate 同口径，前缀由调用方传入——I2 单一真源）：
// 非 cookie 认证不在 CSRF 威胁模型内；会话路径仍强制 CSRF。
// 已注入 PAT Principal 时同样豁免（双保险，兼容任意装配顺序）。
func isPATRequest(r *http.Request, patPrefix string) bool {
	if p := PrincipalFrom(r.Context()); p != nil && p.Source == SourcePAT {
		return true
	}
	return strings.HasPrefix(r.Header.Get("Authorization"), "Bearer "+patPrefix)
}

func CSRFMaskedToken(r *http.Request) string { return csrf.Token(r) }
