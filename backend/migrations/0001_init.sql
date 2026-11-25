-- ===== 基线合并：来源 0001_init.sql =====
;
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

-- ===== 基线合并：来源 0002_apply.sql =====
;
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

-- ===== 基线合并：来源 0003_soft_delete.sql =====
;
-- 软删除支持：为内容模型（业绩分类 / 项目案例 / 业务服务）增加 deleted_at 列。
-- GORM 的 gorm.DeletedAt 会在查询时自动附加 `deleted_at IS NULL`，
-- 删除操作改为 UPDATE 置空该列，避免误删数据；列表接口默认不返回已删除记录。
-- 仅对全新初始化的库执行：已存在的库请手动执行以下 ALTER（可重复执行，列已存在时报错可忽略）。

ALTER TABLE achievement_categories ADD COLUMN deleted_at DATETIME NULL;
ALTER TABLE project_cases ADD COLUMN deleted_at DATETIME NULL;
ALTER TABLE biz_services ADD COLUMN deleted_at DATETIME NULL;

-- ===== 基线合并：来源 0004_token_version.sql =====
;
-- 0004 令牌安全戳：用于 JWT 即时吊销（改密 / 账号禁用时自增 token_version）。
-- SQLite（测试）由 GORM AutoMigrate 自动建列；MySQL（生产 / parity）由本迁移补齐。

ALTER TABLE employees ADD COLUMN token_version INT NOT NULL DEFAULT 0;
ALTER TABLE admins ADD COLUMN token_version INT NOT NULL DEFAULT 0;

-- ===== 基线合并：来源 0005_revoked_sessions.sql =====
;
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

-- ===== 基线合并：来源 0006_employee_landline.sql =====
;
-- 0006 员工座机（landline）：员工个人办公电话，与 company_infos.tel（公司总机）分层。
-- 小程序名片「座机」行优先展示 employees.landline，为空时回退公司总机（见 employees-card 组件）。
-- SQLite（测试 / devserver）由 GORM AutoMigrate 自动建列；MySQL（生产 / parity）由本迁移补齐。
-- 存量库需手动执行；重复执行报「列已存在」可忽略。

ALTER TABLE employees ADD COLUMN landline VARCHAR(32) NOT NULL DEFAULT '';

-- ===== 基线合并：来源 0007_updated_at.sql =====
;
-- 0007 项目案例 / 业务服务补充 updated_at：详情接口需返回「最后更新时间」
-- （见《跳转页组件与接口字段映射》§2.2/§3.2），PC 后台每次编辑由 GORM 自动维护。
-- SQLite（测试 / devserver）由 GORM AutoMigrate 自动建列；MySQL（生产 / parity）由本迁移补齐。
-- 存量库需手动执行；重复执行报「列已存在」可忽略。
-- MySQL 5.6.5+ 允许多个 DATETIME 列同带 CURRENT_TIMESTAMP 默认值，与 created_at 不冲突。

ALTER TABLE project_cases ADD COLUMN updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP;
ALTER TABLE biz_services ADD COLUMN updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP;

-- ===== 基线合并：来源 0008_open_apply_landline.sql =====
;
-- 0008：开通申请增加座机字段（与 devserver AutoMigrate 保持一致）
-- 申请开通时收集座机，通过审核后一并写入员工表（employees 的 landline 已在 0006 加入）。

ALTER TABLE open_applies ADD COLUMN landline VARCHAR(32) NOT NULL DEFAULT '';

-- ===== 基线合并：来源 0009_company_short_name.sql =====
;
-- 0009：企业简称（小程序顶栏居中展示），与 devserver AutoMigrate 保持一致

ALTER TABLE company_infos ADD COLUMN short_name VARCHAR(64) NOT NULL DEFAULT '';

-- ===== 基线合并：来源 0010_wechat_identity.sql =====
;
-- 0010：微信身份采集（openid + 昵称），与 devserver AutoMigrate 保持一致
-- openid：用户在本小程序内的唯一标识（wx.login → jscode2session 换取），供后台核实申请人/登录者身份。
-- nickname：微信昵称（用户授权后采集）。申请开通时记录，审核通过一并写入员工表。

ALTER TABLE open_applies ADD COLUMN openid VARCHAR(64) NOT NULL DEFAULT '';
ALTER TABLE open_applies ADD COLUMN nickname VARCHAR(64) NOT NULL DEFAULT '';

ALTER TABLE employees ADD COLUMN openid VARCHAR(64) NOT NULL DEFAULT '';
ALTER TABLE employees ADD COLUMN nickname VARCHAR(64) NOT NULL DEFAULT '';

-- ===== 基线合并：来源 0011_company_tenant.sql =====
;
-- 0011：多租户第一期（后台多租户 + 数据隔离）
-- 设计：
--   * company 为租户表；朗汇机电为 1 号租户，存量数据全部归属（company_id 默认 1）。
--   * company_infos / company_profiles / home_layouts 由 id=1 单例改为按 company_id 唯一（1:1）。
--   * employees / achievement_categories / project_cases / biz_services / open_applies /
--     card_modify_applies 增加 company_id（NOT NULL DEFAULT 1 + 索引）。
--   * admins 增加 company_id：0 = 平台超级管理员（可见全部租户），>0 = 企业管理员（仅本企业）。
--   * 小程序端公开接口缺省落 company_id=1（朗汇），行为不变；按企业分发留给第二期。
-- 与 devserver AutoMigrate（model 包）保持一致。

