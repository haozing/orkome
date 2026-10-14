# 助理接入设计:用户即执行器(Assistant Access, v4)

> 日期 2026-10-14 · 状态:待评审(v4)
> 沿革:v0→首轮三方审计(21 项)→v2→二轮双审计(19 项,3 P0)→v3→**有人值守重构(v4)**。
> **v4 的根本转向(用户裁决)**:v3 及此前的大量机制强度(心跳/判死/告警/重试)继承自"无人值守的工人"假设;助理模式的真实形态是**有人值守**——人是最大的活性信号,人离开的窗口期靠"超时挂起+交还"而非"平台代杀"。v4 做减法:删三套自动机,加一个 stalled 语义。二轮审计的结构性结论(claim 行锁/部分唯一索引/结算公共层/枚举迁移)全部保留。
> 纪律:不考虑兼容性、不考虑旧数据迁移。工人模型按"冻结→物理删除"两刀退役。

---

## 一、产品定义

**一句话**:用户本机的 AI 客户端(Claude Code / Codex / dsh)以**用户本人身份**接入 orkome——对话式操作平台(助理),并可开启**自动接单**:指派给用户的工单,由用户 AI 认领、执行、结算,全过程在工单时间线上留下与平台智能体同构的结构化任务记录。

### 1.1 两种工作方式,一个身份

| | 对话式(助理) | 自动接单(代跑) |
|---|---|---|
| 驱动 | 用户在客户端里说话,即问即干 | 工单指派给用户 → AI 轮询 → 认领 → 干 → 结算 |
| 平台痕迹 | 业务动作本身(工单变更/评论,记用户名下) | 任务行 + outcome(渲染"张三(AI 代)") |
| 用的工具 | 读+写工具(§3.2 前 8 个:读5+写3) | 再加任务协议 5 + 恢复 1 |
| 需要开启? | 不需要 | 不需要平台开关——认领即开启 |

### 1.2 有人值守假设(v4 的根基)

三档时间线,平台各司其职:
- **人在场**(常态):人是最大的监控——AI 就在用户终端里跑,进度、卡顿用户直接看得见。平台不做活性检测。
- **人短暂离开,AI 继续干**(正常窗口期):平台只负责**留痕**——complete 落 outcome/时间线/通知,用户回来自然看到。
- **人离开且 AI 卡死/进程死**(僵尸窗口期):平台唯一动作是**超时挂起(stalled)+ 通知交还**——"这事没人管了,还给用户",不做判死、不做重试、不发告警噪音。责任交还人,平台不代决策。

由此,v3 的三套工人遗产机制整体删除(详见 §4.4):心跳行+判死分窗(150s/30min)、滞留告警、重试短路(保留 max_attempts=1 但不再需要"短路"逻辑——见 §4.2 fail)。**平台在窗口期只做两件事:交还与留痕。**

### 1.3 为什么任务级(β)而不是工单级(α)

平台灵魂是"编排与归因"。α(直接改工单状态)使 AI 代跑成为平台黑箱:无进度、无产出档案、无统计。β 让所有执行(引擎/用户 AI)在时间线上同构。v4 保留 β 的**结构**(任务行/outcome/同构时间线),放弃其从无人值守继承的**自动机**(判死/重试/告警)。

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
                        claim(单事务+行锁互斥) / report(纯消息) /
                        complete(结算+推进工单) / fail(终态) / stalled(sweeper 超时交还)
                            │
                            ▼
                      agent_task_queue —— 表零变更
                      结算尾部公共层 —— 基建改造之一(§4.3)
