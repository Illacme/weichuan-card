# 生产上线 Runbook（从零到发布）

> 适用场景：首次部署到生产服务器。联调 / 日常开发见《部署与联调.md》，本文只讲生产路径。  
> **没有 Docker，或想先跑起来测试？** 直接看下方「无 Docker 部署」一节（四条路径，A 路径十分钟可跑通；小体量上线可用 D 路径的 SQLite 单机方案，免装数据库）；就绪后再按第 0 节起的 Docker 主流程上生产。  
> 预计耗时：服务器与域名就绪后 1–2 小时（不含微信审核等待）。

## 无 Docker 部署（先测试 / 无 Docker 环境）

本项目不依赖 Docker 也能完整运行：后端 `cmd/devserver` 用纯 Go SQLite（免 cgo、免 MySQL），前端 vite dev server 自带 `/api` 代理。  
前置：**Go ≥ 1.26**（`go.mod` 要求，`go version` 确认）+ **Node ≥ 20**。

### A. 先跑起来测（同机，约 10 分钟）

```bash
# 终端 1：后端 —— SQLite + 演示数据，不需要 MySQL
cd backend && go run ./cmd/devserver          # 监听 :8080

# 终端 2：PC 管理后台 —— vite 自动把 /api 代理到 8080
cd frontend && npm install && npm run dev     # 打开 http://localhost:5173
```

- 演示账号：超管 `admin/admin123456`；员工手机 `13800000001` / 密码 `demo1234`
- **小程序**：开发者工具导入 `miniprogram/`，`app.ts` 的 `baseUrl` 指向 `http://<电脑局域网IP>:8080`（`urlCheck:false` 已配，http 可直接连）
- **手机看 PC 后台**：同一 WiFi 下开 `http://<电脑IP>:5173`（vite 绑定 `0.0.0.0`）
- **真机测上传**：设 `DEV_UPLOAD_BASE_URL=http://<电脑IP>:8080/uploads` 再启动 devserver——否则返回给端的头像 URL 写死 `127.0.0.1`，手机取不到图（启动日志里的 `upload base url:` 会显示当前生效值）
- 已知限制：SQLite 库在系统临时目录，**重启即丢数据**（每次重新 seed 演示数据）——功能测试无所谓，要持久化走 B/C

### B. 编译二进制 + nginx（贴近生产的运行形态，仍免 MySQL）

> ⚠️ 下面这条 `cmd/devserver` 只为「免 MySQL 快速验证」存在：它每次启动都重建
> 临时 SQLite，**重启即丢全部数据**，不能用于任何需要保留数据的部署。
> 要保留数据请走路径 D（`cmd/server` + `DB_DSN=sqlite://<路径>`，表由启动时
> AutoMigrate 维护）。生产上线一律用 `cmd/server`，不要用 devserver。

```bash
cd backend && go build -o weichuan ./cmd/devserver   # 产出单文件二进制
cd frontend && npm run build                         # 产物在 frontend/dist
```

nginx（即 Docker 版 `frontend/nginx.conf` 的配置，仅把 `backend:8080` 换成 `127.0.0.1:8080`、root 指向 dist）：

```nginx
server {
    listen 80;
    root /path/to/weichuan-card/frontend/dist;
    index index.html;
    client_max_body_size 50m;

    # 必须单列一条：不能让它落到下面的 SPA 兜底。try_files 会把 /healthz
    # 回退成 index.html 并返回 200，于是后端已死、数据库连不上，这个地址照样 200——
    # alert_check 的 R1/R4、preflight 第 10 节、LB 探针全部永远绿，最该报的故障不报。
    location = /healthz {
        proxy_pass http://127.0.0.1:8080;
        proxy_set_header Host $host;
        proxy_connect_timeout 3s;
        proxy_read_timeout 5s;
    }

    location / { try_files $uri $uri/ /index.html; }

    # 后台上传小程序代码：同步执行、分钟级（编译 + 传代码都在这一次请求里跑完）。
    # 不单列的话它落到下面 60s 的 /api/，会在上传途中先返回 504——而代码可能
    # 已经传到微信了：前端看到失败、后台没有 draft 记录，运营再点一次就撞上
    # 「版本号已占用」。正则 location 优先于前缀 location，故这里单独放宽。
    # （只有真的用「上传代码」那条路才需要；仅走 CI 上传可删掉这段。）
    location ~ ^/api/admin/companies/[0-9]+/releases/upload$ {
        proxy_pass http://127.0.0.1:8080;
        proxy_set_header Host $host;
        proxy_set_header X-Forwarded-For $remote_addr;
        proxy_read_timeout 600s;
        proxy_send_timeout 600s;
    }

    location /api/ {
        proxy_pass http://127.0.0.1:8080;
        proxy_set_header Host $host;
        # 覆盖而非追加（$proxy_add_x_forwarded_for）：追加会把客户端自带的伪造值
        # 留在链左侧，后端「跳过可信代理取第一个不可信地址」时正好取到它——
        # 登录限流遂可被绕过（等效于超管口令可无限爆破）。详见 frontend/nginx.conf。
        proxy_set_header X-Forwarded-For $remote_addr;
        proxy_read_timeout 60s;
    }

    location /uploads/ {
        proxy_pass http://127.0.0.1:8080;
        proxy_set_header Host $host;
    }
}
```

> 若这一层前面还有 CDN / 公网 LB：请用 `set_real_ip_from <CDN网段>; real_ip_header
> X-Forwarded-For; real_ip_recursive on;` 让 `$remote_addr` 变成真实客户端 IP，
> 上面两行不用改。**切勿 `set_real_ip_from 0.0.0.0/0`**——那等于让客户端自己决定
> 他是谁，限流又回到可以随便换桶的状态。

后端常驻，二选一：

```bash
nohup /path/to/weichuan-card/backend/weichuan > /var/log/weichuan.log 2>&1 &   # 最简
```

```ini
# /etc/systemd/system/weichuan.service（推荐：崩溃自拉起、开机自启）
[Unit]
Description=weichuan backend (dev mode)
After=network.target

[Service]
WorkingDirectory=/path/to/weichuan-card/backend
Environment=DEV_UPLOAD_BASE_URL=http://192.168.1.10:8080/uploads
ExecStart=/path/to/weichuan-card/backend/weichuan
Restart=always
RestartSec=2

[Install]
WantedBy=multi-user.target
```

