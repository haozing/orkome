package app

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"orkome/internal/contract/apierr"
	"orkome/internal/skills/domain"
)

type fakeSkillsRepo struct {
	skills   map[string]Skill
	byName   map[string]string
	files    map[string][]SkillFile
	inserted []Skill
	replaced [][]SkillFile

	// FP-08 P1-4 绑定面：记录归属与写调用
	agentsInWs   map[string]bool
	bindingCalls []string
}

func newFakeSkillsRepo() *fakeSkillsRepo {
	return &fakeSkillsRepo{skills: map[string]Skill{}, byName: map[string]string{},
		files: map[string][]SkillFile{}, agentsInWs: map[string]bool{}}
}

func (f *fakeSkillsRepo) InsertSkill(_ context.Context, s Skill) (Skill, error) {
	if id, ok := f.byName[s.WorkspaceID+"/"+s.Name]; ok {
		old := f.skills[id]
		old.Description, old.Origin, old.OriginRef = s.Description, s.Origin, s.OriginRef
		f.skills[id] = old
		return old, nil
	}
	s.ID = fmt.Sprintf("sk-%d", len(f.skills)+1)
	s.CreatedAt = time.Now()
	f.skills[s.ID] = s
	f.byName[s.WorkspaceID+"/"+s.Name] = s.ID
	f.inserted = append(f.inserted, s)
	return s, nil
}
func (f *fakeSkillsRepo) GetSkill(_ context.Context, ws, id string) (Skill, bool, error) {
	s, ok := f.skills[id]
	return s, ok && s.WorkspaceID == ws, nil
}
func (f *fakeSkillsRepo) ListSkills(_ context.Context, ws string) ([]Skill, error) { return nil, nil }
func (f *fakeSkillsRepo) SearchSkills(_ context.Context, ws, q string, limit int) ([]Skill, error) {
	return nil, nil
}
func (f *fakeSkillsRepo) DeleteSkill(_ context.Context, ws, id string) error { return nil }
func (f *fakeSkillsRepo) UpdateSkill(_ context.Context, ws, id string, name, description *string) (Skill, bool, error) {
	s, ok := f.skills[id]
	if !ok || s.WorkspaceID != ws {
		return Skill{}, false, nil
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
func (f *fakeSkillsRepo) ReplaceFiles(_ context.Context, skillID string, files []SkillFile) error {
	cp := append([]SkillFile(nil), files...)
	for i := range cp {
		cp[i].ID = "f-" + cp[i].Path
	}
	f.files[skillID] = cp
	f.replaced = append(f.replaced, files)
	return nil
}
func (f *fakeSkillsRepo) UpsertFile(_ context.Context, skillID string, nf SkillFile) error {
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
func (f *fakeSkillsRepo) GetFile(_ context.Context, ws, skillID, fileID string) (SkillFile, bool, error) {
	if s, ok := f.skills[skillID]; !ok || s.WorkspaceID != ws {
		return SkillFile{}, false, nil
	}
	for _, x := range f.files[skillID] {
		if x.ID == fileID {
			return x, true, nil
		}
	}
	return SkillFile{}, false, nil
}
func (f *fakeSkillsRepo) DeleteFile(_ context.Context, ws, skillID, fileID string) error {
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
func (f *fakeSkillsRepo) ListFiles(_ context.Context, ws, skillID string) ([]SkillFile, error) {
	return f.files[skillID], nil
}
func (f *fakeSkillsRepo) GetSkillByName(_ context.Context, ws, name string) (Skill, bool, error) {
	id, ok := f.byName[ws+"/"+name]
	if !ok {
		return Skill{}, false, nil
	}
	return f.skills[id], true, nil
}
func (f *fakeSkillsRepo) AgentInWorkspace(_ context.Context, ws, agentID string) (bool, error) {
	return f.agentsInWs[ws+"/"+agentID], nil
}
func (f *fakeSkillsRepo) SetAgentSkills(_ context.Context, ws, agentID string, ids []string, en bool) error {
	f.bindingCalls = append(f.bindingCalls, "set:"+ws+"/"+agentID)
	return nil
}
func (f *fakeSkillsRepo) AddAgentSkill(_ context.Context, ws, agentID, skillID string) error {
	f.bindingCalls = append(f.bindingCalls, "add:"+ws+"/"+agentID+"->"+skillID)
	return nil
}
func (f *fakeSkillsRepo) SetAgentSkillEnabled(_ context.Context, ws, agentID, skillID string, en bool) error {
	f.bindingCalls = append(f.bindingCalls, "enable:"+ws+"/"+agentID+"->"+skillID)
	return nil
}
func (f *fakeSkillsRepo) RemoveAgentSkill(_ context.Context, ws, agentID, skillID string) error {
	f.bindingCalls = append(f.bindingCalls, "remove:"+ws+"/"+agentID+"->"+skillID)
	return nil
}
func (f *fakeSkillsRepo) ListAgentSkills(_ context.Context, ws, agentID string) ([]AgentSkill, error) {
	return nil, nil
}
func (f *fakeSkillsRepo) ListSkillAgents(_ context.Context, ws, skillID string) ([]SkillAgent, error) {
	return nil, nil
}

func zipBytes(t *testing.T, entries map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, content := range entries {
		fw, err := zw.Create(name)
		require.NoError(t, err)
		_, err = fw.Write([]byte(content))
		require.NoError(t, err)
	}
	require.NoError(t, zw.Close())
	return buf.Bytes()
}

func TestImportZipSanitizes(t *testing.T) {
	repo := newFakeSkillsRepo()
	imp := NewImportService(repo)

	sk, err := imp.ImportZip(context.Background(), "ws", "u1", zipBytes(t, map[string]string{
		"demo/SKILL.md":  "---\nname: de\x00mo\ndescription: has\xffinvalid\n---\n# body\x00line",
		"demo/refs/a.md": "ok",
	}), "demo.zip")
	require.NoError(t, err)

	files := repo.files[sk.ID]
	require.Len(t, files, 2)
	for _, f := range files {
		assert.NotContains(t, f.Content, "\x00", "content NUL stripped: "+f.Path)
		assert.True(t, utf8.ValidString(f.Content), "content is valid UTF-8: "+f.Path)
		sum := sha256.Sum256([]byte(f.Content))
		assert.Equal(t, hex.EncodeToString(sum[:]), f.SHA256, "catalog row = sanitized content hash")
		assert.Equal(t, len(f.Content), f.Size)
	}
	assert.Equal(t, "demo", sk.Name, "frontmatter name sanitized (NUL stripped)")
	assert.Equal(t, "has\ufffdinvalid", sk.Description)
}

func TestFilesFailClosedOnTamper(t *testing.T) {
	repo := newFakeSkillsRepo()
	svc := NewSkillService(repo)
	imp := NewImportService(repo)
	sk, err := imp.ImportZip(context.Background(), "ws", "u1", zipBytes(t, map[string]string{
		"demo/SKILL.md": "# clean body",
	}), "demo.zip")
	require.NoError(t, err)

	files, manifest, err := svc.Files(context.Background(), "ws", sk.ID)
	require.NoError(t, err)
	require.Len(t, files, 1)
	assert.Len(t, manifest, 64)
	want := domain.ManifestDigest([]domain.ManifestFile{
		{Path: files[0].Path, Content: files[0].Content, SHA256: files[0].SHA256, Size: files[0].Size},
	})
	assert.Equal(t, want, manifest, "import side and read side share the tree digest algorithm")

	files[0].Content = "# Clean body"
	repo.files[sk.ID] = files
	_, _, err = svc.Files(context.Background(), "ws", sk.ID)
	require.ErrorIs(t, err, domain.ErrManifestMismatch)

	_, err = svc.MainByName(context.Background(), "ws", "demo")
	require.ErrorIs(t, err, domain.ErrManifestMismatch)
}

func TestMainByName(t *testing.T) {
	repo := newFakeSkillsRepo()
	svc := NewSkillService(repo)
	imp := NewImportService(repo)
	_, err := imp.ImportZip(context.Background(), "ws", "u1", zipBytes(t, map[string]string{
		"demo/SKILL.md":  "---\nname: demo\n---\n# real content",
		"demo/refs/x.md": "ref",
	}), "demo.zip")
	require.NoError(t, err)

	content, err := svc.MainByName(context.Background(), "ws", "demo")
	require.NoError(t, err)
	assert.Contains(t, content, "# real content")

	_, err = svc.MainByName(context.Background(), "ws", "missing")
	require.Error(t, err)
}

func TestCreateSanitizes(t *testing.T) {
	repo := newFakeSkillsRepo()
	svc := NewSkillService(repo)
	sk, err := svc.Create(context.Background(), Skill{
		WorkspaceID: "ws", Name: "na\x00me", Description: "desc\xff",
	})
	require.NoError(t, err)
	assert.Equal(t, "name", sk.Name)
	assert.Equal(t, "desc\ufffd", sk.Description)
}

func apierrCode(t *testing.T, err error) string {
	t.Helper()
	var ae *apierr.Error
	require.ErrorAs(t, err, &ae)
	return ae.Code
}

func findFile(files []SkillFile, path string) SkillFile {
	for _, x := range files {
		if x.Path == path {
			return x
		}
	}
	return SkillFile{}
}

func TestUpdateSkill(t *testing.T) {
	repo := newFakeSkillsRepo()
	svc := NewSkillService(repo)
	ctx := context.Background()
	alpha, err := svc.Create(ctx, Skill{WorkspaceID: "ws", Name: "alpha", Description: "old"})
	require.NoError(t, err)
	_, err = svc.Create(ctx, Skill{WorkspaceID: "ws", Name: "beta"})
	require.NoError(t, err)

	nm, desc := " gamma ", "new desc"
	got, err := svc.Update(ctx, "ws", alpha.ID, &nm, &desc)
	require.NoError(t, err)
	assert.Equal(t, "gamma", got.Name)
	assert.Equal(t, "new desc", got.Description)
	byName, ok, err := repo.GetSkillByName(ctx, "ws", "gamma")
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, alpha.ID, byName.ID, "(ws,name) index visible after rename")

	conflict := "beta"
	_, err = svc.Update(ctx, "ws", alpha.ID, &conflict, nil)
	require.ErrorIs(t, err, ErrSkillNameTaken)

	self := "gamma"
	_, err = svc.Update(ctx, "ws", alpha.ID, &self, nil)
	require.NoError(t, err)

	for _, bad := range []string{"", "   ", strings.Repeat("x", 101)} {
		b := bad
		_, err = svc.Update(ctx, "ws", alpha.ID, &b, nil)
		require.Error(t, err, "invalid names must be rejected: "+bad)
		assert.Equal(t, apierr.Validation, apierrCode(t, err))
	}

	dirty := " na\x00me2 "
	got, err = svc.Update(ctx, "ws", alpha.ID, &dirty, nil)
	require.NoError(t, err)
	assert.Equal(t, "name2", got.Name)

	d2 := "desc only"
	got, err = svc.Update(ctx, "ws", alpha.ID, nil, &d2)
	require.NoError(t, err)
	assert.Equal(t, "name2", got.Name)
	assert.Equal(t, "desc only", got.Description)

	n3 := "x"
	_, err = svc.Update(ctx, "ws", "sk-missing", &n3, nil)
	require.Error(t, err)
	assert.Equal(t, apierr.NotFound, apierrCode(t, err))
	_, err = svc.Update(ctx, "ws-other", alpha.ID, &n3, nil)
	require.Error(t, err)
}

func TestUpsertFile(t *testing.T) {
	repo := newFakeSkillsRepo()
	imp := NewImportService(repo)
	svc := NewSkillService(repo)
	ctx := context.Background()
	sk, err := imp.ImportZip(ctx, "ws", "u1", zipBytes(t, map[string]string{
		"demo/SKILL.md":  "---\nname: demo\ndescription: original\n---\n# body",
		"demo/refs/x.md": "ref v1",
	}), "demo.zip")
	require.NoError(t, err)
	_, before, err := svc.Files(ctx, "ws", sk.ID)
	require.NoError(t, err)

	f, digest, err := svc.UpsertFile(ctx, "ws", sk.ID, "refs/new.md", "brand new")
	require.NoError(t, err)
	assert.Equal(t, "refs/new.md", f.Path)
	assert.Equal(t, "f-refs/new.md", f.ID)
	sum := sha256.Sum256([]byte("brand new"))
	assert.Equal(t, hex.EncodeToString(sum[:]), f.SHA256)
	assert.Equal(t, len("brand new"), f.Size)
	assert.NotEqual(t, before, digest, "files change = tree digest advances (version anchor)")
	files, cur, err := svc.Files(ctx, "ws", sk.ID)
	require.NoError(t, err)
	require.Len(t, files, 3)
	assert.Equal(t, digest, cur, "endpoint digest uses the same algorithm as the read-side recompute")

	f, digest2, err := svc.UpsertFile(ctx, "ws", sk.ID, "demo/refs/x.md", "ref v2")
	require.NoError(t, err)
	assert.Equal(t, "f-demo/refs/x.md", f.ID, "existing row ID reused")
	assert.NotEqual(t, digest, digest2)
	files, _, err = svc.Files(ctx, "ws", sk.ID)
	require.NoError(t, err)
	require.Len(t, files, 3, "upsert existing path adds no rows")
	assert.Equal(t, "ref v2", findFile(files, "demo/refs/x.md").Content)

	_, _, err = svc.UpsertFile(ctx, "ws", "sk-missing", "a.md", "x")
	require.Error(t, err)
	assert.Equal(t, apierr.NotFound, apierrCode(t, err))

	for _, p := range []string{"../evil.md", "/abs.md", "a/../b.md", ""} {
		_, _, err = svc.UpsertFile(ctx, "ws", sk.ID, p, "x")
		require.Error(t, err, "path must be rejected: "+p)
		assert.Equal(t, apierr.Validation, apierrCode(t, err))
	}

	_, _, err = svc.UpsertFile(ctx, "ws", sk.ID, "img/logo.png", "x")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "blacklisted")

	_, _, err = svc.UpsertFile(ctx, "ws", sk.ID, "big.md", strings.Repeat("a", domain.MaxFileBytes+1))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "1MiB")

	for _, p := range []string{"SKILL.md", "skill.md"} {
		_, _, err = svc.UpsertFile(ctx, "ws", sk.ID, p, "# no frontmatter")
		require.Error(t, err, "main file without frontmatter must be rejected: "+p)
		assert.Equal(t, apierr.Validation, apierrCode(t, err))
	}
	_, _, err = svc.UpsertFile(ctx, "ws", sk.ID, "SKILL.md", "---\ndescription: no name\n---\n# x")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "frontmatter")

	mainV2 := "---\nname: demo\ndescription: v2 desc\n---\n# new body\x00"
	_, digest3, err := svc.UpsertFile(ctx, "ws", sk.ID, "demo/SKILL.md", mainV2)
	require.NoError(t, err)
	assert.Len(t, digest3, 64)
	got, ok, err := repo.GetSkillByName(ctx, "ws", "demo")
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, "v2 desc", got.Description, "description follows the main file frontmatter")
	files, _, err = svc.Files(ctx, "ws", sk.ID)
	require.NoError(t, err)
	main := findFile(files, "demo/SKILL.md")
	assert.NotContains(t, main.Content, "\x00", "D22② content sanitization")
	assert.Equal(t, len(main.Content), main.Size, "catalog rows count by sanitized content")
}

