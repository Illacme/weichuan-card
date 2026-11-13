package storage

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// ObjectKeyFromURL 覆盖 signHandler 旧实现的空 base bug：baseURL 为空时，
// "/uploads/x.jpg" 必须反推成 "x.jpg"，而不是被错截成 "uploads/x.jpg"（多一层目录）。
func TestObjectKeyFromURL(t *testing.T) {
	cases := []struct {
		name, in, base, want string
		wantErr              bool
	}{
		{"空base本地路径", "/uploads/x.jpg", "", "x.jpg", false},
		{"有base本地路径", "/uploads/x.jpg", "https://cdn.x", "x.jpg", false},
		{"绝对URL去base", "https://cdn.x/abcd.jpg", "https://cdn.x", "abcd.jpg", false},
		{"裸键", "abcd.jpg", "", "abcd.jpg", false},
		{"空输入", "", "", "", true},
		{"不安全引用", "data:x", "", "", true},
	}
	for _, c := range cases {
		got, err := ObjectKeyFromURL(c.in, c.base)
		if c.wantErr {
			require.Error(t, err, c.name)
			continue
		}
		require.NoError(t, err, c.name)
		require.Equal(t, c.want, got, c.name)
	}
}
