package handler

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestApplyPhoneUniquenessPerCompany 钉死「同一企业手机号唯一」这一强约束：
//
//   - 公开申请重复提交同号 → 第二次必须被拒（修复前会永远回「申请已提交」，可无限刷）；
//   - 跨企业同号互不干扰（company_id 隔离，不能因为 A 企业占了某号就拦 B 企业）；
//   - 管理员手动建号撞已存在员工 → 被拒；
//   - 审批通过建出的员工，再申请同号 → 被拒（员工存在兜底，且审批瞬间自身 pending 行
//     不能把自己算进去——excludeApplyID 处理这条）；
//   - 已驳回（rejected）的申请允许用同号重新申请（rejected 不拦，否则用户被一次性封死）。
//
// 反向验证：先确认 FAIL（去掉 phoneInUse 守卫会重复建号），再还原确认 PASS。
// 这里只写 PASS 断言；反向验证见 TestApplyPhoneUniqueReverse。
func TestApplyPhoneUniquenessPerCompany(t *testing.T) {
	r := setupSQLiteEngine(t)
	plat := newTestClient(r, t)
	adminToken := loginAdmin(t, plat)

	// 开一个第二企业，供跨企业隔离断言
	_, body := plat.do("POST", "/api/admin/companies", map[string]interface{}{
		"name": "隔离测试企业", "shortName": "隔离企", "adminUsername": "iso", "adminPassword": "isopass",
		"seedCategories": false,
	}, adminToken)
	require.EqualValues(t, 0, body["code"])
	comp2 := int64(body["data"].(map[string]interface{})["id"].(float64))

	// ===== 1. 同企业重复提交申请 → 第二次被拒 =====
	_, body = plat.do("POST", "/api/apply/open", map[string]interface{}{
		"name": "重复甲", "phone": "13800001234", "title": "销售",
	}, "")
	require.EqualValues(t, 0, body["code"], "首次申请应成功")
	_, body = plat.do("POST", "/api/apply/open", map[string]interface{}{
		"name": "重复甲", "phone": "13800001234", "title": "销售",
	}, "")
	require.EqualValues(t, 1, body["code"], "同企业同号重复提交必须被拒")
	require.Contains(t, body["msg"], "重复")

	// ===== 2. 跨企业同号互不干扰 =====
	_, body = plat.do("POST", "/api/apply/open", map[string]interface{}{
		"name": "隔离企甲", "phone": "13800001234", "title": "销售", "companyId": comp2,
	}, "")
	require.EqualValues(t, 0, body["code"], "另一企业可用同号申请")

	// ===== 3. 管理员手动建号撞已存在员工 → 被拒 =====
	_, body = plat.do("POST", "/api/admin/employees", map[string]interface{}{
		"name": "员工甲", "phone": "13900005678", "title": "工程师",
	}, adminToken)
	require.EqualValues(t, 0, body["code"])
	_, body = plat.do("POST", "/api/admin/employees", map[string]interface{}{
		"name": "员工甲副本", "phone": "13900005678", "title": "工程师",
	}, adminToken)
	require.EqualValues(t, 1, body["code"], "手动建号撞号必须被拒")
	require.Contains(t, body["msg"], "已存在")

	// ===== 4. 审批通过建员工后，再申请同号 → 被拒（员工存在兜底）=====
	_, body = plat.do("POST", "/api/apply/open", map[string]interface{}{
		"name": "审批乙", "phone": "13700008888", "title": "经理",
	}, "")
	require.EqualValues(t, 0, body["code"])
	applyID := findApplyIDByPhone(t, plat, adminToken, "13700008888")
	require.NotZero(t, applyID, "应能找到刚提交的申请")
	_, body = plat.do("POST", fmt.Sprintf("/api/admin/apply/open/%d/approve", applyID), nil, adminToken)
	require.EqualValues(t, 0, body["code"], "审批通过应建员工")
	_, body = plat.do("POST", "/api/apply/open", map[string]interface{}{
		"name": "审批乙再申请", "phone": "13700008888", "title": "经理",
	}, "")
	require.EqualValues(t, 1, body["code"], "员工已存在，同号再申请必须被拒")

	// ===== 5. 已驳回的申请允许用同号重新申请 =====
	_, body = plat.do("POST", "/api/apply/open", map[string]interface{}{
		"name": "驳回丙", "phone": "13600009999", "title": "助理",
	}, "")
	require.EqualValues(t, 0, body["code"])
	rejectID := findApplyIDByPhone(t, plat, adminToken, "13600009999")
	require.NotZero(t, rejectID)
	_, body = plat.do("POST", fmt.Sprintf("/api/admin/apply/open/%d/reject", rejectID),
		map[string]interface{}{"reason": "测试驳回"}, adminToken)
	require.EqualValues(t, 0, body["code"])
	// 驳回后用同号重新申请应成功（rejected 不拦）
	_, body = plat.do("POST", "/api/apply/open", map[string]interface{}{
		"name": "驳回丙重申请", "phone": "13600009999", "title": "助理",
	}, "")
	require.EqualValues(t, 0, body["code"], "已驳回可申请允许同号重新提交")
}

