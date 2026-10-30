# 微传名片 — 后端骨架（Go + Gin + GORM + MySQL）

对应《首页组件与接口字段映射》《跳转页组件与接口字段映射》两份文档。骨架已覆盖需求中的读操作与写操作（员工/超管登录、开通申请、名片修改审核流、员工管理）。

## 目录结构
```
backend/
├── cmd/server/main.go           # 入口：连 DB、注册路由、启动（含 purge 启动清理 / 可观测中间件）
├── cmd/devserver/main.go        # 开发态入口：SQLite（免 cgo）+ 自动 seed 演示数据
├── cmd/license/main.go          # 许可证签发工具（签发方专用，不随服务发布）：keygen / sign / show
├── internal/
│   ├── config/config.go         # 环境变量注入（DB_DSN / PORT / JWT_KEY / 上传 / 小程序码 / 超管 / TTL / METRICS_ENABLED）
│   ├── model/models.go          # GORM 模型（员工/企业/租户/分类/项目/服务/布局/统计/管理员/两类申请/吊销会话）
│   ├── repository/repository.go # 数据访问层（公开内容查询按 company_id 隔离）
│   ├── repository/session.go    # 会话黑名单读写 + 过期清理（revoked_sessions）
│   ├── auth/jwt.go              # JWT 双令牌签发 / 解析（access 15m + refresh 30d，sid 会话维度，claims 带 companyId）
│   ├── auth/password.go         # bcrypt 哈希 / 校验
│   ├── handler/handler.go       # 首页相关 HTTP 接口（读，按租户：companyId 缺省落演示租户）
│   ├── handler/auth.go          # 员工/管理员登录，签发 JWT + 刷新 + 登出（企业停用校验）
│   ├── handler/apply.go         # 开通申请 / 名片修改审核 / 员工管理 / 统计（写 + 审核流，按租户隔离）
│   ├── handler/stat.go          # 埋点统计共享件：track 限流+白名单 / targetId 校验 / 日期范围 / 按天趋势 / CSV 导出 / 明细列表 / TOP 榜
│   ├── handler/content.go       # 企业内容管理（分类/项目/服务/简介…）按租户隔离 + 分类删除强校验
│   ├── handler/companies.go     # 企业管理（平台超管）：开通/停用企业、企业管理员管理、/api/admin/me
│   ├── handler/tenant.go        # 多租户助手：租户范围解析 / 目标租户 / 企业启停校验
│   ├── handler/cookie.go        # httpOnly Cookie 读写（PC 后台鉴权）
│   ├── handler/seed.go          # 首次启动自动创建初始平台超管（SeedAdmin，company_id=0）
│   ├── handler/wechat.go        # 小程序码生成 + 缓存（写入当前存储驱动）
│   ├── handler/company_wx.go    # 租户小程序凭据 GET/PUT（appSecret 只写不读，永不回传）
│   ├── handler/mp_release.go    # 小程序版本发布：登记 / 提审 / 查审核状态 / 发布 / 撤回（状态机白名单流转）
│   ├── wechat/wechat.go         # access_token 缓存（**按 appid 分键**）+ 通用 POST 助手（40001/42001 自动刷新重试）
│   ├── wechat/release.go        # 微信侧四个接口：submit_audit / get_auditstatus / release / undo_code_audit
│   ├── audit/audit.go           # 审计落库（动作常量 + 敏感字段脱敏），每次代发都记一条
│   ├── handler/assets.go        # 开源素材库：清单 / 采用 / 预览（go:embed 内置，免外部找图）
│   ├── assets/assets.go         # 素材库加载器：embed library/ + manifest.json 解析 + 安全读取
│   ├── assets/library/          # 内置素材资产（生成物，勿手改）：manifest.json + CREDITS.md + 图标/封面/LOGO/头像
│   ├── assets/gen_library.py    # 素材生成器（Lucide 图标 + PIL 程序化图形 → PNG/JPEG + manifest）
│   ├── storage/storage.go        # Uploader 接口 + LocalUploader（默认）
│   ├── storage/minio.go          # MinIO / S3 兼容实现
│   ├── storage/oss.go            # 阿里云 OSS 实现
│   └── middleware/
│       ├── auth.go              # JWT 鉴权 + 角色守卫 + 平台超管守卫 + 会话黑名单校验
│       ├── ratelimit.go         # 登录 IP 限流（固定窗口 + 过期桶惰性清理 + 跨副本计数接口）
│       └── observability.go     # 可观测三件套：RequestID / AccessLog / Metrics（/metrics）
├── migrations/0001_init.sql      # 建表 + 经典业绩 7 分类种子
├── migrations/0002_apply.sql    # 审核流表 + employee.staff_id 自增
├── migrations/0003_soft_delete.sql   # 软删除列
├── migrations/0004_token_version.sql # token_version 安全戳（账号级吊销）
├── migrations/0005_revoked_sessions.sql # 会话黑名单表（单设备精确吊销）
├── migrations/0006_employee_landline.sql
├── migrations/0007_updated_at.sql   # 员工座机列（名片「座机」行优先本人，为空回退公司总机）
├── migrations/0008_open_apply_landline.sql  # 开通申请增加座机字段
├── migrations/0009_company_short_name.sql   # 企业简称（小程序顶栏居中展示）
├── migrations/0010_wechat_identity.sql     # 微信身份：openid + 昵称
├── migrations/0011_company_tenant.sql      # 多租户第一期：company 租户表 + 各表 company_id + admin 两级
├── migrations/0012_company_admin_short_name.sql  # 企业简称（company.short_name）：侧边栏品牌名 + 公司信息简称初始值
├── migrations/0013_home_layout_background.sql  # 首页背景图（home_layout.bg_image / bg_mask）
├── migrations/0025_mp_release.sql               # 小程序版本发布：mp_release 表（提审/发布状态机 + 审计字段）
├── migrations/0026_company_wx_app_release_enabled.sql  # 租户「允许平台代发版本」开关 release_enabled
├── Dockerfile                   # 多阶段构建，单二进制镜像
└── go.mod
```

## 本地运行

`DB_DSN` 同时支持 MySQL 与 SQLite，驱动按 DSN 形态自动选择（`internal/dbopen`），同一个二进制不用换包：

| 写法 | 驱动 | 说明 |
|---|---|---|
| `root:pw@tcp(127.0.0.1:3306)/weichuan?charset=utf8mb4&parseTime=true` | MySQL | 生产标准路径，表结构由 `migrations/*.sql` 建立 |
| `sqlite:///var/lib/weichuan/weichuan.db` | SQLite | 免装数据库；启动时按模型 `AutoMigrate` 建表，自动补 WAL / busy_timeout / foreign_keys |
| `file:/var/lib/weichuan/weichuan.db` | SQLite | 同上，URI 写法 |
| `/var/lib/weichuan/weichuan.db` | SQLite | 裸路径兜底（仅认 `.db` / `.sqlite` / `.sqlite3` 结尾） |

> 用 MySQL 时首次部署要建库并导入迁移；**升级则不用手工跑**——服务启动会按
> `schema_migrations` 记账表自动补齐未应用的迁移（详见下节）。用 SQLite 时
> **不要**执行 `migrations/*.sql`
> （那些是 MySQL 方言，`ENGINE=InnoDB` / `AUTO_INCREMENT` / 反引号 SQLite 都不认）。
> SQLite 的适用边界（单机单进程、库文件必须在本地磁盘、备份用 `VACUUM INTO`）
> 见仓库根 `docs/deploy-runbook.md`「路径 D：SQLite 单机生产」。

### 数据库迁移（MySQL）

`migrations/*.sql` 是 MySQL 方言，由 `internal/migrate` 按文件名顺序应用，已应用的记录
在 `schema_migrations(version, applied_at)`。两个入口用的是同一条代码路径：

| 场景 | 怎么做 |
|---|---|
| 首次部署（空库） | 安装向导的 MySQL 分支（或手工 `mysql < migrations/*.sql`） |
| 版本升级（存量库） | **启动时自动补齐**——只做增量，幂等；多副本用 `GET_LOCK` 串行化 |
| 想自己掌控 | `AUTO_MIGRATE=0` 关闭自动迁移，改跑 `./server migrate`（不启服务，退出码可进流水线） |

- 幂等性：语句重复执行报的 `1060 Duplicate column` / `1050 already exists` /
  `1062 Duplicate entry` 一律忽略，因此存量库首次被接管时重放全部迁移也安全。
- 失败不记账：某个迁移失败时**不会**把它记成已应用，下次启动会重试；启动日志打
  `[MIGRATE][FATAL]` 且不终止服务（保留「进后台查日志、修完重启自愈」的入口），
  代价是这条日志必须被看见——请把它纳入日志告警匹配词。
- SQLite 部署不适用：表由启动时按模型 `AutoMigrate` 维护（`./server migrate` 会直接说明）。

```bash
# 手工跑一次（不启服务）
DB_DSN='root:pw@tcp(127.0.0.1:3306)/weichuan?charset=utf8mb4&parseTime=true' ./server migrate
# 查看已应用版本
mysql -u root -p weichuan -e "SELECT version, applied_at FROM schema_migrations ORDER BY version;"
```

### 运维子命令：backup / dbcheck

与 `migrate` 同一定位——不启服务、只做一件事、可进 cron 与流水线。

```bash
# SQLite 在线一致性快照（VACUUM INTO，不阻塞写；产物是干净单文件）
./server backup --out /tmp/weichuan-snapshot.db

# 只读体检任意 SQLite 库文件：integrity_check / 表数 / 各表行数
./server dbcheck --file /tmp/weichuan-snapshot.db
./server dbcheck --file /tmp/weichuan-snapshot.db --json
./server dbcheck --file /tmp/weichuan-snapshot.db --field tables   # 取单个值给脚本用
```

- `backup` 只支持 SQLite；MySQL 请用 `scripts/backup.sh`（`mysqldump --single-transaction`
  才有一致性快照，Go 侧不做这件事）。
- `dbcheck` 以 `mode=ro` 只读打开，**不会创建不存在的文件**——否则体检一个写错的路径
  会得到「0 张表、integrity ok」这种看起来正常的结论。

### 签发工具：cmd/license（签发方专用，不随服务发布）

```bash
go build -o licensectl ./cmd/license

# 1) 生成密钥对：公钥填进 internal/license/embed.go 的 embeddedPublicKeyB64，
#    私钥只留签发环境（绝不进仓库 / 服务二进制）
./licensectl keygen

# 2) 签发一份许可证（私钥也可经 LICENSE_PRIV 传入，避免出现在进程列表 / shell 历史）
./licensectl sign --priv "$PRIV" --tenant 7 --edition private \
  --seats 50 --expires 2027-12-31T23:59:59Z \
  --features mp_release,content_sec --out data/license.key

# 3) 运维核对：读取并验签展示一份许可证（不传 --pub 时用服务内嵌公钥）
./licensectl show --file data/license.key
```

- 为什么单独一个二进制：签名私钥只应存在于签发环境。把它编译进随服务分发的
  二进制，等于把「给任何人签一张永久授权」的能力发到了客户机器上。
- `--seats 0` = 不限席位；不传 `--expires` = 永不过期；`--features` 空 = 全部开通。
- 许可证是 Ed25519 签名（载荷 + 签名两行 base64），改一个字节即验签失败；
  私有部署（`DEPLOY_MODE=private`）验签失败或过期时 `cmd/server` 拒绝启动。
- 这两个子命令是 `scripts/backup.sh` / `restore.sh` / `restore_drill.sh` 的底层，
  容器部署时由脚本 `docker exec` 调用，原生部署可设 `SERVER_BIN` 直接跑。

