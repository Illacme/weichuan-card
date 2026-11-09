# 上线前核对清单

> 生产部署前逐项过一遍；另有零依赖静态自检：`scripts/preflight.sh`
> （接 CI / 发布流水线加 `PREFLIGHT_STRICT=1`，使 WARN 也算失败）。
>
> **启动门槛与本清单不是同一套判据**：`InsecureGates` 只拦「默认凭据」两项
> （`JWT_KEY`、`ADMIN_PASS`）——它们改一个环境变量就能自愈，拦启动是安全且不危险的。
> 其余各项（HTTPS/反代、种子基址、`COOKIE_SECURE`、存储凭据、探针透传）门槛一概不管：
> 它们的故障形态是「服务起来了但用户用不了」，只能靠本清单 + `preflight.sh` +
> 一位真的走了一遍流程的人。**别把「进程没退出」当成上线通过。**

## 一、阻断级（不改 = 起不来 / 任何人可登录 / 功能全挂）

| 项 | 改哪里 | 验证已生效 | preflight 覆盖 |
|---|---|---|---|
| 初始超管口令 `ADMIN_PASS` | 环境变量 / `data/runtime.env`（留空走安装向导现场生成） | 不再是内置缺省值 `admin123456`（该值已随演示账号写进公开文档）；建议 `openssl rand -base64 12` | ✅ 查缺省值 / 长度；后端 `InsecureGates` 也会拒绝启动 |
| 签名密钥 `JWT_KEY` | 同上 | 不再是内置缺省值；长度 ≥ 32（`openssl rand -base64 32`） | ✅ 查缺省值 / 长度；`InsecureGates` 同样拒绝启动 |
| 部署形态 | 用 `cmd/server`（MySQL）或 `docker-compose`；**不要**用 `cmd/devserver` | devserver 每次建临时 SQLite，重启即丢 | ⚠️ 需人工确认启动方式 |
| HTTPS + 反向代理前置 | 网关 / 反代配置 | 对外只暴露 https，后台 `COOKIE_SECURE=true` | ✅ 查 `COOKIE_SECURE`；设 `PUBLIC_BASE_URL` 后还查外部可达性与证书有效期 |
| 小程序种子基址 `DEFAULT_BASE_URL` | 小程序源码常量（`miniprogram/utils/config.ts`，**默认为空**） | 是线上 https 域名；**不能**留空，也**不能**是 `127.0.0.1` / `localhost` / **私有网段**（192.168./10./172.16-31.） | ✅ 上述四类（空值 / 回环 / 私有网段 / 明文 http）都判 FAIL（本机联调可 `PREFLIGHT_ALLOW_PRIVATE=1` 放行） |
| 小程序品牌名 | `app.json` 全局标题 = 产品名；企业名运行时按租户设置 | 不得出现任何**具体客户名**（一套代码多租户，写死客户名等于给别人看别家公司名） | ✅ `miniprogram/tests/brand.spec.ts` 扫全目录守门 |
| `COOKIE_SECURE` | 环境变量 / `runtime.env`（只读项，只能改环境） | 生产 HTTPS 下为 `true` | ✅ |
| **探针地址必须直通后端**（`/healthz`） | 反代配置：给 `/healthz` 单独一条透传规则（`location ^~ /healthz`，**结尾不带斜杠**） | `curl 域名/healthz/ready` 返回 **JSON**（不是 HTML）。返回 HTML 说明被单页应用的兜底规则接走了，此时后端已死也照样 200 | ✅ 第 8 / 10 节都验 `Content-Type`，返回 HTML 判 FAIL |
| **可信反代** `TRUSTED_PROXIES` | 环境变量（默认已是「回环 + 三类私有网段」） | 登录 / refresh / 小程序码的限流是按 `ClientIP()` 分桶的；若它取到了客户端自带的 `X-Forwarded-For`，**换个伪造头就换一个桶，限流等于不存在**（已实测可绕过）。反代不在私有网段（如公网 LB）时必须显式配 | ❌ 需人工确认；启动日志出现 `You trusted all proxies` 即配置有误 |
| **小程序备案（工信部，2024 起强制）** | 微信公众平台 → 小程序管理 → 备案（与域名 ICP 备案相互独立） | 备案状态「已通过」且备案号已公示在小程序关于页；**只备域名、未备小程序时提审最后一步直接驳回、无法上线** | ❌ 纯流程项，无自动化检查；需人工确认备案号（见 runbook §0 前置条件） |

> 上表最后两项是「服务起得来、但安全与监控静默失效」的典型：没有任何报错，
> 只有出事那天才会发现。它们比「起不来」更难查，所以放在阻断级。

