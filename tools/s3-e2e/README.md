# 真桶端到端装置（第六十三轮）

一句话：**真的往一个 S3 服务端写对象，再让真的 nginx 按生成的 snippet 把它取回来，逐字节比对。**

`tools/nginx-r1-check` 验的是 nginx 语义（路径映射、Host/SNI、缓存头、Range），
它的"桶"是 nginx 自己扮演的静态目录——**桶根就是目录根**，这个假设掩盖了一件事：
真 S3 的路径风格要求 `/<bucket>/<key>`。本装置就是为了把这件事从"假设"变成"证据"。

## 覆盖什么

| 层 | 谁做 | 断言 |
| --- | --- | --- |
| 配置行 → 凭据解密 → 实例构造 | `backend/cmd/s3-e2e`（`DBLoader` + `Resolver`） | `For(cid)` 拿到的必须是 `*storage.MinioUploader`，不是平台实例 |
| 真 `PutObject` | 同上（`Save`） | 桶里确有该对象、PNG 头正确、`StatObject` 成功、签名 GET 与桶内字节一致 |
| 预签名直传 | 同上（`PresignPut` + HTTP PUT） | 桶里字节与发出的完全一致 |
| 负例：坏凭据 / 坏密文 | 同上 | Save 显式失败；平台盘文件数零新增（没有静默降级） |
| 生成器 | `backend/cmd/nginx-snippets render` | 用**同一份租户表**生成 snippet，不手写 location |
| 反代回源 | nginx:alpine 容器 | 200 + 字节一致 + Content-Type + 安全头齐 + Cache-Control 恰好一条 |
| 直传对象也可取 | 同上 | 预签名写进去的对象走同一条 nginx 路径也是 200 |
| 前缀隔离 | 同上 | 未配置的 `t/99/` 落到平台回退段，不进桶 |

## 起一个 S3 服务端

本期用 [versitygw](https://github.com/versity/versitygw)（POSIX 后端，真实 S3 协议实现，
用 aws-sdk-go-v2 解析请求，会校验 SigV4）。**不是桩**：它会真的验签名——凭据错的时候
`cmd/s3-e2e` 的负例拿到的是 `SignatureDoesNotMatch`，不是一句写死的 403。

选它而不是 MinIO 的原因很具体：MinIO 已撤掉二进制分发（`dl.min.io/...` 全部 410），
Docker Hub `registry-1.docker.io` 在本机不通，daocloud / 1ms / quay 三个镜像站都没有
`minio/minio`。`versitygw` 能直接从 `goproxy.cn` 源码编译：

```bash
# 注意 GOARCH：本机 Go 是 darwin/amd64（Rosetta），交叉编译时 go install
# 会以 "cannot install cross-compiled binaries when GOBIN is set" 拒绝，
# 必须显式指定宿主架构。
GOOS=darwin GOARCH=arm64 GOPROXY=https://goproxy.cn,direct \
  go install github.com/versity/versitygw/cmd/versitygw@v1.11.0

mkdir -p /tmp/vgw /tmp/vers
ROOT_ACCESS_KEY=testuser ROOT_SECRET_KEY=testsecret \
  ~/go/bin/versitygw --port 127.0.0.1:19000 --iam-dir /tmp/vgw \
  posix --versioning-dir /tmp/vers /tmp/vgw
```

自检：匿名 `curl -o /dev/null -w '%{http_code}' http://127.0.0.1:19000/` 必须是 **403**。
拿到 200 说明这个端点不校验签名，那它证明不了任何事，`run.sh` 第一步就会停下。

## 跑

```bash
cd tools/s3-e2e && bash run.sh
```

可用环境变量覆盖：`S3_E2E_ENDPOINT` / `S3_E2E_ACCESS_KEY` / `S3_E2E_SECRET_KEY` /
`S3_E2E_BUCKET` / `S3_E2E_COMPANY` / `S3_E2E_SITE_PORT`。

前置：docker（`nginx:alpine`，本地已有）、go、curl。

## 这个装置**不**证明什么

- **不是云厂商桶**：没有 TLS/SNI、没有 CDN、没有 CORS、没有跨区一致性。TLS 那条
  目前只有 `tools/nginx-r1-check` 的桩证据（缺 `proxy_ssl_server_name` → 421）。
- **只覆盖公开读桶**：nginx 回源那一跳不带签名，私有桶需要另一条链路（未实现）。
- **只覆盖平台统一入口**：`public_base_url`（客户自有域名）仍是空值路径。
- **不覆盖存量对象**：本装置上传的都是新对象。第六十六轮新增的
  `tools/backfill-e2e/` 补齐了「真桶 + 存量对象」灰度——那份装置复用同一个
  versitygw 端点与同样的凭据，把 `seed.sql` 造出来的历史 `/uploads/` 引用
  真的搬进桶里再搬回来。两个装置合起来才覆盖「新对象 + 存量对象」两条路径。
- endpoint 在容器内外写法不同（`host.docker.internal:19000` vs `127.0.0.1:19000`），
  这是装置的必然，不是被测物：两侧指向同一个桶、同一个键。
