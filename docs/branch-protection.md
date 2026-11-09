# 分支保护与协作规范

本仓库为 monorepo（`backend/` + `frontend/` + `miniprogram/` + `docs/`），CI 见 `.github/workflows/ci.yml`。  
本文给出推荐的 GitHub 分支保护配置，保证 `main` 始终处于「构建通过 + 测试通过 + 类型检查通过」的状态。

## 1. CI 门禁（12 个 job，全部阻塞合并）

> 2026-11-09 订正：CI 早已从 5 个 job 扩到 12 个；`vuln`（2026-11-03）、
> `backend-mysql` 与 `e2e-smoke`（2026-11-09）陆续摘掉「可选 / 非阻塞」，
> 现在**没有任何 job 可以绿着跳过**。下表 job 名取自 `.github/workflows/ci.yml`
> 的 `name:`，必须与分支保护里勾选的 required checks 逐字一致。

| Job（`name:`） | 内容 | 是否阻塞合并 |
| --- | --- | --- |
| `后端 (Go / SQLite)` | `go build` + `go vet` + `go test ./...`（SQLite 全流程，零外部依赖） | ✅ 必跑 |
| `后端 lint (golangci-lint)` | 固定版本 v2.15.0，只开 errcheck/govet/ineffassign/staticcheck/unused | ✅ 必跑 |
| `后端 (Go / MySQL parity)` | CI 自带 `mysql:8.0` service：连接建表 + 同一套 API 断言 + 安装向导 MySQL 分支 | ✅ 必跑（2026-11-09 起，无需仓库变量） |
| `前端 (Vue)` | `npm ci` + eslint + `npm run build` + Vitest（含小程序端纯逻辑测试） | ✅ 必跑 |
| `前端类型检查 (vue-tsc)` | `vue-tsc --noEmit` 全量类型检查 | ✅ 必跑（历史 4 个 wangEditor 类型错已修复） |
| `第三方许可清单（过期检查）` | `python3 scripts/gen_third_party_notices.py --check`：核对 `docs/third-party-licenses.md` 与当前依赖 | ✅ 必跑（清单是交付物） |
| `备份链自检` | `scripts/selftest_backup.sh`：备份→恢复→逐字节一致，以及失败路径 | ✅ 必跑 |
| `发版脚本自检` | `scripts/selftest_deploy_frontend.sh`：非交互口令 + 迁移清单完整性（50 条断言） | ✅ 必跑 |
| `密钥扫描 (gitleaks)` | 固定版本 8.30.1 CLI，只扫本次变更提交范围，命中即红 | ✅ 必跑 |
| `依赖漏洞扫描` | `govulncheck ./...` + `npm audit --omit=dev`（只看运行时依赖） | ✅ 必跑（2026-11-03 起） |
| `镜像构建（backend / frontend）` | buildx 分别 build 两个 Dockerfile（`push: false`） | ✅ 必跑 |
| `端到端冒烟` | `scripts/e2e_smoke.sh`：真起进程，安装向导 → 登录 → 建企业 → 配内容 → 小程序侧读回 | ✅ 必跑（2026-11-09 起） |

## 2. 开启分支保护（GitHub UI）

仓库 **Settings → Branches → Add branch protection rule**（或 Rulesets，新版 UI 入口一致）：

1. **Branch name pattern**：`main`
2. 勾选 **Require a pull request before merging**：
   - 勾选 **Require approvals**（人数 ≥ 1；单人项目可设 0，但建议保留 PR 流程留痕）
   - 勾选 **Dismiss stale pull request approvals when new commits are pushed**
3. 勾选 **Require status checks to pass before merging**：
   - 勾选 **Require branches to be up to date before merging**
   - 在列表中添加上表 **12 个 job**（首次开启时若列表为空，先任意推送一次让 CI 跑出 job 名，再回来勾选；job 名以 CI 里的 `name:` 为准）
4. 勾选 **Require conversation resolution before merging**（评审意见必须处理完）
5. 建议勾选 **Do not allow bypassing the above settings**（管理员也走流程；紧急修复可用 `gh` 临时豁免，事后补记录）
6. 保存

## 3. 日常协作流

```bash
# 新功能 / 修复：从最新 main 切分支
git switch main && git pull
git switch -c feat/xxx        # 或 fix/xxx、chore/xxx

# 小步提交，推送后用 gh 开 PR（CI 自动触发）
git add -A && git commit -m "..."
git push -u origin feat/xxx
gh pr create --fill

# PR 绿了 + 评审通过 → squash merge；删掉远端分支
```

约定：

- **不直接 push `main`**（分支保护会拒绝；本地也别 `--no-verify` 绕过）。
- PR 尽量小且单一职责；描述里写清「改了什么 / 为什么 / 怎么验证的」。
- 合并前确认 CI **12 个 job 全绿**；`后端 (Go / MySQL parity)` 已是必跑门禁（service 容器自供给，`go test` 被 Skip 也会因缺少 `--- PASS` 断言而判红），`端到端冒烟` 同样已摘掉 `continue-on-error`。
- 改动 `backend/go.mod`、`frontend/package.json` / `package-lock.json` 后，`第三方许可清单（过期检查）` 大概率会红——这不是误报：跑一次 `python3 scripts/gen_third_party_notices.py` 并提交新清单即可。
- 安全类改动（鉴权 / 权限 / 数据脱敏）请在 PR 描述里显式标注 `security`，评审时按安全清单过一遍。

## 4. 紧急热修复

生产故障需要绕过完整流程时：

1. 仍走 PR（保留记录），由仓库 Owner 临时豁免 approvals / checks；
2. 合并后**当天内**补一次复盘：根因、为何绕过、如何防止再发（补测试 / 加监控 / 改流程）；
3. 若绕过的是测试门禁，必须补对应用例后再关闭事件。

## 5. 仓库设置建议（Settings 其它页）

- **General**：默认分支 `main`；勾选 "Automatically delete head branches"（合并后自动删分支）。
- **Actions**：Workflow permissions 保持 `Read repository contents`（CI 只需读）；如需 PR 自动写评论再单独提权。MySQL parity 已改用 service 容器自供给，**无需**在 Variables 配 `TEST_MYSQL_DSN`。
- **Code security**：开启 Dependabot alerts（依赖 `.github/dependabot.yml` 已配置周级 npm / gomod / actions 更新）。
