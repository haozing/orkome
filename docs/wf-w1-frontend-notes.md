# W1 前端交付笔记（wf-w1-frontend）

> 日期：2026-10-13 · agent：W1 前端（项目流程定义编辑器）
> 依据：docs/project-workflow-design-2026-10-13.md §6.2 触点 1 + docs/wf-interfaces-frozen.md §3/§6
> 后端 W1 并行开发中，本波次**未联调**——按冻结契约 §3 的 JSON 形状实现，联调留给 W3。

## 1. 文件清单（严格限定）

| 文件 | 动作 | 说明 |
|---|---|---|
| `web/src/api/workflow.ts` | 新建 | 契约 §6 八函数 + WorkflowDefinition/WorkflowStage/WorkflowState 类型（手写自 §2/§3，无 any） |
| `web/src/components/workflow/workflow-editor.tsx` | 新建 | 编辑器全部 UI（单文件，未拆子组件文件——体量可控） |
| `web/src/i18n/locales/zh.ts` `en.ts` | 追加 | 仅追加 `workflow.*` 键段（zh 984-1029 行 / en 对应段），既有键零改动 |
| `web/src/components/project-nav.tsx` | 侵入 | 挂载点（见 §2） |

## 2. 挂载点选型与理由

**结论：`project-nav.tsx` 侧栏项目行内的 Workflow 图标按钮 → Dialog 承载 `WorkflowEditor`。**

理由（读码后裁决）：

1. **项目没有详情页**。全部 routes 里无 `_app.projects.*`；项目在 UI 里只存在于两处：侧栏
   `ProjectNav`（行内 Link 直跳 `/work-items?project=<id>`）和工单列表的项目筛选下拉。
   "项目详情页挂载"无从谈起。
2. **为什么不新建 `/projects/$id` 路由**：任务书限定"其他文件一律禁碰"，而新建路由文件必然
   触发 TanStack Router 插件再生成 `src/routeTree.gen.ts`（构建期产物，属于禁碰清单外延）。
   为一个编辑器引入新路由页 + 生成文件改动，侵入面大于收益。
3. **Dialog 挂载的最小性**：project-nav 只加了 ① 一个 `useState<string|null>` ② ProjectRow
   一个 hover 显隐的图标按钮（与既有 Star 按钮同款交互）③ 一个 `WorkflowDialog` 组件。
   编辑器组件本身不感知载体（只吃 `projectId` prop），W3 若做项目详情页，把
   `<WorkflowEditor projectId={...}/>` 平移进页面区块即可，Dialog 壳整体删除。
4. 按钮 aria/title 走 `workflow.openEditorAria`；Dialog 标题 `workflow.editorTitle`。

## 3. API 层说明（契约对齐）

- 八个导出与契约 §6 逐字对齐；`upsertWorkflow` 走 **PUT**（契约 §3），`setWorkflowEnabled`
  按布尔拼 `enable|disable` 路径。
- **类型手写自 §3**：`WorkflowStage{position,name,exit_status_key,executor_type,executor_id?,instruction}`、
  `WorkflowDefinition{id,workspace_id,project_id,name,trigger_mode,entry_stage_position,enabled,created_by,created_at,updated_at,stages}`、
  `WorkflowUpsertInput{trigger_mode,entry_stage_position,stages}`、`WorkflowState`（§2 全字段，
  `finished_at?` 缺省=在途）。响应包裹 `{workflow:...}` / `{state:...}` 均按 §3 解包。
- **mock**：模块级 `USE_MOCK = false` 开关 + `mockStore`（Map）。W1 后端不可联调期把开关置
  true 即可用假数据驱动编辑器（首次 GET 播种三阶段演示流程）。**W3 联调时删除整段 mock**
  （文件内有醒目注释标记）。工作项侧四函数 mock 态直接抛错/返回 null（W1 无状态面）。
- `name` 字段：§3 PUT 请求体没有 name，但 `WorkflowDefinition` 返回里有。编辑器对已有定义
  渲染 name 输入框但**不随 PUT 发送**（后端管理流程名；W3 联调时若后端支持改名再加进
  `WorkflowUpsertInput`，属契约漂移需主会话裁决）。

