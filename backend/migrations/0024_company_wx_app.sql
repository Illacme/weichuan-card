-- 0024：租户小程序凭据表（company_wx_app）——按企业隔离微信小程序 AppID / AppSecret。
--
-- 背景：小程序有两种形态（见 miniprogram/utils/tenant.ts）
--   1) 平台模式：一个小程序服务所有企业，凭据全局一份即可
--      （sys_setting 的 wx_appid / wx_appsecret）
--   2) 企业独立版：每企业一个小程序（pinnedCompanyId 锁定），**共用同一套后端**
--      一个后端要服务 N 个 AppID，全局那一份只能生成其中一个小程序的码——
--      其余企业的员工海报，码扫出来是别的小程序，直接打不开。
--
-- 所以凭据必须从「平台级」下沉到「租户级」。取值链三级回落：
--   租户凭据（本表） → 平台全局（sys_setting） → 环境变量
-- 平台模式不用给每个租户配，自动落到全局那一份；企业版配自己的即可。
--
-- 为什么不拆列/为什么不放 company 表：凭据是「集成配置」，与租户主数据
-- 变更频率、权限粒度都不同；独立表将来加「小程序名称 / 原始 ID / 多个小程序」
-- 不必动 company。company_id 唯一：一个企业一个小程序。
--
-- app_secret 以明文入库（与 sys_setting.wx_appsecret 同策略）：DB 落盘本身
-- 已由部署方控制（MySQL 不对外、SQLite 仅测试用），真正的防线是接口层——
-- 读取时只回掩码（见 handler 的 mask 常量），审计日志脱敏。
--
-- SQLite（测试 / devserver）由 GORM AutoMigrate 自动建表；
-- MySQL（生产 / parity）由本迁移补齐。存量库需手动执行；重复执行不会报错。

CREATE TABLE IF NOT EXISTS company_wx_app (
    id         BIGINT       NOT NULL AUTO_INCREMENT,
    company_id BIGINT       NOT NULL COMMENT '租户（company.id）',
    app_id     VARCHAR(64)  NOT NULL DEFAULT '' COMMENT '小程序 AppID（wx 开头）',
    app_secret VARCHAR(128) NOT NULL DEFAULT '' COMMENT '小程序 AppSecret',
    created_at DATETIME     NULL,
    updated_at DATETIME     NULL,
    PRIMARY KEY (id),
    UNIQUE KEY uk_company_wx_app_company (company_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='租户小程序凭据（按企业隔离，一个企业一个小程序）';
