// Command backfill-e2e-prep 是 tools/backfill-e2e 灰度装置的**取证准备**工具。
//
// 它不属于生产二进制，只做三件 shell 做不了的事：
//   - upsert：建真桶、清掉上一轮遗留的 t/<cid>/ 对象、把 tenant_storage_configs
//     配置行用 pii.Encrypt 写进真 MySQL（凭据密文没法用 SQL 拼）；
//   - delete：删掉配置行，用来验「缺配置时回填必须显式失败、不回退平台盘」；
//   - stat：用**签名 GET** 从真桶把对象读回来，输出 size 与 sha256。
//     curl 不会签 SigV4，直接从 POSIX 后端目录读又证明不了「服务端真的认这个对象」。
//
// 为什么是独立命令而不是 _test.go：它要求一个正在跑的真 S3 端点与真 MySQL，
// 写进 test 只会得到一条永远 skip 的用例，反而给人「已有覆盖」的错觉
// （与 cmd/s3-e2e 同一条理由）。
package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"weichuan/internal/dbopen"
	"weichuan/internal/model"
	"weichuan/internal/nginxsnippet"
	"weichuan/internal/pii"
	"weichuan/internal/storage"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

func fail(format string, a ...any) {
	fmt.Fprintf(os.Stderr, "✗ "+format+"\n", a...)
	os.Exit(1)
}

func main() {
	var (
		mode     = flag.String("mode", "upsert", "upsert / delete / stat")
		dsn      = flag.String("dsn", "", "真 MySQL DSN")
		endpoint = flag.String("endpoint", "127.0.0.1:19000", "S3 端点 host[:port]")
		ak       = flag.String("access-key", "testuser", "AccessKey")
		sk       = flag.String("secret-key", "testsecret", "SecretKey")
		bucket   = flag.String("bucket", "wc-t7", "桶名")
		cid      = flag.Int64("company", 7, "租户 company_id")
		key      = flag.String("key", "", "stat 模式要读的对象键")
		clean    = flag.Bool("clean", true, "upsert 时是否清掉桶里 t/<cid>/ 的旧对象")
	)
	flag.Parse()

	if pii.UsingPlaceholderKey() {
		fail("必须显式设置 %s（与 storage-backfill 用的是同一把钥匙）", pii.KeyEnvVar)
	}
	if err := pii.KeyError(); err != nil {
		fail("密钥不合法：%v", err)
	}

	lookup, err := storage.S3BucketLookup(nginxsnippet.Target{
		CompanyID: *cid, Driver: nginxsnippet.DriverMinio,
		Endpoint: *endpoint, Bucket: *bucket, Enabled: true,
	})
	if err != nil {
		fail("路径风格判定失败：%v", err)
	}
	client, err := minio.New(*endpoint, &minio.Options{
		Creds:        credentials.NewStaticV4(*ak, *sk, ""),
		Region:       "us-east-1",
		BucketLookup: lookup,
	})
	if err != nil {
		fail("构造 S3 客户端失败：%v", err)
	}
	ctx := context.Background()

	switch *mode {
	case "stat":
		statObject(ctx, client, *bucket, *key)
		return
	case "delete":
		// 不碰桶里的对象：R5 要求回滚后对象保留，负例只撤配置。
	case "upsert":
		prepare(ctx, client, *dsn, *ak, *sk, *endpoint, *bucket, *cid, *clean)
		return
	default:
		fail("未知 -mode=%q", *mode)
	}
	db, _, err := dbopen.Open(*dsn)
	if err != nil {
		fail("连接数据库失败：%v", err)
	}
	if err := db.Where("company_id = ?", *cid).Delete(&model.TenantStorageConfig{}).Error; err != nil {
		fail("删除配置行失败：%v", err)
	}
	fmt.Println("已删除 company_id 的配置行")
}

func prepare(ctx context.Context, client *minio.Client, dsn, ak, sk, endpoint, bucket string, cid int64, clean bool) {
	if dsn == "" {
		fail("upsert 必须给 -dsn")
	}
	exists, err := client.BucketExists(ctx, bucket)
	if err != nil {
		fail("探测桶失败：%v", err)
	}
	if !exists {
		if err := client.MakeBucket(ctx, bucket, minio.MakeBucketOptions{}); err != nil {
			fail("建桶失败：%v", err)
		}
	}
	// 每次从干净态开始，否则「重跑跳过 1 个对象」可能只是上一次留下的。
	// 负例阶段要接着验「对象仍在（R5）」，那时传 -clean=false 保住现场。
	prefix := fmt.Sprintf("t/%d/", cid)
	var stale []string
	if clean {
		for obj := range client.ListObjects(ctx, bucket, minio.ListObjectsOptions{Prefix: prefix, Recursive: true}) {
			if obj.Err != nil {
				fail("列举桶对象失败：%v", obj.Err)
			}
			stale = append(stale, obj.Key)
		}
	}
	for _, k := range stale {
		if err := client.RemoveObject(ctx, bucket, k, minio.RemoveObjectOptions{}); err != nil {
			fail("清理旧对象 %q 失败：%v", k, err)
		}
	}

	akEnc, err := pii.Encrypt(ak)
	if err != nil {
		fail("加密 AccessKey 失败：%v", err)
	}
	skEnc, err := pii.Encrypt(sk)
	if err != nil {
		fail("加密 SecretKey 失败：%v", err)
	}
	db, _, err := dbopen.Open(dsn)
	if err != nil {
		fail("连接数据库失败：%v", err)
	}
	if err := db.Where("company_id = ?", cid).Delete(&model.TenantStorageConfig{}).Error; err != nil {
		fail("清掉旧配置行失败：%v", err)
	}
	now := time.Now()
	row := model.TenantStorageConfig{
		CompanyID: cid, Driver: nginxsnippet.DriverMinio,
		Endpoint: endpoint, Bucket: bucket, Region: "us-east-1",
		AccessKeyEnc: akEnc, SecretKeyEnc: skEnc,
		Enabled: true, Status: "ok", PublicRead: true,
		CreatedAt: now, UpdatedAt: now,
	}
	if err := db.Create(&row).Error; err != nil {
		fail("写配置行失败：%v", err)
	}
	fmt.Printf("桶 %s 就绪；company_id=%d 配置行已写入（清理了 %d 个旧对象）\n", bucket, cid, len(stale))
}

func statObject(ctx context.Context, client *minio.Client, bucket, key string) {
	if key == "" {
		fail("stat 必须给 -key")
	}
	obj, err := client.GetObject(ctx, bucket, key, minio.GetObjectOptions{})
	if err != nil {
		fail("GetObject 失败：%v", err)
	}
	defer func() { _ = obj.Close() }()
	data, err := io.ReadAll(obj)
	if err != nil {
		resp := minio.ToErrorResponse(err)
		if resp.Code == "NoSuchKey" || resp.StatusCode == 404 {
			fmt.Println("MISSING=1")
			return
		}
		fail("读取对象失败：%v", err)
	}
	sum := sha256.Sum256(data)
	fmt.Printf("SIZE=%d\nSHA256=%s\n", len(data), hex.EncodeToString(sum[:]))
}
