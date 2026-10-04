package pgmigrate

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
)

func declaredTables(t *testing.T) map[string]bool {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", "..", "..", "migrations"))
	require.NoError(t, err)
	entries, err := os.ReadDir(root)
	require.NoError(t, err)
	createRe := regexp.MustCompile(`(?im)^CREATE\s+TABLE\s+(?:IF\s+NOT\s+EXISTS\s+)?["\w.]*?"?([\w]+)"?\s*\(`)
	out := map[string]bool{}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".up.sql") {
			continue
		}
		b, err := os.ReadFile(filepath.Join(root, e.Name()))
		require.NoError(t, err)
		for _, m := range createRe.FindAllStringSubmatch(string(b), -1) {
			out[strings.ToLower(m[1])] = true
		}
	}
	require.NotEmpty(t, out, "migrations directory parse failed")
	return out
}

var contractRequired = []string{

	"workspace", "user", "member", "personal_access_token",

	"work_item", "work_item_status", "comment", "work_item_source_context",

	"agent", "agent_task_queue", "task_message", "task_supplement", "task_usage",
	"agent_runtime", "agent_task_checkpoint", "agent_model_credential",
	"workspace_mcp_server", "agent_mcp_server", "squad", "squad_member", "agent_builder_draft",

	"chat_session", "chat_message", "quick_action", "chat_draft_restore",

	"notification", "notification_preference", "event_outbox",

	"attachment", "skill", "agent_skill", "work_item_wakeup", "autopilot",
	"work_item_batch", "work_item_batch_item",
}

func TestTableContract(t *testing.T) {
	base := os.Getenv("TEST_DATABASE_URL")
	if base == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	admin, err := pgx.Connect(context.Background(), base)
	require.NoError(t, err)
	_, _ = admin.Exec(context.Background(), "DROP DATABASE IF EXISTS orkome_contract")
	_, _ = admin.Exec(context.Background(), "CREATE DATABASE orkome_contract")
	admin.Close(context.Background())
	pool, err := pgxpool.New(context.Background(), strings.Replace(base, "/orkome", "/orkome_contract", 1))
	require.NoError(t, err)
	t.Cleanup(pool.Close)

	m := New(pool, "../../../migrations")
	_, err = m.Up(context.Background(), 0)
	require.NoError(t, err)

	rows, err := pool.Query(context.Background(), `
SELECT table_name FROM information_schema.tables
WHERE table_schema='public' AND table_type='BASE TABLE'`)
	require.NoError(t, err)
	defer rows.Close()
	actual := map[string]bool{}
	for rows.Next() {
		var name string
		require.NoError(t, rows.Scan(&name))
		actual[name] = true
	}

	declared := declaredTables(t)
	var missing, extra []string
	for name := range declared {
		if !actual[name] {
			missing = append(missing, name)
		}
	}
	for name := range actual {
		if name == "schema_migrations" {
			continue
		}
		if !declared[name] {
			extra = append(extra, name)
		}
	}
	sort.Strings(missing)
	sort.Strings(extra)
	require.Empty(t, missing, "declared by migrations but missing in DB (reconciliation missing): %v", missing)
	require.Empty(t, extra, "exists in DB but not declared by migrations (reconciliation drift): %v", extra)

	for _, req := range contractRequired {
		require.True(t, declared[req], "implemented-subset required table missing: %s", req)
	}
}
