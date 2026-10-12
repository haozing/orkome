package e2e

// golden_thread_helpers_test.go：D4 金线测试的辅助函数（见 golden_thread_test.go
// 文件头的八步说明）。分段：agentblog server 启动 / MCP 会话 / hook 探针 /
// 平台辅助（PAT·resolver·member·install·enqueue）/ task_message 三卡形状判定。

import (
	"bytes"
	"context"
	"errors"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/haozing/ploykit/platform/egressx"
	"github.com/haozing/ploykit/platform/webx"
	webhookhttp "github.com/haozing/ploykit/webhooks/adapters/http"
	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/client/transport"
	mcpgo "github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"orkome/internal/contract/apierr"
	"orkome/internal/contract/events"
	orchapp "orkome/internal/orchestration/app"
)

// ================= agentblog server / MCP 会话 / hook 探针 =================

// goldenFreePort 借临时监听拿空闲端口（D2 联调同模式）。
func goldenFreePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	p := l.Addr().(*net.TCPAddr).Port
	require.NoError(t, l.Close())
	return p
}

// goldenKillTree 终止 `go run` 整棵进程树（Windows：taskkill /T /F——只杀
// go run 会留下占住日志句柄的孙进程 server）。
func goldenKillTree(cmd *exec.Cmd) {
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

// startGoldenAgentblog 以 fake 形态起 D1 full-server（随机端口），配置
// AGENTBLOG_MCPACK_TOKEN + AGENTBLOG_HOOK_SECRET（D3 接收端与 MCP 端点
// 同进程、共享 JobStore——归档联动的前提）。就绪探测 /healthz 200；
// 失败时转储日志尾部辅助排查。
func startGoldenAgentblog(t *testing.T, dir, mcpToken, hookSecret string) string {
	t.Helper()
	port := goldenFreePort(t)
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
		"AGENTBLOG_MCPACK_TOKEN="+mcpToken,
		"AGENTBLOG_HOOK_SECRET="+hookSecret,
		"AGENTBLOG_HTTP_ADDR=127.0.0.1:"+fmt.Sprint(port),
		"AGENTBLOG_EVENT_INTERVAL_MS=200", // 事件循环快节奏（job 推进及时）
	)
	cmd.Stdout = logF
	cmd.Stderr = logF
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP}
	require.NoError(t, cmd.Start())
	t.Cleanup(func() {
		goldenKillTree(cmd)
		_, _ = cmd.Process.Wait()
		_ = logF.Close()
	})
	t.Cleanup(func() {
		if t.Failed() {
			if b, rerr := os.ReadFile(logPath); rerr == nil {
				lines := strings.Split(strings.TrimRight(string(b), "\n"), "\n")
				if len(lines) > 40 {
					lines = lines[len(lines)-40:]
				}
				t.Logf("agentblog server 日志（尾部）:\n%s", strings.Join(lines, "\n"))
			}
		}
	})

	hc := &http.Client{Timeout: 2 * time.Second}
	deadline := time.Now().Add(120 * time.Second) // go run 首次编译可能较慢
	for time.Now().Before(deadline) {
		if resp, perr := hc.Get(addr + "/healthz"); perr == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == 200 {
				return addr
			}
		}
		time.Sleep(300 * time.Millisecond)
	}
	if b, rerr := os.ReadFile(logPath); rerr == nil {
		t.Fatalf("agentblog server 120s 内未就绪。日志尾部:\n%s", string(b))
	}
	t.Fatalf("agentblog server 120s 内未就绪，日志不可读：%s", logPath)
	return ""
}

// goldenMCPSession 与 agentblog MCP 端点的一条持久会话（测试侧直查 job 用）。
type goldenMCPSession struct{ cli *client.Client }

func newGoldenMCPSession(t *testing.T, addr, token string) *goldenMCPSession {
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
		ClientInfo:      mcpgo.Implementation{Name: "d4-golden", Version: "1.0"},
	}})
	require.NoError(t, err, "MCP initialize（Bearer 鉴权握手）")
	return &goldenMCPSession{cli: cli}
}

