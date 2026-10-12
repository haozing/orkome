# orkome HTTP/WS API 盘点（2026-10-12）

> 事实源优先级：① `cmd/orkome-server/router_contract_test.go`（契约测试 routeTable）
> ② `docs/route-compare/orkome_routes.txt` ③ 各域 `internal/<domain>/adapters/http/routes.go`
> 与 vendored ploykit（v0.4.1）`vendor/github.com/haozing/ploykit/<pkg>/adapters/http/`
> ④ `cmd/orkome-server/main.go` + `wiring_*.go`（组装根）⑤ 前端 `web/src/api/*.ts`（消费面）。
>
> 认证图例：
> **公开** = 不做认证；**登录** = 需会话/PAT（`webx.Authenticate` + `RequireAuth`）；
> **成员** = 登录 + `X-Workspace-Id` header（或路径 workspaceId）+ 工作区成员校验；
> **admin** = 成员 + owner/admin 角色；**runtime** = agent 绑定 PAT（Principal.AgentID 非空）；
> **平台 admin** = 平台级管理员（ploykit admin 中间件）。
>
> 归属图例：**产品** = `internal/*`；**ploykit** = vendored 框架域；**框架** = ploykit platform 级（healthz/metrics 等）。

## 0. 总览

| 分组 | 端点数 | 说明 |
|---|---:|---|
| platform（healthz/readyz/config/metrics/uploads/avatars/ws） | 8 | 含 GET /ws |
| auth + identity 扩展（产品） | 6 | /auth/send-code 等 4 条 + pins 4 条（见 identity 节合并计数） |
| identity/workspace（ploykit） | 39 | auth 全套 19 + tokens 3 + workspaces/members/invitations/share-links/sso 17 |
| work（work-items/comments/labels/projects/statuses/properties/views/batches 前置） | 78 | internal/work |
| orchestration（agents/tasks/squads/mcp/ui-entries/pack-tool-schemas/credentials/builder） | 71 | internal/orchestration + wiring_mcp |
| conversation（chat/quick-actions/pinned-agents） | 30 | internal/conversation |
| notification（产品投影 + ploykit 幸存 3 条） | 19 | internal/notification + ploykit notify |
| skills | 18 | internal/skills |
| content（upload/attachments） | 8 | internal/content |
| automation（wakeups/autopilots/deliveries/title-callback） | 42 | internal/automation |
| channels（title-callback） | 1 | internal/channels |
| webhooks（ploykit 订阅面） | 9 | ploykit webhooks |
| quota + audit + admin（商业面） | 38 | ploykit quota 1 + audit 2 + admin 35 |
| **合计 HTTP** | **≈316**（去重精确数见 §10） | 另有 MCP streamable 端点 1 组（POST+GET 同路径） |

> 注：`POST+GET /api/mcp/w/{workspaceId}/agent` 是 MCP StreamableHTTP 端点（非 REST），单独成节。

---

## 1. platform（根 mux，非 /api 前缀）

| 方法 | 路径 | 认证 | 归属 | 前端调用 |
|---|---|---|---|---|
| GET | /healthz | 公开 | 框架 | 否（运维探针） |
| GET | /readyz | 公开 | 框架 | 否（运维探针；检查 db ping + 迁移 + workers） |
| ANY | /metrics | Token 保护（MetricsToken） | 框架（ploykit metrics） | 否（Prometheus 抓取） |
| GET | /config | 公开 | ploykit webx.ConfigHandler | 是（identity.ts `getServerConfig`） |
| GET | /uploads/{rest...} | 公开（LOCAL 存储模式才有） | 产品 internal/content | 是（图片/附件直链，URL 由 upload-file 返回） |
| HEAD | /uploads/{rest...} | 公开（同上） | 产品 internal/content | 否 |
| GET | /api/avatars/{rest...} | 签名校验（`?exp=&sig=` HMAC，不过 Cookie/PAT） | 产品 internal/content | 是（头像 URL 内嵌签名） |
| GET | /ws | 登录（authMw 包裹；cookie 或 `?token=PAT`） | ploykit wsx Hub | 是（realtime/client.ts） |

`/config` 实测响应（localhost:8080）：`{"billing_channels":[],"csrf_token":"…","oauth_providers":[],"product":"orkome"}`。

---

## 2. auth + identity

### 2.1 认证入口（ploykit identityhttp.Mount → root mux）

| 方法 | 路径 | 认证 | 归属 | 前端调用 |
|---|---|---|---|---|
| POST | /auth/send-code | 公开 | ploykit | 是（identity.ts） |
| POST | /auth/verify-code | 公开（成功即种 cookie） | ploykit | 是（identity.ts） |
| POST | /auth/register | 公开（AllowSignup 开关） | ploykit | 否 |
| POST | /auth/login | 公开（密码登录） | ploykit | 否 |
| POST | /auth/logout | 登录 | ploykit | 是（identity.ts） |
| GET | /auth/me | 登录 | ploykit | 是（identity.ts meQueryOptions） |
| PATCH | /auth/me | 登录 | ploykit | 否 |
| DELETE | /auth/me | 登录（RequireHuman） | ploykit | 否 |
| POST | /auth/forgot-password | 公开 | ploykit | 否 |
| POST | /auth/reset-password | 公开 | ploykit | 否 |
| POST | /auth/send-verification | 登录（RequireHuman） | ploykit | 否 |
| POST | /auth/verify-email | 公开 | ploykit | 否 |
| POST | /auth/change-password | 登录（RequireHuman） | ploykit | 否 |
| POST | /auth/confirm-password | 登录（RequireHuman） | ploykit | 否 |
| GET | /auth/sessions | 登录（RequireHuman） | ploykit | 否 |
| DELETE | /auth/sessions | 登录（RequireHuman，吊销全部） | ploykit | 否 |
| DELETE | /auth/sessions/{sessionId} | 登录（RequireHuman） | ploykit | 否 |
| GET | /auth/oauth/{provider}/start | 公开 | ploykit | 否 |
| GET | /auth/oauth/{provider}/callback | 公开 | ploykit | 否 |
| GET | /auth/fed/start | 公开 | ploykit | 否 |
| GET | /auth/fed/callback | 公开 | ploykit | 否 |

### 2.2 PAT tokens（ploykit identityhttp）

| 方法 | 路径 | 认证 | 归属 | 前端调用 |
|---|---|---|---|---|
| GET | /api/tokens | 登录（RequireHuman） | ploykit | 是（identity.ts listTokens） |
| POST | /api/tokens | 登录（RequireHuman） | ploykit | 是（identity.ts createToken `{name, ttl_seconds}` → `{token,id,name,prefix}`） |
| DELETE | /api/tokens/{id} | 登录（RequireHuman） | ploykit | 是（identity.ts revokeToken） |

### 2.3 产品账户扩展（internal/identity/adapters/http.MountExtensions）

| 方法 | 路径 | 认证 | 归属 | 前端调用 |
|---|---|---|---|---|
| GET | /api/pins | 成员（X-Workspace-Id） | 产品 | 否（后端注册；web 无调用点——占位路由未建） |
| PUT | /api/pins | 成员 | 产品 | 否 |
| PUT | /api/pins/reorder | 成员 | 产品 | 否 |
| DELETE | /api/pins/{itemType}/{itemId} | 成员 | 产品 | 否 |
| POST | /api/ui-tokens | 登录 | 产品 | 是（pack-entries.tsx / pack-slot-frame.tsx / task-tool-renderers.tsx，铸 60s 票 `{token}`） |
| POST | /api/ui-tokens/consume | 登录 | 产品 | 否（票由 pack iframe 内部回换，非 web 直调） |
| POST | /cli-token | 登录 | 产品 | 否（CLI 专用：session→PAT 交换，client-kit 用） |

### 2.4 workspace 与成员（ploykit workspacehttp.Mount → /api 前缀）