```bash
# 1. 建库并导入全部迁移（含 7 分类种子 / 审核流表 / 软删除 / token_version / 会话黑名单 / 多租户 company_id）——仅 MySQL 需要
for f in migrations/000*.sql; do mysql -u root -p weichuan < "$f"; done

# 2. 配置环境变量（JWT_KEY 必填，用于 token 签名）
export DB_DSN="root:root@tcp(127.0.0.1:3306)/weichuan?charset=utf8mb4&parseTime=true"
# 或走 SQLite（免装数据库，表由服务启动时自动建）：
# export DB_DSN="sqlite:///var/lib/weichuan/weichuan.db"
export PORT=8080
export JWT_KEY="change-me-to-a-long-random-string"
# 文件上传（可选，有默认值）
export UPLOAD_DIR="./uploads"
export UPLOAD_BASE_URL="http://127.0.0.1:8080/uploads"
# 文件存储驱动：local(默认) | minio | oss；上传/小程序码均写入该驱动
export STORAGE_DRIVER="local"
# —— MinIO / S3 兼容（STORAGE_DRIVER=minio）——
export MINIO_ENDPOINT="play.min.io"
export MINIO_ACCESS_KEY="your-access-key"
export MINIO_SECRET_KEY="your-secret-key"
export MINIO_BUCKET="weichuan"
export MINIO_USE_SSL="true"
# —— 阿里云 OSS（STORAGE_DRIVER=oss）——
export OSS_ENDPOINT="oss-cn-hangzhou.aliyuncs.com"
export OSS_ACCESS_KEY_ID="your-access-key-id"
export OSS_ACCESS_KEY_SECRET="your-secret"
export OSS_BUCKET="weichuan"
# 注：MINIO/OSS 模式下 UPLOAD_BASE_URL 改为对象存储的公开访问基址（如 https://cdn.xxx 或 bucket 公网域名）
# 存储可见性：true=公开桶（默认）；false=私有桶，需配合限时签名访问（STORAGE_PUBLIC=false）
export STORAGE_PUBLIC="true"
# CDN 刷新（覆盖写后使边缘缓存失效）：CDN_ENABLED=1 + 厂商 + 该厂商要求的凭据
export CDN_ENABLED="false"
# 厂商：aliyun / tencent / qiniu / cloudflare / cloudfront（留空按 aliyun）
export CDN_PROVIDER="aliyun"
export CDN_ACCESS_KEY_ID=""      # aliyun=AccessKeyId / tencent=SecretId / qiniu=AccessKey / cloudfront=AccessKeyId
export CDN_ACCESS_KEY_SECRET=""  # aliyun=Secret / tencent=SecretKey / qiniu=SecretKey / cloudfront=SecretAccessKey / cloudflare=API Token
export CDN_RESOURCE_ID=""        # 仅 cloudflare=Zone ID / cloudfront=Distribution ID
export CDN_ENDPOINT=""           # 留空按厂商取默认（cdn.aliyuncs.com / cdn.tencentcloudapi.com / fusion.qiniuapi.com / api.cloudflare.com / cloudfront.amazonaws.com）
# 微信小程序码（wxacode）所需凭据
export WX_APPID="wxXXXXXXXXXXXXXXXX"
export WX_APPSECRET="your-app-secret"
# 小程序码磁盘缓存有效期（小时，默认 720 = 30 天，到期自动重新生成）
export WXACODE_TTL_HOURS="720"
# 后台上传小程序代码（可选，不配则只走「登记版本」那条路）。详见下方「小程序版本发布」
export MP_NODE_BIN="node"            # node 可执行；容器里给绝对路径，如 /usr/local/bin/node
export MP_CI_MODULE_DIR="/opt/mp-ci" # 装过 miniprogram-ci 的目录；留空则试 MP_SOURCE_DIR
export MP_SOURCE_DIR="/opt/weichuan-miniprogram" # 小程序源码根目录（含 project.config.json）
export MP_UPLOAD_TIMEOUT_SEC="300"   # 单次上传超时；nginx proxy_read_timeout 须大于它
# 腾讯视频档：界面未设置时的回落值（系统设置 → 功能开关 → 「腾讯视频档」）
export MP_TENCENT_VIDEO="false"

# 初始超管（首启动若 admin 表为空则自动 seed，上线务必修改为强密码）
export ADMIN_USER="admin"
export ADMIN_PASS="admin123456"

# 3. 安装依赖并启动
go mod tidy
go run ./cmd/server
```

> 启动时会执行 `SeedAdmin`：若 `admin` 表为空，则用 `ADMIN_USER`/`ADMIN_PASS`（bcrypt 哈希后）创建初始超管并打印登录提示；已存在则跳过。避免手工写库。

## 首次部署：不配环境变量也能装（安装向导）
`DB_DSN` 完全没提供时，`cmd/server` 不会 Fatal，而是进入「未配置模式」：
不加载业务，只提供 `/setup` 安装页（`/healthz/ready` 如实返回 503，`/healthz` 索引页仍 200 但 `configured=false`）。打开页面填数据库与管理员，
保存即完成「建库 → 建表 → seed 系统设置与初始超管 → 写配置文件 → 自重启」。
配置写进 `CONFIG_FILE`（默认 `./data/runtime.env`，权限 0600、原子写），
优先级仍是 **环境变量 > 配置文件 > 内置缺省**，所以老部署给过环境变量的行为完全不变。

```bash
# 什么都不设，直接起（当前工作目录需可写，配置会落到 ./data/runtime.env）
go run ./cmd/server
# 浏览器打开 http://127.0.0.1:8080 → 自动跳 /setup
```

安装入口只在 `DB_DSN` 缺失时存在（进程内一次性锁 + 正常模式不挂写接口），
且拒绝初始化 `admins` 表已有数据的库。想重装：删配置文件（或清空库）后重启。
细节见 `docs/deploy-runbook.md` §2.1。

## 容器运行
```bash
docker build -t weichuan-backend ./backend
docker run -e DB_DSN="$DB_DSN" -e JWT_KEY="$JWT_KEY" -e UPLOAD_DIR="./uploads" -e UPLOAD_BASE_URL="$UPLOAD_BASE_URL" -e STORAGE_DRIVER="$STORAGE_DRIVER" -e MINIO_ENDPOINT="$MINIO_ENDPOINT" -e MINIO_ACCESS_KEY="$MINIO_ACCESS_KEY" -e MINIO_SECRET_KEY="$MINIO_SECRET_KEY" -e MINIO_BUCKET="$MINIO_BUCKET" -e MINIO_USE_SSL="$MINIO_USE_SSL" -e OSS_ENDPOINT="$OSS_ENDPOINT" -e OSS_ACCESS_KEY_ID="$OSS_ACCESS_KEY_ID" -e OSS_ACCESS_KEY_SECRET="$OSS_ACCESS_KEY_SECRET" -e OSS_BUCKET="$OSS_BUCKET" -e WX_APPID="$WX_APPID" -e WX_APPSECRET="$WX_APPSECRET" -e WXACODE_TTL_HOURS="$WXACODE_TTL_HOURS" -e ADMIN_USER="$ADMIN_USER" -e ADMIN_PASS="$ADMIN_PASS" -p 8080:8080 weichuan-backend
```

## 本地联调（SQLite devserver，免 MySQL）
`cmd/devserver` 是仅用于联调/演示的开发态入口：用 SQLite（纯 Go，免 cgo）替代 MySQL，复用与 `cmd/server` **完全一致**的路由装配，启动即 seed 演示数据（超管 / 公司信息 / 7 分类 / 1 精选项目 / 1 服务 / 首页布局 / 1 演示员工）。适合小程序端本地联调，免去起 MySQL / Docker。
```bash
go run ./cmd/devserver          # 监听 :8080（PORT 可覆盖）
```
演示账号：超管 `admin/admin123456`；员工手机 `13832603521` / 密码 `weichuan123`（高立新，业务经理，staffId=1）。生产部署走 `cmd/server`（MySQL + docker-compose；小体量也可用 SQLite 单机，见「本地运行」的 DSN 表与 `docs/deploy-runbook.md` 路径 D）。

两个联调注意点：
- **真机 / 局域网上传**：上传基址默认 `http://127.0.0.1:<port>/uploads`（同机测试可直接用）；手机取不到图时设 `DEV_UPLOAD_BASE_URL=http://<本机IP>:8080/uploads` 再启动（启动日志 `upload base url:` 显示生效值）。
- **数据不持久**：SQLite 库在系统临时目录，重启即丢并重新 seed；要持久化运行见仓库根 `docs/deploy-runbook.md`「无 Docker 部署」路径 B/C/D（D 是生产可用的 SQLite 单机方案）。

## 生产文件存储（MinIO / OSS）
- 通过 `STORAGE_DRIVER` 切换，业务代码（handler / 前端 / 小程序）完全不变，仅 `main.go` 的 `newUploader` 工厂按环境变量选择实现，统一满足 `storage.Uploader` 接口：
  - `local`（默认）：写本地 `UPLOAD_DIR` 并由 `/uploads` 静态路由托管，适用于开发/单机。
  - `minio`：写入 MinIO（或任意 S3 兼容对象存储，含阿里云 OSS 的 S3 兼容端点），需 `MINIO_*` 环境变量；`UPLOAD_BASE_URL` 设为 bucket 的公开访问基址或 CDN 域名。
  - `oss`：写入阿里云 OSS，需 `OSS_*` 环境变量；`UPLOAD_BASE_URL` 设为 bucket 公网域名或绑定的自定义域名/CDN。
- 三种实现都做了**统一的大小 / 类型白名单校验**（`checkFile`），对象命名布局一致（用户上传放 `uploads/<rand>`，小程序码放 `wxacode/<staffId>.jpg`），便于从本地平滑迁移到对象存储。
- **小程序码**也走同一抽象（`SaveBytes`），写入当前驱动并带内存 TTL 缓存（按 `WXACODE_TTL_HOURS`），到期自动重新生成；不再依赖本地文件系统 ModTime。
- 前置要求：bucket 需设为**公开读**（或经 CDN 回源公开），这样返回的 URL 才能被小程序/H5 直接访问。
- 依赖：`github.com/minio/minio-go/v7`、`github.com/aliyun/aliyun-oss-go-sdk`。因 SDK 要求，**项目 Go 版本需 ≥ 1.26**（见 `go.mod` 与 `Dockerfile` 的 `golang:1.26-alpine`）。

## 存储增强（直传预签名 / 私有桶签名 / CDN 刷新）
三项增强均基于 `storage.Uploader` 之上的可选能力接口，业务代码不变：
- `Presigner`：`PresignPut(key, contentType, ttl)` → 预签名 PUT URL（MinIO/OSS 实现；本地磁盘无此能力，返回 code=1 由前端回退普通上传）。
- `Signer`：`SignGetURL(key, ttl)` → 限时 GET URL（私有桶用）。
- `CDNRefresher`：`Refresh(urls)` → CDN 缓存刷新（`AliyunCDNRefresher` 纯标准库实现，无额外依赖）。

### 1. 上传直传预签名 URL
- 流程：前端先 `GET /api/admin/upload/presign?ext=png&contentType=image/png`（员工用 `/api/employee/...`）拿到 `putUrl` + `url`；再用 `PUT putUrl` 把文件直接传到对象存储；`url` 即最终可访问地址，写回表单。
- 好处：大文件不流经应用服务器，省带宽、减存储压力。
- 本地驱动不支持预签名，`presignHandler` 返回 `code=1`，`UploadField.vue` 自动回退到普通 `/api/admin/upload` 上传。
- 注意（OSS）：预签名 URL 已绑定 `Content-Type`，前端 `PUT` 时必须带**相同** `Content-Type`（组件已处理）；MinIO 对 PUT Content-Type 不强制。
- 小程序端：原生 `wx.uploadFile` 只能 multipart POST，不便做 raw PUT；小程序仍走普通 `/api/employee/upload` 服务端上传（能力已具备），直传预签名主要在 PC 后台生效。

### 2. CDN 刷新（五家厂商）
- 启用：`CDN_ENABLED=1` + `CDN_PROVIDER`（aliyun / tencent / qiniu / cloudflare / cloudfront，留空按 aliyun）+ 该厂商要求的凭据。
- 各家要填什么（界面按厂商联动只显示需要的字段）：

  | 厂商 | AccessKeyId 位 | AccessKeySecret 位 | 资源标识 | 默认端点 | 单次上限 |
  | --- | --- | --- | --- | --- | --- |
  | aliyun 阿里云 | AccessKeyId | AccessKeySecret | — | cdn.aliyuncs.com | 1000 |
  | tencent 腾讯云 | SecretId | SecretKey | — | cdn.tencentcloudapi.com | 1000 |
  | qiniu 七牛云 | AccessKey | SecretKey | — | fusion.qiniuapi.com | 20（官方默认配额） |
  | cloudflare | （不需要） | API Token | Zone ID | api.cloudflare.com | 30 |
  | cloudfront | AccessKeyId | SecretAccessKey | Distribution ID | cloudfront.amazonaws.com | 1000（官方默认 3000） |

- 触发点（三处覆盖写路径，均 best-effort，失败只记日志、不影响接口结果）：
  - `POST /api/admin/upload`（含员工端 / 申请头像）：对象名随机，实际不会覆盖，刷新属「有备无患」；
  - `POST /api/admin/assets/adopt`：对象名固定 `library/<素材ID>.<ext>`，**重复采用 = 覆盖写**（素材库升级换图），必须让边缘失效；
  - `GET /api/wechat/wxacode`：对象名固定 `wxacode/<staffID>.jpg`，TTL 到期重生成 = 覆盖写；私有桶下刷新的是公开形态 URL（短期签名 URL 每次都变，不该交给 CDN 刷）。
  - 注：CloudFront 提交的是 **URL 的 path**（`/uploads/a.png`），不含协议 / 主机 / 查询串。
- 也可手动触发：`POST /api/admin/cdn/refresh` body `{"urls":["https://cdn.xxx/a.jpg", ...]}`（单次最多 1000 条，按厂商上限自动分批）。
- 加厂商：实现 `storage.CDNRefresher`（单方法 `Refresh(urls []string) error`），在 `storage/cdn.go` 的 `NewCDNRefresher` 加一个 case，并在 `setting.go` registry 登记（Desc 写清该厂商要填什么）。全部为标准库实现，无额外依赖。

