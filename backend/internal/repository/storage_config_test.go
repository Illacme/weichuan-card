package repository

// 生成器读配置表的行为守门（迁移 0044 / tenant_storage_configs）。
//
// 这组测试盯的不是「能不能读出来」，而是**三种结果彼此可区分**：
// 表不存在 / 查询出错 / 真的没人配。前两种若退化成第三种，症状是
// 「生成器成功、snippet 为空、nginx -t 通过、平台照常工作」，直到某家租户
// 上传完发现图全裂——而那时距离出错已经很久了。

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"weichuan/internal/dbopen"
	"weichuan/internal/model"
)

// newStorageTestDB 建一个只有租户存储配置表的临时库。
// migrate=true 时会 AutoMigrate（模拟「迁移已应用」）。
func newStorageTestDB(t *testing.T, migrate bool) *gorm.DB {
	t.Helper()
	db, _, err := dbopen.Open(filepath.Join(t.TempDir(), "storage.db"))
	require.NoError(t, err)
	if migrate {
		require.NoError(t, db.AutoMigrate(&model.TenantStorageConfig{}))
	}
	return db
}

func seedStorageRow(t *testing.T, db *gorm.DB, row model.TenantStorageConfig) {
	t.Helper()
	if row.Status == "" {
		row.Status = "unknown"
	}
	require.NoError(t, db.Create(&row).Error)
}

// 映射必须逐字段到位，且**不过滤** enabled=false / driver=local：
// 判定只在 nginxsnippet.Target.NeedsSnippet 一处，repository 提前过滤会让
// 「驱动改成 local 后 snippet 残留」这类问题换个地方复现。
func TestListStorageTargetsMapsAllRows(t *testing.T) {
	db := newStorageTestDB(t, true)

	seedStorageRow(t, db, model.TenantStorageConfig{
		CompanyID: 7, Driver: "minio", Endpoint: "minio.c7.example.com",
		Bucket: "c7", UseSSL: true, HostHeader: "c7.minio.example.com",
		Enabled: true, Status: "ok",
	})
	// 停用 + local：两条都不该生成 snippet，但都必须出现在结果里。
	seedStorageRow(t, db, model.TenantStorageConfig{
		CompanyID: 3, Driver: "oss", Endpoint: "oss-cn-hangzhou.aliyuncs.com",
		Bucket: "c3", Enabled: false, Status: "error",
	})
	seedStorageRow(t, db, model.TenantStorageConfig{
		CompanyID: 5, Driver: "local", Enabled: true,
	})

	got, err := ListStorageTargets(db)
	require.NoError(t, err)
	require.Len(t, got, 3, "未启用 / local 的行也必须返回，交给 NeedsSnippet 判定")

	// 按 company_id 升序：输出稳定，运维 diff 两份 snippet 时不被行序变化干扰。
	require.Equal(t, []int64{3, 5, 7}, []int64{got[0].CompanyID, got[1].CompanyID, got[2].CompanyID})

	c7 := got[2]
	require.Equal(t, "minio", c7.Driver)
	require.Equal(t, "minio.c7.example.com", c7.Endpoint)
	require.Equal(t, "c7", c7.Bucket)
	require.True(t, c7.UseSSL)
	require.Equal(t, "c7.minio.example.com", c7.HostHeader)
	require.True(t, c7.Enabled)
	require.Equal(t, "ok", c7.Status)

	// 映射到渲染目标后，NeedsSnippet 的结论必须与库里的配置一致。
	require.True(t, c7.NginxTarget().NeedsSnippet(), "启用 + minio 应生成 location")
	require.False(t, got[0].NginxTarget().NeedsSnippet(), "enabled=false 不该生成 location")
	require.False(t, got[1].NginxTarget().NeedsSnippet(), "driver=local 不该生成 location")
}

// 表不存在必须是**可识别的哨兵错误**，而不是一句混在一起的 Error 1146 / no such table。
// 调用方要靠它给出「先跑 ./server migrate」的处置建议。
func TestListStorageTargetsMissingTableIsRecognizable(t *testing.T) {
	db := newStorageTestDB(t, false)

	got, err := ListStorageTargets(db)
	require.Error(t, err)
	require.True(t, errors.Is(err, ErrStorageConfigMissing),
		"表缺失必须能被 errors.Is 命中，否则调用方只能靠字符串匹配；实际错误：%v", err)
	require.Nil(t, got, "失败时不要返回空切片：那与「真的没人配」无法区分")
	require.Contains(t, err.Error(), "0044", "错误信息要指向具体迁移，运维才知道跑哪个版本")
}

// 表存在但没有行 = 「当前没有任何租户接管存储」，是**正常成功**，不是错误。
// 这条与上一条互为反面：两者都返回 len==0，唯一区别就是 err 是否为 nil，
// 所以任何一个被写坏都会让另一个失去意义。
func TestListStorageTargetsEmptyIsSuccess(t *testing.T) {
	db := newStorageTestDB(t, true)

	got, err := ListStorageTargets(db)
	require.NoError(t, err, "空表是合法状态（还没有租户接管存储），不能当错误")
	require.Empty(t, got)
}

// 凭据列绝不能进 SELECT：生成器跑在能写 nginx 配置的机器上，
// 把 AccessKey/SecretKey 的密文读进内存没有收益，只有风险。
//
// 这条用静态断言 + DryRun 双重钉住：只改注释不改 SQL 时它必须红。
func TestListStorageTargetsNeverSelectsCredentials(t *testing.T) {
	cols := strings.ToLower(storageTargetCols)
	for _, banned := range []string{"access_key_enc", "secret_key_enc", "*"} {
		require.NotContains(t, cols, banned,
			"storageTargetCols 含 %q：凭据列不该被生成器读到", banned)
	}

	// DryRun 只生成 SQL 不执行，用于确认 gorm 拼出来的语句里确实没有这两列
	// （防止有人把 Select 改成链式调用后悄悄放宽）。
	db := newStorageTestDB(t, true)
	var rows []model.TenantStorageConfig
	stmt := db.Session(&gorm.Session{DryRun: true}).
		Model(&model.TenantStorageConfig{}).
		Select(storageTargetCols).
		Find(&rows).Statement
	sql := strings.ToLower(stmt.SQL.String())
	require.NotContains(t, sql, "access_key_enc")
	require.NotContains(t, sql, "secret_key_enc")
}

// 凭据列在库里是可读可写的（只是不出现在生成器视图里），
// 这里顺带钉住「写入必须经 pii.Encrypt」的落点没有被模型层的钩子偷偷解密。
func TestStorageConfigCredentialColumnsAreOpaque(t *testing.T) {
	db := newStorageTestDB(t, true)
	seedStorageRow(t, db, model.TenantStorageConfig{
		CompanyID:    9,
		Driver:       "s3",
		Endpoint:     "s3.example.com",
		Bucket:       "c9",
		AccessKeyEnc: "v1:aaa",
		SecretKeyEnc: "v1:bbb",
		Enabled:      true,
		Status:       "unknown",
		LastCheckedAt: func() *time.Time {
			tm := time.Now()
			return &tm
		}(),
	})

	var raw model.TenantStorageConfig
	require.NoError(t, db.First(&raw, "company_id = ?", 9).Error)
	require.Equal(t, "v1:aaa", raw.AccessKeyEnc, "模型层不许对密文列做任何自动解密")
	require.Equal(t, "v1:bbb", raw.SecretKeyEnc)
	require.NotNil(t, raw.LastCheckedAt)
}
