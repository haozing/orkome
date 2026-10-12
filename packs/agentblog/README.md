# agentblog pack

首个 pack 上板（波 4）。六工具（`agentblog_evaluate_seed` / `agentblog_query_knowledge` /
`agentblog_submit_generation` / `agentblog_job_status` / `agentblog_cancel_job` /
`agentblog_review_result`）全部协议自描述 outputSchema，pack.yaml **无 mcp.tools 声明段**
（B1 安装闸门 source=protocol 直接通过）。

契约背景：`docs/pack-tool-contract-design-2026-10-11.md` §4、
`docs/handoff-d2-install-bridge-2026-10-11.md`。

## 安装

前置：agentblog full-server 已在本机（或可达主机）运行，且配置了
`AGENTBLOG_MCPACK_TOKEN`（token 空 = `/mcp/pack` 端点 503，fail-closed 不放行匿名）：

```bash
# agentblog 侧（fake 形态，零外部依赖；fake cms 不需要 PG）
cd D:\code\agentblog
AGENTBLOG_MCPACK_TOKEN=dev-pack-token AGENTBLOG_HTTP_ADDR=:8080 go run ./cmd/full-server

# orkome 侧
cd D:\code\orkome
go run ./cmd/pack-provision install packs/agentblog \
  --base http://localhost:8080 \
  --workspace <ws-id> --pat <owner/admin PAT> \
  --credential AGENTBLOG_CALLBACK_TOKEN=dev-pack-token
```

卸载：`go run ./cmd/pack-provision uninstall packs/agentblog --base ... --workspace ... --pat ...`

### URL 占位说明（重要）

`mcp[0].url` / `events[0].url` / `ui.entry.urlTemplate` / `ui.slots[0].urlTemplate`
写的是 **localhost 默认值**。provision 现状**不做任何模板/环境变量替换**——
`mcp.url` 是安装时经 `POST /api/workspaces/<ws>/mcp-servers` 写死的注册值，
`ui.*` 经 `PUT /api/ui-entries/<pack>` 落库，`events[0].url` 经 webhook 订阅落库。
换部署环境时**安装前直接编辑本 pack.yaml**（或装后走对应注册表端点修改）。

### 凭证说明（方向分离，勿混用）

| 方向 | 凭证 | 本 pack 的角色 |
|---|---|---|
| 平台 → agentblog（MCP 调用） | `AGENTBLOG_CALLBACK_TOKEN`（pack credential；对端 env `AGENTBLOG_MCPACK_TOKEN`） | 本文件 `credentials[0]` + `mcp[0].credentialRef`；安装时以 `--credential AGENTBLOG_CALLBACK_TOKEN=<值>` 提供，provision 注入 `Authorization: Bearer <值>` 请求头 |
| agentblog → 平台（回调 API） | PAT（专用服务账号 `svc-agentblog-pack`，D4 域） | 不在本 pack |
| 平台 → agentblog（事件推送验签） | webhook subscription 的 signing secret（D3 域） | 不在本 pack |

### events 段现状

订阅 `work_item.updated`（`work_item.completed` 事件不存在——事件目录审计结论），
agentblog 侧按 `payload.WorkItemPayload.status` 过滤终态后归档（events inform, never
orchestrate）。**D3 交付前** agentblog 侧 `/hooks/orkome` 接收端不存在：安装时订阅创建
会成功，但事件推送将失败重试——这是预期状态，D3 落地后自动闭环。

### slots 段

`workitem.detail.tab`（文章预览，height 480）。第一方 pack：provision 默认
`--frame-origin` 为空 = 探测关闭、直接保留；第三方部署时可传
`--frame-origin https://<orkome-web-origin>` 让 provision 做 CSP frame-ancestors 探测。
模板变量 `{workItemId}` / `{uiToken}` / `{workspaceId}` 由前端 PackSlotFrame 铸票渲染。

## 联调测试

`cmd/pack-provision/agentblog_e2e_test.go`（env 门控 `ORKOME_AGENTBLOG_DIR`，
默认跳过，不进默认 `go test ./...` 的依赖面）：

```bash
ORKOME_AGENTBLOG_DIR=D:/code/agentblog go test ./cmd/pack-provision/ -run TestAgentblogE2E -count=1 -v
```

自起 agentblog fake full-server（随机端口）→ 三闸门（前缀/output/三件套）→
schema 存档 PUT（六工具 source=protocol）→ ui entry + slots PUT → 引擎桥接组装
（快照六工具、outputSchema 回收）→ A1 校验器对 `agentblog_job_status` 真实输出判定。
