# 集成页终态设计（Integrations Final Form, v1.2）

> 2026-10-15。v1.2 = v1.1 经二次代码核对修订（修订记录见 §六）。
>
> **核心裁决（用户拍板）**：能力包是"安装时刻"的概念，不是"运行时"的概念。运行时真实配置只有三样——MCP 服务器、Webhook 订阅、页面入口。包退到幕后作安装台账，不再有列表入口。**不管兼容性和旧数据，要干净的代码和架构**。

## 一、产品定义

### 1.1 「集成」页 = 工作区的出线配置总面

```
/tools 集成页（三段）
├── ① MCP 服务器        现有 WorkspaceMcpSection 原样（注册/探针/编辑/删除）
├── ② Webhook 订阅      从 settings 工作区区迁来（列表/Ping/轮换/暂停恢复/重放/删除 + 新建）
└── ③ 页面入口          新段：侧栏深链 + 工单 tab 两类（读+删+手动新增）
```

分工终态：**集成页 = 出线**（工作区连了什么外部东西）；**settings = 治理**（成员/审计/改名/个人偏好）。

### 1.2 能力包的终态定位（保留物，全部退幕后）

| 保留 | 理由 | 入口 |
|---|---|---|
| `pack` 表 + 五个 pack_id FK | 卸载台账需要归属关系 | 无浏览入口 |
| `/packs/{id}` 详情页（五区块） | 干净卸载 + 卸载前资产核对 | 服务器行/订阅行/入口行的「来自 xx 包」徽标链接 |
| `/packs/install` 安装向导 + `pack-install-wizard.tsx` | 平台化 P2 交付物 | 集成页头部「安装能力包」按钮 |
| `PackUsageDialog`（在 `pack-export-dialog.tsx` 内） | 台账页「使用文档」功能（usage.md 生成/复制/存为技能） | 详情页头部按钮 |
| pack-provision CLI | pack.yaml 分发格式载体 | 命令行 |

### 1.3 删除物（干净代码裁决，审计修订后）

| 删除 | 理由 |
|---|---|
| 集成页的包卡片区（`PacksCardsSection`） | 浏览面冗余——上一轮合并的中间态，本轮直接去掉。**删前先把「安装能力包」按钮迁到 /tools 页头**（见 §2.4） |
| `packs-page` 概念残留 + 相关死键 | 无消费方 |
| `pack-attach-dialog.tsx`（`PackAttachPickerDialog` + `PackDetachButton`）| 挂载的唯一目的是往包台账挂资产；包无浏览面后无处可看。pack-detail ⑤ 资产区块改纯只读 |
| settings 工作区区的 Webhook 订阅区块（含 import + 挂载行） | 搬家不是复制——集成页成为唯一家 |
| `api/packs.ts` 的 `setAgentPack` / `setSkillPack` + 挂载相关类型 | 前端无消费即删（后端端点保留） |
| 侧栏/文案中「能力包」导航残留键 | 前一轮 nav 已合一，本轮清残留 |

**不删**：pack-domain 后端（installer/usage_md/订阅关联）、`/api/packs` 端点族、迁移、`pack-install-wizard.tsx`、`pack-export-dialog.tsx`（见 §1.2）。

## 二、逐段设计

### 2.1 段① MCP 服务器（零改动）

现状即终态。行内「来自 xx 包」徽标已是 Link 进 `/packs/{id}`，保留。

### 2.2 段② Webhook 订阅（迁移）

组件 `webhook-subscriptions-section.tsx` 从 `components/settings/` 迁到 `components/integrations/`（新目录），逻辑零改动。props 签名 `{ workspaceId: string; role: string | undefined }`——`/tools` 页已持有这两个值（workspaceId=store，role=me.workspaces），直接挂载。

配套：

- 段头描述改口径：原「订阅本体在工作区 Webhook 管理」的指引文案更新为自指（本页即管理面）
- **pack 详情页事件区块的「在设置中管理订阅」链接改为指向 `/tools#webhooks`**（现为 `to="/settings" hash="webhooks"`，pack-detail.tsx 约 296 行，含 data-testid `pack-subscriptions-manage-link` → 更新 testid 或保持，实现时定）
- settings 壳只删两处：`_app.settings.tsx` 的 import（第 17 行）+ 工作区区块内的 `<WebhookSubscriptionsSection …/>`（第 134 行）；区块结构不动
- **i18n 键整族迁移**：现为 `settingsWs.webhook*`（约 30 键，zh.ts 1309 行起）→ 迁 `tools.webhook*`，zh/en 逐键同步删旧增新，组件内 `t()` 调用同步改（键清单以组件 grep `settingsWs.webhook` 为准，共 4+ 处调用点分布在桌面/移动两段渲染）

