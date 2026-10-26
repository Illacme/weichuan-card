package license

import (
	"context"
	"os"
)

// privateMode 是否按「私有 / 独立部署」处理授权：
//  1. 显式 DEPLOY_MODE=private；或
//  2. 默认路径（或 LICENSE_PATH）存在许可证文件。
//
// 否则视为集中 / 平台模式（走 DBResolver，宽松不强制）。
func privateMode() bool {
	if os.Getenv("DEPLOY_MODE") == "private" {
		return true
	}
	p := DefaultLicensePath
	if v := os.Getenv("LICENSE_PATH"); v != "" {
		p = v
	}
	if _, err := os.Stat(p); err == nil {
		return true
	}
	return false
}

// PrivateMode 导出私有 / 独立部署判定，供 handler 层在租户解析时区分单租户形态。
func PrivateMode() bool { return privateMode() }

// Verify 解析本次启动的授权：私有模式验本地签名文件，集中模式读设置。
// 解析成功会写入 Current() 供 Enforcement 读取。
//
// 返回的错误在私有模式下应视为致命（调用方据此拒绝启动）；
// 集中模式宽松返回、不报错。
func Verify(ctx context.Context) (*Entitlement, error) {
	var (
		ent *Entitlement
		err error
	)
	if privateMode() {
		ent, err = DefaultFileResolver().Resolve(ctx, 0)
	} else {
		ent, err = DefaultDBResolver().Resolve(ctx, 0)
	}
	if err != nil {
		return nil, err
	}
	SetCurrent(ent)
	return ent, nil
}
