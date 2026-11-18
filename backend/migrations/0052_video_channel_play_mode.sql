-- 0052 视频号播放方式：内嵌播放 / 跳转打开。
--
-- 背景：此前视频号档只有 <channel-video> 内嵌播放一条路，跨主体要靠 feed-token，
-- 且受基础库与非个人主体限制。wx.openChannelsActivity 走跳转打开，无主体要求，
-- 适合只是想跳到视频号视频页的客户。两者是并列能力，需由后台显式选择，不能自动切换。
--
-- 默认 embed 保证升级前后视觉零变化，历史行读到空串时由后端归一层兜底。
-- 本文件只在 MySQL 下由 migrate 应用，SQLite（测试 / devserver）走 GORM AutoMigrate。

ALTER TABLE company_profiles
  ADD COLUMN video_channel_play_mode VARCHAR(16) NOT NULL DEFAULT 'embed' COMMENT '视频号播放方式 embed=内嵌/redirect=跳转打开';
