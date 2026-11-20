package handler

// 存储绑定是**平台级动作**，不是企业级设置。
//
// 为什么必须钉住：绑定决定「这家企业的对象写进哪个桶」。档案里装的是**客户**的
// 桶名与凭据；一旦企业管理员能改自己的绑定，他就能把本企业上传写进别家客户的桶——
// 数据落到第三方基础设施里、对方能直接读到，而两边后台都显示「配置正常」，
// 没有任何人会察觉。这与 `docs/tenant-storage-design.md` §1.1 的权限模型同源：
// 「平台超管代填，这种权限肯定不能放给企业管理员的」。
//
// 所以这一组接口（列表 / 绑定 / 解绑 / 全局默认）全部挂 PlatformAdminMiddleware，
// 企业管理员连读都拿 403。本用例逐条验证，并顺带证明「平台超管放行」——
// 只测拒绝不测放行，等于没测（接口整体挂掉也会让「拒绝」恒绿）。

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	"weichuan/internal/model"
)

func TestStorageBindingIsPlatformAdminOnly(t *testing.T) {
	r, db := setupEngineWithDB(t)
	c := newTestClient(r, t)
	platTok := loginAdmin(t, c)
	tenantTok := loginTenantAdmin(t, c, db) // companyId=1 的企业管理员

	// 只造 local 档案：不碰网络，也不依赖任何云厂商。
	sp := model.StorageProfile{
		Name: "perm-local", Driver: "local", PublicRead: true, UseSSL: true, Status: "ok", Version: 1,
	}
	require.NoError(t, db.Create(&sp).Error)

	cases := []struct {
		name   string
		method string
		path   string
		body   map[string]interface{}
	}{
		{"读绑定列表", "GET", "/api/admin/storage-bindings", nil},
		{"绑定某企业", "PUT", "/api/admin/storage-bindings/companies/1", map[string]interface{}{"storageProfileId": sp.ID}},
		{"解绑某企业", "DELETE", "/api/admin/storage-bindings/companies/1", nil},
		{"改全局默认", "PUT", "/api/admin/storage-bindings/global", map[string]interface{}{"storageProfileId": sp.ID}},
	}

	for _, tc := range cases {
		t.Run(tc.name+"：企业管理员 403", func(t *testing.T) {
			_, body := c.do(tc.method, tc.path, tc.body, tenantTok)
			require.EqualValues(t, 403, body["code"], "企业管理员不该能操作存储绑定：%v", body["msg"])
		})
	}

	// 关键一条：被拒之后**库里必须没有留下任何绑定**。
	// 只看返回码会漏掉「先写库再报错」这种实现（返 403 但副作用已经发生）。
	var n int64
	require.NoError(t, db.Model(&model.StorageBinding{}).Count(&n).Error)
	require.Zero(t, n, "企业管理员被拒后仍写入了绑定")

	for _, tc := range cases {
		t.Run(tc.name+"：平台超管放行", func(t *testing.T) {
			_, body := c.do(tc.method, tc.path, tc.body, platTok)
			require.EqualValues(t, 0, body["code"], "平台超管应放行：%v", body["msg"])
		})
	}

	// 企业自选页下线后，这个只回 id + name 的选项接口不该再存在：
	// 留着等于把「平台有哪些档案」暴露给企业侧，且没有调用方。
	//
	// 这里手搓请求而不走 c.do：gin 的默认 404 是纯文本 "404 page not found"，
	// 而 c.do 会把响应体当 JSON 解，解不开就 require 失败（分不清「路由没了」与
	// 「路由还在但返回了非 JSON」）。只看状态码才能把这两件事分开。
	req := httptest.NewRequest(http.MethodGet, "/api/admin/storage-bindings/options", nil)
	req.Header.Set("Cookie", platTok)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusNotFound, w.Code, "已删除的档案选项接口仍然可达")
}