### 3. 私有桶 + 限时签名访问
- 启用：`STORAGE_PUBLIC=false`（默认 `true`）。对象存储 Bucket 设为私有读。
- 存储：DB 仍按现有字段保存「公开形态 URL」（`BaseURL/key`），对象本身私有。
- 访问：前端在需要展示私有文件时，调用 `POST /api/admin/storage/sign`（员工用 `/api/employee/...`）提交已存 URL 列表，后端按 `BaseURL` 前缀反推对象 key，返回 **30 分钟有效**的限时签名 URL（`SignGetURL`），前端用签名 URL 取图。
- 本地私有：若 `local` 驱动 + `STORAGE_PUBLIC=false`，文件不再经 `/uploads` 静态公开，改用 `GET /api/storage/file?key=...`（鉴权后流式返回，防目录穿越）。
- 小程序码已接入私有桶签名通道：`STORAGE_PUBLIC=false` 且存储支持签名时，`/api/wechat/wxacode` 返回 **1 小时有效**的限时签名 URL（每次请求重新签发），二维码内容本身仍按 `WXACODE_TTL_HOURS` 缓存、到期才重新调微信生成；公开桶/本地驱动仍返回公开 URL。

## 腾讯视频档（系统设置 → 功能开关）

公司简介的「视频来源」支持五档，`tencent`（腾讯视频插件）是唯一需要**先开开关**的一档：

| 档位 | 播放形态 | 存储/流量 | 播放埋点 | 门槛 |
|---|---|---|---|---|
| `none` | 封面占位 | — | — | 无 |
| `channels` | 跨主体点击拉起视频号页；封面带视频号名称与推荐角标 | 微信承担 | **无**（官方 channel-video 只有 binderror） | 非个人主体 + 基础库 ≥ 2.31.1 |
| `upload` | 原位播放 | 客户对象存储 | 有 | 需配置对象存储（本地磁盘下该档禁用） |
| `url` | 原位播放 | 客户自备 CDN | 有 | 正式版需把 mp4 域名加入小程序「业务域名」 |
| `tencent` | 原位播放、无视频号角标 | 腾讯视频承担 | **有**（插件透传 bindplay） | 系统设置开启 + 插件授权 + `app.json` 声明 |

为什么默认关：腾讯视频插件（provider `wxa75efa648b60994b`）必须在 `app.json` **静态声明**，而声明了却没在 MP 后台「设置 → 第三方服务 → 插件管理」授权的插件，会让**整个小程序编译失败、连预览都传不上去**——不是某个客户视频播不出来，是所有客户的整个小程序发不了版。所以开关默认关，打开即等于运维自证「插件已授权 + 已声明」。

**内容来源（播不出来时的排查点）**：`<txv-video>` 取一个 `vid`（v.qq.com 播放页 `page/` 与 `.html` 之间的那段字符）。**只要是腾讯视频上公开可播的 vid 都能放**——您自己创作号上传的、别人公开上传的非会员视频都行（微信服务市场文案写「仅可播放您上传的视频」是营销口径，微信开放社区开发者实测他人非会员 vid 也能播）。插件**不支持任意 URL**：传进去的是腾讯视频 `vid` 而不是裸 mp4/http 链接，拿一个外站地址填进来必然播不出来。另：创作号上传时**分类别选「文化」「历史」可能导致无法播放全片**（有用户反馈，未逐一核验），宣传类视频建议选「企业 / 商业」等类别。

开关在哪：

- **系统设置 → 功能开关 → 「腾讯视频档」**（key `video_tencent_enabled`），改完**即时生效、不需要重启**。
- 界面留空（从未设置过）时回退部署环境变量 `MP_TENCENT_VIDEO`——这一路留给自动化部署 / 安装向导预设默认形态；界面一旦显式设过就以界面为准（与 `metrics_enabled` 同一套路）。
- 关闭时：`GET /api/admin/video-capability` 返回 `tencentVideo:false` 与可执行原因，后台选项置灰；直接 `PUT videoSource=tencent` 也被服务端拒（前端置灰拦不住接口调用）。开启后 `videoVid` 必填（首尾空白会被剥掉）。

小程序侧声明插件（改 `app.json` + `company-video/index.json`，可 `--off` 还原）：

```bash
node miniprogram/scripts/enable-tencent-video.mjs on
#   插件版本号默认 2.6.2（1.X 已停维护），需覆盖时：PLUGIN_VERSION=2.6.3 node ... on
```

之后还需在 MP 后台「设置 → 第三方服务 → 插件管理」添加腾讯视频插件并等授权通过（这步只能人工做）。

## 开源素材库（内置，随二进制分发）

后台所有需要图片的位置（分类/服务图标、项目封面、公司简介封面、公司信息 LOGO、员工头像、**小程序首页背景**）都支持从**内置开源素材库**直接选用，无需再去各处找图、抠图、调尺寸。

- **资产构成**（`internal/assets/library/`，共 **959 项**、约 17MB，随 `//go:embed` 编译进二进制，Docker 单文件部署零额外挂载）：
  - 图标 **748 个 = 187 个图形 × 4 档配色**（冷色原版 / 暖阳 / 鎏金 / **素雅浅底**，`ICON_PALETTES` 定义）：Lucide（ISC 许可证）SVG 经 `rsvg-convert` 光栅化为线形图形，合成渐变圆角方块。素雅浅底（白→浅蓝底 + 蓝色细线）即客户原型图风格，适合轻盈清新的商务调性；光影流动一档**刻意不做**——高饱和底配白/蓝线形图标对比度差；
  - 封面 52 张（960×504 JPEG + 480×252 缩略图，含**玻璃塔楼/楼盘社区等建筑主题**，可作视频播放封面）、LOGO 40 个、头像 24 个：PIL 程序化生成的渐变 + 几何图形，按配色归入五档调性，商用无授权风险。
  - **背景 51 张（750×1334 竖幅 JPEG + 300×534 缩略图）**：深色高端商务 + 行业调性 + **浅蓝素雅系**（晴空/云雾/冰蓝，即客户原型图的页面浅蓝背景）+ 其他浅色系，供小程序首页背景直接选用。
  - **海报 59 张（640×920 竖幅 JPEG + 320×460 缩略图）**：24 张二十四节气 + 23 张节日（春节/元宵/端午/中秋/除夕/腊八/小年/龙抬头/七夕/重阳/情人节/女神节/劳动节/母亲节/儿童节/父亲节/教师节/感恩节/平安夜…）+ 12 张商务场景（商务合作/开业大吉/新品上市/客户答谢/开工大吉…），供员工在小程序分享海报页换背景。构图为「上部标题区（标题烧进图里）+ 下部信息区（压深）」，压深是硬要求——小程序用浅色字在下半部实时画员工信息，不压深则浅色海报上的白字读不出。**新增节日海报必须同步登记 `POSTER_FESTIVALS` 白名单**，否则图上不会盖那枚「節慶」朱红印章——有没有章正是用户在素材库里区分「节气图 / 节日图 / 商务图」的最快线索。小程序端按当天日期自动选图，见 `miniprogram/README.md`「节日 / 节气日自动选背景」。
  - 素材按「构图 motif + 配色」解耦：同一 motif 换配色即可派生新款式，`gen_library.py` 里加一行就能扩充；`main()` 内置素材 **id 唯一性自检**（id 即落盘文件名，重复会静默覆盖，直接 fail-fast）。
  - `manifest.json` 描述分类与素材项；`CREDITS.md` 记录图标来源与许可证。
- **采用（adopt）模式**：`POST /api/admin/assets/adopt` 把内嵌素材经 `Uploader.SaveBytes` **复制进当前上传存储**（local → `uploads/library/<id>.<ext>`），返回与普通上传完全同构的 URL——下游（小程序 `<image>` / CDN）零感知，不引入新的静态资源域。
- **预览图公开化**：`GET /api/assets/file/*path` 无鉴权 + `Cache-Control: immutable` 长缓存（通用素材，无租户数据；`<img>` 标签无法带 Authorization 头，故公开）。
- **两个正交的筛选维度**：
  - `category` 分类 = **用在哪**：图标 / 封面 / LOGO / 头像 / 背景 / 海报；
  - `style` 视觉调性 = **什么气质**：`clean` 简约清晰 / `nature` 温暖自然 / `luxury` 轻奢尊贵 / `soft` 柔美清新 / `vivid` 光影流动。
  - 调性命名刻意用**用户能感知的形容词**，不暴露 mesh / grain / noise 等实现术语——客户不知道什么是网格渐变，但知道自己要"沉稳"还是要"温暖"。调性中文名并入搜索标签，搜「轻奢」可直接命中该档素材。
  - 归类表见 `gen_library.py` 的 `_STYLE_GROUPS`；未登记的素材缺省 `clean`（新增素材漏标也能安全降级）。图标/LOGO/头像视觉语言统一，全部落 `clean`，故这些分类下风格行会自动隐藏。
- **素材更新**：增删素材只需重新跑 `python3 internal/assets/gen_library.py`（需联网拉取 Lucide 图标 + 本机 `rsvg-convert`/Pillow），产物目录提交入库即可，接口与前端零改动。

