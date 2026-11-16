package v1

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"speedapp-packager/app/packager/internal/middleware"
	"speedapp-packager/app/packager/internal/module/v1/packaging"
	"speedapp-packager/app/packager/internal/response"
)

func Register(api *gin.RouterGroup, h *packaging.Handler, token string) {
	const artifacts = "/artifacts/"
	group := api.Group("/packaging")
	artifactPrefix := group.BasePath() + artifacts
	group.POST("/build", middleware.PackToken(token, func(c *gin.Context) {
		response.Fail(c, response.Unauthorized, "unauthorized")
	}), func(c *gin.Context) { h.Build(c, artifactPrefix) })
	group.GET(artifacts+":name", middleware.PackToken(token, func(c *gin.Context) {
		c.Status(http.StatusUnauthorized)
	}), h.Artifact)
}
