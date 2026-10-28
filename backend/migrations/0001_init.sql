CREATE TABLE IF NOT EXISTS employees (
  staff_id      BIGINT PRIMARY KEY,
  name          VARCHAR(64)  NOT NULL DEFAULT '',
  title         VARCHAR(64)  NOT NULL DEFAULT '',
  avatar        VARCHAR(255) NOT NULL DEFAULT '',
  phone         VARCHAR(32)  NOT NULL DEFAULT '',
  email         VARCHAR(128) NOT NULL DEFAULT '',
  password_hash VARCHAR(255) NOT NULL DEFAULT '',
  status        VARCHAR(16)  NOT NULL DEFAULT 'enabled',
  created_at    DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS company_infos (
  id        BIGINT PRIMARY KEY AUTO_INCREMENT,
  full_name VARCHAR(128) NOT NULL DEFAULT '',
  address   VARCHAR(255) NOT NULL DEFAULT '',
  tel       VARCHAR(32)  NOT NULL DEFAULT '',
  logo      VARCHAR(255) NOT NULL DEFAULT ''
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS company_profiles (
  id               BIGINT PRIMARY KEY AUTO_INCREMENT,
  intro_text       TEXT,
  video_channel_id VARCHAR(64)  NOT NULL DEFAULT '',
  backup_mp4_url   VARCHAR(512) NOT NULL DEFAULT '',
  cover            VARCHAR(512) NOT NULL DEFAULT ''
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS achievement_categories (
  id        BIGINT PRIMARY KEY AUTO_INCREMENT,
  name      VARCHAR(64) NOT NULL DEFAULT '',
  icon      VARCHAR(512) NOT NULL DEFAULT '',
  sort      INT NOT NULL DEFAULT 0,
  is_locked TINYINT(1) NOT NULL DEFAULT 0
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS project_cases (
  id               BIGINT PRIMARY KEY AUTO_INCREMENT,
  category_id      BIGINT NOT NULL DEFAULT 0,
  name             VARCHAR(128) NOT NULL DEFAULT '',
  summary          VARCHAR(512) NOT NULL DEFAULT '',
  detail           TEXT,
  cover            VARCHAR(512) NOT NULL DEFAULT '',
  album            JSON,
  is_home_featured TINYINT(1) NOT NULL DEFAULT 0,
  enabled          TINYINT(1) NOT NULL DEFAULT 1,
  sort             INT NOT NULL DEFAULT 0,
  created_at       DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  KEY idx_cat (category_id),
  KEY idx_featured (is_home_featured, enabled)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS biz_services (
  id        BIGINT PRIMARY KEY AUTO_INCREMENT,
  title     VARCHAR(128) NOT NULL DEFAULT '',
  content   TEXT,
  sort      INT NOT NULL DEFAULT 0,
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS home_layouts (
  id        BIGINT PRIMARY KEY AUTO_INCREMENT,
  order_col JSON
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS stat_events (
  id         BIGINT PRIMARY KEY AUTO_INCREMENT,
  staff_id   BIGINT NOT NULL DEFAULT 0,
  event_type VARCHAR(16) NOT NULL DEFAULT '',
  stat_date  DATE NOT NULL,
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  KEY idx_staff_date (staff_id, stat_date),
  KEY idx_event (event_type)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

-- 这里**刻意不再预置业绩分类**（原先是 7 条「居住住宅/政务办公/…」的种子 INSERT）。
--
-- 商用版默认必须是**零业务数据**：装完就带着一套行业分类，客户第一眼看到的是
-- 别人的东西，还以为是自己建的。行业分类属于「某企业的业务内容」，不是表结构，
-- 因此归 internal/demoseed（演示数据包）与「新建企业时复制的分类模板」管，
-- 不归迁移管。
--
-- 已上线的库不受影响：迁移按文件名记账（schema_migrations），删掉语句只影响
-- 从此往后新建的空库——而那正是我们要它变干净的那个。
