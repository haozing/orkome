package http

// W2-3 / FP-09 P1-4：MCP 两级注册表 PUT 的三态凭证契约（HTTP wire 层）。
//   - 不带 credentials 字段 → Credentials=nil →（pg 层 coalesce）保留库中现值；
//   - 显式 null / {} → Credentials=非 nil 空 map → 显式清空；
//   - 新值 → 覆盖。
// ws 级与 agent 级同一口径；agent 级 POST 重名 → 409（FP-07 P1-4，不再静默 upsert）。
// pg 层行为（密封落库/23505 映射）见 pgrepo 集成测试。

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/haozing/ploykit/platform/webx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"orkome/internal/contract/apierr"
	"orkome/internal/contract/events"
	orchapp "orkome/internal/orchestration/app"
)

type mcpFakeRepo struct {
	orchapp.Repo // 兜底（未用方法不实现）

	agents       map[string]orchapp.Agent
	wsCreated    *orchapp.WorkspaceMCP
	wsUpdated    *orchapp.WorkspaceMCP
	agentCreated *orchapp.AgentMCP
	agentUpdated *orchapp.AgentMCP
	dupNameErr   bool // 置 true 模拟 PG 23505 → 409
	nextServerID string
	agentMCPRows []orchapp.AgentMCP
	wsMCPRows    []orchapp.WorkspaceMCP
}

func (f *mcpFakeRepo) GetAgent(_ context.Context, _, id string) (orchapp.Agent, bool, error) {
	a, ok := f.agents[id]
	return a, ok, nil
}

func (f *mcpFakeRepo) CreateWorkspaceMCP(_ context.Context, m orchapp.WorkspaceMCP) (orchapp.WorkspaceMCP, error) {
	if f.dupNameErr {
		return m, apierr.New(apierr.Conflict, "mcp server name already taken")
	}
	f.wsCreated = &m
	m.ID = "ws-srv-1"
	m.HasCreds = len(m.Credentials) > 0 // 对齐真 repo 的 scan 行为
	return m, nil
}

func (f *mcpFakeRepo) UpdateWorkspaceMCP(_ context.Context, m orchapp.WorkspaceMCP) (orchapp.WorkspaceMCP, bool, error) {
	if f.dupNameErr {
		return m, false, apierr.New(apierr.Conflict, "mcp server name already taken")
	}
	f.wsUpdated = &m
	m.HasCreds = len(m.Credentials) > 0 // 无凭证语义=保留现值，此处仅单测简化
	return m, true, nil
}

func (f *mcpFakeRepo) ListWorkspaceMCP(context.Context, string) ([]orchapp.WorkspaceMCP, error) {
	return f.wsMCPRows, nil
}

func (f *mcpFakeRepo) CreateAgentMCP(_ context.Context, m orchapp.AgentMCP) (orchapp.AgentMCP, error) {
	if f.dupNameErr {
		return m, apierr.New(apierr.Conflict, "mcp server name already taken")
	}
	f.agentCreated = &m
	m.ID = "ag-srv-1"
	m.HasCreds = len(m.Credentials) > 0 // 对齐真 repo 的 scan 行为
	return m, nil
}

func (f *mcpFakeRepo) UpdateAgentMCP(_ context.Context, m orchapp.AgentMCP) (orchapp.AgentMCP, bool, error) {
	if f.dupNameErr {
		return m, false, apierr.New(apierr.Conflict, "mcp server name already taken")
	}
	f.agentUpdated = &m
	m.HasCreds = len(m.Credentials) > 0
	return m, true, nil
}

func (f *mcpFakeRepo) ListAgentMCP(_ context.Context, _ string, agentID string) ([]orchapp.AgentMCP, bool, error) {
	if _, ok := f.agents[agentID]; !ok {
		return nil, false, nil
	}
	return f.agentMCPRows, true, nil
}

// GetCredential：credential_id 存在性校验（P1-4）的 fake——只认 "cred-ok"。
func (f *mcpFakeRepo) GetCredential(_ context.Context, _, id string) (string, string, bool, error) {
	if id == "cred-ok" {
		return "anthropic", "sk-wired", true, nil
	}
	return "", "", false, nil
}

