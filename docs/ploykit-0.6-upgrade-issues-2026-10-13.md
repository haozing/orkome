# ploykit v0.4.x → v0.6.x 升级问题清单（orkome 升级实测）

> 升级执行：2026-10-13，orkome commit `c06e3d4`（后端 v0.4.1→v0.6.1，前端 v0.4.0 源码别名→@ploykit/*@0.6.0 npm 包）。
> 总体评价：**Go 侧 API 完全向后兼容**（138 个引用文件零破损、全量测试绿），值得点赞。问题集中在 **npm 包的发布产物质量**——下面 5 项按严重程度排序，每项附复现证据。

---

## Issue 1（高）：npm 包 `files` 不含 `src`，但 `exports.development` 指向它——`development` 条件实际不可用

**现象**：四个包（client/hooks/runtime/ui）的 `package.json` 都声明：

```json
"exports": {
  ".": {
    "development": "./src/index.ts",   // ← 指向 src
    "types": "./dist/index.d.ts",
    "default": "./dist/index.js"
  }
},
"files": ["dist"]                       // ← 只发布 dist
```

`files` 不含 `src`，所以从 npm 安装后 `development` 条件指向的文件**不存在**。消费者（vite 的 `resolve.conditions: ['development']`、jest 的 `customExportConditions`）一旦启用 development 条件就直接 `ERR_MODULE_NOT_FOUND`。

**影响**：`development` 条件形同虚设且是颗地雷——0.4 时代我们靠它从源码消费，升级时第一反应就是用它，踩坑后发现 src 根本没发布。

**建议**：二选一并文档化：① `files` 加 `"src"`，让 development 条件真实可用（推荐——源码消费对 monorepo 调试很有价值）；② 删掉 `exports.development` 条件，避免消费者误用。

---

## Issue 2（高）：dist 产物内部使用无扩展名相对导入，违反自身 ESM 声明

**现象**：`@ploykit/client/dist/index.js`（包声明 `"type": "module"`）：

```js
export { api, apiFetch, ... } from './api';   // ← 无扩展名
export { WsClient } from './ws';
```

Node 原生 ESM 解析器**要求**相对导入带扩展名。实测后果分两层：

- **vitest（jsdom）+ pnpm 符号链接布局**：node 解析器从 `.pnpm/.../node_modules/@ploykit/client/dist/` 出发找不到 `./api` → 全部 API 层测试文件加载失败（`Cannot find module '...\dist\api'`）。我们的修复：vite.config 的 `test.server.deps.inline: [/@ploykit\//]` 把包内联交 vite 转换（vite 自身的 extension 补全能兜住）。
- **纯 Node ESM 消费者**（SSR、node scripts）会同样炸。

**建议**：构建输出补扩展名（`'./api.js'`）。tsc 的 `moduleResolution: node10` 不补，但发布 `"type": "module"` 的包按规范必须带。或至少在 README 声明「仅支持 bundler 解析，不支持 Node 原生 ESM」。

---

## Issue 3（中）：`exports` 只暴露 `"."`，深导入（deep import）被堵死但下游有真实需求

**现象**：`exports` 没有 `"./api"`、`"./ws"` 等子路径声明。下游需要两个真实场景的深导入：

1. **测试钩子**：`__resetApiForTests`（CSRF 状态重置）不在任何公开子路径里，只能靠 vite alias 把 `@ploykit/client/api` 硬指到 `dist/api.js` 绕过 exports；
2. **树摇/绕过 barrel**：`@ploykit/hooks` 的 `ws-query-bridge` 叶子（我们只为了一个函数 import 整个 barrel，会把 Provider 全家拖进 bundle）。

0.4 的源码形态下这不是问题（文件系统路径随便指）；0.6 的 exports 封装后全靠下游写 alias 绕，且 pnpm 布局下绕法很脆。

**建议**：`exports` 显式声明高频子路径，例如：

```json
"./api": { "types": "./dist/api.d.ts", "default": "./dist/api.js" },
"./ws": { ... },
"./lib/ws-query-bridge": { ... }
```

---

## Issue 4（中）：`@ploykit/runtime` 声明 peer `react-router@^7`，但下游可能用 `@tanstack/react-router`

**现象**：`@ploykit/runtime` 的 `peerDependencies` 含 `react-router: ^7.0.0`。orkome 的路由是 `@tanstack/react-router`（未装 react-router），pnpm 安装时对缺失 peer 给了警告；如果下游按 peer 提示去装 react-router，会引入一整个用不上的路由栈。

**疑问**：runtime 对 react-router 是真依赖还是仅类型引用？若只是类型，建议改为 `peerDependenciesMeta: { "react-router": { optional: true } }` 或挪到 devDependencies（`import type` 不需要 peer）。

---

## Issue 5（低）：Go 侧升级体验很好，但缺变更日志

v0.4.1→v0.6.1 跨了两个 minor，带来了大量新能力（MFA TOTP、`MountMFA`、workspace 的 `StrongGuard`、webhooks 的 `doc.go`、audit/quota 新包），**兼容性也确实做到了零破损**——但全过程我们只能靠 diff vendor 目录反推：

- 046/047 两个新迁移会随启动自动执行（对下游库做 DDL），升级公告里应该提示；
- `workspace/adapters/http/routes.go` 新增 `StrongGuard` 挂载点、identity 新增 `MFAChangeGuard`——这些是接线点扩展，下游需要知道才能消费；
- 建议仓库维护 `CHANGELOG.md`（至少 minor 级别一行一段），或 GitHub Release 带 notes。**这次升级零破损属于运气好（我们没用被改动的面），不是设计保证**——没有 changelog，下一次跨版本升级依然只能靠盲 diff。

---

## 附：升级数据（供参考）

| 项 | 数值 |
|---|---|
| Go 引用面 | 138 个文件（pg/webx/pgmigrate/migrations/identity/egressx/workspace/storagex/redactx/authz/webhooks/notify） |
| Go 编译破损 | 0；全量测试 0 失败 |
| npm 消费面 | client ×35 导入、hooks ×2、ui ×0 |
| 前端验证 | typecheck / 237 测试 / build 全绿（含 vitest inline 修复后） |
| 新框架迁移 | 046 `user_mfa`(+backup_code)、047 `user.updated_by`，随启动自动落库 |
| 下游为升级新增的代码 | vite alias 全量重写 + `test.server.deps.inline` + tsconfig paths 重写（均为绕 Issue 1/2/3） |
