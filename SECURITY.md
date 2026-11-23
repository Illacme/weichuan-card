# 安全政策 (Security Policy)

## 支持的版本

| 版本 | 状态 |
|---|---|
| main | ✅ 维护中 |

## 上报安全漏洞

如发现安全漏洞（如越权、注入、敏感信息泄露等），**请勿在公开 issue 中披露**，
请通过以下方式私信项目维护者，并尽量提供复现步骤与影响范围：

- 邮件 / 站内信联系仓库 Owner
- 或在 GitHub 通过 **Private vulnerability reporting**（仓库 Settings → Security → Private
  vulnerability reporting）提交

我们会在收到后 72 小时内确认，并协商修复与披露时间线。

## 安全设计要点（当前实现）

- 登录鉴权：账号 + 密码（bcrypt 哈希），JWT（HS256，密钥来自 `JWT_KEY` 环境变量）。
- JWT 双令牌：access（15 分钟）+ refresh（30 天）；`POST /api/refresh` 用 refresh 换发新 access，避免频繁重登。
- **PC 管理后台：httpOnly Cookie 鉴权**——access/refresh 写入 `HttpOnly; SameSite=Lax` 的 Cookie（`access_token` / `refresh_token`），
  由浏览器自动携带、JS 不可读，规避 XSS 窃取令牌；`SameSite=Lax` 阻止跨站写操作携带 Cookie，缓解 CSRF。`POST /api/logout` 清除 Cookie。
- **小程序端：Bearer 头鉴权**——token 由 JS 显式携带（微信沙箱内代码受审核，无 DOM XSS 面，风险可接受）。
- 即时吊销（两级）：
  - **账号级**（`token_version` 安全戳）：改密 / 管理员重置密码 / 禁用启用时自增版本号，鉴权中间件校验令牌内 `vid` 与当前版本一致，**旧令牌立即失效**（该账号所有设备，无需黑名单表，多实例 / 重启均安全）。
  - **会话级**（`revoked_sessions` 黑名单）：令牌含 `sid`（会话 ID，一次登录一个，access/refresh 共享，刷新不换）。`POST /api/logout` 把当前会话 sid 写入黑名单，该设备所有令牌立即失效且无法靠刷新绕过；**不影响同一账号的其他设备登录**。
- 上传：扩展名白名单 + 文件头魔法字节嗅探，单文件 ≤10MB；绑定到企业的对象存储支持私有桶——对外地址仍是本站 `/uploads/t/<企业id>/...`，取图时后端按对象归属现签短时 URL 并 302（跳转不缓存）。平台自身存储与本地磁盘不支持私有读。
- 登录限流：按客户端 IP 固定窗口限流，防爆破（刷新接口单独限流）。
  **前提是客户端 IP 可信**：限流按 `ClientIP()` 分桶，而它读不读 `X-Forwarded-For`
  取决于 `TRUSTED_PROXIES`。gin 的缺省是「信任所有代理」，那种设置下客户端只要
  每次换一个伪造头就换一个桶——实测同一来源连打 15 次全部放行（正常应在第 10 次拦下），
  等于防爆破不存在。本项目已收窄为「回环 + 三类私有网段」，并要求反代**覆盖**而非
  追加 `X-Forwarded-For`（`frontend/nginx.conf` 与 runbook 均按此配置）。
  同时 backend 的 8090 与 mysql 的 3306 都绑回环：docker 端口映射会把外部来源
  NAT 成桥接网关，而网关正好落在可信网段里，端口一对外，这条防线就漏了。
  **多副本**：进程内的桶是每副本一份，实际阈值会变成「单副本阈值 × 副本数」，
  故计数落到 `rate_limit_hits` 表共享（`middleware.WithCounter`）；存储故障时
  自动降级到进程内计数，不会把登录接口打挂、也不会让限流消失。
- **埋点接口（`POST /api/stat/track`）**：公开无鉴权（小程序访客产生），是统计数据的
  唯一进水口，按「会被刷」设计：按客户端 IP 限流 60 次/分（计数同样走
  `rate_limit_hits` 共享，不按 staffId 分桶——扫号脚本正是靠换 ID 放大调用量）、
  staffId 必须真实存在、eventType 走白名单。三者任一不过即 `code:1` 且不落库。
- 软删除：内容模型支持软删除，避免误删。

## 已知待加固项（尚未落地）

- Cookie 为 `SameSite=Lax`（同源部署已足够缓解 CSRF）；若未来跨域部署或需更强保证，可加 double-submit CSRF Token。
- 生产须设 `COOKIE_SECURE=true`（HTTPS）并前置反向代理；跨子域共享登录态才需设 `COOKIE_DOMAIN`。
- ~~黑名单表 `revoked_sessions` 暂无自动清理任务~~：**已落地**——`StartExpiredSessionPurge`
  按 `REVOKED_PURGE_MINUTES`（默认 360）定期删除过期行，启动时另跑一次。
- ~~无集中式密钥管理 / 审计日志 / 监控告警~~：**审计日志已落地**（`audit_log` 表 + 「平台管理 → 操作日志」），
  覆盖后台写操作与登录失败，敏感值脱敏不落明文、只提供查询。仍未落地的是集中式密钥管理与完整监控告警栈。
- ~~多实例部署下限流不共享~~：**已落地**——登录 / 刷新限流的计数落到 `rate_limit_hits`
  表（`middleware.WithCounter` + `repository.RateHit`），同一 (key, 窗口) 在所有副本上
  是同一行，总阈值与副本数无关；存储故障时降级到进程内计数兜底。
  仍未共享的是小程序码缓存与 access_token：多副本下会各打各的微信接口，
  配额消耗按副本数放大（不会突破安全边界，但会多花钱）。

> 上线前务必修改默认 `JWT_KEY` 与初始超管口令 `ADMIN_PASS`（这两个默认值连同演示账号写进了公开文档，属已知凭据）。
> 已由代码强制把关，不依赖人记住：MySQL 形态（`--profile mysql`）下 `MYSQL_ROOT_PASSWORD`
> 未设置则 mysql 容器启动即报错退出、不会带空口令悄悄启动（默认 SQLite 形态连 mysql 都不起，
> 与该变量无关；`JWT_KEY` / `ADMIN_PASS` 留空即进安装向导、由向导现场生成强随机值）；
> `cmd/server` 启动检测到缺省 / 过短的 `JWT_KEY` 或缺省口令时**拒绝启动**并打印 `[SECURITY][FATAL]`（本地联调加 `ALLOW_INSECURE=1` 放行，切勿用于生产）。
> 部署前可先跑 `scripts/preflight.sh` 做静态自检。它对小程序端 `DEFAULT_BASE_URL`
> 的判定覆盖 127/localhost **与私有网段**（192.168./10./172.16-31.）——仓库里这个值
> 是开发机的局域网 IP，他人 clone 后真机必然连不上，而种子基址是拉取后台配置的
> 唯一入口，连不上就再也拿不到正确地址，只能重新打包。本机真机联调可用
> `PREFLIGHT_ALLOW_PRIVATE=1` 放行该项。
