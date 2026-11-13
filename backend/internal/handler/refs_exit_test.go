package handler

import (
	"testing"

	"weichuan/internal/auth"
	"weichuan/internal/model"

	"github.com/stretchr/testify/require"
)

// 出参侧收口（第五十七轮）：库里是裸键（t/<cid>/x）时，**后台出参**也必须补回
// /uploads/ 前缀——与公开接口同一口径。
//
// 为什么这条要单独钉住：后台裂图比公开页裂图更难被发现。公开页坏了立刻有人报，
// 后台是运营一个人看到预览破图，第一反应通常是「前端 bug」；而同时公开页正常，
// 很容易把排查引到前端去。
//
// 写入侧还没收敛成裸键，所以这里直接往库里写裸键来构造「将来」的数据形态——
// 这正是回归该测的东西：出参能不能认。
func TestAdminExitsResolveBareKeys(t *testing.T) {
	r, db := setupEngineWithDB(t)
	c := newTestClient(r, t)
	adminToken := loginAdmin(t, c)

	// ===== 公司基础信息：logo =====
	require.NoError(t, db.Create(&model.CompanyInfo{
		CompanyID: defaultCompanyID, FullName: "朗汇机电", Logo: "t/1/logo.png",
	}).Error)

	_, body := c.do("GET", "/api/admin/company-info", nil, adminToken)
	require.EqualValues(t, 0, body["code"])
	require.Equal(t, "/uploads/t/1/logo.png", body["data"].(map[string]interface{})["logo"],
		"后台出参必须把裸键补成可访问 URL")

	// 出参解析绝不能顺手写回库：写回去形态就从裸键退回历史形态，
	// 而且没有任何报错——`upload_base_url` 改动影响存量的老问题就是这么复发的。
	var ci model.CompanyInfo
	require.NoError(t, db.Where("company_id = ?", defaultCompanyID).First(&ci).Error)
	require.Equal(t, "t/1/logo.png", ci.Logo, "出参解析不得污染数据库")
}

// 项目案例：cover 走模型方法、album 走展开点的手工解析（模型方法解不开 JSON 字符串）。
// 这两个字段后台与公开页各有一处出口，必须都覆盖到。
func TestAdminProjectExitResolvesCoverAndAlbum(t *testing.T) {
	r, db := setupEngineWithDB(t)
	c := newTestClient(r, t)
	adminToken := loginAdmin(t, c)

	require.NoError(t, db.Create(&model.ProjectCase{
		CompanyID: defaultCompanyID,
		Name:      "案例A",
		Cover:     "t/1/cover.jpg",
		Album:     `["t/1/a1.jpg","https://cdn.example.com/a2.jpg",""]`,
		Enabled:   true,
	}).Error)

	_, body := c.do("GET", "/api/admin/projects", nil, adminToken)
	require.EqualValues(t, 0, body["code"])
	list := body["data"].([]interface{})
	require.Len(t, list, 1)
	row := list[0].(map[string]interface{})

	require.Equal(t, "/uploads/t/1/cover.jpg", row["cover"], "封面裸键要补前缀")

	album := row["album"].([]interface{})
	require.Equal(t, "/uploads/t/1/a1.jpg", album[0], "图集裸键要补前缀")
	require.Equal(t, "https://cdn.example.com/a2.jpg", album[1], "存量绝对 URL 必须原样返回")
	require.Equal(t, "", album[2], "空元素保持空，不能补成 /uploads/")
}

// 小程序「我的」页：avatar 裸键必须补前缀。少了这一步，端上 assetUrl 会把裸键
// 当相对路径拼成 /t/5/x.jpg（少一层 uploads），头像静默裂图——请求 200、无报错。
func TestEmployeeMeResolvesAvatar(t *testing.T) {
	r, db := setupEngineWithDB(t)
	c := newTestClient(r, t)

	emp := model.Employee{CompanyID: defaultCompanyID, Avatar: "t/1/av.jpg", Status: "enabled"}
	require.NoError(t, db.Create(&emp).Error)

	tok, _, _, err := auth.IssueTokens(emp.StaffID, "employee", emp.TokenVersion, emp.CompanyID)
	require.NoError(t, err)

	_, body := c.do("GET", "/api/employee/me", nil, tok)
	require.EqualValues(t, 0, body["code"], "员工本人信息读取失败：%v", body)
	require.Equal(t, "/uploads/t/1/av.jpg", body["data"].(map[string]interface{})["avatar"],
		"端上拿到的头像必须是可访问 URL，不能是裸键")

	var got model.Employee
	require.NoError(t, db.First(&got, "staff_id = ?", emp.StaffID).Error)
	require.Equal(t, "t/1/av.jpg", got.Avatar, "出参解析不得污染数据库")
}