func TestDeleteFile(t *testing.T) {
	repo := newFakeSkillsRepo()
	imp := NewImportService(repo)
	svc := NewSkillService(repo)
	ctx := context.Background()
	sk, err := imp.ImportZip(ctx, "ws", "u1", zipBytes(t, map[string]string{
		"demo/SKILL.md":  "---\nname: demo\n---\n# body",
		"demo/refs/x.md": "ref",
	}), "demo.zip")
	require.NoError(t, err)
	files, before, err := svc.Files(ctx, "ws", sk.ID)
	require.NoError(t, err)
	mainID := findFile(files, "demo/SKILL.md").ID
	refID := findFile(files, "demo/refs/x.md").ID

	digest, err := svc.DeleteFile(ctx, "ws", sk.ID, refID)
	require.NoError(t, err)
	assert.Len(t, digest, 64)
	assert.NotEqual(t, before, digest)
	files, _, err = svc.Files(ctx, "ws", sk.ID)
	require.NoError(t, err)
	require.Len(t, files, 1)
	assert.Equal(t, "demo/SKILL.md", files[0].Path)

	_, err = svc.DeleteFile(ctx, "ws", sk.ID, mainID)
	require.Error(t, err)
	assert.Equal(t, apierr.Validation, apierrCode(t, err))
	files, _, err = svc.Files(ctx, "ws", sk.ID)
	require.NoError(t, err)
	require.Len(t, files, 1, "main file kept")

	_, err = svc.DeleteFile(ctx, "ws", sk.ID, "f-nope")
	require.Error(t, err)
	assert.Equal(t, apierr.NotFound, apierrCode(t, err))
	_, err = svc.DeleteFile(ctx, "ws", "sk-missing", mainID)
	require.Error(t, err)
	assert.Equal(t, apierr.NotFound, apierrCode(t, err))
	_, err = svc.DeleteFile(ctx, "ws-other", sk.ID, mainID)
	require.Error(t, err)
}

