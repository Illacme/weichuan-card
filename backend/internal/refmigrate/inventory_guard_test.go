package refmigrate

import (
	"os"
	"reflect"
	"regexp"
	"sync"
	"testing"

	"gorm.io/gorm/schema"
)

// 本文件是 Inventory 与出参侧 ResolveRefs 之间的**源码级守门**。
//
// Inventory 的注释写着「新增资源字段时必须同时改三处（模型 / 出参解析 / 这里）」，
// 但这句话原本没有任何东西盯着。漏掉的后果不是报错，而是：盘点看不见那一列 →
// 计划里没有那批对象 → 执行器不搬 → 关掉平台存储那天用户发现裂图，而**没有任何
// 日志或断言在这之前响过**。这正是本项目反复强调的「没人报错时用户无从察觉」。
//
// 守的方向只有一个：**ResolveRefs 里出现的字段，Inventory 必须有**。
// 反方向不成立且不该成立——Inventory 刻意比 ResolveRefs 宽（open_apply.avatar
// 只在申请页用、project_case.album 是 json:"-" 由两处展开点单独解析、
// home_layout.bg_image 与 media_check 两列不走出参解析），这是设计而非遗漏。

var (
	// func (cp *CompanyProfile) ResolveRefs() {
	reResolveFunc = regexp.MustCompile(`^func \((\w+) \*(\w+)\) ResolveRefs\(\)`)
	// cp.Cover = ResolveResourceRef(cp.Cover)
	//
	// 不锚 ^：单行形态 `func (e *Employee) ResolveRefs() { e.Avatar = … }` 的字段
	// 与方法声明在同一行上。第一版锚了 ^，于是 7 个字段只抓到 4 个（单行的全漏），
	// 靠下面 count < 7 的兜底才没变成「静默通过」。
	reResolveField = regexp.MustCompile(`(\w+)\.(\w+) = ResolveResourceRef\(`)
)

// resolveRefFields 从 model/refs.go 里抓出 (模型类型名, 字段名) 对。
// 用源码扫描而不是反射：ResolveRefs 是方法体，反射看不到它碰了哪些字段。
func resolveRefFields(t *testing.T) [][2]string {
	t.Helper()
	src, err := os.ReadFile("../model/refs.go")
	if err != nil {
		t.Fatalf("读取 model/refs.go 失败（这个守门依赖它存在且形态不变）：%v", err)
	}
	var (
		out   [][2]string
		cur   string // 当前 ResolveRefs 方法的接收者类型名
		count int    // 抓到的行数，用来兜底「正则被重构搞失效」
	)
	for _, line := range regexp.MustCompile(`\r?\n`).Split(string(src), -1) {
		if m := reResolveFunc.FindStringSubmatch(line); m != nil {
			cur = m[2]
		}
		if m := reResolveField.FindStringSubmatch(line); m != nil {
			if cur == "" {
				t.Fatalf("在 ResolveRefs 方法体外出现在的字段解析行：%q", line)
			}
			out = append(out, [2]string{cur, m[2]})
			count++
		}
	}
	// 正则静默失效是这个守门最可能的坏法：改个写法就一条也抓不到，
	// 于是「全部通过」。所以要求至少抓到已知的这批字段数量。
	if count < 7 {
		t.Fatalf("只从 model/refs.go 抓到 %d 个字段解析行（期望 ≥ 7）：正则很可能已与源码形态脱节，守门失效", count)
	}
	return out
}

// TestInventoryCoversEveryResolveRefsField 是主守门。
//
// 两侧用的名字不同：ResolveRefs 里是 Go 字段名（VideoURL），Inventory 里是
// 物理列名（video_url）。这层映射不自己拼（拼了就是第三份命名规则），
// 而是问 gorm 的 schema——与运行期建表/扫库用的是同一套 NamingStrategy。
func TestInventoryCoversEveryResolveRefsField(t *testing.T) {
	// 建索引：模型类型名 → (模型值, 已盘点的物理列名集合)
	type entry struct {
		model any
		cols  map[string]bool
	}
	byType := map[string]*entry{}
	for _, col := range Inventory() {
		typ := reflect.TypeOf(col.Model)
		if typ == nil || typ.Kind() != reflect.Ptr {
			t.Fatalf("Inventory 里 %q 的 Model 必须是模型指针，实际 %T", col.Label, col.Model)
		}
		name := typ.Elem().Name()
		if byType[name] == nil {
			byType[name] = &entry{model: col.Model, cols: map[string]bool{}}
		}
		byType[name].cols[col.Col] = true
	}

	for _, f := range resolveRefFields(t) {
		typ, goField := f[0], f[1]
		e := byType[typ]
		if e == nil {
			t.Errorf("model.%s.ResolveRefs 存在，但 refmigrate.Inventory 里没有任何一行来自 model.%s：\n"+
				"该模型的资源字段完全没有纳入盘点，执行器不会搬它们。", typ, typ)
			continue
		}
		dbCol := dbColumn(t, e.model, goField)
		if !e.cols[dbCol] {
			t.Errorf("model.%s.ResolveRefs 解析了字段 %s（物理列 %s），但 refmigrate.Inventory 里没有对应列。\n"+
				"后果：盘点看不见那一列 → 那批对象永远不会被搬迁 → 关掉平台存储后裂图且无任何报错。\n"+
				"修法：在 Inventory() 里补一条 {标签, &model.%s{}, 主键列, %q, Single}", typ, goField, dbCol, typ, dbCol)
		}
	}
}

// dbColumn 把 Go 字段名翻成物理列名，走 gorm 自己的 NamingStrategy。
func dbColumn(t *testing.T, model any, goField string) string {
	t.Helper()
	s, err := schema.Parse(model, &sync.Map{}, schema.NamingStrategy{})
	if err != nil {
		t.Fatalf("解析模型 schema 失败：%v", err)
	}
	f := s.LookUpField(goField)
	if f == nil {
		t.Fatalf("模型 %s 上找不到字段 %s", s.Name, goField)
	}
	return f.DBName
}

// TestInventoryHasNoDuplicateColumn 同一条「表 + 列」不许出现两次。
// 重复的症状是报告里出现两行同名数据、合计翻倍，而库里的数据只有一份——
// 执行器照计划执行会重复复制同一个对象、重复改同一行。
func TestInventoryHasNoDuplicateColumn(t *testing.T) {
	seen := map[string]string{}
	for _, col := range Inventory() {
		typ := reflect.TypeOf(col.Model).Elem().Name()
		key := typ + "." + col.Col
		if prev, ok := seen[key]; ok {
			t.Errorf("Inventory 里 %s 出现了两次（%q 与 %q）", key, prev, col.Label)
		}
		seen[key] = col.Label
	}
}
