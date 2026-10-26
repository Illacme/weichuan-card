package handler

import (
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"strings"

	"weichuan/internal/middleware"
	"weichuan/internal/model"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// 海报个性化设置（自定义背景 + 版式）。
//
// 为什么需要后端存：海报页虽然在小程序里，但员工会换手机、换微信重装——
// 只在 wx.setStorageSync 里记，换设备就全丢，还得重挑一遍背景、重调一遍版式。
// 存后端跟着员工账号走，才是「设置」而不是「这一次的选择」。
//
// 为什么用 JSON 而不是拆列：版式可选项会随需求增删（现在 4 个模板 / 5 个开关 /
// 配色 / 位移 / 头像大小，以后还会加），拆列每加一项都要写迁移 + 改 GORM 模型。
// 代价是数据库层面看不出结构，所以**校验必须在写入前做**——本文件
// normalizePosterLayout / normalizePosterBackgrounds 是唯一的准入闸口，
// 白名单外的值一律归一成默认值，脏数据进不了库（也污染不了别人的渲染）。

// 版式模板（前端 poster.ts 的 TEMPLATES 与此一一对应）。
const (
	PosterTemplateClassic = "classic" // 经典名片：头像左上，右侧姓名职位，下方公司/地址/电话
	PosterTemplateCenter  = "center"  // 居中：头像居中、文字居中
	PosterTemplateMinimal = "minimal" // 极简：只姓名 + 职位 + 码
	PosterTemplateCard    = "card"    // 卡片：信息装进半透明卡片
	PosterTemplateSplit   = "split"   // 分栏：头像在左，文字全部右对齐
	PosterTemplateLeft    = "left"    // 大字：姓名 27px 独占一行 + 主色短线
	PosterTemplateStack   = "stack"   // 清单：每行「标签 + 值」两列对齐
	PosterTemplatePoster  = "poster"  // 画报：内容下沉到中下部，上方留给背景主体
)

// 文字配色。auto = 按背景自动（有背景图用浅色字，白底用深色字）。
const (
	PosterTextAuto  = "auto"
	PosterTextLight = "light"
	PosterTextDark  = "dark"
)

// 头像尺寸三档（画布 320×460 坐标，非物理像素）。
// 不做连续滑条：小程序里拖动调尺寸的手感很差，且三档已够区分「小清新 / 标准 / 突出」。
var posterAvatarSizes = []int{56, 72, 88}

// 二维码尺寸档位（画布像素）。0 = 自动（客户端按内容高度自适应）。
//
// 中档 104 就是改造前自动算出的上限——「没动过设置的人」选自动和选中间看到
// 的是同一张图。大档 140 是给「扫得到最重要」的场景（贴群里、打印出来），
// 320 宽画布上占 44%，还能和文字共存。
// 与小程序端 QR_SIZES 一一对应；客户端另有「不与内容重叠」的夹紧。
var posterQrSizes = []int{0, 76, 104, 140}

// 位移上限：信息区 / 二维码各自平移的画布像素（画布 320×460）。
//
// 这里曾经写死纵向 ±60、横向 ±16——那是「猜」出来的安全区，结果是两字姓名
// 拖到 16px 就被卡住，而居中模板的长公司名拖过去照样被裁掉尾字：真正的可用
// 余量取决于文字实际多宽、内容实际多高，服务端根本算不出来。
//
// 现在夹紧交给客户端按**内容包围盒**算（miniprogram/pages/poster/poster.ts
// 的 shiftRange），后端只挡「离谱值」：超过画布长边的位移在任何情况下都画不
// 出来，没有入库的理由。两边用同一个上限，客户端再怎么被改也污染不了库。
const (
	posterOffsetLimit = 480 // ≥ 画布长边 460，正常拖拽永远够用
	// maxPosterBackgrounds 自定义背景上限。上传的图会占对象存储，且选择条太长反而难挑。
	maxPosterBackgrounds = 9
	// maxPosterBgNameLen / maxPosterBgFileLen 单个背景的名称与路径长度上限（按 rune 计）。
	maxPosterBgNameLen = 24
	maxPosterBgFileLen = 512
	// maxPosterTextLen 自定义文字（一句话宣传语）上限，按 rune 计。
	// 320px 宽的画布上 30 字已经要折两行，再长只能靠省略号收尾，不如从入口拦住。
	maxPosterTextLen = 30
)

// posterBgIDRe 背景 id 白名单：内置素材是 poster-xxx，自定义是 c-xxxxxxxx。
// 限制字符集是为了让 id 能安全地出现在 URL / 本地存储键里，不必再转义。
var posterBgIDRe = regexp.MustCompile(`^[A-Za-z0-9_-]{0,64}$`)

// PosterBackground 一张自定义背景图。
// ID 由客户端生成（c- + 8 位随机），后端只校验格式——服务端不需要为它建表，
// 一张图的全部信息就在这三个字段里。
type PosterBackground struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	File string `json:"file"` // /uploads/... 或 https://cdn...
}

