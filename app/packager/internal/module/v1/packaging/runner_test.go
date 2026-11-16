package packaging

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"speedapp-packager/app/packager/internal/config"
)

func testRunner(t *testing.T, origin string) *Runner {
	t.Helper()
	root := t.TempDir()
	c := &config.Config{ProjectRoot: root, DataDir: filepath.Join(root, "data"), DefaultAppName: "Test App", VersionPath: "/app/version", BuildTimeoutSec: 30, Download: config.DownloadConfig{AllowHTTP: true, AllowedOrigins: []string{origin}, AllowedPrivateCIDRs: []string{"127.0.0.1/32"}, TimeoutSec: 5, MaxZipBytes: 1 << 20, MaxExpandedBytes: 2 << 20, MaxFileBytes: 1 << 20, MaxEntries: 100, MaxCompressionRatio: 200}}
	r, err := NewRunner(c)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestPrepareURLAndLocalInputs(t *testing.T) {
	var b bytes.Buffer
	z := zip.NewWriter(&b)
	f, _ := z.Create("index.html")
	f.Write([]byte("<html>Cocos</html>"))
	f, _ = z.Create("assets/main/game.js")
	f.Write([]byte("window.scene=1"))
	z.Close()
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write(b.Bytes()) }))
	defer s.Close()
	r := testRunner(t, s.URL)
	for _, mode := range []string{"url", "local"} {
		t.Run(mode, func(t *testing.T) {
			req := BuildReq{DominList: []string{"backend.example.com"}, Version: "1.0.1", LastVersionURL: ""}
			if mode == "url" {
				req.URL = "https://game.example.com"
			} else {
				req.LastVersionURL = s.URL + "/content.zip"
			}
			id, _ := NewID()
			job, rc, err := r.Prepare(context.Background(), req, id)
			if err != nil {
				t.Fatal(err)
			}
			if rc.VersionEndpoints[0] != "https://backend.example.com/app/version" {
				t.Fatal("bad backend version endpoint")
			}
			data, err := os.ReadFile(filepath.Join(job.AssetsDir, "pack-config.json"))
			if err != nil {
				t.Fatal(err)
			}
			var roundtrip RuntimeConfig
			if err = json.Unmarshal(data, &roundtrip); err != nil {
				t.Fatal(err)
			}
			if roundtrip.Version != "1.0.1" || roundtrip.URL != req.URL || roundtrip.BuildID != id {
				t.Fatal("request config changed")
			}
			_, err = os.Stat(filepath.Join(job.AssetsDir, "html", "assets", "main", "game.js"))
			if mode == "local" && (err != nil || rc.BundleSHA256 == "") {
				t.Fatal("local assets not prepared")
			}
			if mode == "url" && !os.IsNotExist(err) {
				t.Fatal("URL mode unexpectedly bundled assets")
			}
		})
	}
}

func TestValidateAndProjectLock(t *testing.T) {
	r := testRunner(t, "https://cdn.example.com")
	for _, req := range []BuildReq{
		{URL: "https://site", DominList: []string{}, Version: "1"},
		{DominList: []string{"api.example.com"}, Version: "1"},
		{URL: "https://site", DominList: []string{"https://api.example.com/path"}, Version: "1"},
		{URL: "https://site", DominList: []string{"api.example.com"}, Version: "../bad"},
		{DominList: []string{"api.example.com"}, Version: "1", LastVersionURL: "https://unlisted.example.com/content.zip"},
	} {
		if err := r.Validate(&req); err == nil {
			t.Fatalf("invalid request accepted: %#v", req)
		}
	}
	if err := os.WriteFile(filepath.Join(r.cfg.DataDir, "build.lock"), []byte("other task"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Run(context.Background(), BuildReq{}); !errors.Is(err, ErrBusy) {
		t.Fatal("concurrent build lock ignored")
	}
	data, _ := os.ReadFile(filepath.Join(r.cfg.DataDir, "build.lock"))
	if string(data) != "other task" {
		t.Fatal("other task lock modified")
	}
}

// 只验证产物检查器；这些合成 ZIP 不是可安装 APK，也不用于构建验收。
func TestVerifyAPKRejectsMissingOrChangedAssets(t *testing.T) {
	root := t.TempDir()
	assets := filepath.Join(root, "assets")
	os.Mkdir(assets, 0700)
	os.WriteFile(filepath.Join(assets, "pack-config.json"), []byte("expected"), 0600)
	for _, data := range []string{"", "wrong"} {
		var b bytes.Buffer
		z := zip.NewWriter(&b)
		for _, name := range []string{"AndroidManifest.xml", "classes.dex"} {
			f, _ := z.Create(name)
			f.Write([]byte("synthetic"))
		}
		if data != "" {
			f, _ := z.Create("assets/pack-config.json")
			f.Write([]byte(data))
		}
		z.Close()
		p := filepath.Join(root, "synthetic.zip")
		os.WriteFile(p, b.Bytes(), 0600)
		if err := VerifyAPK(p, assets); err == nil {
			t.Fatal("invalid asset content accepted")
		}
	}
}