CREATE TABLE IF NOT EXISTS company (
  id         BIGINT PRIMARY KEY AUTO_INCREMENT,
  name       VARCHAR(128) NOT NULL DEFAULT '',
  status     VARCHAR(16)  NOT NULL DEFAULT 'enabled',
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

-- 这里**刻意不再预置朗汇机电**（原先是 INSERT company (1, ...)）。
--
-- 商用版装完必须是一家企业都没有：由平台超管先「新建企业」，再逐步录入信息、
-- 员工与内容，小程序才立得起来。预置一家真实客户的企业名与白标码，
-- 等于把演示数据当成了产品初始状态。
--
-- 需要样板数据时走安装向导的「加载演示企业数据」勾选，数据由 internal/demoseed
-- 写入（同样落 1 号企业，因为 defaultCompanyID = 1 是小程序公开接口的缺省租户）。
--
-- 下方 ALTER 的 DEFAULT 1 保留：它是「存量行回填」的写法，空库执行时无行可填，
-- 不影响零数据。已上线的库不受影响（迁移按文件名记账，不会重放本文件）。

ALTER TABLE company_infos    ADD COLUMN company_id BIGINT NOT NULL DEFAULT 1;
ALTER TABLE company_profiles ADD COLUMN company_id BIGINT NOT NULL DEFAULT 1;
ALTER TABLE home_layouts     ADD COLUMN company_id BIGINT NOT NULL DEFAULT 1;
ALTER TABLE employees        ADD COLUMN company_id BIGINT NOT NULL DEFAULT 1;
ALTER TABLE achievement_categories ADD COLUMN company_id BIGINT NOT NULL DEFAULT 1;
ALTER TABLE project_cases    ADD COLUMN company_id BIGINT NOT NULL DEFAULT 1;
ALTER TABLE biz_services     ADD COLUMN company_id BIGINT NOT NULL DEFAULT 1;
ALTER TABLE open_applies      ADD COLUMN company_id BIGINT NOT NULL DEFAULT 1;
ALTER TABLE card_modify_applies ADD COLUMN company_id BIGINT NOT NULL DEFAULT 1;
ALTER TABLE admins           ADD COLUMN company_id BIGINT NOT NULL DEFAULT 0;

-- 单例表按租户唯一；存量行已由 DEFAULT 回填为 1（朗汇）
ALTER TABLE company_infos    ADD UNIQUE INDEX uk_company_info_company (company_id);
ALTER TABLE company_profiles ADD UNIQUE INDEX uk_company_profile_company (company_id);
ALTER TABLE home_layouts     ADD UNIQUE INDEX uk_home_layout_company (company_id);

-- 业务表租户索引（后台列表按企业过滤）
ALTER TABLE employees        ADD INDEX idx_employee_company (company_id);
ALTER TABLE achievement_categories ADD INDEX idx_category_company (company_id);
ALTER TABLE project_cases    ADD INDEX idx_project_company (company_id);
ALTER TABLE biz_services     ADD INDEX idx_service_company (company_id);
ALTER TABLE open_applies      ADD INDEX idx_open_apply_company (company_id);
ALTER TABLE card_modify_applies ADD INDEX idx_modify_apply_company (company_id);
ALTER TABLE admins           ADD INDEX idx_admin_company (company_id);

-- ===== 基线合并：来源 0012_company_admin_short_name.sql =====
;
-- 0012：企业简称（company.short_name）——PC 后台侧边栏「xxxx · 管理后台」品牌名，
-- 并作为开通企业时公司信息简称（company_infos.short_name，仅小程序展示）的初始值。
-- 与 0009 的 company_infos.short_name 相互独立：后台侧边栏读本列，小程序读公司信息。
-- 与 devserver AutoMigrate 保持一致。

ALTER TABLE company ADD COLUMN short_name VARCHAR(64) NOT NULL DEFAULT '';
-- 历史种子：当时的 1 号演示租户（朗汇）简称。全新安装的空库执行到这里时没有
-- id=1 的行，UPDATE 命中 0 行；演示企业现在由安装向导 loadDemoData 显式装载，
-- 不依赖这条语句。保留 SQL 是为了不改动已应用过的迁移，别把它当当前配置依赖。
UPDATE company SET short_name = '朗汇机电' WHERE id = 1;

-- ===== 基线合并：来源 0013_home_layout_background.sql =====
;
-- 0013：小程序首页背景图（home_layouts.bg_image / bg_mask）——商业化高端美化包装。
-- bg_image 首页全屏背景图 URL（可从内置素材库「首页背景」直接选用）；
-- bg_mask 背景遮罩浓度 0~100（叠加黑色蒙层，保证白卡片与文字可读性）。
-- 两者均为可选：bg_image 为空即保持原有灰底布局，不设背景。
-- 与 devserver AutoMigrate 保持一致。

ALTER TABLE home_layouts ADD COLUMN bg_image VARCHAR(512) NOT NULL DEFAULT '';
ALTER TABLE home_layouts ADD COLUMN bg_mask INT NOT NULL DEFAULT 0;

-- ===== 基线合并：来源 0014_sys_setting.sql =====
;
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

-- ===== 基线合并：来源 0015_audit_log.sql =====
;
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

-- ===== 基线合并：来源 0016_company_profile_video_source.sql =====
;
-- 0016 公司简介视频：单「视频号 + 降级 MP4」改为 video_source 五档多来源
--
-- 背景：原 company_profiles 只有 video_channel_id + backup_mp4_url，表达不了
-- 「云存储上传」与「外部 MP4 直链」的区别（前者走对象存储直传、后者是客户自备 URL），
-- 也无法让客户显式选档 + 单独开关兜底。现统一为：
--   video_source = none     不展示视频，显示封面占位（默认）
--                  channels 微信视频号 <channel-video feed-id>（零存储零流量，官方无资质要求）
--                  upload   云存储上传（对象存储直传，不经应用服务器）
--                  url      外部 MP4 直链
--                  tencent  腾讯视频插件（预留，服务端暂拒；插件须在 app.json 静态声明）
-- video_url 由 upload / url 两档共用，并作为 channels / tencent 开兜底时的回落源。
--
-- 只加列、不删列：backup_mp4_url 保留（代码已不再读写），出问题可直接回滚。

ALTER TABLE company_profiles
  ADD COLUMN video_source       VARCHAR(16)   NOT NULL DEFAULT '' AFTER intro_text,
  ADD COLUMN video_feed_id      VARCHAR(128)  NOT NULL DEFAULT '' AFTER video_channel_id,
  ADD COLUMN video_vid          VARCHAR(64)   NOT NULL DEFAULT '' AFTER video_feed_id,
  ADD COLUMN video_url          VARCHAR(512)  NOT NULL DEFAULT '' AFTER video_vid,
  ADD COLUMN video_fallback_url TINYINT(1)    NOT NULL DEFAULT 0 AFTER video_url;

-- 存量搬迁：先搬 URL，再按「有 URL 算直链、只有视频号 ID 算视频号」定档，最后兜底 none。
-- 顺序不能反——否则有 URL 的行会被 video_channel_id 抢先定成 channels。
UPDATE company_profiles SET video_url = backup_mp4_url WHERE video_url = '' AND backup_mp4_url <> '';
UPDATE company_profiles SET video_source = 'url'      WHERE video_source = '' AND video_url <> '';
UPDATE company_profiles SET video_source = 'channels' WHERE video_source = '' AND video_channel_id <> '';
UPDATE company_profiles SET video_source = 'none'     WHERE video_source = '';

-- ===== 基线合并：来源 0017_company_profile_feed_token.sql =====
;
-- 0017 公司简介视频：新增 feed-token（跨主体通道）
--
-- 背景：channel-video 的 feed-id 官方明确「仅视频号视频与小程序同主体时生效」。
-- 平台模式下（我们的小程序服务多家客户），客户的视频号与我们小程序几乎必然不同主体，
-- feed-id 会直接播放失败。官方给的跨主体通道是 feed-token（基础库 2.31.1+，
-- 非个人主体小程序），代价是强制 autoplay=false（不能自动播放）。
--
-- 与 feed_id 二选一，feed_id 优先（同主体可自动播放、且从视频号助手复制即可，更稳定）。
-- feed-token 的获取有 24 小时窗口且只对操作者本人可见，属于需要人工协助配置的字段。

ALTER TABLE company_profiles
  ADD COLUMN video_feed_token VARCHAR(512) NOT NULL DEFAULT '' AFTER video_feed_id;

-- ===== 基线合并：来源 0018_phone2.sql =====
;
-- 0018 员工第二个手机号（phone2）：名片「手机」行下方并列展示，选填，不参与登录。
-- 与 landline 同一套路：员工自助申请（open_applies）与员工表（employees）都要有，
-- 审核通过时由申请行搬到员工行（见 handler/apply.go 的 approve）。
-- SQLite（测试 / devserver）由 GORM AutoMigrate 自动建列；MySQL（生产 / parity）由本迁移补齐。
-- 存量库需手动执行；重复执行报「列已存在」可忽略。

ALTER TABLE employees ADD COLUMN phone2 VARCHAR(32) NOT NULL DEFAULT '';

-- 申请开通时收集第二个手机号，审核通过后一并写入员工表
ALTER TABLE open_applies ADD COLUMN phone2 VARCHAR(32) NOT NULL DEFAULT '';

-- ===== 基线合并：来源 0019_biz_service_icon.sql =====
;
-- 0019：业务服务图标（biz_services.icon）——首页服务区卡片展示用。
-- 背景：icon 随模型新增，SQLite（测试 / devserver）由 GORM AutoMigrate 自动建列；
-- MySQL 侧漏了对应迁移，compose MySQL 形态下业务服务增改一直报
-- "Unknown column 'icon'"（2026-10-21 MySQL parity 首跑抓出）。
-- 与 achievement_categories.icon（0001）同型：URL / 路径，512 足够。
-- 存量库需手动执行；重复执行报「列已存在」可忽略。

ALTER TABLE biz_services ADD COLUMN icon VARCHAR(512) NOT NULL DEFAULT '';

-- ===== 基线合并：来源 0020_rate_limit_hits.sql =====
;
-- 0020：限流计数表（rate_limit_hits）——多副本共享的限流分桶。
-- 背景：进程内限流器在多副本下每个副本各有一份桶，实际阈值变成
-- 「单副本阈值 × 副本数」——登录防爆破从 10 次/分悄悄翻倍，且不报错。
-- 计数落到同一张表后，同一窗口内所有副本打同一行，阈值与副本数无关。
-- 详见 internal/model/models.go 的 RateLimitHit 注释。
-- SQLite（测试 / devserver）由 GORM AutoMigrate 自动建表；MySQL（生产 / parity）由本迁移补齐。
-- 存量库需手动执行；本语句自带存在性兜底，重复执行不会报错。

CREATE TABLE IF NOT EXISTS rate_limit_hits (
    bucket_key   VARCHAR(191) NOT NULL COMMENT '分桶键，如 login:1.2.3.4',
    window_start BIGINT       NOT NULL COMMENT '窗口起始 Unix 秒（对齐到窗口边界）',
    hits         INT          NOT NULL DEFAULT 0 COMMENT '该窗口内累计次数',
    PRIMARY KEY (bucket_key, window_start)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='限流计数（多副本共享，按窗口聚合）';

-- 过期窗口行由 repository.StartRateLimitPurge 定期清理，这里不建定时任务。

-- ===== 基线合并：来源 0021_stat_events_target_id.sql =====
;
-- 0021：stat_events 加 target_id 列——埋点从「只记转化」扩展到「访客对什么内容感兴趣」。
--
-- 背景：原三类事件（visit/call/save）全发生在首页与操作栏，回答的是
-- 「有没有人找上门」；企业客户更需要「访客看了哪个案例、哪个服务、视频，
-- 转发了什么、存了海报」——这些事件都发生在内容页，且大多关联一个具体
-- 业务对象（案例 / 服务）。只有计数没有 target_id，后台只能知道「有人看
-- 案例」，永远不知道「哪个案例热」——后者才是内容运营与销售跟进的依据。
--
-- target_id 语义：关联业务对象的 id（案例 / 服务）；无关联对象的事件
-- （visit / video_play / poster_save / 转发名片）记 0。事件类型的完整
-- 清单与各类型的 target 约定见 internal/handler/stat.go 的 statEventTypes。
--
-- 复合索引 (event_type, target_id)：后台「内容浏览 TOP 榜」的查询形状是
--   WHERE event_type = 'project_view' ... GROUP BY target_id ORDER BY n DESC，
-- 前缀 event_type 走索引过滤，target_id 随聚合覆盖，不必回表。
--
-- SQLite（测试 / devserver）由 GORM AutoMigrate 自动补列；MySQL（生产 /
-- parity）由本迁移补齐。存量库需手动执行；ALTER/INDEX 命中已存在的列 /
-- 索引时框架按 Duplicate 错误忽略（见 internal/migrate/apply.go 包注释），
-- 重复执行不会报错。

ALTER TABLE stat_events
  ADD COLUMN target_id BIGINT NOT NULL DEFAULT 0 AFTER event_type,
  ADD INDEX idx_event_target (event_type, target_id);

-- ===== 基线合并：来源 0022_stat_events_source.sql =====
;
-- 0022：stat_events 加 source 列——统计「流量来源」与「裂变」。
--
-- 背景：原埋点只回答「有多少人、点了多少次」与「对什么内容感兴趣」，
-- 但企业客户更关心「访客从哪来」与「谁的名片被转发带来了新访客」。
-- 这两件事共用一列 source 即可，不必各加一张表：
--
--   * 流量来源分布（sourceDist）：对 visit 事件按 source 分组，
--     回答「扫码 vs 转发 vs 搜索 vs 海报」各占多少——运营才知道该把精力
--     投到哪个渠道（比如发现搜索占大头，就该优化小程序名称/关键词）。
--   * 裂变榜（fissionTop）：对 share_card 来源的 visit 事件按 staff_id 分组，
--     回答「哪个员工名片的转发带来的新访客最多」——裂变能力的排行。
--
-- source 的取值（后端 stat.go 的 statSourceTypes 白名单归一）：
--   scan        扫码（二维码 / 小程序码，含海报上的码）
--   share_card  转发卡片（聊天/群聊/App/小程序互跳，scene 1007/1008/1036/...）
--   search      微信搜索 / 发现 / 我的小程序等入口
--   poster      扫海报上的小程序码（poster 页生成码时带 &from=poster 标记）
--   unknown     缺省（老客户端 / 历史数据未带 source）
--   other       带了的、但不在白名单里的脏值（永不进白名单，避免脏数据污染分布）
--
-- 安全边界（重要）：source 来自客户端，只能当「统计维度」，
-- 绝不参与权限 / 租户 / 计费任何判断。白名单归一就是为了挡住脏值——
-- 否则谁都能往统计里塞一个自定义来源、把分布图玩坏（静默污染）。
--
-- 复合索引 (event_type, source)：来源分布查询形状是
--   WHERE event_type = 'visit' ... GROUP BY source，
-- 前缀 event_type 过滤、source 随聚合覆盖，不必回表。
--
-- SQLite（测试 / devserver）由 GORM AutoMigrate 自动补列；MySQL（生产 /
-- parity）由本迁移补齐。存量库需手动执行；ALTER/INDEX 命中已存在列 / 索引时
-- 框架按 Duplicate 错误忽略（见 internal/migrate/apply.go 包注释），重复执行不报错。

ALTER TABLE stat_events
  ADD COLUMN source VARCHAR(16) NOT NULL DEFAULT '' AFTER target_id,
  ADD INDEX idx_event_source (event_type, source);

-- ===== 基线合并：来源 0023_poster_pref.sql =====
;
-- 0023：员工海报个性化设置表（poster_prefs）——自定义背景 + 版式。
--
-- 背景：海报页原来只能从内置素材库（24 节气 + 20 场景）里挑一张，员工想用
-- 自己的照片 / 公司活动照当背景就无从下手；版式也是写死的（头像左上、
-- 信息竖排），所有人发出去的海报长得一模一样。
--
-- 本表存两件事：
--   bg_id       当前选中的背景（"" 白底 / poster-* 内置素材 / c-* 自定义）
--   backgrounds 自定义背景图清单（JSON 数组，最多 9 条，见 handler/poster.go）
--   layout      版式配置（JSON：模板 / 元素显隐 / 配色 / 位移 / 头像大小）
--
-- 为什么不拆列：版式可选项会随需求增删，拆列每加一项都要写迁移；
-- JSON 在 handler 层用白名单校验归一（normalizePosterLayout），脏数据进不来。
--
-- 为什么不复用 employee 表：employee 的写入走「提交 → 待审 → 通过」审核流
-- （CardModifyApply），而海报版式是纯个人偏好——调个字号还要管理员审批很荒谬。
--
-- staff_id 唯一：一个员工一行，UPSERT 更新。
-- SQLite（测试 / devserver）由 GORM AutoMigrate 自动建表；MySQL（生产 / parity）由本迁移补齐。
-- 存量库需手动执行；本语句自带存在性兜底，重复执行不会报错。

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

-- ===== 基线合并：来源 0024_company_wx_app.sql =====
;
-- 0024：租户小程序凭据表（company_wx_app）——按企业隔离微信小程序 AppID / AppSecret。
--
-- 背景：小程序有两种形态（见 miniprogram/utils/tenant.ts）
--   1) 平台模式：一个小程序服务所有企业，凭据全局一份即可
--      （sys_setting 的 wx_appid / wx_appsecret）
--   2) 企业独立版：每企业一个小程序（pinnedCompanyId 锁定），**共用同一套后端**
--      一个后端要服务 N 个 AppID，全局那一份只能生成其中一个小程序的码——
--      其余企业的员工海报，码扫出来是别的小程序，直接打不开。
--
-- 所以凭据必须从「平台级」下沉到「租户级」。取值链三级回落：
--   租户凭据（本表） → 平台全局（sys_setting） → 环境变量
-- 平台模式不用给每个租户配，自动落到全局那一份；企业版配自己的即可。
--
-- 为什么不拆列/为什么不放 company 表：凭据是「集成配置」，与租户主数据
-- 变更频率、权限粒度都不同；独立表将来加「小程序名称 / 原始 ID / 多个小程序」
-- 不必动 company。company_id 唯一：一个企业一个小程序。
--
-- app_secret 以明文入库（与 sys_setting.wx_appsecret 同策略）：DB 落盘本身
-- 已由部署方控制（MySQL 不对外、SQLite 仅测试用），真正的防线是接口层——
-- 读取时只回掩码（见 handler 的 mask 常量），审计日志脱敏。
--
-- SQLite（测试 / devserver）由 GORM AutoMigrate 自动建表；
-- MySQL（生产 / parity）由本迁移补齐。存量库需手动执行；重复执行不会报错。

