package content

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"speedapp-packager/app/packager/internal/config"
)

func limits() config.DownloadConfig {
	return config.DownloadConfig{TimeoutSec: 5, MaxZipBytes: 1 << 20, MaxExpandedBytes: 2 << 20, MaxFileBytes: 1 << 20, MaxEntries: 100, MaxCompressionRatio: 200}
}
func archive(t *testing.T, paths ...string) []byte {
	t.Helper()
	var b bytes.Buffer
	z := zip.NewWriter(&b)
	for i := 0; i < len(paths); i += 2 {
		f, err := z.CreateHeader(&zip.FileHeader{Name: paths[i], Method: zip.Store})
		if err != nil {
			t.Fatal(err)
		}
		if _, err = f.Write([]byte(paths[i+1])); err != nil {
			t.Fatal(err)
		}
	}
	if err := z.Close(); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}
func extractBytes(t *testing.T, b []byte, l config.DownloadConfig) (map[string]FileInfo, string, error) {
	t.Helper()
	root := t.TempDir()
	p := filepath.Join(root, "input.zip")
	os.WriteFile(p, b, 0600)
	destination := filepath.Join(root, "html")
	m, err := Extract(p, destination, l)
	return m, destination, err
}

func TestRealHTTPDownloadPreservesNestedStaticZIP(t *testing.T) {
	b := archive(t, "index.html", "<html>Cocos</html>", "assets/main/settings.json", "{\"scene\":1}", "assets/sub/game.js", "window.game=1", "engine/cocos.wasm", "\x00asm")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write(b) }))
	defer server.Close()
	l := limits()
	l.AllowHTTP = true
	l.AllowedOrigins = []string{server.URL}
	l.AllowedPrivateCIDRs = []string{"127.0.0.1/32"}
	d, err := NewDownloader(l)
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	p := filepath.Join(root, "content.zip")
	hash, err := d.Download(context.Background(), server.URL+"/content.zip", p)
	if err != nil {
		t.Fatal(err)
	}
	want := sha256.Sum256(b)
	if hash != hex.EncodeToString(want[:]) {
		t.Fatal("wrong hash")
	}
	m, err := Extract(p, filepath.Join(root, "html"), l)
	if err != nil {
		t.Fatal(err)
	}
	if len(m) != 4 {
		t.Fatalf("lost static resources: %v", m)
	}
	data, _ := os.ReadFile(filepath.Join(root, "html", "assets", "sub", "game.js"))
	if string(data) != "window.game=1" {
		t.Fatal("resource changed")
	}
}

func TestDownloaderBlocksPrivateIPAndRedirects(t *testing.T) {
	var hits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if r.URL.Path == "/redirect" {
			http.Redirect(w, r, "http://localhost:"+strings.Split(r.Host, ":")[1]+"/target", 302)
			return
		}
		w.Write([]byte("zip"))
	}))
	defer server.Close()
	l := limits()
	l.AllowHTTP = true
	l.AllowedOrigins = []string{server.URL}
	d, _ := NewDownloader(l)
	if _, err := d.Download(context.Background(), server.URL, filepath.Join(t.TempDir(), "blocked.zip")); err == nil {
		t.Fatal("private IP accepted")
	}
	if hits.Load() != 0 {
		t.Fatal("blocked destination was contacted")
	}
	l.AllowedPrivateCIDRs = []string{"127.0.0.1/32"}
	d, _ = NewDownloader(l)
	if _, err := d.Download(context.Background(), server.URL+"/redirect", filepath.Join(t.TempDir(), "redirect.zip")); err == nil {
		t.Fatal("redirect outside whitelist accepted")
	}
	if hits.Load() != 1 {
		t.Fatal("redirect target was contacted")
	}
	for _, raw := range []string{"file:///etc/passwd", "http://user:secret@127.0.0.1/", "https://example.com/", server.URL + "/#fragment"} {
		if err := d.Validate(raw); err == nil {
			t.Fatalf("accepted %s", raw)
		}
	}
}

func TestDownloadSizeAndTruncation(t *testing.T) {
	for _, test := range []struct {
		name    string
		handler http.HandlerFunc
	}{
		{"declared-large", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Length", "10000")
			w.Write([]byte("a"))
		}},
		{"stream-large", func(w http.ResponseWriter, r *http.Request) {
			w.(http.Flusher).Flush()
			w.Write(bytes.Repeat([]byte("x"), 100))
		}},
		{"truncated", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Length", "10")
			w.Write([]byte("x"))
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			s := httptest.NewServer(test.handler)
			defer s.Close()
			l := limits()
			l.AllowHTTP = true
			l.AllowedOrigins = []string{s.URL}
			l.AllowedPrivateCIDRs = []string{"127.0.0.1/32"}
			l.MaxZipBytes = 20
			d, _ := NewDownloader(l)
			p := filepath.Join(t.TempDir(), "input.zip")
			if _, err := d.Download(context.Background(), s.URL, p); err == nil {
				t.Fatal("bad download accepted")
			}
			if _, err := os.Stat(p); !os.IsNotExist(err) {
				t.Fatal("partial ZIP retained")
			}
		})
	}
}

