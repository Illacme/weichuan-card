# 微传名片（三端工程）

企业电子名片小程序：员工扫码/分享进入名片页（含公司信息、经典业绩、业务服务、视频号、埋点），
员工可自助申请开通与修改名片，超管在 PC 后台统一管理内容、审核与员工。

本仓库为 **monorepo**，包含后端、PC 管理后台、微信小程序三端与中文映射文档。

## 目录结构

| 目录 | 说明 |
|---|---|
| `backend/` | Go + Gin + GORM 后端服务（API / 鉴权 / 存储 / 小程序码 / 审核流） |
| `frontend/` | Vue 3 + Element Plus 的 PC 管理后台（超管使用） |
| `miniprogram/` | 原生微信小程序前端（员工 / 访客侧） |
| `backend/migrations/` | MySQL 建表与种子 SQL（含软删除迁移 `0003` 等） |
| `docs/` | OpenAPI 3 接口规范（`openapi.yaml`）· 分支保护协作规范（`branch-protection.md`）· 生产上线 Runbook（`deploy-runbook.md`） |
| `scripts/` | 运维脚本（`preflight.sh`：上线前配置自检；`backup.sh`：MySQL 逻辑备份 / SQLite 在线快照 + 轮转；`restore.sh`：恢复；`restore_drill.sh`：恢复演练；`alert_check.sh`：最小告警四规则，含外部拨测） |
| `*.md`（根目录） | 需求 / 架构 / 接口字段映射等中文文档 |

## 技术栈

- **后端**：Go 1.26 · Gin · GORM · MySQL（生产）/ SQLite（默认测试）· MinIO / 阿里云 OSS（可选对象存储）
- **前端**：Vue 3.5 · TypeScript · Vite 5 · Element Plus 2 · Pinia · Vue Router 4 · Vitest
- **小程序**：微信小程序原生 + TypeScript

## 快速开始

### 方式一：免 MySQL 本地联调（推荐）
后端用 SQLite 起一个自带演示数据的开发态服务，小程序 / 前端可直接联调：
```bash
cd backend && go run ./cmd/devserver      # 监听 :8080
```
演示账号：超管 `admin / admin123456`；员工手机 `13800000001` / 密码 `demo1234`（staffId=1）。
初始超管由后端按 `ADMIN_USER` / `ADMIN_PASS` 首次启动时 seed（表为空才建）；之后可在
「平台管理 → 账号管理」改用户名或新增平台超管，不必再改环境变量或改库。
> 注意：`go run ./cmd/devserver` 每次启动都用**新的临时 SQLite 库**，改过的密码/新建的账号
> 重启即回到初始演示状态（开发态有意设计）。需要持久化请用 `cmd/server`（MySQL）或 docker-compose。

### 方式二：容器一键起（两种数据库形态任选）
```bash
cp .env.example .env

# 形态一（默认）：零依赖起步——只起 backend + frontend，浏览器开 http://localhost:8080
# 进安装向导选 SQLite 即可，JWT_KEY / ADMIN_PASS / MYSQL_ROOT_PASSWORD 全部不用给
docker compose up -d --build

# 形态二：生产 MySQL——加 --profile mysql 额外起 mysql 容器（MYSQL_ROOT_PASSWORD 必填；
# --profile 是顶层标志，必须放在 up 之前）
docker compose --profile mysql up -d --build
```
> `docker-compose.yml` 里只有 `MYSQL_ROOT_PASSWORD` 是**必填**变量，且只在
> `--profile mysql` 形态校验：未设置则 mysql 容器启动即报错退出（日志有明确提示），
> 不会带默认值悄悄启动；默认 SQLite 形态连 mysql 都不起，完全不受影响；
> `JWT_KEY` / `ADMIN_PASS` 刻意放宽为**可选**——留空即进入安装向导，由向导现场生成强随机值
> 并写进 `/app/data/runtime.env`（详见 `docs/deploy-runbook.md` §2.1）。
> 后端 `cmd/server` 也会拒绝用缺省密钥 / 缺省口令启动（`InsecureGates`）。
> 上线前可跑 `scripts/preflight.sh` 做一次静态自检（含小程序端引导基址是否仍指向 127.0.0.1）。
> **国内网络**：构建需拉 `golang/node/nginx/mysql` 基础镜像，Docker Hub 直连超时的话先按
> `部署与联调.md`「国内网络先看这里」配 registry 镜像加速（colima 改 `~/.colima/default/colima.yaml`
> 的 `docker.registry-mirrors` 后 `colima restart`；Docker Desktop 在 Settings → Docker Engine 加
> `registry-mirrors`），`scripts/verify_compose_dual_mode.sh --diag` 可自动探测这一点。