```

**归一原则**:业务层一套(WorkItemService/CommentService/eventbus/PAT 全共用);新逻辑集中在 AssistantTaskService + 三项基建改造(§4.3)。

---

## 三、助理 MCP 端点

### 3.1 端点与认证

- 路由:`POST/GET /api/mcp/w/{ws}/me`。**workspace 由 URL path 携带**(全平台惯例:REST 用 X-Workspace-Id 头,MCP 先例用 URL path——不采用"工具参数携带 ws",那会让 LLM 幻觉 ws id 且多区用户无从发现自己有哪些区;v0 不做 list_workspaces,backlog)。
- 认证:`auth(RequireAuth(workspaceMW(...)))`——workspaceMW 复用 wiring_mcp.go:59 先例(成员闸);只认用户 PAT(`Bearer ork_` 前缀,csrf_session.go:68 已豁免,零新机制)。principal 即登录用户。
- 拒绝:runtime credential 的 PAT 进不来(它挂虚拟用户、p.AgentID 非空,按 user 成员校验不通过)。
- **限流**:复用 automation 域现成 **WebhookRateLimiter**(internal/automation/adapters/http/ratelimit.go,memory+redis 双 store,默认 60/min;apierr.RateLimited→429 全链现成)——按 PAT 维度 key 接线,零新依赖。

### 3.2 工具清单(14 个 = 读 5 + 写 3 + 任务协议 5 + 恢复 1;对话式用前 8)

**读(5)**
| 工具 | 入参 | 底层 |
|---|---|---|
| `list_work_items` | assignee?(`me`)/ status? / project? / limit? | WorkItemService.List(`me`=PAT 用户) |
| `get_work_item` | work_item_id | brief 聚合(实体+时间线+任务结论+**活跃任务 task_id**——单工单视角的恢复数据面) |
| `list_projects` | — | **CatalogService.ListProjects**(service_catalog.go:192;v0 笔误 ProjectService 不存在) |
| `list_labels` | — | CatalogService.ListLabels(service_catalog.go:132)——update 要带 labels,**值域必须有发现工具** |
| `list_statuses` | — | status 目录读面——**status_key 每区可配,AI 不能靠猜** |

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
| `list_my_claims` | — | 指派给我 AND 待处理集 AND NOT(queued/running 代理任务 OR 24h 内终态代理任务且其后无用户评论)——**查询归属**:AssistantTaskService 先解析 proxy 行,work 域 ListQuery 新增谓词参数 `ExcludeAgentActiveTasks(agentID)` 注入(iron law 不拦跨域 SQL,先例 work repo.go:1171 join agent_task_queue;AgentID 由 orchestration 侧解析传入) |
| `claim_work_item` | work_item_id | **单事务+行锁**:防重+InsertTask+CAS 置 running(§4.2) |
| `report_progress` | task_id, content | **纯进度消息**(AppendTaskMessage)——不再承担心跳职能(v4 删活性检测);AI 随做随报,节奏自由 |
| `complete_work_item` | task_id, result | 结算(公共尾部:outcome 落库)+ 按规则推进工单(§4.2) |
| `fail_work_item` | task_id, reason | 终态,无重试(§4.2) |

**恢复(1)**
| 工具 | 入参 | 语义 |
|---|---|---|
| `get_my_running_tasks` | — | 我的代理行下 running 任务(id/工单/摘要/开始时间)——**AI(重)启动后的活动上下文入口**,也是用户问"那单怎么样了"的状态面(v4 升格为核心查询,替代被删除的活性检测) |

### 3.3 权限模型

- 工单操作面 = member 权限(REST 面 PUT 工单无 admin 闸,MCP 端点经 workspaceMW 自带成员闸,"与网页同权"在业务操作面成立)。
- **PAT 暴露面显式裁决(v0)**:AI 客户端持有 PAT = **账号完整权限**(含 workspace/成员/webhook/自动化管理面)。v0 接受,页面文案明示风险;PAT scope 收敛列 backlog。
- 评论与本人发言 v0 不区分(backlog:via=assistant 元数据)。

---

## 四、核心机制:AssistantTaskService(claim-on-behalf)

### 4.1 代理智能体行(proxy agent)

- 每个开启过接单的用户,每工作区一个代理行:`runtime_kind='assistant_proxy'`,幂等键 = (workspace_id, owner_user_id),唯一性必须用**部分唯一索引**(迁移 1046):`CREATE UNIQUE INDEX uq_agent_assistant_proxy ON agent (workspace_id, owner_user_id) WHERE runtime_kind='assistant_proxy'`——**不可加普通 UNIQUE**:所有 API 建的普通 agent 默认写 owner_user_id(routes.go:273 ownerFallback),一 owner 多 agent 是常态,普通唯一索引会让用户建第二个 agent 即崩(二轮 N1,P0)。
- **名字不落库**(name 存固定标识如 `assistant-proxy-<userID 前 8 位>`,防改名漂移);展示层动态拼"张三的 AI"。
- 惰性创建:claim 首次触发,`INSERT ON CONFLICT DO NOTHING` + 按 key SELECT 拿确定性行 id;**四处同步改**:AgentService.Create 的 switch(service.go:67)、agent 表 DB CHECK(**unnamed 自动名 agent_runtime_kind_check**,DROP CONSTRAINT+ADD CHECK IN ('engine','external','assistant_proxy'),W1 扩/W3 收敛各一次)、dispatcher、**触发豁免修正**(service_trigger.go:141 本人评论豁免按 owner 判断,proxy 行 owner=user 会让用户评论被误豁免——豁免条件补 RuntimeKind=='engine')。
- 隐藏:GET /api/agents 过滤 assistant_proxy(现有查询无过滤——repo.go:89;**@mention 候选同步排除**,comment-thread.tsx:50 吃同一接口);不可指派、引擎不执行(§4.3)。

### 4.2 接单循环

**claim_work_item——单事务 + 行锁互斥**(二轮 R1/R2/N3:纯事务内 NOT EXISTS 在 READ COMMITTED 下两事务互不可见双双通过;InsertTask 硬编码 queued(repo.go:319),不存在"直插 running"):
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
      domain 常量集同步加 SrcAssistantClaim——rules.go:81 无此值,归因消费端行为需单测)
COMMIT   冲突 → 409
```
(与 v3 的差异:**删第 4/5 步**——不写 runtime_id、不建心跳行。无活性检测后二者无消费方;agent_runtime 表完全不碰。)