```bash
sudo systemctl daemon-reload && sudo systemctl enable --now weichuan
```

- 数据持久化同 A（临时库重启丢）；要真持久化用路径 C。日后如需 devserver 持久库，可加 `DEV_DB_PATH` 环境变量（当前未实现）。
- `DEV_UPLOAD_BASE_URL` 设为局域网 IP 后，内网同事 / 真机的上传与头像均正常。

### C. 完整生产（MySQL 原生 + systemd + HTTPS，不用 Docker）

1. 安装 MySQL 8.0（`apt install mysql-server` 或 yum），建库 `weichuan`，手动应用迁移（命令见第 4 节，与 Docker 无关完全适用）。
2. `go build -o weichuan-server ./cmd/server`，环境变量：`DB_DSN` / `JWT_KEY` / `ADMIN_USER` / `ADMIN_PASS` / `UPLOAD_BASE_URL` / `COOKIE_SECURE=true`。
3. systemd 常驻（unit 同 B，`ExecStart` 换二进制、补 `Environment=` 行）。
4. nginx 反代（同 B）+ HTTPS：前置 Caddy 自动证书（配置见第 5 节）。
5. **MySQL parity 验证照常执行（第 6 节）**——与是否用 Docker 无关，生产用 MySQL 就必须跑。

### D. SQLite 单机生产（免装数据库，但**真持久化**）

`cmd/server` 按 `DB_DSN` 形态自动选驱动：默认 MySQL，写成下面任意一种即走 SQLite——  
**同一个二进制，不用换包**：

```bash
DB_DSN=sqlite:///var/lib/weichuan/weichuan.db
DB_DSN=file:/var/lib/weichuan/weichuan.db      # 等价的 URI 写法
DB_DSN=/var/lib/weichuan/weichuan.db           # 裸路径兜底（仅认 .db/.sqlite/.sqlite3 结尾）
```

```bash
cd backend && go build -o weichuan-server ./cmd/server
sudo mkdir -p /var/lib/weichuan /var/lib/weichuan/uploads
export DB_DSN=sqlite:///var/lib/weichuan/weichuan.db
export JWT_KEY=$(openssl rand -base64 32)     # 上线前必改
export ADMIN_PASS=$(openssl rand -base64 12)  # 上线前必改
export UPLOAD_DIR=/var/lib/weichuan/uploads
export COOKIE_SECURE=true
export TZ=Asia/Shanghai                        # 统计按服务端本地日期落库，见下方说明
./weichuan-server                              # 日志首行会打印 database driver=sqlite
```

> **TZ 与「今日」口径**：埋点的 `stat_date` 由服务端 `time.Now()` 按**本地时区**写日期。
> 不设 `TZ` 时 Go 读不到系统时区数据库就**静默按 UTC**——表现是北京时间每天
> 00:00–08:00 产生的访问落到前一天的桶里，数字悄悄错位且无人察觉（不报错）。
> 故：Docker 部署 compose 已默认 `TZ=Asia/Shanghai`（运行镜像装了 tzdata）；
> 原生部署（路径 C/D）请在 systemd unit 或环境里显式设 `TZ`，并把系统时区配对。
> 管理后台的日期筛选按浏览器本地日期取「今天」，两边同处东八区时一致；
> 跨时区运营以各自本地日期为准。

与路径 A/B 的 devserver 区别：devserver 用**临时库 + 强制 seed 演示数据 + 写死 JWT 密钥**，  
只能测功能；本路径是**真正的生产入口**，数据持久、走 `InsecureGates` 安全门槛。

建表无需手工执行 `migrations/*.sql`（那是 MySQL 方言，`ENGINE=InnoDB` / `AUTO_INCREMENT` /  
反引号 SQLite 都不认）：SQLite 下服务启动时按模型 `AutoMigrate` 自动建表，  
`sys_setting` 由 `setting.Init` 自建。启动时会自动补三项必需 pragma：

| pragma         | 值      | 不设会怎样                                    |
| -------------- | ------ | ---------------------------------------- |
| `journal_mode` | `WAL`  | 一次写挡住所有读，名片页与后台并发时大面积卡顿                  |
| `busy_timeout` | `5000` | 并发写直接报 `database is locked`（SQLite 是单写者） |
| `foreign_keys` | `1`    | SQLite 默认**关闭**外键约束，配了等于没配               |

想覆盖默认值就在 DSN 里显式写同名 pragma，例如  
`sqlite:///var/lib/weichuan/weichuan.db?_pragma=busy_timeout(15000)`——  
只覆盖你写的那一项，其余仍取默认值。

**边界（上生产前请确认都能接受）**：

- **单机单进程**。SQLite 不支持多副本 / 多实例同时写同一个库文件。要横向扩展就回 MySQL（路径 C）。
- **库文件必须在本地磁盘**。NFS / SMB 等网络文件系统上 SQLite 的文件锁不可靠，会静默损坏数据。
- **备份用 `VACUUM INTO`，不要 `cp`**。WAL 模式下裸拷贝可能拿到残缺快照：
  ```bash
  sqlite3 /var/lib/weichuan/weichuan.db "VACUUM INTO '/backup/weichuan-$(date +%F).db'"
  ```
  没有 `sqlite3` 客户端时，可用后端自带驱动跑一次性 Go 脚本，或直接停服后拷贝（简单但会中断）。
- **不需要跑第 6 节的 MySQL parity 验证**（那是 MySQL 生产路的检查项）。
- `db_dsn` 是系统设置里的**只读项**：改错会连不上库、把「改回来的入口」一起弄丢，  
  所以库路径只能在启动时定好（环境变量 `DB_DSN`，或安装向导写入的配置文件
  `data/runtime.env`；优先级 环境变量 > 配置文件 > 内置缺省），改完重启才生效。

### 四条路径对比

|            | A 先跑起来                    | B 二进制 + nginx | C 完整生产                 | D SQLite 单机  |
| ---------- | ------------------------- | ------------- | ---------------------- | ------------ |
| 数据库        | 无（devserver 临时 SQLite）    | 同 A           | MySQL（原生安装）            | SQLite（本地文件） |
| 数据持久       | 重启丢                       | 重启丢           | 持久                     | **持久**       |
| 真机 / 内网上传  | ✅ 设 `DEV_UPLOAD_BASE_URL` | ✅ 同 A         | ✅ `UPLOAD_BASE_URL` 可配 | ✅ 同 C        |
| HTTPS      | 无                         | 无（可加 Caddy）   | ✅                      | ✅ 同 C        |
| 多副本 / 横向扩展 | —                         | —             | ✅                      | ❌ 单机单进程      |
| 适合         | 功能测试 / 演示                 | 内网部署 / 给同事看   | 上线（标准）                 | 上线（小体量免运维）   |

