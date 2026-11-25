-- 0060：平台自身的 CDN 刷新凭据收进分发档案。
--
-- 背景：0059 把平台存储的连接信息收进了 storage_profiles 的 platform_primary，
-- 但 CDN 刷新那组 cdn_provider / cdn_access_key_id / cdn_access_key_secret /
-- cdn_resource_id / cdn_endpoint / cdn_enabled 仍留在 sys_setting。它们与
-- delivery_profiles 的 provider / access_key / secret_key / resource_id / endpoint
-- 是字段级重复：同一家 CDN 的同一份凭据要填两遍，改了一处另一处不动，而且
-- 两边都不会报错——覆盖了图、边缘仍是旧的。
--
-- 本迁移给 delivery_profiles 补 builtin / purpose，与 storage_profiles 同口径：
--   - 内置行由启动时 ensure 创建（凭据取决于部署环境与历史 sys_setting，迁移
--     脚本无法知道），purpose=platform_primary，不可删、不可绑定给企业；
--   - 平台对象的刷新器启动期由该档案构造，改完需重启；
--   - 平台分发档案的 domain 不参与出参：平台域名唯一真值源是 storage_profiles
--     里 platform_primary 的 base_url，避免同一个域名存两处。
--
-- 方言：SQLite（测试 / devserver）由 GORM AutoMigrate 按模型 tag 补齐；
-- MySQL（生产 / parity）由本迁移补齐。

ALTER TABLE delivery_profiles
  ADD COLUMN builtin TINYINT(1) NOT NULL DEFAULT 0 COMMENT '是否内置档案（启动时 ensure，不可删除、不可绑定给企业）';

ALTER TABLE delivery_profiles
  ADD COLUMN purpose VARCHAR(24) NOT NULL DEFAULT '' COMMENT '内置用途：platform_primary；空=普通档案';
