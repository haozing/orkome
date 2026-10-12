# FP-09 审计：MCP 页"Packs 与集成"三段式（Packs 列表 / ws 级注册表 / PAT / 客户端接入）

> 日期：2026-10-12 · 审计人：FP-09 agent · 环境：后端 :8080（dev，日志 /tmp/orkome-server.log）+ 前端 :5173（未开浏览器，纯静态 + curl 实测）
> 测试账号 `inventory-test@orkome.dev`（workspace owner）· 工作区 `inventory-ws`（e83dd858-7412-4625-b048-0741e5ae6552）
> 方法：七步审计法（全链梳理 → 契约对齐 → curl 实测 → 代码质量 → 后端正确性 → 测试覆盖 → 缺陷清单）
> 测试数据均用 `fp09` 前缀，结束后已清理（4 个 ws MCP 服务器、1 个 ui-entry、1 个 pack-tool-schema、7 个 PAT 全部删除）
> 上游参考：inventory-2026-10-12.md（P0-3 同根因）、inventory-api-2026-10-12.md §2.2/§2.3/§4.4/§4.5/§4.6/§13.6、audit-fp07-agents-2026-10-12.md（agent 级 MCP 结论直接引用）、handoff-a3-packs-page-2026-10-11.md（A3 交付边界）

---

## 0. 一句话结论

三段式的第 1、2 段（Packs 只读列表 / ws 级注册表 CRUD）链路完整、文案披露到位、slots 契约吻合；但 **PAT 创建 100% 失败（P0，ttl 字段名 + 响应形状双错位）**，第 3 段"客户端接入"整体失真（URL、工具清单、上线状态三重虚构，且 **PAT 前缀与 CSRF 豁免前缀不匹配导致所有 PAT 写请求被系统级 403**——外部客户端接入在当前代码下即使改对 URL 也走不通），另发现 MCP 凭证绕过 sealx 加密链明文落库。

**计数：P0 ×1 · P1 ×4 · P2 ×7**

---

## 1. 全链梳理（实测功能面构成）

### 1.1 页面三段式 → 后端全链

| 段 | 前端 | API 封装 | 后端端点 | service → repo |
|---|---|---|---|---|
| 页壳 + 第 3 段 | `web/src/routes/_app.mcp.tsx`（319 行：页标题 `packs.pageTitle`、三客户端 Snippet、工具清单卡、PAT 管理） | `identity.ts:90-99`（listTokens/createToken/revokeToken） | `GET/POST /api/tokens`、`DELETE /api/tokens/{id}`（ploykit vendor `identity/adapters/http/routes.go:65-67`，RequireHuman） | vendor `app/service_token.go:29 CreatePAT` → pgrepo `pats.go` |
| 第 1 段 Packs | `web/src/components/packs-sections.tsx:33-102` PacksInstalledSection | `packs.ts:35-38` listPackEntries | `GET /api/ui-entries`（`routes.go:121`，X-Workspace-Id 头，member 门；PUT/DELETE 另有 admin 门） | `app/service_uientry.go` → pgrepo（slots 迁移 1036 COALESCE 为 []） |
| 第 2 段 ws 注册表 | `packs-sections.tsx:113-362` WorkspaceMcpSection + WorkspaceMcpDialog | `packs.ts:63-97`（list/create/update/delete） | `GET/POST /api/workspaces/{wsId}/mcp-servers`、`PUT/DELETE .../{serverId}`（`routes.go:172-175`，**workspace id 在 URL path**，memberPathMW） | `app/service.go:311-367 MCPServerService`（name/url 必填 + egressx Guard）→ pgrepo `repo.go:520-565` |

第 3 段声明对接的 MCP 运行时端点：`POST+GET /api/mcp/w/{workspaceId}/agent`（`cmd/orkome-server/wiring_mcp.go:45-46`，StreamableHTTP，RequireAuth + workspaceMW + **agent 绑定 PAT 才能调用工具**）。

### 1.2 三段与 FP-07 的并存语义（引用 + 核对）

