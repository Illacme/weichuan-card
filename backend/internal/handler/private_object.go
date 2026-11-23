package handler

import (
	"log"
	"net/http"
	"strconv"
	"strings"

	"weichuan/internal/storage"

	"github.com/gin-gonic/gin"
)

// /uploads/t/<cid>/... 的私有桶读侧。
//
// 出参契约刻意不变：公开 GET 仍返回稳定的 `/uploads/t/<cid>/x.jpg`，
// 前端、小程序、海报快照、缩略图派生全部按这条路径工作，不感知签名过期。
// 私有桶的差异只在读侧：nginx 的租户 location 把这段请求交给本进程，
// 这里按 key 的归属取对应租户实例现签一条短 TTL 的 GET URL，回 302 让客户端直取。
//
// 两条不能放松的边界：
//  1. **只签 t/<cid>/**。p/ 平台对象与历史裸键走匿名静态入口，不在这里签；
//     否则一个公开的 /uploads/ 路径就能换到平台盘上任意老对象的签名地址。
//  2. **按 key 归属选实例**，不是按调用者身份。用 A 的凭据签 B 桶里的键，
//     签名对不上，端上只会 404 且没有任何报错。
//
// 这个端点不是访问控制边界：公开名片页本来就允许匿名看到这些对象。
// 它只是把「桶的匿名读」换成「服务端持有的凭据读」，因此 TTL 要短，
// 且 302 必须 no-store——让 CDN / 浏览器缓存住签名跳转等于把凭据固化下来。

var privateReadFn func(cid int64) bool

// SetPrivateReadResolver 注入「该租户是否私有桶」的运行时判定。
// 与 SetProfileOriginResolver 同构：handler 不依赖具体解析器实现，
// 未注入时一律按公开读处理，老部署与单测行为不变。
func SetPrivateReadResolver(fn func(cid int64) bool) { privateReadFn = fn }

// PrivateObjectRedirect 返回挂到 /uploads 静态路由之前的中间件。
//
// 用中间件而不是新 API 路由，是为了让外部 URL 一字不变；gin 的 /uploads/*filepath
// 通配也不能再挂一条更具体的子路由（radix tree 会直接 panic）。
func PrivateObjectRedirect(res storage.Resolver) gin.HandlerFunc {
	return func(c *gin.Context) {
		if res == nil || privateReadFn == nil {
			c.Next()
			return
		}
		m := c.Request.Method
		if m != http.MethodGet && m != http.MethodHead {
			c.Next()
			return
		}
		cid, key, ok := privateKeyFromPath(c.Request.URL.Path)
		if !ok || !privateReadFn(cid) {
			c.Next()
			return
		}

		up, err := res.For(cid)
		if err != nil {
			log.Printf("[STORAGE-PRIVATE] cid=%d resolve_failed: %v", cid, err)
			c.AbortWithStatus(http.StatusBadGateway)
			return
		}
		signer, ok := up.(storage.Signer)
		if !ok {
			// 配置写入侧已挡住 local + 私有桶；这里仍要挡手工改库的场景。
			log.Printf("[STORAGE-PRIVATE] cid=%d uploader_has_no_signer", cid)
			c.AbortWithStatus(http.StatusInternalServerError)
			return
		}
		signed, err := signer.SignGetURL(key, signTTL("sign_get_ttl_minutes", 30))
		if err != nil {
			// 错误里可能含 endpoint / bucket，只进服务端日志，不出响应。
			log.Printf("[STORAGE-PRIVATE] cid=%d sign_failed: %v", cid, err)
			c.AbortWithStatus(http.StatusBadGateway)
			return
		}
		c.Header("Cache-Control", "no-store")
		c.Header("X-Content-Type-Options", "nosniff")
		c.Header("Referrer-Policy", "strict-origin-when-cross-origin")
		c.Redirect(http.StatusFound, signed)
		c.Abort()
	}
}

// privateKeyFromPath 把 /uploads/t/7/abc.jpg 拆成 (7, "t/7/abc.jpg")。
//
// ParseRef 同时挡住路径穿越、空键、前缀不合法；再比对 cid 是为了防住
// 「路径里的 cid 与键里的 cid 不一致」这种只在手工构造 URL 时出现的错配。
func privateKeyFromPath(path string) (int64, string, bool) {
	const prefix = "/uploads/t/"
	if !strings.HasPrefix(path, prefix) {
		return 0, "", false
	}
	rest := strings.TrimPrefix(path, prefix)
	slash := strings.IndexByte(rest, '/')
	if slash <= 0 || slash == len(rest)-1 {
		return 0, "", false
	}
	cid, err := strconv.ParseInt(rest[:slash], 10, 64)
	if err != nil || cid <= 0 {
		return 0, "", false
	}
	ref, err := storage.ParseRef("t/" + rest)
	if err != nil || ref.Scope != storage.ScopeTenant || ref.CompanyID != cid {
		return 0, "", false
	}
	// 用 ParseRef 归一化后的键去签名，而不是路径原文：两者一旦不同（多余斜杠等），
	// 签出来的地址指向的对象与访客请求的不是同一个，表现为「302 了还是 404」。
	return cid, ref.ObjectKey, true
}
