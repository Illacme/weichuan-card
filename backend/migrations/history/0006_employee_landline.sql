-- 0006 员工座机（landline）：员工个人办公电话，与 company_infos.tel（公司总机）分层。
-- 小程序名片「座机」行优先展示 employees.landline，为空时回退公司总机（见 employees-card 组件）。
-- SQLite（测试 / devserver）由 GORM AutoMigrate 自动建列；MySQL（生产 / parity）由本迁移补齐。
-- 存量库需手动执行；重复执行报「列已存在」可忽略。

ALTER TABLE employees ADD COLUMN landline VARCHAR(32) NOT NULL DEFAULT '';
