# FP-08 功能点审计：Squads 与 Skills 管理（2026-10-12）

审计人：FP-08 audit agent（ZCode）。只读代码 + 对 dev 环境（http://localhost:8080）curl 实测；未改任何产品代码。测试数据均以 `fp08` 前缀残留在 `inventory-ws` 工作区。

## 一句话结论

两个域的主链路（列表/新建/详情/成员管理/SKILL.md 编辑/绑定/skills.sh 开关）功能完整、防御面（CSRF/成员门/SSRF/manifest 防篡改/路径逃逸）扎实，但**创建路径的重复名处理两个域都坏了**（squads 500、skills 静默改写既有技能），**squad-evaluated HTTP 回调在生产接线里是死端点**，且 agent-skill 绑定面存在跨工作区越权写。

## 缺陷总览

| 级别 | 数量 |
| --- | --- |
| P0 | 0 |
| P1 | 4 |
| P2 | 11 |

---

## 1. 全链梳理

### Squads（归属 internal/orchestration，非独立域）

| 层 | 文件 |
| --- | --- |
| 路由 | `internal/orchestration/adapters/http/handlers_taskview.go:38-49`（10 条 `/api/squads*` + `POST /api/work-items/{id}/squad-evaluated`） |
| handler | 同文件 `handleListSquads/handleCreateSquad/handleAddSquadMember/handleRemoveSquadMember/handleListSquadMembers/handleSquadMemberStatus/handleUpdateSquadMemberRole/handleSquadEvaluated`；CRUD 的 GET/PUT/DELETE 在 `handlers_squad_crud.go` |
| service | `internal/orchestration/app/service_squad.go`（SquadService：AddMember/RemoveMember/SetMemberRole/MembersStatus/Get/Update/Delete/RecordEvaluation）；领域纯函数 `internal/orchestration/domain/squad.go`（成员状态推导） |
| repo | `internal/orchestration/adapters/pgrepo/repo.go:1722-1965`（CreateSquad/ListSquads/GetSquadRow/SquadNameTaken/UpdateSquad/DeleteSquad/AddSquadMember/RemoveSquadMember/UpdateSquadMemberRole/ListSquadMembers/ListSquadMemberTasks/SquadLeader）；engine 侧只读 `GetSquad`(1359)/`LeaderAgentID`(1388) |
| schema | `migrations/1003_orchestration.up.sql:48-63`（squad UNIQUE(workspace_id,name)；squad_member PK(squad_id,agent_id)；leader_agent_id FK→agent，NO ACTION） |
| 前端 | `web/src/routes/_app.squads.index.tsx`、`_app.squads.$squadId.tsx`；API 封装 `web/src/api/squads.ts` |
| engine 只读 | `internal/orchestration/adapters/engine/engine.go:216-235`（squadBrief 组装点）、`engine/squad.go:18-34`（squadBriefing 文案）、`engine/registry_assembly.go:44-55`（领队三件套工具装配）、`engine/tools_squad.go`（dispatch_to_member/create_child_work_item/record_evaluation）；接线 `cmd/orkome-server/wiring_orchestration.go:85,104-105,340` |

### Skills

| 层 | 文件 |
| --- | --- |
| 路由 | `internal/skills/adapters/http/routes.go:54-76`（18 条：CRUD/search/import/refresh/files/labels/agent 绑定×5） |
| service | `internal/skills/app/service.go`（SkillService + ImportService：URL/zip/tarball/skills.sh） |
| domain | `internal/skills/domain/rules.go`（frontmatter 解析、路径清洗、1MiB/200 文件/50MiB 上限、manifest Merkle 摘要与防篡改校验）、`external.go`（GitHub/skills.sh 源解析） |
| repo | `internal/skills/adapters/pgrepo/repo.go`（255 行全量） |
| schema | `migrations/1007_channels_support.up.sql:34-62`（skill UNIQUE(workspace_id,name)；skill_file UNIQUE(skill_id,path)；agent_skill PK）；`1029_workitem_fk_cascade.up.sql:26-27`（skill 删除级联清 agent_skill） |
| 前端 | `web/src/routes/_app.skills.index.tsx`（列表+新建+导入 URL/zip）、`_app.skills.$skillId.tsx`（文件树+编辑器+删除/刷新+绑定展示）、`_app.agents.$agentId.tsx:562-720`（AgentSkillsTab 绑定管理）；API 封装 `web/src/api/skills.ts` |
| engine 只读 | `internal/orchestration/adapters/engine/skill.go`（SkillSource→eino skill middleware，仅注入 enabled 技能）；接线 `cmd/orkome-server/wiring_skills.go:59-82` |
| 开关 | `cmd/orkome-server/config.go:42-43`：`GITHUB_TOKEN`、`ORKOME_RUN_SKILLS_SH_INTEGRATION` → `wiring_skills.go:19-22` |

