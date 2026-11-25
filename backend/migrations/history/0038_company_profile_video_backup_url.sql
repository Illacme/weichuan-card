-- 0038 公司简介视频：自托管 MP4 的兜底（备用）地址
--
-- 背景：video_fallback_url 是「是否启用兜底」的布尔开关，而兜底地址与
-- upload / url 两档的主视频源共用 video_url 一列。于是自托管 MP4 档没法再
-- 填第二个地址——填了也会在保存时被主源的值覆盖（多字段共用一个槽）。
-- 视频号 / 腾讯视频两档因此早就有「兜底 MP4」，唯独自托管 MP4 档没有。
--
-- 本列只服务 upload / url 两档：主 MP4 加载失败时自动切换到这里。
-- channels / tencent 两档继续沿用 video_url 作兜底，存量行为完全不变。
-- 是否启用仍由 video_fallback_url 这一个开关控制，四种档位语义一致。
--
-- 为什么不复用 0001 里的 backup_mp4_url：0016 把它的值搬进 video_url 后
-- 没有清空，老库里那一列残留的是「旧版主视频地址」。重新启用会让客户一打开
-- 开关就播一段早该下线的旧视频，属于静默的存量突变，比加一列危险得多。
--
-- 只加列、默认空串、不搬迁任何存量数据：升级后开关默认关，行为零变化。

ALTER TABLE company_profiles
  ADD COLUMN video_backup_url VARCHAR(512) NOT NULL DEFAULT '' AFTER video_url;
