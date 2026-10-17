# orkome × RunLynk × ZCode 三方联调摩擦分析

日期：2026-10-17
方法：主会话沉淀事实时间线，4 个分析 agent 并行作业（各领一个视角、可读仓库源码取证），主会话核实关键断言后汇总。
范围：仅复盘 2026-10-17 当天的真实联调过程（ZCode 接入 orkome → orkome 接入 runlynk → 经 orkome 派任务调通浏览器能力 → 修复 4 缺陷），不做泛化产品评审。

**证据约定**：`file:line` 均已在仓库当前 HEAD（8c0bbc5）核实；「时间线」指 §1 的事实回顾。当天已修复的 4 项不在摩擦清单里重复立案，只在时间线标注。

---

## 1. 联调时间线（事实层）

1. **ZCode 接入 orkome**：读全站统一接入文档 `GET /api/mcp/skill.md`（一次读懂）→ 用户对话里补发 PAT → `GET /api/workspaces` 发现两个同名空工作区（xzrw/xzrws，相隔 12 秒），无法自辨，停下问用户（选了 xzrws）→ 查 ZCode 自身配置文档确认 MCP HTTP 型格式 → 写 `~/.zcode/cli/config.json` → curl 完整握手验证 23 工具 → **配置需重启会话才生效**，本会话全程用 REST + curl 手搓 MCP 协议代替工具调用。
2. **寻找 runlynk 的浏览器 MCP**：用户一句口头指引（无文档）→ ZCode 开内置浏览器登录 runlynk（Playwright click 两次超时，降级 evaluate 原生 click）→ 能力目录页 12 个 ruyi-* 能力但**无端点** → 端点在「调用方」页（/mcp + Bearer 说明藏在「代码示例」弹层）→ 想签发专用凭证被「确认密码 + TOTP」挡死（AI 无验证器）→ 代裁复用 ZCode config.json 里残留的 cloud-mcp-browser 完整令牌。
3. **orkome 注册 runlynk MCP**：接入契约靠 grep 源码获得（wire 形状/三态凭证信封/transport 缺省值）→ REST 注册一次成功 → orkome 服务端探针 probe-tools 实连拉回 18 工具，端到端一次通过。
4. **E2E 派任务**：工作区全空，冷启动五步全靠读源码 → 从 dev 库解密 DeepSeek key 复用（sealx AES-256-GCM）→ 建凭证 → 建 agent（`visibility:"workspace"` 裸 500，去字段才成）→ 助理 MCP 建工单（发现 assignee 只支持 'me'）→ REST PUT 指派（先试 PATCH 吃 405）→ **PUT 指派隐式触发自动派单**（trigger=assignment）→ agent 50 秒完成任务：tool_search 发现 ruyi 工具 → 读 ruyi 文档 → env-list/browser-status → 发现 connected:false 与 alive:true 矛盾后主动读「踩坑记录」按第 3 条裁决 → list-pages + 只读 evaluate 端到端验证 → post_comment → 置 done → outcome 沉淀完整巡检报告。
5. **QA run 500**：在途任务时点 Quick Action run，`EnqueueQuickAction` 走 (nil, reason, nil) 分支、接线闭包裸解引用 panic → 500 误导排查。
6. **修复波次**（5 commit，`6cbcffc..8c0bbc5`）：QA run nil→409（3c6762b）、visibility 枚举闸+测试（45d4fc9）、me-MCP assignee_agent_id（f68b58a）、/access i18n 同步（4fd4c76）、deploy README 迁移纠错（8c0bbc5）。
7. **遗留**：生产库迁移 1047 pending（deploy README 原文错写「server 启动时自动执行迁移」误导所致），需在服务器手动 `cmd/migrate up`。

---

## 2. 视角一：ZCode 作为助理接入 orkome

### 总体评价

「接入即一份 skill.md」的核心契约是成立的：文档一次读懂、工具目录从注册面生成零漂移、发现指引「多个就问用户」被 AI 正确执行，本次联调没有死在契约本身。真正的摩擦集中在契约的四条边上：没有接入后验证方法、客户端示例矩阵不齐且不提示「配置需重启」、PAT 以全权无 scope 的形态人肉传递、以及一条文档-UI 漂移。代价最高的两个停顿点（等用户仲裁工作区、全程 curl 手搓 MCP 协议）一个源于发现接口缺少区分元数据，一个源于文档没把已经存在的 HTTP 降级路径写成明文指引——后者改三行字就能消掉。

### 摩擦点清单

**A1. skill.md 无「接入后验证」步骤，AI 只能自行发明验证方式** —— 严重度：中
- 现象：ZCode 写完配置后自己用 curl 做完整握手（initialize → tools/list = 23 工具）才敢确认接入成功（时间线 1）。
- 根因：文档五段结构（关于/接入配置/工具目录/工作方式/注意事项）没有「配置后如何确认成功、失败时如何定位」。证据：`cmd/orkome-server/mcp_me_skilldoc.go:142-145`（注意事项仅 3 条）。
- 建议：「接入配置」末尾加「验证」小节：可粘贴的 curl initialize 示例、成功标志 = `tools/list` 返回全部工具、两个常见失败（401=令牌错/过期，403=非该工作区成员，见 `cmd/orkome-server/wiring_mcp_me.go:110-122,205-224` 两道闸）。工具数从 `s.ListTools()` 动态取，避免写死 23 造成新漂移。

**A2. 客户端配置示例矩阵不齐，且不警示「schema 严格」** —— 严重度：中
- 现象：skill.md 只给 Claude Code/Codex/通用 HTTP 三种示例；ZCode 这类 config.json `mcp.servers` 客户端靠自身知识补齐，而该 schema 严格、**未知键会静默丢弃整个服务器条目**，写错无任何报错（时间线 1）。
- 根因：通用段（`mcp_me_skilldoc.go:90-93`）是协议描述不是可粘贴配置；未提示各客户端容错性差异。
- 建议：注意事项加一句「部分客户端配置 schema 严格校验（未知字段可能导致整个条目被静默忽略），写入后务必按上文方法验证」。一行字，直接针对静默失效风险。

