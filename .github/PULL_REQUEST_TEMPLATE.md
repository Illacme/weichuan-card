## 变更说明
<!-- 简述本次 PR 的目的与范围 -->

## 关联需求 / 文档
<!-- 关联的根目录映射文档或 issue，如《三端联调与接口总表》§x -->

## 改动类型
- [ ] 后端 (backend)
- [ ] 前端 (frontend)
- [ ] 小程序 (miniprogram)
- [ ] 文档 / 配置
- [ ] CI / 依赖

## 自检清单
- [ ] 后端 `go build ./... && go vet ./... && go test ./...` 通过（CI 中由 backend job 覆盖）
- [ ] 前端 `npm run build && npm test` 通过（CI 中由 frontend job 覆盖）
- [ ] 新增接口已在根目录映射文档或 backend/README.md 中同步
- [ ] 涉及密钥 / 环境变量仅通过 `.env.example` 与 Secrets/Variables 注入，未硬编码
- [ ] 数据库变更（如新增表 / 列）已追加对应 migrations，并考虑存量库迁移
- [ ] 若改了依赖（`backend/go.mod` / `frontend/package.json` / `package-lock.json`），已跑 `python3 scripts/gen_third_party_notices.py` 并提交新清单（CI 中由 notices job 覆盖）

## 测试计划
<!-- 手动验证步骤，或说明 CI 已覆盖 -->

## 备注
<!-- 遗留项、已知限制、需要 reviewer 重点关注的地方 -->
