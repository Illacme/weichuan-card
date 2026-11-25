-- 软删除支持：为内容模型（业绩分类 / 项目案例 / 业务服务）增加 deleted_at 列。
-- GORM 的 gorm.DeletedAt 会在查询时自动附加 `deleted_at IS NULL`，
-- 删除操作改为 UPDATE 置空该列，避免误删数据；列表接口默认不返回已删除记录。
-- 仅对全新初始化的库执行：已存在的库请手动执行以下 ALTER（可重复执行，列已存在时报错可忽略）。

ALTER TABLE achievement_categories ADD COLUMN deleted_at DATETIME NULL;
ALTER TABLE project_cases ADD COLUMN deleted_at DATETIME NULL;
ALTER TABLE biz_services ADD COLUMN deleted_at DATETIME NULL;
