package main

// 生成器的**数据源选择**与**落盘安全**守门。
//
// 这一组测试的存在理由：本工具的输出决定线上哪些 /uploads/t/<cid>/ 被代理到
// 哪个桶。它的失败模式几乎全是静默的——读错源、读成空、写半截，三种都不会
// 报错，只会让某家租户的图 404（而那家租户只会以为是自己的网络问题）。

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"weichuan/internal/dbopen"
	"weichuan/internal/model"
	"weichuan/internal/repository"
)

func TestResolveTargetsRequiresExplicitSource(t *testing.T) {
	_, err := resolveTargets("", "")
	require.Error(t, err)
	require.Contains(t, err.Error(), "--dsn", "错误要告诉运维该怎么填")
	require.Contains(t, err.Error(), "--targets")
}

// 两个数据源同时给时必须拒绝，而不是「按某种优先级选一个」：
// --targets 经常是上一次演练残留的文件，选错的后果是一份看着正常、
// 但里面全是演练配置的 snippet 被推上线。
func TestResolveTargetsRejectsBothSources(t *testing.T) {
	_, err := resolveTargets("tenants.json", "root:pw@tcp(127.0.0.1:3306)/weichuan")
	require.Error(t, err)
	require.Contains(t, err.Error(), "互斥")
}

// 表缺失要能穿透到调用方，且**不能**退化成空列表 + nil。
// 空列表 + nil 会让 render 产出一份「只有注释」的 snippet 并落盘，
// 等于把所有租户撕回平台盘。
func TestLoadTargetsFromDBMissingTable(t *testing.T) {
	dsn := filepath.Join(t.TempDir(), "empty.db")
	got, err := loadTargetsFromDB(dsn)
	require.Error(t, err)
	require.True(t, errors.Is(err, repository.ErrProfileTablesMissing),
		"表缺失的错误要保留哨兵身份（文案自带处置步骤），实际：%v", err)
	require.Contains(t, err.Error(), "0054")
	require.Nil(t, got)
}

// 正常读取：档案 + 绑定映射到渲染目标，driver 的判定交给 NeedsSnippet。
func TestLoadTargetsFromDBMapsRows(t *testing.T) {
	dsn := seedProfilesDB(t, seedProfileData{
		companies: []model.Company{{ID: 4, Name: "四家"}, {ID: 7, Name: "七家"}},
		profiles: []model.StorageProfile{
			{Name: "本地", Driver: "local", PublicRead: true, UseSSL: true, Version: 1},
			{Name: "七家桶", Driver: "minio", Endpoint: "minio.c7.example.com", Bucket: "c7",
				UseSSL: true, HostHeader: "c7.minio.example.com", PublicRead: true, Version: 1, Status: "ok"},
		},
		bindings: []model.StorageBinding{
			{TenantID: 4, StorageProfileID: 1, Version: 1},
			{TenantID: 7, StorageProfileID: 2, Version: 1},
		},
	})

	got, err := loadTargetsFromDB(dsn)
	require.NoError(t, err)
	require.Len(t, got, 2)
	require.Equal(t, int64(4), got[0].CompanyID, "按 company_id 升序，输出稳定")
	require.False(t, got[0].NeedsSnippet(), "本地档案没有桶可回源")
	require.True(t, got[1].NeedsSnippet())
	require.Equal(t, "c7.minio.example.com", got[1].HostHeader)
}

// 全局绑定必须展开成逐家 location：nginx 没有「全局 location」这种形态，
// 漏展开等于所有企业都回落平台盘。
func TestLoadTargetsFromDBExpandsGlobalBinding(t *testing.T) {
	dsn := seedProfilesDB(t, seedProfileData{
		companies: []model.Company{{ID: 3, Name: "三家"}, {ID: 7, Name: "七家"}},
		profiles: []model.StorageProfile{
			{Name: "全局桶", Driver: "minio", Endpoint: "s.example.com", Bucket: "b", PublicRead: true, Version: 1},
		},
		bindings: []model.StorageBinding{{TenantID: 0, StorageProfileID: 1, Version: 1}},
	})

	got, err := loadTargetsFromDB(dsn)
	require.NoError(t, err)
	require.Len(t, got, 2, "全局绑定要展开成两家")
	require.Equal(t, int64(3), got[0].CompanyID)
	require.Equal(t, int64(7), got[1].CompanyID)
	require.True(t, got[0].NeedsSnippet() && got[1].NeedsSnippet(), "两家都要生成 location")
}

// 端到端：render --dsn 落盘 → check --dsn 对账通过。
// 这条同时验证「两个子命令用的是同一个数据源」，否则对账永远绿。
// 也钉住「endpoint 的 scheme 必须剥掉」——档案界面允许填 https://，
// 拼进 proxy_pass 会得到 https://https://。
func TestRenderAndCheckRoundTrip(t *testing.T) {
	dsn := seedProfilesDB(t, seedProfileData{
		companies: []model.Company{{ID: 7, Name: "七家"}},
		profiles: []model.StorageProfile{{
			Name: "桶", Driver: "minio", Endpoint: "https://minio.c7.example.com", Bucket: "c7",
			UseSSL: true, HostHeader: "c7.minio.example.com", PublicRead: true, Version: 1, Status: "ok",
		}},
		bindings: []model.StorageBinding{{TenantID: 0, StorageProfileID: 1, Version: 1}},
	})
	out := filepath.Join(t.TempDir(), "storage.conf")

	require.NoError(t, runRender([]string{"--dsn", dsn, "--out", out}))
	body, err := os.ReadFile(out)
	require.NoError(t, err)
	require.Contains(t, string(body), "location ^~ /uploads/t/7/ {", "全局绑定要展开成逐家 location")
	require.Contains(t, string(body), `proxy_set_header Host "c7.minio.example.com";`)
	require.NotContains(t, string(body), "https://https://", "endpoint 的 scheme 必须被剥掉")

	require.NoError(t, runCheck([]string{"--dsn", dsn, "--snippet", out}))
}

