// 存储模块的产品化事实表：面向用户，而不是面向代码。
//
// 这里集中维护四件事，页面只负责呈现，不再各自散落一段术语解释：
//   1. 整体架构的三种角色与配置流程（StorageOverview.vue）
//   2. 每个菜单 / 页签是干什么的（MENU_GUIDES / TAB_GUIDES）
//   3. 每个配置项的功能、值来源、规则限制、容易踩的坑（FIELD_GUIDES，喂给 FieldHelp）
//   4. 所有第三方平台的官方控制台 / 密钥管理 / 文档地址（*_GUIDES）
//
// 新增云厂商或改凭据形态时：后端 storage 包加枚举 + 本文件加一份 guide + 表单自动出现帮助。
// 链接只放厂商官方域名；已逐个核对可达性（Cloudflare dash 对脚本返回 403，浏览器正常）。

export interface GuideLink {
  label: string
  url: string
}

export interface FieldGuide {
  /** 表单字段名或表格列名 */
  field: string
  /** 用户看到的名字，和界面保持一致 */
  label: string
  /** 这项是干什么的：先用日常话讲，再补技术名 */
  usage: string
  /** 值从哪里来：去哪个后台、点哪几步 */
  source?: string
  /** 什么情况下才能生效、有什么填法限制 */
  rules?: string
  /** 容易踩的坑 / 出问题先看哪里 */
  note?: string
}

export interface PlatformGuide {
  key: string
  label: string
  /** 一句话说明这家是什么、适合谁 */
  summary: string
  /** 什么时候该选它 */
  when: string
  links: GuideLink[]
  /** 这家需要的凭据怎么拿、填到哪个框 */
  credential: string
}

// ── 整体架构：用三句话讲清"文件到底在哪、谁能看到" ──────────────────────────

export const ARCHITECTURE_POINTS = [
  {
    title: '文件先落在「存储」里',
    body: '图片、视频、小程序码都叫「文件对象」。系统默认把文件放在这台服务器自己的磁盘上——开箱即用，不依赖任何外部账号。接入云对象存储后，新文件就写进云端的「桶」里，容量和并发能力随之扩展。',
  },
  {
    title: '「分发」只负责让访问更快',
    body: 'CDN 是可选项：它把文件缓存在离访客更近的节点。不开 CDN 时访客直接从存储读取，功能完全正常；开了 CDN 后，后台替换同名文件时会自动通知 CDN 更新，也可以手动刷新。',
  },
  {
    title: '「绑定」决定哪家企业用哪套',
    body: '一套「文件存哪、怎么连」的设置，叫做一条「文件存储」。不特别指定时，所有企业共用同一条（全局默认）；也可以给某家企业单独指定自己的桶。改完绑定下一个请求就生效，不需要重启。',
  },
] as const

export const DEFAULT_FACTS = [
  '初始状态：本地磁盘 + 不启用 CDN，不填任何云账号也能完整使用。',
  '系统自带三条文件存储与一条 CDN 分发，负责平台自己的文件和备份，不能删除。',
  '接云不会丢老文件：先用「迁移任务」把旧文件复制到新桶，再切换绑定；源桶原样保留。',
] as const

export interface SetupStep {
  no: number
  title: string
  when: string
  body: string
  tab: string
  action: string
}

