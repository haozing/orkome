# FP-15 审计：pack 深链与 slot 前端面（ui-token 三段式 / PackEntries / PackSlotFrame / slots）——四波 C2 交付物专项（2026-10-12）

> 范围：铸票端点与票内容、票据校验链（pack 侧回验）、模板变量替换全链、PackEntries 侧栏、PackSlotFrame（sandbox/postMessage/超时/兜底）、slots 数据流（use-pack-slots 缓存与 tab key）、票据滥用面（跨 workspace / workItemId 伪造）。
> 方法：七步审计法。环境：后端 :8080 实测（cookie jar /tmp/fp15-cookies.txt，inventory-test@orkome.dev / inventory-ws）+ 全量代码审读，未开浏览器（组件行为以既有测试+代码裁决）。
> 前置：`docs/feature-audit-batch1-2026-10-12.md`（FP-04 P1-3 引用勿重复）、`docs/inventory-api-2026-10-12.md` §13.6、`docs/handoff-c2-slots-2026-10-11.md`、`docs/handoff-d4-golden-thread-2026-10-11.md`。
> 边界：只读代码，未改任何生产文件；未做 git add/commit。

---

## 结论速览

| 维度 | 结论 |
|---|---|
| P0 / P1 / P2 | **0 / 3 / 7** |
| 一句话 | 票据本体（签名/单次使用/TTL/隔离）扎实且实测全过；但**三段式的第三段（pack 侧回验） architecturally 不可达**——票对 pack 是「无法验证的字符串」，且 url_template 全链无 scheme/origin 校验，同源逃逸面敞开 |
| 票据安全实测 | 单次使用 ✓ / 60s 过期 ✓ / 重放 401 ✓ / 匿名消费被 CSRF 拦 ✓ / 票不可当 Bearer 用 ✓ / 非成员铸票 403 ✓ |
| ui-entries 隔离 | 无头 400 / 成员 200 / 非成员 403 / 匿名 401 四态全过，fence 硬 |
| 铸票频率 | **无限流**：15/15 burst 全 201；ui_token 表**无 GC**（索引叫 `_gc`，清理任务不存在） |
| C2 交接诚实度 | 与代码逐项吻合（origin 退化、探测豁免、--frame-origin 默认关）；已知坑 4 项复核成立，另新增其未提的 3 项（consume 不可达 / 无 GC / scheme 未校验） |
| FP-04 P1-3 完整裁决 | 多 pack 无冲突；**单 pack 重复声明同一 slot 标识在 provision 与 service 双侧都不去重 → 官方安装路径即可触发**前端 tab key 冲突，维持 P1（潜伏） |

---

## 1. 全链梳理：铸票 → 挂载/跳转 → 回验的完整时序（含断裂点）

```
[铸票] 前端三处消费方各铸各的票（60s，不缓存）：
  PackEntries 侧栏      pack-entries.tsx:29-34      POST /api/ui-tokens {plugin, workspace_id}
  PackSlotFrame 挂载    pack-slot-frame.tsx:54-61   queryKey 含 workItemId，staleTime:0 gcTime:0 → 重挂载必重铸
  JobHandleCard 深链    task-tool-renderers.tsx:391-394  每次点击现铸
    ↓ 后端：handlers_uitoken.go:12-38
    session Principal → GetMember(ws, user) 成员校验（:28-31，非成员 403 实测）
    → repo InsertUIToken（jti 主键，60s 过期，1033 表）
    → HS256 JWT {jti: uit_<32hex>, sub, wsid, plugin, iat, exp}（service_uitoken.go:62-65）
    → 201 {token, expires_in:60}
[挂载/跳转] 模板变量由前端渲染时替换（平台铸票时不注入 workItemId/jobId——设计如此）：
  PackSlotFrame    renderSlotUrl   {uiToken}/{workspaceId}/{workItemId}（:28-38，均 encodeURIComponent）
  PackEntries      renderURL       {uiToken}/{workspaceId}（:10-14）→ window.open 新标签
  JobHandleCard    renderConsoleUrl {uiToken}/{workspaceId}/{jobId}（task-tool-renderers.tsx:142-148）→ window.open
[回验] ★ 断裂点（P1-1）：设计意图是 pack 拿票换上下文（POST /api/ui-tokens/consume →
  {user_id, workspace_id, plugin}），但该端点挂在 auth(webx.RequireAuth(...)) 后
  （identity routes.go:42）+ 全局 CSRF 中间件——pack iframe（跨站，SameSite=Lax 会话
  cookie 不随行）与 pack 后端（无会话无 PAT）双双打不进去：匿名 curl 实测 403
  "CSRF token missing or invalid"。而票是 HS256+平台自持密钥签名（service_uitoken.go:42-47
  派生自 cfg.JWTSecret），pack 无法离线验签。=> pack 侧校验链不存在，三段式实际只有两段。
```

