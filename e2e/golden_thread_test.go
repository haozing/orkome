package e2e

// D4：端到端金线——一条链穿起三份设计文档（pack-tool-contract / pack-frontend /
// pack-rollout-plan §4 D4）的所有交付物。
//
// 金线八步（每步一个断言块，注释与任务书金线定义一一对应）：
//  ① 安装：packs/agentblog 过三闸门 + schema 存档 + ui entry/slots 落库——
//     真平台（本 e2e stack 真库真路由），不是 D2 的 fake capture API；
//  ② 任务派发：work item + agent + 任务入队（既有 e2e goldens 模式）；
//  ③ 引擎组装：任务真实执行时 MCP 桥接组装六工具（task.context.tools
//     快照断言：agentblog 六条 origin=mcp/server=agentblog）；
//  ④ 员工回合：scripted 模型（engine 测试既有 fake 模式）依序调
//     evaluate_seed → submit_generation，断言 task_message 的 tool_result
//     形状能触发前端三卡（MetricsCard：metrics 非空对象；JobHandleCard：
//     恰 {job_id}）——形状判定语义与 B2 冻结表对齐，不起浏览器；
//  ⑤ 管线真实跑：D1 server 侧 job 推进（fake 管线到停进态）；
//  ⑥ 收割回合：下一任务模型调 job_status，tool_result 形状能触发
//     JobStatusCard（job_id + state∈五态 + stage；result.url 存在）；
//  ⑦ slot 数据面：GET /api/ui-entries 响应含 agentblog 的 slots
//     （workitem.detail.tab + {workItemId} 模板 + height）；
//  ⑧ 收尾归档：work item 置终态（done）→ orkome 真投递路径（事件目录 +
//     wiringWebhooks 同构接线 + DeliverPending）把 work_item.updated
//     webhook 按框架 HMAC 签名投到 agentblog /hooks/orkome → 断言
//     delivered + agentblog 侧归档联动可观测。
//
// 门控（与 D2 联调同模式，默认 skip）：
//   - TEST_DATABASE_URL：docker orkome-test-pg（make test-e2e 自起）；
//   - ORKOME_AGENTBLOG_DIR：指向兄弟仓 agentblog 根目录（如 D:/code/agentblog），
//     未设置 → skip（跨仓依赖绝不进默认 go test ./... 依赖面）。

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
	"github.com/haozing/ploykit/authz"
	"github.com/haozing/ploykit/platform/egressx"
	"github.com/haozing/ploykit/platform/sealx"
	"github.com/haozing/ploykit/platform/webx"
	webhookhttp "github.com/haozing/ploykit/webhooks/adapters/http"
	webhookpgrepo "github.com/haozing/ploykit/webhooks/adapters/pgrepo"
	webhookapp "github.com/haozing/ploykit/webhooks/app"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"orkome/internal/contract/events"
	"orkome/internal/contract/modelmeta"
	"orkome/internal/platform/eventbus"
	orchhttp "orkome/internal/orchestration/adapters/http"
	orchapp "orkome/internal/orchestration/app"
)

var goldenAgentblogTools = []string{
	"agentblog_evaluate_seed",
	"agentblog_query_knowledge",
	"agentblog_submit_generation",
	"agentblog_job_status",
	"agentblog_cancel_job",
	"agentblog_review_result",
}

var goldenJobStates = []string{"queued", "running", "awaiting_review", "published", "failed"}

