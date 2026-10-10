package pg

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/rand"
	"net"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/haozing/ploykit/internal/contract/apierr"
	"github.com/jackc/pgx/v5/pgxpool"
)

type DBTX interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

type txKey struct{}

type Options struct {
	StartupTimeout time.Duration

	ConnectTimeout time.Duration

	// ReplicaDSN 可选副本连接串：Read 优先走副本。拨号失败不阻断启动——
	// 记 Error 日志、降级主库-only，后台每 30s 重拨，成功后自动接管读流量
	// （PG1：无日志静默降级在业界不成立——Makara（2013 年的 Ruby 库）都有
	// 拉黑日志钩子，Rails 哲学干脆不降级）。StartupTimeout/ConnectTimeout
	// 同样约束副本拨号窗口。
	ReplicaDSN string

	// 池容量（PG4），透传 pgxpool.Config 同名字段：0 = 不覆盖（pgxpool 库
	// 默认 MaxConns=max(4, NumCPU)、MinConns=0；DSN 的 pool_max_conns /
	// pool_min_conns 参数在零值时保真）。此前无此面——产品要调池容只能裸
	// pgxpool + WrapPool，且失去看门狗与保活默认值。
	MaxConns int32
	MinConns int32

	// 池保活三件套，零值时取下方默认值（治 PG 重启/NAT 半开后死连接不自愈）：
	// 后台健康检查发现坏连接即销毁重建；空闲/超期连接强制回收。
	MaxConnLifetime  time.Duration
	MaxConnIdleTime  time.Duration
	HealthCheckEvery time.Duration

	// 自愈看门狗（默认开）：后台按 HealthCheckEvery 周期 Ping 主库，
	// 失败即 Reset 整池重建。覆盖 HealthCheck 管不到的场景——连接"被借出后
	// 永久消失"（Windows+Docker wslrelay 半开连接）：泄漏连接不进 idle 队列，
	// 池耗尽后所有请求挂起，只有整池 Reset 能解。Reset 会打断当时已在途的
	// 查询，但那会儿它们本来就在失败。
	SelfHeal *bool

	// ExitOnWedge 看门狗连续多轮判定"DB 可达但池楔死"（连接被借出后永不
	// 归还，Reset 救不了）时退出进程，交给部署层（docker restart/k8s）拉起。
	// 默认 false：只打 CRITICAL 日志。楔死的典型根因是业务代码未归还连接
	// （未 Scan 的 pgx.Row / 未 Close 的 Rows），应修根因而非依赖重启。
	ExitOnWedge bool
}

const (
	defaultMaxConnLifetime  = 30 * time.Minute
	defaultMaxConnIdleTime  = 5 * time.Minute
	defaultHealthCheckEvery = 30 * time.Second
)

type DB struct {
	primary *pgxpool.Pool

	// replica 副本池（PG1）：Read 侧无锁读、后台重试 goroutine 原子换入。
	// nil = 无副本（未配置，或配置了但初始拨号失败、重试尚未成功）。
	replica atomic.Pointer[pgxpool.Pool]

	breaker breaker

	// replicaDial 后台重拨用的拨号函数；nil = dialOnce。测试注入点（PG1）。
	replicaDial func(ctx context.Context, dsn string, opt Options) (*pgxpool.Pool, error)

	// replicaRetryStop 后台副本重试的停机口：Connect 里副本初始拨号失败时
	// 创建，Close 经 stopReplicaRetry 关闭。nil（WrapPool/无重试在跑）时
	// select 对 nil chan 永不命中——进程生命周期语义，与看门狗同款取舍。
	replicaRetryStop     chan struct{}
	replicaRetryStopOnce sync.Once
}

