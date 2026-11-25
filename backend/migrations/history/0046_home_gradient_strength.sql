-- 0046：首页顶部品牌主色浓度（home_layouts.bg_gradient_strength）。
--
-- 背景：小程序首页无背景图时，顶部渐变用品牌主色原色 var(--wc-primary) 还原
-- 客户设计稿（宝蓝 #0820d0 / 正红 #d40c0a）。但不同客户对「多浓」的偏好不同，
-- 有的嫌原色太扎眼、想要更柔和的过渡。现在把浓度交给租户自选：0~100 滑杆，
-- 注入渐变顶部的 alpha。
--
-- 默认值选 100 而不是较低值：升级后存量租户的顶部观感**零突变**，仍是原色；
-- 想柔和的管理员自己往下滑即可。这与 0040 默认 date「不消失」是同一原则。
--
-- 与 bg_mask 同属「名片页面样式」页签，和风格两轴（0042）分开写、互不冲掉。
-- 仅作用于无背景图时——设了 bg_image 时整幅渐变被背景层盖住，浓度自然无效。
--
-- 升级零突变：只加列、默认值固定、不搬迁任何存量数据。
-- SQLite（测试 / devserver）由 GORM AutoMigrate 按模型 tag 补齐；MySQL（生产 / parity）
-- 由本迁移补齐。重复执行不会报错（Duplicate column 由 migrate.applyFile 忽略）。

ALTER TABLE home_layouts
  ADD COLUMN bg_gradient_strength INT NOT NULL DEFAULT 100 COMMENT '首页顶部品牌主色浓度 0~100，100=原色';
