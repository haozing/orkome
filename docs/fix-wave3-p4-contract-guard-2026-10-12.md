# fix-wave3 P4：路由契约双向守卫（router contract bidirectional guard）

- 日期：2026-10-12
- 作者：orkome 测试基建 agent（P4）
- 独占文件：`cmd/orkome-server/router_contract_test.go`（改）、`cmd/orkome-server/route_bidirectional_test.go`（新增）
- 禁碰区遵守情况：`main.go` / `wiring_*.go` / `routes.go` 及一切非测试产品代码零改动（见 §6 共享树注记）
- 上游实证：`docs/feature-audit-batch1-2026-10-12.md` P1 第 3 条、`docs/feature-audit-batch2-2026-10-12.md` FP-19

---

## 1. 问题

`TestRouteContractWalk` 只做"表 → mux"单向对账（`mux.Handler()` 探针），证明不了反方向：
**真实挂载但未声明进 routeTable 的路由无人发现**。stdlib `http.ServeMux` 自 go1.22 起不提供
pattern 枚举 API（ploykit 有 "route-recording mux" issue 记录此事），旧测试注释里把反向漂移
交给 "review + 人工对账"——batch1/batch2 审计证明这道防线失效。

## 2. 方案

### 2.1 反向走查（mux → 表）

新文件 `cmd/orkome-server/route_bidirectional_test.go`：

- `enumerateServeMuxPatterns(t, mux) []string`：**自描述反射**遍历 ServeMux 内部路由树，
  逐节点按字段名+Kind 定位（无硬编码偏移），收集全部注册 pattern。运行期自动适配两种布局：
  - go1.22–1.26：`tree *routingNode` + `children map[string]*routingNode`；
  - go1.27+：`tree routingNode`（值）+ `children mapping[K,V]{s []entry; m map}` + `multiChild/emptyChild` 自指针字段（通用发现，未来字段继续兼容）。
  任何未来布局变化都走 `t.Fatalf` 响亮失败，**不会静默返回垃圾**；`GODEBUG=httpmuxgo121=1`
  明确不支持（自检测试会响）。
- `TestRouteContractReverseWalk`：枚举结果逐条要求"在 routeTable 声明过（canonical 归一后）
  或命中显式豁免清单"；**一次列出全部缺失项**，不遇到第一个就停。
- `TestRouteContractExtractorSelfCheck`：枚举器自证——routeTable 全部 /api 相对条目必须被
  树遍历看见；失败即枚举器/布局漂移（不是契约缺口），防止反向结论建立在误读内存上。
- `TestWorkspaceSubroutesDeclared`：workspacehttp 子 mux 的 14 条叶子挂在无方法前缀的
  `/api/workspaces/{workspaceId}/` 子树 mount 后面，顶层枚举不可见。用框架自带的
  `workspacehttp.SubrouteInfos()`（枚举与注册共用 `workspaceSubroutes`，天然不漂移）逐条对账
  routeTable 的 `subRoutes` 声明；同时反向校验 subRoutes 无死键（防笔误）。

### 2.2 走查面扩容到生产组合面（buildTestAPIRouter）

旧 `buildTestAPIRouter()` 少挂了生产的多个 api/root 面，反向走查会漏。补齐（全部测试替身，
注册期不解引用 nil 服务，已逐一核实各框架 Mount 的 nil 安全）：