**责任边界现状**：orkome 只管铸（任务书语），pack 侧「怎么验」在设计文档（`pack-frontend-design-2026-10-11.md`）中**通篇没有定义**——grep consume/回换/验票/共享密钥/JWKS 零命中。这不是实现遗漏，是契约空洞（详见 §6 裁决）。

---

## 2. 契约逐字段对齐（含文档漂移确认）

| 端点/字段 | 后端实际（file:line） | 前端实际 | 文档 §13.6 | 判定 |
|---|---|---|---|---|
| POST /api/ui-tokens body | `{plugin, workspace_id}`（handlers_uitoken.go:19-21） | 三处全发 `plugin`（pack-entries.tsx:32 / pack-slot-frame.tsx:22 / task-tool-renderers.tsx:394） | `{pack, workspace_id}` | **文档漂移实锤**：curl 发 `pack` → 400 "plugin and workspace_id required"。已知漂移（盘点记录在案），本审计 curl 复现确认；**修文档**即可，代码无需动 |
| mint 响应 | 201 `{token, expires_in:60}`（:37） | 只取 `token` | 同 | ✅ 匹配。⚠️ `60` 是字面量，与 `UITokenTTL` 常量（service_uitoken.go:16）双源（P2-7） |
| POST /api/ui-tokens/consume | `{token}` → 200 `{user_id, workspace_id, plugin}`（:40-56）；挂 RequireAuth+CSRF（identity routes.go:42） | 前端零调用（设计：pack 回换） | 「票由 pack iframe 内部回换」 | ⚠️ 端点本身实现正确（单次消费+claims 交叉核对），但**目标消费者不可达**（P1-1） |
| JWT claims | `{jti, sub, wsid, plugin, iat, exp}`（service_uitoken.go:62-65）——**无 workItemId**（live 解码实证，exp-iat=60） | — | 未写 claims 形状 | claims 最小化 ✅；workItemId 不在票内是**有意设计**（C2 交接明示），风险面见 §6 裁决 |
| GET /api/ui-entries | `{items:[PackUIEntry]}`，slots 恒 `[]` 非 null（handlers_uientry.go:14-21 + repo COALESCE） | `items ?? []`（packs.ts:35-38 / pack-entries.tsx:19-22） | 同 | ✅ 匹配，隔离四态实测全过（§3） |
| PUT/DELETE /api/ui-entries/{pack} | requireWorkspaceAdmin（handlers_uientry.go:26,45）；slots 可选、缺省清空 | 前端零调用（provision 桥写，合理） | 同 | ✅ |
| 模板变量目录 | — | `{uiToken}/{workspaceId}/{workItemId}`（frame）+ `{jobId}`（JobHandleCard 专用）；PackEntries 的 renderURL **不**替换 `{workItemId}/{jobId}`（侧栏模板按契约不含这俩） | 前端设计 §1.2 | ✅ 一致；注意 probeFrameAncestors 的哑值替换表（main.go:766-769）与前端目录一致 |

---

## 3. curl 实测记录（:8080，/tmp/fp15-cookies.txt）

