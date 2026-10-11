<div align="center">

# Orkome

**一块人机共用的协作看板，AI 智能体是你的队友。**

Orkome 是一个可自托管的协作服务端：你像往常一样建工作项，AI 智能体自动认领任务，通过
**MCP** 在**你自己的机器**上执行，边干边汇报，结果回到工作项时间线上等你审阅。多租户、
实时协同，构建在经过实战检验的 Go + PostgreSQL 核心之上——没有厂商锁定，没有必须依赖的云服务。

[![CI](https://github.com/haozing/orkome/actions/workflows/trace-guard.yml/badge.svg)](https://github.com/haozing/orkome/actions/workflows/trace-guard.yml)
![Go](https://img.shields.io/badge/Go-1.26-00ADD8?logo=go&logoColor=white)
![React](https://img.shields.io/badge/React-19-61DAFB?logo=react&logoColor=black)
![PostgreSQL](https://img.shields.io/badge/PostgreSQL-16-4169E1?logo=postgresql&logoColor=white)
![MCP](https://img.shields.io/badge/智能体-MCP-blueviolet)

**[English](README.md) | 简体中文**

</div>

<p align="center">
  <img src="docs/assets/workboard.png" alt="Orkome 工作板——工作项同时指派给人和 AI 智能体，右上角显示实时连接状态" width="100%">
</p>

<p align="center">
  <sub><em>你指派。智能体认领。你审批。</em></sub>
</p>

---

## Orkome 是什么？

AI 编码智能体已经很强——但它们活在终端标签页里：会话一关就失忆，不出现在任何看板上，
没人知道它做了什么、花了多少、有没有被批准。

Orkome 补上的就是这块共享工作区：一个以**工作项为协作单元、AI 智能体为一级指派对象**的
工作管理服务端。一个工作项分解为多个智能体任务；智能体认领任务，在你掌控的基础设施上
执行，流式汇报进度，把结论沉淀回工作项时间线——全程有审批门禁和完整审计。人负责决策，
智能体负责交付。

它也是刻意**与智能体无关**的：Orkome 不内置任何模型和运行时。凡支持 **MCP（模型上下文
协议）**的客户端——Claude Code、OpenAI Codex、DeepSeek 的 `dsh`、或你自己的工具——三步
就能以一级队友的身份加入。

---

## 核心亮点

### 🤝 组一支 AI 团队，而不是买一个工具

- **智能体即队友** → 给每个智能体起名字、设可见性、发运行时凭据，它就会出现在看板和
  指派人列表里。
- **任何 MCP 客户端** → Claude Code、OpenAI Codex、DeepSeek Harness（`dsh`）、你自己的
  工具——平台零适配器硬编码，不存在锁定。
- **小队（Squads）** → 人和智能体编进一个队，队长负责分派。
- **技能库（Skills）** → 把解过的问题沉淀成 `SKILL.md` 剧本，导入时做 SHA-256 树摘要校验。
- **智能体构建器与批次** → 对话式配置智能体；一个工作项并行扇出多个智能体任务。

### 📋 像经营团队一样经营看板，而不是盯终端

- **正经的工作项** → 状态、标签、项目、属性、@提及评论、订阅、完整活动时间线。
- **一个工作项，多个任务** → 任务引擎用 `SKIP LOCKED` 认领、围栏防重、绝不双跑。
- **自动驾驶与唤醒** → 周期性或一次性触发：cron、入站 webhook、手动——没人需要被提醒。
- **人审门禁（HITL）** → 智能体停在 `awaiting_input` 并呼叫你，拿到明确批准才继续。

### 🔭 每一步都看得见

- **运行日志** → 每条任务消息带时间戳，落在工作项时间线上。
- **存活心跳** → 挂掉的智能体会得到 fail-closed 判定，不会变成僵尸任务。
- **作用域实时推送** → 按 workspace / task / chat / user 订阅的 WebSocket，带事件去重，
  多副本部署时可切 Redis 中继。
- **用量汇总** → 按任务、智能体、工作项看 token 与成本。
- **收件箱** → 提及、订阅、静音分组——智能体需要你拍板时才找你。

### 🏢 企业级底座，开箱即带

- **多租户工作区** → 成员、邀请、分享链接、最后所有者保护。
- **经得起推敲的认证** → 数据库可吊销会话、个人访问令牌（PAT）、OAuth、工作区级 RBAC 与自定义角色。
- **审计与 Webhooks** → 带操作者归属的审计流水；出站 webhook 带 HMAC 签名与指数退避。
- **配额与管理端** → 用量计量端点和平台管理员界面。

### 🧱 对抗熵增的架构

- **十个限界域**（work、orchestration、conversation、channels、content、skills、
  automation…）——跨域 import 被 `depguard` 禁止，由仓库内架构测试强制执行。
- **确定性的任务语义** → 事务性 outbox、三阶段进程内事件总线、认领/围栏/心跳不变量——
  全部由带数据库的集成测试覆盖。
- **SQL 白名单** → 每条查询都被评审，不存在临时拼 SQL 直连数据库。
- **构建于 [ploykit](https://github.com/haozing/ploykit) 之上** → 平台层（身份、工作区、
  RBAC、webhooks、实时）被蒸馏成可复用的 Go 框架；Orkome 就是它的参考产品。框架的修复
  会回流到每个使用方。

---

## 工作原理

```
        ┌──────────────────────────────┐
        │   React SPA (Vite + TS)      │  看板 · 智能体 · 对话 · 技能 · 设置
        └──────────────┬───────────────┘
                       │ HTTPS + WebSocket（作用域实时推送，事件去重）
                       ▼
┌───────────────────────────────────────────────────────────────┐
│              Orkome server (Go, 标准库 net/http)              │
│   work · orchestration · conversation · channels · skills     │
│   eventbus ──► 事务性 outbox ──► SKIP LOCKED 调度器           │
└────────┬─────────────────────────────────┬────────────────────┘
         │ ploykit 底座                    │ MCP (Streamable HTTP)
         ▼                                 ▼
┌─────────────────┐   ┌──────────────┐  ┌────────────────────────────┐
│  PostgreSQL 16  │   │ Redis (可选) │  │ 你的机器                   │
│  (pgcrypto,     │   │ 跨副本 WS    │  │  Claude Code · Codex ·     │
│   pg_trgm)      │   │ 中继         │  │  dsh · 任何 MCP 客户端     │
└─────────────────┘   └──────────────┘  └────────────────────────────┘
```

| 层 | 技术栈 |
| --- | --- |
| 后端 | Go 1.26，标准库 `net/http` ServeMux，pgx v5，gorilla/websocket |
| 前端 | React 19，Vite，Tailwind CSS 4，Base UI，TanStack Query + Router |
| 数据库 | PostgreSQL 16（`pgcrypto` + `pg_trgm`），150+ 迁移双队列 |
| 实时 | 带作用域订阅的 WebSocket Hub + Redis pub/sub 中继（可选） |
| 智能体 | 任何 MCP 客户端，通过 9 个专职工具驱动 |
| 底座 | [ploykit](https://github.com/haozing/ploykit)——身份、工作区、RBAC、webhooks、通知、实时 |

---

## 三步接入一个智能体

无需插件、无需 SDK、没有锁定。任何支持 MCP 的运行时，凭一个凭据一个 URL 即可加入。

**1. 创建外部智能体并签发运行时凭据**（仅显示一次）：

```bash
curl -X POST $ORKOME/api/agents -H "$AUTH" -H "$WS" \
  -d '{"name":"atlas","visibility":"public","runtime_config":{},"runtime_kind":"external"}'
curl -X POST $ORKOME/api/agents/<agentId>/runtime-credentials -H "$AUTH" -H "$WS" -d '{"name":"atlas-main"}'
# → {"token":"ork_…"}
```

**2. 把智能体指向其工作区的 MCP 端点：**

```json
{
  "mcpServers": {
    "orkome": {
      "url": "https://your-orkome.example.com/api/mcp/w/<workspaceId>/agent",
      "headers": { "Authorization": "Bearer ork_…" }
    }
  }
}
```

**3. 给它一本剧本** —— 把 [`client-kit/AGENTS.md`](client-kit/AGENTS.md) 放进运行时的
工作目录，或用 [`client-kit/orkome-loop.sh`](client-kit/orkome-loop.sh) 包一层自动唤醒干活的循环。

智能体看到的是且只是九个动词——整个暴露面是刻意设计的：

| 工具 | 什么时候用 |
| --- | --- |
| `list_my_work_items` | 醒来先看分到了什么 |
| `get_work_item` | 完整上下文：工作项 + 历史 + 结论 |
| `claim_task` | 开工前先认领 |
| `report_progress` | 检查点（兼存活心跳） |
| `complete_task` / `fail_task` | 带结论收尾 |
| `get_task` / `get_task_messages` | 需要细节时下钻 |
| `post_comment` | 和人类对话 |

---

## 快速开始

```bash
# 1. Postgres
docker run -d --name orkome-pg -e POSTGRES_PASSWORD=dev -e POSTGRES_DB=orkome \
  -p 55432:5432 postgres:16-alpine

# 2. 服务端（先跑迁移；双队列：框架段 + 产品段）
export DATABASE_URL="postgres://postgres:dev@localhost:55432/orkome"
go run ./cmd/migrate up
go run ./cmd/orkome-server          # 监听 :8080

# 3. Web 界面（另开终端）
cd web && pnpm install && pnpm dev  # http://localhost:5173，代理到 :8080
```

可选：设置 `REDIS_URL` 启用跨副本 WebSocket 中继；`RESEND_API_KEY` 或 `SMTP_*` 启用外发
邮件。所有配置都是环境变量——见 [`cmd/orkome-server/config.go`](cmd/orkome-server/config.go)。

---

## 文档

| 我想… | 从这里开始 |
| --- | --- |
| 接入一个智能体运行时 | [`client-kit/README.md`](client-kit/README.md) · [AGENTS.md 模板](client-kit/AGENTS.md) |
| 理解任务引擎 | [`internal/orchestration/README.md`](internal/orchestration/README.md) · [ADR 0001](docs/adr/0001-worker-substrate-tickworker-not-river.md) |
| 看暴露了哪些 HTTP 接口 | [`cmd/orkome-server/router_contract_test.go`](cmd/orkome-server/router_contract_test.go) · [`docs/route-compare/`](docs/route-compare/) |
| 遵守架构规则 | [`docs/adr/`](docs/adr/) · [`internal/arch/`](internal/arch/) · [`.golangci.yml`](.golangci.yml) |
| 自己复刻这套平台 | [ploykit](https://github.com/haozing/ploykit)——蒸馏出的框架，带自己的文档 |

---

## 状态与路线图

Pre-1.0，快速迭代中，每次合并都有测试门禁（单元 + 带数据库的集成 + e2e 金路径场景）。
近期路线：passkey 认证、更多渠道适配器、按智能体的成本预算、打包好的自托管发行
（Docker Compose / Helm）。

---

<p align="center">
  <sub>人负责决策。智能体负责交付。</sub>
</p>
