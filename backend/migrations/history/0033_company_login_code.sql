-- 企业登录页白标码（?company=）：用于登录页按企业区分显示不同简称，对客户更友好。
-- 取值为小写字母 / 数字 / 连字符组成的 slug（如 langhui），前端以 ?company=langhui 传入。
--
-- 唯一性由后端在写入时校验（见 handler/companies.go），这里**不建唯一索引**：
-- 未配置白标码的企业该列为空串，多企业空串会互相冲突，故不依赖 DB 唯一约束，
-- 与「管理员用户名唯一」同为应用层校验（MySQL parity job 真跑，避免 SQLite/MySQL 索引不一致）。
-- 仅回传 short_name 作为登录品牌名，绝不泄露企业 ID / 名称 / 管理员等其它字段。
ALTER TABLE company ADD COLUMN login_code VARCHAR(64) NULL COMMENT '登录页白标码（?company=），仅精确匹配';
