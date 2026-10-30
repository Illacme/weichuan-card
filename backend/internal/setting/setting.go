// Package setting 系统运行参数的「后台可视化配置」基础设施。
//
// 设计目标（与产品诉求对齐）：
//   - 把过去散落在 config.go / main.go / storage.go / auth.go 等处的「写死值」
//     收敛为一张 sys_setting 表，平台超管可在 PC 后台「系统设置」界面统一维护。
//
// 配置项分三档（按「改错能否自愈」与「泄漏爆炸半径」划分，不是按「是否敏感」一刀切）：
//  1. 常规项：保存即时生效（运行时内存缓存 + 即时回写），如品牌名 / 上传上限 / 特性开关；
//  2. 需重启项（Restart）：云存储与 CDN 凭据、上传基址等。它们要在启动时建客户端，
//     保存后需重启进程才生效；凭据写错不会让系统不可达——改回来重启即可自愈；
//  3. 只读项（ReadOnly）：DB_DSN / JWT_KEY / Cookie 安全。它们一旦改错就会把
//     「改回来的入口」一起弄丢（连不上库 → 读不出配置；会话全失效 → 登不进后台），
//     因此只能在界面**掩码展示当前值**并提示改法，实际修改走环境变量 + 重启。
//
// 约定：
//   - 所有「可后台配置项」必须在 registry 登记（含类型与默认值），前端分组表单与之对应；
//     新增可配置项 = 在 registry 追加一行 + 前端加一项，缺省值会在 Init 时自动 seed 进库。
//   - 取值一律带默认值：库未初始化 / 该 key 缺失 时回落默认值，保证任意阶段调用都安全。
//   - 空串 = 「未在界面设置」，读取时回退同名环境变量（见 config.OverlayFromSettings）。
package setting

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"

	"gorm.io/gorm"
)

// Setting 系统设置表（key-value 带类型与分组）。
type Setting struct {
	ID          uint   `gorm:"primaryKey" json:"-"`
	Key         string `gorm:"uniqueIndex;size:64" json:"key"`
	Group       string `gorm:"size:32;index" json:"group"`
	Value       string `gorm:"type:text" json:"value"`
	Type        string `gorm:"size:16" json:"type"` // int | string | bool | string_slice
	Description string `gorm:"type:text" json:"description"`
}

// TableName 显式指定表名（避免 GORM 复数化）。
func (Setting) TableName() string { return "sys_setting" }

// def 单条可配置项的静态定义（类型契约 + 默认值 + 文案 + 生效方式元数据）。
type def struct {
	Key, Group, Type, Value, Desc string
	// Name 中文名：界面主标签。商业产品的配置页不能把 snake_case 的 key
	// 当标签甩给运营看（key 降级为次要灰字，排查日志/环境变量时仍可对照）。
	Name string
	// Secret：敏感项。接口只回传掩码（Mask），真实值永不出现在响应里；
	// 界面提交掩码或缺省表示「不改动」，提交空串表示「清掉、回退环境变量」。
	Secret bool
	// Restart：保存后需重启进程才生效（需在启动时建客户端的项，如对象存储 / CDN）。
	Restart bool
	// ReadOnly：只读项。不落库、不可通过接口修改，只能改环境变量后重启
	//（改错会把「改回来的入口」一起弄丢的项，见包注释第 3 档）。
	ReadOnly bool
	// EnvVar：对应环境变量名。界面据此提示改法；未设置时回退该环境变量。
	EnvVar string
	// Options 枚举候选值（可选）：界面据此渲染下拉而不是文本框，
	// 与 Validate 的 oneOf 同源——写在一处旁边，改值时一起改，
	// 避免出现「下拉能选、保存被拒」或反之（2026-10-22 用户实测：
	// storage_driver 有 oneOf 校验却渲染成文本框，填错只能靠保存时报错）。
	Options []string
	// PathKind 标记这是「服务端路径」类配置（node_bin / module_dir / source_dir），
	// 驱动界面在文本框旁渲染一个「浏览服务器目录」按钮（见 pathpick 包）。
	// 空串表示不是路径类。它解决的不是「能不能输入」，而是「在服务器上挑路径」——
	// 浏览器原生的文件框选的是客户端机器，对指向服务器的这些配置毫无意义。
	PathKind string
	// Validate 业务校验（可选）：**类型之外**的取值约束（上下限 / 枚举 / URL 前缀 /
	// 租户是否存在）。只给「填错会造成大面积故障或把自己锁住」的项加，不做普遍校验——
	// 处处拦截会让运营改个配置处处碰壁，不如让它自愈（见 validate.go 的包注释）。
	// 需要查库的校验也走这里，db 为当前库连接（未初始化时为 nil，校验需自行跳过）。
	Validate func(db *gorm.DB, value string) error
}

// Mask 敏感项在接口响应里的掩码（界面据此显示「已配置」，不回传真实值）。
const Mask = "******"

// Item 后台「系统设置」渲染用 DTO。
// 与 DB 模型 Setting 分开，避免把 secret 的真实值随表结构一并序列化出去。
type Item struct {
	Key         string `json:"key"`
	Name        string `json:"name"` // 中文名（界面主标签；为空时前端回落显示 key）
	Group       string `json:"group"`
	Type        string `json:"type"` // int | string | bool | string_slice
	Description string `json:"description"`
	// Value 当前值：敏感项恒为 Mask（已配置）或空串（未配置）。
	Value    string `json:"value"`
	Secret   bool   `json:"secret"`
	Restart  bool   `json:"restart"`
	ReadOnly bool   `json:"readOnly"`
	EnvVar   string `json:"envVar"`
	// Options 枚举候选值（仅枚举项有）：界面据此渲染下拉。omitempty 保证
	// 旧客户端（不认这个字段）与大多数项（无候选值）的响应形状不变。
	Options    []string `json:"options,omitempty"`
	PathKind   string   `json:"pathKind,omitempty"` // 服务端路径类配置（node_bin/module_dir/source_dir），驱动界面渲染浏览按钮
	Configured bool     `json:"configured"`         // 是否有有效值（库值 / 环境变量 / 进程内缺省）
}

