package handler

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sync"
	"time"

	"context"
	"speedapp-packager/internal/config"
	"speedapp-packager/internal/packager"
	"speedapp-packager/internal/storage"

	"github.com/gin-gonic/gin"
)

type Builder interface {
	Validate(*packager.Request) error
	Ready() error
	Run(context.Context, packager.Request) (packager.Result, error)
}

type Handler struct {
	cfg    *config.Config
	runner Builder
	store  *storage.Client
	slot   chan struct{}
	mu     sync.Mutex
	window time.Time
	count  int
}

func New(cfg *config.Config, runner Builder, store *storage.Client) *Handler {
	return &Handler{cfg: cfg, runner: runner, store: store, slot: make(chan struct{}, 1)}
}

func (h *Handler) auth(c *gin.Context) bool {
	supplied := c.GetHeader("X-Pack-Token")
	if v := c.GetHeader("Authorization"); len(v) > 7 && v[:7] == "Bearer " {
		supplied = v[7:]
	}
	want, got := sha256.Sum256([]byte(h.cfg.AuthToken)), sha256.Sum256([]byte(supplied))
	return len(h.cfg.AuthToken) >= 16 && subtle.ConstantTimeCompare(want[:], got[:]) == 1
}

// 固定四个字段，拒绝未知字段、重复字段、null 和多个 JSON 文档。
func decodeRequest(r io.Reader) (packager.Request, error) {
	var req packager.Request
	d := json.NewDecoder(r)
	first, err := d.Token()
	if err != nil || first != json.Delim('{') {
		return req, fmt.Errorf("expected a JSON object")
	}
	fields := map[string]json.RawMessage{}
	for d.More() {
		token, err := d.Token()
		if err != nil {
			return req, err
		}
		key, ok := token.(string)
		if !ok {
			return req, fmt.Errorf("invalid field")
		}
		switch key {
		case "url", "dominList", "version", "last_version_url":
		default:
			return req, fmt.Errorf("unknown field: %s", key)
		}
		if _, exists := fields[key]; exists {
			return req, fmt.Errorf("duplicate field: %s", key)
		}
		var value json.RawMessage
		if err = d.Decode(&value); err != nil {
			return req, err
		}
		if string(value) == "null" {
			return req, fmt.Errorf("null field: %s", key)
		}
		fields[key] = value
	}
	if _, err = d.Token(); err != nil {
		return req, err
	}
	var extra any
	if err = d.Decode(&extra); err != io.EOF {
		return req, fmt.Errorf("expected exactly one JSON document")
	}
	if len(fields) != 4 {
		return req, fmt.Errorf("required fields: url, dominList, version, last_version_url")
	}
	b, _ := json.Marshal(fields)
	if err = json.Unmarshal(b, &req); err != nil {
		return req, fmt.Errorf("invalid field types")
	}
	return req, nil
}

func (h *Handler) Packaging(c *gin.Context) {
	if !h.auth(c) {
		c.JSON(401, gin.H{"message": "unauthorized"})
		return
	}
	media, _, err := mime.ParseMediaType(c.GetHeader("Content-Type"))
	if err != nil || media != "application/json" {
		c.JSON(415, gin.H{"message": "Content-Type must be application/json"})
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, h.cfg.MaxRequestBytes)
	req, err := decodeRequest(c.Request.Body)
	if err != nil {
		var limitErr *http.MaxBytesError
		if errors.As(err, &limitErr) {
			c.JSON(413, gin.H{"message": "request body too large"})
			return
		}
		c.JSON(400, gin.H{"message": err.Error()})
		return
	}
	if err = h.runner.Validate(&req); err != nil {
		c.JSON(400, gin.H{"message": err.Error()})
		return
	}
	h.mu.Lock()
	if time.Since(h.window) >= time.Minute {
		h.window = time.Now()
		h.count = 0
	}
	limited := h.count >= 10
	if !limited {
		h.count++
	}
	h.mu.Unlock()
	if limited {
		c.Header("Retry-After", "60")
		c.JSON(429, gin.H{"message": "build request rate exceeded"})
		return
	}
	select {
	case h.slot <- struct{}{}:
		defer func() { <-h.slot }()
	default:
		c.Header("Retry-After", "10")
		c.JSON(429, gin.H{"message": "a build is already running"})
		return
	}
	if err = h.runner.Ready(); err != nil {
		c.JSON(503, gin.H{"message": "build environment unavailable", "detail": err.Error()})
		return
	}
	result, err := h.runner.Run(c.Request.Context(), req)
	if errors.Is(err, packager.ErrBusy) {
		c.Header("Retry-After", "10")
		c.JSON(429, gin.H{"message": err.Error()})
		return
	}
	if err != nil {
		c.JSON(500, gin.H{"message": err.Error()})
		return
	}
	if h.store != nil {
		f, err := os.Open(filepath.Join(h.cfg.DataDir, "artifacts", result.FileName))
		if err != nil {
			c.JSON(500, gin.H{"message": "APK artifact unavailable"})
			return
		}
		defer f.Close()
		stat, err := f.Stat()
		if err != nil {
			c.JSON(500, gin.H{"message": "APK artifact unavailable"})
			return
		}
		_, remote, err := h.store.UploadApk(c.Request.Context(), h.cfg.DefaultAppName, result.ID, f, stat.Size(), "")
		if err != nil {
			c.JSON(502, gin.H{"message": "MinIO upload failed; local APK retained", "artifact": result.FileURL})
			return
		}
		result.FileURL = remote
	}
	c.JSON(200, result)
}

var artifactName = regexp.MustCompile(`^[0-9a-f]{32}\.apk$`)

func (h *Handler) Artifact(c *gin.Context) {
	if !h.auth(c) {
		c.Status(401)
		return
	}
	name := c.Param("name")
	if !artifactName.MatchString(name) {
		c.Status(404)
		return
	}
	path := filepath.Join(h.cfg.DataDir, "artifacts", name)
	st, err := os.Lstat(path)
	if err != nil || !st.Mode().IsRegular() {
		c.Status(404)
		return
	}
	c.FileAttachment(path, name)
}

func (h *Handler) Health(c *gin.Context) { c.JSON(200, gin.H{"status": "ok"}) }
func (h *Handler) Ready(c *gin.Context) {
	if !h.auth(c) {
		c.Status(401)
		return
	}
	if err := h.runner.Ready(); err != nil {
		c.JSON(503, gin.H{"status": "degraded", "reason": err.Error()})
		return
	}
	if h.store != nil {
		ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
		defer cancel()
		if err := h.store.Ping(ctx); err != nil {
			c.JSON(503, gin.H{"status": "degraded", "reason": "MinIO unavailable"})
			return
		}
	}
	c.JSON(200, gin.H{"status": "ready"})
}

func Register(r *gin.Engine, h *Handler) {
	r.GET("/health", h.Health)
	r.GET("/ready", h.Ready)
	r.POST("/packaging", h.Packaging)
	r.GET("/artifacts/:name", h.Artifact)
}
