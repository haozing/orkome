package e2e

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestScenario_S1_UserWorkspaceJourney(t *testing.T) {
	runScenario(t, "S1 new-user full journey", []step{
		{name: "owner code login (new email auto-creates the account)", do: func(w *world) {
			w.loginAs("owner", "owner@s1.e2e")
			w.vars["ownerEmail"] = "owner@s1.e2e"
		}},
		{name: "owner creates the workspace", do: func(w *world) {
			w.vars["wsID"] = w.createWSAs("owner", "s1journey")
		}},
		{name: "DB assert: workspace persisted with the correct owner member role", do: func(w *world) {
			var name string
			err := w.st.pool.QueryRow(t.Context(),
				`SELECT name FROM workspace WHERE id = $1`, w.str("wsID")).Scan(&name)
			require.NoError(t, err, "workspace row should exist")
			require.Equal(t, "E2E-s1journey", name)
			require.Equal(t, "owner", w.memberRole("owner@s1.e2e", w.str("wsID")))
		}},
		{name: "owner invites Bob", do: func(w *world) {
			rec := w.st.do("owner", http.MethodPost,
				fmt.Sprintf("/api/workspaces/%s/invitations", w.str("wsID")),
				map[string]string{"email": "bob@s1.e2e", "role": "member"}, nil)
			require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
			w.vars["invID"] = decodeJSON(t, rec)["id"].(string)
		}},
		{name: "Bob logs in and sees the invitation in the invite list", do: func(w *world) {
			w.loginAs("bob", "bob@s1.e2e")
			rec := w.st.do("bob", http.MethodGet, "/api/invitations", nil, nil)
			require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
			require.Contains(t, rec.Body.String(), w.str("invID"),
				"Bob's invitation list should contain the just-received invitation")
		}},
		{name: "Bob accepts the invitation", do: func(w *world) {
			rec := w.st.do("bob", http.MethodPost,
				fmt.Sprintf("/api/invitations/%s/accept", w.str("invID")), nil, nil)
			require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		}},
		{name: "DB assert: Bob is already a member", do: func(w *world) {
			require.Equal(t, "member", w.memberRole("bob@s1.e2e", w.str("wsID")))
		}},
		{name: "Bob views the member list as a member", do: func(w *world) {
			rec := w.st.do("bob", http.MethodGet,
				fmt.Sprintf("/api/workspaces/%s/members", w.str("wsID")), nil, nil)
			require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		}},
		{name: "stranger Carol rejected (permission boundary)", do: func(w *world) {
			w.loginAs("carol", "carol@s1.e2e")
			rec := w.st.do("carol", http.MethodGet,
				fmt.Sprintf("/api/workspaces/%s/members", w.str("wsID")), nil, nil)
			require.GreaterOrEqual(t, rec.Code, http.StatusBadRequest,
				"non-member access should be rejected, got %d: %s", rec.Code, rec.Body.String())
			require.Less(t, rec.Code, http.StatusInternalServerError, "should be a business rejection, not a server error")
		}},
		{name: "owner removes Bob", do: func(w *world) {
			memberID := w.memberIDOf("bob@s1.e2e", w.str("wsID"))
			rec := w.st.do("owner", http.MethodDelete,
				fmt.Sprintf("/api/workspaces/%s/members/%s", w.str("wsID"), memberID), nil, nil)
			require.Equal(t, http.StatusNoContent, rec.Code, rec.Body.String())
		}},
		{name: "Bob loses access after removal (clean isolation)", do: func(w *world) {
			rec := w.st.do("bob", http.MethodGet,
				fmt.Sprintf("/api/workspaces/%s/members", w.str("wsID")), nil, nil)
			require.GreaterOrEqual(t, rec.Code, http.StatusBadRequest,
				"removed member must not access anymore, got %d: %s", rec.Code, rec.Body.String())
		}},
		{name: "C4 ruling check: share link previewable without login (public semantics)", do: func(w *world) {
			rec := w.st.do("owner", http.MethodPost,
				fmt.Sprintf("/api/workspaces/%s/share-links", w.str("wsID")),
				map[string]any{"role": "member"}, nil)
			require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
			code, _ := decodeJSON(t, rec)["code"].(string)
			require.NotEmpty(t, code)

			w.bootstrapCSRF("visitor")
			rec = w.st.do("visitor", http.MethodGet,
				fmt.Sprintf("/api/share-links/%s", code), nil, nil)
			require.Equal(t, http.StatusOK, rec.Code,
				"anonymous visitors should preview share links (public semantics, orkome aligned), body=%s", rec.Body.String())
		}},
		{name: "journey terminal assertion: only the owner remains as the sole member", do: func(w *world) {
			var n int
			err := w.st.pool.QueryRow(t.Context(),
				`SELECT count(*) FROM member WHERE workspace_id = $1`, w.str("wsID")).Scan(&n)
			require.NoError(t, err)
			require.Equal(t, 1, n, "only the owner remains after removal")
			require.Equal(t, "owner", w.memberRole("owner@s1.e2e", w.str("wsID")))
		}},
	})
}
