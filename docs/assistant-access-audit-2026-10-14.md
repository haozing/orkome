# 助理接入方案审计报告(2026-10-14)

> 审计对象:docs/assistant-access-design-2026-10-14.md v0
> 方法:三方独立审计——①事实审计(方案论断逐条对代码实证,10 组论断)②设计自洽审计(并发/生命周期/安全/语义错配,10 维)③主线复核(outcome 链/liveness SQL/dispatcher 亲自读码)。三方发现互证后合并去重。
> **总裁决:方案的核心结构(proxy agent 归因锚点)成立,但"编排基础设施零改造天然生效"的总论断被证伪——四根支柱(dispatcher 天然跳过 / outcome 沉淀 / liveness 天然生效 / 时间线自然渲染)全部与代码事实相反,其中 3 处 P0 不修则接单链完全不可用。v0 文档作废,按本报告 §三 修订。**

---

## 一、致命缺陷(P0,不修接单链不可用)

### P0-1 dispatcher 是黑名单,proxy 任务会被引擎抢走跑废
- **方案论断(§4.3)**:"dispatcher 对 assistant_proxy 永不派发(runtime_kind 白名单里没有它)"。
- **代码事实**:`dispatcher.go:141` 是 `if agent.RuntimeKind == "external" { continue }` ——**黑名单跳过 external**,不是白名单只跑 engine;`ListAllAgents`(repo.go:106)无 runtime_kind 过理。
- **后果**:新 kind `assistant_proxy` 会被 dispatcher 当 engine 认领 → `Engine.Execute`(proxy agent 无模型配置)→ fail 链。用户 AI claim 建的任务在 **500ms 轮询间隔内**就被引擎抢走跑废,后续 guard 全部 409。
- **修法**:dispatcher 改白名单 `if agent.RuntimeKind != "engine" { continue }`。

### P0-2 report_progress 不是心跳,代理任务 150s 必死
- **方案论断(§4.2)**:"report_progress 心跳 150s 内至少一次,liveness 判死自动兜底"。
- **代码事实**:liveness 活信号只有一个——任务带 `runtime_id` 且 `agent_runtime.last_seen_at` 在窗内(repo.go:633-641);`AppendTaskMessage`(repo.go:660)**只插消息,不写 runtime_id 不碰任何时间戳**。代理任务 runtime_id 恒 NULL → `t.runtime_id IS NULL` 直接命中 stale → 建出即倒计时 150s 判死,刷 progress 也没用。external 能活全靠 SQL 里 `runtime_kind='external'` 豁免子句(repo.go:640-641),新 kind 不匹配字符串。
- **后果**:方案的"失控感知"(β 方案的核心卖点)机制根本不存在;且判死后 FailRun 对 Retryable 原因(runtime_recovery 在白名单)会**排一个无人认领的重试子任务**(见 P0-3)。
- **修法**:重定义代理任务活信号——report_progress/claim 事务内写 runtime 心跳行(复用 agent_runtime 表挂虚拟 instance),或 FailStaleRunning 把"最近 task_message 时间"纳入活信号;二者选一,前者更贴现有结构。

### P0-3 outcome 沉淀链断裂,complete 后工单产出为空
- **方案论断(§3.2)**:"complete_work_item 走既有 settle 链:outcome 沉淀"。
- **代码事实**:`ExternalTaskService.Complete`(service_external.go:113-132)只做 AppendTaskMessage + CompleteRun + 发事件 + PromoteDeferred,**全函数无一处 outcome 写入**;outcome 唯一写入点在 `SettlementService.Settle`(service.go:838-849,`s.outcomes.WriteOutcome`),由 dispatcher 引擎链触发——proxy 任务不经 dispatcher,永远不会进 Settle。
- **后果**:验证口径②"complete→outcome 落工单"直接失败;汇报 tab 读 outcome/agent 评论,用户 AI 干完活在工单上无正式产出。
- **修法**:ExternalTaskService 结算路径(或新的 AssistantTaskService)挂 OutcomeWriter——把"结算尾部(outcome 写入+summary)"从 SettlementService 抽成可复用函数,external/proxy 结算调用同一份。**W1 必做**,不是第二刀前的备忘。

