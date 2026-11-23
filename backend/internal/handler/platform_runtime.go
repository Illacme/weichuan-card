package handler

// 平台主存储「访问基址 / 公开读」的运行期读取点。
//
// 基址与公开读的真值源是「主存储」内置档案（迁移 0059）。上传器已经由
// Resolver 按档案 version 现建，上传返回给前端的 URL 天然跟着变；但写入归一、
// 内容安全下架这几条**辅助路径**不在上传器里，它们读的是启动期 cfg 值拷贝，
// 若不联动就会出现「新上传用新基址、归一/下架仍用旧基址」的静默漂移。
//
// 这里用函数注入（与 SetProfileOriginResolver / SetMirrorManager 同一模式），
// 而不是给几十个 handler 加 resolver 参数：
//   - 生产入口在启动时注入 profiles.PlatformBaseURL / PlatformPublic；
//   - 未注入时回落到 cfg 原值——单测与未迁移的旧库行为完全不变。
//
// 目录类（upload_dir / mirror_dir）不在此列，改目录仍需重启。

import "weichuan/internal/config"

var (
	platformBaseURLFn func() (string, bool)
	platformPublicFn  func() (bool, bool)
)

// SetPlatformRuntime 注入平台主存储的运行期配置读取函数。
// 两个参数都可传 nil（只注入其中一个）。重复调用以最后一次为准。
// 第二个返回值 ok=false 表示「还没有生效实例」，必须回落 cfg，不能拿零值当真值。
func SetPlatformRuntime(baseURL func() (string, bool), public func() (bool, bool)) {
	platformBaseURLFn = baseURL
	platformPublicFn = public
}

// effectiveBaseURL 返回当前生效的访问基址：注入的 provider 给出 ok=true 时优先，
// 否则回落启动期 cfg。ok=false 覆盖「档案还没读到 / 首次构造失败」两种情形。
func effectiveBaseURL(cfg config.Config) string {
	if platformBaseURLFn != nil {
		if v, ok := platformBaseURLFn(); ok && v != "" {
			return v
		}
	}
	return cfg.UploadBaseURL
}

// effectivePublic 返回当前生效的公开读开关，未注入或无生效实例时回落 cfg。
func effectivePublic(cfg config.Config) bool {
	if platformPublicFn != nil {
		if v, ok := platformPublicFn(); ok {
			return v
		}
	}
	return cfg.StoragePublic
}