### 素材库接口
| 方法 | 路径 | 鉴权 | 说明 |
|---|---|---|---|
| GET  | /api/assets/file/*path | 否 | 素材预览图（PNG/JPEG），immutable 长缓存 |
| GET  | /api/assets/posters | 否 | **海报素材清单**（公开，小程序「选海报背景」用）：只回 poster 分类，字段压到 `{id,name,style,file,thumb}`；不复用 `/api/admin/assets/library` 是因为后者要管理员登录态、且会返回全量 900+ 素材 |
| GET  | /api/admin/assets/library | admin | 素材清单（**分页**）：`page`（缺省 1）/ `size`（缺省 15 = 选择器网格 5 列 × 3 排，一屏看全、无需滚动；上限 60）/ `type`（分类，空=全部）/ `style`（视觉调性，空=全部）/ `keyword`（名称或标签匹配）→ `{items,total,page,size,pages,categories,styles,styleCounts}`。`styleCounts` 是**当前 分类+关键字 下各调性的素材数**（刻意排除 `style` 自身影响，否则选中一档后其余全变 0），前端据此隐藏 0 结果的档位 |
| POST | /api/admin/assets/adopt | admin | 采用素材：body `{"id":"bg-deepspace"}` → 复制进上传存储，返回 `{url, item}` |

> 分页与筛选都在服务端：素材已达百级且会继续扩充，一次返回全量不合适；
> 且「客户端过滤 + 服务端分页」语义矛盾（翻页只看得见当前页内的匹配项），
> 故 `type` / `keyword` 由 `assets.List` 在服务端过滤后再分页。

### 海报个性化设置接口
| 方法 | 路径 | 鉴权 | 说明 |
|---|---|---|---|
| GET | /api/employee/poster-prefs | employee | 读自己的海报设置：`{staffId, bgId, backgrounds, layout, exists}`。`exists=false` = 服务端没有可信记录（新员工 / 没调过），客户端此时应以本机缓存为准——否则换新账号第一次进页面会把用户手挑的背景冲掉。`staffId` 供端上判断「这张名片是不是我的」（海报页会被转发给任何人） |
| PUT | /api/employee/poster-prefs | employee | 覆盖写：body `{bgId, backgrounds:[{id,name,file}], layout}`，返回归一后的同一结构（客户端据此纠正本地状态） |
| GET | /api/poster-prefs?staffId= | **否**（按 IP 限流 60/分，桶键 `posterpref:`） | **版式公开快照**（只读）。无此端点时访客看到的是默认版式——员工排的版式传不出去，见下方说明 |

#### 为什么要有公开快照

海报页是**编辑器**而不是图片查看页：转发链接只带 `staffId`，访客打开时是当场用
「员工名片数据 + 版式」重新生成一张。版式原本只存在于员工本机（按 `staffId` 分键的
storage）与他的账号里（`/api/employee/poster-prefs` 要鉴权），**访客两者都拿不到**，
于是拿到的是客户端 `DEFAULT_LAYOUT` ——员工调的排版、挑的自定义背景、写的那句 slogan
全都传不出去。海报作为「可传播的营销物料」，物料本身并没有被传递，被传递的只是
一个生成器的链接。

公开快照与员工端点读同一行数据，只暴露**渲染这一张海报所需的最小集**：
`backgrounds` 只回当前选中的那一张自定义背景（`publicPosterBackgrounds`），
未选中的（最多 9 张）是员工私有素材，不该挂在无鉴权接口上；内置素材 `poster-*`
客户端自带清单，一张都不回。存在性与停用判定与 `/api/card` 同口径
（不存在 code=1、已停用 code=2）。

> 存 `poster_prefs` 表（`staff_id` 唯一），两个 JSON 列：`backgrounds`（自定义背景清单，上限 9 条）
> 与 `layout`（版式）。不拆列是因为版式可选项会随需求增删，拆列每加一项都要写迁移；
> 代价是数据库层看不出结构，所以**校验在 handler 层做**（`internal/handler/poster.go`）：
> 枚举白名单、数值夹紧、超限截断、`file` 只认站内相对路径或 http(s)
> （`javascript:` / `data:` / `//evil.com` 一律丢弃）。
> `layout` 里的 `customText`（一句话自定义文案，上限 30 rune）走 `normalizePosterText`：
> 控制字符折成空格、连续空白折成一个、按 rune 截断。必须压平换行——canvas 的 `fillText`
> 不认 `\n`，会画出缺字或方块；小程序端 `normalizeCustomText` 是同一套规则（两边都做，
> 缺一边就会出现「接口正常但海报画错」这种最难查的问题）。
> 位移（`offsetX` / `offsetY` / `qrOffsetX` / `qrOffsetY`）只夹在 ±480（`posterOffsetLimit`，
> 画布长边级别）——这是**挡脏数据的粗框，不是可拖范围**。真正的可拖范围由小程序端按
> 「内容实际包围盒」现算（`miniprogram/pages/poster/poster.ts` 的 `measureBox` / `shiftRange`）：
> 两字姓名能横跨大半个画布，长公司名就只给一点点余量。以前这里写死横向 ±16 / 纵向 ±60，
> 结果是「两字姓名拖两下就顶死、长公司名照样被裁掉尾字」——服务端算不出文字多宽，
> 就不该假装知道边界在哪。信息区与二维码各有各的一对偏移，互不牵连。
> `qrSize`（小程序码大小）只认 0 / 76 / 104 / 140 四档（`posterQrSizes`），
> 其余值走 `nearestPosterQrSize` **吸附**到最近档、等距取较小档（90 → 76，95 → 104），
> 0 与负数归 0。0 表示「自动」（按内容高度自适应），**不是「没传」**，必须原样保留。
> 选吸附而不是回 400：界面本来就是四选一，脏值只可能来自旧数据或手工改库，
> 而用户这次改的可能是配色——为了一个跟他无关的字段让整次保存失败很荒谬。
> 注意服务端**不校验档位与内容是否冲突**（内容太高时 140 会被客户端夹到放得下的高度），
> 因为「内容多高」只有画得出来才知道，服务端没有字形宽度。
> `showLandline` / `showEmail` **默认 false**、`showQr` **默认 true**。两个方向的默认值
> 刻意相反：前两者是「新出现的信息」，默认塞给用户等于改掉他已经调好的版式；
> 后者是海报唯一的落地入口，默认藏起来等于让用户发出去一张没人扫得到的图。
> 模板白名单从 4 套扩到 8 套（新增 `split` / `left` / `stack` / `poster`）：
> 老数据里存的仍是前四个之一，扩白名单不影响存量。
> 不塞进 `employee` 表，是因为 employee 的写入走「提交 → 待审 → 通过」审核流，
> 而海报版式是纯个人偏好——调个字号还要管理员审批很荒谬。
> 版式请求用**指针补丁**结构解析：`bool` 的零值是 `false`，用完整结构接 `{}` 会得到
> 「五个元素全关」的海报（用户发出去是一张只剩背景的白板），指针才能区分「没传」和「明确关掉」。


## 已实现接口

> **多租户**：所有业务表带 `company_id`；1 号租户为内置演示租户。
> 公开内容接口接受可选 `?companyId=`（缺省 1=演示租户），企业停用即拒。
> 后台两级身份：平台超管（`admin.company_id=0`，可见全部租户）/ 企业管理员（`company_id>0`，仅本企业），
> 由 JWT `companyId` claim 承载（见 `internal/auth/jwt.go`、`internal/middleware/auth.go`）。
> 小程序分发（第二期，已落地）：`miniprogram/utils/tenant.ts` 支持两种形态——
> 配置 `pinnedCompanyId` 即为**企业独立小程序版本**（锁定单租户，无任何切换行为）；
> 缺省为平台模式（扫码 scene / 分享链接带 `companyId` 首次绑定并粘滞本机），详见 `miniprogram/README.md`。

### 读（首页 + 跳转页）
| 方法 | 路径 | 说明 |
|---|---|---|
| GET  | /api/card?staffId= | 聚合名片（员工+企业+简介），禁用返回 code=2；企业信息按员工归属租户取 |
| GET  | /api/achievements?companyId= | 分类 + 精选项目（缺省演示租户；企业停用返回 code=1） |
| GET  | /api/projects?categoryId=&companyId= | 项目列表（enabled=1；列表项含回填的 categoryName） |
| GET  | /api/projects/:id?companyId= | 项目详情（解析 album JSON；含 categoryName 与 updatedAt） |
| GET  | /api/services?companyId= | 业务服务列表 |
| GET  | /api/services/:id?companyId= | 业务服务详情（含 updatedAt） |
| GET  | /api/home-layout?companyId= | 首页板块顺序（根级 `order`）+ 首页背景（根级 `bgImage` / `bgMask`；`bgImage` 为空串即不设背景） |
| GET  | /api/company?companyId= | 企业公开信息（名称/简称/LOGO/状态）：小程序申请页展示申请目标、企业独立版自检配置用 |
| GET  | /api/team?companyId=&page=&size=&keyword= | 名片墙：该企业**启用中**员工（staffId/name/title/avatar）+ 企业抬头 + `entry{mode,staffId}`（裸启动的**生效**入口，指定员工不可用时已回落 `wall`）。小程序裸启动（扫体验版二维码 / 直接打开）的落地页数据源。**只输出白名单字段**——`model.Employee` 上的 Phone/Email/Landline/OpenID 一律不出网。**不挂 CDN 缓存**（改完入口设置要能立刻扫码验证）。**分页与搜索**：`size` 缺省 300（与改造前一致，老端不带参数行为不变）、上限 300，新端固定传 60；`keyword` 同时匹配姓名与职位。回包 `{code, company, entry, data, total, page, size, hasMore, keyword}`，`total` 是**匹配总数**、`hasMore` 由服务端算（端上别用 `total - data.length` 推）。姓名是 PII 密文列、SQL 做不了 `LIKE '张%'`，带关键字时走「全员解密 → Go 侧过滤 → 切片」（`repository.MaxTeamSearchScan` 上限 5000 人），**不要优化回 SQL LIKE** |
| POST | /api/stat/track | 埋点上报（staffId + eventType + targetId；公开接口但按 IP 限流 60 次/分并校验员工存在、事件类型白名单、targetId 存在且同租户） |

### 写 + 审核流（员工 / 管理员，账号密码，JWT）
| 方法 | 路径 | 鉴权 | 说明 |
|---|---|---|---|
| POST | /api/employee/login | 否 | 员工登录（手机号+密码）→ 返回 token + refreshToken（小程序 Bearer）；企业停用返回 code=2 |
| POST | /api/employee/login/wechat | 否 | **微信一键登录**：body `{code, companyId}` → 后端 jscode2session 换 openid → 找已绑定该 openid 的员工 → 发令牌。业务码：0 成功 / 1 参数或微信侧错误 / 2 账号或企业停用 / **3 该微信未绑定或绑定了多个账号**（前端据此引导去绑定或改用密码登录）。走登录限流器 |
| POST | /api/employee/wechat/bind | employee | **绑定微信**（body `{code}`）→ 把当前员工与 openid 绑定，之后可一键登录。凭据取员工**自己企业**的 appid（不信客户端传值）；同一企业内一个微信只能绑一个账号；重复绑定是覆盖写（换微信时直接重绑，无需解绑接口） |
| POST | /api/admin/login | 否 | 管理员登录（用户名+密码）→ 写 httpOnly Cookie，响应体仅 expiresIn + 身份（companyId/companyName/isPlatform） |
| POST | /api/refresh | 否 | 换发新 access（PC 读 refresh Cookie；小程序 body 传 refreshToken） |
| POST | /api/logout | 否 | 吊销当前会话 sid（单设备精确吊销）+ 清除 httpOnly 鉴权 Cookie（PC 后台登出） |
| POST | /api/apply/open | 否 | 员工自助提交开通申请（pending；可含 landline / avatar / companyId，缺省落演示租户）。文字经内容安全检测，命中违规返回 code=1 且不落库 |
| POST | /api/apply/open/avatar | 否 | 申请开通时上传头像（公开端点，仅图片，供访客在账号创建前设置头像） |
| GET  | /api/employee/me | employee | 本人名片信息；根级另带 `pendingModify`（有无待审修改）、`wechatBound`（是否已绑定微信，供「我的」页显示状态）、`companyName` / `companyShortName` |
| PUT  | /api/employee/me | employee | 提交名片修改申请（待审，对外仍显旧信息；可含 landline）。姓名 / 职位经内容安全检测，命中违规返回 code=1 且不落库 |
| GET  | /api/employee/me/stat | employee | 本人统计（?from=&to= 日期范围 YYYY-MM-DD，可缺省=全部）：八类事件计数 + 按天趋势 + 案例/服务 TOP3 + **来源分布**（只统计 visit，各行之和 = visit） |
| PUT  | /api/employee/password | employee | 修改本人密码 |
| GET  | /api/admin/apply/open | admin | 开通申请列表（按租户隔离；平台超管可用 ?companyId= 收窄） |
| POST | /api/admin/apply/open/:id/approve | admin | 通过 → 自动建员工账号并生成初始密码（员工落到申请所属租户） |
| POST | /api/admin/apply/open/:id/reject | admin | 驳回 |
| GET  | /api/admin/apply/modify | admin | 名片修改申请列表（按租户隔离） |
| POST | /api/admin/apply/modify/:id/approve | admin | 通过 → 应用快照到员工 |
| POST | /api/admin/apply/modify/:id/reject | admin | 驳回 |
| GET  | /api/admin/employees | admin | 员工列表（按租户隔离；平台超管可用 ?companyId= 收窄） |
| POST | /api/admin/employees | admin | 新增员工（生成账号+初始密码；企业管理员落本企业，平台超管可用 ?companyId= 指定） |
| PUT  | /api/admin/employees/:id | admin | 编辑员工资料（即时生效，不经审核流；姓名/手机必填，不含状态与密码；按租户隔离） |
| GET  | /api/admin/stats | 名片统计（汇总 + 按员工 + 按天趋势 + TOP 案例/服务榜，零数据员工补 0；趋势与按员工均含八项指标；?from=&to= 日期范围；按租户隔离） |
| GET  | /api/admin/stats/export | 统计 CSV 导出（同 stats 口径与租户隔离；UTF-8 BOM + 公式注入防护；含内容浏览三类 + 裂变两类共五列，逐类一列不合并） |
| GET  | /api/admin/stats/detail | 埋点明细列表（汇总的下钻：逐条事件，按 id 倒序分页 {code:0,data,total,page,size}；?staffId=&eventType=&targetId=&from=&to= 筛选，白名单外值 code:1；同租户隔离） |
| POST | /api/admin/employees/:id/disable | admin | 停用账号（按租户隔离；目标不存在/越权返回 code=1「未找到」，不再假装成功；记审计 employee.status） |
| POST | /api/admin/employees/:id/enable | admin | 启用账号（同上，含审计） |
| POST | /api/admin/employees/:id/reset-password | admin | 重置密码（返回新初始密码；按租户隔离；目标不存在返回 code=1；记审计 employee.reset_password） |
| PUT  | /api/admin/password | admin | 修改自己密码 |

### 企业内容管理（管理员登录，按租户隔离）
| 方法 | 路径 | 说明 |
|---|---|---|
| GET  | /api/admin/company-info?companyId= | 公司基础信息（按租户单行；企业管理员固定本企业，平台超管缺省演示租户） |
| PUT  | /api/admin/company-info | 更新公司信息 |
| GET  | /api/admin/company-profile?companyId= | 公司简介 + 视频号 |
| PUT  | /api/admin/company-profile | 更新简介 + 视频号 |
| GET  | /api/admin/categories?companyId= | 业绩分类列表 |
| POST | /api/admin/categories | 新增分类（租户外键以服务端解析为准） |
| PUT  | /api/admin/categories/:id | 编辑分类（带租户条件，越权返回未找到） |
| DELETE | /api/admin/categories/:id | 删除分类（强校验：关联项目存在则拦截 / 锁定分类拦截，均返回 code=1） |
| GET  | /api/admin/projects?categoryId=&companyId= | 项目案例列表（含下架） |
| POST | /api/admin/projects | 新增项目案例 |
| PUT  | /api/admin/projects/:id | 编辑项目案例 |
| DELETE | /api/admin/projects/:id | 删除项目案例 |
| GET  | /api/admin/services?companyId= | 业务服务列表 |
| POST | /api/admin/services | 新增业务服务 |
| PUT  | /api/admin/services/:id | 编辑业务服务 |
| DELETE | /api/admin/services/:id | 删除业务服务 |
| GET  | /api/admin/home-layout?companyId= | 首页布局：板块顺序 + **背景图** `bgImage` + 遮罩浓度 `bgMask`（0~100） |
| PUT  | /api/admin/home-layout | 更新板块顺序 + 背景（`bgImage`/`bgMask` 用指针语义：未提供=保持原值，传空串=清除背景；`bgMask` 服务端钳制 0~100） |
| GET  | /api/admin/mp-entry?companyId= | 小程序**裸启动默认入口**：`mode`（配置原值）+ `effectiveMode`（生效值）+ 指定员工姓名/职位/状态/`staffMissing` |
| PUT  | /api/admin/mp-entry | 设置默认入口：`{mode:'wall'\|'staff', staffId}`。`mode=staff` 时 `staffId` 必填且须为**本企业启用中**员工（跨租户 / 不存在 / 已停用一律 code=1）；`mode=wall` 时 `staffId` 归零（必须显式传 0）。非法 mode 直接拒绝，不静默兜底 |
| POST | /api/admin/upload | 文件上传（图片/mp4），返回可访问 URL（填回封面/相册/头像/LOGO） |
| POST | /api/employee/upload | 员工上传头像（员工鉴权），返回可访问 URL |
| GET  | /api/admin/upload/presign | 直传预签名：返回 `putUrl`（直接 PUT 到对象存储）+ 最终 `url`（仅 minio/oss；local 返回 code=1 由前端回退普通上传） |
| GET  | /api/employee/upload/presign | 同上（员工鉴权） |
| POST | /api/admin/storage/sign | 私有桶：提交已存 URL 列表，返回限时签名 URL（仅 minio/oss 私有桶） |
| POST | /api/employee/storage/sign | 同上（员工鉴权） |
| POST | /api/admin/cdn/refresh | 触发 CDN 缓存刷新（body: `{"urls":[...]}`），需 `CDN_ENABLED=1` |

### 企业管理（仅平台超级管理员，company_id=0）
| 方法 | 路径 | 说明 |
|---|---|---|
| GET  | /api/admin/me | 当前登录身份（用户名 / companyId / companyName / companyShortName / isPlatform；前端刷新后恢复身份用） |
| GET  | /api/admin/companies | 企业列表（含简称 / 员工数 / 管理员数 / **小程序凭据是否配好** `wxConfigured`） |
| POST | /api/admin/companies | 开通企业（name 必填；shortName 为企业简称——侧边栏品牌名 + 公司信息简称初始值，留空默认「微传名片」；adminUsername+adminPassword 可选，一并建企业管理员） |
| PUT  | /api/admin/companies/:id | 编辑企业（name / shortName；shortName 留空回落缺省「微传名片」；只改 company 表，不回写公司信息里的简称） |
| POST | /api/admin/companies/:id/status | 停用 / 启用企业（body `{"status":"enabled"\|"disabled"}`；停用后该企业管理员与员工均不能登录） |
| GET  | /api/admin/companies/:id/admins | 企业管理员列表 |
| POST | /api/admin/companies/:id/admins | 新增企业管理员（username + password） |
| GET  | /api/admin/companies/:id/wechat | 该企业小程序凭据（只回 `appId` 明文 + `configured`；**appSecret 永不回传**） |
| PUT  | /api/admin/companies/:id/wechat | 保存租户小程序凭据（`appId` 须 wx 开头；**`appSecret` 空串 = 清空并回落全局凭据**；`releaseEnabled` 省略则保持原值；不受 independent_lock 限制，见下） |

> 企业简称（`company.short_name`）与公司信息里的简称（`company_info.short_name`，0009）相互独立：
> 前者是 PC 后台侧边栏「简称 · 管理后台」品牌名（未设置时缺省「微传名片」，企业管理员登录/me 时随身份下发），
> 后者仅用于小程序展示（留空则小程序顶栏展示企业全称）。
>
> 租户小程序凭据（`company_wx_app`，migrations/0024）是**唯一不受 independent_lock 只读限制**的写接口：
> 那个开关是「独立版部署就别再动租户结构了」，而凭据是阻塞性配置——真锁住了反而让独立版用户
> 永远配不上小程序。`wxConfigured` 要求 AppID 与 AppSecret 成对非空，半配置算未配置，
> 与取值侧口径一致（见下文「小程序码」的三级回落链）。

### 小程序码（公开，分享海报用）
| 方法 | 路径 | 说明 |
|---|---|---|
| GET  | /api/wechat/wxacode?staffId= | 按员工生成小程序码（wxacodeunlimit，scene=staffId=），磁盘缓存后返回 URL |
| GET  | /api/wechat/wxacode?staffId=&page= | 指定落地页（默认 pages/index/index） |
| POST | /api/wechat/openid | 微信身份换取：传 `wx.login` 的 `code`，经 jscode2session 返回 `openid`（申请/登录时采集，供后台核实身份） |

> openid 接口复用 `WX_APPID/WX_APPSECRET`（与小程序码同一份凭据）：`wx.login` 拿到临时 `code`，后端换取用户在本小程序内的唯一 `openid`。昵称由小程序端经用户授权（`getUserProfile`）获取后随申请/登录提交。未配置凭据时 openid 接口返回明确错误，前端降级为空，不影响账号密码登录与申请。员工登录成功时若带上 openid/昵称会记录到员工表，便于后台「员工管理」核实。

> 小程序码缓存 TTL：生成的图片通过当前存储驱动的 `SaveBytes` 写入（local → `uploads/wxacode/<staffId>.jpg`；minio/oss → 对应对象）。返回策略：公开桶/本地直接返回公开 URL；**私有桶**（`STORAGE_PUBLIC=false` 且存储支持签名）返回**每次重新签发的 1 小时限时签名 URL**。二维码内容按 `WXACODE_TTL_HOURS`（默认 720h=30 天）做内存 TTL 缓存，过期才重新调微信生成，既避免反复消耗微信配额，也支持变更样式/落地页后自然刷新。`access_token` 服务端内存缓存并在 40001/42001 时自动刷新重试。
> 鉴权方式：请求头 `Authorization: Bearer <token>`（admin 接口）。小程序码接口为公开（分享用），未配置 `WX_APPID/WX_APPSECRET` 时返回明确错误。
>
> **未配凭据的后果要知道**：小程序端海报页拿不到码时会画一个「小程序码 / 未生成」占位框，
> 并在页面顶部给一条黄条 + 重试按钮（`code:1` 的 `msg` 会原样透给用户）。
> 也就是说「海报上没有二维码」几乎总是配置问题，而不是前端渲染问题——
> 先 `curl '/api/wechat/wxacode?staffId=1'` 看一眼返回的 msg 即可定位。
> 凭据可在后台「系统设置 → 微信 → 小程序凭据」填写（请求期解析，改完即时生效，不必重启），
> 留空则回退环境变量 `WX_APPID` / `WX_APPSECRET`。
>
> **多小程序（企业独立版）**：产品同时支持「平台一个小程序服务所有企业」与「每企业一个自己的小程序」两种形态，
> 后者共用同一套后端，凭据必须按租户隔离——不隔离的话企业员工生成的小程序码指向平台小程序，
> 扫码进的是平台侧页面。取值是三级回落链：
>
> | 顺序 | 来源 | 配置入口 |
> |---|---|---|
> | 1 | 租户凭据 `company_wx_app`（`company_id` 唯一键） | 企业管理 →「凭据」按钮（平台超管代填） |
> | 2 | 全局 `sys_setting` 的 `wx_appid` / `wx_appsecret` | 系统设置 → 微信 → 小程序凭据 |
> | 3 | 环境变量 `WX_APPID` / `WX_APPSECRET` | 部署配置 |
>
> 半配置（只填了 AppID 而 Secret 为空）按「没配」处理，与列表页 `wxConfigured` 口径一致——
> 否则界面说已配置、实际仍回落全局小程序。租户归属按 `staffId → employee.company_id` 反查
> （**不信客户端传的 companyId**），双层缓存：凭据 30s、归属 5min（负结果也缓存，防枚举 staffId 扫号）；
> 后台保存即清凭据缓存，改完立刻生效。
> `appSecret` 永不回传（连 key 都不出现），也不进审计明细。写接口语义：`appSecret` 空串 = 清空并回落全局。

### 小程序版本发布（平台代租户上传 / 提审 / 发布，migrations/0025 + 0026 + 0027）

| 方法 | 路径 | 说明 |
|---|---|---|
| GET  | /api/admin/companies/:id/releases | 版本列表（分页 + `status` 过滤；只读，不受代发开关限制） |
| POST | /api/admin/companies/:id/releases | 登记「CI 已上传」的版本 → `draft`（不调微信接口；企业 + 版本号唯一） |
| GET  | /api/admin/companies/:id/releases/upload-readiness | 上传前探活：缺 node / 没装 miniprogram-ci / 源码目录不在 / 没配密钥，**一次列全** |
| POST | /api/admin/companies/:id/releases/upload | 后台上传代码到微信（成为体验版）并自动建 `draft` 记录 |
| POST | /api/admin/releases/:id/submit-audit | 提交审核 → `auditing`（微信 `/wxa/submit_audit`，MVP 不传 item_list） |
| GET  | /api/admin/releases/:id/audit-status | 拉一次微信审核状态并按需同步本地记录（回包带 `wxStatus` / `changed`） |
| POST | /api/admin/releases/:id/release | 发布 → `released`（仅 `audit_passed` 态允许） |
| POST | /api/admin/releases/:id/undo-audit | 撤回审核 → `withdrawn`（body 须 `confirm:true`，见下方代价） |

**上传代码有两条路**（`source` 字段区分）：

1. **后台自己传**（`source=backend`）：点「上传代码」→ 后端 `exec node` 跑内嵌脚本
   （`internal/mpupload/upload.js`，`go:embed` 进二进制）调 `miniprogram-ci` 的 `upload`。
   **不需要任何人装微信开发者工具**——服务器上有 Node + 源码 + 密钥就行。
   成功后**自动建一条 draft 记录，版本号就是这次真实上传的那个号**，从根上杜绝
   「填的版本号」和「实际传的版本号」不是同一个；失败则**不建记录**，只写失败审计。
2. **CI 传完后登记**（`source=ci` / `manual`）：CI 用同一把密钥经 `miniprogram-ci` 传，
   后台只把版本号记下来。代价是无法在此校验版本号是否真的传过，写错要到提审才暴露。

> **服务器一次性前置条件**（后台上传这条路，缺一项 `upload-readiness` 就列出来）：
> 1. Node：`MP_NODE_BIN`（默认 `node`，容器镜像里请给绝对路径）；
> 2. 模块：`MP_CI_MODULE_DIR`（留空则试 `MP_SOURCE_DIR`）下执行过
>    `npm install miniprogram-ci`——它不随二进制分发，**必须单独装**；
> 3. 源码：`MP_SOURCE_DIR` 指向小程序源码根目录（含 `project.config.json`）；
> 4. 密钥：租户自己的（`company_wx_app.upload_key`，0027）或平台全局
>    （系统设置 `wx_upload_key`）任一份非空。**代码上传密钥 ≠ AppSecret**——
>    前者传代码，后者换 access_token 调提审 / 发布；都在公众平台 → 开发管理 → 开发设置里；
> 5. **两套独立 IP 白名单**：API IP 白名单（管 access_token）与**代码上传 IP 白名单**
>    （管 miniprogram-ci，漏了报 40164）都要放行服务器出口 IP；
> 6. 反代超时：上传**同步执行、分钟级**，nginx `proxy_read_timeout` 必须大于
>    `MP_UPLOAD_TIMEOUT_SEC`（默认 300）。
>
> 密钥只写不读（接口只回 `uploadKeyConfigured`），落 0600 临时文件、用完即删，
> 不进 argv / stdout / 审计明细。

> **状态机是白名单**（`internal/model/mp_release.go`），只放行这些流向：
> `draft → auditing`、`auditing → audit_passed/rejected/withdrawn`、`audit_passed → released`、
> `rejected/withdrawn → auditing`。`draft → released`（跳过审核）与 `released → *`（终态）刻意不存在。
> 微信 status=4（审核延后）本地仍记为 `auditing`——它不是独立业务态，只是「还没审完」。
>
> **三条不可逆代价，写死在注释与 UI 文案里**：
> ① 提审消耗微信审核额度，从 `rejected`/`withdrawn` 重新提审**再扣一次**；
> ② 撤回**不返还**已扣额度，所以撤回接口要求 `confirm=true` 才执行；
> ③ 发布对租户小程序全部用户立即生效，且微信不支持「再发一次同一版本」。
>
> **凭据一致性**：登记时把当时的 appid 存进记录，动作时重新解析（租户凭据 → 全局 → 环境变量）并比对。
> 企业中途换过小程序时两者必然不同，此时直接拒绝——否则代码在新小程序、auditid 属于旧小程序，
> 微信只会回一句莫名其妙的报错。
>
> **租户开关**：`company_wx_app.release_enabled`（0026，缺省 true）。关掉后该企业的一切代发动作
> 返回 `code=403` 并写失败审计；列表仍是只读（企业要能看见历史）。凭据留空保存会清空凭据行，开关随之重置。
>
> **access_token 按 appid 分键缓存**（`internal/wechat/wechat.go`）：多租户共用一个后端，
> 早先的「一个全局 token」会让 A 企业命中 B 企业的 token——微信会拿 B 的身份执行操作，
> 在版本发布场景等于把代码发到别家小程序。回归测试见 `TestAccessTokenCacheIsKeyedByAppID`。

## 埋点事件与三组统计口径

事件白名单（权威定义见 `internal/handler/stat.go` 文件头）分**三组**，回答三个不同问题：

| 事件 | 语义 | targetId | 组别 |
|---|---|---|---|
| visit | 访问名片（小程序首页） | 0 | 转化 |
| call | 点击拨号 | 0 | 转化 |
| save | 点击保存（存通讯录） | 0 | 转化 |
| project_view | 浏览案例详情 | 案例 ID（强制） | 内容浏览 |
| service_view | 浏览服务详情 | 服务 ID（强制） | 内容浏览 |
| video_play | 播放公司视频（组件级去重） | 0 | 内容浏览 |
| share | 转发（名片 / 案例 / 服务） | 0 或对象 ID（不强制存在） | 裂变 |
| poster_save | 海报**成功写入相册**（失败不计） | 0 | 裂变 |

**三组互不相加**（2026-10-27 口径修正，只改分组与展示，不改埋点、无需迁移历史数据）：
- **转化**：有没有人找上门。
- **内容浏览**：访客对什么内容感兴趣。这三类是唯一带**强制** targetId 的事件，
  也只有它们进 TOP 榜——「对什么内容感兴趣」得有对象才答得上。
- **裂变**：把名片传出去的动作。两个理由不能并进内容浏览：① 不带 targetId，
  回答不了「对什么内容感兴趣」；② **主体可能是员工本人**——海报页唯一入口是
  「我的 → 生成海报」（`pages/employee/me` → `pages/poster/poster`），只有本人才进得去，
  把它算成「访客意向」会把浏览总量抬高。两者都记在「被传播的那张名片的主人」名下。

> 早先的分法是「转化 + 意向（五类）」，share/poster_save 混在「意向」里。
> 数据集层面这两类事件的含义从未变过，改动只在前端分组文案与表格列（
> 管理端由一列「意向」拆成「内容浏览」「裂变」两列）。

- **进水口校验**：`validateStatTarget` 对 `project_view` / `service_view` 强制要求 targetId 存在且**与员工同租户**（`ProjectCase`/`BizService` 都带 `company_id`）；查不到或跨租户一律 `code:1`（同一句回包，不泄露别家对象是否存在）。这是防伪造——放过一个别家 targetId，别家案例名就会出现在自家 TOP 榜上。
- **target_id 列**：`migrations/0021_stat_events_target_id.sql`（MySQL）+ 模型 `StatEvent.TargetID`（SQLite 由 AutoMigrate 补齐）。
- **source 列（流量来源 / 裂变）**：`migrations/0022_stat_events_source.sql`（MySQL）+ 模型 `StatEvent.Source`（SQLite 由 AutoMigrate 补齐）。只统计 `visit` 事件：`statSourceDist` 按归一后的 source 分组出**来源分布**，`statFissionTop` 对 `source='share_card'` 的 visit 按员工聚合出**裂变榜**（回答「谁的名片被转发带来的新访客最多」）。归一白名单见 `stat.go` 的 `statSourceTypes`：空串 / 纯空白→`unknown`、白名单原样、其余任意值→`other`。
  - **source 来自客户端，只作统计维度，绝不参与权限 / 租户 / 计费判断**——白名单归一就是为了挡住「谁都能塞一个自定义来源把分布图玩坏」（静默污染）。
  - 裂变榜的 `LEFT JOIN employees` **不带 `deleted_at IS NULL`**：employees 表没有该列（员工是硬删除），带上会让 MySQL 直接报「无此列」。
  - 两处 SQL 形状已进 `runAPIFlow`（`handler_test.go` 的「内容浏览埋点与 TOP 榜」子测试），由 `TestAPIFlowMySQL` 用真 MySQL 跑同一套断言守着。
- **趋势与按员工都给八项**：`statTrend` 与 `statRowsByStaff` 一次聚合八类事件（同一白名单），前端图例可点选——勾到哪个指标都要有线，否则「勾了却没线」就是静默缺数据。两条 SQL 的**别名必须 snake_case**（`project_view`），写驼峰会静默扫不进来、计数恒 0 不报错（`handler_test.go` 的「内容浏览埋点与 TOP 榜」parity 断言守着这条）。
- **口径一致性**：TOP 榜用 `LEFT JOIN` + `deleted_at IS NULL` + `COALESCE(name,'已删除')`——对象被删除后行仍在、计数不减，避免「汇总 3 条、榜单加起来 2 条」这类最难排查的对账事故。明细的「对象」列与榜单同源（`statTargetName`）。
- **限流分桶**：埋点限流器的桶键带 `track:` 前缀，与 `login:` / `refresh:` 分开计数——三者共用 `rate_limit_hits` 表，键不隔离时办公网 NAT 下的访客埋点会把管理员登录挤到 429（回归测试 `TestTrackDoesNotStarveLogin`）。

## 安全加固

本骨架已落地以下安全基线（详见对应源码）：

- **上传内容嗅探**：`storage.checkFile` 在校验扩展名白名单后，进一步按文件头（魔法字节）校验真实类型（PNG / JPG / GIF / WEBP / MP4），防止把可执行文件改名图片上传。`backend/internal/storage/storage_test.go` 覆盖各类型的合法/伪造场景。
- **登录限流**：`middleware.NewLoginRateLimiter` 按客户端 IP 做固定窗口限流（默认每 IP 每分钟 10 次登录尝试），防爆破；超限返回 `{code:429}`（与现有鉴权约定一致）。桶表按 `sweepEvery` 惰性清理过期项（否则每个「来过一次就不再出现」的 IP 都会永久占用一项，长期运行下 map 单调增长）。
  **多副本**：进程内的桶是每副本一份，实际阈值会翻倍，故 `RegisterAuth` 通过 `WithCounter(dbRateCounter)` 把计数落到 `rate_limit_hits` 表——同一 (key, 窗口) 在所有副本上是同一行，阈值与副本数无关。存储故障时中间件自动降级到进程内计数（不会把登录接口打挂，也不会让限流消失）。`backend/internal/middleware/ratelimit_test.go` 覆盖计数、隔离、阈值热更新、非法阈值回落、过期桶回收、窗口内桶不被误删、共享计数生效与降级；`internal/handler/ratelimit_shared_test.go` 用两个 engine 共用一个库验证「总阈值仍是 10 次」。
- **启动安全门槛（拒绝启动）**：`config.InsecureGates()` 命中「`JWT_KEY` 仍是缺省值 `change-me` 或长度 < 16」「`ADMIN_PASS` 仍是内置缺省口令」时，`cmd/server/main.go` 打印 `[SECURITY][FATAL]` 并 `log.Fatal` **拒绝启动**——这两个默认值连同演示账号写进了公开文档，属已知凭据，带它们上线等于把后台开放给所有人。自愈方式是改一个环境变量重启（不会像 `db_dsn` / `cookie_*` 那样把「改回来的入口」一起弄丢）；本地联调加 `ALLOW_INSECURE=1` 放行。测试见 `internal/config/config_test.go`。
- **启动自检告警（只提示不拦）**：`config.InsecureWarnings()` 对「超管口令长度不足 8 位」打印 `[SECURITY]` 告警。与上面的门槛分工：口令是给人记的，强弱交给运维判断，不卡启动。
- **健康检查（存活 / 就绪分离）**：`GET /healthz/live` 只确认进程活着（不查任何依赖，DB 挂了仍 200）；`GET /healthz/ready` 先 `SELECT 1` 探活数据库——正常 `200 {status:"ok",db:"up"}`，DB 不可达 `503 {status:"degraded",db:"down",error:"..."}`；`GET /healthz`（含尾斜杠形态）是**索引页**——列出子端点、**恒 200**、不做任何健康判断。**监控与编排用 ready**：live 在 DB 断开时仍 200，用它告警会漏掉「进程在但不可用」；根路径索引页同理，更不能用于告警。`docker-compose.yml` 已据此为 backend 配置 `healthcheck`（探 `/healthz/ready`，安装模式下 503 会二次确认 `/api/setup/status` 为 `configured=false`，按健康放行，避免装到一半被反复重启）。详见《运维与部署》。
- **软删除**：`ProjectCase` / `BizService` / `AchievementCategory` 已接入 GORM `DeletedAt`，删除改为置空 `deleted_at`，列表接口默认不返回已删除记录；MySQL 侧迁移见 `migrations/0003_soft_delete.sql`。
- **JWT 双令牌 + 即时吊销**：`auth.IssueTokens` 同时签发 access（15 分钟）与 refresh（30 天）；`POST /api/refresh` 用 refresh 换发新 access。`Employee` / `Admin` 含 `token_version`（安全戳，迁移 `migrations/0004_token_version.sql`），改密 / 管理员重置密码 / 禁用启用时自增，`middleware.AuthMiddleware` 校验令牌内 `vid` 与当前版本一致，**旧令牌立即失效**（账号级吊销，无需黑名单表）。覆盖测试见 `TestTokenRefresh` 与「超管改密与令牌吊销」。
- **PC 管理后台 httpOnly Cookie 鉴权**：`/api/admin/login` 将 access/refresh 写入 `HttpOnly; SameSite=Lax` 的 Cookie（`access_token` / `refresh_token`，见 `handler/cookie.go`），响应体不再回传令牌；`AuthMiddleware` 支持「Authorization 头 或 `access_token` Cookie」两种来源（小程序走 Bearer，PC 走 Cookie）；`POST /api/logout` 清除 Cookie。`COOKIE_SECURE`（生产 HTTPS 置 true）/ `COOKIE_DOMAIN`（跨子域才设）可配。覆盖测试见 `TestCookieAuth`。
- **单设备精确吊销（会话黑名单）**：`Claims.SID` 为会话 ID——一次登录生成一个，access/refresh 共享，**刷新不换 sid**（`auth.IssueTokensWithSID`）。`POST /api/logout` 从 Bearer / Cookie 解析当前会话，把 sid 写入黑名单表 `revoked_sessions`（迁移 `migrations/0005_revoked_sessions.sql`，读写见 `repository/session.go`）；`AuthMiddleware` 与 `/api/refresh` 均校验 sid 不在黑名单内，**登出后该设备的所有令牌立即失效，且不能靠刷新绕过**。与 `token_version`（账号级）互补：登出只失效当前设备，同一账号的其他登录不受影响。覆盖测试见 `TestSingleDeviceRevocation`。
- **黑名单过期清理**：`repository.PurgeExpiredSessions(db)` 按 `expires_at` 删除已过期的吊销记录（这些行的令牌自身也已过期，留着无意义）；`cmd/server` 与 `cmd/devserver` 启动时机会性执行一次并打印日志，生产可另配定时 SQL（`DELETE FROM revoked_sessions WHERE expires_at < NOW();`）。
- **可观测性三件套**（`middleware/observability.go`，零第三方依赖）：
  - `RequestID`：注入 / 透传 `X-Request-Id` 响应头，报障时可引用，串联日志排障；
  - `AccessLog`：每请求一行（方法 / 路径 / 状态 / 耗时 / IP），5xx 以 `[ACCESS-5XX]` 前缀标出，便于日志告警规则直接匹配；panic 抛穿本中间件，由链上 `gin.Recovery`（`handler.New` 已用 `gin.New()+Recovery` 替代 `gin.Default()`）记录含栈日志；
  - `Metrics`：内存计数器 + Prometheus 文本格式 `/metrics`（**默认关闭**，`METRICS_ENABLED=1` 显式开启，避免公网暴露流量规模；生产建议仅内网 / 反代白名单可达）。
- **数据库备份**：仓库根 `scripts/backup.sh` 做 MySQL 逻辑备份（`docker exec mysqldump --single-transaction` → gzip → 按 `KEEP_DAYS` 轮转），可入 crontab；逻辑备份不含 binlog，需 PITR 时另行开启。

### 已知待加固项（需流程 / 基建配合，未在本仓库落地）
- 小程序码 / access_token 等内存缓存多实例不共享，水平扩展需引入 Redis。
- **最小告警已落地**：仓库根 `scripts/alert_check.sh` 消费 `[ACCESS-5XX]` 访问日志与 `/healthz`（healthz 本地探活 / 5xx / 备份新鲜度 / **外部拨测**四条规则，零依赖，配 crontab 即可运行，配置见《运维与部署》「最小告警（零依赖）」）；待运维侧按需扩展完整监控栈（Prometheus + Alertmanager）、多地拨测（本脚本的外部拨测仍是单点视角）与 PITR（binlog 时间点恢复）；生产需 HTTPS 与反向代理。

## 自动化接口测试
接口层（`/api/*`）已配套 `httptest` 集成测试，覆盖读接口、登录鉴权、审核流、员工管理、分类/项目强校验、业务服务、企业内容、文件上传与存储增强、小程序码等全部主路径，并对「本地驱动不支持预签名→code:1」「CDN 未配置→code:1」「分类下有项目/锁定→删除拦截 code=1」等回退分支做了断言。

- 测试文件：`internal/handler/handler_test.go`。入口：
  - `TestAPIFlow`（默认）：SQLite 跑完整流程，按「公开接口 → 超管登录 → 审核流 → 员工管理 → 强校验 → 服务 → 内容 → 上传存储 → 改密」分段执行。
  - `TestStorageEnhanceSuccess`：用 fake `Presigner`/`Signer`/`CDNRefresher` 覆盖存储增强**成功分支**（预签名返回 `putUrl/url/key`、私有签名返回 `signed` 映射、CDN 刷新被调用）。
  - `TestCDNRefreshOnOverwritePaths`：守门「CDN 刷新确实挂在会覆盖同名对象的路径上」——素材采用（`library/<ID>.<ext>`）与小程序码重生成（`wxacode/<staffID>.jpg`，用注入的假生成器替代微信外网调用）各触发一次刷新，并断言刷新失败时接口仍返回 `code:0`。
  - `TestTokenRefresh` / `TestCookieAuth` / `TestSingleDeviceRevocation`：分别覆盖 refresh 换发与账号级吊销、PC 端 httpOnly Cookie 鉴权与登出、单设备精确吊销（会话黑名单）。
  - `TestAssetLibrary`（`internal/handler/assets_test.go`）：素材库匿名 401、**分页（翻页不重复 / size 与上限 / 非法页码回落与末页夹取 / 分类与关键字过滤 / 无命中空结果）**、**视觉调性（五档清单 / 调性过滤生效 / styleCounts 排除 style 自身 / 分类+调性组合 / 图标只落 clean 一档 / 调性中文名可搜）**、采用落盘（含首页背景 JPEG）、预览图公开 + immutable、封面与背景缩略图、未知素材/空参/路径穿越 404。
  - `TestAPIFlowMySQL`（生产契约 parity）：设置 `TEST_MYSQL_DSN` 后，连真实 MySQL、应用 `migrations/*.sql`，再跑与 SQLite **完全相同的断言**，用以校验 AutoMigrate 与生产建表 SQL 行为一致。
  - `internal/handler/hardening_test.go`（P0 加固守门）：专治「静默失败」——摘掉修复必须红。覆盖：登出写不进黑名单必须报 `code:1`（`TestLogoutReportsRevokeFailure`，另有 `TestLogoutSucceedsNormally` 作对照）、密码生成取不到随机数必须报错而不能 panic（`TestGenPasswordEntropyFailure` / `TestGenPasswordRejectsAtAPI`）、小程序码按 IP 限流挡扫号（`TestWXACodeRateLimitedPerIP`）、小程序码缓存到上限要淘汰（`TestWXACodeCacheEvictsAtCap`）、启停员工留审计且目标不存在要报「未找到」（`TestEmployeeStatusAuditAndNotFound`）。
  - `internal/storage/storage_test.go:TestRandNameEntropyFailure`：守门「熵源读取失败必须报错」——从前忽略错误会让每次上传都叫同一个名字，后上传的静默覆盖先上传的。
  - `internal/handler/mp_release_test.go`（8 例，`TestMPRelease*`）：版本发布 handler 全链路，假微信按调用顺序记账。覆盖：**完整闭环**（登记→提审→轮询到审过→发布，且终态后不再多调一次 release）、**微信报错时本地记录回滚**（不留下「以为提审成功了」的半截状态）、**状态机非法流转被拒**（auditing 态点发布返回 code=1）、**重复版本号 / 未配凭据 / 记录不存在**、**租户凭据优先于全局凭据**、**租户关掉代发后一切写动作 403 而列表仍可读**、**保存凭据时省略 `releaseEnabled` 不清开关**、**重新打开后代发恢复**；每次代发动作都断言落了一条审计（成功记结果态、失败带 error）。
  - `internal/handler/mp_release_upload_test.go`（8 例，`TestMPReleaseUpload*`）：后台上传这条路。假 node + 假源码目录 + 假模块目录后**重建引擎**（`RegisterMPRelease` 按值收 cfg，改完不重建路由闭包里还是旧值）。覆盖：**上传成功建 `source=backend` 的 draft**、**上传失败不留记录只写失败审计**（别让运营以为只差提审而白扣审核额度）、**租户上传密钥优先于全局那把**（假 node 会把读到的密钥内容落盘，据此断言用的是哪把）、**readiness 一次列全**、**环境不就绪时不启动 node**、**租户关代发 403**、**重复版本号拒绝**、**上传后可直接提审**。
- 运行：
  ```bash
  cd backend
  go test ./internal/handler/ -v
  # 生产契约校验（需可用 MySQL）：
  TEST_MYSQL_DSN='root:root@tcp(127.0.0.1:3306)/weichuan_test?charset=utf8mb4&parseTime=true' \
    go test ./internal/handler/ -run TestAPIFlowMySQL -v
  ```
- 鉴权约定：中间件返回的是 **HTTP 200 + `{code:401|403}`**（非 HTTP 状态），测试统一断言 `body["code"]`。
- 测试 DB：默认无需外部 MySQL。通过 `handler.New(db)` 注入一个**临时文件的 SQLite 库**（纯 Go 驱动 `github.com/glebarez/sqlite`，免 cgo），用 GORM `AutoMigrate` 按模型建表，并 `SeedAdmin` 初始超管后把完整路由跑起来。SQLite 把模型里的 `JSON` 列按 `TEXT` 存储，与 MySQL `JSON` 列在字符串读写语义上等价，足以验证 handler 业务正确性。MySQL parity 用例通过 `runtime.Caller` 定位源码目录、回溯读取 `migrations/`，不依赖测试进程 CWD。
- 依赖影响：新增依赖 `github.com/glebarez/sqlite`（测试用，纯 Go）与 `github.com/stretchr/testify`（断言，已转 direct）**仅作用于 `_test.go`**，生产二进制（`go build ./...`）不引入它们，也不连任何外部服务。

## 其它测试（按包）

除 handler 集成测试外，各内部包均有针对性单测（全部零外部依赖，`go test ./...` 一并运行）：

- `internal/storage/minio_test.go`：用 `httptest` 起**假 S3 服务**跑 MinIO 驱动真实 SDK 调用链（SaveBytes / Presign / SignGetURL / Delete）。要点：minio-go 需显式 `credentials.NewStaticV4`（否则触发 IAM 凭据链查询）与固定 `Region`（否则会打 `GET /bucket?location` 区域探测）。
- `internal/storage/oss_test.go`：用 `httptest` 起假 OSS 服务（`oss.ForcePathStyle(true)` 才能打 IP:PORT 端点），覆盖上传 / 预签名 / 签名 / 删除与构造期 fail-fast（空 endpoint/bucket 直接报错，不拖到上传才失败）。
- `internal/storage/storage_test.go`：`checkFile` 扩展名白名单 + 文件头魔法字节嗅探的各类型合法 / 伪造场景。
- `internal/middleware/observability_test.go`：RequestID 透传 / 生成、AccessLog 前缀（5xx 走 `[ACCESS-5XX]`）、Metrics 计数与 Prometheus 文本格式。
- `internal/migrate/apply_bookkeeping_test.go`（迁移记账）：用临时 SQLite + 自制
  `WEICHUAN_MIGRATIONS_DIR` 覆盖「只应用未记录的版本」「失败不记账（否则会留下
  一个没人知道的表结构缺口）」「存量库重放报重复要忽略」「applied_at 落库」，
  不必依赖真实 MySQL 就能守住升级自动迁移的核心语义。
- `internal/repository/session_test.go`（会话黑名单）：清理只删过期行、未过期的吊销
  记录必须留着；后台定期清理（默认 6h，`REVOKED_PURGE_MINUTES` 可配）会在到期后清掉
  且 `stop()` 后不再继续（防 goroutine 泄漏）；interval≤0 回退默认间隔而不是 0 间隔死循环；
  `stop()` 可重复调用（main 里 `defer` + 测试里 `t.Cleanup` 会各调一次）。
  实现侧对应一条硬约束：**`stop()` 返回时清理协程必须已退出**（内部 `close(done)` 后再等
  `stopped`），且协程每轮先非阻塞看一眼停止信号——否则 `ticker.C` 与 `done` 同时就绪时
  Go 的 `select` 会随机选，于是「已停止」却仍多执行一次 `DELETE`。
- `internal/auth/jwt_test.go`（令牌签发与校验）：双令牌共享 sid（登出才能一次吊销整条链）、
  access 寿命短于 refresh、refresh 复用 sid 不换会话；解析必须拒绝「篡改签名 / 他人密钥重签 /
  `alg=none` 无签名令牌 / 已过期 / 垃圾串」；有效期由系统设置驱动且填 0 或负数回落缺省
  （access 有效期 0 会让所有人一登录就掉线，连后台都进不去改回来）；熵源失效必须报错。
- `internal/auth/password_test.go`：bcrypt 往返、同明文不同哈希（带盐）、大小写与尾随空格敏感、
  非法哈希返回 false 而不 panic。
- `internal/wechat/wechat_test.go`：用 httptest 假微信接口（`apiBase` 抽成变量注入）覆盖
  jscode2session 成功/errcode/未配凭据、**access_token 缓存与过期重取**、小程序码成功 /
  40001 刷新后重试一次 / 重试仍失败即放弃 / 非 token 类错误不重试、`isImage` 判定。
  （写这组测试时发现 `refreshToken` 持锁调用 `GetAccessToken` 会**死锁**，已拆出无锁的
  `accessToken` 修掉——见代码注释。）
- `internal/wechat/release_test.go`（11 例）：版本发布四个微信接口的假微信联调——
  submit_audit 取 auditid / 不传 item_list / errcode 非 0 转 `APIError` / **errcode=0 却缺
  auditid 也要报错**（微信偶发半截响应，不当成功）、get_auditstatus 五个枚举值含 4 延后、
  release / undo_code_audit 成功与失败分支。另有一条关键回归：
  **`TestAccessTokenCacheIsKeyedByAppID`**——假微信按 appid 回不同 token，交替调两个租户，
  串号必红（原先的「一个全局 token」会把 A 企业的操作打到 B 企业的小程序上）。
- `internal/model/mp_release_test.go`（4 组）：状态机白名单——允许的 7 条流向全部放行，
  **明令禁止的流向全部拒绝**（`draft→released` 跳过审核、`audit_passed→auditing` 审过退回、
  `released→*` 终态、`withdrawn→released` 撤回后上线、自环、空态与未登记状态）；
  released 终态无出路；六态都有中文名且通过校验；**微信 status=4（延后）映射成
  auditing 且不算终局**、未知值按「仍在审核中」处理。
- `internal/mpupload/mpupload_test.go`（7 例）：后台上传的 Go↔Node 边界。用**假 node**
  （shell 脚本，读 stdin、按暗号分支）跑通 `exec` 全链：密钥**不以明文进 argv**、
  `projectPath` 原样透传、退出码非 0 且没有结果行必须报错（miniprogram-ci 自己会往
  stdout 打编译进度，不能假设 stdout 只有我们的内容）、`Problems()` 一次列全缺失项
  （用不存在的 node 路径触发「node 缺失」分支）、CRLF 密钥归一化、0600 临时密钥文件
  用完即删。另有一条守门：`require('miniprogram-ci')` 的解析起点是**脚本所在目录**，
  所以必须设 `NODE_PATH`——漏了在开发机（恰好装过）看不出来，上线才炸。
- `internal/audit/audit_test.go`：操作人姓名按 admin / employee 分别反查、租户落库、
  未登录记 anonymous、Result 缺省为 success、Detail 以 JSON 落库、写库失败不 panic
  （审计是旁路）、`db=nil` 静默返回；`DetailKeys` 敏感项脱敏（清空时记 changed/cleared）。
- `internal/middleware/auth_test.go`（鉴权中间件每一条拒绝路径）：无令牌 401、非法令牌 401、
  **refresh 令牌当 access 用 401**、token_version 过期 401、sid 在黑名单 401、角色不符 403、
  企业管理员进平台接口 403；令牌来源 Authorization 头优先于 Cookie（Cookie 通道也要能放行，
  PC 后台正是走 httpOnly Cookie）；`TokenVersion` 取不到用户必须报错（否则 vid=0 会放行已不存在的用户）。
- `internal/assets/assets_test.go`：开源素材库（`go:embed` 内嵌，随二进制分发）。
  **清单里的每一条 File/Thumb 都必须真能读到**——素材由 `gen_library.py` 生成，
  清单先落盘、文件后拷贝，中途漏一步线上就是一片裂图；另守清单外的 category/style
  不会出现（否则素材「消失在所有筛选里」）、分页边界（Page<1/Size<1 走缺省、
  Size>60 截断、页码越界夹到最后一页，而不是把负下标喂给切片）、关键字匹配名称与标签、
  **styleCounts 必须先排除 style 自身的影响**（否则选中某调性后其余调性全变 0，前端把它们
  全隐藏，等于选一次就锁死）、ContentType 扩展名推断、ReadFile 的路径不可达与前后斜杠归一。
- `internal/middleware/ratelimit_test.go`：登录限流计数、IP 隔离、阈值热更新、非法阈值回落、过期桶回收、窗口内桶不被误删、共享计数生效 / 存储故障降级 / 降级期间计数接续。
- `internal/repository/ratelimit_test.go`：跨副本计数的窗口内累加、key 隔离、翻页重置、窗口边界对齐、过期行清理、stop 幂等。
- `internal/handler/ratelimit_shared_test.go`：两个 engine 共用一个库，验证总阈值仍是 10 次（不是每副本 10 次）。

CI：见仓库根 `.github/workflows/ci.yml`，共五个 job：
  - `backend`（**默认必跑，零外部依赖**）：`go build` + `go vet` + `go test ./...`。
    未设置 `TEST_MYSQL_DSN` 时，两个 MySQL 用例（`TestAPIFlowMySQL` / `TestMySQLMigrations`）自动 `t.Skip`，
    因此本 job **不需要任何数据库账号**，靠 SQLite 覆盖全部业务流程。
  - `backend-mysql`（**可选**）：仅当仓库配置了变量 `TEST_MYSQL_DSN` 时才运行，
    先跑「连接 + 建表」再跑完整 parity，用于校验生产 MySQL 契约。
    配置路径：仓库 Settings → Secrets and variables → Actions → Variables 新增 `TEST_MYSQL_DSN`
    （例：`root:pwd@tcp(host:3306)/weichuan_test?charset=utf8mb4&parseTime=true`）。
  - `frontend`：`npm ci` + `npm run build` + `npm run test`（Vitest，含 PC 后台视图层测试与经前端工程纳管的小程序纯逻辑测试）。
  - `frontend-typecheck`（**阻塞门禁**）：`vue-tsc --noEmit`。历史 4 个 `@wangeditor/*` 类型错误已通过 tsconfig `paths` 指向真实 d.ts + 精准断言修复，当前 0 错误。
  - `notices`（**阻塞门禁**）：`python3 scripts/gen_third_party_notices.py --check`，核对 `docs/third-party-licenses.md`
    与当前依赖是否一致。它同时用 Go 和 Node：Go 取 `go list -deps ./...` 命中的模块，Node 侧读
    `frontend/node_modules/<pkg>/LICENSE` 的**正文**（不是 package.json 的 license 字段）。
    **改了 `go.mod` / 前端依赖后这个 job 会红**——跑一次生成脚本并提交新清单即可。

> **CI 红、本机绿时：在 Linux 容器里复现。**
> CI 跑的是 `ubuntu-latest`，而开发机多为 macOS，两者在 goroutine 调度 / 时序上差异真实存在
> （踩过一次：`select` 多 case 就绪的随机选择，macOS 恰好绕开、Linux 必现）。
> 复现手法（容器不联网，直接复用宿主机已下载的模块缓存）：
>
> ```bash
> # 仓库根目录执行
> docker run --rm -v "$PWD:/src" -v "$(go env GOMODCACHE):/gomod" \
>   -e GOMODCACHE=/gomod -e GOPROXY=off -e GOFLAGS=-mod=mod \
>   -w /src/backend golang:1.26 sh -c 'go test ./... -count=1'
> ```
>
> 镜像 tag 跟 `backend/go.mod` 的 `go` 行保持一致；怀疑 flaky 时对单个包加 `-count=8` 重复跑。
> Actions 日志需登录才能下载（`gh auth login` 后 `gh run view --log`），未登录时只能用上述方式自证。

## 运维与部署

> 本节把 `scripts/backup.sh` / `scripts/alert_check.sh` / 日志落盘 / `docker-compose.yml` 的 `healthcheck` 串成可操作的运维手册。安全加固相关的能力（上传嗅探、限流、JWT 双令牌、会话黑名单、可观测三件套等）见《安全加固》。

### 1. 健康检查（存活 / 就绪分离）
后端提供三个端点，路径集合与安装模式（`internal/setup`）完全一致：

| 端点 | 语义 | DB 可达 | DB 不可达 |
|---|---|---|---|
| `GET /healthz/live` | 存活：进程能响应即 200，不碰任何依赖 | `200 {status:"ok"}` | `200 {status:"ok"}`（照旧） |
| `GET /healthz/ready` | 就绪：`SELECT 1` 探 DB | `200 {status:"ok",db:"up"}` | `503 {status:"degraded",db:"down",error:"..."}` |
| `GET /healthz[/]` | 索引页：列出上面两个端点，**恒 200**，不做健康判断 | `200 {status:"ok",configured:true,endpoints:{...}}` | `200`（照旧，不查 DB） |

> **为什么要拆**：此前只有一个 `/healthz`，既当存活探针又查 DB。若被 K8s `livenessProbe` 使用，DB 抖动一次就判定进程死亡 → 重启 → 新进程起来 DB 仍不可用 → 再判死，形成重启风暴。K8s 官方文档明确要求 liveness 不检查外部依赖。拆开后 **liveness 只看进程，readiness 才看 DB**：DB 故障时进程不重启，只是被负载均衡摘出流量，等 DB 恢复自动放回。
>
> **监控与编排一律用 `ready`，不要用 `live`**：`live` 在 DB 断开时仍返回 200，用它告警会漏掉「进程在但不可用」这一整类故障——而那正是探活存在的意义。
>
> **根路径 `/healthz` 不是探针，别拿它告警**：它只列端点、不查任何东西，DB 挂了照样 200。`TestHealthzRootIgnoresDB` 把这条约定钉死了——将来有人「顺手」给索引页加一句 `SELECT 1`，测试立刻红。

> **为什么根路径同时注册 `/healthz` 和 `/healthz/`**：Nginx 把「以 `/` 结尾的 location」当目录建模，请求 `/healthz` 会被内建 `auto_redirect` 301 到 `/healthz/`；若后端没注册 `/healthz/`，它又按 `RedirectTrailingSlash` 301 回 `/healthz`，两边互踢成死循环（线上真实报过 `ERR_TOO_MANY_REDIRECTS`）。两个都注册后是终态 200，循环断开。子端点（`/live`、`/ready`）的路径比反代前缀 `/healthz/` 长，不是「父路径」，天然不触发该行为——这也是推荐监控改用子端点的原因。

`docker-compose.yml` 已为 backend 配置 `healthcheck`（每 15s 探 `/healthz/ready`，连续 5 次失败标记不健康；安装模式下 ready 如实返回 503，此时会再看一眼 `/api/setup/status`，`configured=false` 按健康放行——否则首次部署装到一半就被反复重启）。frontend 只 `depends_on: backend`（**不等 healthy**：安装模式下 backend 的 ready 就是 503，而安装页正是由本前端提供的，等 healthy 会永远等不到）；backend 也**不依赖** mysql 的 healthy（连不连库由安装向导在「装」的那一步决定，硬绑会让纯 SQLite 部署永远起不来）。

### 2. 访问日志落盘（告警 R2 的前置条件，极易踩坑）
`middleware.AccessLog` 使用 Go 标准 `log` 包，输出到 **stderr**（每行含方法/路径/状态/耗时/IP；5xx 以 `[ACCESS-5XX]` 前缀标出）。`scripts/alert_check.sh` 的 **R2 规则读取日志文件**（`LOG_FILE`，默认 `/var/log/weichuan/access.log`）扫描新增 `[ACCESS-5XX]` 行。

⚠️ **必须把后端 stderr 重定向到该文件，否则 R2 永远扫不到 5xx**（这是"明明有 5xx 却没告警"的最常见根因）。两种落地方式：

- 方式 A — 进程管理器（systemd / supervisor）重定向（推荐裸机部署）：
  ```ini
  # /etc/systemd/system/weichuan-backend.service 片段
  [Service]
  ExecStart=/opt/weichuan/backend -port 8080
  StandardOutput=append:/var/log/weichuan/access.log
  StandardError=append:/var/log/weichuan/access.log
  ```
- 方式 B — 直接重定向（裸跑）：
  ```bash
  cd backend && go run ./cmd/server >> /var/log/weichuan/access.log 2>&1 &
  ```
- Docker 部署：`docker logs weichuan-backend` 即可查看；若要在宿主机用 `alert_check.sh` 读文件，可挂卷 + `2>` 重定向，或改用 Docker `json-file` logging driver 配合外部采集（filebeat 等）。**告警脚本假设的是裸机/文件场景**，纯 docker 场景建议改用 `docker logs` 轮询或接入标准监控栈。

### 3. 数据库备份（MySQL）
`scripts/backup.sh` 做 MySQL 逻辑备份（`docker exec mysqldump --single-transaction --quick` → gzip → 按 `KEEP_DAYS` 轮转）。
```bash
# 每天 03:00 全量备份（crontab）
0 3 * * * cd /path/to/repo && scripts/backup.sh >> /var/log/weichuan-backup.log 2>&1
```
环境变量：`DB_CONTAINER`(默认 weichuan-mysql) / `DB_USER` / `DB_PASSWORD` / `DB_NAME` / `BACKUP_DIR`(默认 ./backups) / `KEEP_DAYS`(默认 7)。逻辑备份不含 binlog，需 PITR 时另行开启 binlog 并定期备份。

> 开发态 `cmd/devserver` 用 SQLite 临时库（每次启动全新、自动 seed），本就无需备份；生产走 `cmd/server`，用本脚本。若生产用的是 SQLite 单机（路径 D），备份改用 `VACUUM INTO` 取在线一致性快照，不要用 `cp`。

### 4. 最小告警（零依赖）
`scripts/alert_check.sh` 四条规则，配 crontab **每分钟**运行：
- **R1 healthz**：`GET /healthz/ready` 非 2xx/超时即告警（依赖第 1 节的就绪探针，DB 宕机也能捕获）
- **R2 5xx**：扫描日志文件新增 `[ACCESS-5XX]`（依赖第 2 节的日志落盘）
- **R3 备份新鲜度**：`backups/` 最新备份超 `BACKUP_MAX_HOURS`(默认 26h) 即告警（backup.sh 失败时无新文件，本规则兜底）
- **R4 外部拨测**：从对外地址（`EXTERNAL_URL`）访问 `/healthz/ready`——R1 是本机视角，看不出
  域名没解析 / 443 未放行 / 反代 upstream 挂了 / 证书过期这类「本机全绿、用户全挂」的故障。
  同时校验响应耗时（`EXTERNAL_MAX_MS`，默认 2000ms）与证书剩余有效期（`CERT_MIN_DAYS`，默认 14 天）
```bash
# 每分钟检查（crontab）
* * * * * cd /path/to/repo && EXTERNAL_URL=https://你的域名/healthz/ready scripts/alert_check.sh
```
配置（环境变量，均有默认值）：`HEALTH_URL` / `LOG_FILE` / `BACKUP_DIR` / `BACKUP_MAX_HOURS` / `STATE_DIR` / `ALERT_WEBHOOK`(企业微信/钉钉机器人，未配置则打到 stderr) / `ALERT_COOLDOWN`(默认 1800s 防轰炸) / `EXTERNAL_URL`(默认空=R4 跳过) / `EXTERNAL_MAX_MS` / `CERT_MIN_DAYS`。本脚本不替代 Prometheus/Alertmanager，是"无监控基建也能跑"的最小闭环；R4 仍是**单点**视角，区域性故障要靠多地拨测服务。

### 5. 指标（可选）
`/metrics`（Prometheus 文本格式）默认关闭，**`METRICS_ENABLED=1`** 显式开启。生产建议仅内网/反代白名单可达，避免公网暴露流量规模。

### 6. 日志轮转
裸机部署建议配 `logrotate`（访问日志 + 备份日志）：
```
/var/log/weichuan/*.log { daily missingok rotate 14 compress delaycompress }
```

## 待补全（下一步，均为可选增强）
- 富文本中视频号块渲染：已落地 `rich-video` 组件 + `utils/richtext.ts` 鲁棒解析（见 miniprogram README）
- 存储增强已落地：直传预签名 / CDN 刷新 / 私有桶限时签名（见上文「存储增强」）
- 内容模型软删除已落地（见「安全加固」），如需「回收站 / 恢复」可在此基础上扩展
- 可观测性已落地：RequestID / AccessLog / Metrics + 黑名单定期清理（启动时一次 + 后台每 6h）+ 备份脚本（见「安全加固」与仓库根 `scripts/backup.sh`）
- 后续可按需补充：更多 CDN 厂商、上传进度/断点续传、完整监控栈（Prometheus + Alertmanager）、PITR（binlog）等
  （**登录限流的多副本共享已落地**，走 `rate_limit_hits` 表 + 进程内降级兜底，不需要 Redis；
   仍未共享的是小程序码缓存与 access_token，多副本下会各打各的微信接口。）

## 说明
- 需求使用「账号 + 密码」登录，不走微信 openid 免鉴权，因此自研部署完全够用；若日后迁云托管，仅需容器化 + 配置外置（已在 config 中预留），不依赖微信云能力。
- 密码使用 bcrypt 哈希，token 使用 HS256 签名（JWT_KEY 环境变量）。

## 可信反代（`TRUSTED_PROXIES`）——限流的分桶依据

登录 / `refresh` / 小程序码三处限流都按 `c.ClientIP()` 分桶，而这个值读不读
`X-Forwarded-For` 由 gin 的 trusted proxies 决定：

- gin 的**内置缺省是 `0.0.0.0/0`（信任所有代理）**，启动日志会打
  `You trusted all proxies, this is NOT safe`。此时 `ClientIP()` 直接取 XFF 的
  **最左**一项——那一项完全由客户端填写。实测：同一来源连打 15 次错误密码，
  每次换一个伪造头，**15/15 全部放行**（正常应在第 10 次返回 `code:429`）。
  也就是说「防爆破限流」在缺省配置下等于不存在。
- 本项目缺省收窄为 `DefaultTrustedProxies`（回环 + 10/8 + 172.16/12 + 192.168/16
  + `fc00::/7`），覆盖同机 nginx、docker bridge、k8s、内网 LB。
  反代不在这些网段（如公网 LB）时用 `TRUSTED_PROXIES=1.2.3.0/24,...` 显式指定；
  前面确实没有反代则设 `TRUSTED_PROXIES=none`（一律用 TCP 对端地址）。
  配置写错不静默：打印 `[WARN]` 并退回「不信任任何代理」——偏严，但不会被绕过。
- **只收窄后端不够，反代也必须改**：`X-Forwarded-For` 要**覆盖**而不是
  `$proxy_add_x_forwarded_for` 追加。追加会把伪造值留在链左侧，而后端跳过可信代理
  （反代自己）后正好取到它。实测只改后端、nginx 仍追加：伪造头 14/14 仍放行。
  见 `frontend/nginx.conf`。
- **端口暴露也是这条防线的一部分**：docker 端口映射会把所有外部来源 NAT 成桥接
  网关地址，而网关落在可信网段里。所以 backend 的 8090 与 mysql 的 3306 在
  `docker-compose.yml` 里都绑了 `127.0.0.1`；一旦改成对外发布，谁都能伪造来源 IP。
