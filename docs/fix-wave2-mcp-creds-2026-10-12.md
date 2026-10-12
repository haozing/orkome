# W2-3 交接笔记：MCP 凭证 sealx 密封 + 两级语义统一（409 / 三态）

> 日期：2026-10-12 · 执行：W2-3 agent（MCP 凭证与两级语义）
> 裁决输入：docs/audit-fp09-mcp-page-2026-10-12.md（P1-3 明文落库 / P1-4 两端同步修法）+ docs/audit-fp07-agents-2026-10-12.md（P1-4 重名静默覆盖 / P1-5 改名撞名 500 / P2-6 两级语义相反）
> 对照先例：agent_model_credential 的 `Seal.Encrypt` + Seal 未接线 fail-closed（pgrepo/repo.go CreateCredential）
> 环境：Windows + Git Bash；集成测试用 docker `orkome-test-pg`（localhost:54329）

---

## 0. 一句话

MCP 服务器凭证（ws 级 + agent 级）写入改走 sealx 加密（fail-closed），存量明文由迁移 1038 一次性清空（v0 无兼容）；PUT 凭证三态语义两级统一（缺字段=保留、null/{}=清空、新值=覆盖）；重名两级统一 409（agent 级不再静默 upsert，PUT 改名撞名不再 500）；前端两个对话框 + API 封装同步三态口径。

> **接力说明**：本文件到达时工作树里已有一份未完成的 `repo_mcp.go`（同任务前次中断运行所留，构建因与 repo.go 旧 MCP 函数重复声明而 broken）。本波在其基础上补齐：删旧、接线 app/HTTP 层、迁移、前端、测试。交接笔记此前不存在，本篇为第一份。

---

## 1. 修复清单与实现要点

### 1.1 凭证密封（FP-09 P1-3）

- `internal/orchestration/adapters/pgrepo/repo_mcp.go`（MCP 存储独占文件，自 repo.go 拆出）：
  - 写入 `sealCreds`：非空凭证经 `Seal.Encrypt`（sealx AES-GCM）密封为 `{"sealed":"<base64>"}` 信封落 jsonb；空凭证（nil/{}）恒落 `'{}'` 不触碰 Seal；**Seal 未接线写非空凭证直接报错（fail-closed，对照 CreateCredential）**。
  - 读取 `unsealCreds`：信封 → 解密回明文 map（唯一消费方 engine 的 `ResolveForEngine`，接口形状不变）；`{}`/空 → 无凭证；**其余形状（存量明文残留、损坏数据）一律报错拒绝，绝不回落明文**——解密失败即拒绝该服务器集成。
  - Seal 接线点不变：`cmd/orkome-server/wiring_orchestration.go:64`（`ENGINE_CREDENTIAL_MASTER_KEY`，回落 JWTSecret 派生）。
- HTTP 响应不回传凭证：`WorkspaceMCP.Credentials` / `AgentMCP.Credentials` 均为 `json:"-"`，只回 `has_credentials`（原有设计，测试加了不泄漏断言钉死）。

### 1.2 存量清空（迁移 1038）

- `migrations/1038_mcp_creds_sealed.up.sql`：`UPDATE workspace_mcp_server/agent_mcp_server SET credentials='{}' WHERE credentials::text <> '{}'`（带 WHERE 防死写）。
- `down.sql`：**不恢复**——明文从未备份（这正是清空的目的），密文归应用层 sealx 管，迁移层无钥不涉密文；理由写在文件头。
- v0 无兼容裁决：不做明文→密文就地转换（避免把"短暂可读的明文"固化进迁移历史），重装即重填。

### 1.3 三态语义（FP-09 P1-4 裁决：缺=保留、null/空=清空、新值=覆盖）

| PUT body credentials | wire（`handlers_mcp.go`） | model | SQL（repo 层） | 语义 |
|---|---|---|---|---|
| 字段缺失 | `json.RawMessage` 零长 | `nil` | `coalesce(NULL, 现值)` | **保留** |
| 显式 `null` / `{}` | `"null"`/`"{}"` → 归一非 nil 空 map | 空 map | 落 `'{}'` | **显式清空** |
| 对象 | 反序列化 map | 值 map | 密封信封 | **覆盖** |

