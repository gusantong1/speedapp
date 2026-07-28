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

	if abs, ok := h.cfg.KeystoreOK(); !ok {
		c.JSON(http.StatusBadRequest, gin.H{
			"message": "打包环境未就绪",
			"detail":  "缺少签名 keystore: " + abs + "，请挂载到容器内该路径后重启",
		})
		return
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
	abs, ok := h.cfg.KeystoreOK()
	out := gin.H{"status": "ok", "keystore": abs, "keystoreReady": ok}
	if !ok {
		out["status"] = "degraded"
		out["hint"] = "挂载 keystore 到 keystore 字段对应路径，例如 -v /host/xxx.jks:/app/app/henry20230831114241-keystore.jks:ro"
	}
	code := http.StatusOK
	if !ok {
		code = http.StatusServiceUnavailable
	}
	c.JSON(code, out)
}

func Register(r *gin.Engine, h *Handler) {
	r.GET("/health", h.Health)
	r.POST("/packaging", h.Packaging)
}
