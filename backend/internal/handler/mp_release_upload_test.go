package handler

// 后台上传代码（mp_release.upload）的测试。
//
// 用假 node 跑通这条路：它按约定读 stdin、打结果行，并把入参里 privateKeyPath
// 指向的文件内容拷一份出来——这样才能断言「用的是租户那把钥匙还是平台全局那把」。
// 真 Node + miniprogram-ci 的联调在 internal/mpupload 的测试里用同样手法覆盖。
//
// 覆盖重点：
//  1. 上传成功 → 自动建 draft 记录（source=backend），版本号与代码必然一致；
//  2. 上传失败 → **不留下记录**（否则运营会以为只差提审，白扣一次审核额度）；
//  3. 租户上传密钥优先于平台全局那把（用错钥匙=把代码传到别的小程序）；
//  4. 探活接口把缺什么一次列全（「点了才说不行」是这套功能最劝退的形态）；
//  5. 环境没就绪时不去跑 node；
//  6. 租户关掉代发后上传同样 403，但只读探活不受影响。

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"weichuan/internal/model"
	"weichuan/internal/setting"

	"github.com/stretchr/testify/require"
)

// uploadFake 假 node 的落盘位置。
type uploadFake struct {
	stdin   string // node 收到的配置 JSON
	keyUsed string // 本次实际上传使用的密钥内容
}

// rebuild 用当前 e.cfg 重新装配引擎。
// RegisterMPRelease 按值接收 cfg，路由闭包里捕获的是一份拷贝——事后再改
// e.cfg 对已注册的路由无效，所以改完配置必须重建。
func (e *mpReleaseEnv) rebuild(t *testing.T) {
	t.Helper()
	r := buildEngine(t, e.db, e.cfg, fakeUploader{}, &fakeRefresher{})
	e.c = newTestClient(r, t)
	e.token = loginAdmin(t, e.c)
}

// newMPReleaseUploadEnv 带上传能力的发布测试环境。mode: "ok" | "fail"。
func newMPReleaseUploadEnv(t *testing.T, companyID int64, mode string) (*mpReleaseEnv, uploadFake) {
	t.Helper()
	e := newMPReleaseEnvWith(t, companyID, "wx-global-appid", "wx-global-secret")

	dir := t.TempDir()
	src := filepath.Join(dir, "miniprogram")
	mod := filepath.Join(dir, "ci")
	require.NoError(t, os.MkdirAll(filepath.Join(mod, "node_modules", "miniprogram-ci"), 0o755))
	require.NoError(t, os.MkdirAll(src, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(src, "project.config.json"), []byte("{}"), 0o644))

	fake := uploadFake{
		stdin:   filepath.Join(dir, "node-stdin.json"),
		keyUsed: filepath.Join(dir, "node-key-used.txt"),
	}
	t.Setenv("FAKE_NODE_IN", fake.stdin)
	t.Setenv("FAKE_NODE_KEY_OUT", fake.keyUsed)

	fail := ""
	if mode == "fail" {
		fail = "1"
	}
	script := "#!/bin/sh\n" +
		"cat > \"$FAKE_NODE_IN\"\n" +
		"KEY=$(sed -n 's/.*\"privateKeyPath\":\"\\([^\"]*\\)\".*/\\1/p' \"$FAKE_NODE_IN\")\n" +
		"cat \"$KEY\" > \"$FAKE_NODE_KEY_OUT\" 2>/dev/null\n" +
		"if [ \"" + fail + "\" = \"1\" ]; then\n" +
		"  printf '%s{\"ok\":false,\"error\":\"假微信：服务器 IP 不在上传白名单\"}\\n' '__WEICHUAN_MP_UPLOAD__'\n" +
		"  exit 1\n" +
		"fi\n" +
		"echo 'compiling...'\n" +
		"printf '%s{\"ok\":true}\\n' '__WEICHUAN_MP_UPLOAD__'\n"
	nodeBin := filepath.Join(dir, "fake-node")
	require.NoError(t, os.WriteFile(nodeBin, []byte(script), 0o755))

	e.cfg.MPNodeBin = nodeBin
	e.cfg.MPCIModuleDir = mod
	e.cfg.MPSourceDir = src
	e.rebuild(t)
	return e, fake
}

const (
	testUploadKey       = "-----BEGIN RSA PRIVATE KEY-----\nMII-fake-tenant-key\n-----END RSA PRIVATE KEY-----"
	testGlobalUploadKey = "-----BEGIN RSA PRIVATE KEY-----\nMII-fake-global-key\n-----END RSA PRIVATE KEY-----"
)

// useGlobalUploadKey 配上平台全局那一把上传密钥。
// 只关心「上传链路通不通」的用例用它；关心「用哪把钥匙」的用例见
// TestMPReleaseUploadUsesTenantKey，那里两把都配、断言租户那把赢。
func useGlobalUploadKey(t *testing.T) {
	t.Helper()
	require.NoError(t, setting.Upsert("wx_upload_key", testGlobalUploadKey))
}

