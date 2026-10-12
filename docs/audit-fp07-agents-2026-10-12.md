# FP-07 审计：Agents 管理全流程（新建 / 详情 6 tab / 权限 / MCP tab / 执行配置）

> 日期：2026-10-12 · 审计人：FP-07 agent · 环境：后端 :8080（dev，日志 /tmp/orkome-server.log）+ 前端 :5173（未开浏览器，纯静态 + curl 实测）
> 测试账号 `inventory-test@orkome.dev`（workspace owner）· 工作区 `inventory-ws`（X-Workspace-Id: e83dd858-7412-4625-b048-0741e5ae6552）
> 方法：七步审计法（全链梳理 → 契约对齐 → curl 实测 → 代码质量 → 后端正确性 → 测试覆盖 → 缺陷清单）
> 测试数据均用 `fp07` 前缀，结束后已清理（MCP 绑定/ws 级服务器已删，agent 已归档）
> 上游参考：docs/inventory-2026-10-12.md（P0-1 同根因）、inventory-api-2026-10-12.md §4.1/4.4、inventory-frontend-static-2026-10-12.md §2.7/2.8

---

## 0. 一句话结论

Agents 全流程链路结构完整、权限 fence 与密钥脱敏闭环质量高，但**权限 tab 因 members envelope 崩溃整页不可达（P0）**，另有 2 个后端 500 类契约缺陷（nil runtime_config、CHECK 约束未映射）与 agent/ws 两级 MCP 凭证语义相反、重名静默覆盖等一致性问题。

**计数：P0 ×1 · P1 ×4 · P2 ×9**

---

## 1. 全链梳理（实测功能面构成）

### 1.1 页面与 tab 实际构成

| 面 | 文件 | 说明 |
|---|---|---|
| 列表页 | `web/src/routes/_app.agents.index.tsx`（134 行） | 三态筛选 all/active/archived（客户端过滤）、活跃任务数徽章（working-agents）、可见性徽章、新建弹窗、成功跳详情 |
| 详情页 | `web/src/routes/_app.agents.$agentId.tsx`（1255 行） | 6 tab：**base**（名称/描述/对话开场 starters）/ **model**（provider/model/baseUrl/凭证/max_concurrent + params 只读 + EnvEditor）/ **skills**（绑定/启停/解绑/选择器）/ **mcp**（专属 CRUD + resolved 合并视图）/ **perm**（visibility 三选 + 调用白名单）/ **tasks**（运行记录，即"审计"位） |
| 新建弹窗 | `web/src/components/agent/create-agent-dialog.tsx` | name/description/provider(硬编码 openai)/model/baseUrl/credential；关闭即 reset |
| API 封装 | `web/src/api/orchestration.ts`（:120-217 agents 面） | envelope 解包 `agents/servers/env ?? []` 与后端一致 |
| 凭证字段 | `web/src/components/agent/credential-field.tsx` | 下拉选已有 + 内联创建（credentials 域） |
| tab 状态 | URL search `?tab=`（zod 校验 :43-45） | 刷新可保持 tab；非法值回落 base |

> 注：任务书写的"审计 tab"实际是 **tasks** tab（任务记录含失败原因/重试次数/触发类型/工作项链接 + 手动刷新）。

### 1.2 后端链

```
routes.go:140-158（agents 路由注册，memberMW 成员门）
  → app/service.go AgentService（Create/Get/List/Update/Archive/CancelTasks）
  → app/service.go MCPServerService（ws 级 + agent 级 CRUD + Resolved）
  → app/ports.go:698 ResolveForEngine（引擎侧合并，agent 凭证优先）
  → domain/rules.go:119 MergeMCP（同名覆盖 + disabled 过滤 + 排序）
  → adapters/pgrepo/repo.go（agent 表 :40-99、ws MCP :539-567、agent MCP :584-655）
```

- runtime-credentials：`handlers_external.go:91`（POST only，admin 门 `handlers_credentials.go:10`，仅 `runtime_kind=external` 可铸）
- env 独立端点：`handlers_taskview.go:501-540`（GET 明文 / PUT 写回 runtime_config.env）

---

## 2. 契约对齐（逐字段核对 + 实测形状）

### 2.1 Agent CRUD