- ws 级（本页第 2 段）与 agent 级（agent 详情页 MCP tab）并存：resolved 合并同名覆盖、agent disabled 屏蔽同名、删除 ws 服务器无 agent 级悬垂——FP-07 §2.2 已裁，本次复核成立，仅一处修正见 P2-7（`workspace_server_id` 死列）。
- 页面文案 `packs.wsMcpDesc`（zh.ts:68-70）明确写了"智能体私有的服务器在智能体详情页的 MCP 标签页管理"——并存语义有用户侧披露 ✓。

---

## 2. 契约对齐（逐字段核对 + 实测形状）

### 2.1 ws 级 MCP 注册表 4 端点（与前端 packs.ts 逐字段对）

| 端点 | 前端封装 | 后端 wire | 实测 |
|---|---|---|---|
| GET list | `res.servers ?? []` | `{servers:[{id,workspace_id,name,transport,url,has_credentials,created_at}]}`，按 name 排序，credentials `json:"-"` 不回传 | ✓ |
| POST | `{name,transport?,url,credentials?}` | mcpWire（routes.go:473-478）；name/url 必填（service.go:329）；transport 空落 http（repo.go orDefault）；URL 过 egressx；重名 409 `E_REVISION_CONFLICT "mcp server name already taken"`（repo.go:545-548 映射 23505） | ✓ 201 / 409 实测 |
| PUT | 同 POST | **全量覆盖** `SET name/transport/url/credentials=$6`（repo.go:552-561）：不带 credentials 与 `{}` 都落 `{}`=清空；**23505 未映射**（改名撞名 → 500，FP-07 P1-5 已实测双 500，本次不重复） | ✓ 清空实测（§3 #3/#4） |
| DELETE | `{result}` | `{result:"deleted"}` | ✓ |

- `WorkspaceMCPInput.transport?` 可选与后端 orDefault 吻合；前端 Dialog 恒发四字段（含 credentials），无字段名陷阱。
- **transport 值域无任何一层校验**：DB 无 CHECK（迁移 1003:25-33），service 不查，repo 不查——实测 `"banana"` 201 落库（引擎 connect 时才报 `unsupported mcp transport`，tools_mcp.go:226-232）。
- **重名 409 的前端呈现**：Dialog 内 `save.error.message` 直接显示 "mcp server name already taken"（英文原文，无 i18n 映射——与 FP-07 P2-8 同模式，可读但不本地化，不单列缺陷）。

### 2.2 PAT tokens 三端点（P0 根因断点）

| 端点 | 后端（vendor） | 前端 | 判定 |
|---|---|---|---|
| GET /api/tokens | 裸数组 `[{id,name,prefix,expires_at?,last_used_at?,created_at,scope?}]`（model.go:28-37，**无 revoked_at**；pgrepo pats.go:99 `WHERE revoked_at IS NULL` 已过滤撤销） | `PatToken{...revoked_at?}` + `mcp.tokRevoked` 徽章分支（_app.mcp.tsx:71） | 字段吻合；revoked 分支为死代码（P2-4） |
| POST /api/tokens | `{name, ttl_hours, scope?}`（vendor handlers.go:265-272）；**`webx.DecodeJSON` DisallowUnknownFields**（vendor respond.go:27-29）→ 未知字段 400 `E_BAD_JSON "invalid request body"`；响应 **`{pat:{...}, token}` 嵌套** | `identity.ts:94-95` 发 **`{name, ttl_seconds}`**；期待扁平 `PatSecret{token,id,name,prefix}` | **双向错位 = P0**（请求字段 → 100% 400；响应形状 → 修好字段后 `secret.name` 仍 undefined，成功横幅渲染「令牌“”已创建」） |
| DELETE /api/tokens/{id} | `{status:"ok"}` | 忽略响应体 | ✓ |

- `ttl_seconds` 的正主是 `POST /cli-token`（internal/identity/adapters/http/handlers.go:93-95 `TTLS int json:"ttl_seconds"`）——前端把 CLI 交换端点的契约错套在了 PAT 端点上。
- ttl 语义：`ttl_hours<=0 → exp=nil` 永不过期（service_token.go:36-39）；前端 TTL_OPTIONS 以秒定义（0/30d/90d/180d，_app.mcp.tsx:76-81），换算小时为 720/2160/4320，无精度损失。**最小修复**：`identity.ts:95` 改发 `ttl_hours: Math.round(ttlSeconds/3600)` + `createToken` 返回前解包 `res.pat`（唯一调用方 _app.mcp.tsx:105，无需动组件）。

