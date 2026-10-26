package model

// 小程序版本发布状态机。
//
// 为什么值得单开一个文件、还把流转写成白名单：这条链上有钱和责任的代价。
// 提审消耗微信审核额度、**撤回不返还**（官方硬约束），发布则直接改变租户
// 小程序的线上行为。状态写错一步的后果不是「界面显示不对」，而是
// 「已经把审过的版本发上去了却以为还在审核中」。
//
// 所以：**只有 canTransition 里列出的流向才允许发生**，其余一律由 service 层
// 拒绝。不要在这里加「方便一下」的边——每加一条边，上面那句话的可信度就降一分。

const (
	// MPReleaseDraft 已登记（代码已由 CI 上传，微信侧已是体验版），尚未提审。
	MPReleaseDraft = "draft"
	// MPReleaseAuditing 已提审，等微信审。审核时长小时到天级，不可催。
	MPReleaseAuditing = "auditing"
	// MPReleaseAuditPassed 审核通过、待发布。此时才允许点「发布」。
	MPReleaseAuditPassed = "audit_passed"
	// MPReleaseReleased 已上线。终态——微信侧不支持「再发一次同一版本」，
	// 要再动就得走新一轮 上传→提审→发布。
	MPReleaseReleased = "released"
	// MPReleaseRejected 审核被拒绝。微信会给出 reason，改完可重新提审。
	MPReleaseRejected = "rejected"
	// MPReleaseWithdrawn 已撤回。撤回**不返还**已消耗的审核额度，
	// 所以这是个应当慎用的动作，UI 上要有二次确认。
	MPReleaseWithdrawn = "withdrawn"
)

// MPReleaseStatuses 全部状态的固定顺序（界面筛选项与自检遍历用）。
// 顺序即「一条版本的正常生命周期」，终态排在最后。
var MPReleaseStatuses = []string{
	MPReleaseDraft,
	MPReleaseAuditing,
	MPReleaseAuditPassed,
	MPReleaseReleased,
	MPReleaseRejected,
	MPReleaseWithdrawn,
}

// MPReleaseStatusNames 状态的中文名（界面直接用）。
var MPReleaseStatusNames = map[string]string{
	MPReleaseDraft:       "待提审",
	MPReleaseAuditing:    "审核中",
	MPReleaseAuditPassed: "审过待发布",
	MPReleaseReleased:    "已上线",
	MPReleaseRejected:    "已驳回",
	MPReleaseWithdrawn:   "已撤回",
}

// canTransition 合法流转白名单。key = 当前态，value = 可达的目标态。
//
//	draft    → auditing            提交审核
//	auditing → audit_passed        轮询发现审过
//	auditing → rejected            轮询发现驳回
//	auditing → withdrawn           撤回审核
//	audit_passed → released        发布
//	rejected   → auditing          改完重新提审（会再消耗一次额度）
//	withdrawn  → auditing          重新提审
//
// 刻意没有的边：
//   - draft → released：跳过审核直接发布。微信不允许，我们也绝不提供入口。
//   - released → *：已上线是终态。回退是另一套机制（版本回退接口），
//     且要回退到「上一个版本」而不是「把这条记录改回去」——那会丢历史。
//   - audit_passed → auditing：审过了又退回审核，微信没有这个动作。
var canTransition = map[string][]string{
	MPReleaseDraft:       {MPReleaseAuditing},
	MPReleaseAuditing:    {MPReleaseAuditPassed, MPReleaseRejected, MPReleaseWithdrawn},
	MPReleaseAuditPassed: {MPReleaseReleased},
	MPReleaseRejected:    {MPReleaseAuditing},
	MPReleaseWithdrawn:   {MPReleaseAuditing},
	MPReleaseReleased:    {},
}

// CanTransition 判断状态流转是否合法。未知状态（含空串）一律 false——
// 宁可拒掉也不猜，猜错会把一条脏记录推到更脏的状态。
func CanTransition(from, to string) bool {
	if from == "" || to == "" {
		return false
	}
	for _, t := range canTransition[from] {
		if t == to {
			return true
		}
	}
	return false
}

// ValidMPReleaseStatus 判断是否为已知状态（过滤外部输入用）。
func ValidMPReleaseStatus(s string) bool {
	_, ok := MPReleaseStatusNames[s]
	return ok
}

// WXAuditStatusToRelease 把微信 get_auditstatus 返回的 status 映射成本表状态。
// 返回 (目标态, 是否终局)。
//
// 微信枚举（官方文档）：0 审核成功 / 1 审核被拒绝 / 2 审核中 / 3 已撤回 / 4 审核延后。
//   - 2 审核中：不是终局，调用方应继续等，不要落库。
//   - 4 审核延后：**刻意映射成 auditing 而不是新状态**。延后是「这单先挂着」，
//     微信还可能变回通过或拒绝；如果我们落一个「延后」态，就得为它补一堆流转边，
//     而它对用户的意义与「审核中」没有区别——都是等着。reason 照样存下来。
//   - 未知值：不猜，按「仍在审核中」处理并让上层记录一条告警。
func WXAuditStatusToRelease(wxStatus int) (string, bool) {
	switch wxStatus {
	case 0:
		return MPReleaseAuditPassed, true
	case 1:
		return MPReleaseRejected, true
	case 3:
		return MPReleaseWithdrawn, true
	case 4:
		return MPReleaseAuditing, false
	default: // 2 审核中，以及任何没见过的值
		return MPReleaseAuditing, false
	}
}