| 端点 | 前端封装 | 后端 | 实测 |
|---|---|---|---|
| GET /api/agents | listAgents `res.agents ?? []` | `{"agents":[...]}` nil→[] | ✓ |
| POST /api/agents | createAgent | 201 + Sanitized() | ✓（默认 max_concurrent=6、visibility=public、runtime_kind=engine、starters=[]） |
| GET /api/agents/{id} | getAgent | 附 `invocation_targets`（nil→[]） | ✓（:11 实测 shape） |
| PUT /api/agents/{id} | updateAgent | 全量替换 + mergeRuntimeConfig | ✓（mask/unmask 闭环，见 §5） |
| POST archive/restore | archiveAgent | `{"result":"archived\|restored"}` | ✓ |
| POST cancel-tasks | cancelAgentTasks | `{"cancelled":n}` | 未触发（无运行任务） |
| GET/PUT env | getAgentEnv/putAgentEnv | `{"env":{}}` | ✓ |
| GET /api/working-agents | listWorkingAgents | `{"agents":[]}` | ✓ |
| POST runtime-credentials | **前端无封装**（CLI/外部 runtime 用） | `{token}` 201 / 400 external-only / 403 非 admin | ✓（engine agent POST→400；GET→405） |

### 2.2 MCP 两级注册表（核心语义表）

| 行为 | ws 级（/api/workspaces/{wsId}/mcp-servers） | agent 级（/api/agents/{id}/mcp-servers） | 一致性 |
|---|---|---|---|
| POST 重名 | **409** "mcp server name already taken"（repo.go:545-548 映射 23505） | **静默覆盖**（upsert ON CONFLICT (agent_id,name) DO UPDATE，repo.go:620-628；实测同名 POST 返回同一 id、url 被改） | ✗ 不一致 |
| PUT 不带 credentials | **清空**（`credentials=$6`，credJSON(nil)={}，实测 has_credentials→false） | **保留**（`coalesce($6,s.credentials)`，repo.go:634；实测 has_credentials→true） | ✗ **语义相反** |
| PUT credentials:{} | 覆盖为 {}（清空） | 覆盖为 {}（清空，实测） | ✓ |
| PUT 改名撞已有名 | **500**（23505 未映射，repo.go:552-561） | **500**（repo.go:631-647，实测） | ✗ 两级都错 |
| transport 缺省 | http（orDefault） | http | ✓ |
| resolved=1 合并 | — | agent 级同名覆盖 ws 级（实测 :19b）、agent enabled=false 屏蔽同名 ws 条目（MergeMCP rules.go:119-135 `taken` 无视 Enabled）、ws 恒 enabled=true | ✓ |
| 删 ws 级服务器后 agent 绑定 | agent_mcp_server 是独立行（无外键引用 ws 服务器），**无悬垂**；resolved 只少一条 | ✓ 设计安全 | ✓ |

> **修正 inventory-2026-10-12.md §三 P2"agent 级 MCP 编辑对话框无警示"**：agent 级对话框 label `agents.apiKeyKeepLabel`（zh.ts:166）已正确写明"留空=保留已存凭证"，且后端 coalesce 语义安全——它缺的不是警示，而是**清除已存凭证的入口**（见 P2-6）。ws 级琥珀警示（zh.ts:80 apiKeyClearWarning）对应其"留空=清空"语义，是对的。

### 2.3 权限（perm）

- 后端 fence（实测全过）：
  - private/public + 非空 targets → 400 `invocation targets require public_to visibility`
  - member target user_id 非工作区成员 → 400 `invocation target user is not a workspace member`
  - type 枚举（workspace 不带 user_id / member 必带）、(type,user_id) 去重 → 400
  - MemberCheck 未接线 → fail-closed 400
  - 单测：`app/agent_access_test.go` 14 个（starters ≤3×80 rune、targets 各规则、nil keep 语义）
- **前端崩溃（P0-1）**：`membersQueryOptions` → `listMembers`（`web/src/api/identity.ts:71-74`）把 `{"items":[...],"total":1}`（实测形状）当 `Member[]` 用；PermForm 渲染时 `_app.agents.$agentId.tsx:1063` `(members.data ?? []).filter(...)` 对对象调 `.filter` → TypeError（:1038 `memberName` 内 `.find` 同害）。路由无 errorComponent（main.tsx/_app.tsx/__root.tsx 均未配置）→ TanStack 默认错误边界兜整页 → **六个 tab 全部不可达**。与工单页 P0-1 同根因，修 identity.ts 一处可同时修复。

### 2.4 执行配置（model tab）

