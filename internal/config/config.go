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
	KeystorePath string        `yaml:"KeystorePath"`
	Storage      StorageConfig `yaml:"Storage"`
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