// breaker 副本读熔断器。状态机：关闭（全量放行）→ 连接类故障 Trip（冷却
// openFor）→ 打开（全量回绝，读流量走主库）→ 冷却到点半开——**单探测**
// （PG2①）：第一个调用经 atomic CAS 抢到保留位成为探测者，其余并发立即
// 回绝走主库；探测成功关闭、失败重新打开。等价于 gobreaker 半开
// maxRequests=1 的拒绝语义（超量返回 ErrTooManyRequests 而非放行）在
// 路由层的翻译："拒绝"= 走主库 fallback。此前半开对全量并发放行，副本
// 仍挂时每个窗口的全部并发读各吃一次最长 ConnectTimeout 的失败尾延迟，
// 失败风暴持续 re-Trip 形成周期性脉冲（业界无主流库半开放行无界并发：
// Hystrix=1、gobreaker 默认=1、resilience4j 有界默认 10）。
type breaker struct {
	openFor time.Duration
	now     func() time.Time

	mu        sync.Mutex
	openUntil time.Time

	// probing 半开探测保留位。必须用 atomic 而非复用 mu：抢位语义就是一次
	// CAS——半开窗口内 CompareAndSwap(false→true) 恰好成功一次，并发竞争者
	// 全部落空。OnSuccess/Trip/release 归还。
	probing atomic.Bool
}

// Allow 报告本次调用可否使用副本：打开（冷却未到）→ false；关闭 → true
// （无需保留位）；半开 → 仅抢到保留位的第一个调用 true。
func (b *breaker) Allow() bool {
	b.mu.Lock()
	open, now := b.openUntil, b.now()
	b.mu.Unlock()
	if now.Before(open) {
		return false
	}
	if open.IsZero() {
		return true
	}
	return b.probing.CompareAndSwap(false, true)
}

// OnSuccess 探测/调用成功：关闭熔断并归还保留位。
func (b *breaker) OnSuccess() {
	b.mu.Lock()
	b.openUntil = time.Time{}
	b.mu.Unlock()
	b.probing.Store(false)
}

// Trip 记一次连接类故障：重新打开熔断（冷却 openFor）并归还保留位。
func (b *breaker) Trip() {
	b.mu.Lock()
	b.openUntil = b.now().Add(b.openFor)
	b.mu.Unlock()
	b.probing.Store(false)
}

// release 归还半开保留位但不改变开/闭状态。两个消费者：①探测因调用方
// ctx 取消而无结论（PG2②——不算成功也不算故障，熔断维持半开，下一个
// 调用成为新探测）；②Read 的 defer 兜底（fn panic 时也必须归还，否则
// 下次冷却到点无人能再探测，副本被永久静默降级——PG1 同族病灶）。
func (b *breaker) release() { b.probing.Store(false) }

func Connect(ctx context.Context, dsn string, opt Options) (*DB, error) {
	if opt.StartupTimeout <= 0 {
		opt.StartupTimeout = 3 * time.Minute
	}
	if opt.ConnectTimeout <= 0 {
		opt.ConnectTimeout = 5 * time.Second
	}

	primary, err := dialWithRetry(ctx, dsn, opt)
	if err != nil {
		return nil, fmt.Errorf("pg: connect primary: %w", err)
	}
	db := &DB{primary: primary, breaker: newBreaker()}
	startPoolWatchdog(primary, opt)
	if opt.ReplicaDSN != "" {
		if rp, err := dialWithRetry(ctx, opt.ReplicaDSN, opt); err == nil {
			db.replica.Store(rp)
			// PG4：副本池与主池同一门控挂看门狗（对齐自愈覆盖面）。
			startPoolWatchdog(rp, opt)
		} else {
			// PG1：配了副本却拨不上，必须留下运维信号。此前 err 被静默丢弃，
			// 运维显式配置了副本、得到主库-only 部署且零信号，副本读全量压
			// 主库的性能后果无声发生。启动不阻断（副本是可选降级件），但
			// Error 是底线——benchmark §五：静默降级在业界不成立。
			slog.Error("pg: replica connect failed, running primary-only", "err", err)
			startReplicaRetry(db, opt)
		}
	}
	return db, nil
}

