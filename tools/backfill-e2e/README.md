# 真桶 + 存量对象灰度（第六十六 / 六十七轮）

一句话：**把历史形态的存量对象，经 `storage-backfill apply --target-store tenant`
真的复制进客户自己的 S3 桶，改库、记账、再原样搬回来；桶里的字节用签名 GET 取回比对。**

`tools/s3-e2e` 验的是「新上传的对象能不能进真桶」，它的 README 里曾明确写着
「**不覆盖存量对象**：P3 回填未做，本装置上传的都是新对象」。本目录补的就是这条空白。

本目录有**两套并列、互不覆盖**的装置，各自可独立跑、各自复位自己的桶与配置：

| 脚本 | 场景 | 轮次 | 断言 |
| --- | --- | --- | --- |
| `run.sh` | 平台**本地盘** → 客户桶 | 第六十六轮 | 37 条 |
| `run-s3-source.sh` | 平台**自有 S3 桶** → 客户桶 | 第六十七轮 | 74 条 |

第六十七轮补的是 `--source-store=s3` 那一半：存量对象本来就在平台自己的 MinIO / S3
兼容桶里，不必先下载到本地再从 `--source-dir` 读一遍。下面 §「覆盖什么」与
「不证明什么」描述的是 `run.sh`；`run-s3-source.sh` 见最后一节。

## 覆盖什么

| 层 | 谁做 | 断言 |
| --- | --- | --- |
| 真 S3 端点 | versitygw（POSIX 后端，验 SigV4） | 匿名 GET 必须是 **403**，拿到 200 就停下（那是个不验签名的桩） |
| 真配置行 | `backend/cmd/backfill-e2e-prep -mode upsert` | 凭据经 `pii.Encrypt` 写进真 MySQL `tenant_storage_configs`；桶不存在则创建；清掉上一轮 `t/<cid>/` 旧对象 |
| 存量对象 | `tools/backfill-fixture/seed.sql` | company 7 的 3 条 `/uploads/shared-logo.png` 引用，去重成 1 个 20 字节对象 |
| 只读盘点 | `storage-backfill plan --company 7` | 3 条待搬 / 1 个对象；九张业务表 `CHECKSUM` 不变 |
| 预检 | `apply --target-store tenant --dry-run` | 零写入：库值不变、校验和不变、桶里无对象 |
| 真搬迁 | `apply --target-store tenant --yes` | 对象经 `PutObject` 进桶；**签名 GET 取回的 sha256 与源文件一致**；三处库值变 `t/7/...`；平台本地盘没有 `t/7/` 文件 |
| 幂等 / 台账 | 重跑 / `status` | 复制 0 / 改写 0；台账 3 行 `applied` |
| 回滚 | `rollback --yes` | 三处库值回 `/uploads/...`；**九张业务表校验和与搬迁前逐字节一致**；对象仍在桶里（R5） |
| 负例 | 三个 | ① `tenant` 与 `--target-dir` 同给拒绝；② 删掉配置行后必须显式失败且写明「不回退平台盘」；③ 把 `access_key_enc` 改坏后必须报解密失败 |
| 无静默降级 | 全程 | 平台本地盘始终不出现 `t/7/shared-logo.png` |

## 跑

前置：docker（`wc-mysql-ci`，mysql:8.0）、go、curl、shasum，以及一个在 `127.0.0.1:19000`
跑着的 versitygw。起服务端的方法见 `tools/s3-e2e/README.md`：

```bash
mkdir -p /tmp/vgw /tmp/vers
ROOT_ACCESS_KEY=testuser ROOT_SECRET_KEY=testsecret \
  ~/go/bin/versitygw --port 127.0.0.1:19000 --iam-dir /tmp/vgw \
  posix --versioning-dir /tmp/vers /tmp/vgw
```

然后（**脚本没有可执行位，用 `bash` 调**）：

```bash
bash tools/backfill-e2e/run.sh
```

可用环境变量覆盖：`BF_E2E_ENDPOINT` / `BF_E2E_ACCESS_KEY` / `BF_E2E_SECRET_KEY` /
`BF_E2E_BUCKET` / `BF_E2E_COMPANY`。

`PII_ENC_KEY` 在脚本里写死成固定值：写配置行与回填命令必须解得出同一把钥匙，
写死才让取证可复现。`apply --target-store tenant` 会拒绝内置 dev 密钥。

取证输出同时打到 stdout 与 `tools/backfill-e2e/logs/run.log`（该目录已 gitignore）。
二进制与计划文件放 `/tmp/backfill-e2e/`，避免中文路径上的工具差异。

## 一条值得留档的坑：空串对空串的「假绿」

第一版脚本的 `mysqlq` 忘了带库名，MySQL 报 `No database selected`，
而 `2>/dev/null` 把错误吞了 → `vals()` 与 `checksum()` **都返回空串**。
于是「九张业务表校验和一致」「回滚后回到基线」这类断言全部恒绿，
只有「库值等于 `/uploads/...`」那几条暴露了异常。

修法是给 `mysqlq` 补库名，并额外加两条**非空兜底断言**
（「基线校验和非空」「库值查询有效」）。教训与项目里那条一致：
> 测试要测承诺本身；当对比双方可能一起失效时，先证明量尺本身有读数。

## 这个装置**不**证明什么

