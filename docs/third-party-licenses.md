# 第三方依赖许可清单（自动生成）

> 生成日期：2026-10-26　·　随依赖版本变动会失效，交付前请重新生成一次。
>
> 本软件（微传名片）为专有商业软件，许可见 [../LICENSE](../LICENSE)。
> **本清单中的第三方组件不因该许可而改变**——它们各自仍受其原始许可约束。
> 概览与人工复核要点见 [../NOTICES.md](../NOTICES.md)。

## 生成方式

```bash
python3 scripts/gen_third_party_notices.py    # 仓库根目录执行，覆写本文件
```

判定依据是各依赖目录里的**许可文件正文**（不是 package.json / go.mod 的 license 字段——
后者可能与实际文件不符，本次生成就遇到两处不一致，一律以正文为准）。
BSD 家族按第三条款（不得以作者名义背书）区分 2-Clause / 3-Clause。

## 后端 Go 依赖（44 个，随服务二进制分发）

| 模块                                            | 许可                          | 依据文件       |
|-----------------------------------------------|-----------------------------|------------|
| github.com/aliyun/aliyun-oss-go-sdk           | MIT                         | LICENSE    |
| github.com/dustin/go-humanize                 | MIT                         | LICENSE    |
| github.com/gabriel-vasile/mimetype            | MIT                         | LICENSE    |
| github.com/gin-contrib/sse                    | MIT                         | LICENSE    |
| github.com/gin-gonic/gin                      | MIT                         | LICENSE    |
| github.com/glebarez/go-sqlite                 | BSD-3-Clause                | LICENSE    |
| github.com/glebarez/sqlite                    | MIT                         | License    |
| github.com/go-ini/ini                         | Apache-2.0                  | LICENSE    |
| github.com/go-playground/locales              | MIT                         | LICENSE    |
| github.com/go-playground/universal-translator | MIT                         | LICENSE    |
| github.com/go-playground/validator/v10        | MIT                         | LICENSE    |
| github.com/go-sql-driver/mysql                | MPL-2.0                     | LICENSE    |
| github.com/golang-jwt/jwt/v5                  | MIT                         | LICENSE    |
| github.com/google/uuid                        | BSD-3-Clause                | LICENSE    |
| github.com/jinzhu/inflection                  | MIT                         | LICENSE    |
| github.com/jinzhu/now                         | MIT                         | License    |
| github.com/klauspost/compress                 | Apache-2.0                  | LICENSE    |
| github.com/klauspost/cpuid/v2                 | MIT                         | LICENSE    |
| github.com/klauspost/crc32                    | BSD-3-Clause                | LICENSE    |
| github.com/leodido/go-urn                     | MIT                         | LICENSE    |
| github.com/mattn/go-isatty                    | MIT                         | LICENSE    |
| github.com/minio/crc64nvme                    | Apache-2.0                  | LICENSE    |
| github.com/minio/md5-simd                     | Apache-2.0                  | LICENSE    |
| github.com/minio/minio-go/v7                  | Apache-2.0                  | LICENSE    |
| github.com/pelletier/go-toml/v2               | MIT                         | LICENSE    |
| github.com/philhofer/fwd                      | MIT                         | LICENSE.md |
| github.com/remyoudompheng/bigfft              | BSD-3-Clause                | LICENSE    |
| github.com/rs/xid                             | MIT                         | LICENSE    |
| github.com/tinylib/msgp                       | MIT                         | LICENSE    |
| github.com/ugorji/go/codec                    | MIT                         | LICENSE    |
| go.yaml.in/yaml/v3                            | MIT / Apache-2.0（双许可，取其一即可） | LICENSE    |
| golang.org/x/crypto                           | BSD-3-Clause                | LICENSE    |
| golang.org/x/net                              | BSD-3-Clause                | LICENSE    |
| golang.org/x/sys                              | BSD-3-Clause                | LICENSE    |
| golang.org/x/text                             | BSD-3-Clause                | LICENSE    |
| golang.org/x/time                             | BSD-3-Clause                | LICENSE    |
| google.golang.org/protobuf                    | BSD-3-Clause                | LICENSE    |
| gopkg.in/yaml.v3                              | MIT / Apache-2.0（双许可，取其一即可） | LICENSE    |
| gorm.io/driver/mysql                          | MIT                         | License    |
| gorm.io/gorm                                  | MIT                         | LICENSE    |
| modernc.org/libc                              | BSD-3-Clause                | LICENSE    |
| modernc.org/mathutil                          | BSD-3-Clause                | LICENSE    |
| modernc.org/memory                            | BSD-3-Clause                | LICENSE    |
| modernc.org/sqlite                            | BSD-3-Clause                | LICENSE    |

> 只含 `go list -deps ./...` 命中的模块，即真正链进 `cmd/server` / `cmd/devserver` 的那些；
> 仅测试期使用的模块（如 `github.com/stretchr/testify`）不随产物分发，故不在表内。

