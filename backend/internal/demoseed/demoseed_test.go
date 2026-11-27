package demoseed

import (
	"bytes"
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"

	"weichuan/internal/auth"
	"weichuan/internal/model"
	"weichuan/internal/pii"
)

// openDB 建一个干净的 SQLite 库并建表（与安装向导的 SQLite 分支同一路径）。
func openDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "demo.db")), &gorm.Config{})
	if err != nil {
		t.Fatalf("打开测试库失败：%v", err)
	}
	if err := db.AutoMigrate(model.All()...); err != nil {
		t.Fatalf("建表失败：%v", err)
	}
	return db
}

func count(t *testing.T, db *gorm.DB, m any) int64 {
	t.Helper()
	var n int64
	if err := db.Model(m).Count(&n).Error; err != nil {
		t.Fatalf("统计失败：%v", err)
	}
	return n
}

// 一套演示数据必须真的落齐：企业 / 分类 / 案例 / 服务 / 首页布局 / 员工 / 企业管理员，
// 缺任何一类都会让「勾了演示数据却只看到半套」变成线上问题。
func TestLoadWritesFullPackage(t *testing.T) {
	db := openDB(t)
	upDir := t.TempDir()

	res, err := Load(db, Options{UploadDir: upDir, Quiet: true})
	if err != nil {
		t.Fatalf("写入演示数据失败：%v", err)
	}

	if res.CompanyID != DemoCompanyID {
		t.Fatalf("演示数据必须落在 1 号企业（小程序公开接口的缺省租户），got %d", res.CompanyID)
	}
	// 素材：27 个内嵌原图全部落盘，返回的 URL 是相对路径（各端按源拼绝对）。
	// 每个 jpg/jpeg/png 还会额外生成 _thumb 兄弟图，故目录文件数 > 原图数。
	if res.AssetCount != 27 {
		t.Fatalf("应写入 27 个素材，实际 %d", res.AssetCount)
	}
	entries, err := os.ReadDir(upDir)
	if err != nil {
		t.Fatalf("读取上传目录失败：%v", err)
	}
	if len(entries) <= 27 {
		t.Fatalf("缩略图应已随种子生成，目录文件数应 > 27，实际 %d", len(entries))
	}
	// 精选封面与分类图标的 _thumb 必须真的存在：端上列表先请求缩略图，
	// 缺它会先吃一次 404 再靠 binderror 回退，回退链路一坏就整块空白。
	for _, name := range []string{
		"cover-daxing_thumb.jpg", "cover-hospital_thumb.jpg",
		"cat-1_thumb.png", "svc-1_thumb.png",
	} {
		info, err := os.Stat(filepath.Join(upDir, name))
		if err != nil || info.Size() == 0 {
			t.Errorf("种子缩略图 %s 缺失或为空：%v", name, err)
		}
	}

	want := []struct {
		name string
		m    any
		n    int64
	}{
		{"企业", &model.Company{}, 1},
		{"企业信息", &model.CompanyInfo{}, 1},
		{"企业简介", &model.CompanyProfile{}, 1},
		{"业绩分类", &model.AchievementCategory{}, 8},
		{"项目案例", &model.ProjectCase{}, 10},
		{"业务服务", &model.BizService{}, 4},
		{"首页布局", &model.HomeLayout{}, 1},
		{"员工", &model.Employee{}, 1},
		{"企业管理员", &model.Admin{}, 1},
	}
	for _, w := range want {
		if got := count(t, db, w.m); got != w.n {
			t.Errorf("%s 应有 %d 条，实际 %d", w.name, w.n, got)
		}
	}

	// 企业必须带白标码：少了它「?company=langhui」的品牌回落静默失效
	var comp model.Company
	if err := db.Where("id = ?", DemoCompanyID).First(&comp).Error; err != nil {
		t.Fatalf("读取企业失败：%v", err)
	}
	if comp.LoginCode != DemoLoginCode || comp.Status != "enabled" {
		t.Fatalf("企业白标码/状态不对：%+v", comp)
	}

	// 图片列必须指回真实素材（相对路径），空串意味着前端一片裂图
	var p model.ProjectCase
	if err := db.Where("id = ?", 101).First(&p).Error; err != nil {
		t.Fatalf("读取案例失败：%v", err)
	}
	if !strings.HasPrefix(p.Cover, "/uploads/") || !strings.Contains(p.Album, "/uploads/") {
		t.Fatalf("案例封面/相册应是 /uploads 相对路径：cover=%q album=%q", p.Cover, p.Album)
	}
	if !p.IsHomeFeatured {
		t.Fatal("101 号案例是首页精选，应 IsHomeFeatured=true")
	}
}

