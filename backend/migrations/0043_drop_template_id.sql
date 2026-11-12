-- 0043：删除 home_layouts.template_id 兼容派生列。
--
-- 背景：0041 引入单列 template_id（12 款平铺内置配色）；0042 升级为两轴
-- (style_id + brand_color_id) 后，template_id 仅作「旧端兼容近似值」派生列，
-- 由两轴反推得来，且不可逆（12 款平铺装不下 4 风格 × 9 色档的 36 组合）。
--
-- 收敛时机：P5 已落地 36 组合全叉乘，前端/小程序端均已直接读写两轴
-- （小程序端保留 splitTheme 做旧缓存零迁移，不再回写 template_id）。
-- 兼容列完成历史使命，删除以收敛存储真值，避免「两轴 vs 单列」双写歧义。
--
-- 存储真值唯一化：删除后 style_id / brand_color_id 为唯一真值，
-- 公开接口与渲染逻辑不再需要「两轴反推 template_id」再「template_id 反推两轴」的折返。
--
-- 升级零突变：只删列、不搬迁任何业务数据。存量行在 0042 已回填两轴，
-- 删列对其展示无影响；SQLite（测试 / devserver）由 GORM AutoMigrate 管表结构，
-- 本迁移仅在 MySQL（生产 / parity）执行。重复执行安全：DROP COLUMN IF EXISTS
-- 在 MySQL 8.0 幂等（列已不存在时静默跳过，不报错）。

ALTER TABLE home_layouts DROP COLUMN IF EXISTS template_id;