// registry 全量「可后台配置项」清单：新增可配置项只在此追加一行即可。
// 分组（group）直接对应前端「系统设置」的分组表单：
//
//	general    通用（品牌名）
//	miniprogram 小程序（默认基址 / 独立版锁定租户）
//	upload     上传（单文件上限 / 允许扩展名 / 各类签名有效期）
//	wechat     微信（小程序码 TTL / AppID / AppSecret / 代码上传密钥）
//	mpupload   后台上传小程序代码（Node / miniprogram-ci / 源码目录 / 超时）
//	auth       登录与令牌（令牌有效期 / 登录与刷新限流）
//	storage    文件存储与 CDN（驱动 / 端点 / 桶 / 凭据 / 上传基址 / CDN 刷新）
//	security   安全与基础设施（只读：DB 连接串 / JWT 密钥 / Cookie）
//	feature    特性开关（Prometheus 指标 / 独立版企业锁定）
//	license    授权（新租户试用天数 / 席位 / 是否自动试用）
var registry = []def{
	{Key: "brand_name", Group: "general", Type: "string", Value: "微传名片",
		Name: "品牌名称",
		Desc: "显示在后台侧边栏、登录页和浏览器标签页上的名字。改完立即生效。"},
	{Key: "miniprogram_base_url", Group: "miniprogram", Type: "string",
		Name:     "小程序接口基址",
		Validate: httpURLOrEmpty(),
		// 说清「填错会怎样」比只说格式有用：这是首配最容易填错、且错了表现最远的一项。
		// 必须写 https：微信生产环境只收 https（http 仅本地开发关域名校验时可用），
		// 后端校验放行 http 是「可自愈」哲学，文案不能据此说 http 也能用——
		// 与向导的 httpsOnly 规则、悬浮提示同一口径（2026-10-22 全量核查统一）。
		Desc: "小程序访问后端的地址，必须是小程序能访问到的公网 https 地址（例：https://api.example.com；仅本地开发且关闭了域名校验才可用 http://）。留空则小程序用自己内置的地址——首次部署务必填对，填错小程序会连不上后端。"},
	{Key: "pinned_company_id", Group: "miniprogram", Type: "int", Value: "0",
		Name:     "独立版锁定企业",
		Validate: companyEnabledOrZero,
		Desc:     "把整套系统锁定成某一个企业的独立版：填企业 ID 后，小程序只展示该企业的内容（该企业必须已存在且启用）；填 0 表示平台模式，不锁定。"},
	{Key: "upload_max_mb", Group: "upload", Type: "int", Value: "10",
		Name:     "单张图片大小上限",
		Validate: intRange(1, 1024),
		Desc:     "单张图片最大能传多大（MB），可填 1~1024。超过这个大小的图片会被直接拒绝。"},
	{Key: "upload_allow_ext", Group: "upload", Type: "string_slice", Value: "png,jpg,jpeg,gif,webp,mp4",
		Name: "允许上传的文件类型",
		Desc: "允许上传的文件后缀，小写、用英文逗号分隔（例：png,jpg,webp,mp4）。不在这个名单里的文件会被拒绝。"},
	// 视频单独一个上限：图片 10MB 够用，视频动辄几十上百 MB。
	// 若复用 upload_max_mb，为迁就视频把上限拉到 100MB，等于把图片的限制一起放开
	// （误传超大图片、占满磁盘都更难收场），故两条线分开。
	// 注意：本值只约束「前端选文件时的预校验」——直传预签名由对象存储直接收文件，
	// 应用服务器看不到内容，无法在服务端强拦；要真正封顶需在对象存储侧配 Post Policy。
	{Key: "upload_video_max_mb", Group: "upload", Type: "int", Value: "100",
		Name:     "单个视频大小上限",
		Validate: intRange(1, 2048),
		Desc:     "单个视频最大能传多大（MB），可填 1~2048。视频单独设上限，图片仍受「单张图片大小上限」约束。"},
	// 签名有效期：直传 PUT 与私有桶 GET 的限时链接时长。太短前端还没传完就过期，
	// 太长等于把对象存储写成公开读，故留界面可调，但代码内有下限兜底。
	{Key: "presign_put_ttl_minutes", Group: "upload", Type: "int", Value: "15",
		Name: "上传链接有效期",
		// 兜底语义写准：填 0 / 负数不是夹到 1 分钟，而是回落 registry 的缺省值
		// （见 signTTL）。写「最短 1 分钟」会让人以为 0 会被当成 1，与实际不符。
		Desc: "上传文件时生成的临时直传链接能用多久（分钟）。太短可能传到一半就过期；填 0 或负数会回到默认 15 分钟。"},
	{Key: "sign_get_ttl_minutes", Group: "upload", Type: "int", Value: "30",
		Name: "私有桶下载链接有效期",
		Desc: "私有存储里的文件，生成的临时访问链接能用多久（分钟）。填 0 或负数会回到默认 30 分钟。"},
	{Key: "wxacode_ttl_hours", Group: "wechat", Type: "int", Value: "720",
		Name: "小程序码缓存时长",
		Desc: "生成的小程序码图片缓存多久（小时），到期后会自动重新生成。未设置或填 0 会用部署环境的默认值（默认 720 小时，约 30 天）。"},
	{Key: "wxacode_sign_ttl_minutes", Group: "wechat", Type: "int", Value: "60",
		Name: "小程序码签名链接有效期",
		Desc: "私有存储里小程序码的临时访问链接能用多久（分钟）。填 0 或负数会回到默认 60 分钟。"},
	{Key: "wxacode_rate_limit_per_min", Group: "wechat", Type: "int", Value: "30",
		Name: "小程序码生成频率限制",
		// 为什么要有这一项：小程序码接口是公开的（访客扫码看名片就要取，不能挂鉴权），
		// 而每次生成都会真调微信接口（有配额）+ 写一次对象存储，staffId 又是可枚举的。
		// 按客户端 IP 计数（不区分 staffId）：扫号正是靠换 staffId 放大调用量的，
		// 若按 staffId 分桶，每个 id 各请求一次就绕过去了。
		Desc: "同一个 IP 每分钟最多生成几次小程序码，防止有人批量扫号把微信配额打光。正常分享点开远达不到这个频率。填 0 或负数会回到默认 30 次——不会把这道防护关掉。"},
	{Key: "wx_appid", Group: "wechat", Type: "string", EnvVar: "WX_APPID",
		Name: "小程序 AppID",
		Desc: "微信小程序的 AppID，用来生成小程序码和做微信登录。留空则使用部署环境里配置的值（环境变量 WX_APPID 或配置文件）。"},
	{Key: "wx_appsecret", Group: "wechat", Type: "string", Secret: true, EnvVar: "WX_APPSECRET",
		Name: "小程序 AppSecret",
		Desc: "微信小程序的 AppSecret，用来生成小程序码。属于敏感信息，界面不会显示已保存的内容，填新值即覆盖。留空则使用部署环境里配置的值（环境变量 WX_APPSECRET 或配置文件）。"},
	{Key: "wx_upload_key", Group: "wechat", Type: "string", Secret: true,
		Name: "小程序代码上传密钥",
		Desc: "在微信公众平台 → 开发管理 → 开发设置 → 小程序代码上传 下载的 .key 文件内容，整个粘贴进来。这是把代码传到微信的唯一凭证，与 AppSecret 不是同一把钥匙（AppSecret 只能调提审/发布接口，传不了代码）。企业独立版的小程序请在「企业管理 → 凭据」里各配各的，这里配的是平台全局那一份。属于敏感信息，界面不会显示已保存的内容。"},
	// ---- 微信消息推送（图片内容安全的结论从这里回来）----
	// 为什么要单独两项：图片检测只有异步接口，送出去只拿到 trace_id，
	// 判定结果由微信推回来。没有推送配置时「送检能成功、结论永远收不到」——
	// 记录会永久停在待结论，比不送检更容易误导人（见 mediachk.Plan）。
	// Token 用于验签（必填）；EncodingAESKey 只在安全模式下用来解密（明文模式可留空）。
	{Key: "wx_msg_token", Group: "wechat", Type: "string", EnvVar: "WX_MSG_TOKEN",
		Name: "消息推送 Token",
		Desc: "微信公众平台 → 开发管理 → 开发设置 → 消息推送 里自己填的 Token，用于校验回调确实来自微信。不填则图片内容安全不送检（因为收不到结论）。填完把本页显示的推送 URL 填到微信后台。"},
	{Key: "wx_msg_aeskey", Group: "wechat", Type: "string", Secret: true, EnvVar: "WX_MSG_AESKEY",
		Name: "消息推送 EncodingAESKey",
		Desc: "消息推送选「安全模式」时必填的 43 位密钥（微信后台随机生成，可点击随机获取）。选「明文模式」可留空。属于敏感信息，界面不会显示已保存的内容。"},
	// ---- 订阅消息（审核结果通知）----
	// 这是本项目唯一能主动触达用户的通道：员工提交开通申请后就关掉小程序了，
	// 审批通过时他在别处——不发这条，他永远不知道自己已经能用了。
	//
	// 为什么只有一个模板（而不是「开通」和「改资料」各一个）：微信一次性订阅
	// 的配额是**按模板累积**的，两个模板等于让用户点两次同意、拿两次配额，
	// 而这两件事的语义都是「审核结果」。合并成一个模板，运营只需申请一次。
	//
	// 字段名做成配置项而不是写死在代码里：模板是运营在公众平台「选」的，
	// 不同模板的字段名不同（thing1 / phrase2 / character_string3 …）。
	// 写死会导致「运营换个模板，代码就静默发不出去」——而现象只是
	// 「审批成功了但员工没收到通知」，极难往配置上想。
	{Key: "wx_tmpl_audit", Group: "wechat", Type: "string",
		Name: "审核结果通知模板 ID",
		Desc: "微信公众平台 → 订阅消息 → 我的模板 里「审核结果通知」类模板的 ID（形如 AbCdEf123...）。留空则不发送通知（审批照常成功）。注意选**一次性订阅**模板，不要选长期订阅（企业服务类目申请不到）。模板建议包含三项：事项名称、审核结果、备注说明。"},
	{Key: "wx_tmpl_audit_fields", Group: "wechat", Type: "string", Value: "thing1,phrase2,thing3",
		Name: "审核结果通知模板字段",
		Desc: "上面那个模板的三个字段名，按「事项名称、审核结果、备注说明」的顺序用英文逗号分隔，默认 thing1,phrase2,thing3。请**照抄模板详情里实际显示的字段名**（例如详情里写 {{thing6.DATA}} 就填 thing6）——名字对不上微信会回「字段不存在」导致发送失败。第二项的值只有「审核通过」「未通过」几个字，thing 类型（如本模板的 thing5）或 phrase 类型都容得下，不必强求 phrase。"},

	// ---- 后台上传小程序代码（仅「后台直接传代码」这条路需要）----
	// 单独一组，不与 wx_* 那组混：那组回答「微信认谁」（凭据），这组回答
	// 「服务器上有什么」（Node / 模块 / 源码）。混在一组会让人以为配齐
	// AppSecret 就能传代码——两把钥匙 + 一套环境，缺一不可。
	{Key: "mp_node_bin", Group: "mpupload", Type: "string", EnvVar: "MP_NODE_BIN",
		Name: "Node 可执行文件", PathKind: "node_bin",
		Desc: "后台上传小程序代码要靠 Node 跑 miniprogram-ci。填 node（在 PATH 里）或绝对路径（例：/usr/local/bin/node；容器镜像里建议给绝对路径）。留空则用部署环境里配置的值（环境变量 MP_NODE_BIN 或配置文件），再留空用默认 node。不走「上传代码」这条路可以不用配。"},
	{Key: "mp_ci_module_dir", Group: "mpupload", Type: "string", EnvVar: "MP_CI_MODULE_DIR",
		Name: "miniprogram-ci 安装目录", PathKind: "module_dir",
		Desc: "服务器上装过 miniprogram-ci 的目录（在那个目录执行过 npm install miniprogram-ci）。它不随后端二进制分发，必须单独装一次。留空则改用「小程序源码目录」下找，再留空用环境变量 MP_CI_MODULE_DIR。"},
	{Key: "mp_source_dir", Group: "mpupload", Type: "string", EnvVar: "MP_SOURCE_DIR",
		Name: "小程序源码目录", PathKind: "source_dir",
		Desc: "服务器上小程序源码的根目录，里面要有 project.config.json。上传的就是这份代码——请确认它是你要发布的那一份（改完源码后不用在这里改什么）。留空则用部署环境里配置的值（环境变量 MP_SOURCE_DIR 或配置文件）。"},
	// 这一项给的是**界面缺省值**（不挂环境变量）：超时是运行期参数，
	// 没有「部署时才知道」的属性，挂在环境变量上只会出现「界面明明显示 300、
	// 实际却用环境变量里的值」这种对不上的情况。
	{Key: "mp_upload_timeout_sec", Group: "mpupload", Type: "int", Value: "300",
		Name: "上传超时（秒）",
		Desc: "等一次上传代码最长多久（秒），编译 + 传代码都算在里面，默认 300。上传是同步执行的，反代（nginx proxy_read_timeout）要设得比它大，否则会在传到一半时先返回 504。改完下一次上传就生效，不用重启服务。"},

	// ---- 登录与令牌（即时生效）----
	// 令牌有效期设太短会把人一直踢下线；代码内保留**安全下限**，
	// 界面填 0 / 负数时回落缺省，避免管理员把自己锁在系统外（改错可自愈）。
	{Key: "access_token_ttl_minutes", Group: "auth", Type: "int", Value: "15",
		Name: "登录凭证有效期",
		Desc: "登录后凭证能用多久（分钟），到期后需要换新的。改完对新登录的人立即生效，已经登录的人不受影响。填 0 或负数会回到默认 15 分钟。"},
	{Key: "refresh_token_ttl_days", Group: "auth", Type: "int", Value: "30",
		Name: "免登录保持时长",
		Desc: "「保持登录」最多能维持多少天，到期后必须重新登录。填 0 或负数会回到默认 30 天。"},
	{Key: "login_rate_limit_per_min", Group: "auth", Type: "int", Value: "10",
		Name: "登录尝试频率限制",
		Desc: "同一个 IP 每分钟最多允许试几次登录，用来防暴力破解。填 0 或负数会回到默认 10 次——不会把这道防护关掉。"},
	{Key: "refresh_rate_limit_per_min", Group: "auth", Type: "int", Value: "60",
		Name: "凭证刷新频率限制",
		Desc: "同一个 IP 每分钟最多允许刷新几次凭证。刷新比登录频繁，所以配额给得更宽。填 0 或负数会回到默认 60 次。"},
	{Key: "upload_rate_limit_per_min", Group: "auth", Type: "int", Value: "20",
		Name: "匿名上传频率限制",
		Desc: "同一个 IP 每分钟最多允许上传几次文件（申请开通页的访客头像上传走这条）。这个端点不需要登录，不限流就等于把对象存储开成免费网盘，也能批量把违规图刷到公开地址上。填 0 或负数会回到默认 20 次——不会把这道防护关掉。"},

	// ---- 文件存储与 CDN（改后需重启：客户端在启动时构造）----
	// 凭据写错最坏结果是「上传失败」，改回来重启即可自愈，不会把自己锁在系统外，
	// 因此允许在界面维护；留空则回退同名环境变量。
	{Key: "storage_driver", Group: "storage", Type: "string", Restart: true, EnvVar: "STORAGE_DRIVER",
		Name:     "存储方式",
		Options:  []string{"local", "minio", "oss", "s3"},
		Validate: oneOf("local", "minio", "oss", "s3"),
		// s3 与 minio 共用同一套端点 / 桶 / 凭据字段与 S3 客户端实现，差别只在
		// 「声明的用途」：minio 面向自建 MinIO，s3 面向腾讯云 COS / 华为云 OBS /
		// 七牛云 / AWS S3 / Cloudflare R2 等 S3 兼容云（这类云常需填存储区域）。
		Desc: "图片和视频存到哪里：local=服务器本地磁盘，minio=MinIO 或自建 S3 兼容服务，oss=阿里云 OSS，s3=S3 兼容云存储（腾讯云 COS / 华为云 OBS / 七牛云 / AWS S3 / R2 等）。留空则使用部署环境里配置的值。改完要重启服务才生效。"},
	{Key: "storage_endpoint", Group: "storage", Type: "string", Restart: true, EnvVar: "MINIO_ENDPOINT",
		Name: "存储服务端点",
		Desc: "对象存储的服务地址，只填域名或 IP:端口，不要带 http://（例：127.0.0.1:9000）。MinIO 和阿里云 OSS 共用这一项。改完要重启服务才生效。"},
	{Key: "storage_bucket", Group: "storage", Type: "string", Restart: true, EnvVar: "MINIO_BUCKET",
		Name: "存储空间名称",
		Desc: "对象存储里存放文件的桶名（MinIO 与阿里云 OSS 都叫 bucket）。上传的文件都会存进这个桶。改完要重启服务才生效。"},
	{Key: "storage_access_key", Group: "storage", Type: "string", Restart: true, EnvVar: "MINIO_ACCESS_KEY",
		Name: "存储 AccessKey",
		Desc: "对象存储的用户名（阿里云 OSS 里叫 AccessKeyId）。改完要重启服务才生效。"},
	{Key: "storage_secret_key", Group: "storage", Type: "string", Secret: true, Restart: true, EnvVar: "MINIO_SECRET_KEY",
		Name: "存储 SecretKey",
		Desc: "对象存储的密码（阿里云 OSS 里叫 AccessKeySecret）。界面不会显示已保存的内容，填新值即覆盖。改完要重启服务才生效。"},
	{Key: "storage_use_ssl", Group: "storage", Type: "bool", Restart: true, EnvVar: "MINIO_USE_SSL",
		Name: "存储启用 HTTPS",
		// 「OSS 用不到」必须写进 Desc：NewOssUploader 不收 SSL 参数（阿里云客户端
		// 固定 https），oss 驱动下这一项是空操作，前端也把它置灰（2026-10-22 全量核查口径）。
		Desc: "连接 MinIO / S3 兼容存储时是否走 HTTPS（阿里云 OSS 固定 HTTPS，用不到这项）。留空则使用部署环境里配置的值。改完要重启服务才生效。"},
	{Key: "storage_region", Group: "storage", Type: "string", Restart: true, EnvVar: "STORAGE_REGION",
		Name: "存储区域",
		// 为什么允许留空、错了也不拦：minio-go 的 Region 留空时会先按 endpoint URL
		// 猜（AWS / Google 可猜中），猜不到再向服务端发 GetBucketLocation 探测
		// （bucket-cache.go），主流 S3 云都能自动拿到；只有探测失败或不实现该接口的
		// 云才需要手填。填错的最坏结果是上传失败，改回正确值重启即可自愈，
		// 不属于「填错会把自己锁在系统外」的项，故不加 Validate。
		Desc: "连接 MinIO / S3 兼容存储（腾讯云 COS / 华为云 OBS / 七牛云 / AWS S3 / R2）时使用的区域，例：ap-guangzhou、us-east-1。留空会自动探测，大多数服务不用填；填错会导致上传失败，改回正确值重启即可恢复。阿里云 OSS 用不到这项。改完要重启服务才生效。"},
	{Key: "storage_public", Group: "storage", Type: "bool", Restart: true, EnvVar: "STORAGE_PUBLIC",
		Name: "存储空间公开访问",
		Desc: "存储桶是否允许公开访问。选「关」（私有桶）时，已上传的图片只能通过限时签名链接访问。改完要重启服务才生效。"},
	{Key: "upload_dir", Group: "storage", Type: "string", Restart: true, EnvVar: "UPLOAD_DIR",
		Name: "本地上传目录",
		Desc: "存储方式选 local 时，文件保存在服务器上的哪个目录（小程序码缓存也放这里）。换成其它存储方式后这一项不再起作用。改完要重启服务才生效。"},
	{Key: "upload_base_url", Group: "storage", Type: "string", Restart: true, EnvVar: "UPLOAD_BASE_URL",
		Name:     "文件访问基址",
		Validate: httpURLOrEmpty(),
		// 「仅对象存储需要」必须写进 Desc：local 驱动 Save 返回相对地址 /uploads/...，
		// 端上自行拼基址（浏览器按页面源、小程序用 miniprogram_base_url），
		// 这一项不参与——早期文案说「小程序和后台都用它拼」对 local 是误导，
		// 界面联动也已按 minio / oss / s3 专属过滤（2026-10-22 全量核查统一口径）。
		Desc: "已上传文件对外访问的根地址（仅 minio / oss / s3 需要），必须是 http:// 或 https:// 开头的完整地址（例：https://cdn.example.com）。小程序和后台都用它拼出完整的图片链接，填错的话所有图片都会打不开；本地存储用相对地址，不经过这项。改完要重启服务才生效。"},
	{Key: "cdn_enabled", Group: "storage", Type: "bool", Restart: true, EnvVar: "CDN_ENABLED",
		Name: "启用 CDN 刷新",
		// 2026-10-23：从「只支持阿里云」扩到五家（阿里云 / 腾讯云 / 七牛云 /
		// Cloudflare / CloudFront），Desc 里的厂商限制必须跟着改，否则用户仍以为
		// 只有阿里云能用。「覆盖同名文件」也要说准：普通上传的对象名是随机的、
		// 不会覆盖，真正会覆盖的是素材采用与小程序码重生成这两条固定对象名的路径。
		Desc: "覆盖同名文件后是否自动刷新 CDN 缓存，让访客立刻看到新文件。需先选好下面的 CDN 厂商，并把该厂商要求的凭据填全；刷新失败只记日志，不影响上传结果。改完要重启服务才生效。"},
	{Key: "cdn_provider", Group: "storage", Type: "string", Restart: true, EnvVar: "CDN_PROVIDER",
		Name:     "CDN 厂商",
		Options:  []string{"aliyun", "tencent", "qiniu", "cloudflare", "cloudfront"},
		Validate: oneOf("aliyun", "tencent", "qiniu", "cloudflare", "cloudfront"),
		// 五家的凭据形态不同（AK/SK 三家、Cloudflare 用 Token+Zone、CloudFront 用
		// AK/SK+Distribution），所以下面几项是「通用凭据位」，具体含义按厂商变：
		// 界面按厂商联动显示需要的字段，Desc 里逐项写清各厂商对应什么。
		Desc: "用哪家 CDN 的刷新接口：aliyun=阿里云，tencent=腾讯云，qiniu=七牛云，cloudflare=Cloudflare，cloudfront=AWS CloudFront。留空按阿里云处理（早期版本只有阿里云）。改完要重启服务才生效。"},
	{Key: "cdn_access_key_id", Group: "storage", Type: "string", Restart: true, EnvVar: "CDN_ACCESS_KEY_ID",
		Name: "CDN AccessKeyId",
		Desc: "刷新凭据的「账号」部分：阿里云填 AccessKeyId（可与 OSS 相同），腾讯云填 SecretId，七牛云填 AccessKey，CloudFront 填 AccessKeyId；Cloudflare 用不到这项（它只用 API Token）。改完要重启服务才生效。"},
	{Key: "cdn_access_key_secret", Group: "storage", Type: "string", Secret: true, Restart: true, EnvVar: "CDN_ACCESS_KEY_SECRET",
		Name: "CDN AccessKeySecret",
		Desc: "刷新凭据的「密钥」部分：阿里云填 AccessKeySecret，腾讯云填 SecretKey，七牛云填 SecretKey，CloudFront 填 SecretAccessKey，Cloudflare 填 API Token。界面不会显示已保存的内容，填新值即覆盖。改完要重启服务才生效。"},
	{Key: "cdn_resource_id", Group: "storage", Type: "string", Restart: true, EnvVar: "CDN_RESOURCE_ID",
		Name: "CDN 资源标识",
		Desc: "只有 Cloudflare 与 CloudFront 需要：Cloudflare 填站点 Zone ID，CloudFront 填 Distribution ID（分发 ID）。其余三家按提交 URL 的域名自动定位，不用填。改完要重启服务才生效。"},
	{Key: "cdn_endpoint", Group: "storage", Type: "string", Restart: true, EnvVar: "CDN_ENDPOINT",
		Name: "CDN 服务端点",
		Desc: "刷新接口地址，留空按厂商取默认值（阿里云 cdn.aliyuncs.com / 腾讯云 cdn.tencentcloudapi.com / 七牛云 fusion.qiniuapi.com / Cloudflare api.cloudflare.com / CloudFront cloudfront.amazonaws.com），一般不用改。改完要重启服务才生效。"},

	// ---- 安全与基础设施（只读：只能改环境变量后重启）----
	// 这三项改错会把「改回来的入口」一起弄丢（连不上库 / 登不进后台），故界面只展示。
	{Key: "db_dsn", Group: "security", Type: "string", Secret: true, Restart: true, ReadOnly: true, EnvVar: "DB_DSN",
		Name: "数据库连接串",
		// 改法要写两条：环境变量**或**配置文件。多数部署走安装向导，DB_DSN 就
		// 落在 data/runtime.env 里而没有同名环境变量，只提环境变量等于指错路。
		// 「不保存在数据库里」必须说清：否则会被读成「连接串存在库里」，与本项
		// ReadOnly 不落库（seedDefaults 跳过、Set 拒绝）的实现正好相反。
		Desc: "当前服务连的是哪个数据库（下方可查看，密码部分已隐藏）。这一项在界面上不能改：其余配置都保存在数据库里，连接串一旦填错，重启后连不上数据库，连改回来的入口都会一起消失。如需修改，请到部署环境改环境变量 DB_DSN 或配置文件（默认 data/runtime.env），再重启服务。"},
	{Key: "jwt_key", Group: "security", Type: "string", Secret: true, Restart: true, ReadOnly: true, EnvVar: "JWT_KEY",
		Name: "登录签名密钥",
		Desc: "给登录凭证签名的密钥（下方可查看首尾，中间已隐藏）。这一项在界面上不能改：改错会让所有已登录的人立刻掉线，而且没法回退。如需修改，请到部署环境改环境变量 JWT_KEY 或配置文件（默认 data/runtime.env），再重启服务。"},
	{Key: "cookie_secure", Group: "security", Type: "bool", Restart: true, ReadOnly: true, EnvVar: "COOKIE_SECURE",
		Name: "登录 Cookie 强制 HTTPS",
		Desc: "登录 Cookie 是否只在 HTTPS 下传输。正式上线（HTTPS）应为「开」；本地 http 环境选「开」会导致登录不上。界面不能改，请到部署环境改环境变量 COOKIE_SECURE 或配置文件（默认 data/runtime.env）后重启。"},
	{Key: "cookie_domain", Group: "security", Type: "string", Restart: true, ReadOnly: true, EnvVar: "COOKIE_DOMAIN",
		Name: "登录 Cookie 作用域",
		Desc: "登录 Cookie 在哪个范围内有效。留空只对当前域名生效；需要在多个子域名之间共享登录状态时才填（例：.example.com）。界面不能改，请到部署环境改环境变量 COOKIE_DOMAIN 或配置文件（默认 data/runtime.env）后重启。"},

	// 审计日志保留期（C3）。默认 0 = 永不自动清理，这是刻意的：
	// 删审计日志不可逆，而审计的价值恰恰在「出事之后能翻到很久以前」。升级当天
	// 就把历史日志清掉＝把本来就有的证据变成再也拿不回来（与 B3 配额默认不限、
	// B4 存量不补授权同一条「升级不得突变存量」纪律）。想清理必须由运维显式填天数。
	// 清理动作本身会记一条 audit.purge 审计（操作者 = 系统），所以即便有人把保留期
	// 改成 1 天，事后也能在审计页看到「什么时候、按多少天、清了多少条」。
	{Key: "audit_retention_days", Group: "security", Type: "int", Value: "0",
		Name:     "审计日志保留天数",
		Validate: intRange(0, 3650),
		Desc:     "审计日志（后台操作留痕）保留多少天，超期的自动删除。0 = 永不自动清理（默认）。填了才会清理，建议按合规要求填 180 或 365；删除不可恢复，请在「审计日志」页按需先导出留底。清理动作本身也会记一条审计（操作人显示为「系统」）。"},

	// 缺省空串 = 「未设置」，读取时回退环境变量 METRICS_ENABLED；
	// 一旦在界面显式设为 true/false 就以界面为准（避免升级后让既有部署的开关行为突变）。
	{Key: "metrics_enabled", Group: "feature", Type: "bool", EnvVar: "METRICS_ENABLED",
		Name: "开放监控指标",
		Desc: "是否开放 /metrics 监控接口，供 Prometheus 抓取数据。留空则使用部署环境里配置的值。"},
	// 默认开：UGC 内容安全是小程序提审的硬性要求，不该让运维「想起来才开」。
	// 开了也不会立刻产生副作用——没配小程序凭据、或提交人没有 openid 时一律跳过
	// （fail-open，只记日志），所以「配了凭据就自动生效」是安全的默认。
	{Key: "content_sec_enabled", Group: "feature", Type: "bool", Value: "true",
		Name: "内容安全检测",
		Desc: "员工提交名片修改、访客提交开通申请时，把姓名 / 职位 / 昵称等文字送微信内容安全接口检测，命中违规直接拒绝提交；员工上传的图片也会异步送检（需配「消息推送 Token」才生效，否则收不到结论）。依赖小程序 AppID / AppSecret，未配置时自动跳过，不影响提交。"},
	// 开启后企业管理页整体只读：列表/管理员列表可查，但新建企业、编辑企业、
	// 启停企业、新建企业管理员一律拒绝（见 handler/companies.go 的 writeLocked）。
	{Key: "independent_lock", Group: "feature", Type: "bool", Value: "false",
		Name: "独立版企业锁定",
		Desc: "开启后企业管理页变成只读：不能新建、编辑、启停企业，也不能添加企业管理员。适合交付给单一企业使用的独立版。"},
	// 腾讯视频档开关：与 metrics_enabled 同一套路——界面显式设置优先，留空回退
	// 部署环境变量 MP_TENCENT_VIDEO（空串 = 未设置，见 Validate 的 bool 分支）。
	//
	// 为什么是「开关」而不是「自动可用」：该档依赖腾讯视频小程序插件，而插件必须在
	// app.json 静态声明，声明了却没在 MP 后台授权的插件会让**整个小程序编译失败**、
	// 连预览都传不上去。所以默认关，由运维确认「插件已授权 + 已声明」后打开——
	// 打开即等于自证前置条件已满足，描述里把这个责任讲清楚。
	// 放在界面而不是只留环境变量：运维不该为了开一个功能去登服务器改配置再重启。
	{Key: "video_tencent_enabled", Group: "feature", Type: "bool", EnvVar: "MP_TENCENT_VIDEO",
		Name: "腾讯视频档",
		Desc: "开启后「公司简介 → 视频来源」才可选「腾讯视频」档（视频托管在腾讯视频，小程序内原位播放、画面不带视频号角标，且能记播放统计）。前置条件在界面之外，需由部署方完成：① 小程序后台「设置 → 第三方服务 → 插件管理」添加腾讯视频插件并授权；② 小程序 app.json 已声明该插件（用 miniprogram/scripts/enable-tencent-video.mjs 打开）——缺任一项，小程序端该组件渲染不出来，会按兜底规则回落到封面图或兜底 MP4。留空则使用部署环境变量 MP_TENCENT_VIDEO 的值。即时生效，无需重启。"},

	// 授权（升级后新增分组）：平台模式「新租户默认给试用额度」的三个口径。
	//
	// 为什么这三项要能在界面改：试用期是天数、席位是销售政策，随客户分层经常调整，
	// 不该让运营改一次代码重新发版。取值在**开通企业那一刻**落到 tenant_license 行，
	// 之后改这三项不会追溯影响已开通的企业（改的只是「以后新开的」）。
	{Key: "license_trial_enabled", Group: "license", Type: "bool", Value: "true",
		Name: "新租户自动试用",
		Desc: "开通新企业时自动写入一条试用授权（席位 + 到期日）。关闭后新企业不写授权行，行为回到「不限制」。已有企业不受影响。"},
	{Key: "license_trial_days", Group: "license", Type: "int", Value: "14",
		Name:     "试用天数",
		Validate: intRange(1, 3650),
		Desc:     "试用授权的有效天数，从开通企业当天起算。到期后该企业后台变为只读：仍可登录查看，但所有写操作被拒绝，续费后在「企业管理 → 授权」里延长即可恢复。"},
	{Key: "license_trial_seats", Group: "license", Type: "int", Value: "5",
		Name:     "试用员工席位",
		Validate: intRange(1, 100000),
		Desc:     "试用期最多能建多少个员工（管理员直接新增与审批开通申请两条路径合并计数）。超出后被拒绝，提示升级授权。"},

	// 存储配额：按租户累计上传字节数。默认 0 = 不限制——存量部署升级后行为不变，
	// 想按量计费或做成本封顶时再填（单企业上限，可被 tenant 级 override 覆盖）。
	{Key: "storage_quota_mb", Group: "storage", Type: "int", Value: "0",
		Name:     "每租户存储配额",
		Validate: intRange(0, 1048576),
		Desc:     "单个企业可用的上传空间上限（MB）。0 = 不限制（默认）。超出后该企业再上传会被拒绝，并提示已用 / 上限；用量与配额可在「企业管理」列表查看（统计自本功能启用后新上传的文件）。"},
}