## 二、功能级（不改 = 线上功能残缺，但服务能起）

| 项 | 改哪里 | 验证已生效 | preflight 覆盖 |
|---|---|---|---|
| 对象存储 `STORAGE_DRIVER` + 凭据 | 「系统设置 → 文件存储与 CDN」或环境变量 `STORAGE_*` | 取值只可是 `local` / `minio` / `oss`；选非 local 时 `STORAGE_ENDPOINT` / `STORAGE_BUCKET` / 凭据已填 | ✅ 查合法值 / 凭据非空 |
| 上传基址 `UPLOAD_BASE_URL` | 同上 `UPLOAD_BASE_URL`，或 `docker-compose.yml` 的 `${UPLOAD_BASE_URL:-…}` | 是真实可访问的 https 基址（也可在后台系统设置覆盖并重启） | ✅ 查本机地址 / http。**注意**：它此前在 compose 里是写死的，改 `.env` 不生效而 preflight 照旧 PASS；现已改成变量壳 |
| 微信 `wx_appid` / `wx_appsecret` | 后台「系统设置」覆盖（留空回退 `WX_*` 环境变量） | 真机扫码能拉起、小程序码能生成 | ✅ 查是否配置与格式；`PREFLIGHT_WX_LIVE=1` 时真调微信接口验证是否配对 |
| 数据库迁移已应用到最新（MySQL 升级） | 启动时自动补齐（`AUTO_MIGRATE=0` 关闭，改跑 `server migrate`） | **MySQL**：启动日志是「已应用 N 个迁移」或「数据库已是最新」，**无** `[MIGRATE][FATAL]`。**SQLite**：表由启动时 `AutoMigrate` 维护，上面两行**根本不会出现**——别照着找（`server migrate` 在 SQLite 下会直接提示无需迁移） | ❌ 需看启动日志（详见 runbook §4） |
| gin 运行模式 `GIN_MODE` | `docker-compose.yml`（默认 `release`） | 不是 `debug`：debug 下每次启动把全部路由表打进日志，把真正要看的 `[SECURITY]` / `[MIGRATE]` 挤走 | ❌ 需看启动日志有无 `[GIN-debug]` 行 |
| 服务端时区 `TZ` | `docker-compose.yml` 默认 `Asia/Shanghai`；原生部署设 systemd `Environment=TZ=Asia/Shanghai` | 统计「今日」口径正确：`docker exec weichuan-backend date` 与北京时间一致。不设时 Go 读不到时区库会**静默按 UTC**——每天 00:00–08:00 的访问落到前一天，不报错、只是数字悄悄错位 | ❌ 需人工看一眼容器时间 |
| CDN 刷新凭据 `CDN_*` | 同上「文件存储与 CDN」或环境变量 | 需刷新时选了厂商（`CDN_PROVIDER`，五家之一）并填全该厂商要求的凭据、开 `CDN_ENABLED` | ❌ 需人工确认 |
| `MYSQL_ROOT_PASSWORD` | `docker-compose.yml` / 环境 | 仅 `--profile mysql` 形态必填；改强随机值（默认 SQLite 形态无所谓） | ✅ 查示例值 / 必填 |
| **微信小程序服务器域名** | mp.weixin.qq.com → 开发管理 → 开发设置 → 服务器域名 | `request` / `uploadFile` / `downloadFile` **三类**都含 `https://<种子基址域名>`。少一类对应功能在真机上静默失败：上传头像走 `wx.uploadFile`，海报背景与小程序码走 `canvas.createImage()`；开发者工具勾「不校验合法域名」时**复现不出来**，只有真机会中招 | ❌ 需人工确认（逐项判据见 `docs/real-device-acceptance.md` §0.5） |

## 三、运行级（上线后看护）

- **部署前跑一遍** `bash scripts/preflight.sh`：期望全 `PASS`；`PREFLIGHT_STRICT=1` 时 `WARN` 也返回退出码 1。
  - **在哪跑很重要**：脚本只读「仓库根 `.env`（若存在）+ 当前进程环境变量」，**看不到**
    后台「系统设置」里覆盖的值，也读不到服务器上的部署形态。在开发机裸跑，
    `JWT_KEY` / `ADMIN_PASS` 两条 FAIL 往往只是「本机没有 `.env`」的假警报——
    线上服务能正常返回 `/healthz/ready` 的 JSON，而 `InsecureGates` 会以默认凭据
    **拒绝启动**，这本身就证明服务器已配好。**判据以在服务器上跑（或注入该部署的
    环境变量后跑）的结果为准**，别拿本机输出去改服务器。
  - 同理，`UPLOAD_BASE_URL` / `COOKIE_SECURE` / `WX_APPID`+`WX_APPSECRET` 三项 WARN
    常是「已在后台系统设置里覆盖」而脚本读不到，需到后台人工核对，别照 WARN 重复改环境变量。
