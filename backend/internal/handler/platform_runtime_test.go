package handler

// 平台运行期配置注入守门（A1）：基址 / 公开读改完不重启就生效，
// 且未注入时回落启动期 cfg（保住单测与未迁移旧库的行为）。

import (
	"testing"

	"github.com/stretchr/testify/require"

	"weichuan/internal/config"
)

func TestEffectiveBaseURLFallsBackToCfgWhenNotInjected(t *testing.T) {
	SetPlatformRuntime(nil, nil)
	t.Cleanup(func() { SetPlatformRuntime(nil, nil) })
	cfg := config.Config{UploadBaseURL: "https://env.example.com/uploads"}
	require.Equal(t, "https://env.example.com/uploads", effectiveBaseURL(cfg))
	require.True(t, effectivePublic(config.Config{StoragePublic: true}))
}

func TestEffectiveBaseURLUsesInjectedProvider(t *testing.T) {
	current := "https://first.example.com/uploads"
	SetPlatformRuntime(func() (string, bool) { return current, true }, func() (bool, bool) { return false, true })
	t.Cleanup(func() { SetPlatformRuntime(nil, nil) })
	cfg := config.Config{UploadBaseURL: "https://env.example.com/uploads"}

	require.Equal(t, "https://first.example.com/uploads", effectiveBaseURL(cfg), "注入后应优先读档案真值源")
	require.False(t, effectivePublic(cfg), "公开读开关同样走注入")

	// 档案改成新基址（模拟保存后）：下一次读立即是新值，不重启。
	current = "https://second.example.com/uploads"
	require.Equal(t, "https://second.example.com/uploads", effectiveBaseURL(cfg))
}

func TestEffectiveBaseURLProviderEmptyFallsBack(t *testing.T) {
	// ok=false 表示「还没有生效实例」（未迁移旧库 / 首次构造失败），
	// 必须回落 cfg；否则旧私有桶会被当成公开读、基址被清空导致裂图。
	SetPlatformRuntime(func() (string, bool) { return "", false }, func() (bool, bool) { return true, false })
	t.Cleanup(func() { SetPlatformRuntime(nil, nil) })
	cfg := config.Config{UploadBaseURL: "https://env.example.com/uploads", StoragePublic: false}
	require.Equal(t, "https://env.example.com/uploads", effectiveBaseURL(cfg))
	require.False(t, effectivePublic(cfg), "ok=false 时必须回落 cfg 的私有桶设置，不能当成公开读")
}
