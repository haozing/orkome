# 助理接入设计:用户即执行器(Assistant Access, v0)

> 日期 2026-10-14 · 状态:**v0 已审计作废,按审计报告修订为 v1**(docs/assistant-access-audit-2026-10-14.md;§五 修订要点已并入下文各节,凡标注 [v1] 处为审计修正)
> 前置讨论结论:开放接入(/access)= 助理模式;工人模型从产品面退役(机制沉睡);自动接单走 **β 方案(任务级)**——用户的 AI 代跑产生与平台智能体同构的结构化执行记录。
> 纪律:**不考虑兼容性、不考虑旧数据迁移**,要干净的结构。工人模型(external agent / runtime credential / `/api/mcp/w/{ws}/agent`)按"可删"设计,分阶段冻结→拆除。

---

## 一、产品定义

**一句话**:用户本机的 AI 客户端(Claude Code / Codex / dsh)以**用户本人身份**接入 orkome——对话式操作平台(助理),并可开启**自动接单**:指派给用户的工单,由用户 AI 认领、执行、结算,全过程在工单时间线上留下与平台智能体完全同构的任务记录。

### 1.1 两种工作方式,一个身份

| | 对话式(助理) | 自动接单(代跑) |
|---|---|---|
| 驱动 | 用户在客户端里说话,即问即干 | 工单指派给用户 → AI 轮询 → 认领 → 干 → 结算 |
| 平台痕迹 | 业务动作本身(工单变更/评论,记用户名下) | **任务行 + 状态机 + 心跳 + outcome**(记"用户(AI 代)") |
| 用的工具 | 读+写工具(第 3 节前 8 个) | 再加任务协议 4 工具(第 3 节后 4 个) |
| 需要开启? | 不需要,接上就能用 | 不需要平台开关——**认领即开启**(见 4.1) |

### 1.2 为什么任务级(β)而不是工单级(α)

平台灵魂是"编排与归因"。α(直接改工单状态)会让"AI 代跑"成为平台看不见的黑箱:无进度、无失控感知、无 outcome、无统计。β 让**所有执行(引擎/用户 AI)在时间线上是同一种东西**——这正是 outcome/汇报 tab 已定型的一等公民语义。

---

## 二、总体结构

```
                    ┌─ /access 页(重写)
                    │   助理接入:配置 snippet + PAT 管理 + 接单玩法说明
                    │
用户 PAT ──Bearer──▶│ /api/mcp/me  (新:助理 MCP 端点, streamable-http)
                    │
                    ├─ 读工具: WorkItemService.List/Get (共用, 零新逻辑)
                    ├─ 写工具: WorkItemService.Update/Create + CommentService (共用)
                    │           作者一律 = PAT 归属用户 (author_type=user)
                    └─ 任务协议: AssistantTaskService (新, 见 §4)
                        claim_on_behalf / report / complete / fail
                            │
                            ▼
                      agent_task_queue (复用现有表, agent_id → 代表用户的那一行"代理智能体")
                      liveness sweeper / 结算 / outcome 沉淀 —— 全部复用现有编排基础设施
```

**归一原则**:底层 service 一套(WorkItemService/CommentService/eventbus/PAT 体系全共用),MCP 只是第三张皮。唯一新逻辑 = "用户任务"的代理行机制(§4)。

---

## 三、助理 MCP 端点 `/api/mcp/me`

### 3.1 端点与认证

- 路由:`POST/GET /api/mcp/me`,挂 root mux(与 `/api/mcp/w/...` 平级)
- 认证:`auth(RequireAuth(...))` —— **只认用户 PAT**(Bearer `ork_` 前缀,CSRF 已豁免该前缀,零新机制)。principal 即登录用户,**无 workspace 绑定**——workspace 由工具参数携带(用户可属多区;v0 工具全部显式收 `workspace_id` 参数,不收 header 隐式态)
- 拒绝:会话 cookie 不进(RequireAuth 的 PAT 分支);runtime credential 的 PAT 天然进不来(它挂在虚拟用户上、不是"人",见 §6 拆除)

### 3.2 工具清单(12 个)