// 演示稿的呈现参数是客户逐项确认过的，不是「随便给个值」：
// 简介缩进 / 视频号三份凭证 / 小字 + 单图横滚 + 宝蓝 80% 浓度。
// 少一项真机就不会长成验收稿的样子，而且不会报任何错。
func TestLoadDemoPresentationDefaults(t *testing.T) {
	db := openDB(t)
	upDir := t.TempDir()
	if _, err := Load(db, Options{UploadDir: upDir, Quiet: true}); err != nil {
		t.Fatalf("写入演示数据失败：%v", err)
	}

	var cp model.CompanyProfile
	if err := db.Where("company_id = ?", DemoCompanyID).First(&cp).Error; err != nil {
		t.Fatalf("读取企业简介失败：%v", err)
	}
	if !strings.HasPrefix(cp.IntroText, "\u3000\u3000") {
		t.Errorf("简介开头应有两个全角空格，got %q", cp.IntroText[:min(12, len(cp.IntroText))])
	}
	if cp.VideoChannelID != "sphKWMbPjskCCKz" ||
		cp.VideoFeedID != "export/UzFfBgAAxK-rDC5YAQKwk8zT4DCaMqbNxBuNPlRLSP0n3Z20VQ" ||
		cp.VideoFeedToken != "token/A235iUAnf" {
		t.Errorf("视频号三份凭证不是验收稿的值：%+v", cp)
	}

	if cp.IntroFontScale != model.IntroFontSmall {
		t.Errorf("简介字号档位应为 small，got %q", cp.IntroFontScale)
	}

	var hl model.HomeLayout
	if err := db.Where("company_id = ?", DemoCompanyID).First(&hl).Error; err != nil {
		t.Fatalf("读取首页布局失败：%v", err)
	}
	if hl.FeaturedCount != 1 || hl.FeaturedEffect != model.FeaturedEffectScroll ||
		hl.BgGradientStrength != 80 {
		t.Errorf("首页布局呈现参数不对：%+v", hl)
	}
	// 演示初始值：精选案例 16:9 裁切 + 显示名称，让开发联调 / 验收时开箱就见效果。
	// 注意这**不是**全局缺省（见 model.DefaultFeaturedRatio = none）——别在这里改全局。
	if hl.FeaturedRatio != model.FeaturedRatio16x9 {
		t.Errorf("演示精选裁切比应为 16:9，got %q", hl.FeaturedRatio)
	}
	if !hl.ShowFeaturedName {
		t.Errorf("演示精选案例名称应默认显示")
	}
	if hl.BrandColorID == nil || *hl.BrandColorID != "brand-royal" {
		t.Errorf("品牌配色应为宝蓝 brand-royal，got %v", hl.BrandColorID)
	}

	var emp model.Employee
	if err := db.Where("company_id = ?", DemoCompanyID).First(&emp).Error; err != nil {
		t.Fatalf("读取演示员工失败：%v", err)
	}
	if emp.Avatar != "/uploads/avatar-gaolixin.png" {
		t.Errorf("演示员工头像路径不对：%q", emp.Avatar)
	}
	if _, err := os.Stat(filepath.Join(upDir, "avatar-gaolixin_thumb.png")); err != nil {
		t.Errorf("演示头像缩略图缺失：%v", err)
	}
}

// 演示账号：口令不能写死（商用版装出来的库不能有「文档里写明的已知口令」），
// 必须随机生成、能真的登录进去，并且库里存的是哈希。
func TestLoadGeneratesUsableAccounts(t *testing.T) {
	db := openDB(t)
	res, err := Load(db, Options{UploadDir: t.TempDir(), Quiet: true})
	if err != nil {
		t.Fatalf("写入演示数据失败：%v", err)
	}
	if len(res.Accounts) != 2 {
		t.Fatalf("应新建 2 个演示账号（小程序员工 + PC 企业管理员），实际 %d", len(res.Accounts))
	}
	for _, a := range res.Accounts {
		if len(a.Pass) < 12 {
			t.Errorf("%s 的口令过短（%q），应随机生成", a.User, a.Pass)
		}
		if a.Where == "" || a.Role == "" {
			t.Errorf("账号展示信息不全：%+v", a)
		}
	}

	// 企业管理员：哈希可校验
	var adm model.Admin
	if err := db.Where("username = ?", DemoAdminUser).First(&adm).Error; err != nil {
		t.Fatalf("读取企业管理员失败：%v", err)
	}
	if adm.CompanyID != DemoCompanyID {
		t.Fatalf("企业管理员应归属 1 号企业，got %d", adm.CompanyID)
	}
	pass := accountPass(res, DemoAdminUser)
	if !auth.CheckPassword(adm.PasswordHash, pass) {
		t.Fatal("回显的企业管理员口令必须能通过哈希校验（否则运维拿着口令登不进去）")
	}
	if adm.PasswordHash == pass {
		t.Fatal("库里绝不能存明文口令")
	}

	// 员工：能按手机号盲索引查到，且口令可校验
	var emp model.Employee
	if err := db.Where("phone_bidx = ?", pii.BlindIndex(DemoEmployeePhone)).First(&emp).Error; err != nil {
		t.Fatalf("按手机号盲索引查不到演示员工：%v", err)
	}
	if !auth.CheckPassword(emp.PasswordHash, accountPass(res, DemoEmployeePhone)) {
		t.Fatal("回显的员工口令必须能通过哈希校验")
	}
	if emp.Phone != DemoEmployeePhone || emp.Name != DemoEmployeeName {
		t.Fatalf("演示员工信息不对：phone=%q name=%q", emp.Phone, emp.Name)
	}
}

