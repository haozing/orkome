# 0013 · ploykit 底座切换（rebase）

日期：2026-10-11 ｜ 状态：Accepted ｜ 执行：分支 `rebase-ploykit`

## 背景

orkome 自带的 18 包平台层与身份/工作区/webhooks/通知实现，与蒸馏自本项目的框架 ploykit
（`github.com/haozing/ploykit`）高度同源。本 ADR 记录把 orkome 从"自带全套底座"切换为
**消费 ploykit 的产品仓库**的裁定与结果；过程与裁定细节见
`docs/ploykit-rebase-plan-2026-10-06.md`（含 v2/v3 基线修订与逐阶段执行记录）。

## 裁定（正文裁定 D1–D7 的最终态）

| # | 裁定 |
|---|---|
| D1 | 迁移基线重建（无存量数据负担）：框架迁移 001–044 打底，产品迁移重编号 1001+，双队列执行 |
| D2 | 工作区解析以 ADR 0005 为准（缺头 400 / 非成员 403）；框架 v0.4.0 已天然满足 |
| D3 | chi → 标准库 ServeMux（框架 Mount 形状决定）；全部域完成 |
| D4 | 纯线上 module 消费（`go get` + vendor，无 replace）；v0.3.0 tag 因复用作废，基线为 v0.4.0 |
| D5 | contract 词汇包全本地（框架的 contract 在 internal/ 不可 import；wswire 例外：v0.4.0 已公开为 platform/wswire，已采用） |
| D6 | 会话语义升级：无状态 JWT → 框架 DB 可吊销会话（cookie 滑动 7d/绝对 30d） |
| D7 | 事件底座三包（eventbus/outboxx/flagsx）保留本地（schedx 经分支终审发现零使用者，已删除）；框架已转 River（platform/events + schedule 域），orkome 不迁——迁移列为后续独立项目 |

## 结果

- platform 18 包 → 保留三包（eventbus/outboxx/flagsx；schedx 零使用者已删；其余全删）；身份/工作区/webhooks/通知/实时层全部换框架；
- 保留产品域：七业务域零改动 + identity 瘦身为产品扩展域（pins/ui-tokens/cli-token）+ notification 投影层（mention/静音组）；
- 白赚：authz（27 产品权限点已注册）、audit、quota usage、admin、analytics；
- billing 挂起：框架 billing 域硬依赖 River 事件底座，与 D7 冲突，商业化启动时一并采用；
- 全量测试绿（50 包）+ 真实浏览器冒烟全链路（登录/建区/WS 实时/webhook 订阅/通知）。

## 已知行为变化（对外契约）

1. 错误信封扁平化：`{error:{code}}` → `{error:"E_CODE",message,details}`（前端已同步）；
2. `/api/me` → `/auth/me`（+`/api/workspaces` 组合）；`/api/auth/refresh` 移除（会话服务端滑动）；
3. trailing-slash 请求 404（原 chi 200）；深路径错方法 405（原 404）；GET 自动应答 HEAD；
4. egressx fail-closed：不可解析 https 域名在门上拒绝（原连接期失败）；
5. webhook secret 密封存储（原明文列）；dev 登录验证码格式 `code=NNNNNN`（原 JSON body）。

## 对 ADR 0012 的修订

"platform 自持/零外部依赖"表述废止：架构守门（depguard + internal/arch）保留，但平台纯净性
的执行点移至框架仓库；orkome 侧规则改为"框架 import 白名单 + 业务域互斥"。

## 框架回 feeder

执行过程沉淀 21 条框架 issue（`docs/ploykit-framework-issues.md`），其中 F1–F4、F7 已在
v0.4.0 修复；F16（client/hub subscribe 协议断裂，P0）与 F21（AccessLog 破坏 WS 升级，P1）
建议框架优先处理。
