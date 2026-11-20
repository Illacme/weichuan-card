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
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"weichuan/internal/dbopen"
	"weichuan/internal/model"
	"weichuan/internal/nginxsnippet"
	"weichuan/internal/repository"
)

func TestResolveTargetsRequiresExplicitSource(t *testing.T) {
	_, err := resolveTargets("", "", sourceAuto)
	require.Error(t, err)
	require.Contains(t, err.Error(), "--dsn", "错误要告诉运维该怎么填")
	require.Contains(t, err.Error(), "--targets")
}

// 两个数据源同时给时必须拒绝，而不是「按某种优先级选一个」：
// --targets 经常是上一次演练残留的文件，选错的后果是一份看着正常、
// 但里面全是演练配置的 snippet 被推上线。
func TestResolveTargetsRejectsBothSources(t *testing.T) {
	_, err := resolveTargets("tenants.json", "root:pw@tcp(127.0.0.1:3306)/weichuan", sourceAuto)
	require.Error(t, err)
	require.Contains(t, err.Error(), "互斥")
}

// --source 拼错必须当场拒绝：静默按 auto 处理会让「我以为在读档案」变成
// 「实际读的是旧表」，而两种结果都成功、都产出看着正常的 snippet。
func TestParseSourceRejectsUnknown(t *testing.T) {
	_, err := parseSource("profile")
	require.Error(t, err)
	require.Contains(t, err.Error(), "auto / legacy / profiles")

	for in, want := range map[string]string{"": sourceAuto, "AUTO": sourceAuto, " legacy ": sourceLegacy, "profiles": sourceProfiles} {
		got, err := parseSource(in)
		require.NoError(t, err, "输入 %q", in)
		require.Equal(t, want, got, "输入 %q", in)
	}
}

// --source 只对 --dsn 有意义：给 --targets 配 --source 是误用，
// 静默忽略等于让运维以为自己在读档案。
func TestResolveTargetsRejectsSourceWithTargetsFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tenants.json")
	require.NoError(t, os.WriteFile(path, []byte(`{"tenants":[]}`), 0o644))
	_, err := resolveTargets(path, "", sourceProfiles)
	require.Error(t, err)
	require.Contains(t, err.Error(), "只对 --dsn 生效")
}

// 表缺失要能穿透到调用方，且**不能**退化成空列表 + nil。
func TestLoadTargetsFromDBMissingTable(t *testing.T) {
	dsn := filepath.Join(t.TempDir(), "empty.db")
	got, err := loadTargetsFromDB(dsn, sourceAuto)
	require.Error(t, err)
	require.True(t, errors.Is(err, repository.ErrStorageConfigMissing),
		"表缺失的错误要保留哨兵身份（调用方据此给处置步骤），实际：%v", err)
	require.Nil(t, got)
}

// 正常读取：库里的行映射到渲染目标，且 enabled / driver 的判定交给 NeedsSnippet。
func TestLoadTargetsFromDBMapsRows(t *testing.T) {
	dsn := seedDB(t, []model.TenantStorageConfig{
		{CompanyID: 7, Driver: "minio", Endpoint: "minio.c7.example.com", Bucket: "c7",
			UseSSL: true, HostHeader: "c7.minio.example.com", Enabled: true, Status: "ok"},
		{CompanyID: 4, Driver: "local", Enabled: true, Status: "ok"},
	})

	got, err := loadTargetsFromDB(dsn, sourceAuto)
	require.NoError(t, err)
	require.Len(t, got, 2)
	require.Equal(t, int64(4), got[0].CompanyID, "按 company_id 升序，输出稳定")
	require.False(t, got[0].NeedsSnippet())
	require.True(t, got[1].NeedsSnippet())
	require.Equal(t, "c7.minio.example.com", got[1].HostHeader)
}

// 档案表整体缺失时 auto 允许降级到旧表（这个部署本来就没有档案），
// 但 profiles 是显式指定，必须报错——否则会产出一份「零 location」的 snippet，
// 把所有租户撕回平台盘。
func TestSourceProfilesRequiresProfileTables(t *testing.T) {
	dsn := seedDB(t, []model.TenantStorageConfig{
		{CompanyID: 7, Driver: "minio", Endpoint: "minio.c7.example.com", Bucket: "c7", Enabled: true},
	})

	got, err := loadTargetsFromDB(dsn, sourceAuto)
	require.NoError(t, err, "auto 要能降级")
	require.Len(t, got, 1)

	_, err = loadTargetsFromDB(dsn, sourceProfiles)
	require.Error(t, err)
	require.True(t, errors.Is(err, repository.ErrProfileTablesMissing))
	require.Contains(t, err.Error(), "0054")
}

