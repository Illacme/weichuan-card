package handler

import (
	"archive/tar"
	"compress/gzip"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"weichuan/internal/audit"
	"weichuan/internal/config"
	"weichuan/internal/dbbackup"
	"weichuan/internal/dbopen"
	"weichuan/internal/middleware"
	"weichuan/internal/safego"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// 平台超管的「备份与恢复」面板：看状态 / 手动触发一次 / 下载产物。
//
// 为什么要有这个面板：自动备份跑在服务器 cron 上，管理员在后台完全看不见它——
// 「有没有在备、最近一次是什么时候、缺哪一件」全靠问运维。看不见的东西等于没有，
// 而备份恰恰是「失败于无声」的典型（漏跑一天不会有任何报错）。
//
// 三条刻意的边界：
//
//  1. **不做「上传备份并恢复」**。恢复是破坏性操作，误点一次就把生产库盖回上周。
//     恢复必须走 scripts/restore.sh：它有交互确认、有恢复前的安全网、有回滚指引。
//     后台只负责「看得见 + 拿得到」，不负责「一键盖掉」。
//  2. **既不生成、也不下载密钥包**（runtime.env 里的 PII_ENC_KEY 等）。密钥的价值
//     就在于「不与数据放在一起」；后台若能一键取走，加密就白做了——哪怕取到的是
//     密文，它和口令最终会落到同一台办公电脑上。面板只如实显示有没有，缺了就说
//     缺了，并把「该谁去做」写清楚。
//  3. **不在请求里同步跑备份**。GB 级图片打包会拖到反代超时，用户只看到 504、
//     以为没成功，实际后台还在写文件。这里异步执行 + 状态轮询，重复点击被互斥锁挡住。
const backupFreshHours = 26 // 日备 + 2h 余量，与 scripts/alert_check.sh 的默认阈值一致

// 备份产物命名白名单。下载接口只认这几种形状——它同时承担「防止路径穿越」的
// 第一道检查（第二道是 filepath.Join 之后的前缀校验）。
var (
	reBackupDB      = regexp.MustCompile(`^[A-Za-z0-9._-]+-[0-9]{8}-[0-9]{6}\.(db|sql)\.gz$`)
	reBackupUploads = regexp.MustCompile(`^uploads-[0-9]{8}-[0-9]{6}\.tar\.gz$`)
	reBackupSecrets = regexp.MustCompile(`^secrets-[0-9]{8}-[0-9]{6}\.tar\.gz\.enc$`)
)

// backupDispatch 默认丢到 goroutine 里跑；测试替换成同步执行，避免 sleep 等结果。
// 与 subscribe.go 的 dispatch 同一套路：可测性不该靠「等一会儿再看」。
var backupDispatch = func(fn func()) { safego.Run("backup.run", fn) }

type backupRunResult struct {
	At      time.Time `json:"at"`
	OK      bool      `json:"ok"`
	Message string    `json:"message"`
}

// backupState 进程内的运行态。单副本部署下足够；多副本各自独立（与限流同源问题，
// 见 docs/commercial-readiness.md 的 C4：上 Redis 前不要开多副本）。
type backupState struct {
	mu      sync.Mutex
	running bool
	last    *backupRunResult
}

var bkState = &backupState{}

func (s *backupState) tryStart() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.running {
		return false
	}
	s.running = true
	return true
}

func (s *backupState) finish(r backupRunResult) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.running = false
	s.last = &r
}

func (s *backupState) snapshotState() (bool, *backupRunResult) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.running, s.last
}

// backupKindOf 按文件名判断产物种类。认不出返回空串（下载接口据此拒绝）。
func backupKindOf(name string) string {
	switch {
	case reBackupSecrets.MatchString(name):
		return "secrets"
	case reBackupUploads.MatchString(name):
		return "uploads"
	case reBackupDB.MatchString(name):
		return "db"
	}
	return ""
}

var backupKindLabels = map[string]string{
	"db":      "数据库",
	"uploads": "上传文件（图片）",
	"secrets": "运行时密钥",
}

