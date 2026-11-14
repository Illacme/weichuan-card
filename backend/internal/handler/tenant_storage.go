package handler

import (
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"weichuan/internal/audit"
	"weichuan/internal/middleware"
	"weichuan/internal/model"
	"weichuan/internal/nginxsnippet"
	"weichuan/internal/pii"
	"weichuan/internal/storage"
	"weichuan/internal/uploadroute"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// 租户自有对象桶的后台配置接口（平台超管专属，设计稿 §1.1 / §3.2）。
//
// 权限模型是用户 2026-11-13 拍板的：「平台超管代填，这种权限肯定不能放给企业管理员的」。
// 理由不只是权限收敛——这条配置里装的是**客户的云凭据**。企业管理员一旦能改，
// 就能把自己企业的对象写到任意桶、把别人的桶指到自己账号下，而平台从业务侧察觉不到。
// 所以四个接口全部挂 PlatformAdminMiddleware，企业管理员连读都不给。
//
// **不要照抄 RegisterStorage**（handler/storage.go:20）：那批预签名/签名/CDN 路由挂的是
// AuthMiddleware(db,"admin")，企业管理员可以过。那批是平台级既有能力，本轮不动；
// 但本文件任何一个接口若挂成 AuthMiddleware，权限模型从第一天就是漏的。
// 这条由 TestTenantStorageRoutesUsePlatformAdminGuard 做源码级守门。
//
// 这一轮做「配置能写进去、能自检、能切回默认」。配置究竟生不生效由进程的
// TENANT_STORAGE_ROUTE 决定（第六十一轮接入）：off 时上传/出参仍走平台那一个实例，
// on 时按 company_id 分流到客户桶。出参里的 trafficEnabled 如实回报这件事，
// 界面据此切换顶部提示——写死 false 会在开关打开后继续骗人。

// tenantStorageCheckLimitPerMin 自检接口每分钟每管理员的上限。
//
// 这是本项目第二个「按管理员输入发起出向请求」的接口（第一个是 url_probe）。
// 与 url_probe 的差别：这里**不套 urlprobe 的私网黑名单**——自建 MinIO 常在内网，
// 套上就等于把主要落地场景禁掉了。风险靠「只有平台超管能调」这一层控制，
// 限流只用来防止误点/脚本放大。
const tenantStorageCheckLimitPerMin = 30

// storageProbeFn 抽成包级变量，测试才能在不联网的前提下覆盖成功/失败分支
// （与 urlProbeFn 同一套路）。生产代码不覆盖它。
var storageProbeFn = storage.ProbeBucket

// tenantStorageCheckLimitFn 同 urlProbeLimitFn：测试里连续多打几次不该撞 429。
var tenantStorageCheckLimitFn = func() int { return tenantStorageCheckLimitPerMin }

// RegisterTenantStorage 挂载租户自有存储配置的读写接口。
//
//	GET    /api/admin/companies/:id/storage       读（凭据只回掩码）
//	PUT    /api/admin/companies/:id/storage       新建 / 覆盖（凭据加密落库）
//	DELETE /api/admin/companies/:id/storage       切回平台存储（删行）
//	POST   /api/admin/companies/:id/storage/check 连通性自检（回写 status）
func RegisterTenantStorage(r *gin.Engine, db *gorm.DB) {
	plat := middleware.PlatformAdminMiddleware(db)

	r.GET("/api/admin/companies/:id/storage", plat, func(c *gin.Context) {
		cid, ok := storageTargetCompanyID(c)
		if !ok {
			return
		}
		row, err := loadTenantStorage(db, cid)
		if err != nil {
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "读取存储配置失败"})
			return
		}
		if row == nil {
			// 没配过不是错误：这家企业走平台存储。回空视图让界面直接进入「新建」态，
			// 前端不必靠 404 或 error 来区分「没配」和「读失败」。
			c.JSON(http.StatusOK, gin.H{"code": 0, "data": emptyTenantStorageView(cid)})
			return
		}
		c.JSON(http.StatusOK, gin.H{"code": 0, "data": tenantStorageView(row)})
	})

	r.PUT("/api/admin/companies/:id/storage", plat, func(c *gin.Context) {
		if writeLocked(c) {
			return
		}
		cid, ok := storageTargetCompanyID(c)
		if !ok {
			return
		}
		var req tenantStorageReq
		if c.ShouldBindJSON(&req) != nil {
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "参数错误"})
			return
		}
		// 企业必须真实存在：配置行以 company_id 为唯一键，配给一个不存在的 id
		// 只会让生成器产出一段永远匹配不到请求的 location。
		var company model.Company
		if err := db.Select("id").Where("id = ?", cid).First(&company).Error; err != nil {
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "企业不存在"})
			return
		}

		row, err := loadTenantStorage(db, cid)
		if err != nil {
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "读取存储配置失败"})
			return
		}
		if row == nil {
			row = &model.TenantStorageConfig{
				CompanyID: cid,
				// 新建时的缺省：公开读（第一阶段试点口径）、未启用（填完先自检再开）、
				// 走 HTTPS（云端桶几乎都只开 443；填错的代价是多一次自检失败，
				// 而缺省成 http 会让「明明配对了却报 SSL 错」这种反向困惑出现）。
				PublicRead: true,
				Enabled:    false,
				UseSSL:     true,
				Status:     storageStatusUnknown,
			}
		}

		// 逐字段应用。布尔与凭据用指针区分「没传」与「传了零值」——
		// 与 company_wx.go 的 ReleaseEnabled 同一个理由：界面只改一项时，
		// 不带其他字段不能等于把它们重置掉。
		row.Driver = strings.TrimSpace(req.Driver)
		row.Endpoint = strings.TrimSpace(req.Endpoint)
		row.Bucket = strings.TrimSpace(req.Bucket)
		row.Region = strings.TrimSpace(req.Region)
		row.HostHeader = strings.TrimSpace(req.HostHeader)
		if req.UseSSL != nil {
			row.UseSSL = *req.UseSSL
		}
		if req.PublicRead != nil {
			row.PublicRead = *req.PublicRead
		}
		if req.Enabled != nil {
			row.Enabled = *req.Enabled
		}
		// 自有访问域名同样走三态。格式裁判只在 storage.ValidatePublicOrigin 一处：
		// 它与 ResolveRef(raw, origin) 的 origin 校验是同一个函数，避免「后台存成功、
		// 端上拼出带路径的怪地址」这种不报错的漂移。
		if req.PublicBaseURL != nil {
			base, err := storage.ValidatePublicOrigin(*req.PublicBaseURL)
			if err != nil {
				c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "自有访问域名不合法：" + err.Error()})
				return
			}
			row.PublicBaseURL = base
		}

		// 凭据三态：nil=保留、空串=清空、有值=覆盖（pii.Encrypt 落密文）。
		credentialsChanged := false
		if req.AccessKey != nil {
			enc, err := pii.Encrypt(strings.TrimSpace(*req.AccessKey))
			if err != nil {
				c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "AccessKey 加密失败：PII_ENC_KEY 未正确配置"})
				return
			}
			row.AccessKeyEnc = enc
			credentialsChanged = true
		}
		if req.SecretKey != nil {
			enc, err := pii.Encrypt(strings.TrimSpace(*req.SecretKey))
			if err != nil {
				c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "SecretKey 加密失败：PII_ENC_KEY 未正确配置"})
				return
			}
			row.SecretKeyEnc = enc
			credentialsChanged = true
		}

		if msg := validateTenantStorageRow(row); msg != "" {
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": msg})
			return
		}

		if err := db.Save(row).Error; err != nil {
			// 这里不能用 dbWriteFailed：它只打日志不回话，而本接口要告诉界面「没存上」。
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "保存失败"})
			return
		}
		// 配置改了，之前那次自检的结论就不再代表当前配置。
		// 若不改回 unknown，「改完地址但没重新自检」的后台会一直显示上一次的绿点，
		// 读的人会以为新配置已经验过了。
		if err := db.Model(&model.TenantStorageConfig{}).
			Where("company_id = ?", cid).
			Updates(map[string]any{"status": storageStatusUnknown, "last_error": ""}).Error; err != nil {
			// 回写自检状态失败不阻断保存：配置本身已经落库，最坏是显示一个过期的状态点。
			// 但必须打日志——「状态点不可信」这件事没人会自己发现。
			log.Printf("[STORAGE][WARN] 企业 %d 的存储配置已保存，但自检状态重置失败：%v", cid, err)
		}

		audit.Record(db, c, audit.Entry{
			Action:     audit.ActionTenantStorageUpdate,
			TargetType: "company_storage",
			TargetID:   strconv.FormatInt(cid, 10),
			Detail: map[string]any{
				"driver":     row.Driver,
				"endpoint":   row.Endpoint,
				"bucket":     row.Bucket,
				"enabled":    row.Enabled,
				"publicRead": row.PublicRead,
				// 自有访问域名不是秘密（它本来就是要公开的），且它决定「出参 URL
				// 打算指到哪」。审计里要能回答「谁把哪家企业的访问域名改成了什么」。
				"publicBaseUrl": row.PublicBaseURL,
				// 只记「凭据有没有被改」，不记值也不记掩码：掩码仍是可关联的碎片。
				"credentialsChanged": credentialsChanged,
				"credentialsPresent": row.AccessKeyEnc != "" && row.SecretKeyEnc != "",
			},
		})

		saved, err := loadTenantStorage(db, cid)
		if err != nil || saved == nil {
			// 落库成功了但回读失败：回一个最小视图而不是报错，避免「其实存上了却提示失败」
			// 导致管理员重复提交。
			c.JSON(http.StatusOK, gin.H{"code": 0, "msg": "ok", "data": emptyTenantStorageView(cid)})
			return
		}
		c.JSON(http.StatusOK, gin.H{"code": 0, "msg": "ok", "data": tenantStorageView(saved)})
	})

	// DELETE /storage = 解约 / 撤掉自有桶配置（不可逆）。
	//
	// 保守保留政策（第七十轮，契合 R5 与 audit.purge 的「默认不删」哲学）：
	//   - 删的只有 tenant_storage_configs 这一行；
	//   - 凭据密文（AccessKeyEnc/SecretKeyEnc）只落在这张表，删行即随行清除，
	//     不单独再写一次「清凭据」；
	//   - 以下三类**一律不碰**，对账时要能解释这个「裂缝」：
	//       1) 客户桶里的对象——平台不持有删桶权限，也不主动主张删；
	//       2) 回填台账 storage_backfill_refs——按 company_id 溯源留痕，R5 兜底；
	//       3) 业务表 t/<cid>/ 引用（refmigrate.Inventory 的 14 列）——配置删后
	//          新上传走平台 origin 回落，存量对象仍在客户桶不丢（R5）。
	//   企业 disabled 是软状态、可逆，绝不在此级联触发——禁用与重置是两件独立的事。
	//   这条「只删配置行」的边界由 tenant_storage_guard_test.go 的源码级守门钉死，
	//   并由 tenant_storage_test.go 的运行时用例断言三张表都不动。
	r.DELETE("/api/admin/companies/:id/storage", plat, func(c *gin.Context) {
		if writeLocked(c) {
			return
		}
		cid, ok := storageTargetCompanyID(c)
		if !ok {
			return
		}
		row, err := loadTenantStorage(db, cid)
		if err != nil {
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "读取存储配置失败"})
			return
		}
		if row == nil {
			// 幂等：没配过就是「已经是平台存储」，不报错。
			c.JSON(http.StatusOK, gin.H{"code": 0, "msg": "ok"})
			return
		}
		if err := db.Where("company_id = ?", cid).Delete(&model.TenantStorageConfig{}).Error; err != nil {
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "删除失败"})
			return
		}
		audit.Record(db, c, audit.Entry{
			Action:     audit.ActionTenantStorageReset,
			TargetType: "company_storage",
			TargetID:   strconv.FormatInt(cid, 10),
			Detail: map[string]any{
				"driver":   row.Driver,
				"endpoint": row.Endpoint,
				"bucket":   row.Bucket,
				"enabled":  row.Enabled,
				// 明确记下「删了什么 / 留了什么」，审计员一眼能区分
				// 「保守保留式删除」与「连带清理式删除」——后者绝不能在此发生。
				"deleted": "tenant_storage_configs",
				// 切回平台盘只改新上传的去向，存量对象不会被搬走。
				"objectsLeftInBucket": true,
				// 注：retained 用语义名而非表名，避免与「源码级守门」的表名清单互相干扰；
				// 语义依次为：客户桶对象 / 回填台账 / 业务表引用。
				"retained": []string{
					"customer_bucket_objects",
					"backfill_ledger",
					"business_references",
				},
			},
		})
		c.JSON(http.StatusOK, gin.H{"code": 0, "msg": "ok"})
	})

	limiter := middleware.NewRateLimiter(
		func() int { return tenantStorageCheckLimitFn() }, tenantStorageCheckLimitPerMin, time.Minute,
	).
		WithKey(func(c *gin.Context) string {
			return "tenantstorage:" + strconv.FormatInt(c.GetInt64("staffId"), 10)
		}).
		WithMessage("自检过于频繁，请稍后再试").
		WithCounter(dbRateCounter{db: db})

	// 顺序：plat 先把 staffId 写进上下文，限流分桶才拿得到它（同 url_probe）。
	r.POST("/api/admin/companies/:id/storage/check", plat, limiter.Handler(), func(c *gin.Context) {
		cid, ok := storageTargetCompanyID(c)
		if !ok {
			return
		}
		row, err := loadTenantStorage(db, cid)
		if err != nil {
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "读取存储配置失败"})
			return
		}
		if row == nil {
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "该企业尚未配置自有存储，先保存配置再自检"})
			return
		}

		detail, probeErr := probeTenantStorage(row)
		status, lastErr := storageStatusOK, ""
		if probeErr != "" {
			status, lastErr = storageStatusError, probeErr
		}
		now := time.Now()
		if err := db.Model(&model.TenantStorageConfig{}).
			Where("company_id = ?", cid).
			Updates(map[string]any{
				"status":          status,
				"last_checked_at": now,
				"last_error":      lastErr,
			}).Error; err != nil {
			// 回写失败要明确告诉界面：本次探测结果是真的，但它没被记住。
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "自检完成，但结果写入失败，请重试"})
			return
		}

		entry := audit.Entry{
			Action: audit.ActionTenantStorageCheck, TargetType: "company_storage",
			TargetID: strconv.FormatInt(cid, 10),
			Detail: map[string]any{
				"ok":       probeErr == "",
				"driver":   row.Driver,
				"endpoint": row.Endpoint,
				"bucket":   row.Bucket,
			},
		}
		if probeErr != "" {
			entry.Result = audit.ResultFail
			entry.Detail["error"] = probeErr
		}
		audit.Record(db, c, entry)

		c.JSON(http.StatusOK, gin.H{"code": 0, "data": gin.H{
			"ok":            probeErr == "",
			"detail":        detail,
			"error":         lastErr,
			"status":        status,
			"lastCheckedAt": now,
			// 说清这次自检验了什么、没验什么。自检过了不等于接入层配好了：
			// nginx 的 Host/SNI（R1 的 421）、CDN 缓存、证书链都不在覆盖范围。
			"checkedScope": "凭据有效性与桶可达性；不含 nginx 侧 Host/SNI、CDN 与证书链",
		}})
	})
}

