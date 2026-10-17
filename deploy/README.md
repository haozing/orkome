# 生产部署(单二进制 + nginx 反代)

orkome-server 是单二进制,自带全部 API 路由,但**不含静态托管**——SPA
(`web/dist`)必须由外部 nginx(或 Caddy 等)托管,并把后端前缀反代到
server(:8080,`PORT`)。

## 路径契约(最重要的一节)

后端所有路由都带自身前缀,**没有任何一层剥前缀**:vite dev 代理
(`web/vite.config.ts`)原样透传,生产 nginx 也必须原样透传。
dev 与生产同构,不存在"dev 剥、生产不剥"的差异。

**致命坑:`proxy_pass http://upstream/;`(带尾斜杠/URI)会剥掉前缀,
所有请求 404。所有 proxy_pass 一律不带 URI。**

另一个隐性坑:漏配某个后端前缀时,请求会落到 SPA 的 `try_files →
index.html`,浏览器拿到 **200 + HTML 而不是 JSON**,报 JSON parse 错且
服务端无任何日志。看到"接口全挂但 nginx 访问日志是 200"就是这种情况。

## 后端前缀全集

| 前缀 | 用途 | 备注 |
|---|---|---|
| `/api/` | 业务 API(后端自带前缀) | `/api/admin`、`/api/pins`、`/api/avatars` 都在其下 |
| `/auth/` | 登录/验证码/OAuth/会话 | ploykit identity 挂根路径 |
| `/config` | 前端引导配置 | 精确匹配 |
| `/uploads/` | 附件静态文件 | |
| `/ws` | 实时推送 websocket | 需 Upgrade 头 |
| `/webhooks/` | autopilot 公开入站 | 自带 HMAC 防护 |
| `/metrics` | Prometheus 指标 | `REALTIME_METRICS_TOKEN` 保护,建议限内网 |
| `/healthz` | 存活探针 | |
| `/readyz` | 就绪探针(db/迁移/worker) | 编排器探活用它 |

完整可抄的配置见 [`nginx.conf.example`](nginx.conf.example)。

## 部署步骤

1. **构建**:`make build`(或 `go build ./cmd/orkome-server`)、
   `cd web && pnpm build` 产出 `web/dist`。
2. **静态目录**:nginx `root` 指向 `web/dist`(样例里是
   `/opt/orkome/web/dist`),SPA 兜底 `try_files $uri $uri/ /index.html`。
3. **反代**:按上表把 9 个前缀透传到 `http://127.0.0.1:8080`。
4. **环境变量**:见 [`nginx.conf.example`](nginx.conf.example) 同目录的
   systemd unit 说明(或 `cmd/orkome-server/config.go` 的 env tag 全集)。
   关键项:
   - `APP_ENV=production`
   - `DATABASE_URL`
   - `JWT_SECRET`(≥32 字节)
   - `ENGINE_CREDENTIAL_MASTER_KEY`(**恰好** 32 个原始 ASCII 字符,
     不是 hex/base64 编码——AES-256 原始 key)
   - 邮件通道:`RESEND_API_KEY` 或 `SMTP_*`。都不配时验证码只打进
     服务日志(journald,WARN 级提示),仅限单人自用。
5. **systemd**:样例 unit(可选):

   ```ini
   [Unit]
   After=network.target postgresql.service
   [Service]
   EnvironmentFile=/opt/orkome/orkome.env
   ExecStart=/opt/orkome/orkome-server
   Restart=on-failure
   [Install]
   WantedBy=multi-user.target
   ```

6. **迁移(手动,必做)**:server **不会**自动迁移——启动后它只在
   `/readyz` 报告缺失的迁移号(如 `migrations pending: 1047`,
   HTTP 503),期间部分新功能会因表结构缺列而 500。换 binary 的同时
   用同一个 `DATABASE_URL` 跑一次迁移工具:

   ```bash
   go build -o /opt/orkome/migrate ./cmd/migrate
   DATABASE_URL="$(grep '^DATABASE_URL' /opt/orkome/orkome.env | cut -d= -f2-)" \
     /opt/orkome/migrate up
   ```

   然后 `systemctl restart orkome`,`curl :8080/readyz` 应返回 `ready`。

## 自检清单

- [ ] `curl http://127.0.0.1:8080/config` 返回 JSON(直连后端)
- [ ] 经 nginx `curl https://your-domain/config` 也返回 JSON(不 200+HTML)
- [ ] 登录页能收到验证码(生产无邮件通道时查 journald:`journalctl -u orkome | grep verification`)
- [ ] 浏览器实时通知可用(`/ws` upgrade 成功)