// RegisterBackup 平台超管的备份面板（只读状态 + 手动触发 + 下载）。
func RegisterBackup(r *gin.Engine, db *gorm.DB, cfg config.Config) {
	plat := middleware.PlatformAdminMiddleware(db)
	dir := strings.TrimSpace(cfg.BackupDir)
	if dir == "" {
		dir = "./backups"
	}

	r.GET("/api/admin/backup/status", plat, func(c *gin.Context) {
		running, last := bkState.snapshotState()
		items, err := scanBackups(dir)
		if err != nil {
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "读取备份目录失败：" + err.Error()})
			return
		}

		sqlite := dbopen.IsSQLiteDSN(cfg.DBDSN)
		notes := make([]string, 0, 2)
		if !sqlite {
			notes = append(notes, "当前为 MySQL 形态：后台只能查看状态与下载，执行备份请在服务器运行 scripts/backup.sh（mysqldump 一致性快照）。")
		}
		if !hasKind(items, "secrets") {
			notes = append(notes, "缺少「运行时密钥」备份：它刻意不在后台生成——密钥一旦能从后台导出，就等于和数据库放在一起，加密失去意义。请在服务器运行 scripts/backup_files.sh 并单独保管 BACKUP_ENC_KEY；否则主机损毁后库里的姓名/手机无法解密。")
		} else {
			// 有备份却下不了，必须解释一句，否则运维会以为是按钮坏了。
			notes = append(notes, "「运行时密钥」已有备份，但后台不提供下载：请从服务器用 scp/rsync 取走，并与 BACKUP_ENC_KEY 分开保管（同一个人同时拿到两者，加密就形同虚设）。")
		}

		c.JSON(http.StatusOK, gin.H{"code": 0, "data": gin.H{
			"dir":          dir,
			"driver":       driverName(sqlite),
			"canRun":       sqlite,
			"running":      running,
			"freshHours":   backupFreshHours,
			"items":        items,
			"lastRun":      last,
			"notes":        notes,
			"automaticTip": "自动备份需在服务器配置 crontab（见 docs/deploy-runbook.md §9.2）；后台按钮只补「临时要一份」，不能替代每日备份。",
		}})
	})

	r.POST("/api/admin/backup/run", plat, func(c *gin.Context) {
		if !dbopen.IsSQLiteDSN(cfg.DBDSN) {
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "当前为 MySQL 形态：请在服务器执行 scripts/backup.sh（mysqldump 一致性快照），后台不代跑"})
			return
		}
		if !bkState.tryStart() {
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "已有备份任务在进行，请稍候再试"})
			return
		}

		dsn, upDir := cfg.DBDSN, cfg.UploadDir
		backupDispatch(func() {
			// finish 必须一定执行：runBackupOnce 里任何 panic 若让协程直接死掉，
			// bkState.running 会永久卡在 true，此后「立即备份」永远回「已有任务在进行」，
			// 只能重启进程才能恢复。用闭包 defer 兜底收尾（写成 defer bkState.finish(res)
			// 会把参数提前求值，成功路径也会记成失败）。safego 负责兜住 panic 并打日志。
			res := backupRunResult{At: time.Now(), OK: false, Message: "备份任务异常终止"}
			defer func() { bkState.finish(res) }()
			res = runBackupOnce(dir, dsn, upDir)
		})

		// 审计放在这里（同步、handler 返回前）：备份是数据外带的前置动作，必须留痕。
		// 不记 Detail 里的文件内容，只记目录——审计日志本身不该成为泄漏面。
		audit.Record(db, c, audit.Entry{
			Action:     audit.ActionBackupRun,
			TargetType: "backup",
			Detail:     map[string]any{"dir": dir},
		})
		c.JSON(http.StatusOK, gin.H{"code": 0, "data": gin.H{"started": true}})
	})

	r.GET("/api/admin/backup/download", plat, func(c *gin.Context) {
		name := strings.TrimSpace(c.Query("name"))
		// 白名单先行：既挡路径穿越（../../etc/passwd 不匹配），也挡「下载备份目录里
		// 任何别的文件」（目录里可能被运维放了别的东西）。
		kind := backupKindOf(name)
		if kind == "" {
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "文件名不合法"})
			return
		}
		// 密钥包认得出来（状态面板要显示它有没有），但不给下载。
		// 界面上本来就没有这个按钮；这里再挡一次，是为了让「密钥不与数据同路」
		// 成为代码里的约束，而不是只靠前端没画按钮。
		if kind == "secrets" {
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "运行时密钥包不在后台下载：请从服务器用 scp/rsync 取走，并与 BACKUP_ENC_KEY 分开保管"})
			return
		}
		full := filepath.Join(dir, name)
		// 第二道：Join 之后确认仍在备份目录内（防 filepath.Join 被 .. 归一化绕过）
		absDir, err1 := filepath.Abs(dir)
		absFull, err2 := filepath.Abs(full)
		if err1 != nil || err2 != nil || !strings.HasPrefix(absFull, absDir+string(os.PathSeparator)) {
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "文件名不合法"})
			return
		}
		fi, err := os.Stat(absFull)
		if err != nil || fi.IsDir() {
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "备份文件不存在"})
			return
		}

		audit.Record(db, c, audit.Entry{
			Action:     audit.ActionBackupDownload,
			TargetType: "backup",
			TargetID:   name,
			Detail:     map[string]any{"bytes": fi.Size()},
		})
		// FileAttachment 会流式发送，不把整个文件读进内存——备份动辄上百 MB。
		c.FileAttachment(absFull, name)
	})
}

