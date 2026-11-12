package handler

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// 后台列表分页的统一约定（2026-10-19）：
//
//   - 查询参数：page（缺省 1）/ size（缺省 20，上限 100）；另有各列表自己的筛选参数
//     （如 keyword / status）。
//   - 回包信封：{ code: 0, data: [...当前页...], total, page, size }。
//     data 仍是数组（与未分页时同构），分页元信息放在顶层——这样既有消费者
//     （脚本 / 旧前端 / 测试）取 data 依然可用，只是拿到首页一页。
//     与「审计日志」等新接口的历史 { data: { list, total } } 形状不同，
//     本文件是唯一权威实现，新接口一律走这里。
//
// 为什么要服务端分页：管理端列表（员工 / 申请 / 租户）是随运营时间单调增长的，
// 拉全表在千级就会把响应体与内存拖垮，且前端拿全量后自己切片只会掩盖问题
// （网络传了 1000 条，界面只显示 10 条）。

const (
	defaultPageSize = 20
	maxPageSize     = 100
)

// limitParam 解析 ?limit=：只做「上限保护」，不做分页。
//
// 适用对象与 pageParams 不同——小程序端的案例 / 服务 / 首页精选是「一次全量展示」
// 的内容型列表（端上没有分页 UI，也不该有：案例就那么多，翻页反而打断浏览）。
// 但全量 ≠ 无上限：内容是可增长的，必须给一个天花板，否则首屏 payload 会随运营
// 时间单调膨胀。缺省值与上限都由调用方给出（各列表的合理量级不同）。
func limitParam(c *gin.Context, def, max int) int {
	n, err := strconv.Atoi(c.Query("limit"))
	if err != nil || n <= 0 {
		return def
	}
	if n > max {
		return max
	}
	return n
}

// cdnCache 给公开只读接口加短缓存（只给共享缓存用，不给端上）。
//
// 小程序自身的 wx.request 不利用 HTTP 缓存，所以这不是给端上用的——是给反向代理
// / CDN 用的：内容型列表重复访问比例很高（同一个访客反复进出首页），短缓存能把
// 这部分重复回源挡掉。时长刻意取短（分钟级）：后台改了内容后，访客不该长时间
// 看到旧数据。
//
// 为什么同时发 max-age 和 s-maxage（2026-11-12 补）：
//
//	· s-maxage 是**共享缓存**（CDN / 反代）的专用指令，优先级高于 max-age；
//	· 但各家实现取哪个字段并不统一——只发 s-maxage，遇到只读 max-age 的边缘节点
//	  就等于没配（表现为「头看着对、cf-cache-status 一直 DYNAMIC」这种静默失效）；
//	· 两个都发同样的值，任何实现取哪个都得到同一个 TTL。
//
// 为什么必须**只在成功分支**调用：`add_header`/`Header()` 一旦落到错误响应上，
// 4xx/5xx 也会被边缘缓存——「员工不存在」被缓存一分钟事小，接口抖动期间把
// 500 缓存住才是灾难。所以调用点全部写在 `code == 0` 的那一支前面。
func cdnCache(c *gin.Context, seconds int) {
	c.Header("Cache-Control",
		"public, max-age="+strconv.Itoa(seconds)+", s-maxage="+strconv.Itoa(seconds))
}

// pageParams 解析分页参数：非法值回落缺省，size 超上限截断。
func pageParams(c *gin.Context) (page, size int) {
	page = atoiDefault(c.Query("page"), 1)
	size = atoiDefault(c.Query("size"), defaultPageSize)
	if size > maxPageSize {
		size = maxPageSize
	}
	return page, size
}

// pagedQuery 执行「先统计总数 → 夹页码 → 取当前页」的分页查询。
// 越界页码（如删完数据后仍停在第 5 页）夹到末页，避免返回空列表让用户以为没数据。
// 返回实际生效的 page，供回包元信息使用。
func pagedQuery(q *gorm.DB, order string, dest any, page, size int) (total int64, effPage int) {
	q.Count(&total)
	pages := int((total + int64(size) - 1) / int64(size))
	if pages < 1 {
		pages = 1
	}
	if page > pages {
		page = pages
	}
	q.Order(order).Offset((page - 1) * size).Limit(size).Find(dest)
	return total, page
}

// pagedOK 统一分页回包（见文件头信封约定）。
func pagedOK(c *gin.Context, list any, total int64, page, size int) {
	c.JSON(http.StatusOK, gin.H{
		"code": 0, "data": list, "total": total, "page": page, "size": size,
	})
}

// likeKeyword 关键字 → LIKE 匹配串；空关键字返回空串（调用方据此跳过过滤）。
//
// 不转义 % / _：SQL 走参数化（? 占位）不存在注入风险；而 SQLite 与 MySQL 的
// 转义语法不一致（SQLite 的 LIKE 没有默认转义符），强行加 \ 会让 SQLite 搜不到。
// 最坏后果是用户输一个 % 匹配变宽，不构成安全问题。
func likeKeyword(kw string) string {
	kw = strings.TrimSpace(kw)
	if kw == "" {
		return ""
	}
	return "%" + kw + "%"
}

// keywordCond 多字段 OR 模糊匹配：任一字段命中即算命中。
// fields 为白名单内的列名常量（不接受外部输入，不存在注入面）。
func keywordCond(kw string, fields ...string) (string, []any) {
	like := likeKeyword(kw)
	if like == "" || len(fields) == 0 {
		return "", nil
	}
	var sb strings.Builder
	args := make([]any, 0, len(fields))
	for i, f := range fields {
		if i > 0 {
			sb.WriteString(" OR ")
		}
		sb.WriteString(f + " LIKE ?")
		args = append(args, like)
	}
	return sb.String(), args
}

// statusCond 状态筛选：只接受调用方枚举出的合法值，非法值（含空串）不过滤。
func statusCond(status string, allowed ...string) (string, any) {
	for _, a := range allowed {
		if status == a {
			return "status = ?", status
		}
	}
	return "", nil
}

// intListParam 解析逗号分隔的 id 列表（如 ?staffIds=1,2,3），忽略非法片段；
// 超过 limit 时截断——用于「按 id 精确取少数几条」的场景，防止一次拉全表。
func intListParam(raw string, limit int) []int64 {
	if raw == "" {
		return nil
	}
	out := make([]int64, 0, 8)
	for _, part := range strings.Split(raw, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if id, err := strconv.ParseInt(part, 10, 64); err == nil && id > 0 {
			out = append(out, id)
			if limit > 0 && len(out) >= limit {
				break
			}
		}
	}
	return out
}