**complete_work_item——结算 + 推进工单**(二轮 N2:**"同 SettlementService 行为"是不存在的先例**——Settle(service.go:819-861)全文零处写 work_item.status,引擎模式下工单状态是引擎 LLM 自己调 update_work_item_status 工具(tools_work.go:22)推进的。自动推进是**本设计全新行为**,规则如下):
1. 结算走**公共尾部**(从 Settle 抽 outcome 段 service.go:838-849——依赖仅 outcomes 端口+WorkItemID+事件,可抽;chat 追写段 external/proxy 无上下文,分段参数化;ExternalTaskService 同步接线,顺带修复 external 从不落 outcome 的存量缺陷);
2. **推进规则**(有人值守强度——克制):查该工作区 status 定义,推到 **category='done' 且 position 最小的 status_key**;**当前已属终态类(done/closed)→ 只结算不推进**(不越权覆盖用户手动结果);推进走 UpdateStatusByAgent 通道(service_workbench.go:32);**revision 冲突直接放弃推进**(不重试——人就在旁边,结算已成功,时间线留痕"推进因并发编辑跳过");
3. **resetIdleWorkItem 裁决**(v3 漏项,v4 显式):代理任务的 TaskCompleted **不触发** resetIdleWorkItem(main.go:108 订阅里按 originator_source='assistant_claim' 分流跳过)——它不是引擎闲置;若只结算不推进(工单留在 in_progress),reset 的 status guard 会把工单打回 todo,这不是代理语义想要的。

**fail_work_item——终态无重试**:max_attempts=1 已在 claim 时强制,FailRun 的重试分支天然不会触发(attempt≥max)——**v3 的"重试短路"专项改造取消**,max_attempts=1 一个参数即达成(做减法)。
**fail 后再求助通路**:list_my_claims 排除 = "有 queued/running 代理任务 OR 24h 内有终态代理任务(**且其后用户未再评论**)";claim 防重 = 只挡 queued/running。fail 后 24h 内工单暂列不出但 claim 照样可进——**用户在该工单发一条评论即重置排除**(主通路),口述工单 ID 直接 claim(兜底)。

