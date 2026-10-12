# 工单产出（Outcome）一等公民化 —— 交接笔记

> 2026-10-12 · 依据：用户裁决"最干净的方式，不考虑兼容与旧数据"+ 两问确认（评论回归讨论面；outcome 后写覆盖）
> 状态：全部落地，门禁全绿，真模型终验通过

## 1. 模型

工单完成态 = **需求（description）→ 产出（outcome）**。outcome 由结算链路结构化写入（agent 最终 assistant 文本 + 任务溯源），详情区渲染"需求→产出"两段式；评论区回归交流面。

**数据**（迁移 1039，`work_item.outcome jsonb`）：
`{"summary": text(≤4000 rune), "task_id": uuid 溯源, "completed_at": timestamptz}`；NULL=尚无完成产出；失败不写（失败没有产出）；后写覆盖（重跑=最新结论）；无 FK（列随工单行删）。

## 2. 后端落点

| 件 | 位置 |
|---|---|
| Outcome 类型 + WorkItem 字段 | `internal/work/app/model.go` |
| PATCH（人工编辑路径） | `ports.go WorkItemPatch.Outcome`（nil=不动）+ `handlers.go PUT` + pgrepo `UpdateWorkItem` |
| 扫列 | pgrepo `wiCols` + 四处 Scan 全部加 `&w.Outcome` |
| 写入 | pgrepo `WriteOutcome`（后写覆盖，工单不存在报错） |
| 结算漏斗（唯一写入点） | `SettlementService.SetOutcomeWriter` + `complete()` 内：`out.Output`（已脱敏）截 4000 rune → `OutcomeWriter.WriteOutcome`；**成功后跳过兜底评论**（同一内容不落两处）；写失败回落评论兜底（产出不丢） |
| 端口（域零） | `orchapp.OutcomeWriter`（仿 CommentWriter），work 侧适配器 `workOutcomeWriter` 在 wiring 注入 |
| fail 路径 | **不写** outcome（裁决见迁移 1039 注释） |

## 3. 前端落点

- `OutcomeBlock`（新组件，`web/src/components/work-item/outcome-block.tsx`）：detail tab `DescriptionEditor` 下方。三态：有 outcome → Markdown + 溯源底注（"来自任务 xxx · 重新派发将覆盖"）+ 人工编辑；工单已完成无 outcome → 空态引导；未完成且无产出 → 不渲染。
- 评论区：最新一条 agent 汇报置顶（"最新汇报"徽章，仅排序提升不移动本体）；配合既有折叠。
- `WorkItem`/`WorkItemPatch` 类型随行；i18n `outcome.*` + `comment.latestReport` 中英双份。

## 4. 顺手拆掉的定时炸弹（与本特性无关但同批发现）

ui-token 三处测试夹具写死时钟 `2026-10-12 12:00 UTC`——JWT exp=+60s，**当天 20:01（本地）起全部测试"过期"爆炸**（今天准时引爆，`TestUITokenConsume_*` 三连 FAIL）。修法：夹具时钟改相对真实时间截秒 + `verifyUIToken` 加 `jwt.WithTimeFunc(s.now)`（exp 校验与 DB 消费同钟，夹具自洽不再依赖墙钟日期）。

## 5. 测试

- 单测 5 例（`service_settle_notify_test.go`）：completed 后写覆盖 / fail 不写 / outcome 落库抑制兜底评论 / 写失败回落评论 / 未接线旧行为
- 真库门控（`outcome_integration_test.go`，TEST_DATABASE_URL）：NULL 语义 / WriteOutcome 覆盖 / GET 随行 / PATCH 人工覆盖 / 幽灵工单报错 / jsonb 往返
- 终验（真 DeepSeek 全管线重派）：任务 completed → `GET /api/work-items/{id}` outcome present（中文结论 + 三种子对比表）；评论区无结算兜底重复（仅模型自己 post_comment 的一条）

## 6. 门禁

Go 49 包 0 FAIL / lint 0 issues / web build + 229 测试 / lint 零发现。

## 7. 遗留

- OutcomeBlock 的人工编辑未做 Markdown 实时预览（textarea 纯文本，可后补）
- `HasAgentComment` 的"首条守卫"语义随兜底评论守卫调整后仍在（outcome 未接线的产品仍依赖它防重复）
