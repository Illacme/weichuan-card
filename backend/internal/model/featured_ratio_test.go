package model

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// 精选案例裁切比例是单列枚举：缺省 none 保持原图（升级零视觉突变），
// 其余四档固定比例容器 + aspectFill 居中裁切。这里钉住归一化契约，
// 防止有人把缺省改成某个固定比例，导致所有存量租户升级当天封面被裁掉两边。
func TestNormalizeFeaturedRatio(t *testing.T) {
	require.Equal(t, FeaturedRatioNone, DefaultFeaturedRatio, "缺省必须 none（原图不裁切）")

	// 空串（历史行 / 缺省）回 none
	require.Equal(t, FeaturedRatioNone, NormalizeFeaturedRatio(""))

	for _, v := range []string{
		FeaturedRatioNone, FeaturedRatio16x9, FeaturedRatio4x3,
		FeaturedRatio2x1, FeaturedRatio1x1,
	} {
		require.Equal(t, v, NormalizeFeaturedRatio(v), "合法档位 %s 应原样返回", v)
	}

	// 枚举外的值返回空串，由调用方（PUT）拒绝；不能静默兜底成某档。
	for _, bad := range []string{"3:2", "16/9", "1:2", "full", "auto"} {
		require.Equal(t, "", NormalizeFeaturedRatio(bad), "非法档位 %s 必须返回空串由上层拒绝", bad)
	}
}
