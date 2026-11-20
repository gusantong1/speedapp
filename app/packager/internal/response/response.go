package response

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// v1 JSON 接口使用与 admin 相同的通用业务码。
const (
	ParamErr     = 1001
	Unauthorized = 1002
	TooManyReq   = 1005
	ServerErr    = 1006
	ServerBusy   = 1007
)

const requestIDHeader = "X-Request-ID"

// Body 是 v1 打包接口的统一 JSON 响应。
type Body struct {
	Code      int    `json:"code"`       // 业务状态码，0 表示成功。
	Msg       string `json:"msg"`        // 业务提示文案。
	Data      any    `json:"data"`       // 业务数据，可包含 APK 产物信息，空值编码为 JSON null。
	RequestID string `json:"request_id"` // 请求标识，与响应头 X-Request-ID 一致。
	Timestamp int64  `json:"timestamp"`  // 服务端响应时间，Unix 毫秒时间戳。
}

func RequestID(c *gin.Context) {
	id := c.GetHeader(requestIDHeader)
	if id == "" {
		id = uuid.NewString()
	}
	c.Header(requestIDHeader, id)
	c.Next()
}

func Success(c *gin.Context, data any) {
	Write(c, http.StatusOK, 0, "success", data)
}

func Fail(c *gin.Context, code int, msg string) {
	status := http.StatusOK
	switch code {
	case ServerErr:
		status = http.StatusInternalServerError
	case ServerBusy:
		status = http.StatusServiceUnavailable
	}
	Write(c, status, code, msg, nil)
}

func Write(c *gin.Context, status, code int, msg string, data any) {
	c.JSON(status, Body{
		Code:      code,
		Msg:       msg,
		Data:      data,
		RequestID: c.Writer.Header().Get(requestIDHeader),
		Timestamp: time.Now().UnixMilli(),
	})
}