- **上线后只读复核（无需登录，三条 curl 就能挡住「静默失效」）**：
  `curl -sSI <域名>/healthz/ready` 必须是 **JSON** 而非 HTML（返回 HTML = 被 SPA 兜底接走，
  后端已死也照样 200）；`curl -sSI <域名>/uploads/<不存在的名字>.png` 必须是
  `404` + `cache-control: no-store`（若带 `immutable` 说明源站配置没更新，
  缩略图回填会静默失效，见 `deploy-runbook.md` §5.2.5）；
  `curl -sS <域名>/api/public/settings` 看 `miniprogram_base_url` 是否已按本部署填写
  （留空时端上回落 `utils/config.ts` 的内置种子域名，换域名时两端都要改）。
- **接最小告警** `scripts/alert_check.sh`（crontab 每分钟，五条规则）：healthz 本地探活 / 5xx / 备份新鲜度 /
  **外部拨测**（配 `EXTERNAL_URL`，覆盖反代 / 证书 / 防火墙这类本机探活看不见的故障）/ **授权到期**（R5，见下方授权条目）。
- **备份任务进 crontab**：`BACKUP_ENC_KEY='<口令>' DB_DRIVER=sqlite scripts/backup.sh`（或 `DB_DRIVER=mysql`），每天一次。
  一次产出**三件套**：库（`.db.gz` / `.sql.gz`）、图片（`uploads-*.tar.gz`）、
  密钥包（`secrets-*.tar.gz.enc`）。SQLite 形态**不能直接 `cp` 库文件**
  （WAL 里还有未合并的页，cp 出来是坏库），必须走这个脚本。
- **`BACKUP_ENC_KEY` 必须设，且口令与备份分开保管**：`runtime.env` 里有 `PII_ENC_KEY`，
  没有它库里的姓名/手机就是解不开的密文（无 re-key）。密钥包用该口令加密后落盘，
  口令请放密码管理器 / 纸质封存，**不要**和备份放同一台机器。
  未设时脚本会**直接失败并删掉本次产物**（确需跳过用 `BACKUP_ALLOW_NO_KEY=1`，会打 WARN）。
  验证：`ls backups/` 应同时看到 `weichuan-*`、`uploads-*`、`secrets-*` 三类文件，且时间戳一致。
- **上线后在后台确认三件套**：「平台管理 → 备份与恢复」应看到三行都是绿色（新鲜度 < 26h）、无黄色缺件提示。
  这一页能看到 cron 的产物、也能「立即备份」补一份、并下载库与图片；
  **但密钥包不提供下载**（后端直接拒）——请用 `scp`/`rsync` 从服务器取走，
  与 `BACKUP_ENC_KEY` 分开保管。**面板不能替代 crontab**：按钮不会自己按，失败也不告警。
- **上线前跑一次恢复演练** `BACKUP_ENC_KEY='<口令>' scripts/restore_drill.sh`：期望退出码 `0`。
  `2` 是「环境不完整、没真正校验」，不算通过。演练覆盖库、图片、密钥包三项；
  「有密钥包却没给口令」判 FAIL。把输出贴进运维记录——没演练过的备份不算备份。
- 数据库：MySQL 形态请确认 `DB_DSN` 指向的实例可达、已建库、迁移已应用到最新、备份策略就位
  （SQLite 形态库文件勿放 NFS，单机单进程；备份用 `server backup` 的 `VACUUM INTO`，
  恢复见 `docs/deploy-runbook.md` §9）。