// 存储配置的三种自检状态。
//
// 用「unknown」而不是空串作为初始值：后台列表要靠它区分「配好了没验」与
// 「验过了、是好的」。空串在这套 UI 里长得和「没有这个概念」一样，
// 读的人无法判断是该点自检还是该改配置。
const (
	storageStatusUnknown = "unknown"
	storageStatusOK      = "ok"
	storageStatusError   = "error"
)

// tenantStorageReq 是 PUT 的入参。
//
// 布尔与凭据一律用指针：nil = 本次请求没提这一项（保留原值）。这与
// company_wx.go 的 ReleaseEnabled 同一条纪律——界面只改 bucket 时不带
// enabled，不能等于把企业悄悄停用。
type tenantStorageReq struct {
	Driver     string `json:"driver"`
	Endpoint   string `json:"endpoint"`
	Bucket     string `json:"bucket"`
	Region     string `json:"region"`
	UseSSL     *bool  `json:"useSsl"`
	HostHeader string `json:"hostHeader"`
	PublicRead *bool  `json:"publicRead"`
	Enabled    *bool  `json:"enabled"`
	// PublicBaseURL 客户自有访问域名。三态与凭据一致：nil = 保留原值、
	// "" = 清空、有值 = 按 storage.ValidatePublicOrigin 归一化后覆盖。
	//
	// 第六十九轮起它**真的会承载流量**：出参侧按租户选 origin 已接入，
	// 但生效还要同时满足「对象真的写进了客户桶」（TENANT_STORAGE_ROUTE /
	// enabled / driver / 凭据 / publicRead）；出参里的 publicBaseUrlActive
	// 由 publicOriginState 实况回报，填了不等于立刻切换，界面必须照实渲染。
	PublicBaseURL *string `json:"publicBaseUrl"`
	// AccessKey / SecretKey：nil = 保留原值（界面显示掩码时点保存走这条），
	// "" = 清空，其他 = 覆盖。**不接受**已加密的值——加密只发生在服务端，
	// 允许客户端提交密文等于允许它把别的行密文复制过来。
	AccessKey *string `json:"accessKey"`
	SecretKey *string `json:"secretKey"`
}

