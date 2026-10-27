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
| **探针地址必须直通后端**（`/healthz`） | 反代配置：给 `/healthz` 单独一条透传规则 | `curl 域名/healthz` 返回 **JSON**（不是 HTML）。返回 HTML 说明被单页应用的兜底规则接走了，此时后端已死也照样 200 | ✅ 第 8 / 10 节都验 `Content-Type`，返回 HTML 判 FAIL |
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

## 三、运行级（上线后看护）

- **部署前跑一遍** `bash scripts/preflight.sh`：期望全 `PASS`；`PREFLIGHT_STRICT=1` 时 `WARN` 也返回退出码 1。
- **接最小告警** `scripts/alert_check.sh`（crontab 每分钟）：healthz 本地探活 / 5xx / 备份新鲜度 /
  **外部拨测**（配 `EXTERNAL_URL`，覆盖反代 / 证书 / 防火墙这类本机探活看不见的故障）。
- **备份任务进 crontab**：`DB_DRIVER=sqlite scripts/backup.sh`（或 `DB_DRIVER=mysql`），每天一次。
  SQLite 形态**不能直接 `cp` 库文件**（WAL 里还有未合并的页，cp 出来是坏库），必须走这个脚本。
- **上线前跑一次恢复演练** `scripts/restore_drill.sh`：期望退出码 `0`。
  `2` 是「环境不完整、没真正校验」，不算通过。把输出贴进运维记录——
  没演练过的备份不算备份。
- 数据库：MySQL 形态请确认 `DB_DSN` 指向的实例可达、已建库、迁移已应用到最新、备份策略就位
  （SQLite 形态库文件勿放 NFS，单机单进程；备份用 `server backup` 的 `VACUUM INTO`，
  恢复见 `docs/deploy-runbook.md` §9）。
- 代码层兜底已就位：内置缺省密钥 / 口令会被 `InsecureGates` 拒绝启动，不会带着默认凭据悄悄上线。
- **前端报错已接入服务端日志**：浏览器侧异常（Vue 运行时、`window.onerror`、未捕获 Promise 拒绝）现上报到无鉴权端点 `POST /api/client-error`，服务端以 `[CLIENT-ERROR]` 前缀写日志并带 `X-Request-Id`，可与后端同次请求日志串起来排查。运维侧只需按 `[CLIENT-ERROR]` 关键字在日志 / 归集系统里建视图或告警；DEV 下前端只本地 `console.warn` 不上报，不必担心本地噪音污染。
- **私有 / 独立部署需有效许可证**：私有化交付时设置 `DEPLOY_MODE=private` 并在 `data/license.key` 放入由签发私钥签名的许可证文件（Ed25519，公钥验签）；验签失败或过期时 `cmd/server` 会**拒绝启动**（与 `InsecureGates` 同一类硬门槛）。集中 / 平台模式无需许可证，走系统设置读取。
- **私有 / 独立部署必须注入发行方自己的验签公钥**：环境变量 `LICENSE_PUB`（base64 的 Ed25519 公钥，优先于二进制内嵌值）。用 `license keygen` 生成密钥对，**公钥**填进 `LICENSE_PUB`，**私钥只留在签发环境、绝不进仓库或二进制**。仓库里内嵌的那把是占位演示密钥（配对私钥是公开的）——私有部署若仍用它，`cmd/server` 会**拒绝启动**并打印换钥指引（自检排在验签之前，避免只看到"授权校验失败"而误判方向）。集中 / 平台模式不验本地许可证文件，不受此限制（保证开源仓库「clone 就能跑」）。
- **生产必须设置 `PII_ENC_KEY`（已升级为启动硬门槛）**：员工 / 申请人 PII 落库用 AES-256-GCM 加密 + 盲索引，密钥来自该环境变量（base64 32 字节，`openssl rand -base64 32` 生成）。未设置时 `cmd/server` **拒绝启动**并打印生成命令（此前只打一条 WARN，生产漏设无人察觉）；本地联调可加 `ALLOW_INSECURE=1` 放行，放行时日志打 `[SECURITY][ALLOW_INSECURE]` 留痕。走**安装向导**的部署会**自动签发**并写入 `data/runtime.env`——请务必把该文件纳入备份：换钥后旧数据无法解密（没有 re-key 机制）。环境变量或配置文件两种写法都支持。**时间约束：必须在首次写入业务数据之前设定**，否则已用内置 dev 密钥写入的 PII 换钥后再也解不回来。
- **微信隐私保护指引必须在 MP 后台配好**：小程序代码已开启 `__usePrivacyCheck__` 并接入自绘授权弹窗（`components/privacy-consent`），但**平台侧未声明的隐私类型会直接禁用对应接口**（`errno: 112`）。需在「小程序管理后台 → 设置 → 服务内容声明 → 用户隐私保护指引」声明：收集你的昵称、头像、选中的照片或视频信息、使用你的相册（仅写入）权限、读取你的剪切板、使用你的通讯录（仅写入）权限，以及 openid 所属的「用户标识信息」。未声明时提审会被驳回 / 真机对应功能不可用。
- **运行时强制层已生效（取代原 `independent_lock` 只读开关）**：`EnforceLicense` 中间件按 `Entitlement` 落地三类拦截（响应均为 HTTP 200 + JSON `code` 字段）——① 许可证**过期**自动降级只读：拦截写操作（POST/PUT/PATCH/DELETE，白名单登录/刷新/安装/探针/指标/前端报错除外），返回 `{code:2,"授权已过期…只读模式"}`；② **席位上限**：`POST /api/admin/employees` 创建员工时按当前租户（企业）隔离统计的员工数 `>= Seats` 拒绝（`{code:403}`），`Seats<=0` 不限（Phase 3 已落地：集中模式按请求解析的企业隔离；私有 / 独立部署固定为许可证声明的租户，单租户形态下即整个部署）；③ **功能开关**：`RequireFeature` 拦代发等高级功能，未开通返回 `{code:403,"当前授权未开通该功能：name"}`，代发路由已接 `RequireFeature("mp_release")`。运维侧：私有部署过期时服务虽起得来但所有写操作被拦，需在日志按 `EnforceLicense` / `[LICENSE]` 关键字建告警，及时提醒续签；集中模式靠系统设置里的 `license_*` 字段（未配置时宽松不强制）。
- **多租户授权按企业配置（Phase 4 + B4 试用授权）**：`tenant_license` 表（迁移 `0029`）可按企业单独配席位 / 到期时间 / 功能开关；解析链为「该租户的行 → 全局 `license_*` 设置 → 无（不过期 / 不限席位）」。**平台模式下开通企业会自动落一条试用授权行**（口径取「系统设置 → 授权」的 `license_trial_enabled/days/seats`，缺省 14 天 / 5 席），到期自动降级只读——**降权是自动的，恢复不是**：客户续费请到「企业管理 → 授权」把到期日改到未来（或转正/改席位），不然后台一直是只读。存量企业**不会**被补行（无行 = 不限制），所以升级后不会有人突然被停服。私有 / 独立部署不走本表（单租户走 `data/license.key`）。运维可用 `cmd/license show --file data/license.key` 核对一份许可证的到期时间与席位数。
- **存储配额（B3）默认不限制**：`storage_usage` 表（迁移 `0032`）按租户累计上传量，取值链「租户覆盖（`storage_usage.quota_mb`）→ 全局 `storage_quota_mb` → 0 = 不限」。默认 0 是刻意的：升级后突然开始拒绝上传会让人猝不及防。要给 SaaS 报价时在「企业管理 → 授权与配额」里设上限即可（可单租户单独放大，不必为一个大客户放松所有人）。**注意口径**：用量统计自本功能启用起累计、**不含历史文件**，也不含覆盖写（`SaveBytes` 的三个固定名对象）——它回答的是"谁还在往里塞东西"，不是"磁盘上谁占了多少"。上传报「存储空间已用尽」时先看这个页面的已用/上限，再看日志有无 `[STORAGE][WARN]`（那表示累加失败、数字偏低）。

