// Package common 放置 HTTP 接入层共用的小型契约与工具。
// 只收录被两个及以上子包使用的符号，避免演变成新的杂物间。
package common

import (
	"errors"
	"fmt"
	"io"
	"net/http"

	"github.com/gin-gonic/gin"
	"insistanan/api-proxy/internal/infra/config"
)

// WriteSaveError 保留配置已提交但收尾失败的状态，避免调用方误按未提交重试。
func WriteSaveError(c *gin.Context, message string, err error) {
	status := http.StatusInternalServerError
	body := gin.H{"error": message, "detail": err.Error()}
	if config.IsCommittedError(err) {
		body["code"] = "CONFIG_COMMITTED"
		body["committed"] = true
		body["error"] = message + "（已写入，请刷新查看实际状态）"
	} else if errors.Is(err, config.ErrConfigConflict) || errors.Is(err, config.ErrChannelIdentityConflict) {
		status = http.StatusConflict
		body["code"] = "CONFIG_CONFLICT"
	} else if errors.Is(err, config.ErrChannelNotFound) {
		status = http.StatusNotFound
	} else if config.IsConfigError(err) {
		status = http.StatusBadRequest
		body = gin.H{"error": err.Error()}
	}
	c.JSON(status, body)
}

// ReadLimitedBody 读取至多 maxBytes 字节的响应体，超限时返回错误而不是截断。
func ReadLimitedBody(reader io.Reader, maxBytes int64) ([]byte, error) {
	if maxBytes <= 0 {
		return nil, errors.New("响应大小限制无效")
	}
	body, err := io.ReadAll(io.LimitReader(reader, maxBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(body)) > maxBytes {
		return nil, fmt.Errorf("响应超过 %d MB", maxBytes/(1<<20))
	}
	return body, nil
}
