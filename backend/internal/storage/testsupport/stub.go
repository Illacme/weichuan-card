// Package testsupport 提供只在测试里使用的 storage.Resolver 桩实现。
//
// 为什么单独成包：生产代码里**只有一个** Resolver 实现
// （`storageprofile.Resolver`）。历史上这里还有一个 `storage.StaticResolver`，
// 它唯一的立身理由是「TENANT_STORAGE_ROUTE=off 时的正式实现」——开关在第七十五轮
// 删除后，这个理由消失了，而它留在生产包里会不断引诱人「图省事退回静态实现」，
// 那会让租户桶永远不生效（档案界面配了、上传仍走平台盘，且没有任何报错）。
//
// 挪到独立包后，生产包只剩一个实现，源码级守门（`upload_route_guard_test.go`）
// 继续盯着两个 main 不许出现任何静态包装。
package testsupport

import "weichuan/internal/storage"

// StaticResolver 是「所有租户都走同一份实例」的桩，仅供测试包装 stub Uploader。
//
// **它不表达任何产品行为**：不要在任何 cmd/ 或 internal/handler 的非测试文件里引用它。
type StaticResolver struct {
	up     storage.Uploader
	driver string
}

// NewStaticResolver 包装一个实例。driver 传空串时按 local 口径返回，
// 与 handler 侧 capabilityHandler 的历史口径一致（空 = local），
// 不要把空串当「未知驱动」另立分支。
func NewStaticResolver(up storage.Uploader, driver string) *StaticResolver {
	return &StaticResolver{up: up, driver: driver}
}

// For 忽略租户，永远返回同一个实例。返回 error 的唯一情形是实例本身没建起来，
// 此时**不能**返回 (nil, nil)：调用方会拿 nil 去 Save 而 panic。
func (s *StaticResolver) For(int64) (storage.Uploader, error) {
	if s.up == nil {
		return nil, storage.ErrNoUploader
	}
	return s.up, nil
}

// Platform 返回同一个实例。
func (s *StaticResolver) Platform() storage.Uploader { return s.up }

// DriverFor 返回构造时给的驱动名，空串按 local。
func (s *StaticResolver) DriverFor(int64) string {
	if s.driver == "" {
		return "local"
	}
	return s.driver
}

var _ storage.Resolver = (*StaticResolver)(nil)