// replicaRetryEvery 后台重拨副本的周期（生产 30s；仅测试临时改短）。
var replicaRetryEvery = 30 * time.Second

// startReplicaRetry 副本初始拨号失败后的后台重拨（PG1）：每 30s 重试一次，
// 成功即换入 db.replica 接管读流量（Info 日志）并退出；此前是永久放弃、
// 直到重启进程。
//
// 停机口：DB.Close（本包唯一的优雅关闭钩子）→ stopReplicaRetry 关闭
// replicaRetryStop。Close 不被调用的部署里 goroutine 随进程退出回收——
// 与看门狗（startWatchdog，连停机口都没有）同一档进程生命周期语义。
// 注意不绑定 Connect 的 ctx：那是启动预算语境，"启动完成即 cancel"是
// 常见调用模式，绑定会把重试一并杀掉。
func startReplicaRetry(d *DB, opt Options) {
	d.replicaRetryStop = make(chan struct{})
	dial := d.replicaDial
	if dial == nil {
		dial = func(_ context.Context, dsn string, opt Options) (*pgxpool.Pool, error) {
			// 自建拨号窗（ConnectTimeout 超时）；不继承调用方 ctx——自建窗的
			// 超时经 pgx 包装为 *pgconn.ConnectError，仍计故障（见
			// isConnectTransient 取证注释）。
			ctx, cancel := context.WithTimeout(context.Background(), opt.ConnectTimeout)
			defer cancel()
			return dialOnce(ctx, dsn, opt)
		}
	}
	go func() {
		t := time.NewTicker(replicaRetryEvery)
		defer t.Stop()
		for {
			select {
			case <-d.replicaRetryStop:
				return
			case <-t.C:
			}
			rp, err := dial(context.Background(), opt.ReplicaDSN, opt)
			if err != nil {
				slog.Warn("pg: replica retry connect failed, still primary-only", "err", err)
				continue
			}
			// 与 Close 的窄竞态：stop 检查刚通过、Close 随即发生——本池会在
			// 无人 Close 的情况下遗留（其健康检查 goroutine 随进程退出回收）。
			// Close 是停机语义、进程将退，接受；彻底消除需把 Store 收进与
			// Close 同一把锁，收益不成比例。
			select {
			case <-d.replicaRetryStop:
				rp.Close()
				return
			default:
			}
			d.replica.Store(rp)
			// PG4：重试接管的新副本池同样挂看门狗（与 Connect 初始拨号路径
			// 同一门控——否则晚到的副本反而没有自愈覆盖）。
			startPoolWatchdog(rp, opt)
			// 新池新账：清掉旧副本遗留的熔断开窗，读流量立即可探测新副本。
			d.breaker.OnSuccess()
			slog.Info("pg: replica connected after retry, taking over read traffic")
			return
		}
	}()
}

// poolConfig 解析 DSN 并落连接/池参数（主副本共用；零值取默认三件套）。
func poolConfig(dsn string, opt Options) (*pgxpool.Config, error) {
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("pg: parse dsn: %w", err)
	}
	cfg.ConnConfig.ConnectTimeout = opt.ConnectTimeout
	// 池容量（PG4）：0 = 不覆盖，pgxpool 默认 / DSN 参数保真。
	if opt.MaxConns > 0 {
		cfg.MaxConns = opt.MaxConns
	}
	if opt.MinConns > 0 {
		cfg.MinConns = opt.MinConns
	}
	if opt.MaxConnLifetime > 0 {
		cfg.MaxConnLifetime = opt.MaxConnLifetime
	} else {
		cfg.MaxConnLifetime = defaultMaxConnLifetime
	}
	if opt.MaxConnIdleTime > 0 {
		cfg.MaxConnIdleTime = opt.MaxConnIdleTime
	} else {
		cfg.MaxConnIdleTime = defaultMaxConnIdleTime
	}
	if opt.HealthCheckEvery > 0 {
		cfg.HealthCheckPeriod = opt.HealthCheckEvery
	} else {
		cfg.HealthCheckPeriod = defaultHealthCheckEvery
	}
	return cfg, nil
}

