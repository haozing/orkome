// Package pg 提供连接池（主/副本 + 自愈看门狗）、事务封装（Within/Tx/Read）
// 与多租户上下文通道（本文件的 WithTenant/WithService）。
//
// # RLS 八坑清单（精要，全文见 docs/dev-environment.md「RLS 上下文通道」）
//
//  1. 池化：GUC 必须事务级注入（set_config 第三参 true = SET LOCAL）。本包
//     永不提供会话级 setter——池化连接跨请求复用，会话级租户上下文会把
//     A 租户的身份泄漏给下一个复用该连接的 B 租户请求（pgbouncer transaction
//     模式下同样成立，seedfa.st 一手实测）；这不是实现取舍，是安全性红线。
//  2. autocommit：is_local=true 的 GUC 只覆盖当前事务。脱离显式事务的语句
//     （池上裸 Exec）各自独立，set 完下一条就丢。因此上下文通道必须由
//     WithTenant 拥有事务边界（Begin → set_config → fn → Commit），业务
//     代码不得在通道外自行 set_config。
//  3. 空串：GUC 未设时 current_setting(name, true) 返回 NULL，被设过 ” 则
//     返回 ”——”::uuid 会抛 22P02。且 SET LOCAL 的自定义 GUC 在事务结束
//     后复位为【空串占位符】而非消失（psql 实测：current_setting 返回 ”
//     而非 NULL），故访问器必须 nullif(v,”)::uuid（见 example 迁移 1007 的
//     app.workspace_id()，Supabase auth.uid() 同型）。
//  4. owner 绕过：表 owner 默认无视 RLS。单账号演示模式须 FORCE ROW LEVEL
//     SECURITY（owner 也受策略约束）；生产用三账号分离（ploykit_app 运行 /
//     ploykit_migrator 迁移=owner / ploykit_service 逃生口），运行账号非
//     owner 即天然受策略约束。superuser 与 BYPASSRLS 无视一切策略，含 FORCE。
//  5. 静默失败：策略不匹配时 RLS 不报错、只返回 0 行（PostHog 教训：读少了
//     行且无法度量）。隔离性只能靠三态测试证明（example/internal/task/
//     rls_test.go：带上下文只见本租户 / 裸查 0 行 / 跨租户写被拒），并且
//     测试连接必须用非 owner 运行账号——owner 测过等于没测。
//  6. InitPlan 三件套：策略谓词写 workspace_id = (select app.workspace_id())
//     ——(select …) 包装让函数每语句求值一次而非每行；函数标 STABLE（否则
//     优化器不敢提升）；租户列上有前导索引（否则每行回表）。三缺一都会把
//     RLS 变成全表顺序扫描。
//  7. 唯一约束必须带租户：GLOBAL 唯一约束会让 B 租户通过 23505 冲突探测出
//     A 租户资源的存在（跨租户存在性泄漏），也直接破坏隔离。一律
//     UNIQUE(workspace_id, …)。
//  8. 递归策略 42P17：策略子查询查策略所在表自身会无限递归报 42P17。把它
//     当配置错误 lint 掉（PostHog #76182），需要关联数据就物化进侧表。
package pg

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// GUC 键名（访问器函数与策略引用同一组名字；自定义 GUC 必须带点号前缀）。
const (
	gucWorkspaceID = "app.workspace_id"
	gucUserID      = "app.user_id"
)

// Identity 是一次请求的租户上下文：进入 RLS 的唯一事实来源。
// Go 侧已完成 token 鉴权（authz 横切层），这里只发明文身份给数据库，
// 不把 JWT 下发给数据库（PostgREST/capyrls 同款裁定）。
type Identity struct {
	WorkspaceID uuid.UUID
	UserID      uuid.UUID
}

// Beginner 是事务通道的最小依赖形态，*DB 与 *pgxpool.Pool 均满足——
// WithTenant/WithService 既接受框架 DB，也接受裸池（example 的 Deps.Pool 形态）。
type Beginner interface {
	Begin(ctx context.Context) (pgx.Tx, error)
}

