# orkome 功能点审计总报告（第一批 FP-01~FP-10）

> 日期：2026-10-12 · 方法：10 agent 并行，一 agent 一功能点，统一七步审计法（全链梳理→契约逐字段对齐→curl 实测成功+失败态→代码质量→后端正确性→测试覆盖盲区→P0/P1/P2 清单）
> 环境：后端 :8080（迁移至 1036）+ 前端 :5173 真实启动；测试账号 inventory-test@orkome.dev；所有实测缺陷均有 curl 复现记录
> 分册：`docs/audit-fp01..fp10-*.md`（10 份，含全链 file:line、契约矩阵、实测记录）· 前置：三维盘点 `docs/inventory-2026-10-12.md`

---

## 一、总账

| FP | 功能点 | P0 | P1 | P2 | 一句话结论 |
|---|---|---|---|---|---|
| 01 | 认证与会话 | 0 | 2 | 9 | 核心链扎实（26 项实测全通）；两个长会话"静默失效" P1 |
| 02 | 工作区与切换器 | 3 | 3 | 5 | fence 硬、生命周期两端断：进不去/出不来/切不动 |
| 03 | 工单创建与列表 | 1 | 3 | 10 | 后端全过，被 members envelope 一断点击穿 |
| 04 | 工单详情信息面 | 1 | 3 | 10 | 崩溃链全闭环；slot tab 四触点复核通过 |
| 05 | 任务面板与消息流 | 0 | 2 | 10 | 三卡与契约零漂移；分页盲区+WS 帧不截断 |
| 06 | 评论线程 | 2 | 4 | 8 | 写存读主干好；三重断裂压死（500/WS 死/IDOR） |
| 07 | Agents 管理 | 1 | 4 | 9 | 权限 tab 崩溃；3 个 500 类契约缺陷；两级 MCP 语义相反 |
| 08 | Squads 与 Skills | 0 | 4 | 11 | 重名双坏 + 死端点 + 越权写；"测试A生产B"样本集中营 |
| 09 | MCP 三段式页 | 1 | 4 | 7 | 两段好；PAT 双断点 + CSRF 前缀错配 + 第 3 段整体失真 |
| 10 | 通知与壳层 | 0 | 5 | 10 | 读面好；生成与到达环节集中全部硬伤 |
| **合计** | | **9**（归并为 **5 个根因**） | **34** | **85** | 另 P3×6（FP-05） |

## 二、P0 焦点：5 个根因，每个都到了"一行级"修复

### 根因 A：members envelope 契约错配（占 9 个 P0 中的 4 个）
后端 `GET /workspaces/{id}/members` 返回 `{items,total}`（ploykit 统一 envelope），前端 `web/src/api/identity.ts:71-74` 当裸数组上抛。波及全站 **7 处消费方**：工单详情整页崩溃（info-panel.tsx:28）、agent 权限 tab 崩溃致六 tab 不可达（_app.agents.$agentId.tsx:1063）、评论框输入 `@` 即 TypeError（comment-thread.tsx:42，tiptap mention items 回调）、另有 4 处静默故障（指派筛选条/指派下拉永不渲染、无任何报错）。
**修复**：`listMembers` 一处解包 `res.items ?? []`，全站治愈。**不要动后端**——envelope 是 ploykit 框架契约。

### 根因 B：工作区切换器渲染即崩
`workspace-switcher.tsx:23` `<Menu.GroupLabel>` 脱离 `<Menu.Group>` 上下文，Base UI 直接 throw，任意页面点击即崩进错误边界。
**修复**：外包一层 `<Menu.Group>`。

### 根因 C：onboarding 邀请码加入双重断链
前端打 `POST /api/share-links/join`（identity.ts:60），后端只有 `/api/share-links/redeem`（404 纯文本）；契约清单 routes.go:14 同错。且修好路由后 redeem 返回 Workspace（无 `workspace_id` 字段），onboarding.tsx:64 取 `member.workspace_id` → undefined → 多工作区用户加入后切错工作区。
**修复**：前端改调 `/redeem` + onSuccess 改用响应 `id` + 修契约清单，共 3 处小改。

