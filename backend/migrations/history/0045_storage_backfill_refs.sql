-- 0045：存量回填台账（storage_backfill_refs）
--
-- 为什么台账必须在业务库里、而不是一个 JSON 文件：P3 把「复制对象」和「改库」
-- 分成两步，前者在对象存储上、不可回滚；后者在 MySQL 上、可以回滚。回滚的依据
-- 就是这张表里每一行的 old_value。若台账写在文件里，「库改成功了、台账写失败」
-- 这个窗口会让那批行永远回不去，而且没人知道缺了哪几条。放进同一个库后，
-- 改业务行与记台账可以放在一个事务里：要么都成，要么都不成。
--
-- 一行 = 一条被改写过的资源引用（数组列按元素计）。plan_hash + ref_id 唯一：
-- 同一份计划重跑时命中旧行即跳过，这是幂等的判据；不同计划改同一行时，
-- 预检会发现库里的当前值已经不是那份计划的 raw，直接停下而不是覆盖。
--
-- 对象复制状态刻意不入库：目标对象是否存在、大小是否一致，用 Stat 就能判定，
-- 那是更可靠的真相（复制成功但记账失败仍会留下一个完整对象）。复制只增不删，
-- 回滚也**不删对象**（R5：解约 / 换桶后旧对象留着做兜底）。
CREATE TABLE IF NOT EXISTS storage_backfill_refs (
    id           BIGINT       NOT NULL AUTO_INCREMENT,
    plan_hash    VARCHAR(64)  NOT NULL COMMENT '计划文件字节的 sha256，标识一次搬迁',
    ref_id       VARCHAR(64)  NOT NULL COMMENT '引用身份的 sha256：表 + 主键 + 列 + 数组下标',
    table_name   VARCHAR(64)  NOT NULL COMMENT '给人看的溯源信息，不用于拼 SQL',
    pk_col       VARCHAR(64)  NOT NULL,
    pk_value     BIGINT       NOT NULL,
    ref_col      VARCHAR(64)  NOT NULL,
    array_index  INT          NOT NULL DEFAULT -1 COMMENT '-1 = 单值列，>=0 = JSON 数组下标',
    old_value    TEXT         NOT NULL COMMENT '改库前的原值，回滚依据',
    new_value    VARCHAR(512) NOT NULL COMMENT '改库后的对象键',
    company_id   BIGINT       NOT NULL,
    status       VARCHAR(16)  NOT NULL DEFAULT 'applied' COMMENT 'applied / rolled_back',
    created_at   DATETIME     NULL,
    updated_at   DATETIME     NULL,
    PRIMARY KEY (id),
    UNIQUE KEY uk_backfill_refs_plan_ref (plan_hash, ref_id),
    KEY idx_backfill_refs_plan_status (plan_hash, status)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='存量资源引用搬迁台账（P3）';
