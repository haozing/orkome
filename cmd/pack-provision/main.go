package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/client/transport"
	mcpgo "github.com/mark3labs/mcp-go/mcp"
	"gopkg.in/yaml.v3"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "pack-provision:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: pack-provision install|uninstall <pack-dir> --base URL --workspace ID --pat TOKEN [--credential KEY=VALUE] [--no-prefix-check]")
	}
	fs := flag.NewFlagSet(args[0], flag.ExitOnError)
	base := fs.String("base", "http://localhost:8080", "orkome base URL")
	ws := fs.String("workspace", "", "workspace id")
	pat := fs.String("pat", "", "PAT (owner/admin)")
	creds := credsFlag{}
	fs.Var(&creds, "credential", "pack credential KEY=VALUE (repeatable)")
	noPrefix := fs.Bool("no-prefix-check", false, "skip tool name prefix check (legacy server exemption; pack/v1 output & job-handle gates are NOT exempted)")
	frameOrigin := fs.String("frame-origin", "", "orkome web origin for slot frame-ancestors probing (e.g. https://orkome.example.com); empty = probing disabled, non-first-party slots are kept with a warning")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	if fs.NArg() < 1 {
		return fmt.Errorf("pack directory is required")
	}
	if *ws == "" || *pat == "" {
		return fmt.Errorf("--workspace and --pat are required")
	}
	pack, err := parsePack(fs.Arg(0))
	if err != nil {
		return err
	}
	c := &apiClient{Base: strings.TrimRight(*base, "/"), WS: *ws, PAT: *pat, HC: &http.Client{Timeout: 15 * time.Second}}
	ctx := context.Background()

	switch args[0] {
	case "install":
		return install(ctx, c, pack, creds.m, !*noPrefix, *frameOrigin)
	case "uninstall":
		return uninstall(ctx, c, pack)
	default:
		return fmt.Errorf("unknown subcommand %q（install|uninstall）", args[0])
	}
}

type packSpec struct {
	APIVersion string      `yaml:"apiVersion"`
	Name       string      `yaml:"name"`
	Version    string      `yaml:"version"`
	Creds      []packCred  `yaml:"credentials"`
	MCP        []packMCP   `yaml:"mcp"`
	Events     []packEvent `yaml:"events"`
	UI         packUI      `yaml:"ui"`
	Agents     []packAgent `yaml:"-"`
	Skills     []packSkill `yaml:"-"`
}

type packUI struct {
	Entry *struct {
		Label       string `yaml:"label"`
		URLTemplate string `yaml:"urlTemplate"`
	} `yaml:"entry"`
	// Slots 是 pack/v1 ui.slots 段（列表；前端设计 §1.2）。
	// v0 非 strict 解析静默忽略（B1 已锚定）；v1 解析期做目录冻结校验。
	Slots []packUISlot `yaml:"slots"`
}

// packUISlot 是 ui.slots[] 条目。slot 标识 v0 只认 workitem.detail.tab（目录冻结）。
type packUISlot struct {
	Slot        string `yaml:"slot"`
	Label       string `yaml:"label"`
	URLTemplate string `yaml:"urlTemplate"`
	Height      int    `yaml:"height"`
}

// SlotWorkItemDetailTab 与后端 app.SlotWorkItemDetailTab 同值（CLI 不 import 内部包，保持自包含）。
const SlotWorkItemDetailTab = "workitem.detail.tab"

type packCred struct {
	Key         string `yaml:"key"`
	Description string `yaml:"description"`
}

type packMCP struct {
	Name          string        `yaml:"name"`
	Transport     string        `yaml:"transport"`
	URL           string        `yaml:"url"`
	CredentialRef string        `yaml:"credentialRef"`
	Header        string        `yaml:"header"`
	Tools         []packMCPTool `yaml:"tools"` // v1 新增：工具 output 声明段；v0 非 strict 解析静默忽略
}

// packMCPTool 是 pack/v1 mcp.tools[] 条目（接口冻结 docs/interfaces-job-handle-2026-10-11.md §2）。
type packMCPTool struct {
	Name   string `yaml:"name"`   // 含 pack 前缀的工具全名
	Output any    `yaml:"output"` // 输出 JSON Schema 对象；块标量 JSON 或 YAML 映射均可，规范化见 validateV1ToolDecls
}

type packEvent struct {
	URL   string   `yaml:"url"`
	Types []string `yaml:"types"`
}

type packAgent struct {
	Name          string         `yaml:"name"`
	Description   string         `yaml:"description"`
	MaxConcurrent int            `yaml:"max_concurrent_tasks"`
	Visibility    string         `yaml:"visibility"`
	RuntimeConfig map[string]any `yaml:"runtime_config"`
	Skills        []string       `yaml:"skills"`
}

type packSkill struct {
	Name        string `yaml:"name"`
	Description string `yaml:"description"`
	File        string `yaml:"-"`
}