### 方式三：分别启动（开发态）
```bash
# 后端（需自备 MySQL，配置 DB_DSN / JWT_KEY 等环境变量）
cd backend && go run ./cmd/server

# 前端
cd frontend && npm install && npm run dev   # http://localhost:5173

# 小程序：微信开发者工具导入 miniprogram/ 目录
```

## 系统设置（运行参数后台化）

产品运行参数不再写死在代码里，平台超管可在 PC 后台「平台管理 → 系统设置」界面维护。
### 取值优先级

**界面设置 > 环境变量 > 配置文件 > 内置缺省值**，对所有配置项一致：

- 界面填了值并保存 → 以界面为准；
- 界面留空 → 只有**登记了环境变量名**的项（界面上会显示「部署环境配置名：XXX」，
  如 `storage_*`、`cdn_*`、`wx_*`、`metrics_enabled`）才继续往下找：先环境变量、
  再配置文件（默认 `data/runtime.env`）、最后内置缺省值；
  未登记环境变量名的项（如上传上限、令牌有效期、限流）直接用内置缺省值，
  **环境变量不参与**；
- 只读项不落库、界面不能改，只可能来自环境变量或配置文件。

把某一项清空保存，就等于把它交还给环境变量 / 配置文件——这也是「改坏了能退回」的保证。

配置分**三档生效方式**（界面上用标签标明，保存时接口也会点名提示）：

| 档位 | 含义 | 配置项 |
| --- | --- | --- |
| 即时生效 | 保存即写入内存缓存，无需重启 | 下表「常规」全部 |
| 需重启 | 客户端在启动时构造，重启后才生效 | `storage_*`、`cdn_*`、`upload_dir`、`upload_base_url` |
| 只读 | 界面不能改，展示**可辨认预览**（连接串遮口令、密钥遮中段）并提示改法；改它要动环境变量**或配置文件**后重启 | `db_dsn`、`jwt_key`、`cookie_secure`、`cookie_domain` |

| 分组 | 配置项 | 说明 |
| --- | --- | --- |
| 通用 | `brand_name` | 后台侧边栏 / 登录页 / 页标题品牌名 |
| 小程序 | `miniprogram_base_url` | 小程序端接口基址（须 `http(s)://` 开头；冷启动拉 `/api/public/settings` 覆盖） |
| 小程序 | `pinned_company_id` | 企业独立版锁定租户（>0 锁定且该企业须存在并启用，0 = 平台模式） |
| 上传 | `upload_max_mb` | 单文件上传上限（MB，1~1024） |
| 上传 | `upload_allow_ext` | 允许上传的扩展名（逗号分隔） |
| 上传 | `presign_put_ttl_minutes` / `sign_get_ttl_minutes` | 直传 PUT / 私有桶 GET 签名链接有效期（分钟；填 0 或负数回落默认值 15 / 30） |
| 微信 | `wxacode_ttl_hours` | 小程序码缓存有效期（小时；未设置或填 0 回部署默认值 720） |
| 微信 | `wxacode_sign_ttl_minutes` | 私有桶下小程序码签名链接有效期（分钟；填 0 或负数回落默认值 60） |
| 微信 | `wx_appid` | 小程序 AppID（留空回退 `WX_APPID`，改完即时生效） |
| 微信 | `wx_appsecret` | 小程序 AppSecret（敏感；留空回退 `WX_APPSECRET`，改完即时生效） |
| 微信 | `wx_msg_token` | 微信「消息推送」Token（推送地址验签用；留空回退 `WX_MSG_TOKEN`，改完即时生效）。图片内容安全靠它把检测结论推回来，未配则图片不送检 |
| 微信 | `wx_msg_aeskey` | 微信「消息推送」EncodingAESKey（仅安全模式需要；留空回退 `WX_MSG_AESKEY`，改完即时生效） |

