package main

// D2：agentblog pack 真实握手联调测试（env 门控，默认跳过）。
//
// 任务书：docs/pack-rollout-plan-2026-10-11.md §4 D2。
// 依赖：兄弟仓 agentblog（D1 交付的 MCP server，只读使用）——测试自起
// `go run ./cmd/full-server`（fake 形态：无 PG、fake cms、fake SERP/LLM），
// 走完整安装与引擎桥接链路，证明 pack 通道端到端可用。
//
// 门控：ORKOME_AGENTBLOG_DIR 指向 agentblog 仓根（如 D:/code/agentblog）。
// 未设置 → t.Skip（跨仓依赖绝不进默认 `go test ./...` 的依赖面）。
//
// 链路（对照 B1/C2/C1/A1 交接）：
//  ① 起 D1 server（随机端口 + 就绪探测 /healthz → /mcp/pack 401）；
//  ② 写临时 pack.yaml（packs/agentblog 的同构副本，url 指向随机端口）；
//  ③ install → fake API 承接 PUT/POST（B1/C2 既有测试模式）；
//     断言：三闸门全过（前缀/output/三件套）、schema 存档 PUT 形状
//     （六工具 source=protocol）、ui entry + slots PUT 形状；
//  ④ 引擎桥接：engine 包的 buildMCPTools 直连该 server（C1 组装路径），
//     断言桥接快照含六个 agentblog 工具、outputSchema 回收成功
//     （注册表 has_output_schema=true、concurrency_safe=false）；
//  ⑤ A1 校验器：调一次 fake submit 生成 job → job_status 拿真输出 →
//     validator 按冻结 schema 判定合法 + 篡改 state 枚举判违规（反向锚）。

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/haozing/ploykit/platform/egressx"
	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/client/transport"
	mcpgo "github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"orkome/internal/orchestration/adapters/engine"
	orchapp "orkome/internal/orchestration/app"
)

const agentblogPackYAML = `apiVersion: pack/v1
name: agentblog
version: 0.1.0
credentials:
  - key: AGENTBLOG_CALLBACK_TOKEN
mcp:
  - name: agentblog
    transport: streamable_http
    url: %s/mcp/pack
    credentialRef: AGENTBLOG_CALLBACK_TOKEN
events:
  - url: %s/hooks/orkome
    types: ["work_item.updated"]
ui:
  entry:
    label: 内容工厂
    urlTemplate: %s/console?ws={workspaceId}&t={uiToken}
  slots:
    - slot: workitem.detail.tab
      label: 文章预览
      urlTemplate: %s/preview?item={workItemId}&t={uiToken}
      height: 480
`

// agentblogToolNames 是 D1 六工具的清单（断言基准，见 D1 交接笔记 §一）。
var agentblogToolNames = []string{
	"agentblog_evaluate_seed",
	"agentblog_query_knowledge",
	"agentblog_submit_generation",
	"agentblog_job_status",
	"agentblog_cancel_job",
	"agentblog_review_result",
}

// freePort 借一个临时监听拿空闲端口（随即释放，存在极小竞态——测试容错）。
func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	p := l.Addr().(*net.TCPAddr).Port
	require.NoError(t, l.Close())
	return p
}