### P0-4 判死重试对代理任务是客户端可触发的垃圾排队
- **代码事实**:FailRun(repo.go:418-454)对 Retryable 失败税白名单(failuretax.go:24-27)**自动 INSERT queued 重试子任务**挂同一 agent;InsertTask 默认 max_attempts=2。代理任务的"重试子任务"没有任何执行者(用户 AI 不知道它的存在;list_my_claims 列的是工单),滞留告警 CountStaleExternalQueued(repo.go:628)硬编码 `runtime_kind='external'`,连告警都不覆盖。
- **修法**:代理任务强制 max_attempts=1;FailRun/liveness 判死按 runtime_kind 短路重试分支;滞留告警 SQL 放开 kind。

## 二、严重缺陷(P1)

### P1-1 claim 无互斥,双客户端并发 claim 同一工单会建出两个 running 任务
唯一索引 `uq_one_pending_task_per_work_item_agent`(1003 迁移:106-109)只 fence `(queued,dispatched)`,**不含 running**;ClaimExternalTask 的 CAS(`WHERE status='queued'`)只适用"认领既有任务",claim-on-behalf 是"创建",无对等机制。修法:claim 单事务"校验工单无 active 代理任务 + InsertTask + 置 running",冲突 409;唯一索引谓词扩到 running 或事务内仲裁。

### P1-2 complete 后工单状态无人推进 → list_my_claims 谓词再次满足 → 死循环重 claim
CompleteRun/FailRun 只 UPDATE 任务表,不碰 work_item.status;工单留在待处理集+无进行中任务 → 轮询 AI 重新 claim 同一工单,无限循环。修法:complete_work_item 语义定为"结算+自动推进工单状态到完成态"(AI 不再手动 update);fail 则任务终态+工单留待处理但"曾结算过"进排除谓词。**这是接单循环状态机闭环的关键缺口。**

### P1-3 重启误杀:FailRunningAtStartup 只豁免 external
repo.go:507-518 注释明说 external 豁免理由是"client-side execution, survives server restarts"——proxy 同为客户端执行,不豁免则平台每次重启杀光在跑的代理任务。修法:豁免条件改 runtime_kind IN ('external','assistant_proxy')。

### P1-4 runtime_kind 双重校验(代码 switch + DB CHECK)会拒绝 assistant_proxy
`AgentService.Create` switch(service.go:67-73)只认 engine|external;**agent 表还有 DB CHECK 约束**(migrations/1032:1-2)。惰性建 proxy 行两处都要改(本波)。方案 §6"第二刀删 runtime_kind 概念"与本波扩枚举自相矛盾,应改为"收敛为 engine|assistant_proxy,external 删除"。

### P1-5 workspace 携带方式自造第三种模式 + 工具集无 ws 发现能力
全平台惯例是 X-Workspace-Id 头,MCP 先例是 URL path(/api/mcp/w/{ws}/agent);方案让每个工具收 workspace_id 参数——LLM 幻觉 ws id 直接错区,且 12 个工具全要 ws 却没有 list_workspaces 工具,多区用户的 AI 无从得知自己有哪些区。修法:端点改 `/api/mcp/w/{ws}/me`(复用 workspaceMW 成员闸先例)。

### P1-6 PAT 全权 + 零限流 + 评论无 AI 标记
①PAT scope 字段存在但全仓无消费点,"与网页同权"实际暴露 workspace/成员/webhook/自动化全管理面;②中间件链无 rate limiter,轮询频率纯靠客户端自觉;③add_comment 记 author_type=user 与本人评论完全同形,不可区分、不可审计。修法:v0 决断——方案 §3.3 显式列暴露面;若接受全权,PAT 段警示文案同步改;限流至少给 /api/mcp/me 挂一个粗闸;评论加 via=assistant 元数据(P2 可后置但要在方案里裁决)。

