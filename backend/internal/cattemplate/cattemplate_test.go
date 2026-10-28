package cattemplate

import "testing"

// TestDefaultReturnsCopy 钉住「Default() 返回副本」这个契约。
//
// 为什么值得单独测：调用方（开通企业、演示数据）会拿这些分类塞进 model 结构体，
// 顺手改 Sort 是很自然的事。如果 Default() 直接返回包级切片，第一次开通时改过的
// 排序会留在里面，第二次开通拿到的就是被改过的模板——这种 bug 只在「连续开通两家
// 企业」时现形，本地点一次永远正常。
func TestDefaultReturnsCopy(t *testing.T) {
	a := Default()
	if len(a) == 0 {
		t.Fatal("模板不应为空")
	}
	a[0].Name = "被改过的名字"
	a[0].Sort = 999

	b := Default()
	if b[0].Name == "被改过的名字" || b[0].Sort == 999 {
		t.Fatalf("Default() 返回了共享切片：第二次调用拿到了被改过的值 %+v", b[0])
	}
}

// TestDefaultSortIsSequential 排序必须是 1..N 且无重复。
//
// 开通企业时分类顺序直接决定客户在「经典业绩」页与小程序里看到的 Tab 次序；
// 手写排序最容易出的是「两条都是 3」——列表按 sort asc 排，并列时次序由数据库决定，
// 换台机器就换个顺序，且不报错。
func TestDefaultSortIsSequential(t *testing.T) {
	list := Default()
	seen := map[int]bool{}
	for i, c := range list {
		if c.Name == "" {
			t.Fatalf("第 %d 条分类名称为空", i)
		}
		if c.Sort != i+1 {
			t.Fatalf("第 %d 条 sort 应为 %d，实际 %d（%s）", i, i+1, c.Sort, c.Name)
		}
		if seen[c.Sort] {
			t.Fatalf("sort %d 重复", c.Sort)
		}
		seen[c.Sort] = true
	}
}
