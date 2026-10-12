package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	einoextmcp "github.com/cloudwego/eino-ext/components/tool/mcp"
	"github.com/cloudwego/eino/components/tool"
	mcpclient "github.com/mark3labs/mcp-go/client"
	mcptransport "github.com/mark3labs/mcp-go/client/transport"
	mcpgo "github.com/mark3labs/mcp-go/mcp"

	"github.com/haozing/ploykit/platform/egressx"
	orchapp "orkome/internal/orchestration/app"
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
	schemas  map[string]json.RawMessage // toolName → 协议自描述 outputSchema（A1 回收）
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

func mcpCatalogPut(key string, cli *mcpclient.Client, tools []tool.BaseTool, schemas map[string]json.RawMessage) {
	mcpCatalog.Lock()
	defer mcpCatalog.Unlock()

	if old, ok := mcpCatalog.entries[key]; ok {
		_ = old.cli.Close()
	}
	mcpCatalog.entries[key] = &mcpCacheEntry{cli: cli, tools: tools, schemas: schemas, lastUsed: time.Now()}
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

	// schemas 是协议自描述 outputSchema（A1 回收，toolName → schema）。
	// C1 起 buildMCPTools 接受 B1 存档（pack_tool_schema）补位：未自描述
	// 的工具用存档 schema，最终生效结果同时落这里（供注册表与快照消费）。
	schemas map[string]json.RawMessage
}

// schemaOf 返回工具最终生效的输出 schema（协议自描述优先，B1 存档补位；
// 无契约返回 nil → 运行期结构化探测回落）。优先级与 A1 冻结文档 §3、
// B1 冻结文档 §6 一致。
func (br *mcpBridge) schemaOf(name string) json.RawMessage {
	if br == nil || br.schemas == nil {
		return nil
	}
	return br.schemas[name]
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
	// C1：B1 存档（pack_tool_schema）一次拉全量，为未自描述工具补契约。
	archived := e.archivedToolSchemas(ctx, task)
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

	validator := e.outputValidator()
	var tools []tool.BaseTool
	var schemas map[string]json.RawMessage
	if cached, ok := mcpCatalogGet(mcpCacheKey(srv, headers)); ok {
		tools = cached.tools
		schemas = cached.schemas
	} else {
		cli, err := newMCPClient(srv, headers)
		if err != nil {
			entry.Status = mcpStatusConnectFail
			entry.Note = err.Error()
			br.snapshot = append(br.snapshot, entry)
			continue
		}
		tools, schemas, err = mcpConnect(ctx, cli, headers)
		if err != nil {
			_ = cli.Close()
			entry.Status = mcpStatusListFail
			entry.Note = err.Error()
			br.snapshot = append(br.snapshot, entry)
			continue
		}
		mcpCatalogPut(mcpCacheKey(srv, headers), cli, tools, schemas)
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
		// A1+C1：输出契约校验装饰器——schema 优先级：协议自描述（缓存随
		// mcpCatalog）→ B1 存档（archived）→ 无（结构化探测回落）；
		// 违规时模型看到标准错误而非未验证结构。注册表 middleware
		// （registry_middleware.go）不重复校验 MCP 工具（origin=mcp 跳过），
		// 两个执行位、同一个 validator。
		schema := schemas[info.Name]
		if len(schema) == 0 {
			schema = archived[info.Name]
		}
		if len(schema) > 0 {
			if br.schemas == nil {
				br.schemas = map[string]json.RawMessage{}
			}
			br.schemas[info.Name] = schema
		}
		br.tools = append(br.tools, wrapWithValidator(t, info.Name, schema, validator, e.auditLogger()))
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
		// D2 遗留坑的运行期修复：vendored mcp-go v0.43 零值 http.Client 的
		// 空闲连接池会复用服务端已关闭的 SSE 连接（IdleConnTimeout=90s），
		// 偶发 list tools 挂到 context deadline（真模型 E2E 间歇实证：
		// 同 URL 一次 ok tool_count=6、一次 list_failed）。禁用 keep-alive
		// 根除复用；任务级工具调用量级下无性能影响。
		hc := &http.Client{Transport: &http.Transport{DisableKeepAlives: true}, Timeout: 60 * time.Second}
		return mcpclient.NewStreamableHttpClient(srv.URL,
			mcptransport.WithHTTPBasicClient(hc),
			mcptransport.WithHTTPHeaders(headers))
	case "sse":
		return mcpclient.NewSSEMCPClient(srv.URL, mcptransport.WithHeaders(headers))
	default:
		return nil, fmt.Errorf("unsupported mcp transport %q", srv.Transport)
	}
}