## 三、中低缺陷(P2)

| # | 问题 | 事实 | 修法 |
|---|---|---|---|
| P2-1 | ExternalTaskService.Claim 拒绝非 external agent | service_external.go:82 显式校验 | claim_work_item 走新逻辑(AssistantTaskService),不复用 Claim |
| P2-2 | EnqueueAssignment 有模型配置闸 | service.go:596-604 非 external 校验 ValidConfig → proxy 会 ModelNotConfigured | 建任务绕过 Enqueue 层直插 InsertTask(handlers_taskview.go:296 先例) |
| P2-3 | 前端三处渲染不吃 agent 名 | 汇报 tab(agent-reports-tab.tsx:43)显示 ID 前缀;时间线(timeline-list.tsx:38)硬编码 'Agent';/api/agents 无 kind 过滤 → proxy 行会进智能体列表和 @mention 候选 | "张三(AI 代)"渲染是一组前端待办;列表过滤+@mention 排除是必做 |
| P2-4 | get_my_context 无现成数据面 | my-work 页纯前端拼装(my-work.tsx:107-131) | 工具内三次查询自拼或砍掉该工具 |
| P2-5 | ProjectService.List 命名错 | 实为 CatalogService.ListProjects(service_catalog.go:192) | 更正 |
| P2-6 | WorkItemService.Update 要 expectedRevision 乐观锁 | service_workitem.go:168 | MCP 工具约定:先读拿 revision 再写 |
| P2-7 | CommentService.Create 触发 triggerEngine.OnComment | service_comment.go:203 | AI 以用户身份评论可能意外触发 agent 派发——需裁决(跳过 trigger 或接受) |
| P2-8 | 工具数对不上(标题 12,labels 又说可并入) | — | 定稿:labels 并入 update,共 11 个(4 读+4 写+4 任务协议-1) |
| P2-9 | §6"从建 agent 表单移除 external"是空操作 | 前端表单(create-agent-dialog.tsx)从来没有 runtime_kind 字段 | 第一刀改口径:CreateAgent API 拒绝 external |
| P2-10 | awaiting_input 协议缺口 | awaiting 是引擎 HITL 机制,助理工具集无"请求输入" | v0 文档化"评论+心跳保活"等待模式;request_input 工具列 backlog |
| P2-11 | 第二刀依赖面补充 | agent_credential 挂在 PAT 认证链(agentBoundPAT,wiring_identity.go:122);runtimeAgentWriteGuard 有 /api/mcp/w 路径分支 | §6 清单补录 |

## 四、成立的部分(审计确认的真复用)

- **PAT 过 CSRF 豁免**(csrf_session.go:68 `Bearer ork_` 前缀放行)——/api/mcp/me 无 session 无 CSRF 可通,先例即 /api/mcp/w。✅
- **member 权限口径**:REST 面 PUT 工单(含改状态)确无 admin 闸,member 即可——"与网页同权"在工单操作面成立(MCP 工具层要自带 memberCheck,是少量新代码不是零)。✅
- **agent_task_queue 表零变更**:DDL 层成立(agent_id NOT NULL / context 默认 '{}' / status CHECK 都兼容代理任务直插)。✅
- **proxy agent 归因锚点结构**:相比"任务表加 user 列"的双态方案,单锚点结构依然正确,审计未推翻。✅

---

## 五、方案 v1 修订要点(去替换 v0 的对应章节)

