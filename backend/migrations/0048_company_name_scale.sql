-- 0048：首页视频下方「公司名称」展示档位（company_infos.company_name_scale）。
--
-- 背景：客户反馈首页版式要能随要求灵活调整——视频下方的公司名称与简介文字，
-- 既想各自调大中小，也想直接关掉某一块。简介文字的三档已于 0047 落地；
-- 本迁移补上公司名称的同一套四档：large / medium / small / hidden。
--
-- hidden 的语义是**关闭**：小程序端用 wx:if 移除整个节点（含 ink 双栏下的空槽），
-- 不是 display:none，所以关掉后那一行连高度都不占，真正腾出屏幕空间。
--
-- 默认值选 large：升级后存量租户的公司名照旧显示、字号不变，零视觉突变；
-- 要调小或关闭的管理员自己在后台切即可。与 0040 默认 date、0046 默认 100、
-- 0047 默认 large 同一原则——后加的展示偏好一律不动存量。
--
-- 只作用于首页视频下方的公司名一行（.company-name 与其分隔线），
-- 不影响小程序顶栏的企业简称，也不影响后台侧边栏品牌名。
--
-- 升级零突变：只加列、默认值固定、不搬迁任何存量数据。
-- SQLite（测试 / devserver）由 GORM AutoMigrate 按模型 tag 补齐；MySQL（生产 / parity）
-- 由本迁移补齐。重复执行不会报错（Duplicate column 由 migrate.applyFile 忽略）。

ALTER TABLE company_infos
  ADD COLUMN company_name_scale VARCHAR(16) NOT NULL DEFAULT 'large' COMMENT '首页公司名展示档位 large/medium/small/hidden';
