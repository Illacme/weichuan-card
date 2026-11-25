-- 0062：系统自带的三条文件存储改用职责名——主存储 / 应急存储 / 备份存储。
--
-- 背景：旧默认名里两个带「平台」、两个带「本地」（平台自身存储 / 平台本地存储 /
-- 平台本地镜像），管理员在列表里分不清谁是谁。名字已在代码常量里改成现在的样子，
-- 这里只负责把**已经建出来的行**改成同一套名字。
--
-- 为什么放迁移而不是启动时的 ensure：改名只对「装过更早版本的环境」有意义，
-- 跑一次就该结束。写成迁移由 schema_migrations 记账，此后永不再执行，也不会在
-- 启动路径上留一条常驻的兼容分支。产品尚未正式发布，这一条纯粹是清掉测试库里
-- 遗留的旧名。
--
-- 两条边界都写进 WHERE 里，宁可这行不改，也不能让迁移失败挡住启动：
--   1. 只认「字符串完全等于旧默认名」的行——管理员自己改过的展示名不动，
--   2. 目标名已被别的行占用时整条不改——name 上有唯一索引，硬改会 1062 让整个
--      迁移中断，而迁移失败等于服务起不来。
--
-- 方言：本文件只在 MySQL（生产 / parity）执行，SQLite（测试 / devserver）走
-- GORM AutoMigrate、不读 migrations 目录，其内置行由启动 ensure 按新名直接创建。

UPDATE storage_profiles
   SET name = '主存储'
 WHERE purpose = 'platform_primary'
   AND name IN ('平台自身存储', '平台主存储')
   AND NOT EXISTS (
       SELECT 1 FROM (SELECT id, name FROM storage_profiles) AS others
        WHERE others.name = '主存储'
   );

UPDATE storage_profiles
   SET name = '应急存储'
 WHERE purpose = 'tenant_fallback'
   AND name IN ('平台本地存储', '本地应急盘')
   AND NOT EXISTS (
       SELECT 1 FROM (SELECT id, name FROM storage_profiles) AS others
        WHERE others.name = '应急存储'
   );

UPDATE storage_profiles
   SET name = '备份存储'
 WHERE purpose = 'platform_mirror'
   AND name IN ('平台本地镜像', '本地备份盘')
   AND NOT EXISTS (
       SELECT 1 FROM (SELECT id, name FROM storage_profiles) AS others
        WHERE others.name = '备份存储'
   );
