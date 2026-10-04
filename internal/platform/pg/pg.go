package pg

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"net"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/jackc/pgx/v5/pgxpool"
	"orkome/internal/contract/apierr"
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

	ReplicaDSN string
}

type DB struct {
	primary *pgxpool.Pool
	replica *pgxpool.Pool

	breaker breaker
}

type breaker struct {
	openFor time.Duration
	now     func() time.Time

	mu        sync.Mutex
	openUntil time.Time
}

func (b *breaker) Allow() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return !b.now().Before(b.openUntil)
}

func (b *breaker) OnSuccess() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.openUntil = time.Time{}
}

func (b *breaker) Trip() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.openUntil = b.now().Add(b.openFor)
}

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
	if opt.ReplicaDSN != "" {

		if rp, err := dialWithRetry(ctx, opt.ReplicaDSN, opt); err == nil {
			db.replica = rp
		}
	}
	return db, nil
}

func dialWithRetry(ctx context.Context, dsn string, opt Options) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("pg: parse dsn: %w", err)
	}
	cfg.ConnConfig.ConnectTimeout = opt.ConnectTimeout

	deadline := time.Now().Add(opt.StartupTimeout)
	delay := time.Second
	var pool *pgxpool.Pool
	for attempt := 1; ; attempt++ {
		pool, err = pgxpool.NewWithConfig(ctx, cfg)
		if err == nil {
			if pingErr := pool.Ping(ctx); pingErr == nil {
				return pool, nil
			} else {
				err = pingErr
			}
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

func isConnectTransient(err error) bool {
	if err == nil {
		return false
	}
	var connectErr *pgconn.ConnectError
	if errors.As(err, &connectErr) {
		return true
	}
	var netErr net.Error
	return errors.As(err, &netErr)
}

func jitter(d time.Duration) time.Duration {
	return d + time.Duration(rand.Int63n(int64(d/2)+1))
}

func (d *DB) Ping(ctx context.Context) error { return d.primary.Ping(ctx) }

func WrapPool(pool *pgxpool.Pool) *DB {
	return &DB{primary: pool, breaker: newBreaker()}
}

func newBreaker() breaker { return breaker{openFor: 2 * time.Second, now: time.Now} }

func (d *DB) Acquire(ctx context.Context) (*pgxpool.Conn, error) {
	return d.primary.Acquire(ctx)
}

func (d *DB) Close() {
	d.primary.Close()
	if d.replica != nil {
		d.replica.Close()
	}
}

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

func (d *DB) Tx(ctx context.Context) DBTX {
	if tx, ok := ctx.Value(txKey{}).(pgx.Tx); ok {
		return tx
	}
	return d.primary
}

func (d *DB) Read(ctx context.Context, fn func(ctx context.Context, q DBTX) error) error {
	if d.replica == nil || !d.breaker.Allow() {
		return fn(ctx, d.primary)
	}
	err := fn(ctx, d.replica)
	if err == nil {
		d.breaker.OnSuccess()
		return nil
	}
	if isConnectTransient(err) {
		d.breaker.Trip()

		return fn(ctx, d.primary)
	}

	return err
}

func AsNotFound(err error, msg string) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23503" {
		return apierr.New(apierr.NotFound, msg)
	}
	return err
}