### 2.3 ui-tokens（pack iframe 铸票，非本页直连但属 tokens 全貌）

`POST /api/ui-tokens {plugin, workspace_id}` → 201 `{token, expires_in:60}`（60s 一次性票）；`POST /api/ui-tokens/consume {token}` → `{user_id, workspace_id, plugin}`（internal/identity/adapters/http/handlers_uitoken.go:14-48）。消费方为 pack-entries.tsx / pack-slot-frame / task-tool-renderers——与本页无直接耦合，契约吻合（inventory-api §2.3 一致）。

### 2.4 ui-entries（第 1 段）slots 随行

实测 PUT 带 slots → GET 回读：`slots:[{slot,label,url_template,height}]` 四字段与前端 `PackUISlot`（packs.ts:15-21）及 `PackUISlot`（service_uientry.go:22-27）逐字段吻合；slot 标识目录冻结校验生效（service_uientry.go:66-80 只认 `workitem.detail.tab`，label/url 必填，height ∈ (0,4096]）。空态 `{"items":[]}`（handler nil→[]，handlers_uientry.go:16-18）✓。

### 2.5 pack-tool-schema 三端点

- `GET /api/pack-tool-schemas` → `{items:[PackToolSchema]}`；**无任何 web 前端消费**（全仓 grep：仅 cmd/pack-provision/main.go:804,885 写入 + 引擎 registry 兜底读）。设计即"引擎兜底 + 调试"，未接线 UI 属规划面（记录，不算缺陷）。
- PUT `{items:[...]}` → `{result:"replaced"}`（实测 200），DELETE → `{result:"deleted"}`（实测 200）。字段与 service_packtoolschema.go:14-22 吻合。
- **三端点未进契约表**（router_contract_test.go 无 pack-tool-schemas 条目，routes.go:127-129 实际挂载）——walk 单向对账盲区的延续（P2-6）。

### 2.6 第 3 段客户端接入 vs 真实 MCP 端点（逐项对照）

| 项 | 页面拼法（_app.mcp.tsx） | 真实情况 | 判定 |
|---|---|---|---|
| 端点 URL | `MCP_ENDPOINT = origin + "/mcp"`（:21） | `/api/mcp/w/{workspaceId}/agent`（wiring_mcp.go:45-46，字面量 `/w/` 段 + wsId 必填） | **错**（路径错 + 缺 wsId） |
| 凭证 | 本页 PAT 管理创建的 tk（暗示直接可用） | 端点要求 **agent 绑定 PAT**（`agentOf`：`p.AgentID == ""` → 403 "runtime credential required"，wiring_mcp.go:65-71）；个人 PAT（本页铸的）AgentID 恒空 | **错**（正确入口是 `POST /api/agents/{agentId}/runtime-credentials`，FP-07 §2.1） |
| 工具清单 | 5 个：list_work_items / get_work_item / create_work_item / add_comment / my_work（:22-28） | 实测 tools/list 9 个：claim_task, complete_task, fail_task, get_task, get_task_messages, get_work_item, list_my_work_items, post_comment, report_progress | **1 真 4 假，缺 8** |
| 上线状态 | `mcp.availableAfterLaunch`"MCP 服务上线后可用"徽章 + 工具卡 "comingSoon"（:141,187） | 端点已上线且实测可完成 initialize/tools-list（会话流） | **矛盾** |
| 客户端片段语法 | Codex `[mcp_servers.orkome] url/bearer_token_env_var`、Claude Code `.mcp.json {type:url,headers}`、通用 POST + `Accept: application/json, text/event-stream` | 三个片段的**格式本身**正确（SSE 头、Bearer 拼法对），坏的只是 URL 与 token 语义 | 格式 ✓ 内容 ✗ |

---

## 3. curl 实测记录（关键样本）

