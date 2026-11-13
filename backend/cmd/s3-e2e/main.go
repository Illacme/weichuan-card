// Command s3-e2e 是「真桶端到端验收」（第六十三轮）里由 Go 负责的那一半。
//
// 它跑的就是线上那条链：一份真实配置行（凭据经 pii.Encrypt 加密）→
// uploadroute.DBLoader 读回并解密 → Resolver.For(company) 构造出 MinioUploader →
// Save 真的往桶里 PutObject → 再把对象取回来比对字节。
//
// 为什么是独立命令而不是 _test.go：
//   - 它要求**真实的 S3 端点**（tools/s3-e2e/run.sh 起 versitygw）。写成 test
//     在 CI / 开发机上只会得到一条永远 skip 的用例，反而给人「已有覆盖」的错觉。
//   - 它要把 KEY / URL / GETURL 吐给 nginx 那一跳（run.sh 读走）。test 的输出
//     不是契约，改一行 t.Log 格式就悄悄断了。
//
// 它**只做断言与取证**：不生成 snippet、不碰 nginx。混进来就说不清「红了到底
// 是谁的错」——本轮正是因为这条边界，才定位到「生成器的路径风格假设错了」而不是
// 「客户端写错了」。
package main

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/base64"
	"flag"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"os"
	"path/filepath"
	"strings"
	"time"

	"weichuan/internal/model"
	"weichuan/internal/pii"
	"weichuan/internal/storage"
	"weichuan/internal/uploadroute"

	"github.com/glebarez/sqlite"
	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// png1px 是 1×1 PNG。用图片而不是随便一段字节：Save 会先过 checkFile 的
// 魔法字节嗅探，纯文本会被自己挡掉——那样测的就不是存储链路了。
const png1pxB64 = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8BQDwAEhQGAhKmMIQAAAABJRU5ErkJggg=="

// emit 文件格式：一行一个 KEY=VALUE，由 run.sh 用 . 读进 shell 变量。
// 不用 JSON 是因为 shell 侧解析 JSON 又要多一个依赖（jq 不保证在）。
type emit struct {
	Key       string // 主对象键（经 Save 写入，带 t/<cid>/ 前缀）
	URL       string // Save 返回的公开 URL（平台统一入口 + 对象键）
	GetURL    string // 该对象的限时签名 GET（run.sh 用它取「桶里的真字节」做基准）
	PutKey    string // 预签名直传写入的对象键
	PutPubURL string // 直传后的公开 URL
}

