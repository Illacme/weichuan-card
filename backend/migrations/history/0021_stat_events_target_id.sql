-- 0021：stat_events 加 target_id 列——埋点从「只记转化」扩展到「访客对什么内容感兴趣」。
--
-- 背景：原三类事件（visit/call/save）全发生在首页与操作栏，回答的是
-- 「有没有人找上门」；企业客户更需要「访客看了哪个案例、哪个服务、视频，
-- 转发了什么、存了海报」——这些事件都发生在内容页，且大多关联一个具体
-- 业务对象（案例 / 服务）。只有计数没有 target_id，后台只能知道「有人看
-- 案例」，永远不知道「哪个案例热」——后者才是内容运营与销售跟进的依据。
--
-- target_id 语义：关联业务对象的 id（案例 / 服务）；无关联对象的事件
-- （visit / video_play / poster_save / 转发名片）记 0。事件类型的完整
-- 清单与各类型的 target 约定见 internal/handler/stat.go 的 statEventTypes。
--
-- 复合索引 (event_type, target_id)：后台「内容浏览 TOP 榜」的查询形状是
--   WHERE event_type = 'project_view' ... GROUP BY target_id ORDER BY n DESC，
-- 前缀 event_type 走索引过滤，target_id 随聚合覆盖，不必回表。
--
-- SQLite（测试 / devserver）由 GORM AutoMigrate 自动补列；MySQL（生产 /
-- parity）由本迁移补齐。存量库需手动执行；ALTER/INDEX 命中已存在的列 /
-- 索引时框架按 Duplicate 错误忽略（见 internal/migrate/apply.go 包注释），
-- 重复执行不会报错。

ALTER TABLE stat_events
  ADD COLUMN target_id BIGINT NOT NULL DEFAULT 0 AFTER event_type,
  ADD INDEX idx_event_target (event_type, target_id);
