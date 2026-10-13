package http

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/haozing/ploykit/platform/webx"
	"orkome/internal/skills/app"
)

type crudFakeRepo struct {
	app.Repo
	skills map[string]app.Skill
	byName map[string]string
	files  map[string][]app.SkillFile
}

func (f *crudFakeRepo) InsertSkill(_ context.Context, s app.Skill) (app.Skill, error) {
	if s.ID == "" {
		s.ID = "sk-" + s.Name
	}
	s.CreatedAt = time.Now()
	f.skills[s.ID] = s
	f.byName[s.WorkspaceID+"/"+s.Name] = s.ID
	return s, nil
}

func (f *crudFakeRepo) GetSkill(_ context.Context, ws, id string) (app.Skill, bool, error) {
	s, ok := f.skills[id]
	return s, ok && s.WorkspaceID == ws, nil
}

func (f *crudFakeRepo) GetSkillByName(_ context.Context, ws, name string) (app.Skill, bool, error) {
	id, ok := f.byName[ws+"/"+name]
	if !ok {
		return app.Skill{}, false, nil
	}
	return f.skills[id], true, nil
}

func (f *crudFakeRepo) UpdateSkill(_ context.Context, ws, id string, name, description *string) (app.Skill, bool, error) {
	s, ok := f.skills[id]
	if !ok || s.WorkspaceID != ws {
		return app.Skill{}, false, nil
	}
	if name != nil {
		delete(f.byName, s.WorkspaceID+"/"+s.Name)
		s.Name = *name
		f.byName[s.WorkspaceID+"/"+s.Name] = s.ID
	}
	if description != nil {
		s.Description = *description
	}
	f.skills[id] = s
	return s, true, nil
}

func (f *crudFakeRepo) UpsertFile(_ context.Context, skillID string, nf app.SkillFile) error {
	files := f.files[skillID]
	for i, x := range files {
		if x.Path == nf.Path {
			nf.ID = x.ID
			files[i] = nf
			f.files[skillID] = files
			return nil
		}
	}
	nf.ID = "f-" + nf.Path
	f.files[skillID] = append(files, nf)
	return nil
}

func (f *crudFakeRepo) GetFile(_ context.Context, ws, skillID, fileID string) (app.SkillFile, bool, error) {
	if s, ok := f.skills[skillID]; !ok || s.WorkspaceID != ws {
		return app.SkillFile{}, false, nil
	}
	for _, x := range f.files[skillID] {
		if x.ID == fileID {
			return x, true, nil
		}
	}
	return app.SkillFile{}, false, nil
}

func (f *crudFakeRepo) DeleteFile(_ context.Context, ws, skillID, fileID string) error {
	if s, ok := f.skills[skillID]; !ok || s.WorkspaceID != ws {
		return nil
	}
	files := f.files[skillID]
	for i, x := range files {
		if x.ID == fileID {
			f.files[skillID] = append(files[:i:i], files[i+1:]...)
			return nil
		}
	}
	return nil
}

func (f *crudFakeRepo) ListFiles(_ context.Context, ws, skillID string) ([]app.SkillFile, error) {
	if s, ok := f.skills[skillID]; !ok || s.WorkspaceID != ws {
		return nil, nil
	}
	return f.files[skillID], nil
}

func newCRUDRouter(t *testing.T) (*http.ServeMux, *crudFakeRepo) {
	t.Helper()
	repo := &crudFakeRepo{
		skills: map[string]app.Skill{}, byName: map[string]string{}, files: map[string][]app.SkillFile{},
	}
	mk := func(id, ws, name string) app.Skill {
		s := app.Skill{ID: id, WorkspaceID: ws, Name: name}
		repo.skills[id] = s
		repo.byName[ws+"/"+name] = id
		return s
	}
	mk("sk-1", "ws-1", "demo")
	mk("sk-2", "ws-1", "taken")
	add := func(skillID, id, path, content string) {
		sum := sha256.Sum256([]byte(content))
		repo.files[skillID] = append(repo.files[skillID], app.SkillFile{
			ID: id, Path: path, Content: content,
			SHA256: hex.EncodeToString(sum[:]), Size: len(content)})
	}
	add("sk-1", "f-main", "SKILL.md", "---\nname: demo\n---\n# body")
	add("sk-1", "f-ref", "refs/x.md", "ref")

	r := http.NewServeMux()
	Mount(r, Deps{
		Skills:      app.NewSkillService(repo),
		MemberCheck: func(context.Context, string, string) (bool, error) { return true, nil },
	}, func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, r.WithContext(webx.WithPrincipal(r.Context(),
				&webx.Principal{UserID: "u-1", Source: webx.SourceSession})))
		})
	})
	return r, repo
}

func crudReq(r http.Handler, method, target, body string) *httptest.ResponseRecorder {
	var req *http.Request
	if body == "" {
		req = httptest.NewRequest(method, "/api"+target, nil)
	} else {
		req = httptest.NewRequest(method, "/api"+target, strings.NewReader(body))
	}
	req.Header.Set("X-Workspace-Id", "ws-1")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

// newCRUDRouterAs 同 newCRUDRouter，但 auth 注入的 principal 带 Role
// （模拟 owner/admin PAT——origin=pack 放行的鉴权路径测试入口）。
func newCRUDRouterAs(t *testing.T, role string) (*http.ServeMux, *crudFakeRepo) {
	t.Helper()
	repo := &crudFakeRepo{
		skills: map[string]app.Skill{}, byName: map[string]string{}, files: map[string][]app.SkillFile{},
	}
	r := http.NewServeMux()
	Mount(r, Deps{
		Skills:      app.NewSkillService(repo),
		MemberCheck: func(context.Context, string, string) (bool, error) { return true, nil },
	}, func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, r.WithContext(webx.WithPrincipal(r.Context(),
				&webx.Principal{UserID: "u-1", Source: webx.SourcePAT, Role: role})))
		})
	})
	return r, repo
}