func driverName(sqlite bool) string {
	if sqlite {
		return "sqlite"
	}
	return "mysql"
}

// backupItem 是面板上的一行：某类产物里最新的那一份。
type backupItem struct {
	Kind     string `json:"kind"`
	Label    string `json:"label"`
	File     string `json:"file"`
	Bytes    int64  `json:"bytes"`
	MTime    string `json:"mtime"`
	AgeHours int    `json:"ageHours"`
	Fresh    bool   `json:"fresh"`
}

// scanBackups 扫备份目录，按种类取最新一份。
//
// 目录不存在不是错误：新部署还没跑过备份，面板该显示「还没有备份」而不是报错。
func scanBackups(dir string) ([]backupItem, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return []backupItem{}, nil
		}
		return nil, err
	}

	newest := map[string]backupItem{}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		kind := backupKindOf(e.Name())
		if kind == "" {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		prev, ok := newest[kind]
		if ok && !info.ModTime().After(parseBackupTime(prev.MTime)) {
			continue
		}
		age := int(time.Since(info.ModTime()).Hours())
		newest[kind] = backupItem{
			Kind:     kind,
			Label:    backupKindLabels[kind],
			File:     e.Name(),
			Bytes:    info.Size(),
			MTime:    info.ModTime().Format(time.RFC3339),
			AgeHours: age,
			Fresh:    age < backupFreshHours,
		}
	}

	out := make([]backupItem, 0, len(newest))
	for _, v := range newest {
		out = append(out, v)
	}
	// 固定顺序：数据库 → 图片 → 密钥。前端按这个顺序渲染，不靠 map 的随机序。
	order := map[string]int{"db": 0, "uploads": 1, "secrets": 2}
	sort.Slice(out, func(i, j int) bool { return order[out[i].Kind] < order[out[j].Kind] })
	return out, nil
}

func parseBackupTime(s string) time.Time {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Time{}
	}
	return t
}

func hasKind(items []backupItem, kind string) bool {
	for _, it := range items {
		if it.Kind == kind {
			return true
		}
	}
	return false
}