## 4. 编辑器行为

- **空态**：引导文案 + 「创建流程」按钮；点击落一个空白阶段卡进入编辑（保存走同一 PUT=创建，
  符合"upsert 一次成型"语义）。
- **阶段卡**：阶段名 / 出口状态下拉（`GET /api/work-item-statuses`，**过滤 category=closed**——
  契约 §1.1 出口态候选只允许 unstarted|started|done）/ 执行者三态下拉（agent/squad/none，
  选 agent/squad 出二级选择器：`listAgents` 过滤 archived / `listSquads`）/ 指令 textarea
  （占位符提示 `{{title}}`/`{{number}}`）。
  - i18next 初始化为 `escapeValue: false` 且 v26 默认 `skipOnVariables: true`：提示文案里的
    `{{title}}` 在**未传变量**时按字面保留（已读 i18next dist 源码确认），不会被打空。
- **排序**：上移/下移本地换位 + 入口 radio 跟随原阶段换位（按 position 换算）；新增/删除本地
  生效；**position 只在提交时重算**（数组下标+1），无断档可能。
- **顶部**：trigger_mode（auto/manual 下拉，文案说明语义）、入口 radio 标记在每张卡上、启用/
  停用 checkbox **直接调 enable/disable**（不等保存）。
- **校验**：前端先挡——空阶段名（`workflow.stage.nameRequired`）、agent/squad 未选 executor
  （`workflow.stage.executorRequired`），逐卡标红；后端 4xx 走 `ApiError.message` 原样展示
  （position 断档/重名/exit_status 不存在等由后端裁决）。
- **删除流程**：行内 confirm 条（红底，含"在途单子脱离"警示）→ DELETE → 查询失效回空态
  （EditorBody key 绑定 workflow.data.id，删后 key 翻转重挂为空态分支）。

## 5. 门禁输出（2026-10-13）

```
pnpm -C web build  → ✓ built in 1.62s（仅既有 chunk>500kB 警告=已知噪音）
pnpm -C web test   → Test Files 25 passed (25) · Tests 237 passed (237)
pnpm -C web lint   → 12 problems (2 errors, 10 warnings) —— 与基线完全一致：
                     errors = project-nav.tsx:61（原 :58，因新增 import/useState 行平移，
                     代码未动）+ notification-detail-dialog.tsx:35（预存）
                     workflow-editor.tsx / api/workflow.ts 零 finding
```

## 6. 给 W3 前端的交接

1. **消费面现成**：`getWorkItemWorkflow/startWorkItemWorkflow/restartWorkItemWorkflow/detachWorkItemWorkflow`
   已按 §3 实现并导出，侧栏「流程」区块（state + ●●○○ + transition 折叠 + restart/detach）直接用。
   `WorkflowState.stages` 内嵌升序阶段（§3 注释明示），进度点不用另拉定义。
2. **删除 mock 段**：`api/workflow.ts` 里 `USE_MOCK` 至 `mockSetEnabled` 整段是 W1 联调脚手架，
   W3 接真后端时删除并把各函数的 `if (USE_MOCK)` 分支拆掉。
3. **挂载点迁移**：若 W3 做项目详情页，把 `WorkflowDialog`（project-nav.tsx 末尾）删掉，
   `<WorkflowEditor projectId>` 平移进页面；`workflow.openEditorAria/editorTitle` 两个键可顺手复用或清理。
4. **i18n 契约**：`workflow.*` 段 W1 已占（zh/en 同构），W3 追加消费侧键（如 `workflow.state.*`）
   时沿用同段，勿另起前缀。
5. **已知未验证**（联调清单）：PUT 无 `name` 字段后端是否接受 name 编辑入口；exit_status 过滤
   closed 与后端校验文案一致性；`{workflow:null}` 首次 GET 的 404-vs-null 语义（前端按 §3
   `{"workflow":null}` 处理，若后端回 404 需在 api 层归一）。