// PosterLayout 海报版式（完整形态：每个字段都有具体值）。
// 存库与回传给客户端都用它——客户端不必再猜「这个字段没给是不是要默认」。
type PosterLayout struct {
	Template   string `json:"template"`
	ShowAvatar bool   `json:"showAvatar"`
	// 姓名可以关掉：活动海报 / 只做品牌曝光时不想露个人名字。
	// 关掉后客户端会把职位升为标题行，不会在头像旁留一条孤零零的小字。
	ShowName    bool `json:"showName"`
	ShowTitle   bool `json:"showTitle"`
	ShowCompany bool `json:"showCompany"`
	ShowAddress bool `json:"showAddress"`
	ShowPhone   bool `json:"showPhone"`
	// 座机 / 邮箱默认关：这两行以前从不出现在海报上，升级后不该让老用户
	// 突然多出两行。需要的人在「元素」里打开。
	ShowLandline bool `json:"showLandline"`
	ShowEmail    bool `json:"showEmail"`
	// 小程序码开关：关掉是纯形象海报，没有扫码入口。默认开。
	ShowQr    bool   `json:"showQr"`
	TextColor string `json:"textColor"`
	// 自定义文字：一句话宣传语 / 服务承诺 / 活动信息，画在名片信息下方。
	// 存这里而不是每次现填——它更像「我的签名」，调好一次以后每张海报都带着。
	CustomText string `json:"customText"`
	// 信息区位移（拖动海报调整），±posterOffsetLimit。真实可用区间由客户端
	// 按内容包围盒算，这里只保证不出现画布量级的离谱值。
	OffsetX int `json:"offsetX"`
	OffsetY int `json:"offsetY"`
	// 二维码位移：与信息区各自独立，两者互不牵连
	QrOffsetX int `json:"qrOffsetX"`
	QrOffsetY int `json:"qrOffsetY"`
	// 二维码尺寸档位：0 = 自动（跟着内容高度自适应），其余见 posterQrSizes
	QrSize     int `json:"qrSize"`
	AvatarSize int `json:"avatarSize"`
}

// posterLayoutPatch 写入请求 / 读库解析用的「补丁」形态：布尔用指针。
//
// 为什么不能直接用 PosterLayout 接请求：bool 的零值就是 false，
// 无法区分「客户端没传这个字段」和「客户端明确要关掉」。
// 用完整结构接 `{}` 会得到「五个元素全关」的海报——用户发出去是一张
// 只有背景和二维码的白板，而他只是没升级客户端。指针一区分就没了歧义。
type posterLayoutPatch struct {
	Template     *string `json:"template"`
	ShowAvatar   *bool   `json:"showAvatar"`
	ShowName     *bool   `json:"showName"`
	ShowTitle    *bool   `json:"showTitle"`
	ShowCompany  *bool   `json:"showCompany"`
	ShowAddress  *bool   `json:"showAddress"`
	ShowPhone    *bool   `json:"showPhone"`
	ShowLandline *bool   `json:"showLandline"`
	ShowEmail    *bool   `json:"showEmail"`
	ShowQr       *bool   `json:"showQr"`
	TextColor    *string `json:"textColor"`
	CustomText   *string `json:"customText"`
	OffsetX      *int    `json:"offsetX"`
	OffsetY      *int    `json:"offsetY"`
	QrOffsetX    *int    `json:"qrOffsetX"`
	QrOffsetY    *int    `json:"qrOffsetY"`
	QrSize       *int    `json:"qrSize"`
	AvatarSize   *int    `json:"avatarSize"`
}

