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
    root /path/to/weichuan-card-v2/frontend/dist;
    index index.html;
    client_max_body_size 50m;

    # 必须单列一条：不能让它落到下面的 SPA 兜底。try_files 会把 /healthz/ready
    # 回退成 index.html 并返回 200，于是后端已死、数据库连不上，这个地址照样 200——
    # alert_check 的 R1/R4、preflight 第 10 节、LB 探针全部永远绿，最该报的故障不报。
    #
    # 用 `^~ /healthz`（结尾不带 /）一条覆盖 /healthz、/healthz/、/healthz/live、
    # /healthz/ready 四个路径。写成 `^~ /healthz/` 会触发 nginx 的目录语义
    # （auto_redirect 把 /healthz 301 到 /healthz/），与后端 RedirectTrailingSlash
    # 互踢成死循环；proxy_pass 也必须不带路径，否则前缀替换会改写上游 URI。
    location ^~ /healthz {
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

### 排障：刷新或直接访问 `/login`、`/companies` 等页面返回 nginx 404

**现象**：登录后站内跳转一切正常；但「刷新页面」或在地址栏直接输入某个路由（如 `/login`、`/companies`、`/login?company=xxx`）就得到 nginx 的 `404 Not Found`。

**原因**：Vue 用 history 路由，这些路径在服务端并没有对应文件。线上宝塔站点**漏了 SPA 兜底** `location / { try_files $uri $uri/ /index.html; }`——站内跳转是浏览器端路由（不发 HTTP 请求），所以看不出问题；刷新/直连才会真正向 nginx 要 `/login` 这个文件，找不到就是 404。用 `curl` 一眼可辨：

```bash
curl -sI https://<域名>/ | head -1          # 200  → root/index 正常
curl -sI https://<域名>/login | head -1     # 404  → 缺兜底（本条就是）
```

若 `/` 也是 404，则先查 `root` 是否指向 `dist`（里面必须有 `index.html`）、以及 `index index.html;` 是否生效。

**修复**：在宝塔「网站 → 站点 → 配置文件」的 `server { }` 内，把下面这段放在 `/api/`、`/uploads/`、`^~ /healthz` 三条 location **之后**：

```nginx
location / { try_files $uri $uri/ /index.html; }
```

保存后 `nginx -t && nginx -s reload`（或面板「重载配置」）。前面若有 Cloudflare/其它 CDN：这类 HTML 响应通常是 `cf-cache-status: DYNAMIC`（不缓存），改完即时生效；若曾缓存过 404，清一下对应 URL 缓存即可。

> 顺序不能反：`^~ /healthz` 必须**单列且结尾不带斜杠**，否则会被 `try_files` 兜成 `index.html` 恒返回 200——后端进程已死、数据库连不上，这个探针照样绿，监控静默失效（见上方注释）。

后端常驻，二选一：

```bash
nohup /path/to/weichuan-card-v2/backend/weichuan > /var/log/weichuan.log 2>&1 &   # 最简
```

```ini
# /etc/systemd/system/weichuan.service（推荐：崩溃自拉起、开机自启）
[Unit]
Description=weichuan backend (dev mode)
After=network.target

[Service]
WorkingDirectory=/path/to/weichuan-card-v2/backend
Environment=DEV_UPLOAD_BASE_URL=http://192.168.1.10:8080/uploads
ExecStart=/path/to/weichuan-card-v2/backend/weichuan
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
| 授权公钥 | 私有 / 独立部署需注入发行方自己的 Ed25519 公钥（环境变量 `LICENSE_PUB`） | 在**签发环境**跑 `scripts/license_setup.sh` 一键生成密钥对并把公钥写进 `data/runtime.env` 的 `LICENSE_PUB`（私钥落盘 `license-private.key`，0600）；手工做法是 `license keygen` 后自行填。**私钥只留在签发环境**。仓库内嵌的那把是占位演示密钥（配对私钥公开），私有部署带着它会**拒绝启动**。集中 / 平台模式不验本地许可证，不受此限 |

## 1. 获取代码与配置

```bash
git clone git@github.com:Illacme/weichuan-card-v2.git
cd weichuan-card-v2
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

`miniprogram/utils/config.ts` 的 `DEFAULT_BASE_URL` 是小程序**首次**拉配置用的引导种子：
空着或连不通，就永远拿不到后台下发的正确基址（后台配置只有在种子能连通一次之后才生效），
届时只能重新打包提审。

本仓库是已上线部署仓，`DEFAULT_BASE_URL` **已预填生产域名**
`https://weichuan.ziyuanxingqiu.cn`——首次冷启动即可连后端，无需再改代码即可发出首个请求；
之后正式基址由后台「系统设置 → 小程序 → 小程序接口地址」（`miniprogram_base_url`）下发覆盖。
若换域名，改这一处（或后台下发）并重新打包小程序即可。

- 生产：种子已预填 `https://weichuan.ziyuanxingqiu.cn`；如需换域名改这一处后重新打包
- 真机联调：可临时把种子改回电脑局域网 IP（如 `http://192.168.1.20:8080`），跑 preflight 时加
  `PREFLIGHT_ALLOW_PRIVATE=1`，**别把这段改动提交**
- 守卫：`scripts/preflight.sh` 第 3 节对「`127.0.0.1` / 私有网段 / 明文 http」直接判 FAIL；
  `miniprogram/tests/config.spec.ts`（内网 IP / 非 https 明文即红）是第二道闸

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
3. **初始数据（默认不装任何数据）**：不勾选 = 装完是一个**干净的空系统**——没有企业、  
   没有业绩分类、没有员工，由平台超管登录后先在「企业管理」新建企业，再逐步录入内容。  
   勾选「装载演示企业数据」则写入一整套朗汇机电样板（企业信息 + 8 个业绩分类 +  
   10 个项目案例 + 4 项服务 + 27 张配套占位图），并额外生成 2 个演示登录账号；  
   **口令只在安装完成页显示一次**（库里存哈希），离开页面就找不回来了。  
   演示数据与本地开发环境（`go run ./cmd/devserver`）用的是同一个包 `internal/demoseed`；
4. **测试连接 → 开始安装**：连通性通过后确认一次即执行。

安装动作（服务端一次做完，任何一步失败都不会留下半配置）：

| 步骤     | MySQL                                                                          | SQLite         |
| ------ | ------------------------------------------------------------------------------ | -------------- |
| 建库     | 勾选「自动创建」时 `CREATE DATABASE IF NOT EXISTS`（utf8mb4）                             | 目录自动创建         |
| 建表     | 按 `migrations/*.sql` 顺序应用（幂等：Duplicate column/already exists 忽略）              | 按模型 `AutoMigrate` |
| 初始数据   | `sys_setting` 缺省值 + 初始超管（bcrypt 哈希）+ 签发 `PII_ENC_KEY`；演示数据仅在勾选时写入                 | 同              |
| 落配置    | 写入 `CONFIG_FILE`（compose 里是 `/app/data/runtime.env`），权限 0600、临时文件 + rename 原子写 | 同              |
| 生效     | `syscall.Exec` 原地自重启（Linux）；Windows / 手动托管时提示人工重启                              | 同              |

> **`PII_ENC_KEY` 为什么排在「写演示数据」之前**：员工的手机号盲索引是用当时的密钥算的，
> 先写数据后换钥 = 这条记录再也查不到（小程序登录报「账号不存在」），且报错与密钥毫无关联。
> 顺序不可调换，`install_test.go` 的 `TestInstallWithDemoData` 钉住了它。

> **迁移里没有业务种子数据**（`migrations/0001` 曾硬插 7 条业绩分类、`0011` 曾硬插朗汇企业，
> 都已摘除）。守卫用例 `TestMigrationsCarryNoBusinessData` 会拦住任何 `INSERT` 回退。
> 已上线的库不受影响：迁移按文件名记账（`schema_migrations`），不会重放。

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

- [ ] `curl http://127.0.0.1:8090/healthz/ready` → `{"status":"ok","db":"up"}`（8090 是后端直连调试端口；  
  安装模式下这里返回 503 + `"db":"unknown"` 是**正常**的，装完才会变 200）
- [ ] `docker compose logs backend | grep SECURITY` **无输出**（有输出说明密钥还是默认值，回第 1 步）
- [ ] 浏览器开 `http://<服务器IP>:8080`：未配置时先出安装页（§2.1），装完落到登录页；  
  用安装时填的管理员口令（或 `.env` 的 `ADMIN_USER/ADMIN_PASS`）登录 PC 后台，**立即改密码**
- [ ] `docker compose ps` 里 backend 是 healthy（安装期间判据放宽为「进程活着」，装完恢复按 `db` 判）
- [ ] 表结构齐全（MySQL 形态）：`docker exec -i weichuan-mysql mysql -uroot -p"$MYSQL_ROOT_PASSWORD" weichuan -e "show tables;"` —— 应包含 `revoked_sessions`（0005）等全部迁移产物（复数表名：`employees`/`admins`/`company_infos`…）；SQLite 形态可跳过此项（`AutoMigrate` 自动建表，库在 `data_data` 卷里）
- [ ] `docker compose logs backend | grep MIGRATE`：期望是「已应用 N 个迁移」或「数据库已是最新」，**不能**出现 `[MIGRATE][FATAL]`（详见 §4）
- [ ] 迁移记账表已建立（MySQL 形态）：`SELECT COUNT(*) FROM schema_migrations;` 与 `backend/migrations/*.sql` 数量一致
- [ ] **传输优化生效**（宝塔部署必查，Docker 部署的 `frontend/nginx.conf` 自带）：
  ```bash
  # 静态资源必须带长缓存 + gzip；拿任一 /assets/ 下的 js 实测
  curl -sI -H 'Accept-Encoding: gzip' https://<域名>/assets/<任一hash文件名>.js \
    | grep -iE 'content-encoding|cache-control'
  # 期望：Content-Encoding: gzip 且 Cache-Control: public, max-age=31536000, immutable
  curl -sI https://<域名>/index.html | grep -i cache-control   # 期望 no-cache
  # 安全头：宝塔默认配置里一个都没有，必须确认这四行都在
  curl -sI https://<域名>/index.html | grep -iE 'x-content-type-options|x-frame-options|referrer-policy|x-robots-tag'
  ```
  安全头的**可粘贴片段**在 `deploy/baota/security-headers.snippet.conf`（每个带 `add_header` 的
  location 都要重复声明整组，一处都别漏，尤其 `location = /index.html` 与 `location ~* ^/assets/`
  ——nginx 的 `add_header` 继承陷阱：location 里只要有一个 `add_header`，server 级的整组都不再继承）。
  **判断 gzip 是否生效只能看实测的 `content-encoding`，不能看站点配置文件**：宝塔的 gzip
  是面板 `性能调整 → gzip` 的全局开关（写进 `nginx.conf` 的 `http` 块），站点配置文件里
  本来就搜不到，据此断言「没开 gzip」是错的。若 HEAD 请求没返回 `content-encoding`
  （部分 CDN 对 HEAD 不压缩），改用 `curl -sS -o /dev/null -D- --compressed <url>` 复测。
  缺长缓存则每次刷新几十个资源都要回源校验——这是「感觉整体很慢」最常见的原因。
- [ ] **全站不可索引**（后台被搜索引擎收录 = 白送一份后台结构图）：
  ```bash
  curl -s https://<域名>/robots.txt | head -3      # 期望 User-agent: * / Disallow: /
  curl -sI https://<域名>/index.html | grep -i x-robots-tag
  # 期望 X-Robots-Tag: noindex, nofollow, noarchive
  curl -s https://<域名>/robots.txt -o /dev/null -w '%{content_type}\n'   # 期望 text/plain
  ```
  `robots.txt` 之前返回的是 `index.html`（`200 text/html`）——那是 SPA 兜底把不存在的路径
  也兜成了壳，`content_type` 是唯一的判据。三道防线与「以后要开官网时怎么放行」见 §5.5。
- [ ] 上传一张图片（公司信息/封面），确认返回 URL 可访问（`uploads_data` 卷已挂载）
- [ ] **零数据核对**：没勾「装载演示企业数据」时，后台「企业管理」应为空、企业内容页**看不到任何分类/案例/服务**。
  接着做一遍「从零起步」：企业管理 → 新建企业（可一并填企业管理员账号）→ 顶栏**选择该企业** →
  录公司信息 / 分类 / 案例 / 服务 → 小程序能打开对应名片。  
  新建企业默认会复制**业绩分类模板**（经典业绩 8 类，只有名称、图标为空、可改名可删）；
  开通时取消勾选则不建任何分类，企业管理员得先自建分类才能录案例。  
  平台超管在**未选企业**时，企业内容 / 数据统计 / 审核管理 / 员工管理会显示「请先选择要管理的企业」空态——
  这是刻意的：这几页一次只能对着某一家企业操作，不选就会被后端兜到 1 号企业或「全部企业」。
- [ ] **小程序落地页核对**：微信「体验版二维码」扫进去应看到**名片墙**（公司抬头 + 全员宫格），
  不是「输入员工编号」的弹窗；点任一同事能进他的名片。想看**某个具体员工**的名片，
  必须扫后台生成的员工小程序码（`scene=staffId=`）或员工转发的名片卡片——见 §3.1。  
  例外：企业若在「企业内容 → 小程序设置 → 访客先看到谁」里把默认入口改成「指定员工的名片」，
  裸启动会直接进那位员工——这是**配置生效**，不是串号。


### 3.1 小程序的三种「入口」及其参数（验收时最常踩的坑）

微信小程序**没有任何办法**让「体验版二维码」带上参数——那是微信的机制，不是本项目的缺陷。
弄错入口，就会得到「扫进去怎么让我输员工编号 / 怎么是别人的名片」这种结论。

| 入口 | 谁生成 | 是否带 `staffId` | 落地页 | 用途 |
|---|---|---|---|---|
| 微信「体验版二维码」/ 直接打开 | 微信后台、开发者工具 | ❌ 不带（裸启动） | **按企业设置**：名片墙（缺省）或指定员工名片 | 验证「小程序能打开、能跑通」 |
| 员工小程序码 | PC 后台「员工管理 → 小程序码」/ 海报页 | ✅ `scene=staffId=<id>` | 该员工的名片 | 验证「某人的名片长什么样」、真实分发 |
| 名片卡片转发 | 员工点右上角转发 / 海报分享 | ✅ `?staffId=<id>` | 该员工的名片 | 真实裂变 |

- 裸启动的落点可在后台**「企业内容 → 小程序设置 → 访客先看到谁」**里改（`company_infos.mp_entry_mode`，
  迁移 0034）：`wall`=名片墙（缺省，升级后行为不变），`staff`=直接进指定员工的名片。
  企业管理员自己能改，不必找平台。同页第二个页签「名片首页样式」管的是**那一页长什么样**
  （板块顺序 / 背景图 / 遮罩）——两项是不同接口、各自保存，菜单合并成一项只是因为它们
  回答同一个问题「客户打开小程序看到什么」。旧路径 `/content/home-layout` 与
  `/content/mp-entry` 仍可访问（重定向到对应页签）。
- 只影响裸启动。员工小程序码与转发卡片**永远**进对应员工的名片，改这个设置不动它们。
- 指定的员工被**停用/删除**时，服务端会把裸启动**回落**到名片墙（不会让访客看到一张
  停用的废页），但**不改配置原值**——员工重新启用后设置自动恢复生效。后台此时会显示
  黄色告警（「指定的员工已停用 / 已不存在」），看到告警就去换人或改回名片墙。
- `GET /api/team` 随列表一起下发 `entry { mode, staffId }`（**生效值**），且**不走 CDN 缓存**：
  改完设置立刻扫码就能验，缓存 60 秒会被误判成「没保存成功」。
- 裸启动落到名片墙是 `app.json` 的 `entryPagePath`（`pages/team/team`）决定的，**不是**
  `pages` 数组第一项——分享链接写死了 `pages/index/index?staffId=`，把首页改成名片墙会
  让所有历史分享链接失效。
- 名片墙的数据来自 `GET /api/team`，**只列启用员工**，只回姓名/职位/头像/编号
  （手机号不在其中，公开接口不能给）。
- 企业信息还没填时名片墙标题回落「微传名片」；企业被停用则显示「该企业暂不可用」。
  三种状态（加载失败 / 企业不可用 / 公司还没人）文案是分开的，看到哪一种就查哪一类问题。

### 3.2 日常发版：前端 dist 怎么传（用脚本，别手拖）

手拖 dist 有个隐蔽的坑：**顺序**。Vite 产物是 `index.html` + 一堆带 hash 的 `assets/`。
如果先把 `index.html` 拖上去，用户会在那几秒里拿到新壳、却找不到还没传完的新资源，
浏览器直接 404 → **白屏**。反过来做（先 assets 后 index.html）任何时刻都是可用组合。

这个顺序靠人记不住，用脚本：

```bash
# 先看要做什么（不动服务器）
./scripts/deploy_frontend.sh -n

# 真发（默认会先校验产物自洽性 → 传 assets → 换壳 → 自检）
# 默认目标已固化在脚本里（DEFAULT_DEPLOY_HOST=root@123.56.154.49），直接跑即可
./scripts/deploy_frontend.sh

# 连构建一起（等价于 npm run build && 上面那条）
./scripts/deploy_frontend.sh --build
```

脚本做了五件手拖做不到的事：

1. **产物自洽性校验**：`index.html` 引用的每个 `/assets/...` 在本地必须存在。
   抓的是「改了代码忘了重新 build 就发版」——壳引用了已被删掉的旧 hash，发上去就是白屏。
   不通过直接退出码 1，一个字节都不传。
2. **目标目录自检**：先看远端 `DEPLOY_PATH` 在不在、里面有没有 `index.html`，
   并把「线上当前壳」和「本地待发壳」两个 hash 并排打出来。路径写错时脚本会在别处
   悄悄建一棵新树、传满 2.6MB，而 nginx 永远不会读它——**这个事故 dry-run 看不出来**
   （dry-run 不碰服务器），只能靠这一步提前拦。目录不存在时会要求输入 `yes` 确认。
3. **先 assets 后 index.html**（见上）。
4. **不带 `--delete`**：旧 assets 故意留着。除了保证切换窗口安全，也是启用
   §5.3「HTML 边缘缓存」的前提——边缘可能还在发几十秒前的旧壳，旧壳引用的旧 hash
   必须还在。要清理用 `--prune=7`（默认只列出、要输入 `yes` 才删）。
5. **发完自检**：带随机串拉一次线上首页，比对 hash 是否换过来了；再把首页引用的每个
   资源 HEAD 一遍，任何一个不是 200 就报错退出。这两步覆盖了「传错目录」和「白屏」。

> **增量上传**：rsync 用 `-rltz`，未改动的文件按「大小 + mtime」跳过。
> 实测同一个产物第二次发版传输 0 个文件，只有真正变化的 chunk 才上传。
> 注意别把 `-t` 去掉——不带它目标端 mtime 每次都是新的，2.6MB 会全量重传。

> **远端没装 rsync 会自动降级 tar**：宝塔用的 CentOS 最小镜像常常不带 rsync，
> 手跑 rsync 会得到 `bash: rsync: command not found` 或 `rsync error: unexpected end of file`。
> 脚本会先探测，没有就改用 `tar | ssh` 流式传输（全量约 2.6MB，两三秒的事），
> 不需要你先上服务器装任何东西。想恢复增量：`yum install -y rsync`（Debian/Ubuntu 用
> `apt install -y rsync`），装一次就够。也可以 `DEPLOY_TRANSFER=tar|rsync` 强制指定。
> 注意 tar 分支解包时带了 `--no-same-owner`：以 root 解包时 GNU tar 默认会照搬包里的
> uid（macOS 是 501），在服务器上会变成一个不存在的属主。

> **别被 tar 的 xattr 警告吓到**：macOS 的 bsdtar 会把文件扩展属性写进 pax 头，
> 远端 GNU tar 解包时每个文件打一行
> `tar: Ignoring unknown extended header keyword 'LIBARCHIVE.xattr.com.apple.provenance'`
> ——70 个文件就是 70 行，把真正的错误淹掉。脚本会自动加 `--no-xattrs`（先探测，
> GNU tar 没这个选项就不加），打包体积也小 3.7KB。**看到这个警告说明脚本版本旧了。**

> **macOS 的 rsync 是 openrsync**（macOS 15+），不支持 `--chmod` / `--chown`。
> 脚本只用两边都支持的最小参数集，权限靠 `umask 022` 保证（目录 755 / 文件 644，nginx 能读）。
> 需要指定属主就 `brew install rsync` 后用 `DEPLOY_CHOWN=www:www`。

> **密码只输一次**：脚本用 ssh 连接复用（`ControlMaster` + `ControlPersist`），
> 探测 / 上传 / 清理 / 自检共用一条连接。彻底免密就 `ssh-copy-id root@<服务器IP>`。

其它可用环境变量见 `./scripts/deploy_frontend.sh -h`（`DEPLOY_PORT` / `DEPLOY_PATH` /
`DEPLOY_URL` / `DIST_DIR`）。没有 SSH 直连的机器可以不用脚本，但**手动传时务必遵守
「先 assets 后 index.html」**。


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

## 4.5 升级后必做：存量图片缩略图回填（只限本地磁盘存储）

缩略图（`<hash>_thumb.<ext>`）**只在新上传时生成**。升级到带 E4 的版本后，存量图
（早期上传的、导入的、`SaveBytes` 写出的小程序码/素材）都没有缩略图，但各端列表页
照常会去请求 `_thumb` → **404 → 回退原图**：不但没省流量，还多打一次失败请求
（实测每个 0.3–0.5s）。所以升级后必须跑一次回填：

```bash
cd backend
go run ./cmd/thumbbackfill -dry-run     # 先只统计，看量（不写盘）
go run ./cmd/thumbbackfill              # 确认后正式回填
```

输出形态：

```text
[DRY-RUN] 只统计，不写盘
回填完成 dir=./uploads 扫描 137 张，生成 121 张，原样存 9 张(原图已够小)，跳过(已有) 0 张，失败 7 张，预计省传输 124.9 KB
```

- `生成`：缩略图比原图小，正常写入；
- `原样存`：原图已 ≤ 480px 或压缩率到顶，重编码反而更大 → 直接存原图字节。
  体积没省，但 `_thumb` **存在**，端上就不会再 404 回退一次；
- `失败`（同时打 `[WARN] 跳过 <path>`）：解码失败/读不了，跳过不中断整轮，需要人工看一眼。

常用开关：

```bash
go run ./cmd/thumbbackfill -dir /www/wwwroot/weichuan/uploads   # 显式指定目录
go run ./cmd/thumbbackfill -force                               # 全部重生成（调整 ThumbMaxEdge 后）
```

**可以随时多次执行**：已存在且 mtime 不早于原图的缩略图会被跳过（幂等），原图只读、
绝不改写或删除。新上传的图自带缩略图，所以日常无需常驻，只在「升级后 / 换规格后」
手动跑一次即可。对象存储（MinIO/OSS/S3）的对象不在本地目录，本工具会直接拒绝
（除非显式 `-dir` 指定某个本地目录）。

## 5. HTTPS（生产必须，小程序合法域名的前提）

compose 自身对外是 http。仓库已内置 Caddy 服务（`profile=https`），自动申请并续期
Let's Encrypt 证书，无需手动操作：

```bash
# 生产形态：在默认 backend+frontend 之外，额外起 Caddy 终结 TLS（80/443 对外）
docker compose --profile https up -d --build
```

Caddyfile 已放在 `deploy/caddy/Caddyfile`，域名写死为 `weichuan.ziyuanxingqiu.cn`（换域名只改这一处）：

```
weichuan.ziyuanxingqiu.cn {
    reverse_proxy frontend:80
}
```

> 首次申请证书需要 80 端口对外可达（ACME http-01 挑战），请确保服务器防火墙放行 80/443。
> 证书与账户数据落在 `caddy_data` 卷，删掉会重新申请（Let's Encrypt 有频率限制，别频繁删）。

同时确认 `.env` 里 `COOKIE_SECURE=true`——否则 httpOnly Cookie 不带 `Secure` 标志，HTTPS 下浏览器虽仍会发送，但丧失了「永不走明文」的保证（A2 加固在生产打折）。本仓库 `.env.example` 已默认 `true`。

> 也可用云厂商 SLB/ALB 或自建 nginx 终结 TLS，效果等价；`COOKIE_SECURE=true` 同样必须。

### 5.1 前面挂了 Cloudflare / CDN：SSL 模式必须选 Full (strict)

浏览器地址栏是 `https://` **不代表**回源那一段也是加密的——那由 CDN 的「SSL/TLS 加密模式」决定，
和源站有没有证书是两件事：

| 模式 | CDN → 源站 | 结论 |
|---|---|---|
| Off | 明文 HTTP | 不可用 |
| **Flexible** | **明文 HTTP** | **最危险**：用户到 CF 是 HTTPS，CF 到源站却是明文，登录口令、Cookie、令牌全在公网裸奔。而且源站若强制跳 HTTPS，会与 CF 形成 301 死循环 |
| Full | HTTPS，但不校验证书 | 加密有了，挡不住中间人（自签证书也放行） |
| **Full (strict)** | **HTTPS + 校验证书** | **生产唯一正确选择** |

**切 Full (strict) 的前提是源站有 HTTPS，且证书是 Cloudflare 认的**——不需要公网 CA，但必须二选一：

1. **Let's Encrypt**（宝塔面板一键申请，默认就是）——CF 信任，但 90 天要续期。
   注意：域名走 CF 代理时，HTTP-01 挑战要经过 CF 回源，若开了「Always Use HTTPS」把 80 强制跳转，
   续期可能失败 → 证书过期 → CF 报 **526**。
2. **Cloudflare Origin CA**（推荐）——CF 面板 → **SSL/TLS → Origin Server → Origin Certificates → Create Certificate**
   （直达：`https://dash.cloudflare.com/?to=/:account/:zone/ssl-tls/origin`）。
   有效期在创建时选，**最长 15 年**；**Free 计划即可用、不额外收费**；只被 Cloudflare 信任。
   宝塔安装：网站 → 设置 → SSL →「其他证书」→ 分别粘贴 PEM 证书与私钥（不要用 Let's Encrypt 那个标签页）。
   不存在续期问题。代价是这份证书不能拿给浏览器直连用（也不需要，用户永远走 CF）。

   > **两个已知限制**（官方文档明说）：
   > - 账号成员**没有 API Access 权限时创建会报错**（`This zone is either not part of your account…`），
   >   要用主账号或有 API Access 权限的成员操作；
   > - Cloudflare **不会**为 Origin CA 证书发送到期提醒，再长的有效期也要自己记一笔。
   >
   > 另外：一旦**暂停 Cloudflare** 或在子域上**关掉代理**，浏览器直连源站会报证书不受信任
   > ——它只加密 CF → 源站这一段。

切换前在服务器上先自检源站 HTTPS 是否真的可用：

```bash
curl -sSI -k https://127.0.0.1/ -H 'Host: <域名>' | head -1
echo | openssl s_client -connect 127.0.0.1:443 -servername <域名> 2>/dev/null \
  | openssl x509 -noout -subject -issuer -dates
```

- `issuer` 是 Let's Encrypt / DigiCert 等公网 CA，或 Cloudflare Origin SSL → 可以切 strict；
- `issuer` 是自签（`subject` 与 `issuer` 相同）→ 只能选 **Full**，否则必报 526；
- `notAfter` 已过期 → 先续期再切。

切换后确认回源确实走了 443（CF 默认就是，改过非标端口的要核对），并且 **CF 到源站这段已经加密**：
此前 Flexible 下源站返回的 301 `Location` 是 `http://...`，切成 Full (strict) 后应变成 `https://...`——
这是判断「回源是否真的上了 TLS」最快的一个观测点。

> 源站防火墙只需放行 Cloudflare 网段访问 443；不要为了省事对全网开放 8080（后端端口），
> 见 `deploy/baota/nginx-site.conf` 顶部的防火墙说明。
>
> **宝塔这条路径的实际配置在 `deploy/baota/nginx-site.live.conf`**——那是 2026-10-29 从
> 服务器取回、逐行核对后的最终版（含 SSL 段、PHP include、伪静态 include 这些宝塔生成的部分），
> 可以直接整体替换。`nginx-site.conf` 是带长注释的通用参考版，别拿它整段覆盖实际配置。

**顺带把 HSTS 打开**：CF 面板 → **SSL/TLS → Edge Certificates → HTTP Strict Transport Security**。
开了之后 CF 会给**所有**响应补 `strict-transport-security`，浏览器在有效期内不会再用
`http://` 访问这个域名（连第一次的明文请求都省了，也就没有降级攻击的窗口）。
前提是「Always Use HTTPS」已经是开的——两者在同一个页面，顺手一起开。

四个开关怎么设（**照抄这一列，别照抄网上的「一键最强」**）：

| 项 | 设成 | 理由 |
|---|---|---|
| 启用 HSTS | **开** | 证书是 CF Universal SSL（`CN=ziyuanxingqiu.cn`，自动续期），Always Use HTTPS 已开 |
| 最长期限 `max-age` | **6 个月**，观察一周无异常再升 **1 年** | 见下方「为什么不一上来就 1 年」 |
| 应用于子域 `includeSubDomains` | **关** | 见下方「zone 级的坑」 |
| 预加载 `preload` | **关** | 单向门：提交后要撤销得等浏览器版本更新（几个月到几年），且要求 `includeSubDomains` 常开 |
| 无嗅探标头 `nosniff` | **关** | nginx 里已经加了（见 `deploy/baota/nginx-site.conf`），CF 再加一遍会出现两个同值头，无害但脏。二选一，保留 nginx 那份 |

> **zone 级的坑**：CF 的 HSTS 是按 **zone** 配的，不是按 hostname。本项目
> `weichuan.ziyuanxingqiu.cn` **不是独立 zone**（`dig NS weichuan.ziyuanxingqiu.cn` 为空，
> 与父域共用一套 Cloudflare NS），所以这个开关作用于 `ziyuanxingqiu.cn` 整个 zone。
> 一旦勾上 `includeSubDomains`，**所有** `*.ziyuanxingqiu.cn` 都会被钉死在 https——
> 任何灰云（DNS only）记录，比如只跑 http 的测试机、面板、邮件服务，会立刻不可访问，
> 而且浏览器不会给"继续访问"的按钮。用不到就别开，要开先把这个 zone 的 DNS 记录全查一遍。

> **为什么不一上来就 1 年**：`max-age` 是浏览器单方面执行的，**服务端没有任何办法提前撤销**——
> 只能改小，且只对**新访问**生效，已经记住旧值的客户端仍按旧值走。所以配错一次，
> 用户可能被锁住整个有效期。先 6 个月跑一周，确认微信内置浏览器、小程序 webview、
> 后台上传取图、`/healthz` 探活都正常，再升到 1 年。

> **真实踩过的坑（2026-10-29）**：开启 HSTS 时，这个 zone 的 apex `ziyuanxingqiu.cn`
> 还是 **526**（CF 不信任该 hostname 的回源证书），而 zone 里另外两个子域
> `ylb` / `km` 也各有自己的站点。**HSTS 是按 zone 生效的，zone 里任何一个对外 hostname
> 没理顺，都会在开了之后被一起钉死。** 所以开之前先把整个 zone 过一遍：
>
> ```bash
> # 把 zone 里所有对外 hostname 都打一遍，任何一个不是 2xx/3xx 就先修
> for h in ziyuanxingqiu.cn weichuan.ziyuanxingqiu.cn ylb.ziyuanxingqiu.cn km.ziyuanxingqiu.cn; do
>   printf '%-32s ' "$h"; curl -s --noproxy '*' -o /dev/null -m 10 -w '%{http_code}\n' "https://$h/"
> done
> ```
>
> 另一个更隐蔽的点：老站点常用**协议相对地址**（`//cdn.xxx.com/x.css`）。页面从 http
> 变成 https 之后，这些 `//` 全部改走 https——第三方 CDN 若不支持 https，整页样式和脚本
> 直接消失，而**错误只在浏览器控制台可见，curl 查不出来**。切 https 前要把页面里所有
> `//` 开头的外部引用逐个试一遍。

```bash
curl -sI https://<域名>/ | grep -i strict-transport-security   # 期望 max-age=15552000 或 31536000
```

> 注意：这个头是 **CF 加的**，不是 nginx 加的，所以查源站配置永远查不到它；
> 反过来，如果 CF 上没开、nginx 也没写，线上就一个都没有——别靠"印象里配过"判断。
> 另外它**只对走 CF 代理（橙云）的流量下发**，但浏览器一旦记住，**以后就算把 CF 关掉
> 改成灰云，仍然会强制 https**——所以源站必须长期保有公网可信证书。

### 5.2 静态资源长缓存：Cloudflare Cache Rule

CF 对 `.js/.css` 这类扩展名会**自动**缓存，但默认 TTL 只有 **4 小时**；源站没发
`Cache-Control` 时它还会自己补一个 `max-age=14400`。后果是每 4 小时，第一个访客
要为每个资源付一次跨境回源。而构建产物文件名带内容 hash，本来可以永久缓存。

**Cloudflare → Caching → Cache Rules → Create rule**（也可用模板「自定义缓存」）：

| 项 | 值 |
|---|---|
| 规则名称 | `静态资源长缓存`（别留模板默认名，排查时要靠它认） |
| 匹配 | 字段 **URI 路径** + 通配符 + `/assets/*` |
| 缓存资格 | 符合缓存条件 |
| 边缘 TTL | **忽略缓存控制标头，使用此 TTL** → 1 年 |
| 浏览器 TTL | **替代源服务器，使用此 TTL** → 1 年 |
| 其余可选设置 | **全部留空**（缓存密钥 / Vary / Origin 范围请求 / 过期内容 / ETag / 错误页） |

表达式预览应为：

```
(http.request.uri.path wildcard r"/assets/*")
```

> ⚠️ **别用「URI 完整」（`http.request.full_uri`）**。该字段含 scheme + 主机名 + 查询串，
> 而 `wildcard` 运算符要求**整个字段值全值匹配**（不是子串匹配），写 `r"/assets/*"`
> 会一条都匹配不到。面板输入框下的示例 `https://*.example.com/files/*` 就是在提示这一点。
> 若确实要用完整 URI，须写成 `*://<域名>/assets/*`——但换域名就得改规则，不如用 URI 路径。

**边缘 TTL 与浏览器 TTL 都要设**，只设一个效果打对折：前者管 CF 节点多久回源一次，
后者管用户浏览器多久来问一次。

**生效验证**（创建后先清一次 `/assets/*` 缓存，否则已缓存的副本仍按旧 TTL 走）：

```bash
curl -sI https://<域名>/assets/<任一hash文件>.js | grep -iE 'cache-control|age|cf-cache-status'
# 期望 cache-control: max-age=31536000；隔几分钟再打，age 应从 0 递增
```

> 发版后文件名全变，即新 URL，**不需要清缓存**。`index.html` 不在规则范围内——
> 但正因如此，**它必须靠 nginx 的 `Cache-Control: no-cache` 兜住**：CF 不缓存 HTML，
> 浏览器却可能按 `Last-Modified` 做启发式缓存（典型为文件年龄的 10%），
> 发版后部分用户会拿到旧壳去加载已被删除的旧资源。

> ⚠️ **验证时要分清 MISS 与 HIT**：设了「边缘/浏览器 TTL = 1 年」之后，CF 会**重写**
> 缓存命中响应的 `Cache-Control`——只留下 `max-age=31536000`，`public` 和 `immutable`
> 都被去掉。所以看到 HIT 上少了 `immutable` 并不代表 nginx 配错了，
> 判断 nginx 是否生效要看**回源那一次**：`curl -sI "...js?cb=$RANDOM"`
> 强制 MISS，期望完整的 `public, max-age=31536000, immutable`。

### 5.2.5 uploads 长缓存：让源站的 immutable 真的透传到浏览器

源站 nginx（见 `deploy/baota/nginx-site.live.conf` 的 `/uploads/` 段）已经给「非 wxacode 的上传」发了
`Cache-Control: public, max-age=31536000, immutable`，给 `^~ /uploads/wxacode/` 发了 `no-cache`
（小程序码是**原地覆盖**重写，不能缓存）。

**但 Cloudflare 的 Browser Cache TTL 默认 4 小时（`max-age=14400`）会覆盖源站的 Cache-Control**——
浏览器实际只缓存 4 小时，4 小时后每个资源的第一个访客都要跨境回源一次。
要让 1 年 + immutable 真的落到浏览器，二选一：

| 方案 | 操作 | 适用 |
|---|---|---|
| **A. 全局 Respect Existing（推荐）** | CF → **Caching → Configuration → Browser Cache TTL** 改成 **Respect Existing Headers** | 想一次解决 `/assets/`、`/uploads/`、HTML 全部尊重源站头 |
| **B. 按路径 Cache Rule** | 新建 Cache Rule 匹配 `/uploads/*`，Edge TTL = Ignore + 1 年、Browser TTL = Respect Existing（或 1 年） | 只想动 uploads，不动全局 TTL |

#### A 推荐：全局 Respect Existing Headers

CF → Caching → Configuration → **Browser Cache TTL** → 选 **Respect Existing Headers**。

为什么安全：本站点所有来源的 `Cache-Control` 都是**显式**的——`/assets/` 是 immutable 1 年、
`/index.html` 是 no-cache、`/uploads/wxacode/` 是 no-cache、`/uploads/` 其余是 immutable 1 年。
改成 Respect Existing 之后 CF 一律原样透传：HTML 仍 no-cache（不发版不缓存）、
wxacode 仍 no-cache（重生成即生效）、其余按 1 年 + immutable 缓存。
**连 §5.2 的 `/assets/` 也一并受益**，不用再单独建 Cache Rule。

> 副作用：之前 §5.2 那条 `/assets/*` 的 Cache Rule 可以保留也可以删——两种 TTL 现在都设成 1 年，
> 结果一致；保留无害，删掉少一处维护点。

#### B 替代：只给 `/uploads/*` 建 Cache Rule（注意 wxacode 例外）

若不想动全局 Browser Cache TTL，可只给 uploads 建 Cache Rule：

| 项 | 值 |
|---|---|
| 规则名称 | `uploads 长缓存` |
| 匹配 | URI 路径 通配符 `/uploads/*`（**不要**写成 `http.request.full_uri` 的 `https://.../uploads/*`，否则换域名要改） |
| 边缘 TTL | 忽略缓存控制标头，使用此 TTL → 1 年 |
| 浏览器 TTL | **Respect Existing Headers**（让源站的 immutable / no-cache 各自生效） |
| 其余 | 留空 |

⚠️ **wxacode 例外**：上面的通配 `/uploads/*` 同时命中 `/uploads/wxacode/<staffID>.jpg`，
而 wxacode 在源站是 `no-cache`（小程序码原地覆盖，必须每次回源）。如果这里把 Browser TTL 设成
「1 年」而非「Respect Existing」，wxacode 会被强行缓存一年——重生成小程序码后用户拿到旧码。两种规避：

- **浏览器 TTL 选「Respect Existing Headers」**（推荐）：源站 no-cache 透传，wxacode 不缓存，其余 immutable 1 年；
- 或再加一条**更高优先级**的 Cache Rule：`/uploads/wxacode/*` → Browser TTL = Respect Existing / no-cache，排在 uploads 那条之前。

表达式预览：
```
(http.request.uri.path wildcard r"/uploads/*")
```

#### 验证

改完后先清一次 `/uploads/*` 缓存（已缓存副本仍按旧 TTL 走）。然后强制 MISS 看源站头：

```bash
# 强制回源（?cb 让 CF 不命中），期望完整 immutable
curl -sI "https://<域名>/uploads/<任一随机名文件>?cb=$RANDOM" \
  | grep -iE 'cache-control|cf-cache-status'
# 期望：cache-control: public, max-age=31536000, immutable；cf-cache-status: MISS

# wxacode 必须 no-cache（重生成即生效）
curl -sI "https://<域名>/uploads/wxacode/<staffID>.jpg?cb=$RANDOM" \
  | grep -i cache-control
# 期望：cache-control: no-cache
```

> 与 §5.2 同理：设了长 TTL 之后，CF 命中（HIT）响应会把 `Cache-Control` 重写为只剩
> `max-age=31536000`，`public` 和 `immutable` 被去掉——这是 CF 行为，**不代表 nginx 配错**；
> 判断 nginx 是否生效要看上面那个**强制 MISS** 的回源响应。

### 5.3 首屏 TTFB：HTML 不缓存 = 每次全页加载都跨境回源

实测（Cloudflare 境外节点 → 国内源站，5 次）：

```
/          TTFB 0.96s / 1.05s / 1.15s / 1.19s / 1.63s   ← 中位约 1.1s
index.html 717 字节
```

717 字节要 1.1 秒，说明时间全花在**跨境回源**上，跟体积无关。`cf-cache-status: DYNAMIC`
每次都出现——CF 默认不缓存 HTML，所以每一次「首次进入 / F5 / 从微信深链打开」都要
等一个完整的中国—海外往返。

**影响面要说清**：SPA 内部跳转不重新加载 HTML，所以这只影响首屏那一次。登录后
在后台点来点去不会重复付这个成本。真正被拖慢的是「点开链接的第一下」。

想压下去只有一条路——让 CF 也缓存 HTML：

| 项 | 值 |
|---|---|
| 规则名称 | `HTML 边缘短缓存` |
| 匹配 | 字段 **URI 路径** + 等于 + `/` ；再加一条 **或** 等于 `/index.html` |
| 缓存资格 | 符合缓存条件 |
| 边缘 TTL | 忽略缓存控制标头，使用此 TTL → **60 秒**（不要设长） |
| 浏览器 TTL | **留空**（源站已发 `no-cache`，浏览器侧必须保持每次校验） |

> ⚠️ **前置条件：发版上传 `dist` 时不要清空 `assets/` 目录。** 边缘可能还在发
> 60 秒内的旧壳，旧壳引用的是旧 hash 文件名——文件被删掉，用户看到的就是白屏。
> Vite 产物文件名带内容 hash，新旧共存不会冲突，占几百 KB 而已。
> 做不到这点就别开这条规则：宁可每次多等 1 秒，也不要白屏。

**代价**：发版后最长 60 秒内，新访客可能拿到旧壳。因为旧资源还在，旧壳能正常工作，
只是看到的不是最新版——对 SPA 来说这是常规取舍。要「发版即时生效」，就保持现状。

验证：`curl -sI https://<域名>/ | grep -iE 'cf-cache-status|age'`，第二次起应为
`HIT` 且 `age` 递增；TTFB 应从 ~1.1s 降到 0.2–0.3s。

### 5.4 真实客户端 IP：`real_ip` + `TrustedProxies` 是一条链，缺一环就整条断

访客 IP 要传到业务代码，中间有四跳，**任何一跳断掉，`c.ClientIP()` 拿到的是边缘节点 IP**：

```
浏览器 → Cloudflare → nginx（源站） → Go 后端
         CF-Connecting-IP    real_ip 改写     TrustedProxies 采信
```

| 跳 | 配置位置 | 不配的后果 |
|---|---|---|
| ① CF 注入 `CF-Connecting-IP` | CF 默认行为，无需配置 | — |
| ② nginx 采信它 | `set_real_ip_from <CF 网段>` + `real_ip_header CF-Connecting-IP` | `$remote_addr` 是 CF 边缘 IP（几十个地址之一） |
| ③ nginx 覆盖着传给后端 | `proxy_set_header X-Forwarded-For $remote_addr;` | **追加**（`$proxy_add_x_forwarded_for`）会让客户端自带的伪造 XFF 留在左侧 |
| ④ 后端只信 ③ 的来源 | `TRUSTED_PROXIES`（缺省回环 + RFC1918） | gin 缺省信任一切，直接取 XFF **最左**一项 = 完全由客户端决定 |

②的网段写在 `deploy/baota/nginx-site.live.conf` 的 `=== 真实客户端 IP ===` 段里（22 段，
IPv4 15 + IPv6 7），网段会变，定期核对 <https://www.cloudflare.com/ips-v4>。

**为什么 ③ 必须是「覆盖」而不是「追加」**：`$proxy_add_x_forwarded_for` 的语义是
`$http_x_forwarded_for + ", " + $remote_addr`。客户端随手带一个
`X-Forwarded-For: 1.2.3.4`，后端按「从右往左跳过可信代理、取第一个不可信」的规则算出来
的仍然是 1.2.3.4——伪造成功，**登录限流按伪造 IP 分桶，等于可以无限爆破**。

#### 怎么验证 ②③④ 都生效

在**服务器上**执行（本地回环请求，不经过 CF）：

```bash
# ②：确认 nginx 编译进了 realip 模块（宝塔默认有）
nginx -V 2>&1 | tr ' ' '\n' | grep realip

# ②：确认实际生效的配置里有网段（-T 打印 include 展开后的完整配置）
nginx -T 2>/dev/null | grep -c set_real_ip_from     # 期望 22
nginx -T 2>/dev/null | grep real_ip_header          # 期望 CF-Connecting-IP

# ③：抓一次真实请求，看后端收到的 XFF 有几个值
tail -f /www/wwwlogs/weichuan.ziyuanxingqiu.cn.log   # 第一列就是改写后的 $remote_addr
```

> ⚠️ **不能从外部用「伪造 `CF-Connecting-IP` 直连源站」来验证 ②**——`real_ip` 只采信
> 来自 CF 网段的来源，你直连时源 IP 不在网段里，nginx 会（正确地）忽略你伪造的头。
> 看到「伪造无效」说明防伪造生效了，**不代表 `real_ip` 没配**。这两件事必须分开确认。
>
> 判别 ② 是否配置，最可靠的是**读配置**（上面的 `nginx -T`），其次是看访问日志第一列
> 是否出现真实访客 IP（而不是 `172.71.x.x` / `162.158.x.x` 这类 CF 段地址）。

#### 后端侧：不需要改代码，但要知道缺省值

`backend/internal/config/config.go` 的 `DefaultTrustedProxies` 是「回环 + 三类私有网段」，
同机 nginx（`127.0.0.1`）落在里面，所以**默认就能正确工作**。三种情形要显式覆盖：

| 场景 | `TRUSTED_PROXIES` |
|---|---|
| nginx 与后端同机（本仓库默认） | 不设，用缺省值 |
| 后端在另一台内网机器 / 云 LB 后面 | 加上那台的 IP 或 CIDR，如 `10.0.0.5/32` |
| 后端直接对公网（前面没有反代） | `none` —— 完全不读转发头 |

设错的两种表现都要能认出来：

- **收得比实际松**（含 `0.0.0.0/0`）：启动日志出现 `You trusted all proxies, this is NOT safe`，
  限流可被伪造头绕过；
- **收得比实际紧**（例如 `none` 但前面确有 nginx）：全站访客被算成同一个 IP、共用一个桶，
  **一个人就能把整站登录限流打满**。表现是「我没试几次就提示登录过于频繁」。

## 5.5 全站不可索引：后台不能被搜索引擎收录

管理后台（`/login`、`/employees`、`/apply/*`、平台管理…）**没有一个页面应该进索引**：
它们全部需要登录，被收录了就是死链；更糟的是搜索结果里会暴露后台的路径结构
（`/api/admin/companies` 这类名字直接告诉别人哪里有接口）。

三道防线，**三道都要**，因为各自的失效场景不同：

| 防线 | 位置 | 挡住什么 | 失效场景 |
|---|---|---|---|
| ① `robots.txt` | `frontend/public/robots.txt` → 构建进 `dist/robots.txt` | 守规矩的爬虫根本不进来 | 外链、聊天软件预览、恶意爬虫**不看** robots.txt |
| ② `X-Robots-Tag` 响应头 | `nginx-site.live.conf`（server 级 + 每个带 `add_header` 的 location） | 已经抓到页面的爬虫不索引 | 某个 location 有 `add_header` 却漏了这一行 → 该路径整组失效 |
| ③ `<meta name="robots">` | `frontend/index.html` | 上面两层都失效时的兜底 | 只对 HTML 生效，且需要爬虫执行到 `<head>` |

### 为什么 robots.txt 不能只写在 nginx 里

写成 `location = /robots.txt { return 200 "Disallow: /"; }` 也能用，但**内容就脱离了版本控制**，
下次换服务器/换人接手就丢了。放在 `frontend/public/` 里跟着构建产物走，发版即生效，无需碰服务器。

要注意的坑：**`robots.txt` 不存在时不会 404，而是被 SPA 兜底成 `index.html`**（`200 text/html`）。
所以「访问 robots.txt 有内容」不代表它是对的，必须看 `content_type`：

```bash
curl -s https://<域名>/robots.txt -o /dev/null -w '%{content_type}\n'   # 必须 text/plain
curl -s https://<域名>/robots.txt | grep -c 'Disallow: /'                # 必须 ≥ 1
```

### `X-Robots-Tag` 为什么要在 5 个地方写

又是 `add_header` 的整组继承：`/assets/`、`/index.html`、`/uploads/`、`/uploads/wxacode/`
这四个 location 各自有 `add_header`（缓存头），server 级那组对它们**完全失效**。
漏一个的表现很隐蔽 —— 页面照样打不开索引？不，只有那一条路径能被索引，而它恰好是
`/index.html`，也就是**所有 SPA 深链**（`/console/companies` 等）的响应。

不变量是「任何响应都带这 4 个头」，`~/nginx-check/verify.sh` 的第 10 节逐路径断言它。
反向验证做过：删掉 `location = /index.html` 里的那一行，`/`、`/index.html`、
`/console/companies` **三条同时变红** —— 这也顺带证明了 `try_files ... /index.html`
是内部重定向，会重新走 location 匹配、继承 `= /index.html` 那组的头。

### 已经被收录了怎么办

`Disallow: /` 只能阻止**再次抓取**，不会把已有条目从索引里删掉（Google 拿不到页面内容，
会保留一条「无摘要」的旧记录）。真发生了要走 Google Search Console 的
「移除网址」工具，或用 `410` 响应明确告知已删除。上线前就配好这三道防线，就不用处理这些。

### 以后要开官网 / 公开名片页时

必须**同时**做三件事，少一件新站就永远进不了搜索：

1. `frontend/public/robots.txt` 里把那个路径从 `Disallow: /` 放出来（改成只 disallow 后台路径）；
2. 给公开页单独一个 server 块或 location，**不要**继承 `X-Robots-Tag`（或在那个 location 里
   显式 `add_header X-Robots-Tag "all"`）；
3. `index.html` 的 `<meta name="robots">` 必须去掉 —— 现在它是全站写死的 `noindex`，
   **同一个壳服务两个用途时这条最先出问题**。公开页建议独立入口（独立 `index.html`）。

## 5.6 源站仅允许 Cloudflare 回源（堵住「直连源站绕过 WAF」）

**痛点**：源站 443 对全网开放，任何人 `curl -k https://<源站IP>/ -H 'Host: <域名>'` 都能拿 200，
Cloudflare 的 WAF / 限流 / Bot 管理 / 缓存全部被绕过，等于没上 CDN。

### ⚠️ 不能用 nginx 的 `allow/deny`

本站开了 `real_ip`（`set_real_ip_from` + `real_ip_header CF-Connecting-IP`），它会把
`$remote_addr` **改写成真实访客 IP**。而 nginx 的 `allow/deny` 匹配的就是 `$remote_addr` ——
于是「来自 CF 的请求」被改写成了访客 IP，反而落进 `deny` 被拒。开了 real_ip 再用
`allow <CF网段>; deny all;` 会把**正常走 CF 的流量也挡掉**。

### 正确做法：`geo` + `$realip_remote_addr`

`$realip_remote_addr` 是 TCP 对端**原始** IP（CF 边缘，不受 real_ip 改写影响）。用它做判定：

```nginx
# http 级（server 块外面；本文件被 Baota 在 http{} 内 include，写在 server 之前即可）
geo $realip_remote_addr $is_cf {
    default          0;
    127.0.0.1        1;          # 本机自检
    ::1              1;
    173.245.48.0/20  1;          # 以下同 real_ip 段的 22 个 CF 网段
    ...
}

server {
    # 放在 80→443 跳转之前：非 CF 的 HTTP 直连也直接拒
    if ($is_cf = 0) { return 403; }
    ...
}
```

- 走 CF：对端是 CF 边缘 IP → `$is_cf=1` → 放行；
- 直连源站：对端是攻击者真实 IP（不在 CF 网段）→ `$is_cf=0` → 403。
- CF 网段会变，与 `real_ip` 段保持一致，定期核对 `https://www.cloudflare.com/ips-v4|v6`。
- 回滚：把 `if ($is_cf = 0)` 那 3 行注释掉，`nginx -s reload`，不会把自己锁在外面。

### 验证（直连源站，绕开 CF）

```bash
# 你的机器不在 CF 网段，期望 403
curl -sk --resolve weichuan.ziyuanxingqiu.cn:443:<源站IP> \
  https://weichuan.ziyuanxingqiu.cn/ -o /dev/null -w '%{http_code}'   # 期望 403
# 走 CF 正常访问应仍是 200
curl -s  https://weichuan.ziyuanxingqiu.cn/ -o /dev/null -w '%{http_code}'   # 期望 200
```

### 更彻底：在防火墙上做（可选）

nginx 层拦截时端口仍「开着」（TLS 握手能完成，只是被 403）。要连握手都不给，用 firewalld
ipset 只允许 CF 网段到 443（流量根本到不了 nginx）。注意 firewalld 的 drop/reject 规则
**优先级高于 accept**，不能「先全放行 443 再加一条拒绝」，要用 ipset + rich-rule 整体替换。
回滚：`firewall-cmd --permanent --add-port=443/tcp && firewall-cmd --reload`。

## 5.7 SPA 兜底 404（未匹配路径不再误返 200）

**问题**：原来 `location / { try_files $uri $uri/ /index.html; }` 把所有未匹配路径都兜底成 `index.html` + 200，
导致 ① 扫描器扫 `/auth.json`、`.credentials`、`backup.env` 等敏感路径时看到 200（误报「找到文件」）
② 缺失的 `/assets/*.js` 返回 HTML 触发 MIME 错 ③ 日志被污染。

**修复**（`deploy/baota/nginx-site.live.conf` 的 SPA 段，已脱离宝塔「伪静态」面板 include）：
```nginx
location / {
    try_files $uri $uri/ @spa;
}
location @spa {
    if ($http_accept ~* "text/html") { rewrite ^ /index.html last; }  # 浏览器导航 → 回落壳
    return 404;                                                        # 其余 → 真 404
}
```
效果：浏览器导航深链（`/login`、`/console/companies`，`Accept: text/html`）仍是 200 + SPA 壳；
非 HTML 请求（`/auth.json`、`.env`、扫描器、缺失静态资源）一律 404。深链刷新不受影响。

### 验证（必须用服务器 localhost，别从本机直连公网 IP）

⚠️ **不能在你的笔记本上 `curl 公网IP/路径` 验证**：本站开了 geo（`if ($is_cf=0) return 403`），
你本机不是 CF 网段，会在 HTTP 最前面被 403 挡掉，**根本走不到 SPA 逻辑**，只能证明 geo 生效。
正确做法：SSH 进服务器，用 `127.0.0.1`（已在 geo 白名单 → 不被挡）测：

```bash
# 非 HTML 请求 → 期望 404（旧版会返 200 壳子，这是修复的标志）
curl -sk https://127.0.0.1/auth.json -H 'Host: weichuan.ziyuanxingqiu.cn' -o /dev/null -w '%{http_code}'   # 期望 404

# 浏览器导航深链（Accept: text/html）→ 期望 200 + SPA 壳
curl -sk -H 'Accept: text/html' https://127.0.0.1/console/companies -H 'Host: weichuan.ziyuanxingqiu.cn' \
  -o /dev/null -w '%{http_code}'   # 期望 200
```
两个结果符合预期即修复生效。本地桩装置（`~/nginx-check/` 的 `gen_site.py`+`verify.sh`）另有 75 项行为断言覆盖此修复。

⚠️ 部署时顺手把宝塔「伪静态」设为「无」：本配置已删掉面板伪静态的 `include`，若面板伪静态仍是
Vue/React 模板，日后在面板改其它设置（如 SSL）时可能自动重新插入 `location /` → 与 `@spa` 段冲突 →
nginx 启动失败。设为「无」即可永久避免。

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
  HEALTH_URL=http://127.0.0.1:8080/healthz/ready \
  EXTERNAL_URL=https://你的域名/healthz/ready \
  LOG_FILE="$(docker inspect -f '{{.LogPath}}' weichuan-backend)" \
  BACKUP_DIR=/path/to/repo/backups \
  scripts/alert_check.sh >> /var/log/weichuan-alert.log 2>&1
```

`EXTERNAL_URL` 是**外部拨测**（R4）：从对外域名访问 `/healthz/ready`，覆盖本地探活看不到的
那一半故障——域名没解析、443 没放行、证书过期、反代 upstream 挂了。同时校验响应耗时
（`EXTERNAL_MAX_MS`，默认 2000ms）与证书剩余有效期（`CERT_MIN_DAYS`，默认 14 天）。
不填该项则 R4 跳过（已用 UptimeRobot 之类外部拨测服务的部署可以不填，避免重复）。

> 外部拨测只从「本机之外」发起，仍可能漏掉「机房出口正常、某运营商不通」这类区域性故障。
> 真要盯全国可用性，还是需要多地拨测服务；本脚本的定位是「没有监控基建也能跑起来的最小闭环」。

## 8. 小程序侧切换（提审前）

1. 接口基址：本仓库 `miniprogram/utils/config.ts` 已预填生产种子 `https://weichuan.ziyuanxingqiu.cn`（首次冷启动即可连后端）；正式基址仍由后台「系统设置 → 小程序 → 小程序接口地址」（`miniprogram_base_url`）下发覆盖，二者一致即可。若换域名，改这一处（或后台下发）并重新打包小程序；改完清一次小程序缓存再验证。
2. 微信公众平台 → 开发管理 → 开发设置 → 服务器域名：
   - `request` 合法域名：`https://weichuan.ziyuanxingqiu.cn`
   - `uploadFile` 合法域名：`https://weichuan.ziyuanxingqiu.cn`（头像上传）
   - `downloadFile` 合法域名：同上（如用到）
   - **域名必须已备案；每月修改次数有限，一次填对。**
3. **开发管理 → 用户隐私保护指引**（微信审核必查项，漏了会被打回）：代码侧调用的隐私接口必须逐个声明对应类型——

   | 代码里的调用 | 需声明的隐私类型 |
   |---|---|
   | `wx.chooseMedia`（头像 / 海报背景选图） | 收集你选中的照片或视频信息 |
   | `<input type="nickname">`（微信昵称，2026-10-27 起取代 `wx.getUserProfile`） | 收集你的昵称、头像 |
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
4. 校验：SQLite 用 `server dbcheck` 验 `integrity_check` 与表数，并等 `/healthz/ready` 回 2xx；
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
| 安装向导「测试连接」报 403（界面只显示 `Request failed with status code 403`） | **反代没透传 Host**，与数据库账号密码无关。nginx 的 `proxy_set_header` 默认值是 `$proxy_host`，后端收到的 Host 变成 `127.0.0.1:8080`，跟浏览器 Origin 里的域名对不上，被安装接口的同源检查（挡 CSRF）拒掉。修法：反代 `/api` 的 location 加 `proxy_set_header Host $host;`，然后 `nginx -s reload`；前面还有 CDN / 负载均衡时再加 `proxy_set_header X-Forwarded-Host $host;`。新后端的 403 响应体会直接写明「浏览器来源 X / 后端收到的 Host Y」和这句改法，照着改就行 |
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