func parsePack(dir string) (*packSpec, error) {
	raw, err := os.ReadFile(filepath.Join(dir, "pack.yaml"))
	if err != nil {
		return nil, fmt.Errorf("read pack.yaml: %w", err)
	}
	var p packSpec
	if err := yaml.Unmarshal(raw, &p); err != nil {
		return nil, fmt.Errorf("parse pack.yaml: %w", err)
	}
	if p.APIVersion != "pack/v0" && p.APIVersion != "pack/v1" {
		return nil, fmt.Errorf("apiVersion must be pack/v0 or pack/v1 (got %q)", p.APIVersion)
	}
	if p.APIVersion == "pack/v1" {
		if err := validateV1ToolDecls(&p); err != nil {
			return nil, err
		}
		if err := validateV1UISlots(&p); err != nil {
			return nil, err
		}
	} else {
		// v0 行为不变：slots 是 v1 语义——yaml 非 strict 解析已带出该段，这里显式丢弃。
		p.UI.Slots = nil
	}
	// entry url_template 与 slots 同一安全纪律（FP-15 P1-3）：绝对 http(s) 才放行。
	// v0/v1 都查（fail the install，早于 service 侧 PUT 的 400）；空模板留给
	// install 的"无 entry 不注册"分支。
	if p.UI.Entry != nil && p.UI.Entry.URLTemplate != "" {
		if err := validateAbsHTTPURL(p.UI.Entry.URLTemplate); err != nil {
			return nil, fmt.Errorf("pack ui.entry: urlTemplate %w", err)
		}
	}
	if p.Name == "" {
		return nil, fmt.Errorf("pack.name is required")
	}

	agentFiles, _ := filepath.Glob(filepath.Join(dir, "agents", "*.yaml"))
	for _, f := range agentFiles {
		b, err := os.ReadFile(f)
		if err != nil {
			return nil, err
		}
		var a packAgent
		if err := yaml.Unmarshal(b, &a); err != nil {
			return nil, fmt.Errorf("parse %s: %w", f, err)
		}
		p.Agents = append(p.Agents, a)
	}

	skillFiles, _ := filepath.Glob(filepath.Join(dir, "skills", "*", "SKILL.md"))
	for _, f := range skillFiles {
		b, err := os.ReadFile(f)
		if err != nil {
			return nil, err
		}
		sk, err := parseSkillFrontmatter(string(b))
		if err != nil {
			return nil, fmt.Errorf("parse %s: %w", f, err)
		}
		sk.File = string(b)
		p.Skills = append(p.Skills, sk)
	}
	return &p, nil
}

// validateV1UISlots 在解析期静态校验 pack/v1 的 ui.slots 段（前端设计 §1.2/§1.4）：
// slot 标识 v0 只认 workitem.detail.tab（目录冻结，防概念漂移），其他值 fail the install；
// label/urlTemplate 必填；urlTemplate 必须是绝对 http(s) URL（FP-15 P1-3——相对
// 路径渲染成同源 iframe、javascript: 模板继承父源执行）；height 必须在 (0,4096]。
// v0 yaml 出现 slots 段静默忽略（B1 锚定）。
func validateV1UISlots(p *packSpec) error {
	for _, s := range p.UI.Slots {
		if s.Slot == "" {
			return fmt.Errorf("pack/v1 ui.slots: slot is required")
		}
		if s.Slot != SlotWorkItemDetailTab {
			return fmt.Errorf("pack/v1 ui.slots: unknown slot %q (v0 catalog frozen: %s only; dashboard slots deferred)", s.Slot, SlotWorkItemDetailTab)
		}
		if s.Label == "" {
			return fmt.Errorf("pack/v1 ui.slots[%s]: label is required", s.Slot)
		}
		if s.URLTemplate == "" {
			return fmt.Errorf("pack/v1 ui.slots[%s]: urlTemplate is required", s.Slot)
		}
		if err := validateAbsHTTPURL(s.URLTemplate); err != nil {
			return fmt.Errorf("pack/v1 ui.slots[%s]: urlTemplate %w", s.Slot, err)
		}
		if s.Height <= 0 || s.Height > 4096 {
			return fmt.Errorf("pack/v1 ui.slots[%s]: height must be in (0,4096] px (got %d)", s.Slot, s.Height)
		}
	}
	return nil
}

// validateAbsHTTPURL 强制 url_template 为绝对 http(s) URL（FP-15 P1-3，与
// orchestration service 侧同规则、自包含拷贝——CLI 不 import 内部包）。
// 相对路径会把 slot 渲染成同源 iframe（sandbox 的 allow-scripts+allow-same-origin
// 组合对同源内容形同虚设），javascript:/data: 模板在 iframe src 继承父源执行。
// 解析后 scheme 必须 http/https 且 host 非空——协议相对（//host/x）与其他 scheme 一律拒绝。
func validateAbsHTTPURL(template string) error {
	u, err := url.Parse(template)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return errors.New("must be an absolute http(s) URL (got " + truncate(template, 80) + ")")
	}
	return nil
}

func parseSkillFrontmatter(s string) (packSkill, error) {
	if !strings.HasPrefix(s, "---") {
		return packSkill{}, fmt.Errorf("missing frontmatter")
	}
	parts := strings.SplitN(strings.TrimPrefix(s, "---"), "---", 2)
	var sk packSkill
	if err := yaml.Unmarshal([]byte(parts[0]), &sk); err != nil {
		return packSkill{}, err
	}
	if sk.Name == "" {
		return packSkill{}, fmt.Errorf("frontmatter missing name")
	}
	return sk, nil
}

