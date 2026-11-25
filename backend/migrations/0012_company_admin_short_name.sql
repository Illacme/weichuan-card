-- 0012：企业简称（company.short_name）——PC 后台侧边栏「xxxx · 管理后台」品牌名，
-- 并作为开通企业时公司信息简称（company_infos.short_name，仅小程序展示）的初始值。
-- 与 0009 的 company_infos.short_name 相互独立：后台侧边栏读本列，小程序读公司信息。
-- 与 devserver AutoMigrate 保持一致。

ALTER TABLE company ADD COLUMN short_name VARCHAR(64) NOT NULL DEFAULT '';
-- 历史种子：当时的 1 号演示租户（朗汇）简称。全新安装的空库执行到这里时没有
-- id=1 的行，UPDATE 命中 0 行；演示企业现在由安装向导 loadDemoData 显式装载，
-- 不依赖这条语句。保留 SQL 是为了不改动已应用过的迁移，别把它当当前配置依赖。
UPDATE company SET short_name = '朗汇机电' WHERE id = 1;
