package handler

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"weichuan/internal/auth"
	"weichuan/internal/config"
	"weichuan/internal/model"
	"weichuan/internal/pii"
	"weichuan/internal/storage"
	"weichuan/internal/wechat"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// 图片内容安全的**接线**测试：策略在 mediachk 包里测、解密在 wechat 包里测，
// 这里只验证三件事真的连上了：
//  1. 上传图片后确实发起了送检，并落了一条带 trace_id 的 pending 记录；
//  2. 微信推送回来时确实按 trace_id 更新了结论（明文 + 安全模式两条路）；
//  3. 后台一键下架确实把对象覆盖成了占位图，且只对「命中违规」的记录生效。
//
// 少了这一层，每个零件都对，整条链路照样可能一条结论都落不了库。

// setupMediaEngine 与 setupEngineWithDB 同构，但上传基址指向**公网可达**地址：
// mediachk.Plan 对 127.0.0.1 / 私有网段一律按 not_public 跳过（微信访问不到），
// 用默认的 http://127.0.0.1:8080/uploads 会让这条链路在测试里根本不触发。
func setupMediaEngine(t *testing.T) (*gin.Engine, *gorm.DB, config.Config) {
	t.Helper()

	tmpDB, err := os.CreateTemp("", "weichuan-mc-*.db")
	require.NoError(t, err)
	dbPath := tmpDB.Name()
	require.NoError(t, tmpDB.Close())
	t.Cleanup(func() { os.Remove(dbPath) })

	db, err := gorm.Open(sqlite.Open(dbPath), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(model.All()...))
	require.NoError(t, db.Create(&model.Company{ID: 1, Name: "朗汇", ShortName: "朗汇", Status: "enabled"}).Error)

	uploadDir, err := os.MkdirTemp("", "weichuan-mc-upload-")
	require.NoError(t, err)
	t.Cleanup(func() { os.RemoveAll(uploadDir) })

	cfg := config.Config{
		UploadDir:     uploadDir,
		UploadBaseURL: "https://card.example.com/uploads",
		StorageDriver: "local",
		StoragePublic: true,
		JWTKey:        "test-jwt-key",
		AdminUser:     "admin",
		AdminPass:     "admin123456",
	}
	up := &storage.LocalUploader{
		Dir:      cfg.UploadDir,
		BaseURL:  cfg.UploadBaseURL,
		MaxBytes: 10 * 1024 * 1024,
		AllowExt: storage.DefaultAllowExt,
	}
	var refresher storage.CDNRefresher
	return buildEngine(t, db, cfg, up, refresher), db, cfg
}

// stubMediaCheck 替换送检接口，返回调用计数。
// 不替换就只能打真微信接口——那等于「这条链路永远没有测试」。
func stubMediaCheck(t *testing.T, traceID string, err error) *int32 {
	t.Helper()
	var n int32
	old := mediaCheckAsync
	mediaCheckAsync = func(_ wechat.Config, _ wechat.MediaCheckReq) (string, error) {
		atomic.AddInt32(&n, 1)
		return traceID, err
	}
	t.Cleanup(func() { mediaCheckAsync = old })
	return &n
}

// configureMediaCheck 通过后台设置接口写入小程序凭据 + 消息推送 Token。
func configureMediaCheck(t *testing.T, c *testClient, adminToken string) {
	t.Helper()
	_, body := c.do("PUT", "/api/admin/settings", map[string]string{
		"wx_appid":     "wx-media-appid",
		"wx_appsecret": "wx-media-secret",
		"wx_msg_token": "push-token",
	}, adminToken)
	require.EqualValues(t, 0, body["code"], "写入凭据失败：%v", body)
}

