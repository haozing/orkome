# orkome 功能点审计总报告（第二批 FP-11~FP-20）

> 日期：2026-10-12 · 方法与第一批相同：10 agent 并行、一 agent 一功能点、七步审计法、curl 实测+file:line 定位
> 前置：第一批总报告 `docs/feature-audit-batch1-2026-10-12.md`（已知缺陷只引用编号不重复申报）；两批合计已覆盖**全部 17 个页面 + 全部 ~316 条 API 端点**
> 分册：`docs/audit-fp11..fp20-*.md`（10 份）

---

## 一、总账（第二批）

| FP | 功能点 | P0 | P1 | P2 | 一句话结论 |
|---|---|---|---|---|---|
| 11 | my-work 聚合页 | 0 | 2 | 6 | 四 tab 语义正确；空游标重复页+默认排序双断 |
| 12 | run-logs 与转写抽屉 | 0 | 0 | 6 | 列表链路零漂移；抽屉是"死回放"；FP-05 盲区命中确认 |
| 13 | automation 域 | 3 | 4 | 11 | 调度内核全仓顶级；接线层三条主链路 100% 必败全静默 |
| 14 | conversation/chat 域 | 0 | 4 | 13 | 后端先行域里主干最健康；4 个 P1 接入前必修 |
| 15 | pack 深链与 slot 面 | 0 | 3 | 7 | 票据本体扎实；回验链 architecturally 不可达 |
| 16 | settings 页 | 0 | 2 | 8 | 页面仅 2 节；AccountService 三行接线缺失致 5 端点全 500 |
| 17 | onboarding 旅程 | 0 | 1 | 6 | 主干全通；零工作区用户被双向弹弓困死 |
| 18 | work 高级端点 | 1 | 7 | 11 | 安全底座硬；table/rows 坏+契约欺诈三连 |
| 19 | webhooks + admin | 0 | 4 | 7 | webhooks 全仓最高质量；admin 39 条实为 10 条（文档幻觉） |
| 20 | WS realtime 横切 | 0 | 3 | 9 | 41 类帧只有通知定向帧一条窄路是活的 |
| **合计** | | **4** | **30** | **84** | 另 P3×11 |

**两批累计**：P0×13（归并 8 个根因）· P1×64 · P2×169 · P3×17。覆盖：17 页 / ~316 端点 / 41 类 WS 帧。

## 二、P0 焦点（第二批 4 个，全部十行内可修）

1. **手动触发 autopilot 100% 必败**（FP-13）：`repo.go:774` InsertRun 的 `trigger_id` 空串未包 `nullif()::uuid` → PG uuid 解析错误 → 事务回滚。前端唯一的执行按钮就是"立即触发"，schedule/webhook 路径不受影响——炸的恰好是前端唯一暴露的那条，且 `routes.go:468` 无日志吞根因。
2. **API 创建的唤醒永不 fire + 每秒 PG 错误洪水**（FP-13）：createWakeup 不设 `CreatedBy` → fire 时补零 UUID 撞 `originator_user_id` FK → next_fire_at 永不推进，due 扫描每秒重试同一失败，无告警永不自愈。
3. **autopilot 删除被自己的默认 trigger 100% 挡死**（FP-13）：创建必产 enabled trigger（空 cron 兜底），删除守卫不区分 → DELETE 必 409，前端无 trigger 管理入口 → 删除闭环全断。
4. **table/rows 端到端损坏**（FP-18）：自产 next_cursor 用 Go `time.Time`/uuid 的 `%v` 文本拼接（service_catalog.go:458-462）回放必 500；SELECT 缺 `::text` 导致响应里 id 是数字数组、字段名叫 `"coalesce"`。e2e 只断言首页 200 所以全绿——表格视图主链路翻第二页即死，需返工。

## 三、30 个 P1 按主题归组

