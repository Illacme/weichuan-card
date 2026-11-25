-- 0011：多租户第一期（后台多租户 + 数据隔离）
-- 设计：
--   * company 为租户表；朗汇机电为 1 号租户，存量数据全部归属（company_id 默认 1）。
--   * company_infos / company_profiles / home_layouts 由 id=1 单例改为按 company_id 唯一（1:1）。
--   * employees / achievement_categories / project_cases / biz_services / open_applies /
--     card_modify_applies 增加 company_id（NOT NULL DEFAULT 1 + 索引）。
--   * admins 增加 company_id：0 = 平台超级管理员（可见全部租户），>0 = 企业管理员（仅本企业）。
--   * 小程序端公开接口缺省落 company_id=1（朗汇），行为不变；按企业分发留给第二期。
-- 与 devserver AutoMigrate（model 包）保持一致。

CREATE TABLE IF NOT EXISTS company (
  id         BIGINT PRIMARY KEY AUTO_INCREMENT,
  name       VARCHAR(128) NOT NULL DEFAULT '',
  status     VARCHAR(16)  NOT NULL DEFAULT 'enabled',
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

-- 这里**刻意不再预置朗汇机电**（原先是 INSERT company (1, ...)）。
--
-- 商用版装完必须是一家企业都没有：由平台超管先「新建企业」，再逐步录入信息、
-- 员工与内容，小程序才立得起来。预置一家真实客户的企业名与白标码，
-- 等于把演示数据当成了产品初始状态。
--
-- 需要样板数据时走安装向导的「加载演示企业数据」勾选，数据由 internal/demoseed
-- 写入（同样落 1 号企业，因为 defaultCompanyID = 1 是小程序公开接口的缺省租户）。
--
-- 下方 ALTER 的 DEFAULT 1 保留：它是「存量行回填」的写法，空库执行时无行可填，
-- 不影响零数据。已上线的库不受影响（迁移按文件名记账，不会重放本文件）。

ALTER TABLE company_infos    ADD COLUMN company_id BIGINT NOT NULL DEFAULT 1;
ALTER TABLE company_profiles ADD COLUMN company_id BIGINT NOT NULL DEFAULT 1;
ALTER TABLE home_layouts     ADD COLUMN company_id BIGINT NOT NULL DEFAULT 1;
ALTER TABLE employees        ADD COLUMN company_id BIGINT NOT NULL DEFAULT 1;
ALTER TABLE achievement_categories ADD COLUMN company_id BIGINT NOT NULL DEFAULT 1;
ALTER TABLE project_cases    ADD COLUMN company_id BIGINT NOT NULL DEFAULT 1;
ALTER TABLE biz_services     ADD COLUMN company_id BIGINT NOT NULL DEFAULT 1;
ALTER TABLE open_applies      ADD COLUMN company_id BIGINT NOT NULL DEFAULT 1;
ALTER TABLE card_modify_applies ADD COLUMN company_id BIGINT NOT NULL DEFAULT 1;
ALTER TABLE admins           ADD COLUMN company_id BIGINT NOT NULL DEFAULT 0;

-- 单例表按租户唯一；存量行已由 DEFAULT 回填为 1（朗汇）
ALTER TABLE company_infos    ADD UNIQUE INDEX uk_company_info_company (company_id);
ALTER TABLE company_profiles ADD UNIQUE INDEX uk_company_profile_company (company_id);
ALTER TABLE home_layouts     ADD UNIQUE INDEX uk_home_layout_company (company_id);

-- 业务表租户索引（后台列表按企业过滤）
ALTER TABLE employees        ADD INDEX idx_employee_company (company_id);
ALTER TABLE achievement_categories ADD INDEX idx_category_company (company_id);
ALTER TABLE project_cases    ADD INDEX idx_project_company (company_id);
ALTER TABLE biz_services     ADD INDEX idx_service_company (company_id);
ALTER TABLE open_applies      ADD INDEX idx_open_apply_company (company_id);
ALTER TABLE card_modify_applies ADD INDEX idx_modify_apply_company (company_id);
ALTER TABLE admins           ADD INDEX idx_admin_company (company_id);
