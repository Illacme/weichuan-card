// Package demoseed 演示数据包：一套「装上就能看」的完整企业样板数据。
//
// 两个使用方，数据完全同源，避免「演示环境好看、正式环境空白」的割裂：
//   - cmd/devserver（本地联调）：每次启动都写，账号口令固定，方便反复登录；
//   - internal/setup（安装向导）：仅在运维勾选「加载演示企业数据」时写一次，
//     账号口令随机生成并回显——商用版绝不能出现「文档里写明的已知口令」。
//
// 为什么单独成包而不是留在 cmd/devserver：安装向导跑在生产二进制里，
// 而 cmd/* 之间不能互相引用（Go 不允许 import main），种子数据必须下沉到 internal。
//
// 素材（LOGO / 头像 / 分类图标 / 项目封面 / 服务图标）内嵌在 seedassets/ 下，
// 由 gen_seed_assets.py 生成的蓝色主题 PNG 占位图。写进上传目录后以相对路径
// /uploads/<name> 入库，各端按源拼绝对地址（与 storage 包的统一约定一致）。
package demoseed

import (
	"crypto/rand"
	"embed"
	"encoding/base64"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"

	"gorm.io/gorm"

	"weichuan/internal/auth"
	"weichuan/internal/cattemplate"
	"weichuan/internal/model"
	"weichuan/internal/pii"
)

// seedAssetFS 内嵌演示占位素材。仅用于演示；正式运营请在 PC 后台上传真实图片替换。
//
//go:embed seedassets/*.png seedassets/*.jpg
var seedAssetFS embed.FS

// 演示数据固定落在 1 号企业：代码里 defaultCompanyID = 1 是小程序公开接口的
// 缺省租户（见 handler/tenant.go），演示数据必须以它为主体，小程序才无需任何
// 额外参数就能展示。
const (
	DemoCompanyID   int64  = 1
	DemoCompanyName        = "廊坊市朗汇机电设备有限公司"
	DemoShortName          = "朗汇机电"
	DemoLoginCode          = "langhui"
	DemoEmployeePhone      = "13832603521"
	DemoEmployeeName       = "高立新"
	DemoAdminUser          = "langhui"
)

// Options 演示数据的写入参数。
type Options struct {
	// UploadDir 素材落盘目录（本地存储驱动用）。为空表示不写素材文件——
	// 此时库里仍会写入 /uploads/<name> 形式的相对路径，图片需要运维自行补齐。
	UploadDir string
	// CompanyAdminPassword 演示企业管理员的口令。留空则随机生成并在 Result 中回传。
	CompanyAdminPassword string
	// EmployeePassword 演示员工（小程序登录）的口令。留空则随机生成并在 Result 中回传。
	EmployeePassword string
	// Quiet 为真时不打印演示账号日志（安装向导自己会把账号写进安装结果，不必重复刷屏）。
	Quiet bool
}

// Result 演示数据写入结果。账号相关字段仅在「本次新建」时有值：
// 已存在同号账号时不会重置口令，字段留空即表示「沿用原账号」。
type Result struct {
	CompanyID  int64 `json:"companyId"`
	AssetCount int   `json:"assetCount"`
	// Accounts 本次新建的演示账号（用户名/手机号 → 口令）与用途说明，按展示顺序排列。
	Accounts []Account `json:"accounts"`
}

// Account 一个演示登录账号。
type Account struct {
	// Where 登录入口（「PC 后台」/「小程序」），Role 角色说明。
	Where string `json:"where"`
	User  string `json:"user"`
	Pass  string `json:"pass"`
	Role  string `json:"role"`
}

