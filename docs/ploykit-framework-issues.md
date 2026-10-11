# ploykit 框架 issue 清单（orkome rebase 过程中记录）

记录时间：2026-10-10（P2 期间）。**2026-10-11 状态更新：框架 v0.4.0 已修复 F1/F2/F3/F4/F7（用户确认）；orkome 已升级至 v0.4.0 并全绿。** F5 经验证仍为 internal（按 N2 裁定维持产品自持词汇，视为设计意图，关闭）；F6 留待 P5 切 wsx 时验证。

| # | 类别 | 问题 | 建议 | 证据 |
|---|---|---|---|---|
| F1 | API 设计 | `platform/pgmigrate` 目录名与包名不一致（包声明为 `pgm`）——所有消费者必须 `pgmigrate "…"` 别名 import，否则编译错"imported as pgm" | 目录改 `platform/pgm` 或包名改 `pgmigrate`；二选一保持一致 | orkome 80+ 文件转换时全部踩中 |
| F2 | API 设计 | pgmigrate 只接受 `embed.FS`，不支持 `fs.FS`/`os.DirFS`——测试与工具无法指向磁盘目录，外部迁移目录必须先包一层 embed 包 | `New` 接 `fs.FS`（embed.FS 天然满足）；embed 语义留给消费方 | orkome 新增 migrations/embed.go + 26 个测试文件被迫改 embed |
| F3 | API 设计 | ~~`sealx` 缺密钥派生助手~~ ✅ v0.4.0 已提供 `DeriveKey`；orkome 已切换（裁定：不做旧数据兼容，直接用框架版） | — | 已完成 |
| F4 | 发布流程 | v0.3.0 tag 打在 Next.js 历史上后被 Go 版复用——sumdb 记录不可变，公共生态里该版本永久作废（任何人 `go get` 校验失败） | Go 线打新 tag（v0.4.0+）；发布流程加"tag 不可复用"检查 | GONOSUMDB 绕过记录见 rebase 计划 §0 v3 |
| F5 | 架构 | contract 词汇包在 `ploykit/internal/contract/`——外部模块按 Go 可见性规则**无法 import**，产品各自持有一套词汇；且框架 wswire 能力常量含母本产品词（CapAutopilot/CapSquad） | 二选一：移出 internal 成为公共 API；或声明"产品自持词汇"为设计意图并清理母本残留 | orkome 六个 contract 包全部本地保留的裁定依据 |
| F6 | 一致性 | ~~wsx Hub.PATPrefix 双源~~ **升级：PAT 前缀三源一致性**——消费方必须同时配 `webx.AuthConfig.PATPrefix`（Bearer 解析）+ `identitypg.Config.PATPrefix`（repo 格式预检，配错静默返回 nil→401 invalid token）+ `TokenService.WithPATPrefix`（铸币），缺一处即断 | 单一来源：repo cfg 缺省回落 AuthConfig；或 TokenService 构造收 AuthConfig | orkome P3-3 冒烟实测踩中三处 |
| F7 | 功能缺口 | ~~webx 无"带路由清单的 Mux 包装"~~ ✅ v0.4.0 已提供 `webx.Router` 接口 + `*webx.Mux` 路由录制（RouteInfo 枚举）；orkome 尚未迁移到 webx.Mux（后续批次） | — | 已修复，待采用 |
| F8 | API 设计 | notify 域路由面窄（无 unread-count/summary/facets/preferences/archived 端点，inbox 无 workspace 作用域）——产品被迫叠层：框架端点查 user_id 列、产品端点查 recipient_user_id+workspace_id 列，**同表双列写入**维持两套表面 | notify 支持 workspace 作用域收件箱（或注入扩展路由位）；偏好表面进框架 | orkome P4 notification 替换的设计约束 |
| F9 | 一致性 | 域间认证模式不一致：identity Mount 不带认证（要求产品在链上包 Authenticate），webhooks/workspace 域内自带 authz guard——两种心智模型并存，产品接线易错（identity 侧漏包即全员 401） | 统一为一种模式（建议全部域内 guard，identity 提供带认证的便捷 Routes()） | P3-3 踩坑 #1 + P4 接线差异 |

## 提交策略

F1–F3、F7 是 API/功能改进（框架仓库 issue + PR）；F4 是流程修正（打新 tag 即关闭）；F5 需要架构裁定；F6 是 P5 切换前的验证项。全部在 orkome rebase Phase 3 开始前提交给框架仓库，避免消费期反复绕行。