- **CRUD 层无模型白名单校验**：`modelmeta.ValidConfig`（internal/contract/modelmeta/modelmeta.go:35-37，仅查 provider 已注册+model 非空）只在 dispatch 时检查（ports.go:750-758 EnqueueLeaderDispatch → ModelNotConfigured blocked）。实测 `{"provider":"nonexistent-provider","model":"fake-model-999"}` 创建 **201 成功**。registry 实际只有 openai（engine/modelconfig.go:36）与 fake（engine.go:501，测试用）。
- params 仅只读 JSON 展示（ModelForm :434-441），无温度等参数编辑 UI——参数面未暴露，无校验缺口。
- mergeRuntimeConfig（service.go:159-195）：incoming 键覆盖、`"•••"` 回落 existing、env 整键继承（PUT 无 env 键时不丢）——7 个单测（service_update_merge_test.go）。

---

## 3. curl 实测记录（关键样本）

| # | 请求 | 结果 | 判定 |
|---|---|---|---|
| 1 | GET members | `{"items":[...],"total":1}` | P0-1 根因实证 |
| 2 | POST /api/agents fp07-audit-agent | 201，默认值齐 | ✓ |
| 3 | POST 非法 provider/model | **201** | P2-7 |
| 4 | POST 重名 | 400 `agent name taken` | ✓ |
| 5 | POST description 1.2MB | 400 `invalid JSON body`（1MB MaxBytesReader，routes.go:71） | ✓ |
| 6 | POST max_concurrent_tasks=99999 | **500 E_INTERNAL** | P1-3 |
| 7 | POST visibility=banana | **500 E_INTERNAL** | P1-3 |
| 8 | POST 不带 runtime_config | **500 E_INTERNAL** | P1-2 |
| 9 | PUT private+targets | 400 + field details | ✓ |
| 10 | PUT public_to+member(自己) | 200，GET 附 targets | ✓ |
| 12 | PUT public_to+非成员 user_id | 400 | ✓ |
| 13 | POST agent MCP（example.com）带 api_key | 201 has_credentials=true | ✓ |
| 14 | PUT 不带 credentials | has_credentials **true**（保留） | P2-6 语义 |
| 15 | PUT credentials:{} | has_credentials **false**（清空） | ✓ |
| 16 | POST 同名（url 不同） | 返回**同一 id**、url 被覆盖、无 409 | P1-4 |
| 19 | resolved=1（agent 同名+ws 独有） | agent 级 URL 胜出、ws-only 保留 | ✓ |
| 20 | ws 级 PUT 不带 credentials | has_credentials **false**（清空） | 与 agent 级相反（P2-6） |
| 21 | GET runtime-credentials | **405**（幽灵条目确认） | P2-13 |
| 21b | POST runtime-credentials（engine agent） | 400 `runtime credentials are for external agents`（admin 门在其前） | ✓ |
| 23/24 | MCP PUT 改名撞已有名（agent 级+ws 级） | **双 500** | P1-5 |
| 25/26 | env PUT 后 GET /env vs GET /agents/{id} | /env 明文回读；主接口 `FP07_API_KEY:"•••"` | P2-14（设计权衡） |
| 27 | archive/restore | `{"result":"archived"/"restored"}` | ✓ |
| 28 | GET working-agents | `{"agents":[]}` | ✓ |

> 500 根因（服务端日志 + 代码）：`migrations/1003_orchestration.up.sql:8-9` CHECK（max_concurrent BETWEEN 1 AND 50 / visibility 三值枚举）与 `runtime_config jsonb NOT NULL` 兜底，pgrepo 未把 23514/23505 映射为 400/409，`writeErr` 落 E_INTERNAL。服务层 service.go:60-65/126-131 只做下限与空值默认。

---

## 4. 代码质量（六 tab 逐一）

| tab | loading | 错误态 | 备注 |
|---|---|---|---|
| base | 页级 PageLoading | save.error `role=alert` + ApiError message | dirty 守卫 ✓；`key=dataUpdatedAt` remount 防串数据 ✓ |
| model | 页级 | 同上 + EnvEditor 自带 pending/error 文案 | max_concurrent 手输 99999 直接提交 → 500（P1-3 前端无拦截）；清空输入 Number("")=0 → 后端静默改 6（P2-11） |
| skills | PageLoading | PageError + **onRetry** ✓ | 两段确认解绑 ✓；选择器 400ms 防抖 |
| mcp | 行内文案 | own: PageError + **onRetry** ✓；resolved 无错误分支（isError 静默空态文案） | 编辑 label 已写明"留空=保留" ✓；无清凭证入口（P2-6） |
| **perm** | — | — | **渲染即崩（P0-1）**；private+targets 可保存 → 400 英文错误（P2-8）；崩溃后整页六 tab 不可达（无路由级边界隔离） |
| tasks | PageLoading | PageError **无 onRetry**（:1193-1195，与其余 tab 不一致，P2-9） | 触发类型 i18n 映射 + 未知值原文回退 ✓ |