| 方法 | 路径 | 认证 | 归属 | 前端调用 |
|---|---|---|---|---|
| GET | /api/workspaces | 登录 | ploykit | 是（identity.ts meQueryOptions） |
| POST | /api/workspaces | 登录 | ploykit | 是（identity.ts createWorkspace） |
| PATCH | /api/workspaces/{workspaceId} | 成员+guard(workspace:update) | ploykit | 否 |
| DELETE | /api/workspaces/{workspaceId} | owner（sensitive guard） | ploykit | 否 |
| GET | /api/workspaces/{workspaceId}/members | 成员+guard(members:read) | ploykit | 是（identity.ts listMembers） |
| PATCH | /api/workspaces/{workspaceId}/members/{userId} | guard(members:write) | ploykit | 否 |
| DELETE | /api/workspaces/{workspaceId}/members/{userId} | guard(members:remove) | ploykit | 否 |
| GET | /api/workspaces/{workspaceId}/roles | guard(roles:manage) | ploykit | 否 |
| POST | /api/workspaces/{workspaceId}/leave | guard(workspace:read) | ploykit | 否 |
| GET | /api/workspaces/{workspaceId}/invitations | guard(invites:read) | ploykit | 否 |
| POST | /api/workspaces/{workspaceId}/invitations | guard(members:invite) | ploykit | 否 |
| DELETE | /api/workspaces/{workspaceId}/invitations/{invitationId} | guard(members:invite) | ploykit | 否 |
| GET | /api/workspaces/{workspaceId}/share-links | guard(invites:read) | ploykit | 否 |
| POST | /api/workspaces/{workspaceId}/share-links | guard(members:invite) | ploykit | 否 |
| DELETE | /api/workspaces/{workspaceId}/share-links/{linkId} | guard(members:invite) | ploykit | 否 |
| GET | /api/invitations/mine | 登录 | ploykit | 否 |
| POST | /api/invitations/{id}/accept | 登录 | ploykit | 否 |
| POST | /api/invitations/{id}/decline | 登录 | ploykit | 否 |
| POST | /api/share-links/redeem | 登录 | ploykit | 是（identity.ts joinByShareCode；注意前端调 `/api/share-links/join`，实际路由是 `/api/share-links/redeem`——见 §9 差异） |
| GET/PUT/DELETE | /api/workspaces/{workspaceId}/sso | guard（fed.go） | ploykit | 否 |

> 前端已无 `GET /api/me`、`GET /api/workspaces/{id}` 单查调用（rebase 后拆为 /auth/me + /api/workspaces）；后端也只挂列表路由，无 `GET /api/workspaces/{id}`。

---

## 3. work（internal/work/adapters/http — work-items/comments/labels/projects/catalog）

认证一律：登录 + X-Workspace-Id 成员门（wsGuard）。标注 RequireHuman 的端点拒绝 agent PAT。

| 方法 | 路径 | 认证 | 归属 | 前端调用 |
|---|---|---|---|---|
| GET | /api/work-items | 成员 | 产品 | 是（work.ts listWorkItems，cursor 分页） |
| POST | /api/work-items | 成员 | 产品 | 是（work.ts createWorkItem） |
| GET | /api/work-items/{workItemId} | 成员 | 产品 | 是（work.ts getWorkItem） |
| PUT | /api/work-items/{workItemId} | 成员 | 产品 | 是（work.ts updateWorkItem，带 expected_revision） |
| DELETE | /api/work-items/{workItemId} | 成员 | 产品 | 是（work.ts deleteWorkItem） |
| POST | /api/work-items/{workItemId}/move | 成员 | 产品 | 否 |
| POST | /api/work-items/query | 成员 | 产品 | 否 |
| POST | /api/work-items/table/rows | 成员 | 产品 | 否 |
| POST | /api/work-items/table/groups | 成员 | 产品 | 否 |
| POST | /api/work-items/table/facets | 成员 | 产品 | 否 |
| GET | /api/work-items/search | 成员 | 产品 | 否 |
| POST | /api/work-items/batch-update | 成员 | 产品 | 否 |
| POST | /api/work-items/batch-delete | 成员 | 产品 | 否 |
| GET | /api/work-items/child-progress | 成员 | 产品 | 否 |
| GET | /api/work-items/children | 成员 | 产品 | 是（work.ts listChildWorkItems `?parent=`） |
| GET | /api/work-items/grouped | 成员 | 产品 | 否 |
| GET | /api/work-items/assignee-frequency | 成员 | 产品 | 否 |
| POST | /api/work-items/preview-trigger | 成员 | 产品 | 否 |
| POST | /api/work-items/quick-create | 成员（RequireHuman） | 产品 | 是（work.ts quickCreateWorkItem） |
| POST | /api/work-items/{workItemId}/reactions | 成员 | 产品 | 否 |
| DELETE | /api/work-items/{workItemId}/reactions | 成员 | 产品 | 否 |
| GET | /api/work-items/{workItemId}/comments | 成员 | 产品 | 是（work.ts listComments） |
| POST | /api/work-items/{workItemId}/comments | 成员 | 产品 | 是（work.ts createComment → `{comment, admission}`；创建即触发 agent） |
| POST | /api/work-items/{workItemId}/comments/trigger-preview | 成员 | 产品 | 是（work.ts previewCommentTrigger） |
| GET | /api/work-items/{workItemId}/labels | 成员 | 产品 | 是（work.ts listWorkItemLabels） |
| POST | /api/work-items/{workItemId}/labels | 成员 | 产品 | 是（work.ts attachLabel `{label_id}`） |
| DELETE | /api/work-items/{workItemId}/labels/{labelId} | 成员 | 产品 | 是（work.ts detachLabel） |
| GET | /api/work-items/{workItemId}/timeline | 成员 | 产品 | 是（work.ts listTimeline） |
| GET | /api/work-items/{workItemId}/subscribers | 成员 | 产品 | 否 |
| POST | /api/work-items/{workItemId}/subscribers | 成员 | 产品 | 否 |
| GET | /api/work-items/{workItemId}/duplicates | 成员 | 产品 | 否 |
| GET | /api/work-items/{workItemId}/metadata/{key} | 成员 | 产品 | 否 |
| PUT | /api/work-items/{workItemId}/metadata/{key} | 成员 | 产品 | 否 |
| DELETE | /api/work-items/{workItemId}/metadata/{key} | 成员 | 产品 | 否 |
| GET | /api/work-items/{workItemId}/properties/{key} | 成员 | 产品 | 否 |
| PUT | /api/work-items/{workItemId}/properties/{key} | 成员 | 产品 | 否 |
| DELETE | /api/work-items/{workItemId}/properties/{key} | 成员 | 产品 | 否 |
| GET | /api/labels | 成员 | 产品 | 是（work.ts listLabels） |
| POST | /api/labels | 成员 | 产品 | 是（work.ts createLabel） |
| GET | /api/labels/{labelId} | 成员 | 产品 | 否 |
| PUT | /api/labels/{labelId} | 成员 | 产品 | 否 |
| DELETE | /api/labels/{labelId} | 成员 | 产品 | 否 |
| GET | /api/projects | 成员 | 产品 | 是（work.ts listProjects） |
| POST | /api/projects | 成员 | 产品 | 是（work.ts createProject） |
| GET | /api/projects/search | 成员 | 产品 | 否 |
| GET | /api/projects/{projectId} | 成员 | 产品 | 否 |
| PUT | /api/projects/{projectId} | 成员 | 产品 | 否 |
| DELETE | /api/projects/{projectId} | 成员 | 产品 | 否 |
| GET | /api/projects/{projectId}/resources | 成员 | 产品 | 否 |
| POST | /api/projects/{projectId}/resources | 成员 | 产品 | 否 |
| PUT | /api/projects/{projectId}/resources/{resourceId} | 成员 | 产品 | 否 |
| DELETE | /api/projects/{projectId}/resources/{resourceId} | 成员 | 产品 | 否 |
| GET | /api/work-item-statuses | 成员 | 产品 | 是（work.ts listStatuses） |
| POST | /api/work-item-statuses | 成员 | 产品 | 否 |
| POST | /api/work-item-statuses/reorder | 成员 | 产品 | 否 |
| PATCH | /api/work-item-statuses/{statusKey} | 成员 | 产品 | 否 |
| GET | /api/properties | 成员 | 产品 | 否 |
| POST | /api/properties | 成员 | 产品 | 否 |
| GET | /api/properties/{key} | 成员 | 产品 | 否 |
| PATCH | /api/properties/{key} | 成员 | 产品 | 否 |
| DELETE | /api/properties/{key} | 成员 | 产品 | 否 |
| GET | /api/work-item-views | 成员 | 产品 | 否 |
| PUT | /api/work-item-views | 成员 | 产品 | 否 |
| GET | /api/work-item-view-preferences | 成员 | 产品 | 否 |
| PUT | /api/work-item-view-preferences | 成员 | 产品 | 否 |
| POST | /api/unsubscribe | 成员 | 产品 | 否 |
| POST | /api/unsubscribe/subtree | 成员 | 产品 | 否 |
| POST | /api/client-usage | 成员（RequireHuman） | 产品 | 否（埋点；web 未接） |
| PUT | /api/comments/{commentId} | 成员 | 产品 | 否 |
| DELETE | /api/comments/{commentId} | 成员 | 产品 | 否 |
| DELETE | /api/comments/{commentId}/keep-replies | 成员 | 产品 | 否 |
| POST | /api/comments/{commentId}/resolve | 成员 | 产品 | 否 |
| DELETE | /api/comments/{commentId}/resolve | 成员 | 产品 | 否 |
| POST | /api/comments/{commentId}/reactions | 成员 | 产品 | 否 |
| DELETE | /api/comments/{commentId}/reactions | 成员 | 产品 | 否 |
| GET | /api/comments/{commentId}/sub-work-item-preview | 成员（RequireHuman） | 产品 | 否 |
| POST | /api/comments/{commentId}/sub-work-items | 成员（RequireHuman） | 产品 | 否 |

