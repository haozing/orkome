# D2 交接笔记：agentblog pack 安装 + 桥接联调

> 日期：2026-10-11 · 工作流：D2（波 4）· 任务书：`docs/pack-rollout-plan-2026-10-11.md` §4 D2
> 上游：D1（`D:\code\agentblog\doc\pack-mcp-jiaojie.md`——MCP server 六工具）、B1（三闸门 + schema 存档）、C1（引擎组装 + 注册表）、C2（slots）、A1（输出校验器）
> 结论先行：**pack 通道端到端可用**——真实 D1 server 过三闸门、六工具 schema 存档 PUT、ui entry+slots PUT、引擎桥接组装快照、A1 校验器对真实输出判定合法，全部真跑通过（连续 10/10 全绿、亚秒级）。

## 一、交付清单

| 文件 | 内容 |
|---|---|
| `packs/agentblog/pack.yaml`（新） | 首个真实 pack（v1 格式）：apiVersion pack/v1 / name agentblog / credentials（AGENTBLOG_CALLBACK_TOKEN，仅 MCP 调用方向）/ mcp（streamable_http，url=localhost 占位，credentialRef）/ events（work_item.updated）/ ui.entry（内容工厂）+ ui.slots（workitem.detail.tab 文章预览，{workItemId}，height 480）。**无 mcp.tools 声明段**——六工具全部协议自描述 |
| `packs/agentblog/README.md`（新） | 安装命令、URL 占位说明（provision 不做模板替换）、凭证方向分离表、events/slots 现状、联调测试跑法 |
| `cmd/pack-provision/agentblog_e2e_test.go`（新） | env 门控（`ORKOME_AGENTBLOG_DIR`）真实握手联调测试：自起 D1 fake full-server → install 全链路 → 引擎桥接 → A1 校验器。默认 skip，不进默认 `go test ./...` 依赖面 |
| `internal/orchestration/adapters/engine/registry_assembly.go`（C1 未跟踪新文件的尾部追加） | **D2 联调测试门面**：`BuildMCPTools` / `MCPBridgeForTest`（ToolsForTest/SnapshotForTest/SchemaOf）/ `NewToolRegistryForTest` / `RegisterMCPBridgeForTest` / `ToolRegistryForTest.SnapshotForTest`——全部是既有非导出装配函数的薄导出包装（一处一行透传），非第二个实现。任务书边界"不改既有 Go 文件"的执行说明见 §五 |
| `docs/handoff-d2-install-bridge-2026-10-11.md`（本笔记） | — |

## 二、pack.yaml 字段说明（对照契约文档 §4）

- **`apiVersion: pack/v1`**：B1 双版本放行路径的真实验证（首个 v1 pack 上板）。
- **`credentials[0].key: AGENTBLOG_CALLBACK_TOKEN`**：仅"平台 → agentblog（MCP 调用）"方向。安装时 `--credential AGENTBLOG_CALLBACK_TOKEN=<值>` 提供，provision 经 `credentialRef` 注入 `Authorization: Bearer <值>` 请求头（联调已断言该注入形状）；对端对应 env `AGENTBLOG_MCPACK_TOKEN`（token 空 = `/mcp/pack` 端点 503，fail-closed）。回调 PAT 与 webhook signing secret 严格分离（README 有表）。
- **`mcp[0].url: http://localhost:8080/mcp/pack`**：**localhost 默认值，provision 不做任何模板/环境变量替换**（现状 `mcp.url` 是安装时经 POST mcp-servers 写死的注册值）——换环境时安装前直接编辑 pack.yaml。README 已写明。
- **`events[0]: work_item.updated`**（非 work_item.completed——事件目录审计结论）。url 指向 agentblog 侧 webhook 接收端；**D3 交付前该端点不存在**，安装时订阅创建成功但推送会失败重试——预期状态，D3 落地后闭环。
- **`ui.slots[0]`**：`workitem.detail.tab`（C2 目录冻结的唯一合法标识）、`{workItemId}` 模板变量、height 480。第一方 pack：`--frame-origin` 空 = 探测关闭直接保留。
- **无 `mcp.tools` 段**：六工具 outputSchema 全部协议自描述（D1 侧 mcp-go ListTools 可见），B1 闸门 `source=protocol` 直接过——契约文档 §2.2"协议自描述优先"的落地实证。测试夹具与仓内 pack.yaml 有同构锚点（`assertPackYAMLShapeMatchesRepo`），防 pack 文件漂移。

## 三、联调结果（实际命令输出摘要，2026-10-11 Windows + Git Bash）

