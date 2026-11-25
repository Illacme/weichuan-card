-- 0025：小程序版本发布记录表（mp_release）——平台代租户上传 / 提审 / 发布。
--
-- 背景：产品支持「企业独立小程序」（每企业一个 appid，共用同一套后端，见
-- migrations/0024）。版本发布原先只能由租户自己登进微信公众平台点，平台侧
-- 既看不到进度，也没有痕迹。本表把「哪一版、审到哪了、谁操作的」落下来。
--
-- 三条设计取舍：
--   1) **不存代码包**。代码由 CI 或后台自己上传（后台那条路见
--      internal/mpupload 与 migrations/0027 的上传密钥），本表只存版本号与状态。
--      真要回溯代码，按 user_version 去构建记录里找——代码和它的构建上下文是
--      一体的，拆开存两边都会对不上。
--   2) **audit_id 不建唯一索引**。同一 auditid 只会有一行在审，但驳回后重新
--      提审会拿到新的 auditid；若给 audit_id 加唯一约束，「重新提审」就得先
--      清空旧值，反而容易写出「审计单 id 丢失」的脏数据。
--   3) **三个时间戳可空**。未提审的版本没有 submitted_at / released_at，
--      用 DATETIME NULL 而不是 '1970-01-01' 这种哨兵——空值在 SQL 里就能过滤，
--      哨兵值要靠应用层记着特殊值是什么。
--
-- source 取值：ci（CI 传的）/ manual（人工登记）/ backend（后台自己传的，
-- 版本号与代码必然一致，见 migrations/0027）。
--
-- 状态机见 internal/model/mp_release.go 的 canTransition，**只有白名单内的
-- 流向才允许发生**。注意微信侧约束：提审消耗审核额度，撤回不返还。
--
-- SQLite（测试 / devserver）由 GORM AutoMigrate 自动建表；
-- MySQL（生产 / parity）由本迁移补齐。存量库需手动执行；重复执行不会报错。

CREATE TABLE IF NOT EXISTS mp_release (
    id           BIGINT       NOT NULL AUTO_INCREMENT,
    company_id   BIGINT       NOT NULL DEFAULT 0 COMMENT '租户（company.id）；0 = 平台自有小程序',
    app_id       VARCHAR(64)  NOT NULL DEFAULT '' COMMENT '本次发布打到哪个小程序',
    user_version VARCHAR(64)  NOT NULL DEFAULT '' COMMENT '微信侧代码版本号（CI 上传时自定义）',
    user_desc    VARCHAR(255) NOT NULL DEFAULT '' COMMENT '版本说明',
    status       VARCHAR(32)  NOT NULL DEFAULT 'draft' COMMENT '状态机当前态，见 model/mp_release.go',
    audit_id     BIGINT       NOT NULL DEFAULT 0 COMMENT '微信审核单 id（提审返回）；未提审为 0',
    audit_reason VARCHAR(500) NOT NULL DEFAULT '' COMMENT '驳回/延后原因（微信原文）',
    source       VARCHAR(32)  NOT NULL DEFAULT '' COMMENT '登记来源：ci / manual',
    operator_id  BIGINT       NOT NULL DEFAULT 0 COMMENT '最后一步操作人（admin.id）',
    submitted_at DATETIME     NULL COMMENT '提审时间',
    released_at  DATETIME     NULL COMMENT '上线时间',
    created_at   DATETIME     NULL,
    updated_at   DATETIME     NULL,
    PRIMARY KEY (id),
    KEY idx_mp_release_company_status (company_id, status),
    KEY idx_mp_release_app_version (app_id, user_version)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='小程序版本发布记录（平台代租户上传/提审/发布）';