（work 域共 78 条）

---

## 4. orchestration（internal/orchestration + wiring_mcp — agents/tasks/squads/mcp/ui/pack）

认证：agents/tasks/squads 等为成员门；`/api/workspaces/{workspaceId}/mcp-servers` 用路径参数做成员门；
带 `runtime` 标注的要求 agent 绑定 PAT；`admin` 标注要求 owner/admin。

### 4.1 agents

| 方法 | 路径 | 认证 | 归属 | 前端调用 |
|---|---|---|---|---|
| GET | /api/agents | 成员 | 产品 | 是（orchestration.ts listAgents） |
| POST | /api/agents | 成员 | 产品 | 是（createAgent） |
| GET | /api/agents/{agentId} | 成员 | 产品 | 是（getAgent；附 invocation_targets） |
| PUT | /api/agents/{agentId} | 成员 | 产品 | 是（updateAgent） |
| POST | /api/agents/{agentId}/archive | 成员 | 产品 | 是（archiveAgent） |
| POST | /api/agents/{agentId}/restore | 成员 | 产品 | 是（archiveAgent） |
| POST | /api/agents/{agentId}/cancel-tasks | 成员 | 产品 | 是（cancelAgentTasks） |
| GET | /api/agents/{agentId}/env | 成员 | 产品 | 是（getAgentEnv） |
| PUT | /api/agents/{agentId}/env | 成员 | 产品 | 是（putAgentEnv） |
| POST | /api/agents/{agentId}/runtime-credentials | admin | 产品 | 否（铸 agent 绑定 PAT `{token}` 201；CLI/MCP 配置用） |
| GET | /api/agents/{agentId}/labels | 成员 | 产品 | 否 |
| POST | /api/agents/{agentId}/labels | 成员 | 产品 | 否 |
| DELETE | /api/agents/{agentId}/labels/{labelId} | 成员 | 产品 | 否 |

### 4.2 tasks（视图 + 外部回写）

| 方法 | 路径 | 认证 | 归属 | 前端调用 |
|---|---|---|---|---|
| GET | /api/tasks | 成员 | 产品 | 是（listWorkspaceTasks，status/agent_id/cursor） |
| GET | /api/tasks/{taskId} | 成员 | 产品 | 否 |
| GET | /api/tasks/{taskId}/messages | 成员 | 产品 | 是（listTaskMessages；kind 白名单 text/tool_use/tool_result/error/approval） |
| GET | /api/tasks/{taskId}/interrupt | 成员 | 产品 | 否（awaiting 任务的 interrupt 载荷 `{task_id,target_id,kind,payload}`） |
| POST | /api/tasks/{taskId}/approve | 成员（RequireHuman） | 产品 | 是（settleTask(true) `{note}` → `{result:"approved"}`） |
| POST | /api/tasks/{taskId}/deny | 成员（RequireHuman） | 产品 | 是（settleTask(false)） |
| POST | /api/tasks/{taskId}/cancel | 成员 | 产品 | 否 |
| POST | /api/tasks/{taskId}/retry-source-context | 成员（RequireHuman） | 产品 | 否（201 带重试任务） |
| POST | /api/tasks/{taskId}/claim | runtime | 产品 | 否（agent 回写环；client-kit 用） |
| POST | /api/tasks/{taskId}/messages | runtime | 产品 | 否（`{kind,call_id,content}` → `{seq}`；心跳兼进度） |
| POST | /api/tasks/{taskId}/complete | runtime | 产品 | 否（`{result}`） |
| POST | /api/tasks/{taskId}/fail | runtime | 产品 | 否（`{reason,detail?}`） |
| GET | /api/agents/{agentId}/tasks | 成员 | 产品 | 是（listAgentTasks） |
| GET | /api/working-agents | 成员 | 产品 | 是（listWorkingAgents） |
| GET | /api/agent-run-counts | 成员 | 产品 | 否 |
| GET | /api/agent-activity-30d | 成员 | 产品 | 否 |
| GET | /api/agent-run-snapshot | 成员 | 产品 | 否（`{items:[…]}` 全工作区活动任务） |
| GET | /api/work-items/{workItemId}/active-task | 成员 | 产品 | 否 |
| GET | /api/work-items/{workItemId}/tasks | 成员 | 产品 | 是（listWorkItemTasks） |
| GET | /api/work-items/{workItemId}/usage | 成员 | 产品 | 否 |
| POST | /api/work-items/{workItemId}/tasks/{taskId}/cancel | 成员 | 产品 | 是（cancelWorkItemTask） |
| POST | /api/work-items/{workItemId}/rerun | 成员 | 产品 | 否 |
| POST | /api/work-items/{workItemId}/tasks/{taskId}/supplements | 成员（RequireHuman） | 产品 | 否 |
| POST | /api/work-items/{workItemId}/tasks/{taskId}/supplements/{commentId}/retry | 成员（RequireHuman） | 产品 | 否 |

### 4.3 squads

| 方法 | 路径 | 认证 | 归属 | 前端调用 |
|---|---|---|---|---|
| GET | /api/squads | 成员 | 产品 | 是（squads.ts） |
| POST | /api/squads | 成员 | 产品 | 是 |
| GET | /api/squads/{squadId} | 成员 | 产品 | 是 |
| PUT | /api/squads/{squadId} | 成员 | 产品 | 是 |
| DELETE | /api/squads/{squadId} | 成员 | 产品 | 是 |
| POST | /api/squads/{squadId}/members | 成员 | 产品 | 是 |
| GET | /api/squads/{squadId}/members | 成员 | 产品 | 是 |
| GET | /api/squads/{squadId}/members/status | 成员 | 产品 | 是 |
| PATCH | /api/squads/{squadId}/members/{agentId} | 成员 | 产品 | 是（`{role}`） |
| DELETE | /api/squads/{squadId}/members/{agentId} | 成员 | 产品 | 是 |
| POST | /api/work-items/{workItemId}/squad-evaluated | 成员 | 产品 | 否（squad 评估回调写入口） |

### 4.4 MCP 注册表（workspace 级 + agent 级）