export const SETUP_STEPS: SetupStep[] = [
  {
    no: 1,
    title: '先看清楚现在存哪',
    when: '第一次接手后台时',
    body: '页面顶部会直接告诉你当前文件和 CDN 的状态。只需要确认一件事：现在是不是本地磁盘。是的话就可以放心继续，不需要做任何改动。',
    tab: 'overview',
    action: '就在此页',
  },
  {
    no: 2,
    title: '需要更大容量时，新建文件存储',
    when: '本地磁盘不够、想用云盘时',
    body: '先在云平台创建存储桶并拿到密钥，再回到「文件存储」新建一条。填完点「自检」——通过才说明系统真的能读写这个桶。此时还不会影响任何现有文件。',
    tab: 'storage',
    action: '去新建文件存储',
  },
  {
    no: 3,
    title: '（可选）让访客访问更快',
    when: '访客分布广、图片加载慢时',
    body: '在云厂商开通 CDN 并指向你的桶，然后到「CDN 分发」新建一条，填好厂商凭据和公开域名。这一步只影响访客侧 URL，不改变文件存在哪。',
    tab: 'delivery',
    action: '去新建 CDN 分发',
  },
  {
    no: 4,
    title: '把老文件搬到新桶',
    when: '切换之前，且新桶是空的',
    body: '在「迁移任务」里选源存储和目标存储，先「试运行」看看数量对不对，再启动搬运。只复制不删除，失败的对象会列出清单。',
    tab: 'migration',
    action: '去创建迁移任务',
  },
  {
    no: 5,
    title: '切换生效',
    when: '迁移零失败之后',
    body: '到「绑定关系」选择新文件存储并保存，下一个请求就走新桶。切错了可以一键回滚。修改平台自己的存储或 CDN 凭据同样保存即生效，无需重启。',
    tab: 'binding',
    action: '去切换绑定',
  },
]

export const STORAGE_GLOSSARY: { term: string; meaning: string }[] = [
  { term: '文件对象', meaning: '一张图片、一段视频或一个小程序码文件。系统里叫「对象」，就是一份文件。' },
  { term: '文件存储', meaning: '一套「文件存哪、怎么连进去」的配置：用本地磁盘还是某个云桶、桶名、访问地址和密钥。可以建多套。' },
  { term: 'CDN 分发', meaning: '一套「让访客更快读到文件」的配置：用哪家 CDN、公开域名、刷新凭据。和文件存储互相独立，同一个桶可以配多套。' },
  { term: '绑定', meaning: '决定「哪家企业用哪套文件存储和 CDN 分发」。不单独指定时，所有人用全局默认那套。' },
  { term: '迁移任务', meaning: '把老桶里的文件复制到新桶，只复制不删除，用于切换前搬家。' },
  // 三条内置存储就是三个角色，名字本身就是职责，不再另起一套说法：
  // 主存储 / 应急存储 / 备份存储（2026-11-23 由「平台自身存储 / 平台本地存储 /
  // 平台本地镜像」两轮收敛而来）。
  { term: '主存储', meaning: '平台自己那份文件的落点：p/ 平台对象（素材采用 / 小程序码）和没单独指定存储的企业都写这里。' },
  { term: '应急存储', meaning: '主存储（云端）连不上时，上传还能落到的本地目录——保的是「还能用」，不是备份。' },
  { term: '备份存储', meaning: '上传成功后自动留的本地副本，只写不读；误删、误覆盖时用来找回。' },
  { term: '自检', meaning: '拿当前填的凭据真实访问一次桶或 CDN，确认能连通。保存配置前建议先点它。' },
]

// ── 侧栏菜单 / 页签说明：补上"这个入口是干什么的" ────────────────────────────

export const MENU_GUIDES: Record<string, { desc: string; detail: string }> = {
  '/platform/storage-profiles': {
    desc: '图片视频存哪、怎么加速访问',
    detail: '总览一句话说清现在存哪、CDN 开没开；要接云时点「展开帮助」看五步流程与各家官方地址。文件存储、CDN 分发、缓存刷新、迁移、绑定都在这个模块里。',
  },
  '/platform/backup': {
    desc: '检查数据库和文件备份是否齐全',
    detail: '备份包含数据库、上传文件和密钥三部分，缺一件就恢复不了。这里看是否齐全、多新鲜，并可临时补一份。',
  },
  '/platform/settings': {
    desc: '品牌名、上传大小、有效期等运行参数',
    detail: '存储连接信息和 CDN 凭据已收进「存储与分发」，这里只留目录、配额和有效期等少量启动参数。',
  },
}

