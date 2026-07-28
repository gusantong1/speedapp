package main

import (
	"flag"
	"log"

	"speedapp-packager/internal/config"
	"speedapp-packager/internal/handler"
	"speedapp-packager/internal/packager"
	"speedapp-packager/internal/storage"

	"github.com/gin-gonic/gin"
)

func main() {
	configPath := flag.String("f", "etc/packager.yaml", "配置文件路径")
	flag.Parse()

	cfg, err := config.Load(*configPath)
	if err != nil {
		log.Fatalf("load config: %v", err)
	}

	store, err := storage.NewClient(cfg.Storage)
	if err != nil {
		log.Fatalf("storage: %v", err)
	}

	runner := packager.NewRunner(cfg.ProjectRoot, cfg.BuildTimeoutSec)
	h := handler.New(cfg, runner, store)

	if abs, ok := cfg.KeystoreOK(); ok {
		log.Printf("keystore ready: %s", abs)
	} else {
		log.Printf("WARNING: keystore missing at %s — Gradle release 会失败，请挂载 .jks 后重启容器", abs)
	}

	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.Use(gin.Recovery(), gin.Logger())
	handler.Register(r, h)

	log.Printf("speedapp packager listening on %s", cfg.ListenAddr())
	if err := r.Run(cfg.ListenAddr()); err != nil {
		log.Fatal(err)
	}
}
