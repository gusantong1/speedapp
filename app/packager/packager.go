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

	"speedapp-packager/app/packager/internal/buildprocess"
	"speedapp-packager/app/packager/internal/config"
	"speedapp-packager/app/packager/internal/router"
	"speedapp-packager/app/packager/internal/svc"

	"github.com/gin-gonic/gin"
)

func main() {
	buildprocess.HandleInvocation()
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	configPath := flag.String("f", "app/packager/etc/packager-etc.yaml", "配置文件路径")
	healthcheck := flag.Bool("healthcheck", false, "check local HTTP liveness")
	flag.Parse()
	c, err := config.Load(*configPath)
	if err != nil {
		return err
	}
	if *healthcheck {
		return checkHealth(c)
	}
	service, err := svc.NewServiceContext(c)
	if err != nil {
		return err
	}
	gin.SetMode(gin.ReleaseMode)
	engine := gin.New()
	engine.Use(gin.Recovery())
	engine.SetTrustedProxies(nil)
	router.Register(engine, service)
	server := &http.Server{
		Addr:              c.ListenAddr(),
		Handler:           engine,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      time.Duration(c.BuildTimeoutSec+60) * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    16 << 10,
	}
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(stop)
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
		return err
	}
	<-stopped
	return nil
}
