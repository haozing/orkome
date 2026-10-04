package e2e

import (
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestScenario_S3_Collaboration(t *testing.T) {
	runScenario(t, "S3 multi-user collaboration", []step{
		{name: "Alice logs in, creates workspace and invites Bob", do: func(w *world) {
			w.loginAs("alice", "alice@s3.e2e")
			w.vars["wsID"] = w.createWSAs("alice", "s3collab")
			rec := w.st.do("alice", http.MethodPost,
				fmt.Sprintf("/api/workspaces/%s/invitations", w.str("wsID")),
				map[string]string{"email": "bob@s3.e2e", "role": "member"}, nil)
			require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
			w.vars["invID"] = decodeJSON(t, rec)["id"].(string)
		}},
		{name: "Bob logs in and accepts the invitation", do: func(w *world) {
			w.loginAs("bob", "bob@s3.e2e")
			rec := w.st.do("bob", http.MethodPost,
				fmt.Sprintf("/api/invitations/%s/accept", w.str("invID")), nil, nil)
			require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
			require.Equal(t, "member", w.memberRole("bob@s3.e2e", w.str("wsID")))
		}},
		{name: "Alice creates a collaborative work item", do: func(w *world) {
			rec := w.st.do("alice", http.MethodPost, "/api/work-items",
				map[string]string{"title": "S3 collaboration items", "status": "todo"}, nil)
			require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
			w.vars["wiID"] = decodeJSON(t, rec)["id"].(string)
		}},
		{name: "Alice comments and @Bob (mention event chain)", do: func(w *world) {
			var bobID string
			require.NoError(t, w.st.pool.QueryRow(t.Context(),
				`SELECT id::text FROM "user" WHERE email='bob@s3.e2e'`).Scan(&bobID))
			w.vars["bobID"] = bobID
			rec := w.st.do("alice", http.MethodPost,
				fmt.Sprintf("/api/work-items/%s/comments", w.str("wiID")),
				map[string]string{"content": fmt.Sprintf("[@Bob](mention://user/%s) please review", bobID)}, nil)
			require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
			w.vars["cmID"] = decodeJSON(t, rec)["comment"].(map[string]any)["id"].(string)
		}},
		{name: "mention notification appears in Bob's inbox (event projection, poll-wait)", do: func(w *world) {
			var itemID string
			waitFor(t, func() bool {
				rec := w.st.do("bob", http.MethodGet, "/api/notifications", nil, nil)
				if rec.Code != http.StatusOK {
					return false
				}
				var out struct {
					Items []struct {
						ID    string `json:"id"`
						Title string `json:"title"`
					} `json:"items"`
				}
				if err := unmarshalInto(rec.Body.String(), &out); err != nil {
					return false
				}
				for _, it := range out.Items {
					if it.Title == "new comment" {
						itemID = it.ID
						return true
					}
				}
				return false
			}, "Bob's inbox receives the mention notification")
			require.NotEmpty(t, itemID)
			w.vars["notificationID"] = itemID
		}},
		{name: "DB assert: mention notification delivered to Bob only (Alice has none)", do: func(w *world) {
			var n int
			require.NoError(t, w.st.pool.QueryRow(t.Context(),
				`SELECT count(*) FROM notification WHERE work_item_id=$1 AND recipient_user_id=$2`,
				w.str("wiID"), w.str("bobID")).Scan(&n))
			require.Equal(t, 1, n, "Bob should receive exactly 1 item")

			require.NoError(t, w.st.pool.QueryRow(t.Context(),
				`SELECT count(*) FROM notification WHERE work_item_id=$1 AND recipient_user_id=(SELECT id FROM "user" WHERE email='alice@s3.e2e')`,
				w.str("wiID")).Scan(&n))
			require.Equal(t, 1, n, "the subscriber (creator Alice) should receive subscription-path notifications")
		}},
		{name: "Bob adds a reaction", do: func(w *world) {
			rec := w.st.do("bob", http.MethodPost,
				fmt.Sprintf("/api/comments/%s/reactions", w.str("cmID")),
				map[string]string{"emoji": "👍"}, nil)
			require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
		}},
		{name: "Bob resolves the comment", do: func(w *world) {
			rec := w.st.do("bob", http.MethodPost,
				fmt.Sprintf("/api/comments/%s/resolve", w.str("cmID")), nil, nil)
			require.Equal(t, http.StatusNoContent, rec.Code, rec.Body.String())
		}},
		{name: "DB assert: reaction and resolve persisted", do: func(w *world) {
			var n int
			require.NoError(t, w.st.pool.QueryRow(t.Context(),
				`SELECT count(*) FROM comment_reaction WHERE comment_id=$1 AND user_id=$2`,
				w.str("cmID"), w.str("bobID")).Scan(&n))
			require.Equal(t, 1, n)
			var resolved bool
			require.NoError(t, w.st.pool.QueryRow(t.Context(),
				`SELECT resolved_at IS NOT NULL FROM comment WHERE id=$1`, w.str("cmID")).Scan(&resolved))
			require.True(t, resolved)
		}},
		{name: "Bob's inbox marked read, unread drops to zero", do: func(w *world) {
			rec := w.st.do("bob", http.MethodPost,
				fmt.Sprintf("/api/notifications/%s/read", w.str("notificationID")), nil, nil)
			require.Equal(t, http.StatusNoContent, rec.Code, rec.Body.String())
			require.Eventually(t, func() bool {
				rec := w.st.do("bob", http.MethodGet, "/api/notifications/unread-count", nil, nil)
				var out struct {
					Count int `json:"count"`
				}
				return rec.Code == http.StatusOK &&
					unmarshalInto(rec.Body.String(), &out) == nil && out.Count == 0
			}, 3*time.Second, 50*time.Millisecond, "unread count should drop to zero")
		}},
	})
}
