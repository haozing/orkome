package pgmigrate

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
)

const noTxMarker = "-- migrate:no-transaction"

type Hook func(ctx context.Context, pool *pgxpool.Pool) error

type StatusRow struct {
	Version   string
	Applied   bool
	AppliedAt *string
}

type Migrator struct {
	pool  *pgxpool.Pool
	dir   string
	hooks map[string][]Hook
}

func New(pool *pgxpool.Pool, dir string) *Migrator {
	return &Migrator{pool: pool, dir: dir, hooks: map[string][]Hook{}}
}

func (m *Migrator) RegisterPreHook(version string, h Hook) {
	m.hooks[version] = append(m.hooks[version], h)
}

var fileRe = regexp.MustCompile(`^([0-9]{3,})_[a-z0-9_]+\.(up|down)\.sql$`)

type migrationFile struct {
	version string
	kind    string
	path    string
}

func (m *Migrator) scan() (map[string]migrationFile, map[string]migrationFile, []string, error) {
	entries, err := os.ReadDir(m.dir)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("pgmigrate: read dir %s: %w", m.dir, err)
	}
	ups, downs := map[string]migrationFile{}, map[string]migrationFile{}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		mat := fileRe.FindStringSubmatch(e.Name())
		if mat == nil {
			return nil, nil, nil, fmt.Errorf("pgmigrate: invalid migration filename %q (must be NNN_name.up|down.sql)", e.Name())
		}
		mf := migrationFile{version: mat[1], kind: mat[2], path: filepath.Join(m.dir, e.Name())}
		if mf.kind == "up" {
			if _, dup := ups[mf.version]; dup {
				return nil, nil, nil, fmt.Errorf("pgmigrate: duplicate up version %q", mf.version)
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
		applied[v] = at
	}
	return applied, rows.Err()
}

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
					return fmt.Errorf("pgmigrate: pre-hook of %s: %w", v, err)
				}
			}
			if err := m.execFile(ctx, ups[v], true); err != nil {
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
				return fmt.Errorf("pgmigrate: %s is missing its down file, cannot roll back", v)
			}
			if err := m.execFile(ctx, down, false); err != nil {
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

func (m *Migrator) withRunLock(ctx context.Context, fn func() error) error {
	if err := m.ensureVersionTable(ctx); err != nil {
		return fmt.Errorf("pgmigrate: ensure version table: %w", err)
	}
	conn, err := m.pool.Acquire(ctx)
	if err != nil {
		return fmt.Errorf("pgmigrate: acquire conn: %w", err)
	}
	defer conn.Release()

	if _, err := conn.Exec(ctx, `SELECT pg_advisory_lock(hashtext('orkome_migrations'))`); err != nil {
		return fmt.Errorf("pgmigrate: advisory lock: %w", err)
	}
	defer func() {
		_, _ = conn.Exec(context.WithoutCancel(ctx), `SELECT pg_advisory_unlock(hashtext('orkome_migrations'))`)
	}()
	return fn()
}

func (m *Migrator) execFile(ctx context.Context, mf migrationFile, isUp bool) error {
	raw, err := os.ReadFile(mf.path)
	if err != nil {
		return fmt.Errorf("pgmigrate: read %s: %w", mf.path, err)
	}
	bookkeeping := fmt.Sprintf("INSERT INTO schema_migrations(version) VALUES ('%s');", mf.version)
	if !isUp {
		bookkeeping = fmt.Sprintf("DELETE FROM schema_migrations WHERE version = '%s';", mf.version)
	}

	conn, err := m.pool.Acquire(ctx)
	if err != nil {
		return fmt.Errorf("pgmigrate: acquire conn: %w", err)
	}
	defer conn.Release()
	pgc := conn.Conn().PgConn()

	if strings.HasPrefix(strings.TrimSpace(string(raw)), noTxMarker) {
		if _, err := pgc.Exec(ctx, string(raw)).ReadAll(); err != nil {
			return fmt.Errorf("pgmigrate: exec %s: %w", mf.path, err)
		}
		if _, err := pgc.Exec(ctx, bookkeeping).ReadAll(); err != nil {
			return fmt.Errorf("pgmigrate: bookkeep %s: %w", mf.version, err)
		}
		return nil
	}

	body := strings.TrimSpace(string(raw))
	if !strings.HasSuffix(body, ";") {
		body += ";"
	}
	script := fmt.Sprintf("BEGIN;\n%s\n%s\nCOMMIT;", body, bookkeeping)
	if _, err := pgc.Exec(ctx, script).ReadAll(); err != nil {
		_, _ = pgc.Exec(ctx, "ROLLBACK").ReadAll()
		return fmt.Errorf("pgmigrate: exec %s: %w", mf.path, err)
	}
	return nil
}