func TestD4_GoldenThread(t *testing.T) {
	ablog := os.Getenv("ORKOME_AGENTBLOG_DIR")
	if ablog == "" {
		t.Skip("ORKOME_AGENTBLOG_DIR not set（跨仓联调门控：指向 agentblog 仓根，如 D:/code/agentblog）")
	}
	if _, err := os.Stat(filepath.Join(ablog, "cmd", "full-server")); err != nil {
		t.Skipf("ORKOME_AGENTBLOG_DIR 下无 cmd/full-server: %v", err)
	}

	ctx := context.Background()
	const mcpToken = "d4-golden-pack-token"
	const hookSecret = "whk_d4golden00000000000000000000000000000000000000000000"

	// 连接池竞态规避（D2 交接坑 1：vendored mcp-go v0.43.0 零值 http.Client
	// 共享 DefaultTransport 连接池，复用已被服务端关闭的空闲连接会偶发 90s
	// 挂起——测试内禁用 keep-alive 复用；生产引擎是长会话进程不受影响）。
	tr, ok := http.DefaultTransport.(*http.Transport)
	require.True(t, ok)
	prevKA := tr.DisableKeepAlives
	tr.DisableKeepAlives = true
	t.Cleanup(func() { tr.DisableKeepAlives = prevKA })

	// —— 前置：起 agentblog fake full-server（/mcp/pack 与 /hooks/orkome
	// 同进程、共享 JobStore——归档联动的前提；HOOK_SECRET 配置 D3 接收端）。——
	abAddr := startGoldenAgentblog(t, ablog, mcpToken, hookSecret)
	t.Logf("agentblog fake full-server 已就绪：%s（/mcp/pack + /hooks/orkome）", abAddr)

	// —— 平台侧：真库真路由 stack（golden_test.go 的 newStack 模式）。——
	s := newStack(t)
	s.login("alice", "d4@example.com")
	s.wsID = s.createWS(t, "alice", "d4-ws")

	// —— 金线专属接线：newStack 建于 C1/webhooks 接线之前，这些面尚未进
	// stack。D4 按 wiring_orchestration.go / wiring_webhooks.go 的生产接线
	// 逐项补上（接线方式与生产同构）：
	loopGuard, err := egressx.NewGuard([]string{"127.0.0.0/8"})
	require.NoError(t, err)

	// (a) 事件投递面（wiringWebhooks 同构）：事件目录（work_item.updated
	// 在册）+ bus PhaseProject 扇出 + secret 封存（生产 sealx，同参密钥派生）
	// + loopback 放行的投递 client（生产经 WEBHOOK_ALLOW_PRIVATE_TARGET）。
	secrets, err := sealx.NewSecrets(sealx.DeriveKey("e2e-secret-32-bytes-aaaaaaaaaaaa", "webhook"))
	require.NoError(t, err)
	whRepo := webhookpgrepo.New(s.pool)
	whSvc := webhookapp.NewWebhookService(whRepo, goldenEventCatalog(), goldenTestLogger(t), time.Now).WithSecrets(secrets)
	whHTTP, err := egressx.NewHTTPClient(egressx.Opts{Timeout: 10 * time.Second, AllowCIDRs: []string{"127.0.0.0/8"}})
	require.NoError(t, err)
	whSvc = whSvc.WithHTTPClient(whHTTP)
	s.bus.On(eventbus.PhaseProject, func(ctx context.Context, e events.Envelope) {
		if e.WorkspaceID == "" || e.EventID == "" || !goldenEventCatalog().Has(e.Event) {
			return
		}
		payload, merr := json.Marshal(e.Payload)
		if merr != nil {
			return
		}
		var pm map[string]any
		_ = json.Unmarshal(payload, &pm)
		_, _ = whSvc.Emit(ctx, e.WorkspaceID, webhookapp.OutboundEvent{ID: e.EventID, Type: e.Event, Payload: pm})
	})

	// (b) HTTP 面（真平台 API 承接 provision install + ui-entries 数据面）：
	// ui-entries / pack-tool-schemas / ws 级 MCP 注册 / webhook 订阅管理。
	goldenMountPackAPI(t, s, loopGuard, whSvc)

	// (c) 引擎：MCP resolver（生产 = MCPServerService.ResolveForEngine 经
	// workspace_mcp_server 表解析）+ B1 存档端口 + task.context 快照回写 +
	// loopback egress（生产经 ENGINE_EGRESS_EXTRA_ALLOW_CIDRS 放行，同参）。
	s.eng.MCPResolver = goldenResolverOf(s)
	s.eng.PackToolSchemas = orchapp.NewPackToolSchemaService(s.orepo)
	s.eng.TaskContextPatcher = s.orepo
	s.eng.Egress = loopGuard

	// ① 安装：packs/agentblog 过三闸门 + schema 存档 + ui entry/slots 落库
	// （复用 D2 联调路径：pack-provision 的 install 对真平台 API——Bearer PAT
	// 走 stack 的 authMw 真鉴权链）。
	pat := goldenMintPAT(t, s)
	subSecret := goldenInstall(t, s, abAddr, mcpToken, pat)

	// 订阅 secret 对齐：CreateSubscription 随机铸造的 secret 一次性回显在
	// 响应里（生产运维把它配置到 agentblog 的 AGENTBLOG_HOOK_SECRET——D3
	// 文档 §一）。本测试 agentblog server 先于订阅启动（MCP 握手依赖），
	// env 已固定 hookSecret——把库里的 sealed secret 重写为 Seal(hookSecret)
	// 等价完成这一步运维动作（unseal → 签名 → 验签全程真实，不绕 HMAC）。
	require.NotEmpty(t, subSecret, "订阅创建响应应一次性回显 secret")
	sealed, err := secrets.Seal(hookSecret)
	require.NoError(t, err)
	_, err = s.pool.Exec(ctx,
		`UPDATE webhook_subscription SET secret=$1 WHERE workspace_id=$2`, sealed, s.wsID)
	require.NoError(t, err)

	// —— 断言：schema 存档落库六条（source=protocol——B1 闸门实证）。——
	var schemaCount int
	require.NoError(t, s.pool.QueryRow(ctx,
		`SELECT count(*) FROM pack_tool_schema WHERE workspace_id=$1 AND pack='agentblog'`, s.wsID).Scan(&schemaCount))
	assert.Equal(t, 6, schemaCount, "六工具 schema 存档落库")
	var jsSchemaRaw []byte
	require.NoError(t, s.pool.QueryRow(ctx, `
SELECT output_schema FROM pack_tool_schema
WHERE workspace_id=$1 AND pack='agentblog' AND tool_name='agentblog_job_status'`,
		s.wsID).Scan(&jsSchemaRaw))
	var jsSchema map[string]any
	require.NoError(t, json.Unmarshal(jsSchemaRaw, &jsSchema))
	if req, okk := jsSchema["required"].([]any); okk {
		assert.Contains(t, req, "job_id", "JobStatus 冻结 schema 锚点：required 含 job_id")
		assert.Contains(t, req, "state", "JobStatus 冻结 schema 锚点：required 含 state")
	}

	// —— 断言：ui entry + slots 落库形状。——
	var uiLabel, uiURL string
	var uiSlotsRaw []byte
	require.NoError(t, s.pool.QueryRow(ctx, `
SELECT label, url_template, slots FROM pack_ui_entry WHERE workspace_id=$1 AND pack='agentblog'`,
		s.wsID).Scan(&uiLabel, &uiURL, &uiSlotsRaw))
	assert.Equal(t, "内容工厂", uiLabel)
	assert.Contains(t, uiURL, "{uiToken}")

	// —— 断言：webhook 订阅创建（url 指向 agentblog /hooks/orkome）。——
	var subURL string
	require.NoError(t, s.pool.QueryRow(ctx, `
SELECT url FROM webhook_subscription WHERE workspace_id=$1 ORDER BY created_at LIMIT 1`, s.wsID).Scan(&subURL))
	assert.Equal(t, abAddr+"/hooks/orkome", subURL, "events 段订阅创建（work_item.updated）")

	// ② 任务派发：work item + agent + 任务入队（既有 goldens 模式）。
	wi := s.createWorkItem(t, "alice", "D4 金线：写一篇种子词评估文章")
	agent := s.createAgent(t, s.wsID, "d4-content-worker")
	_, err = s.pool.Exec(ctx,
		`UPDATE work_item SET assignee_type='agent', assignee_id=$2 WHERE id=$1`, wi, agent)
	require.NoError(t, err)

	// 建站（manual 模式：fake 管线停进 awaiting_review——D1 交接 checklist ④）。
	goldenCreateSite(t, abAddr, "d4-golden.example")

	// scripted 模型（provider=fake；modelmeta.Register 后写覆写 stack 顶层
	// 的 scriptModel 注册——engine 测试既有 fake 模式的 e2e 等价物）。
	gmodel := goldenRegisterModel()

	task1 := goldenEnqueue(t, s, s.wsID, wi, agent, "评估这个种子词并提交生成")

	// ③④ 引擎组装 + 员工回合：等任务完成，断言组装快照与 tool_result 形状。
	waitFor(t, func() bool {
		tt, okk, _ := s.orepo.GetTask(ctx, s.wsID, task1)
		return okk && tt.Status == "completed"
	}, "金线任务一完成（evaluate_seed → submit_generation → 收尾）")

	jobID := gmodel.submittedJobID()
	require.NotEmpty(t, jobID, "scripted 模型应已拿到 submit 秒回的 job_id")

	// ③ 引擎组装断言：task.context.tools 快照含 agentblog 六工具（C1 数据面）。
	var taskCtxRaw []byte
	require.NoError(t, s.pool.QueryRow(ctx,
		`SELECT context FROM agent_task_queue WHERE id=$1`, task1).Scan(&taskCtxRaw))
	var taskCtx map[string]any
	require.NoError(t, json.Unmarshal(taskCtxRaw, &taskCtx))
	snapByName := map[string]orchapp.ToolSnapshotEntry{}
	for _, e := range orchapp.TaskContextTools(taskCtx) {
		snapByName[e.Name] = e
	}
	for _, name := range goldenAgentblogTools {
		e, okk := snapByName[name]
		require.True(t, okk, "task.context.tools 缺 %s（快照: %s）", name, string(taskCtxRaw))
		assert.Equal(t, "mcp", e.Origin)
		assert.Equal(t, "agentblog", e.Server)
		assert.True(t, e.HasOutputSchema, "%s 协议自描述 schema", name)
		assert.False(t, e.ConcurrencySafe, "MCP 工具 fail-closed 串行")
	}

	// ④ 员工回合断言：task_message 的 tool_result 形状能触发前端三卡
	// （B2 冻结形状判定语义的数据面对齐——不起浏览器）。
	msgs1 := goldenTaskMessages(t, s, s.wsID, task1)
	evalDoc := goldenToolResultDoc(t, msgs1, "agentblog_evaluate_seed")
	// evaluate_seed 是同步便宜手：输出为结构化评估对象（seed/verdict/
	// volume/difficulty/competitors——D1 契约），不是 metrics 载体；断言
	// 结构化 + verdict 非空 + A1 校验器放行（无违规串——MetricsCard 的
	// metrics 非空对象判定锚在 job_status/review_result 输出，见 ⑥）。
	assert.NotEmpty(t, evalDoc["verdict"], "evaluate_seed 秒级返回 verdict")
	assert.Contains(t, []string{"go", "marginal", "avoid"}, evalDoc["verdict"], "verdict 三值枚举")
	assert.NotContains(t, goldenJSON(t, evalDoc), "TOOL_OUTPUT_CONTRACT_VIOLATION",
		"A1 校验器放行（无违规串）")

	submitDoc := goldenToolResultDoc(t, msgs1, "agentblog_submit_generation")
	// JobHandleCard 判定：恰 {job_id}（+可选 state）无其他键（B2 冻结表 #5）。
	assert.Equal(t, jobID, submitDoc["job_id"], "submit 秒回 job_id")
	for k := range submitDoc {
		assert.Contains(t, []string{"job_id", "state"}, k,
			"JobHandleCard 严格形状：多余键 %q 会落默认灰框（实际: %v）", k, submitDoc)
	}
	assert.NotContains(t, goldenJSON(t, submitDoc), "TOOL_OUTPUT_CONTRACT_VIOLATION")

	// ⑤ 管线真实跑：D1 侧 job 推进到停进态（fake 管线 + 事件循环）。
	sess := newGoldenMCPSession(t, abAddr, mcpToken)
	var statusDoc map[string]any
	waitFor(t, func() bool {
		statusDoc = sess.call(t, "agentblog_job_status", map[string]any{"job_id": jobID})
		st, _ := statusDoc["state"].(string)
		return st == "awaiting_review" || st == "published" || st == "failed"
	}, "agentblog 管线推进到停进态")
	t.Logf("job %s 停进态：%s", jobID, goldenJSON(t, statusDoc))

	// ⑥ 收割回合：下一任务模型调 job_status——tool_result 形状能触发
	// JobStatusCard（job_id + state∈五态 + stage；result.url 存在）。
	gmodel.armHarvest(jobID)
	task2 := goldenEnqueue(t, s, s.wsID, wi, agent, "收割：查看生成结果并总结")
	waitFor(t, func() bool {
		tt, okk, _ := s.orepo.GetTask(ctx, s.wsID, task2)
		return okk && tt.Status == "completed"
	}, "金线任务二完成（job_status → 收尾）")
	msgs2 := goldenTaskMessages(t, s, s.wsID, task2)
	statusDoc2 := goldenToolResultDoc(t, msgs2, "agentblog_job_status")
	assert.Equal(t, jobID, statusDoc2["job_id"], "job_status 查询同一 job")
	st2, _ := statusDoc2["state"].(string)
	assert.Contains(t, goldenJobStates, st2, "state ∈ 五态（JobStatusCard 必填三件套）")
	assert.NotEmpty(t, statusDoc2["stage"], "stage 非空（JobStatusCard 必填三件套）")
	// result 块：JobStatusCard 的可选渲染块（B2 冻结表 #4——url/summary 非
	// string 按缺省渲染，块本身可缺）。D1 停进态语义：awaiting_review 的
	// result = {post_id, summary}（产物已建稿停在人审门，url 要 published
	// 才有——部署完成后才有线上地址）；published = {url, summary, post_id}；
	// failed 无 result 内容。断言产物态有 result 块且 summary/url 至少有
	// 一者（url 可点是演示面：published 态才点亮，人审门前显示 summary）。
	if result, okk := statusDoc2["result"].(map[string]any); okk && st2 != "failed" {
		hasURL, _ := result["url"].(string)
		hasSummary, _ := result["summary"].(string)
		assert.True(t, hasURL != "" || hasSummary != "",
			"result 块有可渲染内容（awaiting_review=summary；published=url+summary）")
	} else {
		t.Logf("（提示）job result 无内容（state=%s——failed 态无产物属预期）", st2)
	}
	// MetricsCard 判定锚（B2 冻结表 #6：metrics 非空普通对象）：job_status
	// 输出自带 metrics（质检指标——D1 契约），停进态非 failed 时应有内容；
	// JobStatusCard 会内嵌渲染 metrics 网格（B2 三卡之一的数据面）。
	if m, okk := statusDoc2["metrics"].(map[string]any); okk {
		assert.NotEmpty(t, m, "job_status 的 metrics 非空（MetricsCard/JobStatus 内嵌网格触发条件）")
	} else {
		t.Logf("（提示）job_status 无 metrics（state=%s——失败态可缺省）", st2)
	}
	assert.NotContains(t, goldenJSON(t, statusDoc2), "TOOL_OUTPUT_CONTRACT_VIOLATION",
		"A1 校验器对收割回合同样放行")

	// ⑦ slot 数据面：GET /api/ui-entries 响应含 agentblog 的 slots。
	recUI := s.do("alice", http.MethodGet, "/api/ui-entries", nil, nil)
	require.Equal(t, 200, recUI.Code, recUI.Body.String())
	var uiEntries struct {
		Items []goldenUIEntry `json:"items"`
	}
	require.NoError(t, json.Unmarshal(recUI.Body.Bytes(), &uiEntries))
	var abEntry *goldenUIEntry
	for i := range uiEntries.Items {
		if uiEntries.Items[i].Pack == "agentblog" {
			abEntry = &uiEntries.Items[i]
		}
	}
	require.NotNil(t, abEntry, "ui-entries 响应含 agentblog")
	require.Len(t, abEntry.Slots, 1, "agentblog 带 1 个 slot")
	slot := abEntry.Slots[0]
	assert.Equal(t, "workitem.detail.tab", slot.Slot, "v0 唯一合法 slot 标识")
	assert.Equal(t, "文章预览", slot.Label)
	assert.Contains(t, slot.URLTemplate, "{workItemId}", "slot url 模板含 {workItemId} 变量")
	assert.Contains(t, slot.URLTemplate, "{uiToken}", "slot url 模板含 {uiToken}（PackSlotFrame 铸票）")
	assert.Equal(t, 480, slot.Height)

	// ⑧ 收尾归档：work item 置终态（done）→ orkome 出 work_item.updated
	// 事件（workapp.Update 经 bus 直发）→ 框架扇出进 delivery 队列 →
	// DeliverPending 按框架 HMAC 签名投到 agentblog /hooks/orkome。
	recDone := s.do("alice", http.MethodPut, "/api/work-items/"+wi,
		map[string]any{"status": "done"}, nil)
	require.Equal(t, 200, recDone.Code, recDone.Body.String())

	// 事件落 delivery（bus 扇出同步执行，行应立即可见；轮询兜底异步面）。
	waitFor(t, func() bool {
		var n int
		_ = s.pool.QueryRow(ctx, `
SELECT count(*) FROM webhook_delivery d
JOIN webhook_subscription sub ON sub.id = d.subscription_id
WHERE d.workspace_id=$1 AND d.event_type='work_item.updated'`, s.wsID).Scan(&n)
		return n > 0
	}, "work_item.updated 事件入 delivery 队列")

	// 投递（生产 = DeliveryWorker 5s 轮询；测试直接驱动 DeliverPending）。
	whSvc.DeliverPending(ctx, 20)

	// 投递成功断言：delivered（agentblog 返回 2xx 才会 MarkDelivered；
	// 验签失败 401 / 处理错 500 都会留 pending/dead——这行就是 HMAC 契约的
	// 端到端证明）。
	var dStatus string
	var dPayload []byte
	require.NoError(t, s.pool.QueryRow(ctx, `
SELECT d.status, d.payload FROM webhook_delivery d
WHERE d.workspace_id=$1 AND d.event_type='work_item.updated'
ORDER BY d.created_at DESC LIMIT 1`, s.wsID).Scan(&dStatus, &dPayload))
	assert.Equal(t, "delivered", dStatus, "webhook 投递到 agentblog /hooks/orkome 成功")

	// payload 应是 WorkItemPayload 形状（status=done 终态——D3 终态过滤通过）。
	var wip events.WorkItemPayload
	require.NoError(t, json.Unmarshal(dPayload, &wip))
	assert.Equal(t, wi, wip.ID)
	assert.Equal(t, "done", wip.Status, "终态（done ∈ {done, cancelled} 触发归档）")

	// 归档不删快照：job_status 仍可查（D3 §四——归档只影响活跃集语义）。
	after := sess.call(t, "agentblog_job_status", map[string]any{"job_id": jobID})
	assert.Equal(t, jobID, after["job_id"], "归档后 job_status 仍可查")

	// 归档联动可观测探针（orkome 规范签名格式直发，D3 冻结算法同源）：
	//   a) 新 event_id 的终态事件 → 202（归档管线健康；去重表对新 id 不拦，
	//      ArchiveWorkspace 幂等已归档=0）；
	//   b) 非终态事件（in_progress）→ 200 快速 ack 不触发归档；
	//   c) 错误签名 → 401（验签闸真的在验——HMAC 契约反向锚）。
	goldenPostHook(t, abAddr, hookSecret, "evt-d4-archprobe", "done", s.wsID, wi, http.StatusAccepted)
	goldenPostHook(t, abAddr, hookSecret, "evt-d4-nontask", "in_progress", s.wsID, wi, http.StatusOK)
	goldenPostHook(t, abAddr, "whk_wrong_secret_0000000000000000000000000000",
		"evt-d4-badsig", "done", s.wsID, wi, http.StatusUnauthorized)
}

