package config

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// 镜像目录与主盘重叠会把私有对象暴露到免鉴权的 /uploads 静态路由，
// 或让备份脚本把主盘整份复制一遍。这里是启动期硬门槛，必须逐个方向验证。
func TestValidateMirrorDirs(t *testing.T) {
	base := t.TempDir()
	upload := filepath.Join(base, "uploads")
	mirror := filepath.Join(base, "mirror")

	require.NoError(t, ValidateMirrorDirs(upload, mirror))
	require.NoError(t, ValidateMirrorDirs("./uploads", "./storage/mirror"))

	bad := [][2]string{
		{upload, upload},
		{upload, filepath.Join(upload, "backup")},
		{filepath.Join(mirror, "uploads"), mirror},
		{"./uploads", "./uploads/../uploads/inside"},
	}
	for _, p := range bad {
		err := ValidateMirrorDirs(p[0], p[1])
		require.Error(t, err, "upload=%s mirror=%s 必须被拒绝", p[0], p[1])
		require.Contains(t, err.Error(), "完全分离")
	}
}