// 档案优先、旧表补位：这是 auto 的全部语义，也是接入层跟着对象走的关键。
func TestLoadTargetsFromDBAutoPrefersProfiles(t *testing.T) {
	dsn := seedProfileDB(t, seedProfileData{
		companies: []model.Company{{ID: 3, Name: "三家"}, {ID: 7, Name: "七家"}},
		legacy: []model.TenantStorageConfig{
			// 7 号旧表指向旧桶，但档案已把它绑到新桶 → 必须用新桶
			{CompanyID: 7, Driver: "minio", Endpoint: "old.c7.example.com", Bucket: "old7", Enabled: true},
			// 3 号只在旧表里 → 补位
			{CompanyID: 3, Driver: "minio", Endpoint: "legacy.c3.example.com", Bucket: "c3", Enabled: true},
		},
		profiles: []model.StorageProfile{
			{Name: "新桶", Driver: "minio", Endpoint: "https://new.c7.example.com", Bucket: "new7", UseSSL: true, PublicRead: true, Version: 1},
		},
		bindings: []model.StorageBinding{{TenantID: 7, StorageProfileID: 1, Version: 1}},
	})

	got, err := loadTargetsFromDB(dsn, sourceAuto)
	require.NoError(t, err)
	require.Len(t, got, 2, "两家都要有 location")

	require.Equal(t, int64(3), got[0].CompanyID)
	require.Equal(t, "legacy.c3.example.com", got[0].Endpoint, "没有档案绑定的企业由旧表补位")

	require.Equal(t, int64(7), got[1].CompanyID)
	require.Equal(t, "new.c7.example.com", got[1].Endpoint, "档案优先；endpoint 必须去掉 scheme")
	require.Equal(t, "new7", got[1].Bucket)
}

// **最重要的一条**：档案把企业绑到本地时，旧表那行指向客户桶的启用行必须失效。
// 不覆盖的话 nginx 会去客户桶取一个根本不存在的对象——图全裂、后台看着全对。
func TestMergeTargetsLocalProfileSuppressesLegacyRow(t *testing.T) {
	legacy := []repository.StorageTarget{
		{CompanyID: 7, Driver: "minio", Endpoint: "old.c7.example.com", Bucket: "old7", Enabled: true},
	}
	prof := []repository.ProfileTarget{
		{CompanyID: 7, Target: nginxsnippet.Target{CompanyID: 7, Driver: "local", Enabled: true}},
	}

	got := mergeTargets(legacy, prof, sourceAuto)
	require.Len(t, got, 1)
	require.Equal(t, "local", got[0].Driver, "档案绑定 local 必须覆盖旧表，而不是跳过")
	require.False(t, got[0].NeedsSnippet(), "local 不该生成 location")
}

// legacy 只读旧表：回滚通道必须是干净的，不能混进档案。
func TestMergeTargetsLegacySourceIgnoresProfiles(t *testing.T) {
	legacy := []repository.StorageTarget{
		{CompanyID: 7, Driver: "minio", Endpoint: "old.c7.example.com", Bucket: "old7", Enabled: true},
	}
	prof := []repository.ProfileTarget{
		{CompanyID: 9, Target: nginxsnippet.Target{CompanyID: 9, Driver: "minio", Endpoint: "new.c9.example.com", Bucket: "new9", Enabled: true}},
	}

	got := mergeTargets(legacy, prof, sourceLegacy)
	require.Len(t, got, 1)
	require.Equal(t, int64(7), got[0].CompanyID)
}

// 端到端：render --dsn 落盘 → check --dsn 对账通过。
// 这条同时验证「两个子命令用的是同一个数据源」，否则对账永远绿。
func TestRenderAndCheckRoundTripWithDSN(t *testing.T) {
	dsn := seedDB(t, []model.TenantStorageConfig{
		{CompanyID: 7, Driver: "minio", Endpoint: "minio.c7.example.com", Bucket: "c7",
			UseSSL: true, HostHeader: "c7.minio.example.com", Enabled: true, Status: "ok"},
	})
	out := filepath.Join(t.TempDir(), "storage.conf")

	require.NoError(t, runRender([]string{"--dsn", dsn, "--out", out}))
	body, err := os.ReadFile(out)
	require.NoError(t, err)
	require.Contains(t, string(body), "location ^~ /uploads/t/7/ {")
	require.Contains(t, string(body), `proxy_set_header Host "c7.minio.example.com";`)

	require.NoError(t, runCheck([]string{"--dsn", dsn, "--snippet", out}))
}

