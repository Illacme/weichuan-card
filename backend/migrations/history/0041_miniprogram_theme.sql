-- 0041：小程序配色模板（home_layouts.template_id）。
--
-- 背景：此前小程序的全站主色全部硬编码在 app.wxss 等 12 份 WXSS 里，客户想换
-- 一套品牌配色只能改代码重新提审。现在把「选哪一套内置配色」交给租户在后台自选，
-- 作为按授权开关的增值能力。
--
-- 为什么存 id 而不存色值 / CSS：小程序代码是编译后上传审核的，端上不能动态加载
-- WXSS；让后台下发自由样式既绕不过审核，也会把内部类名变成对客户的长期承诺。
-- 模板包随版本内置（backend/internal/mptemplate + miniprogram/utils/theme.ts），
-- 这里只记「选了哪一款」。
--
-- 默认值选 default（升级前的经典蓝）：存量租户的页面逐字不变，属于「多了可选项」
-- 而不是「换了套皮」。
--
-- 升级零突变：只加列、默认值固定、不搬迁任何存量数据。
-- SQLite（测试 / devserver）由 GORM AutoMigrate 按模型 tag 补齐；MySQL（生产 / parity）
-- 由本迁移补齐。重复执行不会报错（Duplicate column 由 migrate.applyFile 忽略）。

ALTER TABLE home_layouts
  ADD COLUMN template_id VARCHAR(32) NOT NULL DEFAULT 'default' COMMENT '小程序配色模板 id：default/ink/warm';
