package mpupload

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 用一个假 node 可执行文件跑通 Go 侧逻辑：它按约定读 stdin、打结果行，
// 并按入参里的暗号决定成功或失败。这样不必真装 Node + miniprogram-ci
// 也能守住「参数怎么拼、结果怎么解、失败怎么报」这几件事——
// 而这几件事恰恰是上传功能最常见的翻车点（参数拼错 → 微信报一句听不懂的错）。

// fakeNode 生成一个假 node 脚本，把 stdin 原样写到 $FAKE_NODE_IN 供断言，
// 入参含 "boom" 时模拟上传失败。
func fakeNode(t *testing.T, dir string) string {
	t.Helper()
	inFile := filepath.Join(dir, "node-stdin.json")
	t.Setenv("FAKE_NODE_IN", inFile)

	script := "#!/bin/sh\n" +
		"cat > \"$FAKE_NODE_IN\"\n" +
		"if grep -q '\"boom\"' \"$FAKE_NODE_IN\"; then\n" +
		"  printf '%s{\"ok\":false,\"error\":\"假微信：代码上传密钥不正确\"}\\n' '__WEICHUAN_MP_UPLOAD__'\n" +
		"  exit 1\n" +
		"fi\n" +
		"if grep -q '\"silent\"' \"$FAKE_NODE_IN\"; then\n" +
		"  echo 'compiling...' >&2\n" +
		"  exit 3\n" +
		"fi\n" +
		"echo 'compiling...'\n" +
		"printf '%s{\"ok\":true}\\n' '__WEICHUAN_MP_UPLOAD__'\n"
	p := filepath.Join(dir, "fake-node")
	if err := os.WriteFile(p, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

// env 搭一套「看起来就绪」的环境：源码目录 + 装了 miniprogram-ci 的目录。
func env(t *testing.T, nodeBin string) (Config, string) {
	t.Helper()
	root := t.TempDir()
	src := filepath.Join(root, "miniprogram")
	mod := filepath.Join(root, "ci")
	for _, d := range []string{
		src,
		filepath.Join(mod, "node_modules", "miniprogram-ci"),
	} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(src, "project.config.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	return Config{NodeBin: nodeBin, ModuleDir: mod, SourceDir: src, Timeout: 0}, root
}

func TestUploadSuccessPassesRightPayload(t *testing.T) {
	dir := t.TempDir()
	cfg, root := env(t, fakeNode(t, dir))
	inFile := filepath.Join(dir, "node-stdin.json")

	if err := Upload(context.Background(), cfg, Request{
		AppID:   "wxabc123",
		Version: "1.2.0",
		Desc:    "补齐节日海报",
		Key:     "-----BEGIN RSA PRIVATE KEY-----\nabc\n-----END RSA PRIVATE KEY-----",
	}); err != nil {
		t.Fatalf("应上传成功，实际报错：%v", err)
	}

	got, err := os.ReadFile(inFile)
	if err != nil {
		t.Fatalf("假 node 没收到配置：%v", err)
	}
	s := string(got)
	for _, want := range []string{`"appid":"wxabc123"`, `"version":"1.2.0"`, `"desc":"补齐节日海报"`} {
		if !strings.Contains(s, want) {
			t.Errorf("传给 node 的配置里应有 %s，实际：%s", want, s)
		}
	}
	// 密钥不能出现在配置里：只允许出现临时文件路径
	if strings.Contains(s, "BEGIN RSA PRIVATE KEY") {
		t.Error("密钥内容不应进 node 入参（会出现在进程列表/临时文件里）")
	}
	if !strings.Contains(s, `"privateKeyPath":"`) {
		t.Errorf("应以文件路径传递密钥，实际：%s", s)
	}
	// 源码目录要原样传给 node（miniprogram-ci 按它找工程）
	if !strings.Contains(s, `"projectPath":"`+root+"/miniprogram\"") {
		t.Errorf("projectPath 应为源码目录，实际：%s", s)
	}
}

func TestUploadSurfacesWeChatError(t *testing.T) {
	dir := t.TempDir()
	cfg, _ := env(t, fakeNode(t, dir))

	err := Upload(context.Background(), cfg, Request{
		AppID:   "wxabc123",
		Version: "boom",
		Key:     "-----BEGIN RSA PRIVATE KEY-----\nx\n-----END RSA PRIVATE KEY-----",
	})
	if err == nil {
		t.Fatal("微信侧失败时必须报错，不能当成成功")
	}
	if !strings.Contains(err.Error(), "代码上传密钥不正确") {
		t.Errorf("错误信息应带上微信给的原因，实际：%v", err)
	}
}

func TestUploadNonZeroExitWithoutResultIsNotSuccess(t *testing.T) {
	dir := t.TempDir()
	cfg, _ := env(t, fakeNode(t, dir))

	err := Upload(context.Background(), cfg, Request{
		AppID:   "wxabc123",
		Version: "silent",
		Key:     "-----BEGIN RSA PRIVATE KEY-----\nx\n-----END RSA PRIVATE KEY-----",
	})
	if err == nil {
		t.Fatal("node 非 0 退出且没有结果行时必须报错——静默失败比报错危险")
	}
	if !strings.Contains(err.Error(), "compiling...") {
		t.Errorf("应把 stderr 尾部带进错误信息，实际：%v", err)
	}
}

func TestUploadBlockedWhenEnvNotReady(t *testing.T) {
	dir := t.TempDir()
	cfg, _ := env(t, fakeNode(t, dir))
	cfg.ModuleDir = filepath.Join(dir, "not-installed")

	err := Upload(context.Background(), cfg, Request{
		AppID: "wxabc", Version: "1.0.0",
		Key: "-----BEGIN RSA PRIVATE KEY-----\nx\n-----END RSA PRIVATE KEY-----",
	})
	if err == nil {
		t.Fatal("环境不就绪时不应真的去跑 node")
	}
	if !strings.Contains(err.Error(), "miniprogram-ci") {
		t.Errorf("应点名缺的是 miniprogram-ci，实际：%v", err)
	}
}

func TestProblemsListsEverything(t *testing.T) {
	// 用一个不存在的 node 路径把「node 找不到」也触发出来。
	// 不能假设测试机上没装 node——开发机通常装着，那样这项就不会出现。
	cfg := Config{NodeBin: filepath.Join(t.TempDir(), "no-such-node")}
	ps := cfg.Problems()
	if len(ps) < 3 {
		t.Fatalf("全空配置应列出多个缺失项（而不是只报第一个），实际：%v", ps)
	}
	joined := strings.Join(ps, "|")
	for _, want := range []string{"node", "miniprogram-ci", "源码目录"} {
		if !strings.Contains(joined, want) {
			t.Errorf("缺失项里应提到 %s，实际：%v", want, ps)
		}
	}
}

func TestKeyProblems(t *testing.T) {
	ok := "-----BEGIN RSA PRIVATE KEY-----\nMIIabc\n-----END RSA PRIVATE KEY-----"
	if ps := KeyProblems(ok); len(ps) > 0 {
		t.Errorf("正常密钥不应报问题：%v", ps)
	}
	if ps := KeyProblems("  \n\t "); len(ps) == 0 {
		t.Error("空密钥必须报问题")
	}
	// 贴成 AppSecret 是最常见的误操作
	if ps := KeyProblems("a1b2c3d4e5f6g7h8i9j0"); len(ps) == 0 {
		t.Error("把 AppSecret 当密钥贴上必须报问题")
	}
	// 粘贴被截断
	if ps := KeyProblems("-----BEGIN RSA PRIVATE KEY-----\nMIIabc"); len(ps) == 0 {
		t.Error("不完整的密钥必须报问题")
	}
}

func TestNormalizeKeyStripsCRLF(t *testing.T) {
	got := normalizeKey("  -----BEGIN RSA PRIVATE KEY-----\r\nMIIabc\r\n-----END RSA PRIVATE KEY-----\r\n  ")
	want := "-----BEGIN RSA PRIVATE KEY-----\nMIIabc\n-----END RSA PRIVATE KEY-----"
	if got != want {
		t.Errorf("应去掉首尾空白并把 CRLF 转成 LF\n期望：%q\n实际：%q", want, got)
	}
}
