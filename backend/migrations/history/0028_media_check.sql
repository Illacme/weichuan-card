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
