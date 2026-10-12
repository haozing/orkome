# B1 交接笔记：job-handle 三件套安装期闸门 + pack 工具 schema 存档

> 日期：2026-10-11 · 工作流：B1（波 2）· 接口冻结文档：`docs/interfaces-job-handle-2026-10-11.md`（**下游先读它**，本笔记是实现侧补充）
> 需求来源：`pack-tool-contract-design-2026-10-11.md` §2.1/§2.2/§4.1；`pack-rollout-plan-2026-10-11.md` §2 B1

## 1. 改了什么

| 文件 | 变更 |
|---|---|
| `cmd/pack-provision/main.go` | ① 版本双放行 `pack/v0`+`pack/v1`（v1 追加解析期静态校验 `validateV1ToolDecls`）；② `packMCP` 增 `Tools []packMCPTool`（v1 `mcp.tools` 声明段，v0 非 strict 静默忽略）；③ `checkPrefix` 重构为 `handshakeAndGate`——一次握手（Initialize+ListTools）跑三闸门并产出存档清单；④ install 在 v1 闸门通过后 `PUT /api/pack-tool-schemas/<pack>` 落存档；uninstall 先 `DELETE /api/pack-tool-schemas/<pack>` |
| `cmd/pack-provision/provision_test.go` | 四态夹具 + 版本门 + `--no-prefix-check` 豁免语义 + install 全链路存档捕获 + v0 无存档回归（详见 §4） |
| `migrations/1035_pack_tool_schema.up/down.sql` | 新表 `pack_tool_schema`（UNIQUE(workspace_id,pack,server,tool_name)，source CHECK 三值） |
| `internal/orchestration/app/service_packtoolschema.go`（新） | `PackToolSchema` 结构 + `PackToolSchemaService`（Replace/List/Get/Delete）——**C1 的读取 API 在这里** |
| `internal/orchestration/adapters/pgrepo/repo_packtoolschema.go`（新） | 上述接口的 pg 实现（Replace = delete+insert 幂等覆盖） |
| `internal/orchestration/adapters/pgrepo/repo_packtoolschema_test.go`（新） | TEST_DATABASE_URL 门控的迁移+读写集成测试 |
| `internal/orchestration/adapters/http/handlers_packtoolschema.go`（新） + `routes.go` | `GET/PUT/DELETE /api/pack-tool-schemas[/{pack}]`（GET members 只读；PUT/DELETE workspace admin） |
| `cmd/orkome-server/wiring_orchestration.go` | Deps 增 `PackToolSchemas: orchapp.NewPackToolSchemaService(repo)`（一行 wire） |
| `arch/sql_allowlist.yml`、`e2e/golden_test.go` | 守卫表登记 `pack_tool_schema`（table_owners=orchestration；e2e TRUNCATE 金表补名）——**这两处是既有守卫测试的强制要求，非越界** |

## 2. 为何这么改

- **闸门搭在前缀握手上**：install 本来就对每个 mcp server 做一次 mcp-go 直连握手（前缀校验），output/三件套闸门需要同样的 ListTools 结果，合进 `handshakeAndGate` 零额外往返。
- **协议自描述优先**（与 A1 冻结文档 §3、契约文档 §2.2 倒置优先级一致）：mcp-go `Tool.OutputSchema.Type != ""` 即自描述（`RawOutputSchema` 也处理）；pack.yaml 声明仅补位。存档 `source` 列如实记录来源。
- **v0 行为完全不变**：`gateOptions{prefixCheck: true}` 与旧 `checkPrefix` 逐行为等价；v0 不写存档（`TestInstallV0NoArchive` 锚定）。既有 e2e `TestProvisionE2E`（v0 pack）零改动照常绿。
- **`--no-prefix-check` 只豁免前缀**：legacy server 豁免是 v0 时代语义；v1 的 output/三件套是契约闸门，不可豁免（测试锚定）。
- **三件套按 server 前缀判定**：`P_submit_*` 存在 ⇒ `P_job_status`+`P_cancel_job` 必须在场；无 submit 则不强制（同步 pack 不造空壳）。只看 ListTools 实际暴露，不看声明。
- **ui 段最小路径**：`entry` 单数不动；v1 yaml 出现 `slots` 段不炸（非 strict 忽略，测试锚定）——slots 落库是 C2+迁移 1036。

## 3. 给 C1 的读取 API（一句话）

**C1 在桥接层组装时调 `(*orchapp.PackToolSchemaService).ListPackToolSchemas(ctx, ws)` 一次拉全量、自建 toolName→schema map（toolName 即桥接层工具名，含 pack 前缀）；未命中再走协议自描述，都无则回落 A1 结构化探测——详见 `docs/interfaces-job-handle-2026-10-11.md` §6。**