// registryByKey 供按 key 快速查类型。
var registryByKey = func() map[string]def {
	m := make(map[string]def, len(registry))
	for _, d := range registry {
		m[d.Key] = d
	}
	return m
}()

// publicKeys 公开接口仅下发这些非敏感项（小程序 / 前端引导用）。
var publicKeys = map[string]bool{
	"brand_name":           true,
	"miniprogram_base_url": true,
	"pinned_company_id":    true,
}

// Store 内存缓存 + DB 持久化的设置存储。
type Store struct {
	db     *gorm.DB
	mu     sync.RWMutex
	values map[string]string
}

var (
	global *Store
	gmu    sync.RWMutex
)

// Init 首次启动时建表（AutoMigrate）、seed 缺省值、载入内存缓存，并置为全局实例。
// 必须在 handler 装配之前调用（cmd/server 与 devserver 均已接入）。
//
// 建表加 HasTable 门闩的原因（2026-10-21 实证，别改回去）：
// MySQL 下 sys_setting 由 migrations/0014 建（UNIQUE KEY uk_setting_key），
// 而模型 Key 用的是 uniqueIndex（gorm 自动名 uni_sys_setting_key）而非 unique
// tag——field.Unique=false。gorm 的 MigrateColumnUnique 看到「库里的 key 列带
// 唯一索引（column_key=UNI）而模型没有 unique tag」，误判为「模型删掉了唯一
// 约束」，按命名策略名执行 DROP CONSTRAINT uni_sys_setting_key；mysql driver
// v1.5.2 的 DropConstraint 无条件发 ALTER TABLE ... DROP FOREIGN KEY（v1.3.6
// 还有 nil 守卫，5.x 删了），而库里实际叫 uk_setting_key → Error 1091，
// 安装向导与 MySQL 形态启动全部卡死在这。
// 表已存在时跳过 AutoMigrate 即绕开该假阳性；MySQL 的表结构本就归 migrations
// 管（与 main.go 对 MySQL 不 AutoMigrate 同一策略），缺列应补迁移而非靠
// AutoMigrate 擅自改。SQLite 下 HasTable 恒同步，行为不变。
func Init(db *gorm.DB) error {
	s := &Store{db: db, values: map[string]string{}}
	if !db.Migrator().HasTable(&Setting{}) {
		if err := db.AutoMigrate(&Setting{}); err != nil {
			return fmt.Errorf("auto migrate sys_setting: %w", err)
		}
	}
	if err := s.seedDefaults(); err != nil {
		return err
	}
	if err := s.reload(); err != nil {
		return err
	}
	gmu.Lock()
	global = s
	gmu.Unlock()
	return nil
}

