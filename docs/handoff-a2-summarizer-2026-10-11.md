# A2 交接笔记：dsh 七段 summary 提示词搬运 + summary 生成上移 service 层

> 日期：2026-10-11
> 任务书：`docs/pack-rollout-plan-2026-10-11.md` §1 A2 + §8 审计修正（summary 生成上移）
> 提示词来源：`dsh-borrowing-table-v2-2026-10-11.md` §1.2（MIT，本地 clone `deepseek-harness/packages/compaction/compaction-basic/src/summarizer.ts`）
> 状态：已完成；开关默认关，行为与现状 byte-identical。**未 commit（按任务书禁令，只留工作区改动）。**

---

## 一、改了什么

### 1. 分层上移：summary 生成从 repo 层挪到 service/app 层

- `internal/orchestration/adapters/pgrepo/repo.go`
  - `CompleteRun(ctx, id, result, sessionRef, engineTag, summary, usage)` / `FailRun(ctx, id, reason, errMsg, summary)`：签名新增 `summary` 参数，repo 内的 `domain.TaskSummary` 计算**已删除**——repo 只落存。
  - 新增导出 `LastTaskText(ctx, taskID)`（原内部 `lastText` 转正），给 service 层读材料。
- `internal/orchestration/app/ports.go`：`Repo` 接口同步两处签名 + 新增 `LastTaskText`。
- `internal/orchestration/app/service_summary.go`（新）：`SummaryService`——开关关走 `domain.TaskSummary` 等价纯截断；开关开走 LLM（见下）；LLM 失败/空产出 **fail-open 回落纯截断**（结算永不被 summary 卡死）。材料 = lastText + task_message 轨迹（最新 200 条回正序）+ 同 work item 前序任务转写（`SessionTranscript`，与派发侧同源）。
- `internal/orchestration/app/service.go`：`SettlementService` 持 `summaries` 字段 + `SetSummarySummarizer` 接线；`complete()`/`fail()`/`SweepExpiredAwaiting` 三处结算点改为先产 summary 再落库。**未接线时回落 `domain.TaskSummary`，与上移前 byte-identical**（老测试/老用法不受影响）。
- `internal/orchestration/app/service_external.go`：external runtime 的 `Complete`/`Fail` 同口径接线。
- `internal/orchestration/adapters/workers/sweeper.go`：判死路径 `FailRun` 改调 `Settle.SummaryForSweep`（sweep 侧只有 taskID，无 workspace 材料，LLM 路径自然回落纯截断——判死任务轨迹本就残缺，可接受）。
- `cmd/orkome-server/wiring_orchestration.go`：装配 `SummaryService`（engine=既有 EinoEngine，secret=既有 credential 链），接到 settle 与 external。

### 2. 七段提示词搬运（MIT 署名）

- **落位：`internal/orchestration/domain/checkpoint.go`**（不是初稿的 `orchestration/summarizer` 子包——`internal/arch` 依赖铁律规定 app 层只能 import `orchestration/domain`，独立子包会被 arch 测试打回。这是落位时发现的实际约束，README 的分层说明与 `.golangci.yml` 的 depguard 同源）。
- 逐段搬运：八个小标题（Primary Request and Intent / Key Technical Concepts / Files and Code / Errors and Fixes / Pending Jobs / Current Work / Next Step / Critical Context）+ Rules 保留规则（精确路径/命令/错误串/标识符/数值/函数签名/语法片段原样保留；用户纠正忠实记录）+ `CHECKPOINT_PREAMBLE`（"既定背景直接续做"）+ `<compacted-summary>` 包裹标签。文件头有 MIT 来源署名注释。
- 参数化：`SystemPrompt()`（dsh 原设计 instruction 是重放对话末尾的 user 消息——那是暖前缀复用的一部分，orkome 无此管线，故上移为 system 角色）；`UserPrompt(TaskMaterial)` 把 orkome 的 lastText/轨迹/会话转写装进材料块；`FrameCheckpoint` 供后续注入复用。
- **明确不搬**（借鉴表 §1.2 裁决）：暖前缀缓存复用（tools/replay 管线）——orkome summary 是独立 aux 小调用，抄了不生效。

