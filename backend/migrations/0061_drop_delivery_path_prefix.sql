-- 0061：删掉 delivery_profiles.path_prefix，这一列从未真正改变出参。
--
-- 背景：0054 建表时给它写的用途是「域名下的目录前缀，拼 URL 时插入」。
-- 全仓唯一读取它的地方是 Resolver.PublicURL，而 PublicURL 在生产代码里没有任何调用方；
-- 真正拼企业出参的 ResolveRef / OriginFor 只看 delivery_profiles.domain，
-- 且域名校验明确拒绝带路径。于是管理员填了 pathPrefix 只会得到「填了没用」的错觉。
--
-- 与 0058 删 storage_profiles.path_style 同一口径：模型、写接口、出参、表单、契约
-- 一并移除，避免库里留一个谁都不消费的开关。
--
-- 方言：SQLite（测试 / devserver）由 GORM AutoMigrate 按模型处理，不主动删列，
-- 但模型不再读写它，留列无害。MySQL（生产 / parity）由本迁移删列。
-- 重复执行由 schema_migrations 记账，另有 ignoredErrs 的 1091 兜底。

ALTER TABLE delivery_profiles
  DROP COLUMN path_prefix;
