package wechat

import (
	"encoding/json"
	"errors"
	"strings"
)

// 内容安全（UGC 检测）。
//
// 小程序里只要存在用户生成内容（员工头像、名片修改申请里的文字与图片），
// 就必须具备内容安全能力：微信审核会查，上线后被举报会直接封「上传」能力。
//
// 两条通道：
//   - 文本走 /wxa/msg_sec_check（v2，同步返回结论）——本文件实现；
//   - 图片 / 音视频只有异步接口 /wxa/media_check_async，结论要靠微信
//     「消息推送」回调回来，需要在微信后台配推送 URL，尚未接入
//     （见 docs/commercial-readiness.md 的 A1）。

// 内容安全场景值（微信 v2 口径）。场景只影响微信侧的判定尺度，
// 名片资料属于「资料」场景。
const (
	SceneProfile = 1 // 资料（昵称、签名、头像等）
	SceneComment = 2 // 评论
	SceneForum   = 3 // 论坛
	SceneSocial  = 4 // 社交日志
)

// SecVerdict 一次内容安全检测的结论。
type SecVerdict struct {
	Suggest string // pass / review / risky
	Label   int    // 违规标签，0 = 未命中
	TraceID string // 微信侧追踪号：申诉与排障都要它
}

// Risky 命中违规，必须拦下。
func (v SecVerdict) Risky() bool { return v.Suggest == "risky" }

// Review 疑似违规，微信建议人工复核。我们不拦（拦了等于把误判成本转给用户），
// 但调用方应记日志以便事后抽查。
func (v SecVerdict) Review() bool { return v.Suggest == "review" }

// MsgSecCheck 文本内容安全检测（v2）。content 为空直接判过，不发请求。
//
// openid 是 v2 的必填项：微信用它把这次检测归到具体用户。没有 openid 时不调
// 接口（否则必然报错、且每次都是一次无意义的外网往返），由调用方按「未检测」处理。
func MsgSecCheck(cfg Config, openid, content string, scene int) (SecVerdict, error) {
	content = strings.TrimSpace(content)
	if content == "" {
		return SecVerdict{Suggest: "pass"}, nil
	}
	if openid == "" {
		return SecVerdict{}, errors.New("缺少 openid，无法调用 v2 内容安全接口")
	}
	if scene <= 0 {
		scene = SceneProfile
	}
	body := map[string]interface{}{
		"content": content,
		"version": 2,
		"scene":   scene,
		"openid":  openid,
	}
	b, err := postWX("/wxa/msg_sec_check", cfg, body, true)
	if err != nil {
		// 87014 = 内容含违法违规内容。v2 正常走 result.suggest，但旧账号 / 旧版本
		// 仍可能直接回这个码。它表达的是「命中」而不是「调用失败」——当成 error
		// 抛出去会被上层按 fail-open 放行，等于亲手把违规内容放进来。
		var ae *APIError
		if errors.As(err, &ae) && ae.Code == 87014 {
			return SecVerdict{Suggest: "risky", Label: ae.Code}, nil
		}
		return SecVerdict{}, err
	}
	var r struct {
		ErrCode int `json:"errcode"`
		Result  struct {
			Suggest string `json:"suggest"`
			Label   int    `json:"label"`
		} `json:"result"`
		TraceID string `json:"trace_id"`
	}
	if err := json.Unmarshal(b, &r); err != nil {
		return SecVerdict{}, err
	}
	suggest := r.Result.Suggest
	if suggest == "" {
		// 没有 result 字段（接口降级 / 老账号）时不能当成违规：那会把所有正常
		// 提交一起拦下，且用户按提示改多少遍都没用。按通过处理，留 trace_id 备查。
		suggest = "pass"
	}
	return SecVerdict{Suggest: suggest, Label: r.Result.Label, TraceID: r.TraceID}, nil
}