// runBackupOnce 产出一份「库 + 图片」备份。
//
// 只做这两件，不做密钥包（见 RegisterBackup 的注释）。
// 任一步失败都要把已产出的半成品删掉：一份「看起来新鲜、其实缺件」的备份会
// 让备份新鲜度告警闭嘴，比没有备份更危险——这条与 scripts/backup_files.sh 一致。
func runBackupOnce(dir, dsn, uploadDir string) backupRunResult {
	now := time.Now()
	res := backupRunResult{At: now}
	stamp := now.Format("20060102-150405")

	if err := os.MkdirAll(dir, 0o755); err != nil {
		res.Message = "创建备份目录失败：" + err.Error()
		return res
	}

	// ── 1. 数据库：VACUUM INTO 一致性快照 → gzip ──
	snap := filepath.Join(dir, ".snapshot-"+stamp+".db")
	// 中间产物，无论成败都不留。删不掉也不影响结果（失败路径本就以报错收场），
	// 所以显式忽略错误而不是把它当成功条件。
	defer func() { _ = os.Remove(snap) }()

	rep, err := dbbackup.Snapshot(dsn, snap)
	if err != nil {
		res.Message = "数据库快照失败：" + err.Error()
		return res
	}
	dbOut := filepath.Join(dir, "weichuan-"+stamp+".db.gz")
	if err := gzipFile(snap, dbOut); err != nil {
		_ = os.Remove(dbOut) // 半截的 gz 比没有更危险：解压时才炸
		res.Message = "压缩数据库快照失败：" + err.Error()
		return res
	}

	// ── 2. 图片 ──
	upOut := filepath.Join(dir, "uploads-"+stamp+".tar.gz")
	files, err := tarGzDir(uploadDir, upOut)
	if err != nil {
		// 一件失败就整次不留：留着「新鲜但缺件」的包会喂饱新鲜度告警
		_ = os.Remove(dbOut)
		_ = os.Remove(upOut)
		res.Message = "打包上传目录失败：" + err.Error()
		return res
	}

	res.OK = true
	res.Message = fmt.Sprintf("已生成 2 个文件：数据库 %d 张表（%s），图片 %d 个（%s）",
		rep.Tables, filepath.Base(dbOut), files, filepath.Base(upOut))
	return res
}

func gzipFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	// 只读句柄，关闭错误无处可报也无后果（与 internal/storage 同一写法）
	defer func() { _ = in.Close() }()

	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	zw := gzip.NewWriter(out)
	if _, err := io.Copy(zw, in); err != nil {
		// 已经在错误路径上：关闭时的二次错误不该盖掉真正的失败原因
		_ = zw.Close()
		_ = out.Close()
		return err
	}
	if err := zw.Close(); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}

// tarGzDir 把目录打成 tar.gz，返回写入的文件数。
//
// 目录不存在时返回 0 且不报错：用对象存储的部署本来就没有本地图片目录，
// 那不是故障，不该让整次备份失败。
func tarGzDir(srcDir, dst string) (int, error) {
	if strings.TrimSpace(srcDir) == "" {
		return 0, nil
	}
	if fi, err := os.Stat(srcDir); err != nil || !fi.IsDir() {
		return 0, nil
	}

	out, err := os.Create(dst)
	if err != nil {
		return 0, err
	}
	zw := gzip.NewWriter(out)
	tw := tar.NewWriter(zw)

	count := 0
	walkErr := filepath.Walk(srcDir, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(srcDir, p)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		// 归档内一律用正斜杠：备份包可能被拿到别的系统上解，反斜杠会变成文件名的一部分
		name := path.Clean(filepath.ToSlash(rel))
		switch {
		case info.IsDir():
			return tw.WriteHeader(&tar.Header{
				Typeflag: tar.TypeDir, Name: name + "/",
				Mode: 0o755, ModTime: info.ModTime(),
			})
		case info.Mode().IsRegular():
			hdr := &tar.Header{
				Typeflag: tar.TypeReg, Name: name,
				Mode: 0o644, Size: info.Size(), ModTime: info.ModTime(),
			}
			if err := tw.WriteHeader(hdr); err != nil {
				return err
			}
			f, err := os.Open(p)
			if err != nil {
				return err
			}
			_, cerr := io.Copy(tw, f)
			_ = f.Close() // 只读句柄：拷贝的结果才是要报的错误
			if cerr != nil {
				return cerr
			}
			count++
			return nil
		default:
			// 符号链接跳过：跟随它会跳出上传目录（备份不该把 /etc 打进去），
			// 保留链接本身又会在解包时指向不存在的目标。
			return nil
		}
	})

	if cerr := tw.Close(); walkErr == nil {
		walkErr = cerr
	}
	if cerr := zw.Close(); walkErr == nil {
		walkErr = cerr
	}
	if cerr := out.Close(); walkErr == nil {
		walkErr = cerr
	}
	return count, walkErr
}