// seedDefaults 把 registry 中尚未落库的 key 以默认值写入（幂等）。
// 只读项（ReadOnly）不落库：它们只能由环境变量决定，写进库反而会造成「界面没改、库里有值」的误解。
func (s *Store) seedDefaults() error {
	for _, d := range registry {
		if d.ReadOnly {
			continue
		}
		var cnt int64
		// key 是 MySQL 保留字：必须用 map/struct 条件让 gorm 按方言加引号
		// （裸字符串 "key = ?" 不会被 gorm 补反引号，MySQL 直接 1064；
		// SQLite 宽容所以潜伏至今——2026-10-21 parity 首跑抓出）
		if err := s.db.Model(&Setting{}).Where(map[string]interface{}{"key": d.Key}).Count(&cnt).Error; err != nil {
			return err
		}
		if cnt > 0 {
			continue
		}
		if err := s.db.Create(&Setting{
			Key: d.Key, Group: d.Group, Value: d.Value, Type: d.Type, Description: d.Desc,
		}).Error; err != nil {
			return err
		}
	}
	return nil
}

// reload 全量载入当前值到内存（Put 后会调用，保证下次读取即时生效）。
func (s *Store) reload() error {
	var rows []Setting
	if err := s.db.Find(&rows).Error; err != nil {
		return err
	}
	m := make(map[string]string, len(rows))
	for _, r := range rows {
		m[r.Key] = r.Value
	}
	s.mu.Lock()
	s.values = m
	s.mu.Unlock()
	return nil
}

