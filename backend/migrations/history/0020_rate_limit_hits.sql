-- 0020：限流计数表（rate_limit_hits）——多副本共享的限流分桶。
-- 背景：进程内限流器在多副本下每个副本各有一份桶，实际阈值变成
-- 「单副本阈值 × 副本数」——登录防爆破从 10 次/分悄悄翻倍，且不报错。
-- 计数落到同一张表后，同一窗口内所有副本打同一行，阈值与副本数无关。
-- 详见 internal/model/models.go 的 RateLimitHit 注释。
-- SQLite（测试 / devserver）由 GORM AutoMigrate 自动建表；MySQL（生产 / parity）由本迁移补齐。
-- 存量库需手动执行；本语句自带存在性兜底，重复执行不会报错。

CREATE TABLE IF NOT EXISTS rate_limit_hits (
    bucket_key   VARCHAR(191) NOT NULL COMMENT '分桶键，如 login:1.2.3.4',
    window_start BIGINT       NOT NULL COMMENT '窗口起始 Unix 秒（对齐到窗口边界）',
    hits         INT          NOT NULL DEFAULT 0 COMMENT '该窗口内累计次数',
    PRIMARY KEY (bucket_key, window_start)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='限流计数（多副本共享，按窗口聚合）';

-- 过期窗口行由 repository.StartRateLimitPurge 定期清理，这里不建定时任务。
