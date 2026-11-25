-- 0054：文件存储与分发档案化（storage_profiles / delivery_profiles / storage_bindings）
-- 以及一键迁移任务台账（storage_migration_jobs / storage_migration_errors）。
--
-- 为什么独立成表：sys_setting 是全局 key/value 单值，所有存储 / CDN 字段都标了
-- 「改完要重启」（见 setting registry）；tenant_storage_configs 又是一租户一整组
-- 配置，两套配置之间没有任何共享。客户要「多套配置反复用、默认一套、企业各选各的」时，
-- 这两处都装不下：前者没有多值，后者会把同一份 endpoint / AK / SK 复制 N 份。
--
-- 三层职责（勿合并）：
--   - storage_profiles   只存连接信息与凭据，不关心谁在用；
--   - delivery_profiles  只存 CDN 域名 / 回源标识，独立于存储（同桶多域名、同域名多桶）；
--   - storage_bindings   只存「谁用哪套」。tenant_id=0 表示全局默认；
--                        企业没有绑定行时继承全局，不复制任何配置。
--
-- 即时生效靠 bindings.version + profiles.version 参与 uploadroute 的缓存版本，
-- 不再依赖启动期 config.OverlayFromSettings 的快照（那是旧行为，保留一个版本只读兼容）。
--
-- 凭证明文入库（用户 2026-11-20 决策：暂不做加密存储），回显只给掩码。
-- 因此数据库备份与库文件访问控制必须受控；将来切 PII 加密需专门迁移（本项目无 re-key）。
--
-- 方言：SQLite 由 GORM AutoMigrate 建表；MySQL 8 由本迁移执行，不用 IF EXISTS 的 ALTER。