func (s *Store) getRaw(key string) (string, bool) {
	s.mu.RLock()
	v, ok := s.values[key]
	s.mu.RUnlock()
	return v, ok
}

// GetString 取字符串；缺失回落 def。
func (s *Store) GetString(key, def string) string {
	if v, ok := s.getRaw(key); ok {
		return v
	}
	return def
}

// GetInt 取整数；缺失 / 非法回落 def。
func (s *Store) GetInt(key string, def int) int {
	v, ok := s.getRaw(key)
	if !ok || v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return def
	}
	return n
}

// GetBool 取布尔；缺失 / 非法回落 def（识别 1/true/yes/on 与 0/false/no/off）。
func (s *Store) GetBool(key string, def bool) bool {
	v, ok := s.getRaw(key)
	if !ok || v == "" {
		return def
	}
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	default:
		return def
	}
}

// GetStringSlice 取逗号分隔的切片；缺失 / 空回落 def。
func (s *Store) GetStringSlice(key string, def []string) []string {
	v, ok := s.getRaw(key)
	if !ok || v == "" {
		return def
	}
	parts := strings.Split(v, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	if len(out) == 0 {
		return def
	}
	return out
}

// Set 校验类型后写入 DB 并刷新内存（key 必须已在 registry 登记）。
// 用于后台「系统设置」提交；任何一项非法整体失败，由调用方提示。
func (s *Store) Set(key, value string) error {
	d, ok := registryByKey[key]
	if !ok {
		return fmt.Errorf("未知设置项: %s", key)
	}
	if d.ReadOnly {
		// 措辞不能只说「环境变量」：优先级是 环境变量 > 配置文件 > 内置缺省，
		// 而安装向导正是把 DB_DSN / JWT_KEY / COOKIE_* 写进配置文件（data/runtime.env）。
		// 走向导装的部署根本没有环境变量，说「只能改环境变量」会让人找不到东西改。
		return fmt.Errorf("%s 为只读项，只能改环境变量 %s 或配置文件（默认 data/runtime.env）后重启", key, d.EnvVar)
	}
	if d.Secret && value == Mask {
		return fmt.Errorf("%s 不能提交掩码值；留空表示回退环境变量", key)
	}
	if err := validateType(d.Type, value); err != nil {
		return fmt.Errorf("%s: %w", key, err)
	}
	// 业务校验（值域 / 枚举 / 租户存在性）：类型合法不代表取值合法，
	// 例如上传上限填 0 会让所有上传失败，基址漏写协议头会让小程序永远拿不到正确地址。
	if d.Validate != nil {
		if err := d.Validate(s.db, value); err != nil {
			return fmt.Errorf("%s: %w", key, err)
		}
	}
	err := s.db.Transaction(func(tx *gorm.DB) error {
		var existing Setting
		// 同 seedDefaults：key 是 MySQL 保留字，用 map 条件让 gorm 加引号
		if err := tx.Where(map[string]interface{}{"key": key}).First(&existing).Error; err == nil {
			existing.Value = value
			return tx.Save(&existing).Error
		} else if !isNotFound(err) {
			return err
		}
		return tx.Create(&Setting{
			Key: d.Key, Group: d.Group, Value: value, Type: d.Type, Description: d.Desc,
		}).Error
	})
	if err != nil {
		return err
	}
	s.mu.Lock()
	s.values[key] = value
	s.mu.Unlock()
	return nil
}

func isNotFound(err error) bool {
	return err != nil && (err == gorm.ErrRecordNotFound ||
		strings.Contains(strings.ToLower(err.Error()), "record not found"))
}

func validateType(typ, value string) error {
	switch typ {
	case "int":
		if _, err := strconv.Atoi(value); err != nil {
			return fmt.Errorf("必须为整数，收到 %q", value)
		}
	case "bool":
		if strings.TrimSpace(value) == "" {
			// 空串 = 「未在界面设置」，由调用方回退环境变量（见 metrics_enabled）
			return nil
		}
		switch strings.ToLower(strings.TrimSpace(value)) {
		case "true", "false", "1", "0", "yes", "no", "on", "off":
		default:
			return fmt.Errorf("必须为布尔值(true/false)，收到 %q", value)
		}
	case "string_slice":
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("扩展名列表不能为空")
		}
	case "string", "":
		// 任意字符串均合法
	default:
		return fmt.Errorf("未知类型 %q", typ)
	}
	return nil
}

