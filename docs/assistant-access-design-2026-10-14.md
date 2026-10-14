# 助理接入设计:用户即执行器(Assistant Access, v3)

> 日期 2026-10-14 · 状态:待评审(v3)
> 沿革:v0→首轮三方审计(21 项)→v2→**二轮双审计(事实核 N1-N10+可实施性 R1-R12,合并 19 项,3 P0)→本 v3**。二轮结论:心跳行方案结构成立(agent_runtime 主键/UPSERT/判死 SQL 兼容/无清理 worker 均实证),但落地细节(engine 列/判死分窗/runtime_id 写入路径/claim 行锁)与幂等索引、工单推进两处 P0 在 v3 修正。
> 纪律:不考虑兼容性、不考虑旧数据迁移。工人模型按"冻结→物理删除"两刀退役。

---

## 一、产品定义

**一句话**:用户本机的 AI 客户端(Claude Code / Codex / dsh)以**用户本人身份**接入 orkome——对话式操作平台(助理),并可开启**自动接单**:指派给用户的工单,由用户 AI 认领、执行、结算,全过程在工单时间线上留下与平台智能体同构的结构化任务记录。

### 1.1 两种工作方式,一个身份

| | 对话式(助理) | 自动接单(代跑) |
|---|---|---|
| 驱动 | 用户在客户端里说话,即问即干 | 工单指派给用户 → AI 轮询 → 认领 → 干 → 结算 |
| 平台痕迹 | 业务动作本身(工单变更/评论,记用户名下) | 任务行 + 状态机 + 心跳 + outcome(渲染"张三(AI 代)") |
| 用的工具 | 读+写工具(§3.2 前 8 个:读5+写3) | 再加任务协议 5 + 恢复 1 |
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
- **限流**:复用 automation 域现成 **WebhookRateLimiter**(internal/automation/adapters/http/ratelimit.go,memory+redis 双 store,默认 60/min;apierr.RateLimited→429 全链现成)——v2"全平台首个限流面"论断错误(二轮 N6),按 PAT 维度 key 接线,零新依赖。

### 3.2 工具清单(14 个 = 读 5 + 写 3 + 任务协议 5 + 恢复 1;对话式用前 8)

**读(5)**
| 工具 | 入参 | 底层 |
|---|---|---|
| `list_work_items` | assignee?(`me`)/ status? / project? / limit? | WorkItemService.List(`me`=PAT 用户) |
| `get_work_item` | work_item_id | brief 聚合(实体+时间线+任务结论+**活跃任务 task_id**——单工单视角的恢复数据面,二轮 R5) |
| `list_projects` | — | **CatalogService.ListProjects**(service_catalog.go:192;v0 笔误 ProjectService 不存在) |
| `list_labels` | — | CatalogService.ListLabels(service_catalog.go:132)——update 要带 labels,**值域必须有发现工具**(二轮 R9) |
| `list_statuses` | — | status 目录读面——**status_key 每区可配,AI 不能靠猜**(二轮 R9) |

~~get_my_context~~ **砍掉**:my-work 页是纯前端拼装(my-work.tsx:107-131),无后端聚合可复用;AI 用 list_work_items 自拼(backlog:后端聚合端点)。

**写(3)**
| 工具 | 入参 | 底层 | 备注 |
|---|---|---|---|
| `create_work_item` | title, description?, project?, assignee? | WorkItemService.Create | assignee 可 `me` |
| `update_work_item` | work_item_id, patch(title/description/priority/status/labels/…) | WorkItemService.Update | **内置"先读拿 expected_revision 再写"**(service_workitem.go:168 乐观锁,工具层包掉;labels 并入 patch) |
| `add_comment` | work_item_id, content, parent_id? | CommentService.Create | author=user;**加 skipTrigger 选项**(service_comment.go:203 triggerEngine.OnComment 跳过——AI 评论不得意外触发 agent 派发;用户再求助的"评论重置"走谓词时间戳,与此互不干扰) |