```
$ ORKOME_AGENTBLOG_DIR=D:/code/agentblog go test ./cmd/pack-provision/ -run TestAgentblogE2E -count=1 -v
=== RUN   TestAgentblogE2E
    agentblog_e2e_test.go:272: agentblog fake full-server 已就绪：http://127.0.0.1:53808/mcp/pack
- mcp agentblog registered                                    # POST mcp-servers（Bearer 注入断言过）
- mcp agentblog prefix check passed (6 tools)                 # 闸门 1：前缀
- mcp agentblog passed output contract gates (6 tools)        # 闸门 2：output（六工具全 protocol 自描述）
                                                              # 闸门 3：三件套（submit/job_status/cancel_job 齐全）
- pack agentblog archived 6 tool output schemas              # PUT pack-tool-schemas：六条 source=protocol
                                                              #   job_status 存档含 required=[job_id,state] 冻结锚点
- subscription .../hooks/orkome created (1 event types)       # work_item.updated 订阅
- deep link entry agentblog registered (内容工厂, 1 slot(s))  # PUT ui-entries：entry + slots[{slot, label,
                                                              #   url_template 含 {workItemId}, height=480}]
pack agentblog@0.1.0 installed
    agentblog_e2e_test.go:401: job job-204f8b21cf58963c 真实输出:
      {"job_id":"job-...","metrics":{"publish_state":"none"},
       "result":{"post_id":"11cf...","summary":"D2 bridge seed: A Practical Step-by-Step Guide"},
       "seed":"d2 bridge seed","stage":"awaiting_review","state":"awaiting_review","workspace":"ws1"}
--- PASS: TestAgentblogE2E (0.75s)
```

测试链路五段（全部对真实 server，无桩）：

1. **起 D1 server**：`go run ./cmd/full-server`（fake 形态，随机端口，`AGENTBLOG_MCPACK_TOKEN` + `AGENTBLOG_HTTP_ADDR`），就绪探测 `/healthz 200 → /mcp/pack 无凭证 401`（token 未生效=503 也能被抓住）。
2. **install 三闸门**：`install()` 直调（B1/C2 测试惯例）+ fake API 承接 PUT。断言：三闸门全过、schema 存档 PUT 六条全 `source=protocol`、ui entry+slots PUT 形状、MCP 注册 `Authorization: Bearer` 注入、webhook 订阅创建。
3. **引擎桥接**（C1 组装路径 `buildMCPTools` 直连，含 loopback egress guard——见坑 3）：桥接快照 1 服务器 `status=ok tool_count=6`；六工具 `SchemaOf` 全部非空（协议自描述实时回收）；注册表快照（task.context.tools 数据源）六条 `origin=mcp server=agentblog has_output_schema=true concurrency_safe=false`。
4. **job 真实五态推进**：经 `/internal/v1/sites` 建站（manual 模式）→ `agentblog_submit_generation`（秒回 job_id）→ `agentblog_job_status` 轮询至 `awaiting_review`（真实生成链：fakegen + 事件循环推进，产物 post_id 已建稿停在人审门——D1 交接 checklist ④ 的最深可达停进态）。
5. **A1 校验器**：job_status 真实输出过 `NewDefaultValidator()`——结构化探测放行（object）；按存档的冻结 schema 校验放行（声明与输出同源）；反向锚：篡改 `state` 枚举值 → 判违规（reason 含 enum）——证明校验器真的在验这份 schema。

门禁：

```
$ go build ./...                    # 0 错误
$ go test ./... -count=1            # 49 包 ok，0 FAIL（TestAgentblogE2E 默认 skip）
$ golangci-lint run                 # 0 issues（internal/goarch export data typechecking error 为任务书已知环境噪音）
```

## 四、给 D4 端到端的起点状态

1. **pack 侧已就绪**：`packs/agentblog` 可直接对真实部署安装（编辑 url 后）：
   `go run ./cmd/pack-provision install packs/agentblog --base <orkome> --workspace <ws> --pat <admin> --credential AGENTBLOG_CALLBACK_TOKEN=<token>`
2. **D4 金线前置清单**（对照排期 §4 D4）：
   - agentblog fake 形态下 manual 站点停 `awaiting_review`——D4 演示"员工评估 → submit → JobHandleCard"链路时，job_id 可复制/轮询收割已验证；
   - `review_result` 未在联调中调过（fake 形态产物在 awaiting_review 可查）——D4 顺带补；
   - slot 浏览器渲染（PackSlotFrame 真浏览器）未在本测试覆盖（C2 坑 1 同源），D4 用真 pack 过一次，重点看 sandbox 三权限下 postMessage resize。
3. **D3 起点**：events 段已声明 `work_item.updated`，agentblog 侧 `/hooks/orkome` 接收端是 D3 的全部剩余工作。

## 五、边界执行说明（含一处对"不改既有 Go 文件"边界的处理）

