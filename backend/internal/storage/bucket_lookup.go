package storage

import (
	"weichuan/internal/nginxsnippet"

	"github.com/minio/minio-go/v7"
)

// S3BucketLookup 把「桶名进 path 还是进 Host」的判定交给 nginxsnippet.Target 单点。
//
// 为什么绝不让 minio-go 自己猜（BucketLookupAuto）：Auto 只看 endpoint 是不是 IP /
// 域名，看不到 host_header 这一列。第六十三轮真桶实测（tools/s3-e2e）：nginx 侧
// 按路径风格生成 rewrite，Go 侧若按虚拟主机风格发请求，S3 会把路径第一段当成
// 桶名 → 400 InvalidBucketName(BucketName=t)，而后台那行配置看起来完全正常。
//
// 因此上传侧（uploadroute.build）与回填侧（refmigrate.NewS3Store）都必须调这里，
// 不许各自 new 一个 BucketLookupAuto 客户端。有源码级守门钉住这一点。
//
// t.CompanyID 必须是真实租户 id：Target.Validate 会校验它 > 0，还会挡掉
// 「既不是 endpoint、也不以 <bucket>. 开头」的 hostHeader（那种值判断不出风格）。
func S3BucketLookup(t nginxsnippet.Target) (minio.BucketLookupType, error) {
	if err := t.Validate(); err != nil {
		return minio.BucketLookupAuto, err
	}
	if t.PathStyle() {
		return minio.BucketLookupPath, nil
	}
	return minio.BucketLookupDNS, nil
}
