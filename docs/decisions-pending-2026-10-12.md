# 待产品裁决清单（2026-10-12 · 已全部裁决归档）

> 本文档原为待裁决清单；D1~D10 已由用户授权代为裁决（约束：v0 不考虑兼容与旧数据、/chat 独立页不做），现归档为**决议记录**。

| # | 事项 | 裁决 | 落地 |
|---|---|---|---|
| D1 | chat 会话口径 | **creator 私有**（写面/订阅面全部收紧，sender 列不加） | W2-1 + 主会话（wiring_ws.go chat fence） |
| D2 | chat:done 帧 | **creator 定向（SendToUser）+ 补 fail/cancel 覆盖** | W2-1 |
| D3 | system wakeup 归属 | **originator_user_id 可空 + 存量哨兵置 NULL + 拔 orZeroUUID** | W2-2（迁移 1037） |
| D4 | MCP 凭证加密 | **写路径 sealx fail-closed + 存量明文清空（迁移 1038）** | W2-3 |
| D5 | MCP 两级语义 | **PUT 三态（缺字段=保留/显式空=清空/新值=覆盖）+ 重名两级 409** | W2-3 |
| D6 | workItemId 归属 | **铸票可选绑定 work_item_id 进 claims（平台校验归属）+ 模板仅 http/https** | W2-4 |
| D7 | 工作区删除 | **硬删**（teardown 接 CleanupWorkspaceTables + 58 条跨域拓扑序清理） | W2-5 |
| D8 | /chat 独立页 | **不做**（用户定）；conversation 域 dormant 保留 | — |
| D9 | 登录口令入口 | **不做**（维持验证码 JIT；改密/找回随 settings 安全区块排期） | — |
| D10 | ploykit 升级 | **下个迭代升 main**（消 CSRF 镜像 + 拿回 MFA），时点待框架发版 | 待办 |

## 仍开放的排期项（非缺陷）

- 直聊审批入口 → 已做（fix-wave3-p1，run-logs 抽屉）
- automation 页前端 MVP → 已做（fix-wave3-p3）
- 通知偏好 UI → 已做（fix-wave3-p2）
- 契约双向守卫 → 已做（fix-wave3-p4，抓出 37 条缺口全数入表）
- usage 呈现、work 高级面（table/views）前端 → **缓办**（各需小设计/产品设计）
- admin 运营后台、第三方 pack 的 CSP/sandbox 收紧 → 触发条件未到
