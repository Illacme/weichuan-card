package handler

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestMPEntry 小程序「默认进入」设置：名片墙 / 指定员工名片。
//
// 这个设置为**所有访客**决定第一眼看到谁，所以三类错误都不能容忍：
//  1. 校验漏了租户 → A 企业能把落地页指向 B 企业的员工，访客扫 A 的码看到 B 的人；
//  2. 指定的员工后来停用了 → 访客被送到一张「该员工名片已停用」的空页，
//     而不是回落名片墙（宁可看全员，也不给一张废页）；
//  3. 切回名片墙时 staff_id 没归零 → 下次切回 staff 会拿旧值，落到一个
//     管理员早就不想要的人身上（GORM 结构体写库写不掉零值，必须用 map）。
func TestMPEntry(t *testing.T) {
	r := setupSQLiteEngine(t)
	plat := newTestClient(r, t)
	loginAdmin(t, plat)

	// 1 号企业建两名员工（一个当入口，一个用来验证「停用后回落」）
	var staffID int64
	for i, name := range []string{"高立新", "张三"} {
		_, body := plat.doWithCookie("POST", "/api/admin/employees", map[string]interface{}{
			"name": name, "phone": fmt.Sprintf("1390000000%d", i+1), "title": "销售",
		})
		require.EqualValues(t, 0, body["code"])
		if i == 0 {
			staffID = int64(body["staffId"].(float64))
		}
	}
	// 再开一家企业 + 一名员工，用于验证跨租户拦截
	_, body := plat.doWithCookie("POST", "/api/admin/companies", map[string]interface{}{
		"name": "乙公司", "seedCategories": false,
	})
	require.EqualValues(t, 0, body["code"])
	company2 := int64(body["data"].(map[string]interface{})["id"].(float64))
	_, body = plat.doWithCookie("POST", fmt.Sprintf("/api/admin/employees?companyId=%d", company2),
		map[string]interface{}{"name": "赵六", "phone": "13900000009"})
	require.EqualValues(t, 0, body["code"])
	otherStaff := int64(body["staffId"].(float64))

	// 默认：名片墙（存量企业升级后行为不变）
	_, body = plat.doWithCookie("GET", "/api/admin/mp-entry", nil)
	require.EqualValues(t, 0, body["code"])
	data := body["data"].(map[string]interface{})
	require.Equal(t, "wall", data["mode"])
	require.EqualValues(t, 0, data["staffId"])

	_, body = plat.do("GET", "/api/team?companyId=1", nil, "")
	require.EqualValues(t, "wall", body["entry"].(map[string]interface{})["mode"], "默认落地页应是名片墙")

	// 设为「指定员工」
	_, body = plat.doWithCookie("PUT", "/api/admin/mp-entry", map[string]interface{}{
		"mode": "staff", "staffId": staffID,
	})
	require.EqualValues(t, 0, body["code"])

	_, body = plat.doWithCookie("GET", "/api/admin/mp-entry", nil)
	data = body["data"].(map[string]interface{})
	require.Equal(t, "staff", data["mode"])
	require.Equal(t, "staff", data["effectiveMode"], "员工有效时应生效")
	require.Equal(t, "高立新", data["staffName"], "界面要能显示指定的是谁")
	require.EqualValues(t, staffID, data["staffId"])

	_, body = plat.do("GET", "/api/team?companyId=1", nil, "")
	entry := body["entry"].(map[string]interface{})
	require.Equal(t, "staff", entry["mode"])
	require.EqualValues(t, staffID, entry["staffId"], "公开接口要告诉端上跳给谁")

	// 员工停用 → 公开接口回落名片墙，但管理端仍显示「你设的是张三这种人」的原值
	_, body = plat.doWithCookie("POST", fmt.Sprintf("/api/admin/employees/%d/disable", staffID), nil)
	require.EqualValues(t, 0, body["code"])

	_, body = plat.do("GET", "/api/team?companyId=1", nil, "")
	entry = body["entry"].(map[string]interface{})
	require.Equal(t, "wall", entry["mode"], "指定员工停用后必须回落名片墙，不能给访客一张废页")
	require.EqualValues(t, 0, entry["staffId"])

	_, body = plat.doWithCookie("GET", "/api/admin/mp-entry", nil)
	data = body["data"].(map[string]interface{})
	require.Equal(t, "staff", data["mode"], "管理端应回配置原值，而不是被静默归一后的生效值")
	require.Equal(t, "wall", data["effectiveMode"], "生效值与配置值不一致必须能看出来")
	require.Equal(t, "disabled", data["staffStatus"])

	// 校验：跨租户指定员工 / 不存在 / 未选 / 非法 mode
	_, body = plat.doWithCookie("PUT", "/api/admin/mp-entry", map[string]interface{}{
		"mode": "staff", "staffId": otherStaff,
	})
	require.EqualValues(t, 1, body["code"], "不能把落地页指向别家企业的员工")
	require.Contains(t, body["msg"], "不属于本企业")

	_, body = plat.doWithCookie("PUT", "/api/admin/mp-entry", map[string]interface{}{
		"mode": "staff", "staffId": 999999,
	})
	require.EqualValues(t, 1, body["code"])

	_, body = plat.doWithCookie("PUT", "/api/admin/mp-entry", map[string]interface{}{
		"mode": "staff", "staffId": 0,
	})
	require.EqualValues(t, 1, body["code"], "staff 模式必须选人")
	require.Contains(t, body["msg"], "请选择")

	_, body = plat.doWithCookie("PUT", "/api/admin/mp-entry", map[string]interface{}{
		"mode": "wal",
	})
	require.EqualValues(t, 1, body["code"], "拼错的 mode 必须拒绝，不能静默归一成名片墙")

	// 切回名片墙：staff_id 必须真的归零（用 map 写库才做得到）
	_, body = plat.doWithCookie("PUT", "/api/admin/mp-entry", map[string]interface{}{"mode": "wall"})
	require.EqualValues(t, 0, body["code"])
	_, body = plat.doWithCookie("GET", "/api/admin/mp-entry", nil)
	data = body["data"].(map[string]interface{})
	require.Equal(t, "wall", data["mode"])
	require.EqualValues(t, 0, data["staffId"], "切回名片墙时 staff_id 应归零，否则下次切回会落到旧员工")
}

