-- 0031 PII 静态加密（第二阶段）：开通申请单 open_applies + 名片修改快照 card_modify_applies。
--
-- 延续 0030 员工表的方案（见 internal/pii 与 internal/model 的钩子/回填）：
--   * open_applies 的姓名/手机/手机2/邮箱/昵称：加宽列以容纳 base64 密文，另加盲索引；
--   * card_modify_applies.snapshot：整段 JSON 作为一个密文落库——密文是 base64 文本、
--     不是合法 JSON，故必须把列类型从 JSON 改为 LONGTEXT（否则 MySQL 插入直接报错）。
--
-- 存量数据由服务启动时的 model.BackfillPII 加密（Go 侧，AES 无法在纯 SQL 里做）。
-- 幂等性：MODIFY 可重复执行；ADD COLUMN 重复执行报「列已存在」可忽略。
-- 先清 NULL 再 MODIFY NOT NULL，避免历史可空列升级失败。

UPDATE open_applies SET name = ''     WHERE name IS NULL;
UPDATE open_applies SET phone = ''    WHERE phone IS NULL;
UPDATE open_applies SET phone2 = ''   WHERE phone2 IS NULL;
UPDATE open_applies SET email = ''    WHERE email IS NULL;
UPDATE open_applies SET nickname = '' WHERE nickname IS NULL;

ALTER TABLE open_applies MODIFY COLUMN name     VARCHAR(512) NOT NULL DEFAULT '';
ALTER TABLE open_applies MODIFY COLUMN phone    VARCHAR(512) NOT NULL DEFAULT '';
ALTER TABLE open_applies MODIFY COLUMN phone2   VARCHAR(512) NOT NULL DEFAULT '';
ALTER TABLE open_applies MODIFY COLUMN email    VARCHAR(512) NOT NULL DEFAULT '';
ALTER TABLE open_applies MODIFY COLUMN nickname VARCHAR(512) NOT NULL DEFAULT '';

ALTER TABLE open_applies ADD COLUMN name_bidx     VARCHAR(64) NOT NULL DEFAULT '';
ALTER TABLE open_applies ADD COLUMN phone_bidx    VARCHAR(64) NOT NULL DEFAULT '';
ALTER TABLE open_applies ADD COLUMN phone2_bidx   VARCHAR(64) NOT NULL DEFAULT '';
ALTER TABLE open_applies ADD COLUMN email_bidx    VARCHAR(64) NOT NULL DEFAULT '';
ALTER TABLE open_applies ADD COLUMN nickname_bidx VARCHAR(64) NOT NULL DEFAULT '';

-- snapshot：JSON -> LONGTEXT（存密文；原 JSON 明文值在本迁移后仍可读，由 Go 回填补加密）
ALTER TABLE card_modify_applies MODIFY COLUMN snapshot LONGTEXT;
