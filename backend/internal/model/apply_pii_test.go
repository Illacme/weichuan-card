package model_test

import (
	"path/filepath"
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"weichuan/internal/model"
	"weichuan/internal/pii"
)

func openApplyPiiTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "apply.db")), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&model.OpenApply{}, &model.CardModifyApply{}))
	return db
}

// TestOpenApplyPIIEncryptedAtRest 守卫：申请单落库为密文、盲索引可检索、读回解密。
func TestOpenApplyPIIEncryptedAtRest(t *testing.T) {
	db := openApplyPiiTestDB(t)
	a := model.OpenApply{CompanyID: 1, Name: "申请人", Phone: "13800001111", Email: "o@a.com", Status: "pending"}
	require.NoError(t, db.Create(&a).Error)

	var raw struct {
		Name, Phone, Email, PhoneBidx string
	}
	require.NoError(t, db.Raw(
		"SELECT name, phone, email, phone_bidx FROM open_applies WHERE id = ?", a.ID).Scan(&raw).Error)
	require.True(t, pii.IsEncrypted(raw.Name), "申请人姓名应以密文落库")
	require.True(t, pii.IsEncrypted(raw.Phone), "申请人手机应以密文落库")
	require.NotContains(t, raw.Name, "申请人")
	require.Equal(t, pii.BlindIndex("13800001111"), raw.PhoneBidx)

	var got model.OpenApply
	require.NoError(t, db.First(&got, "id = ?", a.ID).Error)
	require.Equal(t, "申请人", got.Name)
	require.Equal(t, "13800001111", got.Phone)

	var byPhone model.OpenApply
	require.NoError(t, db.First(&byPhone, "phone_bidx = ?", pii.BlindIndex("13800001111")).Error)
	require.Equal(t, a.ID, byPhone.ID)
}

// TestCardModifySnapshotEncryptedAtRest 守卫：修改快照整段密文落库、读回还原 JSON。
func TestCardModifySnapshotEncryptedAtRest(t *testing.T) {
	db := openApplyPiiTestDB(t)
	snap := `{"name":"张三","phone":"13800138000"}`
	app := model.CardModifyApply{CompanyID: 1, StaffID: 1, Snapshot: snap, Status: "pending"}
	require.NoError(t, db.Create(&app).Error)

	var raw struct{ Snapshot string }
	require.NoError(t, db.Raw("SELECT snapshot FROM card_modify_applies WHERE id = ?", app.ID).Scan(&raw).Error)
	require.True(t, pii.IsEncrypted(raw.Snapshot), "快照应以密文落库")
	require.NotContains(t, raw.Snapshot, "张三")
	require.NotContains(t, raw.Snapshot, "13800138000")

	var got model.CardModifyApply
	require.NoError(t, db.First(&got, "id = ?", app.ID).Error)
	require.Equal(t, snap, got.Snapshot, "读回应还原为原 JSON")
}

// TestBackfillAppliesAndSnapshots 覆盖升级：历史明文申请单/快照经 BackfillPII 转密文。
func TestBackfillAppliesAndSnapshots(t *testing.T) {
	db := openApplyPiiTestDB(t)
	require.NoError(t, db.Exec(
		"INSERT INTO open_applies (company_id, name, phone, email, nickname, status) "+
			"VALUES (1, '王五', '13600136000', 'w@a.com', '', 'pending')").Error)
	require.NoError(t, db.Exec(
		"INSERT INTO card_modify_applies (staff_id, snapshot, status) "+
			"VALUES (1, '{\"name\":\"李四\"}', 'pending')").Error)

	require.NoError(t, model.BackfillPII(db))

	var oa struct{ Name, Phone, PhoneBidx string }
	require.NoError(t, db.Raw("SELECT name, phone, phone_bidx FROM open_applies").Scan(&oa).Error)
	require.True(t, pii.IsEncrypted(oa.Name), "回填后申请单姓名应为密文")
	require.True(t, pii.IsEncrypted(oa.Phone), "回填后申请单手机应为密文")
	require.Equal(t, pii.BlindIndex("13600136000"), oa.PhoneBidx)

	var cm struct{ Snapshot string }
	require.NoError(t, db.Raw("SELECT snapshot FROM card_modify_applies").Scan(&cm).Error)
	require.True(t, pii.IsEncrypted(cm.Snapshot), "回填后快照应为密文")

	// 幂等
	require.NoError(t, model.BackfillPII(db))

	var got model.OpenApply
	require.NoError(t, db.First(&got, "phone_bidx = ?", pii.BlindIndex("13600136000")).Error)
	require.Equal(t, "王五", got.Name)
}
