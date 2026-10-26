package handler

import (
	"net/http"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"weichuan/internal/audit"
	"weichuan/internal/config"
	"weichuan/internal/middleware"
	"weichuan/internal/mpupload"
	"weichuan/internal/pathpick"
	"weichuan/internal/setting"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// RegisterSettings 注册系统设置接口：
//   - GET  /api/public/settings  公开：仅返回非敏感项（小程序 / 前端引导用）
//   - GET  /api/admin/settings   平台超管读取全部可配置项（按分组）
//   - PUT  /api/admin/settings   平台超管批量更新配置（按类型校验）
//
// 三档生效方式（详见 internal/setting 包注释）：
//  1. 常规项保存即时生效（内存缓存即时回写）；
//  2. Restart 项（云存储 / CDN / 上传基址）保存后需重启进程；
//  3. ReadOnly 项（DB 连接串 / JWT 密钥 / Cookie）只掩码展示当前值，PUT 会被拒绝。
//
// cfg 用于注入「环境变量类配置」的当前实际值——这些项即使环境变量没设也有进程内缺省，
// 只有拿 cfg 才能如实告诉管理员「现在到底配了什么」，而不是显示一片空白。
func RegisterSettings(r *gin.Engine, db *gorm.DB, cfg config.Config) {
	setting.SetEnvSnapshot(map[string]string{
		"db_dsn":        cfg.DBDSN,
		"jwt_key":       cfg.JWTKey,
		"cookie_secure": strconv.FormatBool(cfg.CookieSecure),
		"cookie_domain": cfg.CookieDomain,
		// 后台上传代码那组：界面留空时实际生效的是这几个值，快照进来，
		// 「已配置」标记才与真实行为一致（走安装向导装的部署没有这些环境变量，
		// 值在 data/runtime.env 里，os.Getenv 取不到）
		"mp_node_bin":      cfg.MPNodeBin,
		"mp_ci_module_dir": cfg.MPCIModuleDir,
		"mp_source_dir":    cfg.MPSourceDir,
	})

	r.GET("/api/public/settings", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"code": 0, "data": setting.Public()})
	})

	plat := middleware.PlatformAdminMiddleware(db)
	r.GET("/api/admin/settings", plat, func(c *gin.Context) {
		items := setting.All()
		sort.SliceStable(items, func(i, j int) bool {
			if items[i].Group != items[j].Group {
				return items[i].Group < items[j].Group
			}
			return items[i].Key < items[j].Key
		})
		c.JSON(http.StatusOK, gin.H{"code": 0, "data": items})
	})

	// 服务端路径选择器：让管理员在「后台上传小程序代码」那几项配置上直接浏览
	// 服务器文件系统挑路径，而不是手敲绝对路径（也绕开「浏览器文件框选的是客户端
	// 机器」这个根本误区——这些路径指向的是后端要 exec 的服务器）。
	r.GET("/api/admin/settings/paths", plat, func(c *gin.Context) {
		listSettingPaths(c, cfg)
	})

	r.PUT("/api/admin/settings", plat, func(c *gin.Context) {
		var req map[string]string
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "参数错误"})
			return
		}
		if len(req) == 0 {
			c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "无更新项"})
			return
		}
		// 逐条校验 + 写入；任何一项失败立即返回提示（Set 内部已即时刷新内存）
		restartKeys := make([]string, 0)
		for k, v := range req {
			if err := setting.Upsert(k, v); err != nil {
				// 失败也记一笔：非法提交（含试图改只读项 / 提交掩码）本身就是需要追溯的行为
				audit.Record(db, c, audit.Entry{
					Action: audit.ActionSettingUpdate, TargetType: "setting", TargetID: k,
					Result: audit.ResultFail, Detail: map[string]any{"keys": keysOf(req), "error": err.Error()},
				})
				c.JSON(http.StatusOK, gin.H{"code": 1, "msg": err.Error()})
				return
			}
			if setting.IsRestart(k) {
				restartKeys = append(restartKeys, k)
			}
		}
		sort.Strings(restartKeys)
		// 审计：记录改了哪些项，敏感项（密钥）只记「已修改」不落明文
		audit.Record(db, c, audit.Entry{
			Action: audit.ActionSettingUpdate, TargetType: "setting",
			Detail: audit.DetailKeys(req, setting.IsSecret),
		})
		msg := "保存成功"
		if len(restartKeys) > 0 {
			// 明确告知哪些项不会立刻生效，避免「改了没反应」的误解
			msg = "保存成功；其中 " + strconv.Itoa(len(restartKeys)) + " 项需重启服务后生效：" +
				strings.Join(restartKeys, "、")
		}
		c.JSON(http.StatusOK, gin.H{"code": 0, "msg": msg, "data": gin.H{"restartKeys": restartKeys}})
	})
}

