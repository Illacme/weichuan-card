// thumbbackfill：存量上传图缩略图回填（一次性 / 可重复执行）。
//
// 为什么需要它：缩略图（<name>_thumb.<ext>）只在「新上传」时生成。存量部署里的图
// （导入的、早期上传的、SaveBytes 写出的素材）都没有缩略图，而各端列表页会照常去
// 请求 _thumb → 404 → 回退原图。等于每张图多打一次失败请求，反而更慢。
// 本工具把缺的补齐，之后缩略图才真正省流量。
//
// 用法（在服务器上，与后端同一套环境变量 / 配置文件）：
//
//	go run ./cmd/thumbbackfill                       # 按 UPLOAD_DIR 回填
//	go run ./cmd/thumbbackfill -dry-run              # 只统计不写盘，先看量
//	go run ./cmd/thumbbackfill -force                # 全部重生成（改了缩略图规格时用）
//	go run ./cmd/thumbbackfill -dir /www/.../uploads # 显式指定目录
//
// 可以随时多次执行：已存在且是最新的缩略图会被跳过（幂等），原图只读不动。
// 详见 storage.BackfillThumbs 注释。
package main

import (
	"flag"
	"log"
	"os"

	"weichuan/internal/config"
	"weichuan/internal/storage"
)

func main() {
	dirFlag := flag.String("dir", "", "上传目录；留空取配置的 UPLOAD_DIR")
	dryRun := flag.Bool("dry-run", false, "只统计不落盘")
	force := flag.Bool("force", false, "忽略已存在的缩略图，全部重新生成")
	flag.Parse()

	cfg := config.Load()

	dir := *dirFlag
	if dir == "" {
		dir = cfg.UploadDir
	}
	// 对象存储的对象不在本地磁盘，扫 UPLOAD_DIR 只会扫到空目录/无关文件，
	// 静默跑出「扫描 0 张」比直接报错更难发现——所以显式挡一下。
	if cfg.StorageDriver != "local" && *dirFlag == "" {
		log.Fatalf("当前 STORAGE_DRIVER=%s：本工具只处理本地磁盘上传目录；确要处理某个本地目录请显式 -dir 指定", cfg.StorageDriver)
	}
	info, err := os.Stat(dir)
	if err != nil {
		log.Fatalf("上传目录不可用 %s: %v", dir, err)
	}
	if !info.IsDir() {
		log.Fatalf("上传路径不是目录: %s", dir)
	}
	if *dryRun {
		log.Printf("[DRY-RUN] 只统计，不写盘")
	}
	if *force {
		log.Printf("[FORCE] 已存在的缩略图也会重新生成")
	}

	res, err := storage.BackfillThumbs(dir, storage.BackfillOptions{
		DryRun: *dryRun,
		Force:  *force,
		Warn:   func(p string, e error) { log.Printf("[WARN] 跳过 %s: %v", p, e) },
	})
	if err != nil {
		log.Fatalf("扫描失败 %s: %v", dir, err)
	}
	log.Printf("回填完成 dir=%s %s", dir, res.String())
}
