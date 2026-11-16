package router

import (
	_ "embed"
	"net/http"

	"github.com/gin-gonic/gin"

	"speedapp-packager/app/packager/internal/middleware"
	"speedapp-packager/app/packager/internal/module/v1/packaging"
	"speedapp-packager/app/packager/internal/response"
	v1 "speedapp-packager/app/packager/internal/router/v1"
	"speedapp-packager/app/packager/internal/svc"
)

const APIPrefix = "/api/packager/v1"

//go:embed openapi.json
var openAPISpec []byte

func Register(engine *gin.Engine, service *svc.ServiceContext) {
	h := packaging.NewHandler(service.Packaging, service.Config.MaxRequestBytes)
	token := service.Config.AuthToken
	protected := middleware.PackToken(token, func(c *gin.Context) { c.Status(http.StatusUnauthorized) })

	engine.GET("/healthz", health)
	engine.GET("/readyz", protected, h.Ready)
	engine.GET("/health", health)
	engine.GET("/ready", protected, h.Ready)
	engine.GET(packaging.LegacyArtifactPrefix+":name", protected, h.Artifact)
	engine.POST("/packaging", middleware.PackToken(token, func(c *gin.Context) {
		c.JSON(http.StatusUnauthorized, gin.H{"message": "unauthorized"})
	}), h.LegacyBuild)

	api := engine.Group(APIPrefix, response.RequestID)
	api.GET("/doc/openapi.json", func(c *gin.Context) {
		c.Data(http.StatusOK, "application/json", openAPISpec)
	})
	v1.Register(api, h, token)
}

func health(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}