## 0. 前置条件

| 项      | 要求                         | 说明                                                                                    |
| ------ | -------------------------- | ------------------------------------------------------------------------------------- |
| 服务器    | Linux x86_64，2C4G+，磁盘 40G+ | 默认形态 backend + frontend 两容器（选 SQLite）；MySQL 形态（`--profile mysql`）三容器。名片类应用流量小，2C4G 足够 |
| Docker | Docker 24+ 与 compose 插件    | `docker compose version` 验证                                                           |
| 域名     | **已 ICP 备案**，A 记录指向服务器     | 微信小程序合法域名的硬性要求，未备案域名配不了                                                               |
| 小程序备案 | 主体已在工信部完成「**小程序备案**」（微信公众平台 → 小程序管理 → 备案），备案号需公示 | 与域名 ICP 备案**相互独立**的强制要求（2024 年起）：只备了域名、没备小程序，提审会在最后一步被驳回、无法上线对外提供服务。**备案有审核周期（通常数日，材料不齐会退回重走），应尽早启动**，不要等到提审前才办 |
| 端口     | 80 / 443 对外                | 只有 frontend 的 8080（→80）该对外。mysql 的 3306 与 backend 的 8090 在 compose 里**已绑 127.0.0.1**：docker 端口映射会把所有外部来源 NAT 成桥接网关，而网关落在后端的可信反代网段里——端口一旦对外，谁都能自带伪造的 `X-Forwarded-For` 绕过登录限流。不需要直连调试就把这两行删掉 |
| 微信     | 正式 AppID（提审阶段必须）           | 联调期可用测试号；`channel-video` 视频号渲染还需绑定视频号                                                 |
| 隐私保护指引 | 已在微信公众平台配置《小程序用户隐私保护指引》并声明用到的隐私类型 | **平台侧未声明的隐私类型会直接禁用对应接口**（`errno: 112`），代码再对也调不通。需声明：收集你的昵称、头像；收集你选中的照片或视频信息；使用你的相册（仅写入）权限；读取你的剪切板；使用你的通讯录（仅写入）权限；以及 openid 所属的「用户标识信息」。小程序代码侧已开启 `__usePrivacyCheck__` 并接入自绘授权弹窗，声明项须与代码实际调用一致 |
| 授权公钥 | 私有 / 独立部署需注入发行方自己的 Ed25519 公钥（环境变量 `LICENSE_PUB`） | 用 `license keygen` 生成密钥对，公钥填进 `LICENSE_PUB`，**私钥只留在签发环境**。仓库内嵌的那把是占位演示密钥（配对私钥公开），私有部署带着它会**拒绝启动**。集中 / 平台模式不验本地许可证，不受此限 |

## 1. 获取代码与配置

```bash
git clone git@github.com:Illacme/weichuan-card.git
cd weichuan-card
cp .env.example .env
```

生成强随机值（**不要用默认值**，启动时的 `[SECURITY]` 告警必须全部消除）：

```bash
openssl rand -base64 48   # → JWT_KEY
openssl rand -base64 24   # → MYSQL_ROOT_PASSWORD（仅 --profile mysql 形态需要）
openssl rand -base64 12   # → ADMIN_PASS（或自定义强口令）
```

> 走 **§2.1 安装向导**的话：`JWT_KEY` 与初始超管口令由向导现场随机生成并写进配置文件，这两行可以跳过；  
> `MYSQL_ROOT_PASSWORD` 只有 **MySQL 形态**（`docker compose --profile mysql up`）才必须自己给，  
> 默认的 SQLite 形态连这个都不用填。

编辑 `.env`：`COOKIE_SECURE=true`（HTTPS 生产必须，见第 5 节）是唯一无条件必改项；  
`JWT_KEY` / `ADMIN_PASS` 不给就由向导生成；`MYSQL_ROOT_PASSWORD` 仅 MySQL 形态必填。  
可选按需：`WX_APPID/WX_APPSECRET`（小程序码）、`MINIO_*`/`OSS_*`（对象存储）、`METRICS_ENABLED=1`。

> **三处「忘了改就出事」的项，已由代码强制把关**（不是靠人记住）：
>
> 1. `MYSQL_ROOT_PASSWORD` 在 `docker-compose.yml` 里是**必填变量**（仅 MySQL 形态），  
>    只在你显式启用 MySQL 形态（`docker compose --profile mysql up`）时才校验——  
>    未设置则 mysql 容器启动即报错退出（日志有明确提示）、不会带默认值悄悄启动，  
>    backend/frontend 属默认形态照常运行；默认 SQLite 形态不起 mysql，完全不碰这个变量；  
>    `DB_DSN` / `JWT_KEY` / `ADMIN_PASS` 则刻意放宽为**可选**——留空即进入安装向导，  
>    由向导现场生成强随机值并写进 `/app/data/runtime.env`（见 §2.1），  
>    这样「忘记生成密钥」这件事从根上不存在；
> 2. 后端 `cmd/server` 启动时若检测到 `JWT_KEY` 仍是缺省值 `change-me`（或长度 < 16）、  
>    或 `ADMIN_PASS` 仍是内置缺省口令，会**拒绝启动**并打印 `[SECURITY][FATAL]`  
>    （这两个默认值连同演示账号写进了公开文档，等于已知凭据）；  
>    本地联调确需放行加 `ALLOW_INSECURE=1`，**切勿用于生产**；
> 3. 部署前可先跑一遍静态自检（服务不必先启动），它会逐项列出还差什么：
>    ```bash
>    scripts/preflight.sh                 # 有 FAIL 则退出码 1
>    PREFLIGHT_STRICT=1 scripts/preflight.sh   # WARN 也算失败（接发布流水线用）
>    ```

### 小程序端必改（容易漏，且只影响真机）

`miniprogram/utils/config.ts` 的 `DEFAULT_BASE_URL` **默认为空**（源码里不放任何地址——
免得把某台开发机的内网 IP、或明文 http 地址随源码分发给别人）。
它是小程序**首次**拉配置用的引导种子：空着或连不通，就永远拿不到后台下发的正确基址
（后台配置只有在种子能连通一次之后才生效），届时只能重新打包提审。

