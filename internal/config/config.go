package config

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Host            string        `yaml:"Host"`
	Port            int           `yaml:"Port"`
	ProjectRoot     string        `yaml:"ProjectRoot"`
	DefaultAppName  string        `yaml:"DefaultAppName"`
	BuildTimeoutSec int           `yaml:"BuildTimeoutSec"`
	AuthToken       string        `yaml:"AuthToken"`
	Storage         StorageConfig `yaml:"Storage"`
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
	return &cfg, nil
}

func (c *Config) ListenAddr() string {
	return fmt.Sprintf("%s:%d", c.Host, c.Port)
}