| 方法 | 路径 | 认证 | 归属 | 前端调用 |
|---|---|---|---|---|
| GET | /api/workspaces/{workspaceId}/mcp-servers | 成员（路径门） | 产品 | 是（packs.ts listWorkspaceMcp） |
| POST | /api/workspaces/{workspaceId}/mcp-servers | 成员（路径门） | 产品 | 是（`{name,transport,url,credentials}` 201） |
| PUT | /api/workspaces/{workspaceId}/mcp-servers/{serverId} | 成员（路径门） | 产品 | 是 |
| DELETE | /api/workspaces/{workspaceId}/mcp-servers/{serverId} | 成员（路径门） | 产品 | 是（`{result:"deleted"}`） |
| GET | /api/agents/{agentId}/mcp-servers | 成员 | 产品 | 是（listAgentMcp；`?resolved=1` 解析继承 → resolvedAgentMcp） |
| POST | /api/agents/{agentId}/mcp-servers | 成员 | 产品 | 是（upsertAgentMcp） |
| PUT | /api/agents/{agentId}/mcp-servers/{serverId} | 成员 | 产品 | 是 |
| DELETE | /api/agents/{agentId}/mcp-servers/{serverId} | 成员 | 产品 | 是 |

### 4.5 MCP StreamableHTTP 端点（agent 运行时，非 REST）

| 方法 | 路径 | 认证 | 归属 | 前端调用 |
|---|---|---|---|---|
| POST | /api/mcp/w/{workspaceId}/agent | 登录 + 成员 + **runtime**（AgentID 必须非空） | 产品（wiring_mcp.go） | 否（client-kit / 外部 MCP 客户端） |
| GET | /api/mcp/w/{workspaceId}/agent | 同上 | 产品 | 否 |

工具注册表（9 个，MCP tool 而非 HTTP 路由）：
`list_my_work_items` / `get_work_item` / `get_task` / `get_task_messages`（读）；
`claim_task` / `report_progress` / `complete_task` / `fail_task` / `post_comment`（回写环）。

### 4.6 credentials / agent-builder / batches / ui-entries / pack-tool-schemas

| 方法 | 路径 | 认证 | 归属 | 前端调用 |
|---|---|---|---|---|
| GET | /api/credentials | 成员 | 产品 | 是（credentials.ts） |
| POST | /api/credentials | admin | 产品 | 是（`{provider,secret}` 201） |
| DELETE | /api/credentials/{credentialId} | admin | 产品 | 是 |
| GET | /api/agent-builder/sessions | 成员 | 产品 | 否 |
| POST | /api/agent-builder/sessions | 成员 | 产品 | 否 |
| GET | /api/agent-builder/sessions/{sessionId}/draft | 成员 | 产品 | 否 |
| PUT | /api/agent-builder/sessions/{sessionId}/draft | 成员 | 产品 | 否 |
| POST | /api/work-items/{workItemId}/batches | 成员 | 产品 | 否 |
| GET | /api/work-items/{workItemId}/batches | 成员 | 产品 | 否 |
| GET | /api/batches/{batchId} | 成员 | 产品 | 否 |
| GET | /api/batches/{batchId}/items | 成员 | 产品 | 否 |
| POST | /api/batches/{batchId}/pause | 成员 | 产品 | 否 |
| POST | /api/batches/{batchId}/resume | 成员 | 产品 | 否 |
| POST | /api/batches/{batchId}/cancel | 成员 | 产品 | 否 |
| POST | /api/batches/{batchId}/retry-failed | 成员 | 产品 | 否 |
| GET | /api/ui-entries | 成员 | 产品 | 是（packs.ts + pack-entries.tsx；slots 随行返回） |
| PUT | /api/ui-entries/{pack} | admin | 产品 | 否（pack-provision 桥写） |
| DELETE | /api/ui-entries/{pack} | admin | 产品 | 否 |
| GET | /api/pack-tool-schemas | 成员 | 产品 | 否（C1/前端兜底读；当前 web 未接） |
| PUT | /api/pack-tool-schemas/{pack} | admin | 产品 | 否（pack-provision 写） |
| DELETE | /api/pack-tool-schemas/{pack} | admin | 产品 | 否 |

---

## 5. conversation（internal/conversation/adapters/http — chat/quick-actions）

认证一律：登录 + X-Workspace-Id 成员门。**整个域前端零调用**（chat 路由页是 PagePlaceholder 占位）。

| 方法 | 路径 | 认证 | 归属 | 前端调用 |
|---|---|---|---|---|
| GET | /api/chat/sessions | 成员 | 产品 | 否 |
| POST | /api/chat/sessions | 成员 | 产品 | 否（`{agent_id,title}` 201） |
| GET | /api/chat/sessions/{sessionId} | 成员 | 产品 | 否 |
| PATCH | /api/chat/sessions/{sessionId} | 成员 | 产品 | 否 |
| DELETE | /api/chat/sessions/{sessionId} | 成员 | 产品 | 否 |
| POST | /api/chat/sessions/{sessionId}/pin | 成员 | 产品 | 否 |
| POST | /api/chat/sessions/{sessionId}/unpin | 成员 | 产品 | 否 |
| POST | /api/chat/sessions/{sessionId}/archive | 成员 | 产品 | 否 |
| POST | /api/chat/sessions/{sessionId}/unarchive | 成员 | 产品 | 否 |
| POST | /api/chat/sessions/{sessionId}/messages | 成员 | 产品 | 否（`{content}` 201 → `{message, task_id}`；发消息即建任务） |
| GET | /api/chat/sessions/{sessionId}/messages | 成员 | 产品 | 否（limit/before_seq 分页 → `{messages}`） |
| GET | /api/chat/sessions/{sessionId}/pending-task | 成员 | 产品 | 否（`{task,status}` 或 `{task:null}`） |
| DELETE | /api/chat/sessions/{sessionId}/pending-task | 成员 | 产品 | 否（取消） |
| DELETE | /api/chat/sessions/{sessionId}/queued-tasks | 成员 | 产品 | 否 |
| POST | /api/chat/sessions/{sessionId}/queued-tasks/{taskId}/prioritize | 成员 | 产品 | 否 |
| POST | /api/chat/sessions/{sessionId}/read | 成员 | 产品 | 否 |
| GET | /api/chat/sessions/{sessionId}/draft-restores | 成员 | 产品 | 否 |
| DELETE | /api/chat/sessions/{sessionId}/draft-restores | 成员 | 产品 | 否 |
| GET | /api/chat/history | 成员 | 产品 | 否 |
| GET | /api/chat/thread | 成员 | 产品 | 否 |
| GET | /api/chat/pending-tasks | 成员 | 产品 | 否 |
| GET | /api/chat/pending-tasks/has-any | 成员 | 产品 | 否 |
| GET | /api/chat/pinned-agents | 成员 | 产品 | 否 |
| POST | /api/chat/pinned-agents | 成员 | 产品 | 否 |
| DELETE | /api/chat/pinned-agents | 成员 | 产品 | 否 |
| GET | /api/quick-actions | 成员 | 产品 | 否 |
| POST | /api/quick-actions | 成员 | 产品 | 否 |
| PATCH | /api/quick-actions/{quickActionId} | 成员 | 产品 | 否 |
| DELETE | /api/quick-actions/{quickActionId} | 成员 | 产品 | 否 |
| POST | /api/work-items/{workItemId}/quick-actions/{quickActionId}/run | 成员 | 产品 | 否 |
| POST | /api/work-items/{workItemId}/quick-actions/{quickActionId}/render | 成员 | 产品 | 否 |

---

## 6. notification（产品投影 + ploykit 幸存面）

产品侧认证：登录 + X-Workspace-Id 成员门（workspace-scoped 收件箱）。ploykit 三条是 user-scoped。

