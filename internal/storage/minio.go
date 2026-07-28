package storage

import (
	"context"
	"fmt"
	"io"
	"net/url"
	"strings"

	"speedapp-packager/internal/config"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

type Client struct {
	cfg    config.StorageConfig
	client *minio.Client
}

func NewClient(cfg config.StorageConfig) (*Client, error) {
	endpoint, useSSL, err := parseEndpoint(cfg.Endpoint, cfg.UseSSL)
	if err != nil {
		return nil, err
	}
	if cfg.AccessKey == "" || cfg.SecretKey == "" {
		return nil, fmt.Errorf("MinIO AccessKey / SecretKey 不能为空")
	}
	client, err := minio.New(endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(cfg.AccessKey, cfg.SecretKey, ""),
		Secure: useSSL,
		Region: cfg.Region,
	})
	if err != nil {
		return nil, err
	}
	return &Client{cfg: cfg, client: client}, nil
}

func (c *Client) Ping(ctx context.Context) error {
	bucket := c.cfg.ApkBucket
	if bucket == "" {
		return fmt.Errorf("ApkBucket 未配置")
	}
	_, err := c.client.BucketExists(ctx, bucket)
	if err != nil {
		return fmt.Errorf("无法连接 MinIO（请检查 Endpoint 是否在容器内可达，勿用 127.0.0.1 指宿主机）: %w", err)
	}
	return nil
}

// UploadApk 对齐 Nest：先按关键字删旧包，再上传；object key 含 bucket 前缀
func (c *Client) UploadApk(ctx context.Context, appName, domain string, body io.Reader, size int64, userDomain string) (fileName, fileURL string, err error) {
	bucket := c.cfg.ApkBucket
	if bucket == "" {
		return "", "", fmt.Errorf("ApkBucket 未配置")
	}
	if err := c.ensureBucket(ctx, bucket); err != nil {
		return "", "", err
	}

	delKeyword := fmt.Sprintf("%s/%s-%s", bucket, appName, domain)
	_ = c.deleteByKeyword(ctx, bucket, delKeyword)

	var millis int64
	if ts := ctx.Value(uploadTimeKey{}); ts != nil {
		if v, ok := ts.(int64); ok {
			millis = v
		}
	}
	objectName := fmt.Sprintf("%s/%s-%s-%d.apk", bucket, appName, domain, millis)

	_, err = c.client.PutObject(ctx, bucket, objectName, body, size, minio.PutObjectOptions{
		ContentType: "application/vnd.android.package-archive",
	})
	if err != nil {
		return "", "", fmt.Errorf("upload apk: %w", err)
	}

	fileURL = "https://" + strings.TrimSpace(userDomain) + "/" + objectName
	return objectName, fileURL, nil
}

type uploadTimeKey struct{}

func WithUploadTime(ctx context.Context, millis int64) context.Context {
	return context.WithValue(ctx, uploadTimeKey{}, millis)
}

func (c *Client) ensureBucket(ctx context.Context, bucket string) error {
	exists, err := c.client.BucketExists(ctx, bucket)
	if err != nil {
		return err
	}
	if exists {
		return nil
	}
	return c.client.MakeBucket(ctx, bucket, minio.MakeBucketOptions{Region: c.cfg.Region})
}

func (c *Client) deleteByKeyword(ctx context.Context, bucket, keyword string) error {
	var toDelete []minio.ObjectInfo
	for obj := range c.client.ListObjects(ctx, bucket, minio.ListObjectsOptions{Recursive: true}) {
		if obj.Err != nil {
			return obj.Err
		}
		if keyword != "" && strings.Contains(obj.Key, keyword) {
			toDelete = append(toDelete, obj)
		}
	}
	if len(toDelete) == 0 {
		return nil
	}
	objectsCh := make(chan minio.ObjectInfo, len(toDelete))
	go func() {
		defer close(objectsCh)
		for _, o := range toDelete {
			objectsCh <- o
		}
	}()
	for err := range c.client.RemoveObjects(ctx, bucket, objectsCh, minio.RemoveObjectsOptions{}) {
		if err.Err != nil {
			return err.Err
		}
	}
	return nil
}

func parseEndpoint(raw string, useSSL bool) (endpoint string, secure bool, err error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", false, fmt.Errorf("Endpoint 不能为空")
	}
	secure = useSSL
	if strings.Contains(raw, "://") {
		u, parseErr := url.Parse(raw)
		if parseErr != nil {
			return "", false, parseErr
		}
		if u.Host == "" {
			return "", false, fmt.Errorf("Endpoint 缺少 host")
		}
		switch strings.ToLower(u.Scheme) {
		case "https":
			secure = true
		case "http":
			secure = false
		}
		return u.Host, secure, nil
	}
	return strings.TrimRight(raw, "/"), secure, nil
}