CREATE TABLE IF NOT EXISTS company_wx_app (
    id         BIGINT       NOT NULL AUTO_INCREMENT,
    company_id BIGINT       NOT NULL COMMENT '租户（company.id）',
    app_id     VARCHAR(64)  NOT NULL DEFAULT '' COMMENT '小程序 AppID（wx 开头）',
    app_secret VARCHAR(128) NOT NULL DEFAULT '' COMMENT '小程序 AppSecret',
    created_at DATETIME     NULL,
    updated_at DATETIME     NULL,
    PRIMARY KEY (id),
    UNIQUE KEY uk_company_wx_app_company (company_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='租户小程序凭据（按企业隔离，一个企业一个小程序）';

-- ===== 基线合并：来源 0025_mp_release.sql =====
;
-- 0025：小程序版本发布记录表（mp_release）——平台代租户上传 / 提审 / 发布。
--
-- 背景：产品支持「企业独立小程序」（每企业一个 appid，共用同一套后端，见
-- migrations/0024）。版本发布原先只能由租户自己登进微信公众平台点，平台侧
-- 既看不到进度，也没有痕迹。本表把「哪一版、审到哪了、谁操作的」落下来。
--
-- 三条设计取舍：
--   1) **不存代码包**。代码由 CI 或后台自己上传（后台那条路见
--      internal/mpupload 与 migrations/0027 的上传密钥），本表只存版本号与状态。
--      真要回溯代码，按 user_version 去构建记录里找——代码和它的构建上下文是
--      一体的，拆开存两边都会对不上。
--   2) **audit_id 不建唯一索引**。同一 auditid 只会有一行在审，但驳回后重新
--      提审会拿到新的 auditid；若给 audit_id 加唯一约束，「重新提审」就得先
--      清空旧值，反而容易写出「审计单 id 丢失」的脏数据。
--   3) **三个时间戳可空**。未提审的版本没有 submitted_at / released_at，
--      用 DATETIME NULL 而不是 '1970-01-01' 这种哨兵——空值在 SQL 里就能过滤，
--      哨兵值要靠应用层记着特殊值是什么。
--
-- source 取值：ci（CI 传的）/ manual（人工登记）/ backend（后台自己传的，
-- 版本号与代码必然一致，见 migrations/0027）。
--
-- 状态机见 internal/model/mp_release.go 的 canTransition，**只有白名单内的
-- 流向才允许发生**。注意微信侧约束：提审消耗审核额度，撤回不返还。
--
-- SQLite（测试 / devserver）由 GORM AutoMigrate 自动建表；
-- MySQL（生产 / parity）由本迁移补齐。存量库需手动执行；重复执行不会报错。

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

-- ===== 基线合并：来源 0026_company_wx_app_release_enabled.sql =====
;
-- 0026：company_wx_app 增加「是否允许平台代发小程序版本」开关。
--
-- 背景：平台超管可以代租户上传代码 / 提审 / 发布小程序版本（见 internal/handler/mp_release.go）。
-- 但企业独立版的小程序归企业自己管——平台不该拥有单方面推送上线的能力，
-- 企业需要一条能自己关掉代发的路。关掉后一切代发动作一律拒绝（service 层校验）。
--
-- 默认 1（允许）：与既有行为一致，不改变任何存量租户的现状。
-- 平台自有小程序（company_id=0）不落本表，天然没有这个开关——平台推自己的版本
-- 不需要谁来授权。
--
-- 为什么不做成 sys_setting 的全局开关：代发的授权粒度在「企业」这一层
-- （关的是「别家别动我的小程序」），不是平台功能总开关。平台真要全局停掉，
-- 不给入口即可（前端不显示），服务端这里的租户级拒绝是最后一道闸。
--
-- SQLite（测试 / devserver）由 GORM AutoMigrate 自动补列；
-- MySQL（生产 / parity）由本迁移补齐。存量库需手动执行。
-- 注意与 0024 不同：ALTER ... ADD COLUMN **不是幂等**的，对已存在该列的库
-- 再执行会报 Duplicate column。正常路径不会重放（migrate 包按 schema_migrations
-- 记账，见 internal/migrate/apply.go），手工执行前请先确认列不存在。
-- （注释里刻意不写出完整的改表语句样例：migrations_guard_test 的正则按语句
--   匹配表名且不剥注释，注释里出现表名样例会把它绊成「对不上任何模型」。）

ALTER TABLE company_wx_app
    ADD COLUMN release_enabled TINYINT(1) NOT NULL DEFAULT 1
        COMMENT '是否允许平台代发小程序版本（0=拒绝一切提审/发布/撤回）';

-- ===== 基线合并：来源 0027_company_wx_app_upload_key.sql =====
;
-- 0027：租户小程序「代码上传密钥」（company_wx_app.upload_key）。
--
-- 背景：版本发布链路的「上传代码」这一步，微信只认「代码上传密钥」
-- （公众平台 → 开发管理 → 开发设置 → 小程序代码上传 里下载的 .key），
-- **不认 AppSecret**。后台要代传代码（见 internal/mpupload），就得存这把钥匙。
--
-- 为什么跟 app_secret 放同一张表、同样只写不读：它和 AppSecret 是同一层东西
-- ——「这个租户的小程序归谁管」的凭据。取值链也是三级回落：
--   租户（本列） → 平台全局（sys_setting.wx_upload_key） → 无
-- 最后一环刻意没有环境变量：PEM 是**多行**文本，而配置文件（runtime.env）
-- 按「一行一项」解析、写入时直接拒绝含换行的值（见 config.WriteFileValues），
-- 所以这把钥匙只能从界面上配。这反而更安全——它不会跟着部署配置被复制到
-- 镜像、CI 变量和备份脚本里去。
--
-- 大小给到 2048：RSA 2048 的 PEM 约 1700 字节，留余量。
--
-- SQLite（测试 / devserver）由 GORM AutoMigrate 自动补列；
-- MySQL（生产 / parity）由本迁移补齐。存量库需手动执行。
--
-- 与 0026 不同：加列**不是幂等**的，对已存在该列的库再执行会报 Duplicate column。
-- 正常路径不会重放（migrate 包按 schema_migrations 记账，见 internal/migrate/apply.go），
-- 手工执行前请先确认列不存在。

ALTER TABLE company_wx_app
    ADD COLUMN upload_key VARCHAR(2048) NOT NULL DEFAULT '' COMMENT '小程序代码上传密钥（private.key 原文，只写不读）';

-- ===== 基线合并：来源 0028_media_check.sql =====
;
-- 0028：图片 / 音频内容安全检测记录表（media_check）。
--
-- 背景：微信的图片检测只有**异步**接口（/wxa/media_check_async），调一次只回
-- 一个 trace_id，真正的判定结果由「消息推送」在几秒到几十秒后推回来。
-- 中间这段时间必须有个地方存住「这张图属于哪次送检、哪个租户、对象键是什么」，
-- 否则回调到达时既对不上图，也没法下架它。本表就是那个地方。
--
-- 四条设计取舍：
--   1) **trace_id 不建唯一索引**。微信在没收到 success 回包时会重推同一条
--      推送（最多 3 次），而更新是幂等的（同值覆盖），不需要靠唯一约束去重；
--      建了唯一索引反而让「送检失败」这类 trace_id 为空的记录互相冲突
--      （第二条就插不进去）。只建普通索引供回调反查。
--   2) **object_key 在送检时就落下来**，不在下架时现算。下架是人工触发的，
--      那时存储驱动 / 公开基址可能已经改过，现场推导有推错的风险——
--      而对象键推错会把别人的图覆盖掉（覆盖是不可逆的）。
--   3) **raw 存微信原文**。申诉时微信要的就是 result / detail 原文，
--      只存归一化后的 suggest 等于把证据丢了。
--   4) **没有「pending 超时自动失败」字段**。微信推送可能晚到很久，
--      提前标失败会让后来到达的真实结论写不进去（终态不回退）。
--      列表页按 created_at 显示「待结论 N 分钟」，由人判断是否异常。
--
-- 状态取值：pending / pass / review / risky / failed / skipped
-- 处置动作：''（未处置）/ takedown（对象已覆盖成占位图）
--
-- SQLite（测试 / devserver）由 GORM AutoMigrate 自动建表；
-- MySQL（生产 / parity）由本迁移补齐。存量库需手动执行；重复执行不会报错。

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

-- ===== 基线合并：来源 0029_tenant_license.sql =====
;
-- 0029：多租户授权表（tenant_license）——按企业配置席位 / 到期时间 / 功能开关。
--
-- 背景：Phase 1~3 的授权是「全局一份」（sys_setting 的 license_*，或私有部署的
-- 签名许可证文件）。平台要按企业售卖或给不同企业不同额度时，全局一份不够用：
-- 企业 A 买 50 席、企业 B 买 10 席，共用一个 seats 就会互相挤占（Phase 3 之前
-- 甚至是全局计数，A 的员工直接顶掉 B 的额度）。
--
-- 取值链（见 internal/license/tenant_db.go 的 ResolveFor）：
--   本表该租户的行 → 全局系统设置（license_*）→ 无（不过期 / 不限席位）
-- 没给某租户单独配时自动落到全局那一份，行为与 Phase 1~3 完全一致——
-- 新增本表不会让存量部署「突然开始拦人」。
--
-- 私有 / 独立部署不走本表：那种形态只有单一租户，授权来自 data/license.key
-- 的 Ed25519 签名文件，启动时验签、过期即拒绝启动。
--
-- features 逗号分隔（空 = 全部开通，缺省放行），与 sys_setting.license_features
-- 同格式，避免两套解析逻辑。
--
-- SQLite（测试 / devserver）由 GORM AutoMigrate 自动建表；
-- MySQL（生产 / parity）由本迁移补齐。存量库需手动执行；重复执行不会报错。

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

-- ===== 基线合并：来源 0030_pii_encryption.sql =====
;
-- 0030 员工 PII 静态加密：姓名 / 手机 / 手机2 / 邮箱 / 微信昵称。
--
-- 做法（见 internal/pii + internal/model 的钩子/回填）：
--   * 敏感字段在库里只存密文（AES-256-GCM，带 v1: 前缀），写回**原列名**，
--     所以先把这些列加宽——base64 密文比原文长，沿用旧宽度会被 MySQL 截断
--     （非严格模式静默截断 = 数据损坏，比报错更危险）。
--   * 可检索字段（姓名/手机/手机2/邮箱/昵称）另加 HMAC 盲索引列：
--     登录按手机号、后台按关键字检索都改为比对盲索引，库里看不到明文。
--
-- 存量数据由服务启动时的 Go 回填（model.BackfillEmployees）加密并补盲索引；
-- 本文件只改结构、不动数据（AES 无法在纯 SQL 里做）。
--
-- 幂等性：MODIFY 可重复执行；ADD COLUMN 重复执行会报「列已存在」，可忽略
-- （schema_migrations 记账后不会重放）。先清 NULL 再 MODIFY NOT NULL，避免
-- 历史可空列升级失败。