| # | 用例 | 请求/操作 | 结果 |
|---|---|---|---|
| 1 | 铸票（真实字段名） | POST /api/ui-tokens `{plugin:"fp15pack", workspace_id}` | **201** `{token, expires_in:60}` ✅ |
| 2 | 文档漂移双试 | 同上但 `{pack:"fp15pack",...}` | **400** E_VALIDATION ✅（漂移实锤） |
| 3 | 缺字段 | 缺 plugin | 400 ✅ |
| 4 | 非成员铸票 | workspace_id=随机 uuid | **403** "workspace membership required" ✅ |
| 5 | 票内容解码 | base64 解 payload | `{"exp":…7973+60,"iat":…,"jti":"uit_32db…","plugin":"fp15pack","sub":"7c89…","wsid":"e83d…"}`——**无 workItemId、60s 整** ✅ |
| 6 | 会话内消费 | POST consume（带 cookie+CSRF） | **200** `{plugin,user_id,workspace_id}` ✅ |
| 7 | 重放 | 同票再消费 | **401** "already consumed or expired"（单次使用，pgrepo/uitoken.go:20-23 原子 UPDATE）✅ |
| 8 | 匿名消费（模拟 pack 侧） | 无 cookie POST consume | **403 CSRF token missing or invalid**——**P1-1 铁证**：pack 侧根本进不了鉴权层 |
| 9 | 票当 Bearer 用（提权探针） | `Authorization: Bearer <ui-token>` 打 /api/workspaces/{ws}/members | **401** ✅ 票对 orkome API 零权限（Bearer 通道只认 `ork_` PAT，webx/auth.go:125-137） |
| 10 | 过期票 | 铸票后 61s 再消费 | **401** "invalid ui token"（JWT exp 先拒）✅ |
| 11 | 铸票频率 | 15 连发 burst | **15/15 全 201**——无限流（P2-2） |
| 12 | ui-entries 隔离四态 | 无头/带头/他区/匿名 | 400 / **200 `{"items":[]}`** / 403 / 401 ✅ fence 硬 |
| 13 | ui_token 表状态 | 直查 PG | 有过期未清理行残留；全库无任何 DELETE 路径（P2-1，idx_ui_token_gc 索引在而 GC 不在） |

---

## 4. 代码质量

### 4.1 PackSlotFrame（pack-slot-frame.tsx）

- **sandbox 三权限裁决**：`sandbox="allow-scripts allow-same-origin allow-popups"`（:128）。跨源 pack 页下这是标准组合（allow-same-origin 让 pack 用自己的存储/cookie，allow-popups 供其开控制台），**组合本身不是缺陷；缺陷是平台不强制「内容必须跨源」**——url_template 全链无 scheme/host 校验（§4.4 + P1-3），相对路径模板（API 文档 §13.6 自己的示例 `"/packs/github?wi={id}"` 就是这个形状！）会渲染成**同源 iframe**：allow-scripts+allow-same-origin 组合下沙箱对同源内容形同虚设（帧内脚本可直取父 DOM、读 `orkome_auth` cookie 之外的本地态、删自身 sandbox 属性）。`javascript:` 模板更直接：iframe src 的 javascript: URL 在 about:blank 初始文档中执行、继承**父源**，配合三权限 = 确定的父 DOM 接管（Chromium 实证行为）。→ P1-3。
- **postMessage origin 校验退化路径**（:74-84，C2 交接坑 4 自述成立）：`origin = new URL(src).origin`——①模板相对路径 → `new URL` **throw** → origin=null → `if (origin && …)` 短路 → **接受任意 origin** 的消息；②`javascript:`/`data:` 模板 → `URL.origin` 返回字符串 `"null"`（truthy！）→ 只接受 ev.origin==="null"（不透明源帧）的消息，同样非预期。两条退化路径的爆炸半径都只是「高度被伪造」（钳制 [1,8192] 内），**非安全漏洞，但协议可信度归零**，且无测试锚定（§7）。
- **3s 超时语义**：timer 只在 effect 周期内存在，收到首个合法 resize 即 clearTimeout 定格（:88-91）；无消息回落声明高度（:80-82）。卸载清理正确：clearTimeout + removeEventListener 成对（:94-97）。✅ 与 8 用例中的超时/定格两测一致。
- **铸票失败态**：isError → `slots.mintFailed` 错误卡（:105-115）；loading 态声明高度占位（:116-120）。✅
- **深链兜底**：右上角固定 external-link（:131-143），href 与 iframe src 同 token。**设计注意**（记录非缺陷）：一旦回验链修通（票单次消费），iframe 加载消费掉票后，同票的外链按钮就指向死票——修复 P1-1 时兜底链接应改走「重新铸票」或接受此损耗。

### 4.2 PackEntries 侧栏（pack-entries.tsx，挂载 _app.tsx:126）

- **空列表 `return null` 活体裁决**（:39）：无 pack 时侧栏**什么都不渲染**——没有区块标签、没有空态、没有「去 MCP 页安装」引导（对照同侧栏 AI Team 区有 `nav.aiTeam` 标签，_app.tsx:128）。行为可接受（侧栏减负），但三态不可分辨：**无 pack / 加载中 / 加载失败**（query error 时 `entries.data` 为 undefined，同样走 null）用户看起来一模一样。裁决：**合理但欠一档**——至少 error 态该有提示（P2-3）。
- **jump mutation 无 onError**（:27-38）：铸票失败点击无任何反馈（JobHandleCard 有 `taskMsg.openConsoleFailed` 兜底，侧栏没有——同功能两种质量）。`disabled={jump.isPending}` 连坐禁用全部按钮（轻）。→ P2-3。
- **async window.open 弹窗拦截风险**：`onSuccess` 在网络往返后才调 `window.open`（:37），脱离用户手势调用栈——Safari/严格弹窗设置下可能被拦（JobHandleCard 同构，task-tool-renderers.tsx:396）。Chrome 的 5s transient activation 窗口内通常放行，故列 P2 观察项。