func (s *goldenMCPSession) call(t *testing.T, tool string, args map[string]any) map[string]any {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	resp, err := s.cli.CallTool(ctx, mcpgo.CallToolRequest{
		Params: mcpgo.CallToolParams{Name: tool, Arguments: args},
	})
	require.NoError(t, err, "call %s", tool)
	require.False(t, resp.IsError, "%s 业务错误: %s", tool, goldenToolText(resp))
	if m, ok := resp.StructuredContent.(map[string]any); ok {
		return m
	}
	var m map[string]any
	require.NoError(t, json.Unmarshal([]byte(goldenToolText(resp)), &m))
	return m
}

func goldenToolText(resp *mcpgo.CallToolResult) string {
	for _, c := range resp.Content {
		if txt, ok := c.(mcpgo.TextContent); ok {
			return txt.Text
		}
	}
	return ""
}

// goldenCreateSite 经 full-server 的 internal 接缝建站（manual 模式——
// fake cms 下生成链停进 awaiting_review，D1 交接 checklist ④）。
func goldenCreateSite(t *testing.T, addr, domain string) {
	t.Helper()
	body := fmt.Sprintf(`{"domain":%q,"name":"d4-golden","auto_publish":false}`, domain)
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

// goldenSignHook 按 orkome 框架（ploykit webhooks/app model.go Sign，D3 交接
// 冻结算法）构造签名：sha256=hex(HMAC-SHA256(secret, unixSeconds "." body))。
func goldenSignHook(secret string, ts time.Time, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	fmt.Fprintf(mac, "%d", ts.Unix())
	mac.Write([]byte("."))
	mac.Write(body)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

// goldenPostHook 以 orkome 框架规范签名格式 POST 一条 work_item.updated
// 事件到 agentblog /hooks/orkome，断言期望状态码（归档联动可观测探针）。
func goldenPostHook(t *testing.T, addr, secret, eventID, status, ws, wi string, want int) {
	t.Helper()
	payload, err := json.Marshal(events.WorkItemPayload{
		ID: wi, WorkspaceID: ws, Number: 1, Title: "d4 golden", Status: status,
		Revision: 1, CreatorType: "user", CreatorID: "d4",
	})
	require.NoError(t, err)
	now := time.Now()
	req, err := http.NewRequest(http.MethodPost, addr+"/hooks/orkome", bytes.NewReader(payload))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Signature", goldenSignHook(secret, now, payload))
	req.Header.Set("X-Timestamp", now.Format(time.RFC3339Nano))
	req.Header.Set("X-Event-Id", eventID)
	req.Header.Set("X-Event-Type", "work_item.updated")
	req.Header.Set("X-Delivery-Id", "dlv-"+eventID)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
	assert.Equal(t, want, resp.StatusCode, "hook %s（status=%s）→ %d（body: %s）", eventID, status, want, string(b))
}

// goldenTestLogger 测试用 slog（error 级别静音，e2e 不刷屏）。
func goldenTestLogger(t *testing.T) *slog.Logger {
	t.Helper()
	return slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelError}))
}

// goldenLoopbackHTTPClient 构造放行 loopback 的 HTTP client（webhook 投递
// 走 agentblog 127.0.0.1；生产经 WEBHOOK_ALLOW_PRIVATE_TARGET=1 等价放行）。
func goldenLoopbackHTTPClient() (*http.Client, error) {
	return egressx.NewHTTPClient(egressx.Opts{Timeout: 10 * time.Second, AllowCIDRs: []string{"127.0.0.0/8"}})
}

// ================= 平台辅助（PAT / resolver / member / install / enqueue） =================