- 生产：填 https 域名（如 `https://card.example.com`）后重新打包
- 真机联调：填电脑局域网 IP（如 `http://192.168.1.20:8080`），跑 preflight 时加
  `PREFLIGHT_ALLOW_PRIVATE=1`，**别把这段改动提交**
- 守卫：`scripts/preflight.sh` 第 3 节对「空值 / `127.0.0.1` / 私有网段」直接判 FAIL；
  `miniprogram/tests/config.spec.ts`（内网 IP / 非 https 明文即红）与
  `brand.spec.ts`（源码不得出现演示租户名）是第二道闸

`preflight.sh` 第 3 项会检查这一处；未改时判 FAIL。

## 2. 启动

两种形态任选其一（数据库引擎进了安装向导还能再改）：

```bash
# 形态一：零依赖起步（默认）——只起 backend + frontend，向导里选 SQLite，不需要任何口令
docker compose up -d --build
docker compose ps        # backend / frontend 两服务；没有 mysql 是正常的

# 形态二：生产 MySQL——额外起 mysql 容器（MYSQL_ROOT_PASSWORD 必填，见第 1 节；
# --profile 是顶层标志，必须放在 up 之前）
docker compose --profile mysql up -d --build
docker compose ps        # mysql / backend / frontend 三服务均 healthy
```

MySQL 形态首启时（`mysql_data` 卷为空），mysql 容器自动按序执行 `backend/migrations/` 下**全部**
`.sql`（0001–0019）建表 + 种子。安装向导在 MySQL 分支也会按序应用同一批迁移（幂等：已应用过的
语句按 Duplicate column / already exists / Duplicate key name / Duplicate entry 忽略），
因此卷被预建、initdb 未执行的场景照样装得上。  
backend 刻意**不**依赖 mysql 的 healthy 状态：安装模式下它根本不连库，连不连得上由安装向导  
在「装」的那一步决定；硬绑 `service_healthy` 反而会让纯 SQLite 部署（mysql 不启）时  
backend 永远起不来。MySQL 形态装完自重启时，mysql 早已就绪。

> **2026-10-21 表名对齐修复（只影响 2026-10-21 之前初始化过的 MySQL 库）**：  
> migrations 建的表名原为单数（`employee` / `admin` / `company_info`…），而业务代码（gorm 模型）
> 用的是复数（`employees` / `admins` / `company_infos`…）——旧版 MySQL 形态下 migrations 建的表
> 根本没有业务使用，且安装向导会另建一套复数空表，系统是残的（`sys_setting` 还会直接报
> Error 1091 装不上）。现已把 migrations 全部改为复数表名，并有守门测试
> （`internal/model/migrations_guard_test.go`）永久钉住两边一致。  
> **存量旧库处理**：那种库从未真正跑起来过（无业务数据），建议 `docker compose down -v` 删卷
> 重建；确要保留卷则手动改名：`RENAME TABLE employee TO employees, admin TO admins, ...`
> （清单见 migrations 各文件），并补执行 0019。

> **backend 镜像自带 migrations（2026-10-21 修）**：运行镜像把 `migrations/` 拷到
> `/app/migrations`（与二进制同级），安装向导的 MySQL 分支直接按序应用——曾因镜像只含
> 二进制、`runtime.Caller` 在容器里只返回编译期 `/src` 路径，MySQL 安装报
> 「读取 migrations 目录失败」。自建镜像/改布局时可用环境变量
> `WEICHUAN_MIGRATIONS_DIR` 显式指定目录（候选顺序：该变量 → 可执行同级
> `migrations/` → 源码树路径 → CWD 下 `migrations/`）。

## 2.1 首次访问：安装向导（推荐路径）

`.env` 里 `DB_DSN` / `JWT_KEY` / `ADMIN_PASS` 全部留空时（`MYSQL_ROOT_PASSWORD` 按形态给：  
SQLite 形态不用给，MySQL 形态必须给），服务起来会进入「未配置模式」：  
浏览器打开 `http://<服务器IP>:8080`（nginx 发布端口）会直接落到安装页，  
不用再回头改 `.env`、也不用记环境变量名。

页面三步：

1. **选数据库**：MySQL 或 SQLite，与启动时选的 compose 形态对应即可（向导里也能改）。  
   MySQL 形态（`--profile mysql`）下会检测到编排环境并预填  
   主机 `mysql` / 端口 `3306` / 用户 `root` / 库名 `weichuan`，  
   口令仍需自己填 `.env` 里的 `MYSQL_ROOT_PASSWORD`（响应与日志都不回显口令）；  
   SQLite 形态下直接选 SQLite，库文件落在 `/app/data`（`data_data` 卷持久化）；
2. **填管理员账号**：用户名 + 口令（≥8 位，两次一致）。JWT 密钥留空由服务端随机生成；  
   勾选「Cookie 强制 HTTPS」后才写 `COOKIE_SECURE=true`；
3. **测试连接 → 开始安装**：连通性通过后确认一次即执行。

安装动作（服务端一次做完，任何一步失败都不会留下半配置）：

| 步骤   | MySQL                                                                          | SQLite         |
| ---- | ------------------------------------------------------------------------------ | -------------- |
| 建库   | 勾选「自动创建」时 `CREATE DATABASE IF NOT EXISTS`（utf8mb4）                             | 目录自动创建         |
| 建表   | 空库按模型 `AutoMigrate`；compose 已跑过迁移时只补缺失的表/列，不删不改                                | 同（与路径 D 同一套代码） |
| 初始数据 | `sys_setting` 缺省值 + 初始超管（bcrypt 哈希）                                            | 同              |
| 落配置  | 写入 `CONFIG_FILE`（compose 里是 `/app/data/runtime.env`），权限 0600、临时文件 + rename 原子写 | 同              |
| 生效   | `syscall.Exec` 原地自重启（Linux）；Windows / 手动托管时提示人工重启                              | 同              |

装完页面自动轮询 `/api/setup/status`，一旦 `configured=true` 就倒计时跳转登录页。