> 上面两个是**平台全局**小程序凭据。企业独立版（每企业一个自己的小程序、共用同一套后端）另有一份
> 按租户隔离的凭据，由平台超管在「企业管理 → 凭据」代填，优先级高于全局这份；该企业没配时回落全局。
| 登录与令牌 | `access_token_ttl_minutes` / `refresh_token_ttl_days` | 令牌有效期；填 0 或负数回落默认值 15 分钟 / 30 天 |
| 登录与令牌 | `login_rate_limit_per_min` / `refresh_rate_limit_per_min` | 每 IP 每分钟登录 / 刷新次数（防爆破；填 0 或负数回落默认值 10 / 60） |
| 文件存储与 CDN | `storage_driver` | `local` / `minio` / `oss` / `s3`（只能取这四个值；留空回退 `STORAGE_DRIVER`，**需重启**） |
| 文件存储与 CDN | `storage_endpoint` / `storage_bucket` | 对象存储端点与桶名（MinIO / OSS / S3 兼容云共用这两个字段） |
| 文件存储与 CDN | `storage_access_key` / `storage_secret_key` | 对象存储凭据（后者敏感，界面只回传掩码） |
| 文件存储与 CDN | `storage_use_ssl` / `storage_region` | MinIO / S3 兼容存储走 HTTPS / 区域（如 `ap-guangzhou`；留空自动探测，阿里云 OSS 用不到） |
| 文件存储与 CDN | `storage_public` | 是否公开桶（私有桶走限时签名） |
| 文件存储与 CDN | `upload_dir` / `upload_base_url` | 本地存储目录 / 上传文件公开访问基址 |
| 文件存储与 CDN | `cdn_enabled` / `cdn_access_key_id` / `cdn_access_key_secret` / `cdn_endpoint` | CDN 缓存刷新（覆盖写后使边缘缓存失效） |
| 安全与基础设施 | `db_dsn` / `jwt_key` / `cookie_secure` / `cookie_domain` | **只读**，仅掩码展示当前值，修改走环境变量**或配置文件**（默认 `data/runtime.env`）后重启 |
| 功能开关 | `metrics_enabled` | 是否开放 `/metrics`（Prometheus 指标）；留空则跟随 `METRICS_ENABLED` |
| 功能开关 | `content_sec_enabled` | UGC 内容安全检测（默认开）：员工提交名片修改、访客提交开通申请时，把姓名 / 昵称 / 职位等文字送微信内容安全接口，命中违规直接拒绝提交。未配小程序凭据、或提交人没有 openid 时自动跳过（fail-open，只记 `[CONTENT-SEC]` 日志），不影响正常提交 |
| 功能开关 | `independent_lock` | 独立版企业锁定：开启后企业管理页只读（不可新建/编辑/启停企业、不可加企业管理员） |
| 后台上传小程序代码 | `mp_node_bin` / `mp_ci_module_dir` / `mp_source_dir` | Node 可执行文件 / miniprogram-ci 安装目录 / 小程序源码目录——三项都是**服务器**绝对路径（后端要拿它们去 exec / 找依赖 / 找源码）。界面文本框旁有「浏览…」按钮，弹服务端目录浏览器在服务器文件系统里挑路径，不用手敲；留空则回落部署环境的值（环境变量 / 配置文件）。详见下方「路径选择器」 |
| 后台上传小程序代码 | `mp_upload_timeout_sec` | 单次上传超时（秒，默认 300；编译 + 传代码都算在内。改完下次上传生效，不用重启。反代 `proxy_read_timeout` 要设得比它大，否则传到一半先返回 504） |

> **为什么分三档？** 判据是「改错能否自愈」，不是「是否敏感」：
> - 云存储 / CDN 凭据填错最坏是上传失败，**改回来重启即可**，所以允许在界面维护；
> - `DB_DSN` / `JWT_KEY` 改错会把「改回来的入口」一起弄丢（连不上库 → 读不出配置；
>   会话全失效 → 登不进后台），所以只展示不编辑；
> - 敏感项（密钥 / 连接串）接口**只回传掩码**，真实值永不出现在响应里；界面输入框留空 = 不修改。
> - 令牌有效期与签名时长这类「设成 0 就等于把功能关死、甚至把自己锁在外面」的项，
>   代码内保留**安全下限**：界面填 0 / 负数一律回落缺省值。
> - **类型合法 ≠ 取值合法**：对「填错会造成大面积故障」的项，保存入口还会做值域校验并当场拒绝——
>   `upload_max_mb`（1~1024）、`miniprogram_base_url` / `upload_base_url`（必须 `http(s)://` 开头）、
>   `storage_driver`（只能 `local` / `minio` / `oss` / `s3`）、`pinned_company_id`（必须是存在且启用的企业，0 = 平台模式）。
>   例如上传上限填 0 会让所有上传失败、报错却指向文件；基址漏写协议头会让小程序永远连不上，
>   且「后台下发正确基址」只在种子地址能连通一次之后才生效，等于自愈不了。
>
> 另：云存储驱动初始化失败时会**降级为本地磁盘**而不是让进程退出——否则管理员
> 连后台都进不去，就再也改不回来（见 `cmd/server` 的 `newUploaderWithFallback`）。