- 代码层兜底已就位：内置缺省密钥 / 口令会被 `InsecureGates` 拒绝启动，不会带着默认凭据悄悄上线。
- **前端报错已接入服务端日志**：浏览器侧异常（Vue 运行时、`window.onerror`、未捕获 Promise 拒绝）现上报到无鉴权端点 `POST /api/client-error`，服务端以 `[CLIENT-ERROR]` 前缀写日志并带 `X-Request-Id`，可与后端同次请求日志串起来排查。运维侧只需按 `[CLIENT-ERROR]` 关键字在日志 / 归集系统里建视图或告警；DEV 下前端只本地 `console.warn` 不上报，不必担心本地噪音污染。
- **私有 / 独立部署需有效许可证**：私有化交付时设置 `DEPLOY_MODE=private` 并在 `data/license.key` 放入由签发私钥签名的许可证文件（Ed25519，公钥验签）；验签失败或过期时 `cmd/server` 会**拒绝启动**（与 `InsecureGates` 同一类硬门槛）。集中 / 平台模式无需许可证，走系统设置读取。
- **私有 / 独立部署必须注入发行方自己的验签公钥**：环境变量 `LICENSE_PUB`（base64 的 Ed25519 公钥，优先于二进制内嵌值）。**一键做法**（推荐）：在**签发环境**跑 `scripts/license_setup.sh`——生成 Ed25519 密钥对、把公钥写进 `data/runtime.env` 的 `LICENSE_PUB`、私钥落盘到 `license-private.key`（0600，已被 .gitignore 排除），并打印下一步的签发命令。手工做法是 `license keygen` 后自行填 `LICENSE_PUB`；两条路都要求**私钥只留在签发环境、绝不进仓库或二进制**。仓库里内嵌的那把是占位演示密钥（配对私钥是公开的）——私有部署若仍用它，`cmd/server` 会**拒绝启动**并打印换钥指引（自检排在验签之前，避免只看到"授权校验失败"而误判方向）。集中 / 平台模式不验本地许可证文件，不受此限制（保证开源仓库「clone 就能跑」）。
- **生产必须设置 `PII_ENC_KEY`（已升级为启动硬门槛）**：员工 / 申请人 PII 落库用 AES-256-GCM 加密 + 盲索引，密钥来自该环境变量（base64 32 字节，`openssl rand -base64 32` 生成）。未设置时 `cmd/server` **拒绝启动**并打印生成命令（此前只打一条 WARN，生产漏设无人察觉）；本地联调可加 `ALLOW_INSECURE=1` 放行，放行时日志打 `[SECURITY][ALLOW_INSECURE]` 留痕。走**安装向导**的部署会**自动签发**并写入 `data/runtime.env`——该文件由 `scripts/backup.sh` 自动**加密**纳入备份（`secrets-*.tar.gz.enc`，见上方备份条目），不必手工处理；但换钥后旧数据无法解密（没有 re-key 机制）。环境变量或配置文件两种写法都支持。**时间约束：必须在首次写入业务数据之前设定**，否则已用内置 dev 密钥写入的 PII 换钥后再也解不回来。
- **微信隐私保护指引（✅ 2026-11-07 已在 MP 后台审核通过）**：小程序代码已开启 `__usePrivacyCheck__` 并接入自绘授权弹窗（`components/privacy-consent`）；平台侧声明的 5 类权限 + openid 所属「用户标识信息」已于 2026-11-07 审核通过，真机复核不再出现 `errno: 112` 死循环。**新部署仍必须重做这一步**——声明是跟着小程序账号走的，换账号/换主体就要重新提交，且审核 1~2 个工作日是串行阻塞，务必最先提交（逐项步骤与权限对照表见 `docs/real-device-acceptance.md` §0.1）。
- **运行时强制层已生效（取代原 `independent_lock` 只读开关）**：`EnforceLicense` 中间件按 `Entitlement` 落地三类拦截（响应均为 HTTP 200 + JSON `code` 字段）——① 许可证**过期**自动降级只读：拦截写操作（POST/PUT/PATCH/DELETE，白名单登录/刷新/安装/探针/指标/前端报错除外），返回 `{code:2,"授权已过期…只读模式"}`；② **席位上限**：`POST /api/admin/employees` 创建员工时按当前租户（企业）隔离统计的员工数 `>= Seats` 拒绝（`{code:403}`），`Seats<=0` 不限（Phase 3 已落地：集中模式按请求解析的企业隔离；私有 / 独立部署固定为许可证声明的租户，单租户形态下即整个部署）；③ **功能开关**：`RequireFeature` 拦代发等高级功能，未开通返回 `{code:403,"当前授权未开通该功能：name"}`，代发路由已接 `RequireFeature("mp_release")`。运维侧：私有部署「进程先起、授权后过期」时服务还在跑但所有写操作被拦（重启即彻底起不来），集中模式靠系统设置里的 `license_*` 字段（未配置时宽松不强制）。三种提醒通道见下一条。
- **授权到期有三层提醒，别只留一层**：① 客户登录后台即见顶部横幅（`GET /api/admin/license`，`warn` 可关、`urgent`/`expired` 不可关）；② 平台超管在同一接口拿到「已过期 / 7 天内 / 30 天内」汇总与清单；③ 后端每天扫一次写 `[LICENSE][EXPIRING]`，由 `scripts/alert_check.sh` 的 R5 推送 webhook。**第 ③ 层是唯一不需要有人登录的通道**，也是私有部署唯一的指望——只做 ①② 的部署，等于把「发现」寄托在客户先发现。上线后请手动验一次 `grep '\[LICENSE\]\[EXPIRING\]' "$LOG_FILE"` 有输出，否则 `LOG_FILE` 指错了、R5 永远不响。详见 `docs/deploy-runbook.md` §8.6。
- **私有部署必须在到期日之前演练过一次续期**：换 `data/license.key` + 重启才算生效，把「第一次换 key」留到到期当天，就是一次确定会发生的停机。平台 / SaaS 形态相反——改到期日下一请求即恢复，不需要重启，所以两种形态的续费提示文案不同。
- **多租户授权按企业配置（Phase 4 + B4 试用授权）**：`tenant_license` 表（迁移 `0029`）可按企业单独配席位 / 到期时间 / 功能开关；解析链为「该租户的行 → 全局 `license_*` 设置 → 无（不过期 / 不限席位）」。**平台模式下开通企业会自动落一条试用授权行**（口径取「系统设置 → 授权」的 `license_trial_enabled/days/seats`，缺省 14 天 / 5 席），到期自动降级只读——**降权是自动的，恢复不是**：客户续费请到「企业管理 → 授权」把到期日改到未来（或转正/改席位），不然后台一直是只读。存量企业**不会**被补行（无行 = 不限制），所以升级后不会有人突然被停服。私有 / 独立部署不走本表（单租户走 `data/license.key`）。运维可用 `cmd/license show --file data/license.key` 核对一份许可证的到期时间与席位数。
- **存储配额（B3）默认不限制**：`storage_usage` 表（迁移 `0032`）按租户累计上传量，取值链「租户覆盖（`storage_usage.quota_mb`）→ 全局 `storage_quota_mb` → 0 = 不限」。默认 0 是刻意的：升级后突然开始拒绝上传会让人猝不及防。要给 SaaS 报价时在「企业管理 → 授权与配额」里设上限即可（可单租户单独放大，不必为一个大客户放松所有人）。**注意口径**：用量统计自本功能启用起累计、**不含历史文件**，也不含覆盖写（`SaveBytes` 的三个固定名对象）——它回答的是"谁还在往里塞东西"，不是"磁盘上谁占了多少"。上传报「存储空间已用尽」时先看这个页面的已用/上限，再看日志有无 `[STORAGE][WARN]`（那表示累加失败、数字偏低）。

