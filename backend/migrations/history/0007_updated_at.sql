-- 0007 项目案例 / 业务服务补充 updated_at：详情接口需返回「最后更新时间」
-- （见《跳转页组件与接口字段映射》§2.2/§3.2），PC 后台每次编辑由 GORM 自动维护。
-- SQLite（测试 / devserver）由 GORM AutoMigrate 自动建列；MySQL（生产 / parity）由本迁移补齐。
-- 存量库需手动执行；重复执行报「列已存在」可忽略。
-- MySQL 5.6.5+ 允许多个 DATETIME 列同带 CURRENT_TIMESTAMP 默认值，与 created_at 不冲突。

ALTER TABLE project_cases ADD COLUMN updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP;
ALTER TABLE biz_services ADD COLUMN updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP;
