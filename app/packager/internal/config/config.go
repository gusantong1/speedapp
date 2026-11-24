package config

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Host            string         `yaml:"Host"`
	Port            int            `yaml:"Port"`
	ProjectRoot     string         `yaml:"ProjectRoot"`
	DataDir         string         `yaml:"DataDir"`
	DefaultAppName  string         `yaml:"DefaultAppName"`
	BuildTimeoutSec int            `yaml:"BuildTimeoutSec"`
	MaxRequestBytes int64          `yaml:"MaxRequestBytes"`
	AuthToken       string         `yaml:"AuthToken"`
	VersionURL      string         `yaml:"VersionURL"`
	VersionPath     string         `yaml:"VersionPath"`
	Download        DownloadConfig `yaml:"Download"`
	Storage         StorageConfig  `yaml:"Storage"`
}

type DownloadConfig struct {
	AllowedOrigins      []string `yaml:"AllowedOrigins" json:"allowedOrigins"`
	AllowedPrivateCIDRs []string `yaml:"AllowedPrivateCIDRs" json:"allowedPrivateCidrs"`
	AllowHTTP           bool     `yaml:"AllowHTTP" json:"allowHttp"`
	TimeoutSec          int      `yaml:"TimeoutSec" json:"timeoutSec"`
	MaxZipBytes         int64    `yaml:"MaxZipBytes" json:"maxZipBytes"`
	MaxExpandedBytes    int64    `yaml:"MaxExpandedBytes" json:"maxExpandedBytes"`
	MaxFileBytes        int64    `yaml:"MaxFileBytes" json:"maxFileBytes"`
	MaxEntries          int      `yaml:"MaxEntries" json:"maxEntries"`
	MaxCompressionRatio int64    `yaml:"MaxCompressionRatio" json:"maxCompressionRatio"`
}

type StorageConfig struct {
	Enabled       bool   `yaml:"Enabled"`
	Endpoint      string `yaml:"Endpoint"`
	AccessKey     string `yaml:"AccessKey"`
	SecretKey     string `yaml:"SecretKey"`
	UseSSL        bool   `yaml:"UseSSL"`
	Region        string `yaml:"Region"`
	ApkBucket     string `yaml:"ApkBucket"`
	PublicBaseURL string `yaml:"PublicBaseURL"`
}

func Load(path string) (*Config, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var c Config
	if err = yaml.Unmarshal(b, &c); err != nil {
		return nil, err
	}
	return complete(&c)
}

func complete(c *Config) (*Config, error) {
	for _, s := range []struct {
		name   string
		target *string
	}{{"PACK_HOST", &c.Host}, {"PACK_PROJECT_ROOT", &c.ProjectRoot}, {"PACK_DATA_DIR", &c.DataDir}} {
		if value, ok := os.LookupEnv(s.name); ok {
			*s.target = value
		}
	}
	// 密钥优先从环境变量/只读 secret 文件获取，禁止默认密码。
	for _, s := range []struct {
		name   string
		target *string
	}{
		{"PACK_AUTH_TOKEN", &c.AuthToken}, {"PACK_STORAGE_ACCESS_KEY", &c.Storage.AccessKey},
		{"PACK_STORAGE_SECRET_KEY", &c.Storage.SecretKey},
	} {
		if value, ok := os.LookupEnv(s.name); ok {
			*s.target = value
		}
		if file := os.Getenv(s.name + "_FILE"); file != "" {
			value, err := os.ReadFile(file)
			if err != nil {
				return nil, fmt.Errorf("read secret %s: %w", s.name, err)
			}
			*s.target = strings.TrimRight(string(value), "\r\n")
		}
	}
	if len(c.AuthToken) < 16 {
		return nil, fmt.Errorf("PACK_AUTH_TOKEN must contain at least 16 bytes")
	}
	if c.Host == "" {
		c.Host = "127.0.0.1"
	}
	if c.Port == 0 {
		c.Port = 10010
	}
	if c.Port < 1 || c.Port > 65535 {
		return nil, fmt.Errorf("invalid Port")
	}
	if c.ProjectRoot == "" {
		c.ProjectRoot = "."
	}
	var err error
	if c.ProjectRoot, err = filepath.Abs(c.ProjectRoot); err != nil {
		return nil, err
	}
	if c.DataDir == "" {
		c.DataDir = "data"
	}
	if !filepath.IsAbs(c.DataDir) {
		c.DataDir = filepath.Join(c.ProjectRoot, c.DataDir)
	}
	if c.DefaultAppName == "" {
		c.DefaultAppName = "MiningBay"
	}
	if len(c.DefaultAppName) > 80 || strings.ContainsAny(c.DefaultAppName, "\r\n\x00") {
		return nil, fmt.Errorf("invalid DefaultAppName")
	}
	if c.BuildTimeoutSec == 0 {
		c.BuildTimeoutSec = 3600
	}
	if c.BuildTimeoutSec < 1 || c.BuildTimeoutSec > 7200 {
		return nil, fmt.Errorf("invalid BuildTimeoutSec")
	}
	if c.MaxRequestBytes == 0 {
		c.MaxRequestBytes = 16384
	}
	if c.MaxRequestBytes < 256 || c.MaxRequestBytes > 65536 {
		return nil, fmt.Errorf("invalid MaxRequestBytes")
	}
	if c.VersionPath == "" {
		c.VersionPath = "/app/version"
	}
	if !strings.HasPrefix(c.VersionPath, "/") || strings.HasPrefix(c.VersionPath, "//") || strings.ContainsAny(c.VersionPath, "?#\\\r\n") {
		return nil, fmt.Errorf("VersionPath must be an absolute URL path")
	}
	d := &c.Download
	if d.TimeoutSec == 0 {
		d.TimeoutSec = 120
	}
	if d.MaxZipBytes == 0 {
		d.MaxZipBytes = 128 << 20
	}
	if d.MaxExpandedBytes == 0 {
		d.MaxExpandedBytes = 512 << 20
	}
	if d.MaxFileBytes == 0 {
		d.MaxFileBytes = 64 << 20
	}
	if d.MaxEntries == 0 {
		d.MaxEntries = 20000
	}
	if d.MaxCompressionRatio == 0 {
		d.MaxCompressionRatio = 200
	}
	if d.TimeoutSec < 1 || d.TimeoutSec > 600 || d.MaxZipBytes < 1 || d.MaxZipBytes > 512<<20 || d.MaxExpandedBytes < 1 || d.MaxExpandedBytes > 2<<30 || d.MaxFileBytes < 1 || d.MaxFileBytes > d.MaxExpandedBytes || d.MaxEntries < 1 || d.MaxEntries > 50000 || d.MaxCompressionRatio < 1 || d.MaxCompressionRatio > 1000 {
		return nil, fmt.Errorf("invalid Download limits")
	}
	if c.Storage.Region == "" {
		c.Storage.Region = "us-east-1"
	}
	return c, nil
}

func (c *Config) ListenAddr() string { return net.JoinHostPort(c.Host, strconv.Itoa(c.Port)) }
