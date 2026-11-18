-- 0050：首页业绩分类 / 业务服务「封面图下方是否再显示名称」（home_layouts）。
--
-- 客户现状：设计稿把名称直接画进了封面图——业绩分类是「居住住宅」这类带字方图，
-- 业务服务是竖版卡片，图上已有标题。端上再渲染一行文字就是重复，所以默认关闭。
--
-- 需求：客户若把封面换成**不带字**的素图，要能打开开关让端上补回名称，
-- 显示在图片下方（样式复用既有的 .cat-name / .svc-title，四风格已有差异化字号）。
--
-- 拆成两列而不是一个总开关：业绩分类与业务服务常常不是同一批素材风格，
-- 典型情况是「分类换成素图、服务卡仍是带字卡」，共用一列会逼客户二选一。
--
-- 默认 false = 与加配置前完全一致（图上已有名称，不再重复渲染），升级零突变。
-- 只加列、不改存量数据；SQLite（测试 / devserver）由 GORM AutoMigrate 按模型 tag 补齐。
-- 重复执行不会报错（Duplicate column 由 migrate.applyFile 忽略）。

ALTER TABLE home_layouts
  ADD COLUMN show_category_name TINYINT(1) NOT NULL DEFAULT 0 COMMENT '业绩分类封面图下方是否显示名称，默认0=图内已有名称不重复',
  ADD COLUMN show_service_name TINYINT(1) NOT NULL DEFAULT 0 COMMENT '业务服务封面图下方是否显示名称，默认0=图内已有名称不重复';