**A3. 文档-UI 漂移：skill.md 教 AI 找用户复制「带完整端点的链接」，该元素已不在页面上** —— 严重度：中
- 现象：universal 版 skill.md 的 workspaceId 发现方式之二是「让用户打开助理接入页复制带完整端点的链接」；但该页改版后只渲染全站统一 skill.md 地址（含 `{workspaceId}` 占位符）。按文档走的 AI 在这条路径会卡死并反问用户一个答不上来的问题。
- 根因：2026-10-16 改为全站统一文档后页面删掉了 per-workspace 端点展示，生成器 fallback 没同步。证据：`cmd/orkome-server/mcp_me_skilldoc.go:80` 对照 `web/src/routes/_app.access.tsx:98-131`；前端有现成的 `useWorkspaceStore((s) => s.currentId)`（`web/src/routes/_app.tsx:128`）可渲染完整端点。
- 建议：access 页补渲染「本工作区完整端点」Snippet（方案 a，同时是 A4 的解药）；或删掉 skill.md 里这条 fallback（方案 b）。推荐 a。

**A4. 同名工作区歧义：发现接口无区分元数据** —— 严重度：中低
- 现象：两个同名空工作区，AI 无法判断，只能停下问用户（时间线 1）。
- 根因：文档引导本身正常（`mcp_me_skilldoc.go:77-79`「多个就问用户」，AI 照做）；问题在 discovery 响应只有 id/slug/name/plan_code/created_at/role（`vendor/github.com/haozing/ploykit/workspace/app/model.go:5-16`），对全空同名工作区无辨认信号。
- 建议：短期 skill.md 补半句「向用户列出 slug 与创建时间帮助辨认」；产品化解法 = A3 方案 a（用户在网页看着当前工作区复制端点，天然无歧义）；可选给 GET /api/workspaces 加弱区分字段（工单数/成员数）。

**A5. PAT 全权、无 scope、默认永不过期，且设计上明示人肉传递** —— 严重度：**高**
- 现象：用户把 40 字符明文 PAT 直接贴进对话；令牌等同账号完整权限（删工作区/改成员/配 webhook），无 scope 细分，TTL 默认「永不」。
- 根因：三处叠加——(a) HTML 预览页 banner 明示「把本页地址与你创建的令牌发给你的 AI」（`mcp_me_skilldoc.go:214`），文案只提示风险不给替代；(b) scope 机制已实现但被硬编码弃用：`internal/identity/adapters/http/handlers.go:112` 调 `CreatePAT(ctx, p, name, ttl, nil)`，而 `webx.CredentialScope` 的 `AllowsWorkspace/AllowsPermission` 都已实现（`vendor/.../platform/webx/principal.go:24-62`），设计文档自认 scope 收敛在 backlog（`docs/assistant-access-design-2026-10-14.md:107`）；(c) TTL 选项首项即默认「永不过期」（`web/src/components/settings/tokens-section.tsx:26-31`）。
- 建议（按性价比）：① scope 落地是纯接线活——PAT 表单加「限定工作区」勾选（默认当前工作区），`handlers.go:112` 的 `nil` 换成解析请求，认证链已支持；② 助理场景默认 TTL 改 30 天；③ access 页加「按客户端复制完整配置片段」按钮（端点预填、令牌留占位），让令牌以受控格式而非自由文本进入对话。

**A6. 「配置即重启」无预期管理，但降级路径已存在却没写** —— 严重度：中
- 现象：配置写入后当前会话拿不到工具，ZCode 整场用 REST + curl 手搓 MCP 协议（时间线 1）。
- 根因：MCP 客户端会话启动时才连接，是客户端固有事；文档既没预告，也没指出 skill.md 的「通用 MCP 客户端」段（`mcp_me_skilldoc.go:90-93`）恰好就是可用的临时调用路径——本次手搓成功（initialize 取 `mcp-session-id` 头 → tools/call）证明该路径完全可行。
- 建议：注意事项加两句：「多数客户端只在启动时连接 MCP 服务器，写入配置后需重启会话；等待期间可按『通用 MCP 客户端』段用 HTTP 直接调用」。服务端零代码改动。

### 不值得修的（生态固有限制）

- **客户端重启才加载 MCP**：加载模型属客户端生态，orkome 无从改变；可做的只有文档管理预期 + 降级路径（A6），那是文档改进而非修此摩擦本身。
- **令牌人肉传递这一形态**：MCP 生态当前没有跨客户端标准化的令牌注入/OAuth 动态注册方案，「用户把令牌交给 AI」就是现阶段主流。orkome 已有「明文仅显示一次 + 可撤销 + 前缀展示」的合理缓解（`tokens-section.tsx:104-124,178`）；彻底消除需 OAuth callback 级重基建，性价比不成立。该修的是权限上限（A5 的 scope），不是传递方式。
- **不维护 ZCode 等长尾客户端专属配置段**：客户端矩阵无界，逐家维护必然腐烂；「通用协议段 + AI 自带配置知识」本次已验证走通。加 A2 的一句警示即可。
- **workspaceId 走 URL path 换工作区需改配置**：v4 显式设计裁决（`docs/assistant-access-design-2026-10-14.md:43,66`），换来端点自包含、文档可静态生成。改 header 只是换一种配错方式，不减少歧义总量。
- **允许同名工作区**：同名是用户自由；要修的是区分手段（A4），不是禁止重名。

---

## 3. 视角二：寻找并接入 RunLynk 云端浏览器 MCP

### 补充取证（分析期新获得的事实）

- `https://runlynk.qidu.site/.well-known/mcp`、`/.well-known/mcp.json`、`/.well-known/ai-plugin.json`、`/.well-known/openid-configuration` 全部 404。
- `/openapi.json` 与 `/docs` 返回 200 但内容是 589 字节的 SPA index.html 外壳——是 SPA fallback，不是文档。
- `/mcp` 对 GET 返回 405 + `Allow: POST`，协议行为规范；响应头带 `X-Runlynk-Build-Version: prod-20261017-2077030`，可观测性不错。
- ZCode 侧桥接脚本（`D:\code\agentduokai\mcp-cloud-stdio.mjs`）自带：手动 session 复用与 404 重建（L28-34）、task-augmented 回退轮询（L62-87）、最多三层 JSON 信封拆包（L48-60）。
- 节点侧运行手册记录：签发凭证「需登录 + 密码确认；发机器钥匙另需 aal2/MFA」；MCP 调用 scope「需含 `nodes:invoke` + `capabilities:read`」，且早先云端为 taskToolOnly，同步 tools/call 被 -32601 拒。
- 既有 onboard 脚本里甚至内嵌 Python 按 base32 种子现算 TOTP——能跑通的唯一原因是**人手里有 TOTP 种子**。