// ---- 包级便捷函数（读写全局实例；未初始化时读取安全回落默认值，写入报错） ----

func getStore() *Store {
	gmu.RLock()
	s := global
	gmu.RUnlock()
	if s == nil {
		return &Store{values: map[string]string{}}
	}
	return s
}

// GetString / GetInt / GetBool / GetStringSlice 包级封装。
func GetString(key, def string) string  { return getStore().GetString(key, def) }
func GetInt(key string, def int) int    { return getStore().GetInt(key, def) }
func GetBool(key string, def bool) bool { return getStore().GetBool(key, def) }
func GetStringSlice(key string, def []string) []string {
	return getStore().GetStringSlice(key, def)
}

// Reload 重新从 DB 载入内存（Put 已即时刷新，此处供外部强制刷新）。
func Reload() error { return getStore().reload() }

// envSnapshot 进程启动时的「环境变量类配置」实际值快照（仅用于只读项展示，不落库）。
// 由 handler.RegisterSettings 注入（见 SetEnvSnapshot）：DB_DSN 等项即使环境变量没设，
// 进程内也有缺省值，只有注入快照才能如实告诉管理员「当前到底配了什么」。
var (
	envSnapshot   = map[string]string{}
	envSnapshotMu sync.RWMutex
)

// SetEnvSnapshot 注入环境变量类配置项的当前实际值（键为 registry 的 key，如 db_dsn）。
func SetEnvSnapshot(m map[string]string) {
	envSnapshotMu.Lock()
	envSnapshot = m
	envSnapshotMu.Unlock()
}