**投递记录折叠区**：随组件整体迁移（含按订阅过滤、重放）。

### 2.3 段③ 页面入口（新段，v0 契约 + 后端一处小改）

#### 数据面

`pack_ui_entry` 表 + 既有端点：`GET /api/ui-entries`（member 可读）、`PUT /api/ui-entries/{pack}`（admin）、`DELETE /api/ui-entries/{pack}`（admin）。

前端 `PackUIEntry` 类型需补 `pack_id?: string`（后端 wire 已有，D 字段）——「来自包/手动」徽标与徽标链接依赖它。

#### 服务端修改（v1.2 二次核对修正：不止放宽 packID，Uniqe 约束与读列都要动）

事实三重核对（v1.1 的假设有两处错）：

1. `UpsertWithSlots`（service_uientry.go:53-56）要求 `packID` 非空 → **放宽为可选**（空=手动入口），其余必填不变
2. **迁移 1042 已 `DROP` 掉 `UNIQUE(workspace_id, pack)`**（1042:30，换成 `UNIQUE(workspace_id, pack_id)`）——v1.1 说"表已有 UNIQUE(workspace_id, pack) 约束"是错的，手动入口的 ON CONFLICT 目标当前**不存在**。解法：**新迁移 1047** 建 partial unique index：
   ```sql
   CREATE UNIQUE INDEX pack_ui_entry_ws_manual_pack_key
     ON pack_ui_entry (workspace_id, pack) WHERE pack_id IS NULL;
   ```
   （手动入口按 pack 名唯一；provision 行 pack_id 非空不落入谓词，互不干扰）
3. repo `UpsertPackUIEntry` 双分支：packID 非空 → 现有 SQL（冲突目标 pack_id）**一字不改**；packID 空 → `INSERT ... VALUES ($1,$2,NULL,...) ON CONFLICT (workspace_id, pack) WHERE pack_id IS NULL DO UPDATE ...`
4. **`ListPackUIEntries` 的 SELECT 没扫 pack_id 列**（repo_uientry.go:31）——补 `COALESCE(pack_id::text,'')` + Scan `&e.PackID`，否则「来自包/手动」徽标没有数据源

改动面：迁移 2 文件 + service ~3 行 + repo 双分支 + List 一列；provision 路径行为不变（单测钉回归）。

#### 校验规则（实现者照抄）

- `url_template`：绝对 http(s) URL，host 必填（`validateURLTemplate`，前端表单预校验同规则）
- slots：v0 目录冻结只认 `workitem.detail.tab`；slot 必填 label + url_template（同 URL 规则）+ height ∈ (0, 4096]
- PUT 响应 201 `{"result":"upserted"}`；DELETE 响应 200 `{"result":"deleted"}`
- 写操作端点已带 `requireWorkspaceAdmin`，前端按 role 隐藏写控件

#### 视图与交互

数据源 `GET /api/ui-entries`（**缓存键 `['pack-ui-entries', workspaceId]`——与侧栏 PackEntries、工单 tab usePackSlots 三方共享**；本段 add/delete 后必须 `invalidateQueries` 该键，否则侧栏/工单 tab 最长 60s 不刷新）。

行分两类：

- **侧栏入口**：深链型。展示 label + URL 模板 + 「来自包/手动」徽标（`pack_id` 非空 → Badge 链 `/packs/{pack_id}`）
- **工单 tab**：`slots` 含 `workitem.detail.tab` → 展示 slot label + height

行操作：**移除**（DELETE，admin）；**新增**（Dialog：label + URL 模板必填、类型单选「侧栏入口/工单 tab」、选 tab 加 height 默认 480）。

URL 模板占位符提示：`{uiToken}/{workspaceId}`（侧栏）；`{uiToken}/{workspaceId}/{workItemId}`（工单 tab）。

**已知行为**（v0 接受，注记）：侧栏 `PackEntries` 组件对 entry 列表**不过滤**——所有 entry（含仅 tab 型）都会渲染一个侧栏按钮。手动新增的 tab 入口会同时出现在侧栏（点击为裸 URL 跳转，无工单语境）。不修（组件不感知类型；将来要区分再加字段）。

**铸票兼容性（已核实）**：`POST /api/ui-tokens` 的 `plugin` 只校验非空、不校验 pack 存在性（service_uitoken.go:83-86），手动入口的 `custom-*` 键铸票/消费链路天然可用。

### 2.4 页面头部

标题「集成」+ 简介不变；右上「安装能力包」按钮**从即将删除的 PacksCardsSection 头部迁入页头**（`data-testid="packs-install-link"` 保持，测试引用不断）。

## 三、实施切分（两波，文件不相交，可并行）

