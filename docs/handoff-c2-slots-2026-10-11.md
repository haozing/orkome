# C2 交接笔记：slots 通道（F2 后端 + F3 前端）

> 日期：2026-10-11 · 工作流：C2（波 3）· 需求：`docs/pack-frontend-design-2026-10-11.md` §1（v1.1 审计修订版）；任务书：`docs/pack-rollout-plan-2026-10-11.md` §3 C2
> 前置：B1 交接（`docs/handoff-b1-job-gate-2026-10-11.md`——provision 双版本放行、v1 slots 段曾被静默容忍、迁移 1035）

## 1. 改了什么

| 文件 | 变更 |
|---|---|
| `migrations/1036_pack_ui_slots.up/down.sql` | `pack_ui_entry` 扩 `slots jsonb` 可空列（最小路径：不建新表、不加 JOIN；down 成对 DROP COLUMN） |
| `internal/orchestration/app/service_uientry.go` | 新增 `PackUISlot` 类型与 `SlotWorkItemDetailTab` 常量；`PackUIEntry.Slots` 字段；`UpsertWithSlots`（`Upsert` 保留为传 nil 的别名，零调用方破坏）；`validateSlots`（目录冻结 + height ∈ (0,4096]） |
| `internal/orchestration/adapters/pgrepo/repo_uientry.go` | Upsert 写 slots jsonb（无 slots → NULL，整行覆盖语义）；List `COALESCE(slots,'[]')` 且 nil → `[]`（契约：响应永远 `slots:[]` 非 null） |
| `internal/orchestration/adapters/http/handlers_uientry.go` | PUT body 可选 `slots` 数组；GET 响应 items[].slots 随行（一个端点两用，无新路由） |
| `internal/orchestration/adapters/http/handlers_uientry_test.go`（新） | 契约测试 5 态：slots 落库往返 / 无 slots → `[]` 非 null / 未知标识 400 / 缺 height 400 / v0 语义 PUT（无 slots 字段）照常 |
| `internal/orchestration/adapters/pgrepo/repo_uislots_test.go`（新） | TEST_DATABASE_URL 门控真库集成测试：1036 列形状、slots 往返、覆盖清空、空数组非 null、跨 ws 不可见、Delete 连 slots 删 |
| `cmd/pack-provision/main.go` | ① `packUI.Slots`（yaml `slots` 列表，字段 slot/label/urlTemplate/height）；② v1 解析期 `validateV1UISlots`：标识只认 `workitem.detail.tab`（其他 → fail the install，防概念漂移）、label/urlTemplate 必填、height ∈ (0,4096]；**v0 显式丢弃 slots 段**（`p.UI.Slots=nil`，行为不变锚点）；③ slots 有则 entry 必须有（slots 依附同一 PUT）；④ `probeFrameAncestors`：GET slot URL（模板变量换哑值）验 CSP `frame-ancestors` 含 `--frame-origin` 或 `*`；不合规**只剔除该 slot**（pack 其余照常装）；⑤ `--frame-origin` flag：空 = 第一方豁免（v0 默认，探测不跑） |
| `cmd/pack-provision/provision_test.go` | B1 的 "slots section tolerated" 夹具更新为真 schema（B1 时代 `name:` 键本就是占位猜测，C2 接管后 v1 真解析）；新增 v0 静默忽略 / 非法标识拒 / 缺 height 拒三个子测试 |
| `cmd/pack-provision/slots_install_test.go`（新） | install 全链路：slots 随 PUT 落库（wire 形状断言）/ CSP 不合规剔除 / 合规与通配保留 / 探测失败豁免 / v0 不带 slots / `probeFrameAncestors` 判定矩阵 |
| `web/src/api/packs.ts` | `PackUIEntry.slots?` 可选字段 + `PackUISlot` 接口（可选不破坏 B2 测试夹具） |
| `web/src/components/pack-slot-frame.tsx`（新） | PackSlotFrame：现铸 ui-token（60s 短票，queryKey 含 workItemId 切 tab 必重铸）→ 模板变量 `{uiToken}/{workspaceId}/{workItemId}` 渲染 → iframe sandbox 三权限 → `pack.resize` postMessage 调高（origin 校验 + 数值钳制 [0,8192]）→ 3s 窗口超时回落声明高度 → 右上角固定 external-link 深链兜底（iframe 永远不是唯一路径）→ 铸票失败错误态 |
| `web/src/components/use-pack-slots.ts`（新） | `usePackSlots` hook：拉 ui-entries 展平 (pack, slot) 对；与 PackEntries 共用缓存键。**放 components 层**：layering 规则禁 hooks 层依赖 api/stores，react-refresh 又禁组件文件混导 hook——components 下独立文件是两者交集 |
| `web/src/routes/_app.work-items.$id.tsx` | zod 四触点一个不漏：① searchSchema `z.enum(STATIC_TABS).or(z.string().regex(/^pack:/)).optional()` ② `DetailTab` 类型自动放宽 ③ patchSearch 强转不变 ④ 条件渲染新增 `activeSlot` 分支 + 未知 tab 值（如 pack 卸载后残留 URL）回 detail 兜底 |
| `web/src/i18n/locales/{zh,en}.ts` | 新增 `slots.*` 命名空间（mintFailed/openExternal，中英双份） |
| `web/test/components/pack-slot-frame.test.tsx`（新） | 8 测：变量替换、重挂载重铸、resize 调高/非法值忽略/跨 origin 拒、超时窗口语义、铸票失败、深链兜底常在 |
| `web/test/components/work-item-detail-tabs.test.tsx`（新） | 5 测：zod schema 四态、无 slots 四 tab 零回归、空响应零回归、有 slots 出 pack: tab + PackSlotFrame 渲染 + 切回 detail 卸载、stale pack:* 回 detail |

