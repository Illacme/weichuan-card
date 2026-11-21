package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ValidateMirrorDirs 保证本地镜像目录与主上传目录**互不包含**。
//
// 为什么要拦在启动期而不是写文件时：静态路由 `r.Static("/uploads", cfg.UploadDir)`
// 是不鉴权的。一旦镜像目录落进 uploads 里面，私有桶里的对象被回拉成镜像后，
// 任意访客只要猜到 key 就能通过 /uploads/<key> 直接拿到——而管理员在界面上
// 看到的仍然是「私有桶」「镜像备份」，两边都不报错。反过来（uploads 落进
// mirror 里）会让备份打包把主盘整份复制一遍，磁盘直接翻倍。
//
// 判据用绝对路径 + Clean，不做 EvalSymlinks：部署时目录往往还不存在，
// 而符号链接场景由「两个值都由运维显式配置」这一前提兜住；真有软链交叉
// 时，备份脚本看到的也是两条独立路径，不会静默公开私有对象。
func ValidateMirrorDirs(uploadDir, mirrorDir string) error {
	u, err := filepath.Abs(strings.TrimSpace(uploadDir))
	if err != nil || u == "" {
		return fmt.Errorf("主上传目录不合法：%q", uploadDir)
	}
	m, err := filepath.Abs(strings.TrimSpace(mirrorDir))
	if err != nil || m == "" {
		return fmt.Errorf("本地镜像目录不合法：%q", mirrorDir)
	}
	u, m = filepath.Clean(u), filepath.Clean(m)
	if u == m || isSubPath(u, m) || isSubPath(m, u) {
		return fmt.Errorf("本地镜像目录必须与主上传目录完全分离（uploads=%s, mirror=%s）：否则私有对象会被 /uploads 静态路由公开访问", u, m)
	}
	return nil
}

// isSubPath 判断 child 是否落在 parent 目录内（不含相等）。
func isSubPath(parent, child string) bool {
	rel, err := filepath.Rel(parent, child)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator))
}
