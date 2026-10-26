package model

import "testing"

// 状态机是这条链上唯一的安全带：提审扣额度、撤回不返还、发布立即改变租户小程序的
// 线上行为。所以这里把「允许的流向」与「明令禁止的流向」都钉死——
// 后者尤其重要，它们不是「还没实现」，而是**刻意不存在**：
//
//   - draft → released：跳过审核直接上线；
//   - audit_passed → auditing：审过了又退回审核中（微信没有这个反向操作）；
//   - released → *：终态，微信不支持「再发一次同一版本」；
//   - withdrawn → released：撤回后直接上线（撤回不是审核通过的等价物）。
//
// 哪天有人想「方便一下」加条边，这些用例会红。
func TestMPReleaseTransitions(t *testing.T) {
	allowed := [][2]string{
		{MPReleaseDraft, MPReleaseAuditing},
		{MPReleaseAuditing, MPReleaseAuditPassed},
		{MPReleaseAuditing, MPReleaseRejected},
		{MPReleaseAuditing, MPReleaseWithdrawn},
		{MPReleaseAuditPassed, MPReleaseReleased},
		{MPReleaseRejected, MPReleaseAuditing},
		{MPReleaseWithdrawn, MPReleaseAuditing},
	}
	for _, c := range allowed {
		if !CanTransition(c[0], c[1]) {
			t.Errorf("应允许 %s → %s，实际被拒绝", c[0], c[1])
		}
	}

	forbidden := [][2]string{
		{MPReleaseDraft, MPReleaseReleased},       // 跳过审核
		{MPReleaseDraft, MPReleaseAuditPassed},    // 登记即审过
		{MPReleaseDraft, MPReleaseRejected},       // 登记即驳回
		{MPReleaseDraft, MPReleaseWithdrawn},      // 没提审何来撤回
		{MPReleaseAuditing, MPReleaseAuditing},    // 自环
		{MPReleaseAuditing, MPReleaseReleased},    // 审核中直接上线
		{MPReleaseAuditPassed, MPReleaseAuditing}, // 审过退回审核
		{MPReleaseAuditPassed, MPReleaseWithdrawn},
		{MPReleaseRejected, MPReleaseReleased}, // 驳回后直接上线
		{MPReleaseRejected, MPReleaseWithdrawn},
		{MPReleaseWithdrawn, MPReleaseReleased}, // 撤回后直接上线
		{MPReleaseWithdrawn, MPReleaseWithdrawn},
		{MPReleaseReleased, MPReleaseAuditing}, // 终态无出路
		{MPReleaseReleased, MPReleaseReleased},
		{"", MPReleaseAuditing}, // 空态
		{MPReleaseAuditing, ""},
		{"nonsense", MPReleaseAuditing}, // 未登记的状态
		{MPReleaseAuditing, "nonsense"},
	}
	for _, c := range forbidden {
		if CanTransition(c[0], c[1]) {
			t.Errorf("应拒绝 %q → %q，实际被放行", c[0], c[1])
		}
	}
}

// released 是终态：对任何目标态都不放行（含自身）。
func TestMPReleaseReleasedIsTerminal(t *testing.T) {
	for _, to := range append([]string{}, MPReleaseStatuses...) {
		if CanTransition(MPReleaseReleased, to) {
			t.Errorf("终态 released 不应能流向 %s", to)
		}
	}
}

// 每个登记在案的状态都要有中文名：界面直接用它，缺一个就会出现裸键。
func TestMPReleaseStatusNamesCoverAll(t *testing.T) {
	for _, s := range MPReleaseStatuses {
		if MPReleaseStatusNames[s] == "" {
			t.Errorf("状态 %s 缺中文名", s)
		}
		if !ValidMPReleaseStatus(s) {
			t.Errorf("状态 %s 未通过 ValidMPReleaseStatus", s)
		}
	}
	if ValidMPReleaseStatus("nonsense") {
		t.Error("未登记的状态不应通过校验")
	}
	if ValidMPReleaseStatus("") {
		t.Error("空串不应通过校验")
	}
}

// 微信审核状态 → 本地状态。第二个返回值是「是否终局」= 本地记录要不要跟着动。
//
// 重点两条：
//   - 2（审核中）与 4（审核延后）都返回 terminal=false——本地本来就是 auditing，
//     再落一次库既多余又会让审计多出噪音；延后刻意不开新状态（见函数注释）；
//   - 没见过的值不猜，按「仍在审核中」处理（terminal=false），让上层继续等，
//     而不是把未知值塞进某个状态里。
func TestWXAuditStatusToRelease(t *testing.T) {
	cases := []struct {
		wx       int
		want     string
		terminal bool
	}{
		{0, MPReleaseAuditPassed, true},
		{1, MPReleaseRejected, true},
		{2, MPReleaseAuditing, false},
		{3, MPReleaseWithdrawn, true},
		{4, MPReleaseAuditing, false},
		{5, MPReleaseAuditing, false},
		{-1, MPReleaseAuditing, false},
	}
	for _, c := range cases {
		got, terminal := WXAuditStatusToRelease(c.wx)
		if got != c.want || terminal != c.terminal {
			t.Errorf("WXAuditStatusToRelease(%d) = (%q, %v)，期望 (%q, %v)", c.wx, got, terminal, c.want, c.terminal)
		}
	}
}