// TestMPEntryTenantAdmin 企业管理员只能改自己企业的设置，且不需要知道企业 ID。
//
// 这是「企业管理员也能设置」的核心保证：他自己登录后进「小程序入口」页就能改，
// 不用找平台超管。同时他不能顺手把别家改了。
func TestMPEntryTenantAdmin(t *testing.T) {
	r := setupSQLiteEngine(t)
	plat := newTestClient(r, t)
	loginAdmin(t, plat)

	// 开两家企业，各带一个管理员
	var companyIDs []int64
	for i, name := range []string{"甲公司", "乙公司"} {
		_, body := plat.doWithCookie("POST", "/api/admin/companies", map[string]interface{}{
			"name": name, "seedCategories": false,
			"adminUsername": fmt.Sprintf("boss%d", i+1), "adminPassword": "pass1234",
		})
		require.EqualValues(t, 0, body["code"])
		companyIDs = append(companyIDs, int64(body["data"].(map[string]interface{})["id"].(float64)))
	}
	// 甲公司建一名员工
	_, body := plat.doWithCookie("POST", fmt.Sprintf("/api/admin/employees?companyId=%d", companyIDs[0]),
		map[string]interface{}{"name": "甲公司员工", "phone": "13900000021"})
	require.EqualValues(t, 0, body["code"])
	staffA := int64(body["staffId"].(float64))

	// 甲公司管理员登录，改成 staff 模式
	boss := newTestClient(r, t)
	_, body = boss.do("POST", "/api/admin/login", map[string]interface{}{
		"username": "boss1", "password": "pass1234",
	}, "")
	require.EqualValues(t, 0, body["code"])

	_, body = boss.doWithCookie("PUT", "/api/admin/mp-entry", map[string]interface{}{
		"mode": "staff", "staffId": staffA,
	})
	require.EqualValues(t, 0, body["code"], "企业管理员应能改本企业的小程序入口")

	// 生效在甲公司，乙公司不受影响
	_, body = plat.do("GET", fmt.Sprintf("/api/team?companyId=%d", companyIDs[0]), nil, "")
	require.Equal(t, "staff", body["entry"].(map[string]interface{})["mode"])
	_, body = plat.do("GET", fmt.Sprintf("/api/team?companyId=%d", companyIDs[1]), nil, "")
	require.Equal(t, "wall", body["entry"].(map[string]interface{})["mode"], "别家企业不应被牵连")

	// 乙公司管理员想指定甲公司的员工 → 拒绝（租户以服务端解析为准）
	boss2 := newTestClient(r, t)
	_, body = boss2.do("POST", "/api/admin/login", map[string]interface{}{
		"username": "boss2", "password": "pass1234",
	}, "")
	require.EqualValues(t, 0, body["code"])
	_, body = boss2.doWithCookie("PUT", "/api/admin/mp-entry", map[string]interface{}{
		"mode": "staff", "staffId": staffA,
	})
	require.EqualValues(t, 1, body["code"], "乙公司管理员不能把入口指向甲公司的员工")
}
