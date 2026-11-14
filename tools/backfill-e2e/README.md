# 真桶 + 存量对象灰度（第六十六轮）

一句话：**把平台本地盘里一个历史形态的存量对象，经 `storage-backfill apply --target-store tenant`
真的复制进客户自己的 S3 桶，改库、记账、再原样搬回来；桶里的字节用签名 GET 取回比对。**

`tools/s3-e2e` 验的是「新上传的对象能不能进真桶」，它的 README 里曾明确写着
「**不覆盖存量对象**：P3 回填未做，本装置上传的都是新对象」。本装置补的就是这条空白。

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
- **源端只有平台本地盘**：平台自己的 S3 桶当源这一轮没做（`ObjectStore` 接口已留好 `Open`）。
- **`driver=oss` 不支持真桶回填**：`NewS3Store` 会显式拒绝，不会悄悄退回本地盘。
- **虚拟主机风格只验到判定层**：真桶灰度用的是路径风格（`host_header` 留空）；
  `host_header` 指向与 `endpoint` 不同域名时，回填命令不经 nginx、无法改写 Host/SNI，会显式报错。
- **不翻 `TENANT_STORAGE_ROUTE` 默认值**：本装置只验回填命令，上传链路的开关仍是 off。
- 只覆盖公开读桶与单租户灰度，没有做多租户并发 / 配额 / 大对象分块。
