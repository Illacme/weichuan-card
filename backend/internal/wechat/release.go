package wechat

import (
	"encoding/json"
	"fmt"
)

// 小程序版本发布相关接口（提审 / 查状态 / 发布 / 撤回）。
//
// 这条链路的完整形态是：
//
//   上传代码（CI 做，见 handler/mp_release.go 的说明）
//     → 提审 submit_audit          → 拿到 auditid
//     → 轮询 get_auditstatus        → 0 通过 / 1 拒绝 / 2 审核中 / 3 已撤回 / 4 延后
//     → 通过后 release              → 上线
//     （中途可 undo_code_audit 撤回）
//
// 三个必须记住的硬约束（都来自微信官方文档）：
//   1. **提审消耗审核额度，撤回不返还**。所以 service 层绝不自动重试提审，
//      撤回也必须有二次确认。
//   2. **上传后要等代码检测结束才能提审**，否则报 61039。检测是上传时异步
//      启动的，CI 上传完到能提审之间有窗口——所以「登记版本」和「提交审核」
//      是两个独立动作，不能合并成一个按钮。
//   3. **不要一起重试上传和提审**，否则可能重复触发检测，报 9402202
//      「请勿频繁提交」。
//
// 关于 submit_audit 的 body：第三方平台路径要求传 item_list（类目必填），
// 而自有 access_token 路径下类目取「小程序后台已配置的类目」，body 可为空
// （官方 ext 开发文档明确写着「只需要调用 wxa/submit_audit 即可以提交审核」）。
// 这里仍保留可选入参：万一某个小程序后台类目没配齐，调用方可以显式传
// item_list 兜底。传了就用传的，不传就发空 body。

// SubmitAudit 提交代码审核，返回微信审核单 id（auditid）。
//
// itemList 可传 nil（= 空 body，用后台已配类目）。非 nil 时原样放进请求体，
// 由调用方保证内容合法——这里不校验类目，因为合法类目集要另外调
// getAllCategoryName 才知道，而那个列表很长、且各家小程序配的类目不同。
func SubmitAudit(cfg Config, itemList []map[string]any) (int64, error) {
	body := map[string]any{}
	if len(itemList) > 0 {
		body["item_list"] = itemList
	}
	b, err := postWX("/wxa/submit_audit", cfg, body, true)
	if err != nil {
		return 0, err
	}
	var r struct {
		AuditID int64 `json:"auditid"`
	}
	if err := json.Unmarshal(b, &r); err != nil {
		return 0, fmt.Errorf("submit_audit 响应解析失败: %w", err)
	}
	if r.AuditID == 0 {
		// errcode 已是 0 但没有 auditid：微信偶发这种半截响应，别当成功
		return 0, fmt.Errorf("submit_audit 未返回 auditid")
	}
	return r.AuditID, nil
}

// AuditStatus 一次审核状态查询的结果。
type AuditStatus struct {
	// Status 微信原始枚举：0 审核成功 / 1 审核被拒绝 / 2 审核中 /
	// 3 已撤回 / 4 审核延后。保留原值而不在这里转换，是为了让上层能记日志、
	// 也让 model.WXAuditStatusToRelease 成为唯一的状态映射处（单一真相源）。
	Status int
	// Reason 拒绝原因（status=1）或延后原因（status=4）。其余状态为空。
	Reason string
	// Screenshot 审核失败的小程序截图 media_id 列表，竖线分隔（status=1）。
	// 存下来但不解析成图：排查时按 id 去拉永久素材即可，没必要为它引入
	// 素材下载依赖。
	Screenshot string
}

// GetAuditStatus 查询指定审核单的状态。
func GetAuditStatus(cfg Config, auditID int64) (AuditStatus, error) {
	if auditID <= 0 {
		return AuditStatus{}, fmt.Errorf("auditid 无效: %d", auditID)
	}
	b, err := postWX("/wxa/get_auditstatus", cfg, map[string]any{"auditid": auditID}, true)
	if err != nil {
		return AuditStatus{}, err
	}
	var r struct {
		Status     int    `json:"status"`
		Reason     string `json:"reason"`
		Screenshot string `json:"screenshot"`
	}
	if err := json.Unmarshal(b, &r); err != nil {
		return AuditStatus{}, fmt.Errorf("get_auditstatus 响应解析失败: %w", err)
	}
	return AuditStatus{Status: r.Status, Reason: r.Reason, Screenshot: r.Screenshot}, nil
}

// Release 发布已审核通过的版本。
// 前置：必须先提审且审核通过（status=0），否则微信报错。
// 没有入参也没有返回数据——发布是「把当前审过的那一版推上线」的单一动作。
func Release(cfg Config) error {
	_, err := postWX("/wxa/release", cfg, map[string]any{}, true)
	return err
}

// UndoCodeAudit 撤回正在审核中的审核单。
//
// **撤回不返还已消耗的审核额度**（官方约束），所以这不是个可以随便调的动作：
// service 层要求二次确认，且只有 auditing 态允许撤回（见状态机）。
func UndoCodeAudit(cfg Config) error {
	_, err := postWX("/wxa/undo_code_audit", cfg, map[string]any{}, true)
	return err
}
