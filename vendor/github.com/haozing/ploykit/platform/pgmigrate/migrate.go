// Package pgm 是零依赖的嵌入式 SQL 迁移器(平移自 orkome pgmigrate 的完整能力面,
// 数据源从文件系统改为 embed.FS 以适配容器单二进制分发)。
//
// 能力:Up(限量)/Down(回滚)/Status(对账)/版本前置 Hook/多实例 advisory lock/
// `-- migrate:no-transaction` 首行标记(建索引并发操作等不能进事务的迁移)。
// 版本记录在 schema_migrations 表;每个迁移默认独立事务并原子记账。
package pgm

import (
	"context"
	"embed"
	"fmt"
	"io/fs"
	"path"
	"regexp"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
)

const noTxMarker = "-- migrate:no-transaction"

// Hook 在某版本迁移执行前调用(种子数据、缓存预热等)。
type Hook func(ctx context.Context, pool *pgxpool.Pool) error

type StatusRow struct {
	Version   string
	Applied   bool
	AppliedAt *string
}

type Migrator struct {
	pool   *pgxpool.Pool
	fsys   embed.FS
	subdir string
	hooks  map[string][]Hook
}

func New(pool *pgxpool.Pool, fsys embed.FS, subdir string) *Migrator {
	return &Migrator{pool: pool, fsys: fsys, subdir: subdir, hooks: map[string][]Hook{}}
}

// RegisterPreHook 注册某版本执行前的钩子。
func (m *Migrator) RegisterPreHook(version string, h Hook) {
	m.hooks[version] = append(m.hooks[version], h)
}

var fileRe = regexp.MustCompile(`^([0-9]{3,})_[a-z0-9_]+\.(up|down)\.sql$`)

type migrationFile struct {
	version string
	kind    string // up / down
	name    string
	data    string
}

func (m *Migrator) scan() (map[string]migrationFile, map[string]migrationFile, []string, error) {
	entries, err := fs.ReadDir(m.fsys, m.subdir)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("pgm: read migrations dir: %w", err)
	}
	ups, downs := map[string]migrationFile{}, map[string]migrationFile{}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		mat := fileRe.FindStringSubmatch(e.Name())
		if mat == nil {
			return nil, nil, nil, fmt.Errorf("pgm: invalid migration filename %q (must be NNN_name.up|down.sql)", e.Name())
		}
		// io/fs 路径必须是 unrooted 形式;path.Join 会把 subdir="." 归一掉。
		raw, err := fs.ReadFile(m.fsys, path.Join(m.subdir, e.Name()))
		if err != nil {
			return nil, nil, nil, fmt.Errorf("pgm: read %s: %w", e.Name(), err)
		}
		mf := migrationFile{version: mat[1], kind: mat[2], name: e.Name(), data: string(raw)}
		if mf.kind == "up" {
			if _, dup := ups[mf.version]; dup {
				return nil, nil, nil, fmt.Errorf("pgm: duplicate up version %q", mf.version)
			}
			ups[mf.version] = mf
		} else {
			downs[mf.version] = mf
		}
	}
	versions := make([]string, 0, len(ups))
	for v := range ups {
		versions = append(versions, v)
	}
	sort.Strings(versions)
	return ups, downs, versions, nil
}

func (m *Migrator) ensureVersionTable(ctx context.Context) error {
	_, err := m.pool.Exec(ctx, `
CREATE TABLE IF NOT EXISTS schema_migrations (
  version    TEXT PRIMARY KEY,
  applied_at TIMESTAMPTZ NOT NULL DEFAULT now()
)`)
	return err
}

// normVersion 把历史记录的版本串归一为纯数字前缀。
// 早期版本记完整名("002_users"),现版本记数字("002");升级已有库时两者并存,
// 归一后等价,避免重复执行。
func normVersion(v string) string {
	if i := strings.IndexByte(v, '_'); i > 0 {
		return v[:i]
	}
	return v
}

