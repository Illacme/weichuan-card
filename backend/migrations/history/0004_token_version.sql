-- 0004 令牌安全戳：用于 JWT 即时吊销（改密 / 账号禁用时自增 token_version）。
-- SQLite（测试）由 GORM AutoMigrate 自动建列；MySQL（生产 / parity）由本迁移补齐。

ALTER TABLE employees ADD COLUMN token_version INT NOT NULL DEFAULT 0;
ALTER TABLE admins ADD COLUMN token_version INT NOT NULL DEFAULT 0;
