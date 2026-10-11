package pgrepo

import (
	"archive/zip"
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	identitypg "github.com/haozing/ploykit/identity/adapters/pgrepo"
	pkgmigrations "github.com/haozing/ploykit/migrations"
	"github.com/haozing/ploykit/platform/pg"
	pgmigrate "github.com/haozing/ploykit/platform/pgmigrate"
	"orkome/internal/skills/app"
	"orkome/migrations"
)

func skillsEnv(t *testing.T) (*pg.DB, *pgxpool.Pool, string, string) {
	t.Helper()
	base := os.Getenv("TEST_DATABASE_URL")
	if base == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	admin, err := pgx.Connect(context.Background(), base)
	require.NoError(t, err)
	_, _ = admin.Exec(context.Background(), "DROP DATABASE IF EXISTS orkome_b18")
	_, _ = admin.Exec(context.Background(), "CREATE DATABASE orkome_b18")
	admin.Close(context.Background())
	pool, err := pgxpool.New(context.Background(), strings.Replace(base, "/orkome", "/orkome_b18", 1))
	require.NoError(t, err)
	t.Cleanup(pool.Close)
	fw := pgmigrate.New(pool, pkgmigrations.FS, ".")
	_, err = fw.Up(context.Background(), 0)
	require.NoError(t, err)
	m := pgmigrate.New(pool, migrations.FS, ".")
	_, err = m.Up(context.Background(), 0)
	require.NoError(t, err)

	ctx := context.Background()
	idr := identitypg.New(pool, identitypg.Config{})
	u, err := idr.UpsertUserByEmail(ctx, "b18@example.com", time.Now())
	require.NoError(t, err)
	wsFull := struct{ ID string }{}
	err = pool.QueryRow(ctx, `INSERT INTO workspace (slug, name, created_by) VALUES ($1,$2,$3) RETURNING id`,
		"b18-ws", "B", u.ID).Scan(&wsFull.ID)
	require.NoError(t, err)
	return pg.WrapPool(pool), pool, wsFull.ID, u.ID
}

func skillZip(t *testing.T, entries map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, content := range entries {
		fw, err := zw.Create(name)
		require.NoError(t, err)
		_, _ = fw.Write([]byte(content))
	}
	require.NoError(t, zw.Close())
	return buf.Bytes()
}

const mainMd = `---
name: code-review
description: code review tips
---
#   code review
  look at tests first, then the implementation.`

func TestB18_ImportGuards(t *testing.T) {
	db, _, ws, user := skillsEnv(t)
	imp := app.NewImportService(New(db))
	ctx := context.Background()

	sk, err := imp.ImportZip(ctx, ws, user, skillZip(t, map[string]string{
		"code-review/SKILL.md": mainMd,
		"code-review/tips.md":  "# tips\nrun tests first",
	}), "pkg.zip")
	require.NoError(t, err)
	assert.Equal(t, "code-review", sk.Name)
	assert.Equal(t, "import", sk.Origin)
	assert.Equal(t, "code review tips", sk.Description)

	_, err = imp.ImportZip(ctx, ws, user, skillZip(t, map[string]string{
		"code-review/SKILL.md": mainMd,
	}), "pkg2.zip")
	require.NoError(t, err)

	_, err = imp.ImportZip(ctx, ws, user, skillZip(t, map[string]string{
		"../escape/SKILL.md": mainMd,
	}), "evil.zip")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "escape rejected")

	_, err = imp.ImportZip(ctx, ws, user, skillZip(t, map[string]string{
		"ok/SKILL.md":    mainMd,
		"ok/payload.exe": "MZ...",
	}), "bin.zip")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "blacklisted")

	_, err = imp.ImportZip(ctx, ws, user, skillZip(t, map[string]string{
		"ok/README.md": "x",
	}), "nomain.zip")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "SKILL.md")
}

