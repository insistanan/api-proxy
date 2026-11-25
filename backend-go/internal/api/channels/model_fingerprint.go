package channels

import (
	"errors"
	"net/http"
	"path/filepath"
	"strconv"

	"github.com/gin-gonic/gin"
	"insistanan/api-proxy/internal/infra/config"
	"insistanan/api-proxy/internal/management/modelfingerprint"
)

// RegisterModelFingerprintRoutes 注册后台检测及查询、停止接口，返回进程停机清理函数。
func RegisterModelFingerprintRoutes(group *gin.RouterGroup, cfg *config.ConfigManager) func() error {
	service := modelfingerprint.New(cfg)
	jobs := modelfingerprint.NewJobs(service, filepath.Join(modelfingerprint.DataDirectory, "tasks"))
	group.POST("/channels/:id/fingerprint", func(c *gin.Context) {
		var input modelfingerprint.Request
		if err := c.ShouldBindJSON(&input); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "请提供有效的模型和采样目标"})
			return
		}
		target, err := service.Prepare(c.Param("id"), input)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		task, err := jobs.Start(target)
		if err != nil {
			status := http.StatusInternalServerError
			if errors.Is(err, modelfingerprint.ErrTaskRunning) {
				status = http.StatusConflict
			}
			c.JSON(status, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusAccepted, task)
	})
	group.GET("/channels/:id/fingerprint", func(c *gin.Context) {
		c.Header("Cache-Control", "no-store")
		task, err := jobs.Latest(c.Param("id"))
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, task)
	})
	group.POST("/channels/:id/fingerprint/:taskId/retry", func(c *gin.Context) {
		task, err := jobs.RetryFailed(c.Param("id"), c.Param("taskId"))
		if err != nil {
			c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusAccepted, task)
	})
	group.POST("/channels/:id/fingerprint/:taskId/samples/:sampleIndex/retry", func(c *gin.Context) {
		index, err := strconv.Atoi(c.Param("sampleIndex"))
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "样本序号无效"})
			return
		}
		task, err := jobs.Retry(c.Param("id"), c.Param("taskId"), index)
		if err != nil {
			c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusAccepted, task)
	})
	group.DELETE("/channels/:id/fingerprint/:taskId", func(c *gin.Context) {
		task, err := jobs.Cancel(c.Param("id"), c.Param("taskId"))
		if err != nil {
			c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, task)
	})
	return jobs.Close
}