func main() {
	var (
		endpoint   = flag.String("endpoint", "", "S3 端点 host[:port]（不带 scheme），如 127.0.0.1:19000")
		accessKey  = flag.String("access-key", "", "AccessKey")
		secretKey  = flag.String("secret-key", "", "SecretKey")
		bucket     = flag.String("bucket", "", "桶名（不存在则创建）")
		region     = flag.String("region", "", "S3 区域，留空让 SDK 自己探")
		companyID  = flag.Int64("company", 7, "租户 company_id")
		baseURL    = flag.String("base-url", "", "平台统一入口基址（Save 返回的 URL 前缀）")
		platformDi = flag.String("platform-dir", "", "平台盘目录（负例要证明它没被写）")
		useSSL     = flag.Bool("use-ssl", false, "是否 https")
		out        = flag.String("emit", "", "把结果写成 KEY=VALUE 文件（供 run.sh 读取）")
		piiKey     = flag.String("pii-key", "", "PII_ENC_KEY（base64，32 字节）；留空用 pii 内置 dev 密钥")
	)
	flag.Parse()
	if *endpoint == "" || *accessKey == "" || *secretKey == "" || *bucket == "" || *baseURL == "" {
		fail("缺少必填参数：-endpoint / -access-key / -secret-key / -bucket / -base-url")
	}
	if *platformDi == "" {
		*platformDi = filepath.Join(os.TempDir(), "s3-e2e-platform")
	}
	if *piiKey != "" {
		if err := pii.SetKey(*piiKey); err != nil {
			fail("设置 PII_ENC_KEY 失败：%v", err)
		}
	}

	ctx := context.Background()
	s3 := newClient(*endpoint, *accessKey, *secretKey, *region, *useSSL)

	// ── 1. 桶存在性 + 公开读 ───────────────────────────────────────────────
	// 公开读是第一阶段的产品边界（私有桶要走预签名，nginx 回源那一跳签不了名）。
	// 这里顺手把边界显式试一次：策略设不上就直接失败，而不是等 nginx 那一步 403
	// 再回来猜是「键错了」还是「权限错了」。
	if err := ensureBucket(ctx, s3, *bucket); err != nil {
		fail("准备桶失败：%v", err)
	}
	ok("桶 %s 就绪（公开读策略已设置）", *bucket)

	// ── 2. 配置行 → DBLoader → Resolver ────────────────────────────────────
	db := openDB()
	row := model.TenantStorageConfig{
		CompanyID: *companyID,
		Driver:    "minio",
		Endpoint:  *endpoint,
		Bucket:    *bucket,
		Region:    *region,
		UseSSL:    *useSSL,
		Enabled:   true,
	}
	var err error
	if row.AccessKeyEnc, err = pii.Encrypt(*accessKey); err != nil {
		fail("加密 AccessKey 失败：%v", err)
	}
	if row.SecretKeyEnc, err = pii.Encrypt(*secretKey); err != nil {
		fail("加密 SecretKey 失败：%v", err)
	}
	if err = db.Create(&row).Error; err != nil {
		fail("写入配置行失败：%v", err)
	}

	platform := &storage.LocalUploader{
		Dir: *platformDi, MaxBytes: 64 << 20, AllowExt: storage.DefaultAllowExt,
	}
	res := uploadroute.New(platform, "local", uploadroute.DBLoader(db, *baseURL))

	up, err := res.For(*companyID)
	if err != nil {
		fail("Resolver.For(%d) 失败：%v", *companyID, err)
	}
	if _, isMinio := up.(*storage.MinioUploader); !isMinio {
		fail("Resolver 回的不是租户实例（拿到的类型 %T）——配置行没生效", up)
	}
	ok("Resolver.For 取到租户实例（%T），驱动 %s", up, res.DriverFor(*companyID))

	// ── 3. 经 Save 真写一次 ────────────────────────────────────────────────
	// 键由 storage.ObjectKeyFor 生成，不由本工具拼——键布局的唯一真相在那边，
	// 这里拼一遍就等于把被测物抄进测试里。
	key, err := storage.ObjectKeyFor(storage.ScopeTenant, *companyID, "e2e-"+stamp()+".png")
	if err != nil {
		fail("生成对象键失败：%v", err)
	}
	fh, err := fileHeader("e2e.png", "image/png", pngBytes())
	if err != nil {
		fail("构造上传表单失败：%v", err)
	}
	url, err := up.Save(fh, key)
	if err != nil {
		fail("Save 到真桶失败：%v", err)
	}
	want := strings.TrimRight(*baseURL, "/") + "/" + key
	if url != want {
		fail("Save 返回的 URL 形态不对：得到 %q，期望 %q", url, want)
	}
	ok("Save 成功：%s（键 %s）", url, key)

	// ── 4. 桶里真的有没有这份字节 ──────────────────────────────────────────
	// 这是本轮唯一能把「从没对真 S3 端点跑过 PutObject」补掉的一步：
	// URL 拼得再对，也不代表对象在桶里。
	bucketObj, err := s3.GetObject(ctx, *bucket, key, minio.GetObjectOptions{})
	if err != nil {
		fail("从桶里读回 %s 失败：%v", key, err)
	}
	bucketBytes, err := io.ReadAll(bucketObj)
	_ = bucketObj.Close()
	if err != nil {
		fail("读桶内对象内容失败：%v", err)
	}
	if len(bucketBytes) == 0 || !bytes.HasPrefix(bucketBytes, []byte{0x89, 'P', 'N', 'G'}) {
		fail("桶内对象不是一张 PNG（%d 字节）：Save 写进去的东西不对", len(bucketBytes))
	}
	ok("桶内确有该对象：%d 字节，PNG 头正确", len(bucketBytes))

	if _, err := s3.StatObject(ctx, *bucket, key, minio.StatObjectOptions{}); err != nil {
		fail("StatObject 失败（对象键可能被改写）：%v", err)
	}

	signer, okSign := up.(storage.Signer)
	if !okSign {
		fail("租户实例未实现 Signer，无法取基准字节")
	}
	getURL, err := signer.SignGetURL(key, 15*time.Minute)
	if err != nil {
		fail("生成签名 GET URL 失败：%v", err)
	}
	same, err := fetchBytes(getURL)
	if err != nil {
		fail("用签名 URL 取对象失败：%v", err)
	}
	if !bytes.Equal(same, bucketBytes) {
		fail("签名 URL 取回的字节与桶内读取不一致（%d vs %d）", len(same), len(bucketBytes))
	}
	ok("签名 GET 与桶内对象字节一致（%d 字节）", len(same))

	// ── 5. 预签名直传（绕开应用服务器的第二条写路径）─────────────────────
	presigner, okPre := up.(storage.Presigner)
	if !okPre {
		fail("租户实例未实现 Presigner")
	}
	putKey, err := storage.ObjectKeyFor(storage.ScopeTenant, *companyID, "e2e-put-"+stamp()+".png")
	if err != nil {
		fail("生成直传对象键失败：%v", err)
	}
	putURL, putPub, err := presigner.PresignPut(putKey, "image/png", 15*time.Minute)
	if err != nil {
		fail("PresignPut 失败：%v", err)
	}
	raw := []byte("presigned-direct-put-payload-" + stamp())
	if err := putBytes(putURL, "image/png", raw); err != nil {
		fail("按预签名 URL 直传失败：%v", err)
	}
	back, err := s3.GetObject(ctx, *bucket, putKey, minio.GetObjectOptions{})
	if err != nil {
		fail("直传后从桶里读回失败：%v", err)
	}
	backBytes, _ := io.ReadAll(back)
	_ = back.Close()
	if !bytes.Equal(backBytes, raw) {
		fail("直传写入的字节不一致（桶里 %d 字节，发出 %d 字节）", len(backBytes), len(raw))
	}
	ok("预签名直传落桶成功：%s（%d 字节原样）", putKey, len(backBytes))

	// ── 6. 两个负例：坏凭据必须显式失败，不许退回平台盘 ──────────────────
	// 这是本项目一直在防的那类失败：文件悄悄落到平台盘、没有任何人报错。
	platformBefore := countFiles(*platformDi)

	// 6a. 密钥错（能构造出客户端，但签名会被拒）
	if err := db.Model(&model.TenantStorageConfig{}).Where("company_id = ?", *companyID).
		Updates(map[string]any{
			"secret_key_enc": mustEnc("wrong-secret-" + stamp()),
			"updated_at":     time.Now().Add(time.Second),
		}).Error; err != nil {
		fail("改错凭据失败：%v", err)
	}
	upBad, err := res.For(*companyID)
	if err != nil {
		fail("凭据错误时 Resolver.For 不该报错（构造不校验凭据，只校验完整性）：%v", err)
	}
	if _, err := upBad.Save(fh, key); err == nil {
		fail("凭据错误时 Save 竟然成功——签名没被校验，这条链不可信")
	} else {
		ok("凭据错误时 Save 显式失败：%v", err)
	}

	// 6b. 密文坏（PII_ENC_KEY 换过 / 被篡改）：必须 For 就失败，且文案点明不回退
	if err := db.Model(&model.TenantStorageConfig{}).Where("company_id = ?", *companyID).
		Updates(map[string]any{
			"secret_key_enc": mustEnc(*secretKey),
			"access_key_enc": "v1:not-a-real-ciphertext",
			"updated_at":     time.Now().Add(2 * time.Second),
		}).Error; err != nil {
		fail("改坏密文失败：%v", err)
	}
	if _, err := res.For(*companyID); err == nil {
		fail("密文损坏时 Resolver.For 竟然成功——解密失败被吞掉了")
	} else if !strings.Contains(err.Error(), "不回退平台存储") && !strings.Contains(err.Error(), "解密失败") {
		fail("密文损坏时的报错文案没点明原因：%v", err)
	} else {
		ok("密文损坏时显式失败并点明原因：%v", err)
	}

	if after := countFiles(*platformDi); after != platformBefore {
		fail("负例期间平台盘文件数变了（%d → %d）：发生了静默降级", platformBefore, after)
	} else {
		ok("负例全程平台盘零新增（%d 个文件）：没有静默降级", platformBefore)
	}

	// ── 7. 交付给 nginx 那一跳 ─────────────────────────────────────────────
	e := emit{Key: key, URL: url, GetURL: getURL, PutKey: putKey, PutPubURL: putPub}
	if *out != "" {
		// 值一律用单引号包住：签名 URL 里有 & 与 ?，不加引号被 shell 读走时
		// 会被当成命令分隔符（实测报 `X-Amz-Signature=...: command not found`）。
		body := fmt.Sprintf("KEY='%s'\nURL='%s'\nGETURL='%s'\nPUTKEY='%s'\nPUTPUB='%s'\n",
			e.Key, e.URL, e.GetURL, e.PutKey, e.PutPubURL)
		if err := os.WriteFile(*out, []byte(body), 0o644); err != nil {
			fail("写 emit 文件失败：%v", err)
		}
		ok("已写出 %s", *out)
	}
	fmt.Printf("KEY=%s\nURL=%s\n", e.Key, e.URL)
	ok("Go 侧全绿（真桶写入 / 读回比对 / 预签名直传 / 两个负例）")
}

