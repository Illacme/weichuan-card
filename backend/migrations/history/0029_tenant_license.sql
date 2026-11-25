-- 0029：多租户授权表（tenant_license）——按企业配置席位 / 到期时间 / 功能开关。
--
-- 背景：Phase 1~3 的授权是「全局一份」（sys_setting 的 license_*，或私有部署的
-- 签名许可证文件）。平台要按企业售卖或给不同企业不同额度时，全局一份不够用：
-- 企业 A 买 50 席、企业 B 买 10 席，共用一个 seats 就会互相挤占（Phase 3 之前
-- 甚至是全局计数，A 的员工直接顶掉 B 的额度）。
--
-- 取值链（见 internal/license/tenant_db.go 的 ResolveFor）：
--   本表该租户的行 → 全局系统设置（license_*）→ 无（不过期 / 不限席位）
-- 没给某租户单独配时自动落到全局那一份，行为与 Phase 1~3 完全一致——
-- 新增本表不会让存量部署「突然开始拦人」。
--
-- 私有 / 独立部署不走本表：那种形态只有单一租户，授权来自 data/license.key
-- 的 Ed25519 签名文件，启动时验签、过期即拒绝启动。
--
-- features 逗号分隔（空 = 全部开通，缺省放行），与 sys_setting.license_features
-- 同格式，避免两套解析逻辑。
--
-- SQLite（测试 / devserver）由 GORM AutoMigrate 自动建表；
-- MySQL（生产 / parity）由本迁移补齐。存量库需手动执行；重复执行不会报错。

CREATE TABLE IF NOT EXISTS tenant_license (
    id         BIGINT      NOT NULL AUTO_INCREMENT,
    company_id BIGINT      NOT NULL COMMENT '租户（company.id），唯一',
    edition    VARCHAR(32) NOT NULL DEFAULT '' COMMENT '版本标识：private | platform | trial（业务展示用）',
    seats      INT         NOT NULL DEFAULT 0 COMMENT '员工席位上限；0 = 不限制',
    expires_at DATETIME    NULL COMMENT '到期时间；NULL/零值 = 不过期',
    features   VARCHAR(512) NOT NULL DEFAULT '' COMMENT '功能开关，逗号分隔；空 = 全部开通',
    created_at DATETIME    NULL,
    updated_at DATETIME    NULL,
    PRIMARY KEY (id),
    UNIQUE KEY uk_tenant_license_company (company_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='多租户授权（按企业配置席位 / 到期 / 功能开关）';