func tarGz(t *testing.T, prefix string, entries map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, p := range sortedKeys(entries) {
		body := entries[p]
		require.NoError(t, tw.WriteHeader(&tar.Header{Name: prefix + "/" + p, Mode: 0o644, Size: int64(len(body))}))
		_, err := tw.Write([]byte(body))
		require.NoError(t, err)
	}
	require.NoError(t, tw.Close())
	require.NoError(t, gz.Close())
	return buf.Bytes()
}

func sortedKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

const mainMd = "---\nname: pdf-skill\ndescription: process PDF\n---\n\n# PDF\nskill body\n"

type fakeFetch struct {
	bodies map[string][]byte
	errs   map[string]error
	auth   []string
	calls  []string
}

func (f *fakeFetch) fetch(_ context.Context, url string, header map[string]string) ([]byte, error) {
	f.calls = append(f.calls, url)
	if h, ok := header["Authorization"]; ok {
		f.auth = append(f.auth, h)
	}
	if err, ok := f.errs[url]; ok {
		return nil, err
	}
	return f.bodies[url], nil
}

func newURLImportService(repo Repo, ff *fakeFetch, opts ImportOptions) *ImportService {
	opts.Fetch = ff.fetch
	return NewImportServiceWithOptions(repo, opts)
}

func TestImportURL_GitHubTarball(t *testing.T) {
	repo := newFakeSkillsRepo()
	ff := &fakeFetch{bodies: map[string][]byte{
		"https://codeload.github.com/octo/skill-repo/tar.gz/HEAD": tarGz(t, "skill-repo-abc123", map[string]string{
			"SKILL.md":     mainMd,
			"refs/spec.md": "specification document",
			"LICENSE":      "MIT",
		}),
	}}
	svc := newURLImportService(repo, ff, ImportOptions{})

	sk, err := svc.ImportURL(context.Background(), "ws-1", "u1", "https://github.com/octo/skill-repo")
	require.NoError(t, err)
	assert.Equal(t, "pdf-skill", sk.Name, "frontmatter name")
	assert.Equal(t, "url", sk.Origin)
	assert.Equal(t, "https://github.com/octo/skill-repo", sk.OriginRef, "refresh anchor = the original URL")
	files := repo.files[sk.ID]
	require.Len(t, files, 2, "loose LICENSE skipped; SKILL.md+refs/spec.md packed")
	paths := []string{files[0].Path, files[1].Path}
	assert.Contains(t, paths, "SKILL.md")
	assert.Contains(t, paths, "refs/spec.md", "repo root prefix stripped")
	require.Empty(t, ff.auth, "no token, no header")
}