### W1：后端放宽 + 段②迁移 + 段③新段

**后端**
- `service_uientry.go`：`UpsertWithSlots` 放宽 packID（空=手动）；repo `repo_uientry.go` upsert 双分支
- 补单测：手动入口 upsert 幂等（同名覆盖）、provision 路径回归不变

**前端**
- 迁 `webhook-subscriptions-section.tsx` → `components/integrations/`；settings 壳摘除（import + 挂载行）；pack-detail 事件链接改指 `/tools#webhooks`；i18n 键族 `settingsWs.webhook*` → `tools.webhook*`
- 新 `components/integrations/ui-entries-section.tsx`（读+删+新增 Dialog；invalidate `['pack-ui-entries', workspaceId]`）
- `api/packs.ts`：`PackUIEntry` 补 `pack_id?`；新增 `upsertPackEntry` / `deletePackEntry`（DELETE 已存在？实现时 grep 确认，无则补）
- `/tools` 页：三段组装（②锚点 `id="webhooks"`）+ 页头安装按钮迁入
- 删 `PacksCardsSection`（packs-sections.tsx 内）+ `packs-pages.test.tsx` 的 list describe（3 tests，引用随删）
- i18n 新增 `tools.uiEntries*` 键族（zh/en 成对）

### W2：删除资产挂载 + 清残留（纯删除波）

- 删 `pack-attach-dialog.tsx` 文件 + `pack-detail.tsx` 内：import（第 21 行）、`attachPicker` state（112 行区）、⑤ 资产区块两处 attach 按钮（约 375/418 行）+ detach 按钮 + picker Dialog 渲染；**保留** `PackUsageDialog` import（第 20 行，来自 pack-export-dialog）
- `api/packs.ts` 删 `setAgentPack` / `setSkillPack` + 挂载类型
- i18n 清死键：`packsInstall.attachAgent/attachSkill/detach*` 等（以 grep 确认零消费为准）
- 测试：`packs-platform.test.tsx` 精确删 attach/detach 两个 test（247/277 行区），**保留** install wizard 4 tests 与 usage doc test（207 行区）
- grep 零残留验证

### 验收清单（两波合入后真机）

1. /tools 三段渲染：服务器（现有 2 行）+ 订阅（payload 1 条）+ 页面入口（手动建 1 条侧栏入口 → **侧栏 PackEntries 立即出现**可点开铸票跳转 → 删除 → 侧栏消失）
2. 工单 tab 型入口：建一条 → 工单详情 tab 列表出现 `pack:<名>` tab → iframe 渲染
3. Webhook 段：Ping 通/轮换一次性明文/暂停恢复/投递记录展开重放（既有链路回归）
4. settings 工作区区无 Webhook 区块（唯一家验证）
5. pack-detail 从服务器行徽标链入，五区块正常；事件区块链接指向 /tools#webhooks；使用文档按钮仍可用；⑤ 资产区块纯只读无挂载按钮
6. 手动入口 upsert 幂等（同名重 PUT 覆盖不新建行）
7. 挂载/导出代码 grep 零残留；pnpm build/lint（存量 2 error 外零新增）；vitest：packs-pages（list 块已删）/packs-platform（精确删 2 留 5）绿

## 四、风险与边界

1. **手动入口与重装**：手动入口用 `custom-*` 名不撞 provision 的 pack 名；PUT 幂等按 (workspace_id, pack) 覆盖。真包卸载不碰手动入口（repo delete 按 pack 名定向）。
2. **删除入口 ≠ 卸载包**：来自包的入口行移除后，包重装（provision install 幂等）会重新写入。行内文案说明。
3. **webhook 迁移不碰权限**：写操作 admin 门槛在端点上，迁移不涉。
4. **i18n 键族迁移**注意 zh/en 同步删旧增新，防键漂移（历史坑）；旧键残留会以死键告警形式暴露。
5. **UpsertWithSlots 放宽的回归面**：provision 路径必须走原分支——单测钉住（packID 非空 → 原 conflict 目标）。

## 五、核对无误的事实（存档）

- ui-token mint 不校验 plugin 存在性（custom-* 铸票可行）
- `validateURLTemplate` 强制绝对 http(s)
- webhook section props = `{workspaceId, role}`，可直接挂 /tools
- `/packs` 列表 redirect 已在（上一轮落地）
- PUT/DELETE ui-entries 已带 `requireWorkspaceAdmin`
- pack-detail 事件链接现状 `to="/settings" hash="webhooks"` ✓ 待改目标正确
- ~~表约束 `UNIQUE (workspace_id, pack)`（迁移 1033）在手~~ **v1.2 推翻：1042 已 DROP，需 1047 新建 partial index（见 §六 G1）**
- 手动入口 pack_id NULL 不被 pack 详情聚合读到（repo_packtoolschema_byid.go 按 pack_id 过滤）——手动入口不进任何包台账，语义正确