CREATE TABLE IF NOT EXISTS storage_profiles (
    id              BIGINT       NOT NULL AUTO_INCREMENT,
    name            VARCHAR(64)  NOT NULL COMMENT '档案名，展示用，唯一',
    driver          VARCHAR(16)  NOT NULL DEFAULT 'local' COMMENT 'local / minio / oss / s3',
    endpoint        VARCHAR(255) NOT NULL DEFAULT '' COMMENT 'host[:port]，不带 scheme',
    bucket          VARCHAR(128) NOT NULL DEFAULT '',
    region          VARCHAR(64)  NOT NULL DEFAULT '',
    base_url        VARCHAR(255) NOT NULL DEFAULT '' COMMENT '上传返回基址，恒为平台统一入口',
    path_style      TINYINT(1)   NOT NULL DEFAULT 0 COMMENT 'S3 是否 path-style',
    host_header     VARCHAR(255) NOT NULL DEFAULT '' COMMENT 'virtual-hosted-style 的 Host / TLS SNI',
    use_ssl         TINYINT(1)   NOT NULL DEFAULT 1 COMMENT 'S3/OSS 构造与自检是否走 HTTPS',
    local_dir       VARCHAR(512) NOT NULL DEFAULT '' COMMENT 'local 驱动的独立根目录，留空用平台 uploads',
    access_key      VARCHAR(512) NOT NULL DEFAULT '' COMMENT 'AccessKey 明文，回显掩码',
    secret_key      VARCHAR(1024) NOT NULL DEFAULT '' COMMENT 'SecretKey 明文，回显掩码且不回传',
    public_read     TINYINT(1)   NOT NULL DEFAULT 1 COMMENT '是否公开读；第一阶段只支持公开读桶',
    status          VARCHAR(16)  NOT NULL DEFAULT 'unknown' COMMENT 'unknown / ok / error',
    last_probe_at   DATETIME     NULL,
    last_error      VARCHAR(512) NOT NULL DEFAULT '' COMMENT '脱敏后的探测错误摘要',
    version         BIGINT       NOT NULL DEFAULT 1 COMMENT '每次保存递增，作为运行时缓存失效版本',
    builtin         TINYINT(1)   NOT NULL DEFAULT 0 COMMENT '内置 local 档案，不可删除',
    created_at      DATETIME     NULL,
    updated_at      DATETIME     NULL,
    PRIMARY KEY (id),
    UNIQUE KEY uk_storage_profiles_name (name)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='存储档案：连接信息与凭据，供多套配置复用';

CREATE TABLE IF NOT EXISTS delivery_profiles (
    id              BIGINT       NOT NULL AUTO_INCREMENT,
    name            VARCHAR(64)  NOT NULL COMMENT '分发档案名，唯一',
    provider        VARCHAR(24)  NOT NULL DEFAULT 'none' COMMENT 'none / aliyun / tencent / qiniu / cloudflare / cloudfront / custom',
    domain          VARCHAR(255) NOT NULL DEFAULT '' COMMENT '公开访问域名（含 scheme），只影响出参 URL',
    path_prefix     VARCHAR(128) NOT NULL DEFAULT '' COMMENT '域名下的目录前缀，拼 URL 时插入',
    access_key      VARCHAR(512) NOT NULL DEFAULT '' COMMENT 'CDN 刷新凭据，明文；回显掩码',
    secret_key      VARCHAR(1024) NOT NULL DEFAULT '' COMMENT 'CDN 刷新密钥，明文；不回传',
    endpoint        VARCHAR(255) NOT NULL DEFAULT '' COMMENT '刷新接口端点，留空按厂商默认',
    resource_id     VARCHAR(128) NOT NULL DEFAULT '' COMMENT 'Cloudflare Zone ID / CloudFront Distribution ID',
    status          VARCHAR(16)  NOT NULL DEFAULT 'unknown',
    last_probe_at   DATETIME     NULL,
    last_error      VARCHAR(512) NOT NULL DEFAULT '',
    version         BIGINT       NOT NULL DEFAULT 1,
    created_at      DATETIME     NULL,
    updated_at      DATETIME     NULL,
    PRIMARY KEY (id),
    UNIQUE KEY uk_delivery_profiles_name (name)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='分发档案：CDN 域名与刷新凭据，独立于存储档案';

CREATE TABLE IF NOT EXISTS storage_bindings (
    id                   BIGINT      NOT NULL AUTO_INCREMENT,
    tenant_id            BIGINT      NOT NULL COMMENT '0=全局默认；>0=企业 ID',
    storage_profile_id   BIGINT      NOT NULL,
    delivery_profile_id  BIGINT      NOT NULL DEFAULT 0 COMMENT '0=不启用 CDN，直接用存储基址',
    version              BIGINT      NOT NULL DEFAULT 1 COMMENT '每次绑定变更递增，触发运行时缓存失效',
    updated_by           VARCHAR(64) NOT NULL DEFAULT '',
    created_at           DATETIME    NULL,
    updated_at           DATETIME    NULL,
    PRIMARY KEY (id),
    UNIQUE KEY uk_storage_bindings_tenant (tenant_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='存储与分发绑定：全局默认 + 企业覆盖，企业无行即继承';

CREATE TABLE IF NOT EXISTS storage_migration_jobs (
    id                   BIGINT      NOT NULL AUTO_INCREMENT,
    tenant_id            BIGINT      NOT NULL DEFAULT 0 COMMENT '0=全部租户；>0=单企业',
    source_profile_id    BIGINT      NOT NULL,
    target_profile_id    BIGINT      NOT NULL,
    status               VARCHAR(16) NOT NULL DEFAULT 'created' COMMENT 'created/running/done/failed/canceled',
    total                BIGINT      NOT NULL DEFAULT 0,
    done                 BIGINT      NOT NULL DEFAULT 0,
    failed               BIGINT      NOT NULL DEFAULT 0,
    last_key             VARCHAR(512) NOT NULL DEFAULT '' COMMENT '断点续跑位点',
    dry_run              TINYINT(1)  NOT NULL DEFAULT 0,
    auto_switch          TINYINT(1)  NOT NULL DEFAULT 0 COMMENT '完成后是否自动把绑定切到目标档案',
    created_by           VARCHAR(64) NOT NULL DEFAULT '',
    error                VARCHAR(512) NOT NULL DEFAULT '',
    created_at           DATETIME    NULL,
    updated_at           DATETIME    NULL,
    finished_at          DATETIME    NULL,
    PRIMARY KEY (id),
    KEY idx_storage_migration_jobs_status (status)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='一键迁移任务：对象从源档案复制到目标档案';

CREATE TABLE IF NOT EXISTS storage_migration_errors (
    id         BIGINT      NOT NULL AUTO_INCREMENT,
    job_id     BIGINT      NOT NULL,
    object_key VARCHAR(512) NOT NULL,
    error      VARCHAR(512) NOT NULL DEFAULT '',
    created_at DATETIME    NULL,
    PRIMARY KEY (id),
    KEY idx_storage_migration_errors_job (job_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='迁移失败对象清单，可导出逐个重试';
