-- 0057：绑定表记住「切换前的档案」，支撑一键回滚。
--
-- 背景：切换绑定（手动 PUT 或迁移后 autoSwitch）直接覆盖 storage_profile_id，
-- 旧档案身份只散落在审计 detail 与迁移任务的 source_profile_id 里，没有一处
-- 结构化留存。切过去发现漏搬 / 目标桶对象被误删时，管理员只能翻审计手工改回，
-- 既慢又容易改错（尤其 delivery 档案还要一并还原）。
--
-- 本迁移给绑定表补两列，记录「本次切换前的那一套」：
--   - 前进切换（saveBinding / switchBindingAfterMigration）把当前档案写进 previous；
--   - 一键回滚把 previous 换回当前、同时把被换下的写进 previous（交换语义，
--     于是回滚一次后还能再回滚一次，来回切不会丢历史）；
--   - previous=0 表示「绑定第一次建立之前没有档案」，回滚接口据此拒绝，
--     退路用已有的 DELETE binding（回落全局 / 平台），不引入第二套语义。
--
-- 只加列、不改语义、不插数据：existing 行的 previous 保持 0，即「历史绑定不可回滚」，
-- 这比凭空猜一个上一套档案安全。
--
-- 方言：SQLite（测试 / devserver）由 GORM AutoMigrate 按模型 tag 补齐；
-- MySQL（生产 / parity）由本迁移补齐，MySQL 8 裸 DDL 不用 IF EXISTS 的 ALTER。

ALTER TABLE storage_bindings
  ADD COLUMN previous_storage_profile_id BIGINT NOT NULL DEFAULT 0 COMMENT '切换前的存储档案ID，0=此绑定从未切换过',
  ADD COLUMN previous_delivery_profile_id BIGINT NOT NULL DEFAULT 0 COMMENT '切换前的分发档案ID，0=切换前未启用分发';