func TestSkillCreateOriginPassthrough(t *testing.T) {
	r, _ := newCRUDRouter(t)

	// 非 admin（无 Role）显式请求 origin=pack → 强制 manual + 清 origin_ref（防伪造）。
	rec := crudReq(r, "POST", "/skills",
		`{"name":"forge-1","description":"d","origin":"pack","origin_ref":"pk-evil"}`)
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	var sk app.Skill
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &sk))
	assert.Equal(t, "manual", sk.Origin, "non-admin cannot forge origin=pack")
	assert.Empty(t, sk.OriginRef, "origin_ref cleared for non-admin")

	// 非 admin 不带 origin → manual（现状不变）。
	require.Equal(t, http.StatusCreated, crudReq(r, "POST", "/skills", `{"name":"forge-2"}`).Code)

	// admin 显式 origin=pack → 保留 pack + origin_ref 取 body 值（provision 通道）。
	admin, adminRepo := newCRUDRouterAs(t, "admin")
	rec = crudReq(admin, "POST", "/skills",
		`{"name":"forge-3","description":"d","origin":"pack","origin_ref":"pk-123"}`)
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	var packSk app.Skill
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &packSk))
	assert.Equal(t, "pack", packSk.Origin, "admin passthrough for provision")
	assert.Equal(t, "pk-123", packSk.OriginRef)

	// admin 但 origin 非 pack（如伪造 origin=import）→ 仍强制 manual。
	rec = crudReq(admin, "POST", "/skills", `{"name":"forge-4","origin":"import","origin_ref":"https://x"}`)
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &sk))
	assert.Equal(t, "manual", sk.Origin, "only origin=pack is passable, others forced manual")
	assert.Empty(t, sk.OriginRef)

	// 落库视图一致（admin fake repo 按 ID 回读 forge-3 的行）。
	stored, ok := adminRepo.skills[packSk.ID]
	require.True(t, ok, "created skill stored under returned ID")
	assert.Equal(t, "pack", stored.Origin)
	assert.Equal(t, "pk-123", stored.OriginRef)
}

func TestSkillUpdateEndpoint(t *testing.T) {
	r, _ := newCRUDRouter(t)

	rec := crudReq(r, "PUT", "/skills/sk-1", `{"name":"renamed","description":"d"}`)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var sk app.Skill
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &sk))
	assert.Equal(t, "renamed", sk.Name)
	assert.Equal(t, "d", sk.Description)

	assert.Equal(t, http.StatusConflict, crudReq(r, "PUT", "/skills/sk-1", `{"name":"taken"}`).Code,
		"other skill with the same name 409")
	assert.Equal(t, http.StatusBadRequest, crudReq(r, "PUT", "/skills/sk-1", `{"name":""}`).Code)
	assert.Equal(t, http.StatusBadRequest, crudReq(r, "PUT", "/skills/sk-1", `{`).Code, "invalid JSON")
	assert.Equal(t, http.StatusNotFound, crudReq(r, "PUT", "/skills/sk-404", `{"name":"x"}`).Code)
}

func TestSkillUpsertFileEndpoint(t *testing.T) {
	r, _ := newCRUDRouter(t)

	rec := crudReq(r, "PUT", "/skills/sk-1/files", `{"path":"refs/new.md","content":"hello"}`)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var out struct {
		File           app.SkillFile `json:"file"`
		ManifestSHA256 string        `json:"manifest_sha256"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out))
	assert.Equal(t, "refs/new.md", out.File.Path)
	assert.Equal(t, "f-refs/new.md", out.File.ID, "row ID echoed back (the delete endpoint's addressing key)")
	assert.Len(t, out.ManifestSHA256, 64, "response carries the recomputed tree digest")

	assert.Equal(t, http.StatusBadRequest, crudReq(r, "PUT", "/skills/sk-1/files",
		`{"path":"../evil.md","content":"x"}`).Code, "path escape rejected")
	assert.Equal(t, http.StatusBadRequest, crudReq(r, "PUT", "/skills/sk-1/files",
		`{"path":"a.png","content":"x"}`).Code, "binary blacklist rejected")
	assert.Equal(t, http.StatusBadRequest, crudReq(r, "PUT", "/skills/sk-1/files",
		`{"path":"SKILL.md","content":"no frontmatter"}`).Code, "main file missing frontmatter rejected")
	assert.Equal(t, http.StatusBadRequest, crudReq(r, "PUT", "/skills/sk-1/files",
		`{"content":"x"}`).Code, "missing path rejected")
	assert.Equal(t, http.StatusNotFound, crudReq(r, "PUT", "/skills/sk-404/files",
		`{"path":"a.md","content":"x"}`).Code)
}

func TestSkillDeleteFileEndpoint(t *testing.T) {
	r, _ := newCRUDRouter(t)

	rec := crudReq(r, "DELETE", "/skills/sk-1/files/f-ref", "")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var out struct {
		Result         string `json:"result"`
		ManifestSHA256 string `json:"manifest_sha256"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out))
	assert.Equal(t, "deleted", out.Result)
	assert.Len(t, out.ManifestSHA256, 64)

	assert.Equal(t, http.StatusBadRequest, crudReq(r, "DELETE", "/skills/sk-1/files/f-main", "").Code,
		"main file deletion rejected")
	assert.Equal(t, http.StatusNotFound, crudReq(r, "DELETE", "/skills/sk-1/files/f-none", "").Code)
	assert.Equal(t, http.StatusNotFound, crudReq(r, "DELETE", "/skills/sk-404/files/f-main", "").Code)
}
