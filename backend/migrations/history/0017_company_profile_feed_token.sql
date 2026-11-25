-- 0017 公司简介视频：新增 feed-token（跨主体通道）
--
-- 背景：channel-video 的 feed-id 官方明确「仅视频号视频与小程序同主体时生效」。
-- 平台模式下（我们的小程序服务多家客户），客户的视频号与我们小程序几乎必然不同主体，
-- feed-id 会直接播放失败。官方给的跨主体通道是 feed-token（基础库 2.31.1+，
-- 非个人主体小程序），代价是强制 autoplay=false（不能自动播放）。
--
-- 与 feed_id 二选一，feed_id 优先（同主体可自动播放、且从视频号助手复制即可，更稳定）。
-- feed-token 的获取有 24 小时窗口且只对操作者本人可见，属于需要人工协助配置的字段。

ALTER TABLE company_profiles
  ADD COLUMN video_feed_token VARCHAR(512) NOT NULL DEFAULT '' AFTER video_feed_id;