1. **§4.1 proxy 行**:幂等键定为 (workspace_id, owner_user_id)(agent 表该列已有);名字**不落库**,展示层动态拼"张三的 AI"(防改名漂移);建行+建任务的 runtime_kind 校验(代码 switch+DB CHECK+迁移)本波一并改。
2. **§4.2 接单循环**:claim=单事务(防重+插任务+置 running);complete=结算+**自动推进工单状态**+outcome 写入(结算尾部抽公共层,external/proxy 共用);fail=终态+不排重试(max_attempts=1);活信号=report/claim 写 runtime 心跳行;list_my_claims 谓词排除"有进行中任务 OR 曾结算完成"的工单。
3. **§4.3 编排边界**:dispatcher 改 engine 白名单;FailRunningAtStartup 豁免 assistant_proxy;滞留告警放开 kind;Sweeper 150s 判死对代理任务生效(有真心跳后)。
4. **§3.1 端点**:改 `/api/mcp/w/{ws}/me`(URL path 携带 ws,复用 workspaceMW)。
5. **§3.2 工具**:定稿 11 个;update 工具内置"读-拿 revision-写";add_comment 裁决 trigger 副作用;get_my_context 改自拼或砍。
6. **§3.3 权限**:显式列 PAT 暴露面,v0 接受全权+文案警示(收敛列 backlog);/api/mcp/me 挂粗限流。
7. **§6 退役**:第一刀口径改"CreateAgent API 拒绝 external";第二刀枚举收敛为 engine|assistant_proxy;依赖面补 agentBoundPAT/guard_runtime。
8. **§7 切分**:W1 增加"结算尾部公共层抽取 + dispatcher 白名单 + liveness 心跳行 + 重启豁免"四项基础设施改造——工作量比 v0 估的大,但这正是"β 的可观测性"真实成本。


---

# 二轮审计(v2 → v3,2026-10-14)

对 v2 的新论断与修法做第二轮双审计(事实核 N1-N10 / 可实施性 R1-R12,合并去重 19 项)。**总评:v2 对首轮 21 项修复方向全对;心跳行方案结构成立(agent_runtime 主键/UPSERT 形状/判死 SQL 兼容/无清理 worker 均实证);但三组新修法自身不成立——claim 互斥(READ COMMITTED 下事务不互斥+唯一索引不存在)、150s 判死窗口(对 AI 客户端不可执行)、工单推进(先例不存在)——3 P0。已全部并入 v3。**

## P0(3)
1. **N1 幂等索引炸普通 agent**:所有 API 建 agent 默认写 owner(routes.go:273),普通 UNIQUE(workspace_id,owner_user_id) 让一 owner 多 agent 即崩——改部分唯一索引 WHERE runtime_kind='assistant_proxy'(迁移 1046)。
2. **R1/R2 claim 不互斥**:纯事务内 NOT EXISTS 在 READ COMMITTED 双双通过;须 FOR UPDATE 工单行 + 部分唯一索引 + ON CONFLICT DO NOTHING。
3. **N2/R6 推进先例不存在**:Settle 全文不写工单 status(service.go:819-861),引擎推进靠 LLM 调 update_work_item_status 工具;自动推进是全新行为,须定义完成态规则(category='done' 最小 position/终态只结算/revision 冲突降级)。

## P1(6)
R3 判死分窗(150s 统一阈值→engine 150s/proxy 30min,第五项基建);R5 恢复缺口(get_my_running_tasks 工具);N3 直插 running 不可行(InsertTask 硬编码 queued,CAS 置 running 组合拳);N4 心跳行 engine NOT NULL(伪码必须带 engine='assistant',禁 HeartbeatRuntime);R7 fail 再求助断层(评论重置排除谓词);N8 SrcAssistantClaim domain 常量。

## P2/P3(10)
N5 心跳行 DELETE 无先例(新逻辑);N6 限流已有 WebhookRateLimiter 可复用("首个限流面"论断错误);N7 CHECK unnamed 约束 DDL 写法;N9 proxy owner 干扰触发豁免(service_trigger.go:141);N10 list_my_claims 查询归属;R4 心跳节奏约定入 AGENTS.md;R9 补 list_labels/list_statuses 发现工具;R8 工具计数 14;R11 W1 拆分(页面/@mention 后移 W2);R12 验证口径补 4 条(总 11 条)。