> **边界与安全**（这几条是刻意的，改动前先看测试 `backend/internal/setup/install_test.go`）：
>
> - 只有在「`DB_DSN` 未提供」时才有安装入口；装完进程内一次性锁生效，  
>   正常模式连写接口都不挂（`/setup` 只返回 302 回首页），不存在「重放安装页重置系统」；
> - `admins` 表已有数据的库直接拒绝安装（不会接管在用系统）；
> - 写接口带同源检查（Origin/Referer 与 Host 比对），挡跨站 CSRF 与 DNS rebinding；
> - 响应与日志一律掩码，口令与完整 DSN 不落明文；
> - 配置写进**配置文件**而非环境变量。完整取值链只有一条：
>   **界面设置 > 环境变量 > 配置文件 > 内置缺省**——界面上留空的项才继续往下找，
>   且只有登记了环境变量名的项才会去找环境变量 / 配置文件（详见 README「取值优先级」）。
>   已在 `.env` 里显式给过值的老部署行为完全不变。

排查：装完却没跳登录页 → 先看 `docker compose logs backend | grep SETUP`；  
容器里自重启失败（非 Linux、或进程被托管）→ 手动 `docker compose restart backend`，  
页面会提示「重新检测」。想重新走一次向导：停服务后删掉 `data_data` 卷  
（`docker compose down -v` 会连 MySQL / uploads 一起删，别在已上线环境用）。

## 3. 首次验证清单（按顺序过一遍）

- [ ] `curl http://127.0.0.1:8090/healthz` → `{"status":"ok"}`（8090 是后端直连调试端口；  
  安装模式下这里返回 503 + `"db":"unknown"` 是**正常**的，装完才会变 200）
- [ ] `docker compose logs backend | grep SECURITY` **无输出**（有输出说明密钥还是默认值，回第 1 步）
- [ ] 浏览器开 `http://<服务器IP>:8080`：未配置时先出安装页（§2.1），装完落到登录页；  
  用安装时填的管理员口令（或 `.env` 的 `ADMIN_USER/ADMIN_PASS`）登录 PC 后台，**立即改密码**
- [ ] `docker compose ps` 里 backend 是 healthy（安装期间判据放宽为「进程活着」，装完恢复按 `db` 判）
- [ ] 表结构齐全（MySQL 形态）：`docker exec -i weichuan-mysql mysql -uroot -p"$MYSQL_ROOT_PASSWORD" weichuan -e "show tables;"` —— 应包含 `revoked_sessions`（0005）等全部迁移产物（复数表名：`employees`/`admins`/`company_infos`…）；SQLite 形态可跳过此项（`AutoMigrate` 自动建表，库在 `data_data` 卷里）
- [ ] `docker compose logs backend | grep MIGRATE`：期望是「已应用 N 个迁移」或「数据库已是最新」，**不能**出现 `[MIGRATE][FATAL]`（详见 §4）
- [ ] 迁移记账表已建立（MySQL 形态）：`SELECT COUNT(*) FROM schema_migrations;` 与 `backend/migrations/*.sql` 数量一致
- [ ] 上传一张图片（公司信息/封面），确认返回 URL 可访问（`uploads_data` 卷已挂载）


## 4. 数据库迁移（升级时）

### 默认路径：启动时自动补齐（不用手工跑）

从本版起，**MySQL 部署在启动时自动应用「尚未应用」的迁移**，无需人工干预：

- 已应用过的迁移记在 `schema_migrations` 表（version + applied_at），重复启动不会重放；
- 只做增量，且每条语句幂等（已存在的列/表报 `1060 / 1050 / 1062` 一律忽略），
  存量库首次被接管时重放全部迁移也安全（随后补记版本）；
- 多副本同时启动用 MySQL `GET_LOCK` 串行化，抢不到锁的副本跳过（另一个正在做）；
- SQLite 部署不适用本节：表由服务启动时按模型 `AutoMigrate` 维护。

多副本上线前还需确认一件事：**登录限流的计数是共享的**（`rate_limit_hits` 表，
`middleware.WithCounter`），否则每个副本各算一份、实际阈值翻倍——这条已经处理好，
不需要额外配置。仍未共享的是小程序码缓存与 access_token：多副本下会各打各的
微信接口，配额消耗按副本数放大（不突破安全边界，但会多花钱），量大会前先考虑收敛。

启动日志三种形态：

```text
[MIGRATE] 数据库已是最新（无待应用迁移）          ← 常态
[MIGRATE] 已应用 2 个迁移：0018_phone2.sql, …      ← 升级生效了
[MIGRATE][FATAL] 应用数据库迁移失败：…             ← 必须处理（服务仍会起来，但表结构可能落后）
```

> 迁移失败**不终止启动**是刻意的：管理员还能进后台查日志、也保留了修完重启自愈的入口。
> 代价是必须靠日志暴露——请把 `[MIGRATE][FATAL]` 加进日志告警的匹配词。

### 想自己掌控：拆成流水线里的一步

```bash
# 关闭启动自动迁移（环境变量 / .env）
AUTO_MIGRATE=0

# 单独跑一步（不启服务、不监听端口，退出码可直接进流水线判断）
docker exec weichuan-backend /app/server migrate
# → [MIGRATE] 已应用 N 个迁移：…
# → [MIGRATE] 数据库已是最新（无待应用迁移）
```

迁移状态随时可查：

```bash
docker exec -i weichuan-mysql mysql -uroot -p"$MYSQL_ROOT_PASSWORD" weichuan \
  -e "SELECT version, applied_at FROM schema_migrations ORDER BY version;"
```

### 仍要手工执行的场景（回滚 / 排障）

compose 的 `initdb.d` 自动建表只在 `mysql_data` 卷**为空**（首启）时执行。对已存在的数据库：

```bash
for f in backend/migrations/000*.sql; do
  docker exec -i weichuan-mysql mysql -uroot -p"$MYSQL_ROOT_PASSWORD" weichuan < "$f"
done
```

迁移向后兼容、只增不改；若报 `1060 Duplicate column` / `1050 table already exists`，说明该变更已应用过，可忽略。**执行前先 `mysqldump` 备份**。
手工执行后记得把对应版本写进 `schema_migrations`，否则启动时还会再跑一遍（幂等，但会重复产生可忽略的报错）。

## 5. HTTPS（生产必须，小程序合法域名的前提）

compose 自身对外是 http。推荐前置 Caddy 自动申请证书（最省事，无需手动续期）：

```yaml
# 追加到 docker-compose.yml
caddy:
  image: caddy:2
  restart: unless-stopped
  ports: ["80:80", "443:443"]
  volumes:
    - ./Caddyfile:/etc/caddy/Caddyfile:ro
    - caddy_data:/data
  depends_on: [frontend]
```

