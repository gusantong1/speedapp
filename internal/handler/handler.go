package handler

import (
	"context"
	"log"
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
			"detail":  "缺少签名 keystore: " + abs + "，请配置 AutoGenerateKeystore 或挂载 /app/secrets",
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

	uploadCtx := storage.WithUploadTime(context.Background(), time.Now().UnixMilli())
	fileName, fileURL, err := h.store.UploadApk(uploadCtx, appName, req.Domain, f, stat.Size(), req.Domain)
	if err != nil {
		log.Printf("[packager] minio upload failed: %v", err)
		c.JSON(http.StatusBadRequest, gin.H{"message": "上传失败", "detail": err.Error()})
		return
	}

	c.JSON(http.StatusOK, PackagingResp{FileName: fileName, FileURL: fileURL})
}

func (h *Handler) Health(c *gin.Context) {
	abs, ok := h.cfg.KeystoreOK()
	out := gin.H{
		"status":        "ok",
		"keystore":      abs,
		"keystoreReady": ok,
		"minioEndpoint": h.cfg.Storage.Endpoint,
	}
	if err := h.store.Ping(c.Request.Context()); err != nil {
		out["minioReady"] = false
		out["minioError"] = err.Error()
		out["status"] = "degraded"
	} else {
		out["minioReady"] = true
	}
	if !ok {
		out["status"] = "degraded"
		out["hint"] = "挂载 secrets 目录持久化证书，例如 -v /host/secrets:/app/secrets — 切勿挂载到 /app/app，会覆盖 Android 工程"
	}
	if out["minioReady"] == false {
		out["minioHint"] = "packager 与 minio 在不同容器时，Endpoint 不能用 127.0.0.1，请用宿主机 IP:9000 或 Docker 网络内服务名 minio_container:9000"
	}
	code := http.StatusOK
	if out["status"] == "degraded" {
		code = http.StatusServiceUnavailable
	}
	c.JSON(code, out)
}

func Register(r *gin.Engine, h *Handler) {
	r.GET("/health", h.Health)
	r.POST("/packaging", h.Packaging)
}
