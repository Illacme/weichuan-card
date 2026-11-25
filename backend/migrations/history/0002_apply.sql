-- 0002：审核流相关表 + 员工 staff_id 改为自增

ALTER TABLE employees MODIFY staff_id BIGINT NOT NULL AUTO_INCREMENT;

CREATE TABLE IF NOT EXISTS admins (
  id            BIGINT PRIMARY KEY AUTO_INCREMENT,
  username      VARCHAR(64) NOT NULL DEFAULT '',
  password_hash VARCHAR(255) NOT NULL DEFAULT '',
  created_at    DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  UNIQUE KEY uk_username (username)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS open_applies (
  id            BIGINT PRIMARY KEY AUTO_INCREMENT,
  name          VARCHAR(64)  NOT NULL DEFAULT '',
  phone         VARCHAR(32)  NOT NULL DEFAULT '',
  email         VARCHAR(128) NOT NULL DEFAULT '',
  title         VARCHAR(64)  NOT NULL DEFAULT '',
  avatar        VARCHAR(255) NOT NULL DEFAULT '',
  status        VARCHAR(16)  NOT NULL DEFAULT 'pending',
  reject_reason VARCHAR(255) NOT NULL DEFAULT '',
  created_at    DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  KEY idx_status (status)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS card_modify_applies (
  id            BIGINT PRIMARY KEY AUTO_INCREMENT,
  staff_id      BIGINT NOT NULL DEFAULT 0,
  snapshot      JSON,
  status        VARCHAR(16) NOT NULL DEFAULT 'pending',
  reject_reason VARCHAR(255) NOT NULL DEFAULT '',
  created_at    DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  KEY idx_staff (staff_id),
  KEY idx_status (status)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