export const TAB_GUIDES: Record<string, { title: string; desc: string }> = {
  overview: { title: '总览', desc: '一句话说清文件现在存哪、CDN 开没开。要接云时再展开帮助看流程、官方地址与名词解释。' },
  storage: { title: '文件存储', desc: '管「文件存哪」。自带三条：主存储（写和读都走它）、应急存储（云端连不上时顶上）、备份存储（自动留副本）；你自己新建的才用来绑定企业或迁移。' },
  delivery: { title: 'CDN 分发', desc: '管「访客怎么更快读到」。CDN 与存储互相独立，同一个桶可以配多套加速。' },
  refresh: { title: 'CDN 缓存刷新', desc: '手动让边缘缓存立刻失效。日常覆盖同名文件会自动刷新，这里只用于直接在桶里换过文件或要立刻清缓存。私有桶对象取图走临时签名、不经过 CDN，贴它的地址进来会提示无需刷新。' },
  binding: { title: '绑定关系', desc: '管「谁用哪套」。不单独指定就用全局默认；改完下一个请求立即生效。' },
  migration: { title: '迁移任务', desc: '管「老文件怎么搬到新桶」。只复制不删除，零失败后才建议切换。' },
  // 标题必须与页签名一致：页签叫「平台自身配置」，说明却叫「主存储」，
  // 会让人以为这是同一页的两套东西。另外访问基址在「文件存储」里改，不在这里，
  // 写进这句描述会把管理员指到错误的页面。
  platform: { title: '平台自身配置', desc: '服务器目录与上传限制这些启动期参数改完要重启；备份存储开关即时生效。连接信息（驱动 / 桶 / 凭据 / 访问基址）在「文件存储」改、CDN 凭据在「CDN 分发」改，这两处保存即生效，无需重启。' },
}

// ── 第三方平台：控制台 / 密钥 / 文档，全部给到可点开的官方地址 ────────────────

