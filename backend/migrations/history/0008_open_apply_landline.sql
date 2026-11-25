-- 0008：开通申请增加座机字段（与 devserver AutoMigrate 保持一致）
-- 申请开通时收集座机，通过审核后一并写入员工表（employees 的 landline 已在 0006 加入）。

ALTER TABLE open_applies ADD COLUMN landline VARCHAR(32) NOT NULL DEFAULT '';