## 2. 关键抉择（评审请重点看）

1. **存储形状：扩列不建表。** `pack_ui_entry` 一 workspace 一 pack 一行（UNIQUE(workspace_id,pack)，1033），slots 是 pack 级列表——`slots jsonb` 列即可，读取零 JOIN、upsert 零额外语句、uninstall 零额外调用（行删列随删）。设计文档 §1.2 原文"存进既有 pack_ui_entry 表"。
2. **slots 响应形状（一句话）**：`GET /api/ui-entries` 的 `items[].slots` 恒为数组（无 slots 也返回 `[]` 非 null），元素 `{slot, label, url_template, height}`；PUT 请求体 `slots` 可选，缺省 = 清空（整行覆盖语义，与 label/url_template 一致——重装即重写）。
3. **frame-ancestors 探测失败（网络错）→ 豁免保留该 slot**（任务书要求的明示抉择）：探测不通 ≠ CSP 不合规（slot URL 可能只对带票据/内网请求开放），且 slot 是增强面、pack 主体（工具/深链）不应因探测通道故障被挡；判定为"不合规"的三种确证情形（CSP 无 frame-ancestors 指令 / 指令不含 origin 也非 `*` / `'self'` only）才剔除。剔除动作只打 stdout 告警不 fail install，符合"pack 其余件照常装"。
4. **第一方豁免开关 = provision flag `--frame-origin`（pack.yaml 不加标志）**：空（默认）= 探测整体关闭，第一方（agentblog）安装零阻力 = "探测代码到位但不拦截第一方"；传 origin = 非 firstParty slot 全部硬探测。pack.yaml 侧不加字段是因为第一方身份由运维侧认定（self-declared 的 firstParty 标志对第三方无约束力），且 v1 yaml schema 已被 B1 冻结，不再开口子。
5. **v0 pack 带 slots 段 → 解析后显式丢弃**：yaml 非 strict 解析会带出该段，`parsePack` 里 v0 分支 `p.UI.Slots=nil` 保证 v0 行为逐字节不变（测试锚定）；v1 才校验+落库。
6. **slots 依附 entry**：v1 有 slots 无 entry → fail the install（slots 落库走 entry 的同一个 PUT，无 entry 无宿主行）。
7. **未知 tab 值回 detail**：`pack:ghost`（pack 已卸载、URL 残留）渲染 detail 内容——与 zod 丢弃 garbage 后回 detail 同语义，避免空白页。

## 3. 测试实况（2026-10-11，Windows + Git Bash）