```go
// 包 orkome/internal/orchestration/app（构造：NewPackToolSchemaService(repo)）
type PackToolSchema struct {
    WorkspaceID  string         // 冗余便于调试
    Pack, Server string
    ToolName     string         // = 桥接层 toolName（含前缀全名）
    OutputSchema map[string]any // JSON Schema 对象
    Source       string         // "protocol" | "pack_yaml"
    InstalledAt  time.Time
}
func (s *PackToolSchemaService) ListPackToolSchemas(ctx context.Context, ws string) ([]PackToolSchema, error)
func (s *PackToolSchemaService) GetPackToolSchema(ctx context.Context, ws, toolName string) (PackToolSchema, bool, error)
```

注意：C1 拿 service 的方式建议直接复用 wiring 里的 repo 构造（`orchpgrepo.New(db)` → `orchapp.NewPackToolSchemaService(repo)`），不要走 HTTP。

## 4. 测试实况（2026-10-11，Windows + Git Bash）

- `go test ./cmd/pack-provision/... -count=1` → `ok orkome/cmd/pack-provision 0.8s`（20 个子测试全绿，含四态夹具）
- 四态映射：①合法 pack（三件套+全契约）= `TestInstallGates/v1_legal_pack…`×2；②submit 缺 job_status = `…/v1_pack_with_submit_but_missing_job_status_fails`；③工具无 schema 无声明 = `…/v1_tool_with_neither…fails`；④v0 旧 pack 照常安装 = `…/v0_semantics_unchanged…` + `TestInstallV0NoArchive` + 既有 `TestCheckPrefix`/`TestParsePack`。
- 附加锚定：版本门（未知版本拒、v1 声明前缀错拒、非对象 output 拒、slots 容忍）、声明了 server 没有的工具拒、`--no-prefix-check` 不豁免新闸门、install 全链路存档 PUT 捕获（protocol/pack_yaml 来源各就位）。
- `go test ./internal/arch/ ./e2e/ -count=1` → ok（守卫表适配后）。
- pgrepo 集成测试 `repo_packtoolschema_test.go` 需 `TEST_DATABASE_URL`，本机未设自动 skip（CI 设了会真跑迁移 1035 up→读写→down 的形状）。

## 5. 留了什么坑（诚实清单）

1. **e2e `TestProvisionE2E` 未在真库跑**（ORCH_E2E_* 未设）：install 全链路用的是 fake API server 捕获 PUT；真库端到端留给 D2（agentblog 上板）联调时顺带验。
2. **存档 PUT 的鉴权假设**：provision 用 PAT（owner/admin）调 `PUT /api/pack-tool-schemas/{pack}`，与 ui-entries 同鉴权面（requireWorkspaceAdmin）。若未来 provision 改用更低权限 token，这里会 403——届时给存档端点单开机器面或让 provision 保持 admin PAT。
3. **mcp-go `Tool.OutputSchema` 结构体序列化丢非标准关键字**：mcp-go 的 `ToolOutputSchema` 是强类型（type/$defs/properties/required 四字段），server 若自描述里带 `additionalProperties` 之类，经 mcp-go 结构体往返会丢。`RawOutputSchema` 路径原样保留不受影响。v0 够用（MCP 生态 outputSchema 主流就是四字段），C1 若需要全保真，建议桥接层直接用 ListTools 的原始 JSON。
4. **多 server pack 的存档聚合（实现期已修）**：最初版本逐 server PUT，会被服务端 pack 级 Replace 互相清掉；现改为所有 server 闸门通过后聚合 `allArchived` 单次 PUT。单测 `TestInstallArchivesSchemas` 锚定单 server 场景；多 server pack 暂无夹具（现网全部单 server），D2 若出现多 server pack 记得补一个聚合断言。
5. **`builtin_default` source 预留未用**：迁移 CHECK 放行了它但 B1 永不写（v1 下"两者皆缺"根本装不进来）；留给后续运行期兜底注入 JobStatus 标准文档（冻结文档 §1）。
6. **冻结文档 §7 的 B2 深链**：`{jobId}` 变量是 B2 自己的活，B1 未动深链模板渲染。

## 6. 边界确认

- 未动 `internal/orchestration/adapters/engine/`（A1/C1）、未改 `internal/orchestration/app|domain` 既有文件（只加新文件）、未动 `web/`。
- 迁移只用 1035；未跑 migrate-up 到任何真实库。
- 零新增依赖；未做任何 git add/commit。