```
# Caddyfile（把域名换成实际域名，Caddy 自动签发 + 续期 Let's Encrypt 证书）
your-domain.com {
  reverse_proxy frontend:80
}
```

同时确认 `.env` 里 `COOKIE_SECURE=true`——否则 httpOnly Cookie 不带 `Secure` 标志，HTTPS 下浏览器虽仍会发送，但丧失了「永不走明文」的保证（A2 加固在生产打折）。

> 也可用云厂商 SLB/ALB 或自建 nginx 终结 TLS，效果等价；`COOKIE_SECURE=true` 同样必须。

## 6. MySQL parity 验证（上线前必做，约 10 分钟）

项目测试默认跑 SQLite，而生产是 MySQL——**本仓库有真实前科**：`ON CONFLICT DO NOTHING` 在 SQLite 测试全绿，但 MySQL 不支持该语法（生产登出会 500）。上线前必须用真实 MySQL 把 parity 跑绿：

```bash
# 起一个一次性 MySQL 8.0（跑完即可删；也可指向测试库）
docker run -d --name parity-mysql -p 13306:3306 \
  -e MYSQL_ROOT_PASSWORD=root -e MYSQL_DATABASE=weichuan_test mysql:8.0

cd backend
TEST_MYSQL_DSN='root:root@tcp(127.0.0.1:13306)/weichuan_test?charset=utf8mb4&parseTime=true' \
  go test ./... -v
```

预期：`TestAPIFlowMySQL` / `TestMySQLMigrations` 由 SKIP 转为 **PASS**，其余用例不变。全绿再上线。

> 本机需 Go ≥ 1.26（见 `backend/go.mod`）。想让 CI 常驻该门禁：在仓库 Settings → Variables 配 `TEST_MYSQL_DSN`，指向一个 GitHub runner 公网可达的 MySQL（runner 无内网访问）。

## 7. 挂上运维任务

```cron
# 每天凌晨 3 点全量备份（mysqldump → gzip → 保留 7 天）
0 3 * * * cd /path/to/repo && scripts/backup.sh >> /var/log/weichuan-backup.log 2>&1

# 每分钟告警检查（healthz 本地探活 / 5xx / 备份新鲜度 / 外部拨测，webhook 换成实际机器人地址）
* * * * * cd /path/to/repo && ALERT_WEBHOOK="https://qyapi.weixin.qq.com/cgi-bin/webhook/send?key=xxxx" \
  HEALTH_URL=http://127.0.0.1:8080/healthz \
  EXTERNAL_URL=https://你的域名/healthz \
  LOG_FILE="$(docker inspect -f '{{.LogPath}}' weichuan-backend)" \
  BACKUP_DIR=/path/to/repo/backups \
  scripts/alert_check.sh >> /var/log/weichuan-alert.log 2>&1
```

`EXTERNAL_URL` 是**外部拨测**（R4）：从对外域名访问 `/healthz`，覆盖本地探活看不到的
那一半故障——域名没解析、443 没放行、证书过期、反代 upstream 挂了。同时校验响应耗时
（`EXTERNAL_MAX_MS`，默认 2000ms）与证书剩余有效期（`CERT_MIN_DAYS`，默认 14 天）。
不填该项则 R4 跳过（已用 UptimeRobot 之类外部拨测服务的部署可以不填，避免重复）。

> 外部拨测只从「本机之外」发起，仍可能漏掉「机房出口正常、某运营商不通」这类区域性故障。
> 真要盯全国可用性，还是需要多地拨测服务；本脚本的定位是「没有监控基建也能跑起来的最小闭环」。

## 8. 小程序侧切换（提审前）

1. 接口基址改为 `https://你的域名`：基址**不再写死在代码里**，改由后台「系统设置 → 小程序 → 小程序接口地址」下发（`miniprogram/utils/config.ts` 的 `miniprogram_base_url`；`app.ts` 里的 `DEFAULT_BASE_URL` 只是首次冷启动前的引导种子）。改完清一次小程序缓存再验证。
2. 微信公众平台 → 开发管理 → 开发设置 → 服务器域名：
   - `request` 合法域名：`https://你的域名`
   - `uploadFile` 合法域名：`https://你的域名`（头像上传）
   - `downloadFile` 合法域名：同上（如用到）
   - **域名必须已备案；每月修改次数有限，一次填对。**
3. **开发管理 → 用户隐私保护指引**（微信审核必查项，漏了会被打回）：代码侧调用的隐私接口必须逐个声明对应类型——

   | 代码里的调用 | 需声明的隐私类型 |
   |---|---|
   | `wx.chooseMedia`（头像 / 海报背景选图） | 收集你选中的照片或视频信息 |
   | `wx.getUserProfile`（昵称） | 收集你的昵称、头像 |
   | `<button open-type="chooseAvatar">`（微信头像） | 收集你的昵称、头像 |
   | `wx.saveImageToPhotosAlbum`（海报存相册） | 使用你的相册（仅写入）权限 |
   | `wx.setClipboardData`（复制手机号 / 邮箱） | 读取你的剪切板 |
   | `wx.addPhoneContact`（存名片到通讯录） | 使用你的通讯录（仅写入）权限 |
   | `wx.login` 换 openid | 用户标识信息（openid） |

   声明项须与代码实际调用**一致**：多声明无碍，少声明则对应接口被平台直接禁用（`errno: 112`），真机表现为「选图没反应 / 报错」。
4. 视频号渲染（`<channel-video>`）需真机 + 小程序绑定视频号；测试号下该块留空属预期内。

## 8.5 运营：试用授权与存储配额（平台 / SaaS 模式）

两件事都在「企业管理 → 授权与配额」一个弹窗里，但它们的**默认值都是"不限制"**，
这是刻意的：升级不该让存量客户突然被停服。要开始按席位/容量售卖，得显式打开。