### 总体评价

运行时接入这一段体验确实顺：`/mcp` 无状态、免 session-id、Bearer 直连、`tools/list` 一次回全 18 个工具。但本次联调的真实成本几乎全部花在**接入前的发现与凭证阶段**：平台把发现入口只做在控制台 UI 里（端点与能力目录还分居两页），没有任何机器可读的发现协议（实测 /.well-known 全 404、/openapi.json 是假壳），凭证生命周期（只显示一次 + 密码确认 + TOTP）与「AI 代操作」存在结构性冲突。最终接入能走通，靠的是两个**偶然产物**——ZCode config.json 里恰好残留的完整 rag_ 令牌、节点侧人工沉淀的运行手册——而不是平台设计的自助路径。这个平台对「已经拿到钥匙的 AI」很好用，对「来找房门的 AI」几乎闭门。

### 摩擦点清单

**B1. 能力发现入口分裂：能力目录页没有端点，端点藏在「调用方」页** —— 严重度：**高**
- 现象：目录页 12 张能力卡（名称/描述/异步性/来源/授权按钮）不含任何调用方式；MCP 端点、HTTP 端点、代码示例全在「调用方」页上半段。从「我能用什么」到「我怎么用」要跨一次页面跳转和一次视角转换。AI 按语义最短路径先到目录页，恰好是死胡同（时间线 2）。
- 根因：信息架构按**控制台角色**（节点提供者看目录、调用方看接入）切分，不按「消费一个能力需要什么」组织。
- 建议：每张能力卡加「如何调用」区（端点 + 认证头 + 最小 scope + 示例），或放带上下文参数的深链跳到调用方页；目录页顶部放全局接入摘要条（/mcp + /api/v1 + Bearer 说明）。