接口：`GET/PUT /api/admin/settings`（平台超管；PUT 回包含 `data.restartKeys` 点名需重启的项）、
`GET /api/public/settings`（公开，仅品牌名 / 小程序基址 / 锁定租户）、
`GET /api/admin/settings/paths?kind=&dir=`（平台超管；服务端路径选择器，供 `mp_node_bin` / `mp_ci_module_dir` / `mp_source_dir` 三项在服务器上挑路径）。

### 路径选择器（后台上传小程序代码的路径配置）

`mp_node_bin` / `mp_ci_module_dir` / `mp_source_dir` 三项指向**服务器**文件系统（后端要 `exec node`
跑 miniprogram-ci、到那个目录找 `node_modules/miniprogram-ci`、读那个目录的 `project.config.json`），
浏览器原生文件框选的是**客户端**机器，毫无意义。所以界面在文本框旁给了「浏览…」按钮，弹出的选择器
由后端在**服务器**上列举目录：

- **白名单目录**才能浏览（`/opt` `/srv` `/usr/local` `/home` `/data` …，外加**服务进程的家目录**）；用白名单而非黑名单，避免漏掉敏感目录。
  - 把家目录纳入，是因为 macOS 开发机与单用户部署的项目常放在家目录下；这不新增越权能力——管理员本来就能把 `MP_*` 配成任意路径、后端据此 `exec` / 读文件。
  - 白名单只接受**绝对路径**：相对路径（`.` / `node`）会随进程 cwd 漂移，混进来等于把 cwd 悄悄开放出去。
- 解析符号链接后再判白名单；列举时遇到软链条目直接跳过（不展示也不跟随）。
- 敏感点目录（`.ssh` `.aws` `.gnupg` `.docker` `.kube` `.config`）连名字都不回传。
- 只回目录；仅 `node_bin` 这类「目标是文件」的额外回可执行文件。条目数封顶（300），避免巨型目录撑爆响应。
- 已配置的绝对路径会被补进白名单，保证配好的路径一定能继续往上 / 往下浏览。
- 自动探测候选只做**有界浅扫**（深度 ≤ 3、访问目录数 ≤ 2000、不跟随软链、跳过敏感点目录），且**先扫已配置路径再扫家目录**，避免庞大的家目录把一次「打开选择器」拖成几十秒。
- 前端三处校验（界面提示 / 上传前探活 / 选择器实时校验）与后端 `CheckPath` 口径完全一致。
- 接口的 `entries` / `suggestions` **恒为数组**（无内容时是 `[]`，绝不返回 `null`）：Go 的 nil slice 会序列化成 `null`，而界面按数组消费（`.length`），`null` 会让弹窗渲染直接抛异常——这正是「只有 Node 那项能弹窗、另两项点了没反应」的成因，前后端都加了防线（后端 `make([]T,0)` + 序列化边界兜底，前端 `Array.isArray` 归一）。
- `selected` / `selectedIsDir` 表达「当前待选中的是哪一条」：`node_bin` 下点选一个文件后列表仍停在父目录，但选中项已经是那个文件，界面据此高亮并允许确认。

## 测试与 CI

CI 见 `.github/workflows/ci.yml`，五个 job（其中四个必跑）：

