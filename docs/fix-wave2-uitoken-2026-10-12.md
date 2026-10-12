# W2-4 交接笔记：ui-token 票证收口（consume 反转 + workItemId 绑定 + 模板校验）——2026-10-12

> 裁决依据：`docs/audit-fp15-pack-ui-2026-10-12.md` P1-1 / P1-2 / P1-3（裁决已定：consume 以票自证；铸票可选绑定 work_item_id；url_template 只收绝对 http(s)）。v0 不考虑兼容。
> 本文是唯一交付说明；file:line 均为改后现状。

---

## 1. 改了什么

### P1-1 consume 反转：票即凭证，匿名可达

| 位置 | 变更 |
|---|---|
| `internal/identity/adapters/http/routes.go:53-54` | `POST /api/ui-tokens/consume` 摘掉 `auth(webx.RequireAuth(...))` 壳，裸挂 handler。铸票 `POST /api/ui-tokens` 仍要会话 |
| `cmd/orkome-server/csrf_session.go:32-38` | 新增 `uiTokenConsumePrefix = "/api/ui-tokens/consume"` 常量 |
| `cmd/orkome-server/main.go:326` | CSRF `ExemptPrefixes` 追加该前缀——**必须用完整路径**：短前缀 `/api/ui-tokens` 会把仍需会话的铸票端点一起豁免 |

安全语义（与审计 §6.1 一致）：单次 jti（DB 原子 UPDATE）+ 60s TTL + HS256 平台签名 = 票自身即身份证明；consume handler 本就不读 Principal。跨站强迫消费的最坏后果是烧掉一张 60s 单次票（DoS 半径≈0）。orchestration 域的 frame-ancestors 探测、票当 Bearer 用（webx 只认 `ork_`）等既有防线未动。

**边界说明**：`cmd/orkome-server/csrf_session.go` / `main.go` 不在本票证独占清单内，但 CSRF 门挂在组合根，豁免是「匿名可达」裁决的必要组成（审计方案 A 原文即含此步），故一并落地；各一行级，已注明 FP-15 P1-1 出处。

### P1-2 work_item_id 铸票绑定

| 位置 | 变更 |
|---|---|
| `internal/identity/app/service_uitoken.go:21-31` | `UITokenClaims` 加 `WorkItemID`（JWT claim 键 **`wiid`**，空=未绑定）；`WorkItemLocator` 接口 + `WorkItemLocatorFunc` 适配器（identity 不 import work 包） |
| `internal/identity/app/service_uitoken.go:96-119` | `Mint(ctx, ws, user, plugin, workItemID)`——第 4 参可选：非空时 ①`domain.UUIDOK` 预检（非 UUID 400，先于 locator，避免 PG uuid cast 炸 500）→ ②locator 存在性（跨区/不存在 **400**）→ ③locator 未装配 fail closed（Internal） |
| `internal/identity/adapters/http/handlers_uitoken.go:18-40` | mint body 加可选 `work_item_id`；**consume 响应加 `work_item_id` 字段**（未绑定为空串） |
| `cmd/orkome-server/main.go:442-453` | `idWorkItemCheck(db)`：适配 work 域既有 `workpgrepo.Repo.GetWorkItem`（workspace 作用域查询，工单∈他区时 ok=false）→ 跨包只调既有 API，identity↔work 零新依赖 |
| `cmd/orkome-server/wiring_identity.go:110-116` | `Ext.UITokens = NewUITokenService(...).WithWorkItemLocator(WorkItemLocatorFunc(idWorkItemCheck(db)))` |

归属校验收口在**铸票时刻**：pack 页只需比对 claims（经 consume 响应）与 URL 里的 `{workItemId}` 是否一致，无需任何 orkome 凭证。

### P1-3 url_template 只收绝对 http(s)

同一规则双侧落地（CLI 保持自包含，规则函数为有意拷贝，两侧注释互指）：

| 位置 | 变更 |
|---|---|
| `internal/orchestration/app/service_uientry.go:56-58,88-91,94-107` | `validateURLTemplate(field, slot, template)`：`url.Parse` 后必须 `scheme ∈ {http, https}` 且 `Host != ""`（协议相对 `//host/x` 因 scheme 空被拒）。entry 模板与每个 slot 模板都查；违规 → **400 E_VALIDATION**（消息含 "absolute http(s) URL"） |
| `cmd/pack-provision/main.go:163-171` | `parsePack` 对 `ui.entry.urlTemplate` 校验（**v0/v1 都查**，fail the install 早于任何网络交互；空模板留给"无 entry 不注册"分支） |
| `cmd/pack-provision/main.go:207-242` | `validateV1UISlots` 逐 slot 查模板（违规报错含 `ui.slots[<slot>]` + "http(s)"）；`validateAbsHTTPURL` 为 service 侧同规则的自包含拷贝 |

矩阵行为：`https://`/`http://` 绝对 URL 过；`javascript:` / `data:` / 相对路径 / 裸词 / 协议相对 / `ftp:` / 有 scheme 无 host 全拒。审计 §6.3 提的 probe 豁免通道（"URL 不合法 → probe failed → 豁免保留"）随解析期前置校验**自然封闭**：能走到 probe 的 slot 模板必已是合法 http(s)，无需改 probe 代码。

---

## 2. claims 新形状（pack 侧契约措辞建议）

**JWT claims（HS256，pack 不验签、不解码也行）**：