// ── 小工具 ────────────────────────────────────────────────────────────────

func newClient(endpoint, ak, sk, region string, ssl bool) *minio.Client {
	c, err := minio.New(endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(ak, sk, ""),
		Secure: ssl,
		Region: region,
		Transport: &http.Transport{
			// 本装置的端点常常是自签证书（mkcerts.sh 那套），验收不该被证书链卡住。
			// 生产链路的证书校验不在这里，也不该在这里被削弱——本工具只跑本地端点。
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		},
	})
	if err != nil {
		fail("构造 S3 客户端失败：%v", err)
	}
	return c
}

func ensureBucket(ctx context.Context, c *minio.Client, bucket string) error {
	exists, err := c.BucketExists(ctx, bucket)
	if err != nil {
		return fmt.Errorf("BucketExists: %w", err)
	}
	if !exists {
		if err := c.MakeBucket(ctx, bucket, minio.MakeBucketOptions{}); err != nil {
			return fmt.Errorf("MakeBucket: %w", err)
		}
	}
	// 第一阶段只支持公开读桶：nginx 回源那一跳不带签名。
	// 策略设不上必须当场失败——等到 nginx 回 403 时，报错长得像「键写错了」。
	policy := fmt.Sprintf(`{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Principal":{"AWS":["*"]},"Action":["s3:GetObject"],"Resource":["arn:aws:s3:::%s/*"]}]}`, bucket)
	if err := c.SetBucketPolicy(ctx, bucket, policy); err != nil {
		return fmt.Errorf("SetBucketPolicy（公开读）：%w", err)
	}
	return nil
}

