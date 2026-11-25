-- 0022：stat_events 加 source 列——统计「流量来源」与「裂变」。
--
-- 背景：原埋点只回答「有多少人、点了多少次」与「对什么内容感兴趣」，
-- 但企业客户更关心「访客从哪来」与「谁的名片被转发带来了新访客」。
-- 这两件事共用一列 source 即可，不必各加一张表：
--
--   * 流量来源分布（sourceDist）：对 visit 事件按 source 分组，
--     回答「扫码 vs 转发 vs 搜索 vs 海报」各占多少——运营才知道该把精力
--     投到哪个渠道（比如发现搜索占大头，就该优化小程序名称/关键词）。
--   * 裂变榜（fissionTop）：对 share_card 来源的 visit 事件按 staff_id 分组，
--     回答「哪个员工名片的转发带来的新访客最多」——裂变能力的排行。
--
-- source 的取值（后端 stat.go 的 statSourceTypes 白名单归一）：
--   scan        扫码（二维码 / 小程序码，含海报上的码）
--   share_card  转发卡片（聊天/群聊/App/小程序互跳，scene 1007/1008/1036/...）
--   search      微信搜索 / 发现 / 我的小程序等入口
--   poster      扫海报上的小程序码（poster 页生成码时带 &from=poster 标记）
--   unknown     缺省（老客户端 / 历史数据未带 source）
--   other       带了的、但不在白名单里的脏值（永不进白名单，避免脏数据污染分布）
--
-- 安全边界（重要）：source 来自客户端，只能当「统计维度」，
-- 绝不参与权限 / 租户 / 计费任何判断。白名单归一就是为了挡住脏值——
-- 否则谁都能往统计里塞一个自定义来源、把分布图玩坏（静默污染）。
--
-- 复合索引 (event_type, source)：来源分布查询形状是
--   WHERE event_type = 'visit' ... GROUP BY source，
-- 前缀 event_type 过滤、source 随聚合覆盖，不必回表。
--
-- SQLite（测试 / devserver）由 GORM AutoMigrate 自动补列；MySQL（生产 /
-- parity）由本迁移补齐。存量库需手动执行；ALTER/INDEX 命中已存在列 / 索引时
-- 框架按 Duplicate 错误忽略（见 internal/migrate/apply.go 包注释），重复执行不报错。

ALTER TABLE stat_events
  ADD COLUMN source VARCHAR(16) NOT NULL DEFAULT '' AFTER target_id,
  ADD INDEX idx_event_source (event_type, source);
