package http

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/haozing/ploykit/platform/webx"
)

// PAT 收口（P1-1）handler 级单测：POST /cli-token 的 scope 多态——不带 /
// 带合法 / 坏形状（非 UUID、超上限、空约束）。走 MountExtensions 全链
// （auth 中间件注入 session principal），不依赖 DB（/cli-token 只碰
// Deps.Tokens；pins/ui-tokens 路由注册但不请求）。

// cliTokenHarness builds the extension mux with a session-principal-injecting
// auth chain and the shared stubMinter (integration_test.go) recording calls.
func cliTokenHarness(t *testing.T) (*http.ServeMux, *stubMinter) {
	t.Helper()
	minter := &stubMinter{}
	mux := http.NewServeMux()
	auth := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			p := &webx.Principal{UserID: "u-cli-scope", Email: "scope@example.com", Source: webx.SourceSession}
			next.ServeHTTP(w, r.WithContext(webx.WithPrincipal(r.Context(), p)))
		})
	}
	MountExtensions(mux, auth, Deps{Tokens: minter})
	return mux, minter
}

func postCLIToken(t *testing.T, mux *http.ServeMux, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/cli-token", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

func TestCLIToken_Scope(t *testing.T) {
	const wsA = "11111111-1111-1111-1111-111111111111"
	const wsB = "22222222-2222-2222-2222-222222222222"

	// ①不带 scope：透传 nil（= 不限），响应形状不变。
	mux, minter := cliTokenHarness(t)
	rec := postCLIToken(t, mux, `{"name":"ci","ttl_seconds":3600}`)
	require.Equal(t, 201, rec.Code, rec.Body.String())
	var out struct {
		Token  string `json:"token"`
		ID     string `json:"id"`
		Name   string `json:"name"`
		Prefix string `json:"prefix"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out))
	assert.Equal(t, "tk_plainsecret", out.Token)
	assert.Equal(t, "pat-1", out.ID)
	assert.Equal(t, "ci", out.Name)
	assert.Equal(t, "tk_abcde", out.Prefix)
	require.Len(t, minter.calls, 1)
	assert.Nil(t, minter.calls[0].Scope, "no scope in request → nil scope minted (unrestricted)")

	// ②带合法 scope：原样透传给 CreatePAT。
	rec = postCLIToken(t, mux, `{"name":"ci","scope":{"workspace_ids":["`+wsA+`","`+wsB+`"]}}`)
	require.Equal(t, 201, rec.Code, rec.Body.String())
	require.Len(t, minter.calls, 2)
	require.NotNil(t, minter.calls[1].Scope)
	assert.Equal(t, []string{wsA, wsB}, minter.calls[1].Scope.WorkspaceIDs)

	// ③坏 UUID：400 E_VALIDATION，不触达 minter。
	rec = postCLIToken(t, mux, `{"name":"ci","scope":{"workspace_ids":["not-a-uuid"]}}`)
	assert.Equal(t, 400, rec.Code, "non-UUID workspace id must be rejected: "+rec.Body.String())
	assert.Contains(t, rec.Body.String(), "E_VALIDATION")
	assert.Len(t, minter.calls, 2, "rejected request must not mint")

	// ④超上限（>10 个合法 UUID）：400 too_many。
	ids := make([]string, 11)
	for i := range ids {
		ids[i] = fmt.Sprintf(`"44444444-4444-4444-4444-%012d"`, i)
	}
	rec = postCLIToken(t, mux, `{"name":"ci","scope":{"workspace_ids":[`+strings.Join(ids, ",")+`]}}`)
	assert.Equal(t, 400, rec.Code, "more than 10 workspace ids must be rejected: "+rec.Body.String())
	assert.Contains(t, rec.Body.String(), "too_many")
	assert.Len(t, minter.calls, 2)

	// ⑤空约束 scope（空列表）：归一为 nil，不铸出全禁令牌
	//（AllowsWorkspace 对非 nil 空列表 = 全拒，是脚枪）。
	rec = postCLIToken(t, mux, `{"name":"ci","scope":{"workspace_ids":[]}}`)
	require.Equal(t, 201, rec.Code, rec.Body.String())
	require.Len(t, minter.calls, 3)
	assert.Nil(t, minter.calls[2].Scope, "constraint-free scope collapses to nil")
}
