package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"speedapp-packager/internal/buildprocess"
	"speedapp-packager/internal/config"
	"speedapp-packager/internal/handler"
	"speedapp-packager/internal/keystore"
	"speedapp-packager/internal/packager"
	"speedapp-packager/internal/storage"

	"github.com/gin-gonic/gin"
)

func main() {
	buildprocess.HandleInvocation()
	configPath := flag.String("f", "etc/packager.yaml", "配置文件路径")
	healthcheck := flag.Bool("healthcheck", false, "check local HTTP liveness")
	flag.Parse()
	c, err := config.Load(*configPath)
	if err != nil {
		log.Fatal(err)
	}
	if *healthcheck {
		if checkHealth(c) != nil {
			os.Exit(1)
		}
		return
	}
	if err = keystore.Ensure(c); err != nil {
		log.Fatal(err)
	}
	runner, err := packager.NewRunner(c)
	if err != nil {
		log.Fatal(err)
	}
	store, err := storage.NewClient(c.Storage)
	if err != nil {
		log.Fatal(err)
	}
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.Use(gin.Recovery())
	r.SetTrustedProxies(nil)
	handler.Register(r, handler.New(c, runner, store))
	server := &http.Server{Addr: c.ListenAddr(), Handler: r, ReadHeaderTimeout: 10 * time.Second, ReadTimeout: 30 * time.Second, WriteTimeout: time.Duration(c.BuildTimeoutSec+60) * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16 << 10}
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		<-stop
		// 已开始的构建有独立工作进程；服务意外退出时控制管道关闭会取消构建。
		ctx, cancel := context.WithTimeout(context.Background(), time.Duration(c.BuildTimeoutSec+10)*time.Second)
		defer cancel()
		server.Shutdown(ctx)
	}()
	fmt.Printf("packapp listening on %s\n", c.ListenAddr())
	if err = server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatal(err)
	}
	<-stopped
}