### 3. 配置开关（默认关）

- `cmd/orkome-server/config.go`：`SummaryLLMEnabled bool \`env:"ORKOME_SUMMARY_LLM_ENABLED" envDefault:"false"\``，遵循既有 env tag 模式。
- 关闭时：零 LLM 调用、零 aux usage 行、summary 字符串与旧路径 **byte-identical**（单测锁死）。
- 打开后：经 `Engine.Generate`（modelmeta 工厂，与 engine 侧 aux 调用同模式）做一次独立小调用；每次成功记一行 `task_usage`（provider=summary, model=llm-checkpoint, label=summary）便于观测成本。

## 二、为何这么改

1. **LLM 不能进 repo 层**（排期 §8 审计结论）：repo 是纯存取面，LLM 调用有网络 IO/凭证/成本语义，必须在 service 层。上移本身先于开关合入，即使开关永远不开，分层也是净改善。
2. **byte-identical 纪律**：这是"先上代码不改变行为"波次的硬验收——开关关闭路径不引入任何可观测差异（包括 task_usage 行数、summary 字符串、错误路径）。
3. **fail-open 而非 fail-closed**：summary 是锦上添花，结算主路径（状态机推进、重试子任务、事件）不能被摘要失败拖死。LLM 挂了就回落截断。
4. **落位 domain 而非新子包**：项目有 arch 铁律测试（`internal/arch/arch_test.go` rule 2/3 app dependency face），独立 `orchestration/summarizer` 包会被 app import 违规。提示词+材料装配是纯函数，天然 domain 层。

## 三、测试

| 测试 | 文件 | 验证 |
|---|---|---|
| `TestCompactionInstructionGolden` / `TestCheckpointPreambleGolden` | `domain/checkpoint_test.go` | 提示词与 golden 全文逐字节一致（防手滑改坏七段） |
| `TestSevenSectionTitlesPresent` / `TestPreservationRulesPresent` | 同上 | 八标题各恰出现一次；保留规则关键句在 |
| `TestFrameCheckpoint` / `TestFallbackMatchesLegacyByteIdentical` | 同上 | 注入框定形状；Fallback=domain.TaskSummary |
| `TestSummaryServiceDisabledByteIdentical` | `app/service_summary_test.go` | **验收②**：开关关，ForComplete/ForFail 与旧路径 byte-identical，零 LLM 调用零 aux usage |
| `TestSummaryServiceEnabledUsesSevenSectionPrompt` | 同上 | 开关开走 Engine.Generate，system=七段提示词，材料含轨迹/最终文本，记 1 行 summary usage |
| `TestSummaryServiceEnabledFailOpen` | 同上 | LLM 失败/空产出回落截断，byte-identical |
| `TestSummaryServiceForFailPrompt` | 同上 | 失败材料口径 reason+": "+errMsg |
| `TestSettlementLayering` | 同上 | **验收③**：Settlement 持生成职责；repo fake 只收成品；未接线回落 |
| DB 集成 5 例（原 `summary_integration_test.go` 更新） | `pgrepo/summary_integration_test.go` | 上移后落存路径回归：lastText 优先/fallback 截断/重试子任务携带 prior_summary |

命令输出摘要（实际执行）：
- `go test ./... -count=1` → **49 包 ok，0 FAIL**（含 engine 包——见下）
- `golangci-lint run ./internal/orchestration/... ./cmd/... ./internal/arch/...` → **0 issues**（本机 make 不存在，直接跑的仓库 .golangci.yml 同配置；环境注记见"坑 5"）
- DB 集成（`TEST_DATABASE_URL=…orkome-test-pg…`）：`pgrepo -run "TestCompleteRun|TestFailRun"` → 7/7 PASS；workers 全量 12 PASS 1 FAIL（B13，见下，非我引入）

