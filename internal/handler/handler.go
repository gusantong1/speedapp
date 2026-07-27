package handler

import (
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"speedapp-packager/internal/config"
	"speedapp-packager/internal/packager"
	"speedapp-packager/internal/storage"

	"github.com/gin-gonic/gin"
)

type PackagingReq struct {
	Domain  string `json:"domain" binding:"required"`
	AppName string `json:"appName"`
}

type PackagingResp struct {
	FileName string `json:"fileName"`
	FileURL  string `json:"fileUrl"`
}

type Handler struct {
	cfg    *config.Config
	runner *packager.Runner
	store  *storage.Client
	mu     sync.Mutex
}

func New(cfg *config.Config, runner *packager.Runner, store *storage.Client) *Handler {
	return &Handler{cfg: cfg, runner: runner, store: store}
}

func (h *Handler) auth(c *gin.Context) bool {
	if h.cfg.AuthToken == "" {
		return true
	}
	return c.GetHeader("X-Pack-Token") == h.cfg.AuthToken
}

// Packaging POST /packaging — 同步执行打包并上传 MinIO（对齐 Nest domainManager.packaging）
func (h *Handler) Packaging(c *gin.Context) {
	if !h.auth(c) {
		c.JSON(http.StatusUnauthorized, gin.H{"message": "未授权"})
		return
	}

	var req PackagingReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": "参数错误"})
		return
	}

	appName := req.AppName
	if appName == "" {
		appName = h.cfg.DefaultAppName
	}
	webviewURL := req.Domain
	if !strings.HasPrefix(webviewURL, "http://") && !strings.HasPrefix(webviewURL, "https://") {
		webviewURL = "https://" + req.Domain
	}

	h.mu.Lock()
	defer h.mu.Unlock()

	ctx := c.Request.Context()
	if err := h.runner.Run(ctx, appName, webviewURL); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": "打包异常", "detail": err.Error()})
		return
	}

	apkPath := h.runner.ApkPath(appName)
	f, err := os.Open(apkPath)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": "打包异常", "detail": "未找到输出 APK"})
		return
	}
	defer f.Close()

	stat, err := f.Stat()
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": "打包异常"})
		return
	}

	uploadCtx := storage.WithUploadTime(ctx, time.Now().UnixMilli())
	fileName, fileURL, err := h.store.UploadApk(uploadCtx, appName, req.Domain, f, stat.Size(), req.Domain)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": "上传失败", "detail": err.Error()})
		return
	}

	c.JSON(http.StatusOK, PackagingResp{FileName: fileName, FileURL: fileURL})
}

func (h *Handler) Health(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

func Register(r *gin.Engine, h *Handler) {
	r.GET("/health", h.Health)
	r.POST("/packaging", h.Packaging)
}