| # | 请求 | 结果 | 判定 |
|---|---|---|---|
| 1 | POST /api/tokens `{"name":"fp09-pat-bad","ttl_seconds":3600}` | **400** `{"error":"E_BAD_JSON","message":"invalid request body","details":"json: unknown field \"ttl_seconds\""}` | P0 实锤（DisallowUnknownFields） |
| 2 | POST /api/tokens `{"name":"fp09-pat-good","ttl_hours":24}` | **201** `{"pat":{"id,name,prefix,expires_at,created_at},"token":"ork_…"}` | 修复方向证实 + 响应嵌套形状实锤 |
| 3 | ws MCP PUT **不带** credentials | 200，has_credentials **false**（清空） | FP-07 结论复核成立 |
| 4 | ws MCP PUT `credentials:{}`（前端空 key 的 wire） | 200，has_credentials **false** | 前端空输入 = 必然清空 |
| 5 | POST ws MCP transport=`streamable_http` / =`banana` | **双 201**，transport 原样落库 | 三层零校验（P2-1） |
| 6 | POST 重名 | 409 `E_REVISION_CONFLICT "mcp server name already taken"` | ✓ |
| 7 | POST url=不可解析域名 | 400 `egress: … could not be resolved (… fail-closed)`，message 本身可读 | egress ✓（沙箱 DNS 受限，换 api.github.com 通过） |
| 8 | POST /api/mcp/w/{ws}/agent + **有效个人 PAT**（ork_ 前缀）Bearer | **403 `CSRF token missing or invalid`** | **P1-1 实锤**（CSRF 层在 auth 之前，PAT 豁免前缀不匹配） |
| 9 | 同路径 + 会话 cookie + CSRF 头 | **200** initialize，`serverInfo:"orkome-agent"` | 端点本身活着 |
| 10 | tools/list（会话流） | **9 个工具**（见 §2.6） | 前端清单 1 真 4 假 |
| 11 | tools/call claim_task（会话流） | tool 结果 `E_FORBIDDEN: runtime credential required (agent-bound MCP endpoint)` | agentOf 门 ✓（个人凭证连工具调用也过不了） |
| 12 | PUT /api/ui-entries/fp09-pack 带 slots → GET | 201 upserted → slots 四字段回读 ✓ | §2.4 |
| 13 | PUT/GET/DELETE /api/pack-tool-schemas/fp09-pack | 200 replaced / items 回读 / 200 deleted | §2.5 |
| 14 | DELETE /api/tokens/{id} | 200 `{status:"ok"}` | ✓ |

> 环境注：dev 环境下快速连续 DELETE 偶发 TCP 层 `[000]` 连接复位（隔次出现，重试即成功），疑 Windows loopback keep-alive 竞态，非产品缺陷，未计入清单。

---

## 4. 代码质量（三段式逐一）

| 段 | loading | 错误态 | 备注 |
|---|---|---|---|
| 第 1 段 Packs | 文案 ✓ | `role=alert` + message/requestFailed 兜底 ✓ | 空态带引导（pack-provision CLI）✓；**queryKey 无 workspaceId**（P2-3）；只读列表与 A3 交付边界一致（无健康列/版本号，注释写明原因）✓ |
| 第 2 段 ws 注册表 | 文案 ✓ | 行内 `role=alert` ✓；Dialog 内 `save.error` ✓ | 确认式删除（aria-label 带服务器名）✓；transport/凭证/警示文案全部 i18n 且**语义准确**（`apiKeyKeepLabel` 直接写明"留空则清空"、琥珀警示二次提示——见 P1-4 裁决）✓；transport 下拉缺 streamable_http（P2-1） |
| 第 3 段客户端接入 | — | — | 复制按钮 1.5s 反馈 ✓；secret 一次性展示 + "仅显示一次"文案 ✓；但内容三重失真（P1-2）+ PAT P0（P0-1）+ CSRF 拦截（P1-1） |
| PAT 列表 | loading/error/空态 ✓ | ✓ | 确认式撤销 ✓；过期/撤销/活跃三态徽章 ✓（撤销态为死代码 P2-4）；`key=dataUpdatedAt` 未用——列表无编辑，无串数据风险 |

- 警示文案覆盖审计：inventory P2"workspace MCP 编辑留空 Key 清凭证（UI 已警示）"**复核成立且超出预期**——label 本身（:330 `apiKeyKeepLabel`）+ 琥珀警示（:339-343）双层披露，i18n 双语齐备。
- 撤销 PAT 后列表即时失效（invalidateTokens）✓；创建成功横幅 role=status ✓。

---

## 5. 后端正确性

### 5.1 凭证存储：sealx 加密链未覆盖 MCP 凭证（P1-3）