// validateV1ToolDecls 在解析期静态校验 pack/v1 的 mcp.tools 声明段（冻结文档 §2）：
// 工具名必须带所属 mcp 前缀；output 提供时必须是 JSON Schema 对象（块标量 JSON
// 或 YAML 映射均可），非对象/空对象即契约错误。
func validateV1ToolDecls(p *packSpec) error {
	for _, m := range p.MCP {
		prefix := m.Name + "_"
		for _, t := range m.Tools {
			if t.Name == "" {
				return fmt.Errorf("pack/v1 mcp %s: tools[].name is required", m.Name)
			}
			if !strings.HasPrefix(t.Name, prefix) {
				return fmt.Errorf("pack/v1 mcp %s: declared tool %q must start with %q", m.Name, t.Name, prefix)
			}
			if t.Output != nil {
				if err := normalizeOutputDecl(m.Name, t.Name, t.Output); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

// normalizeOutputDecl 把 output 声明规范化为 JSON Schema map[string]any。
// YAML 块标量会解成 string（内含 JSON 文本），YAML 映射直接解成 map。
func normalizeOutputDecl(mcpName, toolName string, raw any) error {
	switch v := raw.(type) {
	case string:
		var m map[string]any
		if err := json.Unmarshal([]byte(v), &m); err != nil {
			return fmt.Errorf("pack/v1 mcp %s tool %s: output must be a JSON Schema object (block-scalar JSON or YAML mapping): %v", mcpName, toolName, err)
		}
		if len(m) == 0 {
			return fmt.Errorf("pack/v1 mcp %s tool %s: output schema must not be empty", mcpName, toolName)
		}
		return nil
	case map[string]any:
		if len(v) == 0 {
			return fmt.Errorf("pack/v1 mcp %s tool %s: output schema must not be empty", mcpName, toolName)
		}
		return nil
	default:
		return fmt.Errorf("pack/v1 mcp %s tool %s: output must be a JSON Schema object, got %T", mcpName, toolName, raw)
	}
}

// outputDeclSchema 返回规范化后的声明 schema（调用前提：validateV1ToolDecls 已通过）。
func (t packMCPTool) outputDeclSchema() map[string]any {
	switch v := t.Output.(type) {
	case string:
		var m map[string]any
		_ = json.Unmarshal([]byte(v), &m)
		return m
	case map[string]any:
		return v
	default:
		return nil
	}
}

// declaredOutput 按 mcp.tools 声明取某工具的输出 schema；ok=false = 未声明或声明无 output。
func (m packMCP) declaredOutput(toolName string) (map[string]any, bool) {
	for _, t := range m.Tools {
		if t.Name == toolName {
			if t.Output == nil {
				return nil, false
			}
			s := t.outputDeclSchema()
			return s, len(s) > 0
		}
	}
	return nil, false
}

type apiClient struct {
	Base, WS, PAT string
	HC            *http.Client
}

func (c *apiClient) do(method, path string, body any, out any) error {
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, c.Base+path, rd)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.PAT)
	req.Header.Set("X-Workspace-Id", c.WS)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.HC.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if resp.StatusCode >= 300 {
		return fmt.Errorf("%s %s → %d: %s", method, path, resp.StatusCode, truncate(string(b), 200))
	}
	if out != nil && len(b) > 0 {
		return json.Unmarshal(b, out)
	}
	return nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

type credsFlag struct{ m map[string]string }

func (f *credsFlag) String() string { return "" }
func (f *credsFlag) Set(v string) error {
	k, val, ok := strings.Cut(v, "=")
	if !ok || k == "" {
		return fmt.Errorf("credential form is KEY=VALUE (got %q)", v)
	}
	if f.m == nil {
		f.m = map[string]string{}
	}
	f.m[k] = val
	return nil
}

type listMCP struct {
	Servers []struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
}
type listAgents struct {
	Agents []struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
}
type listSkills struct {
	Skills []struct {
		ID     string `json:"id"`
		Name   string `json:"name"`
		Origin string `json:"origin"`
	}
}
type listSubs struct {
	Items []struct {
		ID  string `json:"id"`
		URL string `json:"url"`
	}
}

func install(ctx context.Context, c *apiClient, p *packSpec, creds map[string]string, enforcePrefix bool, frameOrigin string) error {

	// 第 0 步（pack 一等公民设计 §4.3 R3）：upsert pack 聚合根，拿 packID。
	// 后续所有资产创建（MCP/agents/skills）与订阅关联都挂这个 id——
	// 服务端按 workspace+name 幂等 upsert，重装即覆盖 version 迁入新结构。
	version := orDefault(p.Version, "0.0.0")
	var upserted struct {
		ID string `json:"id"`
	}
	if err := c.do("PUT", "/api/packs", map[string]any{"name": p.Name, "version": version}, &upserted); err != nil {
		return fmt.Errorf("upsert pack %s: %w", p.Name, err)
	}
	packID := upserted.ID
	if packID == "" {
		return fmt.Errorf("upsert pack %s: server returned no pack id (server side PUT /api/packs not upgraded?)", p.Name)
	}
	fmt.Printf("- pack %s@%s upserted (%s)\n", p.Name, version, packID)

	var mcpList listMCP
	if err := c.do("GET", "/api/workspaces/"+c.WS+"/mcp-servers", nil, &mcpList); err != nil {
		return fmt.Errorf("list MCP registrations: %w", err)
	}
	mcpByName := map[string]string{}
	for _, it := range mcpList.Servers {
		mcpByName[it.Name] = it.ID
	}
	for _, m := range p.MCP {
		if _, ok := mcpByName[m.Name]; ok {
			fmt.Printf("- mcp %s already exists, skipped\n", m.Name)
			continue
		}
		body := map[string]any{"name": m.Name, "transport": m.Transport, "url": m.URL,
			"credentials": map[string]any{}, "pack_id": packID}
		if m.CredentialRef != "" {
			val, ok := creds[m.CredentialRef]
			if !ok {
				return fmt.Errorf("mcp %s references credential %s which was not provided (--credential %s=VALUE)", m.Name, m.CredentialRef, m.CredentialRef)
			}
			header := m.Header
			if header == "" {
				header = "Authorization"
			}
			body["credentials"] = map[string]any{"headers": map[string]any{header: "Bearer " + val}}
		}
		if err := c.do("POST", "/api/workspaces/"+c.WS+"/mcp-servers", body, nil); err != nil {
			return fmt.Errorf("register mcp %s: %w", m.Name, err)
		}
		fmt.Printf("- mcp %s registered\n", m.Name)
	}

	// 安装期闸门（冻结文档 §3）：搭在前缀握手路径上，一次握手依次判定。
	// v0：只查前缀（行为完全不变）；v1：前缀照查（可豁免）+ output/job-handle
	// 两新闸门（不可豁免）+ schema 存档。多 server pack 的存档行聚合后单次
	// PUT（服务端 Replace 是 pack 级 delete+insert，逐 server PUT 会互相清掉）。
	isV1 := p.APIVersion == "pack/v1"
	var allArchived []archivedToolSchema
	for _, m := range p.MCP {
		opt := gateOptions{prefixCheck: enforcePrefix}
		if isV1 {
			opt.enforceContracts = true
		}
		archive, err := handshakeAndGate(ctx, m, creds, opt)
		if err != nil {
			return err
		}
		if len(archive) > 0 {
			fmt.Printf("- mcp %s passed output contract gates (%d tools)\n", m.Name, len(archive))
			allArchived = append(allArchived, archive...)
		}
	}
	if isV1 && len(p.MCP) > 0 {
		if err := archiveToolSchemas(ctx, c, p.Name, allArchived); err != nil {
			return err
		}
		if len(allArchived) > 0 {
			fmt.Printf("- pack %s archived %d tool output schemas\n", p.Name, len(allArchived))
		}
	}

	var agList listAgents
	if err := c.do("GET", "/api/agents", nil, &agList); err != nil {
		return fmt.Errorf("list agents: %w", err)
	}
	agentID := map[string]string{}
	for _, a := range agList.Agents {
		agentID[a.Name] = a.ID
	}
	for _, a := range p.Agents {
		if _, ok := agentID[a.Name]; ok {
			fmt.Printf("- agent %s already exists, skipped\n", a.Name)
			continue
		}
		mc := a.RuntimeConfig
		if mc == nil {
			mc = map[string]any{}
		}
		body := map[string]any{"name": a.Name, "description": a.Description,
			"runtime_config": mc, "visibility": orDefault(a.Visibility, "public"),
			"max_concurrent_tasks": orDefaultInt(a.MaxConcurrent, 6), "pack_id": packID}
		var created struct {
			ID string `json:"id"`
		}
		if err := c.do("POST", "/api/agents", body, &created); err != nil {
			return fmt.Errorf("create agent %s: %w", a.Name, err)
		}
		agentID[a.Name] = created.ID
		fmt.Printf("- agent %s created\n", a.Name)
	}

	var skList listSkills
	if err := c.do("GET", "/api/skills", nil, &skList); err != nil {
		return fmt.Errorf("list skills: %w", err)
	}
	skillID := map[string]string{}
	skillOrigin := map[string]string{}
	for _, s := range skList.Skills {
		skillID[s.Name] = s.ID
		skillOrigin[s.Name] = s.Origin
	}
	for _, s := range p.Skills {
		if _, ok := skillID[s.Name]; ok {
			// 同名碰撞裁决（设计 §6）：已存在且来源非 pack = 用户手工技能，
			// fail 提示而非静默 skip（静默 skip 会把手工技能误记进 pack 资产，
			// uninstall 时被连带误删）；origin=pack（本 pack 或他 pack 先前装的）幂等 skip。
			if skillOrigin[s.Name] != "pack" {
				return fmt.Errorf("skill %s already exists as a %s skill — rename it or the pack skill (same-name collision: manual skills are never silently adopted by a pack)", s.Name, orDefault(skillOrigin[s.Name], "manual"))
			}
			fmt.Printf("- skill %s already exists (origin=pack), skipped\n", s.Name)
			continue
		}
		var created struct {
			ID string `json:"id"`
		}
		if err := c.do("POST", "/api/skills", map[string]any{"name": s.Name, "description": s.Description,
			"origin": "pack", "origin_ref": packID}, &created); err != nil {
			return fmt.Errorf("create skill %s: %w", s.Name, err)
		}
		skillID[s.Name] = created.ID
		if err := c.do("PUT", "/api/skills/"+created.ID+"/files",
			map[string]any{"path": "SKILL.md", "content": s.File}, nil); err != nil {
			return fmt.Errorf("upload SKILL.md %s: %w", s.Name, err)
		}
		fmt.Printf("- skill %s created (with SKILL.md)\n", s.Name)
	}
	for _, a := range p.Agents {
		aid, ok := agentID[a.Name]
		if !ok {
			continue
		}
		for _, sname := range a.Skills {
			sid, ok := skillID[sname]
			if !ok {
				return fmt.Errorf("agent %s references skill %s which is not in the pack", a.Name, sname)
			}
			if err := c.do("POST", "/api/agents/"+aid+"/skills/add", map[string]any{"skill_id": sid}, nil); err != nil {
				return fmt.Errorf("bind skill %s → agent %s: %w", sname, a.Name, err)
			}
		}
	}

	var subList listSubs
	if err := c.do("GET", "/api/workspaces/"+c.WS+"/webhooks/subscriptions", nil, &subList); err != nil {
		return fmt.Errorf("list subscriptions: %w", err)
	}
	subURL := map[string]bool{}
	for _, s := range subList.Items {
		subURL[s.URL] = true
	}
	for _, ev := range p.Events {
		if subURL[ev.URL] {
			fmt.Printf("- subscription %s already exists, skipped\n", ev.URL)
			// 已存在的订阅（按 URL 去重的分支）也要关联到 pack（幂等；连接表
			// UNIQUE(pack_id, subscription_id)，重复关联无害）。
			if err := linkSubscriptions(ctx, c, packID, ev.URL); err != nil {
				return err
			}
			continue
		}
		var created struct {
			Subscription struct {
				ID string `json:"id"`
			} `json:"subscription"`
			// Secret 是创建响应里唯一一次回显的验签密钥（服务端只存 sealx
			// 封存版，此后任何接口不再返回）。丢掉它厂商接收端就无法验签，
			// install 完成时必须完整打印给 pack 作者。
			Secret string `json:"secret"`
		}
		if err := c.do("POST", "/api/workspaces/"+c.WS+"/webhooks/subscriptions",
			map[string]any{"url": ev.URL, "events": ev.Types, "description": "pack " + p.Name}, &created); err != nil {
			return fmt.Errorf("create subscription %s: %w", ev.URL, err)
		}
		if created.Subscription.ID == "" {
			return fmt.Errorf("create subscription %s: server returned no subscription id", ev.URL)
		}
		if err := c.do("PUT", "/api/packs/"+packID+"/subscriptions",
			map[string]any{"subscription_id": created.Subscription.ID}, nil); err != nil {
			return fmt.Errorf("link subscription %s to pack %s: %w", ev.URL, p.Name, err)
		}
		fmt.Printf("- subscription %s created (%d event types)\n", ev.URL, len(ev.Types))
		fmt.Printf("  signing secret (shown once, give it to the receiver operator of %s):\n\n    %s\n\n", ev.URL, created.Secret)
		if created.Secret == "" {
			fmt.Printf("  ! server returned an empty signing secret — rotate-secret on %s to mint one\n", ev.URL)
		}
	}

	if p.UI.Entry != nil && p.UI.Entry.URLTemplate != "" {
		label := p.UI.Entry.Label
		if label == "" {
			label = p.Name
		}
		// slots 随 entry 同一 PUT 落库（前端设计 §1.2 最小路径：entry 单数保持 + slots 追加）。
		// v0 pack 无 slots（解析期静默忽略），PUT body 不带 slots 字段（服务端保持 nil）。
		var slots []packUISlot
		if p.APIVersion == "pack/v1" {
			slots = p.UI.Slots
		}
		// frame-ancestors 探测（前端设计 §1.4）：逐 slot GET urlTemplate 验 CSP
		// frame-ancestors 含 orkome origin；不合规剔除该 slot（pack 其余件照常装）。
		// --frame-origin 空 = 第一方豁免（v0 默认，探测代码到位但不拦截）。
		if frameOrigin != "" && len(slots) > 0 {
			kept := slots[:0]
			for _, s := range slots {
				ok, err := probeFrameAncestors(ctx, s.URLTemplate, frameOrigin, c.HC)
				if err != nil {
					fmt.Printf("! slot %s[%s]: frame-ancestors probe failed (%v) — kept (probe failure treated as exempt, see handoff)\n", p.Name, s.Slot, err)
					kept = append(kept, s)
					continue
				}
				if !ok {
					fmt.Printf("! slot %s[%s]: CSP frame-ancestors does not allow %s — slot rejected (pack continues)\n", p.Name, s.Slot, frameOrigin)
					continue
				}
				kept = append(kept, s)
			}
			slots = kept
		}
		body := map[string]any{"pack": p.Name, "pack_id": packID, "label": label, "url_template": p.UI.Entry.URLTemplate}
		if len(slots) > 0 {
			wire := make([]map[string]any, 0, len(slots))
			for _, s := range slots {
				wire = append(wire, map[string]any{
					"slot": s.Slot, "label": s.Label,
					"url_template": s.URLTemplate, "height": s.Height,
				})
			}
			body["slots"] = wire
		}
		if err := c.do("PUT", "/api/ui-entries/"+p.Name, body, nil); err != nil {
			return fmt.Errorf("register deep link entry: %w", err)
		}
		if len(slots) > 0 {
			fmt.Printf("- deep link entry %s registered (%s, %d slot(s))\n", p.Name, label, len(slots))
		} else {
			fmt.Printf("- deep link entry %s registered (%s)\n", p.Name, label)
		}
	} else if len(p.UI.Slots) > 0 {
		return fmt.Errorf("pack/v1 ui.slots requires ui.entry (slots ride the same PUT /api/ui-entries path)")
	}
	fmt.Printf("pack %s@%s installed\n", p.Name, p.Version)
	return nil
}

// linkSubscriptions 把某个已存在的订阅（按 URL 匹配）关联到 pack 的连接表
// （设计 §1.2 D3 解法）。URL 命中多行时全关联（幂等，UNIQUE 约束去重）。
func linkSubscriptions(_ context.Context, c *apiClient, packID, url string) error {
	var subList listSubs
	if err := c.do("GET", "/api/workspaces/"+c.WS+"/webhooks/subscriptions", nil, &subList); err != nil {
		return fmt.Errorf("list subscriptions for linking: %w", err)
	}
	for _, s := range subList.Items {
		if s.URL != url || s.ID == "" {
			continue
		}
		if err := c.do("PUT", "/api/packs/"+packID+"/subscriptions",
			map[string]any{"subscription_id": s.ID}, nil); err != nil {
			return fmt.Errorf("link subscription %s to pack: %w", url, err)
		}
	}
	return nil
}

// checkPrefix 是 v0 时代的前缀单查入口（保留给既有测试/独立调用）；
// install 路径走 handshakeAndGate 的一次握手多闸门。
func checkPrefix(ctx context.Context, m packMCP, creds map[string]string) error {
	_, err := handshakeAndGate(ctx, m, creds, gateOptions{prefixCheck: true})
	return err
}

// gateOptions 控制一次握手内跑哪些闸门（冻结文档 §3）：
// v0 走 {prefixCheck: enforcePrefix}——行为与旧版完全一致；
// v1 追加 enforceContracts（output 闸门 + job-handle 三件套 + 存档清单），
// 且不随 --no-prefix-check 豁免（legacy 豁免只针对前缀纪律）。
type gateOptions struct {
	prefixCheck      bool // 闸门 1：前缀检查（--no-prefix-check 豁免）
	enforceContracts bool // 闸门 2+3：v1 的 output 契约与 job-handle 三件套（不可豁免）
}

// archivedToolSchema 是存档清单条目，形状与 PUT /api/pack-tool-schemas 的 item 一致
// （冻结文档 §5/§7）。
type archivedToolSchema struct {
	Server       string         `json:"server"`
	ToolName     string         `json:"tool_name"`
	OutputSchema map[string]any `json:"output_schema"`
	Source       string         `json:"source"` // protocol | pack_yaml
}

func handshakeAndGate(ctx context.Context, m packMCP, creds map[string]string, opt gateOptions) ([]archivedToolSchema, error) {
	headers := map[string]string{}
	if m.CredentialRef != "" {
		h := m.Header
		if h == "" {
			h = "Authorization"
		}
		headers[h] = "Bearer " + creds[m.CredentialRef]
	}
	cli, err := client.NewStreamableHttpClient(m.URL, transport.WithHTTPHeaders(headers))
	if err != nil {
		return nil, fmt.Errorf("prefix check mcp %s: %w", m.Name, err)
	}
	defer cli.Close()
	cctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := cli.Start(cctx); err != nil {
		return nil, fmt.Errorf("prefix check mcp %s unreachable (install requires the endpoint online): %w", m.Name, err)
	}
	if _, err := cli.Initialize(cctx, mcpgo.InitializeRequest{Params: mcpgo.InitializeParams{
		ProtocolVersion: mcpgo.LATEST_PROTOCOL_VERSION,
		ClientInfo:      mcpgo.Implementation{Name: "pack-provision", Version: "1.0"},
	}}); err != nil {
		return nil, fmt.Errorf("prefix check mcp %s initialize: %w", m.Name, err)
	}
	list, err := cli.ListTools(cctx, mcpgo.ListToolsRequest{})
	if err != nil {
		return nil, fmt.Errorf("prefix check mcp %s list: %w", m.Name, err)
	}
	prefix := m.Name + "_"

	// 闸门 1：前缀纪律——工具名必须以 <server>_ 开头（--no-prefix-check 豁免，legacy server）。
	if opt.prefixCheck {
		for _, t := range list.Tools {
			if !strings.HasPrefix(t.Name, prefix) {
				return nil, fmt.Errorf("tool name prefix violation: mcp %s exposes %q which must start with %q (pack discipline: prefix = server name)", m.Name, t.Name, prefix)
			}
		}
	}

	var archive []archivedToolSchema
	if opt.enforceContracts {
		// 闸门 2（v1 新增）：output 契约——协议自描述或 pack.yaml 声明，两者皆缺 = fail the install。
		for _, t := range list.Tools {
			if t.OutputSchema.Type != "" {
				archive = append(archive, archivedToolSchema{Server: m.Name, ToolName: t.Name,
					OutputSchema: outputSchemaToMap(t), Source: "protocol"})
				continue
			}
			decl, ok := m.declaredOutput(t.Name)
			if !ok {
				return nil, fmt.Errorf("tool output contract violation: mcp %s tool %q has neither a protocol self-described outputSchema nor a pack.yaml mcp.tools output declaration (pack/v1 requires at least one)", m.Name, t.Name)
			}
			archive = append(archive, archivedToolSchema{Server: m.Name, ToolName: t.Name,
				OutputSchema: decl, Source: "pack_yaml"})
		}
		// pack.yaml 声明了 server 不存在的工具名 = 契约错误（拼写防呆）。
		exposed := map[string]bool{}
		for _, t := range list.Tools {
			exposed[t.Name] = true
		}
		for _, t := range m.Tools {
			if !exposed[t.Name] {
				return nil, fmt.Errorf("pack/v1 mcp %s declares tool %q which the server does not expose (check spelling)", m.Name, t.Name)
			}
		}
		// 闸门 3（v1 新增）：job-handle 三件套——有 submit 形状工具则 job_status/cancel_job 必须齐全。
		if err := checkJobHandleTrio(m.Name, prefix, list.Tools); err != nil {
			return nil, err
		}
	}
	if len(list.Tools) > 0 {
		fmt.Printf("- mcp %s prefix check passed (%d tools)\n", m.Name, len(list.Tools))
	}
	return archive, nil
}

// checkJobHandleTrio 判定 job-handle 三件套（冻结文档 §3.3）：
// 存在 <pack>_submit_* 工具时，<pack>_job_status 与 <pack>_cancel_job 必须同时在场。
func checkJobHandleTrio(mcpName, prefix string, tools []mcpgo.Tool) error {
	hasSubmit := false
	hasStatus, hasCancel := false, false
	for _, t := range tools {
		switch {
		case strings.HasPrefix(t.Name, prefix+"submit_"):
			hasSubmit = true
		case t.Name == prefix+"job_status":
			hasStatus = true
		case t.Name == prefix+"cancel_job":
			hasCancel = true
		}
	}
	if hasSubmit && !(hasStatus && hasCancel) {
		missing := ""
		if !hasStatus {
			missing = prefix + "job_status"
		}
		if !hasCancel {
			if missing != "" {
				missing += " and "
			}
			missing += prefix + "cancel_job"
		}
		return fmt.Errorf("job-handle contract violation: mcp %s exposes %ssubmit_* tools but is missing %s (pack/v1 requires the full trio: submit/job_status/cancel_job)", mcpName, prefix, missing)
	}
	return nil
}

// outputSchemaToMap 把 mcp-go Tool 的自描述 outputSchema（结构体或 RawOutputSchema）转为 map。
func outputSchemaToMap(t mcpgo.Tool) map[string]any {
	if t.RawOutputSchema != nil {
		var m map[string]any
		if err := json.Unmarshal(t.RawOutputSchema, &m); err == nil && len(m) > 0 {
			return m
		}
	}
	if t.OutputSchema.Type != "" {
		b, err := json.Marshal(t.OutputSchema)
		if err != nil {
			return nil
		}
		var m map[string]any
		if err := json.Unmarshal(b, &m); err != nil {
			return nil
		}
		return m
	}
	return nil
}

// probeFrameAncestors 在 slot 注册时 GET slot URL 验 CSP frame-ancestors 是否放行
// orkome origin（前端设计 §1.4）。返回 (放行?, 错误)——错误与判定分离：
// 网络错/读不了头属于"未知"，调用方决定豁免还是拒绝（install 侧选豁免，见调用处）。
// 判定规则：响应头 Content-Security-Policy 的 frame-ancestors 指令值需包含 origin
// 或通配（*）；无 CSP 头/无 frame-ancestors 指令 = 不放行（iframe 会被浏览器默认
// 同源策略拦不住，但 orkome 主站有 frame-ancestors 'none'，第三方必须显式声明）。
func probeFrameAncestors(ctx context.Context, urlTemplate, origin string, hc *http.Client) (bool, error) {
	// 模板变量先替换成哑值再 GET：避免 URL 里出现 {workItemId} 这类非法字符。
	u := strings.NewReplacer(
		"{workspaceId}", "probe", "{uiToken}", "probe", "{workItemId}", "probe", "{jobId}", "probe",
	).Replace(urlTemplate)
	cctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(cctx, http.MethodGet, u, nil)
	if err != nil {
		return false, err
	}
	resp, err := hc.Do(req)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<16))
	csp := resp.Header.Get("Content-Security-Policy")
	if csp == "" {
		return false, nil
	}
	for _, directive := range strings.Split(csp, ";") {
		d := strings.TrimSpace(directive)
		if !strings.HasPrefix(d, "frame-ancestors") {
			continue
		}
		for _, src := range strings.Fields(strings.TrimSpace(strings.TrimPrefix(d, "frame-ancestors"))) {
			if src == "*" || src == origin {
				return true, nil
			}
		}
		return false, nil
	}
	return false, nil
}

func uninstall(ctx context.Context, c *apiClient, p *packSpec) error {
	_ = ctx

	// v1 pack：清掉 schema 存档行（幂等；v0 无存档，删也是 no-op）。
	if err := c.do("DELETE", "/api/pack-tool-schemas/"+p.Name, nil, nil); err == nil {
		fmt.Printf("- tool schema archive %s deleted\n", p.Name)
	}

	// 首选路径（pack 一等公民设计 §4.3 R3 修 D1/D2/D3）：按 pack 聚合根定向
	// 清理——详情聚合返回的 agents/skills/subscriptions IDs 精确命中，不再按
	// 名字猜（按名匹配会误伤同名手工资产）。
	if pk, ok := findPackByName(c, p.Name); ok {
		cleanPackByID(c, pk)
		if p.UI.Entry != nil {
			if err := c.do("DELETE", "/api/ui-entries/"+p.Name, nil, nil); err == nil {
				fmt.Printf("- deep link entry %s deleted\n", p.Name)
			}
		}
		if err := c.do("DELETE", "/api/packs/"+pk.ID, nil, nil); err == nil {
			fmt.Printf("- pack %s (%s) deleted\n", p.Name, pk.ID)
		} else {
			fmt.Printf("! pack %s row delete failed (%v) — assets already cleaned; rerun after server upgrade\n", p.Name, err)
		}
		fmt.Printf("pack %s uninstalled\n", p.Name)
		return nil
	}

	// 兜底路径：GET /api/packs 404（服务端未升级到 pack 实体表）——按旧逻辑
	// 名字匹配清理，并提示。
	fmt.Printf("! pack %s not found via GET /api/packs (server lacks pack registry?) — falling back to name-matched cleanup\n", p.Name)
	return uninstallByName(c, p)
}

// packDetail 是 GET /api/packs/{id} 详情聚合（设计 §1.3）中 uninstall 需要的
// 字段——CLI 只挑用得上的，多余字段忽略。
type packDetail struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Version string `json:"version"`
	// Agents/Skills/Subscriptions 都取 ID 列表做精确清理。
	Agents []struct {
		ID string `json:"id"`
	} `json:"agents"`
	Skills []struct {
		ID string `json:"id"`
	} `json:"skills"`
	Subscriptions []struct {
		ID string `json:"id"`
	} `json:"subscriptions"`
}