// goldenMintPAT 给 alice 铸一枚 PAT（写 personal_access_token 表——鉴权链
// webx.Authenticate 的 Bearer 分支按 token_hash 解析，与生产 POST /api/tokens
// 同一落库形状；token 生成与 domain.MintPAT 同规则：tk_ + 20 字节随机 hex。
// 直插库省去会话路由（e2e 惯例）。
func goldenMintPAT(t *testing.T, s *stack) string {
	t.Helper()
	var uid string
	require.NoError(t, s.pool.QueryRow(context.Background(),
		`SELECT id FROM "user" LIMIT 1`).Scan(&uid))
	raw := make([]byte, 20)
	for i := range raw {
		raw[i] = byte(i * 7 % 251) // 确定性随机样（测试唯一性够用）
	}
	token := "tk_" + hex.EncodeToString(raw)
	hash := sha256.Sum256([]byte("tk-pat:" + token))
	_, err := s.pool.Exec(context.Background(), `
INSERT INTO personal_access_token (user_id, name, token_hash, prefix)
VALUES ($1,'d4-golden',$2,$3)`,
		uid, hex.EncodeToString(hash[:]), token[:8])
	require.NoError(t, err)
	return token
}

// goldenMCPResolver 生产 mcpResolverAd（ResolveForEngine 经 workspace_mcp_server
// 表解析）的 e2e 等价物。
type goldenMCPResolver struct{ svc *orchapp.MCPServerService }

func (g *goldenMCPResolver) ResolveMCP(ctx context.Context, ws, agentID string) ([]orchapp.ResolvedMCPServer, error) {
	return g.svc.ResolveForEngine(ctx, ws, agentID)
}

func goldenResolverOf(s *stack) *goldenMCPResolver {
	return &goldenMCPResolver{svc: orchapp.NewMCPServerService(s.orepo)}
}

// goldenMemberCheck / goldenRoleOf：member 与 role 查询（stack 的 idRepo 闭包
// 等价物——直接查 member 表）。
func goldenMemberCheck(s *stack) func(ctx context.Context, ws, uid string) (bool, error) {
	return func(ctx context.Context, ws, uid string) (bool, error) {
		var n int
		if err := s.pool.QueryRow(ctx,
			`SELECT count(*) FROM member WHERE workspace_id=$1 AND user_id=$2`, ws, uid).Scan(&n); err != nil {
			return false, err
		}
		return n > 0, nil
	}
}

func goldenRoleOf(s *stack) func(ctx context.Context, ws, uid string) (string, error) {
	return func(ctx context.Context, ws, uid string) (string, error) {
		var role string
		err := s.pool.QueryRow(ctx,
			`SELECT role FROM member WHERE workspace_id=$1 AND user_id=$2`, ws, uid).Scan(&role)
		return role, err
	}
}

// goldenEnqueue 任务入队（goldens 的 orchEnqueue 同模式：直插 agent_task_queue，
// dispatcher 轮询认领后经 s.eng 真跑）。
func goldenEnqueue(t *testing.T, s *stack, ws, wi, agent, prompt string) string {
	t.Helper()
	var uid string
	require.NoError(t, s.pool.QueryRow(ctx0(), `SELECT id FROM "user" LIMIT 1`).Scan(&uid))
	task, err := s.orepo.InsertTask(ctx0(), orchapp.Task{
		WorkspaceID: ws, AgentID: agent, WorkItemID: wi, Status: "queued", MaxAttempts: 2,
		Context:          map[string]any{"trigger": "assignment", "work_item_title": "D4 金线", "message": prompt},
		OriginatorUserID: uid, OriginatorSource: "direct_human", TriggerEvidenceKind: "assignment",
	})
	require.NoError(t, err)
	require.NotEmpty(t, task.ID)
	return task.ID
}