| 想做的事 | 在哪里 | 注意 |
|---|---|---|
| 改试用口径（新开通企业自动拿到的席位与天数） | 系统设置 → 授权：`license_trial_enabled` / `license_trial_days` / `license_trial_seats`（缺省 开启 / 14 天 / 5 席） | 只影响**以后新开的**企业。已落行的授权是快照，不会跟着漂 |
| 客户续费 / 转正 / 加席位 | 企业管理 → 「授权」→ 改席位与到期日 | 到期日按 `YYYY-MM-DD` 保存，落库为**当天 23:59:59**；填过去的日期会二次确认（保存即降级只读） |
| 到期后发生了什么 | 后端 `EnforceLicense` 自动降级只读（写操作返回 `{code:2}`） | **降权是自动的，恢复不是**——必须有人来改到期日，所以要把 `[LICENSE]` 日志接告警 |
| 给某个客户单独放大存储 | 企业管理 → 「授权与配额」→ 存储上限（MB，0 = 跟随全局） | 全局默认 `storage_quota_mb = 0`（不限）；单租户覆盖优先于全局 |
| 看谁占了多少 | 企业管理列表「存储」列 / 弹窗内「本企业已用」 | 口径是**自本功能启用起累计的上传量**，不含历史文件、不含覆盖写——不是磁盘真实占用 |
| 客户报"空间已用尽" | 先看该企业已用/上限；再看日志有无 `[STORAGE][WARN]` | `[STORAGE][WARN]` 表示累加失败（配额会低估），不是超额原因 |

> 存量企业的授权状态是「未配置 = 不限制」。这是合法状态，不是数据缺失——不要为了"整齐"批量补行，
> 那等于一次性把所有老客户套上席位与到期限制。

## 9. 回滚、备份与恢复

### 9.1 代码回滚

- `git checkout <上一个提交> && docker compose up -d --build`。数据库不动——迁移只增不删、向后兼容，旧版本代码能读新表结构跑。
- **切勿对生产执行 `docker compose down -v`**——这会清空 `data_data` / `mysql_data` / `uploads_data` 卷。

### 9.2 备份（每天跑，进 crontab）

```bash
# SQLite（默认形态）
DB_DRIVER=sqlite scripts/backup.sh
# MySQL
DB_DRIVER=mysql scripts/backup.sh
```

| 形态 | 产物 | 做法 |
|---|---|---|
| SQLite | `backups/weichuan-<时间戳>.db.gz` | 容器内 `server backup`（`VACUUM INTO` 在线快照）→ 就地 `integrity_check` → 拷出 → gzip |
| MySQL | `backups/weichuan-<时间戳>.sql.gz` | 容器内 `mysqldump --single-transaction` → gzip |

**SQLite 为什么不能直接 `cp` 库文件**：库开着 WAL，主文件里可能只有一部分页，剩下的还在
`-wal` 里。`cp` 出来的是一份撕裂快照，恢复回来 `integrity_check` 直接不通过。
实测：一个刚启动的库主文件只有 4 KB，WAL 有 1.2 MB——`cp` 得到的几乎是个空库。

轮转按 `KEEP_DAYS`（默认 7 天）。备份失败**不落新文件**，所以
`scripts/alert_check.sh` 的 R3（备份新鲜度）能兜底发现「备份停了」。

### 9.3 恢复

```bash
scripts/restore.sh backups/weichuan-20261025-030000.db.gz          # 交互确认
scripts/restore.sh backups/weichuan-20261025-030000.sql.gz --yes   # 脚本化
```

它会做四件事，顺序不能变：

1. **先备份当前库**到 `backups/pre-restore-<时间戳>/`——恢复是破坏性操作，
   拿两周前的备份盖上去等于丢掉这两周的数据，没有退路不能动手。
   这一步失败会直接中止（确需跳过用 `--no-safety`）。
2. 解压到临时文件（管道中断会留下半个库，所以不直接流式导入）。
3. 覆盖：SQLite 走一次性容器挂载 `/app/data` 卷完成「删旧 WAL → 写库 → 停/起容器」；
   MySQL 走 `mysql < dump`（`mysqldump` 自带 `DROP TABLE IF EXISTS`，导入即替换）。
4. 校验：SQLite 用 `server dbcheck` 验 `integrity_check` 与表数，并等 `/healthz` 回 2xx；
   MySQL 验表数与 `schema_migrations` 版本。

**SQLite 必须删 `-wal` / `-shm`**：库文件被替换后，旧 WAL 里还是上一个库的页，
SQLite 打开时会尝试回放它——结果是把旧数据「恢复」进新库，比不恢复更糟。

### 9.4 恢复演练（每季度至少一次）

```bash
scripts/restore_drill.sh                  # 演练最新一份备份
scripts/restore_drill.sh backups/xxx.db.gz
```

演练**不碰生产**：SQLite 全程在临时目录里做，MySQL 恢复到临时库
`weichuan_drill_<时间戳>` 并在结束时删掉。它验证四件事：

| 步骤 | 判据 | 失败含义 |
|---|---|---|
| 1. 归档完整性 | `gzip -t` | 备份包已损坏 |
| 2. 恢复到临时目录 | 文件头是 `SQLite format 3` | 备份内容与预期不符 |
| 3. 库完整性 | `integrity_check = ok` 且表数 > 0 | 备份是坏库 / 空库 |
| 4. 与生产对比 | 生产有的表备份里都有 | **备份不完整**，恢复回去会缺功能 |

行数差异只报告不判失败——日备本来就落后于生产，判失败会让演练永远红着，
最后没人看。

退出码：`0` 通过 / `1` 有 FAIL / `2` 环境不完整（没有可用的 `dbcheck` 或数据库容器）。
**`2` 不等于通过**：它只说明「归档没坏、文件头像 SQLite」，没真正打开过库。
请在部署机上重跑，或设 `SERVER_BIN=/path/to/server`（原生部署）/
`APP_CONTAINER`、`DB_CONTAINER`（容器部署）指向实际环境。

> 演练记录建议贴进运维日志：日期 + 备份文件名 + 结论 + 行数差异。
> 没演练过的备份不算备份——它只是「一堆文件里看起来最新的那个」。
>
> **留档**：演练输出请存到 `docs/drill-records/YYYY-MM-DD-<形态>.md`（`restore_drill.sh`
> 会打印可直接粘贴的记录正文），日期 + 备份文件名 + 结论 + 行数差异四项齐全。
> 客户/审计问「你们怎么证明备份能用」时，这份记录是唯一拿得出手的东西。

### 9.5 时间点恢复（PITR）：全量备份 + binlog

全量备份的恢复点是「备份那一刻」。凌晨 3 点备份、下午 4 点误删一张表，
只用全量备份就得退回凌晨 3 点——中间 13 小时的数据没了。binlog 记的是这 13 小时
里每一次写，**「全量备份 + binlog 回放」才能把数据恢复到误操作前一秒**。