**读(4)**
| 工具 | 入参 | 底层 |
|---|---|---|
| `list_work_items` | workspace_id, assignee?(user_id/`me`), status?, project?, limit? | WorkItemService.List(assignee=me 时解析为 PAT 用户) |
| `get_work_item` | workspace_id, work_item_id | 同工人端点 get_work_item 的 brief 聚合(实体+时间线+任务结论) |
| `list_projects` | workspace_id | ProjectService.List |
| `get_my_context` | workspace_id | 我的工作聚合(今日待办/进行中/最近结算)——复用 my-work 页数据面 |

**写(4)**
| 工具 | 入参 | 底层 | 备注 |
|---|---|---|---|
| `create_work_item` | workspace_id, title, description?, project?, assignee? | WorkItemService.Create | assignee 可 `me` |
| `update_work_item` | workspace_id, work_item_id, patch(title/description/status/priority/…) | WorkItemService.Update | 即用户在页面上能改的一切 |
| `add_comment` | workspace_id, work_item_id, content, parent_id? | CommentService.Create | **author_type=user, author_id=PAT 用户** |
| `list_labels` / `attach_label` | … | 既有 label service | v0 可并入 update_work_item,工具数再收敛 |

**任务协议(4)——自动接单**
| 工具 | 入参 | 语义 |
|---|---|---|
| `list_my_claims` | workspace_id | 列"指派给我且尚未被 AI 认领"的工单(接单看板,见 §4.2) |
| `claim_work_item` | workspace_id, work_item_id | **认领即建任务**(claim-on-behalf) |
| `report_progress` | workspace_id, task_id, content | 心跳+进度消息(复用 ExternalTaskService.ReportMessage 语义) |
| `complete_work_item` / `fail_work_item` | workspace_id, task_id, result/reason | 结算(走既有 settle 链:outcome 沉淀、时间线、失败留痕) |

> 工具命名用 `claim_work_item`(不是 claim_task):助理世界里的对象是工单,任务是认领的产物。协议内部仍是任务状态机。

### 3.3 权限模型

- 不新造权限点:工具操作 = 该用户在该 workspace 的既有 member 权限(与网页同权)。写操作走既有 service 的校验路径,不另设闸。
- v0 不做 per-tool 权限配置(未来需要再说)。

---

## 四、核心新逻辑:用户任务(claim-on-behalf)

### 4.1 关键设计:认领时建任务 + 代理智能体行

现有 `agent_task_queue.agent_id NOT NULL`(任务必须归属一个智能体)。β 要让"用户的 AI"产生任务行,干净的做法不是改表加 user 维度(会让任务表双态、所有下游 SQL 分叉),而是:

> **每个开启过接单的用户,自动获得一个(且仅一个)"代理智能体"(proxy agent)行:`name=<用户显示名> 的 AI`,`runtime_kind=assistant_proxy`。用户的任务挂在它的 agent_id 上。**

- 代理行由 `claim_work_item` 首次触发时惰性创建(用户无感知);一 workspace 一行,幂等。
- 任务的归因链:`agent_id=代理行` + 既有列 `originator_user_id=PAT 用户` + `context.on_behalf_of=user_id`。时间线展示层把 assistant_proxy 任务渲染为"**张三(AI 代)**"。
- **不动 agent_task_queue 表结构**——liveness/sweeper/结算/重试税/outcome 链对代理任务天然生效,零改造。
- 代理行不出现在智能体列表(列表查询过滤 `runtime_kind='assistant_proxy'`);不可指派、不可配置模型——它不是执行者,是归因锚点。

### 4.2 接单循环(用户侧全约定,平台无开关)

1. 工单指派给用户(既有动作,不变);
2. 用户对本机 AI 说一次:"帮我盯着指派给我的工单"(或写入 AGENTS.md 常驻);
3. AI 轮询 `list_my_claims`(assignee=me、状态在待处理集、且无进行中代理任务的工单);
4. `claim_work_item` → 平台建任务(queued→running),工单进入"AI 代处理中"的可观测态;
5. AI 干活,随手 `report_progress`(心跳 150s 内至少一次,liveness 判死自动兜底);
6. 干完 `complete_work_item(result)` → 走既有结算:outcome 沉淀到工单、汇报 tab 可见;失败 `fail_work_item(reason)` → 工单时间线留痕,可重新开始。

**防重复编辑**:claim 后工单详情页显示"AI 处理中"徽标(读任务行即可渲染,前端小改);v0 不做硬锁——用户手动改状态与 AI 结算并发时 last-write-wins,可接受。

