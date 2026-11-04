-- 0036：两张富文本列 TEXT → MEDIUMTEXT（正文含图片/视频块后 64KB 不够用）。
--
-- 背景：富文本由 wangEditor 产出（段落、标题、列表、图片、<wx-video> 视频块）。
-- TEXT 上限 64KB——纯文字够用，但一旦正文里放进几张图片：
--   · 图片若走对象存储 URL（本项目的方式），每条 <img> 约 100~200 字节，
--     64KB 仍可容纳 300 张以上，看似够用；
--   · 但编辑器若被配上「base64 直插」或粘贴富文本时带 data: URI，一张
--     100KB 的图就是 ~133KB 的 base64，**一张图即超限**（MySQL 严格模式报
--     1406 Data too long，非严格模式静默截断——正文后半段凭空消失）。
-- 视频块本身很小（<wx-video ...></wx-video> 几十字节），但长图文 + 多视频
-- 的富文本已经现实接近上限。
--
-- MEDIUMTEXT 上限 16MB（16777215 字节，按字节计、与字符集无关），足够 200 张图的正文，
-- 且不必把列放宽到 LONGTEXT（4GB，夸张且不利于行外存储的边界判断）。
--
-- 影响面：纯扩容，不改语义，存量数据无损（MODIFY 只改类型上限）。
-- 注意：MODIFY 会重建表（InnoDB 需拷数据），大表请在低峰执行；
-- 本项目的这两张表是内容表（项目案例 / 业务服务），行数很小，可忽略。
--
-- SQLite（测试 / devserver）由 GORM AutoMigrate 按模型 tag 维护；
-- MySQL（生产 / parity）由本迁移补齐。

ALTER TABLE project_cases MODIFY COLUMN detail MEDIUMTEXT;
ALTER TABLE biz_services MODIFY COLUMN content MEDIUMTEXT;