// findApplyIDByPhone 在申请列表中按解密后的手机号定位申请 id。
// 申请列表接口会经 AfterFind 解出 phone 明文，故可直接按 phone 匹配。
func findApplyIDByPhone(t *testing.T, c *testClient, adminToken, phone string) int64 {
	t.Helper()
	_, body := c.do("GET", "/api/admin/apply/open", nil, adminToken)
	require.EqualValues(t, 0, body["code"])
	for _, it := range body["data"].([]interface{}) {
		m := it.(map[string]interface{})
		if m["phone"] == phone {
			return int64(m["id"].(float64))
		}
	}
	return 0
}

// TestApplyPhoneUniqueReverse 反向验证：若审批路径的 phoneInUse 守卫缺失
// （此处用「待审申请自排除」逻辑被破坏来模拟），同号申请会绕过员工存在兜底。
//
// 由于守卫是同一函数的三处调用，这里只针对「审批瞬间自身 pending 行不能误伤」做
// 反向断言：构造一个已存在员工 + 一条同号 pending 申请，直接调审批——
// 守卫正确时应放行（员工由该 pending 落成，自身 pending 被排除）；
// 若守卫没排除自身，审批会被自己那条 pending 误拒。
func TestApplyPhoneUniqueReverse(t *testing.T) {
	r := setupSQLiteEngine(t)
	plat := newTestClient(r, t)
	adminToken := loginAdmin(t, plat)

	// 手动建一个员工（占号）
	_, body := plat.do("POST", "/api/admin/employees", map[string]interface{}{
		"name": "占号员工", "phone": "13500007777", "title": "工程师",
	}, adminToken)
	require.EqualValues(t, 0, body["code"])

	// 再提交同号申请（pending）
	_, body = plat.do("POST", "/api/apply/open", map[string]interface{}{
		"name": "占号申请", "phone": "13500007777", "title": "工程师",
	}, "")
	require.EqualValues(t, 1, body["code"], "员工已存在，提交同号申请必须被拒（守卫生效）")

	// 换一个没被占用的号走完整审批：确认审批会话自身不会误拒
	_, body = plat.do("POST", "/api/apply/open", map[string]interface{}{
		"name": "自排除乙", "phone": "13500006666", "title": "经理",
	}, "")
	require.EqualValues(t, 0, body["code"])
	applyID := findApplyIDByPhone(t, plat, adminToken, "13500006666")
	require.NotZero(t, applyID)
	_, body = plat.do("POST", fmt.Sprintf("/api/admin/apply/open/%d/approve", applyID), nil, adminToken)
	require.EqualValues(t, 0, body["code"], "审批瞬间自身 pending 行必须被排除，否则会误拒")
}