// TestMPReleaseUploadCreatesDraft 上传成功 → 自动建一条 draft 记录，source=backend。
// 这就是「没有微信开发者工具也能发版」的那条路：上传不再需要人参与。
func TestMPReleaseUploadCreatesDraft(t *testing.T) {
	e, fake := newMPReleaseUploadEnv(t, 2, "ok")
	useGlobalUploadKey(t)

	_, body := e.c.do("POST", "/api/admin/companies/2/releases/upload",
		map[string]interface{}{"userVersion": "2.0.0", "userDesc": "后台直传"}, e.token)
	require.EqualValues(t, 0, body["code"], "上传响应：%v", body)
	d := dataOf(t, body)
	require.Equal(t, "draft", d["status"])
	require.Equal(t, "2.0.0", d["userVersion"])
	require.Equal(t, "后台直传", d["userDesc"])

	// 传给 node 的配置：appid 用全局凭据（企业没配自己的），版本号原样透传
	in, err := os.ReadFile(fake.stdin)
	require.NoError(t, err)
	require.Contains(t, string(in), `"appid":"wx-global-appid"`)
	require.Contains(t, string(in), `"version":"2.0.0"`)
	// 密钥只能以临时文件路径的形式出现，内容不进 argv/配置
	require.NotContains(t, string(in), "PRIVATE KEY")

	// 列表里能查到，且只有一条
	_, body = e.c.do("GET", "/api/admin/companies/2/releases", nil, e.token)
	require.EqualValues(t, 1, body["total"])
	row := body["data"].([]interface{})[0].(map[string]interface{})
	require.Equal(t, "draft", row["status"])
	require.Equal(t, "backend", row["source"], "后台自己传的版本要能区别于 CI 传的/人工登记的")

	// 上传要留痕：这是整条链上唯一「把代码送上服务器」的动作
	var logs []model.AuditLog
	require.NoError(t, e.db.Where("action = ?", "mp_release.upload").Find(&logs).Error)
	require.Len(t, logs, 1)
	require.Equal(t, "success", logs[0].Result)
	require.Contains(t, logs[0].Detail, "2.0.0")
}

// TestMPReleaseUploadFailureLeavesNoRecord 上传失败时不能留下 draft 记录。
// 传失败却记一条「待提审」，运营会以为只差提审，接着白扣一次审核额度。
func TestMPReleaseUploadFailureLeavesNoRecord(t *testing.T) {
	e, _ := newMPReleaseUploadEnv(t, 2, "fail")
	useGlobalUploadKey(t)

	_, body := e.c.do("POST", "/api/admin/companies/2/releases/upload",
		map[string]interface{}{"userVersion": "2.0.0"}, e.token)
	require.EqualValues(t, 1, body["code"])
	require.Contains(t, body["msg"], "不在上传白名单", "微信给的原因要原样透出：%v", body)

	require.EqualValues(t, 0, countRows(t, e.db, &model.MPRelease{}), "上传失败不应留下记录")

	var log model.AuditLog
	require.NoError(t, e.db.Where("action = ?", "mp_release.upload").First(&log).Error)
	require.Equal(t, "fail", log.Result)
	require.Contains(t, log.Detail, "不在上传白名单")
}

// TestMPReleaseUploadUsesTenantKey 企业自己配的上传密钥优先于平台全局那把。
// 用错钥匙会把代码传到别的小程序上——这正是要按租户隔离的原因。
func TestMPReleaseUploadUsesTenantKey(t *testing.T) {
	e, fake := newMPReleaseUploadEnv(t, 2, "ok")

	require.NoError(t, setting.Upsert("wx_upload_key", testGlobalUploadKey))
	_, body := e.c.do("PUT", "/api/admin/companies/2/wechat",
		map[string]interface{}{
			"appId": "wx-tenant-b", "appSecret": "secret-b",
			"uploadKey": testUploadKey,
		}, e.token)
	require.EqualValues(t, 0, body["code"])

	_, body = e.c.do("POST", "/api/admin/companies/2/releases/upload",
		map[string]interface{}{"userVersion": "3.0.0"}, e.token)
	require.EqualValues(t, 0, body["code"], "上传响应：%v", body)

	used, err := os.ReadFile(fake.keyUsed)
	require.NoError(t, err)
	require.Contains(t, string(used), "fake-tenant-key", "应使用租户自己的上传密钥")
	require.NotContains(t, string(used), "fake-global-key")

	// 记录里的 appid 也应是租户那个
	id := int64(body["data"].(map[string]interface{})["id"].(float64))
	require.Equal(t, "wx-tenant-b", mustGet(t, e, id)["appId"])
}

