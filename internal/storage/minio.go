package storage

import (
	"context"
	"fmt"
	"io"
	"net/url"
	"strings"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
	"speedapp-packager/internal/config"
)

type Client struct {
	cfg    config.StorageConfig
	client *minio.Client
}

// MinIO 是可选的产物存储；未启用时 APK 保存在 data/artifacts。
func NewClient(c config.StorageConfig) (*Client, error) {
	if !c.Enabled {
		return nil, nil
	}
	endpoint, secure, err := parseEndpoint(c.Endpoint, c.UseSSL)
	if err != nil {
		return nil, err
	}
	if c.AccessKey == "" || c.SecretKey == "" || c.ApkBucket == "" {
		return nil, fmt.Errorf("MinIO credentials and ApkBucket are required")
	}
	public, err := url.Parse(c.PublicBaseURL)
	if err != nil || public.Scheme != "https" || public.Host == "" || public.User != nil || public.RawQuery != "" || public.Fragment != "" {
		return nil, fmt.Errorf("Storage.PublicBaseURL must be a configured HTTPS bucket URL")
	}
	client, err := minio.New(endpoint, &minio.Options{Creds: credentials.NewStaticV4(c.AccessKey, c.SecretKey, ""), Secure: secure, Region: c.Region})
	if err != nil {
		return nil, err
	}
	return &Client{cfg: c, client: client}, nil
}

func (c *Client) Ping(ctx context.Context) error {
	exists, err := c.client.BucketExists(ctx, c.cfg.ApkBucket)
	if err != nil {
		return err
	}
	if !exists {
		return fmt.Errorf("configured MinIO bucket does not exist")
	}
	return nil
}

// 保留旧接口形状以复用存储边界；对象只用随机 job ID 命名，不删除已有产物。
func (c *Client) UploadApk(ctx context.Context, appName, id string, body io.Reader, size int64, userDomain string) (string, string, error) {
	name := id + ".apk"
	_, err := c.client.PutObject(ctx, c.cfg.ApkBucket, name, body, size, minio.PutObjectOptions{ContentType: "application/vnd.android.package-archive"})
	if err != nil {
		return "", "", fmt.Errorf("MinIO upload failed")
	}
	return name, strings.TrimRight(c.cfg.PublicBaseURL, "/") + "/" + name, nil
}
func parseEndpoint(raw string, secure bool) (string, bool, error) {
	if !strings.Contains(raw, "://") {
		scheme := "https://"
		if !secure {
			scheme = "http://"
		}
		raw = scheme + raw
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.User != nil || (u.Scheme != "http" && u.Scheme != "https") || (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" {
		return "", false, fmt.Errorf("invalid MinIO endpoint")
	}
	return u.Host, u.Scheme == "https", nil
}