## 六、修订记录

### v1.1 → v1.2（二次核对：迁移/读列两个更深的问题）

| # | v1.1 问题 | 修订 |
|---|---|---|
| G1 | "表已有 UNIQUE(workspace_id, pack)"错误——迁移 1042 已 DROP 该约束 | §2.3 改新迁移 1047 partial unique index（`WHERE pack_id IS NULL`） |
| G2 | repo List 没扫 pack_id 列（徽标无数据源） | §2.3 补 COALESCE 读列 + Scan |
| G3 | 后端改动量描述过小（"约 15 行"） | 重写为"迁移+双分支+读列"，附回归钉 |

### v1 → v1.1（首轮审计：8 项吸收）

| # | v1 问题 | 修订 |
|---|---|---|
| F1 | "手动新增零后端改动"错误——`UpsertWithSlots` 要求 pack_id 非空 | §2.3 加后端放宽 + W1 后端项 + 单测 |
| F2 | 删除目标写错——`pack-export-dialog.tsx` 实为 `PackUsageDialog`（使用文档），非导出功能 | §1.2/§1.3 改为保留；W2 删除范围收窄为 attach-dialog；测试精确删除对 |
| F3 | 前端 `PackUIEntry` 缺 `pack_id` 字段 | §2.3 类型补齐项 + W1 |
| F4 | 三消费方共享缓存键未提 | §2.3 明示 `['pack-ui-entries', workspaceId]` invalidate |
| F5 | 侧栏 PackEntries 不过滤 entry（tab 型漏进侧栏） | §2.3 已知行为注记（v0 接受） |
| F6 | "i18n 搬家/复用"含糊 | §2.2 精确为 `settingsWs.webhook*` → `tools.webhook*` 整族迁移 |
| F7 | 安装按钮随 PacksCardsSection 删除会丢入口 | §2.4 + W1 迁入页头（testid 保持） |
| F8 | 契约细节（响应形状/校验规则）缺 | §2.3 校验规则节（照抄用） |



## 七、v1.3 补记：AI 即安装器落地 + CLI 退役（2026-10-15 当日续做）

用户裁决链："provision CLI 能不能去掉" → 分析出 CLI 是 pack.yaml 唯一消费者 →
"用户能调平台 MCP，直接让大模型在平台配置就好了" → **两期实施完成，CLI 已删**。

### 7.1 助理配置面（/api/mcp/w/{ws}/me 16→26 工具）

| 期 | 工具 | 说明 |
|---|---|---|
| 一期 | 读 3：list_mcp_servers / list_webhook_subscriptions / list_page_entries | member 可读 |
| 一期 | 写 4：create_credential / create_mcp_server / create_webhook_subscription / put_page_entry | admin（requireAdminMe） |
| 二期 | 包 3：list_packs / register_pack / uninstall_pack | 读 member / 写 admin |

关键实现：
- 复用 REST 同批 service 实例（orchHTTPDeps.MCP/Credentials/PackUIEntries + webhookSvc + packDeps.Packs）
- `create_mcp_server` 的 bearer_token → credentials 信封 `headers.Authorization`（credential_id 仅审计关联——MCP 鉴权链不解引用，installer 时代真机 401 教训）
- `create_webhook_subscription` 带 pack_id → 直链 pack_subscription 连接表
- `put_page_entry` 走手动入口路径（packID 空=手动，迁移 1047 partial index），带页签时**自动跑 CSP frame-ancestors 探测**（不合规只警告不拒绝——探测抖动不误杀入口）
- `uninstall_pack` = CLI cleanPackByID 的服务端对应物：订阅本体（webhook 域）→ MCP（pack_id 反查）→ 技能硬删 → 智能体归档 → pack 行（级联入口/schema）

### 7.2 CLI 退役删除清单

- `cmd/pack-provision/` 整目录（main.go 1139 行 + 4 个测试文件）
- `registry_assembly.go` 的 D2 联调门面（BuildMCPTools / NewToolRegistryForTest / RegisterMCPBridgeForTest / SnapshotForTest / ToolsForTest / SchemaOf / Raw——零消费方随删）
- `packs/agentblog/README.md` 安装指引改写为 AI 对话路径
- 保留：pack.yaml 分发格式本身、e2e/golden_thread（agentblog 联调另一门控）

### 7.3 顺手修复

- `e2e/golden_test.go` TRUNCATE 列表补 6 张表（workflow/pack 迁移时代遗留，删列迁移没同步测试夹具）——全量 go test 恢复零失败
