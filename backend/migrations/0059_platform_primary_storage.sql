-- 0059：平台自身存储的连接信息收进存储档案。
--
-- 背景：平台自己的文件（p/ 平台对象、无绑定企业的对象）此前由 sys_setting 里的
-- storage_* 一组启动参数构造。它们和「文件存储与分发」里的存储档案是两套界面、
-- 两套校验、两套自检：管理员在档案里配好了云桶并自检通过，平台那份却还在用
-- 系统设置里的旧值，取图 404 且两边日志都没有错。
--
-- 本版本把连接信息统一到 storage_profiles 的内置档案 purpose=platform_primary：
--   - 连接信息（驱动 / 端点 / 桶 / 凭据 / 基址 / 公开读）只存档案，界面复用档案
--     的编辑与自检接口；改完需重启，平台实例仍是启动期构造、运行期不可变；
--   - sys_setting 里那组 storage_* 仅作为**首次生成档案时的种子**（老部署在界面
--     填过的值不能凭空丢失），档案一旦存在就由启动逻辑清理掉，不再参与运行期；
--   - upload_dir / mirror_dir 这类部署期目录事实、local_mirror_enabled 总闸、
--     配额与上传限制继续留在 sys_setting，它们不是「连接信息」。
--
-- 内置档案行由启动时 ensure 创建（内容取决于部署环境与历史 sys_setting 值，
-- 迁移脚本无法知道），这里只更新用途注释。SQLite（测试 / devserver）按模型
-- AutoMigrate，无此方言文件。

ALTER TABLE storage_profiles
  MODIFY COLUMN purpose VARCHAR(24) NOT NULL DEFAULT '' COMMENT '内置用途：platform_primary / tenant_fallback / platform_mirror；空=普通档案';
