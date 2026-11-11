-- 0042：小程序风格模板两轴化（style_id + brand_color_id）。
--
-- 背景：0041 把「选哪一套内置配色」存成单列 template_id（12 款平铺）。现在要把
-- 主题系统升级成「风格 × 色系」两轴（见 docs/theming-two-axis-design.md）：
-- style_id 决定各模块布局族（default/ink/warm/teal，default 即经典蓝基线），
-- brand_color_id 是叠加在风格之上的纯颜色层（NULL = 用该风格默认色）。
--
-- 存量映射（用户确认的零突变方案）：default/ink/warm/teal → (同名, NULL)；
-- 8 款 brand-* 全部归 default 风格下作色系，即 (default, brand-*)。
-- template_id 列保留作兼容派生列（写入时由两轴反推），不删除，便于回滚锚点。
--
-- 升级零突变：新增两列 + 按上述映射回填；不搬迁业务数据、不改任何展示。
-- SQLite（测试 / devserver）由 GORM AutoMigrate 按模型 tag 补齐新列（无默认值，
-- 存量行 style_id 为空时由后端回退从 template_id 反推）；MySQL（生产 / parity）
-- 由本迁移补齐并回填。重复执行不会报错（Duplicate column 由 migrate.applyFile 忽略）。
-- 回填用 UPDATE（非 ALTER 默认值），保证 brand-* 的 style_id 落到 default。

ALTER TABLE home_layouts
  ADD COLUMN style_id VARCHAR(32) NOT NULL DEFAULT 'default' COMMENT '整体风格（布局族）：default/ink/warm/teal';

ALTER TABLE home_layouts
  ADD COLUMN brand_color_id VARCHAR(32) DEFAULT NULL COMMENT '品牌色系（颜色层），NULL=用风格默认色；P1 仅 default 下挂 brand-*';

-- 存量回填：风格款各归其风格，品牌款统一归 default。
UPDATE home_layouts SET style_id = 'ink',     brand_color_id = NULL WHERE template_id = 'ink';
UPDATE home_layouts SET style_id = 'warm',    brand_color_id = NULL WHERE template_id = 'warm';
UPDATE home_layouts SET style_id = 'teal',    brand_color_id = NULL WHERE template_id = 'teal';
UPDATE home_layouts SET style_id = 'default', brand_color_id = template_id WHERE template_id LIKE 'brand-%';
-- template_id 为 default / 空 / 其它未知值的行保持 style_id='default'、brand_color_id=NULL。