// Begin 让 *DB 满足 Beginner：事务通道一律走主库（RLS 策略与 GUC 都在主库
// 生效，副本读如需上下文将来另行开通道，不隐式复用本口）。
func (d *DB) Begin(ctx context.Context) (pgx.Tx, error) { return d.primary.Begin(ctx) }

// tenantKey 标记"ctx 里已有一个活跃的租户事务"，用于拒绝嵌套。
type tenantKey struct{}

// WithTenant 在单个事务里注入租户上下文后运行 fn（PostgREST PreQuery 的
// 最小子集）：Begin → 同一事务 SELECT set_config('app.workspace_id',$1,true),
// set_config('app.user_id',$2,true) → fn → Commit；回滚或提交后 GUC 自动
// 消失（is_local=true 只覆盖本事务，归还池的连接不带残留，见八坑 1/2）。
//
// 传给 fn 的 ctx 已携带事务（db.Tx(ctx)/db.Within(ctx,…) 会复用它），同时
// tx 直接作为第二参给出。fn 返回 error 即回滚整个事务（Transactional 语义）。
//
// 嵌套规则：ctx 已处于 WithTenant 内时再次调用直接报错——内层换身份会静默
// 扩大行可见性，内层同身份也只是重复注入；跨租户操作请在外层独立发起。
func WithTenant(ctx context.Context, db Beginner, id Identity, fn func(ctx context.Context, tx pgx.Tx) error) error {
	if outer, ok := ctx.Value(tenantKey{}).(Identity); ok {
		return fmt.Errorf("pg: WithTenant nested inside WithTenant (outer workspace %s): nesting rejected, inner scope would widen row visibility; start a separate top-level transaction instead", outer.WorkspaceID)
	}
	tx, err := db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("pg: begin: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback(context.WithoutCancel(ctx))
		}
	}()
	// is_local=true（第三参）= SET LOCAL：GUC 仅在本事务内可见，提交/回滚即消失。
	if _, err := tx.Exec(ctx,
		`SELECT set_config('`+gucWorkspaceID+`', $1, true), set_config('`+gucUserID+`', $2, true)`,
		id.WorkspaceID.String(), id.UserID.String()); err != nil {
		return fmt.Errorf("pg: set tenant context: %w", err)
	}
	tctx := context.WithValue(context.WithValue(ctx, txKey{}, tx), tenantKey{}, id)
	if err := fn(tctx, tx); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("pg: commit: %w", err)
	}
	committed = true
	return nil
}

// WithService 以显式无租户上下文运行 fn：普通事务，不注入任何 GUC。
// 它是后台任务/数据修复/报表跨租户操作的**具名逃生口**，绝不隐式。
//
// 语义边界：本函数自身不绕过 RLS——能否看见全部行取决于连接账号的权限
// （生产配 ploykit_service = BYPASSRLS；演示单账号模式下管理员/超级用户
// 连接等效可绕过，非 owner 无 BYPASSRLS 的账号经由它仍只能看到 0 行）。
// 与 WithTenant 互斥：不得嵌在租户事务里调用（跨连接嵌套事务有死锁风险，
// 且租户事务内突然逃逸属于应当显式拒绝的模式）。
func WithService(ctx context.Context, db Beginner, fn func(ctx context.Context, tx pgx.Tx) error) error {
	if _, ok := ctx.Value(tenantKey{}).(Identity); ok {
		return fmt.Errorf("pg: WithService called inside WithTenant: the service escape hatch must be a separate top-level transaction, not nested in a tenant scope")
	}
	tx, err := db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("pg: begin: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback(context.WithoutCancel(ctx))
		}
	}()
	if err := fn(context.WithValue(ctx, txKey{}, tx), tx); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("pg: commit: %w", err)
	}
	committed = true
	return nil
}
