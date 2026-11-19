-- 0053：名片卡右上角企业 LOGO 的显示档位（home_layouts.logo_size / logo_align）。
--
-- 背景：LOGO 原来是写死的 150×44rpx + mode="aspectFit"。aspectFit 会把图片按比例
-- 缩到框内并**居中**，于是一张正方形 LOGO 只占 44rpx 宽、左右各留约 53rpx 空白——
-- 客户看到的是「浮在右上角偏左」的一块，小尺寸图尤其明显，而当时没有任何可调项。
--
-- 两列都只存档位字符串，不做像素计算：具体 rpx 尺寸与对齐方式在
-- miniprogram/app.wxss 与 employee-card 组件里（小程序不能动态加载 WXSS，
-- 与 featured_effect 同一套路）。
--
-- 默认值选 medium / center = 加配置前的表现（150×44 + aspectFit 居中），
-- 所以升级后存量租户看到的画面一字不变。要更小/更大、更靠左/靠右由客户自己切。
-- 与 0040 默认 date、0046 默认 100、0047 默认 large 同一原则：后加的展示偏好
-- 一律不动存量。
--
-- 升级零突变：只加列、默认值固定、不搬迁任何存量数据。
-- SQLite（测试 / devserver）由 GORM AutoMigrate 按模型 tag 补齐；MySQL（生产 / parity）
-- 由本迁移补齐。重复执行不会报错（Duplicate column 由 migrate.applyFile 忽略）。

ALTER TABLE home_layouts
  ADD COLUMN logo_size VARCHAR(16) NOT NULL DEFAULT 'medium' COMMENT '名片卡 LOGO 尺寸档 small/medium/large';

ALTER TABLE home_layouts
  ADD COLUMN logo_align VARCHAR(16) NOT NULL DEFAULT 'center' COMMENT '名片卡 LOGO 水平对齐 left/center/right';
