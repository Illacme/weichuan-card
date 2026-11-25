-- 0039：管理员微信绑定列（admins.wx_openid）。
--
-- 背景：小程序端要加企业管理员审核。六个 /api/admin/apply/* 端点本来就挂
-- AuthMiddleware(db,"admin")，而 ExtractToken 优先读 Authorization: Bearer，
-- 所以只要能签发 role=admin 的 JWT，小程序就能直接调审核接口，无需改审核端点。
--
-- 产品链路：管理员首次在小程序里用后台账号密码登录 → 用 wx.login 的 code
-- 换 openid 绑到本列 → 之后打开审核页直接用 code 免密换发管理员令牌。
--
-- 与 Employee.openid 同构：明文列、覆盖写（换微信号重新绑定即可，旧 openid
-- 从此匹配不到任何管理员，效果等同解绑），同企业内一个 openid 只允许绑一个管理员。
-- 空串表示未绑定，因此索引刻意不做唯一约束——升级后存量管理员全是空串。
--
-- 升级零突变：只加列 + 加普通索引，默认空，不搬迁任何存量数据。
-- SQLite（测试 / devserver）由 GORM AutoMigrate 按模型 tag 补齐；
-- MySQL（生产 / parity）由本迁移补齐。重复执行不会报错（Duplicate column /
-- Duplicate key name 由 migrate.applyFile 忽略）。

ALTER TABLE admins
  ADD COLUMN wx_openid VARCHAR(64) NOT NULL DEFAULT '' COMMENT '微信 openid（小程序管理员免密登录，空=未绑定）' AFTER token_version;

ALTER TABLE admins
  ADD INDEX idx_admins_wx_openid (wx_openid);
