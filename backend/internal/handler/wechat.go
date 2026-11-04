package handler

import (
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"weichuan/internal/config"
	"weichuan/internal/middleware"
	"weichuan/internal/model"
	"weichuan/internal/setting"
	"weichuan/internal/storage"
	"weichuan/internal/wechat"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// 小程序码内存缓存：避免每次请求都重新调微信 + 重新写对象存储。
// expAt 表示「二维码内容」的过期时间（到达后重新调微信生成）；私有桶下每次返回前会单独签发短期 GET URL。
//
// 键是 staffId + 对象名后缀（见 wxacodeCacheKey），不是裸 staffId：
// 普通码与海报码是**两个不同的码**（scene 不同、对象名不同），必须各占一条缓存。
// 曾经让两者共用一个键，导致后生成的顶掉前者；后来又干脆让海报码不进缓存——
// 那等于每次打开海报页都要同步走「调微信 + 写存储 + 刷 CDN」，海报页首屏
// 硬等 0.3~1s（2026-11-04 用户反馈「生成海报页很慢」就是这条）。
var (
	wxacodeMu    sync.Mutex
	wxacodeCache = map[string]wxacodeEntry{}
)

// wxacodeCacheMax 小程序码缓存条数上限。
// staffId 可被枚举，而每条缓存 miss 都会真调微信接口 + 写一次对象存储：
// 没有上限时，扫一段 staffId 就能把缓存撑成一张越来越大的表（同时也放大微信配额消耗）。
// 限流是第一道闸，容量上限是纵深防御——两者都不是「防止正常使用」，正常员工量远达不到这个数。
const wxacodeCacheMax = 4096

type wxacodeEntry struct {
	key       string    // 对象键，如 wxacode/12.jpg
	publicURL string    // 公开形态 URL（BaseURL/key），公开桶或本地私有兜底用
	expAt     time.Time // 二维码内容过期时间（按 cfg.WxacodeTTL）
	// env 生成这个码时用的环境版本。缓存命中要求它等于当前设置——
	// 否则后台把「正式版 / 体验版」切换后，已缓存的码仍指向旧版本，要等
	// 720 小时 TTL 到期才生效，表现为「设置改了没反应」。
	env string
}

// wxacodeCacheKey 缓存键 = staffId + 对象名后缀（普通码 ""、海报码 "_poster"）。
// 后缀与对象名用的是同一个值，所以「缓存分键」和「对象分名」永远同步，
// 不会出现「缓存分开了但还写同一个对象」或反过来。
func wxacodeCacheKey(staffID int64, suffix string) string {
	return strconv.FormatInt(staffID, 10) + suffix
}

// wxacodePut 写入缓存并在接近上限时先淘汰：先清已过期的，仍满则按 expAt 淘汰最早的一半。
// 调用方不持锁（本函数内部加锁）。
func wxacodePut(key string, e wxacodeEntry) {
	wxacodeMu.Lock()
	defer wxacodeMu.Unlock()
	if _, exists := wxacodeCache[key]; !exists && len(wxacodeCache) >= wxacodeCacheMax {
		wxacodeEvictLocked()
	}
	wxacodeCache[key] = e
}

// wxacodeEvictLocked 淘汰：先删已过期的；删完仍满则删掉 expAt 最早的一半。
// 调用方必须已持有 wxacodeMu。
func wxacodeEvictLocked() {
	now := time.Now()
	for id, e := range wxacodeCache {
		if now.After(e.expAt) {
			delete(wxacodeCache, id)
		}
	}
	if len(wxacodeCache) < wxacodeCacheMax {
		return
	}
	// 按过期时间排序取最早的一半：保留离过期最远的（最可能还被用到的）那一半
	keys := make([]string, 0, len(wxacodeCache))
	for k := range wxacodeCache {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		return wxacodeCache[keys[i]].expAt.Before(wxacodeCache[keys[j]].expAt)
	})
	for _, k := range keys[:len(keys)/2] {
		delete(wxacodeCache, k)
	}
}

// getWXACode 小程序码生成器的间接层：生产指向 wechat.GetWXACode。
// 抽这一层的唯一目的是可测——微信接口要外网 + 真凭据，注入后才能在单测里覆盖
// 「固定对象名覆盖写 → CDN 刷新」这条支线（见 TestCDNRefreshOnOverwritePaths）。
var getWXACode = wechat.GetWXACode