| 方法 | 路径 | 认证 | 归属 | 前端调用 |
|---|---|---|---|---|
| GET | /api/notifications | 成员 | 产品 | 是（notification.ts，limit/cursor → `{items,next_cursor}`） |
| GET | /api/notifications/archived | 成员 | 产品 | 否 |
| GET | /api/notifications/unread-count | 成员 | 产品 | 是（→ `{count}`） |
| GET | /api/notifications/summary | 成员 | 产品 | 是 |
| GET | /api/notifications/facets | 成员 | 产品 | 否 |
| POST | /api/notifications/mark-all-read | 成员 | 产品 | 是（发 WS 帧 notification:batch_read） |
| POST | /api/notifications/archive-all | 成员 | 产品 | 是 |
| POST | /api/notifications/archive-all-read | 成员 | 产品 | 是 |
| POST | /api/notifications/{itemId}/read | 成员 | 产品 | 是（→ WS 帧 notification:read） |
| POST | /api/notifications/{itemId}/unread | 成员 | 产品 | 是（→ WS 帧 notification:unread） |
| POST | /api/notifications/{itemId}/archive | 成员 | 产品 | 是（→ WS 帧 notification:archived） |
| POST | /api/notifications/{itemId}/unarchive | 成员 | 产品 | 是（→ WS 帧 notification:unarchived） |
| GET | /api/notification-preferences | 成员 | 产品 | 否（静音组偏好） |
| PATCH | /api/notification-preferences | 成员 | 产品 | 否 |
| PUT | /api/notification-preferences | 成员 | 产品 | 否 |
| GET | /api/notifications/badge | 登录（user-scoped） | ploykit | 否（产品用 unread-count 取代） |
| POST | /api/notifications/read-all | 登录（user-scoped） | ploykit | 否（刻意保留：与 mark-all-read 语义不同） |
| PUT | /api/notification-preferences/{type} | 登录（user-scoped） | ploykit | 否（邮件/站内渠道开关） |

> ploykit notifyhttp.Mount 中 4 条被产品版取代（filtered router 剔除，见 wiring_notification.go）：
> `GET /api/notifications`、`POST /api/notifications/{id}/read`、`POST /api/notifications/{id}/archive`、`GET /api/notification-preferences`。

---

## 7. skills（internal/skills/adapters/http）

认证一律：登录 + X-Workspace-Id 成员门。

| 方法 | 路径 | 认证 | 归属 | 前端调用 |
|---|---|---|---|---|
| GET | /api/skills | 成员 | 产品 | 是（skills.ts；`?q=` 走 search） |
| POST | /api/skills | 成员 | 产品 | 是（`{name,description}`） |
| GET | /api/skills/search | 成员 | 产品 | 是（`?q=`） |
| POST | /api/skills/import | 成员 | 产品 | 是（JSON `{url}` 或 multipart zip） |
| GET | /api/skills/{skillId} | 成员 | 产品 | 是 |
| PUT | /api/skills/{skillId} | 成员 | 产品 | 是 |
| DELETE | /api/skills/{skillId} | 成员 | 产品 | 是 |
| POST | /api/skills/{skillId}/refresh | 成员 | 产品 | 是 |
| GET | /api/skills/{skillId}/files | 成员 | 产品 | 是 |
| PUT | /api/skills/{skillId}/files | 成员 | 产品 | 是 |
| DELETE | /api/skills/{skillId}/files/{fileId} | 成员 | 产品 | 是 |
| GET | /api/skills/{skillId}/agents | 成员 | 产品 | 是 |
| GET | /api/skills/{skillId}/labels | 成员 | 产品 | 否 |
| POST | /api/skills/{skillId}/labels | 成员 | 产品 | 否 |
| DELETE | /api/skills/{skillId}/labels/{labelId} | 成员 | 产品 | 否 |
| GET | /api/agents/{agentId}/skills | 成员 | 产品 | 是 |
| PUT | /api/agents/{agentId}/skills | 成员 | 产品 | 是（整体替换） |
| POST | /api/agents/{agentId}/skills/add | 成员 | 产品 | 是（`{skill_id}`） |
| PUT | /api/agents/{agentId}/skills/{skillId}/enabled | 成员 | 产品 | 是（`{enabled}`） |
| DELETE | /api/agents/{agentId}/skills/{skillId} | 成员 | 产品 | 是 |

---

## 8. content（internal/content/adapters/http）与 channels

| 方法 | 路径 | 认证 | 归属 | 前端调用 |
|---|---|---|---|---|
| POST | /api/upload-file | 成员 | 产品 | 是（skills/index 等上传场景；web/src/api 无独立封装） |
| GET | /api/work-items/{workItemId}/attachments | 成员 | 产品 | 否 |
| GET | /api/attachments/{attachmentId} | 成员 | 产品 | 否 |
| GET | /api/attachments/{attachmentId}/download | 成员 | 产品 | 否 |
| GET | /api/attachments/{attachmentId}/content | 成员 | 产品 | 否 |
| GET | /api/attachments/{attachmentId}/signed-download | 登录（仅 auth，无成员门；签名在查询串） | 产品 | 否 |
| DELETE | /api/attachments/{attachmentId} | 成员 | 产品 | 否 |
| POST | /api/channels/title-callback | 通道凭证证明（X-Channel-Config header） | 产品（channels） | 否（外部渠道系统回调） |

---

## 9. automation（internal/automation/adapters/http — wakeups/autopilots/deliveries）

认证：Mount 内全部为成员门；`POST /webhooks/autopilots/{token}` 是公开入口（token 即凭证 + HMAC 签名 + 限流）。

| 方法 | 路径 | 认证 | 归属 | 前端调用 |
|---|---|---|---|---|
| POST | /webhooks/autopilots/{token} | 公开（X-Signature/X-Timestamp HMAC + 限流） | 产品 | 否（外部系统触发 autopilot） |
| POST | /api/work-items/{workItemId}/wakeups | 成员 | 产品 | 否 |
| GET | /api/work-items/{workItemId}/wakeups | 成员 | 产品 | 否 |
| GET | /api/work-items/{workItemId}/wakeups/{wakeupId} | 成员 | 产品 | 否 |
| PUT | /api/work-items/{workItemId}/wakeups/{wakeupId} | 成员 | 产品 | 否（乐观锁 `{expected_revision}`） |
| DELETE | /api/work-items/{workItemId}/wakeups/{wakeupId} | 成员 | 产品 | 否（204） |
| POST | /api/work-items/{workItemId}/wakeups/{wakeupId}/enable | 成员 | 产品 | 否（204） |
| POST | /api/work-items/{workItemId}/wakeups/{wakeupId}/disable | 成员 | 产品 | 否（204） |
| PUT | /api/work-items/{workItemId}/wakeups/{wakeupId}/instruction | 成员 | 产品 | 否 |
| POST | /api/work-items/{workItemId}/wakeups/{wakeupId}/checkin | 成员 | 产品 | 否（`{task_id,note}`） |
| POST | /api/work-items/{workItemId}/wakeups/{wakeupId}/trigger | 成员 | 产品 | 否（204） |
| GET | /api/work-items/{workItemId}/wakeups/{wakeupId}/tasks | 成员 | 产品 | 否 |
| GET | /api/work-item-wakeups | 成员 | 产品 | 否 |
| GET | /api/work-item-wakeup-paused | 成员 | 产品 | 否 |
| GET | /api/work-item-wakeup-summaries | 成员 | 产品 | 否 |
| GET | /api/system-wakeups | 成员 | 产品 | 否（`{default,rules}`） |
| PUT | /api/system-wakeups | 成员 | 产品 | 否 |
| GET | /api/system-wakeups/{wakeupId} | 成员 | 产品 | 否 |
| PUT | /api/system-wakeups/{wakeupId} | 成员 | 产品 | 否（`{instruction?,enabled?}`） |
| GET | /api/work-items/{workItemId}/system-wakeups | 成员 | 产品 | 否 |
| POST | /api/work-items/{workItemId}/system-wakeups | 成员 | 产品 | 否（ensure child-done 规则） |
| GET | /api/autopilots | 成员 | 产品 | 是（automation.ts，`?limit=`） |
| POST | /api/autopilots | 成员 | 产品 | 否（`{name,exec_mode,template,target_agent_id,cron_expression,timezone}` 201） |
| GET | /api/autopilots/{autopilotId} | 成员 | 产品 | 否 |
| PATCH | /api/autopilots/{autopilotId} | 成员 | 产品 | 是（`{enabled}` 开关） |
| DELETE | /api/autopilots/{autopilotId} | 成员 | 产品 | 是（204） |
| POST | /api/autopilots/cron-preview | 成员 | 产品 | 否（`{cron_expression,timezone,count}` → `{next_runs}`） |
| POST | /api/autopilots/{autopilotId}/trigger | 成员 | 产品 | 是（手动触发 → run；配额超限返回 200+run） |
| POST | /api/autopilots/{autopilotId}/triggers | 成员 | 产品 | 否（kind=cron|webhook；webhook kind 返回 `{trigger,credential{webhook_token,…}}`） |
| PATCH | /api/autopilots/{autopilotId}/triggers/{triggerId} | 成员 | 产品 | 否 |
| DELETE | /api/autopilots/{autopilotId}/triggers/{triggerId} | 成员 | 产品 | 否（204） |
| PUT | /api/autopilots/{autopilotId}/triggers/{triggerId}/signing-secret | 成员 | 产品 | 否（`{secret}` → credential） |
| POST | /api/autopilots/{autopilotId}/triggers/{triggerId}/rotate-webhook-token | 成员 | 产品 | 否（→ credential） |
| GET | /api/autopilots/{autopilotId}/runs | 成员 | 产品 | 否 |
| GET | /api/autopilots/{autopilotId}/runs/{runId} | 成员 | 产品 | 否 |
| GET | /api/deliveries | 成员 | 产品 | 否 |
| GET | /api/deliveries/{deliveryId} | 成员 | 产品 | 否 |
| POST | /api/deliveries/{deliveryId}/replay | 成员 | 产品 | 否 |
| GET | /api/autopilots/{autopilotId}/collaborators | 成员 | 产品 | 否 |
| POST | /api/autopilots/{autopilotId}/collaborators | 成员 | 产品 | 否 |
| DELETE | /api/autopilots/{autopilotId}/collaborators | 成员 | 产品 | 否 |

