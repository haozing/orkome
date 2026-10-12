package http

// Integration coverage for the wave-1 work-backend contract fixes; requires
// TEST_DATABASE_URL (skips otherwise). One shared stack to keep the per-test
// migrate cost bounded. See docs/fix-wave1-e-work-backend-2026-10-12.md.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func wave1Login(t *testing.T, h *m2h, client, email string) {
	t.Helper()
	rec := h.do(client, http.MethodGet, "/config", nil)
	require.Equal(t, 200, rec.Code)
	var cfg struct {
		CSRFToken string `json:"csrf_token"`
	}
	jsonOf(t, rec, &cfg)
	h.csrf[client] = cfg.CSRFToken
	rec = h.do(client, http.MethodPost, "/auth/verify-code", map[string]string{"email": email, "code": "123456"})
	require.Equal(t, 200, rec.Code, rec.Body.String())
	var vout struct {
		CSRFToken string `json:"csrf_token"`
	}
	jsonOf(t, rec, &vout)
	if vout.CSRFToken != "" {
		h.csrf[client] = vout.CSRFToken
	}
}

func wave1WS(t *testing.T, h *m2h, client, slug string) string {
	t.Helper()
	rec := h.do(client, http.MethodPost, "/api/workspaces",
		map[string]string{"name": "Wave1 " + slug, "slug": slug})
	require.Equal(t, 201, rec.Code, rec.Body.String())
	var ws struct {
		ID string `json:"id"`
	}
	jsonOf(t, rec, &ws)
	h.wsID = ws.ID
	return ws.ID
}

func wave1Item(t *testing.T, h *m2h, client, title string, priority int) string {
	t.Helper()
	rec := h.do(client, http.MethodPost, "/api/work-items",
		map[string]any{"title": title, "status": "todo", "priority": priority})
	require.Equal(t, 201, rec.Code, rec.Body.String())
	var wi struct {
		ID string `json:"id"`
	}
	jsonOf(t, rec, &wi)
	return wi.ID
}

