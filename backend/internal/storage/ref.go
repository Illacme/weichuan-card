package storage

import (
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

// 本文件是「多租户自有存储」P0a 的地基：一组**纯函数**，把资源引用从
// 「库里写死完整 URL」收敛成「裸对象键 + 归属」。
//
// 为什么要单独成一轮：现在业务字段里混着三种形态——云驱动的绝对 URL、
// 本地驱动的 /uploads/ 相对路径、media_check.object_key 里从 URL 反推的裸键。
// 改 upload_base_url 只影响新上传，正是这个结构造成的（backend/README.md:280）。
// 详见 docs/tenant-storage-design.md §3.1。
//
// **本文件没有接进任何 handler / repository / 出参**：P0a 阶段线上行为零变化，
// 整体删掉即可回滚。接入点清单与源码级守门在 P0b 建立；在 P0a 里
// ref_p0a_test.go 会反向钉住「生产包不许调用这些函数」，防止有人顺手接线。
//
// 这里只依赖标准库，不 import mediachk / handler / config，避免把纯函数拖进
// 依赖环。与 mediachk.ObjectKey 同构的那段「从 URL 反推键」刻意保持同语义，
// 等 P0b 接入后那段脆弱逻辑即可退休。

// Scope 资源归属范围。平台自有对象不归属任何租户，company_id 恒为 0。
type Scope string

const (
	ScopePlatform Scope = "platform" // 素材采用 / 小程序码 / 下架占位图 / 平台超管直传
	ScopeTenant   Scope = "tenant"   // 归属某个企业的上传对象
)

const (
	// PlatformKeyPrefix 平台对象键前缀，例如 p/library/cat-1.png。
	PlatformKeyPrefix = "p/"
	// TenantKeyPrefix 租户对象键前缀，例如 t/5/abcd1234.jpg。
	// 前缀里带 company_id 有两个作用：对象不互串、缓存键天然按租户隔离。
	TenantKeyPrefix = "t/"
	// UploadsPathPrefix 平台统一入口的静态路径，也是本地驱动返回的 URL 前缀。
	UploadsPathPrefix = "/uploads/"
)

var (
	ErrEmptyRef      = errors.New("storage: 资源引用为空")
	ErrInvalidRef    = errors.New("storage: 资源引用非法")
	ErrLegacyRef     = errors.New("storage: 历史形态资源引用，需先 NormalizeRef")
	ErrScopeMismatch = errors.New("storage: 资源引用归属与目标租户不一致")
)

// Ref 归一化后的资源引用：库里只写 ObjectKey，Scope/CompanyID 由键前缀携带，
// 因此从一行库记录就能还原归属，不需要额外的关联表或隐含的 JWT 上下文。
type Ref struct {
	ObjectKey string
	Scope     Scope
	CompanyID int64 // ScopePlatform 恒 0；ScopeTenant 恒 > 0
}

// String 返回应当写进业务字段的字符串（就是裸对象键，不是 URL）。
func (r Ref) String() string { return r.ObjectKey }

// cleanRel 校验并归一化「不含归属前缀」的相对对象键。
//
// 刻意**不做** path.Clean 折叠：a/b/../c 折叠成 a/c 会悄悄改变指向，而这类
// 输入本来就非法。宁可显式报错，也不能靠猜出来的键去覆盖别人的对象
// （与 mediachk.ObjectKey「推不出来就拒绝」同一个口径）。
func cleanRel(rel string) (string, error) {
	s := strings.TrimSpace(rel)
	if s == "" {
		return "", ErrEmptyRef
	}
	if strings.Contains(s, "\\") {
		return "", fmt.Errorf("%w: 含反斜杠 %q", ErrInvalidRef, rel)
	}
	if strings.ContainsAny(s, "?#") {
		return "", fmt.Errorf("%w: 对象键不得含查询串或片段 %q", ErrInvalidRef, rel)
	}
	if strings.Contains(s, "://") || strings.HasPrefix(s, "//") {
		return "", fmt.Errorf("%w: 对象键不得是 URL %q", ErrInvalidRef, rel)
	}
	for _, seg := range strings.Split(s, "/") {
		if seg == "" || seg == "." || seg == ".." {
			return "", fmt.Errorf("%w: 含空段或路径穿越 %q", ErrInvalidRef, rel)
		}
	}
	if strings.Contains(s, ":") {
		return "", fmt.Errorf("%w: 对象键不得含冒号 %q", ErrInvalidRef, rel)
	}
	return s, nil
}

// ObjectKeyFor 生成带归属前缀的对象键。
// rel 是**不含前缀**的相对路径（如 "abcd1234.jpg"、"library/cat-1.png"）。
//
// 拒绝已带 p/ 或 t/ 前缀的 rel：双重前缀（t/5/t/5/x.jpg）不会立刻报错，
// 却会让对象落到一个谁也想不到的目录里，属于典型的静默失败。
func ObjectKeyFor(scope Scope, companyID int64, rel string) (string, error) {
	r, err := cleanRel(rel)
	if err != nil {
		return "", err
	}
	if strings.HasPrefix(r, PlatformKeyPrefix) || strings.HasPrefix(r, TenantKeyPrefix) {
		return "", fmt.Errorf("%w: rel 已带归属前缀，禁止双重加前缀 %q", ErrInvalidRef, rel)
	}
	switch scope {
	case ScopePlatform:
		if companyID != 0 {
			return "", fmt.Errorf("%w: 平台对象 companyID 必须为 0，收到 %d", ErrScopeMismatch, companyID)
		}
		return PlatformKeyPrefix + r, nil
	case ScopeTenant:
		if companyID <= 0 {
			return "", fmt.Errorf("%w: 租户对象 companyID 必须 > 0，收到 %d", ErrScopeMismatch, companyID)
		}
		return TenantKeyPrefix + strconv.FormatInt(companyID, 10) + "/" + r, nil
	default:
		return "", fmt.Errorf("%w: 未知 scope %q", ErrInvalidRef, scope)
	}
}

// ParseRef 解析**新形态**对象键，从前缀还原 Scope 与 CompanyID。
// 绝对 URL、/uploads/ 相对路径、无前缀裸键都属于历史形态，返回 ErrLegacyRef
// （调用方据此走 ResolveRef 的兼容分支，而不是拿它当新键）。
func ParseRef(raw string) (Ref, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return Ref{}, ErrEmptyRef
	}
	switch {
	case strings.HasPrefix(s, PlatformKeyPrefix):
		rel, err := cleanRel(strings.TrimPrefix(s, PlatformKeyPrefix))
		if err != nil {
			return Ref{}, err
		}
		return Ref{ObjectKey: PlatformKeyPrefix + rel, Scope: ScopePlatform, CompanyID: 0}, nil
	case strings.HasPrefix(s, TenantKeyPrefix):
		rest := strings.TrimPrefix(s, TenantKeyPrefix)
		i := strings.IndexByte(rest, '/')
		if i <= 0 {
			return Ref{}, fmt.Errorf("%w: 租户键缺少 company_id 或文件名 %q", ErrInvalidRef, raw)
		}
		cid, err := strconv.ParseInt(rest[:i], 10, 64)
		if err != nil || cid <= 0 {
			return Ref{}, fmt.Errorf("%w: 租户键 company_id 非法 %q", ErrInvalidRef, raw)
		}
		rel, err := cleanRel(rest[i+1:])
		if err != nil {
			return Ref{}, err
		}
		key := TenantKeyPrefix + strconv.FormatInt(cid, 10) + "/" + rel
		return Ref{ObjectKey: key, Scope: ScopeTenant, CompanyID: cid}, nil
	default:
		return Ref{}, ErrLegacyRef
	}
}

