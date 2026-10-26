# 第三方组件声明（Third-Party Notices）

本软件（微传名片）为专有商业软件，许可见 [LICENSE](./LICENSE)。
**本文件声明的第三方组件不因该许可而改变**——它们各自仍受其原始开源许可约束，
Illacme 不对其主张专有权利。使用、分发本软件时须一并遵守这些许可。

完整的逐项清单（含版本与许可）见 **[docs/third-party-licenses.md](./docs/third-party-licenses.md)**，
本文件给出概览、需要人工留意的许可，以及重新生成清单的方法。

## 素材资产

- **Lucide 图标**（`backend/internal/assets/library/icons/`）：取自
  [Lucide](https://lucide.dev) v0.469.0，**ISC License**。
  经 `rsvg-convert` 光栅化并合成到渐变圆角方块。
  详见 `backend/internal/assets/library/CREDITS.md`（含许可全文出处）。
- 其余封面 / logo / 头像 / 背景素材为程序化生成（非第三方素材），不适用上游许可。

## 后端 Go 依赖（44 个，随服务二进制分发）

以许可文件**正文**判定（不是 `go.mod` 里的声明），分布如下：

| 许可 | 数量 | 代表组件 |
|---|---|---|
| MIT | 22 | `gin`、`gorm`、`golang-jwt/jwt/v5`、`mimetype`、`minio` 之外的大多数 |
| BSD-3-Clause | 14 | `golang.org/x/*`、`google.golang.org/protobuf`、`modernc.org/*`、`google/uuid` |
| Apache-2.0 | 5 | `minio-go/v7`、`minio/*`、`klauspost/compress`、`go-ini/ini` |
| MPL-2.0 | 1 | `go-sql-driver/mysql` |
| MIT / Apache-2.0 双许可 | 2 | `gopkg.in/yaml.v3`、`go.yaml.in/yaml/v3` |

范围：只含 `go list -deps ./...` 命中的模块，即真正链进 `cmd/server` / `cmd/devserver`
的那些；仅测试期使用的模块（如 `github.com/stretchr/testify`）不随产物分发，故不在清单内。

## 前端 npm 依赖（运行时 128 个，随 `npm run build` 产物分发）

排除 `devDependencies`（构建工具链不进产物）后的分布：

| 许可 | 数量 |
|---|---|
| MIT | 115 |
| ISC | 9 |
| BSD-3-Clause | 2（`source-map-js`、`normalize-wheel-es`） |
| BSD-2-Clause | 1（`entities`） |
| Apache-2.0 | 1（`typescript`，仅构建期类型检查） |

## 小程序

`miniprogram/` 为原生小程序 + TypeScript，**不引第三方 npm 依赖**（无 `package.json`、
无 `node_modules`）。其运行受《微信公众平台服务条款》及相关协议约束，不属于开源许可范畴。

## 需要留意的许可

| 组件 | 许可 | 为什么留意 |
|---|---|---|
| `github.com/go-sql-driver/mysql` | MPL-2.0 | **文件级 copyleft**：作为未修改的依赖使用不影响专有分发；但若**修改了它的源文件**，该等文件须按 MPL 公开源码（同文件外的代码不受传染）。当前未修改。 |
| minio 系、`klauspost/compress`、`go-ini/ini` | Apache-2.0 | 要求保留 NOTICE 文件（如有）与变更声明；仅作未修改依赖时义务较轻。 |
| `golang.org/x/*` 等 | BSD-3-Clause | 二进制分发须在文档 / 材料中保留版权声明——`docs/third-party-licenses.md` 即承担该义务。 |
| Lucide 图标 | ISC | 随素材库分发，见 `backend/internal/assets/library/CREDITS.md`。 |

未发现 GPL / LGPL / AGPL 依赖——这类许可与专有分发不兼容，将来若引入须先做评估。

## 重新生成清单

依赖版本一变，清单就可能与实际情况脱节（本次生成就遇到两处「许可文件正文与
package.json 声明不一致」的情况，一律以文件正文为准）。交付前跑一次：

```bash
python3 scripts/gen_third_party_notices.py    # 仓库根目录执行
```

**CI 已把这件事做成门禁**（`.github/workflows/ci.yml` 的 `notices` job）：改了依赖而没重生成清单，
PR 会红——这种「文档与代码脱节」没人会记得手工核对，只能交给 CI。比对时会忽略「生成日期」行，
所以不会因为过了一天就误报。

它会覆写 `docs/third-party-licenses.md`（零第三方依赖，只用 Python 标准库；
npm 部分需要 `frontend/node_modules` 已安装）。

## 交付建议

对外交付（尤其是私有化部署交付源码）时，随附本文件与
`docs/third-party-licenses.md`，并把生成日期一并给出——过期的清单不如没有清单。