## 前端 npm 依赖（运行时 128 个，随 `npm run build` 产物分发）

| 包                                  | 版本      | 许可           |
|------------------------------------|---------|--------------|
| @babel/helper-string-parser        | 7.29.7  | MIT          |
| @babel/helper-validator-identifier | 7.29.7  | MIT          |
| @babel/parser                      | 7.29.9  | MIT          |
| @babel/runtime                     | 7.29.10 | MIT          |
| @babel/types                       | 7.29.8  | MIT          |
| @ctrl/tinycolor                    | 4.2.1   | MIT          |
| @element-plus/icons-vue            | 2.3.2   | MIT          |
| @floating-ui/core                  | 1.8.0   | MIT          |
| @floating-ui/dom                   | 1.8.0   | MIT          |
| @floating-ui/utils                 | 0.2.12  | MIT          |
| @jridgewell/sourcemap-codec        | 1.6.0   | MIT          |
| @sxzz/popperjs-es                  | 2.11.8  | MIT          |
| @transloadit/prettier-bytes        | 0.0.7   | MIT          |
| @types/event-emitter               | 0.3.5   | MIT          |
| @types/lodash                      | 4.17.26 | MIT          |
| @types/lodash-es                   | 4.17.12 | MIT          |
| @types/web-bluetooth               | 0.0.21  | MIT          |
| @uppy/companion-client             | 2.2.2   | MIT          |
| @uppy/core                         | 2.3.4   | MIT          |
| @uppy/store-default                | 2.1.1   | MIT          |
| @uppy/utils                        | 4.1.3   | MIT          |
| @uppy/xhr-upload                   | 2.1.3   | MIT          |
| @vue/compiler-core                 | 3.5.44  | MIT          |
| @vue/compiler-dom                  | 3.5.44  | MIT          |
| @vue/compiler-sfc                  | 3.5.44  | MIT          |
| @vue/compiler-ssr                  | 3.5.44  | MIT          |
| @vue/devtools-api                  | 6.6.4   | MIT          |
| @vue/reactivity                    | 3.5.44  | MIT          |
| @vue/runtime-core                  | 3.5.44  | MIT          |
| @vue/runtime-dom                   | 3.5.44  | MIT          |
| @vue/server-renderer               | 3.5.44  | MIT          |
| @vue/shared                        | 3.5.44  | MIT          |
| @vueuse/core                       | 15.0.0  | MIT          |
| @vueuse/metadata                   | 15.0.0  | MIT          |
| @vueuse/shared                     | 15.0.0  | MIT          |
| @wangeditor/basic-modules          | 1.1.7   | MIT          |
| @wangeditor/code-highlight         | 1.0.3   | MIT          |
| @wangeditor/core                   | 1.1.19  | MIT          |
| @wangeditor/editor                 | 5.1.23  | MIT          |
| @wangeditor/editor-for-vue         | 5.1.12  | MIT          |
| @wangeditor/list-module            | 1.0.5   | MIT          |
| @wangeditor/table-module           | 1.1.4   | MIT          |
| @wangeditor/upload-image-module    | 1.0.2   | MIT          |
| @wangeditor/video-module           | 1.1.4   | MIT          |
| agent-base                         | 6.0.2   | MIT          |
| async-validator                    | 4.2.5   | MIT          |
| asynckit                           | 0.4.0   | MIT          |
| axios                              | 1.20.0  | MIT          |
| call-bind-apply-helpers            | 1.0.2   | MIT          |
| combined-stream                    | 1.0.8   | MIT          |
| compute-scroll-into-view           | 1.0.20  | MIT          |
| csstype                            | 3.2.3   | MIT          |
| d                                  | 1.0.2   | ISC          |
| dayjs                              | 1.11.23 | MIT          |
| debug                              | 4.4.3   | MIT          |
| delayed-stream                     | 1.0.0   | MIT          |
| dom7                               | 3.0.0   | MIT          |
| dunder-proto                       | 1.0.1   | MIT          |
| element-plus                       | 2.14.7  | MIT          |
| entities                           | 7.0.1   | BSD-2-Clause |
| es-define-property                 | 1.0.1   | MIT          |
| es-errors                          | 1.3.0   | MIT          |
| es-object-atoms                    | 1.1.2   | MIT          |
| es-set-tostringtag                 | 2.1.0   | MIT          |
| es5-ext                            | 0.10.64 | ISC          |
| es6-iterator                       | 2.0.3   | MIT          |
| es6-symbol                         | 3.1.4   | ISC          |
| esniff                             | 2.0.1   | ISC          |
| estree-walker                      | 2.0.2   | MIT          |
| event-emitter                      | 0.3.5   | MIT          |
| ext                                | 1.7.0   | ISC          |
| follow-redirects                   | 1.16.1  | MIT          |
| form-data                          | 4.0.6   | MIT          |
| function-bind                      | 1.1.2   | MIT          |
| get-intrinsic                      | 1.3.0   | MIT          |
| get-proto                          | 1.0.1   | MIT          |
| gopd                               | 1.2.0   | MIT          |
| has-symbols                        | 1.1.0   | MIT          |
| has-tostringtag                    | 1.0.2   | MIT          |
| hasown                             | 2.0.4   | MIT          |
| html-void-elements                 | 2.0.1   | MIT          |
| https-proxy-agent                  | 5.0.1   | MIT          |
| i18next                            | 20.6.1  | MIT          |
| immer                              | 9.0.21  | MIT          |
| is-hotkey                          | 0.2.0   | MIT          |
| is-plain-object                    | 5.1.0   | MIT          |
| is-url                             | 1.2.4   | MIT          |
| lodash                             | 4.18.1  | MIT          |
| lodash-es                          | 4.18.1  | MIT          |
| lodash-unified                     | 1.0.3   | MIT          |
| lodash.camelcase                   | 4.3.0   | MIT          |
| lodash.clonedeep                   | 4.5.0   | MIT          |
| lodash.debounce                    | 4.0.8   | MIT          |
| lodash.foreach                     | 4.5.0   | MIT          |
| lodash.isequal                     | 4.5.0   | MIT          |
| lodash.throttle                    | 4.1.1   | MIT          |
| lodash.toarray                     | 4.4.0   | MIT          |
| magic-string                       | 0.30.21 | MIT          |
| math-intrinsics                    | 1.1.0   | MIT          |
| memoize-one                        | 6.0.0   | MIT          |
| mime-db                            | 1.52.0  | MIT          |
| mime-match                         | 1.0.2   | ISC          |
| mime-types                         | 2.1.35  | MIT          |
| ms                                 | 2.1.3   | MIT          |
| namespace-emitter                  | 2.0.1   | MIT          |
| nanoid                             | 3.3.20  | MIT          |
| next-tick                          | 1.1.0   | ISC          |
| normalize-wheel-es                 | 1.2.0   | BSD-3-Clause |
| picocolors                         | 1.1.1   | ISC          |
| pinia                              | 2.3.1   | MIT          |
| postcss                            | 8.5.30  | MIT          |
| preact                             | 10.29.8 | MIT          |
| prismjs                            | 1.30.0  | MIT          |
| proxy-from-env                     | 2.1.0   | MIT          |
| scroll-into-view-if-needed         | 2.2.31  | MIT          |
| slate                              | 0.72.8  | MIT          |
| slate-history                      | 0.66.0  | MIT          |
| snabbdom                           | 3.6.4   | MIT          |
| source-map-js                      | 1.2.2   | BSD-3-Clause |
| ssr-window                         | 3.0.0   | MIT          |
| tiny-warning                       | 1.0.3   | MIT          |
| type                               | 2.7.3   | ISC          |
| typescript                         | 5.9.3   | Apache-2.0   |
| vue                                | 3.5.44  | MIT          |
| vue-component-type-helpers         | 3.3.13  | MIT          |
| vue-demi                           | 0.14.10 | MIT          |
| vue-router                         | 4.6.4   | MIT          |
| wildcard                           | 1.1.2   | MIT          |