- **审计日志保留期（C3）默认不清理**：后台「系统设置 → 安全 → 审计日志保留天数」缺省 `0`=永不自动删除（删了不可逆，所以不做默认删除）。要合规留存策略就填 180/365；清理动作本身会记一条 `audit.purge` 审计（操作人显示「系统」）。启动清一次 + 每 12 小时一次（`AUDIT_PURGE_MINUTES`）。
- **集中日志（C8）：`LOG_FORMAT=json`**：切完每行一个 JSON 对象（`ts/level/msg/tag`，访问日志还有 `method/path/status/latency_ms/request_id`），配合 `deploy/promtail/config.yml` 进 Loki。不切也能跑，但出事时只能靠 grep 正则捞——「把 5xx 按路径聚合」这类问题答不上来。容器日志轮转已在 compose 里配 `max-size=10m / max-file=7`（**改完要 `--force-recreate`**）。
- **`/metrics` 加第二道门 `METRICS_ALLOW_CIDR`**：即使有人在界面上把 `metrics_enabled` 打开，白名单外的来源访问 `/metrics` 仍一律 404。CIDR 写错时**整体拒绝**（fail-closed），启动日志会点名哪条解析失败。
- **PITR（时间点恢复）要单独开启**：全量备份的恢复点是备份时刻，想回到「误操作前一秒」必须开 binlog（compose 的 mysqld 已带 `--log-bin`）并按小时跑 `scripts/backup_binlog.sh` 归档。SQLite 形态没有 PITR。
- **恢复演练要留档**：`scripts/restore_drill.sh` 结尾会直接打印可粘贴的记录正文，存进 `docs/drill-records/YYYY-MM-DD-<形态>.md`，每季度至少一次。「我们演练过」这句话在没记录时无法向客户/审计证明。
- **CI 门禁**：`.github/workflows/ci.yml` 共 **12 个 job，全部阻塞合并**。后端 `golangci-lint`（`backend/.golangci.yml`，只留 errcheck/govet/ineffassign/staticcheck/unused 五条「静默失败」类规则）、前端类型检查 `vue-tsc`、镜像构建、密钥扫描、许可清单过期检查、**备份链自检**（`scripts/selftest_backup.sh`，验「备份→恢复→逐字节一致」与失败路径）、**发版脚本自检**、依赖漏洞扫描（govulncheck + npm audit，2026-11-03 起）、端到端冒烟（`scripts/e2e_smoke.sh`，2026-11-09 起）、以及 **MySQL parity**（service 容器自供给，2026-11-09 起）均为正式门禁。本地复跑 MySQL parity 见 `docs/deploy-runbook.md` §6。