### 4.3 slots 数据流：FP-04 P1-3 完整裁决

- **数据流**：`usePackSlots`（use-pack-slots.ts:17-35）拉 `['pack-ui-entries', workspaceId]`（staleTime 60s）→ select 展平 (pack, slot) 对 → 详情页 `_app.work-items.$id.tsx:82` `slotTabs.find(s => s.key === search.tab)` → :100-104 渲染 TabsTrigger（key=value=`pack:<pack>`）→ :117-119 activeSlot 挂 PackSlotFrame。链路本身正确、有 5 用例 tabs 测试锚定。
- **多 pack 场景：无冲突**。key=`pack:agentblog` vs `pack:github` 天然互异，两 pack 各一个 `workitem.detail.tab` 是 v0 主形态，实测代码路径无问题。
- **单 pack 多 slot 场景：确认冲突，且可达路径比 FP-04 记载的更宽**。FP-04 P1-3 说「当前无多 slot pack 数据，未爆发」并暗示靠后端收口；本次复核：**两侧校验都不去重**——`service_uientry.go:69-82` validateSlots 逐条查合法性不查重（C2 交接坑 5 自述），`pack-provision/main.go:199-219` validateV1UISlots 同样不查重。即 **pack.yaml 里把 `workitem.detail.tab` 写两遍（作者笔误级别）即可通过官方安装路径落库两行 slots** → 前端 flatMap（use-pack-slots.ts:27-33）生成两个同 key/value 的 tab：React duplicate key 告警、`find`（$id.tsx:82）永远命中第一个、第二个 slot 永不可达且无任何报错。
- **裁决**：维持 **P1（潜伏）**，但定性从「前端 key 设计隐患」升级为「**前后端三层（provision/service/前端键）共同缺一条『每 pack 每 slot 标识至多一条』不变量**」。修法（三层取一，推荐 ①+③）：① service validateSlots 查重拒绝（409/400）；② provision validateV1UISlots 同步查重（fail the install，报错更早）；③ 前端键改 `pack:<pack>:<slot>`（use-pack-slots.ts:14,31 + zod regex `_app.work-items.$id.tsx` 放行同步放宽）——③ 单做会把坏数据从「不可见」变「可见但乱」，故 ① 是治本。
- **双缓存键**（盘点已知 P2，本次给完整裁决）：`['pack-ui-entries', workspaceId]`（侧栏/槽/渲染器三处共用）vs `['pack-ui-entries-page']`（packs-sections.tsx:36，MCP 页）。①失效不联动：装/卸 pack 走 CLI，web 侧最长 60s（staleTime）陈旧，可接受；②**page 键无 workspace 维度**：切工作区时 MCP 页窗口期显示上个工作区的 pack 列表（api client 按当前 ws 发头、数据服务端 fence 硬，纯显示层错位，自愈于 refetch）；③page 键无 staleTime 每挂载必拉（轻）；④PackEntries 内联 queryFn（pack-entries.tsx:18-23）与 `listPackEntries`（packs.ts:35-38）重复实现同一读取——将来 envelope 变更只改一处必漂移。→ P2-4。

### 4.4 双实现与死代码

- `webx/auth.go:65-96` SignUIToken/VerifyUIToken：**零调用方**（迁移遗留，service_uitoken.go:19-20 注释自述「字段名与退役实现匹配以保票可验」）。两套同型 verify 并存，改 claims 只改一处的漂移面。→ P2-6。
- identity `app/service_test.go` 无 Mint/Consume 单测：服务层 Clock 已注入（service_uitoken.go:42-47）却无用例消费——过期消费、DB/JWT claims mismatch 分支（:81-83）、篡改签名、alg 混淆全靠 HTTP 集成测试的 happy path + 重放两例，覆盖单薄（§7）。

---

## 5. 后端正确性