// storageTargetCompanyID 解析并校验路径里的企业 id。
//
// 0 不是合法值：0 在系统里表示「平台自身」，平台那份存储走 sys_setting 的全局配置，
// 不该在这张表里出现一行。放行 0 会让生成器跳过它（NeedsSnippet 里 driver 不是
// local 就生成），于是拼出一段 cid=0 的 location——匹配不到任何 t/0/ 请求，
// 但 nginx -t 是通过的，属于「配了但没用」的静默失败。
func storageTargetCompanyID(c *gin.Context) (int64, bool) {
	cid, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || cid <= 0 {
		c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "企业 id 不合法"})
		return 0, false
	}
	return cid, true
}

// loadTenantStorage 读一行配置；没配过返回 (nil, nil)。
//
// 「没配过」与「读失败」必须分开：前者是正常状态（走平台存储），后者要报错。
// 合成一个 nil 会让「库挂了」被当成「这家企业没配」，界面显示一切正常而配置其实读不到。
//
// 用 Limit(1).Find 而不是 First：第六十九轮起**每条公开 GET 都会调一次**（按租户取
// 出参 origin），「没配过」是绝大多数企业的常态；First 查不到行会打一条 gorm
// "record not found" 错误日志，等于每个访客请求刷屏。三态语义不变，只是别把
// 正常状态当错误打日志。
func loadTenantStorage(db *gorm.DB, cid int64) (*model.TenantStorageConfig, error) {
	var row model.TenantStorageConfig
	res := db.Where("company_id = ?", cid).Limit(1).Find(&row)
	if res.Error != nil {
		return nil, res.Error
	}
	if res.RowsAffected == 0 {
		return nil, nil
	}
	return &row, nil
}

