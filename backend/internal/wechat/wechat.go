package wechat

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sync"
	"time"
)

// Config 小程序凭据
type Config struct {
	AppID     string
	AppSecret string
}

// apiBase 微信接口基址。抽成变量是为了单测能把流量导向 httptest——
// 本包每个函数都是一次外网调用，不注入就只能「不测」或「打真接口」，
// 两者都比不上把成功 / 失败 / errcode 重试这些支线在本地跑一遍。
var apiBase = "https://api.weixin.qq.com"

// SetAPIBase 把微信接口基址指向测试服务，返回的函数用于恢复原值。
//
// 存在的理由：本包的测试在包内可以直接改 apiBase，但 handler 包的集成测试
// （「违规内容提交被拦下」这类端到端断言）跨包够不到它。没有这个口子，
// 那条链路只能靠「不测」或「打真接口」，两者都不可接受。
//
// 它只改进程内变量，没有任何外部入口能触发——不是可被利用的攻击面。
func SetAPIBase(base string) func() {
	old := apiBase
	apiBase = base
	return func() { apiBase = old }
}

// tokenCache 按 appid 分键缓存 access_token。
//
// **为什么必须分键**：这里原来是「一个全局 token 变量」。平台模式下只有一个
// appid，那样写碰巧能跑；但企业独立版是「一个后端服务 N 个小程序」（见
// migrations/0024），A 企业来取码时若缓存里是 B 企业的 token，微信会拿
// **B 的身份**执行这次调用——生成出来的码属于别的小程序，扫不开。
// 到了版本发布更严重：token 决定了「这次操作落在哪个小程序上」，
// 拿错 token 等于把代码提到 / 发布到别家的小程序里去。
//
// 所以 key 用 appid，不用 company_id：token 的归属只由 appid 决定，
// 而同一个 appid 可能同时被平台模式和某个租户共用（未配租户凭据时回落到全局），
// 按 appid 分键天然正确，按 company_id 分反而会为同一个 appid 存两份。
var (
	tokenMu    sync.Mutex
	tokenCache = map[string]tokenEntry{}
)

type tokenEntry struct {
	token string
	exp   time.Time
}

// Code2Session 用小程序 wx.login 拿到的临时 code 换取 openid + session_key。
// jscode2session 无需 access_token，直接用 appid/secret/js_code 调微信接口。
// openid 是用户在本小程序内的唯一标识，用于在后台记录/核实申请人或登录者的微信身份。
func Code2Session(cfg Config, code string) (openid, sessionKey string, err error) {
	if cfg.AppID == "" || cfg.AppSecret == "" {
		return "", "", fmt.Errorf("未配置 WX_APPID / WX_APPSECRET")
	}
	if code == "" {
		return "", "", fmt.Errorf("缺少 js_code")
	}
	u := fmt.Sprintf(
		"%s/sns/jscode2session?appid=%s&secret=%s&js_code=%s&grant_type=authorization_code",
		apiBase, url.QueryEscape(cfg.AppID), url.QueryEscape(cfg.AppSecret), url.QueryEscape(code),
	)
	resp, err := http.Get(u)
	if err != nil {
		return "", "", err
	}
	defer func() { _ = resp.Body.Close() }() // 只读响应体，关闭错误无后果
	b, _ := io.ReadAll(resp.Body)
	var r struct {
		OpenID     string `json:"openid"`
		SessionKey string `json:"session_key"`
		UnionID    string `json:"unionid"`
		ErrCode    int    `json:"errcode"`
		ErrMsg     string `json:"errmsg"`
	}
	if err := json.Unmarshal(b, &r); err != nil {
		return "", "", err
	}
	if r.ErrCode != 0 || r.OpenID == "" {
		return "", "", fmt.Errorf("jscode2session err %d: %s", r.ErrCode, r.ErrMsg)
	}
	return r.OpenID, r.SessionKey, nil
}

// GetAccessToken 获取并缓存 access_token（默认 7200s，提前 5 分钟失效）。
// 缓存按 appid 分键，见 tokenCache 注释。
func GetAccessToken(cfg Config) (string, error) {
	tokenMu.Lock()
	defer tokenMu.Unlock()
	return accessToken(cfg)
}

// accessToken 取 token 的实做，**不带锁**（调用方必须已持有 tokenMu）。
// 拆出来是因为 refreshToken 需要在同一个临界区里强制失效再重取：
// 一旦它带着锁去调 GetAccessToken，后者的 Lock 会撞上自己持有的同一把锁
// ——Go 的 mutex 不可重入，结果是永久死锁（小程序码接口在 40001 重试这条
// 支线上会整个挂住，且之后所有请求都会跟着卡在锁上）。
func accessToken(cfg Config) (string, error) {
	e, ok := tokenCache[cfg.AppID]
	if ok && e.token != "" && time.Now().Before(e.exp) {
		return e.token, nil
	}
	// 与 Code2Session 同一条规矩：凭据没配就在本地报错，别带着空 appid 打外网
	// （微信只会回一句「无效」，排查时看不出其实是自己没配）
	if cfg.AppID == "" || cfg.AppSecret == "" {
		return "", fmt.Errorf("未配置 WX_APPID / WX_APPSECRET")
	}
	u := fmt.Sprintf("%s/cgi-bin/token?grant_type=client_credential&appid=%s&secret=%s", apiBase, cfg.AppID, cfg.AppSecret)
	resp, err := http.Get(u)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }() // 只读响应体，关闭错误无后果
	b, _ := io.ReadAll(resp.Body)
	var r struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
		ErrCode     int    `json:"errcode"`
		ErrMsg      string `json:"errmsg"`
	}
	if err := json.Unmarshal(b, &r); err != nil {
		return "", err
	}
	if r.ErrCode != 0 || r.AccessToken == "" {
		return "", fmt.Errorf("wx token err %d: %s", r.ErrCode, r.ErrMsg)
	}
	tokenCache[cfg.AppID] = tokenEntry{token: r.AccessToken, exp: time.Now().Add(time.Duration(r.ExpiresIn-300) * time.Second)}
	return r.AccessToken, nil
}

