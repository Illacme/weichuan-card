package storage

import "strings"

// NormalizePlan 是「搬迁计划」：一个库里的引用，搬迁后应该长什么样。
//
// 为什么不能直接用 NormalizeOrKeep：那个函数的契约是**写入侧**的——
// 「这个值能不能原样留着」（历史形态原样保留，等 P3 搬），它刻意不产出目标键。
// P3 要的恰好相反：历史形态必须算出「搬完该写成什么」。
//
// 两者共用同一套判定（ParseRef → legacyRel → ParseRef → ObjectKeyFor），
// 不各写一份：一旦分叉，症状是「迁移脚本搬过去的键，写入侧又不认」，
// 而这种分叉在两边各自的单测里都看不出来。差分守门见 ref_plan_test.go。
type NormalizePlan struct {
	// Kind 复用写入侧的三种归类：
	//   RefNormalized  — 已是新形态键（库里不用改；对象是否已在租户桶里另查）
	//   RefLegacyPath  — 历史形态（无归属前缀）：对象要搬、库要改
	//   RefForeignURL  — 外域直链：永不搬
	Kind RefKeepKind
	// Key 是搬迁后**应该写进库里**的对象键。
	//   Kind=RefNormalized 时等于库里的现值；
	//   Kind=RefLegacyPath 时是 t/<cid>/<rel>（目标键）；
	//   Kind=RefForeignURL 时为空。
	Key string
	// LegacyRel 是历史形态剥掉 /uploads/ 或基址后的无前缀相对键，
	// 也就是**平台侧现在那个对象**的键。Kind=RefLegacyPath 时有效。
	LegacyRel string
	// Keep 是「原样保留」的值（空引用与外域直链），Kind=RefForeignURL 时有效。
	Keep string
	// Scope / CompanyID 是 Key 的归属，由键前缀还原（空值时是 "" 与 0）。
	//
	// 为什么在这里给：搬迁命令要判断「这条引用属于本租户、平台、还是别的租户」。
	// 若让它自己去 ParseRef，解析口径就多了一处；而这一处恰恰是最容易写歪的
	// （把 t/9/... 当成 t/7/... 的键，症状是 A 的图出现在 B 的名片页）。
	Scope     Scope
	CompanyID int64
}

// PlanNormalize 计算单个引用的搬迁计划（纯函数，不读库不写库）。
//
// 三类「不该被改」的值与写入侧口径完全一致：
//   - 空值：不是错误，也没有对象可搬（avatar / logo 经常为空）；
//   - 外域直链：video_source=url 那一档就是任意外域地址；
//   - 已是 t/<cid>/ 的键：库不用改。
//
// 同源却推不出键、javascript: / data: / 路径穿越一律返回 error——这些值在
// 迁移里**不能猜**：猜出来的键会把对象搬到错误位置，或者覆盖别人的对象。
func PlanNormalize(raw, baseURL string, scope Scope, companyID int64) (NormalizePlan, error) {
	s := strings.TrimSpace(raw)
	// 先交给写入侧同一套判定，拿到归类与「能不能原样留」的结论。
	key, kind, err := NormalizeOrKeep(s, baseURL, scope, companyID)
	if err != nil {
		return NormalizePlan{}, err
	}
	switch kind {
	case RefForeignURL:
		return NormalizePlan{Kind: kind, Keep: s}, nil

	case RefLegacyPath:
		// 历史形态：NormalizeOrKeep 刚刚成功剥过前缀，这里必然也能剥出来
		// （剥不出来它会返回 error，走不到这个分支）。重算一次是为了拿到 rel，
		// 而不是为了重新判定——判定只有一处。
		rel, rerr := legacyRel(s, baseURL)
		if rerr != nil {
			return NormalizePlan{}, rerr
		}
		target, terr := ObjectKeyFor(scope, companyID, rel)
		if terr != nil {
			return NormalizePlan{}, terr
		}
		return NormalizePlan{
			Kind: kind, Key: target, LegacyRel: rel, Keep: s,
			Scope: scope, CompanyID: companyID,
		}, nil

	default: // RefNormalized（含空值）
		plan := NormalizePlan{Kind: kind, Key: key}
		if key != "" {
			// 已归一的键自己带前缀，归属从键上还原而不是由调用方声明——
			// 这一段刻意复用 ParseRef，不在这里另写字符串判断。
			ref, perr := ParseRef(key)
			if perr != nil {
				return NormalizePlan{}, perr
			}
			plan.Scope, plan.CompanyID = ref.Scope, ref.CompanyID
		}
		return plan, nil
	}
}
