package pgmigrate

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func testPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set, skipping integration test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func TestPgmigrate_UpIdempotent_DownReplayable(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()

	dir := t.TempDir()
	write := func(name, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("901_probe.up.sql", `CREATE TABLE IF NOT EXISTS migrate_probe (v text)`)
	write("901_probe.down.sql", `DROP TABLE IF EXISTS migrate_probe`)
	write("902_probe.up.sql", `INSERT INTO migrate_probe (v) VALUES ('x')`)
	write("902_probe.down.sql", `DELETE FROM migrate_probe`)

	m := New(pool, dir)

	for _, v := range []string{"901", "902"} {
		_, _ = pool.Exec(ctx, `DELETE FROM schema_migrations WHERE version=$1`, v)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DROP TABLE IF EXISTS migrate_probe`)
	})

	applied, err := m.Up(ctx, 0)
	if err != nil {
		t.Fatalf("up#1: %v", err)
	}
	if len(applied) != 2 {
		t.Fatalf("up#1 applied=%v want 2", applied)
	}

	applied, err = m.Up(ctx, 0)
	if err != nil || len(applied) != 0 {
		t.Fatalf("up#2 should be an idempotent no-op: applied=%v err=%v", applied, err)
	}

	rolled, err := m.Down(ctx, 1)
	if err != nil || len(rolled) != 1 || rolled[0] != "902" {
		t.Fatalf("down 1: rolled=%v err=%v", rolled, err)
	}
	var n int
	pool.QueryRow(ctx, `SELECT count(*) FROM migrate_probe`).Scan(&n)
	if n != 0 {
		t.Fatalf("down did not roll back data (n=%d)", n)
	}

	applied, err = m.Up(ctx, 0)
	if err != nil || len(applied) != 1 || applied[0] != "902" {
		t.Fatalf("up#3: applied=%v err=%v", applied, err)
	}

	write("903_bad.up.sql", `INSERT INTO migrate_probe (v) VALUES ('y'); SELECT * FROM no_such_table`)
	write("903_bad.down.sql", `SELECT 1`)
	pool.Exec(ctx, `DELETE FROM schema_migrations WHERE version='903'`)
	if _, err := m.Up(ctx, 0); err == nil {
		t.Fatal("bad migration should fail")
	}
	var cnt int
	pool.QueryRow(ctx, `SELECT count(*) FROM schema_migrations WHERE version='903'`).Scan(&cnt)
	if cnt != 0 {
		t.Fatal("failed migration must not be recorded (retryable semantics)")
	}
}

func TestPgmigrate_PreHookRunsBeforeVersion(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "904_hook.up.sql"), []byte(`CREATE TABLE IF NOT EXISTS hook_probe (v text)`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "904_hook.down.sql"), []byte(`DROP TABLE IF EXISTS hook_probe`), 0o644); err != nil {
		t.Fatal(err)
	}
	_, _ = pool.Exec(ctx, `DELETE FROM schema_migrations WHERE version='904'`)
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `DROP TABLE IF EXISTS hook_probe`) })

	var ran bool
	m := New(pool, dir)
	m.RegisterPreHook("904", func(_ context.Context, _ *pgxpool.Pool) error {
		ran = true
		return nil
	})
	if _, err := m.Up(ctx, 0); err != nil {
		t.Fatalf("up: %v", err)
	}
	if !ran {
		t.Fatal("pre-hook not executed")
	}

	ran = false
	if _, err := m.Up(ctx, 0); err != nil {
		t.Fatalf("up#2: %v", err)
	}
	if ran {
		t.Fatal("applied version must not rerun pre-hook (idempotent)")
	}
}
