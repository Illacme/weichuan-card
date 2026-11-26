-- 0065：精选案例「封面图下方是否显示名称」（home_layouts）。
--
-- 背景：0050 已经给业绩分类 / 业务服务加了「封面图下方显示名称」开关。设计稿里
-- 精选案例的名称也是直接画进封面图的，端上再渲染一行就重复，所以同样默认关闭。
-- 客户换成不带字的素图后，打开这个开关即可让端上在图片下方补回案例名称。
--
-- 为什么单独一列而不是复用 show_category_name：三块素材的更换节奏彼此独立——
-- 常见情况是「分类换成素图、案例仍是带字卡」，共用一列会逼客户二选一。
-- 与 0050 保持同一口径：默认 false = 与加配置前完全一致，升级零突变。
--
-- 方言：本文件只在 MySQL（生产 / parity）执行；SQLite（测试 / devserver）走
-- GORM AutoMigrate，按模型 default tag 建列，口径与这里一致。

ALTER TABLE home_layouts
  ADD COLUMN show_featured_name TINYINT(1) NOT NULL DEFAULT 0
  COMMENT '精选案例封面图下方是否显示名称，默认0=图内已有名称不重复';
