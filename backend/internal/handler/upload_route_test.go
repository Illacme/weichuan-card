package handler

import (
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"weichuan/internal/storage"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// fakePresigner 记录被签的对象键：用它就能在不连真桶的前提下断言
// 「签的是谁的键」——这正是租户路由最容易错的那一半。
type fakePresigner struct {
	key string
}

func (f *fakePresigner) Save(*multipart.FileHeader, string) (string, error) { return "", nil }
func (f *fakePresigner) SaveBytes(string, []byte, string) (string, error)   { return "", nil }
func (f *fakePresigner) PresignPut(key, _ string, _ time.Duration) (string, string, error) {
	f.key = key
	return "PUT:" + key, "https://tenant.example.com/" + key, nil
}

// fakeResolver 记录最近一次被问的 cid，并按需返回固定实例 / 错误。
type fakeResolver struct {
	cid    int64
	up     storage.Uploader
	driver string
	err    error
}

func (r *fakeResolver) For(cid int64) (storage.Uploader, error) {
	r.cid = cid
	if r.err != nil {
		return nil, r.err
	}
	return r.up, nil
}

func (r *fakeResolver) Platform() storage.Uploader { return r.up }
func (r *fakeResolver) DriverFor(int64) string     { return r.driver }

// TestPresignUsesTenantInstanceAndTenantKey 证明直传链路真的按租户走：
//
//  1. 传进来的 cid 必须被带进 For（否则会去问平台那份实例）；
//  2. 签的键必须落在 t/<cid>/ 前缀下（否则客户的文件会写进平台前缀目录）。
//
// 这两条任一条错，症状都是「文件传上去了、URL 也有了、图打不开」。
func TestPresignUsesTenantInstanceAndTenantKey(t *testing.T) {
	gin.SetMode(gin.TestMode)
	fp := &fakePresigner{}
	res := &fakeResolver{up: fp, driver: "minio"}
	h := presignHandler(nil, res) // size 缺省=0 → 不读写库，db 可以不传

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/admin/upload/presign?ext=png&companyId=7", nil)
	h(c)

	var resp struct {
		Code int `json:"code"`
		Data struct {
			PutURL string `json:"putUrl"`
			URL    string `json:"url"`
			Key    string `json:"key"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp), "响应应为 JSON：%s", w.Body.String())
	require.Equal(t, 0, resp.Code, "预签名应成功：%s", w.Body.String())

	require.EqualValues(t, 7, res.cid, "必须用请求所属企业去取实例")
	require.True(t, strings.HasPrefix(fp.key, "t/7/"),
		"租户对象的键必须落在 t/7/ 前缀下，实际 %q", fp.key)
	require.Equal(t, fp.key, resp.Data.Key)
	require.Equal(t, "PUT:"+fp.key, resp.Data.PutURL)
}

// TestPresignReportsResolverError 钉住「构造不出租户实例时不回退」：
// 返回 code=1 让前端回退普通上传——普通上传同样会失败（同一份配置），
// 但两条路径都失败才能让用户停下来找客服，而不是把文件悄悄写到平台盘上。
func TestPresignReportsResolverError(t *testing.T) {
	gin.SetMode(gin.TestMode)
	res := &fakeResolver{err: errFake("凭据不完整（AccessKey / SecretKey 缺一）")}
	h := presignHandler(nil, res)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/admin/upload/presign?ext=png&companyId=3", nil)
	h(c)

	var resp struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.Equal(t, 1, resp.Code)
	require.Contains(t, resp.Msg, "凭据不完整")
}

type errFake string

func (e errFake) Error() string { return string(e) }

// TestCapabilityJudgesTenantInstanceNotGlobalConfig 是本轮行为修正的回归用例。
//
// 场景：平台全局是 local（系统设置里没配对象存储），但 A 企业配了自己的桶。
// 旧实现看 cfg.StorageDriver → 对 A 也回「本地磁盘存储、不可直传」，
// 于是 A 明明能直传却被界面禁用；反过来 B 没配桶，也可能因为全局是 minio
// 被显示成"可直传"，选了就失败。现在按该租户的实例判。
func TestCapabilityJudgesTenantInstanceNotGlobalConfig(t *testing.T) {
	gin.SetMode(gin.TestMode)
	// 全局说 local（这里连 cfg 都不再传进 capabilityHandler——判据已经不在全局配置上）。
	res := &fakeResolver{up: &fakePresigner{}, driver: "minio"}
	h := capabilityHandler(res)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/admin/upload/capability?companyId=7", nil)
	h(c)

	var resp struct {
		Code int `json:"code"`
		Data struct {
			Driver      string `json:"driver"`
			Presign     bool   `json:"presign"`
			VideoUpload bool   `json:"videoUpload"`
			Reason      string `json:"reason"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.Equal(t, 0, resp.Code)
	require.Equal(t, "minio", resp.Data.Driver, "驱动名要报该租户实际生效的那个")
	require.True(t, resp.Data.VideoUpload, "该租户能直传，就不该被全局 local 判成不可用")
	require.Empty(t, resp.Data.Reason)
}

// TestCapabilitySurfacesBrokenTenantConfig 钉住"配置坏了要能被看见"：
// 这是企业管理员唯一能看到自家存储出错的界面（写接口只有平台超管能进去）。
// 用 code=0 而不是 code=1：前端对 code!=0 会整块降级成 fallback 文案，
// 那样 reason 反而显示不出来。
func TestCapabilitySurfacesBrokenTenantConfig(t *testing.T) {
	gin.SetMode(gin.TestMode)
	res := &fakeResolver{
		driver: "minio",
		err:    errFake("企业 7 的自有存储不可用（已按要求不回退平台存储，以免文件落错位置）：凭据不完整"),
	}
	h := capabilityHandler(res)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/admin/upload/capability", nil)
	h(c)

	var resp struct {
		Code int `json:"code"`
		Data struct {
			Presign     bool   `json:"presign"`
			VideoUpload bool   `json:"videoUpload"`
			Reason      string `json:"reason"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.Equal(t, 0, resp.Code)
	require.False(t, resp.Data.Presign)
	require.False(t, resp.Data.VideoUpload)
	require.Contains(t, resp.Data.Reason, "不回退平台存储")
}