// DefaultPosterLayout 新员工的初始版式：与改造前的海报完全一致，
// 保证「没调过设置的人」看到的海报和以前一模一样（升级不惊吓）。
func DefaultPosterLayout() PosterLayout {
	return PosterLayout{
		Template:     PosterTemplateClassic,
		ShowAvatar:   true,
		ShowName:     true,
		ShowTitle:    true,
		ShowCompany:  true,
		ShowAddress:  true,
		ShowPhone:    true,
		ShowLandline: false,
		ShowEmail:    false,
		ShowQr:       true,
		TextColor:    PosterTextAuto,
		CustomText:   "",
		OffsetY:      0,
		AvatarSize:   72,
	}
}

// resolve 把补丁合并到默认值上，得到合法版式。
// 白名单外的枚举值落默认、越界数值夹紧、头像尺寸取最近档位。
//
// **不报错**：版式是体验项，用户传了个错值不该让整次保存失败（他还得重调
// 一遍）；静默纠正成默认值即可。
func (p posterLayoutPatch) resolve() PosterLayout {
	out := DefaultPosterLayout()

	if p.Template != nil {
		switch *p.Template {
		case PosterTemplateClassic, PosterTemplateCenter, PosterTemplateMinimal, PosterTemplateCard,
			PosterTemplateSplit, PosterTemplateLeft, PosterTemplateStack, PosterTemplatePoster:
			out.Template = *p.Template
		}
	}
	if p.TextColor != nil {
		switch *p.TextColor {
		case PosterTextAuto, PosterTextLight, PosterTextDark:
			out.TextColor = *p.TextColor
		}
	}
	if p.ShowAvatar != nil {
		out.ShowAvatar = *p.ShowAvatar
	}
	if p.ShowName != nil {
		out.ShowName = *p.ShowName
	}
	if p.CustomText != nil {
		out.CustomText = normalizePosterText(*p.CustomText)
	}
	if p.ShowTitle != nil {
		out.ShowTitle = *p.ShowTitle
	}
	if p.ShowCompany != nil {
		out.ShowCompany = *p.ShowCompany
	}
	if p.ShowAddress != nil {
		out.ShowAddress = *p.ShowAddress
	}
	if p.ShowPhone != nil {
		out.ShowPhone = *p.ShowPhone
	}
	if p.ShowLandline != nil {
		out.ShowLandline = *p.ShowLandline
	}
	if p.ShowEmail != nil {
		out.ShowEmail = *p.ShowEmail
	}
	if p.ShowQr != nil {
		out.ShowQr = *p.ShowQr
	}
	if p.OffsetX != nil {
		out.OffsetX = clampInt(*p.OffsetX, -posterOffsetLimit, posterOffsetLimit)
	}
	if p.OffsetY != nil {
		out.OffsetY = clampInt(*p.OffsetY, -posterOffsetLimit, posterOffsetLimit)
	}
	if p.QrOffsetX != nil {
		out.QrOffsetX = clampInt(*p.QrOffsetX, -posterOffsetLimit, posterOffsetLimit)
	}
	if p.QrOffsetY != nil {
		out.QrOffsetY = clampInt(*p.QrOffsetY, -posterOffsetLimit, posterOffsetLimit)
	}
	if p.QrSize != nil {
		out.QrSize = nearestPosterQrSize(*p.QrSize)
	}
	if p.AvatarSize != nil {
		out.AvatarSize = nearestAvatarSize(*p.AvatarSize)
	}
	return out
}

