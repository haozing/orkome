# orkome AI 摩擦修复改造方案

日期：2026-10-17
输入：`docs/integration-friction-analysis-2026-10-17.md`（视角一/三/四，含勘误）+ 联调实证。
原则：**不考虑兼容与旧数据**（用户裁决 2026-10-17）——枚举值改名、信封统一等「渐进」项可一刀切。
范围：仅列改造方案，未实施。当天已修 5 commit（3c6762b..8c0bbc5）不重复列入。

## P0（小改动高杠杆，建议下一波）

### P0-1. 指派监听器按「指派变化」过滤（C2，最重要）
- 现状：`cmd/orkome-server/main.go:107-114` 监听 `WorkItemUpdated` 只看最终态 assignee——对已指派 agent 的工单做**任何字段更新**（改标题/状态/优先级）都会静默重派 agent。`AssigneeChanged` 判定已存在于 `internal/work/app/service_workitem.go:466` 但事件消费侧未用。
- 改法：`events.WorkItemPayload` 增补 `assignee_changed bool`（发出点 `service_workitem.go:271-283` 顺手填）；监听器加 `!p.AssigneeChanged` 早退。无兼容包袱，事件形状直接改。
- 附带：`main.go:114` 的 `_, _, _ =` 全弃改为把 skip 决策落通知/时间线（见 P1-6）。

### P0-2. 409/派发冲突响应携带 active_task_id（C3 残留）
- 现状：`wiring_conversation.go` 已落 `ActiveDuplicate` 409，但 body 无在途任务信息，AI 还要再查一轮。
- 改法：`AlreadyActive` 分支顺手 `GET /work-items/{id}/active-task`（`internal/orchestration/adapters/http/handlers_taskview.go:187-198` 现成）把 task_id 塞进 apierr Details。

### P0-3. skill.md 补三段话（A1/A2/A6）
- `cmd/orkome-server/mcp_me_skilldoc.go` 注意事项段补：
  1. **验证方法**：curl initialize 示例 + 成功标志 = tools/list 返回全部工具（数量从 `s.ListTools()` 动态取，不写死 23）；401=令牌错/过期、403=非该工作区成员。
  2. **重启预期 + HTTP 降级路径**：「多数客户端只在启动时连接 MCP 服务器，写入配置后需重启会话；等待期间可按『通用 MCP 客户端』段用 HTTP 直接调用（initialize 取 mcp-session-id 头）」——联调实证该路径完全可行。
  3. **schema 严格警示**：「部分客户端配置 schema 严格（未知字段可能导致整个服务器条目被静默忽略），写入后务必验证」。

### P0-4. /access 页补「本工作区完整端点」Snippet（A3/A4）
- 现状：skill.md 教 AI 让用户复制「带完整端点的链接」，但页面改版后该元素已不存在（文档-UI 漂移，`mcp_me_skilldoc.go:80` vs `web/src/routes/_app.access.tsx:98-131`）。
- 改法：access 页用现成 `useWorkspaceStore((s) => s.currentId)` 渲染 `/api/mcp/w/{currentId}/me` 完整端点 Snippet（同 skillDocUrl 卡片样式）。同时消解同名工作区歧义（用户看着当前工作区复制，天然无歧义）。

### P0-5. 32 工具截断可见化（D5）
- `internal/orchestration/adapters/engine/tools_mcp.go:176-178` 装满即 break 无 note。改：截断时 `entry.Note = "truncated at 32/N tools"`，probe 响应带 `total`。

### P0-6. 非 ASCII 负载坑写进文档（C6）
- skill.md 注意事项加一行：「Windows shell 内联非 ASCII 会被本地代码页转码，请将请求体写入 UTF-8 文件后 --data-binary」。联调实证 GBK 坑直接写坏库。

## P1（结构性，排期）

### P1-1. PAT scope 落地 + 默认 TTL（A5，接入面最大信任不对称）
- `webx.CredentialScope` 已实现（`vendor/.../platform/webx/principal.go:24-62`），只差接线：`internal/identity/adapters/http/handlers.go:112` 的 `CreatePAT(..., nil)` 换成解析请求；PAT 表单加「限定工作区」勾选（默认当前工作区）；TTL 默认值从「永不过期」（`web/src/components/settings/tokens-section.tsx:26-31` 首项 value 0）改为 30 天。