// 指定口令时不再随机（devserver 依赖这条：联调要反复登录，口令必须稳定）。
func TestLoadKeepsGivenPasswords(t *testing.T) {
	db := openDB(t)
	res, err := Load(db, Options{
		UploadDir:            t.TempDir(),
		EmployeePassword:     "fixed-emp-pass",
		CompanyAdminPassword: "fixed-adm-pass",
		Quiet:                true,
	})
	if err != nil {
		t.Fatalf("写入演示数据失败：%v", err)
	}
	if accountPass(res, DemoEmployeePhone) != "fixed-emp-pass" ||
		accountPass(res, DemoAdminUser) != "fixed-adm-pass" {
		t.Fatalf("应原样使用给定口令：%+v", res.Accounts)
	}
}

// 重复调用必须幂等：devserver 每次启动都跑它，第二次若再建一遍就会变成 16 个分类。
// 账号也不该被重建——重建等于把运维改过的口令又改回随机值。
func TestLoadIsIdempotent(t *testing.T) {
	db := openDB(t)
	if _, err := Load(db, Options{UploadDir: t.TempDir(), Quiet: true}); err != nil {
		t.Fatalf("首次写入失败：%v", err)
	}
	res2, err := Load(db, Options{UploadDir: t.TempDir(), Quiet: true})
	if err != nil {
		t.Fatalf("二次写入失败：%v", err)
	}
	if len(res2.Accounts) != 0 {
		t.Fatalf("账号已存在时不应再生成新口令：%+v", res2.Accounts)
	}
	for _, w := range []struct {
		name string
		m    any
		n    int64
	}{
		{"企业", &model.Company{}, 1},
		{"业绩分类", &model.AchievementCategory{}, 8},
		{"项目案例", &model.ProjectCase{}, 10},
		{"业务服务", &model.BizService{}, 4},
		{"员工", &model.Employee{}, 1},
		{"企业管理员", &model.Admin{}, 1},
	} {
		if got := count(t, db, w.m); got != w.n {
			t.Errorf("二次写入后 %s 应仍是 %d 条，实际 %d", w.name, w.n, got)
		}
	}
}

// 盲索引用的是「写入那一刻」的 PII 密钥。这条用例把最贵的坑钉死：
// 安装向导若在签发真密钥之前写演示员工，重启后按手机号永远查不到这个人
// （登录报账号不存在），而报错和密钥毫无关联。
func TestEmployeeBlindIndexFollowsCurrentKey(t *testing.T) {
	if err := pii.SetKey(testKey(1)); err != nil {
		t.Fatalf("装载测试密钥失败：%v", err)
	}
	defer pii.Reload() // 还原：重新读环境变量（测试环境通常未设 → 回落内置 dev 密钥）

	db := openDB(t)
	if _, err := Load(db, Options{UploadDir: t.TempDir(), Quiet: true}); err != nil {
		t.Fatalf("写入演示数据失败：%v", err)
	}

	var n int64
	if err := db.Model(&model.Employee{}).Where("phone_bidx = ?", pii.BlindIndex(DemoEmployeePhone)).Count(&n).Error; err != nil {
		t.Fatalf("查询失败：%v", err)
	}
	if n != 1 {
		t.Fatalf("用写入时的密钥必须能查到演示员工，实际 %d 条", n)
	}

	// 换一把密钥（模拟「旧数据用 dev 密钥写完、重启换真密钥」）→ 查不到，正是要避免的形态
	if err := pii.SetKey(testKey(2)); err != nil {
		t.Fatalf("装载第二把密钥失败：%v", err)
	}
	if err := db.Model(&model.Employee{}).Where("phone_bidx = ?", pii.BlindIndex(DemoEmployeePhone)).Count(&n).Error; err != nil {
		t.Fatalf("查询失败：%v", err)
	}
	if n != 0 {
		t.Fatal("换了密钥还能查到，说明盲索引没用当前密钥计算——安装顺序的守卫失效了")
	}
}

// uploadDir 为空表示「不落素材文件」：不能报错，也不能返回半截映射。
func TestWriteAssetsEmptyDir(t *testing.T) {
	urls, err := WriteAssets("")
	if err != nil {
		t.Fatalf("空目录不应报错：%v", err)
	}
	if len(urls) != 0 {
		t.Fatalf("空目录应返回空映射，实际 %d 项", len(urls))
	}
}

func accountPass(r Result, user string) string {
	for _, a := range r.Accounts {
		if a.User == user {
			return a.Pass
		}
	}
	return ""
}

// testKey 造一把合法的 32 字节 base64 密钥（fill 不同即不同密钥）。
func testKey(fill byte) string {
	return base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{fill}, 32))
}