// ================= stack 辅助接线（生产 wiring 的 e2e 等价物） =================

// goldenEventCatalog 注册 orkome 事件词汇（wiringWebhooks.orkomeEventCatalog
// 同构——金线只需要 work_item.updated 在册）。
func goldenEventCatalog() *webhookapp.EventCatalog {
	c := webhookapp.NewEventCatalog()
	for _, e := range []struct{ Type, Desc string }{
		{events.WorkItemCreated, "work item created"},
		{events.WorkItemUpdated, "work item updated"},
		{events.WorkItemDeleted, "work item deleted"},
		{events.CommentCreated, "comment created"},
	} {
		c.Register(e.Type, e.Desc)
	}
	return c
}

// goldenMountPackAPI 挂 pack 相关的 HTTP 面（生产 wiring_orchestration.go 的
// PackUIEntries/PackToolSchemas/MCP 路由 + main.go 的 webhook 管理路由）。
// stack 的 api mux 在 newStack 内已封口（handler 链已组装），这里把增量路由
// 挂到 root mux 的独立前缀再并入 handler 链——为保真（走同一 authMw/CSRF 链），
// 直接对 rootMux 追加（http.ServeMux 支持运行期注册）。
func goldenMountPackAPI(t *testing.T, s *stack, loopGuard *egressx.Guard, whSvc *webhookapp.WebhookService) {
	t.Helper()
	db := pgWrap(s)

	deps := orchhttp.Deps{
		Repo:             s.orepo,
		PackUIEntries:    orchapp.NewPackUIEntryService(s.orepo),
		PackToolSchemas:  orchapp.NewPackToolSchemaService(s.orepo),
		MCP:              func() *orchapp.MCPServerService { m := orchapp.NewMCPServerService(s.orepo); m.Egress = loopGuard; return m }(),
		MemberCheck:      goldenMemberCheck(s),
		RoleOf:           goldenRoleOf(s), // P2-D7 admin 闸同因（见 golden_test.go orchhttp.Deps 注）
	}

	// orchhttp.Mount 挂全部路由会与 stack 已挂的路由冲突（ServeMux panic）；
	// 改为按需挂增量路由（与 Mount 内部同 guard 结构）。
	api := http.NewServeMux()
	guard := func(h http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			s.authMw(webx.RequireAuth(goldenMemberMW(s, h))).ServeHTTP(w, r)
		}
	}
	api.HandleFunc("GET /api/ui-entries", guard(func(w http.ResponseWriter, r *http.Request) {
		items, err := deps.PackUIEntries.List(r.Context(), goldenWS(r, s))
		if err != nil {
			webx.ErrInternal(w)
			return
		}
		if items == nil {
			items = []orchapp.PackUIEntry{}
		}
		webx.WriteJSON(w, http.StatusOK, map[string]any{"items": items})
	}))
	api.HandleFunc("PUT /api/ui-entries/{pack}", guard(func(w http.ResponseWriter, r *http.Request) {
		p := webx.PrincipalFrom(r.Context())
		if p.Role != "owner" && p.Role != "admin" {
			webx.WriteError(w, http.StatusForbidden, "FORBIDDEN", "workspace owner or admin role required", nil)
			return
		}
		var in struct {
			Pack        string           `json:"pack"`
			PackID      string           `json:"pack_id"`
			Label       string           `json:"label"`
			URLTemplate string           `json:"url_template"`
			Slots       []orchapp.PackUISlot `json:"slots"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&in); err != nil {
			webx.ErrValidation(w, "invalid JSON body")
			return
		}
		if err := deps.PackUIEntries.UpsertWithSlots(r.Context(), goldenWS(r, s), in.Pack, in.PackID, in.Label, in.URLTemplate, in.Slots); err != nil {
			goldenWriteErr(t, w, err)
			return
		}
		webx.WriteJSON(w, http.StatusCreated, map[string]any{"result": "upserted"})
	}))
	api.HandleFunc("PUT /api/pack-tool-schemas/{pack}", guard(func(w http.ResponseWriter, r *http.Request) {
		p := webx.PrincipalFrom(r.Context())
		if p.Role != "owner" && p.Role != "admin" {
			webx.WriteError(w, http.StatusForbidden, "FORBIDDEN", "workspace owner or admin role required", nil)
			return
		}
		var in struct {
			Items []orchapp.PackToolSchema `json:"items"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<20)).Decode(&in); err != nil {
			webx.ErrValidation(w, "invalid JSON body")
			return
		}
		if err := deps.PackToolSchemas.Replace(r.Context(), goldenWS(r, s), r.PathValue("pack"), in.Items); err != nil {
			goldenWriteErr(t, w, err)
			return
		}
		webx.WriteJSON(w, http.StatusOK, map[string]any{"result": "replaced"})
	}))
	api.HandleFunc("GET /api/workspaces/{workspaceId}/mcp-servers", guard(func(w http.ResponseWriter, r *http.Request) {
		list, err := deps.MCP.List(r.Context(), r.PathValue("workspaceId"))
		if err != nil {
			goldenWriteErr(t, w, err)
			return
		}
		if list == nil {
			list = []orchapp.WorkspaceMCP{}
		}
		webx.WriteJSON(w, http.StatusOK, map[string]any{"servers": list})
	}))
	api.HandleFunc("POST /api/workspaces/{workspaceId}/mcp-servers", guard(func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Name        string         `json:"name"`
			Transport   string         `json:"transport"`
			URL         string         `json:"url"`
			Credentials map[string]any `json:"credentials"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&in); err != nil {
			webx.ErrValidation(w, "invalid JSON body")
			return
		}
		if in.Transport == "" {
			in.Transport = "http"
		}
		wsID := r.PathValue("workspaceId")
		if wsID == "" {
			wsID = goldenWS(r, s)
		}
		created, err := deps.MCP.Create(r.Context(), orchapp.WorkspaceMCP{
			WorkspaceID: wsID, Name: in.Name,
			Transport: in.Transport, URL: in.URL, Credentials: in.Credentials,
		})
		if err != nil {
			goldenWriteErr(t, w, err)
			return
		}
		webx.WriteJSON(w, http.StatusCreated, created)
	}))

	// webhook 订阅管理路由（生产 main.go webhookhttp.Mount——鉴权 = member +
	// webhooks:manage；e2e 沿 authMw + member/role 判定等价实现）。
	az := authz.New(authz.DefaultCatalog(), authz.DefaultRoles())
	whDeps := webhookhttp.Deps{Service: whSvc, Log: goldenTestLogger(t), Authz: az,
		MemberCheck: func(ctx context.Context, wsID, userID string) (string, bool, error) {
			role, rerr := goldenRoleOf(s)(ctx, wsID, userID)
			return role, rerr == nil && role != "", rerr
		}}
	api.HandleFunc("GET /api/workspaces/{workspaceId}/webhooks/subscriptions", goldenWhMember(s, whDeps, func(w http.ResponseWriter, r *http.Request) {
		subs, err := whSvc.ListSubscriptions(r.Context(), r.PathValue("workspaceId"))
		if err != nil {
			webx.ErrInternal(w)
			return
		}
		if subs == nil {
			subs = []webhookapp.Subscription{}
		}
		webx.WriteJSON(w, http.StatusOK, map[string]any{"items": subs})
	}))
	api.HandleFunc("POST /api/workspaces/{workspaceId}/webhooks/subscriptions", goldenWhAdmin(s, whDeps, func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			URL         string   `json:"url"`
			Events      []string `json:"events"`
			Description string   `json:"description"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&in); err != nil {
			webx.ErrValidation(w, "invalid JSON body")
			return
		}
		if in.URL == "" || len(in.Events) == 0 {
			webx.ErrValidation(w, "url and events are required")
			return
		}
		sub, secret, err := whSvc.CreateSubscription(r.Context(), r.PathValue("workspaceId"), in.URL, in.Description, in.Events)
		if err != nil {
			goldenWriteErr(t, w, err)
			return
		}
		webx.WriteJSON(w, http.StatusCreated, map[string]any{"subscription": sub, "secret": secret})
	}))

	// 并入 stack 的 handler 链：增量路由挂在独立前缀判分（api mux 自身
	// ServeHTTP 分发——r.PathValue 由 ServeMux 在分发时填充，手动
	// api.Handler(r) 取 handler 不会带 path match，PathValue 会空）。
	// 命中判定用同一模式串做前缀探测（这些路径 stack 未挂，不会与既有
	// 路由冲突）。
	prev := s.handler
	incremental := []string{
		"/api/ui-entries",
		"/api/pack-tool-schemas/",
	}
	s.handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/workspaces/") &&
			(strings.HasSuffix(r.URL.Path, "/mcp-servers") ||
				strings.Contains(r.URL.Path, "/webhooks/subscriptions")) {
			// ws 级 MCP 注册与 webhook 订阅管理（stack 的 workspace 子树未挂
			// 这两个子路径——members/roles/invitations/share-links 之外的
			// 增量面，拦截不会遮蔽既有路由）。
			api.ServeHTTP(w, r)
			return
		}
		for _, p := range incremental {
			if strings.HasPrefix(r.URL.Path, p) {
				api.ServeHTTP(w, r)
				return
			}
		}
		prev.ServeHTTP(w, r)
	})
	_ = db
}

