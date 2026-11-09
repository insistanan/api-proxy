package handlers

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/insistanan/api-proxy/internal/config"
)

func GetChannelPools(cfgManager *config.ConfigManager, kind string) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"pools": cfgManager.GetChannelPools(kind)})
	}
}

func CreateChannelPool(cfgManager *config.ConfigManager, kind string) gin.HandlerFunc {
	return func(c *gin.Context) {
		var pool config.ChannelPool
		if err := c.ShouldBindJSON(&pool); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		created, err := cfgManager.CreateChannelPool(kind, pool)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusCreated, gin.H{"pool": created})
	}
}

func UpdateChannelPool(cfgManager *config.ConfigManager, kind string) gin.HandlerFunc {
	return func(c *gin.Context) {
		id := strings.TrimSpace(c.Param("id"))
		var update config.ChannelPoolUpdate
		if id == "" || c.ShouldBindJSON(&update) != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "无效的分组配置"})
			return
		}
		if err := cfgManager.UpdateChannelPool(kind, id, update); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		c.Status(http.StatusNoContent)
	}
}

func DeleteChannelPool(cfgManager *config.ConfigManager, kind string) gin.HandlerFunc {
	return func(c *gin.Context) {
		if err := cfgManager.DeleteChannelPool(kind, strings.TrimSpace(c.Param("id"))); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		c.Status(http.StatusNoContent)
	}
}

func SaveChannelPoolLayout(cfgManager *config.ConfigManager, kind string) gin.HandlerFunc {
	return func(c *gin.Context) {
		var request struct {
			Pools []config.ChannelPoolLayout `json:"pools"`
		}
		if err := c.ShouldBindJSON(&request); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "无效的分组布局"})
			return
		}
		if err := cfgManager.SaveChannelPoolLayout(kind, request.Pools); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		c.Status(http.StatusNoContent)
	}
}