### 分层不一致（架构观察）

`handleCreateSquad`、`handleListSquads`、`handleRemoveSquadMember`、`handleListSquadMembers` 绕过 `d.Squads` 服务直连 repo（`handlers_taskview.go:358-368,370-393,416-434`），其余成员操作走服务。已造成实际分歧：创建路径没有服务的角色校验与 leader 校验（见 P1-1、P2-2）；`d.Squads==nil` 的 fallback 分支语义也不同（fallback 要求 role 必填，服务路径空 role 默认 `member`，`handlers_taskview.go:459-481`）——生产恒有 Squads，fallback 属死代码但易腐化。

## 2. 契约对齐（前端 api/*.ts ↔ 后端）

逐条核对 18 条 skills + 11 条 squads 路由与 `docs/inventory-api-2026-10-12.md` §4.3/§7：

- **一致**：所有 list 端点均 `{members:[]}`/`{skills:[]}`/`{agents:[]}` envelope，前端 `?? []` 兜底 null（`squads.ts:38,58,82`、`skills.ts:36,60,91,114`）；创建 201、删除/成员操作 `{result}`；PATCH role `{role}`；文件保存 `{file, manifest_sha256}`。
- **文档失真 1 条**：`inventory-api-2026-10-12.md:433` 标注 `PUT /api/agents/{agentId}/skills` 前端"是（整体替换）"，但 `web/src/api/skills.ts` 无该封装（仅 add/enabled/remove/list；实测端点本身工作正常，`{"set":1}`）。
- **载荷字段**：`SquadInput.leader_agent_id?` 可选、后端 `nullif($4,'')::uuid` 接空串，对齐；`choreography` bool 直传对齐。
- skills 前端 search 走 `/api/skills/search?q=`（`skills.ts:30-37`），与后端 `handleSearch` 对齐。

## 3. curl 实测（dev，inventory-test@orkome.dev @ inventory-ws）

### Squads

| # | 用例 | 结果 | 判定 |
| --- | --- | --- | --- |
| T1 | POST /api/squads 正常创建 `fp08-squad` | 201，字段齐全 | ✔ |
| T2 | POST 重复名 `fp08-squad` | **500 E_INTERNAL**（应为 409） | ✘ P1-1 |
| T3 | POST 名称 300 字符 | **201**（前端 maxLength=100 仅 UI 层） | ✘ P2-1 |
| T4 | POST leader_agent_id=不存在 uuid | **500**（FK 裸抛；若为跨 ws 真实 agent uuid 则 FK 放行→201 跨区 leader） | ✘ P2-2 |
| T5 | POST 成员（合法 agent） | 200 added | ✔ |
| T6 | 重复加同一成员不同 role | 200，role 静默更新为 reviewer（ON CONFLICT DO UPDATE） | 观察 |
| T7 | 加不存在 agent | 404 "squad or agent not found" | ✔ |
| T8 | role 90 字符 | 400 "role must be <= 80 runes" | ✔ |
| T9 | PATCH role="" | 200（服务默认 member） | ✔ |
| T10 | DELETE 有成员的小队 | 409 "squad still has members" | ✔ |
| T11 | 移除成员两次 | 两次都 200 removed（幂等静默） | 观察 |
| T12 | GET /api/squads/not-a-uuid | **500**（应 400/404） | ✘ P2-3 |
| L1 | 创建时带 leader（ws 内合法 agent） | leader **不**入成员表 | ✘ P2-2 |
| L2 | PUT 先置空再设同一 leader | leader 自动以 role='leader' 入成员（joinLeader） | ✘ P2-2（与 L1 不一致） |
| U1b | PUT 改成已占用名 | 409 "squad name already taken"（Update 路径 SquadNameTaken 正常） | ✔ |
| E1 | POST squad-evaluated（合法形状） | **500 "evaluation recorder not wired"** | ✘ P1-3 |
| E2 | POST squad-evaluated outcome=bogus | 400 | ✔ |

