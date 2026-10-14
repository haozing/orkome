# 助理接入设计:用户即执行器(Assistant Access, v2)

> 日期 2026-10-14 · 状态:待评审(v2)
> 本版按三方审计(docs/assistant-access-audit-2026-10-14.md,21 项缺陷)整体重写 v0/v1——正文即最终口径,无附录补丁。v0 的"编排基础设施零改造天然生效"论断已被证伪,v2 如实计入四项基建改造成本。
> 纪律:不考虑兼容性、不考虑旧数据迁移。工人模型按"冻结→物理删除"两刀退役。

---

## 一、产品定义

**一句话**:用户本机的 AI 客户端(Claude Code / Codex / dsh)以**用户本人身份**接入 orkome——对话式操作平台(助理),并可开启**自动接单**:指派给用户的工单,由用户 AI 认领、执行、结算,全过程在工单时间线上留下与平台智能体同构的结构化任务记录。

### 1.1 两种工作方式,一个身份

| | 对话式(助理) | 自动接单(代跑) |
|---|---|---|
| 驱动 | 用户在客户端里说话,即问即干 | 工单指派给用户 → AI 轮询 → 认领 → 干 → 结算 |
| 平台痕迹 | 业务动作本身(工单变更/评论,记用户名下) | 任务行 + 状态机 + 心跳 + outcome(渲染"张三(AI 代)") |
| 用的工具 | 读+写工具(§3.2 前 7 个) | 再加任务协议 5 工具 |
| 需要开启? | 不需要 | 不需要平台开关——认领即开启 |

### 1.2 为什么任务级(β)而不是工单级(α)

平台灵魂是"编排与归因"。α(直接改工单状态)使 AI 代跑成为平台黑箱:无进度、无失控感知、无 outcome、无统计。β 让所有执行(引擎/用户 AI)在时间线上同构——与 outcome/汇报 tab 一等公民语义一致。**审计确认:这份可观测性有真实基建成本(§4.3 四项),不是"免费复用"——本设计如实计入。**

---

## 二、总体结构

```
                    ┌─ /access 页(重写,§5)
                    │
用户 PAT ──Bearer──▶ /api/mcp/w/{ws}/me  (助理 MCP 端点; ws 走 URL path)
                    │
                    ├─ 读工具: WorkItemService / CatalogService (共用)
                    ├─ 写工具: WorkItemService.Update / CommentService (共用)
                    │           作者 = PAT 归属用户; 评论不触发 agent 派发(§3.2)
                    └─ 任务协议: AssistantTaskService (新, §4)
                        claim(单事务+互斥) / report(写心跳行) / complete(结算+推进工单) / fail(终态无重试)
                            │
                            ▼
                      agent_task_queue + agent_runtime(心跳行) —— 表零变更
                      liveness sweeper / 滞留告警 / 结算尾部公共层 —— 四项基建改造(§4.3)
```

**归一原则**:业务层一套(WorkItemService/CommentService/eventbus/PAT 全共用);新逻辑集中在 AssistantTaskService + 四项基建改造。

---

## 三、助理 MCP 端点

### 3.1 端点与认证

- 路由:`POST/GET /api/mcp/w/{ws}/me`。**workspace 由 URL path 携带**(全平台惯例:REST 用 X-Workspace-Id 头,MCP 先例用 URL path——不采用"工具参数携带 ws",那会让 LLM 幻觉 ws id 且多区用户无从发现自己有哪些区;v0 不做 list_workspaces,backlog)。
- 认证:`auth(RequireAuth(workspaceMW(...)))`——workspaceMW 复用 wiring_mcp.go:59 先例(成员闸);只认用户 PAT(`Bearer ork_` 前缀,csrf_session.go:68 已豁免,零新机制)。principal 即登录用户。
- 拒绝:runtime credential 的 PAT 进不来(它挂虚拟用户、p.AgentID 非空,按 user 成员校验不通过)。
- **限流**:端点挂粗限流(每 PAT 60 req/min,防轮询失控;全平台首个限流面,backlog 演进)。

### 3.2 工具清单(12 个)

**读(3)**
| 工具 | 入参 | 底层 |
|---|---|---|
| `list_work_items` | assignee?(`me`)/ status? / project? / limit? | WorkItemService.List(`me`=PAT 用户) |
| `get_work_item` | work_item_id | brief 聚合(实体+时间线+任务结论,同工人端点先例) |
| `list_projects` | — | **CatalogService.ListProjects**(service_catalog.go:192;v0 笔误 ProjectService 不存在) |

~~get_my_context~~ **砍掉**:my-work 页是纯前端拼装(my-work.tsx:107-131),无后端聚合可复用;AI 用 list_work_items 自拼(backlog:后端聚合端点)。