func envValue(key, envVar string) string {
	envSnapshotMu.RLock()
	v, ok := envSnapshot[key]
	envSnapshotMu.RUnlock()
	if ok && v != "" {
		return v
	}
	if envVar == "" {
		return ""
	}
	return os.Getenv(envVar)
}

// All 返回全部可配置项（含类型 / 描述 / 当前值 / 生效方式），供后台「系统设置」渲染分组表单。
// 以 registry 为序，保证界面稳定；敏感项只回传掩码，真实值不出接口。
func All() []Item {
	g := getStore()
	out := make([]Item, 0, len(registry))
	for _, d := range registry {
		stored := g.GetString(d.Key, d.Value)
		// 只读项不落库，值一律取进程内的环境变量实际值（由 envSnapshot 注入）。
		if d.ReadOnly {
			stored = envValue(d.Key, d.EnvVar)
		}
		it := Item{
			Key: d.Key, Name: d.Name, Group: d.Group, Type: d.Type,
			Description: d.Desc,
			Secret:      d.Secret, Restart: d.Restart, ReadOnly: d.ReadOnly, EnvVar: d.EnvVar,
			Options: d.Options, PathKind: d.PathKind,
		}
		it.Configured = configured(d, stored)
		if d.Secret {
			// 敏感项：已配置的**可写项**回传掩码（界面显示「已配置」，填新值才覆盖）。
			// 只读项例外——它界面改不了，展示它的全部意义就是让人确认「当前配的是不是这个」，
			// 六颗星等于什么都没说，出问题时无法核对连的是哪个库 / 用的是哪个密钥。
			// 但也不能整串明文：连接串含数据库口令、JWT 密钥可伪造登录态。
			// 折中为「可辨认预览」：保留结构与首尾、只遮中段（与云厂商控制台同一做法）。
			if it.Configured {
				it.Value = Mask
				if d.ReadOnly {
					it.Value = preview(stored)
				}
			}
		} else {
			it.Value = stored
		}
		out = append(out, it)
	}
	return out
}