### Skills

| # | 用例 | 结果 | 判定 |
| --- | --- | --- | --- |
| S1 | POST /api/skills `fp08-skill` | 201 | ✔ |
| S1b | POST 同名再创建（description=SECOND-CREATE） | **201 且改写既有技能**（description/origin/origin_ref 被覆盖，返回同一 id；应为 409） | ✘ P1-2 |
| S3 | name 120 字符 | 400 "invalid skill name"（服务层 ≤100） | ✔ |
| S4 | PUT SKILL.md 无 frontmatter name | 400 "SKILL.md requires frontmatter with a name" | ✔ |
| S5 | PUT 合法 SKILL.md | 200；skill.description 同步为 frontmatter description | ✔ |
| S6 | path=`../evil.txt` | 400 "file path rejected" | ✔ |
| S7 | path 含控制字符（JSON `\b` → U+0008） | **200，路径 `a\x08.txt` 入库**（CleanPath 不拒绝控制字符） | ✘ P2-4 |
| S8 | 文件 >1MiB | 400 "file exceeds 1MiB" | ✔ |
| S9 | 以"新建文件"路径 PUT path=SKILL.md | 200，**主文件内容被静默覆盖**（同 file id） | ✘ P2-5 |
| S10 | DELETE SKILL.md | 400 "cannot delete SKILL.md main file" | ✔ |
| S11 | DELETE 非主文件 | 200 + 新 manifest | ✔ |
| S12 | search `q=%`（通配符注入） | 返回全部（ILIKE 未转义，仅同工作区枚举面） | 观察 |
| S13 | import `https://skills.sh/owner/repo/my-skill`（格式正确） | 400 "skills.sh integration disabled (ORKOME_RUN_SKILLS_SH_INTEGRATION)"——**开关关闭行为正确** | ✔ |
| S13' | import 格式错误的 skills.sh URL | 400 "skills.sh url must be skills.sh/{owner}/{repo}/{skill-name}"（DetectSource 先于开关） | ✔ |
| S14 | import `http://localhost:8080/config`（SSRF 探测） | 400 egress 拒绝（::1 不在放行网段） | ✔ |
| S15 | refresh 手动来源技能 | 200 `{"refreshed":0}` | ✔ |
| S16-S20 | 绑定 agent→enabled=false→绑不存在 skill(404)→绑非法 uuid(**500**)→解绑 | 见 P2-3 | ✔/✘ |
| S21 | GET /api/skills/{id}/agents 解绑后 | `{"agents":[]}` | ✔ |
| U2 | PUT /api/agents/{id}/skills 整体替换 | 200 `{"set":1}`，绑定生效 | ✔ |

## 4. 引用完整性与 squadBrief（engine 只读对照）