| 新增挂载 | 对应生产装配 | 覆盖路由 |
|---|---|---|
| `workspacehttp.Mount(r, Deps{})` | main.go:243 | 工作区/邀请/分享链接 + 子树 |
| `quotahttp.Mount` / `audithttp.Mount` | main.go:253 comm.MountAPI | /api/usage、/api/audit×2 |
| `adminhttp.Mount`（AuthCfg 零值） | main.go:254 comm.MountAdmin | FP-19 的 admin×10 |
| `idhttp.MountExtensions`（零 Deps） | main.go:311 | pins×4、ui-tokens×2、cli-token |
| `autohttp.MountPublic` | main.go:307 | 公开 webhook 入站 |
| `contenthttp.MountUploads`（Local 已有） | main.go:308 | /uploads/* GET/HEAD |
| workhttp `ClientUsage` 置非 nil | wiring_work | POST /api/client-usage |
| orchhttp `PackToolSchemas` 置非 nil | wiring_orchestration | pack-tool-schemas×3 |

### 2.3 豁免清单（豁免≠漏登，一条一行写明理由）

`reverseWalkExemptions`（route_bidirectional_test.go）实际命中 **1 条**：

- `/api/workspaces/{workspaceId}/` — 无方法前缀的子树委托 mount，不是端点；叶子由
  `TestWorkspaceSubroutesDeclared` 经 SubrouteInfos 对账。

同 map 内以注释形式记录**走查边界**（不在 buildTestAPIRouter 重建、故永不匹配的平台/框架面）：
`GET /healthz`、`GET /readyz`（进程探针，main.go 内联）、`/metrics`（MetricsToken 指标面）、
`GET /config`（框架 ConfigHandler）、`GET /ws`（需 wswire Hub + redis）、
`/auth/**` + `/api/tokens×3`（框架 `identityhttp.Mount` 认证域——会话/PAT/OAuth/Fed SSO，
需框架 identity 栈替身；正向由 e2e `TestRoutesWalkGuards` G1 兜底）。

任务点名的 uploads/avatars 未走豁免：uploads 直接声明进表（见下），avatars 既有条目本就在表。

## 3. 首跑当场抓出的缺口（37 条，留证）

补登记前第一跑 `TestRouteContractReverseWalk` 输出（与 batch1 P1 / batch2 FP-19 缺口清单一致）：

```
DELETE /api/pack-tool-schemas/{pack}      GET /api/pack-tool-schemas           PUT /api/pack-tool-schemas/{pack}
DELETE /api/pins/{itemType}/{itemId}      GET /api/pins                        PUT /api/pins
PUT /api/pins/reorder                     POST /api/ui-tokens                  POST /api/ui-tokens/consume
POST /cli-token                           GET /api/workspaces                  POST /api/workspaces
PATCH /api/workspaces/{workspaceId}       DELETE /api/workspaces/{workspaceId} GET /api/invitations/mine
POST /api/invitations/{id}/accept         POST /api/invitations/{id}/decline   POST /api/share-links/redeem
GET /api/usage                            GET /api/audit                       GET /api/audit/export.csv
GET /api/admin/stats                      GET /api/admin/users                 PATCH /api/admin/users/{id}/status
PATCH /api/admin/users/{id}/admin         POST /api/admin/users/{id}/impersonate
GET /api/admin/workspaces                 PATCH /api/admin/workspaces/{id}/plan
GET /api/admin/audit                      GET /api/admin/audit/export.csv      GET /api/admin/analytics
POST /api/client-usage                    GET /uploads/{rest...}               HEAD /uploads/{rest...}
POST /webhooks/autopilots/{token}
```

## 4. 处置

**全部 37 条业务路由补进 routeTable（不豁免）**，另加 14 条 workspace 子路由进 `subRoutes`：
routeTable 由 263 行扩至 **314 行**。新增行按域分组、逐组注明来源（FP 编号/装配点）。
两个旗标集合（存量 263 行零改动）：

- `rawRoutes`（4 条 root 绝对路径，正向探针不加 `/api` 前缀）：`POST /cli-token`、
  `GET|HEAD /uploads/*`、`POST /webhooks/autopilots/{token}`；
- `subRoutes`（14 条子树叶子，正向断言委托 mount、反向由 SubrouteInfos 对账）。

正向 `TestRouteContractWalk` 同步支持两类旗标，双向共用同一张表与同一个走查面。

## 5. 门禁（真实树 D:\code\orkome，非影子）

| 门禁 | 结果 |
|---|---|
| `go build ./...` | ✅ exit 0 |
| `go test ./cmd/orkome-server/ -count=1` | ✅ ok 2.3s |
| `go test ./... -count=1` | ✅ 49 包 ok / 0 FAIL |
| `golangci-lint run` | ✅ 0 issues（typechecking 报错行为任务说明点名的 goarch 环境噪音） |

按名验证（真实树）：

```
--- PASS: TestRouteContractReverseWalk     （反向：mux→表）
--- PASS: TestWorkspaceSubroutesDeclared   （子树叶子↔表）
--- PASS: TestRouteContractExtractorSelfCheck（枚举器自证）
--- PASS: TestRouteContractWalk            （正向：表→mux，既有）
--- PASS: TestShareLinksContractRedeem     （既有）
```

未重启共享 dev server；未执行任何 git add/commit。

## 6. 共享树协作注记

门禁期间两次撞上并行 agent 的中间态（均未触碰，等其 owner 自愈）：

1. `internal/orchestration/adapters/pgrepo/repo.go` ↔ 新增 `repo_mcp.go` 重复声明
   （凭据封印版抽取未删旧块）——等待期间用影子树 `D:\t\okm4`（已删，可随时重建）验证并留证；
   owner 随后落地，真实树恢复绿。
2. `cmd/orkome-server/wiring_automation_integration_test.go`（他人新测试文件）pgx v4 写法
   编译错——owner 40 秒内修复。

## 7. 遗留 / 交接

1. **走查面边界**：`/auth/**`、`/api/tokens×3`、`/ws`、`/healthz|readyz|metrics|config` 仍无
   单测级反向守卫（重建需框架 identity 栈/Hub/进程内联 handler）。如需闭环：ploykit 侧落地
   "route-recording mux"（webx.Mux 已有 `Routes()`，产品 Mount 签名从 `*http.ServeMux`
   放宽到 `webx.Router` 即可源兼容），或 e2e 侧加反向枚举。
2. **routes.go 的 `RouteContract` 文档图**（非本 agent 独占文件）与 routeTable 现在存在口径差：
   它缺 pack-tool-schemas、pins、ui-tokens、admin、audit/usage 等。建议后续 wave 以
   routeTable 为唯一事实源，RouteContract 降级为注释或删除。
3. Go 工具链升级时若 `net/http` 内部布局再变：先看 `TestRouteContractExtractorSelfCheck`
   的响亮失败，按 `enumerateServeMuxPatterns` 的 Fatalf 提示扩展枚举器即可。
