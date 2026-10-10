# ploykit 框架 issue 清单（orkome rebase 过程中记录）

记录时间：2026-10-10（P2 期间）。状态：待提交到框架仓库（打 tag 后一并处理）。

| # | 类别 | 问题 | 建议 | 证据 |
|---|---|---|---|---|
| F1 | API 设计 | `platform/pgmigrate` 目录名与包名不一致（包声明为 `pgm`）——所有消费者必须 `pgmigrate "…"` 别名 import，否则编译错"imported as pgm" | 目录改 `platform/pgm` 或包名改 `pgmigrate`；二选一保持一致 | orkome 80+ 文件转换时全部踩中 |
| F2 | API 设计 | pgmigrate 只接受 `embed.FS`，不支持 `fs.FS`/`os.DirFS`——测试与工具无法指向磁盘目录，外部迁移目录必须先包一层 embed 包 | `New` 接 `fs.FS`（embed.FS 天然满足）；embed 语义留给消费方 | orkome 新增 migrations/embed.go + 26 个测试文件被迫改 embed |
| F3 | API 设计 | `sealx` 缺密钥派生助手（orkome 原 `DeriveKey(baseSecret, domain)`），每个产品都要自己写一遍 HMAC-SHA256 | 提供 `DeriveKey(base, domain string) []byte`（域分隔的 KDF 是通用需求） | wiring_orchestration.go 本地重实现 deriveSealKey |
| F4 | 发布流程 | v0.3.0 tag 打在 Next.js 历史上后被 Go 版复用——sumdb 记录不可变，公共生态里该版本永久作废（任何人 `go get` 校验失败） | Go 线打新 tag（v0.4.0+）；发布流程加"tag 不可复用"检查 | GONOSUMDB 绕过记录见 rebase 计划 §0 v3 |
| F5 | 架构 | contract 词汇包在 `ploykit/internal/contract/`——外部模块按 Go 可见性规则**无法 import**，产品各自持有一套词汇；且框架 wswire 能力常量含母本产品词（CapAutopilot/CapSquad） | 二选一：移出 internal 成为公共 API；或声明"产品自持词汇"为设计意图并清理母本残留 | orkome 六个 contract 包全部本地保留的裁定依据 |
| F6 | 一致性 | wsx `Hub.PATPrefix`（默认 tk_）与 identity `domain/rules.go` 的 patPrefix 常量是否同源取值？两处硬编码有漂移风险 | 单一来源（identity 暴露给 hub，或公共常量） | 附录 E B6 遗留点，P5 切 wsx 时需验证 |
| F7 | 功能缺口 | webx 无"带路由清单的 Mux 包装"——产品无法枚举已注册路由做契约对账（chi.Walk 的等价物），orkome 只能用 `mux.Handler(假请求)` 解析法做单向对账，且**中间件覆盖断言完全丢失** | 框架提供 `webx.NewRecordingMux()`（记录 pattern + 包装链，支持导出对账）或 contractx 吸收该能力 | e2e/routes_walk_test.go 重写说明 |

## 提交策略

F1–F3、F7 是 API/功能改进（框架仓库 issue + PR）；F4 是流程修正（打新 tag 即关闭）；F5 需要架构裁定；F6 是 P5 切换前的验证项。全部在 orkome rebase Phase 3 开始前提交给框架仓库，避免消费期反复绕行。
