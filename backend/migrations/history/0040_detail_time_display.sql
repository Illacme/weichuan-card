-- 0040：小程序详情页「更新于」展示档位（home_layouts.project_time_mode / service_time_mode）。
--
-- 背景：项目案例与业务服务详情页原先硬编码显示 content 行的 updated_at（精确到秒）。
-- 这个值是「运营最后一次保存的时间」，不是「案例交付时间」——批量整理内容会让全站
-- 案例在同一天集体显示「更新于」，秒级精度对访客也只是噪音，且后台无处可关。
-- 现在把档位交给租户自选：hidden（不显示）/ date（只到日）/ minute（到分）。
--
-- 为什么两列而不是一列：案例带「交付年份」语义、服务更像常青内容，两者该不该显示
-- 时间往往不是一个答案，共用一个开关会逼客户二选一。
--
-- 默认值选 date 而不是 hidden：升级后存量租户的时间显示**不消失**，只是不再暴露
-- 秒级，属于「变干净」而不是「东西不见了」；要彻底隐藏可在后台一键关掉。
--
-- 升级零突变：只加列、默认值固定、不搬迁任何存量数据。
-- SQLite（测试 / devserver）由 GORM AutoMigrate 按模型 tag 补齐；MySQL（生产 / parity）
-- 由本迁移补齐。重复执行不会报错（Duplicate column 由 migrate.applyFile 忽略）。

ALTER TABLE home_layouts
  ADD COLUMN project_time_mode VARCHAR(16) NOT NULL DEFAULT 'date' COMMENT '项目案例详情页时间显示：hidden/date/minute',
  ADD COLUMN service_time_mode VARCHAR(16) NOT NULL DEFAULT 'date' COMMENT '业务服务详情页时间显示：hidden/date/minute';