- agent 模型凭证：`agent_model_credential.encrypted_secret` 走 `Seal.Encrypt`（pgrepo/repo.go:215-227），Seal 未接线时 **fail-closed**（:220 "credential sealer not wired (ENGINE_CREDENTIAL_MASTER_KEY)"）——策略明确。
- MCP 凭证：`workspace_mcp_server.credentials` / `agent_mcp_server.credentials` 均为**明文 jsonb**（迁移 1003:32/40），repo `credJSON()` 直接 `json.Marshal` 落库（repo.go:567-574），读取直 Scan 回 map——**不经过 sealx**。第三方 API Key（Bearer 头）明文躺在 DB。同仓两种策略并存，属架构级不对称：要么补密封链（推荐，复用 Seal），要么文档明确豁免理由。

### 5.2 CSRF PATPrefix 错配（P1-1，本次新发现）

- `main.go:308` `CSRFConfig{Key, TrustLocalhostAnyPort, ExemptPrefixes:["/ws"]}` **未设 `PATPrefix`** → vendor csrf.go:33-37 回落 `DefaultPATPrefix = "tk_"`（vendor auth.go:51）。
- orkome PAT 全部 `ork_` 前缀（wiring_identity.go:48/69/78、wiring_ws.go:30、wiring_orchestration.go:293）。
- `isPATRequest`（csrf.go:157-160）对 `Bearer ork_…` 判 false → CSRF Protect 兜住 → **一切 PAT 携带的非 GET 请求 403**（实测 #8）。CSRF 中间件在 authMw 之外层，请求根本到不了 Authenticate（后者倒是配对了 ork_，auth.go:124）。
- 影响：`/api/mcp/w/{ws}/agent` 的 POST（外部 MCP 客户端唯一入口）、runtime-credentials 之外所有 PAT 写面全灭；`/ws?token=` 因 ExemptPrefixes 豁免幸存。
- **修复一行**：`CSRFConfig` 加 `PATPrefix: "ork_"`。vendor 无需动。

### 5.3 删除服务器时的引用清理

- `agent_mcp_server` 行独立（按名合并进 resolved，非引用），删 ws 服务器无悬垂——FP-07 结论成立。
- **修正 FP-07 一处**：迁移 1003:44 确有 `agent_mcp_server.workspace_server_id uuid REFERENCES workspace_mcp_server(id)` 外键列，但**全仓 Go 代码零引用、恒 NULL**（P2-7 死列）——功能结论不变（无 FK 风险），schema 叙述修正。
- `pack_tool_schema.server` 以**名字**引用 ws 服务器（service_packtoolschema.go:17），删服务器后存档行悬垂，靠 pack-provision 卸载流程清理（main.go:804）——生命周期外置，记录不判缺陷。

### 5.4 PAT scope 与过期

- vendor `CreatePAT` 支持 `scope *CredentialScope`，但前端 createToken 从不下发 → 页面创建的 PAT **恒为用户全权限、可跨其全部工作区**；页面无任何 scope/权限披露文案（P2-4）。
- 默认选中 ttl=0（永不过期）——选项存在合理，但默认值配永久凭证值得商榷（同 P2-4 记录）。

### 5.5 SSRF / 权限门

- ws MCP url 过 egressx Guard（service.go:322-325 Create / :348-351 Update），不可解析域名 fail-closed 400（实测 #7）✓。
- ws 注册表写面（POST/PUT/DELETE）仅 `memberPathMW` 成员门（routes.go:172-175）——同页 ui-entries/pack-tool-schema/credentials 的写面都是 `requireWorkspaceAdmin`（handlers_credentials.go:10-17）；任何 member 可注册对所有 agent 生效的 ws 级 MCP 服务器（含凭证），权限面不一致（P2-5）。agent 级 `mustOwnAgent` 也只查存在性不查归属（service.go:427-434，FP-07 同结论）。

---

## 6. 测试覆盖