// Delete 两级：no-op（fake 嵌入的 nil Repo 接口会让自动提升的 Delete panic）。
func (f *mcpFakeRepo) DeleteWorkspaceMCP(context.Context, string, string) error { return nil }
func (f *mcpFakeRepo) DeleteAgentMCP(context.Context, string, string, string) error {
	return nil
}

// noopEmitter：AgentService 需要一个 Emitter；本文件不测事件，静默丢弃。
type noopEmitter struct{}

func (noopEmitter) Emit(context.Context, events.Envelope) {}

func newMCPRouter() (http.Handler, *mcpFakeRepo) {
	r, repo, _ := newMCPRouterRole("owner")
	return r, repo
}

// newMCPRouterRole：可指定 principal 角色的 MCP 路由架子（P2-D7 admin 闸
// 用 member 角色验证 403 面）。返回 svc 供测试接线 ToolProbe/ToolProbeCounted。
func newMCPRouterRole(role string) (http.Handler, *mcpFakeRepo, *orchapp.MCPServerService) {
	repo := &mcpFakeRepo{agents: map[string]orchapp.Agent{"ag-1": {ID: "ag-1"}}}
	// Agents 不能为 nil：Mount 在 agent 段前有 `if d.Agents == nil { return }`，
	// 之后才挂 agent 级与 ws 级 MCP 路由。
	svc := orchapp.NewMCPServerService(repo)
	svc.Egress = nil // 单测不做出网 DNS 解析（egressx fail-closed 会拒绝占位域名）
	d := Deps{
		Agents:      orchapp.NewAgentService(repo, noopEmitter{}),
		MCP:         svc,
		Repo:        repo, // credential_id 存在性校验走 d.Repo.GetCredential
		MemberCheck: func(context.Context, string, string) (bool, error) { return true, nil },
	}
	r := http.NewServeMux()
	mw := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			r2 := r.WithContext(webx.WithPrincipal(r.Context(),
				&webx.Principal{UserID: "mcp-tester", Role: role, Source: webx.SourceSession}))
			r2.Header.Set("X-Workspace-Id", "ws-1")
			next.ServeHTTP(w, r2)
		})
	}
	Mount(r, d, mw)
	return r, repo, svc
}

