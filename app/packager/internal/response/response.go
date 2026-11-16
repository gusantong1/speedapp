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

type Body struct {
	Code      int    `json:"code"`
	Msg       string `json:"msg"`
	Data      any    `json:"data"`
	RequestID string `json:"request_id"`
	Timestamp int64  `json:"timestamp"`
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
