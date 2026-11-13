package handler

// 写入侧资源引用归一的落地层（第五十九轮）。
//
// storage.NormalizeOrKeep 是纯函数，拿不到 cfg、不知道该不该写日志、也不知道
// 「这次写入是哪个租户」。这一层只做四件事：
//  1. 读 cfg.UploadBaseURL（云驱动剥前缀要用）；
//  2. 按 companyID 推 scope（0 = 平台自身）；
//  3. 把「没归一」的两种情况记下来（外链 / 历史形态）——不记日志的话，半年后
//     没人知道库里那些旧形态是哪来的；
//  4. env 回滚开关。
//
// 为什么不把归一放进模型的 BeforeSave 钩子：
//   - 钩子拿不到 scope/companyID（要靠 context 传，而 context 在 gorm 里是隐形状态）；
//   - 钩子会连**回填脚本 / 内部搬运**一起改写，而那些场景恰恰需要按原值写；
//   - album 是 json 字符串列，钩子要解 JSON 再拼回去，等于把出参侧的展开逻辑
//     复制一份到写入侧，两处必然漂移。
// 所以接在 handler 的**落库前**，一处一行，守住「哪些点必须调」靠源码级守门
// （TestResourceRefWriteSitesNormalize）。

import (
	"fmt"
	"log"
	"net/url"
	"os"
	"strings"

	"weichuan/internal/config"
	"weichuan/internal/storage"
)

// storageRefStrictEnv 控制写入侧是否做落库前归一。
//
// 默认**开**（未设即生效）；设成 0 / false 退回第五十八轮的行为：收到什么写什么。
//
// 为什么「默认开 + 可关」而不是「默认关 + 可开」：收敛不默认生效等于代码写了但
// 债没还，而回滚窗口只要「一个 env 能整体退回」就够了。关掉之后出参侧解析照常工作
// （ResolveRef 本来就兼容三种历史形态），所以这次回滚是安全的、不是断崖。
//
// 每次调用读一次 env：handler 是请求级函数，这个开销可忽略；换成包级变量加 init
// 会让测试间的 env 泄漏（t.Setenv 改了不生效，或改了一次污染后续用例）。
const storageRefStrictEnv = "STORAGE_REF_STRICT"

func storageRefStrict() bool {
	v := strings.TrimSpace(os.Getenv(storageRefStrictEnv))
	if v == "" {
		return true
	}
	return v != "0" && !strings.EqualFold(v, "false")
}

// scopeOf 由 companyID 推归属：0 是平台自身对象（素材库 / 小程序码 / 平台超管直传）。
func scopeOf(companyID int64) storage.Scope {
	if companyID == 0 {
		return storage.ScopePlatform
	}
	return storage.ScopeTenant
}

// normalizeResourceRef 把表单回传的资源字段归一，返回应当写库的值。
//
// field 只用于日志（例如 "company-info.logo"），不参与任何逻辑——写日志时能一眼
// 看出是哪个接口的哪个字段，比只打 companyID 有用得多。
//
// 返回错误时调用方必须**放弃本次写入**并回错误：这一步失败意味着收到的值和
// 「这个租户的合法资源」对不上（别的企业的键、伪协议、路径穿越），静默写进去
// 比报错难查得多。
func normalizeResourceRef(cfg config.Config, companyID int64, field, raw string) (string, error) {
	if !storageRefStrict() {
		return raw, nil // 回滚模式：第五十八轮及以前的旧行为
	}
	key, kind, err := storage.NormalizeOrKeep(raw, cfg.UploadBaseURL, scopeOf(companyID), companyID)
	if err != nil {
		return "", err
	}
	noteRefKept(companyID, field, raw, kind)
	return key, nil
}

// normalizeResourceRefs 是 album 这类字符串数组的版本。
//
// nil / 空切片原样返回（**不**把 nil 变成空切片）：出参侧 model.ResolveResourceRefs
// 也是这个约定，两侧一致才不会出现「存的时候是 null、读出来变 []」的契约漂移。
func normalizeResourceRefs(cfg config.Config, companyID int64, field string, list []string) ([]string, error) {
	if len(list) == 0 {
		return list, nil
	}
	out := make([]string, len(list))
	for i, raw := range list {
		k, err := normalizeResourceRef(cfg, companyID, fmt.Sprintf("%s[%d]", field, i), raw)
		if err != nil {
			return nil, err
		}
		out[i] = k
	}
	return out, nil
}

// noteRefKept 记录「这个字段没被归一」的两种情况。
//
// 外链用 WARN：它意味着这个字段将来搬存储时带不走（外链的对象不在我们这边）。
// 历史形态用 WARN 也说得过去，但它是**已知的、待 P3 搬迁处理的债**，量会很大
// （每个用了第五十四轮之前老文件的企业每次保存都会命中），所以只记 host/数量级信息，
// 不逐条刷屏也没意义——真正要盯的是外链。
func noteRefKept(companyID int64, field, raw string, kind storage.RefKeepKind) {
	switch kind {
	case storage.RefForeignURL:
		// 只记 host，不记完整 URL：外链常带签名 / token（视频 CDN 尤其常见），
		// 日志一泄就是别人的凭证。
		log.Printf("[REF][WARN] %s 存的是外链（未归一，搬存储时带不走）company=%d host=%s",
			field, companyID, urlHost(raw))
	case storage.RefLegacyPath:
		log.Printf("[REF][INFO] %s 是无归属前缀的历史对象（待 P3 搬迁）company=%d",
			field, companyID)
	}
}

// urlHost 尽力取出 host；取不到就返回 "-"（不把原文打出去）。
func urlHost(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" {
		return "-"
	}
	return u.Host
}