// goldenInstall 以 Bearer PAT + X-Workspace-Id 直调真平台 API 落 install 的
// 四个写面：MCP 注册（凭证注入 Bearer）→ schema 存档 PUT → webhook 订阅 →
// ui entry + slots PUT。三闸门在写前对 agentblog 真握手判定（与 provision
// handshakeAndGate 同语义；不经 pack-provision 进程——避免 e2e 依赖 main 包
// 与外部 CLI 形态，判定与写面逐条对齐）。
func goldenInstall(t *testing.T, s *stack, abAddr, mcpToken, pat string) string {
	t.Helper()
	// 握手 + 三闸门。
	list := goldenListTools(t, abAddr, mcpToken)
	byName := map[string]bool{}
	for _, ti := range list {
		byName[ti.Name] = true
		// 闸门 1（前缀）+ 闸门 2（output 自描述非空且 type=object）。
		require.True(t, strings.HasPrefix(ti.Name, "agentblog_"), "前缀闸门：%s", ti.Name)
		require.NotNil(t, ti.OutputSchema, "%s 协议自描述 outputSchema", ti.Name)
		require.Equal(t, "object", ti.OutputSchema.Type, "%s schema type=object", ti.Name)
	}
	// 闸门 3（job-handle 三件套齐全）。
	require.True(t, byName["agentblog_submit_generation"], "三件套：submit")
	require.True(t, byName["agentblog_job_status"], "三件套：status")
	require.True(t, byName["agentblog_cancel_job"], "三件套：cancel")

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var subSecret string
	do := func(method, path string, body any) {
		t.Helper()
		var rd io.Reader
		if body != nil {
			b, merr := json.Marshal(body)
			require.NoError(t, merr)
			rd = bytes.NewReader(b)
		}
		req, err := http.NewRequestWithContext(ctx, method, s.svcURL()+path, rd)
		require.NoError(t, err)
		req.Header.Set("Authorization", "Bearer "+pat)
		req.Header.Set("X-Workspace-Id", s.wsID)
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		require.Less(t, resp.StatusCode, 300, "%s %s → %d: %s", method, path, resp.StatusCode, string(b))
		if method == http.MethodPost && strings.HasSuffix(path, "/webhooks/subscriptions") {
			var out struct {
				Secret string `json:"secret"`
			}
			require.NoError(t, json.Unmarshal(b, &out))
			subSecret = out.Secret
		}
	}

	// MCP 注册（credentialRef → Authorization Bearer 注入，provision 同形状）。
	do(http.MethodPost, "/api/workspaces/"+s.wsID+"/mcp-servers", map[string]any{
		"name": "agentblog", "transport": "streamable_http", "url": abAddr + "/mcp/pack",
		"credentials": map[string]any{"headers": map[string]any{"Authorization": "Bearer " + mcpToken}},
	})

	// schema 存档 PUT（六条 source=protocol——provision 存档同形状）。
	items := make([]map[string]any, 0, len(list))
	for _, ti := range list {
		raw, merr := json.Marshal(ti.OutputSchema)
		require.NoError(t, merr)
		var schemaMap map[string]any
		require.NoError(t, json.Unmarshal(raw, &schemaMap))
		items = append(items, map[string]any{
			"server": "agentblog", "tool_name": ti.Name,
			"source": "protocol", "output_schema": schemaMap,
		})
	}
	do(http.MethodPut, "/api/pack-tool-schemas/agentblog", map[string]any{"items": items})

	// webhook 订阅（events 段：work_item.updated → agentblog /hooks/orkome）。
	do(http.MethodPost, "/api/workspaces/"+s.wsID+"/webhooks/subscriptions", map[string]any{
		"url": abAddr + "/hooks/orkome", "events": []string{"work_item.updated"},
		"description": "pack agentblog",
	})

	// ui entry + slots PUT（C2 落库形状）。
	do(http.MethodPut, "/api/ui-entries/agentblog", map[string]any{
		"pack": "agentblog", "label": "内容工厂",
		"url_template": abAddr + "/console?ws={workspaceId}&t={uiToken}",
		"slots": []map[string]any{{
			"slot": "workitem.detail.tab", "label": "文章预览",
			"url_template": abAddr + "/preview?item={workItemId}&t={uiToken}", "height": 480,
		}},
	})
	return subSecret
}

// goldenListTools 与 agentblog MCP 端点握手并 ListTools（闸门判定的输入）。
func goldenListTools(t *testing.T, addr, token string) []mcpgo.Tool {
	t.Helper()
	cli, err := client.NewStreamableHttpClient(addr+"/mcp/pack",
		transport.WithHTTPHeaders(map[string]string{"Authorization": "Bearer " + token}))
	require.NoError(t, err)
	defer func() { _ = cli.Close() }()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	require.NoError(t, cli.Start(ctx))
	_, err = cli.Initialize(ctx, mcpgo.InitializeRequest{Params: mcpgo.InitializeParams{
		ProtocolVersion: mcpgo.LATEST_PROTOCOL_VERSION,
		ClientInfo:      mcpgo.Implementation{Name: "d4-install", Version: "1.0"},
	}})
	require.NoError(t, err)
	listResp, err := cli.ListTools(ctx, mcpgo.ListToolsRequest{})
	require.NoError(t, err)
	return listResp.Tools
}