func mcpReq(r http.Handler, method, target, body string) *httptest.ResponseRecorder {
	var req *http.Request
	if body == "" {
		req = httptest.NewRequest(method, target, nil)
	} else {
		req = httptest.NewRequest(method, target, strings.NewReader(body))
	}
	req.Header.Set("X-Workspace-Id", "ws-1")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

func TestMCPCredsThreeState_WSLevel(t *testing.T) {
	r, repo := newMCPRouter()
	const put = "/api/workspaces/ws-1/mcp-servers/srv-1"

	t.Run("PUT without credentials field keeps existing (nil passthrough)", func(t *testing.T) {
		rec := mcpReq(r, "PUT", put, `{"name":"srv","transport":"streamable_http","url":"https://x.example"}`)
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		require.NotNil(t, repo.wsUpdated)
		assert.Nil(t, repo.wsUpdated.Credentials, "缺字段=保留（pg 层 coalesce 现值）")
	})

	t.Run("PUT credentials null clears explicitly", func(t *testing.T) {
		rec := mcpReq(r, "PUT", put, `{"name":"srv","url":"https://x.example","credentials":null}`)
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		require.NotNil(t, repo.wsUpdated)
		require.NotNil(t, repo.wsUpdated.Credentials, "显式 null 归一为非 nil 空 map=清空，不得误判为缺字段")
		assert.Empty(t, repo.wsUpdated.Credentials)
	})

	t.Run("PUT credentials empty object clears explicitly", func(t *testing.T) {
		rec := mcpReq(r, "PUT", put, `{"name":"srv","url":"https://x.example","credentials":{}}`)
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		require.NotNil(t, repo.wsUpdated)
		require.NotNil(t, repo.wsUpdated.Credentials)
		assert.Empty(t, repo.wsUpdated.Credentials)
	})

	t.Run("PUT credentials new value overwrites", func(t *testing.T) {
		rec := mcpReq(r, "PUT", put, `{"name":"srv","url":"https://x.example","credentials":{"api_key":"sk-new"}}`)
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		require.NotNil(t, repo.wsUpdated)
		assert.Equal(t, map[string]any{"api_key": "sk-new"}, repo.wsUpdated.Credentials)
	})

	t.Run("POST create without credentials yields empty creds", func(t *testing.T) {
		rec := mcpReq(r, "POST", "/api/workspaces/ws-1/mcp-servers", `{"name":"srv2","url":"https://y.example"}`)
		require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
		require.NotNil(t, repo.wsCreated)
		assert.Empty(t, repo.wsCreated.Credentials, "创建侧无保留语义，缺省即空凭证")
	})
}

func TestMCPCredsThreeState_AgentLevel(t *testing.T) {
	r, repo := newMCPRouter()
	const put = "/api/agents/ag-1/mcp-servers/srv-1"
	const post = "/api/agents/ag-1/mcp-servers"

	t.Run("PUT without credentials field keeps existing", func(t *testing.T) {
		rec := mcpReq(r, "PUT", put, `{"name":"srv","url":"https://x.example","enabled":true}`)
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		require.NotNil(t, repo.agentUpdated)
		assert.Nil(t, repo.agentUpdated.Credentials, "agent 级与 ws 级同口径：缺字段=保留")
	})

	t.Run("PUT credentials null clears explicitly", func(t *testing.T) {
		rec := mcpReq(r, "PUT", put, `{"name":"srv","url":"https://x.example","credentials":null}`)
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		require.NotNil(t, repo.agentUpdated)
		require.NotNil(t, repo.agentUpdated.Credentials)
		assert.Empty(t, repo.agentUpdated.Credentials)
	})

	t.Run("PUT credentials new value overwrites", func(t *testing.T) {
		rec := mcpReq(r, "PUT", put, `{"name":"srv","url":"https://x.example","credentials":{"api_key":"sk-agent"}}`)
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		require.NotNil(t, repo.agentUpdated)
		assert.Equal(t, map[string]any{"api_key": "sk-agent"}, repo.agentUpdated.Credentials)
	})

	t.Run("POST create wires CreateAgentMCP not upsert", func(t *testing.T) {
		repo.agentUpdated = nil // 前面子测试留下的痕迹，先清零再断言
		rec := mcpReq(r, "POST", post, `{"name":"fresh","url":"https://z.example"}`)
		require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
		require.NotNil(t, repo.agentCreated)
		assert.Equal(t, "fresh", repo.agentCreated.Name)
		assert.Nil(t, repo.agentUpdated, "POST 必须走 Create 语义（FP-07 P1-4：不再静默覆盖既有条目）")
	})
}

func TestMCPServer_DupName409(t *testing.T) {
	t.Run("agent level POST dup name returns 409 not silent overwrite", func(t *testing.T) {
		r, repo := newMCPRouter()
		repo.dupNameErr = true
		rec := mcpReq(r, "POST", "/api/agents/ag-1/mcp-servers", `{"name":"dup","url":"https://x.example"}`)
		assert.Equal(t, http.StatusConflict, rec.Code, rec.Body.String())
		assert.Contains(t, rec.Body.String(), apierr.Conflict)
		assert.Contains(t, rec.Body.String(), "mcp server name already taken")
		assert.Nil(t, repo.agentCreated, "重名时不落库")
	})

	t.Run("agent level PUT rename collision returns 409", func(t *testing.T) {
		r, repo := newMCPRouter()
		repo.dupNameErr = true
		rec := mcpReq(r, "PUT", "/api/agents/ag-1/mcp-servers/srv-9", `{"name":"dup","url":"https://x.example"}`)
		assert.Equal(t, http.StatusConflict, rec.Code, rec.Body.String())
		assert.Nil(t, repo.agentUpdated)
	})

	t.Run("ws level POST and PUT rename collision return 409", func(t *testing.T) {
		r, repo := newMCPRouter()
		repo.dupNameErr = true
		rec := mcpReq(r, "POST", "/api/workspaces/ws-1/mcp-servers", `{"name":"dup","url":"https://x.example"}`)
		assert.Equal(t, http.StatusConflict, rec.Code, rec.Body.String())
		rec = mcpReq(r, "PUT", "/api/workspaces/ws-1/mcp-servers/srv-9", `{"name":"dup","url":"https://x.example"}`)
		assert.Equal(t, http.StatusConflict, rec.Code, rec.Body.String(), "FP-07 P1-5：PUT 改名撞名也是 409 不是 500")
		assert.Nil(t, repo.wsUpdated)
	})
}

func TestMCPServer_ResponseNeverLeaksCredentials(t *testing.T) {
	r, repo := newMCPRouter()
	repo.wsMCPRows = []orchapp.WorkspaceMCP{{ID: "s1", Name: "leaky", URL: "https://l.example",
		Credentials: map[string]any{"api_key": "sk-plaintext"}, HasCreds: true}}
	repo.agentMCPRows = []orchapp.AgentMCP{{ID: "s2", AgentID: "ag-1", Name: "leaky2", URL: "https://l.example",
		Credentials: map[string]any{"api_key": "sk-plaintext"}, HasCreds: true}}

	rec := mcpReq(r, "GET", "/api/workspaces/ws-1/mcp-servers", "")
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), `"has_credentials":true`)
	assert.NotContains(t, rec.Body.String(), "sk-plaintext", "credentials json:\"-\" 不回传")
	assert.NotContains(t, rec.Body.String(), "api_key")

	rec = mcpReq(r, "GET", "/api/agents/ag-1/mcp-servers", "")
	require.Equal(t, http.StatusOK, rec.Code)
	assert.NotContains(t, rec.Body.String(), "sk-plaintext")

	// 创建/更新响应同样只回 has_credentials。
	repo.dupNameErr = false
	rec = mcpReq(r, "POST", "/api/agents/ag-1/mcp-servers", `{"name":"leaky3","url":"https://l.example","credentials":{"api_key":"sk-created"}}`)
	require.Equal(t, http.StatusCreated, rec.Code)
	assert.NotContains(t, rec.Body.String(), "sk-created")
	assert.Contains(t, rec.Body.String(), `"has_credentials"`)
}