// wxacodeParams 组装出码参数：page 默认首页，环境版本取后台设置。
//
// check_path 与 env_version 必须联动：微信的 check_path 语义是「page 是否存在于
// **该环境版本**」。正式版未发布时 release + check_path=true 必然 41030
// （验收期就是这个坑），而体验版 / 开发版的页面集合与现网不同，校验只会误拒，
// 所以非 release 一律不校验。设置项见 sys_setting.wxacode_env_version。
func wxacodeParams(scene, page string) wechat.WXACodeParams {
	env := strings.ToLower(strings.TrimSpace(setting.GetString("wxacode_env_version", "release")))
	if env != "trial" && env != "develop" {
		env = "release"
	}
	return wechat.WXACodeParams{
		Scene:      scene,
		Page:       page,
		EnvVersion: env,
		CheckPath:  env == "release",
	}
}

// RegisterWechat 小程序码接口：按 staffId 生成（写入当前存储驱动）小程序码，返回可访问 URL
// 海报合成放在小程序端 canvas；后端只负责产出小程序码图片，存到 local/minio/oss 后返回访问 URL。
// 私有桶（STORAGE_PUBLIC=false）下，返回的是限时签名 GET URL，避免把对象设为公开读。
//
// refresher 可选：小程序码对象名固定（默认码 wxacode/<staffID>.jpg、海报码
// wxacode/<staffID>_poster.jpg），TTL 到期重新生成 = 覆盖写，
// 覆盖后要让 CDN 边缘失效，否则已分享出去的旧链接会一直拿到旧图
// （2026-10-23 之前刷新只挂在随机名的上传接口，这条覆盖路径根本没触发）。
func RegisterWechat(r *gin.Engine, db *gorm.DB, cfg config.Config, up storage.Uploader, refresher storage.CDNRefresher) {
	// 按租户取凭据：staffId / companyId → 该企业的小程序凭据，未配则回落
	// 全局设置（sys_setting）与环境变量。取值链见 wxAppOf。
	// 之所以不写成「平台模式走全局、企业版走租户」两条分支：全局那一份本来就是
	// 回落链的最后一环，租户没配时 wxAppOf 自动落到它，行为与改造前一致。
	tenantWC := func(companyID int64) wechat.Config {
		return wxAppOf(db, companyID, cfg)
	}

	// 微信身份换取（公开）：小程序 wx.login 的 code → openid。
	// 无论是否配置小程序码凭据都注册；未配置时返回明确错误，前端据此降级（不影响账号密码登录/申请）。
	r.POST("/api/wechat/openid", func(c *gin.Context) {
		var req struct {
			Code string `json:"code"`
			// 客户端按自己所属租户填（企业独立版固定、平台模式粘滞，见 miniprogram/utils/tenant.ts）。
			// 可以被伪造，但伪造只会让 code2session 失败：微信会校验 appid 与 code
			// 是否同属一个小程序，拼不出「能用的错凭据」。所以这里信任入参，
			// 换来的是「不必为每个 code 反查员工」。
			CompanyID int64 `json:"companyId"`
		}
		if c.ShouldBindJSON(&req) != nil || req.Code == "" {
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "缺少 code"})
			return
		}
		wc := tenantWC(req.CompanyID)
		if wc.AppID == "" || wc.AppSecret == "" {
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "未配置小程序 AppID / WX_APPSECRET"})
			return
		}
		openid, _, err := wechat.Code2Session(wc, req.Code)
		if err != nil {
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"code": 0, "openid": openid})
	})

	// 小程序码是**公开**接口（访客扫码看名片就要取，不能挂鉴权），
	// 但一次 cache miss = 一次真实微信接口调用（有配额）+ 一次对象存储写入，
	// staffId 又是可枚举的整数——没有闸门时，一个脚本就能打光微信配额并撑爆内存缓存。
	// 所以按客户端 IP 限流：正常分享点开远达不到这个频率，撞线的只有扫号行为
	// （不同 staffId 也走同一个桶——扫号正是靠换 staffId 放大调用量的）。
	wxacodeLimiter := middleware.NewRateLimiter(func() int {
		return setting.GetInt("wxacode_rate_limit_per_min", 30)
	}, 30, time.Minute).
		WithMessage("小程序码生成过于频繁，请稍后再试")

	r.GET("/api/wechat/wxacode", wxacodeLimiter.Handler(), func(c *gin.Context) {
		staffID, _ := strconv.ParseInt(c.Query("staffId"), 10, 64)
		if staffID <= 0 {
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "缺少 staffId"})
			return
		}
		// 未配置凭据时降级为明确错误：放在请求期判定（而非注册期一次性判定），
		// 这样后台补上 AppID 后立刻可用，无需重启进程。
		// 按 staffId 归属的租户取——企业独立版的员工要用自己企业的小程序码。
		wc := tenantWC(companyIDOf(db, staffID))
		if wc.AppID == "" || wc.AppSecret == "" {
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "未配置小程序 AppID / WX_APPSECRET"})
			return
		}
		page := c.Query("page")
		if page == "" {
			page = "pages/index/index"
		}
		// from 标记（仅认 whitelist 的 "poster"，其余一律忽略——防止任意字符串
		// 被埋进小程序码 scene）。poster 页生成码时带 &from=poster，使扫海报进来的
		// 访客来源可归为 poster，与「扫普通码」区分（两者 scene 都是 scan 区间）。
		fromMark := c.Query("from")
		if fromMark != "poster" {
			fromMark = ""
		}
		scene := "staffId=" + strconv.FormatInt(staffID, 10)
		if fromMark == "poster" {
			scene += "&from=poster"
		}
		// 出码参数先算出来：缓存命中判断要用到 env（见 wxacodeEntry.env），
		// 不能等真正生成时才算。
		p := wxacodeParams(scene, page)

		// 对象名后缀 / 缓存键后缀：海报码与普通码是两个不同的码，落不同对象、占不同缓存条目。
		// 后缀**只由 fromMark 决定**，与 env 无关：切环境版本时靠 entry.env 判失效，
		// 若把它编进对象名，每切一次版本就会在存储里留一份新的孤儿文件。
		suffix := ""
		if fromMark == "poster" {
			suffix = "_poster"
		}
		cacheKey := wxacodeCacheKey(staffID, suffix)

		// 重新生成的码（不论命中/未命中缓存都会走到这里），e 在函数作用域内声明，
		// 缓存命中的提前返回分支不会用到它，故放在外层。
		var e wxacodeEntry
		var ok bool
		// 1) 命中缓存且二维码内容未过期：直接取（私有桶下当场签发短期 URL）
		//
		// 海报码同样走缓存：它每次打开海报页都会被请求一次，而一次 miss 的代价是
		// 「调微信 + 写对象存储 + 刷 CDN」三个同步外网往返，直接压在海报页首屏上。
		// 分键之后普通码与海报码各存一条，互不覆盖（这正是当初关掉海报码缓存的
		// 原因，用分键解决比放弃缓存正确得多）。
		wxacodeMu.Lock()
		e, ok = wxacodeCache[cacheKey]
		wxacodeMu.Unlock()
		// 命中条件里必须带 env：后台把正式版切成体验版后，旧 entry 指向的是
		// 另一个版本的码，直接给它用户会扫到「非预期版本」（且要等 TTL 到期才
		// 自愈）。env 不同就当作未命中，重新生成。
		if ok && time.Now().Before(e.expAt) && e.env == p.EnvVersion {
			url, err := resolveWxacodeURL(e, cfg, up)
			if err != nil {
				c.JSON(http.StatusOK, gin.H{"code": 1, "msg": err.Error()})
				return
			}
			c.JSON(http.StatusOK, gin.H{"code": 0, "data": gin.H{"url": url}})
			return
		}

		// 2) 重新生成：调微信 -> 写入当前存储驱动 -> 缓存
		b, err := getWXACode(wc, p)
		if err != nil {
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": err.Error()})
			return
		}
		// 对象名：默认码与海报码**必须分开命名**。
		// 两者的 scene 不同（普通码 staffId=N，海报码多一个 &from=poster），
		// 共用 wxacode/<staffID>.jpg 时后生成的那个会直接覆盖前者：
		// 海报码生成完，内存缓存里默认码的 entry 仍指向同一个对象 → 扫普通码
		// 拿到的是「带 from=poster 的码」，来源被记成 poster，裂变榜虚高；反之同理。
		// 这种错**不报错、码也能打开**，只会在数据对不上时才被发现。
		key := fmt.Sprintf("wxacode/%d%s.jpg", staffID, suffix)
		publicURL, err := up.SaveBytes(key, b, "image/jpeg")
		if err != nil {
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": err.Error()})
			return
		}
		e = wxacodeEntry{key: key, publicURL: publicURL, expAt: time.Now().Add(wxacodeTTL(cfg)), env: p.EnvVersion}
		wxacodePut(cacheKey, e)
		// 固定对象名的覆盖写：刷新的是公开形态 URL（私有桶下返回的短期签名 URL 每次都变，
		// 交给 CDN 刷也没有意义；私有桶本身也不该被 CDN 缓存）
		refreshCDN(refresher, e.publicURL)

		url, err := resolveWxacodeURL(e, cfg, up)
		if err != nil {
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"code": 0, "data": gin.H{"url": url}})
	})
}

