-- 0016 公司简介视频：单「视频号 + 降级 MP4」改为 video_source 五档多来源
--
-- 背景：原 company_profiles 只有 video_channel_id + backup_mp4_url，表达不了
-- 「云存储上传」与「外部 MP4 直链」的区别（前者走对象存储直传、后者是客户自备 URL），
-- 也无法让客户显式选档 + 单独开关兜底。现统一为：
--   video_source = none     不展示视频，显示封面占位（默认）
--                  channels 微信视频号 <channel-video feed-id>（零存储零流量，官方无资质要求）
--                  upload   云存储上传（对象存储直传，不经应用服务器）
--                  url      外部 MP4 直链
--                  tencent  腾讯视频插件（预留，服务端暂拒；插件须在 app.json 静态声明）
-- video_url 由 upload / url 两档共用，并作为 channels / tencent 开兜底时的回落源。
--
-- 只加列、不删列：backup_mp4_url 保留（代码已不再读写），出问题可直接回滚。

ALTER TABLE company_profiles
  ADD COLUMN video_source       VARCHAR(16)   NOT NULL DEFAULT '' AFTER intro_text,
  ADD COLUMN video_feed_id      VARCHAR(128)  NOT NULL DEFAULT '' AFTER video_channel_id,
  ADD COLUMN video_vid          VARCHAR(64)   NOT NULL DEFAULT '' AFTER video_feed_id,
  ADD COLUMN video_url          VARCHAR(512)  NOT NULL DEFAULT '' AFTER video_vid,
  ADD COLUMN video_fallback_url TINYINT(1)    NOT NULL DEFAULT 0 AFTER video_url;

-- 存量搬迁：先搬 URL，再按「有 URL 算直链、只有视频号 ID 算视频号」定档，最后兜底 none。
-- 顺序不能反——否则有 URL 的行会被 video_channel_id 抢先定成 channels。
UPDATE company_profiles SET video_url = backup_mp4_url WHERE video_url = '' AND backup_mp4_url <> '';
UPDATE company_profiles SET video_source = 'url'      WHERE video_source = '' AND video_url <> '';
UPDATE company_profiles SET video_source = 'channels' WHERE video_source = '' AND video_channel_id <> '';
UPDATE company_profiles SET video_source = 'none'     WHERE video_source = '';
