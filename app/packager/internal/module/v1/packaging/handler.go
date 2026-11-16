package packaging

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"

	"github.com/gin-gonic/gin"

	"speedapp-packager/app/packager/internal/response"
)

// Handler 负责解析请求和写出响应，构建和存储操作由 Logic 处理。
type Handler struct {
	logic           *Logic
	maxRequestBytes int64
}

func NewHandler(logic *Logic, maxRequestBytes int64) *Handler {
	return &Handler{logic: logic, maxRequestBytes: maxRequestBytes}
}

type buildHTTPError struct {
	status  int
	message string
	detail  string
}

// 固定四个字段，拒绝未知字段、重复字段、null 和多个 JSON 文档。
func decodeRequest(r io.Reader) (BuildReq, error) {
	var req BuildReq
	d := json.NewDecoder(r)
	first, err := d.Token()
	if err != nil || first != json.Delim('{') {
		return req, fmt.Errorf("expected a JSON object")
	}
	fields := map[string]json.RawMessage{}
	for d.More() {
		token, err := d.Token()
		if err != nil {
			return req, err
		}
		key, ok := token.(string)
		if !ok {
			return req, fmt.Errorf("invalid field")
		}
		switch key {
		case "url", "dominList", "version", "last_version_url":
		default:
			return req, fmt.Errorf("unknown field: %s", key)
		}
		if _, exists := fields[key]; exists {
			return req, fmt.Errorf("duplicate field: %s", key)
		}
		var value json.RawMessage
		if err = d.Decode(&value); err != nil {
			return req, err
		}
		if string(value) == "null" {
			return req, fmt.Errorf("null field: %s", key)
		}
		fields[key] = value
	}
	if _, err = d.Token(); err != nil {
		return req, err
	}
	var extra any
	if err = d.Decode(&extra); err != io.EOF {
		return req, fmt.Errorf("expected exactly one JSON document")
	}
	if len(fields) != 4 {
		return req, fmt.Errorf("required fields: url, dominList, version, last_version_url")
	}
	b, _ := json.Marshal(fields)
	if err = json.Unmarshal(b, &req); err != nil {
		return req, fmt.Errorf("invalid field types")
	}
	return req, nil
}

func (h *Handler) build(c *gin.Context) (BuildResp, *buildHTTPError) {
	media, _, err := mime.ParseMediaType(c.GetHeader("Content-Type"))
	if err != nil || media != "application/json" {
		return BuildResp{}, &buildHTTPError{status: http.StatusUnsupportedMediaType, message: "Content-Type must be application/json"}
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, h.maxRequestBytes)
	req, err := decodeRequest(c.Request.Body)
	if err != nil {
		var limitErr *http.MaxBytesError
		if errors.As(err, &limitErr) {
			return BuildResp{}, &buildHTTPError{status: http.StatusRequestEntityTooLarge, message: "request body too large"}
		}
		return BuildResp{}, &buildHTTPError{status: http.StatusBadRequest, message: err.Error()}
	}
	result, failure := h.logic.Build(c.Request.Context(), req)
	if failure == nil {
		return result, nil
	}
	switch failure.kind {
	case invalidInput:
		return result, &buildHTTPError{status: http.StatusBadRequest, message: failure.err.Error()}
	case rateLimited:
		c.Header("Retry-After", "60")
		return result, &buildHTTPError{status: http.StatusTooManyRequests, message: "build request rate exceeded"}
	case buildBusy:
		c.Header("Retry-After", "10")
		return result, &buildHTTPError{status: http.StatusTooManyRequests, message: failure.err.Error()}
	case notReady:
		return result, &buildHTTPError{status: http.StatusServiceUnavailable, message: "build environment unavailable", detail: failure.err.Error()}
	case artifactUnavailable:
		return result, &buildHTTPError{status: http.StatusInternalServerError, message: "APK artifact unavailable"}
	case uploadFailed:
		return result, &buildHTTPError{status: http.StatusBadGateway, message: "MinIO upload failed; local APK retained"}
	default:
		return result, &buildHTTPError{status: http.StatusInternalServerError, message: failure.err.Error()}
	}
}

// Build 返回 v1 统一响应，并为本地 APK 指向 v1 下载路由。
func (h *Handler) Build(c *gin.Context, artifactPrefix string) {
	result, failure := h.build(c)
	if result.FileURL == LegacyArtifactPrefix+result.FileName {
		result.FileURL = artifactPrefix + result.FileName
	}
	if failure == nil {
		response.Success(c, result)
		return
	}
	switch failure.status {
	case http.StatusBadRequest, http.StatusRequestEntityTooLarge, http.StatusUnsupportedMediaType:
		response.Fail(c, response.ParamErr, failure.message)
	case http.StatusTooManyRequests:
		response.Fail(c, response.TooManyReq, failure.message)
	case http.StatusServiceUnavailable:
		response.Fail(c, response.ServerBusy, failure.message)
	case http.StatusBadGateway:
		response.Write(c, http.StatusBadGateway, response.ServerErr, failure.message, result)
	default:
		response.Fail(c, response.ServerErr, "APK build failed")
	}
}

// LegacyBuild 为旧客户端返回原有 HTTP 状态码和 JSON 字段。
func (h *Handler) LegacyBuild(c *gin.Context) {
	result, failure := h.build(c)
	if failure == nil {
		c.JSON(http.StatusOK, result)
		return
	}
	body := gin.H{"message": failure.message}
	if failure.detail != "" {
		body["detail"] = failure.detail
	}
	if failure.status == http.StatusBadGateway {
		body["artifact"] = result.FileURL
	}
	c.JSON(failure.status, body)
}

func (h *Handler) Artifact(c *gin.Context) {
	name := c.Param("name")
	path, ok := h.logic.Artifact(name)
	if !ok {
		c.Status(http.StatusNotFound)
		return
	}
	c.FileAttachment(path, name)
}

func (h *Handler) Ready(c *gin.Context) {
	if err := h.logic.Ready(c.Request.Context()); err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"status": "degraded", "reason": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "ready"})
}
