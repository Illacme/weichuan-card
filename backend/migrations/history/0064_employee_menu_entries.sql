-- 0064：把小程序「我的」页的菜单功能开关扩成四项（功能授权）。
--
-- 背景：原先只有「团队成员」一个按员工下发的开关（0063），管理员在「编辑员工」
-- 弹窗里勾选。现在四项菜单都要能逐个员工授权：生成海报 / 团队成员 / 数据统计 /
-- 管理员登录。开关从编辑弹窗挪到列表「操作」列的独立「功能授权」弹窗——
-- 授权是独立于名片资料的权限配置，混在编辑资料里容易被误当作普通字段一并改掉。
--
-- 默认值是产品口径：
--   - 生成海报 / 数据统计 = 1（开）：这两项是「我的」页的本职功能，绝大多数员工都要用，
--     默认关等于升级当天全员菜单凭空少两块，管理员还得逐个补开。
--   - 团队成员 / 管理员登录 = 0（关）：看全员名单、「企业管理员」入口都是额外能力，
--     默认放开会让每个员工一升级就多两项管理员没同意过的菜单。
-- 与 0063 的口径相反但各有道理：前两项是「默认给的常规能力」，后两项是
-- 「默认拒绝的额外能力」。
--
-- 新增列带 DEFAULT，存量行自动补齐：海报/统计得到 1，团队/管理员登录得到 0，
-- 正好是上面的产品默认，老员工无需逐个补配。
--
-- 方言：本文件只在 MySQL（生产 / parity）执行；SQLite（测试 / devserver）走
-- GORM AutoMigrate，按模型 default tag 建列，口径与这里一致。

ALTER TABLE employees
  ADD COLUMN show_poster_entry TINYINT(1) NOT NULL DEFAULT 1
  COMMENT '小程序「我的」页是否显示「生成海报」入口，默认显示';

ALTER TABLE employees
  ADD COLUMN show_stat_entry TINYINT(1) NOT NULL DEFAULT 1
  COMMENT '小程序「我的」页是否显示「数据统计」入口，默认显示';

ALTER TABLE employees
  ADD COLUMN show_admin_entry TINYINT(1) NOT NULL DEFAULT 0
  COMMENT '小程序「我的」页是否显示「管理员登录」入口，默认不显示';