// 端到端（档案源）：全局绑定展开 → render 出的 location 指向档案里的桶 →
// check 同源对账通过。这条同时钉住「endpoint 的 scheme 必须剥掉」——
// 档案界面允许填 https://，拼进 proxy_pass 会得到 https://https://。
func TestRenderAndCheckRoundTripWithProfiles(t *testing.T) {
	dsn := seedProfileDB(t, seedProfileData{
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
	// 对账必须同源：拿 legacy 去对档案生成的 snippet 应当报不一致，
	// 否则「check 绿了」根本不能证明 render 用对了源。
	require.Error(t, runCheck([]string{"--dsn", dsn, "--source", sourceLegacy, "--snippet", out}))
}

// 停用的租户必须从 snippet 里消失，否则对账会报 Extra——这条用
// render + check 串起来验证，比单测 NeedsSnippet 更接近现场。
func TestRenderDropsDisabledTenant(t *testing.T) {
	dsn := seedDB(t, []model.TenantStorageConfig{
		{CompanyID: 7, Driver: "minio", Endpoint: "minio.c7.example.com", Bucket: "c7", Enabled: false, Status: "ok"},
		{CompanyID: 8, Driver: "oss", Endpoint: "oss.example.com", Bucket: "c8", Enabled: true, Status: "ok"},
	})
	out := filepath.Join(t.TempDir(), "storage.conf")
	require.NoError(t, runRender([]string{"--dsn", dsn, "--out", out}))

	body, err := os.ReadFile(out)
	require.NoError(t, err)
	require.NotContains(t, string(body), "/uploads/t/7/", "停用的租户不该留 location")
	require.Contains(t, string(body), "/uploads/t/8/")
	require.NoError(t, runCheck([]string{"--dsn", dsn, "--snippet", out}))
}

// 渲染结果零 location + 目标文件非空 = 一次静默的线上清空，默认必须拒绝。
//
// 三种现场都会走到这里：配置表被误清、DSN 指到空库、容器里挂错卷。
// 它们的报错都不明显，所以拦在落盘前。
func TestRenderRefusesToWipeExistingSnippet(t *testing.T) {
	dsn := seedDB(t, nil) // 表存在但 0 行
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
	dsn := seedDB(t, nil)
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

// seedDB 建一个已应用 0044 的 SQLite 库并写入给定行，返回 DSN。
func seedDB(t *testing.T, rows []model.TenantStorageConfig) string {
	t.Helper()
	dsn := filepath.Join(t.TempDir(), "wc.db")
	db, _, err := dbopen.Open(dsn)
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&model.TenantStorageConfig{}))
	for _, r := range rows {
		row := r
		if strings.TrimSpace(row.Status) == "" {
			row.Status = "unknown"
		}
		require.NoError(t, db.Create(&row).Error)
	}
	sqlDB, err := db.DB()
	require.NoError(t, err)
	require.NoError(t, sqlDB.Close())
	return dsn
}

// seedProfileData 是 seedProfileDB 的输入：四张表一起建，才能测「档案优先」。
type seedProfileData struct {
	companies []model.Company
	legacy    []model.TenantStorageConfig
	profiles  []model.StorageProfile
	bindings  []model.StorageBinding
}

// seedProfileDB 建一个 0044 + 0054 都应用过的 SQLite 库，返回 DSN。
// profiles 按切片顺序插入，因此第一条的 id 恒为 1（测试里直接引用）。
func seedProfileDB(t *testing.T, d seedProfileData) string {
	t.Helper()
	dsn := filepath.Join(t.TempDir(), "wc-profiles.db")
	db, _, err := dbopen.Open(dsn)
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(
		&model.Company{}, &model.TenantStorageConfig{},
		&model.StorageProfile{}, &model.DeliveryProfile{}, &model.StorageBinding{},
	))
	for _, c := range d.companies {
		row := c
		if row.Status == "" {
			row.Status = "enabled"
		}
		require.NoError(t, db.Create(&row).Error)
	}
	for _, r := range d.legacy {
		row := r
		if strings.TrimSpace(row.Status) == "" {
			row.Status = "unknown"
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