### 4.3 与既有编排的边界

- dispatcher 对 `assistant_proxy` 智能体**永不派发**(runtime_kind 白名单里没有它)——代理任务只由用户 AI 的 claim 创建,引擎不碰;
- sweeper 的 `external_queued_stale`(60 分钟滞留告警)对代理任务照常生效:用户 AI 认领了却不干活,平台会喊;
- 小队/leader 逻辑与代理无关,互不可见。

---

## 五、`/access` 页重写(纯助理)

```
① 三步接入(改)
   1. 建个令牌(下方)  2. 贴配置(端点=/api/mcp/me)  3. 对 AI 说"帮我把 X 工单标完成"
   + 接单玩法一句话:想自动接单?把工单指派给自己,对 AI 说"盯着我被指派的工单"。
② 工具清单(12 个,真实注册面)
③ 访问凭证 = PAT 管理(现有段原样成为主角:创建/一次性明文/TTL/撤销)
④ 一键接入:Claude Code CLI 命令 / Codex / 通用客户端(端点换 /api/mcp/me)
```

- 工人接入(选 agent 铸 runtime credential 段)**整体删除**;
- AGENTS.md 接单引导更新为助理版(client-kit/AGENTS.md 增补 claim 循环说明)。

---

## 六、工人模型退役(冻结→拆除,不做兼容)

既然不要兼容包袱,按两刀切:

**第一刀(本波):产品面消失 + 冻结**
- /access 页工人段删除;`runtime_kind=external` 从建 agent 表单/文档中移除;
- 端点与表**保留但冻结**(代码不删,避免本波 diff 爆炸):`/api/mcp/w/{ws}/agent`、runtime-credentials 两个管理端点、agent_credential 表、ExternalTaskService 全量。

**第二刀(下个清理波):物理删除**
- 删 `mountAgentMCP` 全部(含 runtime-credentials 管理端点、上一波 214c2f3 加的);
- 删 ExternalTaskService + dispatcher 的 external 分支 + sweeper 的 external_queued_stale;
- 删 `runtime_kind` 概念(枚举只剩 engine / assistant_proxy)、agent_credential 表(DROP)、`GetAgentIDByPATID` 映射、runtime-onboarding.md;
- 迁移:DROP agent_credential;agent.runtime_kind 值收敛('external'→'engine',人工确认无存量使用后)。

> 注意:第二刀删 ExternalTaskService 前,确认 §4 复用部分(ReportMessage/settle 链)已抽到与 runtime_kind 无关的公共层——claim-on-behalf 的结算复用的正是这套,拆的时候留骨架。

---

## 七、实施切分

| 波 | 内容 | 产出 |
|---|---|---|
| W1 后端 | `/api/mcp/me` + 12 工具 + proxy agent 惰性创建 + claim-on-behalf(建任务走既有 InsertTask 路径)+ /access 页重写 | 端到端可用;真机:接单→心跳→结算→outcome 全链 |
| W2 收尾 | 前端"AI 处理中"徽标(工单详情读代理任务)+ client-kit AGENTS.md 助理版 + 工人面冻结(表单/文档移除) | 产品面纯助理 |
| W3 清理(独立波) | §6 第二刀物理删除 + 迁移 | 代码收敛 |

**验证口径**:①对话链:AI 以用户身份建单/改状态/评论,作者=用户;②接单链:指派→claim(任务行出现,工单徽标)→progress(心跳续命)→complete(outcome 落工单、汇报 tab 可见);③失控链:claim 后不心跳→150s liveness 判死→时间线留痕;④权限链:member 用户 PAT 只能操作本区、动作与网页同权。

---

## 八、已否决的替代方案(存档)

- **α 工单级接单**:零后端改动,但 AI 代跑成为平台黑箱(无进度/无失控感知/无 outcome/无统计),与产品"编排与归因"灵魂冲突。否决。
- **任务表加 user 维度**(agent_id 可空+user_id 列):双态任务表,所有下游 SQL(结算/sweeper/时间线)分叉,结构脏。否决——proxy agent 行方案零表结构变更。
- **工人模型页面降位保留**:经多轮讨论定案——页面只回答一个问题("怎么让我的 AI 操作平台"),工人模型从页面整体消失比降位更干净;机制随后波物理删除。


