-- 0066：精选案例封面图的裁切比例（home_layouts.featured_ratio）。
--
-- 需求：除「按原图比例展示」外，还要支持 16:9 / 4:3 / 2:1 / 1:1 四种比例
-- 居中裁切——图片等比放大到短边贴合容器，长边溢出的部分从两端居中裁掉。
--
-- 配置设计为单列档位（而不是「是否裁切」布尔 + 另一个比例列）：
--   1. 比例只有五种互斥取值，单列枚举天然表达「选了 A 就没选 B」；
--   2. none 既是缺省也是「不裁切」档，升级存量行自动落 none，零视觉突变；
--   3. 拆成两列会出现「开了裁切但没选比例」这种无意义中间态，还得写兜底逻辑。
--
-- 小程序端不动态加载 WXSS，四个比例容器随包内置，后台只下发档位 id；
-- 非法值在 PUT 接口被拒绝（code=1），不会落进库里让端上落到空白态。
--
-- 方言：本文件只在 MySQL（生产 / parity）执行；SQLite（测试 / devserver）走
-- GORM AutoMigrate，按模型 default tag 建列，口径与这里一致。

ALTER TABLE home_layouts
  ADD COLUMN featured_ratio VARCHAR(16) NOT NULL DEFAULT 'none'
  COMMENT '精选案例封面裁切比例：none/16:9/4:3/2:1/1:1，默认 none 不裁切';