1. **订阅与写面越权（IDOR 扩展）**：FP-20 实锤 task/chat scope 订阅越权——Authorize 丢弃 scope/id 两参只查工作区成员，**任何登录用户可跨区订阅任意 task/chat 持续接收 Result/Content 全文**；FP-14 `/read` 零 fence（随机 UUID 也 200）+ 会话 fence 读写双标（同区任何成员可删改他人会话、chat_message 无 sender 列）；FP-15 workItemId 越权责任悬空（票无绑定、pack 无校验原语）。第一批是写面 fence 缺，第二批把 subscribe 面也补进了地图。
2. **实时链"只活一条窄路"**（FP-20 帧三方矩阵核心结论）：41 类帧词汇中实测可达的只有 `user:{uid}` 定向通知帧 6 类；workspace scope 死（batch1 FP-06）之上再叠加 **11 类死帧**（workspace/member/invitation×9、label:created、task:resumed）、**4 类 batch 帧被空 WorkspaceID 门禁静默丢弃**（wiring_batch.go:34-36）、task:message/progress 只发 workspace 不进 task:{id}（scope 错位，与盘点记载矛盾）。强耦合警告：workspace 一接活，progress 风暴（每工具消息 3 组失效键）会直接命中全员——修复必须同变更集带 debounce。
3. **relayx 回环炸弹**（FP-20，潜伏）：relay 回调误用 `Broadcast`（应为 `hub.DeliverRemote` 一行），Redis 模式 ≥2 副本时远端帧再发布、origin 抑制无法终止——2 副本无限空转、3+ 副本指数倍增。当前 memory 模式无症状，**启用 REDIS_URL 前必修**。
4. **静默接线缺失族**（"内核优秀、装配漏电"）：FP-16 AccountService 三行未设 → 找回密码/邮箱验证/注销 5 端点全 500，**全体用户 email_verified 恒 false、忘旧密码即永久锁死**；FP-19 平台管理员授予/撤销零审计（WithAuditor 未接，一行修）；FP-13 webhook 公开入站被全局 CSRF 拦死（ExemptPrefixes 只豁免 /ws，正确 HMAC 也 403——外部触发链 0% 可用，与 batch1 PAT 前缀错配同属"全局 CSRF 门误伤机器面"族）。
5. **空串/零值语义族**（第二批新增系统性模式）：FP-11 确认空串游标病在 work.ts 同发——`next_cursor:""` 穿透 `?? undefined`，**my-work 四 tab + 工单列表"加载更多"即重复整页**（与 batch1 FP-10 通知域同病，一行 `??`→`||` 三处同愈）；FP-13 两条 P0 本质同族（空 trigger_id/零 CreatedBy）。
6. **契约欺诈三连**（FP-18，前端接入前必须定型）：①`PUT /api/labels/{id}` 忽略路径 id 按 name upsert——改名凭空多出新标签；②batch-update 白名单外字段静默丢弃+空 patch 跳 revision、部分失败只回 `{updated:N}`；③query/table 面的 `labels`/`priority_min` 过滤器回显却从不执行（实测 priority_min:99 全量返回）。
7. **"死回放"与零消费面**：抽屉打开期间无订阅无轮询（一行 `useWsScope` 接活，FP-12）；直聊任务 await_input 全站无处可批、cancel 端点零消费（FP-12）；usage 全链（表+rollup+端点）零消费（FP-12）。
8. **体验断头**：FP-17 零工作区用户双向弹弓困死（login↔onboarding 互弹、onboarding 无登出无回链，~15 行修）；FP-11 默认排序双断（服务端 created_at 分页 vs 前端 last_activity_at 重排 + 核心 PUT 不 bump 活跃时间）；FP-15 url_template 无 scheme/origin 校验（`javascript:` 模板恰好落进探测豁免通道）。
9. **其余**：FP-14 archived agent 发消息 500 丢用户输入 + `chat:done` 只覆盖 2/4 路径且广播口径与 REST 相反；FP-19 fanout `_, _ = Emit` 吞投递丢失；FP-16 改密/会话管理后端 12 项全对但前端零封装（display_name 定终身）；FP-18 preview-trigger 100% 500（batch1 根因 D 的 orchestration 孪生实例，波及评论触发预览）；FP-12 确认 FP-05 分页盲区为纯前端 ~15 行未接。

## 四、系统性模式（第二批新增与强化）

