// Package safego 给后台异步任务加统一的 panic 屏障。
//
// gin.Recovery() 只包住**当前请求的 goroutine**。项目里另有九处 `go fn()`
// （备份、订阅消息、CDN 刷新、媒体送检、定期清理、安装后自重启等）跑在独立
// 协程里，任何一处 panic 都会一路冒泡到 runtime：进程直接退出，日志里只有
// 一堆栈、看不出是哪个任务炸的，而此时可能还有几百个正常请求在飞。
//
// 这里刻意只做「兜住 + 带任务名与堆栈落日志」这一件事，不重试、不重启任务：
// 异步任务失败后能不能重跑，取决于它有没有幂等性（备份有互斥锁、通知可能重发），
// 统一重试反而会制造重复副作用。让运维先看见、再决定就好。
package safego

import (
	"log"
	"runtime/debug"
)

// reportPanic 抽成变量是为了可测：测试替换成 channel 通知，不必去截获全局 log 输出。
// 生产实现带任务名与完整堆栈——只打 panic 值的话，「哪个任务炸了」依旧得靠猜。
var reportPanic = func(name string, recovered any, stack []byte) {
	log.Printf("[PANIC][%s] %v\n%s", name, recovered, stack)
}

// Run 在独立 goroutine 里执行 fn，并兜住 fn 内任何 panic。
//
// 注意：recover 必须写在**同一个 goroutine 的 defer** 里；在启动方 defer 一个
// recover 是接不到子协程 panic 的。这是本包存在的全部理由。
func Run(name string, fn func()) {
	go func() {
		defer func() {
			if r := recover(); r != nil {
				reportPanic(name, r, debug.Stack())
			}
		}()
		fn()
	}()
}