// 绑到本地档案的租户必须从 snippet 里消失，否则对账会报 Extra——这条用
// render + check 串起来验证，比单测 NeedsSnippet 更接近现场。
func TestRenderDropsLocalProfileTenant(t *testing.T) {
	dsn := seedProfilesDB(t, seedProfileData{
		companies: []model.Company{{ID: 7, Name: "七家"}, {ID: 8, Name: "八家"}},
		profiles: []model.StorageProfile{
			{Name: "本地", Driver: "local", PublicRead: true, UseSSL: true, Version: 1},
			{Name: "八家桶", Driver: "oss", Endpoint: "oss.example.com", Bucket: "c8", PublicRead: true, Version: 1, Status: "ok"},
		},
		bindings: []model.StorageBinding{
			{TenantID: 7, StorageProfileID: 1, Version: 1},
			{TenantID: 8, StorageProfileID: 2, Version: 1},
		},
	})
	out := filepath.Join(t.TempDir(), "storage.conf")
	require.NoError(t, runRender([]string{"--dsn", dsn, "--out", out}))

	body, err := os.ReadFile(out)
	require.NoError(t, err)
	require.NotContains(t, string(body), "/uploads/t/7/", "本地档案不该留 location")
	require.Contains(t, string(body), "/uploads/t/8/")
	require.NoError(t, runCheck([]string{"--dsn", dsn, "--snippet", out}))
}

// 渲染结果零 location + 目标文件非空 = 一次静默的线上清空，默认必须拒绝。
//
// 三种现场都会走到这里：绑定被误清、DSN 指到空库、容器里挂错卷。
// 它们的报错都不明显，所以拦在落盘前。
func TestRenderRefusesToWipeExistingSnippet(t *testing.T) {
	dsn := seedProfilesDB(t, seedProfileData{}) // 表存在但 0 行
	out := filepath.Join(t.TempDir(), "storage.conf")
	const before = "# 上一版生成的 location（非空）\n    location ^~ /uploads/t/7/ { }\n"
	require.NoError(t, os.WriteFile(out, []byte(before), 0o644))

	err := runRender([]string{"--dsn", dsn, "--out", out})
	require.Error(t, err)
	require.Contains(t, err.Error(), "--allow-empty", "要告诉运维怎么表达「我确实要清空」")

	after, readErr := os.ReadFile(out)
	require.NoError(t, readErr)
	require.Equal(t, before, string(after), "拒绝时绝不能动文件")

	// 显式 --allow-empty 才允许覆盖。
	require.NoError(t, runRender([]string{"--dsn", dsn, "--out", out, "--allow-empty"}))
	body, readErr := os.ReadFile(out)
	require.NoError(t, readErr)
	require.NotContains(t, string(body), "/uploads/t/7/")
	require.Contains(t, string(body), "没有任何租户接管存储")
}

// 首次部署（目标文件不存在）时零 location 是正常状态，不该被守卫拦住。
func TestRenderAllowsEmptyWhenNoExistingFile(t *testing.T) {
	dsn := seedProfilesDB(t, seedProfileData{})
	out := filepath.Join(t.TempDir(), "first-deploy.conf")
	require.NoError(t, runRender([]string{"--dsn", dsn, "--out", out}))
	require.FileExists(t, out)
}

// 原子落盘：内容正确、权限 0644（nginx worker 要读）、目录里不留临时文件。
func TestWriteFileAtomic(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "storage.conf")
	require.NoError(t, os.WriteFile(path, []byte("old"), 0o644))

	require.NoError(t, writeFileAtomic(path, []byte("new-content")))

	body, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, "new-content", string(body))

	st, err := os.Stat(path)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o644), st.Mode().Perm(),
		"CreateTemp 建的是 0600，忘记 chmod 会让 nginx 读不到配置")

	leftovers, err := filepath.Glob(filepath.Join(dir, ".nginx-snippets-*"))
	require.NoError(t, err)
	require.Empty(t, leftovers, "临时文件必须被 rename 或删除，不能堆积在 nginx 配置目录里")
}

// seedProfileData 是 seedProfilesDB 的输入：档案、绑定、企业三张表一起建。
type seedProfileData struct {
	companies []model.Company
	profiles  []model.StorageProfile
	bindings  []model.StorageBinding
}

// seedProfilesDB 建一个已应用 0054 的 SQLite 库，返回 DSN。
// profiles 按切片顺序插入，因此第一条的 id 恒为 1（测试里直接引用）。
func seedProfilesDB(t *testing.T, d seedProfileData) string {
	t.Helper()
	dsn := filepath.Join(t.TempDir(), "wc-profiles.db")
	db, _, err := dbopen.Open(dsn)
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(
		&model.Company{},
		&model.StorageProfile{}, &model.DeliveryProfile{}, &model.StorageBinding{},
	))
	for _, c := range d.companies {
		row := c
		if row.Status == "" {
			row.Status = "enabled"
		}
		require.NoError(t, db.Create(&row).Error)
	}
	for _, p := range d.profiles {
		row := p
		require.NoError(t, db.Create(&row).Error)
	}
	for _, b := range d.bindings {
		row := b
		require.NoError(t, db.Create(&row).Error)
	}
	sqlDB, err := db.DB()
	require.NoError(t, err)
	require.NoError(t, sqlDB.Close())
	return dsn
}
