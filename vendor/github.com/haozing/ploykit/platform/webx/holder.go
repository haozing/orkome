package webx

import (
	"context"
	"net/http"
	"sync/atomic"
)

// PrincipalCell 是单请求生命周期的 Principal 指针容器（W11）。
// 用途：AccessLog 在 Authenticate 外层，请求派生 ctx（WithPrincipal）不会
// 回流到 AccessLog 持有的原 request——认证身份需要一个可变通道从内层写到
// 外层。并发安全（atomic）：单请求内 Set/Get 本在同一条中间件 goroutine 上，
// 但 cell 经 ctx 可达后，业务 handler 若把 request 交给其它 goroutine 也不
// 产生 data race。
type PrincipalCell struct{ p atomic.Pointer[Principal] }

// Set 覆写当前值（nil = 匿名）。
func (c *PrincipalCell) Set(p *Principal) { c.p.Store(p) }

// Get 读当前值（未 Set 过返回 nil）。
func (c *PrincipalCell) Get() *Principal { return c.p.Load() }

type principalCellKey struct{}

func withPrincipalCell(ctx context.Context, cell *PrincipalCell) context.Context {
	return context.WithValue(ctx, principalCellKey{}, cell)
}

// principalCellFrom 取 holder 注入的 cell；未经 PrincipalHolder 的链返回 nil。
func principalCellFrom(ctx context.Context) *PrincipalCell {
	cell, _ := ctx.Value(principalCellKey{}).(*PrincipalCell)
	return cell
}

// fillPrincipalCell 把认证成功的 Principal 写进 ctx 里的 cell（若链上挂了
// PrincipalHolder）。cell 缺位时无操作——身份仍经 WithPrincipal 进 ctx，
// 内层消费方不受影响。
func fillPrincipalCell(ctx context.Context, p *Principal) {
	if cell := principalCellFrom(ctx); cell != nil {
		cell.Set(p)
	}
}

// PrincipalHolder 为每个请求分配独立的 *PrincipalCell 注入 ctx（W11 修复
// "AccessLog 的 user_id 恒空"：AccessLog 位于 Authenticate 外层，此前的 ctx
// Principal 读取拿到的永远是派生前的请求）。链序要求：
//
//	PrincipalHolder（最外）→ AccessLog → … → Authenticate → handler
//
// Authenticate 认证成功时顺带填充 ctx 里的 cell（见 auth.go），AccessLog 在
// 请求收尾读取（见 middleware.go）；cell 缺位（未挂 holder）时 AccessLog
// 回落到 W11 之前的 ctx 读取，行为不变。
//
// 设计取舍（2026-10-07，W11）：备选方案是装配级共享 cell（PrincipalHolder
// 返回 (middleware, cell)，Authenticate/AccessLog 经参数接收）——共享 cell
// 在并发请求间串写（请求 A 认证后、AccessLog 读取前被请求 B 覆写，
// user_id 记到别人头上；匿名请求还会读到前一请求的残留值）。per-request
// cell 经 ctx 传递是唯一无串号形态，且 Authenticate/AccessLog 签名零变更，
// 是侵入最小的选型。并发安全由 cell 自身的原子语义兜底（见 PrincipalCell）。
func PrincipalHolder() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, r.WithContext(withPrincipalCell(r.Context(), &PrincipalCell{})))
		})
	}
}