### 根因 D：含无 leader 小队的工作区评论创建 100% 500（且评论已落库）
`pgrepo/repo.go:1054-1061` 把可空 `squad.leader_agent_id::text` 直接 Scan 进非空 string → pgx 报错 → 触发链全 500；`writeErr` 不打日志所以服务端无根因痕迹。评论已持久化，用户重试必产生重复评论+重复通知。无小队的工作区不受影响（所以测试没炸）。
**修复**：`coalesce(s.leader_agent_id::text,'')` 一处 + `writeErr` 补错误日志。

### 根因 E：PAT 创建 100% 失败（双重断点）
前端发 `ttl_seconds`（identity.ts:95）后端只收 `ttl_hours` → 400；修好字段后 201 响应是 `{pat:{...},token}` 嵌套，前端按扁平解析会让成功横幅渲染「令牌""已创建」。`ttl_hours` 成功路径已实测证实。
**修复**：identity.ts 两处（改字段 + 解包 `res.pat`）。

## 三、34 个 P1 按主题归组（跨功能点同因合并）

1. **实时链死亡（影响面最大）**：`wiring_ws.go` 从未设置 `hub.WorkspaceMember` → workspace scope 握手 auto-join 与显式 subscribe 全部 rejected → **comment:/work_item:/label: 帧谁都收不到**，多标签/多用户全是陈旧数据（FP-06）；叠加通知八种写入路径**零帧**（FP-10，铃铛只靠 60s 轮询）。只有 task scope 因走另一分支幸存——这正是"任务面板实时是好的"假象来源。修复：接线 WorkspaceMember + 补帧投递集成测试。
2. **CSRF 两处配置缝**（都在 main.go:308）：①gorilla/csrf 默认 MaxAge 12h < 会话 7d，长闲置后所有写请求 403 且前端无恢复（FP-01）；②`PATPrefix` 未设回落 `tk_`，orkome PAT 是 `ork_` → **所有 PAT 请求被系统级 403**，外部 MCP 客户端接入即使改对 URL 也走不通（FP-09，本次新发现）。修复：`csrf.MaxAge(7d)` + `PATPrefix:"ork_"` 两行。
3. **"500 + 数据已落库 → 重试即重复"族**：FP-06 评论（根因 D）、FP-10 评论触发派发错误直通 500（service_comment.go:219）、FP-07 agent 创建 nil map 撞 NOT NULL、非法 visibility/max_concurrent 撞 CHECK 23514 裸抛、MCP PUT 改名撞 23505——服务层缺 fence、错误不映射、多处不打日志。
4. **跨工作区越权写（IDOR）**：评论 Update/Delete/SetResolve 无 ws 比对（FP-06，实测他区改/删/解决全 2xx）；agent-skill 五个绑定方法收 ws 不用（FP-08）。读面 fence 是硬的，写面有遗漏。
5. **工作区删除必 500**：52 处 FK 零级联 + teardown 只取消任务（FP-02，实测 23503，工作区永久残留）。
6. **通知生成三连**：任务完成通知双发+三发（新 EventID 绕过去重，FP-10）；空串游标 hasNextPage 恒真点"加载更多"重复整页；偏好 UI 露出 0%。
7. **消息流分页盲区**：恒取最旧 200 条，长任务尾段（含最终输出）面板与 run-logs 均不可见，且摘要服务读最新 200——同数据两窗口矛盾（FP-05，实测 220 条复现）；WS interim 帧载荷不截断，MB 级结果整包广播。
8. **两级 MCP 语义相反**：agent 级 PUT 不带凭证=保留、ws 级=清空；重名 agent 级静默覆盖、ws 级 409（FP-07/09）。另发现 MCP 凭证绕过 sealx **明文落库**（FP-09）。
9. **其他**：ALLOW_SIGNUP=false 单独设置不生效（FP-01）；防重复 advisory lock 无事务即锁即放 no-op（FP-03）；时间线 updated 事件内容退化（payload 包 data 层读取方取顶层，FP-04）；小队重名预检查死代码 500 / 技能重名静默覆盖 / squad-evaluated nil recorder 恒 500（FP-08）；登录页无找回密码入口（FP-01，信息项）。

