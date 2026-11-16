package handler

import (
	"context"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"speedapp-packager/internal/config"
	"speedapp-packager/internal/packager"
)

const token = "test-token-at-least-16"

type stubBuilder struct {
	calls  atomic.Int32
	start  chan struct{}
	finish chan struct{}
}

func (b *stubBuilder) Validate(*packager.Request) error { return nil }
func (b *stubBuilder) Ready() error                     { return nil }
func (b *stubBuilder) Run(context.Context, packager.Request) (packager.Result, error) {
	b.calls.Add(1)
	if b.start != nil {
		close(b.start)
		<-b.finish
	}
	return packager.Result{ID: "unit-test-result"}, nil
}
func router(t *testing.T, b *stubBuilder) (*gin.Engine, *Handler) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	c := &config.Config{AuthToken: token, MaxRequestBytes: 1024, DataDir: t.TempDir()}
	h := New(c, b, nil)
	r := gin.New()
	Register(r, h)
	return r, h
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
	r, _ := router(t, b)
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
	r, _ := router(t, b)
	done := make(chan int, 1)
	go func() { done <- call(r, "/packaging", valid, token).Code }()
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
	r, _ := router(t, &stubBuilder{})
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
	r, _ := router(t, b)
	for i := 0; i < 10; i++ {
		if call(r, "/packaging", valid, token).Code != 200 {
			t.Fatal("unexpected limit")
		}
	}
	if call(r, "/packaging", valid, token).Code != 429 {
		t.Fatal("rate limit not applied")
	}
}