- **leader 引用**：`squad.leader_agent_id` FK→agent 挡住"不存在 agent"（但报 500），**挡不住跨工作区 agent**——创建路径 `CreateSquad`（`pgrepo/repo.go:1724-1734`）不校验 leader 归属；Update 路径有 `GetAgent` ws 校验（`service_squad.go:218-237`）。前端创建弹窗根本不选 leader，风险集中在 API 面。
- **成员引用**：squad_member FK→agent；agent 仅 archive 无硬删（无 DELETE /api/agents 路由），悬垂不可达。`ListSquadMembers` JOIN agent 带 `archived` 标志（前端置灰展示，`_app.squads.$squadId.tsx:336-343`）；但 **`GetSquad`（engine briefing 数据源，`pgrepo/repo.go:1359-1385`）不过滤 archived** → 已归档 agent 仍进入 squadBrief 成员清单与编排模式（choreography）的 consult member 工具（`engine.go:222-231`）。
- **squadBrief 组装**：`engine.go:216-235` 仅当 `task.IsLeaderTask && task.SquadID != ""` 时经 `SquadLoader`（wiring_orchestration.go:104-105 → `GetSquad`，ws 域内）取数；`squad.go:18-34` 产出成员清单（名字/角色/描述）+ 模式化指令：模式 A（dispatch）附 dispatch_to_member/create_child_work_item/record_evaluation 三件套（`registry_assembly.go:44-55`），模式 B（choreography）逐成员构建 consult 工具。brief 拼在 user prompt 尾部。**与库内数据的对照无出入**；唯一偏差是上述 archived 成员混入。
- **技能删除级联**：skill 删除级联清 agent_skill（1029 迁移），无悬垂绑定；engine 侧 `ListSkills` 只注入 enabled（`wiring_skills.go:66-72`），`GetSkill`（MainByName）读前验 manifest，篡改即 fail-closed（`service.go:245-247`、`routes.go:28-32`）。

## 5. 代码质量（前端编辑器/加载态/i18n/空态）

- SKILL.md 编辑器为**手动保存**（无自动保存），save 按钮 dirty 门控（`_app.skills.$skillId.tsx:315-321`）；保存成功后以 `manifest_sha256` 重建 key 强制 remount 取新内容（:206）。**无并发冲突检测**——`manifest_sha256` 只作 remount key，两个标签页互踩为 last-write-wins（P2-6）。
- frontmatter 前端提示 `content.slice(3).includes('name:')`（:401-403）是近似判断（`---x` 开头会误报），仅提示不阻断，可接受。
- 成员/文件列表 loading、error（带 retry）、空态齐全（squads 详情 :310-321；skills 文件 :150-157、:176-178；AgentSkillsTab :588-609）。
- i18n：抽查 25 个 `squads*`/`skills*`/`agents.skills*` key，en/zh 双语全存在（`web/src/i18n/locales/{en,zh}.ts`）。小瑕疵：squads 列表创建弹窗错误兜底文案借用了 `skills.createFailed`/`skills.creating`（`_app.squads.index.tsx:171,184`）。
- 小队列表无筛选/搜索（前端无输入、后端 `ListSquads` 无参数，按 name 排序）；文档未承诺该能力，记录为观察项。技能列表有 400ms 防抖搜索 ✔。
- `SquadBasicForm` 以 `dataUpdatedAt` 拼 React key（`_app.squads.$squadId.tsx:123`）强制重置本地编辑态——轮询/失焦重取会**清掉用户未保存的编辑**，建议改用 id+版本号。

## 6. 后端正确性补充

- 大小上限：文件 1MiB（S8 ✔）、zip 50MiB、URL 拉取 50MiB（`domain.MaxURLFetchBytes`）、单技能 200 文件；handler 层 JSON body 8MiB（files）/1MiB（其余）双保险。
- 成员重复添加：幂等更新 role（T6），前端候选列表过滤已加入成员（`_app.squads.$squadId.tsx:494`）——行为可接受但 API 无"已是成员"信号。
- SKILL.md frontmatter：只要求 name 非空；`fm.Name` 与 skill.name 不一致不校验、也不同步（description 单向同步，且**清空 description 不会回写**，`service.go:174-179`）→ 技能描述可滞留旧值（P2-7）。
- `Refresh`（service.go:620-648）依赖 `InsertSkill` 的 ON CONFLICT upsert 实现同位更新——修 P1-2 时必须保留该路径语义（建议在 Service.Create 加 `GetSkillByName` 409 检查，与 Update 对齐，不动 repo SQL）。
- CSRF、`memberMW`（X-Workspace-Id 必带 + 成员校验）覆盖两域全部路由 ✔。

## 7. 缺陷清单

### P1（4）