**多客户端**:同用户多客户端共享 proxy 行与身份;claim 互斥由行锁保证(先到先得,后到 409);对同一 running 任务并发 complete——guard(WHERE status='running')先到先得,后到 409,可接受。

### 4.3 stalled:僵尸窗口期的唯一平台动作(替代判死/告警/重试三套)

- **机制**:sweeper 增一条轻量定时 SQL——`UPDATE agent_task_queue SET status='stalled' WHERE runtime_kind 维度判定(agent_id 为 assistant_proxy 行)AND status='running' AND started_at < now()-4h`(纯超时,不关心 AI 活性:活着但 4h 未完成同样该交还);同时**给用户发一条通知**("AI 处理《X》超时挂起,工单已回到你的待办")并把工单状态**转回待处理**(若曾被 AI 推进到 in_progress)。
- **stalled 是新终态**(不 failed、不重试、不进失败税):语义="平台把没人管的事交还给人"。任务行留档(消息都在,继续时可作上下文)。
- **恢复语义**:用户回来说"那单继续"→ AI 走 get_my_running_tasks(含最近 stalled)→ 再 claim(新任务行,上下文从旧任务消息捡);或用户在工单评论一句让 AI 重新接手(评论重置通路)。
- **页面可见性**(W2):工单详情/任务面板对 stalled 代理任务显示"AI 处理超时挂起"徽标;超 4h 未 stalled 的 running 任务显示挂起倒计时(纯展示,由前端按 started_at 算,无后端参与)。
- **为什么不做判死/告警/重试**(v3→v4 删除理由存档):判死(150s/30min 分窗+心跳行)防"静默失控",但有人模式下人是活性信号,窗口期由 stalled 交还兜底,活性检测无服务对象;滞留告警(60min)防"队列无人盯",同理由;重试防"工人挂了没人重派",助手场景重试决策权在人("再试一次"一句话)。保留的硬机制只有互斥(防脏数据)与 max_attempts=1(防垃圾)。

### 4.4 编排边界(三项基建改造,W1)

| # | 改造 | 现状(审计证据) | 改法 |
|---|---|---|---|
| 1 | **dispatcher 白名单** | 黑名单只跳 external(dispatcher.go:141),proxy 会被引擎抢跑废 | `if agent.RuntimeKind != "engine" { continue }` |
| 2 | **结算尾部公共层** | outcome 只在 SettlementService.Settle 写(service.go:838),external/proxy 均不落 | 抽公共函数,external/proxy 接线 |
| 3 | **枚举+stalled 迁移** | runtime_kind CHECK 是 unnamed 约束(1032);task status CHECK 不含 stalled(1003:80) | 一个迁移做三件事:DROP/ADD runtime_kind CHECK 扩 assistant_proxy;task status CHECK 扩 stalled;部分唯一索引 uq_agent_assistant_proxy(§4.1) |

