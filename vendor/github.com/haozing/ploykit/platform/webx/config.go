package webx

import "net/http"

// ConfigDeps /config 端点的可选扩展面。函数注入——platform 不 import 业务域，
// OAuth 提供商/计费渠道名录由组合根喂入；nil 时输出空数组。
type ConfigDeps struct {
	OAuthProviders  func() []string
	BillingChannels func() []string
	// Extra 可选扩展面：返回的键值对原样并入响应体（产品级 /config 扩展，如站点
	// 公告横幅/维护模式等公开配置）。nil 安全；与键冲突时以 Extra 为准。
	Extra func() map[string]any
}

// ConfigHandler 返回 GET /config 处理器：基础响应体恒含
// {"csrf_token": string, "oauth_providers": []string, "billing_channels": []string}，
// ConfigDeps.Extra 返回的键值对原样并入（冲突以 Extra 为准）。
// csrf_token 是 @ploykit/client 的 CSRF bootstrap 硬契约（packages/client/src/api.ts
// 缺字段抛 E_CSRF_BOOTSTRAP）——取 CSRFMaskedToken(r)。未过 CSRF 中间件语境时可能为
// 空串，属预期：真实装配中 /config 在 CSRF 中间件内层，token 已由中间件生成。
// 路由注册留在产品组合根（tools/check_api.py 的扫描面不含 platform/）。
func ConfigHandler(d ConfigDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		body := map[string]any{
			"csrf_token":       CSRFMaskedToken(r),
			"oauth_providers":  nonEmptyStrings(d.OAuthProviders),
			"billing_channels": nonEmptyStrings(d.BillingChannels),
		}
		// Extra 键原样并入；与基础三键冲突时以 Extra 为准（产品级扩展覆盖默认）。
		if d.Extra != nil {
			for k, v := range d.Extra() {
				body[k] = v
			}
		}
		WriteJSON(w, http.StatusOK, body)
	}
}

// nonEmptyStrings 规范扩展面输出：nil 函数或返回 nil 时给空切片，
// 保证 JSON 恒为数组 [] 而非 null（前端按数组消费，null 会破坏契约）。
func nonEmptyStrings(f func() []string) []string {
	if f == nil {
		return []string{}
	}
	if s := f(); s != nil {
		return s
	}
	return []string{}
}