## 四、必须由本人在外部系统完成（仓库代码无法代劳，2026-11-09 汇总）

> 这一节是「剩下的事」的边界：仓库内能自动化的都已收进 CI / 脚本 / 文档，
> 下面这些**没有一条能靠改代码完成**。已完成的打 ✅ 留痕，未完成的按状态逐项推进；
> 每项都写清「在哪做」和「怎么算做完」，避免把「我以为是做完了」当完成。

| # | 事项 | 在哪做 | 完成判据 | 当前状态 |
|---|---|---|---|---|
| 1 | 《用户隐私保护指引》审核 | mp.weixin.qq.com → 设置 → 服务内容声明 | 审核通过 + 真机点「存入通讯录」不再 `errno 112` | ✅ 2026-11-07 通过 |
| 2 | 真机验收（#163） | 真机 + 体验版小程序 | §0 / §1–§4 / §5 / R1–R4 全部通过并留档 | ✅ 2026-11-08 用户口述确认通过（**无逐项截图**，需对外举证时补一轮） |
| 3 | 小程序工信部备案 | 微信公众平台 → 小程序备案 | 备案号已下发并公示在关于页；只备域名不备小程序会在提审最后一步驳回 | ⬜ 需本人办理 |
| 4 | 三类服务器合法域名 | mp.weixin.qq.com → 开发管理 → 开发设置 | `request` / `uploadFile` / `downloadFile` 都含生产域名（见 §0.5） | ⬜ 需本人在后台核对 |
| 5 | 私有部署换真 `LICENSE_PUB` | 构建期注入公钥，重签授权 | 旧占位公钥签发的证全部失效；与客户约好切换窗口再换 | ⬜ 需在私有交付时办理 |
| 6 | 手工部署设 `PII_ENC_KEY` | 部署机环境变量 / `runtime.env` | **在写任何 PII 之前**设定并备份；无 re-key，换钥＝存量密文不可解 | ⬜ 需在每台手工部署机核对 |
| 7 | 三把强随机密钥与后台凭据 | 部署环境 / 后台「系统设置」 | `JWT_KEY` / `ADMIN_PASS` 非缺省值；`WX_*`、对象存储、CDN 凭据配齐且真机能用 | ⬜ 需在服务器上核对（`preflight.sh` 的开发机 WARN/FAIL 多为假警报，判据见其注释） |
| 8 | Cloudflare 面板两项手工配置（E3） | CF 控制台 | 缓存规则按状态码分流已同步到线上；`/uploads/` 404 实测 `cache-control: no-store` + `cf-cache-status: BYPASS` | ✅ 404 缓存雷 2026-11-07 线上复核已闭环；面板其余项按部署形态人工核对 |
| 9 | binlog 回放 / PITR 演练 | 服务器 + `docs/drill-records/` | 真跑一次时间点恢复并把记录正文存档 | ⬜ 运维动作，需留档 |
| 10 | 恢复演练季度节奏 | 服务器 + `scripts/restore_drill.sh` | 每季度至少一次，记录存进 `docs/drill-records/YYYY-MM-DD-<形态>.md` | ⬜ 周期性运维，非一次性可关 |
| 11 | 私有部署续期演练 | 客户现场 `data/license.key` + 重启 | 到期前换过一次 key 并确认写操作恢复，别把首次换证留到到期当天 | ⬜ 需约客户时间 |
| 12 | C4 水平扩展（Redis） | 架构演进 | 多副本共享限流/会话 | ⏸ **明确暂缓**：当前单实例部署不需要，不是缺陷 |

> 状态口径：✅ 已完成 / ⬜ 必须人工完成 / ⏸ 主动暂缓。
> 仓库侧已闭环的证据分别在 `docs/real-device-acceptance.md`、
> `docs/commercial-readiness.md` 第十四轮订正块、第十八轮实施记录，
> 以及 `tools/nginx-check/verify.sh` 的行为守门。

## 五、一次上线窗口执行表（按依赖排序，2026-11-09）

> §四回答「还剩哪些事」，本节回答「**按什么顺序做完**」。分阶段不是好看，是被三条硬约束逼出来的：
> ① 小程序备案是唯一以「工作日」计的串行项，不最先启动就会卡住提审最后一步；
> ② `PII_ENC_KEY` 必须在**写入任何 PII 之前**定下且备份，事后无法补（无 re-key）；
> ③ 换 `LICENSE_PUB` 会让旧证全失效，必须与客户约好切换窗口，不能夹在上线当天做。
>
> 编号沿用 §四。标「🤖」的项有只读探测可替你判「生效没」，标「👤」的只能本人看。