// dialOnce 单次拨号尝试：建池 + Ping。Ping 失败即 Close 池——此前该路径
// 每次尝试泄漏一个带健康检查 goroutine 的池（审计 PG3，重构时顺手修）。
func dialOnce(ctx context.Context, dsn string, opt Options) (*pgxpool.Pool, error) {
	cfg, err := poolConfig(dsn, opt)
	if err != nil {
		return nil, err
	}
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, err
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, err
	}
	return pool, nil
}

func dialWithRetry(ctx context.Context, dsn string, opt Options) (*pgxpool.Pool, error) {
	deadline := time.Now().Add(opt.StartupTimeout)
	delay := time.Second
	var pool *pgxpool.Pool
	var err error
	for attempt := 1; ; attempt++ {
		pool, err = dialOnce(ctx, dsn, opt)
		if err == nil {
			return pool, nil
		}
		if !isConnectTransient(err) {
			return nil, fmt.Errorf("pg: attempt %d: %w", attempt, err)
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("pg: startup budget exhausted after %d attempts: %w", attempt, err)
		}
		sleep := jitter(delay)
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(sleep):
		}
		if delay *= 2; delay > 30*time.Second {
			delay = 30 * time.Second
		}
	}
}

// isCallerCtxErr 报告 err 是否调用方 ctx 取消/超时（PG2②）。此类错误不是
// 副本故障——.NET Polly 默认排除 OperationCanceledException、gobreaker v2
// 专门内置 IsExcluded 且注释点名 context cancellations，这里是 Go 手写
// errors.Is 等价。
func isCallerCtxErr(err error) bool {
	return errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
}

// isConnectTransient 报告 err 是否为"连接类瞬态故障"（值得重试 / 计入副本
// 熔断）。判定次序是有意为之（PG2②）：
//
//  1. *pgconn.ConnectError 优先：pgx 把建立连接全程的失败——包括它以
//     cfg.ConnConfig.ConnectTimeout 自建的拨号窗超时——统一包成
//     ConnectError（inner 为 errTimeout{contextAlreadyDoneError{ctx.Err()}}）。
//     先判它就保住了审计细节"副本拨号自建 ctx 的超时仍应计故障"：黑洞地址
//     的副本若被剔除，熔断永不 Trip，每次读都吃满 ConnectTimeout 的失败
//     尾延迟才回落主库。实证（pgx v5.11.0，2026-10-07 取证）：自建窗超时
//     的链 As(ConnectError)=true 且 Is(DeadlineExceeded)=true。
//  2. 裸 ctx 错误剔除：调用方取消/超时经 pgxpool Ping/查询 surfaced 为裸
//     context.Canceled / context.DeadlineExceeded（不被 ConnectError 包装，
//     同上取证）。注意 context.DeadlineExceeded 实现了 net.Error——不先于
//     errors.As(net.Error) 剔除，调用方短 ctx 超时就会误 Trip（PG2②主缺口：
//     客户端高频 abort 时副本形同虚设）。
//
// 残余歧义（接受并知情）：调用方取消恰好落在 TCP 拨号进行中、被 ConnectError
// 一并包装的窄窗内——按故障计。后果仅一次 2s 冷却、自愈，远好于反向取舍
// （黑洞副本永不熔断）。改进位：拨号路径对自建窗超时包 sentinel 标记，
// 可把误判收敛到零；当前无错误来源标记机制，不做过度设计。
func isConnectTransient(err error) bool {
	if err == nil {
		return false
	}
	var connectErr *pgconn.ConnectError
	if errors.As(err, &connectErr) {
		return true
	}
	if isCallerCtxErr(err) {
		return false
	}
	var netErr net.Error
	return errors.As(err, &netErr)
}

func jitter(d time.Duration) time.Duration {
	return d + time.Duration(rand.Int63n(int64(d/2)+1))
}