---

## 10. webhooks（ploykit webhooks 域 — 工作区出站订阅）

| 方法 | 路径 | 认证 | 归属 | 前端调用 |
|---|---|---|---|---|
| GET | /api/workspaces/{workspaceId}/webhooks/events | 成员 | ploykit | 否 |
| GET | /api/workspaces/{workspaceId}/webhooks/deliveries | 成员 | ploykit | 否 |
| POST | /api/workspaces/{workspaceId}/webhooks/deliveries/{deliveryId}/redeliver | admin | ploykit | 否 |
| GET | /api/workspaces/{workspaceId}/webhooks/subscriptions | 成员 | ploykit | 否 |
| POST | /api/workspaces/{workspaceId}/webhooks/subscriptions | admin | ploykit | 否 |
| POST | /api/workspaces/{workspaceId}/webhooks/subscriptions/{subscriptionId}/ping | admin | ploykit | 否 |
| POST | /api/workspaces/{workspaceId}/webhooks/subscriptions/{subscriptionId}/rotate-secret | admin | ploykit | 否 |
| PATCH | /api/workspaces/{workspaceId}/webhooks/subscriptions/{subscriptionId} | admin | ploykit | 否（启停） |
| DELETE | /api/workspaces/{workspaceId}/webhooks/subscriptions/{subscriptionId} | admin | ploykit | 否 |

> 认证来自组装根全局 Authenticate（webhookhttp.Mount 依赖调用方挂认证），member/admin 由 MemberCheck+Authz 决定。

---

## 11. 商业面：quota / audit / admin（wiring_commercial.go，P6-2 仅挂接）

### 11.1 quota + audit（/api 前缀，成员面）

| 方法 | 路径 | 认证 | 归属 | 前端调用 |
|---|---|---|---|---|
| GET | /api/usage | 成员（X-Workspace-Id 门） | ploykit quota | 否（用量读数） |
| GET | /api/audit | 成员（X-Workspace-Id 门 + authz） | ploykit audit | 否 |
| GET | /api/audit/export.csv | 成员 | ploykit audit | 否 |

### 11.2 admin（root mux，平台管理员）

| 方法 | 路径 | 归属 | 前端调用 |
|---|---|---|---|
| GET | /api/admin/stats | ploykit admin | 否 |
| GET | /api/admin/users | ploykit admin | 否 |
| PATCH | /api/admin/users/{id}/status | ploykit admin | 否 |
| PATCH | /api/admin/users/{id}/admin | ploykit admin | 否 |
| POST | /api/admin/users/{id}/impersonate | ploykit admin | 否 |
| GET | /api/admin/users/{id} | ploykit admin | 否 |
| GET | /api/admin/users/{id}/export | ploykit admin | 否 |
| POST | /api/admin/users/{id}/kick | ploykit admin | 否 |
| POST | /api/admin/users/{id}/sessions/{sid}/revoke | ploykit admin | 否 |
| POST | /api/admin/users/{id}/password-reset | ploykit admin | 否 |
| POST | /api/admin/users/{id}/resend-verification | ploykit admin | 否 |
| POST | /api/admin/users/{id}/mark-verified | ploykit admin | 否 |
| GET | /api/admin/users/{id}/pats | ploykit admin | 否 |
| POST | /api/admin/users/{id}/pats/{patID}/revoke | ploykit admin | 否 |
| DELETE | /api/admin/users/{id} | ploykit admin | 否 |
| GET | /api/admin/workspaces | ploykit admin | 否 |
| GET | /api/admin/workspaces/{id} | ploykit admin | 否 |
| PATCH | /api/admin/workspaces/{id}/plan | ploykit admin | 否 |
| PATCH | /api/admin/workspaces/{id}/members/{uid} | ploykit admin | 否 |
| DELETE | /api/admin/workspaces/{id}/members/{uid} | ploykit admin | 否 |
| DELETE | /api/admin/workspaces/{id} | ploykit admin | 否 |
| POST | /api/admin/workspaces/{id}/quota/grant | ploykit admin | 否 |
| POST | /api/admin/workspaces/{id}/transfer-ownership | ploykit admin | 否 |
| POST | /api/admin/workspaces/{id}/cancel-subscription | ploykit admin | 否 |
| POST | /api/admin/notify | ploykit admin | 否 |
| GET | /api/admin/sso | ploykit admin | 否 |
| GET | /api/admin/analytics | ploykit admin | 否 |
| GET | /api/admin/analytics/recent | ploykit admin | 否 |
| GET | /api/admin/audit | ploykit admin | 否 |
| GET | /api/admin/audit/export.csv | ploykit admin | 否 |
| GET | /api/admin/plans | ploykit admin | 否 |
| GET | /api/admin/orders | ploykit admin | 否 |
| DELETE | /api/admin/orders/{id} | ploykit admin | 否 |
| POST | /api/admin/billing/orders/{id}/mark-paid | ploykit admin | 否 |
| GET | /api/admin/payment-events | ploykit admin | 否 |
| POST | /api/admin/billing/run-expiry | ploykit admin | 否 |
| POST | /api/admin/billing/run-overage | ploykit admin | 否 |
| GET | /api/admin/webhook-deliveries | ploykit admin | 否 |
| POST | /api/admin/webhook-deliveries/{id}/redeliver | ploykit admin | 否 |
| GET | /api/admin/billing/config | ploykit admin | 否 |

> billing 领域本身 DEFERRED（wiring_commercial.go 注释：依赖 platform/events River 底座，商业化启动时一并引入）。

---

## 12. WS 端点与帧语义

### 12.1 连接与订阅

- 端点：`GET /ws`（root mux，authMw 包裹；支持 cookie 会话或 `?token=ork_…` PAT）。
- Hub：ploykit `platform/wsx`（wswire 协议）；多副本经 relayx（Redis pub/sub）扇出，无 Redis 时单机内存广播。
- 授权：handshake 的 `?workspace_id=`（前端 realtime/client.ts 自动拼）或 subscribe 控制帧必须是当前用户**已加入的工作区**，否则 `rejected:`。
- 能力目录（Capabilities，hub 下发）：框架自带 `batch/notification/workspace` + 产品注册的 `task/work_item/comment/chat/agent/autopilot/squad`。
- 控制帧（入站）：`{"type":"auth"|"subscribe"|"unsubscribe","scope":"workspace|task|chat|user","id":"…"}`，接受扁平与嵌套 payload 两种形状（F16）。

### 12.2 作用域（scope）

| scope | 语义 | 来源 |
|---|---|---|
| workspace:{id} | 工作区广播（默认 ridealong） | 框架保留 |
| user:{userId} | 定向单用户（SendToUser） | 框架保留 |
| task:{taskId} | 单任务跟帧 | orkome 注册 |
| chat:{sessionId} | 会话级跟帧 | orkome 注册 |

