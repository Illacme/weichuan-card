-- 0044：租户自有存储配置表（tenant_storage_configs）——按企业登记自己的对象桶。
--
-- 背景：目前所有上传都落在**平台**那一套存储上（sys_setting 的 storage_driver
-- 等全局单值）。客户要求「文件存进我自己公司的桶」时，全局单值装不下第二个租户——
-- 和 0024 的小程序凭据是同一类问题，只是这次带上「数据主权」性质：
-- 客户不是想要一个开关，而是想要「我的文件不落在你机器上」。
--
-- 为什么不放 sys_setting：sys_setting 是全局单值表（key/value），没有租户维度。
-- 硬塞成 storage_cid5_endpoint 这类键会让配置项注册表、掩码下发、重启标记
-- 全部失效，而且行数随租户线性膨胀。独立表是唯一合理落点。
--
-- 与本表的读写边界（非常重要）：
--   - 只有平台超管可写（设计稿 §1.1）。企业管理员侧不暴露写接口、菜单隐藏、
--     连读接口都不给。企业管理员能改自己的桶地址 = 能把全企业的图指到任意主机，
--     而且出参 URL 与对象键都跟着变，事后无从追溯是谁改的。
--   - access_key_enc / secret_key_enc 是**密文**列，写入必须经 pii.Encrypt
--     （与 0024 的 app_secret 明文策略不同：那次的理由是「只回掩码」，
--     而本表的凭据要参与真实 S3 请求签名，漏一次明文就在日志/报错里带出去了）。
--     模型上两列 json:"-"，永远不参与序列化。
--   - 第一阶段 public_base_url 留空 = 走平台统一入口（设计稿 §3.2）：客户自有
--     域名上线还牵扯备案与证书，不是这一轮的事。列先建好，省得下一轮再动表。
--
-- host_header 为什么必须在表里，而不是「从 endpoint 推」：
--   virtual-hosted-style 的桶名在 Host 里，且这个 Host 同时是 TLS SNI 的取值。
--   第五十五轮 R1 用真桶实测过：只给 proxy_ssl_verify + 证书 + ssl_name 而漏掉
--   SNI，请求被桶以 421 拒绝。而「桶名该拼在 endpoint 前面还是后面」逐厂商不同
--   （MinIO 走 path-style，OSS/S3 走 virtual-hosted-style），推导等于猜。
--   所以给一列让超管**照控制台抄**，比写一段「智能推断」可靠。
--
-- 只建表，不写任何业务数据 / 演示配置（零数据是商用版的默认状态，
-- 种子数据归 internal/demoseed）。
--
-- SQLite（测试 / devserver）由 GORM AutoMigrate 自动建表；MySQL（生产 / parity）
-- 由本迁移补齐。重复执行不会报错：建表语句自带 IF NOT EXISTS，MySQL 8.0 认这个。
-- （注释里别把「建表 + IF NOT EXISTS」连成一个短语写出来——守门测试
--   TestMigrationsTableNamesMatchModels 直接扫原文不剥注释，会把紧跟其后的
--   中文当成表名，报出一张叫 IF 的幽灵表。）
--
-- 方言钉子：MySQL 8.0 不支持 MariaDB 那套 ADD/DROP COLUMN IF EXISTS 写法
-- （解析阶段直接 1064，整份迁移失败且不记账，见 0043 的线上事故）。
-- 本文件不涉及任何 ALTER，别在下一次改表时忘掉这条。

CREATE TABLE IF NOT EXISTS tenant_storage_configs (
    id               BIGINT       NOT NULL AUTO_INCREMENT,
    company_id       BIGINT       NOT NULL COMMENT '租户（company.id）；0 不作为合法值，平台自身走全局配置',
    driver           VARCHAR(16)  NOT NULL DEFAULT 'local' COMMENT 'local / minio / oss / s3，与 config.StorageDriver 枚举一致',
    endpoint         VARCHAR(255) NOT NULL DEFAULT '' COMMENT 'host[:port]，不带 scheme（带 scheme 会被 nginx 拼成 http://https://）',
    bucket           VARCHAR(128) NOT NULL DEFAULT '' COMMENT '桶名',
    region           VARCHAR(64)  NOT NULL DEFAULT '' COMMENT '区域（OSS/S3 需要，MinIO 可空）',
    use_ssl          TINYINT(1)   NOT NULL DEFAULT 0 COMMENT '是否 https。为 1 时 snippet 必须同时给 proxy_ssl_server_name + ssl_name，否则真桶 421',
    host_header      VARCHAR(255) NOT NULL DEFAULT '' COMMENT '发给桶的 Host（virtual-hosted-style 的桶名在这里，也是 TLS SNI 取值）；空则退化成 endpoint',
    access_key_enc   VARCHAR(512) NOT NULL DEFAULT '' COMMENT 'AccessKey 密文（pii.Encrypt 产出，带 v1: 前缀）；模型 json:"-"',
    secret_key_enc   VARCHAR(512) NOT NULL DEFAULT '' COMMENT 'SecretKey 密文（pii.Encrypt 产出，带 v1: 前缀）；模型 json:"-"',
    key_id           VARCHAR(32)  NOT NULL DEFAULT '' COMMENT '加密密钥版本标识，为将来换钥匙留口子（换钥时据此判断哪些行需重加密）',
    public_base_url  VARCHAR(255) NOT NULL DEFAULT '' COMMENT '客户自有访问域名（含 scheme）；第一阶段留空 = 走平台统一入口',
    public_read      TINYINT(1)   NOT NULL DEFAULT 0 COMMENT '桶是否公开读；第一阶段试点要求为 1（私有桶要靠预签名，出参契约会变）',
    enabled          TINYINT(1)   NOT NULL DEFAULT 0 COMMENT '开关。关掉即回到平台存储；注意 driver=local 时同样不生成 snippet（见 Target.NeedsSnippet）',
    status           VARCHAR(16)  NOT NULL DEFAULT 'unknown' COMMENT '连通性自检结果：unknown / ok / error',
    last_checked_at  DATETIME     NULL COMMENT '最近一次自检时间',
    last_error       VARCHAR(512) NOT NULL DEFAULT '' COMMENT '最近一次自检的脱敏错误摘要（不含凭据、不含完整响应体）',
    created_at       DATETIME     NULL,
    updated_at       DATETIME     NULL,
    PRIMARY KEY (id),
    UNIQUE KEY uk_tenant_storage_configs_company (company_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='租户自有存储配置（按企业登记对象桶，只有平台超管可写）';