func (d *DB) Ping(ctx context.Context) error { return d.primary.Ping(ctx) }

// Pool 暴露底层主池，供基于 *pgxpool.Pool 的仓储适配器直接使用。
func (d *DB) Pool() *pgxpool.Pool { return d.primary }

// Reset 关闭池中全部连接（含泄漏的），下次使用时重新拨号。
func (d *DB) Reset() { d.primary.Reset() }

// startWatchdog 三态体检（见 Options.SelfHeal / Options.ExitOnWedge）：
//  1. 独立探测（绕过池直连 PG）失败 → DB 不可达：Reset 清理池内半开连接，
//     DB 恢复后池自愈（覆盖 HealthCheck 管不到的 NAT 半开场景）；
//  2. 独立探测成功 + 池内 Ping 失败 → 池楔死：pgxpool.Reset 只回收 idle
//     连接，被借出不归还的连接（未 Scan 的 Row / 未 Close 的 Rows）Reset
//     无效——记 CRITICAL（含 Stat 快照），连续 wedgeThreshold 轮仍楔死且
//     ExitOnWedge 时退出进程交由部署层重启；
//  3. 都成功 → 健康。
func startWatchdog(pool *pgxpool.Pool, exitOnWedge bool) {
	interval := pool.Config().HealthCheckPeriod
	if interval < 5*time.Second {
		interval = 5 * time.Second
	}
	const wedgeThreshold = 3
	wedges := 0
	go func() {
		t := time.NewTicker(interval)
		defer t.Stop()
		for range t.C {
			if err := probeDB(pool); err != nil {
				slog.Error("pg: watchdog probe failed (db unreachable), resetting pool", "err", err)
				pool.Reset()
				wedges = 0
				continue
			}
			pingCtx, pingCancel := probeCtx()
			pingErr := pool.Ping(pingCtx)
			pingCancel()
			if pingErr != nil {
				wedges++
				st := pool.Stat()
				slog.Error("pg: pool wedged (acquired conns never returned); Reset cannot reclaim them",
					"wedges", wedges, "acquired", st.AcquiredConns(), "idle", st.IdleConns(), "total", st.TotalConns(),
					"hint", "likely unclosed pgx Rows / unscanned Row in business code")
				pool.Reset()
				if exitOnWedge && wedges >= wedgeThreshold {
					slog.Error("pg: pool wedged persistently, exiting for supervisor restart")
					os.Exit(1)
				}
				continue
			}
			wedges = 0
		}
	}()
}

func probeCtx() (ctx context.Context, cancel context.CancelFunc) {
	return context.WithTimeout(context.Background(), 3*time.Second)
}

// probeDB 绕过池直连 PG 一次，判断数据库本身是否可达。
func probeDB(pool *pgxpool.Pool) error {
	ctx, cancel := probeCtx()
	defer cancel()
	conn, err := pgx.ConnectConfig(ctx, pool.Config().ConnConfig.Copy())
	if err != nil {
		return err
	}
	if err := conn.Ping(ctx); err != nil {
		_ = conn.Close(context.Background())
		return err
	}
	return conn.Close(context.Background())
}

// watchdogStart 看门狗启动口；包级变量仅测试替换（对齐 replicaRetryEvery
// 的取舍），生产恒 startWatchdog。
var watchdogStart = startWatchdog

// startPoolWatchdog 主/副本池统一挂看门狗入口（PG4：此前只有主池挂——副本
// 池半开连接只有 pgxpool 自身健康检查自愈，"借出后永久消失"场景无人 Reset）。
// SelfHeal 三态：nil（默认）/true 挂，false 跳过。看门狗本身无停机口（进程
// 生命周期语义，见 startWatchdog 注释）；副本池被 Close 后其看门狗对已关池
// 的 Reset/Ping 是 no-op 级噪声，同一取舍。
func startPoolWatchdog(pool *pgxpool.Pool, opt Options) {
	if opt.SelfHeal == nil || *opt.SelfHeal {
		watchdogStart(pool, opt.ExitOnWedge)
	}
}