export const DRIVER_GUIDES: Record<string, PlatformGuide> = {
  local: {
    key: 'local',
    label: '本地磁盘',
    summary: '文件直接存在这台服务器的硬盘上。',
    when: '默认选项。刚安装、磁盘够用、只想先跑起来时用它。',
    links: [],
    credential: '不需要任何账号或密钥。目录由「主存储」里的服务器上传目录决定。',
  },
  minio: {
    key: 'minio',
    label: 'MinIO',
    summary: '自建的对象存储，装在机房或自己的服务器上。',
    when: '希望数据留在内网、又想要对象存储的扩展能力时。',
    links: [
      { label: 'MinIO 官网', url: 'https://min.io/' },
      { label: '安装与部署文档', url: 'https://min.io/docs/minio/linux/index.html' },
    ],
    credential: 'Root 用户名 / 密码在部署 MinIO 时自己设定（MINIO_ROOT_USER / MINIO_ROOT_PASSWORD），不是去官网申请。填在 AccessKey / SecretKey。',
  },
  oss: {
    key: 'oss',
    label: '阿里云 OSS',
    summary: '阿里云的对象存储，国内访问稳定、生态成熟。',
    when: '业务主要在国内、希望容量和带宽随用随扩时。',
    links: [
      { label: '对象存储 OSS 控制台', url: 'https://oss.console.aliyun.com/' },
      { label: '创建 AccessKey（RAM）', url: 'https://ram.console.aliyun.com/manage/ak' },
      { label: 'OSS 产品文档', url: 'https://help.aliyun.com/zh/oss/' },
      { label: '地域与 Endpoint 对照', url: 'https://help.aliyun.com/zh/oss/user-guide/regions-and-endpoints' },
    ],
    credential: '在 RAM 控制台创建 AccessKey ID / Secret，填到 AccessKey / SecretKey；Endpoint 从「地域与 Endpoint 对照」复制，Bucket 填控制台里创建的桶名。',
  },
  s3: {
    key: 's3',
    label: 'S3 兼容存储',
    summary: '一套协议，覆盖腾讯云 COS、AWS S3、Cloudflare R2、七牛 Kodo、华为云 OBS 等。',
    when: '已经在某家云上开了桶、或想避免被单一厂商锁定时。先在下方选具体的服务商照着准备。',
    links: [
      { label: '腾讯云 COS 控制台', url: 'https://console.cloud.tencent.com/cos' },
      { label: '腾讯云 API 密钥', url: 'https://console.cloud.tencent.com/cam/capi' },
      { label: 'COS 的 S3 兼容配置指南', url: 'https://cloud.tencent.com/document/product/436/34688' },
      { label: 'AWS S3 控制台', url: 'https://s3.console.aws.amazon.com/s3/home' },
      { label: 'AWS 安全凭证', url: 'https://console.aws.amazon.com/iam/home' },
      { label: 'AWS S3 文档', url: 'https://docs.aws.amazon.com/AmazonS3/latest/userguide/' },
      { label: 'Cloudflare R2 控制台', url: 'https://dash.cloudflare.com/' },
      { label: 'Cloudflare API Token', url: 'https://dash.cloudflare.com/profile/api-tokens' },
      { label: 'R2 的 S3 API 文档', url: 'https://developers.cloudflare.com/r2/api/s3/api/' },
      { label: '七牛云 Kodo 控制台', url: 'https://portal.qiniu.com/kodo/bucket' },
      { label: '七牛云密钥管理', url: 'https://portal.qiniu.com/user/key' },
      { label: '七牛云开发者文档', url: 'https://developer.qiniu.com/' },
      { label: '华为云 OBS 控制台', url: 'https://console.huaweicloud.com/obs/' },
      { label: '华为云访问密钥', url: 'https://console.huaweicloud.com/iam/' },
      { label: '华为云 OBS 文档', url: 'https://support.huaweicloud.com/obs/index.html' },
    ],
    credential: '从这里选一家打开控制台创建桶并生成密钥，再回到本页填写。Endpoint 填该厂商控制台展示的「访问域名 / S3 端点」，不带 https://；密钥填 AccessKey / SecretKey。各家的具体入口见上面的链接。',
  },
}

// S3 兼容厂商各自的端点写法（示例值以控制台显示为准）
export const S3_VARIANTS: { label: string; endpoint: string; note: string }[] = [
  { label: '腾讯云 COS', endpoint: 'cos.ap-guangzhou.myqcloud.com', note: '地域不同前缀不同，控制台「桶列表 → 访问域名」可查' },
  { label: 'AWS S3', endpoint: 's3.ap-northeast-1.amazonaws.com', note: '换成本桶所在地域；Host Header 留空走路径风格' },
  { label: 'Cloudflare R2', endpoint: 's3.r2.cloudflarestorage.com', note: '部分账号需要带账号 ID，按控制台给出的 S3 API 地址填' },
  { label: '七牛云 Kodo', endpoint: 's3.cn-east-1.qiniucs.com', note: '按桶所在存储区域替换 cn-east-1' },
  { label: '华为云 OBS', endpoint: 'obs.cn-north-4.myhuaweicloud.com', note: '按桶所在区域替换 cn-north-4' },
]

