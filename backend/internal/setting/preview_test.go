package setting

import (
	"fmt"
	"sync/atomic"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

// setting 包此前没有测试文件，这里顺带补上最小 DB 夹具（内存 SQLite，纯 Go 无需 cgo）。
var dbSeq int64

func newTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := fmt.Sprintf("file:setting-test-%d?mode=memory&cache=shared", atomic.AddInt64(&dbSeq, 1))
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatalf("打开内存 SQLite 失败：%v", err)
	}
	return db
}

// resetGlobal 清掉全局实例，避免用例之间共享脏状态
func resetGlobal() {
	gmu.Lock()
	global = nil
	gmu.Unlock()
}

// preview 的语义很窄但很容易改坏：它决定「管理员能看到多少敏感值」——
// 放宽就是明文泄露（数据库口令 / 签名密钥），收紧又回到六颗星、失去核对意义。
// 因此把边界钉成测试：形态识别、遮挡位置、短值兜底、空值。
func TestPreview(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "MySQL 连接串：保留结构与库名，只遮口令段",
			in:   "root:secretpwd@tcp(127.0.0.1:3306)/weichuan?parseTime=true",
			want: "root:" + Mask + "@tcp(127.0.0.1:3306)/weichuan?parseTime=true",
		},
		{
			name: "sqlite 形态无凭据：原样返回，便于核对路径",
			in:   "sqlite:///data/weichuan.db",
			want: "sqlite:///data/weichuan.db",
		},
		{
			name: "密钥：首尾各留 4 位",
			in:   "abcdefghijklmnopqrstuvwxyz012345",
			want: "abcd****2345",
		},
		{
			name: "短值（dev 缺省密钥）：只露首尾各 2 位，仍能认出是它",
			in:   "dev-jwt-key",
			want: "de****ey",
		},
		{
			name: "过短：怎么露都约等于全文，退回掩码",
			in:   "abc12",
			want: Mask,
		},
		{
			name: "空值：保持空（界面显示未配置）",
			in:   "   ",
			want: "",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := preview(c.in); got != c.want {
				t.Fatalf("preview(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

// 预览必须真的隐去秘密：口令段与密钥中段不能出现在结果里。
// 这条是安全底线——改 preview 时若不小心放宽，这里会先红。
func TestPreviewHidesSecret(t *testing.T) {
	dsn := "root:supersecret@tcp(db.internal:3306)/weichuan?parseTime=true"
	if got := preview(dsn); got == dsn || contains(got, "supersecret") {
		t.Fatalf("连接串预览泄露了口令：%q", got)
	}
	key := "aaaabbbbccccddddeeeeffffgggghhhh"
	if got := preview(key); got == key || contains(got, "ccccdddd") {
		t.Fatalf("密钥预览泄露了中段：%q", got)
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

// 可写敏感项仍必须是纯掩码：预览只开放给只读项（All 里按 ReadOnly 分流）。
// 若把预览误用到可写项，界面就会把「已配置」变成可辨认片段，等于逐步泄露。
func TestAllSecretWritableIsMask(t *testing.T) {
	Init(newTestDB(t))
	defer resetGlobal()

	if err := Upsert("wx_appsecret", "plain-secret-value"); err != nil {
		t.Fatalf("写入可写敏感项失败：%v", err)
	}
	for _, it := range All() {
		if it.Key != "wx_appsecret" {
			continue
		}
		if !it.Configured {
			t.Fatalf("刚写入的项应标记已配置")
		}
		if it.Value != Mask {
			t.Fatalf("可写敏感项应回传掩码，实际 %q", it.Value)
		}
		return
	}
	t.Fatalf("registry 中找不到 wx_appsecret")
}