UPDATE employees SET name = ''     WHERE name IS NULL;
UPDATE employees SET phone = ''    WHERE phone IS NULL;
UPDATE employees SET phone2 = ''   WHERE phone2 IS NULL;
UPDATE employees SET email = ''    WHERE email IS NULL;
UPDATE employees SET nickname = '' WHERE nickname IS NULL;

ALTER TABLE employees MODIFY COLUMN name     VARCHAR(512) NOT NULL DEFAULT '';
ALTER TABLE employees MODIFY COLUMN phone    VARCHAR(512) NOT NULL DEFAULT '';
ALTER TABLE employees MODIFY COLUMN phone2   VARCHAR(512) NOT NULL DEFAULT '';
ALTER TABLE employees MODIFY COLUMN email    VARCHAR(512) NOT NULL DEFAULT '';
ALTER TABLE employees MODIFY COLUMN nickname VARCHAR(512) NOT NULL DEFAULT '';

ALTER TABLE employees ADD COLUMN name_bidx     VARCHAR(64) NOT NULL DEFAULT '';
ALTER TABLE employees ADD COLUMN phone_bidx    VARCHAR(64) NOT NULL DEFAULT '';
ALTER TABLE employees ADD COLUMN phone2_bidx   VARCHAR(64) NOT NULL DEFAULT '';
ALTER TABLE employees ADD COLUMN email_bidx    VARCHAR(64) NOT NULL DEFAULT '';
ALTER TABLE employees ADD COLUMN nickname_bidx VARCHAR(64) NOT NULL DEFAULT '';

-- ===== 基线合并：来源 0031_pii_encryption_apply.sql =====
;
-- 0031 PII 静态加密（第二阶段）：开通申请单 open_applies + 名片修改快照 card_modify_applies。
--
-- 延续 0030 员工表的方案（见 internal/pii 与 internal/model 的钩子/回填）：
--   * open_applies 的姓名/手机/手机2/邮箱/昵称：加宽列以容纳 base64 密文，另加盲索引；
--   * card_modify_applies.snapshot：整段 JSON 作为一个密文落库——密文是 base64 文本、
--     不是合法 JSON，故必须把列类型从 JSON 改为 LONGTEXT（否则 MySQL 插入直接报错）。
--
-- 存量数据由服务启动时的 model.BackfillPII 加密（Go 侧，AES 无法在纯 SQL 里做）。
-- 幂等性：MODIFY 可重复执行；ADD COLUMN 重复执行报「列已存在」可忽略。
-- 先清 NULL 再 MODIFY NOT NULL，避免历史可空列升级失败。

UPDATE open_applies SET name = ''     WHERE name IS NULL;
UPDATE open_applies SET phone = ''    WHERE phone IS NULL;
UPDATE open_applies SET phone2 = ''   WHERE phone2 IS NULL;
UPDATE open_applies SET email = ''    WHERE email IS NULL;
UPDATE open_applies SET nickname = '' WHERE nickname IS NULL;

ALTER TABLE open_applies MODIFY COLUMN name     VARCHAR(512) NOT NULL DEFAULT '';
ALTER TABLE open_applies MODIFY COLUMN phone    VARCHAR(512) NOT NULL DEFAULT '';
ALTER TABLE open_applies MODIFY COLUMN phone2   VARCHAR(512) NOT NULL DEFAULT '';
ALTER TABLE open_applies MODIFY COLUMN email    VARCHAR(512) NOT NULL DEFAULT '';
ALTER TABLE open_applies MODIFY COLUMN nickname VARCHAR(512) NOT NULL DEFAULT '';

ALTER TABLE open_applies ADD COLUMN name_bidx     VARCHAR(64) NOT NULL DEFAULT '';
ALTER TABLE open_applies ADD COLUMN phone_bidx    VARCHAR(64) NOT NULL DEFAULT '';
ALTER TABLE open_applies ADD COLUMN phone2_bidx   VARCHAR(64) NOT NULL DEFAULT '';
ALTER TABLE open_applies ADD COLUMN email_bidx    VARCHAR(64) NOT NULL DEFAULT '';
ALTER TABLE open_applies ADD COLUMN nickname_bidx VARCHAR(64) NOT NULL DEFAULT '';

-- snapshot：JSON -> LONGTEXT（存密文；原 JSON 明文值在本迁移后仍可读，由 Go 回填补加密）
ALTER TABLE card_modify_applies MODIFY COLUMN snapshot LONGTEXT;

-- ===== 基线合并：来源 0032_storage_usage.sql =====
;
-- 0032：按租户的存储用量累计表（storage_usage）——配额检查与用量展示。
--
-- 背景（B3）：此前上传只受「单文件上限 upload_max_mb」约束，**总用量没有任何上限**。
-- 一个租户可以无限上传：对象存储 / 磁盘被占满、按量计费的成本无法封顶，
-- 也谈不上给客户报价（没有用量就没有计价单位）。更麻烦的是连「谁占了多少」都答不上来。
--
-- 为什么是累计表而不是每次 SUM：
--   对象存储里没有「这个对象属于哪个租户」的元数据；本地驱动的 /uploads 是扁平目录，
--   反向统计无从做起。配额检查发生在每次上传之前，逐次 SUM 是 O(文件数) 的开销。
--   所以上传时增量累加，检查时一次主键查询。
--
-- 口径与代价（务必知悉）：**统计从本功能启用起累计**，历史文件不回溯计入；
-- Bytes 按上传时的文件大小计（剥 EXIF / 压缩后实际落盘可能更小，取偏大口径）。
-- 覆盖写（SaveBytes 那几条固定名）不扣减——这里统计的是「上传量」不是「存量」。
--
-- company_id 作主键：一个租户一行，company_id = 0（平台自身素材库）不计量。
-- quota_mb 是该租户的配额覆盖（0 = 跟随全局设置 storage_quota_mb）。
--
-- SQLite（测试 / devserver）由 GORM AutoMigrate 自动建表；
-- MySQL（生产 / parity）由本迁移补齐。存量库需手动执行；重复执行不会报错。