> 已排除 `devDependencies`（构建工具链不随前端产物分发）。

## 需要留意的许可

| 组件 | 许可 | 为什么留意 |
|---|---|---|
| `github.com/go-sql-driver/mysql` | MPL-2.0 | **文件级 copyleft**：作为未修改的依赖使用不影响专有分发；但若**修改了它的源文件**，该等文件须按 MPL 公开源码（同一文件之外的代码不受传染）。当前未修改。 |
| `github.com/minio/minio-go/v7` 等 minio 系、`github.com/klauspost/compress`、`github.com/go-ini/ini`、`go.yaml.in/yaml/v3` | Apache-2.0 | 要求保留 NOTICE 文件（如有）与变更声明；仅作未修改依赖时义务较轻。 |
| `golang.org/x/*`、`google.golang.org/protobuf` 等 | BSD-3-Clause | 二进制分发须在文档/材料中保留版权声明——本文件即承担该义务。 |
| `github.com/klauspost/compress` | BSD-3-Clause + Apache-2.0 | 一个 LICENSE 里含两份许可：沿用 Go Authors 的部分按 BSD-3，新增部分按 Apache-2.0。 |
| `gopkg.in/yaml.v3`、`go.yaml.in/yaml/v3` | MIT / Apache-2.0 双许可 | 取其一遵守即可。 |
| `typescript`（前端） | Apache-2.0 | 只用于构建期类型检查，不进产物。 |
| Lucide 图标（`backend/internal/assets/library/icons/`） | ISC | 随素材库分发，见 `../NOTICES.md` 与 `backend/internal/assets/library/CREDITS.md`。 |

> 未发现 GPL / LGPL / AGPL 依赖——这类许可与专有分发不兼容，将来若引入须先做评估。
