package router

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"speedapp-packager/app/packager/internal/config"
	"speedapp-packager/app/packager/internal/module/v1/packaging"
	"speedapp-packager/app/packager/internal/response"
	"speedapp-packager/app/packager/internal/svc"
)

const (
	token       = "test-token-at-least-16"
	artifactID  = "0123456789abcdef0123456789abcdef"
	v1BuildPath = APIPrefix + "/packaging/build"
)

type stubBuilder struct {
	calls  atomic.Int32
	start  chan struct{}
	finish chan struct{}
}

func (b *stubBuilder) Validate(*packaging.BuildReq) error { return nil }
func (b *stubBuilder) Ready() error                       { return nil }
func (b *stubBuilder) Run(_ context.Context, req packaging.BuildReq) (packaging.BuildResp, error) {
	b.calls.Add(1)
	if b.start != nil {
		close(b.start)
		<-b.finish
	}
	name := artifactID + ".apk"
	return packaging.BuildResp{ID: artifactID, FileName: name, FileURL: "/artifacts/" + name, Mode: "url", Version: req.Version}, nil
}
func testRouter(t *testing.T, b *stubBuilder) (*gin.Engine, *config.Config) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	c := &config.Config{AuthToken: token, MaxRequestBytes: 1024, DataDir: t.TempDir()}
	r := gin.New()
	Register(r, &svc.ServiceContext{Config: c, Packaging: packaging.NewLogic(c, b, nil)})
	return r, c
}
func call(r *gin.Engine, path, body, auth string) *httptest.ResponseRecorder {
	req := httptest.NewRequest("POST", path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if auth != "" {
		req.Header.Set("X-Pack-Token", auth)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

const valid = `{"url":"https://game.example.com","dominList":["api.example.com"],"version":"1.0.1","last_version_url":""}`

func TestAuthStrictSchemaAndBodyLimit(t *testing.T) {
	b := &stubBuilder{}
	r, _ := testRouter(t, b)
	if w := call(r, "/packaging", valid, ""); w.Code != 401 {
		t.Fatal(w.Code)
	}
	for _, body := range []string{`{"domain":"legacy"}`, `{"url":"x","url":"y","dominList":[],"version":"1","last_version_url":""}`, `{"url":null,"dominList":[],"version":"1","last_version_url":""}`, valid + `{}`, strings.Replace(valid, `"dominList":["api.example.com"]`, `"dominList":"api"`, 1)} {
		if w := call(r, "/packaging", body, token); w.Code != 400 {
			t.Fatalf("schema accepted: %d %s", w.Code, w.Body.String())
		}
	}
	if w := call(r, "/packaging", `{"url":"`+strings.Repeat("x", 2048), token); w.Code != 413 {
		t.Fatal(w.Code, w.Body.String())
	}
	if b.calls.Load() != 0 {
		t.Fatal("invalid request reached builder")
	}
	if w := call(r, "/packaging", valid, token); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
}
func TestConcurrentRequestsRejectWithoutQueue(t *testing.T) {
	b := &stubBuilder{start: make(chan struct{}), finish: make(chan struct{})}
	r, _ := testRouter(t, b)
	done := make(chan int, 1)
	go func() { done <- call(r, v1BuildPath, valid, token).Code }()
	select {
	case <-b.start:
	case <-time.After(2 * time.Second):
		t.Fatal("first call did not start")
	}
	if w := call(r, "/packaging", valid, token); w.Code != 429 {
		t.Fatal(w.Code)
	}
	close(b.finish)
	if <-done != 200 {
		t.Fatal("first build failed")
	}
	if b.calls.Load() != 1 {
		t.Fatal("concurrent builder was invoked")
	}
}
func TestArtifactAuthAndTraversal(t *testing.T) {
	r, _ := testRouter(t, &stubBuilder{})
	for _, path := range []string{"/artifacts/missing.apk", "/artifacts/..%2Fsecret", "/artifacts/anything.apk"} {
		req := httptest.NewRequest("GET", path, nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code == 200 {
			t.Fatal("artifact leak")
		}
	}
}
func TestBuildRequestRateLimit(t *testing.T) {
	b := &stubBuilder{}
	r, _ := testRouter(t, b)
	if w := call(r, v1BuildPath, valid, token); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	for i := 1; i < 10; i++ {
		if call(r, "/packaging", valid, token).Code != 200 {
			t.Fatal("unexpected limit")
		}
	}
	if call(r, "/packaging", valid, token).Code != 429 {
		t.Fatal("rate limit not applied")
	}
}

func TestVersionedBuildAndArtifact(t *testing.T) {
	b := &stubBuilder{}
	r, cfg := testRouter(t, b)
	for _, tc := range []struct {
		name string
		body string
		auth string
		code int
	}{
		{name: "unauthorized", body: valid, code: response.Unauthorized},
		{name: "invalid", body: `{"version":"1.0.1"}`, auth: token, code: response.ParamErr},
		{name: "success", body: valid, auth: token},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, v1BuildPath, strings.NewReader(tc.body))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("X-Request-ID", "dodo-test")
			if tc.auth != "" {
				req.Header.Set("Authorization", "Bearer "+tc.auth)
			}
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)
			var reply struct {
				Code      int                  `json:"code"`
				Msg       string               `json:"msg"`
				Data      *packaging.BuildResp `json:"data"`
				RequestID string               `json:"request_id"`
				Timestamp int64                `json:"timestamp"`
			}
			if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &reply) != nil ||
				reply.Code != tc.code || reply.Msg == "" || reply.Timestamp <= 0 ||
				reply.RequestID != "dodo-test" || w.Header().Get("X-Request-ID") != reply.RequestID {
				t.Fatalf("unexpected reply: %d %s", w.Code, w.Body.String())
			}
			if tc.code != 0 {
				if reply.Data != nil {
					t.Fatalf("expected null data: %s", w.Body.String())
				}
				return
			}
			wantURL := APIPrefix + "/packaging/artifacts/" + artifactID + ".apk"
			if reply.Data == nil || reply.Data.FileURL != wantURL {
				t.Fatalf("invalid download URL: %s", w.Body.String())
			}
			dir := filepath.Join(cfg.DataDir, "artifacts")
			if err := os.Mkdir(dir, 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, reply.Data.FileName), []byte("test APK bytes"), 0600); err != nil {
				t.Fatal(err)
			}
			download := httptest.NewRequest(http.MethodGet, reply.Data.FileURL, nil)
			w = httptest.NewRecorder()
			r.ServeHTTP(w, download)
			if w.Code != http.StatusUnauthorized {
				t.Fatalf("unauthenticated download returned %d", w.Code)
			}
			download.Header.Set("X-Pack-Token", token)
			w = httptest.NewRecorder()
			r.ServeHTTP(w, download)
			if w.Code != http.StatusOK || w.Body.String() != "test APK bytes" {
				t.Fatalf("download failed: %d %s", w.Code, w.Body.String())
			}
		})
	}
	if b.calls.Load() != 1 {
		t.Fatalf("builder called %d times", b.calls.Load())
	}
}