**P1-1 小队重名创建返回 500（预检查永久失效，死代码）**
- 现象：T2 实测 500 E_INTERNAL；`UNIQUE(workspace_id,name)` 裸抛。
- 根因：`handlers_taskview.go:382` 以空串调 `SquadNameTaken(ctx, ws, name, "")`；`pgrepo/repo.go:1787` 的 `id <> $3::uuid` 对 `''::uuid` 必然报 invalid input syntax → `err == nil && taken` 恒 false → 预检查是死代码，落到唯一约束 500。
- 修复：handler 传真实语义的排除 id（改用 ` SquadNameTakenNoExclude` 或 repo 层 `$3::uuid` 改 `nullif($3,'')::uuid` + `id <> coalesce(..., '0000...'::uuid)`）；或创建路径改走 `SquadService` 统一校验。补一条 HTTP 集成测试断言 409。

**P1-2 技能重名创建静默改写既有技能（含来源改写）**
- 现象：S1b 实测 POST 同名 → 201 同 id，description/origin/origin_ref 全被覆盖（import 来源会被 manual 覆盖，provenance 丢失）；`skill` service 的 `Create` 无重名检查而 `Update` 有 `ErrSkillNameTaken`（`service.go:120-137`），行为自相矛盾。
- 根因：`service.go:82-94` 直接 `InsertSkill`；`pgrepo/repo.go:32-39` `ON CONFLICT (workspace_id, name) DO UPDATE`。
- 修复：`SkillService.Create` 先 `GetSkillByName`，命中即返回 409（与 Update 对齐）；**保留** `ImportService.Refresh→persist` 依赖的 upsert（或在 persist 内部走独立 update）。补 Create 重名单测。

**P1-3 `POST /api/work-items/{id}/squad-evaluated` 生产恒 500（死端点）**
- 现象：E1 实测 500 "evaluation recorder not wired"。
- 根因：`cmd/orkome-server/wiring_orchestration.go:340` `NewSquadService(repo, nil)` 传 nil recorder；engine 侧 `SquadEvals: bench`（:85）正常。集成测试 `squad_integration_test.go:73` 自带 `poolRecorder`，永远测不出生产接线缺失。
- 修复：wiring 复用 `bench`（实现 `RecordSquadEvaluation` 的 workBenchAdapter 已存在，`wiring_orchestration.go:394`）传入 `NewSquadService(repo, bench)`；或若该 HTTP 口确认废弃，从 routes 与 API 文档摘除。

**P1-4 agent-skill 绑定面忽略工作区（跨区越权写）**
- 根因：`service.go:658-676` 五个绑定方法收 `ws` 不用；`pgrepo/repo.go:182-214` 的 Add/SetEnabled/Remove/Set 均无 `ag.workspace_id` 限定（`ListAgentSkills` 有）。`routes.go:70-74` 仅 X-Workspace-Id 成员门。A 区成员持 B 区 agent uuid 即可为他区 agent 增删/启停技能（engine 注入随之改变）。 squads 同类操作全部有 ws 限定（对比 `pgrepo/repo.go:1902-1911`），系遗漏。
- 修复：repo 五方法加 `JOIN agent ag ON ag.id=$1 AND ag.workspace_id=$x`（或 service 层先 `GetAgent` 校验），非法绑定返回 404。注意 `SetAgentSkills` 对 foreign skill uuid 同样放行（FK 只查 skill 存在），需一并校验 skill 归属。

### P2（11）