// validateTenantStorageRow 校验即将落库的配置，返回给运营看的一句话（空串=通过）。
//
// 校验规则**不在这里重写**：endpoint 不许带 scheme、bucket 不许含路径与 ..、
// hostHeader 不许含引号，全部复用 nginxsnippet.Target.Validate 单点。
// 那份规则每一条都对应一种「配置看起来合法、实际会出事」，在写入侧复制一遍
// 必然与生成侧漂移——而漂移的表现是「接口说存成功了，生成器却报错」。
//
// 这里另外加两条，都是生成器没资格判的**产品/出参边界**：
//   - 第一阶段不支持私有桶（私有桶的 snippet 也是对的，但本期没做预签名链路）；
//   - 客户自有访问域名若已有值，必须由 storage.ValidatePublicOrigin 认过。
//     后者同时也兜住「早先手工 SQL 写进来的坏值」：一次只改 bucket 的保存
//     会把它一并挡住，而不是让坏域名继续躺在库里等翻开关那天炸。
func validateTenantStorageRow(row *model.TenantStorageConfig) string {
	if _, err := storage.ValidatePublicOrigin(row.PublicBaseURL); err != nil {
		return "自有访问域名不合法：" + err.Error()
	}
	target := nginxsnippet.Target{
		CompanyID:  row.CompanyID,
		Driver:     row.Driver,
		Endpoint:   row.Endpoint,
		Bucket:     row.Bucket,
		UseSSL:     row.UseSSL,
		HostHeader: row.HostHeader,
		Enabled:    row.Enabled,
	}
	// Target.Validate 对 NeedsSnippet()=false 的行直接放行——这是对的：
	// 未启用或 driver=local 的草稿配置不该被桶地址的格式卡住。
	if err := target.Validate(); err != nil {
		return "配置不合法：" + err.Error()
	}
	if !target.NeedsSnippet() {
		return ""
	}
	if !row.PublicRead {
		// 私有桶要走预签名，而出参契约（存裸键还是存 URL）会跟着变，
		// 本期没有这条链路。放行会得到一个「配置成功、上线图全裂」的结果。
		return "第一阶段只支持公开读桶（publicRead=true）：私有桶需要预签名链路，尚未实现"
	}
	if row.AccessKeyEnc == "" || row.SecretKeyEnc == "" {
		// 启用一个没有凭据的桶，后果是上传时签名失败——而 P2 之前上传根本不看这份配置，
		// 所以这个错误会一直躺到切量那天。在写入侧就拦住。
		return "启用自有存储必须填写 AccessKey 与 SecretKey"
	}
	return ""
}