- **实现陷阱（防回归）**：wire 不能用 `*map[string]any`——encoding/json 对指针字段的显式 `null` 会解成 nil 指针，与"字段缺失"不可区分，三态塌缩成两态。故用 `json.RawMessage` + `credsWireToModel` 归一（非对象非 null → 400）。有哨兵测试 `TestMCPWireCredentialsIsRawMessage` 拦截改回。
- 两级（ws `mcpWire` / agent `agentMCPWire`）同一口径；Update SQL 用 `coalesce($N::jsonb, 表.credentials)`；Create 无保留语义，缺省即空凭证。
- 服务层原样透传不归一（`internal/orchestration/app/service_mcp_test.go` 钉死：nil 不许被偷换成空 map，反之亦然）。

### 1.4 重名统一 409（FP-07 P1-4 / P1-5）

- repo 层统一出口：`errMCPTaken = apierr.New(apierr.Conflict, "mcp server name already taken")` + `isDupName`（23505）映射；**四处全覆盖**：ws Create（原有）、ws Update 改名（原 500）、agent Create（原静默 upsert，已改 `CreateAgentMCP` 纯 INSERT）、agent Update 改名（原 500）。
- 接口更名：`app.Repo.UpsertAgentMCP` → `CreateAgentMCP`（ports.go/service.go 同步）；service 层测试断言**永不再调 upsert**。
- 不受影响的调用方已核对：`cmd/pack-provision/main.go` install 流程先 GET 列表、同名 skip 后才 POST，不依赖 upsert。

### 1.5 前端同步（三态口径）

- `web/src/api/packs.ts`：`WorkspaceMCPInput.credentials?: Record<string, unknown> | null`；重写 updateWorkspaceMcp 注释（P2-2 的注释漂移——引用不存在的 WorkspaceMcpCard、语义写反——一并修正）。
- `web/src/api/orchestration.ts`：`upsertAgentMcp` → **`createAgentMcp`**；两函数注释写明三态与 409。
- `web/src/components/packs-sections.tsx`（ws 级对话框）：
  - 琥珀警示反转：原"留空将清除凭证"→ 现仅随"**清除已存凭证**"勾选出现（`packs.apiKeyClearWarning`）；
  - 新增清除勾选（编辑已存凭证的服务器时显示），勾选发 `credentials: null`；
  - 留空 = **不传字段**（显式不设置键，不是 undefined 占位、更不是 `{}`）；输入新值 = 覆盖（Bearer headers 形状不变）；输入动作自动取消勾选。
- `web/src/routes/_app.agents.$agentId.tsx`（agent 级 MCP tab 对话框）：同三态口径 + 清除勾选；改用 `createAgentMcp`；行内启停 toggle（`updateAgentMcp` 不带 credentials）在新语义下正好=保留，行为不变。
- i18n（zh/en 同步）：`packs.apiKeyKeepLabel`、`packs.apiKeyClearToggle`（新）、`packs.apiKeyClearWarning`、`agents.apiKeyKeepLabel`、`agents.apiKeyClearToggle`（新）、`agents.apiKeyClearWarning`（新）；**`agents.serverNameLabel` 的"同名覆盖既有条目"已改为"与已有服务重名将保存失败"**（原文案随 409 裁决变成谎言）。

---

## 2. 文件清单（本波独占改动）

| 文件 | 动作 |
|---|---|
| `internal/orchestration/adapters/pgrepo/repo_mcp.go` | 新文件（前次运行留坯 + 本波补齐），MCP 存储独占维护点 |
| `internal/orchestration/adapters/pgrepo/repo.go` | 仅删旧 MCP 段（List/Create/Update/Delete WS/Agent、credJSON、credJSONOrNull、UpsertAgentMCP），其余只读未动 |
| `internal/orchestration/app/ports.go` | Repo 接口 Upsert→Create 一处 |
| `internal/orchestration/app/service.go` | CreateAgentMCP 改调 repo.CreateAgentMCP（含注释） |
| `internal/orchestration/adapters/http/handlers_mcp.go` | 新文件，MCP handlers 自 routes.go 拆出 + RawMessage 三态 wire |
| `internal/orchestration/adapters/http/routes.go` | 仅删除原 MCP handler 段（注册行原样），其余未动 |
| `internal/orchestration/adapters/pgrepo/repo_mcp_seal_test.go` | 新：密封往返/空凭证/nil Seal fail-closed/unseal 矩阵/23505 映射（纯单测） |
| `internal/orchestration/adapters/pgrepo/repo_mcp_creds_integration_test.go` | 新：TEST_DATABASE_URL 门控，单夹具五子测试（A 加密往返+库内查无明文；B 三态 SQL 两级；C 重名 409 四场景；D 明文读路径 fail-closed；E 迁移 1038 清空+幂等+读恢复） |
| `internal/orchestration/adapters/http/handlers_mcp_test.go` | 新：三态 wire 矩阵（ws/agent 两级）、重名 409、响应不泄漏凭证、RawMessage 哨兵 |
| `internal/orchestration/app/service_mcp_test.go` | 新：Create 非 upsert 哨兵 + 三态透传 |
| `migrations/1038_mcp_creds_sealed.{up,down}.sql` | 新 |
| `web/src/api/packs.ts`、`web/src/api/orchestration.ts` | 三态类型/注释/更名 |
| `web/src/components/packs-sections.tsx` | ws 对话框三态 + 清除勾选 |
| `web/src/routes/_app.agents.$agentId.tsx` | agent 对话框三态 + 清除勾选 + createAgentMcp |
| `web/src/i18n/locales/{zh,en}.ts` | 上述 8 个键 |
| `web/test/api/packs.test.ts`、`web/test/components/packs-sections.test.tsx` | 三态 wire/交互用例改写与新增（16 测） |
| `docs/fix-wave2-mcp-creds-2026-10-12.md` | 本篇 |