| 阶段 | 时点 | 编号 · 事项 | 在哪做 | 完成判据 | 依赖 / 备注 |
|---|---|---|---|---|---|
| 0 | **T−5 工作日起** | #3 小程序工信部备案 👤 | 微信公众平台 → 小程序备案 | 备案状态「已通过」，备案号已公示在小程序关于页 | **最长串行项，今天就要启动**；只备域名不备小程序，提审最后一步直接驳回 |
| 0 | 与备案并行 | #6 `PII_ENC_KEY` 👤 | 部署机环境变量 / `data/runtime.env` | 首次写 PII **之前**设定；装完立即随配置文件备份，确认备份可还原 | 无 re-key，换钥＝存量密文全变天书且**不报错**。每台手工部署机各核一次 |
| 0 | 与备案并行 | #5 私有部署换真 `LICENSE_PUB` 👤 | 构建期注入公钥 + 重签授权 | 旧占位公钥签发的证全部失效；切换窗口已与客户约定 | 仅私有交付形态；平台 / SaaS 形态跳过 |
| 1 | T−2 ~ T−1 | #4 三类服务器合法域名 👤 | mp.weixin.qq.com → 开发管理 → 开发设置 | `request` / `uploadFile` / `downloadFile` **三类**都含 `https://weichuan.ziyuanxingqiu.cn` | 域名改动有次数限制与生效延迟，必须早于阶段 2 的真机复核；逐项判据见 `docs/real-device-acceptance.md` §0.5 |
| 1 | T−1 | #7 强随机密钥与后台凭据 🤖 | 部署环境 / 后台「系统设置」 | `JWT_KEY` / `ADMIN_PASS` 非内置缺省值；`WX_*`、对象存储、CDN 凭据配齐；`[SECURITY]` 告警清零 | **必须在该部署的服务器上**跑 `preflight.sh`，开发机裸跑的 FAIL 多是「本机无 .env」假警报；见 §三与脚本注释 |
| 1 | T−1 | #8 Cloudflare 面板两项 👤 | CF 控制台 | ① Browser Cache TTL = **Respect Existing Headers**（否则 `/uploads/` immutable 被覆盖成 4h）；② HTML 边缘短缓存 60s 规则 | ② 的前置是「发版不清空 `assets/`」。配置后用下方探测块第 3/4 条实测，别只看面板开关 |
| 1 | T−1 | 迁移目录同步 🤖 | 发版脚本 / 服务器 | 上线同步**整个** `backend/migrations/`；`deploy_frontend.sh --require-migrations` 不报缺文件；启动日志**无** `[MIGRATE][FATAL]` | 只换二进制不换迁移 → 运行期才 `Unknown column`；补齐后 `./server migrate`，**不必重启** |
| 2 | **T 日** | 一键只读探测 🤖 | 服务器 | 下方探测块 8 条全部符合预期 | 全程 GET/HEAD，不写库、不重启、不清缓存 |
| 2 | T 日 | 三类域名真机生效 👤 | 真机 + 体验版小程序 | 上传头像（uploadFile）、海报换背景 / 小程序码（downloadFile）、普通接口（request）都通 | **开发者工具勾了「不校验合法域名」时完全复现不出来**，必须真机 |
| 2 | T 日 | #1 隐私指引复核 👤 | 真机 | 点「存入通讯录」不再 `errno 112` 死循环 | 2026-11-07 已审核通过；**换账号 / 换主体须重新提交**，审核 1~2 工作日又是串行阻塞 |
| 2 | T 日 | WX 凭据可用 🤖 | 后台「生成小程序码」 | 能出图，海报二维码与微信登录不卡 | `PREFLIGHT_WX_LIVE=1 scripts/preflight.sh` 可真调微信接口校验配对（只读校验） |
| 3 | T+1 ~ T+7 | #10 恢复演练 👤 | 服务器 + `scripts/restore_drill.sh` | 退出码 0；把脚本结尾打印的「留档正文」粘进 `docs/drill-records/YYYY-MM-DD-<形态>.md` | 每季度至少一次；升级迁移 / 改备份脚本 / 换部署形态后补跑 |
| 3 | T+1 ~ T+7 | #9 binlog 回放 / PITR 演练 👤 | 服务器 + 留档 | 真跑一次时间点恢复并写进 `docs/drill-records/` | SQLite 形态没有 PITR；MySQL 需开 binlog 与 `scripts/backup_binlog.sh` 归档 |
| 3 | 到期前 | #11 私有部署续期演练 👤 | 客户现场 `data/license.key` + 重启 | 换 key 后写操作恢复；首次换证不留在到期当天 | 需约客户时间；平台形态改到期日下一请求即恢复，不用重启 |

