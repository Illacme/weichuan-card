-- 0026：company_wx_app 增加「是否允许平台代发小程序版本」开关。
--
-- 背景：平台超管可以代租户上传代码 / 提审 / 发布小程序版本（见 internal/handler/mp_release.go）。
-- 但企业独立版的小程序归企业自己管——平台不该拥有单方面推送上线的能力，
-- 企业需要一条能自己关掉代发的路。关掉后一切代发动作一律拒绝（service 层校验）。
--
-- 默认 1（允许）：与既有行为一致，不改变任何存量租户的现状。
-- 平台自有小程序（company_id=0）不落本表，天然没有这个开关——平台推自己的版本
-- 不需要谁来授权。
--
-- 为什么不做成 sys_setting 的全局开关：代发的授权粒度在「企业」这一层
-- （关的是「别家别动我的小程序」），不是平台功能总开关。平台真要全局停掉，
-- 不给入口即可（前端不显示），服务端这里的租户级拒绝是最后一道闸。
--
-- SQLite（测试 / devserver）由 GORM AutoMigrate 自动补列；
-- MySQL（生产 / parity）由本迁移补齐。存量库需手动执行。
-- 注意与 0024 不同：ALTER ... ADD COLUMN **不是幂等**的，对已存在该列的库
-- 再执行会报 Duplicate column。正常路径不会重放（migrate 包按 schema_migrations
-- 记账，见 internal/migrate/apply.go），手工执行前请先确认列不存在。
-- （注释里刻意不写出完整的改表语句样例：migrations_guard_test 的正则按语句
--   匹配表名且不剥注释，注释里出现表名样例会把它绊成「对不上任何模型」。）

ALTER TABLE company_wx_app
    ADD COLUMN release_enabled TINYINT(1) NOT NULL DEFAULT 1
        COMMENT '是否允许平台代发小程序版本（0=拒绝一切提审/发布/撤回）';