func (m *Migrator) applied(ctx context.Context) (map[string]string, error) {
	rows, err := m.pool.Query(ctx, `SELECT version, to_char(applied_at, 'YYYY-MM-DD HH24:MI:SS') FROM schema_migrations`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	applied := map[string]string{}
	for rows.Next() {
		var v, at string
		if err := rows.Scan(&v, &at); err != nil {
			return nil, err
		}
		applied[normVersion(v)] = at
	}
	return applied, rows.Err()
}

// Up 应用未执行的迁移;limit>0 时最多应用 limit 个,返回本次应用的版本列表。
func (m *Migrator) Up(ctx context.Context, limit int) ([]string, error) {
	var appliedList []string
	err := m.withRunLock(ctx, func() error {
		applied, err := m.applied(ctx)
		if err != nil {
			return err
		}
		ups, _, versions, err := m.scan()
		if err != nil {
			return err
		}
		n := 0
		for _, v := range versions {
			if _, ok := applied[v]; ok {
				continue
			}
			if limit > 0 && n >= limit {
				break
			}
			for _, h := range m.hooks[v] {
				if err := h(ctx, m.pool); err != nil {
					return fmt.Errorf("pgm: pre-hook of %s: %w", v, err)
				}
			}
			if err := m.execMigration(ctx, ups[v], true); err != nil {
				return err
			}
			appliedList = append(appliedList, v)
			n++
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return appliedList, nil
}

// Down 回滚最近 limit 个已应用迁移(缺 down 文件则报错)。
func (m *Migrator) Down(ctx context.Context, limit int) ([]string, error) {
	var rolled []string
	err := m.withRunLock(ctx, func() error {
		applied, err := m.applied(ctx)
		if err != nil {
			return err
		}
		_, downs, versions, err := m.scan()
		if err != nil {
			return err
		}
		var appliedVersions []string
		for _, v := range versions {
			if _, ok := applied[v]; ok {
				appliedVersions = append(appliedVersions, v)
			}
		}
		n := 0
		for i := len(appliedVersions) - 1; i >= 0; i-- {
			v := appliedVersions[i]
			if limit > 0 && n >= limit {
				break
			}
			down, ok := downs[v]
			if !ok {
				return fmt.Errorf("pgm: %s is missing its down file, cannot roll back", v)
			}
			if err := m.execMigration(ctx, down, false); err != nil {
				return err
			}
			rolled = append(rolled, v)
			n++
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return rolled, nil
}

// Status 对账:全部已知版本及其应用状态。
func (m *Migrator) Status(ctx context.Context) ([]StatusRow, error) {
	if err := m.ensureVersionTable(ctx); err != nil {
		return nil, err
	}
	applied, err := m.applied(ctx)
	if err != nil {
		return nil, err
	}
	_, _, versions, err := m.scan()
	if err != nil {
		return nil, err
	}
	out := make([]StatusRow, 0, len(versions))
	for _, v := range versions {
		row := StatusRow{Version: v}
		if at, ok := applied[v]; ok {
			row.Applied = true
			row.AppliedAt = &at
		}
		out = append(out, row)
	}
	return out, nil
}

// withRunLock 用 advisory lock 串行化多实例同时启动的迁移。
func (m *Migrator) withRunLock(ctx context.Context, fn func() error) error {
	if err := m.ensureVersionTable(ctx); err != nil {
		return fmt.Errorf("pgm: ensure version table: %w", err)
	}
	conn, err := m.pool.Acquire(ctx)
	if err != nil {
		return fmt.Errorf("pgm: acquire conn: %w", err)
	}
	defer conn.Release()

	if _, err := conn.Exec(ctx, `SELECT pg_advisory_lock(hashtext('ploykit_migrations'))`); err != nil {
		return fmt.Errorf("pgm: advisory lock: %w", err)
	}
	defer func() {
		_, _ = conn.Exec(context.WithoutCancel(ctx), `SELECT pg_advisory_unlock(hashtext('ploykit_migrations'))`)
	}()
	return fn()
}

func (m *Migrator) execMigration(ctx context.Context, mf migrationFile, isUp bool) error {
	bookkeeping := fmt.Sprintf("INSERT INTO schema_migrations(version) VALUES ('%s');", mf.version)
	if !isUp {
		bookkeeping = fmt.Sprintf("DELETE FROM schema_migrations WHERE version = '%s';", mf.version)
	}

	conn, err := m.pool.Acquire(ctx)
	if err != nil {
		return fmt.Errorf("pgm: acquire conn: %w", err)
	}
	defer conn.Release()
	pgc := conn.Conn().PgConn()

	if strings.HasPrefix(strings.TrimSpace(mf.data), noTxMarker) {
		if _, err := pgc.Exec(ctx, mf.data).ReadAll(); err != nil {
			return fmt.Errorf("pgm: exec %s: %w", mf.name, err)
		}
		if _, err := pgc.Exec(ctx, bookkeeping).ReadAll(); err != nil {
			return fmt.Errorf("pgm: bookkeep %s: %w", mf.version, err)
		}
		return nil
	}

	body := strings.TrimSpace(mf.data)
	if !strings.HasSuffix(body, ";") {
		body += ";"
	}
	script := fmt.Sprintf("BEGIN;\n%s\n%s\nCOMMIT;", body, bookkeeping)
	if _, err := pgc.Exec(ctx, script).ReadAll(); err != nil {
		_, _ = pgc.Exec(ctx, "ROLLBACK").ReadAll()
		return fmt.Errorf("pgm: exec %s: %w", mf.name, err)
	}
	return nil
}

// Up 应用全部未执行迁移——单调用便捷入口(等价 New(...).Up(ctx, 0))。
func Up(ctx context.Context, pool *pgxpool.Pool, fsys embed.FS, subdir string) error {
	_, err := New(pool, fsys, subdir).Up(ctx, 0)
	return err
}