| 域 | 测试 | 盲区 |
|---|---|---|
| A3 前端 13 测 | `web/test/api/packs.test.ts` ×5（URL/wire/解包/null 兜底）+ `web/test/components/packs-sections.test.tsx` ×8（三态/增删改） | **PAT 面 0 覆盖**（P0-1 所在的 route 文件无组件测试——identity.test.ts 只测 createWorkspace/joinByShareCode/meQueryOptions，tokens 面零测试）；客户端片段 0 覆盖（P1-2 的 URL 虚构无任何测试能拦）；编辑流的"凭证保留/清空"分支只测了 create 发 `credentials:{}`，未测 has_credentials 服务器的编辑警示与 PUT wire；409/egress 400 渲染未测 |
| B1 pack-tool-schema | pgrepo/repo_packtoolschema_test.go ×1（迁移+repo 往返）+ engine/registry_c1_test.go（存档兜底补 schema） | **HTTP handler 三端点 0 直接测试**（无 handlers_packtoolschema_test.go）；slot 目录冻结校验同样只有 service 层逻辑、无 HTTP 层测试 |
| 契约 walk | router_contract_test.go TestRouteContractWalk | ui-entries ×3 / ws-mcp ×4 / agent-mcp ×4 / `/api/mcp/w` ×2 均在表 ✓；**pack-tool-schemas ×3 缺**（P2-6）；表→mux 单向对账盲区延续（inventory P1 已记模式） |
| PAT 后端 | vendor 自带（仓外） | ttl_hours/DisallowUnknownFields 行为在本仓无守卫——契约测试若覆盖 POST /api/tokens 请求形状即可拦住 P0-1 |

---

## 7. 缺陷清单（P0 → P2，含修复建议）

### P0（1 项）

**P0-1 MCP 页创建 PAT 100% 失败，且修复字段名后仍会渲染空名横幅**
- 断点 1（请求）：`web/src/api/identity.ts:95` 发 `ttl_seconds`；vendor `identity/adapters/http/handlers.go:269` 只收 `ttl_hours` + `webx.DecodeJSON` DisallowUnknownFields（vendor `platform/webx/respond.go:29`）→ 400 `E_BAD_JSON "invalid request body"`（details 才有真实原因 `json: unknown field "ttl_seconds"`，UI 只显示 message——inventory P0-3 实锤复核）。
- 断点 2（响应）：201 响应为 `{"pat":{...},"token"}`（handlers.go:286），前端 `PatSecret{token,id,name,prefix}` 扁平期待 → `secret.name` undefined，`mcp.createdOnce` 横幅渲染「令牌“”已创建」。
- 修复（最小两处，都在 identity.ts）：① `createToken` 改发 `ttl_hours: Math.round(ttlSeconds/3600)`（TTL_OPTIONS 720/2160/4320 整除无损）；② 返回 `{...res.pat, token: res.token}` 解包。唯一调用方 `_app.mcp.tsx:105` 无需动。可选加严：契约测试补 POST /api/tokens wire 断言。

### P1（4 项）

**P1-1 PAT 前缀与 CSRF 豁免前缀错配：所有 PAT 写请求被系统级 403，外部 MCP 客户端在当前代码下不可用**
- `cmd/orkome-server/main.go:308` CSRFConfig 未设 `PATPrefix` → 回落 `"tk_"`（vendor auth.go:51），orkome PAT 恒 `ork_`（wiring_identity.go:48/69/78）→ `isPATRequest`（vendor csrf.go:159-163）恒 false → PAT 的 POST/PUT/DELETE 全被 CSRF 拦截（实测 #8：有效 PAT 打 MCP 端点 403 CSRF；#9 同请求会话 cookie 200）。
- 影响：MCP StreamableHTTP 端点（外部客户端唯一入口）、以及一切 PAT 携带的写请求。页面第 3 段三个片段即使改对 URL 也走不通——本缺陷与 P1-2 共同构成"客户端接入"完整死因链。
- 修复：`main.go:308` CSRFConfig 加 `PATPrefix: "ork_"`（一行）。建议补 e2e：PAT Bearer 打一个写端点断言非 403。