// IsNormalizedRef 是否已是新形态对象键。
func IsNormalizedRef(raw string) bool {
	_, err := ParseRef(raw)
	return err == nil
}

// legacyRel 从历史形态反推不含前缀的相对对象键。
//
// 顺序刻意与 mediachk.ObjectKey 一致——**先处理 /uploads/ 相对路径**，再做基址
// TrimPrefix。反过来的话，BaseURL 为空时会把 "/uploads/x.jpg" 推成
// "uploads/x.jpg"（多一层目录），下架覆盖就会写错位置（mediachk.go:153-158）。
func legacyRel(raw, baseURL string) (string, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return "", ErrEmptyRef
	}
	if strings.HasPrefix(s, "/") {
		if !strings.HasPrefix(s, UploadsPathPrefix) {
			return "", fmt.Errorf("%w: 未知相对路径前缀 %q", ErrInvalidRef, raw)
		}
		rel := strings.TrimPrefix(s, UploadsPathPrefix)
		if i := strings.IndexAny(rel, "?#"); i >= 0 {
			rel = rel[:i]
		}
		return cleanRel(rel)
	}
	if !strings.Contains(s, "://") {
		// 不是绝对 URL：可能是 media_check.object_key 那一类无前缀裸键。
		// 但 javascript: / data: 这类带冒号的伪协议必须先挡掉，
		// 否则会被当成合法文件名写进对象存储。
		if i := strings.IndexByte(s, ':'); i >= 0 {
			return "", fmt.Errorf("%w: 不安全的 scheme %q", ErrInvalidRef, raw)
		}
		return cleanRel(s)
	}
	u, err := url.Parse(s)
	if err != nil {
		return "", fmt.Errorf("%w: URL 解析失败 %v", ErrInvalidRef, err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", fmt.Errorf("%w: 不支持的 scheme %q", ErrInvalidRef, u.Scheme)
	}
	if u.Host == "" {
		return "", fmt.Errorf("%w: 绝对 URL 缺少 host %q", ErrInvalidRef, raw)
	}
	base := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if base != "" && strings.HasPrefix(s, base+"/") {
		rel := strings.TrimPrefix(s, base+"/")
		// 签名 URL 带 ?X-Amz-Signature=...，不切掉会连签名一起当键名
		if i := strings.IndexAny(rel, "?#"); i >= 0 {
			rel = rel[:i]
		}
		return cleanRel(rel)
	}
	// 基址没配或对不上时，只认明确的 /uploads/ 段，绝不猜键。
	const marker = "/uploads/"
	if i := strings.Index(u.Path, marker); i >= 0 {
		return cleanRel(u.Path[i+len(marker):])
	}
	return "", fmt.Errorf("%w: 无法从 URL 推导对象键（基址不匹配且无 %s 段）%q",
		ErrInvalidRef, marker, raw)
}