- **后端 (Go / SQLite)**：`go build` + `go vet` + `go test ./...`，**默认零外部依赖**，用 SQLite 跑完整接口流程、上传内容嗅探与 MinIO/OSS 驱动的 httptest 假服务测试。
- **后端 (Go / MySQL parity)**：可选 job，仅当仓库配置了 `TEST_MYSQL_DSN` 变量时运行，校验生产 MySQL 契约。
- **前端 (Vue)**：`npm ci` + `npm run build` + `npm test`（Vitest；含 PC 后台视图层测试与经前端工程纳管的小程序纯逻辑测试）。
- **前端类型检查 (vue-tsc)**：`vue-tsc --noEmit`，历史 4 个 `@wangeditor/*` 类型错误已修复，**现为阻塞门禁**。
- **第三方许可清单（过期检查）**：`python3 scripts/gen_third_party_notices.py --check`，核对 `docs/third-party-licenses.md` 是否与当前依赖一致。清单是随专有许可一起交付的声明，依赖一变而清单没重生成，交付的就是与实际不符的声明——这种脱节没人会记得手工核对，交给 CI 挡住。改了依赖后跑一次生成脚本并提交即可（比对会忽略「生成日期」行，不会每天误报）。

「前端单测」与「前端类型检查」是两个独立 job，单测全绿不等于类型通过。想在本机提前拦住，仓库自带了一个 `pre-push` 钩子：

```bash
git config core.hooksPath .githooks   # 每个克隆执行一次
```

它按改动范围挑着跑，不做无谓的等待：涉及 `frontend/` 才跑类型检查，涉及依赖文件
（`backend/go.mod` / `go.sum`、`frontend/package.json` / `package-lock.json`）才核对第三方许可清单，
纯后端或文档提交直接放行；还没装依赖时会提示而不阻断。分别用 `SKIP_TYPECHECK=1` / `SKIP_NOTICES=1 git push`
跳过，或对单次推送 `git push --no-verify`。

> 分支保护与协作规范见 `docs/branch-protection.md`；接口契约见 `docs/openapi.yaml`（OpenAPI 3.0.3）。

## 安全与已知限制

已加固项（详见各子目录 README）：
- 上传文件做 **扩展名白名单 + 文件头魔法字节嗅探**，防止把可执行文件改名图片上传。
- **UGC 内容安全**：
  - **文字**（同步）：员工名片修改、访客开通申请的姓名 / 昵称 / 职位 / 邮箱走微信 `/wxa/msg_sec_check`（v2），
    命中违规直接拒绝提交且不落库；未配凭据 / 接口失败 / 无 openid 时放行并记 `[CONTENT-SEC]` 日志
    （fail-open——内容安全是增强项，不该让微信抖动把「提交申请」变成 100% 失败）。
    注意 openid 由客户端提交、可被省略，因此机器检测是**增强**而非硬闸，人工审核仍是兜底。
  - **图片**（异步）：上传成功后异步调 `/wxa/media_check_async`，结论由微信「消息推送」回调落库
    `media_check` 表（迁移 `0028`）；后台「审核管理 → 内容安全」页可查看记录，并对命中违规（risky）且未处置的图一键下架
    （覆盖成「内容不可用」占位图 + 刷 CDN + 记审计，**不自动下架**，微信误判不可逆）。
    需运营在微信公众平台配「消息推送」URL + Token + EncodingAESKey（系统设置 → 微信 已加这两项并展示推送地址）；
    未配置或提交人无 openid 时图片自动跳过检测，不阻断上传。详见 `docs/commercial-readiness.md` A1。
