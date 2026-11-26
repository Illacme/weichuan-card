-- 0067：小程序展示配置版本号（home_layouts.config_version）。
--
-- 背景：公开接口 /api/home-layout 挂了 60 秒 CDN 缓存（cdnCache(c, 60)），
-- 后台保存配置后，访客端可能在缓存窗口内继续看到旧配置；微信开发者工具里
-- 已打开的页面也不会自己重新拉配置。起初想加「后台清缓存按钮」，但后台
-- 无法直接清用户设备 / 已打开页面的缓存，因此改为**版本号自动刷新**：
--
--   · 每次成功保存展示配置（PUT /api/admin/home-layout）都把 config_version +1；
--   · 小程序端用一个独立的 no-store 探针 GET /api/home-layout/version 检测
--     版本变化，发现变化就重新拉一次 /api/home-layout；
--   · 版本号不能塞进被 CDN 缓存的 /api/home-layout 响应体里——那样版本本身
--     也会被缓存 60 秒，端上永远晚一拍，等于没有检测。
--
-- 默认值 1：存量行升上来即为 1；端上首次读到的版本号记入 Storage，
-- 后续只要号变了就刷新，不会因为升级本身误触发一次刷新。
--
-- 方言：本文件只在 MySQL（生产 / parity）执行；SQLite（测试 / devserver）走
-- GORM AutoMigrate，按模型 default tag 建列，口径与这里一致。

ALTER TABLE home_layouts
  ADD COLUMN config_version BIGINT NOT NULL DEFAULT 1
  COMMENT '小程序展示配置版本号；保存时递增，端上检测变化后自动刷新';
