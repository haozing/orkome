package http

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/haozing/ploykit/platform/webx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	orchapp "orkome/internal/orchestration/app"
)

// C2 契约测试：GET /api/ui-entries 响应 items[].slots 随行返回（一个端点两用），
// PUT 请求体可选 slots；slot 目录冻结（workitem.detail.tab only）在 service 层生效。

type uiEntryFakeRepo struct {
	orchapp.PackUIEntryRepo // 未用方法的兜底（接口断言用 nil 不行，嵌接口即可）

	stored []orchapp.PackUIEntry
	ws     string
}

func (f *uiEntryFakeRepo) UpsertPackUIEntry(_ context.Context, e orchapp.PackUIEntry) error {
	for i := range f.stored {
		if f.stored[i].Pack == e.Pack {
			f.stored[i] = e
			return nil
		}
	}
	f.stored = append(f.stored, e)
	return nil
}

func (f *uiEntryFakeRepo) ListPackUIEntries(_ context.Context, ws string) ([]orchapp.PackUIEntry, error) {
	f.ws = ws
	return f.stored, nil
}

func (f *uiEntryFakeRepo) DeletePackUIEntry(_ context.Context, ws, pack string) error {
	f.ws = ws
	out := f.stored[:0]
	for _, e := range f.stored {
		if e.Pack != pack {
			out = append(out, e)
		}
	}
	f.stored = out
	return nil
}

func newUIEntryRouter() (http.Handler, *uiEntryFakeRepo) {
	repo := &uiEntryFakeRepo{}
	d := Deps{
		PackUIEntries: orchapp.NewPackUIEntryService(repo),
		MemberCheck:   func(context.Context, string, string) (bool, error) { return true, nil },
	}
	r := http.NewServeMux()
	// ui-entries 的 PUT/DELETE 走 requireWorkspaceAdmin——测试自带 admin principal。
	admin := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			r2 := r.WithContext(webx.WithPrincipal(r.Context(),
				&webx.Principal{UserID: "uientry-admin", Role: "admin", Source: webx.SourceSession}))
			r2.Header.Set("X-Workspace-Id", "ws-1")
			next.ServeHTTP(w, r2)
		})
	}
	Mount(r, d, admin)
	return r, repo
}