// preview 敏感值的「可辨认预览」：能核对是不是自己配的那一个，但不整串泄露。
//
// 只用于只读项（db_dsn / jwt_key）。它们界面改不了，展示的目的就是核对；
// 而六颗星无法核对，整串明文又是把数据库口令和签名密钥摊在页面上
// （截图、肩窥、录屏都会泄露）。折中规则：
//   - 连接串（user:pass@tcp(host:port)/db）：保留结构，只遮口令那一段；
//   - 无凭据形态（sqlite:///path 等含 "://"）：原样返回，本来就没有秘密；
//   - 其它（如密钥）：首尾各留 4 位，中间遮掉。
func preview(v string) string {
	v = strings.TrimSpace(v)
	if v == "" {
		return ""
	}
	// sqlite:///xxx、file:///xxx 这类不含凭据，直接显示更好核对
	if strings.Contains(v, "://") {
		return v
	}
	// user:password@tcp(host:port)/db?param → user:******@tcp(host:port)/db?param
	if at := strings.IndexByte(v, '@'); at > 0 {
		if c := strings.IndexByte(v[:at], ':'); c > 0 {
			return v[:c+1] + Mask + v[at:]
		}
	}
	// 露头尾的位数随长度伸缩：长密钥露 4 位足矣；短值（如 dev 缺省密钥）露多了
	// 约等于全文，只露 2 位，既保留「能认出是不是它」，又不至于几乎完整泄露。
	keep := 4
	if len(v) < 12 {
		keep = 2
	}
	if len(v) < 8 {
		// 太短：怎么露都约等于全文，退回掩码
		return Mask
	}
	return v[:keep] + "****" + v[len(v)-keep:]
}

// IsSecret 该项是否为敏感项（密钥 / 连接串）：接口只回传掩码，审计详情也不能记明文。
func IsSecret(key string) bool {
	d, ok := registryByKey[key]
	return ok && d.Secret
}

// IsRestart 该项保存后是否需重启进程才生效（云存储 / CDN 客户端在启动时构造）。
// PUT 接口据此在返回 message 里点名提示，避免管理员误以为「改了没生效」。
func IsRestart(key string) bool {
	d, ok := registryByKey[key]
	return ok && d.Restart
}

// configured 判断该项当前是否已有有效值：库值非空，或环境变量 / 进程快照非空。
// 布尔项只看「有没有被显式设置」——缺省 false 不应当显示成已配置。
func configured(d def, stored string) bool {
	if strings.TrimSpace(stored) != "" {
		return true
	}
	// 布尔项的「未设置」是空串而不是 "false"：缺省关的开关不应显示成已配置
	return envValue(d.Key, d.EnvVar) != ""
}

// Upsert 包级写入（校验类型）。未初始化时返回错误。
func Upsert(key, value string) error {
	s := getStore()
	if s.db == nil {
		return fmt.Errorf("系统设置未初始化")
	}
	return s.Set(key, value)
}

// Public 返回非敏感公开项（小程序 / 前端引导用）。
func Public() map[string]string {
	g := getStore()
	m := make(map[string]string, len(publicKeys))
	for k := range publicKeys {
		m[k] = g.GetString(k, "")
	}
	return m
}
