package storage

import (
	"errors"
	"io"
	"time"
)

// Presigner 直传预签名能力：仅对象存储（MinIO / OSS）实现；本地磁盘无此能力。
// 客户端先向后端要一个预签名 PUT URL，再直接把文件 PUT 到对象存储，绕开应用服务器，降低带宽与存储压力。
type Presigner interface {
	// PresignPut 为对象 key 生成预签名 PUT URL（putURL）与最终可访问 URL（finalURL）。
	// contentType 用于 OSS 签名绑定（MinIO 忽略），调用方 PUT 时需带上相同 Content-Type。
	PresignPut(key, contentType string, expiry time.Duration) (putURL, finalURL string, err error)
}

// Signer 限时签名访问能力（私有桶用）：根据对象 key 生成临时可访问的 GET URL。
type Signer interface {
	// SignGetURL 为对象 key 生成有效期 expiry 的临时访问 URL。
	SignGetURL(key string, expiry time.Duration) (string, error)
}

// CDNRefresher CDN 缓存刷新能力：文件覆盖后使边缘节点旧缓存失效（避免回源拿到陈旧内容）。
type CDNRefresher interface {
	Refresh(urls []string) error
}

// MaxObjectReadBytes 单次读回对象的上限，只服务「补缩略图」这条链路。
//
// 为什么必须设：key 是客户端上报的，服务端会把它读进内存再解码成位图。
// 正常上传的图已被 ProcessUpload 压到长边 2000px（一般 < 2MB），16MB 留足余量；
// 而不设限时，一个恶意（或误操作）上报的大对象能让进程按「文件大小 × 数倍」
// 吃内存——解码后的 RGBA 通常是原文件的 10~30 倍。
const MaxObjectReadBytes = 16 << 20

// ObjectStore 对象读写能力：供「直传后补缩略图」使用。
//
// 为什么需要单独一个接口：普通上传走 Uploader.Save，字节在服务端手上，
// ProcessUpload 顺手就把 _thumb 一起写出来了。但**预签名直传**（PresignPut）
// 是浏览器直接把字节 PUT 到对象存储，服务端从头到尾没见过这份数据，
// 于是直传出来的图没有 _thumb 兄弟对象，端上每次都要先 404 再回退原图。
//
// 补的办法只能是「事后按 key 把原图读回来 → 生成缩略图 → 写回同目录」，
// 这需要读对象的能力，而 Uploader 只有写。故单列此接口，
// 由三个驱动（本地 / MinIO / OSS）各自实现；不支持读的驱动不实现即可。
type ObjectStore interface {
	// ReadObject 按 key 读回对象字节，超过 MaxObjectReadBytes 直接报错（不把超量数据读进内存）。
	// 对象不存在时返回的错误应满足 errors.Is(err, fs.ErrNotExist)（本地驱动直接透传 os.ReadFile 的错误）。
	ReadObject(key string) ([]byte, error)
	// PutObject 按 key 写入对象字节，同 key 直接覆盖。
	PutObject(key string, data []byte, contentType string) error
}

// ErrObjectTooLarge 读回对象超过 MaxObjectReadBytes。
var ErrObjectTooLarge = errors.New("storage: 对象超过可读上限")

// readLimited 读尽 rc，但最多读 MaxObjectReadBytes+1 字节：
// 多读的那 1 字节专门用来区分「刚好等于上限」与「超了」，
// 避免为了判大小先发一次 HEAD 请求（对象存储的 Stat 也是一次往返）。
func readLimited(rc io.Reader) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(rc, MaxObjectReadBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > MaxObjectReadBytes {
		return nil, ErrObjectTooLarge
	}
	return data, nil
}
