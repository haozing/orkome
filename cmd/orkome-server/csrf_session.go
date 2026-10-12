package main

// 产品侧 CSRF 链：webx.CSRFConditional 的镜像，唯一增量是 csrf.MaxAge。
//
// 为什么不直接用框架实现：ploykit v0.4.1 的 webx.CSRFConfig 没有 MaxAge
// 旋钮，gorilla/csrf 默认令牌寿命 12h，而 orkome 会话 7 天——SPA 长闲置
// 后首个写请求 403，且前端缓存 token 无恢复路径，写面全灭（FP-01 P1）。
// ploykit 暴露 CSRFConfig.MaxAge 后应整体删除本文件并接回
// webx.CSRFConditional，届时仅需保留 ExemptPrefixes / PATPrefix 两个配置值。

import (
	"errors"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gorilla/csrf"

	"github.com/haozing/ploykit/platform/webx"
)

const (
	// wsPathPrefix：WebSocket 握手无 CSRF 语义（连接认证走 auth 首帧 / PAT）。
	wsPathPrefix = "/ws"
	// webhookInboundPrefix：automation 公开入站（POST /webhooks/autopilots/{token}）。
	// 自有 token+HMAC+时间戳+限流四重防护、无 cookie 会话语义；CSRF 前置拦截
	// 会让正确签名的外部触发也 403（FP-13 P1-4）。带尾斜杠避免误伤其他路径前缀。
	webhookInboundPrefix = "/webhooks/"
	// uiTokenConsumePrefix：ui-ticket 回验（POST /api/ui-tokens/consume，FP-15
	// P1-1）。以票自证——单次 jti + 60s TTL + DB 原子消费即凭证，调用方是
	// pack iframe / pack 后端，没有会话 cookie 也没有 PAT，CSRF 前置拦截会把
	// 回验链整体锁死。必须用完整路径做前缀：短前缀 "/api/ui-tokens" 会连铸票
	// 端点（仍需会话）一起豁免。
	uiTokenConsumePrefix = "/api/ui-tokens/consume"
)

// csrfChain 与 webx.CSRFConditional 行为一致（豁免前缀 → PAT 请求放行 →
// loopback origin 归一 → 非安全方法令牌校验），另按 ttl 传入 csrf.MaxAge
// 与会话 TTL 对齐。
func csrfChain(cfg *webx.CSRFConfig, secure bool, ttl time.Duration) func(http.Handler) http.Handler {
	trusted := cfg.TrustedOrigins
	if cfg.TrustLocalhostAnyPort {
		trusted = append(append([]string{}, cfg.TrustedOrigins...), loopbackBareOrigins...)
	}
	return func(next http.Handler) http.Handler {
		var inner http.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, r)
		})
		protected := csrf.Protect(cfg.Key,
			csrf.CookieName("ploykit_csrf"),
			csrf.Path("/"),
			csrf.Secure(secure), csrf.HttpOnly(true), csrf.SameSite(csrf.SameSiteLaxMode),
			csrf.TrustedOrigins(trusted),
			csrf.MaxAge(int(ttl/time.Second)), // FP-01 P1：对齐会话 TTL（框架默认 12h）
			csrf.ErrorHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				csrfWriteError(w, r, trusted)
			})),
		)(inner)
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			for _, prefix := range cfg.ExemptPrefixes {
				if strings.HasPrefix(r.URL.Path, prefix) {
					next.ServeHTTP(w, r)
					return
				}
			}
			if strings.HasPrefix(r.Header.Get("Authorization"), "Bearer "+patPrefixOf(cfg)) {
				next.ServeHTTP(w, r)
				return
			}
			if cfg.TrustLocalhostAnyPort {
				r = withLoopbackOriginNormalized(r)
			}
			if !secure {
				r = csrf.PlaintextHTTPRequest(r)
			}
			protected.ServeHTTP(w, r)
		})
	}
}

// patPrefixOf 与 webx 的回落语义一致：未配置时回落框架默认 "tk_"。
// orkome 必须显式传 "ork_"——PAT 前缀错配会让所有 PAT 写请求被 CSRF 拦死。
func patPrefixOf(cfg *webx.CSRFConfig) string {
	if cfg.PATPrefix == "" {
		return webx.DefaultPATPrefix
	}
	return cfg.PATPrefix
}

// ---- 以下三个 helper 为 webx/csrf.go 未导出逻辑的镜像（loopback 归一 +
// 403 响应形状），框架暴露 MaxAge 后随本文件一并删除。 ----

var loopbackBareOrigins = []string{"localhost", "127.0.0.1", "[::1]"}

func isLoopbackHostPort(hostport string) bool {
	host := hostport
	if h, _, err := net.SplitHostPort(hostport); err == nil {
		host = h
	}
	return host == "localhost" || host == "127.0.0.1" || host == "::1"
}

func normalizeLoopbackOrigin(value string) (string, bool) {
	if value == "" {
		return "", false
	}
	u, err := url.Parse(value)
	if err != nil || u.Host == "" || !isLoopbackHostPort(u.Host) {
		return "", false
	}
	if h := u.Hostname(); strings.Contains(h, ":") {
		u.Host = "[" + h + "]" // url.URL.Host keeps IPv6 brackets
	} else {
		u.Host = h
	}
	return u.String(), true
}

func withLoopbackOriginNormalized(r *http.Request) *http.Request {
	origin, originOK := normalizeLoopbackOrigin(r.Header.Get("Origin"))
	referer, refererOK := normalizeLoopbackOrigin(r.Header.Get("Referer"))
	if !originOK && !refererOK {
		return r
	}
	r = r.Clone(r.Context())
	if originOK {
		r.Header.Set("Origin", origin)
	}
	if refererOK {
		r.Header.Set("Referer", referer)
	}
	return r
}

func csrfWriteError(w http.ResponseWriter, r *http.Request, trusted []string) {
	reason := csrf.FailureReason(r)
	if errors.Is(reason, csrf.ErrBadOrigin) || errors.Is(reason, csrf.ErrNoReferer) || errors.Is(reason, csrf.ErrBadReferer) {
		webx.WriteError(w, http.StatusForbidden, webx.CodeForbidden,
			"CSRF origin check failed: request Origin is not in the trusted origins list",
			map[string]any{
				"origin_check":    "failed",
				"request_origin":  r.Header.Get("Origin"),
				"request_referer": r.Referer(),
				"trusted_origins": trusted,
				"reason":          reason.Error(),
			})
		return
	}
	webx.WriteError(w, http.StatusForbidden, webx.CodeForbidden, "CSRF token missing or invalid", nil)
}