func TestB18_SkillCRUDAndBinding(t *testing.T) {
	db, pool, ws, _ := skillsEnv(t)
	svc := app.NewSkillService(New(db))
	ctx := context.Background()

	sk, err := svc.Create(ctx, app.Skill{WorkspaceID: ws, Name: "writing assistant", Description: "polished text"})
	require.NoError(t, err)

	found, err := svc.Search(ctx, ws, "polish")
	require.NoError(t, err)
	require.Len(t, found, 1)

	_, err = pool.Exec(ctx, `
INSERT INTO agent (workspace_id, name, description, runtime_config)
VALUES ($1,'b18-agent','reviewer','{"provider":"fake","model":"m"}'::jsonb)`, ws)
	require.NoError(t, err)
	var agentID string
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT id::text FROM agent WHERE workspace_id=$1`, ws).Scan(&agentID))

	require.NoError(t, svc.AddAgentSkill(ctx, ws, agentID, sk.ID))
	bound, err := svc.ListAgentSkills(ctx, ws, agentID)
	require.NoError(t, err)
	require.Len(t, bound, 1)
	assert.True(t, bound[0].Enabled)

	require.NoError(t, svc.SetAgentSkillEnabled(ctx, ws, agentID, sk.ID, false))

	files, manifest, err := svc.Files(ctx, ws, sk.ID)
	require.NoError(t, err)
	assert.Empty(t, files)
	assert.Len(t, manifest, 64)

	require.NoError(t, svc.RemoveAgentSkill(ctx, ws, agentID, sk.ID))
	bound, _ = svc.ListAgentSkills(ctx, ws, agentID)
	assert.Empty(t, bound)
}

func TestB18_ProgressiveDisclosureSource(t *testing.T) {
	db, pool, ws, user := skillsEnv(t)
	repo := New(db)
	imp := app.NewImportService(repo)
	svc := app.NewSkillService(repo)
	ctx := context.Background()

	_, err := imp.ImportZip(ctx, ws, user, skillZip(t, map[string]string{
		"code-review/SKILL.md": mainMd,
	}), "pkg.zip")
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `
INSERT INTO agent (workspace_id, name, description, runtime_config)
VALUES ($1,'b18-agent','','{"provider":"fake","model":"m"}'::jsonb)`, ws)
	require.NoError(t, err)
	var agentID string
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT id::text FROM agent WHERE workspace_id=$1`, ws).Scan(&agentID))
	bound, _, _ := repo.GetSkillByName(ctx, ws, "code-review")
	require.NoError(t, svc.AddAgentSkill(ctx, ws, agentID, bound.ID))

	metas, err := listSkillsFor(ctx, repo, ws, agentID)
	require.NoError(t, err)
	require.Len(t, metas, 1)
	assert.Equal(t, "code-review", metas[0].Name)
	assert.Equal(t, "code review tips", metas[0].Description)

	full, err := getSkillFor(ctx, repo, ws, agentID, "code-review")
	require.NoError(t, err)
	assert.Contains(t, full, "look at tests first")
}

func listSkillsFor(ctx context.Context, r *Repo, ws, agentID string) ([]meta1, error) {
	bound, err := r.ListAgentSkills(ctx, ws, agentID)
	if err != nil {
		return nil, err
	}
	var out []meta1
	for _, b := range bound {
		if b.Enabled {
			out = append(out, meta1{b.Name, b.Description})
		}
	}
	return out, nil
}

type meta1 struct{ Name, Description string }

func getSkillFor(ctx context.Context, r *Repo, ws, agentID, name string) (string, error) {
	sk, ok, err := r.GetSkillByName(ctx, ws, name)
	if err != nil || !ok {
		return "", err
	}
	files, err := r.ListFiles(ctx, ws, sk.ID)
	if err != nil {
		return "", err
	}
	for _, f := range files {
		if strings.EqualFold(filepath.Base(f.Path), "skill.md") {
			return stripFrontmatter(f.Content), nil
		}
	}
	return "", nil
}

func stripFrontmatter(c string) string {
	if !strings.HasPrefix(c, "---") {
		return c
	}
	idx := strings.Index(c[3:], "\n---")
	if idx >= 0 {
		return strings.TrimSpace(c[3+idx+4:])
	}
	return c
}
