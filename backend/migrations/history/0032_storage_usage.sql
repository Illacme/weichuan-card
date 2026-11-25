-- 0032：按租户的存储用量累计表（storage_usage）——配额检查与用量展示。
--
-- 背景（B3）：此前上传只受「单文件上限 upload_max_mb」约束，**总用量没有任何上限**。
-- 一个租户可以无限上传：对象存储 / 磁盘被占满、按量计费的成本无法封顶，
-- 也谈不上给客户报价（没有用量就没有计价单位）。更麻烦的是连「谁占了多少」都答不上来。
--
-- 为什么是累计表而不是每次 SUM：
--   对象存储里没有「这个对象属于哪个租户」的元数据；本地驱动的 /uploads 是扁平目录，
--   反向统计无从做起。配额检查发生在每次上传之前，逐次 SUM 是 O(文件数) 的开销。
--   所以上传时增量累加，检查时一次主键查询。
--
-- 口径与代价（务必知悉）：**统计从本功能启用起累计**，历史文件不回溯计入；
-- Bytes 按上传时的文件大小计（剥 EXIF / 压缩后实际落盘可能更小，取偏大口径）。
-- 覆盖写（SaveBytes 那几条固定名）不扣减——这里统计的是「上传量」不是「存量」。
--
-- company_id 作主键：一个租户一行，company_id = 0（平台自身素材库）不计量。
-- quota_mb 是该租户的配额覆盖（0 = 跟随全局设置 storage_quota_mb）。
--
-- SQLite（测试 / devserver）由 GORM AutoMigrate 自动建表；
-- MySQL（生产 / parity）由本迁移补齐。存量库需手动执行；重复执行不会报错。

CREATE TABLE IF NOT EXISTS storage_usage (
    company_id BIGINT   NOT NULL COMMENT '租户（company.id），一个企业一行',
    bytes      BIGINT   NOT NULL DEFAULT 0 COMMENT '累计上传字节数（按上传文件大小计）',
    files      BIGINT   NOT NULL DEFAULT 0 COMMENT '累计上传文件数',
    quota_mb   BIGINT   NOT NULL DEFAULT 0 COMMENT '本租户配额覆盖（MB）；0 = 跟随全局 storage_quota_mb',
    updated_at DATETIME NULL,
    PRIMARY KEY (company_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='按租户累计的存储用量（配额检查用）';