func TestImportURL_GitHubTarballWithToken(t *testing.T) {
	repo := newFakeSkillsRepo()
	ff := &fakeFetch{bodies: map[string][]byte{
		"https://codeload.github.com/octo/repo/tar.gz/HEAD": tarGz(t, "repo-x", map[string]string{"SKILL.md": mainMd}),
	}}
	svc := newURLImportService(repo, ff, ImportOptions{GitHubToken: "ghp_test"})

	_, err := svc.ImportURL(context.Background(), "ws-1", "u1", "https://github.com/octo/repo")
	require.NoError(t, err)
	require.Len(t, ff.auth, 1)
	assert.Equal(t, "Bearer ghp_test", ff.auth[0], "GITHUB_TOKEN injected via Authorization")
}

func TestImportURL_SkillsShGateAndResolution(t *testing.T) {
	tarball := tarGz(t, "mono-1", map[string]string{
		"SKILL.md":              "---\nname: mono\n---\nroot",
		"pdf-skill/SKILL.md":    mainMd,
		"pdf-skill/refs/api.md": "API",
		"other-skill/SKILL.md":  "---\nname: other\n---\n",
	})
	ff := &fakeFetch{bodies: map[string][]byte{
		"https://codeload.github.com/octo/mono/tar.gz/HEAD": tarball,
	}}

	svc := newURLImportService(newFakeSkillsRepo(), ff, ImportOptions{})
	_, err := svc.ImportURL(context.Background(), "ws-1", "u1", "https://skills.sh/octo/mono/pdf-skill")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "skills.sh integration disabled")

	repo := newFakeSkillsRepo()
	svc = newURLImportService(repo, ff, ImportOptions{SkillsShEnabled: true})
	sk, err := svc.ImportURL(context.Background(), "ws-1", "u1", "https://skills.sh/octo/mono/pdf-skill")
	require.NoError(t, err)
	assert.Equal(t, "pdf-skill", sk.Name)
	files := repo.files[sk.ID]
	require.Len(t, files, 2)
	assert.Equal(t, "SKILL.md", files[0].Path)
	assert.Equal(t, "refs/api.md", files[1].Path, "take only the pdf-skill/ subdirectory")
}

