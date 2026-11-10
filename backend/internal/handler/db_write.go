package handler

import (
	"log"
	"net/http"

	"github.com/gin-gonic/gin"
)

// dbWriteFailed 是**写库失败**的统一出口。
//
// 为什么必须统一：内容管理 / 账号写接口此前成片丢掉 GORM 的 Error，失败时照样回
// code:0——界面显示「保存成功」，库里其实一行没写，而且没有任何报错
// （项目判据：没人报错时用户是否无从察觉？）。最常见的真实诱因是升级只换二进制、
// 漏同步 migrations/ 目录，MySQL 报 Unknown column（0035 / 0038 都栽过）。所以：
//   - err 一律落服务端日志，运维能直接看到列缺失 / 死锁 / 磁盘满；
//   - 客户端只给一句固定文案，**绝不回显 err.Error()**：原始 SQL 错误会带出
//     表名、列名、约束名，等于把库结构送给探测者。
//
// 与「幂等保存」的边界：这里只处理 res.Error != nil。值没变时 MySQL 的
// RowsAffected=0 **不是错误**（见 company-profile 的注释），不能拿来当失败判据；
// 是否要据此判断「记录不存在」由调用方先查存在性再决定。
func dbWriteFailed(c *gin.Context, where, msg string, err error) {
	log.Printf("[DB][ERROR] %s err=%v", where, err)
	c.JSON(http.StatusOK, gin.H{"code": 1, "msg": msg})
}