// findPackByName GET /api/packs 列表按 name 找 packID；not-found 返回 ok=false
// （列表端点不可用与空列表同归为 not found，走兜底）。
func findPackByName(c *apiClient, name string) (packDetail, bool) {
	var list struct {
		Packs []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"packs"`
	}
	if err := c.do("GET", "/api/packs", nil, &list); err != nil {
		return packDetail{}, false
	}
	for _, it := range list.Packs {
		if it.Name == name {
			var detail packDetail
			if err := c.do("GET", "/api/packs/"+it.ID, nil, &detail); err != nil || detail.ID == "" {
				return packDetail{}, false
			}
			return detail, true
		}
	}
	return packDetail{}, false
}

// cleanPackByID 按 pack 详情聚合的资产 IDs 定向清理：订阅（跨系统显式清）、
// skills（硬删）、agents（归档软删）、MCP（pack_id 反查，删除后行置空由
// 迁移语义决定——这里直接删除注册行，与旧 uninstall 行为一致）。
func cleanPackByID(c *apiClient, pk packDetail) {
	for _, s := range pk.Subscriptions {
		if s.ID == "" {
			continue
		}
		if err := c.do("DELETE", "/api/workspaces/"+c.WS+"/webhooks/subscriptions/"+s.ID, nil, nil); err == nil {
			fmt.Printf("- subscription %s deleted\n", s.ID)
		}
	}
	for _, s := range pk.Skills {
		if s.ID == "" {
			continue
		}
		if err := c.do("DELETE", "/api/skills/"+s.ID, nil, nil); err == nil {
			fmt.Printf("- skill %s deleted\n", s.ID)
		}
	}
	for _, a := range pk.Agents {
		if a.ID == "" {
			continue
		}
		if err := c.do("POST", "/api/agents/"+a.ID+"/archive", nil, nil); err == nil {
			fmt.Printf("- agent %s archived (not hard-deleted)\n", a.ID)
		}
	}
	// MCP 注册行不在详情聚合的删除清单里（独立生命周期），按 pack_id 反查定向删。
	var mcpList struct {
		Servers []struct {
			ID     string `json:"id"`
			Name   string `json:"name"`
			PackID string `json:"pack_id"`
		} `json:"servers"`
	}
	if err := c.do("GET", "/api/workspaces/"+c.WS+"/mcp-servers", nil, &mcpList); err == nil {
		for _, m := range mcpList.Servers {
			if m.PackID != "" && m.PackID == pk.ID {
				if err := c.do("DELETE", "/api/workspaces/"+c.WS+"/mcp-servers/"+m.ID, nil, nil); err == nil {
					fmt.Printf("- mcp %s deleted\n", m.Name)
				}
			}
		}
	}
}

// uninstallByName 旧版按名匹配清理（服务端无 pack 注册表时的兜底路径）。
func uninstallByName(c *apiClient, p *packSpec) error {
	var subList listSubs
	if err := c.do("GET", "/api/workspaces/"+c.WS+"/webhooks/subscriptions", nil, &subList); err != nil {
		return err
	}
	wantURL := map[string]bool{}
	for _, ev := range p.Events {
		wantURL[ev.URL] = true
	}
	for _, s := range subList.Items {
		if wantURL[s.URL] {
			if err := c.do("DELETE", "/api/workspaces/"+c.WS+"/webhooks/subscriptions/"+s.ID, nil, nil); err == nil {
				fmt.Printf("- subscription %s deleted\n", s.URL)
			}
		}
	}

	var skList listSkills
	if err := c.do("GET", "/api/skills", nil, &skList); err != nil {
		return err
	}
	wantSkill := map[string]bool{}
	for _, s := range p.Skills {
		wantSkill[s.Name] = true
	}
	for _, s := range skList.Skills {
		if wantSkill[s.Name] {
			if err := c.do("DELETE", "/api/skills/"+s.ID, nil, nil); err == nil {
				fmt.Printf("- skill %s deleted\n", s.Name)
			}
		}
	}

	var agList listAgents
	if err := c.do("GET", "/api/agents", nil, &agList); err != nil {
		return err
	}
	wantAgent := map[string]bool{}
	for _, a := range p.Agents {
		wantAgent[a.Name] = true
	}
	for _, a := range agList.Agents {
		if wantAgent[a.Name] {
			if err := c.do("POST", "/api/agents/"+a.ID+"/archive", nil, nil); err == nil {
				fmt.Printf("- agent %s archived (not hard-deleted)\n", a.Name)
			}
		}
	}

	var mcpList listMCP
	if err := c.do("GET", "/api/workspaces/"+c.WS+"/mcp-servers", nil, &mcpList); err != nil {
		return err
	}
	wantMCP := map[string]bool{}
	for _, m := range p.MCP {
		wantMCP[m.Name] = true
	}
	for _, m := range mcpList.Servers {
		if wantMCP[m.Name] {
			if err := c.do("DELETE", "/api/workspaces/"+c.WS+"/mcp-servers/"+m.ID, nil, nil); err == nil {
				fmt.Printf("- mcp %s deleted\n", m.Name)
			}
		}
	}

	if p.UI.Entry != nil {
		if err := c.do("DELETE", "/api/ui-entries/"+p.Name, nil, nil); err == nil {
			fmt.Printf("- deep link entry %s deleted\n", p.Name)
		}
	}
	fmt.Printf("pack %s uninstalled\n", p.Name)
	return nil
}

// archiveToolSchemas 把闸门产出的存档清单写入平台（PUT /api/pack-tool-schemas/<pack>，
// 幂等覆盖，冻结文档 §3.4/§7）。
func archiveToolSchemas(ctx context.Context, c *apiClient, pack string, items []archivedToolSchema) error {
	_ = ctx
	if err := c.do("PUT", "/api/pack-tool-schemas/"+pack, map[string]any{"items": items}, nil); err != nil {
		return fmt.Errorf("archive tool schemas: %w", err)
	}
	return nil
}

func orDefault(s, def string) string {
	if s == "" {
		return def
	}
	return s
}

func orDefaultInt(v, def int) int {
	if v == 0 {
		return def
	}
	return v
}