// resolveWxacodeURL 公开桶返回公开 URL；私有桶（且存储支持签名）返回限时 GET URL（每次重签，1 小时有效）。
// 本地驱动无 Signer：静态路由已托管，回退公开 URL。
func resolveWxacodeURL(e wxacodeEntry, cfg config.Config, up storage.Uploader) (string, error) {
	if cfg.StoragePublic {
		return e.publicURL, nil
	}
	if s, ok := up.(storage.Signer); ok {
		// 签名时长取系统设置（默认 1 小时），下限 1 分钟
		return s.SignGetURL(e.key, signTTL("wxacode_sign_ttl_minutes", 60))
	}
	return e.publicURL, nil
}

// resolveWXAppID 小程序 AppID：优先取系统设置 wx_appid，留空时回退环境变量 WX_APPID。
// 留空回退而非「设置项存在就覆盖」，是因为 seed 会把 wx_appid 写成空串——
// 若直接以设置为准，会让既有只配了环境变量的部署凭空失效。
func resolveWXAppID(cfg config.Config) string {
	if v := strings.TrimSpace(setting.GetString("wx_appid", "")); v != "" {
		return v
	}
	return cfg.WXAppID
}

// resolveWXAppSecret 与 resolveWXAppID 同构：优先取系统设置 wx_appsecret（界面里以掩码展示、
// 提交后仅存库不回传），留空回退环境变量 WX_APPSECRET。
func resolveWXAppSecret(cfg config.Config) string {
	if v := strings.TrimSpace(setting.GetString("wx_appsecret", "")); v != "" {
		return v
	}
	return cfg.WXAppSecret
}