**B2. 无任何机器可读发现协议，文档面只有 UI** —— 严重度：**高**
- 现象：/.well-known/* 全 404；/openapi.json、/docs 是 SPA 假壳；Bearer 认证方式只在「代码示例」弹层里。AI 唯一合法路径是开浏览器逐页走查 UI，把人读界面翻译成接入知识。
- 根因：REST API 本身完整可编程（onboard 脚本用纯 curl 跑通全流程），但这份能力**没有公开的机器可读描述**——API-first 路径存在却不可被发现。
- 建议：成本最低的三件事——(a) `/.well-known/` 挂一份静态接入清单（端点、认证、token 类型、scope 语义，纯 markdown）；(b) `/api/v1` 输出真实 OpenAPI；(c) 「代码示例」弹层内容做成可直链的公网路由，让 AI 能 curl 而不必开浏览器。
- **勘误（2026-10-17 源码取证后）**：上面「全 404」是探错了路径——实测的是 /.well-known/mcp / mcp.json / ai-plugin.json / openid-configuration 四个**从未实现**的路径。RunLynk 实际已有两个活着的发现端点：`GET /.well-known/agent`（生产实测 200，返回 MCP 面自述 JSON：endpoint/transport/session_mode/auth.token_kinds）和 `GET /.well-known/oauth-protected-resource`（RFC 9728 形状）。另有 `GET /` 根自述文档已在库（cmd/runlynk-api/discovery.go，e1427f7，生产构建 07:03 早于该提交尚未部署）。真正的缺口修正为：**发现端点存在但不可发现**（没有任何入口指向它们——目录页/调用方页/文档都不提）、缺 LLM 叙事型接入文档、缺对外 serve 的 openapi.json。B2 的严重度降为**中**，B1（入口分裂）保持高。

**B3. 凭证生命周期与「AI 代操作」结构性冲突** —— 严重度：**高**
- 现象：已签发凭证只显前缀；新签发 = 类型/名称/scope → 创建 → **确认密码** → **TOTP 6 位**。AI 既无验证器也无密码的合法持有语境，流程必然卡死。本次靠 config.json 残留旧令牌续命（时间线 2）。
- 根因：安全模型假设操作者永远是「带着占有因子的人」。缺少**委托/服务账户**路径：没有「人一次性 TOTP 批准 → 令牌带外交付给指定 agent」，也没有「已认证调用方在 scope 内自助铸造受限子密钥」。runbook 现算 TOTP 恰好反证：平台不缺 API，缺的是不需要人肉输入验证码的**授权委托**语义。另有 free 计划每项目 1 把 rla_ 的配额约束，进一步放大每次签发的仪式成本。
- 建议：「机器签发」流——人完成一次 TOTP 批准后平台生成令牌并经带外通道（剪贴板/下载/webhook）交付；后续续签/吊销/子密钥铸造允许持有 `credentials:mint` scope 的既有调用方走 API。

**B4. scope 模型不自明：三层权限关系无解释** —— 严重度：中
- 现象：签发弹层可见六个 scope（tasks:create/read/update/dispatch/retry + capabilities:read），但按运行手册实测 MCP 调用实际需要 `nodes:invoke` + `capabilities:read`——**观察到的多选列表里甚至没有 nodes:invoke**。能力卡的「授权给调用方」与凭证 scope 是两层还是一层，UI 全程未说。即使当时不被 TOTP 卡死，照 UI 勾出来的 scope 也未必能调通。
- 根因：权限实际有三族（tasks:*、capabilities:read、nodes:invoke）外加能力级 ACL，分散在两个界面，没有「调用一个能力需要哪些权限」的合成视图。
- 建议：签发弹层提供「MCP 全量调用」预设勾选；每个 scope 加一句话 tooltip；能力卡标注「调用本能力需要 scope：…」；**401/403 响应体回带缺失的 scope 名**——这是自助排障最便宜的杠杆。

**B5. UI 可自动化性缺陷放大走查成本** —— 严重度：中
- 现象：登录按钮在 DOM 里、Playwright click 两次挂起超时，必须降级 evaluate 原生 .click()；scope 多选连续点选时 React 重渲染丢已选项（时间线 2）。
- 根因：前者疑似透明遮罩/合成事件拦截且缺 data-testid；后者是受控组件每次选中后重建选项列表、未累积已选状态的典型实现缺陷。
- 建议：关键交互补 `data-testid` + 无障碍命名；修复多选状态累积。但优先级判断要清醒：**真正的修法是做完 B2**——有机器可读文档后 AI 根本不需要走 UI。

**B6. 协议形态中途漂移（taskToolOnly → 混合注册），客户端靠报错考古** —— 严重度：低（已修复）
- 现象：节点工具曾注册为 task-augmented 且服务端 taskToolOnly，同步 tools/call 被 -32601 拒，桥接脚本被迫实现 120 秒轮询；2026-10-17 平台改混合注册后回退同步优先。
- 建议：tools/list 注解显式带 TaskSupport；「代码示例」同时给同步与 task-augmented 两种调用序列。（-32601 文案点名了不支持同步执行，报错质量合格。）

**B7. 返回信封形状不定，客户端写三层拆包兜底** —— 严重度：低
- 现象：工具返回可能是 JSON 字符串、`content[0].text`、`result` 字符串的任意嵌套，桥接脚本用最多三轮 unwrap 暴力解包（`mcp-cloud-stdio.mjs:48-60`）。rag_/rla_ 两族令牌的用途边界（谁能打 /mcp、谁能打 /api/v1）只在弹层可推知。
- 建议：工具返回统一 structuredContent + text 双通道；错误响应提示「本端点期望 rag_ 令牌」之类的 token 类型反馈。

### 不值得修的（生态固有限制）

- **令牌只显示一次**：正确的安全实践，不该改。要修的是 B3 的委托签发路径——把「看不了旧令牌」变成「能低成本铸造新令牌」。
- **admin 开 MFA、敏感操作要 TOTP**：同样正确，AI 本就不该持有人的验证器。任何「为 AI 降低 MFA 强度」都是开倒车；正解是委托架构，不是削弱认证。
- **ZCode 不支持 MCP task augmentation**：客户端生态缺口，不是 RunLynk 的错；桥接 stdio 是当下正确的过渡形态，客户端跟上即可删。
- **SPA 控制台对无视觉自动化天然脆弱**：逐点修 testid 只是减损不是根治。设计结论：**凡是期望被 AI 接入的东西，就不该只存在于 UI 里**（归入 B2 修）。
- **目标网站自身漂移**（如 example.com 2026 版无 `<h1>`）：被操作对象的固有变化，验收断言应先探测 DOM 再取值。

---

## 4. 视角三：经 orkome 派任务调云端浏览器能力

### 总体评价

本次联调证明 orkome 的**执行链**已达到「AI 可自主完成」的水位：agent 靠注入的工具快照与技能文档完成工具发现、矛盾核查和端到端验证，过程与产出全程留痕。但**发单链（人/AI 侧）还是「工程师 API」而非「产品」**：冷启动五步没有任何引导与合并，指派即派单是纯隐式行为且响应里毫无痕迹，多个派单入口共享同一个防重栅栏却没有统一的冲突语义。当天的三个生产故障（visibility 裸 500、assignee 只支持 me、QA run 假 500）全部集中在发单链，执行链零事故——下一个迭代的主战场很明确：把派单侧做到和执行侧同等的 AI 可用度。

### 摩擦点清单

**C1. 冷启动序列无产品化，且最关键一步的失败是「静默的」** —— 严重度：**高**
- 现象：全新工作区跑通「建凭证→建 agent→建工单→指派→派发」五步全靠读源码摸 wire 契约，没有向导/清单/就绪探针。
- 根因：各步骤装配彼此独立（`internal/orchestration/adapters/http/routes.go:134-138` 凭证、`:160-166` agent、`internal/work/adapters/http/routes.go:104-105` 工单），无组合态视图；更糟的是**派单失败原因被整组丢弃**——`cmd/orkome-server/main.go:114` 对 `EnqueueAssignment` 是 `_, _, _ =` 全弃，而它在 agent 未配模型时返回 `(nil, ModelNotConfigured, nil)`（`internal/orchestration/app/service.go:622-630`）。
- 后果推演：如果本次 runtime_config 填错，PUT 指派返回 200、工单显示已指派，但永远没有任务产生、没有任何日志/通知/时间线——AI 只能陷入「为什么没动静」的无线索排查。
- 建议：① 新增 `GET /api/setup-status`（凭证数/可用 agent 数/未完成派单原因汇总）；② 派单 skip 决策（blocked/model_not_configured）落工单 timeline 或通知（通知域已有先例 `main.go:335-342` autopilot 熔断）；③ /access 页补「工作区冷启动五步」可抄序列。

**C2. 「指派即派单」隐式，且结构上必然与 QA run 相撞；任何字段更新都会重派** —— 严重度：**高**
- 现象：PUT 指派后事件链自动创建任务，但 PUT 响应只回工单对象、无 task_id、无「已派发」信号（`internal/work/adapters/http/handlers.go:115`）。AI 不知道派发已发生，随后点 QA run 撞上在途任务（时间线 4/5）。
- 根因一：**结构必然**——QA run 的目标 agent 就是从工单指派人解析的（`cmd/orkome-server/wiring_conversation.go:47-56`），指派链与 QA 链永远指向同一个 (work_item, agent) 对，`CountPendingFence` 按该对计数（`service.go:583,631`），两个入口抢同一工单时第二个**必然**吃栅栏。
- 根因二（主会话已亲自核实）：`main.go:107-114` 监听 `WorkItemUpdated` 且只看**最终态** assignee——**任何字段更新**（改标题/改状态）都会对已指派 agent 的工单触发 `EnqueueAssignment`，在无活跃任务时静默重派 agent 干活。`WorkItemService.Update` 任何字段更新都发该事件（`internal/work/app/service_workitem.go:271-283`），而 `AssigneeChanged` 判定字段（`service_workitem.go:466`）现成却未被使用。
- 建议：① 派单显式化：`POST /api/work-items/{id}/dispatch {agent_id}` 返回 task，PUT 指派退化为纯归属变更（或至少 PUT 响应附带 `dispatched_task_id`/`dispatch_skipped_reason`——数据在 `EnqueueAssignment` 返回值里现成）；② 监听器改用 `AssigneeChanged` 过滤；③ 在 agent 清单/工单详情与 skill 文档明示「指派=派发」语义。

**C3. 500 吞掉真实原因，把 AI 逼成侦探** —— 严重度：高（修复前）/ 中（修复后残留）
- 现象：QA run 返回 500，实际任务早已由指派链创建并跑完、工单已 done。AI 只能靠「查任务列表发现已完成」还原真相（时间线 5）。
- 根因：`EnqueueQuickAction` 未入队时按设计返回 `(nil, reason, nil)`（「决策不是故障」，`service.go:583-585`），旧闭包裸解引用 panic，被 Recover 压成无细节 500（`internal/conversation/adapters/http/routes.go:61-63` 非 apierr 一律 "internal error"）。已修：`wiring_conversation.go` 落 `ActiveDuplicate` → 409（commit 3c6762b）。
- 残留：409 响应仍不含在途任务的 task_id——AI 想确认「谁在跑」还得再发一轮 `GET /api/work-items/{id}/active-task`（端点存在，`internal/orchestration/adapters/http/handlers_taskview.go:51,187-198`）。
- 建议：① 框架级约束：「领域决策结果禁止 (nil, reason, nil) 形状出服务层」——要么类型化联合（`Queued{Task}|Skipped{Reason}`），要么强制调用方处理；② 409 body 携带 `active_task_id` 与 `reason`（AlreadyActive 时顺手查出活跃任务塞进 details），AI 一次响应即可完成「冲突→确认在跑→改为等结果」的决策闭环。

**C4. REST 契约对 AI 不友好：动词不一致、信封漂移、405 无提示** —— 严重度：中
- 现象：① 更新工单只有 `PUT`（`internal/work/adapters/http/routes.go:120`），AI 凭常识试 PATCH 吃 405——Go 1.22 mux 的 405 是纯文本、无 JSON 错误体、不提示正确方法；而同一家族里 quick-actions 更新用 PATCH（conversation `routes.go:127`）、statuses 用 PATCH、properties 用 PATCH——**同一系统三种更新动词**。② 信封形状漂移：工单详情裸对象、任务详情包 `{"task":...}`、工单列表键 `items`、任务列表键 `tasks`、QA run 回 `{"task_id"}`、凭证/agent 创建又是裸对象——每换一个资源重新学一次解包规则。③ `{"items":null}` 空列表（本次两工作区都返回 null 而非 []）。
- 建议：① work-items 补 PATCH 别名（一行路由）或 mux 外层挂方法不符拦截器回 JSON 405 + `allowed:["PUT"]`；② /access 页发一份 REST 契约速查（skilldoc 已有参数速记通道 `mcp_me_skilldoc.go:155,161`，同一通道可承载）；③ 长期统一「单资源裸对象 + 列表 `{<复数>: [...]}`」约定，存量渐进。
- 备注：`{"items":null}` 已在 ploykit 0.7.0 修为 `[]`（历史记录），生产未升级前仍会出现。

**C5. 助理 MCP 面与 REST 面能力不对齐** —— 严重度：中
- 现象：建单工具原只支持 `assignee='me'`（已修，f68b58a）；修复后差集仍在：助理面**没有任何 agent 枚举/创建工具**（23 工具里无 `list_agents`/`create_agent`），`assignee_agent_id` 要求 AI 凭空知道 agent ID；`update_work_item` 不支持改派，改派仍必须走 REST；quick-actions 无助理工具。本次联调被迫全程绕 REST + 手搓 MCP 协议。
- 建议：① 补 `list_agents`（复用 `orchapp.AgentService.List`，顺手暴露 runtime 就绪态，可同时缓解 C1）；② `update_work_item` 增加 `assignee_agent_id`（`WorkItemPatch` 现成字段）；③ 原则：每条高频 REST 写路径都要回答「AI 助理拿 PAT 调它时，工具面有没有等价物」，差集记账到 /access 工具清单。

**C6. 非 ASCII 负载的客户端坑（Windows curl GBK）** —— 严重度：低
- 现象：中文标题/描述/prompt 经 curl 内联提交会被 Windows Git Bash 按 GBK 转码写坏库，必须 UTF-8 文件 + `--data-binary`（时间线 4）。
- 根因：平台侧 UTF-8 全链正确（见下），纯客户端环境坑；但 /access 文档面向的正是最容易踩的 AI/脚本用户群。
- 建议：/access 文档加一行「非 ASCII 负载请写 UTF-8 文件 --data-binary，勿内联」。

### 已经被救回来的（平台设计正确从而没酿成问题）

- **工作区级 MCP 自动注入 + 工具自发现**：agent 零配置拿到 12 个 ruyi-* 工具。注入链靠调度期 resolved 快照 + 引擎期 `patchToolSnapshot` 回写权威态（`internal/orchestration/adapters/engine/registry_assembly.go:95-129`），两级 MCP 合并生效（`internal/orchestration/app/service.go:518-536`）。任务 context 里 `mcp_servers:[{name:runlynk,status:ok,tool_count:18}]` 即实证。没有这套设计，「给 agent 配工具」会再吃一个冷启动摩擦。
- **技能/文档面注入让 agent 会「查手册」**：agent 发现 `connected:false` 与 `alive:true` 矛盾时主动读「踩坑记录」按第 3 条裁决继续验证——「把运维知识做成可检索技能」直接换来自主排障能力。
- **过程留痕 + outcome 沉淀**：26 条消息可回放、outcome 自动沉淀。发单侧的误导（假 500）之所以没变成事故，正因为执行侧把每一步都存了证。
- **防重栅栏保证恰好一次执行**：两个入口抢同一工单的最坏后果止步于一次报错，而非 agent 被派两次。
- **错误分层的既有积累**：writeErr 已有 409 映射表，仓库注释早已把「三无 500」定性为根因（`internal/work/adapters/http/routes.go:36-40`）——当天三个修复是沿既有正确方向补齐，不是方向性返工。

---

## 5. 视角四：orkome 作为 MCP 消费平台接入外部 MCP

### 总体评价

这次接入 runlynk「一次注册、一次探针通过」，靠的是 orkome 三条扎实的底座：**探针与运行期同参同链**、**凭证 sealx 密封 fail-closed**、**工作区级注册自动对全员生效**——运行链路本身值得信任。摩擦集中在运行链路的两侧：接入前，注册契约（三态凭证信封、transport 枚举、headers 信封形状）只活在源码注释里，对 AI 安装器/CLI 消费方没有可机读入口；接入后，外部工具面的变更没有任何主动感知通道，手动探针是唯一窗口。另有 `credential_id` 幽灵关联和 32 工具静默截断两个埋在数据模型里的坑，本次没踩上纯属工具数（18）和用法（bearer_token）恰好躲开。

### 摩擦点清单

**D1. 出站接入契约零文档化，AI 安装器只能考古源码** —— 严重度：**高**
- 现象：注册 runlynk 所需的全部语义（wire 形状、三态凭证、Bearer 信封）靠 grep 两个 Go 文件获得。全仓无 OpenAPI/Swagger；`docs/` 下相关内容是内部审计文档不是消费方契约。skilldoc 是**入站**接入文档，「集成配置」组只有一行参数速记（`mcp_me_skilldoc.go:174-175`），完全没有「帮用户把第三方 MCP 注册进 orkome」的工作指引。
- 根因：契约权威定义放在 handler 注释里（`internal/orchestration/adapters/http/handlers_mcp.go:7-15`、`adapters/pgrepo/repo_mcp.go:6-22`），测试矩阵钉死了行为但没有翻译成对外文档。
- 建议：① skilldoc「集成配置」组补出站接入指引：注册后必须 probe-tools 验证、`has_credentials:true` 只表示信封非空不表示凭证有效、PUT 更新时留空保留/`null` 清空；② 产出一份最小 openapi.yaml 覆盖 mcp-servers 五个端点——对 AI 安装器，200 行 yaml 比读 400 行 Go 源码便宜一个数量级。

**D2. 注册时无连通性验证（域名级除外），配置错误暴露时点靠后且分散** —— 严重度：**高**
- 现象：Create/Update 仅校验 name/url 非空 + egress ValidateURL（`internal/orchestration/app/service.go:369-384,386-403`）。ValidateURL 含 DNS 解析（`vendor/.../egressx/egressx.go:130-134`），所以「域名打错」注册时就会 400；但**路径错、令牌错、对端不是 MCP 端点**都 201 落库，要到手动 probe 或任务执行才暴露（失败只写 task.context 快照，`adapters/engine/tools_mcp.go:157-170`）。两处不对称：① probe 端点只有 ws 级（`adapters/http/routes.go:196`），**agent 级覆盖条目没有 probe**（`routes.go:168-171` 只有 CRUD），agent 级凭证错误只能在任务里炸；② 助理面有 `create_mcp_server` 工具但**没有 probe 工具**——纯走 MCP 的 AI 安装器注册完无法自证有效。
- 根因：探针被设计成「注册后按 id 的独立动作」（`service.go:417-445`），没有「先试连再落库」的干跑形态。
- 建议：① `create_mcp_server`/REST Create 支持 `probe=true`：落库前用入参凭证实连一次，失败返回结构化 warning（允许强行保留）；② 补 `POST /mcp-servers:probe` 干跑端点（收 URL+credentials 不落库）；③ agent 级补 probe-tools 路由（引擎侧 `ProbeServerTools` 现成）。

**D3. credential_id 幽灵关联：一行描述文字对抗一个数据模型陷阱** —— 严重度：**高**
- 现象：`create_credential` 描述承诺返回 id 可被 `create_mcp_server` "reference for audit linkage"（`wiring_mcp_me.go:941`）；`create_mcp_server` 把 credential_id 原样写进信封（`wiring_mcp_me.go:988-991`）不校验存在性；而运行期取鉴权头的 `mcpHeadersOf` **只读 `creds["headers"]`**（`tools_mcp.go:399-411`），信封里的 `credential_id` 键被静默忽略。只传 credential_id 的注册会 `has_credentials:true` 落库成功，然后每次任务/探针对远端 401，错误形态与「令牌填错」完全无法区分。
- 根因：历史教训（MCP 鉴权链不解引用 credential_id）唯一防线是工具描述里的一句话（`wiring_mcp_me.go:962`）——知识被**文档化**了但没被**结构化**。
- 建议：① 收到 credential_id 时校验存在性（credSvc.Get），不存在 400；② 注册响应加 `auth_effective: bool`（headers 信封非空 = true），让 `has_credentials` 不再是唯一且误导的信号；③ 更彻底：credential_id-only 注册直接拒绝。

**D4. 外部工具面变更零感知** —— 严重度：中高
- 现象：runlynk 侧工具增减，orkome 感知通道只有两条且全被动：手动 probe（前端每次展开实连，`web/src/components/packs-sections.tsx:121-143`）；每任务执行写 task.context 快照——只落单个任务上下文，无工作区级健康视图、无事件、无告警。另有 5 分钟 idle TTL 的连接/工具缓存（`tools_mcp.go:24-34,83-92`）：runlynk 新增工具最晚 5 分钟才对任务可见；缓存键包含完整 header 值（`tools_mcp.go:43-60`）。本次「18 个工具」是当场对上的，不是受监控状态。
- 建议：① 最小闭环：定时（如每小时）对全部 ws 级服务器 probe，`tool_count`/工具名集合变化时 emit 事件——orkome 自己的 webhook 订阅通道可复用；② probe 结果落库（最近实测时间/工具数/状态），列表页展示「最后验证于」。

**D5. 每服务器 32 工具上限静默截断** —— 严重度：中
- 现象：`mcpToolsPerServerMax = 32`（`tools_mcp.go:26`），装满即 break（`tools_mcp.go:176-178`），状态仍标 `ok`、无截断 note；probe 同参截断（`tools_mcp.go:255-257`）。重复工具名有 note（"N duplicate tool names dropped"，`tools_mcp.go:214-216`），截断反而没有。runlynk 若从 18 扩到 40，orkome 所有 agent 静默丢后 8 个，UI 探针显示「32 个工具」看起来一切正常。
- 建议：截断时 `entry.Note = "truncated at 32/N tools"`，probe 响应带 `total` 字段——一行改动把静默丢失变成可见信号。

**D6. transport 值域三层零校验 + 三处入口枚举互相矛盾** —— 严重度：中低
- 现象：service/repo/DB 均不校验 transport（repo 兜底 `orDefault(m.Transport,"http")`，`repo_mcp.go:83`）；引擎实际枚举 `""|"http"|"streamable_http"|"sse"`、其余报错（`tools_mcp.go:275-292`）；UI 下拉只有 `http|sse`（`packs-sections.tsx:119`）；`create_mcp_server` 描述写 `http | sse | streamable_http`（`wiring_mcp_me.go:965`）。拼错的值 201 落库，到任务执行才报 `unsupported mcp transport`。`docs/audit-fp09-mcp-page-2026-10-12.md:50,204` 的 P2-1 早已记录，至今未修。
- 建议：service 层加枚举校验（一行 switch）；统一三处值域命名（"http" 实际按 Streamable HTTP 处理，名字与语义有漂移，UI/文档应显式标注）。

**D7. ws 级注册写面仅成员门，与同页其他集成写面权限不一致** —— 严重度：中低
- 现象：ws 级 mcp-servers 的 POST/PUT/DELETE/probe 全走 member 门（`routes.go:192-216` 仅查成员资格）；而同页 credentials/ui-entries 写面是 admin。**任何 member 可注册对全体 agent 生效的服务器**，携带任意 URL 和任意 header 凭证。audit-fp09:156 的 P2-5 已记录未收敛。
- 建议：ws 级注册写面升 admin，与 credentials 对齐；probe（只读+外呼）可保留成员门。

**D8. 凭证信封是自由 map，headers 无键名约束** —— 严重度：低中
- 现象：credentials 接受任意 JSON 对象（`handlers_mcp.go:171-186` 仅要求对象），`mcpHeadersOf` 把 `headers` 下所有字符串键值原样透传为出站 HTTP 头（`tools_mcp.go:399-411`）——键名无白名单、值无形状检查（Authorization 不带 Bearer 前缀照传、可塞 Cookie）。注册时对 header 键的合法性（如 CRLF 注入字符）无防线；写错信封形状的表现同样是远端 401，与令牌错不可区分。
- 建议：注册时校验 header 键名为合法 HTTP token、值不含控制字符；文档给出信封 schema（`{"headers":{...}}`），误写的其他键（如 `{"token":"..."}`）注册时 warning。

### 设计做对了的（本次顺畅的机制原因）

- **探针与运行期同参同链**：`ProbeServerTools` 与 `buildMCPTools` 共享建连、Initialize、ListTools 链路和全部参数（SSRF 门、10s 预算、32 上限、同一 `mcpHeadersOf`），代码注释明确这一意图（`tools_mcp.go:232-235`）。「probe 返回 18 个」不是孤例验证，而是对运行期行为的可靠预演——端到端一次通过正是这条设计在兑现。
- **凭证密封 fail-closed + 永不回显**：sealx AES-GCM 落库，sealer 未接线直接报错而非明文兜底；wire 响应凭证字段 `json:"-"` 只出 `has_credentials`（`app/ports.go:122-123`），有专门防泄漏测试（`handlers_mcp_test.go:242`）。令牌进库即不可逆、不可读。
- **工作区级自动生效模型**：一次注册、全员受益；agent 级同名遮蔽合并收敛在一个纯函数（`domain/rules.go:126-142`）。注册 runlynk 后无需触碰任何 agent——「注册即全效」是这次接入最省事的一步。
- **三态凭证语义实现自洽且有测试钉死**：`json.RawMessage` 区分「缺字段 vs 显式 null」规避指针三态塌缩（`handlers_mcp.go:76-95,166-186`），三层各有测试矩阵。语义没让本次踩坑——问题只在没写进文档，不在它不好用。
- **egress guard 三点布防**：注册时、任务运行时（`tools_mcp.go:142`）、探针时（`tools_mcp.go:240`）都过 `ValidateURL`；公网 https 直连零阻力，同时 SSRF 面被真正堵住——「注册任意 URL」的自由度是带闸门的。
- **`create_mcp_server` 的 bearer_token 抽象**：服务端把 bearer_token 映射成信封（`wiring_mcp_me.go:984-986`），AI 安装器不需要知道信封内部形状——抽象方向正确，本次若走 MCP 面注册本可少读一半源码；只是它只覆盖 Bearer 一种形态。

---

## 6. 汇总：跨视角主题与优先级

### 六个跨视角主题

1. **「凡是期望被 AI 接入的东西，就不该只存在于 UI 里」**（B1/B2/D1/A3/A6 同根）：runlynk 的 /.well-known 全 404 与 orkome 的出站契约零文档是同一个病——接入知识只活在人读的界面和源码注释里，AI 只能走查 UI 或考古源码，UI 的可自动化性缺陷（B5）又把成本放大数倍。本次接入成功靠的是残留令牌、人工 runbook、读源码三个「场外援助」。
2. **凭证治理与 AI 代操作的结构性冲突**（A5/B3 同根）：orkome 的 PAT 全权无 scope、runlynk 的 TOTP 签发死路，本质都是安全模型假设操作者永远是「带着占有因子的人」。本次双方各让一步（人肉传全权令牌 + 复用残留令牌）才接通——能用，但每一步都在放大信任面。
3. **发单链与执行链的成熟度差一个身位**（C1-C5）：执行侧（工具注入/文档查阅/留痕/outcome）零事故且有高光；发单侧三个故障全部集中于此。根源是派单入口的语义重叠（指派即派单 vs QA run vs 评论 @）+ 隐式行为无信号 + 决策结果 `(nil, reason, nil)` 的契约形状把「没派」变成「500」。
4. **`(nil, reason, nil)` 是一个框架级反模式**（C3/D2 同根）：「决策不是故障」的返回形状对内部调用方（`_, _, _ =`）合理，对 HTTP 边界和 AI 消费方是灾难——真实原因被压成无细节 500。建议框架级约束：决策联合类型出服务层，或强制调用方处理 nil 分支。
5. **静态注册 vs 漂移世界的感知缺口**（D4/D5/A3 同根）：注册之后，外部工具面会变、文档与 UI 会漂移、DB 约束会演进，而 orkome 侧没有「定时实测→diff→事件」回路，文档也没有「生成即校验」机制。18 个工具是「当场对上」，不是「持续对上」。
6. **REST 契约的 AI 友好度是新的 API 设计维度**（C4/C6/D6）：动词不一致、信封漂移、405 无提示、枚举不校验——这些对人类开发者是小烦扰（有 IDE/文档兜底），对 AI 是实打实的弯路。本仓库已经开始朝这个方向修（当天的 4 个修复全部属于此类）。

### 优先级清单

**P0（小改动、高杠杆，建议下一波就做）**
| 项 | 视角 | 说明 |
|---|---|---|
| 监听器按 `AssigneeChanged` 过滤 | C2 | `main.go:111` 改一行判定（字段现成于 `service_workitem.go:466`）——消除「改标题静默重派 agent」的隐式行为 |
| 409 响应携带 `active_task_id` + reason | C3 | AlreadyActive 时顺手查出活跃任务塞 details，AI 一次响应完成决策闭环 |
| skill.md 补三段话：验证方法 / 重启预期 + HTTP 降级路径 / schema 严格警示 | A1/A2/A6 | 纯文档，服务端零代码；工具数从 ListTools 动态取 |
| access 页补「本工作区完整端点」Snippet | A3/A4 | 用现成 `currentId` 渲染，同时解文档-UI 漂移和工作区歧义 |
| D5 截断可见化（Note + probe total） | D5 | 一行改动 |

**P1（结构性改进，值得排期）**
| 项 | 视角 | 说明 |
|---|---|---|
| PAT scope 落地（限定工作区勾选）+ 助理默认 TTL | A5 | 纯接线活，认证链已支持；接入面最大信任不对称的解药 |
| 助理面补 `list_agents` + `update_work_item` 支持改派 | C5 | 与 f68b58a 同思路的差集收口 |
| `create_mcp_server`/Create 支持 probe 干跑 + agent 级 probe 路由 | D2 | 先试连再落库；agent 级凭证错误不再只能任务里炸 |
| credential_id 校验 + `auth_effective` 字段 | D3 | 数据模型层堵住「必然失败的注册」 |
| transport 枚举校验 + 三处值域统一 | D6 | 一行 switch + 命名对齐 |
| 派单显式化：`POST /work-items/{id}/dispatch` 或 PUT 响应带派发结果 | C1/C2 | 指派与派发解耦，隐式行为变显式信号 |
| 出站接入文档（skilldoc 集成组指引 + 最小 openapi.yaml） | D1 | AI 安装器自助化 |

**P2（跨产品 / 较大改动）**
| 项 | 归属 | 说明 |
|---|---|---|
| 公网机器可读接入清单（/.well-known）+ 真实 OpenAPI + 示例可直链 | RunLynk | B2，成本最低收益最大的三件 |
| 能力卡内联「如何调用」+ scope 合成视图 + 403 回带缺失 scope | RunLynk | B1/B4 |
| 委托式机器签发（人 TOTP 批准一次 → 带外交付 → 后续 API 自助） | RunLynk | B3，凭证治理的正解 |

> **落地状态（RunLynk 侧，2026-10-17 晚）**：skill.md（`/skill.md`，公网 markdown，动态生成）已上线；
> rest invoke 已强制 `nodes:invoke` 并回带 `token_scope_hint` + `next_actions`；能力卡内联调用示例
> 未做（P1-b 挂账）；委托式机器签发已实现 RFC 8628 设备流（begin/approve/poll 三端点 + 000101/102
> 迁移），已知遗留：approved 行 poll 判 EXPIRED 的 expires_at 生命周期 bug 待查（`clean_device.go`）。
| 定时 probe 巡检 → 工具面变更事件（复用 webhook 通道） | orkome | D4 |
| `GET /api/setup-status` 冷启动就绪探针 + 派单 skip 落 timeline/通知 | orkome | C1 |
| ws 级 MCP 注册写面收敛 admin | orkome | D7（audit-fp09 P2-5 在案） |
| REST 契约速查 / PATCH 别名 / 信封约定统一 | orkome | C4（渐进） |

### 不值得修的（三方共识，生态固有限制）

- MCP 客户端「配置即重启」的加载模型——文档管理预期即可，orkome 已有可用的 HTTP 降级路径（A6）。
- 令牌「只显示一次」与「敏感操作 MFA」——都是正确实践；要修的是委托签发（B3），不是削弱认证（B 不值得修清单原话：「任何为 AI 降低 MFA 强度的方案都是开倒车」）。
- 令牌人肉传递的形态本身——MCP 生态现阶段的主流做法，orkome 已有合理缓解；该修的是权限上限（A5 scope）。
- 逐客户端维护专属配置段、禁止同名工作区、workspaceId 改 header——各有更便宜的正确解法（A2/A4/一.不值得修清单）。
- SPA 控制台的完美自动化——把 UI 修到完美不如把接入知识搬出 UI（B5 的真正修法是 B2）。

---

## 附：当天已修复项（不在摩擦清单重复立案）

| 缺陷 | 修复 | commit |
|---|---|---|
| QA run 在途冲突空指针 panic → 500 | 未入队时落 `ActiveDuplicate` → 409 | 3c6762b |
| agent 非法 visibility 穿透 DB CHECK 裸 500 | 服务层 `validateVisibility` + 回归测试 | 45d4fc9 |
| 助理 MCP create_work_item 无法指派 agent | `assignee_agent_id` 参数（GetAgent 校验，与 'me' 互斥）+ 文案同步 | f68b58a / 4fd4c76 |
| deploy README 错写「server 启动时自动执行迁移」 | 纠正为手动 `cmd/migrate up` 可抄命令 | 8c0bbc5 |

**仍需用户在生产服务器执行**：`cmd/migrate up` 补迁移 1047（readyz 报 `migrations pending: 1047`），随下次部署换上新 binary 即可一并带上当天修复。
