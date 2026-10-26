package license

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"os"
	"path/filepath"
	"testing"
	"time"

	"weichuan/internal/dbopen"
	"weichuan/internal/setting"

	"github.com/stretchr/testify/require"
)

func mustKey(t *testing.T) (ed25519.PublicKey, ed25519.PrivateKey) {
	pub, priv, err := ed25519.GenerateKey(nil)
	require.NoError(t, err)
	return pub, priv
}

// split2 把 "a\nb" 拆成 [a, b]（测试篡改载荷用）。
func split2(s string) [2]string {
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			return [2]string{s[:i], s[i+1:]}
		}
	}
	return [2]string{s, ""}
}

func TestSignAndVerifyRoundTrip(t *testing.T) {
	pub, priv := mustKey(t)
	SetPublicKey(pub)

	ent := &Entitlement{
		Edition:   "private",
		Seats:     50,
		ExpiresAt: time.Now().Add(24 * time.Hour),
		Features:  []string{"content_sec"},
	}
	content, err := SignLicense(priv, ent)
	require.NoError(t, err)

	got, err := ParseLicense(pub, []byte(content))
	require.NoError(t, err)
	require.Equal(t, ent.Edition, got.Edition)
	require.Equal(t, ent.Seats, got.Seats)
	require.Equal(t, ent.Features, got.Features)
	require.False(t, got.IsExpired())
}

func TestVerifyRejectsTamperedPayload(t *testing.T) {
	pub, priv := mustKey(t)
	ent := &Entitlement{Edition: "private", Seats: 10}
	content, err := SignLicense(priv, ent)
	require.NoError(t, err)

	lines := split2(content)
	payload, err := base64.StdEncoding.DecodeString(lines[0])
	require.NoError(t, err)
	payload[0] ^= 0xFF // 翻转解码后 JSON 的首字节
	bad := base64.StdEncoding.EncodeToString(payload) + "\n" + lines[1]

	_, err = ParseLicense(pub, []byte(bad))
	require.ErrorIs(t, err, ErrBadSignature)
}

func TestVerifyRejectsWrongKey(t *testing.T) {
	_, priv := mustKey(t)
	pub2, _ := mustKey(t)
	ent := &Entitlement{Edition: "private"}
	content, err := SignLicense(priv, ent)
	require.NoError(t, err)

	_, err = ParseLicense(pub2, []byte(content))
	require.ErrorIs(t, err, ErrBadSignature)
}

func TestParseRejectsExpired(t *testing.T) {
	pub, priv := mustKey(t)
	ent := &Entitlement{Edition: "private", ExpiresAt: time.Now().Add(-time.Hour)}
	content, err := SignLicense(priv, ent)
	require.NoError(t, err)

	_, err = ParseLicense(pub, []byte(content))
	require.ErrorIs(t, err, ErrExpired)
}

func TestFileResolverLoadsAndVerifies(t *testing.T) {
	pub, priv := mustKey(t)
	SetPublicKey(pub)

	dir := t.TempDir()
	path := filepath.Join(dir, "license.key")
	ent := &Entitlement{Edition: "private", Seats: 5, Domain: "example.com"}
	content, err := SignLicense(priv, ent)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))

	r := NewFileResolver(pub, path)
	got, err := r.Resolve(context.Background(), 0)
	require.NoError(t, err)
	require.Equal(t, 5, got.Seats)
	require.Equal(t, "example.com", got.Domain)
}

func TestFileResolverMissingFile(t *testing.T) {
	pub, _ := mustKey(t)
	r := NewFileResolver(pub, filepath.Join(t.TempDir(), "nope.key"))
	_, err := r.Resolve(context.Background(), 0)
	require.ErrorIs(t, err, ErrNotFound)
}

func TestDBResolverPermissive(t *testing.T) {
	db, _, err := dbopen.Open("sqlite://" + filepath.Join(t.TempDir(), "lic.db"))
	require.NoError(t, err)
	require.NoError(t, setting.Init(db))

	ent, err := DefaultDBResolver().Resolve(context.Background(), 0)
	require.NoError(t, err)
	require.Equal(t, "platform", ent.Edition) // 缺省值
	require.True(t, ent.SeatsUnlimited())
	require.False(t, ent.IsExpired())
}
