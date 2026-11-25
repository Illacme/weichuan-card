-- 0058：删掉 storage_profiles.path_style，这一列从未被运行时消费。
--
-- 背景：S3 路径风格（桶名进 path 还是进 Host）由 nginxsnippet.Target.PathStyle()
-- 单点判定，唯一输入是 host_header：留空或等于 endpoint 走路径风格，以
-- <bucket>. 开头走虚拟主机风格。上传侧与回填侧都走 storage.S3BucketLookup，
-- 这条链路上没有任何地方读 storage_profiles.path_style。
--
-- 于是它成了僵尸字段：库里能存、接口能读写，但改它不会改变任何行为。
-- 管理员若照着旧说明填它，只会得到「填了没用」的错觉。前端表单早已不展示这一列，
-- 本次把它从模型、接口和契约里一并清掉，避免以后有人误以为它是开关。
--
-- 方言：SQLite（测试 / devserver）由 GORM AutoMigrate 按模型处理，不主动删列，
-- 但模型不再读写它，留列无害。MySQL（生产 / parity）由本迁移删列。
-- 重复执行由 schema_migrations 记账，另有 ignoredErrs 的 1091 兜底。

ALTER TABLE storage_profiles
  DROP COLUMN path_style;
