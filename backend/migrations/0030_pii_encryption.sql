-- 0030 员工 PII 静态加密：姓名 / 手机 / 手机2 / 邮箱 / 微信昵称。
--
-- 做法（见 internal/pii + internal/model 的钩子/回填）：
--   * 敏感字段在库里只存密文（AES-256-GCM，带 v1: 前缀），写回**原列名**，
--     所以先把这些列加宽——base64 密文比原文长，沿用旧宽度会被 MySQL 截断
--     （非严格模式静默截断 = 数据损坏，比报错更危险）。
--   * 可检索字段（姓名/手机/手机2/邮箱/昵称）另加 HMAC 盲索引列：
--     登录按手机号、后台按关键字检索都改为比对盲索引，库里看不到明文。
--
-- 存量数据由服务启动时的 Go 回填（model.BackfillEmployees）加密并补盲索引；
-- 本文件只改结构、不动数据（AES 无法在纯 SQL 里做）。
--
-- 幂等性：MODIFY 可重复执行；ADD COLUMN 重复执行会报「列已存在」，可忽略
-- （schema_migrations 记账后不会重放）。先清 NULL 再 MODIFY NOT NULL，避免
-- 历史可空列升级失败。

UPDATE employees SET name = ''     WHERE name IS NULL;
UPDATE employees SET phone = ''    WHERE phone IS NULL;
UPDATE employees SET phone2 = ''   WHERE phone2 IS NULL;
UPDATE employees SET email = ''    WHERE email IS NULL;
UPDATE employees SET nickname = '' WHERE nickname IS NULL;

ALTER TABLE employees MODIFY COLUMN name     VARCHAR(512) NOT NULL DEFAULT '';
ALTER TABLE employees MODIFY COLUMN phone    VARCHAR(512) NOT NULL DEFAULT '';
ALTER TABLE employees MODIFY COLUMN phone2   VARCHAR(512) NOT NULL DEFAULT '';
ALTER TABLE employees MODIFY COLUMN email    VARCHAR(512) NOT NULL DEFAULT '';
ALTER TABLE employees MODIFY COLUMN nickname VARCHAR(512) NOT NULL DEFAULT '';

ALTER TABLE employees ADD COLUMN name_bidx     VARCHAR(64) NOT NULL DEFAULT '';
ALTER TABLE employees ADD COLUMN phone_bidx    VARCHAR(64) NOT NULL DEFAULT '';
ALTER TABLE employees ADD COLUMN phone2_bidx   VARCHAR(64) NOT NULL DEFAULT '';
ALTER TABLE employees ADD COLUMN email_bidx    VARCHAR(64) NOT NULL DEFAULT '';
ALTER TABLE employees ADD COLUMN nickname_bidx VARCHAR(64) NOT NULL DEFAULT '';
