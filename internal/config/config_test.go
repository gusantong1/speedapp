package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDefaultsHaveNoSigningOrStorageCredentials(t *testing.T) {
	t.Setenv("PACK_AUTH_TOKEN", "test-token-at-least-16")
	c, err := complete(&Config{ProjectRoot: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if c.KeystoreStorePass != "" || c.KeystoreKeyPass != "" || c.AutoGenerateKeystore || c.Storage.Enabled {
		t.Fatal("unsafe secret/storage defaults")
	}
	if c.Host != "127.0.0.1" || !filepath.IsAbs(c.DataDir) || c.VersionPath != "/app/version" {
		t.Fatal("bad defaults")
	}
}
func TestAuthAndSecretFiles(t *testing.T) {
	t.Setenv("PACK_AUTH_TOKEN", "")
	if _, err := complete(&Config{}); err == nil {
		t.Fatal("empty auth accepted")
	}
	f := filepath.Join(t.TempDir(), "token")
	os.WriteFile(f, []byte("runtime-token-at-least-16\r\n"), 0600)
	t.Setenv("PACK_AUTH_TOKEN_FILE", f)
	c, err := complete(&Config{ProjectRoot: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if c.AuthToken != "runtime-token-at-least-16" {
		t.Fatal("secret file not consumed")
	}
}
func TestInvalidLimitsAndVersionPath(t *testing.T) {
	for _, path := range []string{"//other-host", "https://host/", "/app/version?token=x", "/a\\b"} {
		t.Run(strings.ReplaceAll(path, "/", "_"), func(t *testing.T) {
			_, err := complete(&Config{AuthToken: "test-token-at-least-16", VersionPath: path, ProjectRoot: t.TempDir()})
			if err == nil {
				t.Fatal("invalid version path accepted")
			}
		})
	}
	if _, err := complete(&Config{AuthToken: "test-token-at-least-16", Download: DownloadConfig{MaxEntries: 50001}, ProjectRoot: t.TempDir()}); err == nil {
		t.Fatal("bad limit accepted")
	}
}
