package wechat

import (
	"encoding/json"
	"errors"
	"strings"
)

// 图片 / 音频内容安全（/wxa/media_check_async）。
//
// 与文本的关键差别：**这个接口没有同步结论**。调一次只拿到一个 trace_id，
// 真正的判定结果由微信通过「消息推送」回调回来（见 msgpush.go）。
// 所以本文件只负责「把任务送出去」，结论落库在 handler 层完成。
//
// 为什么不能只做文本检测：小程序提审时审核员会点开上传入口自己传一张图试试，
// 只接了文本等于「图片通道完全没检测」。而图片恰恰是 UGC 里最容易出事的部分。
//
// 一个绕不过去的约束：media_url 必须是**微信服务器能访问到的公网 URL**。
// 本地部署（http://127.0.0.1:8080/uploads/x.png）送过去只会得到参数错误，
// 所以 mediachk.AbsURL 会先判定可达性，不可达时直接跳过并记日志——
// 让「本地开发」表现为「不检测」，而不是「检测失败刷满日志」。

// 媒体类型（微信口径）。只有音频与图片，视频不在这个接口范围内。
const (
	MediaAudio = 1 // 音频
	MediaImage = 2 // 图片
)

// MediaCheckReq 一次异步送检的参数。
type MediaCheckReq struct {
	MediaURL  string // 公网可访问的媒体地址
	MediaType int    // MediaImage / MediaAudio
	OpenID    string // 提交人 openid（v2 必填）
	Scene     int    // 场景，缺省按「资料」
}

// MediaCheckAsync 提交一次异步内容安全检测，返回微信侧的 trace_id。
//
// trace_id 是**唯一能把回调结论对回这张图**的凭据（回调里只带它），
// 所以调用方必须先把 trace_id 落库再返回，不能只在日志里留一份。
func MediaCheckAsync(cfg Config, req MediaCheckReq) (string, error) {
	url := strings.TrimSpace(req.MediaURL)
	if url == "" {
		// 空 URL 打过去只会拿到参数错误，白耗一次 token 获取与外网往返。
		return "", errors.New("缺少 media_url，无法提交图片检测")
	}
	if req.OpenID == "" {
		// 与文本检测同一条规矩：v2 的 openid 必填，空着打过去必然是参数错误。
		return "", errors.New("缺少 openid，无法调用 v2 内容安全接口")
	}
	if req.MediaType != MediaImage && req.MediaType != MediaAudio {
		return "", errors.New("media_type 只能是 1（音频）或 2（图片）")
	}
	scene := req.Scene
	if scene <= 0 {
		scene = SceneProfile
	}
	body := map[string]interface{}{
		"media_url":  url,
		"media_type": req.MediaType,
		"version":    2,
		"scene":      scene,
		"openid":     req.OpenID,
	}
	b, err := postWX("/wxa/media_check_async", cfg, body, true)
	if err != nil {
		return "", err
	}
	var r struct {
		ErrCode int    `json:"errcode"`
		ErrMsg  string `json:"errmsg"`
		TraceID string `json:"trace_id"`
	}
	if err := json.Unmarshal(b, &r); err != nil {
		return "", err
	}
	if r.TraceID == "" {
		// errcode==0 但没有 trace_id 是「接口降级」的形态：没有 trace_id 就永远
		// 等不到回调，这条检测记录会永久停在 pending。当成失败上报，
		// 让调用方把它标成 failed，而不是留一条看起来还在跑的假记录。
		return "", errors.New("微信未返回 trace_id，无法追踪检测结论")
	}
	return r.TraceID, nil
}