**任务协议(5)——自动接单**
| 工具 | 入参 | 语义 |
|---|---|---|
| `list_my_claims` | — | 指派给我 AND 待处理集 AND NOT(queued/running 代理任务 OR 24h 内终态代理任务且其后无用户评论)——**查询归属**:AssistantTaskService 先解析 proxy 行,work 域 ListQuery 新增谓词参数 `ExcludeAgentActiveTasks(agentID)` 注入(iron law 不拦跨域 SQL,先例 work repo.go:1171 join agent_task_queue;AgentID 由 orchestration 侧解析传入,二轮 N10) |
| `claim_work_item` | work_item_id | **单事务+行锁**:防重+InsertTask+CAS 置 running+runtime 绑定(§4.2) |
| `report_progress` | task_id, content | 进度消息 + UpsertRuntime 续命(§4.2 活信号) |
| `complete_work_item` | task_id, result | 结算(公共尾部:outcome 落库)+ 按规则推进工单(§4.2) |
| `fail_work_item` | task_id, reason | 终态,无重试(§4.2) |

**恢复(1)**
| 工具 | 入参 | 语义 |
|---|---|---|
| `get_my_running_tasks` | — | 我的代理行下 running 任务(id/工单/摘要/最后心跳)——**AI 进程重启后唯一的活动上下文入口**(list_my_claims 按定义排除它们;get_work_item brief 是单工单视角,此工具为全局,二轮 R5) |

### 3.3 权限模型

- 工单操作面 = member 权限(审计确认:REST 面 PUT 工单无 admin 闸,MCP 端点经 workspaceMW 自带成员闸,"与网页同权"在业务操作面成立)。
- **PAT 暴露面显式裁决(v0)**:AI 客户端持有 PAT = **账号完整权限**(含 workspace/成员/webhook/自动化管理面)。v0 接受,页面文案明示风险;PAT scope 收敛列 backlog(scope 字段已存在未消费,审计 P1-6)。
- 评论与本人发言 v0 不区分(backlog:via=assistant 元数据)。

---

## 四、核心机制:AssistantTaskService(claim-on-behalf)

### 4.1 代理智能体行(proxy agent)

- 每个开启过接单的用户,每工作区一个代理行:`runtime_kind='assistant_proxy'`,幂等键 = (workspace_id, owner_user_id),唯一性必须用**部分唯一索引**(迁移 1046):`CREATE UNIQUE INDEX uq_agent_assistant_proxy ON agent (workspace_id, owner_user_id) WHERE runtime_kind='assistant_proxy'`——**不可加普通 UNIQUE**:所有 API 建的普通 agent 默认写 owner_user_id(routes.go:273 ownerFallback),一 owner 多 agent 是常态,普通唯一索引会让用户建第二个 agent 即崩(二轮 N1,P0)。
- **名字不落库**(name 存固定标识如 `assistant-proxy-<userID 前 8 位>`,防改名漂移);展示层动态拼"张三的 AI"。
- 惰性创建:claim 首次触发,`INSERT ON CONFLICT DO NOTHING` + 按 key SELECT 拿确定性行 id;**四处同步改**:AgentService.Create 的 switch(service.go:67)、agent 表 DB CHECK(**unnamed 自动名 agent_runtime_kind_check**,DROP CONSTRAINT+ADD CHECK IN ('engine','external','assistant_proxy'),W1 扩/W3 收敛各一次)、dispatcher、**触发豁免修正**(service_trigger.go:141 本人评论豁免按 owner 判断,proxy 行 owner=user 会让用户评论被误豁免——豁免条件补 RuntimeKind=='engine',二轮 N9)。
- 隐藏:GET /api/agents 过滤 assistant_proxy(现有查询无过滤——repo.go:89;**@mention 候选同步排除**,comment-thread.tsx:50 吃同一接口,审计 P2-3);不可指派、引擎不执行(§4.3)。

### 4.2 接单循环(机制化,不是约定)

