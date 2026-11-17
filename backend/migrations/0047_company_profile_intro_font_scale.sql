-- 0047：公司简介「简介文字」字号档位（company_profiles.intro_font_scale）。
--
-- 背景：小程序首页简介文字此前没有独立字号，继承 page 的 28rpx。客户反馈
-- 不同企业对这个字号诉求不同：有的希望保持现在的「大」，有的嫌太长想小一点。
-- 现在把字号交给租户自选三档：large 28rpx（现状）/ medium 26rpx / small 24rpx。
--
-- 默认值选 large 而不是任何更小档：升级后存量租户的简介观感**零突变**，
-- 仍是客户已经看惯的 28rpx；想变小的管理员自己在后台切即可。这与 0040
-- 默认 date、0046 默认 100 的原则一致——后加的展示偏好一律不动存量。
--
-- 只作用于「简介文字」这一个 <text class="intro">，不改 page 全局字号，
-- 也不动公司名 / 联系表 / 章节标题等其它文字。
--
-- 升级零突变：只加列、默认值固定、不搬迁任何存量数据。
-- SQLite（测试 / devserver）由 GORM AutoMigrate 按模型 tag 补齐；MySQL（生产 / parity）
-- 由本迁移补齐。重复执行不会报错（Duplicate column 由 migrate.applyFile 忽略）。

ALTER TABLE company_profiles
  ADD COLUMN intro_font_scale VARCHAR(16) NOT NULL DEFAULT 'large' COMMENT '简介文字字号 large=28rpx/medium=26rpx/small=24rpx';
