package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
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
	noPrefix := fs.Bool("no-prefix-check", false, "skip tool name prefix check (legacy server exemption)")
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
		return install(ctx, c, pack, creds.m, !*noPrefix)
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
}

type packCred struct {
	Key         string `yaml:"key"`
	Description string `yaml:"description"`
}

type packMCP struct {
	Name          string `yaml:"name"`
	Transport     string `yaml:"transport"`
	URL           string `yaml:"url"`
	CredentialRef string `yaml:"credentialRef"`
	Header        string `yaml:"header"`
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
	if p.APIVersion != "pack/v0" {
		return nil, fmt.Errorf("apiVersion must be pack/v0 (got %q)", p.APIVersion)
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
		ID   string `json:"id"`
		Name string `json:"name"`
	}
}
type listSubs struct {
	Items []struct {
		ID  string `json:"id"`
		URL string `json:"url"`
	}
}

func install(ctx context.Context, c *apiClient, p *packSpec, creds map[string]string, enforcePrefix bool) error {

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
		body := map[string]any{"name": m.Name, "transport": m.Transport, "url": m.URL, "credentials": map[string]any{}}
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

	if enforcePrefix {
		for _, m := range p.MCP {
			if err := checkPrefix(ctx, m, creds); err != nil {
				return err
			}
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
			"max_concurrent_tasks": orDefaultInt(a.MaxConcurrent, 6)}
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
	for _, s := range skList.Skills {
		skillID[s.Name] = s.ID
	}
	for _, s := range p.Skills {
		if _, ok := skillID[s.Name]; ok {
			fmt.Printf("- skill %s already exists, skipped\n", s.Name)
			continue
		}
		var created struct {
			ID string `json:"id"`
		}
		if err := c.do("POST", "/api/skills", map[string]any{"name": s.Name, "description": s.Description}, &created); err != nil {
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
	if err := c.do("GET", "/api/webhook-subscriptions", nil, &subList); err != nil {
		return fmt.Errorf("list subscriptions: %w", err)
	}
	subURL := map[string]bool{}
	for _, s := range subList.Items {
		subURL[s.URL] = true
	}
	for _, ev := range p.Events {
		if subURL[ev.URL] {
			fmt.Printf("- subscription %s already exists, skipped\n", ev.URL)
			continue
		}
		if err := c.do("POST", "/api/webhook-subscriptions",
			map[string]any{"url": ev.URL, "events": ev.Types, "description": "pack " + p.Name}, nil); err != nil {
			return fmt.Errorf("create subscription %s: %w", ev.URL, err)
		}
		fmt.Printf("- subscription %s created (%d event types)\n", ev.URL, len(ev.Types))
	}

	if p.UI.Entry != nil && p.UI.Entry.URLTemplate != "" {
		label := p.UI.Entry.Label
		if label == "" {
			label = p.Name
		}
		if err := c.do("PUT", "/api/ui-entries/"+p.Name,
			map[string]any{"pack": p.Name, "label": label, "url_template": p.UI.Entry.URLTemplate}, nil); err != nil {
			return fmt.Errorf("register deep link entry: %w", err)
		}
		fmt.Printf("- deep link entry %s registered (%s)\n", p.Name, label)
	}
	fmt.Printf("pack %s@%s installed\n", p.Name, p.Version)
	return nil
}

func checkPrefix(ctx context.Context, m packMCP, creds map[string]string) error {
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
		return fmt.Errorf("prefix check mcp %s: %w", m.Name, err)
	}
	defer cli.Close()
	cctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := cli.Start(cctx); err != nil {
		return fmt.Errorf("prefix check mcp %s unreachable (install requires the endpoint online): %w", m.Name, err)
	}
	if _, err := cli.Initialize(cctx, mcpgo.InitializeRequest{Params: mcpgo.InitializeParams{
		ProtocolVersion: mcpgo.LATEST_PROTOCOL_VERSION,
		ClientInfo:      mcpgo.Implementation{Name: "pack-provision", Version: "1.0"},
	}}); err != nil {
		return fmt.Errorf("prefix check mcp %s initialize: %w", m.Name, err)
	}
	list, err := cli.ListTools(cctx, mcpgo.ListToolsRequest{})
	if err != nil {
		return fmt.Errorf("prefix check mcp %s list: %w", m.Name, err)
	}
	prefix := m.Name + "_"
	for _, t := range list.Tools {
		if !strings.HasPrefix(t.Name, prefix) {
			return fmt.Errorf("tool name prefix violation: mcp %s exposes %q which must start with %q (pack discipline: prefix = server name)", m.Name, t.Name, prefix)
		}
	}
	if len(list.Tools) > 0 {
		fmt.Printf("- mcp %s prefix check passed (%d tools)\n", m.Name, len(list.Tools))
	}
	return nil
}

func uninstall(ctx context.Context, c *apiClient, p *packSpec) error {
	_ = ctx

	var subList listSubs
	if err := c.do("GET", "/api/webhook-subscriptions", nil, &subList); err != nil {
		return err
	}
	wantURL := map[string]bool{}
	for _, ev := range p.Events {
		wantURL[ev.URL] = true
	}
	for _, s := range subList.Items {
		if wantURL[s.URL] {
			if err := c.do("DELETE", "/api/webhook-subscriptions/"+s.ID, nil, nil); err == nil {
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