func TestUnsafeOrIncompleteZIPNeverPublished(t *testing.T) {
	for name, b := range map[string][]byte{
		"traversal":      archive(t, "index.html", "ok", "../escape", "bad"),
		"absolute":       archive(t, "/index.html", "bad"),
		"backslash":      archive(t, "index.html", "ok", "a\\..\\bad", "bad"),
		"drive":          archive(t, "index.html", "ok", "C:/bad", "bad"),
		"duplicate":      archive(t, "index.html", "ok", "assets/a", "1", "ASSETS/A", "2"),
		"missing-entry":  archive(t, "game.js", "ok"),
		"empty-entry":    archive(t, "index.html", ""),
		"path-collision": archive(t, "index.html", "ok", "assets", "file", "assets/game.js", "bad"),
	} {
		t.Run(name, func(t *testing.T) {
			_, dest, err := extractBytes(t, b, limits())
			if err == nil {
				t.Fatal("unsafe ZIP accepted")
			}
			if _, err = os.Stat(dest); !os.IsNotExist(err) {
				t.Fatal("invalid directory published")
			}
			stages, _ := filepath.Glob(filepath.Join(filepath.Dir(dest), ".html-stage-*"))
			if len(stages) != 0 {
				t.Fatal("stage not removed")
			}
		})
	}
}

func TestZIPCRCLinksAndExpansionLimits(t *testing.T) {
	b := archive(t, "index.html", "CRC_MARKER")
	b[bytes.Index(b, []byte("CRC_MARKER"))] = 'X'
	if _, _, err := extractBytes(t, b, limits()); err == nil {
		t.Fatal("corrupt CRC accepted")
	}
	var buffer bytes.Buffer
	z := zip.NewWriter(&buffer)
	h := &zip.FileHeader{Name: "index.html", Method: zip.Store}
	h.SetMode(os.ModeSymlink | 0777)
	f, _ := z.CreateHeader(h)
	f.Write([]byte("target"))
	z.Close()
	if _, _, err := extractBytes(t, buffer.Bytes(), limits()); err == nil {
		t.Fatal("symlink accepted")
	}
	good := archive(t, "index.html", "12345", "assets/a", "12345")
	for name, alter := range map[string]func(*config.DownloadConfig){"file": func(l *config.DownloadConfig) { l.MaxFileBytes = 4 }, "total": func(l *config.DownloadConfig) { l.MaxExpandedBytes = 9 }, "entries": func(l *config.DownloadConfig) { l.MaxEntries = 1 }} {
		t.Run(name, func(t *testing.T) {
			l := limits()
			alter(&l)
			if _, _, err := extractBytes(t, good, l); err == nil {
				t.Fatal("limit bypassed")
			}
		})
	}
	buffer.Reset()
	z = zip.NewWriter(&buffer)
	f, _ = z.Create("index.html")
	f.Write(bytes.Repeat([]byte("x"), 50000))
	z.Close()
	if _, _, err := extractBytes(t, buffer.Bytes(), limits()); err == nil {
		t.Fatal("compression bomb accepted")
	}
}

func TestReservedIPAndHTTPSPolicy(t *testing.T) {
	d, _ := NewDownloader(limits())
	for _, ip := range []string{"127.0.0.1", "10.0.0.1", "169.254.169.254", "100.64.0.1", "198.18.0.1", "::1", "fc00::1", "64:ff9b::a00:1", "::ffff:127.0.0.1"} {
		if d.allowedIP(netip.MustParseAddr(ip)) {
			t.Errorf("accepted reserved %s", ip)
		}
	}
	if !d.allowedIP(netip.MustParseAddr("8.8.8.8")) {
		t.Fatal("public IP rejected")
	}
	if _, err := WebURL("http://example.com", false); err == nil {
		t.Fatal("HTTP accepted by default")
	}
	if _, err := NewDownloader(config.DownloadConfig{AllowedOrigins: []string{"https://example.com/path"}}); err == nil {
		t.Fatal("non-origin whitelist accepted")
	}
	if _, err := NewDownloader(config.DownloadConfig{AllowedPrivateCIDRs: []string{"0.0.0.0/0"}}); err == nil {
		t.Fatal("blanket CIDR accepted")
	}
}