**写(4)**
| 工具 | 入参 | 底层 | 备注 |
|---|---|---|---|
| `create_work_item` | title, description?, project?, assignee? | WorkItemService.Create | assignee 可 `me` |
| `update_work_item` | work_item_id, patch(title/description/priority/status/labels/…) | WorkItemService.Update | **内置"先读拿 expected_revision 再写"**(service_workitem.go:168 乐观锁,工具层包掉;labels 并入 patch,不设独立工具) |
| `add_comment` | work_item_id, content, parent_id? | CommentService.Create | author=user;**加 skipTrigger 选项**(service_comment.go:203 的 triggerEngine.OnComment 跳过——AI 以用户身份评论不得意外触发 agent 派发,审计 P2-7 裁决) |
| `claim_work_item` | work_item_id | 见任务协议(§4.2) | |

**任务协议(5)——自动接单**
| 工具 | 入参 | 语义 |
|---|---|---|
| `list_my_claims` | — | 指派给我 AND 状态在待处理集 AND **无进行中代理任务 AND 无完成态结算记录**(谓词防死循环,§4.2)——新增查询(work 域 ListQuery 无 NOT EXISTS 过滤,需加) |
| `claim_work_item` | work_item_id | **单事务**:防重校验+InsertTask+置 running(§4.2) |
| `report_progress` | task_id, content | 进度消息 + **写心跳行**(§4.2 活信号) |
| `complete_work_item` | task_id, result | 结算(经公共尾部:outcome 落库)+ **自动推进工单到完成态** |
| `fail_work_item` | task_id, reason | 终态,无重试(§4.2) |

### 3.3 权限模型

- 工单操作面 = member 权限(审计确认:REST 面 PUT 工单无 admin 闸,MCP 端点经 workspaceMW 自带成员闸,"与网页同权"在业务操作面成立)。
- **PAT 暴露面显式裁决(v0)**:AI 客户端持有 PAT = **账号完整权限**(含 workspace/成员/webhook/自动化管理面)。v0 接受,页面文案明示风险;PAT scope 收敛列 backlog(scope 字段已存在未消费,审计 P1-6)。
- 评论与本人发言 v0 不区分(backlog:via=assistant 元数据)。

---

## 四、核心机制:AssistantTaskService(claim-on-behalf)

### 4.1 代理智能体行(proxy agent)

- 每个开启过接单的用户,每工作区一个代理行:`runtime_kind='assistant_proxy'`,**幂等键 = (workspace_id, owner_user_id)**(按 owner 查重,非按名字——用户改名不破坏幂等)。
- **名字不落库**(name 存固定标识如 `assistant-proxy-<userID 前 8 位>`,防改名漂移);展示层动态拼"张三的 AI"。
- 惰性创建:claim 首次触发;**三处枚举同步改**(审计 P1-4):AgentService.Create 的 switch(service.go:67)、agent 表 DB CHECK(migrations/1032,新迁移)、dispatcher。
- 隐藏:GET /api/agents 过滤 assistant_proxy(现有查询无过滤——repo.go:89;**@mention 候选同步排除**,comment-thread.tsx:50 吃同一接口,审计 P2-3);不可指派、引擎不执行(§4.3)。

### 4.2 接单循环(机制化,不是约定)

**claim_work_item——单事务**(审计 P0-1/P1-1):
```
BEGIN
  0. (事务外先做,幂等)确保 proxy 行存在——UNIQUE(workspace_id, owner_user_id) 兜底并发
  1. 防重:工单 NOT EXISTS(该 proxy 行的 queued/running 任务)——幂等互斥
     (唯一索引 uq_one_pending 不覆盖 running,1003:106;防线放事务内)
  2. InsertTask:status 直插 'running',max_attempts=1,
     originator_source='assistant_claim', context.on_behalf_of=userID
     (绕过 EnqueueAssignment 的模型配置闸,service.go:596 会拒 proxy——
      直插先例 handlers_taskview.go:296)
  3. 写心跳行:agent_runtime(instance_id='assistant:<taskID>', last_seen_at=now())
COMMIT   冲突 → 409
```

**活信号 = 真心跳**(审计 P0-2):report_progress / claim / complete 每次事务内 UPSERT 心跳行。FailStaleRunning 现有 SQL(repo.go:633)按 `runtime_id → agent_runtime.last_seen_at` 查活——代理任务 runtime_id 填 `assistant:<taskID>`,**现有判死逻辑零 SQL 改动天然兼容**;150s 无心跳判死照常生效(这才是 β 承诺的失控感知)。判死即终态(无重试,见下)。

**complete_work_item——结算 + 推进工单**(审计 P0-3/P1-2):
1. 结算走**公共尾部**(从 SettlementService.Settle 抽出:summary 生成 + outcome 写入 `s.outcomes.WriteOutcome`,service.go:838-849 同款;ExternalTaskService 同步接线——顺带修复 external 任务从不落 outcome 的存量缺陷);
2. **自动推进工单状态到完成态**(同 SettlementService 对引擎任务的行为;AI 不手动 update 状态——消除 list_my_claims 死循环:完成结算后谓词不再满足);
3. 心跳行清掉(任务终态)。

**fail_work_item——终态无重试**(审计 P0-4):max_attempts=1 已在 claim 时强制;FailRun 的 Retryable 重试分支(failuretax 白名单,failuretax.go:24)对 assistant_proxy **短路**——客户端可传 "timeout" 触发排队垃圾的洞封死。fail 后工单留在待处理集,但"曾有终态任务"进 list_my_claims 排除谓词;用户重新求助=再 claim(新任务行)。

