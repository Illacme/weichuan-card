-- 0023：员工海报个性化设置表（poster_prefs）——自定义背景 + 版式。
--
-- 背景：海报页原来只能从内置素材库（24 节气 + 20 场景）里挑一张，员工想用
-- 自己的照片 / 公司活动照当背景就无从下手；版式也是写死的（头像左上、
-- 信息竖排），所有人发出去的海报长得一模一样。
--
-- 本表存两件事：
--   bg_id       当前选中的背景（"" 白底 / poster-* 内置素材 / c-* 自定义）
--   backgrounds 自定义背景图清单（JSON 数组，最多 9 条，见 handler/poster.go）
--   layout      版式配置（JSON：模板 / 元素显隐 / 配色 / 位移 / 头像大小）
--
-- 为什么不拆列：版式可选项会随需求增删，拆列每加一项都要写迁移；
-- JSON 在 handler 层用白名单校验归一（normalizePosterLayout），脏数据进不来。
--
-- 为什么不复用 employee 表：employee 的写入走「提交 → 待审 → 通过」审核流
-- （CardModifyApply），而海报版式是纯个人偏好——调个字号还要管理员审批很荒谬。
--
-- staff_id 唯一：一个员工一行，UPSERT 更新。
-- SQLite（测试 / devserver）由 GORM AutoMigrate 自动建表；MySQL（生产 / parity）由本迁移补齐。
-- 存量库需手动执行；本语句自带存在性兜底，重复执行不会报错。

CREATE TABLE IF NOT EXISTS poster_prefs (
    id          BIGINT       NOT NULL AUTO_INCREMENT,
    staff_id    BIGINT       NOT NULL COMMENT '员工编号（employee.staff_id）',
    company_id  BIGINT       NOT NULL DEFAULT 0 COMMENT '租户冗余，便于按企业清理',
    bg_id       VARCHAR(64)  NOT NULL DEFAULT '' COMMENT '当前背景：空=白底 / poster-*=内置 / c-*=自定义',
    backgrounds TEXT         NULL COMMENT '自定义背景 JSON 数组 [{id,name,file}]',
    layout      TEXT         NULL COMMENT '版式 JSON（模板/显隐/配色/位移/头像大小）',
    created_at  DATETIME     NULL,
    updated_at  DATETIME     NULL,
    PRIMARY KEY (id),
    UNIQUE KEY uk_poster_prefs_staff (staff_id),
    KEY idx_poster_prefs_company (company_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='员工海报个性化设置（自定义背景 + 版式）';
