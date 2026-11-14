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
// **接入现状（原「本文件没有接进任何 handler」的说法已过时，2026-11-13 订正）**：
// 出参侧第五十三 / 五十七轮接入（9 条公开 GET + signHandler + 后台 R11–R15），
// 写入侧第五十四轮接入带前缀键、第五十九轮接入落库前归一。接入点清单与源码级守门
// 在 ref_p0a_guard_test.go 的 TestRefWiringGate；**集成不再需要删本文件回滚**——
// 回滚写入侧归一用 STORAGE_REF_STRICT=0（见 handler/resourceref.go），
// 回滚出参侧解析才是「改回不调 ResolveRefs」。
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
// origin 是**访客侧 origin**（平台统一入口或客户自有域名），不含路径；
// 传空串则返回相对 /uploads/...（小程序 assetUrl 会拼 baseUrl()，
// 管理端 vite/nginx 直接同源可达）。
//
// 第六十九轮起「按租户选 origin」的裁判是 ResolveRefForTenant：它决定
// 「这一个引用该不该用客户域名」，本函数只负责拼接与格式校验。
// 两层分开是为了让「格式合法性」与「对象归属」各自只有一处判定，
// 端上契约不变（assetUrl 对绝对 URL 原样返回）。
//
// 三条硬规则：
//  1. 存量绝对 URL **原样返回**，绝不改写——改了老数据立刻裂图；
//  2. 空值返回空串且无错误——avatar / logo / videoUrl 等业务字段经常为空，
//     出参侧不该因为一个空值就整条记录报错；
//  3. 不吞非法输入——data: / javascript: / 路径穿越一律显式报错，
//     不静默降级成某个猜测 URL。
//
// refRel 从库里的资源引用形态提取「不含 /uploads/ 前缀」的相对对象键。
//
// ResolveRef 与 ResolveRefForTenant 共用这一段：前者要拿它拼 URL，后者要拿它判归属。
// 分成两套解析规则必然漂移，而漂移的表现是「同一张图在 A 接口走客户域名、
// 在 B 接口走平台入口」或者反过来，两端都不报错。
func refRel(s string) (string, error) {
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
	return rel, nil
}

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
	rel, err := refRel(s)
	if err != nil {
		return "", err
	}

	// origin 的格式裁判与「客户自有访问域名（public_base_url）」的写入校验是
	// 同一个函数：ValidatePublicOrigin（见 public_origin.go）。两套规则必然漂移，
	// 漂移的表现是「后台说存成功了，端上拼出来的却是带路径的怪地址」而不报错。
	base, err := ValidatePublicOrigin(origin)
	if err != nil {
		return "", err
	}
	return base + UploadsPathPrefix + rel, nil
}

// ResolveRefForTenant 是「按租户选 origin」的出参解析：只把**归属该企业的新形态
// 对象键**指向客户自有域名，其余形态继续走平台统一入口。
//
// 为什么不能无条件把 origin 传进 ResolveRef：客户域名的回源规则只覆盖
// /uploads/t/<企业id>/（见 publicBaseURLNote 与设计稿 §3.4）。而一个租户的字段里
// 真实混着三类平台对象，它们并不在客户桶里：
//   - p/...     素材库采用、小程序码、下架占位图、访客未登录时上传的头像；
//   - 无前缀裸键 第五十四轮之前上传的老对象，物理上就在平台盘 /uploads/x.jpg；
//   - /uploads/ 老相对路径（可能指向 p/ 或无前缀）。
//
// 无差别拼客户域名会让这些图在切量当天全部 404，而且后台预览因为走平台入口
// 看起来完全正常——正是「没人报错时用户无从察觉」那一类静默失败。
//
// 绝对 URL（云驱动存量 / 用户填的外链）由 ResolveRef 原样返回，不受 origin 影响；
// 归属别家租户的键（t/9/... 出现在 t/5 的字段里）也刻意回平台入口，
// 不把别家的对象指到这家的域名下。
func ResolveRefForTenant(raw, origin string, companyID int64) (string, error) {
	effective := origin
	if origin != "" && companyID > 0 {
		s := strings.TrimSpace(raw)
		if isHTTPURL(s) {
			// 存量绝对 URL / 合法外链：ResolveRef 第一条就原样返回，origin 用不上。
			effective = ""
		} else {
			rel, err := refRel(s)
			if err != nil {
				effective = ""
			} else if ref, perr := ParseRef(rel); perr != nil ||
				ref.Scope != ScopeTenant || ref.CompanyID != companyID {
				effective = ""
			}
		}
	}
	return ResolveRef(raw, effective)
}