// startAgentblogServer 以 fake 形态起 D1 的 full-server：
// `go run ./cmd/full-server`（AGENTBLOG_MCPACK_TOKEN + AGENTBLOG_HTTP_ADDR 随机端口）。
// 就绪探测两段：/healthz 200（HTTP 起来）→ /mcp/pack 401（pack 端点挂上 auth）。
// 日志落 t.TempDir，测试失败时尾部转储进 -v 输出辅助排查。
func startAgentblogServer(t *testing.T, dir, token string) string {
	t.Helper()
	port := freePort(t)
	addr := fmt.Sprintf("http://127.0.0.1:%d", port)

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	logPath := filepath.Join(t.TempDir(), "agentblog-server.log")
	logF, err := os.Create(logPath)
	require.NoError(t, err)

	gobin, err := exec.LookPath("go")
	require.NoError(t, err, "PATH 里需要 go 工具链")
	cmd := exec.CommandContext(ctx, gobin, "run", "./cmd/full-server")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"AGENTBLOG_MCPACK_TOKEN="+token,
		"AGENTBLOG_HTTP_ADDR=127.0.0.1:"+fmt.Sprint(port),
		"AGENTBLOG_EVENT_INTERVAL_MS=200", // 联调期事件循环快节奏（job 推进及时）
	)
	cmd.Stdout = logF
	cmd.Stderr = logF
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP}
	require.NoError(t, cmd.Start())
	t.Cleanup(func() {
		killTree(cmd) // Windows 下 Process.Kill 只杀 go run，孙进程 full-server.exe 会残留占住日志句柄
		_, _ = cmd.Process.Wait()
		_ = logF.Close()
	})
	t.Cleanup(func() {
		if t.Failed() {
			if b, rerr := os.ReadFile(logPath); rerr == nil {
				t.Logf("agentblog server 日志（尾部）:\n%s", tailLines(string(b), 40))
			}
		}
	})

	hc := &http.Client{Timeout: 2 * time.Second}
	deadline := time.Now().Add(120 * time.Second) // go run 首次编译可能较慢
	for time.Now().Before(deadline) {
		if resp, perr := hc.Get(addr + "/healthz"); perr == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == 200 {
				// pack 端点鉴权形状：无凭证 GET → 401（token 配置后 auth 中间件在位；
				// 503 = token 未生效——配置错误，fail-closed 测试要能抓到）。
				req, _ := http.NewRequestWithContext(ctx, http.MethodGet, addr+"/mcp/pack", nil)
				if r2, e2 := hc.Do(req); e2 == nil {
					_, _ = io.Copy(io.Discard, r2.Body)
					_ = r2.Body.Close()
					if r2.StatusCode == http.StatusUnauthorized {
						return addr
					}
				}
			}
		}
		time.Sleep(300 * time.Millisecond)
	}
	if b, rerr := os.ReadFile(logPath); rerr == nil {
		t.Fatalf("agentblog server 120s 内未就绪（healthz/401 探测失败）。日志尾部:\n%s", tailLines(string(b), 40))
	}
	t.Fatalf("agentblog server 120s 内未就绪，且日志不可读：%s", logPath)
	return ""
}

func tailLines(s string, n int) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}

// killTree 终止 `go run` 的整棵进程树（Windows：taskkill /T /F 按 PID 追溯
// 子进程；非 Windows 退化为杀进程组——go run 的孙进程是 server 本体，只杀
// go run 会留下占住日志句柄的孤儿 server）。
func killTree(cmd *exec.Cmd) {
	if cmd.Process == nil || cmd.ProcessState != nil && cmd.ProcessState.Exited() {
		return
	}
	if runtime.GOOS == "windows" {
		kill := exec.Command("taskkill", "/T", "/F", "/PID", fmt.Sprint(cmd.Process.Pid))
		_ = kill.Run()
		return
	}
	_ = cmd.Process.Kill()
}

// capturedInstalls 捕获 install 对 fake 平台 API 发出的关键请求形状。
type capturedInstalls struct {
	mu          sync.Mutex
	SchemasPUT  []archivedToolSchema // PUT /api/pack-tool-schemas/agentblog 的 items
	UIEntrySeen bool
	UIEntryBody map[string]any
	MCPRegSeen  bool
	MCPRegBody  map[string]any // POST mcp-servers（凭证注入形状）
	SubsSeen    bool
}