| 项 | 结论 | 证据 |
|---|---|---|
| 票签名与校验 | HS256 + `WithValidMethods(["HS256"])` + `WithExpirationRequired()` + 全 claims 非空检查（service_uitoken.go:87-103）；消费时 DB 行与 JWT claims 三字段交叉核对（:81-83） | 代码 + 用例 5/6/7/10 |
| 单次使用原子性 | `UPDATE … WHERE consumed_at IS NULL AND expires_at > now RETURNING`（pgrepo/uitoken.go:20-23）——并发消费只有一赢 | 用例 7 |
| 成员校验 | mint 前置 GetMember（handlers_uitoken.go:28-31）；**workspace_id 取自 body 而非信任客户端声明**，非成员 403 | 用例 4 |
| claims 最小化 | 只有 user/ws/plugin 三标识，无 Email/Role 等超集 | 用例 5 |
| 票与平台 API 的隔离 | ui-token 不能当 Bearer（auth.go:127 只认 `ork_` 前缀）→ 票泄漏爆炸半径=0（对 orkome 而言） | 用例 9 |
| ui-entries 工作区隔离 | memberMW（orch routes.go:197-227：X-Workspace-Id 头 + MemberCheck + 回填 Principal.WorkspaceID）+ repo 层 ws 过滤 + repo_uislots_test.go 跨 ws 不可见断言 | 用例 12 |
| 铸票频率限制 | **无**。无 RateLimited 映射路径被用上；15/15 burst。单成员可无限铸：行膨胀（与 P2-1 复合）+ 将来 consume 放开时的爆破面 | 用例 11 |
| ui_token 生命周期 | 签发插入 → 消费置 consumed_at → **终点**。过期/已消费行无人删：`idx_ui_token_gc`（1033_ui_tokens.up.sql:9）暗示曾有 GC 计划，清理任务不存在。每次 slot 挂载/侧栏点击永久 +1 行；JWT-exp 先拒的消费路径连 consumed 标记都不落 | 用例 13、DDL、全仓 grep 无 DELETE FROM ui_token |
| slots 校验 | 目录冻结（只认 workitem.detail.tab）+ label/url 必填 + height∈(0,4096] 双侧一致（service_uientry.go:69-82 / provision main.go:199-219）；**缺 scheme/host 校验、缺重复标识去重** | P1-3 / P1-3 关联裁决 |
| frame-ancestors 探测 | 逻辑正确（判定/错误分离、豁免语义明示）；但 ①`--frame-origin` 默认空=探测整体关闭（第一方零阻力=第三方也无阻力）；②探测只看头不看状态码（交接自述）；③`javascript:`/相对模板在 probe 处 `http.NewRequest` 报错→「probe failed」→**豁免保留**——即最危险的模板形态恰好落在豁免通道里 | main.go:557-575, 758-801 |

---

## 6. 安全裁决专节

### 6.1 三段式第三段（pack 侧回验）：裁决 = architecturally dead，P1-1

链路三方各看一眼：
- **orkome 铸票侧**：实现无可挑剔（§5 前四行）。
- **前端挂载侧**：把票放 URL 交给 iframe/新标签，职责完成。
- **pack 消费侧**：拿到票后能做什么？①离线验签——不可能，HS256 密钥是 orkome 的 `cfg.JWTSecret`（wiring_identity.go:102），不共享、无 JWKS；②在线回换——`consume` 挂 `auth(webx.RequireAuth(...))`（identity routes.go:42）+ CSRF：pack iframe 的 fetch 是跨站请求，`orkome_auth` cookie SameSite=Lax 不随行 → 403/401（用例 8 实测 403 CSRF）；pack 后端无会话无 PAT，同样进不去。identity 集成测试 `integration_test.go:377-379` 还把「anonymous consume 401」断言成**预期行为**——这是按实现写测试而非按威胁模型：持有合法未消费未过期票本身即是身份证明（单次使用+60s+DB 原子消费三重自证），再叠会话门等于宣告「此端点只服务 orkome 自己」，而 orkome 自己不需要它。

**修复建议（二选一，改动都在 10 行级）**：
- **方案 A（推荐，最小）**：`consume` 摘掉 RequireAuth，以票自身为凭证（handlers_uitoken.go:40-56 的 handler 本来就不读 Principal，是路由包多余的壳）；CSRF 对无会话调用方本不适用，需在 CSRF 中间件豁免该路径（或保持 401 语义、仅放行 `Authorization: Bearer uit_…` 形态）。同步把 integration_test.go:377-379 的断言反转为「无票 401 / 有票 200」。
- **方案 B（更面向第三方生态）**：铸票改非对称（EdDSA/RS256，私钥签、公钥验）+ 新增 `GET /api/ui-tokens/jwks`；pack 后端离线验签，免一次回程。改动大，建议 v1 再做。