// probeTenantStorage 执行一次自检，返回 (成功时的说明, 失败原因)。
//
// 显式一进一出两个字符串而不是 error：调用方要的是「写进 last_error 的那句话」，
// 而 error 的包装链对运营没有意义，反而容易把内部类型名带进界面。
func probeTenantStorage(row *model.TenantStorageConfig) (detail, failure string) {
	ak, err := pii.Decrypt(row.AccessKeyEnc)
	if err != nil {
		// 解密失败几乎只有一种成因：PII_ENC_KEY 与写入时不是同一把。
		// 这条必须单独说，否则运维会去查桶地址——配置本身没问题，是钥匙换了。
		return "", "凭据无法解密（PII_ENC_KEY 与写入时不一致），需重新填一次密钥"
	}
	sk, err := pii.Decrypt(row.SecretKeyEnc)
	if err != nil {
		return "", "凭据无法解密（PII_ENC_KEY 与写入时不一致），需重新填一次密钥"
	}
	detail, err = storageProbeFn(row.Driver, row.Endpoint, ak, sk, row.Bucket, row.Region, row.UseSSL)
	if err != nil {
		return detail, err.Error()
	}
	return detail, ""
}

// tenantStorageView 出参视图。
//
// 硬要求：**AccessKey / SecretKey 永不出参**。AccessKey 回掩码是为了让运维核对
// 「现在生效的是不是新那把」——这是这张表最常见的失败模式（改了但没生效 / 改错了行），
// 只回「已配置」无法回答它。SecretKey 连掩码都不给：它没有任何核对价值，
// 一旦泄漏等于把桶交出去。
func tenantStorageView(row *model.TenantStorageConfig) gin.H {
	view := emptyTenantStorageView(row.CompanyID)
	view["configured"] = true
	view["driver"] = row.Driver
	view["endpoint"] = row.Endpoint
	view["bucket"] = row.Bucket
	view["region"] = row.Region
	view["useSsl"] = row.UseSSL
	view["hostHeader"] = row.HostHeader
	view["publicRead"] = row.PublicRead
	view["enabled"] = row.Enabled
	// 自有访问域名如实回报三件事：登记了什么、到底生效没有、生效/未生效各自该做什么。
	// active 与 9 条公开 GET 的 origin 同出 publicOriginState，不会出现「界面绿勾、
	// 访客裂图」。库里若被手工塞了坏域名，这里仍回显原值并给出不合法原因，
	// 便于超管照着改，而不是悄悄显示成空串。
	_, active, reason := publicOriginState(row)
	view["publicBaseUrl"] = row.PublicBaseURL
	view["publicBaseUrlActive"] = active
	// note 必须用**登记值**拼，不能用 publicOriginState 的 base：未生效时那个
	// base 为空串（生效的 origin 才能进出参），拿它去拼会得到整段空文案，
	// 超管反而看不到「已登记了哪个域名、为什么没生效」。
	view["publicBaseUrlNote"] = publicBaseURLNote(row.PublicBaseURL, active, reason)
	view["status"] = row.Status
	view["lastError"] = row.LastError
	view["lastCheckedAt"] = row.LastCheckedAt
	view["updatedAt"] = row.UpdatedAt
	view["accessKeyConfigured"] = row.AccessKeyEnc != ""
	view["secretKeyConfigured"] = row.SecretKeyEnc != ""
	if row.AccessKeyEnc != "" {
		if ak, err := pii.Decrypt(row.AccessKeyEnc); err == nil {
			view["accessKeyMasked"] = maskCredential(ak)
		} else {
			// 解密不出来也要如实说：否则界面会显示一个空掩码，看起来像「没配」。
			view["credentialError"] = "凭据无法解密（PII_ENC_KEY 与写入时不一致），需重新填一次密钥"
		}
	}
	// 这份配置到底生不生效，如实告诉界面。
	// 第六十一轮起不再是硬编码 false：路由由 TENANT_STORAGE_ROUTE 控制，
	// 把这句写死会在开关打开后继续骗人说「尚未承载流量」——
	// 而「填了以为没生效」和「以为生效其实没生效」是同一个错误的两个方向。
	view["trafficEnabled"] = tenantStorageTrafficEnabled()
	// 「这家此刻到底在不在用自有桶」与上面那个进程级开关**不是一回事**：
	// 开关开着但这家 enabled=false，它照样写平台存储。两个字段都要给，
	// 否则界面只能二选一地撒谎。inactiveReason 是给超管看的下一步动作。
	view["activeForTenant"], view["inactiveReason"] = tenantStorageActive(row)
	return view
}