CREATE TABLE IF NOT EXISTS storage_usage (
    company_id BIGINT   NOT NULL COMMENT '租户（company.id），一个企业一行',
    bytes      BIGINT   NOT NULL DEFAULT 0 COMMENT '累计上传字节数（按上传文件大小计）',
    files      BIGINT   NOT NULL DEFAULT 0 COMMENT '累计上传文件数',
    quota_mb   BIGINT   NOT NULL DEFAULT 0 COMMENT '本租户配额覆盖（MB）；0 = 跟随全局 storage_quota_mb',
    updated_at DATETIME NULL,
    PRIMARY KEY (company_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='按租户累计的存储用量（配额检查用）';

-- ===== 基线合并：来源 0033_company_login_code.sql =====
;
-- 企业登录页白标码（?company=）：用于登录页按企业区分显示不同简称，对客户更友好。
-- 取值为小写字母 / 数字 / 连字符组成的 slug（如 langhui），前端以 ?company=langhui 传入。
--
-- 唯一性由后端在写入时校验（见 handler/companies.go），这里**不建唯一索引**：
-- 未配置白标码的企业该列为空串，多企业空串会互相冲突，故不依赖 DB 唯一约束，
-- 与「管理员用户名唯一」同为应用层校验（MySQL parity job 真跑，避免 SQLite/MySQL 索引不一致）。
-- 仅回传 short_name 作为登录品牌名，绝不泄露企业 ID / 名称 / 管理员等其它字段。
ALTER TABLE company ADD COLUMN login_code VARCHAR(64) NULL COMMENT '登录页白标码（?company=），仅精确匹配';

-- ===== 基线合并：来源 0034_company_mp_entry.sql =====
;
-- 0034：小程序「默认进入」设置（按租户）——裸启动落到名片墙还是某个员工的名片。
--
-- 背景：扫微信「体验版二维码」/ 直接打开小程序是裸启动，微信不带任何参数。
-- 上一轮把落地页从「输入员工编号」弹窗改成了企业名片墙（pages/team/team）；
-- 但有些客户的用法就是「一张名片打天下」——老板的名片就是公司门面，
-- 访客扫进来应该直接看到他，而不是先看一层员工宫格再点。
-- 这个偏好按企业配置，所以落在 company_infos（与公司名称/LOGO 同一行，随企业开通即有）。
--
-- 取值：
--   mp_entry_mode     'wall'（名片墙，缺省）| 'staff'（指定员工名片）
--   mp_entry_staff_id 指定员工的 staff_id；mode='wall' 时归零。
--
-- 为什么缺省 'wall'：存量企业升级后行为**必须**不变——上一轮刚把裸启动改成名片墙，
-- 这里若缺省 'staff'，所有存量租户会突然跳到某个员工（还可能是已停用的那个）。
-- 「升级不得突变存量行为」优先于「新客户默认值更聪明」。
--
-- 为什么不建外键 / 索引：mp_entry_staff_id 的合法性（属于本企业且启用）由
-- handler 在写入时校验，读取时再校验一次并在失效时**回落 wall**——
-- 外键拦不住「员工停用」这种软状态，而回落是唯一不会把访客引到空名片页的做法。
--
-- SQLite（测试 / devserver）由 GORM AutoMigrate 自动补列；MySQL（生产 / parity）由本迁移补齐。
-- 存量库需手动执行；重复执行报 1060（Duplicate column）由迁移器忽略。

ALTER TABLE company_infos ADD COLUMN mp_entry_mode VARCHAR(16) NOT NULL DEFAULT 'wall' COMMENT '小程序裸启动落地页：wall=名片墙 / staff=指定员工名片';
ALTER TABLE company_infos ADD COLUMN mp_entry_staff_id BIGINT NOT NULL DEFAULT 0 COMMENT 'mode=staff 时的员工 staff_id；0 表示未指定（回落名片墙）';

-- ===== 基线合并：来源 0035_company_wx_app_subscribe.sql =====
;
-- 0035：租户小程序「审核结果通知」订阅消息模板（company_wx_app.subscribe_tmpl_id / subscribe_tmpl_fields）。
--
-- 背景：订阅消息（一次性订阅）的模板**与 AppID 绑定**——一个 AppID 的账号下
-- 配的模板，另一个 AppID 用不了，微信会回「模板不存在」。平台模式（一个 AppID
-- 服务所有企业）用全局 sys_setting.wx_tmpl_audit 一份就够了；但企业独立版小程序
-- 每家企业一个 AppID，必须在**自己那个**微信账号里配模板，再落到本列。
--
-- 取值链（见 internal/handler/subscribe.go 的 auditTemplate）：
--   本企业是独立 AppID 且配了本列 → 用本列；
--   本企业是平台模式（无 own AppID）/ 本列留空 → 回落全局 sys_setting.wx_tmpl_audit。
-- 注意：已是独立 AppID 却没配本列时**不回落**（回落会发错模板），视为「该企业不发」。
--
-- 两列语义同 app_secret / upload_key：只写不读，接口层只回「配没配」，永不明文回传。
-- 字段名用逗号分隔、顺序固定为「事项 / 结果 / 备注」，留空即默认 thing1,phrase2,thing3。
--
-- 大小给到 128：模板 ID 约 40 字符、字段串（如 thing6,thing5,thing4）很短，留足余量。
--
-- SQLite（测试 / devserver）由 GORM AutoMigrate 自动补列；
-- MySQL（生产 / parity）由本迁移补齐。存量库需手动执行。
--
-- 与 0027 同：加列**不是幂等**的，对已存在该列的库再执行会报 Duplicate column。
-- 正常路径不会重放（migrate 包按 schema_migrations 记账，见 internal/migrate/apply.go），
-- 手工执行前请先确认列不存在。

ALTER TABLE company_wx_app
    ADD COLUMN subscribe_tmpl_id VARCHAR(128) NOT NULL DEFAULT '' COMMENT '审核结果通知订阅模板 ID（仅企业独立版小程序用，与 AppID 绑定）',
    ADD COLUMN subscribe_tmpl_fields VARCHAR(128) NOT NULL DEFAULT '' COMMENT '订阅模板字段名，逗号分隔，顺序=事项/结果/备注';

-- ===== 基线合并：来源 0036_richtext_mediumtext.sql =====
;
-- 0036：两张富文本列 TEXT → MEDIUMTEXT（正文含图片/视频块后 64KB 不够用）。
--
-- 背景：富文本由 wangEditor 产出（段落、标题、列表、图片、<wx-video> 视频块）。
-- TEXT 上限 64KB——纯文字够用，但一旦正文里放进几张图片：
--   · 图片若走对象存储 URL（本项目的方式），每条 <img> 约 100~200 字节，
--     64KB 仍可容纳 300 张以上，看似够用；
--   · 但编辑器若被配上「base64 直插」或粘贴富文本时带 data: URI，一张
--     100KB 的图就是 ~133KB 的 base64，**一张图即超限**（MySQL 严格模式报
--     1406 Data too long，非严格模式静默截断——正文后半段凭空消失）。
-- 视频块本身很小（<wx-video ...></wx-video> 几十字节），但长图文 + 多视频
-- 的富文本已经现实接近上限。
--
-- MEDIUMTEXT 上限 16MB（16777215 字节，按字节计、与字符集无关），足够 200 张图的正文，
-- 且不必把列放宽到 LONGTEXT（4GB，夸张且不利于行外存储的边界判断）。
--
-- 影响面：纯扩容，不改语义，存量数据无损（MODIFY 只改类型上限）。
-- 注意：MODIFY 会重建表（InnoDB 需拷数据），大表请在低峰执行；
-- 本项目的这两张表是内容表（项目案例 / 业务服务），行数很小，可忽略。
--
-- SQLite（测试 / devserver）由 GORM AutoMigrate 按模型 tag 维护；
-- MySQL（生产 / parity）由本迁移补齐。

ALTER TABLE project_cases MODIFY COLUMN detail MEDIUMTEXT;
ALTER TABLE biz_services MODIFY COLUMN content MEDIUMTEXT;

-- ===== 基线合并：来源 0037_company_profile_video_autoplay.sql =====
;
-- 0037 公司简介视频：是否自动播放，由后台显式控制
--
-- 背景：腾讯视频插件（provider wxa75efa648b60994b）的 <txv-video> 组件
-- **autoplay 默认值是 true**（官方参数表：autoplay boolean，默认「是」），
-- 而微信原生 <video> 的 autoplay 默认是 false。我们此前没显式传这个属性，
-- 于是「腾讯视频档」一进名片页就带着声音自己播起来——用户明确反馈这是负面体验。
--
-- 默认 0（不自动播放）：与原生 video 的默认一致，也是名片页该有的行为
-- （访客只是来看一张名片，不该被突然的声音打断）。
-- **这是一次有意的行为变更**：升级后腾讯视频档不再自动播放；
-- 需要自动播放的部署，请在「管理后台 → 内容 → 公司简介 → 腾讯视频」把开关打开。
--
-- 只加列、不动存量数据：存量行取默认值 0。

ALTER TABLE company_profiles
  ADD COLUMN video_autoplay TINYINT(1) NOT NULL DEFAULT 0 AFTER video_fallback_url;

-- ===== 基线合并：来源 0038_company_profile_video_backup_url.sql =====
;
-- 0038 公司简介视频：自托管 MP4 的兜底（备用）地址
--
-- 背景：video_fallback_url 是「是否启用兜底」的布尔开关，而兜底地址与
-- upload / url 两档的主视频源共用 video_url 一列。于是自托管 MP4 档没法再
-- 填第二个地址——填了也会在保存时被主源的值覆盖（多字段共用一个槽）。
-- 视频号 / 腾讯视频两档因此早就有「兜底 MP4」，唯独自托管 MP4 档没有。
--
-- 本列只服务 upload / url 两档：主 MP4 加载失败时自动切换到这里。
-- channels / tencent 两档继续沿用 video_url 作兜底，存量行为完全不变。
-- 是否启用仍由 video_fallback_url 这一个开关控制，四种档位语义一致。
--
-- 为什么不复用 0001 里的 backup_mp4_url：0016 把它的值搬进 video_url 后
-- 没有清空，老库里那一列残留的是「旧版主视频地址」。重新启用会让客户一打开
-- 开关就播一段早该下线的旧视频，属于静默的存量突变，比加一列危险得多。
--
-- 只加列、默认空串、不搬迁任何存量数据：升级后开关默认关，行为零变化。

ALTER TABLE company_profiles
  ADD COLUMN video_backup_url VARCHAR(512) NOT NULL DEFAULT '' AFTER video_url;

-- ===== 基线合并：来源 0039_admin_wx_openid.sql =====
;
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

-- ===== 基线合并：来源 0040_detail_time_display.sql =====
;
-- 0040：小程序详情页「更新于」展示档位（home_layouts.project_time_mode / service_time_mode）。
--
-- 背景：项目案例与业务服务详情页原先硬编码显示 content 行的 updated_at（精确到秒）。
-- 这个值是「运营最后一次保存的时间」，不是「案例交付时间」——批量整理内容会让全站
-- 案例在同一天集体显示「更新于」，秒级精度对访客也只是噪音，且后台无处可关。
-- 现在把档位交给租户自选：hidden（不显示）/ date（只到日）/ minute（到分）。
--
-- 为什么两列而不是一列：案例带「交付年份」语义、服务更像常青内容，两者该不该显示
-- 时间往往不是一个答案，共用一个开关会逼客户二选一。
--
-- 默认值选 date 而不是 hidden：升级后存量租户的时间显示**不消失**，只是不再暴露
-- 秒级，属于「变干净」而不是「东西不见了」；要彻底隐藏可在后台一键关掉。
--
-- 升级零突变：只加列、默认值固定、不搬迁任何存量数据。
-- SQLite（测试 / devserver）由 GORM AutoMigrate 按模型 tag 补齐；MySQL（生产 / parity）
-- 由本迁移补齐。重复执行不会报错（Duplicate column 由 migrate.applyFile 忽略）。

ALTER TABLE home_layouts
  ADD COLUMN project_time_mode VARCHAR(16) NOT NULL DEFAULT 'date' COMMENT '项目案例详情页时间显示：hidden/date/minute',
  ADD COLUMN service_time_mode VARCHAR(16) NOT NULL DEFAULT 'date' COMMENT '业务服务详情页时间显示：hidden/date/minute';

-- ===== 基线合并：来源 0041_miniprogram_theme.sql =====
;
-- 0041：小程序配色模板（home_layouts.template_id）。
--
-- 背景：此前小程序的全站主色全部硬编码在 app.wxss 等 12 份 WXSS 里，客户想换
-- 一套品牌配色只能改代码重新提审。现在把「选哪一套内置配色」交给租户在后台自选，
-- 作为按授权开关的增值能力。
--
-- 为什么存 id 而不存色值 / CSS：小程序代码是编译后上传审核的，端上不能动态加载
-- WXSS；让后台下发自由样式既绕不过审核，也会把内部类名变成对客户的长期承诺。
-- 模板包随版本内置（backend/internal/mptemplate + miniprogram/utils/theme.ts），
-- 这里只记「选了哪一款」。
--
-- 默认值选 default（升级前的经典蓝）：存量租户的页面逐字不变，属于「多了可选项」
-- 而不是「换了套皮」。
--
-- 升级零突变：只加列、默认值固定、不搬迁任何存量数据。
-- SQLite（测试 / devserver）由 GORM AutoMigrate 按模型 tag 补齐；MySQL（生产 / parity）
-- 由本迁移补齐。重复执行不会报错（Duplicate column 由 migrate.applyFile 忽略）。

ALTER TABLE home_layouts
  ADD COLUMN template_id VARCHAR(32) NOT NULL DEFAULT 'default' COMMENT '小程序配色模板 id：default/ink/warm';

-- ===== 基线合并：来源 0042_mp_theme_two_axis.sql =====
;
-- 0042：小程序风格模板两轴化（style_id + brand_color_id）。
--
-- 背景：0041 把「选哪一套内置配色」存成单列 template_id（12 款平铺）。现在要把
-- 主题系统升级成「风格 × 色系」两轴（见 docs/theming-two-axis-design.md）：
-- style_id 决定各模块布局族（default/ink/warm/teal，default 即经典蓝基线），
-- brand_color_id 是叠加在风格之上的纯颜色层（NULL = 用该风格默认色）。
--
-- 存量映射（用户确认的零突变方案）：default/ink/warm/teal → (同名, NULL)；
-- 8 款 brand-* 全部归 default 风格下作色系，即 (default, brand-*)。
-- template_id 列保留作兼容派生列（写入时由两轴反推），不删除，便于回滚锚点。
--
-- 升级零突变：新增两列 + 按上述映射回填；不搬迁业务数据、不改任何展示。
-- SQLite（测试 / devserver）由 GORM AutoMigrate 按模型 tag 补齐新列（无默认值，
-- 存量行 style_id 为空时由后端回退从 template_id 反推）；MySQL（生产 / parity）
-- 由本迁移补齐并回填。重复执行不会报错（Duplicate column 由 migrate.applyFile 忽略）。
-- 回填用 UPDATE（非 ALTER 默认值），保证 brand-* 的 style_id 落到 default。

ALTER TABLE home_layouts
  ADD COLUMN style_id VARCHAR(32) NOT NULL DEFAULT 'default' COMMENT '整体风格（布局族）：default/ink/warm/teal';

ALTER TABLE home_layouts
  ADD COLUMN brand_color_id VARCHAR(32) DEFAULT NULL COMMENT '品牌色系（颜色层），NULL=用风格默认色；P1 仅 default 下挂 brand-*';

-- 存量回填：风格款各归其风格，品牌款统一归 default。
UPDATE home_layouts SET style_id = 'ink',     brand_color_id = NULL WHERE template_id = 'ink';
UPDATE home_layouts SET style_id = 'warm',    brand_color_id = NULL WHERE template_id = 'warm';
UPDATE home_layouts SET style_id = 'teal',    brand_color_id = NULL WHERE template_id = 'teal';
UPDATE home_layouts SET style_id = 'default', brand_color_id = template_id WHERE template_id LIKE 'brand-%';
-- template_id 为 default / 空 / 其它未知值的行保持 style_id='default'、brand_color_id=NULL。

-- ===== 基线合并：来源 0043_drop_template_id.sql =====
;
-- 0043：删除 home_layouts.template_id 兼容派生列。
--
-- 背景：0041 引入单列 template_id（12 款平铺内置配色）；0042 升级为两轴
-- (style_id + brand_color_id) 后，template_id 仅作「旧端兼容近似值」派生列，
-- 由两轴反推得来，且不可逆（12 款平铺装不下 4 风格 × 9 色档的 36 组合）。
--
-- 收敛时机：P5 已落地 36 组合全叉乘，前端/小程序端均已直接读写两轴
-- （小程序端保留 splitTheme 做旧缓存零迁移，不再回写 template_id）。
-- 兼容列完成历史使命，删除以收敛存储真值，避免「两轴 vs 单列」双写歧义。
--
-- 存储真值唯一化：删除后 style_id / brand_color_id 为唯一真值，
-- 公开接口与渲染逻辑不再需要「两轴反推 template_id」再「template_id 反推两轴」的折返。
--
-- 升级零突变：只删列、不搬迁任何业务数据。存量行在 0042 已回填两轴，
-- 删列对其展示无影响；SQLite（测试 / devserver）由 GORM AutoMigrate 管表结构，
-- 本迁移仅在 MySQL（生产 / parity）执行。
--
-- 方言钉子（2026-11-13 线上事故订正）：MySQL 8.0 **不支持**
-- ALTER TABLE ... DROP COLUMN IF EXISTS —— 那是 MariaDB 方言，MySQL 会在
-- 解析阶段直接报 1064 near 'IF EXISTS'，整份迁移失败且不会记账。
-- 因此这里必须用裸 DROP，幂等由两层保证：
--   1. schema_migrations 记账，已应用过的文件不会重放；
--   2. migrate.apply.go 的 ignoredErrs 忽略 1091「Can't DROP ... check that
--      column/key exists」，覆盖「列已被手工删除但版本未记账」的应急场景。
-- 别改回 IF EXISTS，也别用 information_schema + PREPARE 动态 SQL 绕：
-- applyFile 逐句 db.Exec，PREPARE 是会话级的，连接池换连接后 EXECUTE 会失效。

ALTER TABLE home_layouts DROP COLUMN template_id;

-- ===== 基线合并：来源 0044_tenant_storage_configs.sql =====
;
-- 0044：租户自有存储配置表（tenant_storage_configs）——按企业登记自己的对象桶。
--
-- 背景：目前所有上传都落在**平台**那一套存储上（sys_setting 的 storage_driver
-- 等全局单值）。客户要求「文件存进我自己公司的桶」时，全局单值装不下第二个租户——
-- 和 0024 的小程序凭据是同一类问题，只是这次带上「数据主权」性质：
-- 客户不是想要一个开关，而是想要「我的文件不落在你机器上」。
--
-- 为什么不放 sys_setting：sys_setting 是全局单值表（key/value），没有租户维度。
-- 硬塞成 storage_cid5_endpoint 这类键会让配置项注册表、掩码下发、重启标记
-- 全部失效，而且行数随租户线性膨胀。独立表是唯一合理落点。
--
-- 与本表的读写边界（非常重要）：
--   - 只有平台超管可写（设计稿 §1.1）。企业管理员侧不暴露写接口、菜单隐藏、
--     连读接口都不给。企业管理员能改自己的桶地址 = 能把全企业的图指到任意主机，
--     而且出参 URL 与对象键都跟着变，事后无从追溯是谁改的。
--   - access_key_enc / secret_key_enc 是**密文**列，写入必须经 pii.Encrypt
--     （与 0024 的 app_secret 明文策略不同：那次的理由是「只回掩码」，
--     而本表的凭据要参与真实 S3 请求签名，漏一次明文就在日志/报错里带出去了）。
--     模型上两列 json:"-"，永远不参与序列化。
--   - 第一阶段 public_base_url 留空 = 走平台统一入口（设计稿 §3.2）：客户自有
--     域名上线还牵扯备案与证书，不是这一轮的事。列先建好，省得下一轮再动表。
--
-- host_header 为什么必须在表里，而不是「从 endpoint 推」：
--   virtual-hosted-style 的桶名在 Host 里，且这个 Host 同时是 TLS SNI 的取值。
--   第五十五轮 R1 用真桶实测过：只给 proxy_ssl_verify + 证书 + ssl_name 而漏掉
--   SNI，请求被桶以 421 拒绝。而「桶名该拼在 endpoint 前面还是后面」逐厂商不同
--   （MinIO 走 path-style，OSS/S3 走 virtual-hosted-style），推导等于猜。
--   所以给一列让超管**照控制台抄**，比写一段「智能推断」可靠。
--
-- 只建表，不写任何业务数据 / 演示配置（零数据是商用版的默认状态，
-- 种子数据归 internal/demoseed）。
--
-- SQLite（测试 / devserver）由 GORM AutoMigrate 自动建表；MySQL（生产 / parity）
-- 由本迁移补齐。重复执行不会报错：建表语句自带 IF NOT EXISTS，MySQL 8.0 认这个。
-- （注释里别把「建表 + IF NOT EXISTS」连成一个短语写出来——守门测试
--   TestMigrationsTableNamesMatchModels 直接扫原文不剥注释，会把紧跟其后的
--   中文当成表名，报出一张叫 IF 的幽灵表。）
--
-- 方言钉子：MySQL 8.0 不支持 MariaDB 那套 ADD/DROP COLUMN IF EXISTS 写法
-- （解析阶段直接 1064，整份迁移失败且不记账，见 0043 的线上事故）。
-- 本文件不涉及任何 ALTER，别在下一次改表时忘掉这条。

CREATE TABLE IF NOT EXISTS tenant_storage_configs (
    id               BIGINT       NOT NULL AUTO_INCREMENT,
    company_id       BIGINT       NOT NULL COMMENT '租户（company.id）；0 不作为合法值，平台自身走全局配置',
    driver           VARCHAR(16)  NOT NULL DEFAULT 'local' COMMENT 'local / minio / oss / s3，与 config.StorageDriver 枚举一致',
    endpoint         VARCHAR(255) NOT NULL DEFAULT '' COMMENT 'host[:port]，不带 scheme（带 scheme 会被 nginx 拼成 http://https://）',
    bucket           VARCHAR(128) NOT NULL DEFAULT '' COMMENT '桶名',
    region           VARCHAR(64)  NOT NULL DEFAULT '' COMMENT '区域（OSS/S3 需要，MinIO 可空）',
    use_ssl          TINYINT(1)   NOT NULL DEFAULT 0 COMMENT '是否 https。为 1 时 snippet 必须同时给 proxy_ssl_server_name + ssl_name，否则真桶 421',
    host_header      VARCHAR(255) NOT NULL DEFAULT '' COMMENT '发给桶的 Host（virtual-hosted-style 的桶名在这里，也是 TLS SNI 取值）；空则退化成 endpoint',
    access_key_enc   VARCHAR(512) NOT NULL DEFAULT '' COMMENT 'AccessKey 密文（pii.Encrypt 产出，带 v1: 前缀）；模型 json:"-"',
    secret_key_enc   VARCHAR(512) NOT NULL DEFAULT '' COMMENT 'SecretKey 密文（pii.Encrypt 产出，带 v1: 前缀）；模型 json:"-"',
    key_id           VARCHAR(32)  NOT NULL DEFAULT '' COMMENT '加密密钥版本标识，为将来换钥匙留口子（换钥时据此判断哪些行需重加密）',
    public_base_url  VARCHAR(255) NOT NULL DEFAULT '' COMMENT '客户自有访问域名（含 scheme）；第一阶段留空 = 走平台统一入口',
    public_read      TINYINT(1)   NOT NULL DEFAULT 0 COMMENT '桶是否公开读；第一阶段试点要求为 1（私有桶要靠预签名，出参契约会变）',
    enabled          TINYINT(1)   NOT NULL DEFAULT 0 COMMENT '开关。关掉即回到平台存储；注意 driver=local 时同样不生成 snippet（见 Target.NeedsSnippet）',
    status           VARCHAR(16)  NOT NULL DEFAULT 'unknown' COMMENT '连通性自检结果：unknown / ok / error',
    last_checked_at  DATETIME     NULL COMMENT '最近一次自检时间',
    last_error       VARCHAR(512) NOT NULL DEFAULT '' COMMENT '最近一次自检的脱敏错误摘要（不含凭据、不含完整响应体）',
    created_at       DATETIME     NULL,
    updated_at       DATETIME     NULL,
    PRIMARY KEY (id),
    UNIQUE KEY uk_tenant_storage_configs_company (company_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='租户自有存储配置（按企业登记对象桶，只有平台超管可写）';

-- ===== 基线合并：来源 0045_storage_backfill_refs.sql =====
;
-- 0045：存量回填台账（storage_backfill_refs）
--
-- 为什么台账必须在业务库里、而不是一个 JSON 文件：P3 把「复制对象」和「改库」
-- 分成两步，前者在对象存储上、不可回滚；后者在 MySQL 上、可以回滚。回滚的依据
-- 就是这张表里每一行的 old_value。若台账写在文件里，「库改成功了、台账写失败」
-- 这个窗口会让那批行永远回不去，而且没人知道缺了哪几条。放进同一个库后，
-- 改业务行与记台账可以放在一个事务里：要么都成，要么都不成。
--
-- 一行 = 一条被改写过的资源引用（数组列按元素计）。plan_hash + ref_id 唯一：
-- 同一份计划重跑时命中旧行即跳过，这是幂等的判据；不同计划改同一行时，
-- 预检会发现库里的当前值已经不是那份计划的 raw，直接停下而不是覆盖。
--
-- 对象复制状态刻意不入库：目标对象是否存在、大小是否一致，用 Stat 就能判定，
-- 那是更可靠的真相（复制成功但记账失败仍会留下一个完整对象）。复制只增不删，
-- 回滚也**不删对象**（R5：解约 / 换桶后旧对象留着做兜底）。
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

-- ===== 基线合并：来源 0046_home_gradient_strength.sql =====
;
-- 0046：首页顶部品牌主色浓度（home_layouts.bg_gradient_strength）。
--
-- 背景：小程序首页无背景图时，顶部渐变用品牌主色原色 var(--wc-primary) 还原
-- 客户设计稿（宝蓝 #0820d0 / 正红 #d40c0a）。但不同客户对「多浓」的偏好不同，
-- 有的嫌原色太扎眼、想要更柔和的过渡。现在把浓度交给租户自选：0~100 滑杆，
-- 注入渐变顶部的 alpha。
--
-- 默认值选 100 而不是较低值：升级后存量租户的顶部观感**零突变**，仍是原色；
-- 想柔和的管理员自己往下滑即可。这与 0040 默认 date「不消失」是同一原则。
--
-- 与 bg_mask 同属「名片页面样式」页签，和风格两轴（0042）分开写、互不冲掉。
-- 仅作用于无背景图时——设了 bg_image 时整幅渐变被背景层盖住，浓度自然无效。
--
-- 升级零突变：只加列、默认值固定、不搬迁任何存量数据。
-- SQLite（测试 / devserver）由 GORM AutoMigrate 按模型 tag 补齐；MySQL（生产 / parity）
-- 由本迁移补齐。重复执行不会报错（Duplicate column 由 migrate.applyFile 忽略）。

ALTER TABLE home_layouts
  ADD COLUMN bg_gradient_strength INT NOT NULL DEFAULT 100 COMMENT '首页顶部品牌主色浓度 0~100，100=原色';

-- ===== 基线合并：来源 0047_company_profile_intro_font_scale.sql =====
;
-- 0047：公司简介「简介文字」字号档位（company_profiles.intro_font_scale）。
--
-- 背景：小程序首页简介文字此前没有独立字号，继承 page 的 28rpx。客户反馈
-- 不同企业对这个字号诉求不同：有的希望保持现在的「大」，有的嫌太长想小一点。
-- 现在把字号交给租户自选三档：large 28rpx（现状）/ medium 26rpx / small 24rpx。
--
-- 默认值选 large 而不是任何更小档：升级后存量租户的简介观感**零突变**，
-- 仍是客户已经看惯的 28rpx；想变小的管理员自己在后台切即可。这与 0040
-- 默认 date、0046 默认 100 的原则一致——后加的展示偏好一律不动存量。
--
-- 只作用于「简介文字」这一个 <text class="intro">，不改 page 全局字号，
-- 也不动公司名 / 联系表 / 章节标题等其它文字。
--
-- 升级零突变：只加列、默认值固定、不搬迁任何存量数据。
-- SQLite（测试 / devserver）由 GORM AutoMigrate 按模型 tag 补齐；MySQL（生产 / parity）
-- 由本迁移补齐。重复执行不会报错（Duplicate column 由 migrate.applyFile 忽略）。

ALTER TABLE company_profiles
  ADD COLUMN intro_font_scale VARCHAR(16) NOT NULL DEFAULT 'large' COMMENT '简介文字字号 large=28rpx/medium=26rpx/small=24rpx';

-- ===== 基线合并：来源 0048_company_name_scale.sql =====
;
-- 0048：首页视频下方「公司名称」展示档位（company_infos.company_name_scale）。
--
-- 背景：客户反馈首页版式要能随要求灵活调整——视频下方的公司名称与简介文字，
-- 既想各自调大中小，也想直接关掉某一块。简介文字的三档已于 0047 落地；
-- 本迁移补上公司名称的同一套四档：large / medium / small / hidden。
--
-- hidden 的语义是**关闭**：小程序端用 wx:if 移除整个节点（含 ink 双栏下的空槽），
-- 不是 display:none，所以关掉后那一行连高度都不占，真正腾出屏幕空间。
--
-- 默认值选 large：升级后存量租户的公司名照旧显示、字号不变，零视觉突变；
-- 要调小或关闭的管理员自己在后台切即可。与 0040 默认 date、0046 默认 100、
-- 0047 默认 large 同一原则——后加的展示偏好一律不动存量。
--
-- 只作用于首页视频下方的公司名一行（.company-name 与其分隔线），
-- 不影响小程序顶栏的企业简称，也不影响后台侧边栏品牌名。
--
-- 升级零突变：只加列、默认值固定、不搬迁任何存量数据。
-- SQLite（测试 / devserver）由 GORM AutoMigrate 按模型 tag 补齐；MySQL（生产 / parity）
-- 由本迁移补齐。重复执行不会报错（Duplicate column 由 migrate.applyFile 忽略）。

ALTER TABLE company_infos
  ADD COLUMN company_name_scale VARCHAR(16) NOT NULL DEFAULT 'large' COMMENT '首页公司名展示档位 large/medium/small/hidden';

-- ===== 基线合并：来源 0049_home_display_limits.sql =====
;
-- 0049：首页业绩分类数量 / 精选案例数量与循环效果（home_layouts）。
--
-- 客户反馈：首页「经典业绩」板块的分类图标墙太长，想限制最多显示几个；
-- 精选案例默认铺几张也要能配，并要求能做成滚动 / 翻转这类有质感的循环呈现。
-- 三项都是纯展示偏好，和 0046/0047/0048 一样归「名片页面样式」页签管理。
--
-- category_limit 0~20，0 = 隐藏分类网格（精选案例照旧显示，板块标题不消失）；
-- featured_count 0~3，0=不显示精选案例（与 category_limit 的 0 同语义），
-- 实际精选案例不足时以实际数量为准，不补空位；
-- featured_effect 四档：static 静态平铺 / fade 淡入淡出 / scroll 横向滚动 / flip 3D 翻转。
-- 小程序端按效果 id 选择随包内置的动画样式——端上不能动态加载 WXSS，
-- 所以后端只下发 id 白名单内的值，新增效果需同时改模型归一与小程序样式。
--
-- 默认值取各配置上限（20 / 3 / static）：分类数量与轮播效果对存量租户零变化；
-- 精选因为范围只有 0~3 无法表达「不限」，缺省 3 会让原本精选超过 3 张的租户
-- 收敛到排序最前的 3 张——案例本身没有被删除，管理员可改「首页精选」标记。
-- 这与 0040/0046/0047/0048「后加的展示偏好默认不动存量」原则的差别仅在于
-- 本配置上限本身就是 3，不存在能表示「全部」的合法值。
--
-- 升级零突变：只加列、默认值固定、不搬迁任何存量数据。
-- SQLite（测试 / devserver）由 GORM AutoMigrate 按模型 tag 补齐；MySQL（生产 / parity）
-- 由本迁移补齐。重复执行不会报错（Duplicate column 由 migrate.applyFile 忽略）。

ALTER TABLE home_layouts
  ADD COLUMN category_limit INT NOT NULL DEFAULT 20 COMMENT '首页业绩分类最多显示数量 0~20，0=隐藏分类网格',
  ADD COLUMN featured_count INT NOT NULL DEFAULT 3 COMMENT '首页精选案例默认呈现数量 0~3，0=不显示，不足按实际',
  ADD COLUMN featured_effect VARCHAR(16) NOT NULL DEFAULT 'static' COMMENT '精选案例循环效果 static/fade/scroll/flip';

-- ===== 基线合并：来源 0050_home_show_names.sql =====
;
-- 0050：首页业绩分类 / 业务服务「封面图下方是否再显示名称」（home_layouts）。
--
-- 客户现状：设计稿把名称直接画进了封面图——业绩分类是「居住住宅」这类带字方图，
-- 业务服务是竖版卡片，图上已有标题。端上再渲染一行文字就是重复，所以默认关闭。
--
-- 需求：客户若把封面换成**不带字**的素图，要能打开开关让端上补回名称，
-- 显示在图片下方（样式复用既有的 .cat-name / .svc-title，四风格已有差异化字号）。
--
-- 拆成两列而不是一个总开关：业绩分类与业务服务常常不是同一批素材风格，
-- 典型情况是「分类换成素图、服务卡仍是带字卡」，共用一列会逼客户二选一。
--
-- 默认 false = 与加配置前完全一致（图上已有名称，不再重复渲染），升级零突变。
-- 只加列、不改存量数据；SQLite（测试 / devserver）由 GORM AutoMigrate 按模型 tag 补齐。
-- 重复执行不会报错（Duplicate column 由 migrate.applyFile 忽略）。

ALTER TABLE home_layouts
  ADD COLUMN show_category_name TINYINT(1) NOT NULL DEFAULT 0 COMMENT '业绩分类封面图下方是否显示名称，默认0=图内已有名称不重复',
  ADD COLUMN show_service_name TINYINT(1) NOT NULL DEFAULT 0 COMMENT '业务服务封面图下方是否显示名称，默认0=图内已有名称不重复';

-- ===== 基线合并：来源 0051_pii_blind_index.sql =====
;
-- 0051 PII 盲索引补数据库索引。
--
-- 背景：0030 / 0031 只给 employees / open_applies 加了 name_bidx、phone_bidx、
-- phone2_bidx、email_bidx、nickname_bidx 五列，但**没有加数据库索引**。
-- 盲索引列存的是 HMAC 定长值，登录与判重全靠等值命中：
--   * /api/admin/login：WHERE phone_bidx = ?（全表）
--   * phoneInUse：WHERE company_id = ? AND phone_bidx = ?（employees）
--   * phoneInUse：WHERE company_id = ? AND phone_bidx = ? AND status = ?（open_applies）
--   * 后台按关键字搜申请/员工：name_bidx = ? OR phone_bidx = ? OR ...
-- 没有索引时这些查询退化为全表扫描；数据量上来后，登录与开通申请会越来越慢。
--
-- 列顺序刻意选 (bidx, company_id)，不是 (company_id, bidx)：
--   * 登录没有 company_id 条件，只有 bidx 打头才能命中最左前缀；
--   * 带 company_id 的判重同样能用（两列都在索引里，与 WHERE 书写顺序无关）；
--   * 一条索引同时服务两种查询，避免为同一列建两条重复索引。
-- status 不进索引：phone_bidx 已把候选收敛到极少行，再过滤 status 不值得多占写成本。
--
-- 幂等：MySQL 8.0 不支持 ADD INDEX IF NOT EXISTS，重复执行报 1061
-- （Duplicate key name），由 migrate.ignoredErrs 忽略；版本记账后正常不会重放。
-- SQLite（测试 / devserver）走 GORM AutoMigrate，不执行本文件，无需兼容其方言。

ALTER TABLE employees
  ADD INDEX idx_emp_name_bidx (name_bidx, company_id);

ALTER TABLE employees
  ADD INDEX idx_emp_phone_bidx (phone_bidx, company_id);

ALTER TABLE employees
  ADD INDEX idx_emp_phone2_bidx (phone2_bidx, company_id);

ALTER TABLE employees
  ADD INDEX idx_emp_email_bidx (email_bidx, company_id);

ALTER TABLE employees
  ADD INDEX idx_emp_nickname_bidx (nickname_bidx, company_id);

ALTER TABLE open_applies
  ADD INDEX idx_open_apply_name_bidx (name_bidx, company_id);

ALTER TABLE open_applies
  ADD INDEX idx_open_apply_phone_bidx (phone_bidx, company_id);

ALTER TABLE open_applies
  ADD INDEX idx_open_apply_phone2_bidx (phone2_bidx, company_id);

ALTER TABLE open_applies
  ADD INDEX idx_open_apply_email_bidx (email_bidx, company_id);

ALTER TABLE open_applies
  ADD INDEX idx_open_apply_nickname_bidx (nickname_bidx, company_id);

-- ===== 基线合并：来源 0052_video_channel_play_mode.sql =====
;
-- 0052 视频号播放方式：内嵌播放 / 跳转打开。
--
-- 背景：此前视频号档只有 <channel-video> 内嵌播放一条路，跨主体要靠 feed-token，
-- 且受基础库与非个人主体限制。wx.openChannelsActivity 走跳转打开，无主体要求，
-- 适合只是想跳到视频号视频页的客户。两者是并列能力，需由后台显式选择，不能自动切换。
--
-- 默认 embed 保证升级前后视觉零变化，历史行读到空串时由后端归一层兜底。
-- 本文件只在 MySQL 下由 migrate 应用，SQLite（测试 / devserver）走 GORM AutoMigrate。

ALTER TABLE company_profiles
  ADD COLUMN video_channel_play_mode VARCHAR(16) NOT NULL DEFAULT 'embed' COMMENT '视频号播放方式 embed=内嵌/redirect=跳转打开';

-- ===== 基线合并：来源 0053_home_logo_size_align.sql =====
;
-- 0053：名片卡右上角企业 LOGO 的显示档位（home_layouts.logo_size / logo_align）。
--
-- 背景：LOGO 原来是写死的 150×44rpx + mode="aspectFit"。aspectFit 会把图片按比例
-- 缩到框内并**居中**，于是一张正方形 LOGO 只占 44rpx 宽、左右各留约 53rpx 空白——
-- 客户看到的是「浮在右上角偏左」的一块，小尺寸图尤其明显，而当时没有任何可调项。
--
-- 两列都只存档位字符串，不做像素计算：具体 rpx 尺寸与对齐方式在
-- miniprogram/app.wxss 与 employee-card 组件里（小程序不能动态加载 WXSS，
-- 与 featured_effect 同一套路）。
--
-- 默认值选 medium / center = 加配置前的表现（150×44 + aspectFit 居中），
-- 所以升级后存量租户看到的画面一字不变。要更小/更大、更靠左/靠右由客户自己切。
-- 与 0040 默认 date、0046 默认 100、0047 默认 large 同一原则：后加的展示偏好
-- 一律不动存量。
--
-- 升级零突变：只加列、默认值固定、不搬迁任何存量数据。
-- SQLite（测试 / devserver）由 GORM AutoMigrate 按模型 tag 补齐；MySQL（生产 / parity）
-- 由本迁移补齐。重复执行不会报错（Duplicate column 由 migrate.applyFile 忽略）。

ALTER TABLE home_layouts
  ADD COLUMN logo_size VARCHAR(16) NOT NULL DEFAULT 'medium' COMMENT '名片卡 LOGO 尺寸档 small/medium/large';

ALTER TABLE home_layouts
  ADD COLUMN logo_align VARCHAR(16) NOT NULL DEFAULT 'center' COMMENT '名片卡 LOGO 水平对齐 left/center/right';

-- ===== 基线合并：来源 0054_storage_profiles.sql =====
;
-- 0054：文件存储与分发档案化（storage_profiles / delivery_profiles / storage_bindings）
-- 以及一键迁移任务台账（storage_migration_jobs / storage_migration_errors）。
--
-- 为什么独立成表：sys_setting 是全局 key/value 单值，所有存储 / CDN 字段都标了
-- 「改完要重启」（见 setting registry）；tenant_storage_configs 又是一租户一整组
-- 配置，两套配置之间没有任何共享。客户要「多套配置反复用、默认一套、企业各选各的」时，
-- 这两处都装不下：前者没有多值，后者会把同一份 endpoint / AK / SK 复制 N 份。
--
-- 三层职责（勿合并）：
--   - storage_profiles   只存连接信息与凭据，不关心谁在用；
--   - delivery_profiles  只存 CDN 域名 / 回源标识，独立于存储（同桶多域名、同域名多桶）；
--   - storage_bindings   只存「谁用哪套」。tenant_id=0 表示全局默认；
--                        企业没有绑定行时继承全局，不复制任何配置。
--
-- 即时生效靠 bindings.version + profiles.version 参与 uploadroute 的缓存版本，
-- 不再依赖启动期 config.OverlayFromSettings 的快照（那是旧行为，保留一个版本只读兼容）。
--
-- 凭证明文入库（用户 2026-11-20 决策：暂不做加密存储），回显只给掩码。
-- 因此数据库备份与库文件访问控制必须受控；将来切 PII 加密需专门迁移（本项目无 re-key）。
--
-- 方言：SQLite 由 GORM AutoMigrate 建表；MySQL 8 由本迁移执行，不用 IF EXISTS 的 ALTER。

CREATE TABLE IF NOT EXISTS storage_profiles (
    id              BIGINT       NOT NULL AUTO_INCREMENT,
    name            VARCHAR(64)  NOT NULL COMMENT '档案名，展示用，唯一',
    driver          VARCHAR(16)  NOT NULL DEFAULT 'local' COMMENT 'local / minio / oss / s3',
    endpoint        VARCHAR(255) NOT NULL DEFAULT '' COMMENT 'host[:port]，不带 scheme',
    bucket          VARCHAR(128) NOT NULL DEFAULT '',
    region          VARCHAR(64)  NOT NULL DEFAULT '',
    base_url        VARCHAR(255) NOT NULL DEFAULT '' COMMENT '上传返回基址，恒为平台统一入口',
    path_style      TINYINT(1)   NOT NULL DEFAULT 0 COMMENT 'S3 是否 path-style',
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
    UNIQUE KEY uk_storage_profiles_name (name)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='存储档案：连接信息与凭据，供多套配置复用';

CREATE TABLE IF NOT EXISTS delivery_profiles (
    id              BIGINT       NOT NULL AUTO_INCREMENT,
    name            VARCHAR(64)  NOT NULL COMMENT '分发档案名，唯一',
    provider        VARCHAR(24)  NOT NULL DEFAULT 'none' COMMENT 'none / aliyun / tencent / qiniu / cloudflare / cloudfront / custom',
    domain          VARCHAR(255) NOT NULL DEFAULT '' COMMENT '公开访问域名（含 scheme），只影响出参 URL',
    path_prefix     VARCHAR(128) NOT NULL DEFAULT '' COMMENT '域名下的目录前缀，拼 URL 时插入',
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
    UNIQUE KEY uk_delivery_profiles_name (name)
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
    UNIQUE KEY uk_storage_bindings_tenant (tenant_id)
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

-- ===== 基线合并：来源 0055_drop_tenant_storage_configs.sql =====
;
-- 删除 tenant_storage_configs（迁移 0044 建立的「按企业一行自有桶」）。
--
-- 第七十五轮：存储配置只剩「档案 + 绑定」一套
-- （storage_profiles / delivery_profiles / storage_bindings，迁移 0054）。
-- 该表已无任何代码读写：上传路由、出参 origin、配额判定、nginx snippet 生成
-- 全部改由档案解析，兼容层与 TENANT_STORAGE_ROUTE 开关一并删除。
--
-- 产品尚未正式商用，故不做数据搬迁：旧行里的 endpoint / bucket / 凭据密文
-- 需要在新模型里重新登记（档案界面）。保留本文件而不是删掉 0044，
-- 是为了让「已应用过 0044 的库」与「全新安装的库」走到同一个终态。
DROP TABLE IF EXISTS tenant_storage_configs;

-- ===== 基线合并：来源 0056_local_mirror.sql =====
;
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

-- ===== 基线合并：来源 0057_binding_previous_profile.sql =====
;
-- 0057：绑定表记住「切换前的档案」，支撑一键回滚。
--
-- 背景：切换绑定（手动 PUT 或迁移后 autoSwitch）直接覆盖 storage_profile_id，
-- 旧档案身份只散落在审计 detail 与迁移任务的 source_profile_id 里，没有一处
-- 结构化留存。切过去发现漏搬 / 目标桶对象被误删时，管理员只能翻审计手工改回，
-- 既慢又容易改错（尤其 delivery 档案还要一并还原）。
--
-- 本迁移给绑定表补两列，记录「本次切换前的那一套」：
--   - 前进切换（saveBinding / switchBindingAfterMigration）把当前档案写进 previous；
--   - 一键回滚把 previous 换回当前、同时把被换下的写进 previous（交换语义，
--     于是回滚一次后还能再回滚一次，来回切不会丢历史）；
--   - previous=0 表示「绑定第一次建立之前没有档案」，回滚接口据此拒绝，
--     退路用已有的 DELETE binding（回落全局 / 平台），不引入第二套语义。
--
-- 只加列、不改语义、不插数据：existing 行的 previous 保持 0，即「历史绑定不可回滚」，
-- 这比凭空猜一个上一套档案安全。
--
-- 方言：SQLite（测试 / devserver）由 GORM AutoMigrate 按模型 tag 补齐；
-- MySQL（生产 / parity）由本迁移补齐，MySQL 8 裸 DDL 不用 IF EXISTS 的 ALTER。

ALTER TABLE storage_bindings
  ADD COLUMN previous_storage_profile_id BIGINT NOT NULL DEFAULT 0 COMMENT '切换前的存储档案ID，0=此绑定从未切换过',
  ADD COLUMN previous_delivery_profile_id BIGINT NOT NULL DEFAULT 0 COMMENT '切换前的分发档案ID，0=切换前未启用分发';

-- ===== 基线合并：来源 0058_drop_storage_path_style.sql =====
;
-- 0058：删掉 storage_profiles.path_style，这一列从未被运行时消费。
--
-- 背景：S3 路径风格（桶名进 path 还是进 Host）由 nginxsnippet.Target.PathStyle()
-- 单点判定，唯一输入是 host_header：留空或等于 endpoint 走路径风格，以
-- <bucket>. 开头走虚拟主机风格。上传侧与回填侧都走 storage.S3BucketLookup，
-- 这条链路上没有任何地方读 storage_profiles.path_style。
--
-- 于是它成了僵尸字段：库里能存、接口能读写，但改它不会改变任何行为。
-- 管理员若照着旧说明填它，只会得到「填了没用」的错觉。前端表单早已不展示这一列，
-- 本次把它从模型、接口和契约里一并清掉，避免以后有人误以为它是开关。
--
-- 方言：SQLite（测试 / devserver）由 GORM AutoMigrate 按模型处理，不主动删列，
-- 但模型不再读写它，留列无害。MySQL（生产 / parity）由本迁移删列。
-- 重复执行由 schema_migrations 记账，另有 ignoredErrs 的 1091 兜底。

ALTER TABLE storage_profiles
  DROP COLUMN path_style;

-- ===== 基线合并：来源 0059_platform_primary_storage.sql =====
;
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
--     填过的值不能凭空丢失），之后不再被读取；原计划「档案存在后物理清理这些行」
--     并未实现，它们只是留在库里成为死数据——不要恢复为第二真值源；
--   - upload_dir / mirror_dir 这类部署期目录事实、local_mirror_enabled 总闸、
--     配额与上传限制继续留在 sys_setting，它们不是「连接信息」。
--
-- 内置档案行由启动时 ensure 创建（内容取决于部署环境与历史 sys_setting 值，
-- 迁移脚本无法知道），这里只更新用途注释。SQLite（测试 / devserver）按模型
-- AutoMigrate，无此方言文件。

ALTER TABLE storage_profiles
  MODIFY COLUMN purpose VARCHAR(24) NOT NULL DEFAULT '' COMMENT '内置用途：platform_primary / tenant_fallback / platform_mirror；空=普通档案';

-- ===== 基线合并：来源 0060_platform_delivery_profile.sql =====
;
-- 0060：平台自身的 CDN 刷新凭据收进分发档案。
--
-- 背景：0059 把平台存储的连接信息收进了 storage_profiles 的 platform_primary，
-- 但 CDN 刷新那组 cdn_provider / cdn_access_key_id / cdn_access_key_secret /
-- cdn_resource_id / cdn_endpoint / cdn_enabled 仍留在 sys_setting。它们与
-- delivery_profiles 的 provider / access_key / secret_key / resource_id / endpoint
-- 是字段级重复：同一家 CDN 的同一份凭据要填两遍，改了一处另一处不动，而且
-- 两边都不会报错——覆盖了图、边缘仍是旧的。
--
-- 本迁移给 delivery_profiles 补 builtin / purpose，与 storage_profiles 同口径：
--   - 内置行由启动时 ensure 创建（凭据取决于部署环境与历史 sys_setting，迁移
--     脚本无法知道），purpose=platform_primary，不可删、不可绑定给企业；
--   - 平台对象的刷新器启动期由该档案构造，改完需重启；
--   - 平台分发档案的 domain 不参与出参：平台域名唯一真值源是 storage_profiles
--     里 platform_primary 的 base_url，避免同一个域名存两处。
--
-- 方言：SQLite（测试 / devserver）由 GORM AutoMigrate 按模型 tag 补齐；
-- MySQL（生产 / parity）由本迁移补齐。

ALTER TABLE delivery_profiles
  ADD COLUMN builtin TINYINT(1) NOT NULL DEFAULT 0 COMMENT '是否内置档案（启动时 ensure，不可删除、不可绑定给企业）';

ALTER TABLE delivery_profiles
  ADD COLUMN purpose VARCHAR(24) NOT NULL DEFAULT '' COMMENT '内置用途：platform_primary；空=普通档案';

-- ===== 基线合并：来源 0061_drop_delivery_path_prefix.sql =====
;
-- 0061：删掉 delivery_profiles.path_prefix，这一列从未真正改变出参。
--
-- 背景：0054 建表时给它写的用途是「域名下的目录前缀，拼 URL 时插入」。
-- 全仓唯一读取它的地方是 Resolver.PublicURL，而 PublicURL 在生产代码里没有任何调用方；
-- 真正拼企业出参的 ResolveRef / OriginFor 只看 delivery_profiles.domain，
-- 且域名校验明确拒绝带路径。于是管理员填了 pathPrefix 只会得到「填了没用」的错觉。
--
-- 与 0058 删 storage_profiles.path_style 同一口径：模型、写接口、出参、表单、契约
-- 一并移除，避免库里留一个谁都不消费的开关。
--
-- 方言：SQLite（测试 / devserver）由 GORM AutoMigrate 按模型处理，不主动删列，
-- 但模型不再读写它，留列无害。MySQL（生产 / parity）由本迁移删列。
-- 重复执行由 schema_migrations 记账，另有 ignoredErrs 的 1091 兜底。

ALTER TABLE delivery_profiles
  DROP COLUMN path_prefix;

-- ===== 基线合并：来源 0062_builtin_storage_names.sql =====
;
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