在修复前，**必须**在 pack 契约文档写明现状：「票当前对 pack 不可验证，slot 页不得据 `{uiToken}` 做任何授权判断」。

### 6.2 workItemId 伪造链：平台侧把链路摆清楚，pack 侧责任书（P1-2）

完整攻击链（今天即可走通，无需任何 bug）：
1. 用户 U 是工作区 W1 成员 → 铸票合法（成员校验过）。
2. U 从浏览器地址栏/历史拿到 slot URL（或点右上角「打开」外链按钮自己复制），把 `{workItemId}` 换成**任何值**——另一个工作区 W2 的工单 id 也行（id 是 UUID 难枚举，但工单还有 `number`，且 URL 是用户完全可控的输入）。
3. pack 页收到：`{uiToken}`（只能证明「某会话成员 ∈ W1」，而这条 U 都验不了，见 6.1）+ `workItemId=W2 的工单`。
4. pack 若按 workItemId 出内容 → **跨工作区读**。

**平台侧责任裁决**：orkome 自身无泄露——票不授任何 orkome 数据访问（用例 9），ui-entries/工单 API 的 fence 全硬（用例 12 + FP-04）。**缺口在 pack 侧没有履行职责的工具**：pack 拿到 consume 响应（假设 6.1 修好）只有 `{user_id, workspace_id, plugin}`，要判断「workItemId 是否 ∈ claims.workspace_id」需要一个工作项→工作区归属的校验原语，而 pack 对 orkome API **没有任何凭证**（PAT 是运维/CLI 的）。=> 三件事必须补：
1. 契约文档明确：「pack 页必须自行校验 workItemId 归属 claims.workspace_id，未校验即视为 pack 的越权漏洞」；
2. 平台提供校验原语：最轻是 `GET /api/ui-entries` 风格的 `GET /api/work-items/{id}/workspace`（成员鉴权，pack 侧拿用户会话不可行——所以实际可行形态是 pack 后端持受限于读的 PAT，或平台提供「票内绑定 workItemId」的铸票扩展：mint body 可选 `work_item_id`，落 claims + 铸票时校验该工单 ∈ 该工作区——**推荐后者**，把归属校验收回铸票时刻，pack 只需比对 claims 与 URL 参数一致，无需任何额外凭证）;
3. 在没有 1/2 之前，本功能点的边界陈述必须写进交接/设计文档（当前两份 handoff 都只写了「票据仍只有 workspace 级 claims」半句话，没有展开责任面）。

### 6.3 sandbox 组合与 url_template 校验缺口：P1-3（见 §4.1）

裁决一句话：**allow-scripts+allow-same-origin 不是问题，不校验「内容跨源」才是问题**。装链（provision validateV1UISlots）与写库（service validateSlots）双处补「绝对 http(s) 且 host ≠ orkome origin」（相对/`javascript:`/`data:` 一律拒），`--frame-origin` 运维开关保留（校验 origin 白名单用）；同时在 probe 豁免通道里把「URL 本身不合法（非绝对 http(s)）」从豁免改为剔除——模板不合法是**确证**不合规，不是探测不通。

---

## 7. 测试覆盖

