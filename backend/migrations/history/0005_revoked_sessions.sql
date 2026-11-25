-- 0005 会话黑名单：单设备精确吊销（登出 / 踢下线）。
-- 一次登录生成一个 sid（Claims.SID，access/refresh 共享，刷新不换），登出时把 sid 写入本表，
-- 之后该会话签发的所有令牌立即失效，不影响同一账号的其他设备登录。
-- 与 0004 的 token_version（账号级吊销）互补：改密/禁用走 token_version，单设备登出走本表。
-- expires_at 仅用于后台按时间清理过期记录，不参与吊销判定（判定只看 sid 是否存在）。
-- SQLite（测试）由 GORM AutoMigrate 自动建表；MySQL（生产 / parity）由本迁移补齐。

CREATE TABLE IF NOT EXISTS revoked_sessions (
  sid        VARCHAR(64) NOT NULL,
  user_type  VARCHAR(16) NOT NULL DEFAULT '',
  user_id    BIGINT      NOT NULL DEFAULT 0,
  expires_at DATETIME    NOT NULL DEFAULT CURRENT_TIMESTAMP,
  created_at DATETIME    NOT NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (sid),
  KEY idx_revoked_sessions_expires_at (expires_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
