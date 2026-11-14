package storage

// 本文件是「客户自有访问域名（public_base_url）」的**唯一格式裁判**。
//
// 为什么单独一个文件、且与 ResolveRef 共用同一套规则：
//   - 写入侧（后台配置表单）要校验管理员填的域名；
//   - 出参侧 ResolveRef(raw, origin) 要拿它当 origin 拼 /uploads/...；
//   - 两处各写一遍规则，迟早漂移成「后台说存成功了，端上拼出来却是带路径的
//     怪地址」，而这类错误不会报错，只会让那家租户的图全部裂开。
//
// 语义边界（容易混，务必分清）：public_base_url 是**访客侧 origin**，回答
// 「端上拿到的 URL 以哪个域名开头」；host_header / endpoint 是**回源侧**，
// 回答「nginx / 回填命令连哪台机器、Host 与 TLS SNI 填什么」。两者正交，
// 改其中一个不影响另一个。这一点由 nginxsnippet 与 refmigrate 的守门钉住。
//
// 只依赖标准库、纯函数：与 ref.go 同一条纪律（见 TestP0aRefIsPureStdlibOnly）。

import (
	"fmt"
	"net/url"
	"strings"
)

// ValidatePublicOrigin 校验并归一化客户自有访问域名。
//
//	"" / 纯空白        → ("", nil)：未启用自有域名，走平台统一入口（第一阶段的常态）
//	http(s)://host[:port] → 归一化后的 origin（去首尾空白与尾斜杠）
//	其它任何形态        → error
//
// 为什么只收纯 origin、不收带路径的基址：ResolveRef 会拼成
// `<origin>/uploads/<key>`。若允许 origin 自带路径，就会拼出
// `https://img.cust.com/prefix/uploads/...` 这种与平台统一入口不一致的地址，
// 而端上、后台、日志里都不会有任何报错——只是那家租户的图全裂。
// userinfo / query / fragment 同理：它们不是域名，却会安静地混进每一条出参 URL。
func ValidatePublicOrigin(raw string) (string, error) {
	o := strings.TrimRight(strings.TrimSpace(raw), "/")
	if o == "" {
		return "", nil
	}
	// url.Parse 对部分控制字符不报错，但它会把引号 / 空格留在 Host 里；
	// 这些字符会破坏 nginx 配置语法，也会让拼出的 URL 形态不可预期。先按字符挡掉。
	if strings.ContainsAny(o, " \t\r\n\"'\\") {
		return "", fmt.Errorf("%w: 自有访问域名含空白或引号 %q", ErrInvalidRef, raw)
	}
	u, err := url.Parse(o)
	if err != nil {
		return "", fmt.Errorf("%w: 自有访问域名解析失败 %v", ErrInvalidRef, err)
	}
	if (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return "", fmt.Errorf("%w: 自有访问域名必须是 http(s)://host[:port] 形式 %q", ErrInvalidRef, raw)
	}
	if u.Path != "" || u.RawQuery != "" || u.Fragment != "" || u.User != nil {
		return "", fmt.Errorf("%w: 自有访问域名不得带路径 / 查询 / 片段 / 用户名密码 %q", ErrInvalidRef, raw)
	}
	return u.Scheme + "://" + u.Host, nil
}