- **不是云厂商桶**：versitygw 没有 TLS/SNI、CDN、CORS、跨区一致性；云厂商侧仍只有有限证据。
- **源端只覆盖平台本地盘**：平台自己的 S3 桶当源请跑 `run-s3-source.sh`，两套装置证据不通用。
- **`driver=oss` 不支持真桶回填**：`NewS3Store` 会显式拒绝，不会悄悄退回本地盘。
- **虚拟主机风格只验到判定层**：真桶灰度用的是路径风格（`host_header` 留空）；
  `host_header` 指向与 `endpoint` 不同域名时，回填命令不经 nginx、无法改写 Host/SNI，会显式报错。
- **不翻 `TENANT_STORAGE_ROUTE` 默认值**：本装置只验回填命令，上传链路的开关仍是 off。
- 只覆盖公开读桶与单租户灰度，没有做多租户并发 / 配额 / 大对象分块。

## `run-s3-source.sh`：平台 S3 桶 → 客户桶（第六十七轮）

```bash
bash tools/backfill-e2e/run-s3-source.sh
```

前置与 `run.sh` 相同（`wc-mysql-ci` + 19000 上的 versitygw），额外需要两个桶：
`wc-platform`（平台源）与 `wc-t7`（租户目标），可用 `BF_E2E_PLATFORM_BUCKET` /
`BF_E2E_BUCKET` 覆盖。取证日志写 `logs/run-s3-source.log`（已 gitignore）。

### 为什么需要「双量尺」

只断言「客户桶里有对象」证明不了源来自哪里——本地盘悄悄顶上也一样绿。所以脚本同时放
**三种内容不同的 20 字节**：

| 位置 | 内容 | 作用 |
| --- | --- | --- |
| 平台源桶 `shared-logo.png` | A | 正确源。键是 `legacyRel` 推出的**裸相对键**，不带 `p/` |
| 本地诱饵盘 `shared-logo.png` | B | 抓「偷偷回退本地盘」 |
| 平台源桶 `p/shared-logo.png` | C | 抓「给源键加 `p/` 前缀」——它会成功搬完但搬错对象，比 404 难察觉 |

`apply` 后客户桶签名 GET 的 sha256 必须 `== A`、`!= B`、`!= C`，并在脚本开头先断言
三种 sha 两两不同（**先证明量尺自己有读数**）。

### 配置同源的真桶证据

env 故意给一份**错误诱饵**（`STORAGE_DRIVER=local`、`MINIO_BUCKET=wc-env-decoy`、
假凭据），`sys_setting` 给 7 项正确值。只看 env 的话 `driver=local` 会被
`NewPlatformS3Source` 当场拒绝，`apply` 不可能成功——所以正向搬迁成功本身就是
「`sys_setting` 覆盖生效」的证据。另有负例：删掉界面里的 `storage_bucket` 后必须
回落到诱饵桶并显式报「平台源桶不存在」。全程还比对 `CHECKSUM TABLE sys_setting`，
确认读配置没有顺手写库（`setting.Init` 会 `seedDefaults`）。

### 成对的负例：桶不存在 ≠ 源对象不存在

源端只读、没有 `Put` 的显式错误码兜底，而 S3 的 `HEAD` 响应按规范不带 body，
`Stat` 层分不出 `NoSuchKey` 和 `NoSuchBucket`。`PlatformS3Source` 用 `BucketExists`
补了一次探测，这组负例验证它真的把两种错误分开了：

| 负例 | 必须报 | 不能报 |
| --- | --- | --- |
| 界面桶名改成不存在的桶 | `平台源桶 "…" 不存在` + `这不是「源对象不存在」` | `源对象 … 无法复制成` |
| 删掉源对象但保留桶 | `源对象 … 不存在，无法复制成`（退出码 2） | `平台源桶` |

其余负例：`s3` + `--source-dir` 同给、未知 `--source-store`、`driver=oss`、
配置缺凭据、`tenant` + `--target-dir` 同给、缺租户配置行、内置 dev `PII_ENC_KEY`。

### 反向验证（装置自己也要有牙齿）

`/tmp/b67-e2e-reverse.py` 风格的做法已落实在第六十七轮交付记录里：对实现做 4 处注入
（源键加 `p/`、`Stat`+`Open` 一起改、删掉桶探测、忽略 `sys_setting`、s3 分支回退本地盘），
每处都跑整套取证，确认对应的字节 / 报错断言**必红**，再逐条还原并复跑全绿。
第一次注入只改了 `Stat` 没改 `Open`，预检看到诱饵 C、复制时仍读裸键 A，断言反而没红——
这正是「测试要测承诺本身，别测一个更容易满足的代理」的又一次实证，已改成两处一起改。

### 这个装置**不**证明什么

- **不是云厂商桶**：仍是 versitygw（POSIX 后端），没有 TLS/SNI、CDN、CORS、跨区一致性。
- **平台桶走的是 `BucketLookupAuto`**：端点 `127.0.0.1:19000` 是 IP，实测走路径风格；
  云厂商上若端点形态不同，仍需单独验收（上传侧与回填侧共用同一构造函数，行为同源）。
- **不覆盖 `driver=oss` 源**：显式拒绝，不会把 OSS 端点当 S3 端点连。
- **不翻 `TENANT_STORAGE_ROUTE` 默认值**：装置只验回填命令，上传链路开关仍是 off。
- 只覆盖单租户灰度，没有做多租户并发 / 配额 / 大对象分块。