| 测试 | 在案 | 盲区 |
|---|---|---|
| `web/test/components/pack-slot-frame.test.tsx`（8 用例） | 现铸现用/变量替换/sandbox 属性串/resize 调高/非法值(-5)/跨 origin 拒/3s 超时两态/铸票失败/深链常在 | ①**畸形/相对模板 → origin 退化**（任意 origin 消息被接受）零覆盖；②卸载后 listener 清理无断言（cleanup 代码在 :94-97）；③height 上限 8193 拒绝未测（只测了负数）；④`{jobId}` 不在 frame 变量目录（正确）但无「模板含未知变量原样保留」的锚；⑤workspaceId null 分支 |
| `web/test/components/work-item-detail-tabs.test.tsx`（5 用例） | zod 四触点/无 slots 零回归/空响应/pack tab 挂载/ghost 回退 | 多 slot 同 pack 冲突形态无用例（正好放 P1-3 的回归锚）；PackSlotFrame 被 mock，宿主传参（pack/slot/workItemId）断言弱 |
| `web/test/components/task-messages.test.tsx:64-138` | JobHandleCard **只测隐藏三态**（entries 加载中/无匹配/无 ws） | **深链成功路径零测试**（铸票→renderConsoleUrl→window.open）；`{jobId}` 替换无锚 |
| `internal/identity/adapters/http/integration_test.go:336-380` | mint 403/400/201、consume 200/重放 401/匿名 401、路由记账 | 过期消费（Clock 注入了没用）、claims mismatch 分支（service_uitoken.go:81-83）、篡改签名/alg 混淆、`expires_in` 与 TTL 一致性 |
| `internal/orchestration/adapters/http/handlers_uientry_test.go:88`（5 态） | slots 往返/空数组非 null/未知标识 400/缺 height 400/v0 语义 | **重复 slot 标识不拒**（P1-3 服务端锚缺口）、多 slot 同 pack 的 List 顺序 |
| `internal/orchestration/adapters/pgrepo/repo_uislots_test.go:63` | 1036 列形状/往返/覆盖清空/空数组非 null/跨 ws 不可见/Delete 连删（TEST_DATABASE_URL 门控） | 覆盖良好，无盲区（该测是本功能点后端测试的范本） |
| e2e 金线 ⑦ | ui-entries slots 数据面（模板含 {workItemId}/{uiToken}、height=480） | 铸票→挂载→回验全链无 e2e（回验本身不可达，P1-1 修复后应补） |

---

## 8. 缺陷清单

### P0（0 项）

无。票据本体、隔离 fence、slot 数据面均实测扎实；以下问题均为「契约空洞/防线缺口」，当前无第三方 pack 在消费票，未爆发。

### P1（3 项）

**P1-1 三段式第三段不可达：pack 无法验证票（回验链 architecturally dead）**
- 根因：`internal/identity/adapters/http/routes.go:42` 给 consume 套 `auth(webx.RequireAuth(...))` + 全局 CSRF；票是 HS256 平台自签（`internal/identity/app/service_uitoken.go:42-47,62-65`），无共享验证原语；设计文档未定义 pack 侧验证机制。
- 实测：匿名 consume → 403 CSRF（§3 用例 8）；integration_test.go:377-379 把该行为锚定为预期。
- 后果：`{uiToken}` 对 pack 是无意义字符串，任何 slot 页的「登录态」都是假的；将来第三方 pack 接入时要么裸奔（不验）要么自己发明（危险）。
- 修复：方案 A——consume 去会话门、以票自证（§6.1，10 行级 + 反转一条测试断言）；或方案 B——非对称签名 + JWKS 端点。修复前文档必须写明「票不可验证」。

**P1-2 workItemId 无绑定且 pack 无校验原语：越权读责任完全悬空（pack 侧责任书缺失）**
- 根因：claims 只有 `{sub, wsid, plugin}`（用例 5 解码实证），workItemId 是前端填的 URL 参数（`pack-slot-frame.tsx:28-38`）；pack 侧对「workItemId ∈ wsid」无验证手段（无凭证、无归属查询 API）。
- 攻击链与平台/包装任切分见 §6.2——平台无数据泄露（fence 硬），但链路照此交付等于把越权面整体转嫁给没有工具的 pack。
- 修复：铸票可选绑定 `work_item_id`（mint 时平台校验归属，落 claims；pack 比对 claims 与 URL 参数）；短期至少在 pack 契约文档写明责任与现状（§6.2 三条）。

**P1-3 url_template 全链无 scheme/origin 校验：同源 iframe 逃逸 + javascript: 模板直通**
- 根因：`internal/orchestration/app/service_uientry.go:69-82` 与 `cmd/pack-provision/main.go:199-219` 都只校验非空+height；API 文档示例自身就是相对路径模板；`main.go:766-769` 哑值替换后 `http.NewRequest` 对非 http(s) URL 报错 → 落入「probe failed → 豁免保留」通道（:563-566），且 `--frame-origin` 默认空=探测整体关闭。
- 后果：`url_template:"/packs/x"` → 同源 iframe + `allow-scripts allow-same-origin`（`pack-slot-frame.tsx:128`）= 沙箱失效、父 DOM/会话态可取；`javascript:` 模板在 iframe src 继承父源执行 = 确定的 XSS。前置条件：workspace admin 安装恶意/被劫持 pack——这正是 pack 生态的常规威胁模型。
- 修复：双处校验「绝对 http(s) 且 host ≠ orkome origin」；probe 豁免通道剔除「URL 本身不合法」形态（§6.3）。