// TestMPReleaseUploadReadiness 上传前探活：缺什么一次列全。
func TestMPReleaseUploadReadiness(t *testing.T) {
	e, _ := newMPReleaseUploadEnv(t, 2, "ok")

	// 环境就绪但没配密钥 → 只缺钥匙
	_, body := e.c.do("GET", "/api/admin/companies/2/releases/upload-readiness", nil, e.token)
	require.EqualValues(t, 0, body["code"])
	d := dataOf(t, body)
	require.Equal(t, false, d["ready"])
	require.Equal(t, false, d["keyConfigured"])
	problems := d["problems"].([]interface{})
	require.Len(t, problems, 1)
	require.Contains(t, problems[0], "未填写代码上传密钥")

	// 配了密钥 → 就绪
	_, body = e.c.do("PUT", "/api/admin/companies/2/wechat",
		map[string]interface{}{"appId": "wx-tenant-b", "appSecret": "s", "uploadKey": testUploadKey}, e.token)
	require.EqualValues(t, 0, body["code"])
	_, body = e.c.do("GET", "/api/admin/companies/2/releases/upload-readiness", nil, e.token)
	d = dataOf(t, body)
	require.Equal(t, true, d["ready"])
	require.Equal(t, true, d["keyConfigured"])
	require.Empty(t, d["problems"])
}

// TestMPReleaseUploadEnvNotReadyDoesNotRunNode 环境缺东西时不要去跑 node。
// 缺 node 的机器上 exec 只会回一句 "executable file not found in $PATH"，
// 运维看不出要装什么——所以在跑之前就把问题列全。
func TestMPReleaseUploadEnvNotReadyDoesNotRunNode(t *testing.T) {
	e, _ := newMPReleaseUploadEnv(t, 2, "ok")
	// 制造「没配源码目录」：改完必须重建引擎，否则路由闭包里还是旧配置
	e.cfg.MPSourceDir = ""
	e.rebuild(t)

	_, body := e.c.do("POST", "/api/admin/companies/2/releases/upload",
		map[string]interface{}{"userVersion": "2.0.0"}, e.token)
	require.EqualValues(t, 1, body["code"])
	require.Contains(t, body["msg"], "源码目录")

	// node 一次都不该被调用：stdin 落盘文件不存在即证
	stdin := filepath.Join(filepath.Dir(e.cfg.MPNodeBin), "node-stdin.json")
	_, err := os.Stat(stdin)
	require.Error(t, err, "环境不就绪时不应真的执行 node")
	require.EqualValues(t, 0, countRows(t, e.db, &model.MPRelease{}))
}

// TestMPReleaseUploadRespectsTenantSwitch 企业关掉代发后，上传同样被拒；
// 但只读的探活不受影响——企业要能看见「现在是缺钥匙还是被关了」。
func TestMPReleaseUploadRespectsTenantSwitch(t *testing.T) {
	e, _ := newMPReleaseUploadEnv(t, 2, "ok")

	_, body := e.c.do("PUT", "/api/admin/companies/2/wechat",
		map[string]interface{}{
			"appId": "wx-tenant-b", "appSecret": "s", "uploadKey": testUploadKey,
			"releaseEnabled": false,
		}, e.token)
	require.EqualValues(t, 0, body["code"])

	_, body = e.c.do("POST", "/api/admin/companies/2/releases/upload",
		map[string]interface{}{"userVersion": "2.0.0"}, e.token)
	require.EqualValues(t, 403, body["code"])
	require.EqualValues(t, 0, countRows(t, e.db, &model.MPRelease{}))

	_, body = e.c.do("GET", "/api/admin/companies/2/releases/upload-readiness", nil, e.token)
	require.EqualValues(t, 0, body["code"])
}

// TestMPReleaseUploadRejectsDuplicateVersion 同一版本号传过就不要再传。
// 微信侧允许重复传同一版本，但我们的记录会重 key，且大概率意味着已经传过了。
func TestMPReleaseUploadRejectsDuplicateVersion(t *testing.T) {
	e, _ := newMPReleaseUploadEnv(t, 2, "ok")
	useGlobalUploadKey(t)

	_, body := e.c.do("POST", "/api/admin/companies/2/releases/upload",
		map[string]interface{}{"userVersion": "2.0.0"}, e.token)
	require.EqualValues(t, 0, body["code"])

	_, body = e.c.do("POST", "/api/admin/companies/2/releases/upload",
		map[string]interface{}{"userVersion": "2.0.0"}, e.token)
	require.EqualValues(t, 1, body["code"])
	require.Contains(t, body["msg"], "已登记过")
	require.EqualValues(t, 1, countRows(t, e.db, &model.MPRelease{}))
}

// TestMPReleaseUploadThenSubmit 上传完就能接着提审——这是「一键发版」的骨架。
func TestMPReleaseUploadThenSubmit(t *testing.T) {
	e, _ := newMPReleaseUploadEnv(t, 2, "ok")
	useGlobalUploadKey(t)

	_, body := e.c.do("POST", "/api/admin/companies/2/releases/upload",
		map[string]interface{}{"userVersion": "4.1.0", "userDesc": "后台直传后直接提审"}, e.token)
	require.EqualValues(t, 0, body["code"], "上传响应：%v", body)
	id := int64(body["data"].(map[string]interface{})["id"].(float64))

	e.release.auditID = 4242
	_, body = e.c.do("POST", fmt.Sprintf("/api/admin/releases/%d/submit-audit", id), nil, e.token)
	require.EqualValues(t, 0, body["code"], "上传后应能直接提审：%v", body)
	d := dataOf(t, body)
	require.Equal(t, "auditing", d["status"])
	require.EqualValues(t, 4242, d["auditId"])
}