// ObjectKeyFromURL 从存库的资源引用反推对象存储键（签名 / 回填等写入侧场景用）。
// baseURL 传当前 UploadBaseURL（云驱动绝对基址；本地驱动传空串）。
//
// 与 signHandler 旧实现（先 strings.TrimPrefix(u, base+"/")、失败再 TrimPrefix(u, "/uploads/")）
// 语义一致，但**先判 /uploads/ 再判基址**——修复了 baseURL 为空时把
// "/uploads/x.jpg" 错截成 "uploads/x.jpg"（多一层目录）的旧 bug。
// 底层复用 legacyRel，保证与 NormalizeRef 反推键的口径完全一致。
func ObjectKeyFromURL(raw, baseURL string) (string, error) {
	return legacyRel(raw, baseURL)
}

// RefKeepKind 说明 NormalizeOrKeep 对输入做了什么。
//
// 分三类而不是一个 bool，是因为「没归一」有两种完全不同的原因，处置方式也相反：
// 外链要原样留着（用户就是要外链），历史形态是待搬迁的债（P3 才处理）。
// 混成一个 bool，日志里就只能写一句「没归一」，半年后没人分得清是哪种。
type RefKeepKind string

const (
	// RefNormalized 已归一成带归属前缀的裸键（本轮唯一要写库的形态）。
	RefNormalized RefKeepKind = "normalized"
	// RefForeignURL 合法外域 URL，原样保留：video_url 的「外部 MP4 直链」档
	// 就是任意外域地址，硬归一会让正常保存失败。
	RefForeignURL RefKeepKind = "foreign-url"
	// RefLegacyPath 第五十四轮之前上传的老对象，键不带归属前缀（对象真的就在
	// /uploads/x.jpg）。**绝不能给它补前缀**：补了指向的键根本不存在，
	// 端上立刻 404，而且是「保存时静默改指向」这种最难查的错。
	// 这类数据归 P3 搬迁处理（搬对象 + 改库），不是写入侧能修的。
	RefLegacyPath RefKeepKind = "legacy-path"
)