### 12.3 出站帧类型

帧结构：`{"type":"<t>","payload":<raw>,"event_id":"…"}`。

产品域事件（internal/contract/events，经 eventbus→wswire.WSName 点转冒号）在 workspace scope 广播，task 类事件**同时**在 task:{id} scope 重复广播（wiring_ws.go scopedTarget）：

| 帧类型 | scope | 载荷要点 | 触发 |
|---|---|---|---|
| work_item:created / updated / deleted | workspace + — | WorkItemPayload{id,number,title,status,revision,assignee_type,assignee_id,creator_*} | work 域写操作 |
| work_item:updated_by_agent | workspace | 同上 | agent 完成回写 |
| comment:created / updated / deleted / resolved | workspace | CommentPayload{id,work_item_id,author_type,content} | 评论写操作 |
| label:created / project:created | workspace | 标签/项目载荷 | 目录操作 |
| workspace:created / updated / deleted；member:added/updated/removed；invitation:created/accepted/declined | workspace | Workspace/Member/InvitationPayload | ploykit workspace 钩子 |
| task:queued / dispatch / running / awaiting_input / resumed / completed / failed / cancelled | workspace + task:{id} | TaskPayload（id/work_item_id/status/attempt…） | 编排引擎 |
| task:message | workspace + task:{id} | `{kind,content,task_id}`（kind 5 种：text/tool_use/tool_result/error/approval） | 引擎流式消息（InterimPush） |
| task:progress | workspace + task:{id} | `{stage,call_id,seq,task_id}` | 引擎进度（ProgressPush） |
| batch:created / progress / completed / failed | workspace | 批次进度 | batch worker |
| chat:done | workspace + chat:{sessionId} | ChatDoneFrame{session_id,…} | 会话任务收尾 |
| notification:read / unread / batch_read / archived / unarchived / batch_archived | **user:{id} 定向**（SendToUser，不在 workspace） | `{id,…}` / 空对象 | 通知产品投影（notifapp.UserFrames） |

控制帧回复：`connected`（连接建立，带 capabilities）、`confirmed:<scope:id>` / `rejected:<scope:id>`。

前端失效规则（web/src/main.tsx）：`^work_item:` → work-items/work-item；`^task:` → work-item/task/workspace-tasks；`^comment:` → work-item(s)；`^notification:` → notifications。

---

## 13. 代表端点请求/响应形状（逐域 2-3 例 + 四波新增全形状）

### 13.1 work

```jsonc
// POST /api/work-items  （X-Workspace-Id 必带）
{ "title": "…", "description": "…", "status": "todo", "priority": 2,
  "assignee_type": "agent", "assignee_id": "…", "project_id": "…" }
// 200 → WorkItem
{ "id": "…", "workspace_id": "…", "number": 1036, "title": "…", "description": "…",
  "acceptance_criteria": "", "status": "in_progress", "status_category": "active",
  "priority": 2, "assignee_type": "agent", "assignee_id": "…",
  "creator_type": "user", "creator_id": "…", "parent_work_item_id": "",
  "project_id": "", "revision": 3, "created_at": "…", "updated_at": "…", "last_activity_at": "…" }

// POST /api/work-items/{id}/comments → { "content": "…", "parent_id": "" }
// 201 → { "comment": Comment{…}, "admission": {/* 触发判定 */} }

// GET /api/work-items?limit=30&cursor=…&status=a&status=b → { "items": [...], "next_cursor": "…" }
```

### 13.2 orchestration

```jsonc
// GET /api/tasks?status=running&agent_id=…&cursor=… →
{ "tasks": [ { "id": "…", "workspace_id": "…", "agent_id": "…", "work_item_id": "…",
    "status": "running", "priority": 5, "attempt": 1, "max_attempts": 3, "context": {},
    "originator_source": "chat", "summary": "…", "created_at": "…" } ],
  "next_cursor": "…" }

// GET /api/tasks/{taskId}/messages?limit=200&order=desc&before_seq=42&kind=text,error →
{ "messages": [ { "id": "…", "task_id": "…", "seq": 41, "kind": "text",
    "call_id": "", "content": "…", "truncated": false, "created_at": "…" } ],
  "next_before_seq": 12 }   // 仅 desc 且取满 limit 时出现

// POST /api/tasks/{taskId}/approve → { "note": "…" } ⇒ { "result": "approved" }
```

### 13.3 conversation

```jsonc
// POST /api/chat/sessions        { "agent_id": "…", "title": "…" }        → 201 Session
// POST /api/chat/sessions/{id}/messages { "content": "…" }
// 201 → { "message": Message{…}, "task_id": "…" }   // 发消息即入队一个任务
// GET  /api/chat/sessions/{id}/messages?limit=50&before_seq=… → { "messages": [...] }
```

### 13.4 notification

```jsonc
// GET /api/notifications?limit=20&cursor=… → { "items": [Notification{…}], "next_cursor": "…" }
// POST /api/notifications/{itemId}/read → 200（并 SendToUser 帧 notification:read）
// GET /api/notifications/unread-count → { "count": 3 }
```

### 13.5 skills / content / automation

```jsonc
// POST /api/skills/import  JSON: { "url": "https://…" }  或 multipart zip → 201 Skill
// PUT  /api/skills/{id}/files { "path": "SKILL.md", "content": "…" } → …

// POST /api/upload-file (multipart) → 201 { "id": "…", "url": "/uploads/…", … }
// POST /api/autopilots { "name": "…", "exec_mode": "…", "template": {},
//                        "target_agent_id": "…", "cron_expression": "0 9 * * *", "timezone": "Asia/Shanghai" }
// POST /api/autopilots/{id}/triggers { "kind": "webhook" } → 201
{ "trigger": {…}, "credential": { "trigger_id": "…", "webhook_token": "…",
  "secret_fingerprint": "…", "has_signing_secret": true } }
```

### 13.6 四波新增端点（形状写全）

**ui-entries（slots 扩展，迁移 1033→1036）**

```jsonc
// GET /api/ui-entries → { "items": [PackUIEntry] }   // slots 已 COALESCE 为 []，随行返回
{ "items": [ {
    "id": "…", "workspace_id": "…", "pack": "github",
    "label": "GitHub", "url_template": "/packs/github?wi={id}",
    "slots": [ { "slot": "workitem.detail.tab", "label": "PR 看板",
                 "url_template": "https://…/embed?wi={id}", "height": 480 } ],
    "installed_at": "…" } ] }
// slot 标识 v0 冻结目录：仅 workitem.detail.tab

// PUT /api/ui-entries/{pack}   （workspace owner/admin）
{ "pack": "github", "label": "GitHub", "url_template": "/packs/github",
  "slots": [ { "slot": "workitem.detail.tab", "label": "…", "url_template": "…", "height": 480 } ] }
// → 201 { "result": "upserted" }   // nil slots = 清空旧值

// DELETE /api/ui-entries/{pack} → { "result": "deleted" }
```

**pack-tool-schemas（迁移 1035，B1；接口冻结 docs/interfaces-job-handle-2026-10-11.md §7）**

```jsonc
// GET /api/pack-tool-schemas → { "items": [PackToolSchema] }
{ "items": [ {
    "workspace_id": "…", "pack": "github",
    "server": "github-main", "tool_name": "github_submit_publish",   // 含前缀全名 = 桥接层键
    "output_schema": { "type": "object", "properties": { … } },
    "source": "protocol",          // protocol | pack_yaml（builtin_default 为 B1 预留）
    "installed_at": "…" } ] }

// PUT /api/pack-tool-schemas/{pack}   （owner/admin；pack-provision 全量替换）
{ "items": [ PackToolSchema, … ] }    // → { "result": "replaced" }
// DELETE /api/pack-tool-schemas/{pack} → { "result": "deleted" }
```

**workspace/agent mcp 注册表**（见 §4.4；body 形状 `{name,transport,url,credentials,enabled?}`，agent 级多 `enabled` 布尔默认 true）

**ui-tokens（pack iframe 票据）**

