package webx

import (
	"context"
	"net/http"
	"strings"
)

// Source 标识 Principal 的来路：浏览器会话 or 个人访问令牌（PAT）or 系统。
type Source string

const (
	SourceSession Source = "session"
	SourcePAT     Source = "pat"
	SourceSystem  Source = "system"
)

// ProjectScope 是 workspace 之下的可选第二层 scope（嵌套 scope 可选层，只做两层）。
// 由产品的 workspace 中间件在解析出 project 上下文时填充（框架不拥有 project 实体，
// ID 形态与角色词汇均由产品定义）。workspace 之上的层级不存在。
type ProjectScope struct {
	// ID project 标识（产品自定形态，框架不解释、不落库）。
	// 填了 Project 就必须给 ID：空 ID 的承载是产品侧 bug。
	ID string
	// Role project 层角色（产品角色词汇）。authz 在 project 语境下用此角色做判定，
	// 不与 workspace 层 Role 求并集（显式集合，无角色继承）；
	// 空 = 在该 project 无角色 → 授权判定全拒。
	Role string
}

// CredentialScope 是凭据（PAT，未来含 service account）的 scope 约束：
// 凭据 = 主体 + scope 集 + 权限集（GitHub FG-PAT / Stripe restricted key 的轴）。
//
// 三态语义（列级 NULL 与空集是两个状态，勿混）：
//   - nil（指针为零值，DB 两列皆 NULL）：无约束 = 现有 PAT 语义（数据默认值，非兼容双轨）；
//   - 集合非 nil：约束生效，元素命中才放行；
//   - 集合非 nil 但为空（'{}'）：显式约束为零个工作区/权限点 → 全拒。
type CredentialScope struct {
	// WorkspaceIDs 可访问工作区子集；nil = 不约束；空集 = 拒绝一切工作区。
	WorkspaceIDs []string `json:"workspace_ids,omitempty"`
	// Permissions 可用权限点子集；nil = 不约束；空集 = 拒绝一切权限点。
	// 元素支持 "域:*" 通配（与 authz.RoleSet 域级通配同语义，如 "tasks:*"）。
	Permissions []string `json:"permissions,omitempty"`
}

// AllowsWorkspace 报告工作区是否在凭据约束内。
// s 为 nil（无约束凭据：会话、未带 scope 的 PAT）恒放行；
// 空集 = 约束为零个工作区，恒拒绝。
func (s *CredentialScope) AllowsWorkspace(workspaceID string) bool {
	if s == nil || s.WorkspaceIDs == nil {
		return true
	}
	for _, id := range s.WorkspaceIDs {
		if id == workspaceID {
			return true
		}
	}
	return false
}

// AllowsPermission 报告权限点是否在凭据约束内。
// s 为 nil 恒放行；子集内精确命中或 "域:*" 通配命中（覆盖 *_own 变体）才放行。
func (s *CredentialScope) AllowsPermission(perm string) bool {
	if s == nil || s.Permissions == nil {
		return true
	}
	dom := permDomain(perm)
	for _, p := range s.Permissions {
		if p == perm {
			return true
		}
		if d, ok := strings.CutSuffix(p, ":*"); ok && d == dom {
			return true
		}
	}
	return false
}

// permDomain 取权限点的域前缀（"tasks:write_own" → "tasks"；无冒号返回原串）。
func permDomain(perm string) string {
	if i := strings.IndexByte(perm, ':'); i > 0 {
		return perm[:i]
	}
	return perm
}

// Principal 是贯穿整个请求链路的身份与工作区上下文（身份 + 工作区上下文的统一载体）。
// 认证中间件填充 UserID/Email/Name/SessionID/PATID/Source/IsPlatformAdmin；
// 工作区中间件在其上追加 WorkspaceID/Role（每请求即时校验成员身份，不信任缓存）。
type Principal struct {
	UserID      string
	Email       string
	Name        string
	Source      Source
	SessionID   string
	PATID       string
	WorkspaceID string
	Role        string
	// Project 可选的第二层 scope（workspace 之下的 project）。nil = 单层
	//（今天的样子，零行为变化）；非 nil 时授权判定用 Project.Role 取代 Role
	//（按所在层级取角色，不求并集）。仅设置 Role 而不留 ID 是产品侧 bug。
	Project *ProjectScope
	// Scope 凭据的 scope 约束（C3，ResolvePAT 填充；会话恒为 nil）。
	// nil = 无约束 = 现有语义；非 nil 时执行点按子集收敛凭据爆炸半径。
	Scope *CredentialScope
	// AgentID 标识凭据绑定的 agent（PAT→agent 绑定与 runtime 写围栏的扩展位）。
	// 空值 = 无绑定（普通用户会话/未绑定 agent 的 PAT）。
	AgentID string
	// IsPlatformAdmin 标记平台管理员（user.is_platform_admin 列）。
	// 由 VerifySession / ResolvePAT 在认证时从用户表读取，admin 中间件消费。
	IsPlatformAdmin bool
	// ImpersonatedBy 非空 = 本会话是管理员模拟会话（session.impersonated_by
	// 列，迁移 034 / ADR 0008；值为发起模拟的平台管理员 userID）。
	// VerifySession 从 session 行带出；普通会话 / PAT / 系统来源恒为空串。
	// 授权语义：非空即吊销模拟权——admin impersonate 入口据此拒绝链式模拟。
	ImpersonatedBy string
}

type principalKey struct{}

func WithPrincipal(ctx context.Context, p *Principal) context.Context {
	return context.WithValue(ctx, principalKey{}, p)
}

func PrincipalFrom(ctx context.Context) *Principal {
	p, _ := ctx.Value(principalKey{}).(*Principal)
	return p
}

func PrincipalFromRequest(r *http.Request) *Principal { return PrincipalFrom(r.Context()) }

// RequireAuth 拒绝匿名请求。
func RequireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if PrincipalFromRequest(r) == nil {
			ErrUnauthenticated(w, "authentication required")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// RequireHuman 限定浏览器会话（PAT 不能改密码/建令牌等敏感操作）。
// 匿名 → 401（未认证）；已认证但非会话（PAT/系统）→ 403（API-FT2-12）。
func RequireHuman(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		p := PrincipalFromRequest(r)
		if p == nil {
			ErrUnauthenticated(w, "authentication required")
			return
		}
		if p.Source != SourceSession {
			ErrForbidden(w, "human session required")
			return
		}
		next(w, r)
	}
}

// RequestID 取出请求标识，供审计与日志关联。ctx 优先（W5）：中间件链上
// RequestIDMiddleware 生成的 uuid v7 只进 ctx，此前本函数只读入站头——
// 无入站头时（绝大多数请求）返回空串，example 业务日志无法与 AccessLog
// 的 request_id 关联；头兜底保留，兼容未经中间件的裸用。
func RequestID(r *http.Request) string {
	if id := RequestIDFromCtx(r.Context()); id != "" {
		return id
	}
	return r.Header.Get(HeaderRequestID)
}

// P 把业务函数适配为 http.HandlerFunc：要求已认证，注入 Principal。
// 是业务域 handler 的标准签名（example/tasks.go 有用例）。
func P(fn func(w http.ResponseWriter, r *http.Request, p *Principal)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		p := PrincipalFromRequest(r)
		if p == nil {
			ErrUnauthenticated(w, "authentication required")
			return
		}
		fn(w, r, p)
	}
}