**放弃上线的硬条件**（任一命中就别发版）：备案未通过 / 三类域名缺任一 / 服务器上 `preflight.sh` 有 FAIL / `/healthz/ready` 返回 HTML / `/uploads/` 404 带 `immutable` / 启动日志有 `[MIGRATE][FATAL]` / `PII_ENC_KEY` 未备份。

### 一键只读探测（在服务器上整段粘贴）

```bash
# 在**仓库根目录**、该部署的服务器上整段粘贴；只读：只发 GET/HEAD，不写库、不重启服务、不清缓存。
# 把 BASE 换成实际生产域名。
BASE="https://weichuan.ziyuanxingqiu.cn"
HOST="${BASE#https://}"

# 1) 就绪探针必须回 JSON；返回 HTML 说明被 SPA 兜底接走了（后端已死也照样 200）
curl -sS --noproxy '*' -o /tmp/wc-healthz.out -w 'healthz HTTP %{http_code}\n' "$BASE/healthz/ready"
head -c 200 /tmp/wc-healthz.out; echo

# 2) SPA 兜底：必须带 Accept: text/html，否则命中 404 分支是预期行为
curl -sSI --noproxy '*' -H 'Accept: text/html' "$BASE/login" | head -5

# 3) /uploads/ 404 绝不能被长缓存（CF 把「文件不存在」锁死一年的老事故）
curl -sSI --noproxy '*' "$BASE/uploads/__launch_probe_missing__.png" \
  | grep -iE '^(HTTP/|cache-control:|cf-cache-status:)'
#    期望：HTTP/2 404 + cache-control: no-store + cf-cache-status: BYPASS

# 4) 对照：一个真实存在对象仍应是 immutable 长缓存（改 404 不能伤到 200）
#    先把 OBJ 换成真实对象名；别把占位符直接留在 URL 里（`<` 会被 shell 当成重定向）
OBJ="换成真实对象名"
curl -sSI --noproxy '*' "$BASE/uploads/$OBJ" | grep -iE '^(HTTP/|cache-control:)'

# 5) 三个安全头 + 证书有效期
curl -sSI --noproxy '*' "$BASE/api/admin/login" \
  | grep -iE '^(HTTP/|x-content-type-options:|x-frame-options:|referrer-policy:)'
echo | openssl s_client -servername "$HOST" -connect "$HOST:443" 2>/dev/null \
  | openssl x509 -noout -dates

# 6) 零依赖静态自检（必须在该部署的服务器上跑，且注入该部署的环境变量）
PREFLIGHT_STRICT=1 PUBLIC_BASE_URL="$BASE" scripts/preflight.sh

# 7) 启动日志里不该出现的三件事；有输出就要查，不能当噪音
grep -E '\[SECURITY\]|\[MIGRATE\]\[FATAL\]|You trusted all proxies' /path/to/backend.log | head -20 \
  || echo '无命中（符合预期）'

# 8) 服务端时区：TZ 不设会静默按 UTC，统计每天 00:00–08:00 悄悄错位
docker exec weichuan-backend date 2>/dev/null || date
```

> 第 4 步的「真实对象名」和第 7 步的日志路径按现场替换；第 6 步 `PREFLIGHT_STRICT=1` 会把 WARN 也算失败，
> 但 `UPLOAD_BASE_URL` / `COOKIE_SECURE` / `WX_*` 这类 WARN 若值在后台「系统设置」里配过，是脚本读不到覆盖值的假警报，
> 到界面人工核对即可，**别照 WARN 回头重复改环境变量**。
> 这套 Nginx 行为另有桩版守门：`tools/nginx-check/run.sh`（95 条断言，会自动起 / 关 nginx 容器）；
> 直接裸跑 `verify.sh` 会因为没起服务而报一整片假失败。

### 仍然无法自动核验、必须本人看一眼的

备案号与提审状态（MP 后台）、三类合法域名是否逐条登记（MP 后台）、CF 面板两个开关（CF 控制台）、
`PII_ENC_KEY` 备份是否真的可还原、`LICENSE_PUB` 切换窗口是否与客户约好、真机上三类域名相关功能是否真通。
这些没有任何脚本能替你确认——**「界面上勾了」不等于「生效了」**，判据一律以上表为准。

## 关联文档
- 生产上线 Runbook：`docs/deploy-runbook.md`
- 部署与联调：`部署与联调.md`
- 安全边界：`SECURITY.md`
