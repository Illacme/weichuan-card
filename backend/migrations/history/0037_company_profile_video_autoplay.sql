-- 0037 公司简介视频：是否自动播放，由后台显式控制
--
-- 背景：腾讯视频插件（provider wxa75efa648b60994b）的 <txv-video> 组件
-- **autoplay 默认值是 true**（官方参数表：autoplay boolean，默认「是」），
-- 而微信原生 <video> 的 autoplay 默认是 false。我们此前没显式传这个属性，
-- 于是「腾讯视频档」一进名片页就带着声音自己播起来——用户明确反馈这是负面体验。
--
-- 默认 0（不自动播放）：与原生 video 的默认一致，也是名片页该有的行为
-- （访客只是来看一张名片，不该被突然的声音打断）。
-- **这是一次有意的行为变更**：升级后腾讯视频档不再自动播放；
-- 需要自动播放的部署，请在「管理后台 → 内容 → 公司简介 → 腾讯视频」把开关打开。
--
-- 只加列、不动存量数据：存量行取默认值 0。

ALTER TABLE company_profiles
  ADD COLUMN video_autoplay TINYINT(1) NOT NULL DEFAULT 0 AFTER video_fallback_url;