(v3 六项→v4 三项:心跳行机制、判死分窗、重启豁免+告警放开整体取消——无活性检测后,FailStaleRunning/FailRunningAtStartup/CountStaleExternalQueued 对 assistant_proxy 的行为是**天然无关**(无心跳行→判死 SQL 会命中?**否**:claim 不写 runtime_id,FailStaleRunning 的 `t.runtime_id IS NULL` 分支会命中 150s 判死——**故第 3 项迁移后还须一个 5 行级修正:FailStaleRunning 与 FailRunningAtStartup 的豁免子句扩为 `runtime_kind IN ('external','assistant_proxy')`**(二轮 R6 重启豁免同款,此为删除心跳行后的替代防线,归入改造 3 的代码面)。)

---

## 五、/access 页重写(纯助理)

```
① 三步接入:1. 建令牌 2. 贴配置(端点=/api/mcp/w/{ws}/me) 3. 对 AI 说"帮我把 X 标完成"
   + 接单一句话:把工单指派给自己,对 AI 说"盯着我被指派的工单"
② 工具清单(14 个,真实注册面)
③ 访问凭证 = PAT 管理(现有段成为主角;文案明示"AI 客户端持令牌=账号完整权限")
④ 一键接入:Claude Code CLI 命令 / Codex / 通用客户端
```

工人接入段整体删除。client-kit/AGENTS.md 增补助理版接单循环(轮询间隔 ≥30s 约定,配合限流;**无心跳约定**——v4 已删)。

---

## 六、工人模型退役(两刀)

**第一刀(本波):产品面消失 + API 闸门**
- /access 工人段删除;**CreateAgent API 拒绝 runtime_kind='external'**(service.go:67 switch 收紧;前端表单本无该字段);runtime-onboarding.md 归档。
- 端点与表冻结保留:mountAgentMCP 全部、agent_credential 表、ExternalTaskService。

**第二刀(独立清理波):物理删除**
- 删 mountAgentMCP(含 runtime-credentials 管理端点)/ ExternalTaskService(结算公共层抽出后)/ sweeper external 分支。
- 迁移:DROP agent_credential;runtime_kind 枚举收敛为 **engine | assistant_proxy**(external 删除)。
- **依赖面**:agentBoundPAT 认证链(wiring_identity.go:122,删表必须同步拆包装);guard_runtime.go 的 /api/mcp/w 路径分支;GetAgentIDByPATID。

---

## 七、实施切分

| 波 | 内容 |
|---|---|
| W1(协议纵切,独立可验证) | **三项基建改造(§4.4)**+ /api/mcp/w/{ws}/me + 14 工具 + AssistantTaskService(claim 行锁事务/结算/无重试/stalled sweeper)+ 枚举四处扩(switch/CHECK/dispatcher/触发豁免)+ 限流接线(复用 WebhookRateLimiter) |
| W2(产品面) | /access 页重写 + "张三(AI 代)"渲染三处前端 + @mention 候选排除 + GET /api/agents 列表过滤 + stalled 徽标可见性 + AGENTS.md 助理版 + 第一刀(API 拒 external) |
| W3 | 第二刀物理删除 + CHECK 收敛迁移 |

**W1 验证口径**:①对话链:AI 建单/改状态/评论,作者=用户,评论不触发派发;②claim 互斥:双客户端并发 claim 同工单只成一单(409×1,行锁生效);③接单链:claim(任务 running)→progress(消息落时间线)→complete(**outcome 落库+按规则推进工单;已终态工单只结算;并发编辑时推进跳过留痕**)→list_my_claims 不再列出;④僵尸链:claim 后不 complete→**4h sweeper 置 stalled+通知用户+工单回待处理**→AI 再 claim 新任务行;⑤引擎隔离:dispatcher 不碰 proxy 任务;**150s liveness 不杀 proxy 任务**(豁免子句生效);⑥重启链:FailRunningAtStartup 不杀在跑代理任务(同豁免);⑦限流:超频 429;⑧fail 不排重试子任务;⑨fail 后用户评论→工单重回 list_my_claims;⑩AI 重启→get_my_running_tasks 拿回上下文。

---

## 八、已否决方案(存档)

- **α 工单级接单**:零后端改动,但 AI 代跑是平台黑箱——否决。
- **任务表加 user 列**:双态任务表,下游 SQL 全分叉——否决(proxy 锚点)。
- **工人模型页面降位**:整体消失比降位干净。
- **(v0)零改造复用编排**:首轮审计证伪。
- **workspace 走工具参数**:LLM 幻觉 ws id+无发现能力——改 URL path。
- **(v3)心跳行+判死分窗+滞留告警+重试短路**:继承自无人值守工人假设——有人值守下无服务对象(v4 删,理由与替代见 §4.3 存档段)。