func TestMCPServer_AgentScopedWrites(t *testing.T) {
	r, repo := newMCPRouter()
	// 不存在的 agent → mustOwnAgent 404。
	rec := mcpReq(r, "POST", "/api/agents/ag-missing/mcp-servers", `{"name":"x","url":"https://x.example"}`)
	assert.Equal(t, http.StatusNotFound, rec.Code)
	assert.Nil(t, repo.agentCreated)

	// body 非法 JSON → 400。
	rec = mcpReq(r, "PUT", "/api/agents/ag-1/mcp-servers/srv-1", `{not-json`)
	assert.Equal(t, http.StatusBadRequest, rec.Code)

	// name/url 缺失 → 400 validation。
	rec = mcpReq(r, "PUT", "/api/agents/ag-1/mcp-servers/srv-1", `{"name":"only-name"}`)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

// 防回归哨兵：wire 的 Credentials 必须保持 RawMessage 形状——若改回
// *map[string]any，显式 null 会被解成 nil 指针，与缺字段不可区分，三态
// 塌缩成两态（"保留"会吞掉"显式清空"），此测试先红。
func TestMCPWireCredentialsIsRawMessage(t *testing.T) {
	var w mcpWire
	require.NoError(t, json.Unmarshal([]byte(`{"name":"n","url":"https://x.example"}`), &w))
	assert.Len(t, w.Credentials, 0, "缺字段 → 零长 RawMessage（保留）")
	require.NoError(t, json.Unmarshal([]byte(`{"name":"n","url":"https://x.example","credentials":null}`), &w))
	assert.Equal(t, "null", string(w.Credentials), "显式 null 保持原字节（清空）")

	creds, err := credsWireToModel(nil)
	require.NoError(t, err)
	assert.Nil(t, creds, "缺字段 → nil（保留现值）")
	creds, err = credsWireToModel(json.RawMessage("null"))
	require.NoError(t, err)
	require.NotNil(t, creds)
	assert.Empty(t, creds, "显式 null → 空 map（清空）")
	creds, err = credsWireToModel(json.RawMessage(`{}`))
	require.NoError(t, err)
	require.NotNil(t, creds)
	assert.Empty(t, creds, "{} → 空 map（清空）")
	creds, err = credsWireToModel(json.RawMessage(`{"api_key":"sk-9"}`))
	require.NoError(t, err)
	assert.Equal(t, map[string]any{"api_key": "sk-9"}, creds)

	_, err = credsWireToModel(json.RawMessage(`42`))
	require.Error(t, err, "非对象 credentials → 400")
	_, err = credsWireToModel(json.RawMessage(`["x"]`))
	require.Error(t, err)
	_, err = credsWireToModel(json.RawMessage(`"sk-string"`))
	require.Error(t, err)

	var aw agentMCPWire
	require.NoError(t, json.Unmarshal([]byte(`{"name":"n","url":"https://x.example","credentials":null}`), &aw))
	assert.Equal(t, "null", string(aw.Credentials), "agent 级 wire 同为 RawMessage")
}

// P1-5：REST 写面 transport 枚举闸——"http" 一刀切拒绝，标准值放行。
func TestMCPServer_TransportEnum(t *testing.T) {
	t.Run("ws create accepts canonical enum and blank", func(t *testing.T) {
		r, _ := newMCPRouter()
		for _, tc := range []struct{ name, tr string }{
			{"a", `"streamable_http"`}, {"b", `"sse"`}, {"c", `""`},
		} {
			rec := mcpReq(r, "POST", "/api/workspaces/ws-1/mcp-servers",
				`{"name":"srv-`+tc.name+`","url":"https://x.example","transport":`+tc.tr+`}`)
			require.Equal(t, http.StatusCreated, rec.Code, "transport=%s: %s", tc.tr, rec.Body.String())
		}
	})

	t.Run("ws create rejects http and junk", func(t *testing.T) {
		r, repo := newMCPRouter()
		for _, tr := range []string{"http", "stdio", "HTTP"} {
			rec := mcpReq(r, "POST", "/api/workspaces/ws-1/mcp-servers",
				`{"name":"srv","url":"https://x.example","transport":"`+tr+`"}`)
			require.Equal(t, http.StatusBadRequest, rec.Code, "transport=%q", tr)
			assert.Contains(t, rec.Body.String(), apierr.Validation)
			assert.Contains(t, rec.Body.String(), "transport")
		}
		assert.Nil(t, repo.wsCreated, "非法 transport 不得触达 repo")
	})

	t.Run("ws put rejects http too", func(t *testing.T) {
		r, _ := newMCPRouter()
		rec := mcpReq(r, "PUT", "/api/workspaces/ws-1/mcp-servers/s1",
			`{"name":"srv","url":"https://x.example","transport":"http"}`)
		assert.Equal(t, http.StatusBadRequest, rec.Code)
	})

	t.Run("agent create/update both gated", func(t *testing.T) {
		r, repo := newMCPRouter()
		rec := mcpReq(r, "POST", "/api/agents/ag-1/mcp-servers",
			`{"name":"srv","url":"https://x.example","transport":"http"}`)
		assert.Equal(t, http.StatusBadRequest, rec.Code)
		assert.Nil(t, repo.agentCreated)
		rec = mcpReq(r, "PUT", "/api/agents/ag-1/mcp-servers/s1",
			`{"name":"srv","url":"https://x.example","transport":"carrier-pigeon"}`)
		assert.Equal(t, http.StatusBadRequest, rec.Code)
		assert.Nil(t, repo.agentUpdated)
	})
}

// P1-4：credential_id 幽灵关联堵死——REST Create/Update 携带 credential_id
// 键时必须指向本工作区已存在的凭证；缺字段（=保留现值）不校验。
func TestMCPServer_CredentialIDValidation(t *testing.T) {
	t.Run("ws create with ghost credential_id rejected", func(t *testing.T) {
		r, repo := newMCPRouter()
		rec := mcpReq(r, "POST", "/api/workspaces/ws-1/mcp-servers",
			`{"name":"srv","url":"https://x.example","credentials":{"credential_id":"cred-missing"}}`)
		require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
		assert.Contains(t, rec.Body.String(), "credential_id")
		assert.Nil(t, repo.wsCreated, "幽灵引用不得落库")
	})

	t.Run("ws create with existing credential_id passes", func(t *testing.T) {
		r, repo := newMCPRouter()
		rec := mcpReq(r, "POST", "/api/workspaces/ws-1/mcp-servers",
			`{"name":"srv","url":"https://x.example","credentials":{"credential_id":"cred-ok"}}`)
		require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
		require.NotNil(t, repo.wsCreated)
		assert.Equal(t, "cred-ok", repo.wsCreated.Credentials["credential_id"])
	})

	t.Run("empty credential_id is also a ghost", func(t *testing.T) {
		r, _ := newMCPRouter()
		rec := mcpReq(r, "POST", "/api/workspaces/ws-1/mcp-servers",
			`{"name":"srv","url":"https://x.example","credentials":{"credential_id":""}}`)
		assert.Equal(t, http.StatusBadRequest, rec.Code)
	})

	t.Run("put without credentials field skips validation (keep semantics)", func(t *testing.T) {
		r, repo := newMCPRouter()
		rec := mcpReq(r, "PUT", "/api/workspaces/ws-1/mcp-servers/s1",
			`{"name":"srv","url":"https://x.example"}`)
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		require.NotNil(t, repo.wsUpdated)
		assert.Nil(t, repo.wsUpdated.Credentials)
	})

	t.Run("agent level create gated identically", func(t *testing.T) {
		r, repo := newMCPRouter()
		rec := mcpReq(r, "POST", "/api/agents/ag-1/mcp-servers",
			`{"name":"srv","url":"https://x.example","credentials":{"credential_id":"cred-missing"}}`)
		assert.Equal(t, http.StatusBadRequest, rec.Code)
		assert.Nil(t, repo.agentCreated)
	})
}

// P2-D8：headers 键值校验——键必须是合法 HTTP token，值必须是字符串且不含
// \r \n \0（头值注入拦截；tab 合法不拦）。
func TestMCPServer_HeadersValidation(t *testing.T) {
	bad := map[string]string{
		"space in key":  `{"credentials":{"headers":{"X Bad":"v"}}}`,
		"colon in key":  `{"credentials":{"headers":{"X:Bad":"v"}}}`,
		"cr in value":   `{"credentials":{"headers":{"X-A":"a\rb"}}}`,
		"lf in value":   `{"credentials":{"headers":{"X-A":"a\nb"}}}`,
		"nul in value":  `{"credentials":{"headers":{"X-A":"a\u0000b"}}}`,
		"non-string":    `{"credentials":{"headers":{"X-A":42}}}`,
		"empty key":     `{"credentials":{"headers":{"":"v"}}}`,
	}
	for name, creds := range bad {
		t.Run(name, func(t *testing.T) {
			r, repo := newMCPRouter()
			rec := mcpReq(r, "POST", "/api/workspaces/ws-1/mcp-servers",
				`{"name":"srv","url":"https://x.example",`+creds[1:len(creds)-1]+`}`)
			// creds 形如 {"credentials":{...}}，去掉首尾大括号拼进外层对象。
			require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
			assert.Nil(t, repo.wsCreated)
		})
	}

	t.Run("valid headers with tab value pass", func(t *testing.T) {
		r, _ := newMCPRouter()
		rec := mcpReq(r, "POST", "/api/workspaces/ws-1/mcp-servers",
			`{"name":"srv","url":"https://x.example","credentials":{"headers":{"Authorization":"Bearer sk-x","X-Org":"acme\ttab"}}}`)
		require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
		assert.Contains(t, rec.Body.String(), `"auth_effective":true`)
	})

	t.Run("put gated too", func(t *testing.T) {
		r, _ := newMCPRouter()
		rec := mcpReq(r, "PUT", "/api/workspaces/ws-1/mcp-servers/s1",
			`{"name":"srv","url":"https://x.example","credentials":{"headers":{"X-A":"a\nb"}}}`)
		assert.Equal(t, http.StatusBadRequest, rec.Code)
	})
}

// P1-4：auth_effective（headers 信封非空 = 认证真正生效）随 Create/Update/
// Get/List 响应下发，与 has_credentials 并列。
func TestMCPServer_AuthEffectiveField(t *testing.T) {
	r, repo := newMCPRouter()
	repo.wsMCPRows = []orchapp.WorkspaceMCP{
		{ID: "s-hdr", Name: "with-headers", URL: "https://h.example",
			Credentials: map[string]any{"headers": map[string]any{"Authorization": "Bearer x"}}, HasCreds: true},
		{ID: "s-key", Name: "api-key-only", URL: "https://k.example",
			Credentials: map[string]any{"api_key": "sk"}, HasCreds: true},
		{ID: "s-none", Name: "no-creds", URL: "https://n.example"},
	}
	repo.agentMCPRows = []orchapp.AgentMCP{
		{ID: "a-hdr", AgentID: "ag-1", Name: "ag-with-headers", URL: "https://h.example",
			Credentials: map[string]any{"headers": map[string]any{"X-Org": "acme"}}, HasCreds: true},
		{ID: "a-none", AgentID: "ag-1", Name: "ag-no-creds", URL: "https://n.example"},
	}

	t.Run("ws list marks only headers envelopes effective", func(t *testing.T) {
		rec := mcpReq(r, "GET", "/api/workspaces/ws-1/mcp-servers", "")
		require.Equal(t, http.StatusOK, rec.Code)
		assert.NotContains(t, rec.Body.String(), "Bearer x", "凭证值不回传")
		var out struct {
			Servers []struct {
				Name          string `json:"name"`
				HasCreds      bool   `json:"has_credentials"`
				AuthEffective bool   `json:"auth_effective"`
			} `json:"servers"`
		}
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out))
		require.Len(t, out.Servers, 3)
		for _, s := range out.Servers {
			switch s.Name {
			case "with-headers":
				assert.True(t, s.HasCreds)
				assert.True(t, s.AuthEffective, "headers 信封非空 = 认证生效")
			case "api-key-only":
				assert.True(t, s.HasCreds)
				assert.False(t, s.AuthEffective, "api_key 顶层键：引擎不消费，不生效")
			case "no-creds":
				assert.False(t, s.HasCreds)
				assert.False(t, s.AuthEffective)
			default:
				t.Fatalf("unexpected row %q", s.Name)
			}
		}
	})

	t.Run("agent list carries the field too", func(t *testing.T) {
		rec := mcpReq(r, "GET", "/api/agents/ag-1/mcp-servers", "")
		require.Equal(t, http.StatusOK, rec.Code)
		assert.NotContains(t, rec.Body.String(), "acme", "headers 值不回传")
		var out struct {
			Servers []struct {
				Name          string `json:"name"`
				AuthEffective bool   `json:"auth_effective"`
			} `json:"servers"`
		}
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out))
		require.Len(t, out.Servers, 2)
		assert.True(t, out.Servers[0].AuthEffective, "ag-with-headers")
		assert.False(t, out.Servers[1].AuthEffective, "ag-no-creds")
	})

	t.Run("create response carries the field", func(t *testing.T) {
		rec := mcpReq(r, "POST", "/api/workspaces/ws-1/mcp-servers",
			`{"name":"fresh","url":"https://f.example","credentials":{"headers":{"Authorization":"Bearer sk"}}}`)
		require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
		assert.Contains(t, rec.Body.String(), `"auth_effective":true`)
		assert.NotContains(t, rec.Body.String(), "sk", "凭证值不回传")

		rec = mcpReq(r, "POST", "/api/agents/ag-1/mcp-servers",
			`{"name":"fresh-ag","url":"https://f.example","credentials":{"credential_id":"cred-ok"}}`)
		require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
		assert.Contains(t, rec.Body.String(), `"has_credentials":true,"auth_effective":false`,
			"credential_id-only：有凭证但不生效")
	})

	t.Run("update response carries the field", func(t *testing.T) {
		rec := mcpReq(r, "PUT", "/api/workspaces/ws-1/mcp-servers/s1",
			`{"name":"srv","url":"https://x.example","credentials":{"headers":{"X-A":"1"}}}`)
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		assert.Contains(t, rec.Body.String(), `"auth_effective":true`)
	})
}