// uploadImage 走真实上传接口传一张 PNG，返回服务端给的 URL。
func uploadImage(t *testing.T, r *gin.Engine, token string) []string {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	part, err := mw.CreateFormFile("file", "a.png")
	require.NoError(t, err)
	_, _ = part.Write([]byte("\x89PNG\r\n\x1a\n"))
	require.NoError(t, mw.Close())

	req := httptest.NewRequest("POST", "/api/employee/upload", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)

	var resp struct {
		Code int `json:"code"`
		Data struct {
			URLs []string `json:"urls"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.EqualValues(t, 0, resp.Code)
	require.NotEmpty(t, resp.Data.URLs)
	return resp.Data.URLs
}

// employeeWithOpenID 建一个员工并补上 openid（v2 检测的必填项），返回登录令牌。
func employeeWithOpenID(t *testing.T, db *gorm.DB, c *testClient) string {
	t.Helper()
	token := loginEmployee(t, c)
	require.NoError(t, db.Model(&model.Employee{}).
		Where("phone_bidx = ?", pii.BlindIndex("13900000999")).Update("openid", "openid-emp").Error)
	return token
}

// 上传图片 → 真的发起了送检，并落一条带 trace_id 的 pending 记录。
func TestImageUploadSubmitsMediaCheck(t *testing.T) {
	calls := stubMediaCheck(t, "trace-img-1", nil)
	r, db, _ := setupMediaEngine(t)
	c := newTestClient(r, t)
	adminToken := loginAdmin(t, c)
	configureMediaCheck(t, c, adminToken)
	empToken := employeeWithOpenID(t, db, c)

	urls := uploadImage(t, r, empToken)
	require.NotEmpty(t, urls)

	var row model.MediaCheck
	require.Eventually(t, func() bool {
		return db.First(&row).Error == nil
	}, 3*time.Second, 20*time.Millisecond, "上传后应落一条检测记录")

	require.EqualValues(t, 1, atomic.LoadInt32(calls))
	require.Equal(t, "trace-img-1", row.TraceID)
	require.Equal(t, model.MediaCheckPending, row.Status)
	require.EqualValues(t, 1, row.CompanyID)
	require.Equal(t, "employee", row.Source)
	require.Equal(t, "openid-emp", row.OpenID)
	require.Contains(t, row.URL, "/uploads/")
	require.NotEmpty(t, row.ObjectKey, "对象键必须在送检时就落下来，下架时才有依据")
}

// 没配消息推送 Token 时**不送检**：送出去也收不到结论，
// 只会造出一条永远 pending 的假记录，比不送更容易误导人。
func TestImageUploadSkipsWithoutPushToken(t *testing.T) {
	calls := stubMediaCheck(t, "trace-x", nil)
	r, db, _ := setupMediaEngine(t)
	c := newTestClient(r, t)
	adminToken := loginAdmin(t, c)
	// 只配小程序凭据，不配 wx_msg_token
	_, body := c.do("PUT", "/api/admin/settings", map[string]string{
		"wx_appid":     "wx-media-appid",
		"wx_appsecret": "wx-media-secret",
	}, adminToken)
	require.EqualValues(t, 0, body["code"])
	empToken := employeeWithOpenID(t, db, c)

	uploadImage(t, r, empToken)

	var n int64
	require.NoError(t, db.Model(&model.MediaCheck{}).Count(&n).Error)
	require.EqualValues(t, 0, n, "没有推送配置时不应落 pending 记录")
	require.EqualValues(t, 0, atomic.LoadInt32(calls), "没有推送配置时不应调送检接口")
}

// 送检失败也要留一条 failed 记录：图确实传上去了、也确实没检过，
// 只写日志的话日志会被轮转掉，后台看不到「有一批图没检」。
func TestImageUploadRecordsSendFailure(t *testing.T) {
	stubMediaCheck(t, "", os.ErrDeadlineExceeded)
	r, db, _ := setupMediaEngine(t)
	c := newTestClient(r, t)
	adminToken := loginAdmin(t, c)
	configureMediaCheck(t, c, adminToken)
	empToken := employeeWithOpenID(t, db, c)

	uploadImage(t, r, empToken)

	var row model.MediaCheck
	require.Eventually(t, func() bool {
		return db.First(&row).Error == nil
	}, 3*time.Second, 20*time.Millisecond)
	require.Equal(t, model.MediaCheckFailed, row.Status)
	require.Contains(t, row.Note, "send_failed")
}

// ---- 消息推送回调 ----

func pushQuery(token, ts, nonce string, extra ...string) string {
	q := "timestamp=" + ts + "&nonce=" + nonce +
		"&signature=" + wechat.Signature(token, ts, nonce)
	if len(extra) > 0 {
		q += "&encrypt_type=aes&msg_signature=" + wechat.Signature(token, ts, nonce, extra...)
	}
	return q
}

func postPush(t *testing.T, r *gin.Engine, query, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest("POST", "/api/wechat/msgpush?"+query, bytes.NewBufferString(body))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

// 微信配置推送 URL 时的校验：验签通过原样回 echostr。
func TestMsgPushEcho(t *testing.T) {
	r, _, _ := setupMediaEngine(t)
	c := newTestClient(r, t)
	configureMediaCheck(t, c, loginAdmin(t, c))

	ok := pushQuery("push-token", "1700000000", "n1")
	req := httptest.NewRequest("GET", "/api/wechat/msgpush?"+ok+"&echostr=hello-wx", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)
	require.Equal(t, "hello-wx", w.Body.String())

	// 签名不对必须拒绝：否则任何人都能拿这个端点做回显探测器
	req = httptest.NewRequest("GET", "/api/wechat/msgpush?timestamp=1&nonce=2&signature=deadbeef&echostr=x", nil)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusForbidden, w.Code)
	require.NotContains(t, w.Body.String(), "hello-wx", "验签失败绝不能回显 echostr")
}

const mediaCheckXML = `<xml>
<ToUserName><![CDATA[gh_1]]></ToUserName>
<FromUserName><![CDATA[o_emp]]></FromUserName>
<MsgType><![CDATA[event]]></MsgType>
<Event><![CDATA[wxa_media_check]]></Event>
<appid><![CDATA[wx-media-appid]]></appid>
<trace_id><![CDATA[trace-img-1]]></trace_id>
<version>2</version>
<result><![CDATA[{"suggest":"risky","label":20001}]]></result>
<errcode>0</errcode>
<status_code>2</status_code>
</xml>`

func seedCheckRow(t *testing.T, db *gorm.DB, status model.MediaCheckStatus, key string) model.MediaCheck {
	t.Helper()
	row := model.MediaCheck{
		CompanyID: 1,
		URL:       "https://card.example.com/uploads/" + key,
		ObjectKey: key,
		MediaType: wechat.MediaImage,
		Scene:     wechat.SceneProfile,
		Source:    "employee",
		TraceID:   "trace-img-1",
		Status:    status,
	}
	require.NoError(t, db.Create(&row).Error)
	return row
}

// 明文模式：按 trace_id 把结论写回记录。
func TestMsgPushPlainUpdatesStatus(t *testing.T) {
	r, db, _ := setupMediaEngine(t)
	c := newTestClient(r, t)
	configureMediaCheck(t, c, loginAdmin(t, c))
	row := seedCheckRow(t, db, model.MediaCheckPending, "2026/10/a.jpg")

	w := postPush(t, r, pushQuery("push-token", "1700000000", "n1"), mediaCheckXML)
	require.Equal(t, http.StatusOK, w.Code)
	require.Equal(t, "success", w.Body.String())

	var got model.MediaCheck
	require.NoError(t, db.First(&got, row.ID).Error)
	require.Equal(t, model.MediaCheckRisky, got.Status)
	require.Equal(t, 20001, got.Label)
	require.Equal(t, "risky", got.Suggest)
	require.Contains(t, got.Raw, "20001", "微信原文要留档，申诉时用的就是它")
}

// 签名不对 → 403 且**不改库**：这是唯一能证明「请求真来自微信」的东西。
func TestMsgPushRejectsBadSignature(t *testing.T) {
	r, db, _ := setupMediaEngine(t)
	c := newTestClient(r, t)
	configureMediaCheck(t, c, loginAdmin(t, c))
	row := seedCheckRow(t, db, model.MediaCheckPending, "2026/10/a.jpg")

	w := postPush(t, r, "timestamp=1&nonce=2&signature=deadbeef", mediaCheckXML)
	require.Equal(t, http.StatusForbidden, w.Code)

	var got model.MediaCheck
	require.NoError(t, db.First(&got, row.ID).Error)
	require.Equal(t, model.MediaCheckPending, got.Status)
}

// 推送 URL 是独占的：关注/扫码等事件也会推过来，必须回 success（否则微信会一直重推）。
func TestMsgPushIgnoresOtherEvents(t *testing.T) {
	r, _, _ := setupMediaEngine(t)
	c := newTestClient(r, t)
	configureMediaCheck(t, c, loginAdmin(t, c))

	w := postPush(t, r, pushQuery("push-token", "1700000000", "n1"),
		`<xml><Event>subscribe</Event></xml>`)
	require.Equal(t, http.StatusOK, w.Code)
	require.Equal(t, "success", w.Body.String())
}

// 已确认违规的记录不能被后续推送降级成 pass：
// 那条结论正是下架的依据，被冲掉就等于「违规图又变成没问题了」。
func TestMsgPushRefusesDowngrade(t *testing.T) {
	r, db, _ := setupMediaEngine(t)
	c := newTestClient(r, t)
	configureMediaCheck(t, c, loginAdmin(t, c))
	row := seedCheckRow(t, db, model.MediaCheckRisky, "2026/10/a.jpg")

	w := postPush(t, r, pushQuery("push-token", "1700000000", "n1"),
		`<xml><Event>wxa_media_check</Event><trace_id>trace-img-1</trace_id>
<result><![CDATA[{"suggest":"pass","label":100}]]></result><status_code>2</status_code></xml>`)
	require.Equal(t, http.StatusOK, w.Code)

	var got model.MediaCheck
	require.NoError(t, db.First(&got, row.ID).Error)
	require.Equal(t, model.MediaCheckRisky, got.Status, "不允许从 risky 降级")
}

// 安全模式：body 是密文，需验 msg_signature + AES 解密后才能拿到结论。
func TestMsgPushEncryptedUpdatesStatus(t *testing.T) {
	const aesKey = "abcdefghijklmnopqrstuvwxyz0123456789ABCDEFG"
	r, db, _ := setupMediaEngine(t)
	c := newTestClient(r, t)
	_, body := c.do("PUT", "/api/admin/settings", map[string]string{
		"wx_appid":      "wx-media-appid",
		"wx_appsecret":  "wx-media-secret",
		"wx_msg_token":  "push-token",
		"wx_msg_aeskey": aesKey,
	}, loginAdmin(t, c))
	require.EqualValues(t, 0, body["code"])
	row := seedCheckRow(t, db, model.MediaCheckPending, "2026/10/a.jpg")

	enc := encryptPushForTest(t, aesKey, mediaCheckXML, "wx-media-appid")
	w := postPush(t, r, pushQuery("push-token", "1700000000", "n1", enc),
		"<xml><Encrypt><![CDATA["+enc+"]]></Encrypt></xml>")
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())

	var got model.MediaCheck
	require.NoError(t, db.First(&got, row.ID).Error)
	require.Equal(t, model.MediaCheckRisky, got.Status)
}

// 安全模式但没配 EncodingAESKey：明确回 503（让运维知道该去配什么），
// 而不是回 200 把这条推送「当成处理过了」丢掉。
func TestMsgPushEncryptedWithoutKeyFails(t *testing.T) {
	r, db, _ := setupMediaEngine(t)
	c := newTestClient(r, t)
	configureMediaCheck(t, c, loginAdmin(t, c)) // 只配 token，不配 aeskey
	row := seedCheckRow(t, db, model.MediaCheckPending, "2026/10/a.jpg")

	w := postPush(t, r, pushQuery("push-token", "1700000000", "n1", "CIPHER"),
		"<xml><Encrypt><![CDATA[CIPHER]]></Encrypt></xml>")
	require.Equal(t, http.StatusServiceUnavailable, w.Code)

	var got model.MediaCheck
	require.NoError(t, db.First(&got, row.ID).Error)
	require.Equal(t, model.MediaCheckPending, got.Status)
}

// encryptPushForTest 按微信报文结构造密文（随机串 + 长度 + 消息 + appid），32 字节块填充。
// 刻意不复用 wechat 包里的解密实现——独立实现才能让 DecryptMsg 的 bug 暴露出来。
func encryptPushForTest(t *testing.T, encodingAESKey, msg, appID string) string {
	t.Helper()
	key, err := base64.StdEncoding.DecodeString(encodingAESKey + "=")
	require.NoError(t, err)
	require.Len(t, key, 32)

	plain := bytes.Repeat([]byte("R"), 16)
	var l [4]byte
	binary.BigEndian.PutUint32(l[:], uint32(len(msg)))
	plain = append(plain, l[:]...)
	plain = append(plain, []byte(msg)...)
	plain = append(plain, []byte(appID)...)
	pad := 32 - len(plain)%32
	if pad == 0 {
		pad = 32
	}
	plain = append(plain, bytes.Repeat([]byte{byte(pad)}, pad)...)

	block, err := aes.NewCipher(key)
	require.NoError(t, err)
	out := make([]byte, len(plain))
	cipher.NewCBCEncrypter(block, key[:aes.BlockSize]).CryptBlocks(out, plain)
	return base64.StdEncoding.EncodeToString(out)
}

// ---- 一键下架 ----

// 命中违规 → 下架：对象被覆盖成占位图（内容不再是原图），记录打上处置痕迹并进审计。
func TestTakedownOverwritesObject(t *testing.T) {
	r, db, cfg := setupMediaEngine(t)
	c := newTestClient(r, t)
	adminToken := loginAdmin(t, c)
	row := seedCheckRow(t, db, model.MediaCheckRisky, "2026/10/a.jpg")

	// 先在存储目录里放一张「原图」
	path := filepath.Join(cfg.UploadDir, "2026/10/a.jpg")
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0755))
	require.NoError(t, os.WriteFile(path, []byte("ORIGINAL-VIOLATING-IMAGE"), 0644))

	_, body := c.do("POST", "/api/admin/media-checks/"+strconv.FormatInt(row.ID, 10)+"/takedown", nil, adminToken)
	require.EqualValues(t, 0, body["code"], "%v", body)

	got, err := os.ReadFile(path)
	require.NoError(t, err)
	require.NotContains(t, string(got), "ORIGINAL-VIOLATING-IMAGE", "原图必须被覆盖掉")
	// .jpg 的键里要放 JPEG 内容，否则静态托管的 Content-Type 与内容不符
	require.True(t, bytes.HasPrefix(got, []byte{0xFF, 0xD8, 0xFF}), "jpg 对象应写入 JPEG 占位图")

	var mc model.MediaCheck
	require.NoError(t, db.First(&mc, row.ID).Error)
	require.Equal(t, model.MediaActionTakedown, mc.Action)
	require.NotNil(t, mc.HandledAt)
	require.NotZero(t, mc.HandlerID)

	var n int64
	require.NoError(t, db.Model(&model.AuditLog{}).
		Where("action = ?", "media_check.takedown").Count(&n).Error)
	require.EqualValues(t, 1, n, "覆盖是不可逆的，必须留痕")
}

// 只有「命中违规且未处置」的记录可下架：pass / review 下架等于用机器结论误伤正常内容。
func TestTakedownRejectsNonRisky(t *testing.T) {
	r, db, _ := setupMediaEngine(t)
	c := newTestClient(r, t)
	adminToken := loginAdmin(t, c)
	row := seedCheckRow(t, db, model.MediaCheckPass, "2026/10/a.jpg")

	_, body := c.do("POST", "/api/admin/media-checks/"+strconv.FormatInt(row.ID, 10)+"/takedown", nil, adminToken)
	require.EqualValues(t, 1, body["code"])
	require.Contains(t, body["msg"], "不可下架")
}

// 已下架的记录不能重复下架（覆盖不可逆，重复点只会多一条审计）。
func TestTakedownRejectsAlreadyHandled(t *testing.T) {
	r, db, _ := setupMediaEngine(t)
	c := newTestClient(r, t)
	adminToken := loginAdmin(t, c)
	row := seedCheckRow(t, db, model.MediaCheckRisky, "2026/10/a.jpg")
	now := time.Now()
	require.NoError(t, db.Model(&model.MediaCheck{}).Where("id = ?", row.ID).
		Updates(map[string]any{"action": model.MediaActionTakedown, "handled_at": &now}).Error)

	_, body := c.do("POST", "/api/admin/media-checks/"+strconv.FormatInt(row.ID, 10)+"/takedown", nil, adminToken)
	require.EqualValues(t, 1, body["code"])
}

// 企业管理员只能处置自己企业的记录：越权下架等于帮别人删图。
func TestTakedownCrossTenantDenied(t *testing.T) {
	r, db, _ := setupMediaEngine(t)
	c := newTestClient(r, t)
	loginAdmin(t, c) // 平台超管先登录一次，确保 admin 表已有 seed
	row := seedCheckRow(t, db, model.MediaCheckRisky, "2026/10/a.jpg")

	// 造一个属于 2 号企业的管理员，直接用签发接口拿令牌（不必走登录）
	require.NoError(t, db.Create(&model.Admin{ID: 90, CompanyID: 2, Username: "other-admin"}).Error)
	token, _, _, err := auth.IssueTokens(90, "admin", 0, 2)
	require.NoError(t, err)

	_, body := c.do("POST", "/api/admin/media-checks/"+strconv.FormatInt(row.ID, 10)+"/takedown", nil, token)
	require.EqualValues(t, 403, body["code"])

	var mc model.MediaCheck
	require.NoError(t, db.First(&mc, row.ID).Error)
	require.Equal(t, model.MediaActionNone, mc.Action, "越权请求不能改动记录")
}

// 列表按租户隔离，并支持按状态筛选。
func TestListMediaChecksScoped(t *testing.T) {
	r, db, _ := setupMediaEngine(t)
	c := newTestClient(r, t)
	adminToken := loginAdmin(t, c)

	mine := seedCheckRow(t, db, model.MediaCheckRisky, "2026/10/a.jpg")
	other := model.MediaCheck{CompanyID: 2, URL: "https://x/b.jpg", ObjectKey: "2026/10/b.jpg",
		MediaType: wechat.MediaImage, TraceID: "trace-2", Status: model.MediaCheckPass}
	require.NoError(t, db.Create(&other).Error)

	_, body := c.do("GET", "/api/admin/media-checks", nil, adminToken)
	require.EqualValues(t, 0, body["code"])
	require.EqualValues(t, 2, body["total"], "平台超管应看到全部租户")

	_, body = c.do("GET", "/api/admin/media-checks?status=risky", nil, adminToken)
	require.EqualValues(t, 1, body["total"])
	list := body["data"].([]interface{})
	require.EqualValues(t, mine.ID, list[0].(map[string]interface{})["id"])

	_, body = c.do("GET", "/api/admin/media-checks?onlyTakedownable=1", nil, adminToken)
	require.EqualValues(t, 1, body["total"], "只有命中违规且未处置的算「可下架」")
}