func TestM2_Wave1WorkContractFixes(t *testing.T) {
	h := newM2(t)
	wave1Login(t, h, "u", "wave1@m2.dev")
	wave1WS(t, h, "u", "wave1-ws")

	// ---------- fix 1: exhausted list page omits next_cursor ----------
	t.Run("list_exhausted_cursor_omitted", func(t *testing.T) {
		for _, s := range []string{"w1 page a", "w1 page b", "w1 page c"} {
			wave1Item(t, h, "u", s, 0)
		}
		rec := h.do("u", http.MethodGet, "/api/work-items?limit=2", nil)
		require.Equal(t, 200, rec.Code, rec.Body.String())
		var p1 struct {
			Items      []json.RawMessage `json:"items"`
			NextCursor string            `json:"next_cursor"`
		}
		jsonOf(t, rec, &p1)
		require.Len(t, p1.Items, 2)
		require.NotEmpty(t, p1.NextCursor, "more pages exist → cursor present")

		rec = h.do("u", http.MethodGet,
			"/api/work-items?limit=2&cursor="+url.QueryEscape(p1.NextCursor), nil)
		require.Equal(t, 200, rec.Code)
		assert.NotContains(t, rec.Body.String(), `"next_cursor":""`,
			"exhausted page must not send an empty-string cursor")
		var p2 map[string]json.RawMessage
		jsonOf(t, rec, &p2)
		if raw, ok := p2["next_cursor"]; ok {
			var s string
			require.NoError(t, json.Unmarshal(raw, &s))
			assert.Empty(t, s, "even if the key is present it must not carry an empty string")
		}
	})

	// ---------- fix 2: core PUT bumps last_activity_at ----------
	t.Run("update_bumps_last_activity_at", func(t *testing.T) {
		wi := wave1Item(t, h, "u", "w1 activity host", 0)
		_, err := h.pool.Exec(context.Background(),
			`UPDATE work_item SET last_activity_at = now() - interval '2 hours' WHERE id=$1`, wi)
		require.NoError(t, err)
		var before time.Time
		require.NoError(t, h.pool.QueryRow(context.Background(),
			`SELECT last_activity_at FROM work_item WHERE id=$1`, wi).Scan(&before))

		rec := h.do("u", http.MethodPut, "/api/work-items/"+wi, map[string]any{"priority": 7})
		require.Equal(t, 200, rec.Code, rec.Body.String())

		var after time.Time
		require.NoError(t, h.pool.QueryRow(context.Background(),
			`SELECT last_activity_at FROM work_item WHERE id=$1`, wi).Scan(&after))
		assert.True(t, after.Sub(before) > time.Hour,
			"priority PUT must bump last_activity_at (FP-11 P1-2): before=%v after=%v", before, after)
	})

	// ---------- fix 3: PUT /api/labels/{id} updates by path id ----------
	t.Run("label_put_by_id", func(t *testing.T) {
		rec := h.do("u", http.MethodPost, "/api/labels", map[string]string{"name": "w1-red", "color": "#f00"})
		require.Equal(t, 201, rec.Code, rec.Body.String())
		var l struct {
			ID string `json:"id"`
		}
		jsonOf(t, rec, &l)

		rec = h.do("u", http.MethodPut, "/api/labels/"+l.ID, map[string]any{"name": "w1-crimson", "color": "#c00"})
		require.Equal(t, 200, rec.Code, rec.Body.String())
		var upd struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		}
		jsonOf(t, rec, &upd)
		assert.Equal(t, l.ID, upd.ID, "same label updated in place — no phantom new label")
		assert.Equal(t, "w1-crimson", upd.Name)

		// count check: exactly one w1-* label, the renamed one
		rec = h.do("u", http.MethodGet, "/api/labels", nil)
		require.Equal(t, 200, rec.Code)
		var all []map[string]any
		jsonOf(t, rec, &all)
		n := 0
		for _, it := range all {
			if name, _ := it["name"].(string); strings.HasPrefix(name, "w1-") {
				n++
			}
		}
		assert.Equal(t, 1, n, "rename must not fabricate a second label")

		// unknown id → 404 (both a valid-shape ghost and a garbage id)
		assert.Equal(t, 404, h.do("u", http.MethodPut, "/api/labels/00000000-0000-4000-8000-0000000000ff",
			map[string]any{"name": "ghost"}).Code)
		assert.Equal(t, 404, h.do("u", http.MethodPut, "/api/labels/not-a-uuid",
			map[string]any{"name": "ghost"}).Code, "non-uuid path id must 404, not 500")
	})

	// ---------- fix 4: batch partial failure contract ----------
	t.Run("batch_partial_failure", func(t *testing.T) {
		a := wave1Item(t, h, "u", "w1 batch a", 0)
		rec := h.do("u", http.MethodPost, "/api/work-items/batch-update",
			map[string]any{"ids": []string{a, "00000000-0000-4000-8000-000000000abc"},
				"patch": map[string]any{"priority": 5}})
		require.Equal(t, 200, rec.Code, rec.Body.String())
		var out struct {
			Updated int            `json:"updated"`
			Failed  []batchFailure `json:"failed"`
		}
		jsonOf(t, rec, &out)
		assert.Equal(t, 1, out.Updated)
		require.Len(t, out.Failed, 1)
		assert.Contains(t, out.Failed[0].Reason, "E_NOT_FOUND")

		rec = h.do("u", http.MethodPost, "/api/work-items/batch-update",
			map[string]any{"ids": []string{a}, "patch": map[string]any{"description": "sneaky"}})
		assert.Equal(t, 400, rec.Code, "unsupported field must 400: "+rec.Body.String())

		rec = h.do("u", http.MethodPost, "/api/work-items/batch-update",
			map[string]any{"ids": []string{a}, "patch": map[string]any{}})
		assert.Equal(t, 400, rec.Code, "empty patch must 400 instead of ghost-bumping revision")
	})

	// ---------- fix 5: filters actually execute; unknown keys 400 ----------
	t.Run("filters_execute", func(t *testing.T) {
		hi := wave1Item(t, h, "u", "w1 filter hi", 8)
		lo := wave1Item(t, h, "u", "w1 filter lo", 1)

		rec := h.do("u", http.MethodPost, "/api/labels", map[string]string{"name": "w1-flt"})
		require.Equal(t, 201, rec.Code)
		var l struct {
			ID string `json:"id"`
		}
		jsonOf(t, rec, &l)
		rec = h.do("u", http.MethodPost, "/api/work-items/"+hi+"/labels", map[string]any{"label_id": l.ID})
		require.Equal(t, 200, rec.Code, rec.Body.String())

		rowsWith := func(filtersJSON string) []string {
			t.Helper()
			body := map[string]any{"filters": json.RawMessage(filtersJSON)}
			rec := h.do("u", http.MethodPost, "/api/work-items/table/rows", body)
			require.Equal(t, 200, rec.Code, rec.Body.String())
			var out struct {
				Items []struct {
					ID string `json:"id"`
				} `json:"items"`
			}
			jsonOf(t, rec, &out)
			ids := make([]string, 0, len(out.Items))
			for _, it := range out.Items {
				ids = append(ids, it.ID)
			}
			return ids
		}

		assert.Contains(t, rowsWith(`{"priority_min":5}`), hi, "priority_min must execute")
		assert.NotContains(t, rowsWith(`{"priority_min":5}`), lo)
		assert.Empty(t, rowsWith(`{"priority_min":99}`), "audit smoking gun: priority_min=99 previously returned everything")

		assert.ElementsMatch(t, []string{hi}, rowsWith(`{"labels":["w1-flt"]}`), "labels by name must execute")
		assert.ElementsMatch(t, []string{hi}, rowsWith(`{"labels":["`+l.ID+`"]}`), "labels by id must execute")

		rec = h.do("u", http.MethodPost, "/api/work-items/table/rows",
			map[string]any{"filters": map[string]any{"banana": 1}})
		assert.Equal(t, 400, rec.Code, "unknown filter key must 400, not silently no-op")

		// /query: implemented filter still compiles; unknown key rejected
		rec = h.do("u", http.MethodPost, "/api/work-items/query",
			map[string]any{"filters": map[string]any{"priority_min": 99}})
		require.Equal(t, 200, rec.Code)
		rec = h.do("u", http.MethodPost, "/api/work-items/query",
			map[string]any{"filters": map[string]any{"banana": 1}})
		assert.Equal(t, 400, rec.Code)
	})

	// ---------- fix 6: rows pagination round trip ----------
	t.Run("rows_cursor_round_trip", func(t *testing.T) {
		for _, s := range []string{"w1 rows a", "w1 rows b", "w1 rows c", "w1 rows d"} {
			wave1Item(t, h, "u", s, 0)
		}
		rec := h.do("u", http.MethodPost, "/api/work-items/table/rows",
			map[string]any{"filters": map[string]any{"status": []string{"todo"}}, "limit": 3})
		require.Equal(t, 200, rec.Code, rec.Body.String())
		var p1 struct {
			Items      []map[string]any `json:"items"`
			NextCursor string           `json:"next_cursor"`
		}
		jsonOf(t, rec, &p1)
		require.Len(t, p1.Items, 3)
		require.NotEmpty(t, p1.NextCursor)
		for _, it := range p1.Items {
			_, isStr := it["id"].(string)
			require.True(t, isStr, "id must be a JSON string, got %T", it["id"])
			_, hasCoalesce := it["coalesce"]
			assert.False(t, hasCoalesce, "expression-named field leaked into rows response")
			require.Contains(t, it, "assignee_id", "assignee_id column must be explicitly named")
		}

		seen := map[string]bool{}
		for _, it := range p1.Items {
			seen[it["id"].(string)] = true
		}

		// walk every remaining page — page 2 replay is the exact call that
		// 500'd before the fix
		cursor := p1.NextCursor
		pages := 0
		for cursor != "" && pages < 20 {
			rec := h.do("u", http.MethodPost, "/api/work-items/table/rows",
				map[string]any{"filters": map[string]any{"status": []string{"todo"}},
					"limit": 3, "cursor": cursor})
			require.Equal(t, 200, rec.Code, "self-produced cursor must replay: "+rec.Body.String())
			var p struct {
				Items      []map[string]any `json:"items"`
				NextCursor string           `json:"next_cursor"`
			}
			jsonOf(t, rec, &p)
			require.LessOrEqual(t, len(p.Items), 3)
			for _, it := range p.Items {
				id, _ := it["id"].(string)
				assert.False(t, seen[id], "no row may repeat across pages")
				seen[id] = true
			}
			cursor = p.NextCursor
			pages++
		}
		assert.Empty(t, cursor, "pagination must terminate")
		require.Positive(t, pages, "there were more rows than one page")

		// garbage cursors are 400, never 500 (D-11)
		for _, cur := range []string{"a|b", "notatime|00000000-0000-4000-8000-0000000000ff",
			"eyJhIjoxfQ", "!!"} {
			rec := h.do("u", http.MethodPost, "/api/work-items/table/rows",
				map[string]any{"filters": map[string]any{}, "cursor": cur})
			assert.Equal(t, 400, rec.Code, "cursor %q must 400, got %d", cur, rec.Code)
		}

		// search plain mode: id must be a string too (D-09 twin)
		rec = h.do("u", http.MethodGet, "/api/work-items/search?q=w1+rows+a", nil)
		require.Equal(t, 200, rec.Code)
		var sr struct {
			Items []map[string]any `json:"items"`
		}
		jsonOf(t, rec, &sr)
		require.NotEmpty(t, sr.Items)
		_, isStr := sr.Items[0]["id"].(string)
		assert.True(t, isStr, "search plain id must be a JSON string")
	})
}