// ================= task_message 读取与三卡形状判定（B2 语义对齐） =================

// goldenTaskMessages 读任务的全部消息（tool_use/tool_result/text）。
func goldenTaskMessages(t *testing.T, s *stack, ws, taskID string) []orchapp.TaskMessage {
	t.Helper()
	msgs, err := s.orepo.ListTaskMessages(ctx0(), ws, taskID, orchapp.TaskMessagesQuery{Limit: 200})
	require.NoError(t, err)
	return msgs
}

// goldenToolResultDoc 从 task_message 定位指定工具的 tool_result 并解出 JSON
// 文档：tool_use（content = "name args"）与 tool_result 按 call_id 配对
//（前端 task-messages.tsx 的 toolNameByCallId 同语义），再按 B2 冻结表
// #2/#3 解包装（CallToolResult 包装 → structuredContent / content[0].text）。
func goldenToolResultDoc(t *testing.T, msgs []orchapp.TaskMessage, tool string) map[string]any {
	t.Helper()
	callID := ""
	for _, m := range msgs {
		if m.Kind == "tool_use" && strings.HasPrefix(m.Content, tool+" ") {
			callID = m.CallID
			break
		}
	}
	for _, m := range msgs {
		if m.Kind != "tool_result" {
			continue
		}
		if callID != "" && m.CallID != callID {
			continue
		}
		if doc, ok := goldenUnwrapToolResult(m.Content); ok {
			return doc
		}
	}
	var sample []string
	for _, m := range msgs {
		sample = append(sample, fmt.Sprintf("%s[%s]:%.150s", m.Kind, m.CallID, m.Content))
	}
	t.Fatalf("工具 %s 的 tool_result 未找到或不可解析。消息流:\n%s", tool, strings.Join(sample, "\n"))
	return nil
}

// goldenUnwrapToolResult 按 B2 冻结表 #2/#3 语义解 tool_result 的 content：
// 整串 JSON.parse → CallToolResult 包装（content 为数组）取 structuredContent
// 或 content[0].text 再 parse；裸对象直接用；解析失败返回 false（灰框路径）。
func goldenUnwrapToolResult(content string) (map[string]any, bool) {
	var outer any
	if err := json.Unmarshal([]byte(content), &outer); err != nil {
		return nil, false
	}
	m, ok := outer.(map[string]any)
	if !ok {
		return nil, false
	}
	if arr, okk := m["content"].([]any); okk { // CallToolResult 包装
		if sc, okk := m["structuredContent"].(map[string]any); okk {
			return sc, true
		}
		if len(arr) > 0 {
			if first, okk := arr[0].(map[string]any); okk {
				if txt, okk := first["text"].(string); okk {
					var inner map[string]any
					if err := json.Unmarshal([]byte(txt), &inner); err == nil {
						return inner, true
					}
				}
			}
		}
		return nil, false
	}
	return m, true // 裸文档（防御未包装结构化输出）
}

func goldenJSON(t *testing.T, m map[string]any) string {
	t.Helper()
	b, err := json.Marshal(m)
	require.NoError(t, err)
	return string(b)
}

func goldenJSON2(m map[string]any) string {
	b, _ := json.Marshal(m)
	return string(b)
}

// goldenStackServers 每测试 stack 一个 httptest.Server（stack 结构体定义在
// golden_test.go 不便扩字段；按 stack 指针 keyed）。
var goldenStackServers = map[*stack]*httptest.Server{}

// svcURL 起（或复用）一个包 stack.handler 的 httptest.Server——install
// 的 HTTP 直调需要真 TCP 端点，而 stack 本体是进程内调用形态。同一 handler
// 链（authMw/CSRF/member 检查）原样生效；测试结束经 goldenCloseStackServers
// 统一关闭。
func (s *stack) svcURL() string {
	if srv, ok := goldenStackServers[s]; ok {
		return srv.URL
	}
	srv := httptest.NewServer(s.handler)
	goldenStackServers[s] = srv
	return srv.URL
}