func openDB() *gorm.DB {
	dsn := filepath.Join(os.TempDir(), fmt.Sprintf("s3-e2e-%d.db", time.Now().UnixNano()))
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{Logger: logger.Discard})
	if err != nil {
		fail("打开 sqlite 失败：%v", err)
	}
	if err := db.AutoMigrate(&model.TenantStorageConfig{}); err != nil {
		fail("建表失败：%v", err)
	}
	return db
}

func fileHeader(name, contentType string, data []byte) (*multipart.FileHeader, error) {
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	h := make(textproto.MIMEHeader)
	h.Set("Content-Disposition", fmt.Sprintf(`form-data; name="file"; filename="%s"`, name))
	h.Set("Content-Type", contentType)
	pw, err := w.CreatePart(h)
	if err != nil {
		return nil, err
	}
	if _, err := pw.Write(data); err != nil {
		return nil, err
	}
	if err := w.Close(); err != nil {
		return nil, err
	}
	form, err := multipart.NewReader(&buf, w.Boundary()).ReadForm(int64(len(data)) + 4096)
	if err != nil {
		return nil, err
	}
	return form.File["file"][0], nil
}

func fetchBytes(u string) ([]byte, error) {
	resp, err := http.Get(u)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("GET %s → %d：%s", u, resp.StatusCode, strings.TrimSpace(string(b)))
	}
	return io.ReadAll(resp.Body)
}

func putBytes(u, contentType string, data []byte) error {
	req, err := http.NewRequest(http.MethodPut, u, bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", contentType)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode/100 != 2 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("PUT → %d：%s", resp.StatusCode, strings.TrimSpace(string(b)))
	}
	return nil
}

// countFiles 数平台盘里的文件数（负例用：证明没有东西被写到这里）。
func countFiles(dir string) int {
	n := 0
	_ = filepath.Walk(dir, func(_ string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() {
			n++
		}
		return nil
	})
	return n
}

func pngBytes() []byte {
	b, err := base64.StdEncoding.DecodeString(png1pxB64)
	if err != nil {
		fail("内置 PNG 解码失败：%v", err)
	}
	return b
}

func mustEnc(plain string) string {
	s, err := pii.Encrypt(plain)
	if err != nil {
		fail("加密失败：%v", err)
	}
	return s
}

func stamp() string { return fmt.Sprintf("%d", time.Now().UnixNano()/1000%1e9) }

func ok(format string, a ...any)   { fmt.Printf("  ✓ "+format+"\n", a...) }
func fail(format string, a ...any) { fmt.Fprintf(os.Stderr, "  ✗ "+format+"\n", a...); os.Exit(1) }