// tenantStorageActive 判断「该企业此刻的文件是不是真的写进自己的桶」，并给出未生效的原因。
//
// 为什么不能只回一个 bool：未生效有五种成因，处置方式完全不同——
// 「没配」要去填、「没启用」是设计如此、「驱动是 local」要去改驱动、「进程没开路由」
// 是部署决策、「凭据不完整」是上传会直接失败（最急）。只回 false 时超管只能猜。
//
// 判定顺序刻意与「谁会先挡住上传」一致：先看路由开关（关着的话别的都不重要），
// 再看这家自己的开关，最后才是凭据（凭据坏了在开关关着时不构成问题）。
func tenantStorageActive(row *model.TenantStorageConfig) (bool, string) {
	if row == nil {
		return false, "尚未配置该企业的自有存储"
	}
	if !tenantStorageTrafficEnabled() {
		return false, "本进程未开启租户存储路由（TENANT_STORAGE_ROUTE=off）：该企业仍写入平台存储"
	}
	if !row.Enabled {
		return false, "配置未启用：该企业仍写入平台存储"
	}
	if row.Driver == nginxsnippet.DriverLocal {
		return false, "驱动为本地磁盘：该企业仍写入平台存储（切到 minio/oss/s3 才会用自有桶）"
	}
	if row.AccessKeyEnc == "" || row.SecretKeyEnc == "" {
		// 这一条不是「未生效」，是「生效了但会失败」——必须排在最后并说清后果。
		return false, "凭据不完整：该企业的上传会直接失败，请补齐 AccessKey / SecretKey"
	}
	return true, ""
}