// WrapPool 把外部持有的 *pgxpool.Pool 包成 *DB（Within/Tx/Read 语义可用）。
//
// 与 Connect 的差异（PG4，选型须知）：
//   - **不启动自愈看门狗**——池的生命周期归调用方所有，看门狗的 Reset 语义
//     （打断当时在途的查询）越过 WrapPool 的所有权边界；也不做副本拨号/
//     重试（池已存在）。
//   - 适用场景：测试替身、或产品已自带 pgxpool 仅想要事务/Read 封装的场合。
//     生产主通道应走 Connect（拿全默认值：保活三件套、看门狗、副本链路）；
//     需要调池容量时优先经 pg.Options.MaxConns/MinConns，而非裸 pgxpool +
//     WrapPool——后者会失去看门狗（本注释存在的理由）。
func WrapPool(pool *pgxpool.Pool) *DB {
	return &DB{primary: pool, breaker: newBreaker()}
}

func newBreaker() breaker { return breaker{openFor: 2 * time.Second, now: time.Now} }

func (d *DB) Acquire(ctx context.Context) (*pgxpool.Conn, error) {
	return d.primary.Acquire(ctx)
}

// Close 关闭主/副本池，并停止后台副本重试 goroutine（若有，PG1）。
func (d *DB) Close() {
	d.stopReplicaRetry()
	if rp := d.replica.Swap(nil); rp != nil {
		rp.Close()
	}
	d.primary.Close()
}

// stopReplicaRetry 关闭后台副本重试停机口（幂等；DB.Close 的一部分）。
// replicaRetryStop 为 nil（WrapPool 或无重试在跑）时直接返回。
func (d *DB) stopReplicaRetry() {
	if d.replicaRetryStop != nil {
		d.replicaRetryStopOnce.Do(func() { close(d.replicaRetryStop) })
	}
}

// Within 开启一个事务并在其中执行 fn：fn 及其内部经 Tx(ctx) 拿到的执行面
// 共享同一事务；fn 返回 error 则整体回滚（Rollback 用 WithoutCancel——即便
// ctx 已取消也要把事务还回池）。ctx 已在事务内（嵌套 Within）时直接复用外层
// 事务，不再开新事务。
//
// 隔离级别面**刻意不加**（PG5 决策留待）：始终使用服务端默认（read
// committed），不提供 Options 暴露 isoLevel——需要更强隔离的场景（写偏序
// 敏感的聚合等）应显式经 Pool().Begin 并自行指定，等真实用例出现再裁决
// 契约形态，勿顺手加宽 API 面。
func (d *DB) Within(ctx context.Context, fn func(ctx context.Context) error) error {
	if _, ok := ctx.Value(txKey{}).(pgx.Tx); ok {
		return fn(ctx)
	}
	tx, err := d.primary.Begin(ctx)
	if err != nil {
		return fmt.Errorf("pg: begin: %w", err)
	}
	committed := false
	defer func() {
		if !committed {

			_ = tx.Rollback(context.WithoutCancel(ctx))
		}
	}()
	if err := fn(context.WithValue(ctx, txKey{}, tx)); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("pg: commit: %w", err)
	}
	committed = true
	return nil
}

// Tx 返回 ctx 当前事务的执行面。**陷阱警示（PG5）：ctx 不在 Within 事务内
// 时静默回落裸池**——自动提交语义，每条语句独立提交、彼此不可见，调用方
// 得不到任何信号。通用业务写路径应始终包在 Within 里（Tx 只在 fn 内部使用）；
// GUC/RLS 场景后果更重：SET LOCAL 只在事务内生效，裸池上 set_config 的租户
// 上下文下一条语句即丢（坑 2，全文见 tenant.go「RLS 八坑清单」与
// WithTenant/WithService 的事务边界契约）。
func (d *DB) Tx(ctx context.Context) DBTX {
	if tx, ok := ctx.Value(txKey{}).(pgx.Tx); ok {
		return tx
	}
	return d.primary
}