```json
{
  "jti": "uit_<32hex>",       // 单次消费凭证
  "sub": "<user uuid>",
  "wsid": "<workspace uuid>",
  "plugin": "<pack name>",
  "wiid": "<work_item uuid>", // ★新增，可选——仅铸票时带 work_item_id 才出现
  "iat": 0, "exp": 0          // exp-iat = 60
}
```

**POST /api/ui-tokens/consume（新增：匿名可调，票即凭证）**

请求：`{"token": "<mint 返回的 JWT>"}`

```json
200 OK
{
  "user_id": "<user uuid>",
  "workspace_id": "<workspace uuid>",
  "plugin": "<pack name>",
  "work_item_id": "<work_item uuid | \"\">"   // ★新增，未绑定为空串
}
```

错误：`401 E_UNAUTHENTICATED`（票无效/已消费/过期——三种不再区分）；`400 E_VALIDATION`（缺 token）。

**给 pack 契约文档的措辞建议（可直接抄）**：

> ### ui-token 回验与 workItemId 责任
> 1. 平台在铸票时刻已验证 `work_item_id`（若绑定）属于铸票工作区，并把结果写入票的 `work_item_id` 声明。pack 页通过 `POST /api/ui-tokens/consume`（无需会话或 API 凭证，票本身即凭证，单次有效、60 秒过期）换取 `{user_id, workspace_id, plugin, work_item_id}`。
> 2. **pack 页必须在渲染工单数据前校验：URL 参数 `{workItemId}` === consume 响应的 `work_item_id`**。未绑定票（`work_item_id` 为空）或两者不一致时，pack 必须拒绝出数据——未做该校验即视为 pack 侧越权漏洞，责任在 pack。
> 3. 票是单次消费的：iframe 加载消费掉票后，同 URL 里的深链兜底外链即指向死票；如需兜底链接可用，pack 页应自行重新铸票（受同一浏览器会话的铸票端点约束）。
> 4. 票对 orkome 其他 API 零权限（不可当 Bearer 用）；泄漏爆炸半径 = 0。

---

## 3. 测试

| 文件 | 覆盖 |
|---|---|
| `internal/identity/app/service_uitoken_test.go`（新） | consume 四态（有效出 claims/重放拒/过期拒/伪造+异签拒）；绑定矩阵（合法工单进 claims、跨区 400、非 UUID 不打 locator、locator 故障不放行、locator 缺位 fail closed） |
| `internal/identity/adapters/http/uitoken_consume_route_test.go`（新，免 DB） | HTTP 级四态：**匿名+有效票=200（反转锚）**、二次 401、过期 401、伪造 401、空体 400、匿名 mint 仍 401；绑定票 consume 透出 `work_item_id` |
| `internal/identity/adapters/http/integration_test.go` | 生命周期重写：匿名 mint 401 / 非成员 403 / **匿名+有效票 200** / 重放 401 / SQL 推过期→401 / 伪造 401；新增 `TestUITokens_WorkItemBinding`（真 PG work_item：合法绑定透出、跨区 400、不存在 400、非 UUID 400、不带字段照常 201） |
| `internal/orchestration/adapters/http/handlers_uientry_test.go` | slot 矩阵（https 过/http 过/javascript: 拒/data: 拒/相对拒/协议相对拒/ftp 拒，400 含 "http(s)"）+ entry 矩阵（https 过/javascript: 拒/相对拒），独立 router 不污染既有计数断言 |
| `cmd/pack-provision/slots_scheme_test.go`（新） | `validateAbsHTTPURL` 纯函数矩阵 9 态；v1 slot 模板矩阵（https 过/javascript: 拒/相对拒/ftp 拒，报错含 `ui.slots`）；entry 模板 v0/v1 矩阵（报错含 `ui.entry`） |
| `internal/identity/adapters/pgrepo/uitoken_integration_test.go` | `Mint` 签名跟进（第 4 参） |

既有夹具无需改模板：e2e/golden、agentblog、packs/agentblog、repo_uislots、handlers_uientry 的模板全部已是绝对 http(s)（`https://…` 或 `http://127.0.0.1:<port>`）。

## 4. 门禁

- `go build ./...`、`go test ./... -count=1`、`golangci-lint run`：见最终回复的门禁摘要（工作树同期有其他 W2 agent 的并行改动，本笔记只对本票证文件背书）。
- `TEST_DATABASE_URL=postgres://postgres:orkome_e2e@localhost:54329/orkome` 下 `go test ./internal/identity/... -count=1` 全绿（含真 DB 集成）。
- 未重启共享 dev server；未 git add/commit。

## 5. 给后续票证的注意点

- **P2-2（铸票限频）在 consume 放开后优先级上升**：匿名 consume 现在可探测票有效性（穷举不可行——jti 128bit + 60s，但限频仍是标准纵深）。
- **PackSlotFrame 深链兜底**（audit §4.1 已预警）：票单次消费后同票外链即死票；前端若要"兜底链接活"，需改走重新铸票——**web 侧无需在本票证处理的范围内**（URL 拼接逻辑未变，`{uiToken}` 占位语义不变）。
- 消费方如需「不消费票只读 claims」的校验原语（probe 型），v1 可加 `POST /api/ui-tokens/inspect`（只验签不置 consumed）；当前裁决不需要。
- `expires_in:60` 字面量与 `UITokenTTL` 双源（P2-7）未动，不在本票证范围。
- `webx.SignUIToken/VerifyUIToken` 死代码（P2-6）未动；claims 已加 `wiid`，删除死代码时无兼容包袱。