## 四、留了什么坑 / 遗留事项

1. **真调用冒烟未做**：环境无可用模型 key（agent 无 credential secret），七段 LLM 路径只过了 fake engine 单测。**待办**：拿真 key 设 `ORKOME_SUMMARY_LLM_ENABLED=true` 跑一次真任务，人工核 checkpoint 质量（中文任务的七段是否会被 Rules 的"English prose"带偏成英文——若要中文 checkpoint 需改 Rules 第一条并同步 golden，是提示词语义变更，应单独决策）。
2. **队友并行改 engine 包**：开发期 `internal/orchestration/adapters/engine/tools_mcp.go`（A1 归属）一度编译失败（`outputValidator` 等中间态）；按任务书我未触碰，收尾时 A1 已修好，全量测试 engine 包 ok。工作区里 engine 的新文件（output_validator*.go 等）与 web/ 前端改动（A3）是队友的，验收时勿混入本任务。
3. **sweep 判死路径材料受限**：`FailStaleRunning` 只回 taskID，`SummaryForSweep` 无 workspace 上下文，开关开时也走纯截断。若将来想让判死任务也有 LLM checkpoint，需 `FailStaleRunning` 顺带返回 workspaceID/agentID（动 repo 签名，留给需要时再做）。
4. **轨迹窗口 24k rune 是拍的**：`DefaultMaxTranscriptRunes`（domain/checkpoint.go）按"约 24k rune ≈ 中端模型 8-16k token"估的，没有实测。真调用冒烟时顺手校准；调大无破坏性（材料函数纯）。
5. **本机 lint 环境注记**：仓库 make 不在 PATH（Git Bash 无 make）；golangci-lint 二进制也缺失，我用 `go install golangci-lint/v2@v2.6.2` 现装的（v2.1 与 go1.27 的 stdlib export data 不兼容，报 export data version 4 > 2 的 typecheck 噪音但判定不受影响）。CI/他人环境跑 `make lint` 应无此问题。
6. **DB 集成里的既有失败**：`workers TestB13_MultimodalOutputParts` 在我改动之外的路径失败（chat_message 查询 no rows——依赖 engine 的 FakeStep 多模态管道，疑与 A1 的 engine 中间态相关）。我用 stash 验证基线的尝试因工作区含队友未完成改动而无法构建干净基线，未能最终归因；本任务在 workers 包只改了 sweeper.go 的 FailRun 传参（B13 不经过该路径）。**留给验收人复核或归 A1 会话**。
7. **`task_usage` 的 summary 行语义**：provider 硬编码 `"summary"`/model `"llm-checkpoint"`，不是真实 provider/model 名（Engine.Generate 的 usage 没透传 provider 信息）。若要精确成本归因，需扩 Engine.Generate 返回 usage（接口变更，牵连 eval/quickaction 调用方，超出本波）。

## 五、给下一个会话的快速上手

- 想开开关：`ORKOME_SUMMARY_LLM_ENABLED=true` 起服即可（agent 需配好模型 credential）。
- 想调提示词：改 `domain/checkpoint.go` 的 `compactionInstruction`，golden 在 `domain/checkpoint_test.go`，**两处必须同步改**（这是防手滑机制，不是障碍）。
- 想看材料组装：`app/service_summary.go` 的 `taskMessages`/`sessionTurns`；材料格式在 `domain/checkpoint.go` 的 `UserPrompt`。
- 相关文件全表：`domain/checkpoint.go`（提示词+装配）、`app/service_summary.go`（生成服务）、`app/service.go`（结算接线）、`app/service_external.go`（external 接线）、`adapters/pgrepo/repo.go`（落存+LastTaskText）、`adapters/workers/sweeper.go`（判死路径）、`cmd/orkome-server/config.go`+`wiring_orchestration.go`（开关+装配）。