// Load 写入整套演示数据（企业 / 企业信息 / 简介 / 8 分类 / 9 案例 / 4 服务 /
// 首页布局 / 1 员工 / 1 企业管理员）。全部用 FirstOrCreate 语义，重复调用安全。
//
// 返回的 error 是**各步骤错误的合并**：任何一步失败都不中断后续步骤——
// 演示数据的价值在于「尽量多铺」，一处写失败就把库停在半截更难善后。
// 调用方决定严重程度：devserver 只打日志，安装向导把失败降级为 WARN 继续安装。
func Load(db *gorm.DB, opt Options) (Result, error) {
	res := Result{CompanyID: DemoCompanyID}
	if db == nil {
		return res, errors.New("演示数据：数据库连接为空")
	}

	var errs []error
	add := func(what string, err error) {
		if err != nil {
			errs = append(errs, fmt.Errorf("%s：%w", what, err))
		}
	}

	assets, err := WriteAssets(opt.UploadDir)
	res.AssetCount = len(assets)
	if err != nil {
		add("写入演示素材", err)
	}

	// ---- 企业（1 号租户） ----
	// FirstOrCreate 命中存量行时不会更新其它列，故白标码需显式补写（幂等，反复调用安全）：
	// 少了它，「?company=langhui」的品牌回落就失效，而这一点不会报任何错。
	add("创建演示企业", db.Where("id = ?", DemoCompanyID).FirstOrCreate(&model.Company{
		ID: DemoCompanyID, Name: DemoCompanyName, ShortName: DemoShortName,
		Status: "enabled", LoginCode: DemoLoginCode,
	}).Error)
	add("补写白标码", db.Model(&model.Company{}).Where("id = ?", DemoCompanyID).
		Update("login_code", DemoLoginCode).Error)

	// ---- 企业信息 / 简介 ----
	add("创建企业信息", db.Where("company_id = ?", DemoCompanyID).FirstOrCreate(&model.CompanyInfo{
		CompanyID: DemoCompanyID, FullName: DemoCompanyName, ShortName: DemoShortName,
		Address: "廊坊市广阳区紫金城商住楼1-2-1911", Tel: "0316-8023950", Logo: assets["logo.png"],
	}).Error)
	add("创建企业简介", db.Where("company_id = ?", DemoCompanyID).FirstOrCreate(&model.CompanyProfile{
		// 视频号档（跨主体）：用 feed-token；feed-id 留空 → 组件走 channels-token 分支
		// （强制 autoplay=false，用户点一下再播）。
		CompanyID: DemoCompanyID,
		IntroText: "成立于 2015 年 11 月，专注于电梯销售、安装、维修保养与改造服务，" +
			"为住宅、政务中心、医疗、商业、公共交通、生产制造与仓储物流等多场景提供机电一体化解决方案。",
		VideoSource:    model.VideoSourceChannels,
		VideoFeedToken: "AThgKivzk",
		Cover:          assets["cover-company.jpg"],
	}).Error)

	// ---- 经典业绩 8 分类（与设计稿一致，客户已确认锁定） ----
	// 名称与排序取自 cattemplate——新建企业自动复制的是同一份模板，这里只额外补上
	// 演示专用的图标（cat-N.png）与 is_locked=true（保设计稿，演示租户不许删分类）。
	// 两处各写一份名单迟早会漂移：某天演示数据叫「医院、学校」、新企业模板叫「医疗」，
	// 而客户在这两处看到的本该是同一套东西。
	for i, t := range cattemplate.Default() {
		cat := model.AchievementCategory{
			ID:        int64(i + 1),
			CompanyID: DemoCompanyID,
			Name:      t.Name,
			Icon:      assets[fmt.Sprintf("cat-%d.png", t.Sort)],
			Sort:      t.Sort,
			IsLocked:  true,
		}
		add("创建分类 "+cat.Name, db.Where("id = ?", cat.ID).FirstOrCreate(&cat).Error)
	}

	// ---- 项目案例：2 个首页精选（设计稿）+ 其余分类各 1 个（点开任意分类都有内容） ----
	type projectSeed struct {
		id, categoryID int64
		name, summary  string
		detail         string
		cover, album   []string // cover 取第一个；album 为相册素材名列表
		featured       bool
		sort           int
	}
	seeds := []projectSeed{
		{101, 1, "大兴临空服务中心", "临空经济区地标机电总包",
			"<p>大兴临空服务中心项目涵盖多栋客货梯与自动扶梯的供货、安装与维保，满足高客流交通枢纽的可靠运行需求。</p>",
			[]string{"cover-daxing.jpg"}, []string{"album-1.png", "album-2.png"}, true, 1},
		{102, 3, "某医院电梯项目", "医疗楼宇专用电梯解决方案",
			"<p>某医院电梯项目提供医疗专用电梯的定制、安装与运维，兼顾洁净、安静与应急响应。</p>",
			[]string{"cover-hospital.jpg"}, []string{"album-1.png"}, true, 2},
		{103, 1, "某高层住宅电梯项目", "住宅客梯批量交付",
			"<p>为高层住宅提供客梯的批量供货与安装，统一调度的群控方案提升高峰运力。</p>",
			[]string{"cover-p1b.jpg"}, []string{"album-1.png"}, false, 2},
		{104, 2, "市民服务中心电梯工程", "政务大厅无机房客梯",
			"<p>市民服务中心采用无机房客梯，节省机房空间，满足政务服务大厅的无障碍通行需求。</p>",
			[]string{"cover-p2.jpg"}, []string{"album-2.png"}, false, 1},
		{105, 3, "某学校电梯项目", "校园电梯安全改造",
			"<p>针对校园既有电梯的安全改造，升级制动与监控，保障师生乘梯安全。</p>",
			[]string{"cover-p3b.jpg"}, []string{"album-1.png"}, false, 3},
		{106, 4, "商业综合体扶梯项目", "商场自动扶梯群控",
			"<p>商业综合体部署自动扶梯群控系统，结合客流分析优化运行节奏，节能高效。</p>",
			[]string{"cover-p4.jpg"}, []string{"album-2.png"}, false, 1},
		{107, 5, "地铁站点电梯项目", "轨道交通垂直交通",
			"<p>地铁站点垂直交通方案，重载电梯与扶梯满足大客流高频率运行。</p>",
			[]string{"cover-p5.jpg"}, []string{"album-1.png"}, false, 1},
		{108, 7, "智能仓储货梯项目", "物流仓储货梯定制",
			"<p>为智能仓储定制货梯，对接 AGV 与 WMS，实现物料垂直转运自动化。</p>",
			[]string{"cover-p6.jpg"}, []string{"album-2.png"}, false, 1},
		{110, 6, "某生产制造货梯项目", "工厂车间货梯定制",
			"<p>为生产制造车间定制载重货梯，耐粉尘、抗震动，保障产线物料垂直运输稳定可靠。</p>",
			[]string{"cover-p8.jpg"}, []string{"album-1.png"}, false, 1},
		{109, 8, "别墅家用电梯项目", "私人住宅别墅电梯",
			"<p>私人别墅家用电梯，定制轿厢与静音驱动，兼顾美观与乘坐舒适。</p>",
			[]string{"cover-p7.jpg"}, []string{"album-1.png"}, false, 1},
	}
	for _, s := range seeds {
		p := model.ProjectCase{
			ID: s.id, CompanyID: DemoCompanyID, CategoryID: s.categoryID,
			Name: s.name, Summary: s.summary, Detail: s.detail,
			Cover: assets[s.cover[0]], Album: jsonArray(assets, s.album),
			IsHomeFeatured: s.featured, Enabled: true, Sort: s.sort,
		}
		add("创建案例 "+p.Name, db.Where("id = ?", p.ID).FirstOrCreate(&p).Error)
	}

	// ---- 4 个业务服务 ----
	svcs := []model.BizService{
		{ID: 1, CompanyID: DemoCompanyID, Title: "电梯销售", Icon: assets["svc-1.png"], Content: "<p>全系列电梯设备销售与选型咨询，匹配不同楼宇场景需求。</p>", Sort: 1},
		{ID: 2, CompanyID: DemoCompanyID, Title: "电梯安装", Icon: assets["svc-2.png"], Content: "<p>专业安装团队，标准化施工与验收交付，保障工期与质量。</p>", Sort: 2},
		{ID: 3, CompanyID: DemoCompanyID, Title: "电梯维保", Icon: assets["svc-3.png"], Content: "<p>7x24 应急响应与按需定制的保养计划，延长设备寿命。</p>", Sort: 3},
		{ID: 4, CompanyID: DemoCompanyID, Title: "电梯改造", Icon: assets["svc-4.png"], Content: "<p>旧梯升级改造，提升安全性、乘运体验与能效表现。</p>", Sort: 4},
	}
	for _, s := range svcs {
		s := s
		add("创建服务 "+s.Title, db.Where("id = ?", s.ID).FirstOrCreate(&s).Error)
	}

	// ---- 首页布局 ----
	add("创建首页布局", db.Where("company_id = ?", DemoCompanyID).FirstOrCreate(
		&model.HomeLayout{CompanyID: DemoCompanyID, Order: `["achievement","service"]`}).Error)

	// ---- 演示员工（小程序登录） ----
	// 注意：phone_bidx 由 Employee 的 BeforeCreate 钩子按当时的 PII 密钥计算。
	// 安装向导必须在调用本函数**之前**把 PII_ENC_KEY 定下来，否则这条记录的盲索引
	// 会用内置 dev 密钥算出来，重启换真密钥后再也查不到这个员工（登录报账号不存在）。
	var empCount int64
	add("查询演示员工", db.Model(&model.Employee{}).
		Where("phone_bidx = ?", pii.BlindIndex(DemoEmployeePhone)).Count(&empCount).Error)
	if empCount == 0 {
		pw := opt.EmployeePassword
		generated := false
		if pw == "" {
			pw, err = randPassword()
			if err != nil {
				add("生成演示员工口令", err)
			}
			generated = true
		}
		if pw != "" {
			hash, herr := auth.HashPassword(pw)
			if herr != nil {
				add("生成演示员工口令哈希", herr)
			} else {
				emp := model.Employee{
					CompanyID: DemoCompanyID,
					// Phone2（第二个手机号，选填）默认不播种：演示名片只展示主号
					Name: DemoEmployeeName, Title: "业务经理", Phone: DemoEmployeePhone,
					Landline: "0316-8023950", Email: DemoEmployeePhone + "@163.com",
					Avatar: assets["avatar-gaolixin.png"], PasswordHash: hash, Status: "enabled",
				}
				if err := db.Create(&emp).Error; err != nil {
					add("创建演示员工", err)
				} else {
					res.Accounts = append(res.Accounts, Account{
						Where: "小程序", User: DemoEmployeePhone, Pass: pw,
						Role: "员工·" + DemoEmployeeName + "（业务经理）" + hint(generated),
					})
				}
			}
		}
	}

	// ---- 演示企业管理员（PC 后台，与管理员的平台超管不是一套账号） ----
	var admCount int64
	add("查询演示企业管理员", db.Model(&model.Admin{}).
		Where("username = ?", DemoAdminUser).Count(&admCount).Error)
	if admCount == 0 {
		pw := opt.CompanyAdminPassword
		generated := false
		if pw == "" {
			pw, err = randPassword()
			if err != nil {
				add("生成企业管理员口令", err)
			}
			generated = true
		}
		if pw != "" {
			hash, herr := auth.HashPassword(pw)
			if herr != nil {
				add("生成企业管理员口令哈希", herr)
			} else {
				if err := db.Create(&model.Admin{
					CompanyID: DemoCompanyID, Username: DemoAdminUser, PasswordHash: hash,
				}).Error; err != nil {
					add("创建演示企业管理员", err)
				} else {
					res.Accounts = append(res.Accounts, Account{
						Where: "PC 后台", User: DemoAdminUser, Pass: pw,
						Role: "企业管理员·" + DemoShortName + hint(generated),
					})
				}
			}
		}
	}

	if !opt.Quiet {
		for _, a := range res.Accounts {
			log.Printf("[DEMOSEED] 演示账号 %s %s/%s（%s）", a.Where, a.User, a.Pass, a.Role)
		}
		log.Printf("[DEMOSEED] 演示数据已就绪：企业 #%d，素材 %d 个", res.CompanyID, res.AssetCount)
	}
	return res, errors.Join(errs...)
}

