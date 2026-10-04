package engine

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	einoextmcp "github.com/cloudwego/eino-ext/components/tool/mcp"
	"github.com/cloudwego/eino/components/tool"
	mcpclient "github.com/mark3labs/mcp-go/client"
	mcptransport "github.com/mark3labs/mcp-go/client/transport"
	mcpgo "github.com/mark3labs/mcp-go/mcp"

	orchapp "orkome/internal/orchestration/app"
	"orkome/internal/platform/egressx"
)

const (
	mcpServerSetupBudget = 10 * time.Second
	mcpToolsPerServerMax = 32

	mcpCacheIdleTTL = 5 * time.Minute
)

var mcpCatalog = struct {
	sync.Mutex
	entries map[string]*mcpCacheEntry
}{entries: map[string]*mcpCacheEntry{}}

type mcpCacheEntry struct {
	cli      *mcpclient.Client
	tools    []tool.BaseTool
	lastUsed time.Time
}

func mcpCacheKey(srv orchapp.ResolvedMCPServer, headers map[string]string) string {
	keys := make([]string, 0, len(headers))
	for k := range headers {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	b.WriteString(strings.ToLower(strings.TrimSpace(srv.Transport)))
	b.WriteString("\x00")
	b.WriteString(srv.URL)
	for _, k := range keys {
		b.WriteString("\x00")
		b.WriteString(k)
		b.WriteString("=")
		b.WriteString(headers[k])
	}
	return b.String()
}

func mcpCatalogGet(key string) (*mcpCacheEntry, bool) {
	mcpCatalog.Lock()
	defer mcpCatalog.Unlock()
	e, ok := mcpCatalog.entries[key]
	if !ok {
		return nil, false
	}
	e.lastUsed = time.Now()
	return e, true
}

func mcpCatalogPut(key string, cli *mcpclient.Client, tools []tool.BaseTool) {
	mcpCatalog.Lock()
	defer mcpCatalog.Unlock()

	if old, ok := mcpCatalog.entries[key]; ok {
		_ = old.cli.Close()
	}
	mcpCatalog.entries[key] = &mcpCacheEntry{cli: cli, tools: tools, lastUsed: time.Now()}
}

func mcpCatalogSweep(now time.Time) {
	mcpCatalog.Lock()
	defer mcpCatalog.Unlock()
	for k, e := range mcpCatalog.entries {
		if now.Sub(e.lastUsed) > mcpCacheIdleTTL {
			_ = e.cli.Close()
			delete(mcpCatalog.entries, k)
		}
	}
}

const (
	mcpStatusOK          = "ok"
	mcpStatusSSRF        = "ssrf_rejected"
	mcpStatusConnectFail = "connect_failed"
	mcpStatusDupDropped  = "duplicate_name_dropped"
	mcpStatusListFail    = "list_failed"
)

type mcpBridge struct {
	tools    []tool.BaseTool
	snapshot []orchapp.MCPSnapshotEntry
}

func (e *EinoEngine) buildMCPTools(ctx context.Context, task orchapp.Task, agent orchapp.Agent) *mcpBridge {
	if e.MCPResolver == nil {
		return nil
	}
	servers, err := e.MCPResolver.ResolveMCP(ctx, task.WorkspaceID, agent.ID)
	if err != nil || len(servers) == 0 {
		return nil
	}
	guard := e.Egress
	if guard == nil {
		guard = egressx.DefaultGuard()
	}
	taken := map[string]bool{}
	br := &mcpBridge{}
	mcpCatalogSweep(time.Now())
	for _, srv := range servers {
		entry := orchapp.MCPSnapshotEntry{Name: srv.Name, Transport: srv.Transport, URL: srv.URL}

		if err := guard.ValidateURL(ctx, srv.URL); err != nil {
			entry.Status = mcpStatusSSRF
			entry.Note = err.Error()
			br.snapshot = append(br.snapshot, entry)
			continue
		}
		headers := mcpHeadersOf(srv.Credentials)

		var tools []tool.BaseTool
		if cached, ok := mcpCatalogGet(mcpCacheKey(srv, headers)); ok {
			tools = cached.tools
		} else {
			cli, err := newMCPClient(srv, headers)
			if err != nil {
				entry.Status = mcpStatusConnectFail
				entry.Note = err.Error()
				br.snapshot = append(br.snapshot, entry)
				continue
			}
			tools, err = mcpConnect(ctx, cli, headers)
			if err != nil {
				_ = cli.Close()
				entry.Status = mcpStatusListFail
				entry.Note = err.Error()
				br.snapshot = append(br.snapshot, entry)
				continue
			}
			mcpCatalogPut(mcpCacheKey(srv, headers), cli, tools)
		}
		dup, added := 0, 0
		for _, t := range tools {
			if added >= mcpToolsPerServerMax {
				break
			}
			info, err := t.Info(ctx)
			if err != nil {
				dup++
				continue
			}
			if taken[info.Name] {
				dup++
				continue
			}
			taken[info.Name] = true
			br.tools = append(br.tools, t)
			added++
		}
		entry.ToolCount = added
		switch {
		case added == 0 && dup == 0:
			entry.Status = mcpStatusListFail
			entry.Note = "no tools listed"
		default:
			entry.Status = mcpStatusOK
			if dup > 0 {
				entry.Note = fmt.Sprintf("%d duplicate tool names dropped", dup)
			}
		}
		br.snapshot = append(br.snapshot, entry)
	}

	return br
}

func newMCPClient(srv orchapp.ResolvedMCPServer, headers map[string]string) (*mcpclient.Client, error) {
	switch strings.ToLower(strings.TrimSpace(srv.Transport)) {
	case "", "http", "streamable_http":
		return mcpclient.NewStreamableHttpClient(srv.URL, mcptransport.WithHTTPHeaders(headers))
	case "sse":
		return mcpclient.NewSSEMCPClient(srv.URL, mcptransport.WithHeaders(headers))
	default:
		return nil, fmt.Errorf("unsupported mcp transport %q", srv.Transport)
	}
}

func mcpConnect(ctx context.Context, cli *mcpclient.Client, headers map[string]string) ([]tool.BaseTool, error) {
	cctx, cancel := context.WithTimeout(ctx, mcpServerSetupBudget)
	defer cancel()
	if err := cli.Start(cctx); err != nil {
		return nil, fmt.Errorf("mcp start: %w", err)
	}
	if _, err := cli.Initialize(cctx, mcpgo.InitializeRequest{
		Params: mcpgo.InitializeParams{
			ProtocolVersion: mcpgo.LATEST_PROTOCOL_VERSION,
			ClientInfo:      mcpgo.Implementation{Name: "orkome-engine", Version: "1.0"},
		},
	}); err != nil {
		return nil, fmt.Errorf("mcp initialize: %w", err)
	}
	return einoextmcp.GetTools(cctx, &einoextmcp.Config{Cli: cli, CustomHeaders: headers})
}

func mcpHeadersOf(creds map[string]any) map[string]string {
	raw, ok := creds["headers"].(map[string]any)
	if !ok {
		return nil
	}
	out := make(map[string]string, len(raw))
	for k, v := range raw {
		if s, ok := v.(string); ok {
			out[k] = s
		}
	}
	return out
}