// keysOf 取提交里的设置项 key 列表（审计用：失败时不记录值，只记动过哪些项）。
func keysOf(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// listSettingPaths 处理 GET /api/admin/settings/paths?kind=&dir=：浏览服务器目录，
// 供「后台上传小程序代码」那几项路径配置在界面上选路径。
//
// kind 必须是三种已知类型之一，否则直接拒绝（避免把本接口当通用文件浏览器用）。
// dir 缺省时按类型给一个合理起点；有效配置值（界面优先、空串回落环境变量，经
// mpUploadCfg 叠加）会被补进白名单，保证已配的路径一定能继续往上/往下浏览。
// 列举与校验的安全口径全部交给 pathpick 包：白名单、符号链接解析、敏感点目录屏蔽。
func listSettingPaths(c *gin.Context, cfg config.Config) {
	kindStr := strings.TrimSpace(c.Query("kind"))
	var kind mpupload.Kind
	switch kindStr {
	case string(mpupload.KindNodeBin):
		kind = mpupload.KindNodeBin
	case string(mpupload.KindModuleDir):
		kind = mpupload.KindModuleDir
	case string(mpupload.KindSourceDir):
		kind = mpupload.KindSourceDir
	default:
		c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "未知的路径类型：" + kindStr})
		return
	}

	// 有效配置值（界面优先、空串回落环境变量）：取覆盖后的那份，而非裸 env。
	up := mpUploadCfg(cfg)

	// 把当前已配置的**绝对**路径及其父目录补进白名单：管理员即便把路径配在默认白名单外，
	// 也能围着它继续浏览，否则会出现「配好了却进不去那个目录」的死局。
	// 只补绝对路径——命令名 node / 相对路径会随进程 cwd 漂移，补进去等于把 cwd 开放出去。
	var extra []string
	addAbs := func(p string) {
		if p = strings.TrimSpace(p); p != "" && filepath.IsAbs(p) {
			extra = append(extra, p, filepath.Dir(p))
		}
	}
	switch kind {
	case mpupload.KindNodeBin:
		addAbs(up.NodeBin)
	case mpupload.KindModuleDir:
		addAbs(up.ModuleDir)
		addAbs(up.SourceDir) // 依赖也可能直接装在源码目录下
	case mpupload.KindSourceDir:
		addAbs(up.SourceDir)
	}

	// dir 缺省时给一个合理起点：优先已配置的目录；都没有就留空，由 pathpick 选白名单内
	// 第一个存在的根目录（家目录已纳入白名单）。不能硬塞家目录——旧实现那样做，家目录
	// 不在白名单时一进来就报「不在允许浏览的范围」（2026-10-25 用户实测）。
	dir := strings.TrimSpace(c.Query("dir"))
	if dir == "" {
		switch kind {
		case mpupload.KindNodeBin:
			if up.NodeBin != "" && filepath.IsAbs(up.NodeBin) {
				dir = filepath.Dir(up.NodeBin)
			}
		case mpupload.KindModuleDir:
			if up.ModuleDir != "" {
				dir = up.ModuleDir
			} else if up.SourceDir != "" {
				dir = up.SourceDir
			}
		case mpupload.KindSourceDir:
			dir = up.SourceDir
		}
	}

	res, err := pathpick.List(dir, kind, extra)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"code": 1, "msg": err.Error()})
		return
	}
	// 顺带把自动探测到的候选一并下发，界面可直接列出「猜你想要的」。
	res.Suggestions = pathpick.Suggest(kind, extra)
	// 数组字段兜底成 []：Go 的 nil slice 会序列化成 JSON null，而界面按数组用
	// （suggestions.length / entries.length）。一旦是 null，选择器渲染直接抛异常、
	// 窗口弹不出来——pathpick 内部已保证非 nil，这里在序列化边界再兜一道。
	if res.Suggestions == nil {
		res.Suggestions = []pathpick.Suggestion{}
	}
	if res.Entries == nil {
		res.Entries = []pathpick.Entry{}
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "data": res})
}