// goldenUIEntry GET /api/ui-entries 响应条目（⑦ 断言的解码形状）。
type goldenUIEntry struct {
	Pack        string `json:"pack"`
	Label       string `json:"label"`
	URLTemplate string `json:"url_template"`
	Slots       []struct {
		Slot        string `json:"slot"`
		Label       string `json:"label"`
		URLTemplate string `json:"url_template"`
		Height      int    `json:"height"`
	} `json:"slots"`
}

// ================= 增量路由的鉴权中间件（routes.go memberMW 同语义） =================

// goldenWS 从 X-Workspace-Id header 取 workspace（orchhttp 的 d.ws 同语义）。
func goldenWS(r *http.Request, s *stack) string {
	if h := r.Header.Get("X-Workspace-Id"); h != "" {
		return h
	}
	return s.wsID
}

// goldenMemberMW 请求级 member 检查 + 角色回填（routes.go memberMW 的等价物：
// principal 经 authMw 已就位；member 判定查 member 表）。
func goldenMemberMW(s *stack, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		p := webx.PrincipalFrom(r.Context())
		if p == nil {
			webx.ErrUnauthenticated(w, "authentication required")
			return
		}
		ws := goldenWS(r, s)
		if ws == "" {
			webx.WriteError(w, http.StatusBadRequest, "VALIDATION", "X-Workspace-Id header required", nil)
			return
		}
		ok, err := goldenMemberCheck(s)(r.Context(), ws, p.UserID)
		if err != nil || !ok {
			webx.WriteError(w, http.StatusForbidden, "FORBIDDEN", "not a member of this workspace", nil)
			return
		}
		if p.Role == "" {
			role, rerr := goldenRoleOf(s)(r.Context(), ws, p.UserID)
			if rerr == nil {
				p.Role = role
			}
		}
		p.WorkspaceID = ws
		next(w, r)
	}
}

// goldenWhMember / goldenWhAdmin webhook 管理路由的鉴权（生产 webhookhttp.Mount
// 的 member/admin 语义：member = webhooks:read，admin = webhooks:manage；
// e2e 以 owner/admin 角色近似——alice 是 workspace owner）。
func goldenWhMember(s *stack, _ webhookhttp.Deps, next http.HandlerFunc) http.HandlerFunc {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.authMw(webx.RequireAuth(goldenMemberMW(s, next))).ServeHTTP(w, r)
	})
}

func goldenWhAdmin(s *stack, _ webhookhttp.Deps, next http.HandlerFunc) http.HandlerFunc {
	return goldenWhMember(s, webhookhttp.Deps{}, func(w http.ResponseWriter, r *http.Request) {
		p := webx.PrincipalFrom(r.Context())
		if p.Role != "owner" && p.Role != "admin" {
			webx.WriteError(w, http.StatusForbidden, "FORBIDDEN", "webhooks:manage permission required", nil)
			return
		}
		next(w, r)
	})
}

// goldenWriteErr apierr → HTTP 状态映射（orchhttp writeErr 的等价物；
// ploykit webx.Error 走 HTTPStatus()，orkome apierr 走 ErrorCode 映射）。
func goldenWriteErr(t *testing.T, w http.ResponseWriter, err error) {
	t.Helper()
	var he interface{ HTTPStatus() int }
	if ok := errors.As(err, &he); ok {
		http.Error(w, err.Error(), he.HTTPStatus())
		return
	}
	var ae *apierr.Error
	if ok := errors.As(err, &ae); ok {
		status := map[string]int{
			apierr.Unauthenticated: 401, apierr.Forbidden: 403, apierr.NotFound: 404,
			apierr.Conflict: 409, apierr.Validation: 422, apierr.RateLimited: 429,
			apierr.Internal: 500,
		}[ae.Code]
		if status == 0 {
			status = 500
		}
		http.Error(w, err.Error(), status)
		return
	}
	t.Logf("goldenWriteErr: %v", err)
	http.Error(w, "internal error", http.StatusInternalServerError)
}