- 表单脏状态：六 tab 均无"未保存切换提示"，切 tab 即卸载丢弃（P2-10）。
- 列表页：三态筛选 + 分状态空态文案 ✓；无搜索框（现状非缺陷）；归档行 opacity + 徽章 ✓。

---

## 5. 后端正确性

- **密钥脱敏闭环**：Sanitized→MaskSecrets 递归 mask（key/secret/token/password 字样 → "•••"，ports.go:69-99）；update mergeRuntimeConfig 将 "•••" 回落原值（service.go:178-195）——前端全量 PUT mask 后的 runtime_config 不会清密钥。✓
- **SSRF**：agent base_url 与两级 MCP url 均过 egressx Guard（Create/Update/CreateAgentMCP/UpdateAgentMCP），实测不可解析域名 fail-closed 400。✓
- **名字唯一**：ws 内 agent 重名 400 ✓；但见 P1-4/P1-5 的 MCP 重名面。
- **悬垂引用**：agent_mcp_server 独立行，删 ws 服务器无悬垂；resolved 合并按名匹配。✓
- **runtime-credentials**：admin 门（owner/admin）+ engine agent 拒绝 + POST only；幽灵 GET 见 P2-13。
- **env 明文面**：GET /env 无 mask 无 role gate（成员即可读全部 agent env 秘密），主接口 mask——编辑器必需，但属已知安全边界（P2-14，建议文档标注或 admin-only）。

---

## 6. 测试覆盖

| 域 | 测试 | 映射结论 |
|---|---|---|
| 权限 fence | app/agent_access_test.go ×14 + pgrepo/agent_access_migration_test.go ×3 | 充分（starters/targets 全规则 + 迁移形状） |
| runtime_config 合并 | app/service_update_merge_test.go ×7 | 充分（mask 回落/env 继承/nil） |
| MCP 引擎合并 | app/ports_leader_test.go:152 TestResolveForEngine | 同名覆盖+凭证优先+ws-only 回落 ✓；**"agent disabled 屏蔽同名 ws 条目"分支无对照**（off 无 ws 同名行） |
| domain.MergeMCP | **零直接单测**（rules_test.go 7 测全在 attribution/retry） | 盲区 |
| tools_mcp | adapters/engine/tools_mcp_test.go ×7 | SSRF 门/transport/snapshot ✓ |
| 前端 | **web/src 无任何 *.test.* 文件** | 六 tab/新建弹窗/列表全部无组件测试（perm 崩溃正是测试盲区的直接后果） |

---

## 7. 缺陷清单（P0 → P2，含修复建议）

### P0（1 项）

**P0-1 权限 tab 渲染即崩，崩溃后整页六 tab 不可达**
- 断点：`web/src/routes/_app.agents.$agentId.tsx:1063`（`(members.data ?? []).filter` → `.filter is not a function`；`:1038` memberName 内 `.find` 同害）
- 根因：`web/src/api/identity.ts:71-74` listMembers 将 `{"items":[],"total":n}`（实测）当数组
- 影响：与工单详情 P0-1 同根因（members envelope 一处适配修五处崩溃）
- 修复：① identity.ts 适配 `res.items ?? []`（推荐，最小改动）；② 详情页路由加 errorComponent 或将 members query 下沉进 PermForm 内部边界，避免单 tab 崩溃拖垮整页。

### P1（4 项）

**P1-2 POST /api/agents 不带 runtime_config → 500**
- `internal/orchestration/adapters/pgrepo/repo.go:44-49` INSERT $4 nil map → jsonb NOT NULL 违约 → E_INTERNAL。实测 500。
- 修复：service.go Create 入口 `if a.RuntimeConfig == nil { a.RuntimeConfig = map[string]any{} }`（或 SQL `coalesce($4,'{}'::jsonb)`）。

**P1-3 max_concurrent>50 / visibility 非法值 → 500（DB CHECK 泄漏）**
- 服务层无上限/枚举校验（service.go:60-65、126-131），`migrations/1003_orchestration.up.sql:8-9` CHECK 23514 未映射 → 500。实测双 500。前端 ModelForm `max=50` 为 HTML-only，手输可提交。
- 修复：service 层显式校验（1-50 + visibility 三值枚举，返回 field error 400）。

