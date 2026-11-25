-- 0027：租户小程序「代码上传密钥」（company_wx_app.upload_key）。
--
-- 背景：版本发布链路的「上传代码」这一步，微信只认「代码上传密钥」
-- （公众平台 → 开发管理 → 开发设置 → 小程序代码上传 里下载的 .key），
-- **不认 AppSecret**。后台要代传代码（见 internal/mpupload），就得存这把钥匙。
--
-- 为什么跟 app_secret 放同一张表、同样只写不读：它和 AppSecret 是同一层东西
-- ——「这个租户的小程序归谁管」的凭据。取值链也是三级回落：
--   租户（本列） → 平台全局（sys_setting.wx_upload_key） → 无
-- 最后一环刻意没有环境变量：PEM 是**多行**文本，而配置文件（runtime.env）
-- 按「一行一项」解析、写入时直接拒绝含换行的值（见 config.WriteFileValues），
-- 所以这把钥匙只能从界面上配。这反而更安全——它不会跟着部署配置被复制到
-- 镜像、CI 变量和备份脚本里去。
--
-- 大小给到 2048：RSA 2048 的 PEM 约 1700 字节，留余量。
--
-- SQLite（测试 / devserver）由 GORM AutoMigrate 自动补列；
-- MySQL（生产 / parity）由本迁移补齐。存量库需手动执行。
--
-- 与 0026 不同：加列**不是幂等**的，对已存在该列的库再执行会报 Duplicate column。
-- 正常路径不会重放（migrate 包按 schema_migrations 记账，见 internal/migrate/apply.go），
-- 手工执行前请先确认列不存在。

ALTER TABLE company_wx_app
    ADD COLUMN upload_key VARCHAR(2048) NOT NULL DEFAULT '' COMMENT '小程序代码上传密钥（private.key 原文，只写不读）';