- 新增文件：`packs/agentblog/*`、`cmd/pack-provision/agentblog_e2e_test.go`、本笔记。
- **`registry_assembly.go` 的追加**：该文件是 C1 的未跟踪新文件（git `??` 状态，从未提交）。D2 需要从测试包（`cmd/pack-provision`）驱动 engine 的真实组装路径，Go 测试无法跨包访问非导出方法。两个选项：a) 在测试里复制组装逻辑（背离"真跑生产路径"的联调目的）；b) 追加薄导出门面。选 b：追加分两段共 ~60 行，每段一行透传既有非导出函数（`BuildMCPTools`→`buildMCPTools`、`RegisterMCPBridgeForTest`→`registerMCPBridge`、`SnapshotForTest`→`snapshot`），不触碰文件里任何 C1 既有行，不引入行为差异。**若评审认为这超出边界，可整体迁到 `registry_assembly_test.go` 同文件名的非测试版本之外，或改为 engine 包内加一个 `export_test.go`**（但那要求测试进 engine 包，无法与 pack-provision 的 install 链路同进程衔接）——记录在此供裁决。
- **未改任何其他既有 Go 文件**（`tools_mcp.go`/`engine.go` 等 M 状态均为 C1 及更早工作流的遗留，`git diff` 可证 D2 未触碰）。
- agentblog 仓**只读**：只 `go run` 它的 server；`cmd/full-server/full-server.exe`（9 月 30 日产物）为 D1 之前遗留，未动。
- 未做任何 git add/commit。

## 六、遗留坑（诚实清单）

1. **【重要·连接池竞态】vendored mcp-go v0.43.0 零值 `http.Client` 的偶发 90s 挂起**：
   `transport.NewStreamableHTTP` 用 `&http.Client{}`（共享 `http.DefaultTransport` 连接池，`IdleConnTimeout=90s`）。go-sdk v1.8.0 服务端每次 POST 的 SSE 流随响应结束即关连接；客户端下一请求若复用到这条已被服务端关闭的空闲连接，会挂起直到连接池 90s idle 清理——实测约 1/4 概率，三个阶段（provision 握手 ListTools 超时 / 引擎桥接 ListTools 0 工具 / 会话 call 卡 90s）都中过招。
   - 测试规避：`TestAgentblogE2E` 开头临时置 `http.DefaultTransport.DisableKeepAlives=true`（t.Cleanup 还原）——10/10 全绿、亚秒级。
   - **生产影响评估**：引擎 mcpCatalog 是长会话缓存（连接持续使用不进 idle 池），provision 是一次性 CLI（无复用窗口）——生产路径基本不受影响；但**建议**后续给 mcp-go 客户端构造显式传 `transport.WithHTTPClient(&http.Client{Transport: 独立实例})`，或升级 mcp-go（v1.1.1 的 readSSE 已有 ctx 取消关闭 reader 的修复）。这是 engine 包 `tools_mcp.go`/`main.go` 的活，属 C1 归属文件，D2 按边界未动。
2. **JobStatus 停进后不实时**（D1 坑 1 复述）：awaiting_review 快照停进后，人在 console 审核通过不会反映到旧快照；D4 演示"审核后变 published"需 D1 侧加 Review 时重推导钩子，或演示路径规避。
3. **本机联调需 loopback egress 放行**：引擎 SSRF 门默认拦回环（`egressx.DefaultGuard`）。测试内自建 `egressx.NewGuard(["127.0.0.0/8"])`；真机部署 agentblog 时需 `ENGINE_EGRESS_EXTRA_ALLOW_CIDRS=127.0.0.0/8`（或 agentblog 部署在非私网地址）。README 已写。
4. **provision 不做 URL 模板替换**：pack.yaml 的 url/urlTemplate 是安装时写死的值（排期已知现状）；换环境 = 安装前改文件。若后续要环境化，属 provision 新功能（如 `${AGENTBLOG_URL}` 展开），非 D2 范围。
5. **`mcp-go` Tool.OutputSchema 结构体往返丢非标准关键字**（B1 坑 3）：agentblog 六工具 schema 用 RawOutputSchema 原样保留，联调未踩；后续第三方 pack 自描述带 `additionalProperties` 之外关键字时注意。
6. **B1 存档读路径未在联调中走到**：桥接测试的 PackToolSchemas 端口注入空桩（六工具全协议自描述，schema 全部来自实时握手回收——这本身是"协议自描述优先"的真实验证）；存档的写路径（PUT）已断言。存档读路径的端到端（存档 → List → 补位）在 C1 的 `TestArchivedToolSchemas_BackfillPriority` 有 fake server 覆盖，真库版本留给 D4 顺带。
7. **e2e fake API 未校验鉴权头**：capture server 对 PAT/X-Workspace-Id 只透传不校验（B1/C2 同模式）；真库鉴权面在 B1 坑 2 已记录。

## 七、复现命令

```bash
# 联调（本机有兄弟仓时）
ORKOME_AGENTBLOG_DIR=D:/code/agentblog go test ./cmd/pack-provision/ -run TestAgentblogE2E -count=1 -v

# 默认门禁（不依赖兄弟仓）
go build ./... && go test ./... -count=1 && golangci-lint run

# 手动安装（server 先起：AGENTBLOG_MCPACK_TOKEN=<tok> go run ./cmd/full-server）
go run ./cmd/pack-provision install packs/agentblog \
  --base http://localhost:8080 --workspace <ws> --pat <pat> \
  --credential AGENTBLOG_CALLBACK_TOKEN=<tok>
```