### P2（7 项）

| # | 缺陷 | 位置 | 修法 |
|---|---|---|---|
| P2-1 | ui_token 无 GC：过期/已消费行永久残留，`idx_ui_token_gc` 索引在而清理任务不存在；每次挂载+1 行 | `migrations/1033_ui_tokens.up.sql`、全仓无 DELETE | 定时任务 `DELETE FROM ui_token WHERE expires_at < now() - interval '1 day'`（索引自洽） |
| P2-2 | 铸票无限流：15/15 burst 全 201 | `handlers_uitoken.go:12-38` | per-user 简单限频（如 10/min，用既有 apierr.RateLimited 映射 429）；consume 放开（P1-1）前必须做 |
| P2-3 | PackEntries 侧栏静默失败：query error 与空列表同为 return null；jump 无 onError；async window.open 有弹窗拦截风险 | `pack-entries.tsx:27-39` | error 态提示（复用 `slots.mintFailed` 文案族）；jump 补 onError；弹窗拦截观察项（Safari 真机一回访） |
| P2-4 | ui-entries 双缓存键：page 键无 workspace 维度（切 ws 窗口期显示他区列表）+ 失效不联动 + PackEntries 内联 queryFn 与 listPackEntries 重复实现 | `packs-sections.tsx:36`、`pack-entries.tsx:17-23` vs `use-pack-slots.ts:20`、`task-tool-renderers.tsx:384` | page 键加 workspaceId 维度；PackEntries 改用 `listPackEntries` |
| P2-5 | 测试盲区群（§7 表）：origin 退化路径、listener 清理、JobHandleCard 深链成功路径、过期消费/claims mismatch/篡改签名、重复 slot 标识 | `pack-slot-frame.test.tsx`、`task-messages.test.tsx:64-138`、`service_test.go`、`handlers_uientry_test.go:88` | 按 §7 盲区列补用例；P1-1/P1-3 修复时同步锚定新行为 |
| P2-6 | ui-token 双实现并存：webx SignUIToken/VerifyUIToken 零调用方，与 identity service 同型，漂移面 | `internal/platform/webx/auth.go:65-96` | 删除或改 delegate 到 identity 实现（保留注释说明迁移兼容语义已由 service_uitoken.go 独立承载） |
| P2-7 | mint 响应 `expires_in:60` 字面量与 `UITokenTTL` 常量双源 | `handlers_uitoken.go:37` vs `service_uitoken.go:16` | 常量导出引用（`int(UITokenTTL.Seconds())`） |

### 修复顺序建议

1. **P1-1**（consume 自证 + 测试反转 + 文档补「票不可验证」现状）——三段式补全的最后一段，也是 P1-2 的前提。
2. **P1-3**（双处 scheme/origin 校验 + probe 豁免通道收紧）——第三方 pack 到来前必须落地。
3. **P1-2**（铸票绑定 workItemId 或至少契约文档责任书）。
4. P2-2（限频）→ P2-1（GC）→ 其余 P2 逐条顺手修。

---

## 9. 与既有审计记录的关系

- **不重复**：盘点 P2「ui-entries 双缓存键」→ 本文档 P2-4 给完整裁决；FP-04 P1-3 → §4.3 完整裁决（可达路径升级为官方安装路径）；API §13.6 ui-tokens body `pack` 漂移 → §2 实锤确认（修文档）。
- **C2 交接（handoff-c2-slots）复核**：7 项诚实坑逐条对代码核实全部属实；新增其未覆盖的 3 项发现（P1-1 consume 不可达、P2-1 无 GC、P1-3 scheme 未校验——交接只提了 origin 退化与 CSP 探测状态码）。
- **D4 金线**：⑦ slot 数据面断言与 ui-entries 实现一致；「slot tab 真渲染需真人验证」项维持（本次未开浏览器，组件行为以 8+5 用例裁决）。

## 附：证据留存与边界

- curl 会话 /tmp/fp15-cookies.txt；票据样本 /tmp/fp15-mint1.json（plugin=fp15pack，已消费）；过期票消费输出 /tmp/fp15-expired.txt；后端日志 /tmp/orkome-server.log。
- 测试数据：仅 ui_token 表 17 行 fp15 前缀票据（60s 逻辑过期，无 API 可删——本身即 P2-1 实证）；**未创建任何 pack_ui_entry / 工作项 / 成员数据**，无需清理动作。
- 未修改任何生产/测试文件；未做 git add/commit。