未动（他 agent 独占，只读）：`app/batch.go`、`web/src/routes/_app.automation.tsx`、`_app.settings.tsx`、run-logs 相关、`adapters/pgrepo/repo.go` 非 MCP 段。

---

## 3. 测试与门禁结果

| 门禁 | 结果 |
|---|---|
| `go build ./...` | ✅ |
| `go test ./... -count=1`（无 TEST_DATABASE_URL，集成 skip） | ✅ 49 包全 ok，0 FAIL |
| `TestMCPCredsSeal_DB`（带 `TEST_DATABASE_URL=postgres://postgres:orkome_e2e@localhost:54329/orkome?sslmode=disable` 实跑） | ✅ 5/5 子测试 PASS（约 35s，含全量迁移建库） |
| `golangci-lint run` | ✅ exit 0，`0 issues`（伴随一条**既有**工具链噪音：lint 内建编译器读不动新版 Go stdlib export data（`export data version 4 > maximum 2`），报在 `internal/contract/events` 的 stdlib import 上，与本波改动无关，exit code 仍 0） |
| `cd web && npx tsc -b` | ✅ 0 错误 |
| `cd web && pnpm vitest run test/api/packs.test.ts test/components/packs-sections.test.tsx` | ✅ 16/16 |
| `cd web && pnpm test`（全量） | ⚠️ 222 passed / 7 failed —— 7 个失败**全部**在 `test/components/settings-notification-prefs.test.tsx`（settings/通知偏好 agent 的在制品：其 `_app.settings.tsx`+`api/notification.ts` 未完成，base-ui Switch 的 `toBeDisabled` 断言不匹配），与 MCP/凭证/文案改动无交集（该文件自己的 31 项 i18n 键检查全绿） |
| `cd web && pnpm build` | ⚠️ vite/rolldown PARSE_ERROR 于 `src/routes/_app.settings.tsx:6`（`Export 'SettingsPage' is not defined`）——同为其独占文件的在制品断点，本波未触碰该文件；`vite build && tsc -b` 的 tsc 段已单独验证 0 错误 |

> 给 settings agent 的提示：`_app.settings.tsx` 第 6 行 `export { SettingsPage };` 当前解析不到同名定义（rolldown PARSE_ERROR，阻塞全仓 `pnpm build`）；`settings-notification-prefs.test.tsx` 6+1 处 `toBeDisabled` 断言对 base-ui Switch（`aria-disabled`，无 `disabled` 属性）不成立。

---

## 4. 语义速查（给后续维护者）

- **凭证在库里的两种合法形状**：`'{}'`（无凭证）与 `{"sealed":"<base64>"}`（密封信封）。其他形状=异常数据，读路径 fail-closed 拒绝（`legacy plaintext rejected` / `unseal failed`）。
- **三态只在 Update 生效**；Create 缺省即空。前端永远"缺=不传键"，要清空必须勾选（发 `null`）。
- **重名一律 409** `E_REVISION_CONFLICT "mcp server name already taken"`，ws/agent、Create/PUT 四处一致。
- 改 wire 形状前先看 `TestMCPWireCredentialsIsRawMessage`（改回指针会先红）。

## 5. 已知未修（超出本波裁决，留档）

- transport 值域三层零校验 + 下拉缺 `streamable_http`（FP-09 P2-1，动 service/前端下拉，独立裁决）。
- ws 级注册表写面 member 门 vs admin 门不一致（FP-09 P2-5，有兼容影响）。
- `packs-sections.tsx` PacksInstalledSection queryKey 无 workspaceId（FP-09 P2-3，属 packs 页 UI 缓存域，本波只动了对证书）。