// mcpConnect 建立连接并取回工具清单。除 eino-ext GetTools 的包装结果外，
// 还回收 mcp-go ListTools 原样返回的每个工具 outputSchema（协议层自描述，
// eino-ext 包装层会丢弃它——A1 在 orkome 桥接层自己存一份
// toolName → outputSchema 注册表映射）。
func mcpConnect(ctx context.Context, cli *mcpclient.Client, headers map[string]string) ([]tool.BaseTool, map[string]json.RawMessage, error) {
	cctx, cancel := context.WithTimeout(ctx, mcpServerSetupBudget)
	defer cancel()
	if err := cli.Start(cctx); err != nil {
		return nil, nil, fmt.Errorf("mcp start: %w", err)
	}
	if _, err := cli.Initialize(cctx, mcpgo.InitializeRequest{
		Params: mcpgo.InitializeParams{
			ProtocolVersion: mcpgo.LATEST_PROTOCOL_VERSION,
			ClientInfo:      mcpgo.Implementation{Name: "orkome-engine", Version: "1.0"},
		},
	}); err != nil {
		return nil, nil, fmt.Errorf("mcp initialize: %w", err)
	}
	schemas, err := collectToolOutputSchemas(cctx, cli, headers)
	if err != nil {
		return nil, nil, fmt.Errorf("mcp list tools: %w", err)
	}
	tools, err := einoextmcp.GetTools(cctx, &einoextmcp.Config{Cli: cli, CustomHeaders: headers})
	if err != nil {
		return nil, nil, fmt.Errorf("mcp get tools: %w", err)
	}
	return tools, schemas, nil
}

// collectToolOutputSchemas 从 mcp-go ListTools 结果回收每个工具的
// outputSchema（Tool 结构自带，协议自描述）。无 schema 的工具不进映射
// （运行期回落结构化探测）。
func collectToolOutputSchemas(ctx context.Context, cli *mcpclient.Client, headers map[string]string) (map[string]json.RawMessage, error) {
	h := http.Header{}
	for k, v := range headers {
		h.Set(k, v)
	}
	lr, err := cli.ListTools(ctx, mcpgo.ListToolsRequest{Header: h})
	if err != nil {
		return nil, err
	}
	schemas := make(map[string]json.RawMessage)
	for _, t := range lr.Tools {
		raw, err := toolOutputSchemaRaw(t)
		if err != nil || len(raw) == 0 {
			continue // 无 schema 或无法还原 → 不注册，运行期回落
		}
		schemas[t.Name] = raw
	}
	return schemas, nil
}

// toolOutputSchemaRaw 把 mcp-go Tool 的 OutputSchema/RawOutputSchema 还原为
// JSON RawMessage（两种形态统一：RawOutputSchema 原样；结构化形态重新序列化）。
func toolOutputSchemaRaw(t mcpgo.Tool) (json.RawMessage, error) {
	if len(t.RawOutputSchema) > 0 {
		return t.RawOutputSchema, nil
	}
	if t.OutputSchema.Type == "" && t.OutputSchema.Properties == nil && len(t.OutputSchema.Required) == 0 {
		return nil, nil
	}
	return json.Marshal(t.OutputSchema)
}

// outputValidator 返回引擎配置的输出校验器；未配置时用 v0 默认两级实现。
func (e *EinoEngine) outputValidator() OutputValidator {
	if e.OutputValidator != nil {
		return e.OutputValidator
	}
	return NewDefaultValidator()
}

// archivedToolSchemas 拉取 B1 存档（pack_tool_schema，迁移 1035）的
// toolName → outputSchema 映射（C1 接线点）。端口未接线或拉取失败 →
// 返回空映射（回落协议自描述/结构化探测，不阻塞任务执行）。
func (e *EinoEngine) archivedToolSchemas(ctx context.Context, task orchapp.Task) map[string]json.RawMessage {
	if e.PackToolSchemas == nil {
		return nil
	}
	list, err := e.PackToolSchemas.ListPackToolSchemas(ctx, task.WorkspaceID)
	if err != nil || len(list) == 0 {
		return nil
	}
	out := make(map[string]json.RawMessage, len(list))
	for _, it := range list {
		if it.ToolName == "" || it.OutputSchema == nil {
			continue
		}
		b, err := json.Marshal(it.OutputSchema)
		if err != nil {
			continue
		}
		out[it.ToolName] = b
	}
	return out
}

// auditLogger 返回违规审计日志器；未配置时用 slog 默认 logger。
func (e *EinoEngine) auditLogger() *slog.Logger {
	if e.AuditLog != nil {
		return e.AuditLog
	}
	return slog.Default()
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
