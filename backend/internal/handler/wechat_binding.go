package handler

import (
	"github.com/gin-gonic/gin"
	"weichuan/internal/model"
)

// 微信绑定的「可见性」统一口径（2026-11-08）。
//
// 背景：管理员的 wx_openid 一直标着 json:"-"、平台超管在后台连「绑没绑微信」都看不到；
// 员工侧反倒把 openid 全文摊在列表里。两边既不对称，也没有统一的状态字段。
//
// 口径：
//   - 后台列表 / 管理员身份接口只回布尔状态 wechatBound + 掩码 wxOpenidMasked，
//     绝不回 openid 原文；
//   - openid 是微信侧的用户标识信息，全量出网等于把「可用作登录凭证的标识」发给浏览者，
//     而后台只需要回答「绑了没 / 大概是哪一个」。要精确定位，走 P1 的按 openid 反查
//     （输入完整值、平台超管专属、只读留痕），方向正好反过来；
//   - 审计 Detail 里连掩码都不写——掩码也是可关联的标识碎片。
//
// 例外：/api/admin/employees/:id/export 是 PIPL 查阅复制权的协助接口，导出的是
// 员工本人全部个人信息，openid 属于其本人数据且该动作特意破例留痕，保留原文。
// 批量 CSV 导出本来就不含 openid。
//
// 另一个边界：/api/employee/me 回的是员工**本人**的完整资料（含本人 PII 明文），
// openid 原文也在其中，属 PIPL 本人查阅范围，与「后台浏览者看别人的 openid」
// 不是同一回事；小程序端也不读它，绑定状态走根级 wechatBound。
// 保留现状是 P0「零行为破坏」的取舍，P1 若要统一收紧需连同这条一起审，别只改半边。

// maskOpenIDForDisplay 把 openid 压成「首字符 + **** + 后四位」，供**下发到后台界面**用。
//
// 刻意不复用 contentsec.go 的 maskOpenID：那个是给服务端日志看的，宽松到保留前 6 位，
// 且空串显示 "-"。界面上 "-" 会被读成「绑了一个叫 - 的微信」，而日志里保留前 6 位
// 是为了排查时能区分不同用户——两者的威胁模型与空值语义都不一样，混用迟早出事。
// 短串（含异常情况）一律只回 ****，避免长度很短时掩码反而把原文露完。
// openid 是 ASCII，按字节切片即可。
func maskOpenIDForDisplay(openid string) string {
	if openid == "" {
		return ""
	}
	if len(openid) <= 6 {
		return "****"
	}
	return openid[:1] + "****" + openid[len(openid)-4:]
}

// withWechatBinding 给管理员类回包补上绑定状态与掩码。
// 用 gin.H 而不是给 model.Admin 加 json 字段：后者会让所有序列化 Admin 的地方
// 都开始下发 wx_openid，等于把 json:"-" 的安全约定一次拆掉。
func withWechatBinding(base gin.H, openid string) gin.H {
	base["wechatBound"] = openid != ""
	base["wxOpenidMasked"] = maskOpenIDForDisplay(openid)
	return base
}

// employeeWithBinding 员工列表的展示视图。
// 嵌入 *model.Employee 保留全部既有字段（含 AfterFind 解密出的 PII 明文），
// 外层同深度更浅的 OpenID 覆盖内层明文 openid → 输出掩码；再补一个布尔状态。
// 字段名保持 openid 不变是为了兼容前端既有列，不需要同步改所有消费者。
type employeeWithBinding struct {
	*model.Employee
	OpenID      string `json:"openid"`
	WechatBound bool   `json:"wechatBound"`
}

func employeeBindingRows(list []model.Employee) []employeeWithBinding {
	out := make([]employeeWithBinding, 0, len(list))
	for i := range list {
		e := &list[i]
		out = append(out, employeeWithBinding{
			Employee:    e,
			OpenID:      maskOpenIDForDisplay(e.OpenID),
			WechatBound: e.OpenID != "",
		})
	}
	return out
}
