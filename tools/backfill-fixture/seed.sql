-- 存量回填（P3）的取证数据：把「历史形态资源引用」在一次性库里造全，覆盖
-- refmigrate 需要区分出的全部七类归类 + 一条串号 + 一条坏图集。
--
-- ⚠️ 只能灌进一次性演示库。文件头部先清空九张表（无 WHERE），
--    在生产库上跑等于删库。用法：
--
--   docker exec -i wc-mysql-ci mysql -uroot -proot weichuan_backfill_demo \
--     < tools/backfill-fixture/seed.sql
--
-- 为什么要有这份 fixture 而不是随手写几条 INSERT：
--   下一轮的执行器要用**同一批数据**验证「复制对象 → 改库 → 台账 → rollback」，
--   如果两次用的种子不一样，上一次跑出来的数字就解释不了这一次的行为。
--   这里的每一条都在 plan.go 的 Inventory / Class 注释里有对应理由。
--
-- 基址约定：--base-url https://card.example.com/uploads
--   （绝对 URL 那条要靠它剥前缀；留空也能跑，会退到「/uploads/ 段匹配」）

SET NAMES utf8mb4;

DELETE FROM media_check;
DELETE FROM home_layouts;
DELETE FROM biz_services;
DELETE FROM project_cases;
DELETE FROM achievement_categories;
DELETE FROM company_profiles;
DELETE FROM company_infos;
DELETE FROM open_applies;
DELETE FROM employees;

-- ── 员工头像：一个字段装下十种形态 ──────────────────────────────────────
-- 第 11 条是空串：**故意留着**，用来证明「空值不进盘点」（SQL 层就排除了）。
-- 第 12 条 company_id=0：没有租户归属，算不出目标键，必须报 invalid 而不是猜。
-- 第 13 条与 company_infos.logo、biz_services.icon 指向同一个源对象（去重用）。
INSERT INTO employees (staff_id, company_id, name, avatar) VALUES
  (1,  5, '历史相对路径',   '/uploads/avatar-a.jpg'),
  (2,  5, '历史绝对 URL',   'https://card.example.com/uploads/avatar-b.jpg'),
  (3,  5, '新形态租户键',   't/5/avatar-c.jpg'),
  (4,  5, '平台对象',       'p/library/cat-1.png'),
  (5,  5, '外域直链',       'https://cdn.other.com/face.jpg'),
  (6,  5, '伪协议',         'javascript:alert(1)'),
  (7,  5, '路径穿越',       '../etc/passwd'),
  (8,  5, '别人的租户键',   't/9/other-tenant.jpg'),
  (9,  5, '同源推不出键',   'https://card.example.com/elsewhere/x.jpg'),
  (10, 5, '历史裸键',       'avatar-plain.jpg'),
  (11, 5, '空头像',         ''),
  (12, 0, '无租户归属',     '/uploads/orphan.jpg'),
  (13, 7, '被三处共用',     '/uploads/shared-logo.png');

-- ── 开通申请头像：访客未登录上传落 p/，这是**既有合法形态**，永不搬 ──────
INSERT INTO open_applies (id, company_id, name, avatar, status) VALUES
  (1, 5, '访客未登录上传', 'p/apply-visitor.png', 0);

-- ── 企业 LOGO ────────────────────────────────────────────────────────────
INSERT INTO company_infos (id, company_id, full_name, logo) VALUES
  (1, 5, '甲企业', 'logo.png'),
  (2, 7, '乙企业', '/uploads/shared-logo.png');

-- ── 公司简介：video_source=url 那一档就是任意外域直链；cover 带缓存戳 ────
INSERT INTO company_profiles (id, company_id, video_source, video_url, video_backup_url, cover) VALUES
  (1, 5, 'url', 'https://cdn.other.com/video.mp4', '/uploads/video-b.mp4',
      'https://card.example.com/uploads/cover.jpg?t=1699999999');

-- ── 业绩分类图标：裸键 ──────────────────────────────────────────────────
INSERT INTO achievement_categories (id, company_id, name, icon) VALUES
  (1, 5, '资质', 'icon-1.png');

-- ── 案例：图集是 JSON 列，解不出 []string 必须报出来而不是当空 ───────────
-- 第 2 行的 album 是 **合法 JSON 但结构不对**的数组（元素是数字）：
--   注意不能写成 'not-json'——MySQL 上 album 的列类型是 json，语法坏的值
--   在 INSERT 阶段就会被拒（ERROR 3140），生产库里**不可能出现**。
--   能出现的坏形态只有「JSON 合法、解不成 []string」这一种。
--   若把它当空处理，这条数据会从盘点里彻底消失：搬到一半时没人知道它还
--   引用着对象，最终表现是裂图且无人报错。
-- 第 3 行的首槽是空串：空槽没有对象可搬，扫描时跳过。
INSERT INTO project_cases (id, company_id, name, cover, album) VALUES
  (1, 5, '正常图集', '/uploads/case1-cover.jpg',
      '["/uploads/case1-a.jpg","t/5/case1-b.jpg","p/library/case1-c.png"]'),
  (2, 5, '坏图集',   'https://cdn.other.com/c.jpg', '["/uploads/ok.jpg", 123]'),
  (3, 5, '空槽',     '', '["","/uploads/case3.jpg"]');

-- ── 服务图标：与员工 13、乙企业 LOGO 同一张图（第三个引用） ──────────────
INSERT INTO biz_services (id, company_id, title, icon) VALUES
  (1, 7, '共用同一张图', '/uploads/shared-logo.png');

-- ── 首页背景：uk_home_layout_company 让一家只能有一行，所以两家各一行 ────
INSERT INTO home_layouts (id, company_id, bg_image) VALUES
  (1, 5, 't/5/bg.jpg'),
  (2, 7, 'p/home/bg.png');

-- ── 送检记录：url 与 object_key 指向同一个对象，两个列都要搬 ─────────────
-- 只搬 url 不搬 object_key 的后果：下架时按 object_key 覆盖写占位图，
-- 会写到旧位置，新位置上仍然是那张违规图——下架看起来成功，实际没生效。
INSERT INTO media_check (id, company_id, url, object_key, media_type, status) VALUES
  (1, 5, 'https://card.example.com/uploads/mc-shot.png', 'mc-shot.png', 1, 0);