**P1-4 agent 级 MCP 重名 POST 静默覆盖既有条目（与 ws 级 409 不一致）**
- `repo.go:620-628` upsert ON CONFLICT；实测同名 POST 返回同一 id 且 url/enabled 被覆盖、无确认。ws 级 Create 是 409（repo.go:545-548）。zh 文案 `serverNameLabel`"同名覆盖既有条目"有披露，但无二次确认。
- 修复：POST 拆 create 语义（23505→409），或前端重名时二次确认。

**P1-5 MCP PUT 改名撞已有名 → 500（两级皆漏）**
- `repo.go:631-647`（agent 级）与 `repo.go:552-561`（ws 级）UPDATE 均未映射 23505 → 500。实测双 500。
- 修复：两处补 `errors.As(pgErr) && Code=="23505"` → 409（照抄 CreateWorkspaceMCP 的写法）。

### P2（9 项）

**P2-6 agent 级 MCP 无清除已存凭证入口，且两级凭证语义相反**
- agent 级 PUT nil=保留（repo.go:634 coalesce，实测）vs ws 级 PUT nil=清空（repo.go:554 直接覆盖，实测）；UI 只发 undefined/{api_key}，无法发 `{}` → has_credentials 徽章恒亮。
- 修复：McpServerDialog 加"清除凭证"勾选（发 credentials:{}）；文档标注两级语义差异。

**P2-7 非法 provider/model 创建 201，失败延迟到 dispatch**
- modelmeta.ValidConfig 仅 dispatch 检查（ports.go:750-758）；registry 仅 openai/fake（modelconfig.go:36）。
- 修复：CRUD 时 `modelmeta.Known(provider)` 预检 400，或前端 placeholder 旁提示可选 provider。

**P2-8 perm 表单 private+targets 可提交 → 400 原始英文错误**
- `_app.agents.$agentId.tsx:1064` editorVisible 含 private，:1101 空白名单提示仅覆盖 public_to。后端规则本身正确。
- 修复：切 private 时清空 targets 或禁用保存 + 文案。

**P2-9 tasks tab 错误态无 onRetry**
- `_app.agents.$agentId.tsx:1193-1195`，其余 tab PageError 均带 onRetry。

**P2-10 六 tab 表单无脏状态离开守卫**
- 切 tab 即卸载丢编辑；`key=dataUpdatedAt` remount 防串数据的设计可与"切 tab 前确认"共存。

**P2-11 maxConcurrent 清空提交 Number("")=0 → 后端静默改 6**
- ModelForm :430；用户意图偏离且无提示（与 P1-3 一并修）。

**P2-12 测试盲区：domain.MergeMCP 零直接单测 + 前端零组件测试**
- `domain/rules.go:119-135`（disabled 屏蔽同名分支无对照，ports_leader_test.go:152 只测独立 disabled）；web/src 无 *.test.*。

**P2-13 route-compare 幽灵条目 GET /api/agents/{id}/runtime-credentials**
- `docs/route-compare/orkome_routes.txt:53` 有 GET，实际仅 POST（routes.go:157），实测 GET 405；契约测试也只有 POST（router_contract_test.go:98）。文档行应删。

**P2-14 GET /api/agents/{id}/env 明文返回秘密且无 role gate**
- handlers_taskview.go:501-512（实测 sk-… 明文回读）；主接口已 mask。编辑器必需，但任何 ws 成员可读任意 agent env。建议：文档标注安全边界，或收敛到 owner/admin。

**P2-15 CreateAgentDialog provider 硬编码 openai 单选**
- create-agent-dialog.tsx:117；与后端 registry 对齐但后端加 provider 需改前端代码；可改为从后端读可用 provider 列表（预留）。

---

## 8. 修复优先级建议

1. P0-1 members envelope 一行适配（同时修复工单页四处 + agent perm tab，收益最大）
2. P1-2/P1-3/P1-5 三个 500 类（service 层 fence + 23505/23514 映射，半天量级）
3. P1-4 + P2-6 MCP 两级语义对齐（create 409 + 清凭证入口）
4. P2-8/9/10/11 前端体验补齐
5. P2-12 补 MergeMCP 单测 + 前端组件测试从 perm/mcp 两个最复杂 tab 起步

## 9. 证据留存

- cookie jar /tmp/fp07-cookies.txt；创建响应样本 /tmp/fp07-agent.json
- 服务端日志 /tmp/orkome-server.log（03:07-03:10 段含本次全部 500 的 request_id）
- 测试数据已清理：agent_mcp_server、workspace_mcp_server（fp07 前缀）已删，fp07-audit-agent / fp07-badmodel / fp07-mc-zero 已归档