---

# [v1] 审计修订附录(2026-10-14,依据 docs/assistant-access-audit-2026-10-14.md)

v0 的核心结构(proxy agent 归因锚点 / 表零变更 / PAT 复用)经三方审计确认成立;但"编排基础设施零改造天然生效"被证伪。实施以本附录为准,与 v0 正文冲突处以本附录为准。

## A. 端点改为 /api/mcp/w/{ws}/me
URL path 携带 workspace(复用 workspaceMW 成员闸先例),废弃"工具参数携带 ws"设计;不做 list_workspaces(v0 单区用户为主,backlog)。

## B. 接单协议的机制修正(替代 §4.2 的约定式描述)
1. **claim_work_item = 单事务**:校验该工单无 active 代理任务(NOT EXISTS running/queued 代理任务)+ InsertTask(max_attempts=1, originator_source='assistant_claim', context.on_behalf_of=user)+ 置 running;冲突 409。
2. **活信号 = 真心跳**:claim/report_progress/complete 事务内写 agent_runtime 心跳行(虚拟 instance_id=`assistant:<taskID>`,last_seen_at=now())——FailStaleRunning 现有 SQL 按 runtime_id 查活,天然兼容;不再依赖"消息=心跳"的错觉。
3. **complete_work_item = 结算 + 自动推进工单**:结算尾部(outcome 写入+summary)从 SettlementService 抽公共函数,external/proxy 共用;结算成功后工单状态自动推进到完成态(AI 不手动 update 状态);list_my_claims 排除谓词含"或已有终态完成结算"。
4. **fail_work_item = 终态无重试**:max_attempts=1 已在 claim 时强制;FailRun 重试分支对代理任务短路;滞留/判死告警 SQL 放开 runtime_kind。
5. **重启豁免**:FailRunningAtStartup 豁免条件扩为 runtime_kind IN ('external','assistant_proxy')。

## C. 编排边界修正(替代 §4.3)
dispatcher 派发条件改**白名单** `runtime_kind == "engine"`(现为黑名单 skip external,新 kind 会被引擎抢跑——P0-1);agent 表 DB CHECK(migrations/1032)与 AgentService.Create switch 同步扩 'assistant_proxy'。

## D. proxy 行修正(替代 §4.1 细节)
幂等键=(workspace_id, owner_user_id);**名字不落库**(动态拼"张三的 AI",防改名漂移);/api/agents 列表过滤 assistant_proxy(现有查询无过滤,@mention 候选同步排除——前端待办);"张三(AI 代)"渲染涉及汇报 tab/时间线三处前端改造(P2-3)。

## E. 工具清单定稿(11 个)
读4:list_work_items / get_work_item / list_projects(实为 CatalogService.ListProjects)/ ~~get_my_context~~(砍——my-work 无后端聚合,backlog);写4:create/update_work_item(update 内置读-revision-写;labels 并入 patch)/ add_comment(裁决:AI 评论不触发 triggerEngine,防意外派发——CommentService 加 skipTrigger 选项);任务4:list_my_claims / claim_work_item / report_progress / complete_work_item + fail_work_item(共5,总 13→砍 context 后 12 个,见 §F)。

## F. 安全与限流(v0 显式裁决)
v0 接受 PAT 全权(文案警示"AI 客户端持 PAT=账号完整权限");/api/mcp/me 挂粗限流(如每 PAT 60 req/min);评论 via=assistant 元数据列 backlog。

## G. 实施切分修正(替代 §7 W1)
W1 在原范围上**追加四项基础设施改造**(这是 β 可观测性的真实成本):①dispatcher engine 白名单 ②agent_runtime 心跳行机制 ③结算尾部公共层抽取(outcome) ④FailRunningAtStartup 豁免+告警 SQL 放开。W1 验证口径增补:双客户端并发 claim 只成一单;判死后无重试子任务;服务重启不杀在跑代理任务;complete 后工单状态自动推进且 outcome 落库。

## H. 退役修正(§6)
第一刀口径改"**CreateAgent API 拒绝 runtime_kind=external**"(前端表单本无该字段,API 层才是闸门);第二刀枚举收敛为 engine|assistant_proxy(不是删除概念);依赖面补录 agentBoundPAT 认证链(wiring_identity.go:122)与 guard_runtime 路径分支。