```jsonc
// POST /api/ui-tokens    { "workspace_id": "…", "pack": "github" }（body 校验成员身份）
// → { "token": "…", "expires_in": 60 }   // 60 秒一票，绝不缓存（pack-slot-frame.tsx 每挂载现铸）
// POST /api/ui-tokens/consume → 票换上下文（供 iframe 内部回换）
```

**mcp ws 级注册表**（本次新增的第四条路：`/api/mcp/w/{workspaceId}/agent` StreamableHTTP，9 个工具表见 §4.5；工具返回统一 `CallToolResult.textContent` 内嵌 JSON）：

```jsonc
// claim_task {task_id} → { "claimed": true, "task_id": "…", "status": "running" }
// report_progress {task_id, content} → { "seq": 17 }
// complete_task {task_id, result} → { "result": "completed" }
// fail_task {task_id, reason, detail?} → { "result": "failed" }
// post_comment {work_item_id, content} → { "posted": true }
```

---

## 14. 无前端消费面的端点（高价值发现）

前端（web/src）只消费了约 90 条。以下端点当前无任何 web 调用点，按价值分三档：

### A. 整域零消费（前端功能未建）

- **conversation 域全部 30 条**（/api/chat/*、/api/quick-actions*）——chat 路由页是 `PagePlaceholder` 占位，后端整域就绪但前端未接。
- **webhooks 订阅面 9 条**（/api/workspaces/{id}/webhooks/*）——设置页无出站订阅 UI。
- **quota/audit 3 条**（/api/usage、/api/audit、/api/audit/export.csv）。
- **admin 39 条**（/api/admin/*）——平台运营面，无 web 控制台（合理）。
- **identity 框架面大半**：/auth/register、/auth/login（密码）、forgot/reset-password、verify-email、change/confirm-password、/auth/sessions*、oauth/fed 入口、workspace SSO CRUD、members PATCH/DELETE、invitations CRUD + mine + accept/decline、share-links CRUD——onboarding 目前只走邮箱验证码 + 分享链接兑换（且 redeem 路径前端写错，见 §15.3）。

### B. 产品域已挂但 web 未接（潜在缺口/预留）

| 域 | 端点 |
|---|---|
| work | move、query、table/rows\|groups\|facets、search、batch-update/delete、child-progress、grouped、assignee-frequency、preview-trigger、reactions×2、subscribers×2、duplicates、metadata×3、properties/{key}×3、labels/{id} GET/PUT/DELETE、projects 单查/搜索/DELETE/resources×4、statuses POST/reorder/PATCH、properties POST/PATCH/DELETE、work-item-views×2、view-preferences×2、unsubscribe×2、client-usage、comments PUT/DELETE/keep-replies/resolve×2/reactions×2/sub-work-item-preview/sub-work-items（约 48 条） |
| orchestration | tasks/{id} GET、interrupt、cancel(top)、retry-source-context、agent-run-counts、agent-activity-30d、agent-run-snapshot、active-task、rerun、supplements×2、usage、squad-evaluated、agent labels×3、runtime-credentials、agent-builder×4、batches×8、pack-tool-schemas×3（GET 预留前端兜底读未接）、ui-entries PUT/DELETE（约 31 条） |
| skills | skill labels×3 |
| content | attachments GET/download/content/DELETE、signed-download、work-items/{id}/attachments（6 条） |
| automation | wakeups 15 条、system-wakeups 6 条、autopilots POST/单查/cron-preview/triggers×5/signing-secret/rotate-token/runs×2/deliveries×3/collaborators×3——web 只接列表/PATCH enabled/trigger/DELETE 四个（约 33 条） |
| identity | pins×4（侧栏钉选未建）、ui-tokens/consume |

### C. 设计上无前端（外部/CLI/agent 消费，合理）

- `POST /webhooks/autopilots/{token}`（外部系统入站触发）
- `/api/mcp/w/{wsId}/agent`（agent 运行时 MCP）+ `POST /api/tasks/{id}/claim|messages|complete|fail`（runtime PAT）
- `/cli-token`（client-kit CLI session→PAT）
- pack-provision 写入面：ui-entries PUT/DELETE、pack-tool-schemas PUT/DELETE（D2 install-bridge）
- `POST /api/channels/title-callback`（外部渠道回调）

---

## 15. 契约测试与实际路由的差异/缺口

1. **route-compare 幽灵条目**：`docs/route-compare/orkome_routes.txt` 有 `GET /api/agents/{agentId}/runtime-credentials`，实际代码只注册 POST（`internal/orchestration/adapters/http/routes.go:157`），契约测试 routeTable 也只有 POST。文档行过期，应删。
2. **契约测试是单向对账**：`TestRouteContractWalk` 只验证「表→mux」（声明必须已挂载）；「挂了但未声明」无自动守卫（测试注释自认靠 review + 各域 route accounting）。挂载而 routeTable 未收录的：pins×4、`POST /cli-token`、`GET /api/usage`、`GET /api/audit(export.csv)`、全部 `/api/admin/*`、`GET/PUT/DELETE /api/workspaces/{id}/sso`、`GET /api/workspaces/{id}/roles`、`GET /api/invitations/mine`、`POST /api/share-links/redeem`、pack-tool-schemas×3。
3. **前端调了不存在的路径（真 bug）**：`web/src/api/identity.ts` 的 `joinByShareCode` 调 `POST /api/share-links/join`，实际 ploykit 路由是 `POST /api/share-links/redeem`——通过分享码加入工作区的功能当前必 404。
4. **通知双面刻意重叠**：badge vs unread-count、read-all vs mark-all-read、PUT prefs/{type} vs GET/PATCH/PUT prefs（route-compare 注释声明 deliberate overlap）。无缺口，但前端只用产品组，ploykit 三条处于「活着没人用」状态。
5. **workspace 单查不存在**：`GET /api/workspaces/{id}` 框架不挂载（列表即成员资格）；`cmd/orkome-server/routes.go` identity_api 注释 `GET/PATCH/DELETE /workspaces/{id}` 中 GET 与实际不符（仅 PATCH/DELETE 存在）。
6. **root-mux 面无契约守卫**：/auth/*、/config、/healthz、/readyz、/uploads/*、/ws、/metrics、/webhooks/autopilots/{token}、/api/admin/* 均不在 routeTable，这些路由的回归靠 e2e（`e2e/routes_walk_test.go` 覆盖一部分）。
7. **环境依赖路由**：`GET/HEAD /uploads/*` 仅 LOCAL 存储模式注册（`d.Local` 非空），对象存储模式下 404——route-compare 静态列出，未标注条件性。
8. **通知框架 4 条被过滤器剔除**（`wiring_notification.go` supersededNotifyPatterns）：ploykit notifyhttp.Mount 的 `GET /api/notifications`、`{id}/read`、`{id}/archive`、`GET /api/notification-preferences` 由产品版顶替注册——filtered router 是刻意的防 ServeMux panic 设计而非缺口，但意味着 ploykit notify 新增 pattern 时需要同步审查该过滤器。

---

## 16. 统计汇总

- **HTTP 端点总数 ≈ 316**：产品域（internal/*，含 channels 回调）≈ 200；ploykit 框架域（identity/workspace/notify 幸存 3 条/webhooks/quota/audit/admin）≈ 108；纯框架/平台（healthz/readyz/config/metrics/uploads/ws）≈ 8。另有 MCP StreamableHTTP 1 组（POST+GET 同路径，9 个工具）。
- **WS**：scope 4 种（workspace/user/task/chat）；出站业务帧 27 类（§12.3 表：work_item×4、comment×4、label/project×2、workspace/member/invitation×9、task 状态×8、task:message、task:progress、batch×4、chat:done×1、notification×6 —— 其中 task:message/progress 为直拼帧名不经 WSName）+ 控制帧 4（connected / confirmed:* / rejected:* / 入站 auth|subscribe|unsubscribe）。
- **无前端消费面端点 ≈ 155 条**（§14 A+B 档；C 档为设计如此）。整域零消费：conversation(30)、webhooks(9)、quota/audit(3)、admin(39)。
- **契约缺口 8 项**（§15），对用户可见的真问题 1 个：前端 share-links/join 路径不存在（404）；其余为文档过期与守卫盲区。