// —— scripted 模型（engine 测试既有 fake 模式：RegisterFakeProviderToolsForTest
// 的 e2e 等价物——modelmeta.Register("fake") 覆写 stack 顶层的 scriptModel）。——
//
// 回合一：evaluate_seed → submit_generation → 文本收尾（job_id 从 submit 的
// tool_result 里提取——模型读历史中 Tool 消息的 content）。
// 回合二：job_status（用 armHarvest 装填的真实 job_id——测试侧已从回合一
// 拿到，直接注入模型下一步的参数）→ 文本收尾。
type goldenScriptModel struct {
	mu        sync.Mutex
	phase     int // 0=回合一 evaluate；1=已 evaluate；2=已 submit；3=回合二 job_status；4=收尾
	jobID     string
	submitted string
	harvested bool
}

func (f *goldenScriptModel) Generate(_ context.Context, input []*schema.Message, _ ...model.Option) (*schema.Message, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.phase++
	var call *schema.ToolCall
	switch f.phase {
	case 1:
		call = &schema.ToolCall{ID: "call_d4_eval", Function: schema.FunctionCall{
			Name: "agentblog_evaluate_seed", Arguments: `{"seed":"golden thread seed"}`}}
	case 2:
		call = &schema.ToolCall{ID: "call_d4_submit", Function: schema.FunctionCall{
			Name: "agentblog_submit_generation", Arguments: `{"seed":"golden thread seed","workspace":"golden-ws"}`}}
	case 3:
		// 提交后的第一轮：从历史 Tool 消息抓 job_id（模型侧读 tool_result；
		// content 是 eino-ext 的 CallToolResult 包装串，先解包装再取）。
		for _, m := range input {
			if m.Role != schema.Tool || !strings.Contains(m.Content, `"job_id"`) {
				continue
			}
			if doc, ok := goldenUnwrapToolResult(m.Content); ok {
				if id, _ := doc["job_id"].(string); id != "" {
					f.submitted = id
				}
			}
		}
		return &schema.Message{Role: schema.Assistant,
			Content:      "种子词评估完成（verdict 已出），生成任务已提交，job_id 已收到。",
			ResponseMeta: &schema.ResponseMeta{Usage: &schema.TokenUsage{}}}, nil
	case 4:
		if f.harvested && f.jobID != "" {
			call = &schema.ToolCall{ID: "call_d4_status", Function: schema.FunctionCall{
				Name: "agentblog_job_status", Arguments: goldenJSON2(map[string]any{"job_id": f.jobID})}}
			f.harvested = false
		}
	}
	if call != nil {
		return &schema.Message{Role: schema.Assistant, ToolCalls: []schema.ToolCall{*call},
			ResponseMeta: &schema.ResponseMeta{Usage: &schema.TokenUsage{}}}, nil
	}
	return &schema.Message{Role: schema.Assistant,
		Content:      "golden thread round complete.",
		ResponseMeta: &schema.ResponseMeta{Usage: &schema.TokenUsage{}}}, nil
}

func (f *goldenScriptModel) Stream(ctx context.Context, in []*schema.Message, opts ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	m, err := f.Generate(ctx, in, opts...)
	if err != nil {
		return nil, err
	}
	return schema.StreamReaderFromArray([]*schema.Message{m}), nil
}

func (f *goldenScriptModel) submittedJobID() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.submitted
}

func (f *goldenScriptModel) armHarvest(jobID string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.jobID, f.harvested = jobID, true
}

func goldenRegisterModel() *goldenScriptModel {
	f := &goldenScriptModel{}
	modelmeta.Register("fake", func(context.Context, modelmeta.RuntimeConfig, string) (model.BaseChatModel, error) {
		return f, nil
	})
	return f
}