**P1-2 第 3 段"客户端接入"三重失真：URL 错、工具清单 1 真 4 假、"未上线"徽章与已上线事实矛盾**
- URL：`_app.mcp.tsx:21` `origin + "/mcp"` ≠ 实际 `/api/mcp/w/{workspaceId}/agent`（wiring_mcp.go:45-46，漏 `/w/` 段与 wsId）。
- 工具清单：`:22-28` 5 个名字仅 `get_work_item` 存在；实测 9 工具缺 8（§2.6 表）；zh 文案 `mcp.toolListWorkItems`"按状态/负责人/标签筛选"连 `list_my_work_items` 的真实语义（当前 runtime agent 被指派的工单）都对不上。
- 徽章：`:141` `mcp.availableAfterLaunch` + `:187` `comingSoon`，而后端已上线可握手（实测 #9/#10）。
- 修复：MCP_ENDPOINT 改为按当前 workspace 拼真实端点（`useWorkspaceStore` 已在页面里）；工具清单重写为 9 个真名（desc i18n 同步重写）；徽章改为真实状态（或注明"需 agent 绑定凭证 + 管理员铸发"）。同时补一段"如何获得 agent 绑定 token"的指引（现页面暗示用个人 PAT，语义错误，见 §2.6 凭证行）。

**P1-3 MCP 服务器凭证绕过 sealx 加密链，明文落库**
- `migrations/1003_orchestration.up.sql:32,40` credentials jsonb 明文；`internal/orchestration/adapters/pgrepo/repo.go:567-574 credJSON` 直接 marshal、`scanWSMCP/scanAgentMCP` 直读；对照模型凭证的 `Seal.Encrypt` + fail-closed（repo.go:215-227）——同仓两策。
- 修复：workspace/agent MCP credentials 走 Seal 封密（列改 bytea 或 jsonb 存密文 + 读取解密），或最低限度在 ploykit 文档/内部存档标注豁免理由与威胁模型。

**P1-4 PUT 全量覆盖清凭证：后端 PATCH 语义缺失的裁决与两端修法**
- 现状：ws 级 `repo.go:552-561` `SET credentials=$6` 无 coalesce（nil 与 `{}` 同归 `{}`=清空，实测 #3/#4）；agent 级 `repo.go:634` `coalesce($6, s.credentials)`（nil=保留）——**两级语义相反**（FP-07 P2-6 已记）。
- 本页前端核对结论：**不是放大，是必然触发**——Dialog 空输入显式发 `{}`（packs-sections.tsx:254-256），唯一缓解是双层文案（label"留空则清空" + 琥珀警示，§4）。UI 披露完整，风险在"用户无视警示即丢凭证"与两级不一致造成的认知负荷。
- **裁决**：根修在后端 PATCH 缺失。推荐组合拳：① 后端 ws 级 Update 改 `coalesce($6::jsonb, credentials)` 对齐 agent 级（nil=保留，`{}`=显式清空——两语义并存且自洽）；② 前端 Dialog 空输入改发 `credentials: undefined`（保留）+ 新增"清除凭证"勾选发 `{}`；③ 删除琥珀警示（语义反转后文案反向错误）。只改后端不改前端 = 警示文案变谎言；只改前端不改后端 = 无清除入口。必须两端同步。

### P2（7 项）

**P2-1 transport 值域三层零校验 + 下拉缺 streamable_http（A3 遗留 3）**
- 实测 `banana` 201 落库（#5）；引擎 connect 时才报 `unsupported mcp transport`（tools_mcp.go:226-232 default 分支）。下拉仅 http/sse（packs-sections.tsx:111）：编辑存量 `streamable_http` 服务器时受控 select 视觉回落首项而 state 不变，用户一旦触碰下拉即静默改写 transport。
- 修复：service 层白名单（`""/http/streamable_http/sse`，与引擎 switch 对齐）→ 400；下拉补 `streamable_http` 第三项（或未知值动态注入 option）。

**P2-2 packs.ts:76-80 注释漂移：引用不存在的组件、描述与实现相反**
- `updateWorkspaceMcp` 的 doc 注释让编辑时"回传占位保持现状，见 WorkspaceMcpCard 的处理"——全仓无 `WorkspaceMcpCard`（实为 `WorkspaceMcpDialog`），且实际处理恰恰是空输入发 `{}` **清空**（packs-sections.tsx:254-256）。下一个照注释改代码的人会被带偏。修复：随 P1-4 一并重写该注释。