1. **小队名无长度上限**：T3 300 字符 201；服务层 `service_squad.go` Create/Update 无校验（技能为 ≤100，`service.go:86`），前端 maxLength=100 仅 UI。建议服务层统一 ≤100。
2. **leader 语义三处不一致**：创建带 leader 不入成员（L1）、更新设 leader 自动以 role='leader' 入成员（L2，`repo.go:1806-1814`）、创建 leader 指向不存在/跨区 agent → 500 或 201（T4）。建议创建路径并入 SquadService 并复用 Update 的 GetAgent 校验 + joinLeader。
3. **路径参数不做 uuid 预校验**：`GET /api/squads/not-a-uuid`（T12）、`skills/add` 非法 skill_id（S19）→ 500。建议 handler 层 `uuid.Parse` 失败返 400，或 pgxerr 统一映射 invalid_input。
4. **文件路径接受控制字符**：S7 实测 U+0008 入库（`domain/rules.go:55-66` CleanPath 只管 `..`/前导 `/`/反斜杠归一）。建议拒绝 `< 0x20` 与 DEL。
5. **"新建文件"可静默覆盖 SKILL.md**：S9 实测 200 覆盖主文件（upsert 语义无主文件护栏；删除有护栏 `service.go:204-206`）。建议 `path==SKILL.md` 且已存在时要求显式 `overwrite:true` 或前端改名提示。
6. **编辑器无并发冲突保护**：`manifest_sha256` 已返回但未用于 If-Match 类校验（`routes.go:164-179`）。两窗口 last-write-wins。建议 PUT 带 `base_manifest_sha256` 不匹配返回 409。
7. **frontmatter 描述清空不回写**：`service.go:174` 仅在 `fm.Description != ""` 时同步，技能描述滞留旧值。
8. **归档成员进入 squadBrief 与 consult 工具**：`GetSquad`（`pgrepo/repo.go:1371-1385`）不过滤 `archived_at`；编排模式会为归档成员构建模型工具（`engine.go:223-231`）。建议 briefing/工具装配过滤 archived，或在 brief 中标注"已归档"。
9. **移除成员/小队删除的静默幂等**：T11 二次移除仍 200；`RemoveSquadMember`（`repo.go:1915-1921`）不区分"本来就不存在"。建议返回 404 或响应体带 `removed:false`。
10. **search 通配符未转义**：`repo.go:73-75` ILIKE 直接拼 `%q%`（S12 `q=%` 全量命中）；同工作区低危，建议转义 `%_\`。
11. **API 文档与前端失真**：`inventory-api-2026-10-12.md:433` PUT agents/{id}/skills 标"前端是"，`web/src/api/skills.ts` 无封装；另 squads 列表创建弹窗借用 `skills.*` 文案 key（`_app.squads.index.tsx:171,184`）。

## 8. 测试覆盖评估

- **skills：良好。** `app/service_test.go` 15 例（sanitize/tamper fail-closed/MainByName/UpsertFile/DeleteFile/GitHub tarball+token/skills.sh 门/裸文件/SSRF/拉取上限/refresh 双态）；`domain` 6 例；`pgrepo/b18_integration_test.go` 3 例（需 TEST_DATABASE_URL，覆盖导入护栏/CRUD 绑定/渐进披露源）；`routes_test.go` 3 例。缺口：Create 重名 upsert 行为无断言（P1-2 因此漏网）、控制字符路径无断言。
- **squads：中上。** `app/service_squad_test.go` 3 例（状态推导/评估门禁/角色校验）；`http/squad_integration_test.go`（成员端点/状态推导/评估端点含防伪与幂等）；`http/handlers_squad_crud_test.go`（GET/PUT/删除闸门/无服务 fallback）；`engine/tools_squad_test.go` 9 例（派发归属/准入/防伪）；`workers/b12_squad_test.go`。缺口：**创建路径零覆盖**（P1-1/P2-1/P2-2 全在创建路径）、生产 recorder 接线无冒烟（P1-3）、ListSquads 无测试。
- **结构性教训**：P1-1/2/3 全部位于"测试走了 A 路径、生产走 B 路径"的缝里（handler 直连 repo vs 服务；测试自带 recorder vs 生产 nil）。建议对 wiring 层加一条装配断言测试（反射检查非 nil 依赖）。

## 9. 修复优先级建议

1. P1-1 + P1-2（各 ≤10 行，用户可直接踩到，且修复模式相同：Service 层重名检查）
2. P1-3（wiring 一行：`NewSquadService(repo, bench)`；或摘除端点）
3. P1-4（repo 五方法补 ws 限定）
4. P2 按序：2（leader 一致性）→ 5（SKILL.md 覆盖护栏）→ 3/4（输入 hygiene）→ 其余

## 附：实测残留

`inventory-ws` 内保留 fp08 前缀数据：`fp08-squad`（c05de78b）、`fp08-squad-leader-test`（71c5675d，含 1 leader 成员）、300 字符名小队（60227a42）、`fp08-skill`（1f539e5d，主文件为 S9 覆盖后内容）；控制字符文件与 agent 绑定已在测试中清理。
