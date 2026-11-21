-- 0056：平台本地镜像内置档案 + 按租户开关。
--
-- 背景：既有内置本地档案解决的是「云配置坏了还能上传」的租户退路；
-- 平台自身的对象（p/ 前缀）与租户云桶里的对象此前只有一份字节。
-- 本迁移引入第二个不可删的本地档案，专门承载「上传成功后从云异步回拉」的镜像副本：
--   - storage_profiles.purpose 区分两套内置档案的用途，避免管理员把镜像档案
--     误绑成租户的写入目标（写入仍由普通档案 / 平台实例负责）；
--   - company.local_mirror_enabled 是按租户开关：默认 0（不开启），由平台超管
--     在企业管理里逐家打开。平台对象（cid=0）不受该列影响，只受总闸控制。
--
-- 两套内置档案都在启动时 ensure（目录取决于部署的 UPLOAD_DIR / MIRROR_DIR，
-- 迁移脚本无法知道），这里只补列，不插行。
--
-- 方言：SQLite（测试 / devserver）由 GORM AutoMigrate 按模型 tag 补齐；
-- MySQL（生产 / parity）由本迁移补齐。

ALTER TABLE storage_profiles
  ADD COLUMN purpose VARCHAR(24) NOT NULL DEFAULT '' COMMENT '内置用途：tenant_fallback / platform_mirror；空=普通档案';

-- 升级前已有的内置行就是租户退路档案；不给它补这一列会让两套内置档案在界面上
-- 变成同一个用途，管理员无法区分。普通档案保持空串。
UPDATE storage_profiles SET purpose = 'tenant_fallback' WHERE builtin = 1 AND purpose = '';

ALTER TABLE company
  ADD COLUMN local_mirror_enabled TINYINT(1) NOT NULL DEFAULT 0 COMMENT '该企业是否开启对象本地镜像备份（0=关闭，1=开启）';
