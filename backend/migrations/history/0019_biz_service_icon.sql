-- 0019：业务服务图标（biz_services.icon）——首页服务区卡片展示用。
-- 背景：icon 随模型新增，SQLite（测试 / devserver）由 GORM AutoMigrate 自动建列；
-- MySQL 侧漏了对应迁移，compose MySQL 形态下业务服务增改一直报
-- "Unknown column 'icon'"（2026-10-21 MySQL parity 首跑抓出）。
-- 与 achievement_categories.icon（0001）同型：URL / 路径，512 足够。
-- 存量库需手动执行；重复执行报「列已存在」可忽略。

ALTER TABLE biz_services ADD COLUMN icon VARCHAR(512) NOT NULL DEFAULT '';
