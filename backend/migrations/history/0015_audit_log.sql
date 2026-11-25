-- 0015：管理操作审计日志表 audit_log。
-- 背景：后台能改系统设置（含云存储 / CDN / 微信 AppSecret 等密钥）、重置他人密码、
-- 删除平台超管、启停企业，此前**完全没有操作留痕**，出事无法回答「谁在什么时候改了什么」。
-- 覆盖写操作与登录失败；只读查询不记，避免日志量淹没有用信息。
-- 敏感值（密钥 / 密码 / 连接串）只记「已修改」，不落明文——否则审计表会变成新的泄密面。
-- 与 devserver AutoMigrate 保持一致（见 internal/model/models.go 的 AuditLog）。

CREATE TABLE IF NOT EXISTS audit_log (
  id          BIGINT       NOT NULL AUTO_INCREMENT,
  actor_type  VARCHAR(16)  NOT NULL DEFAULT '',   -- admin / employee / anonymous
  actor_id    BIGINT       NOT NULL DEFAULT 0,
  actor_name  VARCHAR(64)  NOT NULL DEFAULT '',   -- 冗余用户名：账号删除后仍可追溯
  company_id  BIGINT       NOT NULL DEFAULT 0,    -- 操作者所属租户（0=平台）
  action      VARCHAR(32)  NOT NULL DEFAULT '',   -- setting.update / admin.reset_password ...
  target_type VARCHAR(32)  NOT NULL DEFAULT '',   -- setting / admin / company / employee ...
  target_id   VARCHAR(64)  NOT NULL DEFAULT '',
  detail      TEXT,                                -- JSON 变更摘要（敏感值脱敏）
  ip          VARCHAR(64)  NOT NULL DEFAULT '',
  result      VARCHAR(16)  NOT NULL DEFAULT '',   -- success / fail
  created_at  DATETIME(3)  NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  PRIMARY KEY (id),
  KEY idx_audit_created (created_at),
  KEY idx_audit_action (action),
  KEY idx_audit_actor (actor_type, actor_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