**P2-3 PacksInstalledSection queryKey 无 workspaceId，切工作区显示旧工作区的 pack 列表**
- `packs-sections.tsx:36` queryKey `['pack-ui-entries-page']` 恒定；X-Workspace-Id 头由 `setWorkspaceIdProvider` 按当前 store 动态注入（main.tsx:12），但 react-query 无 key 变化不 refetch：`staleTime 15s`（lib/query.ts:8）+ `refetchOnWindowFocus: false`（:9）+ 路由不 remount → 切 ws 后列表滞留旧数据直到离开页面再回来。对照同页注册表 `['workspace-mcp', workspaceId]`（:125）写法正确。修复：key 加 workspaceId。另 inventory P2"双缓存键不联动"同域，建议一并收敛。

**P2-4 PAT 面：页面铸的令牌恒全权限 + 默认永不过期 + 撤销徽章死代码**
- scope 入参存在（vendor handlers.go:270 `scope *webx.CredentialScope`）但前端从不下发 → 全权限令牌；TTL 默认选中"永不过期"（_app.mcp.tsx:99 `useState(0)`）；页面无权限提示文案。`mcp.tokRevoked` 徽章分支不可达（ListPATs pags 过滤 revoked：vendor pgrepo/pats.go:99；PAT JSON 无 revoked_at 字段：vendor model.go:28-37）。修复：文案披露"此令牌具账号完整权限"；默认 TTL 改 30d；删除死分支或后端补 revoked_at。

**P2-5 ws 级注册表写面 member 级即可操作，与同页其他写面的 admin 门不一致**
- routes.go:172-175 仅 memberPathMW；对照 ui-entries/pack-tool-schema PUT/DELETE、credentials POST 均 requireWorkspaceAdmin（handlers_credentials.go:10）。member 可注册对所有 agent 生效的 MCP 服务器（含自控 URL 的凭证，SSRF 门只挡内网不挡外域）。修复：ws 注册表 POST/PUT/DELETE 收敛 admin（或至少 DELETE/凭证写入 admin）。

**P2-6 pack-tool-schemas ×3 挂载未进契约表 + HTTP 面零测试**
- routes.go:127-129 实际挂载，router_contract_test.go 无条目——walk 单向对账拦不住"挂了未声明"（inventory P1 模式延续）；handler 三端点无直接测试（仅 repo ×1 + engine 兜底 ×1）。修复：契约表补三行 + 一个 handler 冒烟测试。GET 无 UI 消费为设计内规划面（引擎兜底），不另计缺陷。

**P2-7 workspace_mcp_server.workspace_server_id 死列（FP-07 叙述修正）+ schema 按名引用的悬垂面**
- 迁移 1003:44 外键列全仓 Go 代码零引用、恒 NULL（FP-07 §2.2"无外键"修正为"有列但死列"）；`pack_tool_schema.server` 以名字引用 ws 服务器，删服务器后存档悬垂，靠 pack-provision 卸载兜底（main.go:804）。修复：死列随下次迁移清理或注释标注预留意图；engine 兜底读 schema 时可对 server 名做存在性过滤。

---

## 8. 修复优先级建议

1. **P0-1 + P1-1 两个一行级修复**（identity.ts 两处 + main.go CSRFConfig 一行）——合计约 10 行代码，解锁 PAT 创建与全部 PAT 写面，收益最大。
2. **P1-2 第 3 段内容重写**（URL/工具清单/徽章/凭证指引）——纯前端 + i18n，无后端依赖。
3. **P1-4 两级凭证语义统一**（后端 coalesce + 前端清除勾选 + 删警示 + 重写 packs.ts 注释=P2-2）——一次 PR 打包。
4. **P1-3 sealx 覆盖 MCP 凭证**——动 schema 与 repo，需迁移，排后但应立专项。
5. P2-1/P2-3/P2-4 前端体验包；P2-5 权限收敛单独裁决（有兼容影响）；P2-6/P2-7 顺手修。

## 9. 证据留存

- cookie jar /tmp/fp09-cookies.txt；PAT 响应样本 /tmp/fp09-pat2.json（{pat,token} 形状）
- 服务端日志 /tmp/orkome-server.log（03:38-03:44 段含本次全部请求 request_id）
- 测试数据已清理：ws MCP 服务器 ×4（含上次中断会话遗留的 fp09-srv-a）、ui-entries/fp09-pack、pack-tool-schemas/fp09-pack、PAT ×7（含遗留 fp09-ttlhours-ok）——终态 GET 均为空
