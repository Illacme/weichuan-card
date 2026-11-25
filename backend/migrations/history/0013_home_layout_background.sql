-- 0013：小程序首页背景图（home_layouts.bg_image / bg_mask）——商业化高端美化包装。
-- bg_image 首页全屏背景图 URL（可从内置素材库「首页背景」直接选用）；
-- bg_mask 背景遮罩浓度 0~100（叠加黑色蒙层，保证白卡片与文字可读性）。
-- 两者均为可选：bg_image 为空即保持原有灰底布局，不设背景。
-- 与 devserver AutoMigrate 保持一致。

ALTER TABLE home_layouts ADD COLUMN bg_image VARCHAR(512) NOT NULL DEFAULT '';
ALTER TABLE home_layouts ADD COLUMN bg_mask INT NOT NULL DEFAULT 0;