// NormalizeOrKeep 写入侧**宽松归一**：能确认是本平台对象就收敛成裸键，
// 合法外链与历史形态原样保留，不安全的输入显式报错。
// 返回的 key 就是要写进库的字符串。
//
// 为什么不能直接用 NormalizeRef：NormalizeRef 的契约是「必须产出带前缀的键」，
// 而写入侧收到的值有三种都不该被改：
//
//   - **外域 URL**：company_profile.video_url 有一档「外部 MP4 直链」
//     （model.VideoSourceURL）就是任意外域地址；员工也可能从别处复制一个
//     CDN 链接贴进头像框。硬归一会让一次正常保存变成「保存失败」，用户只会反复重试。
//   - **无前缀的历史对象**：第五十四轮之前上传的文件键是 "x.jpg"，对象真的就在
//     /uploads/x.jpg。给它补成 t/5/x.jpg 不会报错，但出参解析会得到
//     /uploads/t/5/x.jpg —— 一个不存在的对象，端上直接裂图。
//   - **空值**：avatar / cover / logo 大量为空，出参侧 ResolveRef 已约定空值不报错，
//     两侧口径必须一致，否则「清空头像」这个操作会失败。
//
// 但宽松不等于什么都收。仍然显式失败的四种：
//
//  1. 不安全的伪协议 / 路径穿越 / 反斜杠 —— data: 、javascript: 、//evil.com 、
//     t/5/../../etc 进了 <img src> 或库字段就是实打实的注入面；
//  2. 归属不一致 —— 把 A 租户的键用 B 租户身份再存一次，被静默改写成 t/B/...
//     正是多租户最怕的串号；
//  3. **同源却推不出键** —— 基址配错时（比如接入租户自有域名后 upload_base_url
//     改了、库里还是老域名的绝对 URL）本平台的老 URL 会推不出键。若当外链放过，
//     库里形态就静默退回绝对 URL，而**出参侧看不出任何异常**（绝对 URL 本就原样
//     返回），这一轮就白做了；
//  4. 非 http(s) 也不是相对路径的怪东西。
func NormalizeOrKeep(raw, baseURL string, scope Scope, companyID int64) (string, RefKeepKind, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return "", RefNormalized, nil
	}

	// ① 已是带归属前缀的裸键（第五十四轮之后新写入的形态）。
	if ref, err := ParseRef(s); err == nil {
		return checkedScope(ref.ObjectKey, ref, scope, companyID)
	}

	// ② 剥掉 /uploads/ 或基址，拿到不含 /uploads/ 的相对部分。
	//    这一步顺带挡掉全部不安全输入（伪协议、穿越、反斜杠），
	//    所以 ② 失败后只剩「外链 or 同源异常」两种可能。
	rel, relErr := legacyRel(s, baseURL)
	if relErr == nil {
		// ②-a 剥完仍带前缀 —— 这就是本地驱动返回的 /uploads/t/5/x.jpg。
		//      这是最常见的形态（本地存储 + 云存储都走这里），必须走通。
		if ref, perr := ParseRef(rel); perr == nil {
			return checkedScope(ref.ObjectKey, ref, scope, companyID)
		}
		// ②-b 剥完不带前缀 —— 第五十四轮之前的老对象。原样保留，等 P3 搬迁。
		return s, RefLegacyPath, nil
	}

	// ③ 剥不出来：要么是合法外链，要么是同源异常。
	if !isHTTPURL(s) {
		return "", "", relErr
	}
	if sameHostAsBase(s, baseURL) {
		return "", "", fmt.Errorf("%w（与本平台基址同源，多为 upload_base_url 配错或数据形态异常）", relErr)
	}
	return s, RefForeignURL, nil
}

// checkedScope 校验归属，通过则返回可写库的键。
//
// 这里比 NormalizeRef 宽松一档：**平台对象出现在租户字段里是允许的**。
// 理由是一条真实的链路：访客在小程序申请页上传头像时还没登录，若请求没带
// companyId，上传就落在 p/ 下（见 handler.uploadCompanyID），随后
// open_apply.avatar 里存的就是 p/xxx.jpg 而 company_id 是某个租户。
// 这是既有数据形态，不是串号——对象在哪不影响「它属于谁的业务字段」。
//
// 真正要拒的是反过来的情况：A 租户的键（t/9/...）写进 B 租户的字段（t/5）。
// 那会让 B 的页面显示 A 的文件，且两端都看不出异常。
func checkedScope(key string, ref Ref, scope Scope, companyID int64) (string, RefKeepKind, error) {
	if ref.Scope == ScopePlatform {
		return key, RefNormalized, nil
	}
	if scope == ScopeTenant && ref.CompanyID == companyID {
		return key, RefNormalized, nil
	}
	return "", "", fmt.Errorf("%w: 键 %q 归属 (%s,%d)，目标 (%s,%d)",
		ErrScopeMismatch, key, ref.Scope, ref.CompanyID, scope, companyID)
}

// isHTTPURL 判断是否形如 http(s)://host/... 的合法绝对 URL。
// 只做形态判断，不保证可访问——这里不联网。
func isHTTPURL(s string) bool {
	u, err := url.Parse(s)
	if err != nil {
		return false
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return false
	}
	return u.Host != ""
}

// sameHostAsBase 判断 s 与本平台基址是否同 host。
//
// 只比 host 不比 path：基址常写成 .../uploads 或干脆是 CDN 域名，路径段在接入层
// 改过好几次；host 才是「这是不是我们自己的域名」的判据。
// baseURL 为空时恒 false——本地驱动没有绝对基址，不存在「同源」这回事。
func sameHostAsBase(s, baseURL string) bool {
	base := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if base == "" {
		return false
	}
	bu, err := url.Parse(base)
	if err != nil || bu.Host == "" {
		return false
	}
	su, err := url.Parse(s)
	if err != nil {
		return false
	}
	return strings.EqualFold(bu.Host, su.Host)
}