func TestImportURL_BareFile(t *testing.T) {
	repo := newFakeSkillsRepo()
	ff := &fakeFetch{bodies: map[string][]byte{
		"https://example.com/skills/pdf.md": []byte(mainMd),
	}}
	svc := newURLImportService(repo, ff, ImportOptions{})

	sk, err := svc.ImportURL(context.Background(), "ws-1", "u1", "https://example.com/skills/pdf.md")
	require.NoError(t, err)
	assert.Equal(t, "pdf-skill", sk.Name)
	files := repo.files[sk.ID]
	require.Len(t, files, 1)
	assert.Equal(t, "SKILL.md", files[0].Path, "the bare file is the main file")
}

func TestImportURL_SSRFRejected(t *testing.T) {
	cases := []struct {
		name string
		url  string
	}{
		{"private IPv4", "https://10.1.2.3/SKILL.md"},
		{"loopback address", "https://127.0.0.1:8443/x.md"},
		{"link-local", "https://169.254.169.254/latest/meta-data"},
		{"private range 192.168", "https://192.168.1.10/skill.md"},
		{"IPv6 local", "https://[::1]/SKILL.md"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc := newURLImportService(newFakeSkillsRepo(), &fakeFetch{}, ImportOptions{})
			_, err := svc.ImportURL(context.Background(), "ws-1", "u1", tc.url)
			require.Error(t, err, "SSRF private target must be rejected")
			assert.Contains(t, err.Error(), "rejected")
		})
	}
}