- 登录接口按 IP **固定窗口限流**，防爆破。
- 启动自检：JWT 密钥为默认值或超管口令为默认值时打印 **[SECURITY] 告警**。
- 内容模型（分类 / 项目 / 服务）支持 **软删除**（GORM `DeletedAt`）。
- JWT **双令牌 + 两级即时吊销**：access 15 分钟 / refresh 30 天，`POST /api/refresh` 静默续期；账号级走 `token_version` 安全戳（改密 / 禁用即时失效），会话级走 `revoked_sessions` 黑名单（**单设备精确吊销**，登出只失效当前设备）。
- PC 管理后台 **httpOnly Cookie 鉴权**（`HttpOnly; SameSite=Lax`），token 不下发到 JS，防 XSS 窃取；小程序端走 Bearer 头。
- **可观测性**：`X-Request-Id` 贯穿请求（响应头带回，便于报障定位）；每请求一行访问日志（方法/路径/状态/耗时/IP，5xx 单独 `[ACCESS-5XX]` 前缀供告警匹配）；`METRICS_ENABLED=1` 可选开启 `/metrics`（Prometheus 文本格式）。
- **前端报错回传服务端**：浏览器侧异常（Vue 运行时 / `window.onerror` / 未捕获 Promise 拒绝）上报到无鉴权端点 `POST /api/client-error`，服务端以 `[CLIENT-ERROR]` 前缀写日志并带 `X-Request-Id`，可与后端同次请求日志串起来排查（端点带每 IP 限流 60/分钟 + 64KB 载荷上限，防刷日志；DEV 下前端只本地 `console.warn` 不上报）。
- **授权与许可（Phase 1+2+3+4 已落地）**：统一的 `Entitlement` 抽象（`internal/license`）——私有 / 独立部署放 `data/license.key`（Ed25519 签名，内嵌公钥验签，`DEPLOY_MODE=private` 时验签失败或过期即拒绝启动）；集中 / 平台模式读系统设置。**运行时强制层已生效**：`EnforceLicense` 中间件（`handler.New` 统一注册）按 `Entitlement` 落地三类拦截（响应均为 HTTP 200 + JSON `code` 字段，与现有一致）——① 过期自动降级只读：拦截写操作（POST/PUT/PATCH/DELETE，白名单登录/刷新/安装/探针/指标/前端报错除外），返回 `{code:2,"授权已过期…只读模式"}`，管理员仍可登录查看、运维仍可探活；② 席位上限：`POST /api/admin/employees` 创建员工时按当前租户（企业）隔离统计的员工数 `>= Seats` 则拒绝（`{code:403}`），`Seats<=0` 不限；私有 / 独立部署固定为许可证声明的租户（单租户形态下即整个部署）；③ 功能开关：`RequireFeature(name)` 中间件拦代发等高级功能，未开通返回 `{code:403,"当前授权未开通该功能：name"}`，代发路由已接 `RequireFeature("mp_release")`。这取代了原 `independent_lock` 只读开关。**多租户授权（Phase 4）**：新增 `tenant_license` 表，可按企业单独配席位 / 到期时间 / 功能开关；解析链为「该租户的行 → 全局系统设置 → 无（不过期 / 不限席位）」，没给某租户单独配时自动回落全局，故空表不会改变存量行为。签发私钥绝不进仓库，公钥已内嵌；签发走 `cmd/license` 工具（`keygen` 生成密钥对、`sign` 签发、`show` 验签展示），私钥只留签发环境、不随服务分发。
- **上线前自检已落地**：`scripts/preflight.sh`（零依赖静态检查：JWT 密钥 / 初始口令 / 小程序引导基址 / 存储凭据 / 上传基址 / Cookie / 数据库口令 / 服务探活 / **微信凭据** / **对外 HTTPS 网关**（外部可达性 + 证书有效期），输出 PASS·WARN·FAIL·SKIP，有 FAIL 退出码 1；`PREFLIGHT_STRICT=1` 时 WARN 也算失败，便于接发布流水线）。与后端启动门槛是同一套判据的两道闸——门槛负责「跑不起来」，本脚本负责「跑起来之前先告诉你差哪几项」。
- **备份与恢复**：`scripts/backup.sh` 按 `DB_DRIVER` 选形态——MySQL 走 `mysqldump --single-transaction`，
  SQLite 走二进制自带的 `server backup`（`VACUUM INTO` 在线快照，**不能直接 `cp` 库文件**：
  WAL 里还有未合并的页，cp 出来是坏库）。`scripts/restore.sh` 恢复（**恢复前自动备份当前库**，
  恢复后校验）；`scripts/restore_drill.sh` 在临时目录/临时库上演练，不碰生产，
  退出码 `0/1/2` 分别是「通过 / 有失败 / 环境不完整」。详见 `docs/deploy-runbook.md` §9。
- **运维子命令**：`server backup --out <文件>`（SQLite 在线快照）、
  `server dbcheck --file <文件> [--field <字段>]`（只读体检：完整性 / 表数 / 各表行数），
  与 `server migrate` 一样不启服务，可进 cron 与流水线。
- **测试覆盖**：后端 httptest 集成（SQLite 零外部依赖 + 可选 MySQL parity）+ MinIO/OSS 假服务单测；前端含视图层组件测试；小程序纯逻辑测试经前端 Vitest 运行。
- **操作审计**：后台写操作（改系统设置 / 管理员增删改 / 企业启停 / 改密码）与**登录失败**
  全部记进 `audit_log`，平台超管可在「平台管理 → 操作日志」按动作 / 操作人 / 结果筛选查询。
  敏感值只记「已修改」不落明文（否则审计表会变成新的泄密面）；只提供查询、不提供修改——
  审计记录一旦可改就等于没有审计。