// ---- 按租户取小程序凭据 ----
//
// 小程序有两种形态，凭据来源不同：
//   平台模式（一个 appid 服务所有企业）→ sys_setting 的全局那一份就够；
//   企业独立版（每企业一个 appid，共用同一套后端）→ 必须用该企业自己那份。
//
// 所以取值链是三级回落：**租户凭据 → 全局设置 → 环境变量**。
// 平台模式的租户不配凭据，自动落到第二级，行为与改造前完全一致。

var (
	wxAppMu    sync.Mutex
	wxAppCache = map[int64]wxAppEntry{}
)

type wxAppEntry struct {
	appID     string
	appSecret string
	expAt     time.Time
}

// wxAppCacheTTL 租户凭据的缓存时长。设置成短 TTL 而不是「改完即失效」：
// 后台保存时已经主动清缓存（见 clearWXAppCache），这里的 TTL 只是兜底，
// 兼顾「不每请求查库」与「改错了最多 30 秒就恢复」。
const wxAppCacheTTL = 30 * time.Second

// clearWXAppCache 后台保存租户凭据后调用：改完立刻生效，不用等 TTL。
func clearWXAppCache(companyID int64) {
	wxAppMu.Lock()
	delete(wxAppCache, companyID)
	wxAppMu.Unlock()
}

