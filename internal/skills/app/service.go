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
	"io"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/haozing/ploykit/platform/egressx"
	"github.com/haozing/ploykit/platform/redactx"
	"orkome/internal/contract/apierr"
	"orkome/internal/skills/domain"
)

type Skill struct {
	ID          string    `json:"id"`
	WorkspaceID string    `json:"workspace_id"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	Origin      string    `json:"origin"`
	OriginRef   string    `json:"origin_ref,omitempty"`
	CreatedBy   string    `json:"created_by,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
}

type SkillFile struct {
	ID      string `json:"id,omitempty"`
	Path    string `json:"path"`
	Content string `json:"content"`
	SHA256  string `json:"sha256"`
	Size    int    `json:"size_bytes"`
}

type AgentSkill struct {
	SkillID     string `json:"skill_id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Enabled     bool   `json:"enabled"`
}

type SkillAgent struct {
	AgentID string `json:"agent_id"`
	Name    string `json:"name"`
	Enabled bool   `json:"enabled"`
}

type Repo interface {
	InsertSkill(ctx context.Context, s Skill) (Skill, error)
	GetSkill(ctx context.Context, ws, id string) (Skill, bool, error)
	ListSkills(ctx context.Context, ws string) ([]Skill, error)
	SearchSkills(ctx context.Context, ws string, q string, limit int) ([]Skill, error)
	DeleteSkill(ctx context.Context, ws, id string) error
	UpdateSkill(ctx context.Context, ws, id string, name, description *string) (Skill, bool, error)
	ReplaceFiles(ctx context.Context, skillID string, files []SkillFile) error
	UpsertFile(ctx context.Context, skillID string, f SkillFile) error
	GetFile(ctx context.Context, ws, skillID, fileID string) (SkillFile, bool, error)
	DeleteFile(ctx context.Context, ws, skillID, fileID string) error
	ListFiles(ctx context.Context, ws, skillID string) ([]SkillFile, error)
	GetSkillByName(ctx context.Context, ws, name string) (Skill, bool, error)
	SetAgentSkills(ctx context.Context, ws, agentID string, skillIDs []string, enabled bool) error
	AddAgentSkill(ctx context.Context, ws, agentID, skillID string) error
	SetAgentSkillEnabled(ctx context.Context, ws, agentID, skillID string, en bool) error
	RemoveAgentSkill(ctx context.Context, ws, agentID, skillID string) error
	AgentInWorkspace(ctx context.Context, ws, agentID string) (bool, error)
	ListAgentSkills(ctx context.Context, ws, agentID string) ([]AgentSkill, error)

	ListSkillAgents(ctx context.Context, ws, skillID string) ([]SkillAgent, error)
}

type SkillService struct{ repo Repo }

func NewSkillService(repo Repo) *SkillService { return &SkillService{repo: repo} }

func (s *SkillService) Create(ctx context.Context, sk Skill) (*Skill, error) {

	sk.Name = strings.TrimSpace(redactx.Sanitize(sk.Name))
	sk.Description = redactx.Sanitize(sk.Description)
	if sk.Name == "" || len(sk.Name) > 100 {
		return nil, apierr.NewValidation("invalid skill name")
	}
	out, err := s.repo.InsertSkill(ctx, sk)
	if err != nil {
		return nil, err
	}
	return &out, nil
}

func (s *SkillService) Get(ctx context.Context, ws, id string) (*Skill, error) {
	out, ok, err := s.repo.GetSkill(ctx, ws, id)
	if err != nil || !ok {
		return nil, apierr.New(apierr.NotFound, "skill not found")
	}
	return &out, nil
}

func (s *SkillService) List(ctx context.Context, ws string) ([]Skill, error) {
	return s.repo.ListSkills(ctx, ws)
}

func (s *SkillService) Search(ctx context.Context, ws, q string) ([]Skill, error) {
	q = strings.TrimSpace(q)
	if q == "" {
		return s.repo.ListSkills(ctx, ws)
	}
	return s.repo.SearchSkills(ctx, ws, q, 20)
}

func (s *SkillService) Delete(ctx context.Context, ws, id string) error {
	return s.repo.DeleteSkill(ctx, ws, id)
}

var ErrSkillNameTaken = apierr.New(apierr.Conflict, "skill name already exists in this workspace")

func (s *SkillService) Update(ctx context.Context, ws, id string, name, description *string) (*Skill, error) {
	if _, err := s.Get(ctx, ws, id); err != nil {
		return nil, err
	}
	if name != nil {
		n := strings.TrimSpace(redactx.Sanitize(*name))
		if n == "" || len(n) > 100 {
			return nil, apierr.NewValidation("invalid skill name")
		}
		if other, ok, err := s.repo.GetSkillByName(ctx, ws, n); err != nil {
			return nil, err
		} else if ok && other.ID != id {
			return nil, ErrSkillNameTaken
		}
		*name = n
	}
	if description != nil {
		*description = redactx.Sanitize(*description)
	}
	out, ok, err := s.repo.UpdateSkill(ctx, ws, id, name, description)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, apierr.New(apierr.NotFound, "skill not found")
	}
	return &out, nil
}

func (s *SkillService) UpsertFile(ctx context.Context, ws, skillID, rawPath, content string) (*SkillFile, string, error) {
	if _, err := s.Get(ctx, ws, skillID); err != nil {
		return nil, "", err
	}

	clean, ok := domain.CleanPath(redactx.Sanitize(rawPath))
	if !ok {
		return nil, "", apierr.NewValidation("file path rejected: " + rawPath)
	}
	f, err := buildFile(clean, []byte(content))
	if err != nil {
		return nil, "", err
	}
	isMain := domain.MainFilenameGuard(clean)
	var fm domain.Frontmatter
	if isMain {
		if fm = domain.ParseFrontmatter(f.Content); fm.Name == "" {
			return nil, "", apierr.NewValidation("SKILL.md requires frontmatter with a name")
		}
	}
	if err := s.repo.UpsertFile(ctx, skillID, f); err != nil {
		return nil, "", err
	}
	if isMain && fm.Description != "" {
		desc := redactx.Sanitize(fm.Description)
		if _, _, err := s.repo.UpdateSkill(ctx, ws, skillID, nil, &desc); err != nil {
			return nil, "", err
		}
	}
	files, err := s.repo.ListFiles(ctx, ws, skillID)
	if err != nil {
		return nil, "", err
	}
	digest := s.manifest(files)
	for i := range files {
		if files[i].Path == clean {
			return &files[i], digest, nil
		}
	}
	return &f, digest, nil
}

func (s *SkillService) DeleteFile(ctx context.Context, ws, skillID, fileID string) (string, error) {
	if _, err := s.Get(ctx, ws, skillID); err != nil {
		return "", err
	}
	f, ok, err := s.repo.GetFile(ctx, ws, skillID, fileID)
	if err != nil {
		return "", err
	}
	if !ok {
		return "", apierr.New(apierr.NotFound, "file not found")
	}
	if domain.MainFilenameGuard(f.Path) {
		return "", apierr.NewValidation("cannot delete SKILL.md main file")
	}
	if err := s.repo.DeleteFile(ctx, ws, skillID, fileID); err != nil {
		return "", err
	}
	files, err := s.repo.ListFiles(ctx, ws, skillID)
	if err != nil {
		return "", err
	}
	return s.manifest(files), nil
}

func (s *SkillService) Files(ctx context.Context, ws, id string) ([]SkillFile, string, error) {
	if _, err := s.Get(ctx, ws, id); err != nil {
		return nil, "", err
	}
	files, err := s.repo.ListFiles(ctx, ws, id)
	if err != nil {
		return nil, "", err
	}
	if err := s.verify(files); err != nil {
		return nil, "", err
	}
	return files, s.manifest(files), nil
}

var ErrSkillMainMissing = apierr.New(apierr.NotFound, "skill main file missing")

func (s *SkillService) MainByName(ctx context.Context, ws, name string) (string, error) {
	sk, ok, err := s.repo.GetSkillByName(ctx, ws, name)
	if err != nil {
		return "", err
	}
	if !ok {
		return "", apierr.New(apierr.NotFound, "skill not found")
	}
	files, err := s.repo.ListFiles(ctx, ws, sk.ID)
	if err != nil {
		return "", err
	}
	if err := s.verify(files); err != nil {
		return "", err
	}
	for _, f := range files {
		if domain.MainFilenameGuard(f.Path) {
			return f.Content, nil
		}
	}
	return "", ErrSkillMainMissing
}

func (s *SkillService) verify(files []SkillFile) error {
	mf := make([]domain.ManifestFile, len(files))
	for i, f := range files {
		mf[i] = domain.ManifestFile{Path: f.Path, Content: f.Content, SHA256: f.SHA256, Size: f.Size}
	}
	return domain.VerifyManifest(mf)
}

func (s *SkillService) manifest(files []SkillFile) string {
	mf := make([]domain.ManifestFile, len(files))
	for i, f := range files {
		mf[i] = domain.ManifestFile{Path: f.Path, Content: f.Content, SHA256: f.SHA256, Size: f.Size}
	}
	return domain.ManifestDigest(mf)
}

type Fetch func(ctx context.Context, url string, header map[string]string) ([]byte, error)

type ImportOptions struct {
	Fetch           Fetch
	Guard           *egressx.Guard
	GitHubToken     string
	SkillsShEnabled bool
}

type ImportService struct {
	repo     Repo
	svc      *SkillService
	fetch    Fetch
	guard    *egressx.Guard
	githubTk string
	skillsSh bool
}

func NewImportService(repo Repo) *ImportService {
	return NewImportServiceWithOptions(repo, ImportOptions{})
}

func NewImportServiceWithOptions(repo Repo, o ImportOptions) *ImportService {
	guard := o.Guard
	if guard == nil {
		guard = egressx.DefaultGuard()
	}
	fetch := o.Fetch
	if fetch == nil {
		fetch = defaultHTTPFetch
	}
	return &ImportService{repo: repo, svc: NewSkillService(repo), fetch: fetch,
		guard: guard, githubTk: o.GitHubToken, skillsSh: o.SkillsShEnabled}
}

func defaultHTTPFetch(ctx context.Context, rawURL string, header map[string]string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	for k, v := range header {
		req.Header.Set(k, v)
	}
	resp, err := httpFetchClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fetch %s: status %d", rawURL, resp.StatusCode)
	}
	return io.ReadAll(io.LimitReader(resp.Body, domain.MaxURLFetchBytes+1))
}

var httpFetchClient = &http.Client{Timeout: 30 * time.Second}

func buildFile(clean string, raw []byte) (SkillFile, error) {
	if domain.IsBinaryExt(clean) {
		return SkillFile{}, apierr.NewValidation("binary file blacklisted: " + clean)
	}
	if len(raw) > domain.MaxFileBytes {
		return SkillFile{}, apierr.NewValidation("file exceeds 1MiB: " + clean)
	}
	content := redactx.Sanitize(string(raw))
	sum := sha256.Sum256([]byte(content))
	return SkillFile{Path: clean, Content: content,
		SHA256: hex.EncodeToString(sum[:]), Size: len(content)}, nil
}

func (s *ImportService) persist(ctx context.Context, sk Skill, files []SkillFile) (*Skill, error) {
	out, err := s.repo.InsertSkill(ctx, sk)
	if err != nil {
		return nil, err
	}
	if err := s.repo.ReplaceFiles(ctx, out.ID, files); err != nil {
		return nil, err
	}
	return &out, nil
}

func defaultSkillName(files []SkillFile) string {
	if seg := strings.Split(files[0].Path, "/"); len(seg) > 1 {
		return seg[0]
	}
	return strings.TrimSuffix(files[0].Path, ".md")
}

func (s *ImportService) ImportZip(ctx context.Context, ws, userID string, zipBytes []byte, originRef string) (*Skill, error) {
	if len(zipBytes) > domain.MaxZipBytes {
		return nil, apierr.NewValidation("zip exceeds 50MiB limit")
	}
	zr, err := zip.NewReader(bytes.NewReader(zipBytes), int64(len(zipBytes)))
	if err != nil {
		return nil, apierr.NewValidation("invalid zip archive")
	}
	var files []SkillFile
	var fm domain.Frontmatter
	hasMain := false
	for _, zf := range zr.File {
		if zf.FileInfo().IsDir() {
			continue
		}
		if len(files) >= domain.MaxFilesPerSkill {
			return nil, apierr.NewValidation("skill exceeds 200 files")
		}

		clean, ok := domain.CleanPath(redactx.Sanitize(zf.Name))
		if !ok {
			return nil, apierr.NewValidation("zip path escape rejected: " + zf.Name)
		}
		if int64(zf.UncompressedSize64) > domain.MaxFileBytes {
			return nil, apierr.NewValidation("file exceeds 1MiB: " + clean)
		}
		rc, err := zf.Open()
		if err != nil {
			return nil, apierr.NewValidation("zip entry unreadable")
		}
		var buf bytes.Buffer
		n, err := io.Copy(&buf, io.LimitReader(rc, domain.MaxFileBytes+1))
		rc.Close()
		if err != nil || n > domain.MaxFileBytes {
			return nil, apierr.NewValidation("file exceeds 1MiB: " + clean)
		}
		f, err := buildFile(clean, buf.Bytes())
		if err != nil {
			return nil, err
		}
		if domain.MainFilenameGuard(clean) {
			hasMain = true
			fm = domain.ParseFrontmatter(f.Content)
		}
		files = append(files, f)
	}
	if !hasMain {
		return nil, apierr.NewValidation("SKILL.md main file required")
	}

	fm.Name = strings.TrimSpace(redactx.Sanitize(fm.Name))
	fm.Description = redactx.Sanitize(fm.Description)
	name := fm.Name
	if name == "" {
		name = defaultSkillName(files)
	}
	return s.persist(ctx, Skill{
		WorkspaceID: ws, Name: name, Description: fm.Description,
		Origin: "import", OriginRef: originRef, CreatedBy: userID,
	}, files)
}

func (s *ImportService) ImportURL(ctx context.Context, ws, userID, rawURL string) (*Skill, error) {
	files, fm, err := s.fetchSource(ctx, rawURL)
	if err != nil {
		return nil, err
	}
	_ = fm
	name := fm.Name
	if name == "" {
		name = defaultSkillName(files)
	}
	return s.persist(ctx, Skill{
		WorkspaceID: ws, Name: name, Description: fm.Description,
		Origin: "url", OriginRef: rawURL, CreatedBy: userID,
	}, files)
}

func (s *ImportService) fetchSource(ctx context.Context, rawURL string) ([]SkillFile, domain.Frontmatter, error) {
	parts, err := domain.DetectSource(rawURL)
	if err != nil {
		return nil, domain.Frontmatter{}, apierr.NewValidation(err.Error())
	}
	if parts.Kind == domain.SourceSkillsSh && !s.skillsSh {
		return nil, domain.Frontmatter{}, apierr.NewValidation("skills.sh integration disabled (ORKOME_RUN_SKILLS_SH_INTEGRATION)")
	}

	if err := s.guard.ValidateURL(ctx, rawURL); err != nil {
		return nil, domain.Frontmatter{}, apierr.NewValidation("url rejected: " + err.Error())
	}
	if parts.Kind == domain.SourceBareFile {
		return s.fetchBareFile(ctx, rawURL)
	}
	return s.fetchTarball(ctx, parts)
}

func (s *ImportService) fetchBareFile(ctx context.Context, rawURL string) ([]SkillFile, domain.Frontmatter, error) {
	body, err := s.fetch(ctx, rawURL, nil)
	if err != nil {
		return nil, domain.Frontmatter{}, apierr.NewValidation("fetch failed: " + err.Error())
	}
	if len(body) > domain.MaxURLFetchBytes {
		return nil, domain.Frontmatter{}, apierr.NewValidation("download exceeds 50MiB limit")
	}
	f, err := buildFile("SKILL.md", body)
	if err != nil {
		return nil, domain.Frontmatter{}, err
	}
	return []SkillFile{f}, domain.ParseFrontmatter(f.Content), nil
}

func (s *ImportService) fetchTarball(ctx context.Context, parts domain.SourceParts) ([]SkillFile, domain.Frontmatter, error) {
	header := map[string]string{}
	if s.githubTk != "" {
		header["Authorization"] = "Bearer " + s.githubTk
	}
	tarURL := parts.GitHubTarballURL()
	if err := s.guard.ValidateURL(ctx, tarURL); err != nil {
		return nil, domain.Frontmatter{}, apierr.NewValidation("url rejected: " + err.Error())
	}
	body, err := s.fetch(ctx, tarURL, header)
	if err != nil {
		return nil, domain.Frontmatter{}, apierr.NewValidation("fetch failed: " + err.Error())
	}
	if len(body) > domain.MaxURLFetchBytes {
		return nil, domain.Frontmatter{}, apierr.NewValidation("download exceeds 50MiB limit")
	}
	entries, err := readTarball(body)
	if err != nil {
		return nil, domain.Frontmatter{}, apierr.NewValidation("invalid tarball: " + err.Error())
	}

	rel := make(map[string][]byte, len(entries))
	for path, content := range entries {
		if i := strings.IndexByte(path, '/'); i > 0 {
			rel[path[i+1:]] = content
		}
	}

	var dirPrefix string
	if parts.Kind == domain.SourceSkillsSh {
		if _, ok := rel[parts.Skill+"/SKILL.md"]; ok || hasDirPrefix(rel, parts.Skill+"/") {
			dirPrefix = parts.Skill + "/"
		}
	}
	if dirPrefix == "" {
		if _, ok := rel["SKILL.md"]; ok {
			dirPrefix = ""
		} else {
			var candidates []string
			for p := range rel {
				if strings.HasPrefix(p, "/") {
					continue
				}
				seg := strings.SplitN(p, "/", 2)
				if len(seg) == 2 && seg[1] == "SKILL.md" {
					candidates = append(candidates, seg[0])
				}
			}
			if len(candidates) != 1 {
				return nil, domain.Frontmatter{}, apierr.NewValidation(
					"tarball has no unambiguous SKILL.md (root or single top-level dir required)")
			}
			dirPrefix = candidates[0] + "/"
		}
	}
	var files []SkillFile
	var fm domain.Frontmatter
	hasMain := false

	paths := make([]string, 0, len(rel))
	for p := range rel {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	for _, p := range paths {
		if !strings.HasPrefix(p, dirPrefix) {
			continue
		}
		clean := strings.TrimPrefix(p, dirPrefix)
		if clean == "" {
			continue
		}
		if len(files) >= domain.MaxFilesPerSkill {
			return nil, domain.Frontmatter{}, apierr.NewValidation("skill exceeds 200 files")
		}

		if domain.IsBinaryExt(clean) || skipRepoMeta(clean) {
			continue
		}
		cleanOK, ok := domain.CleanPath(clean)
		if !ok {
			return nil, domain.Frontmatter{}, apierr.NewValidation("tarball path rejected: " + clean)
		}
		f, err := buildFile(cleanOK, rel[p])
		if err != nil {
			return nil, domain.Frontmatter{}, err
		}
		if domain.MainFilenameGuard(cleanOK) {
			hasMain = true
			fm = domain.ParseFrontmatter(f.Content)
		}
		files = append(files, f)
	}
	if !hasMain {
		return nil, domain.Frontmatter{}, apierr.NewValidation("SKILL.md main file required")
	}
	fm.Name = strings.TrimSpace(redactx.Sanitize(fm.Name))
	fm.Description = redactx.Sanitize(fm.Description)
	return files, fm, nil
}

func skipRepoMeta(clean string) bool {
	if strings.HasPrefix(clean, ".git") {
		return true
	}
	return strings.HasPrefix(clean, "LICENSE")
}

func hasDirPrefix(files map[string][]byte, prefix string) bool {
	for p := range files {
		if strings.HasPrefix(p, prefix) {
			return true
		}
	}
	return false
}

func readTarball(body []byte) (map[string][]byte, error) {
	gz, err := gzip.NewReader(bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	defer gz.Close()
	out := map[string][]byte{}
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return out, nil
		}
		if err != nil {
			return nil, err
		}
		if hdr.Typeflag != tar.TypeReg {
			continue
		}
		if hdr.Size > domain.MaxFileBytes {
			return nil, fmt.Errorf("file exceeds 1MiB: %s", hdr.Name)
		}
		b, err := io.ReadAll(io.LimitReader(tr, domain.MaxFileBytes+1))
		if err != nil {
			return nil, err
		}
		if int64(len(b)) > domain.MaxFileBytes {
			return nil, fmt.Errorf("file exceeds 1MiB: %s", hdr.Name)
		}
		out[hdr.Name] = b
	}
}

func (s *ImportService) Refresh(ctx context.Context, ws, id string) (int, error) {
	sk, err := s.svc.Get(ctx, ws, id)
	if err != nil {
		return 0, err
	}
	if sk.Origin != "url" || sk.OriginRef == "" {
		return 0, nil
	}
	stored, err := s.repo.ListFiles(ctx, ws, id)
	if err != nil {
		return 0, err
	}
	files, fm, err := s.fetchSource(ctx, sk.OriginRef)
	if err != nil {
		return 0, err
	}
	if domain.ManifestDigest(toManifest(files)) == domain.ManifestDigest(toManifest(stored)) {
		return 0, nil
	}

	_, err = s.persist(ctx, Skill{
		WorkspaceID: ws, Name: sk.Name, Description: fm.Description,
		Origin: "url", OriginRef: sk.OriginRef, CreatedBy: sk.CreatedBy,
	}, files)
	if err != nil {
		return 0, err
	}
	return len(files), nil
}

func toManifest(files []SkillFile) []domain.ManifestFile {
	mf := make([]domain.ManifestFile, len(files))
	for i, f := range files {
		mf[i] = domain.ManifestFile{Path: f.Path, Content: f.Content, SHA256: f.SHA256, Size: f.Size}
	}
	return mf
}

// ---- agent-skill 绑定面（FP-08 P1-4）----
// 五个绑定方法此前收 ws 不用，repo 写面无 ag.workspace_id 限定：A 区成员
// 持 B 区 agent uuid 即可为他区 agent 增删/启停技能（跨区越权写）。
// 现在统一先做工作区归属校验（agent ∈ ws、skill ∈ ws），再调用带 ws 限定
// 的 repo 写——跨区一律 404，不泄露存在性（对照 squads GetAgent 同款链）。

func (s *SkillService) requireWsAgent(ctx context.Context, ws, agentID string) error {
	ok, err := s.repo.AgentInWorkspace(ctx, ws, agentID)
	if err != nil {
		return err
	}
	if !ok {
		return apierr.New(apierr.NotFound, "agent not found in this workspace")
	}
	return nil
}

func (s *SkillService) SetAgentSkills(ctx context.Context, ws, agentID string, ids []string) error {
	if err := s.requireWsAgent(ctx, ws, agentID); err != nil {
		return err
	}
	for _, id := range ids {
		// 外来 skill uuid 此前靠 FK 放行（只查存在性），这里一并校验归属。
		if _, err := s.Get(ctx, ws, id); err != nil {
			return err
		}
	}
	return s.repo.SetAgentSkills(ctx, ws, agentID, ids, true)
}

func (s *SkillService) AddAgentSkill(ctx context.Context, ws, agentID, skillID string) error {
	if err := s.requireWsAgent(ctx, ws, agentID); err != nil {
		return err
	}
	if _, err := s.Get(ctx, ws, skillID); err != nil {
		return err
	}
	return s.repo.AddAgentSkill(ctx, ws, agentID, skillID)
}

func (s *SkillService) SetAgentSkillEnabled(ctx context.Context, ws, agentID, skillID string, en bool) error {
	if err := s.requireWsAgent(ctx, ws, agentID); err != nil {
		return err
	}
	if _, err := s.Get(ctx, ws, skillID); err != nil {
		return err
	}
	return s.repo.SetAgentSkillEnabled(ctx, ws, agentID, skillID, en)
}

func (s *SkillService) RemoveAgentSkill(ctx context.Context, ws, agentID, skillID string) error {
	if err := s.requireWsAgent(ctx, ws, agentID); err != nil {
		return err
	}
	if _, err := s.Get(ctx, ws, skillID); err != nil {
		return err
	}
	return s.repo.RemoveAgentSkill(ctx, ws, agentID, skillID)
}

func (s *SkillService) ListAgentSkills(ctx context.Context, ws, agentID string) ([]AgentSkill, error) {
	return s.repo.ListAgentSkills(ctx, ws, agentID)
}

func (s *SkillService) ListSkillAgents(ctx context.Context, ws, skillID string) ([]SkillAgent, error) {
	list, err := s.repo.ListSkillAgents(ctx, ws, skillID)
	if err != nil {
		return nil, err
	}
	if list == nil {
		list = []SkillAgent{}
	}
	return list, nil
}