// normalizePosterBackgrounds 校验自定义背景清单：格式不对的**整条丢弃**
// （不是报错），条数超限截断。返回 nil 表示「没有自定义背景」。
//
// 为什么 file 只认相对路径或 http(s)：这个值最终会进 canvas 的 img.src。
// 放行 javascript: / data: 之类伪协议没有意义，还会给以后某个不小心的
// <web-view> 或原生渲染留口子。
func normalizePosterBackgrounds(in []PosterBackground) []PosterBackground {
	if len(in) == 0 {
		return nil
	}
	out := make([]PosterBackground, 0, len(in))
	seen := make(map[string]bool, len(in))
	for _, bg := range in {
		bg.ID = strings.TrimSpace(bg.ID)
		bg.Name = strings.TrimSpace(bg.Name)
		bg.File = strings.TrimSpace(bg.File)
		if !posterBgIDRe.MatchString(bg.ID) || bg.ID == "" {
			continue
		}
		if seen[bg.ID] { // 同 id 重复：后者覆盖前者，避免选择条出现两张一样的图
			continue
		}
		if !validPosterFile(bg.File) {
			continue
		}
		if len([]rune(bg.Name)) > maxPosterBgNameLen {
			bg.Name = string([]rune(bg.Name)[:maxPosterBgNameLen])
		}
		if bg.Name == "" {
			bg.Name = "我的背景"
		}
		seen[bg.ID] = true
		out = append(out, bg)
		if len(out) >= maxPosterBackgrounds {
			break
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// normalizePosterText 自定义文字归一：控制字符（含换行 / 制表）压成空格、
// 连续空白折成一个、去首尾空白、按 rune 截断。
//
// 为什么必须压平换行：canvas 的 fillText 不认 \n，会画出缺字或方块，
// 而输入框里用户很自然会敲回车——不处理就会得到一条莫名其妙缺字的标语。
// 与小程序端 normalizeCustomText 同一套规则（两边都做，缺一边就会出现
// 「接口正常但海报画错」这种最难查的问题）。
func normalizePosterText(s string) string {
	var b strings.Builder
	space := false
	for _, r := range s {
		if r < 0x20 || r == 0x7f || r == '\u3000' {
			r = ' '
		}
		if r == ' ' {
			if space {
				continue
			}
			space = true
			b.WriteRune(' ')
			continue
		}
		space = false
		b.WriteRune(r)
	}
	out := strings.TrimSpace(b.String())
	if rs := []rune(out); len(rs) > maxPosterTextLen {
		out = string(rs[:maxPosterTextLen])
	}
	return out
}

// validPosterFile 只接受站内相对路径（/ 开头）或 http(s) 绝对地址。
func validPosterFile(file string) bool {
	if file == "" || len(file) > maxPosterBgFileLen {
		return false
	}
	if strings.HasPrefix(file, "/") {
		// 拒绝 //evil.com 这种协议相对地址（浏览器/canvas 会当成跨站绝对地址）
		return !strings.HasPrefix(file, "//")
	}
	lower := strings.ToLower(file)
	return strings.HasPrefix(lower, "https://") || strings.HasPrefix(lower, "http://")
}

func clampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// nearestAvatarSize 取最接近的合法档位：用户传 70 → 72，传 200 → 88。
// 等距时取较小档（80 距 72 和 88 都是 8 → 72），不让人「莫名其妙变大一号」。
func nearestAvatarSize(v int) int {
	best := posterAvatarSizes[1] // 默认 72
	bestDiff := -1
	for _, s := range posterAvatarSizes {
		d := v - s
		if d < 0 {
			d = -d
		}
		if bestDiff < 0 || d < bestDiff {
			best, bestDiff = s, d
		}
	}
	return best
}

// nearestPosterQrSize 二维码尺寸吸附到最近档位。
// 0（自动）不参与「最近」比较：传 30 说明用户要的是固定大小，吸到 76 而不是自动。
// 与小程序端 nearestQrSize 同一规则——库里不留界面表达不出来的值，
// 否则下次进页面「选中态」会凭空消失。
func nearestPosterQrSize(v int) int {
	if v <= 0 {
		return 0
	}
	best := posterQrSizes[1]
	bestDiff := -1
	for _, s := range posterQrSizes {
		if s == 0 {
			continue
		}
		d := v - s
		if d < 0 {
			d = -d
		}
		if bestDiff < 0 || d < bestDiff {
			best, bestDiff = s, d
		}
	}
	return best
}

// RegisterPoster 注册海报个性化设置接口（员工鉴权）。
//
// 为什么不做成公开端点：自定义背景是员工自己上传的私有素材，
// 别人（包括扫海报进来的访客）没有理由读到他的背景库；访客看海报时
// 渲染的是**已生成的图片**，不需要这些配置。
func RegisterPoster(r *gin.Engine, db *gorm.DB) {
	emp := middleware.AuthMiddleware(db, "employee")

	r.GET("/api/employee/poster-prefs", emp, func(c *gin.Context) {
		staffID := c.GetInt64("staffId")
		var p model.PosterPref
		if err := db.Where("staff_id = ?", staffID).First(&p).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				// 没存过：返回默认值而不是 404。客户端不必区分「新用户」和
				// 「老用户但没调过」——两种情况渲染结果本来就一样。
				// exists=false 是给客户端的一个信号：服务端没有可信记录，
				// 请沿用本机缓存的选择（否则换新账号第一进页面会把手挑的背景冲掉）。
				c.JSON(http.StatusOK, gin.H{"code": 0, "data": posterPrefPayload("", nil, DefaultPosterLayout(), false)})
				return
			}
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "查询失败"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"code": 0, "data": posterPrefPayload(
			p.BgID, decodePosterBackgrounds(p.Backgrounds), decodePosterLayout(p.Layout), true)})
	})

	r.PUT("/api/employee/poster-prefs", emp, func(c *gin.Context) {
		staffID := c.GetInt64("staffId")
		companyID := c.GetInt64("companyId")
		var req struct {
			BgID        string             `json:"bgId"`
			Backgrounds []PosterBackground `json:"backgrounds"`
			Layout      posterLayoutPatch  `json:"layout"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "参数错误"})
			return
		}

		// 归一（白名单 + 边界夹紧 + 超限截断），脏值在这里被吸收掉
		bgID := strings.TrimSpace(req.BgID)
		if !posterBgIDRe.MatchString(bgID) {
			bgID = ""
		}
		bgs := normalizePosterBackgrounds(req.Backgrounds)
		layout := req.Layout.resolve()

		// 选中的背景必须真实存在：内置素材 id 客户端会自己回落到默认款，
		// 但自定义背景被删了还留着 bgId，下次进页面就会渲染一张空背景。
		if bgID != "" && !isPosterBgAvailable(bgID, bgs) {
			bgID = ""
		}

		bgJSON, _ := json.Marshal(bgs) // nil → "null"，读取侧 decode 后即空切片
		layoutJSON, _ := json.Marshal(layout)

		var p model.PosterPref
		err := db.Where("staff_id = ?", staffID).First(&p).Error
		switch {
		case errors.Is(err, gorm.ErrRecordNotFound):
			p = model.PosterPref{
				StaffID: staffID, CompanyID: companyID,
				BgID: bgID, Backgrounds: string(bgJSON), Layout: string(layoutJSON),
			}
			if err := db.Create(&p).Error; err != nil {
				c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "保存失败"})
				return
			}
		case err != nil:
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "保存失败"})
			return
		default:
			p.BgID, p.Backgrounds, p.Layout = bgID, string(bgJSON), string(layoutJSON)
			if p.CompanyID == 0 {
				p.CompanyID = companyID // 历史行 / 早期未写租户：顺手补上
			}
			if err := db.Save(&p).Error; err != nil {
				c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "保存失败"})
				return
			}
		}

		// 回传归一后的结果：客户端据此纠正自己的本地状态（比如上传了第 10 张被截断、
		// 或者选中的自定义背景刚好被这次保存丢掉），避免「本地显示有、后端没有」的错位。
		c.JSON(http.StatusOK, gin.H{"code": 0, "data": posterPrefPayload(bgID, bgs, layout, true)})
	})
}

// isPosterBgAvailable 判断选中的背景是否还在。
// 内置素材（poster-*）本地清单由客户端负责校验，这里只拦自定义背景。
func isPosterBgAvailable(bgID string, bgs []PosterBackground) bool {
	if !strings.HasPrefix(bgID, "c-") {
		return true
	}
	for _, bg := range bgs {
		if bg.ID == bgID {
			return true
		}
	}
	return false
}

func posterPrefPayload(bgID string, bgs []PosterBackground, layout PosterLayout, exists bool) gin.H {
	if bgs == nil {
		bgs = []PosterBackground{} // 序列化成 []，客户端不必判 null
	}
	return gin.H{"bgId": bgID, "backgrounds": bgs, "layout": layout, "exists": exists}
}

// decodePosterLayout 读库并归一。库里可能有历史脏值（改过白名单 / 手工改库），
// 读取侧同样走一遍归一，保证返回给客户端的永远合法。
// 用 patch 形态解析：老版本存的行缺字段时按默认值补，而不是当成「关闭」。
func decodePosterLayout(raw string) PosterLayout {
	var p posterLayoutPatch
	if raw != "" {
		_ = json.Unmarshal([]byte(raw), &p)
	}
	return p.resolve()
}

func decodePosterBackgrounds(raw string) []PosterBackground {
	if raw == "" {
		return nil
	}
	var bgs []PosterBackground
	if err := json.Unmarshal([]byte(raw), &bgs); err != nil {
		return nil
	}
	return normalizePosterBackgrounds(bgs)
}