func uiEntryReq(r http.Handler, method, target, body string) *httptest.ResponseRecorder {
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

func TestUIEntrySlotsContract(t *testing.T) {
	r, repo := newUIEntryRouter()

	t.Run("PUT with slots stores and GET returns slots inline", func(t *testing.T) {
		rec := uiEntryReq(r, "PUT", "/api/ui-entries/agentblog",
			`{"pack":"agentblog","pack_id":"dc217df4-2851-480f-b3c9-b5cc039e40cc","label":"内容工厂","url_template":"https://ab.example/console",
			  "slots":[{"slot":"workitem.detail.tab","label":"文章预览",
			            "url_template":"https://ab.example/slot?item={workItemId}","height":480}]}`)
		require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())

		rec = uiEntryReq(r, "GET", "/api/ui-entries", "")
		require.Equal(t, http.StatusOK, rec.Code)
		body := rec.Body.String()
		assert.Contains(t, body, `"pack":"agentblog"`)
		assert.Contains(t, body, `"slots":[{`)
		assert.Contains(t, body, `"slot":"workitem.detail.tab"`)
		assert.Contains(t, body, `"height":480`)
		assert.Equal(t, "ws-1", repo.ws, "workspace 从 X-Workspace-Id 头取")
	})

	t.Run("PUT without slots then GET yields empty slots array not null", func(t *testing.T) {
		rec := uiEntryReq(r, "PUT", "/api/ui-entries/plainpack",
			`{"pack":"plainpack","pack_id":"dc217df4-2851-480f-b3c9-b5cc039e40cc","label":"P","url_template":"https://p.example"}`)
		require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
		// fake repo 不会做 COALESCE（那是 pg repo 的职责），模拟 repo 层语义：
		repo.stored[len(repo.stored)-1].Slots = []orchapp.PackUISlot{}

		rec = uiEntryReq(r, "GET", "/api/ui-entries", "")
		require.Equal(t, http.StatusOK, rec.Code)
		body := rec.Body.String()
		assert.Contains(t, body, `"pack":"plainpack"`)
		assert.Contains(t, body, `"slots":[]`, "无 slots 的 pack 序列化为空数组")
		assert.NotContains(t, body, `"slots":null`)
	})

	t.Run("PUT with unknown slot identifier rejected (catalog frozen)", func(t *testing.T) {
		rec := uiEntryReq(r, "PUT", "/api/ui-entries/badpack",
			`{"pack":"badpack","pack_id":"dc217df4-2851-480f-b3c9-b5cc039e40cc","label":"B","url_template":"https://b.example",
			  "slots":[{"slot":"workspace.dashboard.card","label":"卡片",
			            "url_template":"https://b.example/card","height":300}]}`)
		assert.Equal(t, http.StatusBadRequest, rec.Code)
		assert.Contains(t, rec.Body.String(), "workitem.detail.tab")
	})

	t.Run("PUT with slot missing height rejected", func(t *testing.T) {
		rec := uiEntryReq(r, "PUT", "/api/ui-entries/noheight",
			`{"pack":"noheight","pack_id":"dc217df4-2851-480f-b3c9-b5cc039e40cc","label":"N","url_template":"https://n.example",
			  "slots":[{"slot":"workitem.detail.tab","label":"x",
			            "url_template":"https://n.example/s"}]}`)
		assert.Equal(t, http.StatusBadRequest, rec.Code)
		assert.Contains(t, rec.Body.String(), "height")
	})

	// FP-15 P1-3：url_template 必须是绝对 http(s) URL——相对路径渲染成同源
	// iframe（sandbox 的 allow-scripts+allow-same-origin 对同源内容形同虚设），
	// javascript: 模板在 iframe src 继承父源执行。独立 router，不污染上方计数断言。
	t.Run("slot url_template scheme matrix", func(t *testing.T) {
		r, _ := newUIEntryRouter()
		for _, tc := range []struct {
			name, template string
			want           int
		}{
			{"https accepted", "https://ab.example/slot?item={workItemId}", http.StatusCreated},
			{"http accepted", "http://ab.example/slot", http.StatusCreated},
			{"javascript rejected", `javascript:alert(1)`, http.StatusBadRequest},
			{"data rejected", "data:text/html,<script>alert(1)</script>", http.StatusBadRequest},
			{"relative rejected", "/packs/github?wi={workItemId}", http.StatusBadRequest},
			{"protocol-relative rejected", "//evil.example/slot", http.StatusBadRequest},
			{"ftp rejected", "ftp://files.example/slot", http.StatusBadRequest},
		} {
			t.Run(tc.name, func(t *testing.T) {
				pack := "scheme-" + strings.ReplaceAll(tc.name, " ", "-")
				body := `{"pack":"` + pack + `","pack_id":"dc217df4-2851-480f-b3c9-b5cc039e40cc","label":"S","url_template":"https://s.example",
				  "slots":[{"slot":"workitem.detail.tab","label":"x",
				            "url_template":"` + strings.ReplaceAll(tc.template, `"`, `\"`) + `","height":480}]}`
				rec := uiEntryReq(r, "PUT", "/api/ui-entries/"+pack, body)
				assert.Equal(t, tc.want, rec.Code, tc.template+" → "+rec.Body.String())
				if tc.want == http.StatusBadRequest {
					assert.Contains(t, rec.Body.String(), "http(s)")
				}
			})
		}
	})

	t.Run("entry url_template scheme matrix", func(t *testing.T) {
		r, _ := newUIEntryRouter()
		for _, tc := range []struct {
			name, template string
			want           int
		}{
			{"https entry accepted", "https://e.example/console", http.StatusCreated},
			{"javascript entry rejected", `javascript:alert(1)`, http.StatusBadRequest},
			{"relative entry rejected", "/packs/x", http.StatusBadRequest},
		} {
			t.Run(tc.name, func(t *testing.T) {
				pack := "entry-" + strings.ReplaceAll(tc.name, " ", "-")
				body := `{"pack":"` + pack + `","pack_id":"dc217df4-2851-480f-b3c9-b5cc039e40cc","label":"E","url_template":"` +
					strings.ReplaceAll(tc.template, `"`, `\"`) + `"}`
				rec := uiEntryReq(r, "PUT", "/api/ui-entries/"+pack, body)
				assert.Equal(t, tc.want, rec.Code, tc.template+" → "+rec.Body.String())
			})
		}
	})

	// 集成页终态 W1 §2.3：pack_id 空 = 手动入口（合法路径，pack 名即内部键）；
	// ws/pack/label/url_template 必填仍不放行。
	t.Run("PUT without pack_id accepted as manual entry, required fields still enforced", func(t *testing.T) {
		rec := uiEntryReq(r, "PUT", "/api/ui-entries/custom-side",
			`{"pack":"custom-side","label":"侧栏入口","url_template":"https://l.example"}`)
		require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
		last := repo.stored[len(repo.stored)-1]
		require.Equal(t, "custom-side", last.Pack)
		assert.Empty(t, last.PackID, "手动入口不伪造 pack_id")

		rec = uiEntryReq(r, "GET", "/api/ui-entries", "")
		require.Equal(t, http.StatusOK, rec.Code)
		assert.Contains(t, rec.Body.String(), `"pack":"custom-side"`)

		// 必填项矩阵：pack / label / url_template 任一缺失 → 400。
		for _, body := range []string{
			`{"label":"L","url_template":"https://l.example"}`,
			`{"pack":"manual-no-label","url_template":"https://l.example"}`,
			`{"pack":"manual-no-url","label":"L"}`,
		} {
			rec := uiEntryReq(r, "PUT", "/api/ui-entries/x", body)
			assert.Equal(t, http.StatusBadRequest, rec.Code, body+" → "+rec.Body.String())
		}
		// agentblog + plainpack（前两个子测试）+ custom-side；被拒请求未入库。
		require.Len(t, repo.stored, 3)
	})
}