export const CDN_GUIDES: Record<string, PlatformGuide> = {
  none: {
    key: 'none',
    label: '不启用 CDN',
    summary: '访客直接从存储读取文件。',
    when: '默认选项。站点访问量不大、或还在本地磁盘阶段时用它，不需要任何云账号。',
    links: [],
    credential: '不需要填任何东西。选择「不启用 CDN」即表示不做刷新。',
  },
  aliyun: {
    key: 'aliyun',
    label: '阿里云 CDN',
    summary: '阿里云的内容分发网络，适合国内访客。',
    when: '桶已在阿里云 OSS、访客主要在国内时。',
    links: [
      { label: '阿里云 CDN 控制台', url: 'https://cdn.console.aliyun.com/' },
      { label: '创建 AccessKey（RAM）', url: 'https://ram.console.aliyun.com/manage/ak' },
      { label: 'CDN 产品文档', url: 'https://help.aliyun.com/zh/cdn/' },
      { label: 'CDN 快速入门', url: 'https://help.aliyun.com/zh/cdn/getting-started/' },
    ],
    credential: '在 RAM 控制台创建 AccessKey，填到 AccessKey / SecretKey。资源标识不用填，系统按文件 URL 的域名自动定位加速域名。',
  },
  tencent: {
    key: 'tencent',
    label: '腾讯云 CDN',
    summary: '腾讯云的内容分发网络。',
    when: '桶在腾讯云 COS、或访客以国内为主时。',
    links: [
      { label: '腾讯云 CDN 控制台', url: 'https://console.cloud.tencent.com/cdn' },
      { label: '腾讯云 API 密钥', url: 'https://console.cloud.tencent.com/cam/capi' },
      { label: 'CDN 产品文档', url: 'https://cloud.tencent.com/document/product/228' },
    ],
    credential: '在访问管理 → API 密钥管理拿 SecretId / SecretKey，分别填到 AccessKey / SecretKey。资源标识不用填。',
  },
  qiniu: {
    key: 'qiniu',
    label: '七牛云 CDN',
    summary: '七牛云的融合 CDN，和七牛对象存储搭配最省事。',
    when: '桶在七牛 Kodo、或需要国内外混合加速时。',
    links: [
      { label: '七牛云 CDN 控制台', url: 'https://portal.qiniu.com/cdn' },
      { label: '七牛云密钥管理', url: 'https://portal.qiniu.com/user/key' },
      { label: '七牛云开发者文档', url: 'https://developer.qiniu.com/fusion' },
    ],
    credential: '在密钥管理页面复制 AccessKey / SecretKey，填到 AccessKey / SecretKey。资源标识不用填。',
  },
  cloudflare: {
    key: 'cloudflare',
    label: 'Cloudflare',
    summary: '全球边缘网络，免费套餐就支持按 URL 刷新缓存。',
    when: '访客分布在全球、或桶在 Cloudflare R2 时。',
    links: [
      { label: 'Cloudflare 控制台', url: 'https://dash.cloudflare.com/' },
      { label: '创建 API Token', url: 'https://dash.cloudflare.com/profile/api-tokens' },
      { label: '刷新缓存文档', url: 'https://developers.cloudflare.com/cache/how-to/purge-cache/' },
      { label: '按单个 URL 刷新', url: 'https://developers.cloudflare.com/cache/how-to/purge-cache/purge-by-single-file/' },
    ],
    credential: 'Token 在「我的个人资料 → API Tokens」创建，权限需包含 Cache Purge，填到 SecretKey（本厂商没有 AccessKey）。资源标识填 Zone ID，在控制台站点的「概览 → API」区域可见。',
  },
  cloudfront: {
    key: 'cloudfront',
    label: 'AWS CloudFront',
    summary: '亚马逊的全球 CDN，与 S3 原生集成。',
    when: '桶在 AWS S3、访客分布在全球时。',
    links: [
      { label: 'CloudFront 控制台', url: 'https://console.aws.amazon.com/cloudfront/v4/home' },
      { label: 'AWS 安全凭证', url: 'https://console.aws.amazon.com/iam/home' },
      { label: 'CloudFront 开发文档', url: 'https://docs.aws.amazon.com/AmazonCloudFront/latest/DeveloperGuide/' },
      { label: '可信签名者设置', url: 'https://docs.aws.amazon.com/AmazonCloudFront/latest/DeveloperGuide/private-content-trusted-signers.html' },
    ],
    credential: '在 IAM 创建一对访问密钥，填到 AccessKey / SecretKey；资源标识填 Distribution ID。使用前还需在 CloudFront 里把这对密钥的公钥加入「可信密钥组」，步骤见上面的签名者设置文档。',
  },
}

