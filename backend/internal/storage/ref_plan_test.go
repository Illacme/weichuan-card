package storage

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestPlanNormalizeMatchesNormalizeOrKeep 是**差分守门**：
//
//	写入侧的「能不能原样留」  ==  搬迁计划的「搬完该是什么」
//
// 两个函数的判定必须是同一套。分叉的症状很隐蔽：迁移脚本按 PlanNormalize 把
// 对象搬到 t/7/x.jpg、库里也写成 t/7/x.jpg，而写入侧 NormalizeOrKeep 认为这个值
// 属历史形态又「原样保留」——两边各自的单测都绿，只有真跑一遍才看得出不一致。
//
// 所以这里不重写期望值，而是拿同一份输入同时喂两个函数，逐条比对语义：
//   - normalized → 两者产出的键必须一模一样；
//   - legacy-path → NormalizeOrKeep 原样返回，PlanNormalize 必须给出
//     ObjectKeyFor(ScopeTenant, cid, rel) 且 rel 就是被保留的那个值的无前缀形态；
//   - foreign-url → 两者都原样保留，PlanNormalize 不得给出目标键；
//   - 报错 → 两者都必须报错（不能一个宽容一个严格）。
func TestPlanNormalizeMatchesNormalizeOrKeep(t *testing.T) {
	const base = "https://card.example.com/uploads"
	cases := []struct {
		name string
		raw  string
		cid  int64
	}{
		{"空值", "", 7},
		{"新形态租户键", "t/7/abc.jpg", 7},
		{"新形态平台键", "p/library/cat-1.png", 7},
		{"本地驱动相对路径带前缀", "/uploads/t/7/abc.jpg", 7},
		{"历史相对路径", "/uploads/abc.jpg", 7},
		{"历史绝对 URL（同基址）", "https://card.example.com/uploads/abc.jpg", 7},
		{"历史绝对 URL 带缓存戳", "https://card.example.com/uploads/abc.jpg?v=3", 7},
		{"历史裸键", "abc.jpg", 7},
		{"历史裸键带目录", "2026/10/abc.jpg", 7},
		{"外域直链", "https://cdn.other.com/a.png", 7},
		{"伪协议", "javascript:alert(1)", 7},
		{"路径穿越", "../../etc/passwd", 7},
		{"别的租户的键", "t/9/abc.jpg", 7},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			key, kind, err := NormalizeOrKeep(c.raw, base, ScopeTenant, c.cid)
			plan, perr := PlanNormalize(c.raw, base, ScopeTenant, c.cid)

			if err != nil {
				require.Error(t, perr, "NormalizeOrKeep 报错时 PlanNormalize 也必须报错：%v", err)
				return
			}
			require.NoError(t, perr)
			require.Equal(t, kind, plan.Kind, "归类必须一致")

			switch kind {
			case RefNormalized:
				require.Equal(t, key, plan.Key, "已归一的键必须原样出现在计划里")
			case RefLegacyPath:
				// 写入侧原样保留的正是历史值；计划必须先剥出 rel 再加前缀。
				require.Equal(t, key, plan.Keep, "写入侧保留的原值")
				require.NotEmpty(t, plan.LegacyRel, "历史形态必须给出平台侧对象键")
				require.NotContains(t, plan.LegacyRel, TenantKeyPrefix, "rel 不该带租户前缀")
				want, oerr := ObjectKeyFor(ScopeTenant, c.cid, plan.LegacyRel)
				require.NoError(t, oerr)
				require.Equal(t, want, plan.Key, "计划里的目标键必须由 ObjectKeyFor 产出")
			case RefForeignURL:
				require.Equal(t, key, plan.Keep)
				require.Empty(t, plan.Key, "外域直链不该有目标键")
			default:
				t.Fatalf("未覆盖的归类 %q", kind)
			}
		})
	}
}

// TestPlanNormalizeRejectsForeignScope 单独钉住「A 租户的键出现在 B 租户字段」：
// 迁移命令必须能把它认出来（errors.Is ErrScopeMismatch），而不是当成普通非法值。
// 两者处置不同——串号要人工确认，非法值直接跳过。
func TestPlanNormalizeRejectsForeignScope(t *testing.T) {
	_, err := PlanNormalize("t/9/abc.jpg", "", ScopeTenant, 7)
	require.Error(t, err)
	require.True(t, errors.Is(err, ErrScopeMismatch), "必须是 ErrScopeMismatch，实际：%v", err)
}

// TestPlanNormalizeNeverTouchesUnsafeInputs 钉住「猜不出来就报错」：
// 迁移是批量操作，一次猜错就是一张图指向不存在的键，而现场只会表现为「图裂了」。
func TestPlanNormalizeNeverTouchesUnsafeInputs(t *testing.T) {
	for _, raw := range []string{"javascript:alert(1)", "data:image/png;base64,AAAA", "../etc/passwd", "a//b.jpg"} {
		t.Run(raw, func(t *testing.T) {
			_, err := PlanNormalize(raw, "", ScopeTenant, 7)
			require.Error(t, err, "不安全输入必须报错，不能猜一个键出来")
		})
	}
}
