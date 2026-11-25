-- 0018 员工第二个手机号（phone2）：名片「手机」行下方并列展示，选填，不参与登录。
-- 与 landline 同一套路：员工自助申请（open_applies）与员工表（employees）都要有，
-- 审核通过时由申请行搬到员工行（见 handler/apply.go 的 approve）。
-- SQLite（测试 / devserver）由 GORM AutoMigrate 自动建列；MySQL（生产 / parity）由本迁移补齐。
-- 存量库需手动执行；重复执行报「列已存在」可忽略。

ALTER TABLE employees ADD COLUMN phone2 VARCHAR(32) NOT NULL DEFAULT '';

-- 申请开通时收集第二个手机号，审核通过后一并写入员工表
ALTER TABLE open_applies ADD COLUMN phone2 VARCHAR(32) NOT NULL DEFAULT '';