- **审计日志保留期（C3）默认不清理**：后台「系统设置 → 安全 → 审计日志保留天数」缺省 `0`=永不自动删除（删了不可逆，所以不做默认删除）。要合规留存策略就填 180/365；清理动作本身会记一条 `audit.purge` 审计（操作人显示「系统」）。启动清一次 + 每 12 小时一次（`AUDIT_PURGE_MINUTES`）。
- **集中日志（C8）：`LOG_FORMAT=json`**：切完每行一个 JSON 对象（`ts/level/msg/tag`，访问日志还有 `method/path/status/latency_ms/request_id`），配合 `deploy/promtail/config.yml` 进 Loki。不切也能跑，但出事时只能靠 grep 正则捞——「把 5xx 按路径聚合」这类问题答不上来。容器日志轮转已在 compose 里配 `max-size=10m / max-file=7`（**改完要 `--force-recreate`**）。
- **`/metrics` 加第二道门 `METRICS_ALLOW_CIDR`**：即使有人在界面上把 `metrics_enabled` 打开，白名单外的来源访问 `/metrics` 仍一律 404。CIDR 写错时**整体拒绝**（fail-closed），启动日志会点名哪条解析失败。
- **PITR（时间点恢复）要单独开启**：全量备份的恢复点是备份时刻，想回到「误操作前一秒」必须开 binlog（compose 的 mysqld 已带 `--log-bin`）并按小时跑 `scripts/backup_binlog.sh` 归档。SQLite 形态没有 PITR。
- **恢复演练要留档**：`scripts/restore_drill.sh` 结尾会直接打印可粘贴的记录正文，存进 `docs/drill-records/YYYY-MM-DD-<形态>.md`，每季度至少一次。「我们演练过」这句话在没记录时无法向客户/审计证明。
- **CI 门禁**：后端 `golangci-lint`（`backend/.golangci.yml`，只留 errcheck/govet/ineffassign/staticcheck/unused 五条「静默失败」类规则）、前端类型检查 `vue-tsc`、镜像构建、密钥扫描、许可清单过期检查为门禁；依赖漏洞扫描（govulncheck + npm audit）与端到端冒烟（`scripts/e2e_smoke.sh`）先非阻塞跑着积累数据。

## 关联文档
- 生产上线 Runbook：`docs/deploy-runbook.md`
- 部署与联调：`部署与联调.md`
- 安全边界：`SECURITY.md`
