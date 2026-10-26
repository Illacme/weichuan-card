package contentsec

import (
	"errors"
	"strings"
	"testing"

	"weichuan/internal/wechat"

	"github.com/stretchr/testify/require"
)

// 策略层的测试。所有分支都不打外网：checkText 被替换成桩。
//
// 这里最要紧的两条：
//   - fail-open（接口失败必须放行）——否则微信抖动会让「提交申请」整体不可用；
//   - 命中违规必须拦——这是 fail-open 的唯一例外，写错了整件事就白做。

func withCheck(t *testing.T, fn func(wechat.Config, string, string, int) (wechat.SecVerdict, error)) {
	t.Helper()
	old := checkText
	checkText = fn
	t.Cleanup(func() { checkText = old })
}

var goodCred = wechat.Config{AppID: "wxappid", AppSecret: "secret"}

func TestCheckTextPass(t *testing.T) {
	withCheck(t, func(wechat.Config, string, string, int) (wechat.SecVerdict, error) {
		return wechat.SecVerdict{Suggest: "pass"}, nil
	})
	d := CheckText(Params{Enabled: true, Cred: goodCred, OpenID: "o1"}, "张三", "销售总监")
	require.True(t, d.Allowed)
	require.False(t, d.Skipped)
}

func TestCheckTextRiskyBlocks(t *testing.T) {
	withCheck(t, func(wechat.Config, string, string, int) (wechat.SecVerdict, error) {
		return wechat.SecVerdict{Suggest: "risky", Label: 20001, TraceID: "t1"}, nil
	})
	d := CheckText(Params{Enabled: true, Cred: goodCred, OpenID: "o1"}, "违规内容")
	require.False(t, d.Allowed, "命中违规必须拦")
	require.NotEmpty(t, d.Reason)
	require.Equal(t, 20001, d.Verdict.Label)
}

// 微信建议复核：放行，但带 Note 让调用方记日志。
func TestCheckTextReviewAllowedWithNote(t *testing.T) {
	withCheck(t, func(wechat.Config, string, string, int) (wechat.SecVerdict, error) {
		return wechat.SecVerdict{Suggest: "review", Label: 20002}, nil
	})
	d := CheckText(Params{Enabled: true, Cred: goodCred, OpenID: "o1"}, "疑似违规")
	require.True(t, d.Allowed)
	require.Equal(t, "pending_review", d.Note)
}

// fail-open：接口报错一律放行。这条反直觉，但它是「内容安全是增强项、
// 不是业务前提」的直接体现——微信抖动不该让开通申请 100% 失败。
func TestCheckTextAPIErrorFailsOpen(t *testing.T) {
	withCheck(t, func(wechat.Config, string, string, int) (wechat.SecVerdict, error) {
		return wechat.SecVerdict{}, errors.New("dial tcp: i/o timeout")
	})
	d := CheckText(Params{Enabled: true, Cred: goodCred, OpenID: "o1"}, "正常内容")
	require.True(t, d.Allowed)
	require.True(t, d.Skipped)
	require.Contains(t, d.Note, "api_error")
	require.Contains(t, d.Note, "i/o timeout")
}

func TestCheckTextDisabledSkips(t *testing.T) {
	withCheck(t, func(wechat.Config, string, string, int) (wechat.SecVerdict, error) {
		t.Fatal("开关关闭时不该调用微信接口")
		return wechat.SecVerdict{}, nil
	})
	d := CheckText(Params{Enabled: false, Cred: goodCred, OpenID: "o1"}, "内容")
	require.True(t, d.Allowed)
	require.Equal(t, "disabled", d.Note)
}

func TestCheckTextNoCredentialSkips(t *testing.T) {
	withCheck(t, func(wechat.Config, string, string, int) (wechat.SecVerdict, error) {
		t.Fatal("没有凭据时不该调用微信接口")
		return wechat.SecVerdict{}, nil
	})
	d := CheckText(Params{Enabled: true, Cred: wechat.Config{}, OpenID: "o1"}, "内容")
	require.True(t, d.Allowed)
	require.Equal(t, "no_credential", d.Note)
}

// 匿名提交（访客还没登录）没有 openid：跳过而不是报错，靠后台人工审核兜底。
func TestCheckTextNoOpenIDSkips(t *testing.T) {
	withCheck(t, func(wechat.Config, string, string, int) (wechat.SecVerdict, error) {
		t.Fatal("没有 openid 时不该调用微信接口")
		return wechat.SecVerdict{}, nil
	})
	d := CheckText(Params{Enabled: true, Cred: goodCred}, "内容")
	require.True(t, d.Allowed)
	require.Equal(t, "no_openid", d.Note)
}

func TestCheckTextEmptyContentSkips(t *testing.T) {
	withCheck(t, func(wechat.Config, string, string, int) (wechat.SecVerdict, error) {
		t.Fatal("没有内容时不该调用微信接口")
		return wechat.SecVerdict{}, nil
	})
	d := CheckText(Params{Enabled: true, Cred: goodCred, OpenID: "o1"}, "  ", "")
	require.True(t, d.Allowed)
	require.Equal(t, "no_content", d.Note)
}

// 多个字段拼接后一次提交：违规词可能被拆在两个字段里，单字段检测会双双放行。
func TestCheckTextJoinsFields(t *testing.T) {
	var got string
	withCheck(t, func(_ wechat.Config, _ string, content string, _ int) (wechat.SecVerdict, error) {
		got = content
		return wechat.SecVerdict{Suggest: "pass"}, nil
	})
	CheckText(Params{Enabled: true, Cred: goodCred, OpenID: "o1"}, " 张三 ", "", " 销售总监 ")
	require.Equal(t, "张三\n销售总监", got)
}

// 超长内容按 rune 截断到 2500 字，且不能切坏 UTF-8。
func TestCheckTextTruncatesByRune(t *testing.T) {
	var got string
	withCheck(t, func(_ wechat.Config, _ string, content string, _ int) (wechat.SecVerdict, error) {
		got = content
		return wechat.SecVerdict{Suggest: "pass"}, nil
	})
	long := strings.Repeat("违", maxContentRunes+50)
	CheckText(Params{Enabled: true, Cred: goodCred, OpenID: "o1"}, long)
	require.Equal(t, maxContentRunes, len([]rune(got)), "按 rune 截断")
	require.True(t, strings.HasPrefix(got, "违"), "截断后仍是合法 UTF-8 中文")
}

// 场景值透传：名片资料属于 SceneProfile。
func TestCheckTextPassesScene(t *testing.T) {
	var gotScene int
	withCheck(t, func(_ wechat.Config, _ string, _ string, scene int) (wechat.SecVerdict, error) {
		gotScene = scene
		return wechat.SecVerdict{Suggest: "pass"}, nil
	})
	CheckText(Params{Enabled: true, Cred: goodCred, OpenID: "o1", Scene: wechat.SceneProfile}, "内容")
	require.Equal(t, wechat.SceneProfile, gotScene)
}

// openid 原样透传给微信（微信用它把检测归到具体用户）。
func TestCheckTextPassesOpenID(t *testing.T) {
	var gotOpenID string
	withCheck(t, func(_ wechat.Config, openid string, _ string, _ int) (wechat.SecVerdict, error) {
		gotOpenID = openid
		return wechat.SecVerdict{Suggest: "pass"}, nil
	})
	CheckText(Params{Enabled: true, Cred: goodCred, OpenID: "openid-xyz"}, "内容")
	require.Equal(t, "openid-xyz", gotOpenID)
}