// newAgentblogCaptureAPI 起 fake 平台 API（B1/C2 既有测试模式：承接 install
// 的全部 GET/POST/PUT/DELETE，捕获关键 PUT/POST 形状）。
func newAgentblogCaptureAPI(t *testing.T) (*apiClient, *capturedInstalls) {
	t.Helper()
	cap := &capturedInstalls{}
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "PUT" && strings.Contains(r.URL.Path, "/api/pack-tool-schemas/"):
			var body struct {
				Items []archivedToolSchema `json:"items"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			cap.mu.Lock()
			cap.SchemasPUT = body.Items
			cap.mu.Unlock()
		case r.Method == "PUT" && strings.Contains(r.URL.Path, "/api/ui-entries/"):
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			cap.mu.Lock()
			cap.UIEntrySeen = true
			cap.UIEntryBody = body
			cap.mu.Unlock()
		case r.Method == "POST" && strings.HasSuffix(r.URL.Path, "/mcp-servers"):
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			cap.mu.Lock()
			cap.MCPRegSeen = true
			cap.MCPRegBody = body
			cap.mu.Unlock()
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(struct {
				ID string `json:"id"`
			}{ID: "mcp-1"})
			return
		case r.Method == "POST" && strings.HasSuffix(r.URL.Path, "/webhooks/subscriptions"):
			cap.mu.Lock()
			cap.SubsSeen = true
			cap.mu.Unlock()
		}
		w.WriteHeader(200)
	}))
	t.Cleanup(api.Close)
	return &apiClient{Base: api.URL, WS: "ws1", PAT: "tok", HC: &http.Client{Timeout: 15 * time.Second}}, cap
}

func TestAgentblogE2E(t *testing.T) {
	dir := os.Getenv("ORKOME_AGENTBLOG_DIR")
	if dir == "" {
		t.Skip("ORKOME_AGENTBLOG_DIR not set（跨仓联调门控：指向 agentblog 仓根，如 D:/code/agentblog）")
	}
	st, err := os.Stat(filepath.Join(dir, "cmd", "full-server"))
	require.NoError(t, err, "ORKOME_AGENTBLOG_DIR 下应有 cmd/full-server")
	require.True(t, st.IsDir(), "cmd/full-server 应为目录")

	const token = "d2-e2e-pack-token"
	ctx := context.Background()

	// 连接池竞态规避（本机联调特有）：vendored mcp-go v0.43.0 的 streamable
	// 传输用零值 http.Client（共享 http.DefaultTransport 连接池）。跨三个阶段
	// （provision 握手/引擎桥接/会话调用）复用同 host 连接时，若复用到一条
	// 服务端已随 SSE 流结束而关闭的空闲连接，请求会挂起直到连接池
	// IdleConnTimeout(90s) 清理——表现为偶发 90s 级慢/超时（实测约 1/4 概率）。
	// 测试内禁用 keep-alive 复用（每请求新连接）即可稳定；生产引擎是长驻
	// 进程 + 独立部署网络路径，不受本竞态影响（详见交接笔记"遗留坑"）。
	tr, ok := http.DefaultTransport.(*http.Transport)
	require.True(t, ok, "http.DefaultTransport 应为 *http.Transport")
	prevKA := tr.DisableKeepAlives
	tr.DisableKeepAlives = true
	t.Cleanup(func() { tr.DisableKeepAlives = prevKA })

	// ① 起 D1 server（fake 形态）。
	addr := startAgentblogServer(t, dir, token)
	t.Logf("agentblog fake full-server 已就绪：%s/mcp/pack", addr)

	// ② 写临时 pack.yaml（packs/agentblog 的同构副本，url 指向随机端口）。
	packDir := t.TempDir()
	packYAML := fmt.Sprintf(agentblogPackYAML, addr, addr, addr, addr)
	require.NoError(t, os.WriteFile(filepath.Join(packDir, "pack.yaml"), []byte(packYAML), 0o644))
	// 同构性锚点：仓内 packs/agentblog/pack.yaml 与本测试夹具逐段同构（防漂移）。
	assertPackYAMLShapeMatchesRepo(t, packYAML)

	p, err := parsePack(packDir)
	require.NoError(t, err, "pack.yaml 解析（v1）")
	require.Equal(t, "agentblog", p.Name)
	require.Len(t, p.MCP, 1)
	require.Empty(t, p.MCP[0].Tools, "六工具协议自描述——pack.yaml 无 mcp.tools 声明段")
	require.Len(t, p.UI.Slots, 1, "ui.slots 解析")

	// ③ install（fake API 承接 PUT/POST）→ 三闸门 + 存档 + ui entry/slots。
	c, cap := newAgentblogCaptureAPI(t)
	creds := map[string]string{"AGENTBLOG_CALLBACK_TOKEN": token}
	require.NoError(t, install(ctx, c, p, creds, true, ""),
		"install：三闸门（前缀/output/三件套）全过 + 存档 + ui entry/slots")

	// —— 断言：schema 存档 PUT 形状（六工具，全部 source=protocol）——
	cap.mu.Lock()
	archived := append([]archivedToolSchema(nil), cap.SchemasPUT...)
	uiSeen, uiBody := cap.UIEntrySeen, cap.UIEntryBody
	mcpSeen, mcpBody := cap.MCPRegSeen, cap.MCPRegBody
	subsSeen := cap.SubsSeen
	cap.mu.Unlock()

	require.Len(t, archived, 6, "六工具全部存档")
	byName := map[string]archivedToolSchema{}
	for _, a := range archived {
		byName[a.ToolName] = a
	}
	for _, name := range agentblogToolNames {
		a, ok := byName[name]
		require.True(t, ok, "存档缺 %s", name)
		assert.Equal(t, "protocol", a.Source, "%s 应为协议自描述", name)
		assert.Equal(t, "agentblog", a.Server)
		require.NotNil(t, a.OutputSchema, "%s 的 outputSchema 非空", name)
		assert.Equal(t, "object", a.OutputSchema["type"], "%s schema type=object", name)
	}
	// JobStatus 冻结 schema 的两个锚点（逐字版见 D1 mcp/pack/schemas.go）。
	js := byName["agentblog_job_status"].OutputSchema
	if req, ok := js["required"].([]any); ok {
		assert.Contains(t, req, "job_id")
		assert.Contains(t, req, "state")
	} else {
		t.Fatalf("job_status schema 的 required 不是数组: %T", js["required"])
	}

	// —— 断言：ui entry + slots PUT 形状 ——
	require.True(t, uiSeen, "PUT /api/ui-entries 已发出")
	assert.Equal(t, "agentblog", uiBody["pack"])
	assert.Equal(t, "内容工厂", uiBody["label"])
	assert.Contains(t, uiBody["url_template"], "{uiToken}")
	slots, ok := uiBody["slots"].([]any)
	require.True(t, ok, "slots 随行落库")
	require.Len(t, slots, 1)
	slot0, _ := slots[0].(map[string]any)
	assert.Equal(t, "workitem.detail.tab", slot0["slot"])
	assert.Equal(t, "文章预览", slot0["label"])
	assert.Contains(t, slot0["url_template"], "{workItemId}")
	assert.EqualValues(t, 480, slot0["height"])

	// —— 断言：MCP 注册的凭证注入形状（Authorization Bearer）——
	require.True(t, mcpSeen, "POST mcp-servers 已发出")
	assert.Equal(t, "agentblog", mcpBody["name"])
	assert.Equal(t, "streamable_http", mcpBody["transport"])
	mcpCreds, _ := mcpBody["credentials"].(map[string]any)
	headers, _ := mcpCreds["headers"].(map[string]any)
	assert.Equal(t, "Bearer "+token, headers["Authorization"], "credentialRef → Authorization Bearer 注入")
	assert.True(t, subsSeen, "webhook 订阅创建（work_item.updated）")

	// ④ 引擎桥接：C1 组装路径（buildMCPTools）直连该 server。
	// Egress 门用"回环放行"guard（与 C1 包内测试同模式）：生产默认 guard 拦
	// 私网/回环（SSRF 门），联调部署需 ENGINE_EGRESS_EXTRA_ALLOW_CIDRS 显式
	// 放行——README 已写明。存档读路径留空桩：六工具全协议自描述，桥接
	// schema 全部来自实时握手回收（正是"协议自描述优先"优先级的真实验证）。
	loopGuard, err := egressx.NewGuard([]string{"127.0.0.0/8"})
	require.NoError(t, err)
	eng := &engine.EinoEngine{
		MCPResolver: &e2eMCPResolver{servers: []orchapp.ResolvedMCPServer{
			{Name: "agentblog", Transport: "streamable_http", URL: addr + "/mcp/pack",
				Credentials: map[string]any{"headers": map[string]any{"Authorization": "Bearer " + token}}},
		}},
		Egress:          loopGuard,
		PackToolSchemas: &e2ePackToolSchemas{},
	}
	task := orchapp.Task{ID: "t-d2", WorkspaceID: "ws1", Status: "running", Context: map[string]any{"trigger": "d2-e2e"}}
	bridge := eng.BuildMCPTools(ctx, task)
	require.NotNil(t, bridge)
	require.Len(t, bridge.ToolsForTest(), 6, "桥接组装六个 agentblog 工具")
	require.Len(t, bridge.SnapshotForTest(), 1)
	assert.Equal(t, "ok", bridge.SnapshotForTest()[0].Status)
	assert.Equal(t, 6, bridge.SnapshotForTest()[0].ToolCount)
	for _, name := range agentblogToolNames {
		require.NotNil(t, bridge.SchemaOf(name), "%s outputSchema 回收成功（协议自描述）", name)
	}

	// 注册表快照（task.context.tools 数据源）：六工具 origin=mcp、
	// server=agentblog、has_output_schema=true、concurrency_safe=false。
	reg := engine.NewToolRegistryForTest()
	engine.RegisterMCPBridgeForTest(ctx, reg, bridge)
	snap := reg.SnapshotForTest()
	snapByName := map[string]orchapp.ToolSnapshotEntry{}
	for _, s := range snap {
		snapByName[s.Name] = s
	}
	for _, name := range agentblogToolNames {
		s, ok := snapByName[name]
		require.True(t, ok, "注册表缺 %s", name)
		assert.Equal(t, "mcp", s.Origin)
		assert.Equal(t, "agentblog", s.Server)
		assert.True(t, s.HasOutputSchema, "%s has_output_schema=true", name)
		assert.False(t, s.ConcurrencySafe, "MCP 工具 fail-closed 串行")
	}

	// ⑤ A1 校验器对真实输出的判定：
	//   建站（internal 接缝，manual 模式）→ submit（秒回 job_id）→ job_status
	//   轮询到停进态 → 冻结 schema 判合法 + 篡改 state 枚举判违规（反向锚）。
	createAgentblogSite(t, addr, "d2-e2e.example")
	sess := newAgentblogSession(t, addr, token)
	validator := engine.NewDefaultValidator()
	jsSchema := mustJSONRaw(t, js)

	jobID := submitAgentblogJob(t, sess, "ws1")
	statusDoc := jobStatusRaw(t, sess, jobID)
	t.Logf("job %s 真实输出: %s", jobID, truncJSON(statusDoc))
	// 五态推进锚点：fake cms + manual 站点 → awaiting_review（真实生成链完成、
	// 产物已建稿停在人审门——D1 交接 §三 checklist ④ 的最深可达停进态；
	// failed 也是合法停进态——失败分型同样是真实管线推导）。
	state, _ := statusDoc["state"].(string)
	assert.Contains(t, []string{"awaiting_review", "published", "failed"}, state,
		"job 应推进到停进态（manual 站点 = awaiting_review）")

	// 无 schema 的结构化探测（应放行——job_status 输出恒为 object）。
	assert.NoError(t, validator.Validate(engine.Input{
		ToolName: "agentblog_job_status", Document: statusDoc,
	}), "A1 结构化探测：真实输出为 object → 放行")
	// 冻结 schema 校验（应放行——D1 声明与输出同源）。
	assert.NoError(t, validator.Validate(engine.Input{
		ToolName: "agentblog_job_status", Schema: jsSchema, Document: statusDoc,
	}), "A1 schema 校验：agentblog_job_status 真实输出符合冻结 schema")
	// 反向锚：篡改 state 枚举 → 违规（证明校验器真的在验这份 schema）。
	bad := cloneDoc(t, statusDoc)
	bad["state"] = "phantom_state"
	verr := validator.Validate(engine.Input{
		ToolName: "agentblog_job_status", Schema: jsSchema, Document: bad,
	})
	require.Error(t, verr, "篡改 state 枚举应判违规")
	assert.Contains(t, verr.Error(), "enum")
}

// —— D1 server 的 MCP 客户端薄封装（submit / job_status 真调用）——

// agentblogSession 是与 D1 server 的一条持久 MCP 会话（initialize 一次、
// 全部 call 复用——与生产引擎同构：C1 的 mcpCatalog 即单客户端长会话缓存）。
type agentblogSession struct {
	cli *client.Client
}

func newAgentblogSession(t *testing.T, addr, token string) *agentblogSession {
	t.Helper()
	cli, err := client.NewStreamableHttpClient(addr+"/mcp/pack",
		transport.WithHTTPHeaders(map[string]string{"Authorization": "Bearer " + token}))
	require.NoError(t, err)
	t.Cleanup(func() { _ = cli.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	require.NoError(t, cli.Start(ctx))
	_, err = cli.Initialize(ctx, mcpgo.InitializeRequest{Params: mcpgo.InitializeParams{
		ProtocolVersion: mcpgo.LATEST_PROTOCOL_VERSION,
		ClientInfo:      mcpgo.Implementation{Name: "d2-e2e", Version: "1.0"},
	}})
	require.NoError(t, err, "MCP initialize（Bearer 鉴权握手）")
	return &agentblogSession{cli: cli}
}

// call 执行一次工具调用并返回结构化输出（structuredContent 优先；
// 否则解析 content[0].text——与 A1 unwrapCallToolResultDocument 同规则）。
func (s *agentblogSession) call(t *testing.T, tool string, args map[string]any) map[string]any {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	resp, err := s.cli.CallTool(ctx, mcpgo.CallToolRequest{
		Params: mcpgo.CallToolParams{Name: tool, Arguments: args},
	})
	require.NoError(t, err, "call %s", tool)
	require.False(t, resp.IsError, "%s 返回业务错误: %s", tool, toolResultText(t, resp))
	if resp.StructuredContent != nil {
		switch v := resp.StructuredContent.(type) {
		case map[string]any:
			return v
		default:
			b, merr := json.Marshal(v)
			require.NoError(t, merr)
			var m map[string]any
			require.NoError(t, json.Unmarshal(b, &m))
			return m
		}
	}
	var m map[string]any
	require.NoError(t, json.Unmarshal([]byte(toolResultText(t, resp)), &m))
	return m
}

func toolResultText(t *testing.T, resp *mcpgo.CallToolResult) string {
	t.Helper()
	require.NotEmpty(t, resp.Content, "CallToolResult 无 content")
	for _, c := range resp.Content {
		if txt, ok := c.(mcpgo.TextContent); ok {
			return txt.Text
		}
	}
	t.Fatalf("CallToolResult 无 text content")
	return ""
}

// createAgentblogSite 经 full-server 的 internal 接缝建站（manual 模式：
// fake cms 无 PG 时 publish_mode=auto 也会因闸门缺位转人工——取 manual 显式
// 确定性）。建站后 submit 的 job 会走真实生成链（fakegen 确定性伪模型 +
// 事件循环推进），停进 awaiting_review。
func createAgentblogSite(t *testing.T, addr, domain string) {
	t.Helper()
	body := fmt.Sprintf(`{"domain":%q,"name":"d2-e2e","auto_publish":false}`, domain)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, addr+"/internal/v1/sites", strings.NewReader(body))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
	require.Equal(t, http.StatusCreated, resp.StatusCode, "建站失败: %s", string(b))
}

// submitAgentblogJob 提交 fake 生成 job，返回秒回的 job_id。
func submitAgentblogJob(t *testing.T, s *agentblogSession, workspace string) string {
	t.Helper()
	out := s.call(t, "agentblog_submit_generation",
		map[string]any{"workspace": workspace, "seed": "d2 bridge seed"})
	id, _ := out["job_id"].(string)
	require.NotEmpty(t, id, "submit 秒回 job_id")
	return id
}

// jobStatusRaw 轮询到停进态（published/failed/awaiting_review——D1 runner 的
// 三停进条件）即返回；90s 上限兜底（fake 生成链秒级，正常毫秒到）。
func jobStatusRaw(t *testing.T, s *agentblogSession, jobID string) map[string]any {
	t.Helper()
	var doc map[string]any
	deadline := time.Now().Add(90 * time.Second)
	for time.Now().Before(deadline) {
		doc = s.call(t, "agentblog_job_status", map[string]any{"job_id": jobID})
		switch st, _ := doc["state"].(string); st {
		case "published", "failed", "awaiting_review":
			return doc
		}
		time.Sleep(300 * time.Millisecond)
	}
	require.NotNil(t, doc, "job_status 90s 内无可读输出")
	t.Logf("job %s 未在 90s 内到停进态（当前 %v），按最后快照继续断言", jobID, doc["state"])
	return doc
}

// —— e2e resolver / 存档桩（engine 包端口的最小实现）——

type e2eMCPResolver struct{ servers []orchapp.ResolvedMCPServer }

func (f *e2eMCPResolver) ResolveMCP(context.Context, string, string) ([]orchapp.ResolvedMCPServer, error) {
	return f.servers, nil
}

// e2ePackToolSchemas：List 恒空 → 桥接回落协议自描述（六工具全自描述，
// 桥接/注册表断言不受影响；D2 联调不依赖存档读路径，存档的写路径已在 ③ 断言）。
type e2ePackToolSchemas struct{}

func (f *e2ePackToolSchemas) ListPackToolSchemas(context.Context, string) ([]orchapp.PackToolSchema, error) {
	return nil, nil
}

// —— 小工具 ——

func mustJSONRaw(t *testing.T, m map[string]any) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(m)
	require.NoError(t, err)
	return b
}

func cloneDoc(t *testing.T, m map[string]any) map[string]any {
	t.Helper()
	b, err := json.Marshal(m)
	require.NoError(t, err)
	var out map[string]any
	require.NoError(t, json.Unmarshal(b, &out))
	return out
}

func truncJSON(m map[string]any) string {
	b, err := json.Marshal(m)
	if err != nil {
		return "<marshal error>"
	}
	if len(b) > 300 {
		return string(b[:300]) + "…"
	}
	return string(b)
}

// assertPackYAMLShapeMatchesRepo 校验仓内 packs/agentblog/pack.yaml 与测试
// 夹具逐段同构（关键行锚点 + 无 mcp.tools 声明段），防 pack 文件漂移后测试仍绿。
func assertPackYAMLShapeMatchesRepo(t *testing.T, testYAML string) {
	t.Helper()
	repoPath := filepath.Join("..", "..", "packs", "agentblog", "pack.yaml")
	raw, err := os.ReadFile(repoPath)
	if err != nil {
		t.Logf("（提示）未读到仓内 pack.yaml（%s），跳过同构锚点：%v", repoPath, err)
		return
	}
	anchors := []string{
		"apiVersion: pack/v1",
		"name: agentblog",
		"  - key: AGENTBLOG_CALLBACK_TOKEN",
		"  - name: agentblog",
		"    transport: streamable_http",
		"    credentialRef: AGENTBLOG_CALLBACK_TOKEN",
		`types: ["work_item.updated"]`,
		"    label: 内容工厂",
		"    - slot: workitem.detail.tab",
		"      label: 文章预览",
		"      height: 480",
	}
	for _, a := range anchors {
		assert.Contains(t, string(raw), a, "仓内 pack.yaml 应含 %q", a)
		assert.Contains(t, testYAML, a, "测试夹具应含 %q", a)
	}
	assert.NotContains(t, string(raw), "tools:", "仓内 pack.yaml 不应有 mcp.tools 声明段（六工具协议自描述）")
}
