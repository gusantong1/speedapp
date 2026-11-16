package middleware

import (
	"crypto/sha256"
	"crypto/subtle"
	"strings"

	"github.com/gin-gonic/gin"
)

// PackToken 使用服务配置的 token 验证发起打包和下载的调用方。
func PackToken(token string, deny gin.HandlerFunc) gin.HandlerFunc {
	want := sha256.Sum256([]byte(token))
	return func(c *gin.Context) {
		supplied := c.GetHeader("X-Pack-Token")
		if bearer, ok := strings.CutPrefix(c.GetHeader("Authorization"), "Bearer "); ok && bearer != "" {
			supplied = bearer
		}
		got := sha256.Sum256([]byte(supplied))
		if len(token) < 16 || subtle.ConstantTimeCompare(want[:], got[:]) != 1 {
			deny(c)
			c.Abort()
			return
		}
		c.Next()
	}
}
