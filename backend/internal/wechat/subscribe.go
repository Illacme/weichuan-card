package wechat

import (
	"errors"
	"fmt"
	"strings"
)

// 小程序订阅消息（subscribeMessage.send）——「服务通知」里那条可点的卡片。
//
// 这是本项目唯一能**主动触达**用户的通道：小程序没有客服消息、没有短信、没有邮件。
// 员工提交开通申请后就关掉小程序了，审批通过时他在别处——不发这条，他永远不知道
// 自己已经能用了（这正是「功能做完了但没人用」的典型形态）。
//
// 微信的两条硬约束决定了这里只能是「一次性订阅」，不能当推送通道用：
//  1. 一次性订阅：用户同意一次 → 只能下发一条。发完配额归零，下次要重新让用户点同意。
//  2. 长期订阅：只对政务 / 医疗 / 交通等特定行业类目开放，企业服务类目申请不到。
//
// 所以本包只服务**低频、用户强预期**的通知（审核结果），不做「名片被访问」这类
// 高频事件——那种场景下用户同意一次只收到第一条，之后再也不响，看起来就是坏的。
//
// 另一个必须知道的点：**微信不提供「查还剩几条配额」的接口**。所以发送前无法判断
// 用户是否订阅过，只能发出去看 errcode。43101 是正常现象（用户没同意），
// 不是故障，调用方不该把它当错误上报。

// SubscribeMsg 一条订阅消息的发送参数。
type SubscribeMsg struct {
	// ToUser 接收者的 openid。空串会被微信拒（40003），故本地先挡。
	ToUser string
	// TemplateID 模板 ID（公众平台 → 订阅消息 → 我的模板）。运营选完模板粘到这里。
	TemplateID string
	// Page 用户点卡片后打开的小程序页面（不带前导斜杠，可带 query）。
	// 留空则打开首页——「点进去不知道去哪」比「点进去是首页」更糟。
	Page string
	// Data 模板数据：字段名 → 值。字段名必须与模板**逐字一致**（thing1 / phrase2 …），
	// 类型也必须对得上：thing ≤20 字符、phrase ≤5 个汉字、character_string 只能是数字字母。
	Data map[string]string
	// State 打开的小程序版本：formal（正式版）/ trial（体验版）/ developer（开发版）。
	// 留空按 formal。测试期若填 trial 而线上是正式版，用户点开会是「页面不存在」。
	State string
}

// 订阅消息的常见 errcode（微信文档口径）。
const (
	ErrSubscribeNoPermission = 43101 // 用户未同意接收 / 配额已用完（正常现象）
	ErrSubscribeBadTemplate  = 40037 // template_id 不正确（模板 ID 填错或已删）
	ErrSubscribeBadData      = 47003 // 模板参数不准确（字段名 / 类型 / 长度不对）
	ErrSubscribeBadOpenID    = 40003 // touser 不是合法 openid
	ErrSubscribeBadPage      = 41030 // page 路径不正确（多半是漏了前导斜杠的反面：多写了 /）
)

// SendSubscribe 下发一条订阅消息。
//
// 与其它接口共用 postWX：token 失效（40001/42001）自动刷新重试一次。
// 重试在这里尤其重要——access_token 由多个接口共用，别处刚把它刷掉时，
// 这次发送不该因为「撞上了一个过期的 token」而丢掉用户的唯一一次配额。
func SendSubscribe(cfg Config, m SubscribeMsg) error {
	to := strings.TrimSpace(m.ToUser)
	tpl := strings.TrimSpace(m.TemplateID)
	if to == "" {
		return fmt.Errorf("订阅消息缺少接收者 openid")
	}
	if tpl == "" {
		return fmt.Errorf("订阅消息缺少模板 ID")
	}
	// 凭据没配就本地报错，别带着空 appid 打外网（同 Code2Session 的规矩）
	if cfg.AppID == "" || cfg.AppSecret == "" {
		return fmt.Errorf("未配置 WX_APPID / WX_APPSECRET")
	}
	if len(m.Data) == 0 {
		return fmt.Errorf("订阅消息缺少模板数据")
	}

	data := make(map[string]map[string]string, len(m.Data))
	for k, v := range m.Data {
		data[k] = map[string]string{"value": v}
	}
	body := map[string]interface{}{
		"touser":      to,
		"template_id": tpl,
		"data":        data,
		"lang":        "zh_CN",
	}
	// page 去掉前导斜杠：微信要的是 pages/index/index，写成 /pages/index/index 会 41030
	if p := strings.TrimPrefix(strings.TrimSpace(m.Page), "/"); p != "" {
		body["page"] = p
	}
	if s := strings.TrimSpace(m.State); s != "" {
		body["miniprogram_state"] = s
	}
	_, err := postWX("/cgi-bin/message/subscribe/send", cfg, body, true)
	return err
}

// SubscribeErrCode 取出 errcode；不是微信业务错误时返回 0。
// 调用方据此区分「用户没订阅」（不必告警）与「配置错了」（必须让运维看见）。
func SubscribeErrCode(err error) int {
	var apiErr *APIError
	if errors.As(err, &apiErr) {
		return apiErr.Code
	}
	return 0
}

// IsSubscribeNotGranted 这条失败是否属于「用户没同意 / 配额用完」。
//
// 单独一个判断而不是让调用方到处写 `code == 43101`：这个码在业务上不是错误，
// 而是「本次没通知到，仅此而已」——审批照样成功，不该记 error 级日志，
// 更不该把审批接口的返回码改掉。
func IsSubscribeNotGranted(err error) bool {
	return SubscribeErrCode(err) == ErrSubscribeNoPermission
}

// SubscribeErrHint 把 errcode 翻成一句能直接照做的排查提示（给运维看，不给用户看）。
//
// 为什么需要：这些码的原始 errmsg 是英文且极简（"invalid template_id"），
// 而运营看到「审核通过但员工没收到通知」时，第一反应是「代码坏了」。
// 把 40037/47003 直接翻成「去哪改哪个配置」，才能让问题停在运维那一层。
func SubscribeErrHint(err error) string {
	switch SubscribeErrCode(err) {
	case ErrSubscribeNoPermission:
		return "用户未同意接收该消息或配额已用完（一次性订阅，同意一次只能发一条），属正常情况"
	case ErrSubscribeBadTemplate:
		return "模板 ID 不正确：请到「系统设置 → 微信」核对审核结果通知模板 ID，模板被删或换了 ID 都要更新"
	case ErrSubscribeBadData:
		return "模板字段不匹配：请核对模板字段名（默认 thing1,phrase2,thing3）与字段类型（第二项须是 phrase，值不超过 5 个汉字）"
	case ErrSubscribeBadOpenID:
		return "openid 无效：接收者可能来自另一个小程序（企业独立版请确认用的是本企业 AppID）"
	case ErrSubscribeBadPage:
		return "page 路径不正确：应形如 pages/employee/me/me，不能带前导斜杠"
	}
	return ""
}