// tenantWXApp 读租户的小程序凭据。**必须成对返回**：只配了 appid 或只配了
// appsecret 时按「没配」处理——半配置比不配更危险，会拼出
// 「平台的 appid + 企业的 secret」这种组合，生成出来的码谁也扫不开，
// 而报错信息只会指向微信侧，极难查。后台界面也是成对保存的。
func tenantWXApp(db *gorm.DB, companyID int64) (string, string) {
	if db == nil || companyID <= 0 {
		return "", ""
	}
	wxAppMu.Lock()
	e, ok := wxAppCache[companyID]
	wxAppMu.Unlock()
	if ok && time.Now().Before(e.expAt) {
		return e.appID, e.appSecret
	}
	var row model.CompanyWXApp
	err := db.Where("company_id = ?", companyID).First(&row).Error
	appID, appSecret := "", ""
	if err == nil {
		appID = strings.TrimSpace(row.AppID)
		appSecret = strings.TrimSpace(row.AppSecret)
	}
	if appID == "" || appSecret == "" {
		appID, appSecret = "", ""
	}
	wxAppMu.Lock()
	wxAppCache[companyID] = wxAppEntry{appID: appID, appSecret: appSecret, expAt: time.Now().Add(wxAppCacheTTL)}
	wxAppMu.Unlock()
	return appID, appSecret
}

// wxAppOf 完整取值链：租户凭据 → 全局设置 → 环境变量。
func wxAppOf(db *gorm.DB, companyID int64, cfg config.Config) wechat.Config {
	appID, appSecret := tenantWXApp(db, companyID)
	if appID == "" || appSecret == "" {
		return wechat.Config{AppID: resolveWXAppID(cfg), AppSecret: resolveWXAppSecret(cfg)}
	}
	return wechat.Config{AppID: appID, AppSecret: appSecret}
}

// ---- staffId → companyID ----
//
// wxacode 是公开接口（访客扫码看名片就要取，不能挂鉴权），所以不能信客户端
// 传的 companyId——伪造它就能让别的企业的码用错 appid 生成。可靠路径是
// staffId → employee.company_id：staffId 是全局唯一主键，伪造不了归属。
//
// 但每次请求回表 employee 会在扫描洪峰时打到主库，所以加一层短缓存。
// 员工归属企业几乎不变；负结果（staffId 不存在 → 0）也缓存，
// 否则枚举 staffId 扫号时会变成「每次 miss 都查一次库」。
var (
	staffCompanyMu    sync.Mutex
	staffCompanyCache = map[int64]staffCompanyEntry{}
)

type staffCompanyEntry struct {
	companyID int64
	expAt     time.Time
}

const staffCompanyTTL = 5 * time.Minute
const staffCompanyCacheMax = 8192

// companyIDOf staffId → 租户。查库失败或 staffId 不存在都返回 0（= 用全局凭据），
// 不阻断出码——码出不来是整条分享链断掉，比用错一次凭据严重得多。
func companyIDOf(db *gorm.DB, staffID int64) int64 {
	if db == nil || staffID <= 0 {
		return 0
	}
	now := time.Now()
	staffCompanyMu.Lock()
	e, ok := staffCompanyCache[staffID]
	staffCompanyMu.Unlock()
	if ok && now.Before(e.expAt) {
		return e.companyID
	}
	var cid int64
	if err := db.Model(&model.Employee{}).Where("staff_id = ?", staffID).
		Select("company_id").Scan(&cid).Error; err != nil {
		cid = 0
	}
	staffCompanyMu.Lock()
	if len(staffCompanyCache) >= staffCompanyCacheMax {
		// 直接清空而不是逐个淘汰：这是纯缓存，重建成本只是一次主键查询
		staffCompanyCache = map[int64]staffCompanyEntry{}
	}
	staffCompanyCache[staffID] = staffCompanyEntry{companyID: cid, expAt: now.Add(staffCompanyTTL)}
	staffCompanyMu.Unlock()
	return cid
}

// wxacodeTTL 小程序码缓存有效期：优先取系统设置 upload/wxacode_ttl_hours，
// 未配置 / 非法时回退 config.WxacodeTTL（环境变量或默认 720h）。
// 让「小程序码缓存时长」从系统设置界面可改，而不再写死在环境变量。
func wxacodeTTL(cfg config.Config) time.Duration {
	h := setting.GetInt("wxacode_ttl_hours", int(cfg.WxacodeTTL.Hours()))
	if h <= 0 {
		h = int(cfg.WxacodeTTL.Hours())
	}
	return time.Duration(h) * time.Hour
}