**claim_work_item——单事务 + 行锁互斥**(二轮 R1/R2/N3 修正:纯事务内 NOT EXISTS 在 READ COMMITTED 下两事务互不可见双双通过;InsertTask 硬编码 queued(repo.go:319),不存在"直插 running"):
```
BEGIN
  0. (事务外,幂等)INSERT proxy 行 ON CONFLICT DO NOTHING(部分唯一索引 1046 兜底)
     → SELECT id ... WHERE ws AND owner AND runtime_kind='assistant_proxy'
  1. SELECT ... FROM work_item WHERE id=$wi FOR UPDATE      -- 行锁串行化同工单并发
  2. 防重校验(锁内):NOT EXISTS(该 proxy 行 queued/running 任务)→ 有则 409
  3. InsertTask(落库 queued)→ 同事务 CAS 置 running:
     UPDATE ... SET status='running', started_at=now() WHERE id=$t AND status='queued'
     (ClaimExternalTask repo.go:539 同款;不走 StartTask——它要求 dispatched 起点)
     max_attempts=1, originator_source='assistant_claim', context.on_behalf_of=userID
     (绕过 EnqueueAssignment 模型配置闸 service.go:596,直插先例 handlers_taskview.go:296;
      domain 常量集同步加 SrcAssistantClaim——rules.go:81 无此值,归因消费端行为需单测,二轮 N8)
  4. SetRuntime(taskID, 'assistant:<taskID>')(repo.go:533,恰好要求 status='running')
  5. UpsertRuntime('assistant:<taskID>', engine='assistant', caps={})(repo.go:520 形状)
     —— agent_runtime.engine NOT NULL 无默认(1003:68),必须带 engine 列(二轮 N4);
      禁用 HeartbeatRuntime 冒充 UPSERT(UPDATE-only,行丢失时静默无操作→误判死)
COMMIT   冲突 → 409
```

**活信号 = 真心跳**(二轮 R3/R4 P0 修正:**150s 全 kind 统一窗口对 AI 客户端不可执行**——长本地工具执行期间模型无法并发调 MCP,"每 150s 必 report"是空头约定;误杀=终态无重试+工单从列表消失,整链即伤):report_progress / claim 每次事务内 UpsertRuntime 续命;**判死按 kind 分窗(基建第五项)**——FailStaleRunning 加 kind 维度:engine 维持 150s(装配 wiring_orchestration.go:313),assistant_proxy 放宽到 **30min**;AGENTS.md 助理版约定"心跳 ≤15min 一次,长本地任务开工前先 report 预告"。判死即终态(无重试);判死后 AI 回来 complete 得 409 属预期行为(文档化)。

**complete_work_item——结算 + 推进工单**(二轮 N2 P0 修正:**"同 SettlementService 行为"是不存在的先例**——Settle(service.go:819-861)全文零处写 work_item.status;引擎模式下工单状态是引擎 LLM 自己调 update_work_item_status 工具(tools_work.go:22)推进的。自动推进是**本设计全新行为**,规则如下):
1. 结算走**公共尾部**(从 Settle 抽 outcome 段 service.go:838-849——依赖仅 outcomes 端口+WorkItemID+事件,可抽;chat 追写段 external/proxy 无上下文,分段参数化;ExternalTaskService 同步接线,顺带修复 external 从不落 outcome 的存量缺陷);
2. **推进规则**:查该工作区 status 定义(per-workspace 可配),推到 **category='done' 且 position 最小的 status_key**;**当前已属终态类(done/closed)→ 只结算不推进**(不越权覆盖用户手动结果);推进走 UpdateStatusByAgent 通道(service_workbench.go:32),revision 冲突重读重试一次,再败则结算成功+推进放弃+时间线留痕;与 resetIdleWorkItem 级联(TaskCompleted→in_progress 打回 todo,main.go:108)交互:推进到 done 后 reset 的 status guard 自然跳过,安全;
3. 心跳行清理:complete/fail 事务内 DELETE agent_runtime(**全新逻辑,全仓无先例**,二轮 N5);判死路径由 sweeper FailRun 后补删 runtime_id LIKE 'assistant:%' 的孤儿行。

**fail_work_item——终态无重试**(审计 P0-4):max_attempts=1 已在 claim 时强制;FailRun 的 Retryable 重试分支(failuretax 白名单,failuretax.go:24)对 assistant_proxy **短路**——客户端传 "timeout" 触发排队垃圾的洞封死。
**fail 后再求助通路**(二轮 R7 统一谓词):list_my_claims 排除 = "有 queued/running 代理任务 OR 24h 内有终态代理任务(**且其后用户未再评论**)";claim 防重 = 只挡 queued/running。fail 后 24h 内工单暂列不出但 claim 照样可进——**用户在该工单发一条评论即重置排除**(主通路),口述工单 ID 直接 claim(兜底),交互断层闭合。

**多客户端**:同用户多客户端(Claude Code+Codex)共享 proxy 行与身份;claim 互斥由事务保证(先到先得,后到 409);对同一 running 任务并发 complete——guard(WHERE status='running')先到先得,后到 409,可接受。

