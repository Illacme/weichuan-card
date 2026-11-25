-- 0014：系统运行参数「后台可视化配置」基础表 sys_setting。
-- 把过去散落在 config / main / storage / auth 等处的写死值收敛为一张表，
-- 平台超管可在 PC 后台「系统设置」界面统一维护，无需改代码、无需重启即可生效。
-- 密钥类（DB/JWT/云存储/CDN/微信 AppSecret/Cookie）始终留环境变量，不进本表。
-- 与 devserver AutoMigrate 保持一致（见 internal/setting/setting.go 的 Init）。

CREATE TABLE IF NOT EXISTS sys_setting (
  id          BIGINT       NOT NULL AUTO_INCREMENT,
  `key`       VARCHAR(64)  NOT NULL,
  `group`     VARCHAR(32)  NOT NULL DEFAULT '',
  value       TEXT,
  type        VARCHAR(16)  NOT NULL DEFAULT 'string',
  description TEXT,
  PRIMARY KEY (id),
  UNIQUE KEY uk_setting_key (`key`),
  KEY idx_setting_group (`group`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