// publicBaseUrlActive / publicBaseUrlNote 的判定已移到 publicorigin.go 的
// publicOriginState（第六十九轮）。
//
// 第六十八轮这里曾是 `const publicBaseURLActive = false` 并配一段「出参侧尚未接入」
// 的固定文案。出参侧真的接上之后，继续写死 false 与继续写「尚未接入」是同一个
// 错误的两个方向：都会让超管对「客户域名到底切没切」判断错。
// 现在 active 与 9 条公开 GET 的 origin 取自同一个函数，结构上不可能漂移；
// TENANT_STORAGE_ROUTE 仍 off 时它会如实回报 false，不需要在这里写死。

// emptyTenantStorageView 是「没配过」时的视图：字段齐全但值全是空的。
//
// 不返回 null：前端拿到 null 要在每个字段上判空，而「没配」与「配了空值」
// 在界面上应该是同一套渲染（都是空表单）。
func emptyTenantStorageView(cid int64) gin.H {
	return gin.H{
		"companyId":     cid,
		"configured":    false,
		"driver":        nginxsnippet.DriverLocal,
		"endpoint":      "",
		"bucket":        "",
		"region":        "",
		"useSsl":        true,
		"hostHeader":    "",
		"publicRead":    true,
		"enabled":       false,
		"publicBaseUrl": "",
		// 没配过就一定不生效：这里写死 false 是「无行」这一态的定义，
		// 与有行时由 publicOriginState 实况计算不冲突。
		"publicBaseUrlActive": false,
		"publicBaseUrlNote":   "",
		"status":              storageStatusUnknown,
		"lastError":           "",
		"lastCheckedAt":       nil,
		"updatedAt":           nil,
		"accessKeyConfigured": false,
		"secretKeyConfigured": false,
		"accessKeyMasked":     "",
		"credentialError":     "",
		"trafficEnabled":      tenantStorageTrafficEnabled(),
		// 没配过就一定不生效，理由也唯一。这两个字段在 tenantStorageView 里会被重算，
		// 放在这里是为了让「没配过」与「配了」走同一个渲染路径（前端少一个分支）。
		"activeForTenant": false,
		"inactiveReason":  "尚未配置该企业的自有存储",
	}
}

// tenantStorageTrafficEnabled 回报「本进程是否已打开租户存储路由」。
//
// 单独抽出来是为了两个出参走同一个判定（有行 / 没行）：两处各写一次，
// 迟早出现「没配过的企业显示未生效、配过的显示已生效」这种自相矛盾。
func tenantStorageTrafficEnabled() bool { return uploadroute.RouteEnabled() }

// maskCredential 把 AccessKey 打码成「前4****后4」。
//
// 对长度不足的极短值整串打码：AccessKey 至少 16 位，短到 8 位以内的一定是填错了，
// 这时候露前 4 后 4 等于把整个值露出来。
func maskCredential(s string) string {
	r := []rune(strings.TrimSpace(s))
	if len(r) == 0 {
		return ""
	}
	if len(r) <= 8 {
		return "******"
	}
	return string(r[:4]) + "****" + string(r[len(r)-4:])
}
