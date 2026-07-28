package config

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Host            string `yaml:"Host"`
	Port            int    `yaml:"Port"`
	ProjectRoot     string `yaml:"ProjectRoot"`
	DefaultAppName  string `yaml:"DefaultAppName"`
	BuildTimeoutSec int    `yaml:"BuildTimeoutSec"`
	AuthToken       string `yaml:"AuthToken"`
	// KeystorePath 相对 ProjectRoot；Gradle release 与 index.js apksigner 均依赖此文件
	KeystorePath string `yaml:"KeystorePath"`
	// AutoGenerateKeystore 为 true 且文件不存在时，启动时用 keytool 自动生成（新证书无法覆盖安装旧签名 APK）
	AutoGenerateKeystore bool          `yaml:"AutoGenerateKeystore"`
	KeystoreAlias        string        `yaml:"KeystoreAlias"`
	KeystoreStorePass    string        `yaml:"KeystoreStorePass"`
	KeystoreKeyPass      string        `yaml:"KeystoreKeyPass"`
	KeystoreDN           string        `yaml:"KeystoreDN"`
	Storage              StorageConfig `yaml:"Storage"`
}

type StorageConfig struct {
	Endpoint  string `yaml:"Endpoint"`
	AccessKey string `yaml:"AccessKey"`
	SecretKey string `yaml:"SecretKey"`
	UseSSL    bool   `yaml:"UseSSL"`
	Region    string `yaml:"Region"`
	ApkBucket string `yaml:"ApkBucket"`
}

func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}
	if cfg.Host == "" {
		cfg.Host = "0.0.0.0"
	}
	if cfg.Port == 0 {
		cfg.Port = 10010
	}
	if cfg.ProjectRoot == "" {
		cfg.ProjectRoot = "/app"
	}
	if cfg.DefaultAppName == "" {
		cfg.DefaultAppName = "MiningBay"
	}
	if cfg.BuildTimeoutSec <= 0 {
		cfg.BuildTimeoutSec = 3600
	}
	if cfg.Storage.Region == "" {
		cfg.Storage.Region = "us-east-1"
	}
	if cfg.KeystorePath == "" {
		cfg.KeystorePath = "app/henry20230831114241-keystore.jks"
	}
	if cfg.KeystoreAlias == "" {
		cfg.KeystoreAlias = "henry20230831114241"
	}
	if cfg.KeystoreStorePass == "" {
		cfg.KeystoreStorePass = "123456"
	}
	if cfg.KeystoreKeyPass == "" {
		cfg.KeystoreKeyPass = cfg.KeystoreStorePass
	}
	if cfg.KeystoreDN == "" {
		cfg.KeystoreDN = "CN=MiningBay, OU=Mobile, O=MiningBay, L=Unknown, ST=Unknown, C=CN"
	}
	return &cfg, nil
}

func (c *Config) ListenAddr() string {
	return fmt.Sprintf("%s:%d", c.Host, c.Port)
}

func (c *Config) KeystoreAbsPath() string {
	if filepath.IsAbs(c.KeystorePath) {
		return c.KeystorePath
	}
	return filepath.Join(c.ProjectRoot, c.KeystorePath)
}

func (c *Config) KeystoreOK() (absPath string, ok bool) {
	absPath = c.KeystoreAbsPath()
	st, err := os.Stat(absPath)
	if err != nil || st.IsDir() {
		return absPath, false
	}
	return absPath, true
}
