-- 0049：首页业绩分类数量 / 精选案例数量与循环效果（home_layouts）。
--
-- 客户反馈：首页「经典业绩」板块的分类图标墙太长，想限制最多显示几个；
-- 精选案例默认铺几张也要能配，并要求能做成滚动 / 翻转这类有质感的循环呈现。
-- 三项都是纯展示偏好，和 0046/0047/0048 一样归「名片页面样式」页签管理。
--
-- category_limit 0~20，0 = 隐藏分类网格（精选案例照旧显示，板块标题不消失）；
-- featured_count 1~3，实际精选案例不足时以实际数量为准，不补空位；
-- featured_effect 四档：static 静态平铺 / fade 淡入淡出 / scroll 横向滚动 / flip 3D 翻转。
-- 小程序端按效果 id 选择随包内置的动画样式——端上不能动态加载 WXSS，
-- 所以后端只下发 id 白名单内的值，新增效果需同时改模型归一与小程序样式。
--
-- 默认值取各配置上限（20 / 3 / static）：分类数量与轮播效果对存量租户零变化；
-- 精选因为范围只有 1~3 无法表达「不限」，缺省 3 会让原本精选超过 3 张的租户
-- 收敛到排序最前的 3 张——案例本身没有被删除，管理员可改「首页精选」标记。
-- 这与 0040/0046/0047/0048「后加的展示偏好默认不动存量」原则的差别仅在于
-- 本配置上限本身就是 3，不存在能表示「全部」的合法值。
--
-- 升级零突变：只加列、默认值固定、不搬迁任何存量数据。
-- SQLite（测试 / devserver）由 GORM AutoMigrate 按模型 tag 补齐；MySQL（生产 / parity）
-- 由本迁移补齐。重复执行不会报错（Duplicate column 由 migrate.applyFile 忽略）。

ALTER TABLE home_layouts
  ADD COLUMN category_limit INT NOT NULL DEFAULT 20 COMMENT '首页业绩分类最多显示数量 0~20，0=隐藏分类网格',
  ADD COLUMN featured_count INT NOT NULL DEFAULT 3 COMMENT '首页精选案例默认呈现数量 1~3，不足按实际',
  ADD COLUMN featured_effect VARCHAR(16) NOT NULL DEFAULT 'static' COMMENT '精选案例循环效果 static/fade/scroll/flip';