- `go build ./...` → 0 错误。
- `go test ./... -count=1` → **49 包全部 ok**（含 C1 并行改动的 engine 包——收尾时 C1 已稳定）。
- `golangci-lint run` → **0 issues**（typechecking error 为 internal/goarch export data 环境噪音，任务书已知；对我的包集合单独跑同样 0 issues）。
- 契约测试：`TestUIEntrySlotsContract` 5/5 PASS；provision 包 33 个子测试全 PASS（含 B1 原有 20 + C2 新增 13）。
- repo 集成测试（TEST_DATABASE_URL → docker orkome-test-pg，自建库 orkome_uislots 跑全部迁移 up，**未对共享库跑 migrate-up**）：`TestPackUIEntrySlots_MigrationAndRepo` PASS（含 1036 列形状断言）。
- `pnpm build` → ✓ built 1.34s；`pnpm test` → **116/116 全绿**（20 文件，含我新增 13 测）；`pnpm lint` → 新改/新增文件零发现（全量 lint 仅剩 `project-nav.tsx:58` 预存 error，任务书明示不属于本任务）。
- e2e 守卫 `TestGuardTruncateCoversAllTables` PASS（1036 无新表，TRUNCATE 清单/allowlist 零改动）。

## 4. 留了什么坑（诚实清单）

1. **真浏览器 slot 渲染未端到端跑**（任务书"不强制"）：无后端登录态。等价覆盖已做：jsdom 层 PackSlotFrame 渲染/resize/变量替换/超时/兜底全测（`pack-slot-frame.test.tsx`），Go 层 CSP 探测四态真 HTTP（`slots_install_test.go`）。**D2/D4 联调时**用真 pack 过一次浏览器渲染，重点看 sandbox 三权限下 pack 页的 postMessage 是否被 agentblog 前端正常发出。
2. **`--frame-origin` 是全局单 origin**：多前端域名（如 canary 域）时需扩成列表。v0 一个 origin 够用。
3. **帧内 404/CSP 头与真实页不一致**：探测只看响应头不看状态码（404 也算探测成功）——SPA 服务器通常全局发 CSP 头，v0 可接受；第三方 pack 出现时若要更严可加"2xx/3xx 才算可达"。
4. **resize 消息的 origin 校验依赖 `new URL(src).origin`**：slot 模板是 pack 控制的字符串，畸形 URL 时 origin=null → 退化为不校验 origin（仍校验 type/数值钳制）。要让畸形 URL 直接进错误态，可在 D2 联调时按需收紧。
5. **slots 与 entry 同表行**：一个 pack 理论上可 PUT 多个不同 slot 标识（服务端 validateSlots 只查单条合法性不查重）——provision 侧 v0 只有一个合法标识所以实际不可达；将来放开目录时记得在 service 层加 slot 标识去重。
6. **B1 交接笔记 §2 所述"v1 里 slots 段已被静默容忍"已过时**：C2 起 v1 真解析，B1 那条测试夹具我已更新为真 schema（其"entry 解析不受影响"的意图保留）。下游若引用该笔记请以本笔记为准。
7. **`pack_ui_entry.slots` 无 DB CHECK（jsonb 数组形状由 repo 层 json.Marshal 保证）**：手工 SQL 可写入非数组 jsonb 会让 List 反序列化报错。要加 `CHECK (jsonb_typeof(slots)='array')` 可在 1037+ 补——1036 保持最小。

## 5. 边界确认

- 未动 `internal/orchestration/adapters/engine/`、`app/ports.go`、`app/service.go`、`cmd/orkome-server/wiring_orchestration.go`（C1 独占）；未动 B1 的 pack_tool_schema 文件（`routes.go` 的 ui-entries 路由段零改动，`PackUIEntries` wire 零改动——service 构造签名不变）。
- 未动 web 的 task-messages / task-tool-renderers / _app.mcp.tsx / packs-sections（A3/B2 归属）。`api/packs.ts` 仅追加可选类型字段（该文件不在任何人的独占/禁碰清单，且为 PackUIEntry 类型正确性所需——B2 测试夹具不受影响）。
- 迁移只用 1036；未对任何共享库跑 migrate-up（repo 集成测试自建 orkome_uislots 库跑迁移后即弃）。零新增依赖。未做任何 git add/commit。