### P1-2. 助理面工具差集收口（C5）
- 补 `list_agents`（复用 `orchapp.AgentService.List`，响应带 runtime 就绪态——credential_id 是否配置，可同时缓解冷启动盲区）；
- `update_work_item` 加 `assignee_agent_id`（`WorkItemPatch` 现成字段）；
- 原则记账：每条高频 REST 写路径回答「助理面有无等价工具」，差集记到 /access 工具清单页。

### P1-3. 注册前探活干跑 + agent 级 probe（D2）
- `create_mcp_server` 与 REST Create 支持 `probe=true`：落库前用入参凭证实连一次，失败返回结构化 warning（允许强行保留）；
- 补 `POST /agents/{id}/mcp-servers/{serverId}/probe-tools`（引擎侧 `ProbeServerTools` 现成，agent 级条目目前凭证错误只能在任务里炸）。

### P1-4. credential_id 幽灵关联堵死（D3）
- `create_mcp_server` 收到 credential_id 时校验存在性（credSvc.Get），不存在 400；
- 注册响应加 `auth_effective: bool`（headers 信封非空 = true），替代误导性的 `has_credentials` 作为唯一信号；
- 无兼容包袱：credential_id-only 注册直接拒绝。

### P1-5. transport 枚举一刀切统一（D6，无兼容红利）
- 现状三层零校验 + 三处入口枚举互相矛盾（引擎 `""|"http"|"streamable_http"|"sse"`、UI `http|sse`、工具描述 `http|sse|streamable_http`）。
- 改法：service 层加 `validateTransport` 枚举闸（同 visibility 闸先例 45d4fc9）；**无兼容包袱直接定标准值 `streamable_http` + `sse`**（"http" 实际按 Streamable HTTP 处理，名字与语义漂移一并终结），UI 下拉/工具描述/文档三处同步，repo `orDefault` 兜底改为 `streamable_http`。

### P1-6. 派单显式化（C1/C2 深层）
- 新增 `POST /api/work-items/{id}/dispatch {agent_id}` 返回 task（显式派单动作）；PUT 指派响应附带 `dispatched_task_id` / `dispatch_skipped_reason`（数据在 `EnqueueAssignment` 返回值现成）；
- `main.go:114` 的 skip 决策（blocked/model_not_configured）落工单 timeline 条目或通知（通知域先例 `main.go:335-342` autopilot 熔断）——消灭「指派了但永远没任务、零线索」的静默失败；
- 新增 `GET /api/setup-status`（凭证数/可用 agent 数）供 AI 冷启动判断。

### P1-7. 出站接入契约文档化（D1）
- skilldoc「集成配置」组补出站接入指引：注册后必须 probe 验证、三态凭证语义（留空保留/null 清空/对象覆盖）、`auth_effective` 含义；
- 产出最小 `openapi.yaml` 覆盖 mcp-servers 五端点（对 AI 安装器，200 行 yaml 比读 400 行 Go 便宜一个数量级）。

### P1-8. REST 契约 AI 友好度（C4，无兼容可一刀切）
- work-items 补 PATCH 别名（或 JSON 405 + `allowed:["PUT"]`）；
- 信封约定统一：单资源裸对象 + 列表 `{<复数>: []}`，`{"items":null}` 已在 ploykit 0.7.0 修为 `[]`（生产升级即得）；
- skilldoc 附 REST 速查段（动词表 + 信封形状表）。

## P2（跨产品/较大）

- 定时 probe 巡检 → 工具面变更事件（复用 webhook 订阅通道，D4）；
- ws 级 MCP 注册写面收敛 admin（D7，audit-fp09 P2-5 在案）；
- 凭证信封 headers 键值校验（合法 HTTP token / 无控制字符，D8）；
- 32 上限改为每服务器可配（D5 长期形态）。

## 与 RunLynk 侧的配合点

- orkome 的 skill.md（入站）教 AI 接 orkome；RunLynk 补 skill.md（出站目标）教 AI 接 RunLynk——两侧同格式后，「AI 助理在 orkome 里帮用户把 RunLynk 注册进来」的完整链路（P1-2 list_agents + P1-3 probe 干跑 + RunLynk P0-a）全部自助。
- 生产待办不变：服务器跑 `cmd/migrate up` 补 1047。
