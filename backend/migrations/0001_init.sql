-- 纯净终态 schema：由 scripts/gen_migrations_clean_schema.py 从 history/ 推演生成。
-- 全新初始化安装用本文件建表；数据由安装向导与内置档案 ensure 写入。
-- 升级安装走 migrations/history/ 的增量脚本，按 schema_migrations 记账。
-- DO NOT EDIT：改 schema 请改 history/ 后重跑生成脚本。

CREATE TABLE IF NOT EXISTS employees (
  staff_id BIGINT NOT NULL AUTO_INCREMENT PRIMARY KEY,
  name     VARCHAR(512) NOT NULL DEFAULT '',
  title         VARCHAR(64)  NOT NULL DEFAULT '',
  avatar        VARCHAR(255) NOT NULL DEFAULT '',
  phone    VARCHAR(512) NOT NULL DEFAULT '',
  email    VARCHAR(512) NOT NULL DEFAULT '',
  password_hash VARCHAR(255) NOT NULL DEFAULT '',
  status        VARCHAR(16)  NOT NULL DEFAULT 'enabled',
  created_at    DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  token_version INT NOT NULL DEFAULT 0,
  landline VARCHAR(32) NOT NULL DEFAULT '',
  openid VARCHAR(64) NOT NULL DEFAULT '',
  nickname VARCHAR(512) NOT NULL DEFAULT '',
  company_id BIGINT NOT NULL DEFAULT 1,
  INDEX idx_employee_company (company_id),
  phone2   VARCHAR(512) NOT NULL DEFAULT '',
  name_bidx     VARCHAR(64) NOT NULL DEFAULT '',
  phone_bidx    VARCHAR(64) NOT NULL DEFAULT '',
  phone2_bidx   VARCHAR(64) NOT NULL DEFAULT '',
  email_bidx    VARCHAR(64) NOT NULL DEFAULT '',
  nickname_bidx VARCHAR(64) NOT NULL DEFAULT '',
  INDEX idx_emp_name_bidx (name_bidx, company_id),
  INDEX idx_emp_phone_bidx (phone_bidx, company_id),
  INDEX idx_emp_phone2_bidx (phone2_bidx, company_id),
  INDEX idx_emp_email_bidx (email_bidx, company_id),
  INDEX idx_emp_nickname_bidx (nickname_bidx, company_id),
  show_team_entry TINYINT(1) NOT NULL DEFAULT 0
  COMMENT '小程序「我的」页是否显示「团队成员」入口（名片墙），默认不显示',
  show_poster_entry TINYINT(1) NOT NULL DEFAULT 1
  COMMENT '小程序「我的」页是否显示「生成海报」入口，默认显示',
  show_stat_entry TINYINT(1) NOT NULL DEFAULT 1
  COMMENT '小程序「我的」页是否显示「数据统计」入口，默认显示',
  show_admin_entry TINYINT(1) NOT NULL DEFAULT 0
  COMMENT '小程序「我的」页是否显示「管理员登录」入口，默认不显示'
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS company_infos (
  id        BIGINT PRIMARY KEY AUTO_INCREMENT,
  full_name VARCHAR(128) NOT NULL DEFAULT '',
  address   VARCHAR(255) NOT NULL DEFAULT '',
  tel       VARCHAR(32)  NOT NULL DEFAULT '',
  logo      VARCHAR(255) NOT NULL DEFAULT '',
  short_name VARCHAR(64) NOT NULL DEFAULT '',
  company_id BIGINT NOT NULL DEFAULT 1,
  UNIQUE INDEX uk_company_info_company (company_id),
  mp_entry_mode VARCHAR(16) NOT NULL DEFAULT 'wall' COMMENT '小程序裸启动落地页：wall=名片墙 / staff=指定员工名片',
  mp_entry_staff_id BIGINT NOT NULL DEFAULT 0 COMMENT 'mode=staff 时的员工 staff_id；0 表示未指定（回落名片墙）',
  company_name_scale VARCHAR(16) NOT NULL DEFAULT 'large' COMMENT '首页公司名展示档位 large/medium/small/hidden'
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS company_profiles (
  id               BIGINT PRIMARY KEY AUTO_INCREMENT,
  intro_text       TEXT,
  video_source       VARCHAR(16)   NOT NULL DEFAULT '',
  video_channel_id VARCHAR(64)  NOT NULL DEFAULT '',
  video_feed_id      VARCHAR(128)  NOT NULL DEFAULT '',
  video_feed_token VARCHAR(512) NOT NULL DEFAULT '',
  video_vid          VARCHAR(64)   NOT NULL DEFAULT '',
  video_url          VARCHAR(512)  NOT NULL DEFAULT '',
  video_backup_url VARCHAR(512) NOT NULL DEFAULT '',
  video_fallback_url TINYINT(1)    NOT NULL DEFAULT 0,
  video_autoplay TINYINT(1) NOT NULL DEFAULT 0,
  backup_mp4_url   VARCHAR(512) NOT NULL DEFAULT '',
  cover            VARCHAR(512) NOT NULL DEFAULT '',
  company_id BIGINT NOT NULL DEFAULT 1,
  UNIQUE INDEX uk_company_profile_company (company_id),
  intro_font_scale VARCHAR(16) NOT NULL DEFAULT 'large' COMMENT '简介文字字号 large=28rpx/medium=26rpx/small=24rpx',
  video_channel_play_mode VARCHAR(16) NOT NULL DEFAULT 'embed' COMMENT '视频号播放方式 embed=内嵌/redirect=跳转打开'
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS achievement_categories (
  id        BIGINT PRIMARY KEY AUTO_INCREMENT,
  name      VARCHAR(64) NOT NULL DEFAULT '',
  icon      VARCHAR(512) NOT NULL DEFAULT '',
  sort      INT NOT NULL DEFAULT 0,
  is_locked TINYINT(1) NOT NULL DEFAULT 0,
  deleted_at DATETIME NULL,
  company_id BIGINT NOT NULL DEFAULT 1,
  INDEX idx_category_company (company_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS project_cases (
  id               BIGINT PRIMARY KEY AUTO_INCREMENT,
  category_id      BIGINT NOT NULL DEFAULT 0,
  name             VARCHAR(128) NOT NULL DEFAULT '',
  summary          VARCHAR(512) NOT NULL DEFAULT '',
  detail MEDIUMTEXT,
  cover            VARCHAR(512) NOT NULL DEFAULT '',
  album            JSON,
  is_home_featured TINYINT(1) NOT NULL DEFAULT 0,
  enabled          TINYINT(1) NOT NULL DEFAULT 1,
  sort             INT NOT NULL DEFAULT 0,
  created_at       DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  KEY idx_cat (category_id),
  KEY idx_featured (is_home_featured, enabled),
  deleted_at DATETIME NULL,
  updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  company_id BIGINT NOT NULL DEFAULT 1,
  INDEX idx_project_company (company_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS biz_services (
  id        BIGINT PRIMARY KEY AUTO_INCREMENT,
  title     VARCHAR(128) NOT NULL DEFAULT '',
  content MEDIUMTEXT,
  sort      INT NOT NULL DEFAULT 0,
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  deleted_at DATETIME NULL,
  updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  company_id BIGINT NOT NULL DEFAULT 1,
  INDEX idx_service_company (company_id),
  icon VARCHAR(512) NOT NULL DEFAULT ''
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS home_layouts (
  id        BIGINT PRIMARY KEY AUTO_INCREMENT,
  order_col JSON,
  company_id BIGINT NOT NULL DEFAULT 1,
  UNIQUE INDEX uk_home_layout_company (company_id),
  bg_image VARCHAR(512) NOT NULL DEFAULT '',
  bg_mask INT NOT NULL DEFAULT 0,
  project_time_mode VARCHAR(16) NOT NULL DEFAULT 'date' COMMENT '项目案例详情页时间显示：hidden/date/minute',
  service_time_mode VARCHAR(16) NOT NULL DEFAULT 'date' COMMENT '业务服务详情页时间显示：hidden/date/minute',
  style_id VARCHAR(32) NOT NULL DEFAULT 'default' COMMENT '整体风格（布局族）：default/ink/warm/teal',
  brand_color_id VARCHAR(32) DEFAULT NULL COMMENT '品牌色系（颜色层），NULL=用风格默认色；P1 仅 default 下挂 brand-*',
  bg_gradient_strength INT NOT NULL DEFAULT 100 COMMENT '首页顶部品牌主色浓度 0~100，100=原色',
  category_limit INT NOT NULL DEFAULT 20 COMMENT '首页业绩分类最多显示数量 0~20，0=隐藏分类网格',
  featured_count INT NOT NULL DEFAULT 3 COMMENT '首页精选案例默认呈现数量 0~3，0=不显示，不足按实际',
  featured_effect VARCHAR(16) NOT NULL DEFAULT 'static' COMMENT '精选案例循环效果 static/fade/scroll/flip',
  show_category_name TINYINT(1) NOT NULL DEFAULT 0 COMMENT '业绩分类封面图下方是否显示名称，默认0=图内已有名称不重复',
  show_service_name TINYINT(1) NOT NULL DEFAULT 0 COMMENT '业务服务封面图下方是否显示名称，默认0=图内已有名称不重复',
  logo_size VARCHAR(16) NOT NULL DEFAULT 'medium' COMMENT '名片卡 LOGO 尺寸档 small/medium/large',
  logo_align VARCHAR(16) NOT NULL DEFAULT 'center' COMMENT '名片卡 LOGO 水平对齐 left/center/right'
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS stat_events (
  id         BIGINT PRIMARY KEY AUTO_INCREMENT,
  staff_id   BIGINT NOT NULL DEFAULT 0,
  event_type VARCHAR(16) NOT NULL DEFAULT '',
  target_id BIGINT NOT NULL DEFAULT 0,
  source VARCHAR(16) NOT NULL DEFAULT '',
  stat_date  DATE NOT NULL,
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  KEY idx_staff_date (staff_id, stat_date),
  KEY idx_event (event_type),
  INDEX idx_event_target (event_type, target_id),
  INDEX idx_event_source (event_type, source)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS admins (
  id            BIGINT PRIMARY KEY AUTO_INCREMENT,
  username      VARCHAR(64) NOT NULL DEFAULT '',
  password_hash VARCHAR(255) NOT NULL DEFAULT '',
  created_at    DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  UNIQUE KEY uk_username (username),
  token_version INT NOT NULL DEFAULT 0,
  wx_openid VARCHAR(64) NOT NULL DEFAULT '' COMMENT '微信 openid（小程序管理员免密登录，空=未绑定）',
  company_id BIGINT NOT NULL DEFAULT 0,
  INDEX idx_admin_company (company_id),
  INDEX idx_admins_wx_openid (wx_openid)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS open_applies (
  id            BIGINT PRIMARY KEY AUTO_INCREMENT,
  name     VARCHAR(512) NOT NULL DEFAULT '',
  phone    VARCHAR(512) NOT NULL DEFAULT '',
  email    VARCHAR(512) NOT NULL DEFAULT '',
  title         VARCHAR(64)  NOT NULL DEFAULT '',
  avatar        VARCHAR(255) NOT NULL DEFAULT '',
  status        VARCHAR(16)  NOT NULL DEFAULT 'pending',
  reject_reason VARCHAR(255) NOT NULL DEFAULT '',
  created_at    DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  KEY idx_status (status),
  landline VARCHAR(32) NOT NULL DEFAULT '',
  openid VARCHAR(64) NOT NULL DEFAULT '',
  nickname VARCHAR(512) NOT NULL DEFAULT '',
  company_id BIGINT NOT NULL DEFAULT 1,
  INDEX idx_open_apply_company (company_id),
  phone2   VARCHAR(512) NOT NULL DEFAULT '',
  name_bidx     VARCHAR(64) NOT NULL DEFAULT '',
  phone_bidx    VARCHAR(64) NOT NULL DEFAULT '',
  phone2_bidx   VARCHAR(64) NOT NULL DEFAULT '',
  email_bidx    VARCHAR(64) NOT NULL DEFAULT '',
  nickname_bidx VARCHAR(64) NOT NULL DEFAULT '',
  INDEX idx_open_apply_name_bidx (name_bidx, company_id),
  INDEX idx_open_apply_phone_bidx (phone_bidx, company_id),
  INDEX idx_open_apply_phone2_bidx (phone2_bidx, company_id),
  INDEX idx_open_apply_email_bidx (email_bidx, company_id),
  INDEX idx_open_apply_nickname_bidx (nickname_bidx, company_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS card_modify_applies (
  id            BIGINT PRIMARY KEY AUTO_INCREMENT,
  staff_id      BIGINT NOT NULL DEFAULT 0,
  snapshot LONGTEXT,
  status        VARCHAR(16) NOT NULL DEFAULT 'pending',
  reject_reason VARCHAR(255) NOT NULL DEFAULT '',
  created_at    DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  KEY idx_staff (staff_id),
  KEY idx_status (status),
  company_id BIGINT NOT NULL DEFAULT 1,
  INDEX idx_modify_apply_company (company_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS revoked_sessions (
  sid        VARCHAR(64) NOT NULL,
  user_type  VARCHAR(16) NOT NULL DEFAULT '',
  user_id    BIGINT      NOT NULL DEFAULT 0,
  expires_at DATETIME    NOT NULL DEFAULT CURRENT_TIMESTAMP,
  created_at DATETIME    NOT NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (sid),
  KEY idx_revoked_sessions_expires_at (expires_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS company (
  id         BIGINT PRIMARY KEY AUTO_INCREMENT,
  name       VARCHAR(128) NOT NULL DEFAULT '',
  status     VARCHAR(16)  NOT NULL DEFAULT 'enabled',
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  short_name VARCHAR(64) NOT NULL DEFAULT '',
  login_code VARCHAR(64) NULL COMMENT '登录页白标码（?company=），仅精确匹配',
  local_mirror_enabled TINYINT(1) NOT NULL DEFAULT 0 COMMENT '该企业是否开启对象本地镜像备份（0=关闭，1=开启）'
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

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

CREATE TABLE IF NOT EXISTS audit_log (
  id          BIGINT       NOT NULL AUTO_INCREMENT,
  actor_type  VARCHAR(16)  NOT NULL DEFAULT '',
  -- admin / employee / anonymous
  actor_id    BIGINT       NOT NULL DEFAULT 0,
  actor_name  VARCHAR(64)  NOT NULL DEFAULT '',
  -- 冗余用户名：账号删除后仍可追溯
  company_id  BIGINT       NOT NULL DEFAULT 0,
  -- 操作者所属租户（0=平台）
  action      VARCHAR(32)  NOT NULL DEFAULT '',
  -- setting.update / admin.reset_password ...
  target_type VARCHAR(32)  NOT NULL DEFAULT '',
  -- setting / admin / company / employee ...
  target_id   VARCHAR(64)  NOT NULL DEFAULT '',
  detail      TEXT,
  -- JSON 变更摘要（敏感值脱敏）
  ip          VARCHAR(64)  NOT NULL DEFAULT '',
  result      VARCHAR(16)  NOT NULL DEFAULT '',
  -- success / fail
  created_at  DATETIME(3)  NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  PRIMARY KEY (id),
  KEY idx_audit_created (created_at),
  KEY idx_audit_action (action),
  KEY idx_audit_actor (actor_type, actor_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS rate_limit_hits (
  bucket_key   VARCHAR(191) NOT NULL COMMENT '分桶键，如 login:1.2.3.4',
  window_start BIGINT       NOT NULL COMMENT '窗口起始 Unix 秒（对齐到窗口边界）',
  hits         INT          NOT NULL DEFAULT 0 COMMENT '该窗口内累计次数',
  PRIMARY KEY (bucket_key, window_start)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='限流计数（多副本共享，按窗口聚合）';

CREATE TABLE IF NOT EXISTS poster_prefs (
  id          BIGINT       NOT NULL AUTO_INCREMENT,
  staff_id    BIGINT       NOT NULL COMMENT '员工编号（employee.staff_id）',
  company_id  BIGINT       NOT NULL DEFAULT 0 COMMENT '租户冗余，便于按企业清理',
  bg_id       VARCHAR(64)  NOT NULL DEFAULT '' COMMENT '当前背景：空=白底 / poster-*=内置 / c-*=自定义',
  backgrounds TEXT         NULL COMMENT '自定义背景 JSON 数组 [{id,name,file}]',
  layout      TEXT         NULL COMMENT '版式 JSON（模板/显隐/配色/位移/头像大小）',
  created_at  DATETIME     NULL,
  updated_at  DATETIME     NULL,
  PRIMARY KEY (id),
  UNIQUE KEY uk_poster_prefs_staff (staff_id),
  KEY idx_poster_prefs_company (company_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='员工海报个性化设置（自定义背景 + 版式）';

CREATE TABLE IF NOT EXISTS company_wx_app (
  id         BIGINT       NOT NULL AUTO_INCREMENT,
  company_id BIGINT       NOT NULL COMMENT '租户（company.id）',
  app_id     VARCHAR(64)  NOT NULL DEFAULT '' COMMENT '小程序 AppID（wx 开头）',
  app_secret VARCHAR(128) NOT NULL DEFAULT '' COMMENT '小程序 AppSecret',
  created_at DATETIME     NULL,
  updated_at DATETIME     NULL,
  PRIMARY KEY (id),
  UNIQUE KEY uk_company_wx_app_company (company_id),
  release_enabled TINYINT(1) NOT NULL DEFAULT 1
        COMMENT '是否允许平台代发小程序版本（0=拒绝一切提审/发布/撤回）',
  upload_key VARCHAR(2048) NOT NULL DEFAULT '' COMMENT '小程序代码上传密钥（private.key 原文，只写不读）',
  subscribe_tmpl_id VARCHAR(128) NOT NULL DEFAULT '' COMMENT '审核结果通知订阅模板 ID（仅企业独立版小程序用，与 AppID 绑定）',
  subscribe_tmpl_fields VARCHAR(128) NOT NULL DEFAULT '' COMMENT '订阅模板字段名，逗号分隔，顺序=事项/结果/备注'
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='租户小程序凭据（按企业隔离，一个企业一个小程序）';

CREATE TABLE IF NOT EXISTS mp_release (
  id           BIGINT       NOT NULL AUTO_INCREMENT,
  company_id   BIGINT       NOT NULL DEFAULT 0 COMMENT '租户（company.id）；0 = 平台自有小程序',
  app_id       VARCHAR(64)  NOT NULL DEFAULT '' COMMENT '本次发布打到哪个小程序',
  user_version VARCHAR(64)  NOT NULL DEFAULT '' COMMENT '微信侧代码版本号（CI 上传时自定义）',
  user_desc    VARCHAR(255) NOT NULL DEFAULT '' COMMENT '版本说明',
  status       VARCHAR(32)  NOT NULL DEFAULT 'draft' COMMENT '状态机当前态，见 model/mp_release.go',
  audit_id     BIGINT       NOT NULL DEFAULT 0 COMMENT '微信审核单 id（提审返回）；未提审为 0',
  audit_reason VARCHAR(500) NOT NULL DEFAULT '' COMMENT '驳回/延后原因（微信原文）',
  source       VARCHAR(32)  NOT NULL DEFAULT '' COMMENT '登记来源：ci / manual',
  operator_id  BIGINT       NOT NULL DEFAULT 0 COMMENT '最后一步操作人（admin.id）',
  submitted_at DATETIME     NULL COMMENT '提审时间',
  released_at  DATETIME     NULL COMMENT '上线时间',
  created_at   DATETIME     NULL,
  updated_at   DATETIME     NULL,
  PRIMARY KEY (id),
  KEY idx_mp_release_company_status (company_id, status),
  KEY idx_mp_release_app_version (app_id, user_version)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='小程序版本发布记录（平台代租户上传/提审/发布）';

CREATE TABLE IF NOT EXISTS media_check (
  id          BIGINT       NOT NULL AUTO_INCREMENT,
  company_id  BIGINT       NOT NULL DEFAULT 0 COMMENT '租户（company.id）；0 = 平台',
  url         VARCHAR(1024) NOT NULL DEFAULT '' COMMENT '送检时提交给微信的绝对地址',
  object_key  VARCHAR(512) NOT NULL DEFAULT '' COMMENT '存储对象键，下架时用它覆盖占位图',
  media_type  INT          NOT NULL DEFAULT 2 COMMENT '1 音频 / 2 图片',
  scene       INT          NOT NULL DEFAULT 1 COMMENT '内容安全场景值，1 = 资料',
  openid      VARCHAR(64)  NOT NULL DEFAULT '' COMMENT '提交人 openid（只写不读）',
  source      VARCHAR(32)  NOT NULL DEFAULT '' COMMENT '上传来源：employee / admin / apply',
  trace_id    VARCHAR(128) NOT NULL DEFAULT '' COMMENT '微信侧追踪号（回调对账凭据）',
  status      VARCHAR(16)  NOT NULL DEFAULT 'pending' COMMENT 'pending/pass/review/risky/failed/skipped',
  suggest     VARCHAR(16)  NOT NULL DEFAULT '' COMMENT '微信原始 suggest',
  label       INT          NOT NULL DEFAULT 0 COMMENT '违规标签',
  raw         TEXT         NULL COMMENT '微信回调 result/detail 原文（申诉证据）',
  note        VARCHAR(500) NOT NULL DEFAULT '' COMMENT '跳过/失败原因',
  action      VARCHAR(32)  NOT NULL DEFAULT '' COMMENT '处置动作：空 / takedown',
  handled_at  DATETIME     NULL COMMENT '处置时间',
  handler_id  BIGINT       NOT NULL DEFAULT 0 COMMENT '操作人（admin.id）',
  created_at  DATETIME     NULL,
  updated_at  DATETIME     NULL,
  PRIMARY KEY (id),
  KEY idx_media_check_trace (trace_id),
  KEY idx_media_check_status (status),
  KEY idx_media_check_company (company_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='图片/音视频内容安全检测记录（异步送检 → 消息推送回调 → 后台下架）';

CREATE TABLE IF NOT EXISTS tenant_license (
  id         BIGINT      NOT NULL AUTO_INCREMENT,
  company_id BIGINT      NOT NULL COMMENT '租户（company.id），唯一',
  edition    VARCHAR(32) NOT NULL DEFAULT '' COMMENT '版本标识：private | platform | trial（业务展示用）',
  seats      INT         NOT NULL DEFAULT 0 COMMENT '员工席位上限；0 = 不限制',
  expires_at DATETIME    NULL COMMENT '到期时间；NULL/零值 = 不过期',
  features   VARCHAR(512) NOT NULL DEFAULT '' COMMENT '功能开关，逗号分隔；空 = 全部开通',
  created_at DATETIME    NULL,
  updated_at DATETIME    NULL,
  PRIMARY KEY (id),
  UNIQUE KEY uk_tenant_license_company (company_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='多租户授权（按企业配置席位 / 到期 / 功能开关）';

CREATE TABLE IF NOT EXISTS storage_usage (
  company_id BIGINT   NOT NULL COMMENT '租户（company.id），一个企业一行',
  bytes      BIGINT   NOT NULL DEFAULT 0 COMMENT '累计上传字节数（按上传文件大小计）',
  files      BIGINT   NOT NULL DEFAULT 0 COMMENT '累计上传文件数',
  quota_mb   BIGINT   NOT NULL DEFAULT 0 COMMENT '本租户配额覆盖（MB）；0 = 跟随全局 storage_quota_mb',
  updated_at DATETIME NULL,
  PRIMARY KEY (company_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='按租户累计的存储用量（配额检查用）';

CREATE TABLE IF NOT EXISTS storage_backfill_refs (
  id           BIGINT       NOT NULL AUTO_INCREMENT,
  plan_hash    VARCHAR(64)  NOT NULL COMMENT '计划文件字节的 sha256，标识一次搬迁',
  ref_id       VARCHAR(64)  NOT NULL COMMENT '引用身份的 sha256：表 + 主键 + 列 + 数组下标',
  table_name   VARCHAR(64)  NOT NULL COMMENT '给人看的溯源信息，不用于拼 SQL',
  pk_col       VARCHAR(64)  NOT NULL,
  pk_value     BIGINT       NOT NULL,
  ref_col      VARCHAR(64)  NOT NULL,
  array_index  INT          NOT NULL DEFAULT -1 COMMENT '-1 = 单值列，>=0 = JSON 数组下标',
  old_value    TEXT         NOT NULL COMMENT '改库前的原值，回滚依据',
  new_value    VARCHAR(512) NOT NULL COMMENT '改库后的对象键',
  company_id   BIGINT       NOT NULL,
  status       VARCHAR(16)  NOT NULL DEFAULT 'applied' COMMENT 'applied / rolled_back',
  created_at   DATETIME     NULL,
  updated_at   DATETIME     NULL,
  PRIMARY KEY (id),
  UNIQUE KEY uk_backfill_refs_plan_ref (plan_hash, ref_id),
  KEY idx_backfill_refs_plan_status (plan_hash, status)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='存量资源引用搬迁台账（P3）';

CREATE TABLE IF NOT EXISTS storage_profiles (
  id              BIGINT       NOT NULL AUTO_INCREMENT,
  name            VARCHAR(64)  NOT NULL COMMENT '档案名，展示用，唯一',
  driver          VARCHAR(16)  NOT NULL DEFAULT 'local' COMMENT 'local / minio / oss / s3',
  endpoint        VARCHAR(255) NOT NULL DEFAULT '' COMMENT 'host[:port]，不带 scheme',
  bucket          VARCHAR(128) NOT NULL DEFAULT '',
  region          VARCHAR(64)  NOT NULL DEFAULT '',
  base_url        VARCHAR(255) NOT NULL DEFAULT '' COMMENT '上传返回基址，恒为平台统一入口',
  host_header     VARCHAR(255) NOT NULL DEFAULT '' COMMENT 'virtual-hosted-style 的 Host / TLS SNI',
  use_ssl         TINYINT(1)   NOT NULL DEFAULT 1 COMMENT 'S3/OSS 构造与自检是否走 HTTPS',
  local_dir       VARCHAR(512) NOT NULL DEFAULT '' COMMENT 'local 驱动的独立根目录，留空用平台 uploads',
  access_key      VARCHAR(512) NOT NULL DEFAULT '' COMMENT 'AccessKey 明文，回显掩码',
  secret_key      VARCHAR(1024) NOT NULL DEFAULT '' COMMENT 'SecretKey 明文，回显掩码且不回传',
  public_read     TINYINT(1)   NOT NULL DEFAULT 1 COMMENT '是否公开读；第一阶段只支持公开读桶',
  status          VARCHAR(16)  NOT NULL DEFAULT 'unknown' COMMENT 'unknown / ok / error',
  last_probe_at   DATETIME     NULL,
  last_error      VARCHAR(512) NOT NULL DEFAULT '' COMMENT '脱敏后的探测错误摘要',
  version         BIGINT       NOT NULL DEFAULT 1 COMMENT '每次保存递增，作为运行时缓存失效版本',
  builtin         TINYINT(1)   NOT NULL DEFAULT 0 COMMENT '内置 local 档案，不可删除',
  created_at      DATETIME     NULL,
  updated_at      DATETIME     NULL,
  PRIMARY KEY (id),
  UNIQUE KEY uk_storage_profiles_name (name),
  purpose VARCHAR(24) NOT NULL DEFAULT '' COMMENT '内置用途：platform_primary / tenant_fallback / platform_mirror；空=普通档案'
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='存储档案：连接信息与凭据，供多套配置复用';

CREATE TABLE IF NOT EXISTS delivery_profiles (
  id              BIGINT       NOT NULL AUTO_INCREMENT,
  name            VARCHAR(64)  NOT NULL COMMENT '分发档案名，唯一',
  provider        VARCHAR(24)  NOT NULL DEFAULT 'none' COMMENT 'none / aliyun / tencent / qiniu / cloudflare / cloudfront / custom',
  domain          VARCHAR(255) NOT NULL DEFAULT '' COMMENT '公开访问域名（含 scheme），只影响出参 URL',
  access_key      VARCHAR(512) NOT NULL DEFAULT '' COMMENT 'CDN 刷新凭据，明文；回显掩码',
  secret_key      VARCHAR(1024) NOT NULL DEFAULT '' COMMENT 'CDN 刷新密钥，明文；不回传',
  endpoint        VARCHAR(255) NOT NULL DEFAULT '' COMMENT '刷新接口端点，留空按厂商默认',
  resource_id     VARCHAR(128) NOT NULL DEFAULT '' COMMENT 'Cloudflare Zone ID / CloudFront Distribution ID',
  status          VARCHAR(16)  NOT NULL DEFAULT 'unknown',
  last_probe_at   DATETIME     NULL,
  last_error      VARCHAR(512) NOT NULL DEFAULT '',
  version         BIGINT       NOT NULL DEFAULT 1,
  created_at      DATETIME     NULL,
  updated_at      DATETIME     NULL,
  PRIMARY KEY (id),
  UNIQUE KEY uk_delivery_profiles_name (name),
  builtin TINYINT(1) NOT NULL DEFAULT 0 COMMENT '是否内置档案（启动时 ensure，不可删除、不可绑定给企业）',
  purpose VARCHAR(24) NOT NULL DEFAULT '' COMMENT '内置用途：platform_primary；空=普通档案'
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='分发档案：CDN 域名与刷新凭据，独立于存储档案';

CREATE TABLE IF NOT EXISTS storage_bindings (
  id                   BIGINT      NOT NULL AUTO_INCREMENT,
  tenant_id            BIGINT      NOT NULL COMMENT '0=全局默认；>0=企业 ID',
  storage_profile_id   BIGINT      NOT NULL,
  delivery_profile_id  BIGINT      NOT NULL DEFAULT 0 COMMENT '0=不启用 CDN，直接用存储基址',
  version              BIGINT      NOT NULL DEFAULT 1 COMMENT '每次绑定变更递增，触发运行时缓存失效',
  updated_by           VARCHAR(64) NOT NULL DEFAULT '',
  created_at           DATETIME    NULL,
  updated_at           DATETIME    NULL,
  PRIMARY KEY (id),
  UNIQUE KEY uk_storage_bindings_tenant (tenant_id),
  previous_storage_profile_id BIGINT NOT NULL DEFAULT 0 COMMENT '切换前的存储档案ID，0=此绑定从未切换过',
  previous_delivery_profile_id BIGINT NOT NULL DEFAULT 0 COMMENT '切换前的分发档案ID，0=切换前未启用分发'
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='存储与分发绑定：全局默认 + 企业覆盖，企业无行即继承';

CREATE TABLE IF NOT EXISTS storage_migration_jobs (
  id                   BIGINT      NOT NULL AUTO_INCREMENT,
  tenant_id            BIGINT      NOT NULL DEFAULT 0 COMMENT '0=全部租户；>0=单企业',
  source_profile_id    BIGINT      NOT NULL,
  target_profile_id    BIGINT      NOT NULL,
  status               VARCHAR(16) NOT NULL DEFAULT 'created' COMMENT 'created/running/done/failed/canceled',
  total                BIGINT      NOT NULL DEFAULT 0,
  done                 BIGINT      NOT NULL DEFAULT 0,
  failed               BIGINT      NOT NULL DEFAULT 0,
  last_key             VARCHAR(512) NOT NULL DEFAULT '' COMMENT '断点续跑位点',
  dry_run              TINYINT(1)  NOT NULL DEFAULT 0,
  auto_switch          TINYINT(1)  NOT NULL DEFAULT 0 COMMENT '完成后是否自动把绑定切到目标档案',
  created_by           VARCHAR(64) NOT NULL DEFAULT '',
  error                VARCHAR(512) NOT NULL DEFAULT '',
  created_at           DATETIME    NULL,
  updated_at           DATETIME    NULL,
  finished_at          DATETIME    NULL,
  PRIMARY KEY (id),
  KEY idx_storage_migration_jobs_status (status)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='一键迁移任务：对象从源档案复制到目标档案';

CREATE TABLE IF NOT EXISTS storage_migration_errors (
  id         BIGINT      NOT NULL AUTO_INCREMENT,
  job_id     BIGINT      NOT NULL,
  object_key VARCHAR(512) NOT NULL,
  error      VARCHAR(512) NOT NULL DEFAULT '',
  created_at DATETIME    NULL,
  PRIMARY KEY (id),
  KEY idx_storage_migration_errors_job (job_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='迁移失败对象清单，可导出逐个重试';
