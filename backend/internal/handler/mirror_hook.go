package handler

import (
	"weichuan/internal/mirror"
)

// mirrorManager 由 cmd/server、cmd/devserver 注入（测试 harness 不注入 → no-op）。
// 用包级注入而不是给所有写接口加参数：镜像是横切增强，签名变动会波及几十处测试，
// 而它本身没有任何请求期分支——只有「在写入成功后丢一个后台任务」这一件事。
var mirrorManager *mirror.Manager

// SetMirrorManager 注入本地镜像管理器（幂等）。
func SetMirrorManager(m *mirror.Manager) { mirrorManager = m }

// scheduleMirror 在对象成功写入主存储后调用。绝不阻断主流程、绝不回传错误。
func scheduleMirror(cid int64, key string) {
	if mirrorManager != nil {
		mirrorManager.Schedule(cid, key)
	}
}

// removeMirrorErr 清理本地镜像副本，把错误交给调用方决定是否中止。
// 目前唯一调用方是对象双清端点：它必须先清镜像再删主对象，镜像清不掉就整体取消，
// 不能像「上传后回拉」那样只留痕——否则会留下「主对象已删、备份还在」的半清状态。
func removeMirrorErr(key string) error {
	if mirrorManager == nil {
		return nil
	}
	return mirrorManager.Remove(key)
}