export function driverGuide(driver: string): PlatformGuide | null {
  return DRIVER_GUIDES[(driver || '').trim()] || null
}

export function cdnGuide(provider: string): PlatformGuide | null {
  return CDN_GUIDES[(provider || '').trim()] || null
}

// ── 表单字段说明：字段 → FieldHelp 的四段结构 ─────────────────────────────────

export const STORAGE_FIELD_GUIDES: Record<string, FieldGuide> = {
  name: {
    field: 'name', label: '名称',
    usage: '给这套存储配置起个自己看得懂的名字，方便在绑定和迁移时认出来。',
    rules: '只是显示名，不参与上传或读取，可以随便改。',
  },
  driver: {
    field: 'driver', label: '存储方式',
    usage: '决定文件写进哪里：本地磁盘，或某种云对象存储。',
    source: '不确定选哪个时，先看「总览」里的默认说明：本地磁盘开箱即用，云平台需要自己开通。',
    rules: '系统自带的文件存储里，只有「主存储」能改这一项；另外两套锁定本地磁盘。',
    note: '保存即生效，无需重启；已有文件不会自动搬过去，换存储前先用「迁移任务」搬运。',
  },
  endpoint: {
    field: 'endpoint', label: '服务地址（Endpoint）',
    usage: '云厂商提供的 API 入口地址，系统靠它找到你的桶。',
    source: '在对应云厂商控制台的桶详情里复制「访问域名 / S3 端点」。本弹窗上方有直达链接。',
    rules: '只填主机名，不要带 https:// 和路径，例如 oss-cn-hangzhou.aliyuncs.com。',
    note: '填错会自检失败或全站图片打不开；点「自检」可以立刻验证。',
  },
  bucket: {
    field: 'bucket', label: '存储桶名（Bucket）',
    usage: '桶就是云端放文件的文件夹，所有图片视频都写进它。',
    source: '在云厂商控制台先创建一个桶，把桶名抄过来。',
    rules: '桶名通常是全局唯一的，创建后一般不能改名。',
    note: '公开读桶可直接访问；私有桶也能用，访客取图时由后端临时签名。两种都要填对访问基址。',
  },
  region: {
    field: 'region', label: '地域（Region）',
    usage: '桶所在的地理区域，部分厂商用它来定位服务。',
    source: '创建桶时选择的地域代码，例如 cn-hangzhou。',
    rules: 'S3 兼容存储可以留空，系统会尝试自动识别。',
  },
  baseUrl: {
    field: 'baseUrl', label: '访问基址',
    usage: '上传成功后返回给前端的文件 URL 前缀，访客就是用它打开图片。',
    source: '填你的 CDN 加速域名，或云厂商给的桶公开访问域名，例如 https://cdn.example.com/uploads。',
    rules: '必须是完整的 https:// 开头地址。',
    note: '这里填错，文件其实传成功了，但页面显示的是打不开的链接。',
  },
  hostHeader: {
    field: 'hostHeader', label: 'Host Header',
    usage: '高级项：控制访问桶时用「路径风格」还是「虚拟主机风格」。',
    source: '云厂商的 S3 兼容文档会写明用哪种，通常留空即可。',
    rules: '留空 = 路径风格；填 <bucket>.域名 = 虚拟主机风格。',
    note: '选错会导致签名不一致、全部请求失败。不确定就留空并点自检。',
  },
  useSsl: {
    field: 'useSsl', label: '使用 HTTPS',
    usage: '系统连接云厂商 API 时是否走加密通道。',
    rules: '默认开启；阿里云 OSS 强制使用 HTTPS。',
  },
  publicRead: {
    field: 'publicRead', label: '公开读桶',
    usage: '决定访客取图时是直接从桶读取，还是由后端签发短时访问地址。',
    source: '要与云厂商控制台的桶权限保持一致；平台自身固定公开读。',
    rules: '可关闭：对象仍是本站 /uploads/ 地址，后端现签后跳转；仅绑定到企业的对象存储支持。',
    note: '切换后须重新生成并 reload nginx 回源配置。私有桶不走客户 CDN，没开备份存储时没有副本。',
  },
  accessKey: {
    field: 'accessKey', label: 'AccessKey',
    usage: '访问云厂商 API 的账号标识，相当于「用户名」。',
    source: '本弹窗上方的「创建 AccessKey / 密钥管理」链接直达对应控制台页面。',
    rules: '编辑已有配置时留空表示不修改；新建时必填。',
    note: '保存后只回显掩码，忘记只能重新生成一对密钥。',
  },
  secretKey: {
    field: 'secretKey', label: 'SecretKey',
    usage: '与 AccessKey 配对的密钥，相当于「密码」。',
    source: '与 AccessKey 在同一页面一次性显示，本弹窗上方有直达链接。',
    rules: '编辑已有配置时留空表示不修改；新建时必填。',
    note: '以明文入库、回显只给掩码。请保管好数据库备份，并不要贴到聊天工具里。',
  },
  localDir: {
    field: 'localDir', label: '本地根目录',
    usage: '本地磁盘模式下，文件写在服务器的哪个目录。',
    source: '自己新建的文件存储可以自由指定；系统自带的那几条目录继承自「主存储」里的两个目录设置。',
    rules: '做迁移时源存储和目标存储必须是不同目录，否则同名文件会被覆盖。',
    note: '系统自带的文件存储目录在这里改不动，要去「平台自身配置」改服务器目录并重启服务（目录属于启动配置，不随档案热更新）。',
  },
}

