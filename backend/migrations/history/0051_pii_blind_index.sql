-- 0051 PII 盲索引补数据库索引。
--
-- 背景：0030 / 0031 只给 employees / open_applies 加了 name_bidx、phone_bidx、
-- phone2_bidx、email_bidx、nickname_bidx 五列，但**没有加数据库索引**。
-- 盲索引列存的是 HMAC 定长值，登录与判重全靠等值命中：
--   * /api/admin/login：WHERE phone_bidx = ?（全表）
--   * phoneInUse：WHERE company_id = ? AND phone_bidx = ?（employees）
--   * phoneInUse：WHERE company_id = ? AND phone_bidx = ? AND status = ?（open_applies）
--   * 后台按关键字搜申请/员工：name_bidx = ? OR phone_bidx = ? OR ...
-- 没有索引时这些查询退化为全表扫描；数据量上来后，登录与开通申请会越来越慢。
--
-- 列顺序刻意选 (bidx, company_id)，不是 (company_id, bidx)：
--   * 登录没有 company_id 条件，只有 bidx 打头才能命中最左前缀；
--   * 带 company_id 的判重同样能用（两列都在索引里，与 WHERE 书写顺序无关）；
--   * 一条索引同时服务两种查询，避免为同一列建两条重复索引。
-- status 不进索引：phone_bidx 已把候选收敛到极少行，再过滤 status 不值得多占写成本。
--
-- 幂等：MySQL 8.0 不支持 ADD INDEX IF NOT EXISTS，重复执行报 1061
-- （Duplicate key name），由 migrate.ignoredErrs 忽略；版本记账后正常不会重放。
-- SQLite（测试 / devserver）走 GORM AutoMigrate，不执行本文件，无需兼容其方言。

ALTER TABLE employees
  ADD INDEX idx_emp_name_bidx (name_bidx, company_id);

ALTER TABLE employees
  ADD INDEX idx_emp_phone_bidx (phone_bidx, company_id);

ALTER TABLE employees
  ADD INDEX idx_emp_phone2_bidx (phone2_bidx, company_id);

ALTER TABLE employees
  ADD INDEX idx_emp_email_bidx (email_bidx, company_id);

ALTER TABLE employees
  ADD INDEX idx_emp_nickname_bidx (nickname_bidx, company_id);

ALTER TABLE open_applies
  ADD INDEX idx_open_apply_name_bidx (name_bidx, company_id);

ALTER TABLE open_applies
  ADD INDEX idx_open_apply_phone_bidx (phone_bidx, company_id);

ALTER TABLE open_applies
  ADD INDEX idx_open_apply_phone2_bidx (phone2_bidx, company_id);

ALTER TABLE open_applies
  ADD INDEX idx_open_apply_email_bidx (email_bidx, company_id);

ALTER TABLE open_applies
  ADD INDEX idx_open_apply_nickname_bidx (nickname_bidx, company_id);