- **平台超管账号管理**：PC 后台「平台管理 → 账号管理」可改用户名、新增平台超管、重置他人密码、删除账号（护栏：不能删自己、至少保留一个平台超管）。改自己密码仍走右上角「修改密码」（需原密码）。
- **管理端列表服务端分页**：员工 / 企业 / 开通申请 / 名片修改申请 / 平台超管账号 / 审计日志
  统一支持 `?page=&size=`（缺省 20、上限 100）与服务端关键字 / 状态筛选，回包
  `{ code:0, data:[...当前页...], total, page, size }`（`data` 仍是数组，总数与页码在根层）。
  筛选必须落在服务端：前端拿到全量再筛会出现「搜得到但翻页后又没了」的错觉。
  分类 / 项目 / 服务（数量有界且需整表拖拽排序）与单个企业下的管理员列表仍不分页。

当前版本**已知边界**（需基建/流程配合，尚未在本仓库落地）：
- 小程序码 / access_token 等内存缓存在多实例下不共享，水平扩展需引入 Redis。
- **最小告警已落地**：`scripts/alert_check.sh`（healthz 本地探活 / 5xx / 备份新鲜度 / **外部拨测**（含证书临期），零依赖，crontab 每分钟运行，详见《部署与联调》「最小告警配置」）；待运维侧按需扩展的是完整监控栈（Prometheus + Alertmanager）、多地拨测（本脚本的外部拨测仍是单点视角）与 PITR（binlog 时间点恢复）；生产需 HTTPS 与反向代理。
- **数据库迁移自动补齐**：MySQL 部署在启动时按 `schema_migrations` 记账表应用未执行的迁移（幂等、只做增量、多副本用 `GET_LOCK` 串行化）；也可 `AUTO_MIGRATE=0` + `server migrate` 拆成流水线里的一步，详见 `docs/deploy-runbook.md` §4。
- 分类 / 业绩项目 / 业务服务列表未分页（数量有界且需整表拖拽排序）；其余管理端列表已服务端分页（见上）。

## 文档导航

- 需求与架构：《微传名片V1.3_开发内容与工作量确认》《微传名片V1.3_自建技术架构方案》
- 接口 / 字段映射：《首页组件》《跳转页组件》《PC 后台组件与接口字段映射》《三端联调与接口总表》
- 接口规范：`docs/openapi.yaml`（OpenAPI 3.0.3）
- 部署与运维：《部署与联调》（联调环境）· `docs/deploy-runbook.md`（生产上线 Runbook）· `scripts/preflight.sh` · `scripts/backup.sh` · `scripts/alert_check.sh`
- 协作规范：`docs/branch-protection.md`（分支保护 / CI 门禁 / 评审流）
- 许可与第三方声明：`LICENSE`（商业专有许可）· `NOTICES.md`（第三方组件概览与重新生成方法）· `docs/third-party-licenses.md`（逐项清单，由 `scripts/gen_third_party_notices.py` 生成）
- 子工程说明：`backend/README.md` · `frontend/README.md` · `miniprogram/README.md`

## 许可证

**商业专有许可（Proprietary）**，见 [LICENSE](./LICENSE)。

本软件**不是开源软件**，不依据 MIT 或任何其他开源许可授权。除协议明确授予的权利
或 Illacme 另行书面授权外，一切权利保留：未获书面授权不得部署 / 修改 / 逆向 /
转售 / 对外提供 SaaS，也不得移除声明或**规避内置授权校验**（签名校验、席位上限、
到期降级只读、功能开关）。被授权方可在授权范围（席位 / 有效期 / 功能 / 部署形态）
内为自身内部业务部署运行。

- 第三方开源组件**仍受各自原许可**约束（本仓库不对其主张专有权利），
  清单与获取方式见 [NOTICES.md](./NOTICES.md)。
- 本软件的早期版本曾以 MIT 发布；MIT 不可撤销，当时已合法取得的权利不受影响，
  本协议适用于此后分发的版本（LICENSE 第九条）。

商务与授权咨询见 `LICENSE` 末尾联系方式。