export const DELIVERY_FIELD_GUIDES: Record<string, FieldGuide> = {
  name: STORAGE_FIELD_GUIDES.name,
  provider: {
    field: 'provider', label: 'CDN 厂商',
    usage: '选择用哪家的 CDN 来加速访问和刷新缓存。',
    source: '选好后本弹窗会显示这家的控制台、密钥、文档直达链接。',
    rules: '选「不启用 CDN」表示不做任何刷新，也不需要填凭据。',
    note: '保存即生效，无需重启；平台对象（小程序码 / 素材采用）的刷新会立即改用新厂商凭据。',
  },
  domain: {
    field: 'domain', label: '公开域名',
    usage: '访客访问文件时用的 CDN 域名，例如 https://cdn.example.com。',
    source: '在云厂商控制台把域名接入 CDN 并做 CNAME 解析后得到。',
    rules: '必须是 https:// 开头的完整地址。平台自身的 CDN 分发不填这项，域名以文件存储的访问基址为准。',
  },
  endpoint: {
    field: 'endpoint', label: '刷新接口地址',
    usage: '提交缓存刷新请求的地址。',
    source: '本弹窗上方的厂商文档里有默认值说明，通常不用填。',
    rules: '留空使用系统内置的厂商默认地址。',
  },
  resourceId: {
    field: 'resourceId', label: '资源标识',
    usage: '告诉厂商要刷新哪个加速区域：Cloudflare 填 Zone ID，CloudFront 填 Distribution ID。',
    source: 'Cloudflare 在站点「概览 → API」；CloudFront 在分发列表里。上方链接直达控制台。',
    rules: '只有 Cloudflare 和 CloudFront 需要填，其余厂商留空。',
  },
  accessKey: {
    field: 'accessKey', label: 'AccessKey / Token',
    usage: '调用厂商刷新接口的账号标识。Cloudflare 不使用这一项。',
    source: '本弹窗上方的密钥管理链接直达。',
    rules: '编辑已有配置时留空表示不修改。',
  },
  secretKey: {
    field: 'secretKey', label: 'SecretKey / API Token',
    usage: '调用刷新接口的密钥。Cloudflare 把 API Token 填在这一项。',
    source: '本弹窗上方的密钥管理链接直达。',
    rules: '编辑已有配置时留空表示不修改。',
    note: '保存后不再回传真值，忘记只能重新生成。',
  },
}
