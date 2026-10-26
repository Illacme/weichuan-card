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

func openPiiTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "pii.db")), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&model.Employee{}))
	return db
}

// TestEmployeePIIEncryptedAtRest 是「加密真的发生了」的守卫：
// 落库的列必须是密文（v1: 前缀）、不等于明文，且读回能正确解密、盲索引能命中。
// 若有人把 BeforeCreate/AfterFind 的加解密删掉，这里会立刻变红——正是反向验证要盯的点。
func TestEmployeePIIEncryptedAtRest(t *testing.T) {
	db := openPiiTestDB(t)
	e := model.Employee{
		CompanyID: 1, Name: "张三", Phone: "13800138000", Phone2: "13900139000",
		Email: "a@b.com", Nickname: "昵称X", Status: "enabled",
	}
	require.NoError(t, db.Create(&e).Error)

	var raw struct {
		Name, Phone, Email, Nickname string
		NameBidx, PhoneBidx          string
	}
	require.NoError(t, db.Raw(
		"SELECT name, phone, email, nickname, name_bidx, phone_bidx FROM employees WHERE staff_id = ?",
		e.StaffID).Scan(&raw).Error)

	require.True(t, pii.IsEncrypted(raw.Name), "姓名应以密文落库")
	require.True(t, pii.IsEncrypted(raw.Phone), "手机应以密文落库")
	require.True(t, pii.IsEncrypted(raw.Email), "邮箱应以密文落库")
	require.NotEqual(t, "13800138000", raw.Phone, "库里不得出现明文手机号")
	require.NotContains(t, raw.Name, "张三", "库里不得出现明文姓名")
	require.Equal(t, pii.BlindIndex("13800138000"), raw.PhoneBidx, "手机盲索引应可检索")
	require.Equal(t, pii.BlindIndex("张三"), raw.NameBidx, "姓名盲索引应可检索")

	// 读回（走 AfterFind）应还原明文
	var got model.Employee
	require.NoError(t, db.First(&got, "staff_id = ?", e.StaffID).Error)
	require.Equal(t, "张三", got.Name)
	require.Equal(t, "13800138000", got.Phone)
	require.Equal(t, "a@b.com", got.Email)

	// 登录式等值查询：按盲索引命中
	var byPhone model.Employee
	require.NoError(t, db.First(&byPhone, "phone_bidx = ?", pii.BlindIndex("13800138000")).Error)
	require.Equal(t, e.StaffID, byPhone.StaffID)
}

// TestBackfillEncryptsLegacyPlaintext 覆盖升级场景：库里已有历史明文行（绕过钩子写入），
// BackfillEmployees 应把它加密并补盲索引，且幂等（再跑一次不出错、数据仍可读）。
func TestBackfillEncryptsLegacyPlaintext(t *testing.T) {
	db := openPiiTestDB(t)
	// 直接写原始列，模拟「未加密的历史数据」
	require.NoError(t, db.Exec(
		"INSERT INTO employees (company_id, name, phone, phone2, email, nickname, status) "+
			"VALUES (1, '李四', '13700137000', '', 'l@a.com', '', 'enabled')").Error)

	require.NoError(t, model.BackfillEmployees(db))

	var raw struct {
		Name, Phone, NameBidx, PhoneBidx string
	}
	require.NoError(t, db.Raw("SELECT name, phone, name_bidx, phone_bidx FROM employees").Scan(&raw).Error)
	require.True(t, pii.IsEncrypted(raw.Name), "回填后姓名应为密文")
	require.True(t, pii.IsEncrypted(raw.Phone), "回填后手机应为密文")
	require.Equal(t, pii.BlindIndex("13700137000"), raw.PhoneBidx)

	// 幂等：重复回填不报错
	require.NoError(t, model.BackfillEmployees(db))

	// 回填后仍能按手机盲索引查到、且解密正确
	var got model.Employee
	require.NoError(t, db.First(&got, "phone_bidx = ?", pii.BlindIndex("13700137000")).Error)
	require.Equal(t, "李四", got.Name)
	require.Equal(t, "13700137000", got.Phone)
}