前置条件：数据库必须开了 binlog。本项目 compose 的 mysql 服务默认已带
`--log-bin --server-id=1 --binlog-format=ROW`，自建 MySQL 需自行确认：

```sql
SHOW VARIABLES LIKE 'log_bin';   -- 必须是 ON
```

归档（建议每小时一次，比全量备份密得多）：

```cron
0 * * * * cd /path/to/repo && scripts/backup_binlog.sh >> /var/log/weichuan-binlog.log 2>&1
```

它按「先 FLUSH 滚动 → 逐个复制并 `gzip -t` 校验 → 校验全过才 `PURGE BINARY LOGS`」
的顺序做，顺序不能换：先 PURGE 再校验的话，一旦归档坏了，源和副本就一起没了。
`log_bin=OFF` 时脚本**报错退出**（不是空跑成功）——「以为有 PITR 其实没有」比
「知道没有」危险得多。

恢复到某个时间点：

```bash
# 1) 找一份早于目标时刻的全量备份，恢复到临时实例（不要直接盖生产）
scripts/restore.sh backups/weichuan-20261025-030000.sql.gz

# 2) 回放从备份时刻到误操作前一刻的 binlog
gunzip -c backups/binlog/<时间戳>/mysql-bin.000123.gz > /tmp/mysql-bin.000123
mysqlbinlog --start-datetime="2026-10-25 03:00:00" \
            --stop-datetime="2026-10-25 15:59:59" \
            /tmp/mysql-bin.000123 | mysql -uroot -p weichuan

# 3) 核对数据后再切流
```

`--stop-datetime` 取「误操作前一秒」而不是「误操作那一刻」：binlog 里的事件是
按事务边界组织的，取到等于误操作的时刻很容易把那条 `DELETE` 也一起放进去，
回放完等于没恢复。

> SQLite 形态没有 PITR：它没有 binlog 这种增量日志，恢复点只能是备份时刻。
> 对数据丢失零容忍的客户请走 MySQL 形态。

## 9.6 集中日志

服务日志默认是人读文本（保留 `[WARN]` / `[ACCESS-5XX]` 这类前缀，grep 好用）。
要做集中采集就把形态切成 JSON：

```bash
LOG_FORMAT=json          # .env 里设置；容器形态 compose 已留好变量
```

切完之后**每一行都是一个 JSON 对象**，既有代码一行都不用改（实现见
`backend/internal/logx`——它包装的是标准 log 的输出目标，级别从既有的方括号标签
推断，认不出的按 info，绝不丢行）：

```json
{"level":"warn","msg":"磁盘剩余空间不足","tag":"WARN","ts":"2026-10-27T01:20:04.123+08:00"}
{"level":"info","msg":"http_access","method":"GET","path":"/api/employees","status":200,"latency_ms":3,"request_id":"a1b2","ip":"1.2.3.4"}
```

| 项 | 说明 |
|---|---|
| 采集 | `deploy/promtail/config.yml`（送 Grafana Loki），或任意支持读容器 stdout 的采集器 |
| 轮转 | 容器已在 compose 里配 `json-file` + `max-size=10m` / `max-file=7`；**改完要 `--force-recreate`**（docker 只在容器创建时读日志配置） |
| 非容器部署 | 用 `deploy/logrotate/weichuan.conf`（注意 `copytruncate`：服务是追加写同一句柄，默认的 create 会让它继续写已被改名的旧文件，磁盘不会释放） |
| 指标 | `METRICS_ENABLED=1` 开 `/metrics`；建议同时设 `METRICS_ALLOW_CIDR=127.0.0.1/32,10.0.0.0/8`——白名单是第二道门，即便开关被人在界面上打开，公网访问 `/metrics` 仍一律 404 |
| 建议告警 | `level="fatal"` 立即告警；`level="error"` 5 分钟内 ≥3 次告警；`status>=500` 比例突增告警 |

> 白名单写错（CIDR 非法）时 `/metrics` 会**全部拒绝**而不是放行——配了白名单却没生效
> 是最难发现的一类暴露。启动日志会点名哪一条解析失败。

## 常见问题

| 现象 | 原因 / 处理 |
|---|---|
| backend 反复重启 | 已配置模式下多是数据库连不上：`docker compose logs backend` 看 DB_DSN 与实际库状态（SQLite 形态确认 `data_data` 卷还在；MySQL 形态确认启动时带了 `--profile mysql`）。安装模式（未配置）下 backend 健康判据已放宽为「进程活着」，不属于此项 |
| 登录后立刻 401 循环 | `COOKIE_SECURE` 与协议不匹配——http 访问却设了 `true`（或反之） |
| 上传报 413 | 请求体超限：`frontend/nginx.conf` 已设 50m；前置 Caddy 默认不限 |
| 小程序码接口 code=1 | 未配 `WX_APPID/WX_APPSECRET`，联调期属预期；配了仍失败多为凭据不配对或调用 IP 不在白名单（`PREFLIGHT_WX_LIVE=1 scripts/preflight.sh` 可直接问微信接口） |
| 启动日志出现 `[MIGRATE][FATAL]` | 迁移没应用完（表结构落后于代码版本）。看上面的报错处理，或 `AUTO_MIGRATE=0` + `docker exec weichuan-backend /app/server migrate` 单独跑（详见 §4）。服务不会因此终止，但功能可能报错 |
| 版本升级后功能报错、表缺列 | 同上一行：确认 `schema_migrations` 里有没有最新那个迁移文件（§4 有查询 SQL） |
| 视频号块空白 | 测试号 / 未绑视频号 / 模拟器——需真机 + 绑定视频号的正式 AppID |
| 日志变成一行行 JSON 看不懂 | 部署环境里设了 `LOG_FORMAT=json`（集中日志用）。本地排查改回 `LOG_FORMAT=text` |
| `/metrics` 一直 404 | 三道门任一未过：`metrics_enabled` 开关没开（后台「系统设置 → 功能」）、`METRICS_ALLOW_CIDR` 配了但来源 IP 不在其中、CIDR 写错（启动日志有 `[WARN]` 点名） |
| 审计日志表越来越大 | 后台「系统设置 → 安全 → 审计日志保留天数」填 180/365；默认 0 = 永不自动清理（删了不可逆，所以不做默认删除）。清理动作本身会记一条 `audit.purge` 审计 |