1. **"内核优秀、装配漏电"**（本批主旋律）：调度内核 CAS 认领/四层熔断全仓顶级却三条主链 100% 必败（FP-13）；AccountService 框架完整就绪只差三行构造（FP-16）；webhooks 域质量最高但 adminops 是不可达死代码（FP-19）。模式：**每一层单独看都测试通过，层与层之间的接线无人测**。
2. **文档幻觉与契约漂移进档案**：admin "39 条"实为 10 条（三个 Mount 全未装配，盘点总档 §11.2 需修正）；§13.6 ui-tokens `pack` 字段漂移实锤（前端三处全发 `plugin`）；labels PUT/batch/过滤器的实现与契约语义相反。**文档不再可信，契约测试双向对账的优先级再次上调**。
3. **"测试 A 生产 B"持续收割**：FP-13 三个 P0 全部漏过 45+ 存量单测（fake repo 不经 PG 约束/集成测试无 DB 即 skip/MountPublic 测试不穿全局 CSRF）；FP-17 发现 `identity.test.ts` 用错误契约 mock 把 P0 锁成绿测试（batch1 模式 1 的第二实证）。
4. **安全面读写不对称**：读面（wsGuard/票据隔离/SSRF 13 向量）全硬，写面与订阅面（IDOR 族）系统性缺 fence——两批合计：评论三写端点、agent-skill 绑定、chat /read、task/chat scope 订阅。
5. **v0.4.1 版本钉死的落差**：MFA 1176 行已在上游 ploykit main、orkome 钉 v0.4.1 全 404（FP-16）——升级评估应与接线清单合并做。

## 五、修复路线图（第二批增量）

**第一波（十行级，清 4 个 P0 + 3 个高危 P1）**
1. FP-13×3：`nullif` 包 trigger_id / createWakeup 必填 CreatedBy / 删除守卫跳过空 cron 默认 trigger
2. FP-20 F1：Authorize 消费 scope/id 参数（订阅 fence）
3. FP-20 F2：`Broadcast` → `hub.DeliverRemote`（REDIS 上线前必修）
4. FP-16：AccountService 三行接线（解锁找回密码/邮箱验证/注销）

**第二波（接线与小改）**
5. FP-11 空游标 `??`→`||`（三处同愈）+ PUT 更新 last_activity_at
6. FP-17 onboarding 逃生门（unauth 守卫 + 换号按钮，~15 行）
7. FP-12 抽屉接 `useWsScope('task')`（一行接活）
8. FP-13 webhook 入站 CSRF 豁免（与 batch1 PATPrefix 同一变更集）
9. FP-18 preview-trigger 双因各一行（根因 D 孪生）
10. FP-14 archived agent 前置校验 + `/read` fence

**第三波（返工/裁决类）**
11. FP-18 table/rows 游标与列别名返工 + 契约欺诈三连定型（前端接入前置条件）
12. FP-15 consume 票据自证改造（10 行）+ url_template scheme 校验 + workItemId 绑定设计裁决
13. FP-20 workspace 接线 + progress debounce（同变更集）+ batch 帧补 WorkspaceID + 11 类死帧逐个裁决（接/删）
14. chat 域接入前裁决：fence 双标口径 + chat:done 广播策略（FP-14，接入成本 MVP 1 天/完整 3~4 天）

## 六、两批合并的产品级结论

1. **质量分布极度不均**：内核层（调度/事务/fence 读面/SSRF/票据签名）是高水准；坏的全部集中在**装配接线、错误处理、契约对齐**三个横切面——这正是"分层测试各自绿、集成缝无人守"的必然产物。
2. **优先修复顺序建议不变**：第一波一行级修改（两批合计约 15 处）可清掉全部 13 个 P0 中的 12 个（唯一需返工的是 FP-18 table/rows）。
3. **监控缺位放大一切**：本批 4 个 P0 中 3 个"静默运行"（无日志无告警每秒刷错/吞根因/投递丢失无痕）——错误日志与投递可观测性应与修复同批落地，否则修完还会再坏。
4. **文档与契约需一次性对账**：route-compare 幽灵条目、admin 计数幻觉、§13.6 字段漂移、labels/batch/filter 契约欺诈——建议立"契约对账"专项，产出物即测试。

## 七、分册索引与证据留存

- 分册：audit-fp11-my-work / fp12-run-logs / fp13-automation / fp14-conversation / fp15-pack-ui / fp16-settings / fp17-onboarding / fp18-work-advanced / fp19-webhooks-admin / fp20-realtime（均 docs/ 下 2026-10-12）
- FP-20 核心交付物：41 行帧三方矩阵（帧×发送点×前端规则）、scope 矩阵、7 组 WS 实测记录、8 条测试缺口
- 盘点总档需修正项：admin 端点 39→10（FP-19）；§13.6 ui-tokens 字段 pack→plugin（FP-15 实锤）
- 遗留活体证据：fp17-fresh-ws（删除 500 第二实证）、fp11-ws（空工作区删除 500）、fp13-wi 工单（receipt 卡活样本）、fp18 四个自定义状态（状态目录无删除端点实证）；其余 fp 前缀测试数据各 agent 已清理
