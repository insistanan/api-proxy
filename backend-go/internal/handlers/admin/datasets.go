package admin

import (
	"math"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/insistanan/api-proxy/internal/dialogue/semantic"
	"github.com/insistanan/api-proxy/internal/infra/config"
)

// RegisterDatasetAPI 仅挂在已鉴权的管理路由组；不会自动调用远程模型或导入请求日志。
func RegisterDatasetAPI(api *gin.RouterGroup, service *semantic.Service) {
	group := api.Group("/datasets")
	group.Use(func(c *gin.Context) {
		limit := int64(64 << 10)
		if c.Request.Method == http.MethodPost && c.Param("id") == "" || c.Request.Method == http.MethodPut {
			limit = 16 << 20
		}
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, limit)
	})
	group.GET("", func(c *gin.Context) {
		items, err := service.List(c.Request.Context())
		if err != nil {
			c.JSON(500, gin.H{"error": err.Error()})
			return
		}
		c.JSON(200, items)
	})
	group.GET("/builtin", func(c *gin.Context) { c.JSON(200, semantic.BuiltinDatasets()) })
	group.GET("/:id", func(c *gin.Context) {
		value, err := service.Get(c.Param("id"))
		if err != nil {
			c.JSON(404, gin.H{"error": err.Error()})
			return
		}
		c.JSON(200, value)
	})
	save := func(c *gin.Context) {
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 16<<20)
		var value semantic.Dataset
		if err := c.ShouldBindJSON(&value); err != nil {
			c.JSON(400, gin.H{"error": "数据集必须是有效 JSON，大小不超过 16 MiB"})
			return
		}
		if id := c.Param("id"); id != "" && id != value.ID {
			c.JSON(400, gin.H{"error": "路径与数据集 ID 不一致"})
			return
		}
		saved, err := service.Save(c.Request.Context(), value)
		if err != nil {
			c.JSON(400, gin.H{"error": err.Error()})
			return
		}
		c.JSON(200, saved)
	}
	group.POST("", save)
	group.PUT("/:id", save)
	group.DELETE("/:id", func(c *gin.Context) {
		var req struct {
			Revision int64 `json:"revision"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(400, gin.H{"error": "删除参数无效"})
			return
		}
		if err := service.Delete(c.Request.Context(), c.Param("id"), req.Revision); err != nil {
			c.JSON(400, gin.H{"error": err.Error()})
			return
		}
		c.JSON(200, gin.H{"ok": true})
	})
	group.DELETE("/:id/index", func(c *gin.Context) {
		var req semantic.IndexStatus
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(400, gin.H{"error": "索引参数无效"})
			return
		}
		if err := config.ValidateSemanticEmbedding(req.Profile); err != nil {
			c.JSON(400, gin.H{"error": err.Error()})
			return
		}
		if err := service.DeleteIndex(c.Request.Context(), c.Param("id"), req); err != nil {
			c.JSON(400, gin.H{"error": err.Error()})
			return
		}
		c.JSON(200, gin.H{"ok": true})
	})
	group.POST("/:id/build", func(c *gin.Context) {
		var req struct {
			Revision int64                          `json:"revision"`
			Profile  config.SemanticEmbeddingConfig `json:"profile"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(400, gin.H{"error": "索引构建参数无效"})
			return
		}
		result, err := service.Build(c.Request.Context(), c.Param("id"), req.Revision, req.Profile)
		if err != nil {
			c.JSON(400, gin.H{"error": err.Error()})
			return
		}
		c.JSON(200, result)
	})
	group.POST("/:id/search", func(c *gin.Context) {
		var req struct {
			Text    string                         `json:"text"`
			Profile config.SemanticEmbeddingConfig `json:"profile"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(400, gin.H{"error": "检索参数无效"})
			return
		}
		d, err := service.Get(c.Param("id"))
		if err != nil {
			c.JSON(404, gin.H{"error": err.Error()})
			return
		}
		result, err := service.Search(c.Request.Context(), []string{d.ID}, d.Kind, req.Profile, req.Text, "dataset-preview")
		if err != nil {
			c.JSON(400, gin.H{"error": err.Error()})
			return
		}
		c.JSON(200, result)
	})
	group.POST("/:id/evaluate", func(c *gin.Context) {
		var req struct {
			Profile   config.SemanticEmbeddingConfig `json:"profile"`
			Threshold float64                        `json:"threshold"`
			Margin    float64                        `json:"margin"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(400, gin.H{"error": "评测参数无效"})
			return
		}
		for _, n := range []float64{req.Threshold, req.Margin} {
			if math.IsNaN(n) || math.IsInf(n, 0) || n < 0 || n > 1 {
				c.JSON(400, gin.H{"error": "评测阈值和差值须为 0–1"})
				return
			}
		}
		result, err := service.Evaluate(c.Request.Context(), c.Param("id"), req.Profile, req.Threshold, req.Margin)
		if err != nil {
			c.JSON(400, gin.H{"error": err.Error()})
			return
		}
		c.JSON(200, result)
	})
}