**多客户端**:同用户多客户端(Claude Code+Codex)共享 proxy 行与身份;claim 互斥由事务保证(先到先得,后到 409);对同一 running 任务并发 complete——guard(WHERE status='running')先到先得,后到 409,可接受。

### 4.3 编排边界(四项基建改造,W1)

| # | 改造 | 现状(审计证据) | 改法 |
|---|---|---|---|
| 1 | **dispatcher 白名单** | 黑名单只跳 external(dispatcher.go:141),proxy 会被引擎抢跑废 | `if agent.RuntimeKind != "engine" { continue }` |
| 2 | **心跳行机制** | report_progress 非心跳(repo.go:660 只插消息),代理任务 150s 必死 | §4.2 心跳行(UPSERT agent_runtime) |
| 3 | **结算尾部公共层** | outcome 只在 SettlementService.Settle 写(service.go:838),external/proxy 均不落 | 抽公共函数,external/proxy 接线 |
| 4 | **重启豁免+告警放开** | FailRunningAtStartup 只豁免 external(repo.go:507);滞留告警硬编码 external(repo.go:628) | 豁免 IN ('external','assistant_proxy');告警 SQL 放开 kind |

---

## 五、/access 页重写(纯助理)

```
① 三步接入:1. 建令牌 2. 贴配置(端点=/api/mcp/w/{ws}/me) 3. 对 AI 说"帮我把 X 标完成"
   + 接单一句话:把工单指派给自己,对 AI 说"盯着我被指派的工单"
② 工具清单(12 个,真实注册面)
③ 访问凭证 = PAT 管理(现有段成为主角;文案明示"AI 客户端持令牌=账号完整权限")
④ 一键接入:Claude Code CLI 命令 / Codex / 通用客户端
```

工人接入段整体删除。client-kit/AGENTS.md 增补助理版接单循环(含最小轮询间隔约定,如 ≥30s,配合 §3.1 限流)。

---

## 六、工人模型退役(两刀)

**第一刀(本波):产品面消失 + API 闸门**
- /access 工人段删除;**CreateAgent API 拒绝 runtime_kind='external'**(service.go:67 switch 收紧;前端表单本无该字段——审计 P2-9,原表述"从表单移除"是空操作);runtime-onboarding.md 归档。
- 端点与表冻结保留:mountAgentMCP 全部、agent_credential 表、ExternalTaskService。

**第二刀(独立清理波):物理删除**
- 删 mountAgentMCP(含 runtime-credentials 管理端点)/ ExternalTaskService(结算公共层抽出后,§4.3-3 已完成时)/ sweeper external 分支。
- 迁移:DROP agent_credential;runtime_kind 枚举收敛为 **engine | assistant_proxy**(external 删除;v0 方案"删概念"与扩枚举矛盾,以此为准)。
- **依赖面(审计 P2-11 补录)**:agentBoundPAT 认证链(wiring_identity.go:122 每次认证查 agent_credential,删表必须同步拆包装);guard_runtime.go 的 /api/mcp/w 路径分支;GetAgentIDByPATID。

---

## 七、实施切分

| 波 | 内容 |
|---|---|
| W1 | 四项基建改造(§4.3)+ /api/mcp/w/{ws}/me + 12 工具 + AssistantTaskService(claim 事务/心跳/结算/无重试)+ /access 重写 + 枚举三处扩(proxy)+ 列表/@mention 过滤 |
| W2 | "张三(AI 代)"渲染三处前端(汇报 tab/时间线/任务面板——现状多数只显 ID 前缀或硬编码 'Agent',审计 P2-3)+ AGENTS.md 助理版 + 第一刀(API 拒 external) |
| W3 | 第二刀物理删除 + 迁移 |

**W1 验证口径**(审计增补后):①对话链:AI 建单/改状态/评论,作者=用户,评论不触发派发;②claim 互斥:双客户端并发 claim 只成一单(409×1);③接单链:claim(任务 running+心跳行)→progress(心跳续命)→complete(**outcome 落库+工单自动推进**)→list_my_claims 不再列出;④失控链:claim 后停心跳→150s 判死→**无重试子任务**→时间线留痕;⑤重启链:FailRunningAtStartup 不杀在跑代理任务;⑥引擎隔离:dispatcher 不碰 proxy 任务;⑦限流:超频 429。

---

## 八、已否决方案(存档)

- **α 工单级接单**:零后端改动,但 AI 代跑是平台黑箱——否决。
- **任务表加 user 列**:双态任务表,下游 SQL 全分叉——否决(proxy 锚点)。
- **工人模型页面降位**:页面只回答"怎么让我的 AI 操作平台"——整体消失比降位干净。
- **(v0)零改造复用编排**:审计证伪——dispatcher 黑名单/report 非心跳/outcome 断链/重试垃圾,四项基建改造为真实成本。
- **workspace 走工具参数**:LLM 幻觉 ws id+无发现能力——改 URL path(v1)。
