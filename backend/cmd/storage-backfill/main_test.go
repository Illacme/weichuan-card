package main

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

// TestExitForDistinguishesProblemsFromFailures 钉住「2 = 数据有异常」与
// 「1 = 命令挂了」的分界。usage 里承诺过这个约定，而第一版实现把两种都
// 走成了 1——手跑完全看不出来，只有 CI 会误判：红灯亮了先去重跑命令，
// 而不是去处理那条串号数据。
func TestExitForDistinguishesProblemsFromFailures(t *testing.T) {
	if got := exitFor(nil); got != 0 {
		t.Errorf("无错误应返回 0，实际 %d", got)
	}
	// 包装过的也要认（runPlan 是 fmt.Errorf("%w：共 N 条", errProblems, …)）
	wrapped := fmt.Errorf("%w：共 6 条", errProblems)
	if got := exitFor(wrapped); got != 2 {
		t.Errorf("有异常数据应返回 2，实际 %d", got)
	}
	if got := exitFor(errors.New("连接数据库失败")); got != 1 {
		t.Errorf("真错误应返回 1，实际 %d", got)
	}
	// 反例守门：任何**不带** errProblems 的错误都不许返回 2，
	// 否则 CI 会把「连不上库」当成「有串号数据」，去改数据而不是修环境。
	if got := exitFor(fmt.Errorf("扫描 employees.avatar 失败：%w", errors.New("boom"))); got == 2 {
		t.Errorf("普通错误不得映射成 2")
	}
}

// TestDispWidthCountsWideChars 钉住「中文一个字符占两列」这条前提。
// 如果 dispWidth 退化成 utf8.RuneCountInString，「员工头像」会算成 4 而不是 8，
// 下面的对齐测试立刻红——这就是本文件存在的意义。
func TestDispWidthCountsWideChars(t *testing.T) {
	cases := []struct {
		in   string
		want int
	}{
		{"", 0},
		{"abc", 3},
		{"员工头像", 8},
		{"t/5/a.jpg", 9},
		{"待搬迁", 6},
		{"全角（括号）", 12}, // 全角标点必须算 2，否则带括号的列名整列左移一格
		{"员工头像2", 9},   // 半角数字算 1
		// ⚠ 是 East Asian Width = Ambiguous：CJK 终端里显示 2 列、其它环境 1 列。
		// 刻意按 1 算——报告里它不出现在表格内（只在表外的警示行），
		// 而歧义字符按宽的算会让某些终端反向错位，猜错不如不猜。
		{"⚠", 1},
	}
	for _, c := range cases {
		if got := dispWidth(c.in); got != c.want {
			t.Errorf("dispWidth(%q) = %d，期望 %d", c.in, got, c.want)
		}
	}
}

// TestRenderTableAlignsEveryRow 断言「每行显示宽度相同」。
// 注意这条是**自洽性**检查：它用的是 dispWidth 自己那把尺子，尺子整体算错
// （比如把中文算成 1 列）时它仍然会通过。真正的绝对值由下面的金标准用例钉。
// 它抓的是另一类错：只给某些行补了空格、或分隔符数量不一致。
func TestRenderTableAlignsEveryRow(t *testing.T) {
	out := renderTable(
		[]string{"列", "表", "总计", "待搬迁"},
		[][]string{
			{"员工头像", "employees", "12", "4"},
			{"案例图集", "project_cases", "5", "2"},
			{"送检原稿地址", "media_check", "1", "1"},
		},
	)
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) != 4 {
		t.Fatalf("期望 1 行表头 + 3 行数据，实际 %d 行：\n%s", len(lines), out)
	}
	want := dispWidth(lines[0])
	for i, ln := range lines {
		if got := dispWidth(ln); got != want {
			t.Errorf("第 %d 行显示宽度 %d ≠ 表头 %d，表格错位：%q", i, got, want, ln)
		}
	}
}

// TestRenderTableRightAlignsNumbers 让数字右对齐：一列里 1 和 12 混在一起时，
// 左对齐会让人按个位数读（1 和 12 都"顶格"）。
func TestRenderTableRightAlignsNumbers(t *testing.T) {
	out := renderTable([]string{"列", "总计"}, [][]string{
		{"甲", "1"},
		{"乙", "120"},
	})
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if !strings.HasSuffix(lines[1], "  1") {
		t.Errorf("个位数应当右对齐（前面补空格），实际 %q", lines[1])
	}
	if !strings.HasSuffix(lines[2], "120") {
		t.Errorf("最长值不该补空格，实际 %q", lines[2])
	}
}

// TestRenderTableLeftAlignsLabels 第一列左对齐：中文标签长度不一时，
// 右对齐会让「列名」离它对应的表名忽远忽近。
func TestRenderTableLeftAlignsLabels(t *testing.T) {
	out := renderTable([]string{"列", "表"}, [][]string{
		{"短", "a"},
		{"很长的列名", "b"},
	})
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if !strings.HasPrefix(lines[1], "短 ") {
		t.Errorf("短标签应当左对齐（后面补空格），实际 %q", lines[1])
	}
}

// TestRenderTableGolden 是绝对检查：把渲染结果整段写死比对。
// 上面那条对齐用例是用同一把尺子量自己——dispWidth 整体算错（把中文当 1 列）时
// 它依然通过。这一条才有独立预期：下面每个空格数都是按「中文两列宽」手算的。
//
// 列宽 = 各列显示宽度最大值：col0 max(2, 8, 16)=16（「公司简介备用视频」8 个字 = 16 列）；
// col1 max(2, 9, 16)=16；col2 max(4, 1, 3)=4。
// 第一列左对齐（空格补在后面），其余右对齐（空格补在前面），列之间固定 2 个空格。
func TestRenderTableGolden(t *testing.T) {
	sp := func(n int) string { return strings.Repeat(" ", n) }
	out := renderTable(
		[]string{"列", "表", "总计"},
		[][]string{
			{"员工头像", "employees", "1"},
			{"公司简介备用视频", "company_profiles", "120"},
		},
	)
	want := strings.Join([]string{
		"列" + sp(14) + "  " + sp(14) + "表" + "  " + sp(0) + "总计",                      // 2+14=16；2+14=16；4 占满
		"员工头像" + sp(8) + "  " + sp(7) + "employees" + "  " + sp(3) + "1",              // 8+8=16；9+7=16；1+3=4
		"公司简介备用视频" + sp(0) + "  " + sp(0) + "company_profiles" + "  " + sp(1) + "120", // 16 占满；16 占满；3+1=4
	}, "\n") + "\n"
	if out != want {
		t.Errorf("渲染结果与金标准不符\n实际：\n%s期望：\n%s", out, want)
	}
}

// TestRenderTableEmpty 空表也要只剩表头，不能多一行空行——
// 否则「没有一个引用」的库会打出一片空白，看起来像命令挂了。
func TestRenderTableEmpty(t *testing.T) {
	out := renderTable([]string{"列", "表"}, nil)
	if out != "列  表\n" {
		t.Errorf("空表期望只有表头，实际 %q", out)
	}
}
