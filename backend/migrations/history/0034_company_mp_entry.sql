-- 0034：小程序「默认进入」设置（按租户）——裸启动落到名片墙还是某个员工的名片。
--
-- 背景：扫微信「体验版二维码」/ 直接打开小程序是裸启动，微信不带任何参数。
-- 上一轮把落地页从「输入员工编号」弹窗改成了企业名片墙（pages/team/team）；
-- 但有些客户的用法就是「一张名片打天下」——老板的名片就是公司门面，
-- 访客扫进来应该直接看到他，而不是先看一层员工宫格再点。
-- 这个偏好按企业配置，所以落在 company_infos（与公司名称/LOGO 同一行，随企业开通即有）。
--
-- 取值：
--   mp_entry_mode     'wall'（名片墙，缺省）| 'staff'（指定员工名片）
--   mp_entry_staff_id 指定员工的 staff_id；mode='wall' 时归零。
--
-- 为什么缺省 'wall'：存量企业升级后行为**必须**不变——上一轮刚把裸启动改成名片墙，
-- 这里若缺省 'staff'，所有存量租户会突然跳到某个员工（还可能是已停用的那个）。
-- 「升级不得突变存量行为」优先于「新客户默认值更聪明」。
--
-- 为什么不建外键 / 索引：mp_entry_staff_id 的合法性（属于本企业且启用）由
-- handler 在写入时校验，读取时再校验一次并在失效时**回落 wall**——
-- 外键拦不住「员工停用」这种软状态，而回落是唯一不会把访客引到空名片页的做法。
--
-- SQLite（测试 / devserver）由 GORM AutoMigrate 自动补列；MySQL（生产 / parity）由本迁移补齐。
-- 存量库需手动执行；重复执行报 1060（Duplicate column）由迁移器忽略。

ALTER TABLE company_infos ADD COLUMN mp_entry_mode VARCHAR(16) NOT NULL DEFAULT 'wall' COMMENT '小程序裸启动落地页：wall=名片墙 / staff=指定员工名片';
ALTER TABLE company_infos ADD COLUMN mp_entry_staff_id BIGINT NOT NULL DEFAULT 0 COMMENT 'mode=staff 时的员工 staff_id；0 表示未指定（回落名片墙）';