func TestImportURL_FetchLimit(t *testing.T) {
	repo := newFakeSkillsRepo()
	ff := &fakeFetch{bodies: map[string][]byte{
		"https://codeload.github.com/octo/big/tar.gz/HEAD": bytes.Repeat([]byte("a"), domain.MaxURLFetchBytes+1),
	}}
	svc := newURLImportService(repo, ff, ImportOptions{})

	_, err := svc.ImportURL(context.Background(), "ws-1", "u1", "https://github.com/octo/big")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "50MiB")
}

func TestRefresh_URL_BothStates(t *testing.T) {
	repo := newFakeSkillsRepo()
	urlV1 := "https://example.com/skills/pdf.md"
	ff := &fakeFetch{bodies: map[string][]byte{urlV1: []byte(mainMd)}}
	svc := newURLImportService(repo, ff, ImportOptions{})

	sk, err := svc.ImportURL(context.Background(), "ws-1", "u1", urlV1)
	require.NoError(t, err)
	replacedBefore := len(repo.replaced)

	n, err := svc.Refresh(context.Background(), "ws-1", sk.ID)
	require.NoError(t, err)
	assert.Zero(t, n)
	assert.Len(t, repo.replaced, replacedBefore, "unchanged content does not write to DB")

	urlV2 := "---\nname: pdf-skill\ndescription: process PDF v2\n---\n\n# PDF\nnew body\n"
	ff.bodies[urlV1] = []byte(urlV2)
	n, err = svc.Refresh(context.Background(), "ws-1", sk.ID)
	require.NoError(t, err)
	assert.Equal(t, 1, n)
	assert.Len(t, repo.replaced, replacedBefore+1, "rewrite the catalog after change")
	got, ok, err := repo.GetSkillByName(context.Background(), "ws-1", "pdf-skill")
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, "process PDF v2", got.Description, "description follows the remote frontmatter")
	files := repo.files[sk.ID]
	assert.Contains(t, files[0].Content, "new body")
}

func TestRefresh_NonURLOriginNoOp(t *testing.T) {
	repo := newFakeSkillsRepo()
	ss := NewSkillService(repo)
	sk, err := ss.Create(context.Background(), Skill{WorkspaceID: "ws-1", Name: "handwritten", Origin: "manual"})
	require.NoError(t, err)
	svc := NewImportService(repo)
	require.NoError(t, repo.ReplaceFiles(context.Background(), sk.ID, []SkillFile{
		{Path: "SKILL.md", Content: "x", SHA256: "h", Size: 1},
	}))
	n, err := svc.Refresh(context.Background(), "ws-1", sk.ID)
	require.NoError(t, err)
	assert.Zero(t, n)
}
