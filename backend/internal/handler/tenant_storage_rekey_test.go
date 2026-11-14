package handler

import (
	"bytes"
	"encoding/base64"
	"testing"

	"weichuan/internal/model"
	"weichuan/internal/pii"

	"github.com/stretchr/testify/require"
)

// b64Rep 造一把确定性的 base64 32 字节密钥（全部字节相同），用于 re-key 测试里
// 区分「旧密钥 / 新密钥 / 未知第三把」。
func b64Rep(b byte) string {
	return base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{b}, 32))
}

// TestTenantStoragePutRecordsKeyID 验证 PUT 写入凭据时会把 key_id 记成当前密钥指纹，
// 且不传凭据时 key_id 不被覆盖（否则 re-key 会漏掉这行）。
func TestTenantStoragePutRecordsKeyID(t *testing.T) {
	r, db := bindingEngine(t)
	c := newTestClient(r, t)
	plat := loginAdmin(t, c)
	stubStorageProbeOK(t)
	const path = "/api/admin/companies/1/storage"

	// 首次 PUT 带凭据：key_id 必须记录当前密钥指纹。
	doStorage(t, c, "PUT", path, storagePayload(), plat)
	row := loadStorageRow(t, db, 1)
	require.NotEmpty(t, row.KeyID, "带凭据写入后 key_id 必须记录当前密钥指纹")
	require.Equal(t, pii.CurrentKeyID(), row.KeyID, "key_id 必须等于当前 pii 密钥指纹")

	// 同一把密钥下重复写入，key_id 不应漂变。
	doStorage(t, c, "PUT", path, storagePayload(), plat)
	row2 := loadStorageRow(t, db, 1)
	require.Equal(t, row.KeyID, row2.KeyID, "同一把密钥下重复写入，key_id 不应漂变")

	// 只改 endpoint、不传凭据（缺省字段=保留）：key_id 必须保持原值，不被覆盖成空。
	noCred := storagePayload()
	delete(noCred, "accessKey")
	delete(noCred, "secretKey")
	noCred["endpoint"] = "play2.min.io"
	doStorage(t, c, "PUT", path, noCred, plat)
	row3 := loadStorageRow(t, db, 1)
	require.Equal(t, row.KeyID, row3.KeyID, "不传凭据时 key_id 必须保持原值，不被覆盖")
	require.Equal(t, "play2.min.io", row3.Endpoint, "endpoint 应被更新")
}

// TestRekeyTenantStorageRefreshesKeyID 是停机式 re-key 的正向证据：
// 用旧密钥加密的存量行，提供旧密钥后能被解密 → 用新密钥重加密，key_id 刷新为新指纹，
// 且重加密后凭据仍能用当前（新）密钥解密出原明文。
func TestRekeyTenantStorageRefreshesKeyID(t *testing.T) {
	_, db := bindingEngine(t)
	keyOld := b64Rep(1)
	keyNew := b64Rep(2)
	defer pii.SetKey("") // 恢复内置 dev 密钥，避免影响其它测试

	// 用旧密钥加密一行凭据，key_id 留空（模拟换钥前的存量行）。
	akOld, err := pii.EncryptWithKey(keyOld, "AK-OLD")
	require.NoError(t, err)
	skOld, err := pii.EncryptWithKey(keyOld, "SK-OLD")
	require.NoError(t, err)
	require.NoError(t, db.Create(&model.TenantStorageConfig{
		CompanyID: 1, Driver: "minio", Bucket: "b", Endpoint: "e", UseSSL: true,
		PublicRead: true, Enabled: true, AccessKeyEnc: akOld, SecretKeyEnc: skOld, KeyID: "",
	}).Error)

	// 切到新密钥：re-key 的目标（当前进程以新密钥装载）。
	require.NoError(t, pii.SetKey(keyNew))
	require.NotEmpty(t, pii.CurrentKeyID())

	n, err := RekeyTenantStorage(db, keyOld)
	require.NoError(t, err)
	require.Equal(t, 1, n, "应处理 1 行")

	row := loadStorageRow(t, db, 1)
	require.Equal(t, pii.CurrentKeyID(), row.KeyID, "re-key 后 key_id 应等于新密钥指纹")
	ak, err := pii.Decrypt(row.AccessKeyEnc) // 当前=新密钥
	require.NoError(t, err)
	require.Equal(t, "AK-OLD", ak, "re-key 后凭据仍能用当前（新）密钥解密出原明文")
	sk, err := pii.Decrypt(row.SecretKeyEnc)
	require.NoError(t, err)
	require.Equal(t, "SK-OLD", sk)
}

// TestRekeyTenantStorageFailsOnUnknownKey 是 re-key 的反向验证：
// 若存在一行既非当前密钥、也非所给旧密钥能解密的密文，re-key 必须失败返回错误，
// 绝不能静默跳过（跳过等于把这条凭据永久弄丢）。验证方式是塞一把第三方的密钥密文、
// 且只提供新密钥当 oldB64——它必然解不开，函数应中止且不改写任何行。
func TestRekeyTenantStorageFailsOnUnknownKey(t *testing.T) {
	_, db := bindingEngine(t)
	keyNew := b64Rep(2)
	keyThird := b64Rep(9) // 既非旧也非新
	defer pii.SetKey("")

	akThird, err := pii.EncryptWithKey(keyThird, "AK-3RD")
	require.NoError(t, err)
	require.NoError(t, db.Create(&model.TenantStorageConfig{
		CompanyID: 1, Driver: "minio", AccessKeyEnc: akThird, SecretKeyEnc: akThird, KeyID: "",
	}).Error)

	require.NoError(t, pii.SetKey(keyNew))
	// 把 keyNew 当旧密钥传入：仍解不开 keyThird 的密文，re-key 必须失败。
	n, err := RekeyTenantStorage(db, keyNew)
	require.Error(t, err, "未知密钥加密的行无法解密，re-key 必须失败而非静默跳过")
	require.Equal(t, 0, n)

	// 失败不应改写任何行：密文与 key_id 都保持原样。
	row := loadStorageRow(t, db, 1)
	require.Equal(t, "", row.KeyID)
	require.Equal(t, akThird, row.AccessKeyEnc, "失败不应改写任何行")
}