## 四、系统性模式（比单个缺陷更值得修的组织级问题）

1. **前端测试按错误契约 mock**：members mock 成裸数组、InfoPanel 被 stub、queryOptions 被整体 mock——三层全绕开真实 envelope，P0 全绿漏网的直接原因（FP-04）。修 P0 时必须同步用真实抓样锁 mock。
2. **"测试走 A 路径，生产走 B 路径"**：集成测试自带 recorder 掩盖生产 nil 接线（FP-08）；单测扁平 payload 掩护 payload 包层缝（FP-04）；fake repo 不经 pgx 扫描所以 NULL 不可见（FP-06）。
3. **契约测试只单向对账**：route-compare 有幽灵条目、无"挂了未声明"守卫（盘点已发现）；本批新增实证：`GET /api/work-items` 列表端点全仓零测试（FP-03）、认证链装配层零覆盖（FP-01）。
4. **错误处理三无**：非 apierr 一律 500、不打日志、不映射错误码——"invalid request body" 裸奔给用户、根因在服务端无痕（FP-03/06/07/09 共性）。
5. **死代码/死旋钮群**：squad 重名预检查死代码、workspace_server_id 死列、AUTH_TOKEN_TTL 死旋钮、quickCreateWorkItem 等 4 个无调用方封装——盘点即知，本批又添数处。

## 五、修复路线图（按收益/成本排序）

**第一波（约 6 处一行级修改，清掉全部 9 个 P0）**
1. `identity.ts` listMembers 解包 `.items ?? []` → 治 4 个 P0 + 2 个静默 P1
2. `workspace-switcher.tsx:23` 包 `<Menu.Group>`
3. `identity.ts` joinByShareCode 改 `/redeem` + onboarding 取 `res.id`
4. `pgrepo/repo.go:1054` coalesce leader_agent_id + writeErr 补日志
5. `identity.ts` PAT 改 `ttl_hours` + 解包 `res.pat`

**第二波（配置接线，P1 主体）**
6. `main.go` CSRF：MaxAge 7d + PATPrefix "ork_"（两行，解开 PAT 全线 403 与长会话死锁）
7. `wiring_ws.go` 接 hub.WorkspaceMember（workspace 实时链复活）
8. 工作区删除：teardown hook 按拓扑序清业务表
9. 评论 Update/Delete/SetResolve + agent-skill 绑定补 ws fence（IDOR）
10. 服务层错误映射（CHECK/FK → 400/409 + 日志），止住"500+已落库"族
11. 通知：去重 emit、空游标修正、新通知推帧、偏好 UI 露出

**第三波（结构性，随下批功能）**
12. 契约测试补双向对账 + 列表端点/auth 装配层覆盖
13. 前端 mock 全部改真实抓样锁定契约
14. 消息流分页改 desc 游标（~15 行）+ WS interim 截断
15. i18n 补 zh status.*/priority.* 11 键；MCP 凭证进 sealx；两级 MCP 语义统一

## 六、分册索引与证据留存

- 分册：audit-fp01-auth / fp02-workspace / fp03-workitem-create-list / fp04-workitem-detail / fp05-task-messages / fp06-comments / fp07-agents / fp08-squads-skills / fp09-mcp-page / fp10-notifications-shell（均 `docs/` 下 2026-10-12）
- 测试数据：各 agent 已清理 fp 前缀数据；遗留活体证据——fp02-ws-a（删不掉的工作区，P1 实证）、"盘点验证工单-20261012"（220 条消息分页盲区复现样本，FP-05 留作修复验证）
- 交叉修正记录：FP-09 修正 FP-07 两处结论（agent_mcp_server 实有死外键列；agent 级对话框其实有正确警示文案）；FP-02 将 members 波及面从 5 处扩至 7 处；api 盘点 §13.6 的 ui-tokens body 字段（pack vs plugin）为文档漂移