// Read 以副本优先执行只读查询 fn，副本不可用时自动回落主库。
//
// 调用方契约（PG2③，回落语义的全部义务）：
//   - fn 必须只读且幂等：副本吃连接类故障时，Read 会在主库上**重放整个
//     fn**——非幂等 fn 可能被执行两次，其副作用（应用层状态、发送等）
//     同样翻倍。
//   - DBTX 类型面包含 Exec，但经 Read 传入的 q 上执行写语句违反本契约：
//     回落重放会把写放大为两次执行，且写在副本故障切换语义下不保证送达。
//     写操作走 Within/Tx。
//   - stale 语义：副本允许落后于主库（异步复制），本 API 不提供读己之写
//     或单调读保证——fn 可能读到比主库旧的值。写后立即读、对新鲜度敏感
//     的查询应直接走主池（Pool/Within）。
//   - ctx 语义：fn 因调用方 ctx 取消/超时失败时，Read 原样返回错误、不在
//     主库重放（同一 ctx 重放必然同样失败）、不计副本故障（PG2②）。
//
// 熔断（PG2①）：副本连接类故障 Trip、冷却 2s；冷却到点半开单探测——第一
// 个调用成为探测者，其余并发直接走主库（不打副本，无失败风暴脉冲）；
// 探测成功关闭熔断，失败重新打开。副本未配置（Options.ReplicaDSN 为空或
// 拨号失败尚未重试成功）时恒走主库。
func (d *DB) Read(ctx context.Context, fn func(ctx context.Context, q DBTX) error) error {
	rp := d.replica.Load()
	if rp == nil || !d.breaker.Allow() {
		return fn(ctx, d.primary)
	}
	// 探测者义务：无论成败（含 fn panic 经 defer 兜底）都必须归还半开保留
	// 位，否则冷却到点后无人能再探测，副本被永久静默降级（PG1 同族）。
	defer d.breaker.release()

	err := fn(ctx, rp)
	if err == nil {
		d.breaker.OnSuccess()
		return nil
	}
	if isConnectTransient(err) {
		// 仅探测失败才 re-Trip（PG2①）：其余并发早已在主库上，不再打副本。
		d.breaker.Trip()
		return fn(ctx, d.primary)
	}
	if !isCallerCtxErr(err) {
		// 非连接类、非调用方取消的错误（典型：SQL 语法/约束错误）——副本已
		// 实际执行了查询，可达性成立 → 按成功关闭熔断；不回落重放（同一 fn
		// 在主库上大概率同样失败，重放无意义还放大故障面）。
		d.breaker.OnSuccess()
	}
	// 调用方 ctx 取消/超时：结果不明——不算成功也不算故障，仅归还保留位
	//（熔断维持半开，下一个调用成为新探测）；错误原样返回。
	return err
}

func AsNotFound(err error, msg string) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23503" {
		return apierr.New(apierr.NotFound, msg)
	}
	return err
}

// IsUniqueViolation 报告 err 是否为 PostgreSQL 唯一约束冲突（23505）。
// constraint 非空时进一步要求约束/索引名精确匹配——同码不同源的场景
// （如幂等键冲突与号码竞态同报 23505）靠它区分。对 wrapped error 有效
// （errors.As 语义）；err 为 nil 或非 23505 一律 false。
func IsUniqueViolation(err error, constraint string) bool {
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "23505" {
		return false
	}
	return constraint == "" || pgErr.ConstraintName == constraint
}

// AsDuplicate 把 23505 唯一约束冲突翻译为调用方给定的语义错误（如各域的
// ErrDuplicate，服务层据此返回 409），其余错误原样返回——AsNotFound（23503）
// 的 23505 兄弟。语义错误由调用方提供：平台件不定义业务错误词汇。
func AsDuplicate(err error, dup error) error {
	if IsUniqueViolation(err, "") {
		return dup
	}
	return err
}
