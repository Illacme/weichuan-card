-- 0063：员工级开关「小程序『我的』页是否显示『团队成员』入口」。
--
-- 背景：团队成员（名片墙）是员工自视角功能，放在登录后的「我的」页。但并不是
-- 每家企业都希望每个员工都能看到全员名单——很多企业只让管理层或销售骨干看到。
-- 因此按员工逐个微信控制，而不是做成企业级总开关。
--
-- 默认 0（不显示）：这属于「额外开放的能力」，必须由管理员显式勾选。
-- 反过来默认 1 的话，升级当天所有员工的菜单里凭空多一项，且是管理员
-- 从没同意过要开放的能力——默认拒绝比默认放开安全。
--
-- 注意 app 侧的两处兜底口径（缺了会出现「后台开了但不生效」或「后台关了还显示」）：
--   1. 启动时的 GORM AutoMigrate 会按模型 default:false 给 SQLite / 新库建列；
--   2. /api/employee/me 只在字段为 true 时才回 showTeamEntry，前端按「有没有这个字段」
--      判断，不依赖后端一定回 false（老库没跑迁移时也不会误显示）。
--
-- 方言：本文件只在 MySQL（生产 / parity）执行；SQLite（测试 / devserver）走
-- GORM AutoMigrate，不读 migrations 目录。

ALTER TABLE employees
  ADD COLUMN show_team_entry TINYINT(1) NOT NULL DEFAULT 0
  COMMENT '小程序「我的」页是否显示「团队成员」入口（名片墙），默认不显示';