// NormalizeRef 写入侧：把上传 / 采用 / 预签名返回的 URL 收敛成新形态对象键。
// baseURL 传当前 UploadBaseURL（云驱动绝对基址；本地驱动传空串即可）。
//
// 幂等：已是新形态的键不会再加前缀，但**归属必须与目标一致**。否则
// 「把 A 租户的对象用 B 租户的身份再存一次」会被静默改写成 t/B/...，
// 正是多租户最怕的串号。
func NormalizeRef(raw, baseURL string, scope Scope, companyID int64) (Ref, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return Ref{}, ErrEmptyRef
	}
	if ref, err := ParseRef(s); err == nil {
		if ref.Scope != scope || ref.CompanyID != companyID {
			return Ref{}, fmt.Errorf("%w: 键 %q 归属 (%s,%d)，目标 (%s,%d)",
				ErrScopeMismatch, s, ref.Scope, ref.CompanyID, scope, companyID)
		}
		return ref, nil
	} else if !errors.Is(err, ErrLegacyRef) {
		return Ref{}, err
	}
	rel, err := legacyRel(s, baseURL)
	if err != nil {
		return Ref{}, err
	}
	key, err := ObjectKeyFor(scope, companyID, rel)
	if err != nil {
		return Ref{}, err
	}
	return ParseRef(key) // 统一由 ParseRef 产出，保证三个字段自洽
}

// ResolveRef 读取侧：把库里的任一形态还原成端上可访问的 URL。
//
// origin 是**平台统一入口的 origin**（如 https://weichuan.ziyuanxingqiu.cn），
// 不含路径；传空串则返回相对 /uploads/...（小程序 assetUrl 会拼 baseUrl()，
// 管理端 vite/nginx 直接同源可达）。第一阶段只有一个入口，所以是单一参数；
// 将来开放客户自有域名时，按租户选 origin 的回调接在这里，端上契约不变。
//
// 三条硬规则：
//  1. 存量绝对 URL **原样返回**，绝不改写——改了老数据立刻裂图；
//  2. 空值返回空串且无错误——avatar / logo / videoUrl 等业务字段经常为空，
//     出参侧不该因为一个空值就整条记录报错；
//  3. 不吞非法输入——data: / javascript: / 路径穿越一律显式报错，
//     不静默降级成某个猜测 URL。
func ResolveRef(raw, origin string) (string, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return "", nil
	}
	if u, err := url.Parse(s); err == nil &&
		(u.Scheme == "http" || u.Scheme == "https") && u.Host != "" {
		return s, nil
	}
	if strings.Contains(s, ":") || strings.HasPrefix(s, "//") {
		return "", fmt.Errorf("%w: 不安全的引用 %q", ErrInvalidRef, raw)
	}

	var rel string
	switch {
	case strings.HasPrefix(s, UploadsPathPrefix):
		rel = strings.TrimPrefix(s, UploadsPathPrefix)
	case strings.HasPrefix(s, PlatformKeyPrefix), strings.HasPrefix(s, TenantKeyPrefix):
		ref, err := ParseRef(s)
		if err != nil {
			return "", err
		}
		rel = ref.ObjectKey
	default:
		rel = s // 存量裸键：按 /uploads/ 拼，与 LocalUploader 返回形态一致
	}
	// 带 ?v= / ?cb= 的业务字段不做静默剥离：缓存戳丢掉可能让端上拿回旧对象。
	// 宁可报错让人去修数据，也不能悄悄改写 URL。
	if _, err := cleanRel(rel); err != nil {
		return "", err
	}

	base, err := cleanOrigin(origin)
	if err != nil {
		return "", err
	}
	return base + UploadsPathPrefix + rel, nil
}

// cleanOrigin 只接受纯 origin（scheme://host[:port]），拒绝带路径 / 查询 / userinfo。
// 若允许 base 带路径，ResolveRef 会拼出 .../prefix/uploads/... 这种与统一入口
// 不一致的地址，且不会有任何报错——又是一个「没人报错时用户无从察觉」的坑。
func cleanOrigin(origin string) (string, error) {
	o := strings.TrimRight(strings.TrimSpace(origin), "/")
	if o == "" {
		return "", nil
	}
	u, err := url.Parse(o)
	if err != nil {
		return "", fmt.Errorf("%w: origin 解析失败 %v", ErrInvalidRef, err)
	}
	if (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return "", fmt.Errorf("%w: origin 必须是 http(s)://host %q", ErrInvalidRef, origin)
	}
	if u.Path != "" || u.RawQuery != "" || u.Fragment != "" || u.User != nil {
		return "", fmt.Errorf("%w: origin 不得带路径 / 查询 / 片段 / userinfo %q", ErrInvalidRef, origin)
	}
	return u.Scheme + "://" + u.Host, nil
}
