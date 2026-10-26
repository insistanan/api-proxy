package proxycore

import (
	"context"
	"net/http"

	"github.com/BenedictKing/api-proxy/internal/logger"
	"github.com/BenedictKing/api-proxy/internal/utils"
	"github.com/gin-gonic/gin"
)

func BindRequestLogID(c *gin.Context) string {
	if c == nil {
		return logger.NewRequestID()
	}
	if existing, ok := c.Get(utils.ContextKeyRequestLogID); ok {
		if requestID, ok := existing.(string); ok && requestID != "" {
			if c.Request != nil && logger.RequestIDFromContext(c.Request.Context()) == "" {
				c.Request = c.Request.WithContext(logger.ContextWithRequestID(c.Request.Context(), requestID))
			}
			return requestID
		}
	}
	requestID := logger.NewRequestID()
	c.Set(utils.ContextKeyRequestLogID, requestID)
	if c.Request != nil {
		c.Request = c.Request.WithContext(logger.ContextWithRequestID(c.Request.Context(), requestID))
	}
	return requestID
}

func AttachRequestLogID(c *gin.Context, req *http.Request) *http.Request {
	if req == nil {
		return req
	}
	requestID := RequestLogID(c)
	if requestID == "" {
		return req
	}
	return req.WithContext(logger.ContextWithRequestID(req.Context(), requestID))
}

func RequestLogID(c *gin.Context) string {
	if c == nil {
		return ""
	}
	if existing, ok := c.Get(utils.ContextKeyRequestLogID); ok {
		if requestID, ok := existing.(string); ok && requestID != "" {
			return requestID
		}
	}
	if c.Request != nil {
		return logger.RequestIDFromContext(c.Request.Context())
	}
	return ""
}

type detachedCancellationContext struct {
	context.Context
	parent context.Context
}

func (detached detachedCancellationContext) Value(key any) any {
	if detached.parent == nil {
		return nil
	}
	return detached.parent.Value(key)
}

// DetachClientCancellation 保留父 context 的请求值，但 Done 不再跟随客户端取消。
// 图片已经完整写给浏览器后，上游 response.completed 仍可能携带 usage；
// 此时不能因为浏览器关闭 SSE 而中止已经开始的上游读取。
func DetachClientCancellation(parent context.Context) context.Context {
	if parent == nil {
		parent = context.Background()
	}
	return detachedCancellationContext{Context: context.Background(), parent: parent}
}