### 4.3 编排边界(六项基建改造,W1;二轮增补第 5/6 项)

| # | 改造 | 现状(审计证据) | 改法 |
|---|---|---|---|
| 1 | **dispatcher 白名单** | 黑名单只跳 external(dispatcher.go:141),proxy 会被引擎抢跑废 | `if agent.RuntimeKind != "engine" { continue }` |
| 2 | **心跳行机制** | report_progress 非心跳(repo.go:660 只插消息),代理任务必死 | §4.2 UpsertRuntime 心跳行(engine='assistant') |
| 3 | **结算尾部公共层** | outcome 只在 SettlementService.Settle 写(service.go:838),external/proxy 均不落 | 抽公共函数,external/proxy 接线 |
| 4 | **重启豁免+告警放开** | FailRunningAtStartup 只豁免 external(repo.go:507);滞留告警硬编码 external(repo.go:628) | 豁免 IN ('external','assistant_proxy');告警 SQL 放开 kind |
| 5 | **判死按 kind 分窗**(二轮 R3 P0) | FailStaleRunning 全 kind 统一 150s(repo.go:633;装配 :313) | kind 维度:engine 150s / assistant_proxy 30min |
| 6 | **claim 写路径三件套**(二轮 N3/N5/N7) | InsertTask 硬编码 queued(repo.go:319)且无 runtime_id 列;agent_runtime 无 DELETE 先例;CHECK 是 unnamed 约束 | 事务内 CAS 置 running+SetRuntime;心跳行 DELETE 新方法;DROP/ADD CHECK DDL |

---

## 五、/access 页重写(纯助理)

```
① 三步接入:1. 建令牌 2. 贴配置(端点=/api/mcp/w/{ws}/me) 3. 对 AI 说"帮我把 X 标完成"
   + 接单一句话:把工单指派给自己,对 AI 说"盯着我被指派的工单"
② 工具清单(14 个,真实注册面)
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
| W1(协议纵切,独立可验证) | **六项基建改造(§4.3)**+ /api/mcp/w/{ws}/me + 14 工具 + AssistantTaskService(claim 行锁事务/心跳/结算/无重试)+ 枚举四处扩(switch/CHECK DDL/dispatcher/触发豁免)+ 限流接线(复用 WebhookRateLimiter) |
| W2(产品面) | /access 页重写 + "张三(AI 代)"渲染三处前端 + **@mention 候选排除**(与渲染同批,同吃 listAgents)+ GET /api/agents 列表过滤 + AGENTS.md 助理版(心跳 ≤15min 约定)+ 第一刀(API 拒 external) |
| W3 | 第二刀物理删除 + CHECK 收敛迁移 |

**W1 验证口径**:①对话链:AI 建单/改状态/评论,作者=用户,评论不触发派发;②claim 互斥:双客户端并发 claim 同工单只成一单(409×1,FOR UPDATE 行锁生效);③接单链:claim(任务 running+runtime_id+心跳行)→progress(UpsertRuntime 续命)→complete(**outcome 落库+按规则推进工单;已终态工单只结算**)→list_my_claims 不再列出;④失控链:claim 后停心跳→**30min** 判死→无重试子任务→时间线留痕;⑤重启链:FailRunningAtStartup 不杀在跑代理任务;⑥引擎隔离:dispatcher 不碰 proxy 任务;⑦限流:超频 429;⑧**显式 fail_work_item 不排重试子任务**(二轮 R12);⑨**滞留告警覆盖 assistant_proxy**(R12);⑩**fail 后用户评论→工单重回 list_my_claims**(再求助通路);⑪AI 重启→get_my_running_tasks 拿回上下文。

---

## 八、已否决方案(存档)

- **α 工单级接单**:零后端改动,但 AI 代跑是平台黑箱——否决。
- **任务表加 user 列**:双态任务表,下游 SQL 全分叉——否决(proxy 锚点)。
- **工人模型页面降位**:页面只回答"怎么让我的 AI 操作平台"——整体消失比降位干净。
- **(v0)零改造复用编排**:审计证伪——dispatcher 黑名单/report 非心跳/outcome 断链/重试垃圾,四项基建改造为真实成本。
- **workspace 走工具参数**:LLM 幻觉 ws id+无发现能力——改 URL path(v1)。
