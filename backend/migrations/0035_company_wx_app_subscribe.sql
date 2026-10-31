-- 0035：租户小程序「审核结果通知」订阅消息模板（company_wx_app.subscribe_tmpl_id / subscribe_tmpl_fields）。
--
-- 背景：订阅消息（一次性订阅）的模板**与 AppID 绑定**——一个 AppID 的账号下
-- 配的模板，另一个 AppID 用不了，微信会回「模板不存在」。平台模式（一个 AppID
-- 服务所有企业）用全局 sys_setting.wx_tmpl_audit 一份就够了；但企业独立版小程序
-- 每家企业一个 AppID，必须在**自己那个**微信账号里配模板，再落到本列。
--
-- 取值链（见 internal/handler/subscribe.go 的 auditTemplate）：
--   本企业是独立 AppID 且配了本列 → 用本列；
--   本企业是平台模式（无 own AppID）/ 本列留空 → 回落全局 sys_setting.wx_tmpl_audit。
-- 注意：已是独立 AppID 却没配本列时**不回落**（回落会发错模板），视为「该企业不发」。
--
-- 两列语义同 app_secret / upload_key：只写不读，接口层只回「配没配」，永不明文回传。
-- 字段名用逗号分隔、顺序固定为「事项 / 结果 / 备注」，留空即默认 thing1,phrase2,thing3。
--
-- 大小给到 128：模板 ID 约 40 字符、字段串（如 thing6,thing5,thing4）很短，留足余量。
--
-- SQLite（测试 / devserver）由 GORM AutoMigrate 自动补列；
-- MySQL（生产 / parity）由本迁移补齐。存量库需手动执行。
--
-- 与 0027 同：加列**不是幂等**的，对已存在该列的库再执行会报 Duplicate column。
-- 正常路径不会重放（migrate 包按 schema_migrations 记账，见 internal/migrate/apply.go），
-- 手工执行前请先确认列不存在。

ALTER TABLE company_wx_app
    ADD COLUMN subscribe_tmpl_id VARCHAR(128) NOT NULL DEFAULT '' COMMENT '审核结果通知订阅模板 ID（仅企业独立版小程序用，与 AppID 绑定）',
    ADD COLUMN subscribe_tmpl_fields VARCHAR(128) NOT NULL DEFAULT '' COMMENT '订阅模板字段名，逗号分隔，顺序=事项/结果/备注';