// hint 标注口令是随机生成的（避免运维以为是文档里的固定值）。
func hint(generated bool) string {
	if generated {
		return "，本次随机口令"
	}
	return ""
}

// jsonArray 把素材名列表转成 JSON 数组字符串（相册列存的是 URL 数组的文本）。
// 素材缺失时对应元素为空串——宁可留空也不要写坏 JSON，前端 JSON.parse 会整体失败。
func jsonArray(assets map[string]string, names []string) string {
	out := "["
	for i, n := range names {
		if i > 0 {
			out += ","
		}
		out += `"` + assets[n] + `"`
	}
	return out + "]"
}

// randPassword 生成随机口令（base64url，无歧义字符）。
// crypto/rand 失败时返回错误而不是回退常量：演示账号也是真账号，
// 退回可预测口令等于在商用库里留一个后门。
func randPassword() (string, error) {
	b := make([]byte, 12)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// WriteAssets 把内嵌素材写入上传目录，返回「文件名 → 相对 URL」映射。
//
// 反复调用是安全的：文件名固定，覆盖写即可（这些是占位图，无需保留历史版本；
// 真实上传走的对象名是随机的，永不覆盖）。
// 返回相对路径 /uploads/<name>，与各端「按源拼绝对地址」的统一约定一致。
//
// uploadDir 为空时直接返回空映射（调用方明确表示不落素材文件）。
// 单个文件写失败只累计错误、不中断——剩下 26 个能写就写。
func WriteAssets(uploadDir string) (map[string]string, error) {
	urls := map[string]string{}
	if uploadDir == "" {
		return urls, nil
	}
	if err := os.MkdirAll(uploadDir, 0o755); err != nil {
		return urls, fmt.Errorf("创建上传目录 %s：%w", uploadDir, err)
	}
	entries, err := seedAssetFS.ReadDir("seedassets")
	if err != nil {
		return urls, fmt.Errorf("读取内嵌素材：%w", err)
	}
	var errs []error
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		data, err := seedAssetFS.ReadFile("seedassets/" + e.Name())
		if err != nil {
			errs = append(errs, fmt.Errorf("读取 %s：%w", e.Name(), err))
			continue
		}
		if err := os.WriteFile(filepath.Join(uploadDir, e.Name()), data, 0o644); err != nil {
			errs = append(errs, fmt.Errorf("写入 %s：%w", e.Name(), err))
			continue
		}
		urls[e.Name()] = "/uploads/" + e.Name()
	}
	return urls, errors.Join(errs...)
}