// P0-5 + P1-3：probe 响应补 total（截断前总数）；agent 级 probe 路由。
func TestMCPServer_ProbeToolsTotalAndAgentRoute(t *testing.T) {
	t.Run("ws probe returns total from counted wiring", func(t *testing.T) {
		r, repo, svc := newMCPRouterRole("owner")
		repo.wsMCPRows = []orchapp.WorkspaceMCP{{ID: "srv-1", Name: "w", URL: "https://x.example"}}
		svc.ToolProbeCounted = func(_ context.Context, _ orchapp.ResolvedMCPServer, _ string) ([]orchapp.ProbeTool, int, error) {
			return make([]orchapp.ProbeTool, 32), 40, nil
		}
		rec := mcpReq(r, "POST", "/api/workspaces/ws-1/mcp-servers/srv-1/probe-tools", "")
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		assert.Contains(t, rec.Body.String(), `"total":40`, "截断前总数")
		var out struct {
			Tools []orchapp.ProbeTool `json:"tools"`
			Total int                 `json:"total"`
		}
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out))
		assert.Len(t, out.Tools, 32)
		assert.Equal(t, 40, out.Total)
	})

	t.Run("ws probe falls back to len(tools) when only ToolProbe wired", func(t *testing.T) {
		r, repo, svc := newMCPRouterRole("owner")
		repo.wsMCPRows = []orchapp.WorkspaceMCP{{ID: "srv-1", Name: "w", URL: "https://x.example"}}
		svc.ToolProbe = func(_ context.Context, _ orchapp.ResolvedMCPServer, _ string) ([]orchapp.ProbeTool, error) {
			return []orchapp.ProbeTool{{Name: "t1"}, {Name: "t2"}}, nil
		}
		rec := mcpReq(r, "POST", "/api/workspaces/ws-1/mcp-servers/srv-1/probe-tools", "")
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		assert.Contains(t, rec.Body.String(), `"total":2`)
	})

	t.Run("agent probe route probes the agent-level entry", func(t *testing.T) {
		r, repo, svc := newMCPRouterRole("owner")
		repo.agentMCPRows = []orchapp.AgentMCP{{ID: "ag-srv-1", AgentID: "ag-1", Name: "agent-srv",
			Transport: "sse", URL: "https://a.example",
			Credentials: map[string]any{"headers": map[string]any{"X-A": "1"}}}}
		var got orchapp.ResolvedMCPServer
		svc.ToolProbe = func(_ context.Context, srv orchapp.ResolvedMCPServer, ws string) ([]orchapp.ProbeTool, error) {
			got = srv
			assert.Equal(t, "ws-1", ws)
			return []orchapp.ProbeTool{{Name: "at1"}}, nil
		}
		rec := mcpReq(r, "POST", "/api/agents/ag-1/mcp-servers/ag-srv-1/probe-tools", "")
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		assert.Contains(t, rec.Body.String(), "at1")
		assert.Contains(t, rec.Body.String(), `"total":1`)
		assert.Equal(t, "agent-srv", got.Name)
		assert.Equal(t, map[string]any{"headers": map[string]any{"X-A": "1"}}, got.Credentials,
			"agent 级条目凭证进探测链路")
	})

	t.Run("agent probe 404s on missing entry or agent", func(t *testing.T) {
		r, repo, svc := newMCPRouterRole("owner")
		repo.agentMCPRows = nil
		svc.ToolProbe = func(_ context.Context, _ orchapp.ResolvedMCPServer, _ string) ([]orchapp.ProbeTool, error) {
			return nil, nil
		}
		rec := mcpReq(r, "POST", "/api/agents/ag-1/mcp-servers/ghost/probe-tools", "")
		assert.Equal(t, http.StatusNotFound, rec.Code)
		rec = mcpReq(r, "POST", "/api/agents/ag-missing/mcp-servers/ghost/probe-tools", "")
		assert.Equal(t, http.StatusNotFound, rec.Code)
	})
}