// refreshToken 强制失效并重新获取（token 过期时重试用）。
// 与 GetAccessToken 共用同一把锁，故只调无锁的 accessToken（见其注释）。
func refreshToken(cfg Config) (string, error) {
	tokenMu.Lock()
	defer tokenMu.Unlock()
	delete(tokenCache, cfg.AppID)
	return accessToken(cfg)
}

// GetWXACodeUnlimit 生成无数量限制的小程序码（scene 携带参数），返回图片二进制
func GetWXACodeUnlimit(cfg Config, scene, page string) ([]byte, error) {
	return getWXACodeUnlimit(cfg, scene, page, true)
}

func getWXACodeUnlimit(cfg Config, scene, page string, retry bool) ([]byte, error) {
	tk, err := GetAccessToken(cfg)
	if err != nil {
		return nil, err
	}
	url := apiBase + "/wxa/getwxacodeunlimit?access_token=" + tk
	body, _ := json.Marshal(map[string]interface{}{
		"scene":       scene,
		"page":        page,
		"width":       430,
		"env_version": "release",
	})
	resp, err := http.Post(url, "application/json", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }() // 只读响应体，关闭错误无后果
	b, _ := io.ReadAll(resp.Body)
	if isImage(b) {
		return b, nil
	}
	// 失败返回 JSON {errcode, errmsg}
	var r struct {
		ErrCode int    `json:"errcode"`
		ErrMsg  string `json:"errmsg"`
	}
	_ = json.Unmarshal(b, &r)
	// access_token 失效：刷新后重试一次
	if retry && (r.ErrCode == 40001 || r.ErrCode == 42001) {
		if _, e := refreshToken(cfg); e == nil {
			return getWXACodeUnlimit(cfg, scene, page, false)
		}
	}
	return nil, fmt.Errorf("wxacode err %d: %s", r.ErrCode, r.ErrMsg)
}

// isImage 判断字节是否为 JPEG/PNG（成功时微信返回图片二进制，失败时返回 JSON）
func isImage(b []byte) bool {
	if len(b) >= 3 && bytes.Equal(b[:3], []byte("\xff\xd8\xff") /* JPEG */) {
		return true
	}
	if len(b) >= 8 && bytes.Equal(b[:8], []byte("\x89PNG\r\n\x1a\n") /* PNG */) {
		return true
	}
	return false
}

// postWX 统一的微信 JSON POST：拼 access_token、发请求、解析 errcode。
// 提取出来是因为版本发布那一组接口（提审 / 查状态 / 发布 / 撤回）都是
// 「POST + JSON in / JSON out + errcode 判成败」，各写一遍就会有哪一份
// 忘了处理 40001 重试或忘了 defer Close。
//
// retry 语义与 getWXACodeUnlimit 一致：token 失效（40001/42001）时刷新重试一次。
func postWX(path string, cfg Config, body any, retry bool) ([]byte, error) {
	tk, err := GetAccessToken(cfg)
	if err != nil {
		return nil, err
	}
	buf, _ := json.Marshal(body)
	resp, err := http.Post(apiBase+path+"?access_token="+tk, "application/json", bytes.NewReader(buf))
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }() // 只读响应体，关闭错误无后果
	b, _ := io.ReadAll(resp.Body)
	var r struct {
		ErrCode int    `json:"errcode"`
		ErrMsg  string `json:"errmsg"`
	}
	_ = json.Unmarshal(b, &r)
	if retry && (r.ErrCode == 40001 || r.ErrCode == 42001) {
		if _, e := refreshToken(cfg); e == nil {
			return postWX(path, cfg, body, false)
		}
	}
	if r.ErrCode != 0 {
		return b, &APIError{Code: r.ErrCode, Msg: r.ErrMsg}
	}
	return b, nil
}

// APIError 微信接口返回的业务错误（errcode != 0）。
// 单独一个类型是为了让调用方能 errors.As 拿到错误码——版本发布要把
// 85012（无效审核单）、86000/86001（第三方相关）这些码翻成用户看得懂的话，
// 只拿一句 errmsg 字符串是没法可靠判断的（errmsg 的措辞微信会改）。
type APIError struct {
	Code int
	Msg  string
}

func (e *APIError) Error() string { return fmt.Sprintf("wx api err %d: %s", e.Code, e.Msg) }
