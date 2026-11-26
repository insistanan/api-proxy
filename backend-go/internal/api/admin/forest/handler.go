package forest

import (
	"errors"
	"log"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	domain "insistanan/api-proxy/internal/management/forest"
)

func Register(parent *gin.RouterGroup, store *domain.Store) {
	r := parent.Group("/forest", func(c *gin.Context) {
		if store == nil {
			c.AbortWithStatusJSON(http.StatusServiceUnavailable, gin.H{"error": "个人森林不可用，请检查网关运行日志"})
			return
		}
		c.Next()
	})
	r.GET("/world", func(c *gin.Context) {
		value, err := store.World(c.Request.Context(), time.Now())
		reply(c, value, err)
	})
	r.POST("/visit", func(c *gin.Context) {
		reply(c, nil, store.Visit(c.Request.Context(), time.Now()))
	})
	r.POST("/trees/:id/recover", func(c *gin.Context) {
		id, ok := parseID(c)
		if !ok {
			return
		}
		var input domain.RecoveryCommand
		if !bind(c, &input) {
			return
		}
		reply(c, nil, store.RecoverTree(c.Request.Context(), id, input, time.Now()))
	})
	r.GET("/events", func(c *gin.Context) {
		after, err := strconv.ParseInt(c.DefaultQuery("after", "0"), 10, 64)
		if err != nil || after < 0 {
			reply(c, nil, domain.ErrInvalid)
			return
		}
		value, err := store.Events(c.Request.Context(), after)
		reply(c, value, err)
	})
	r.GET("/events/:id", func(c *gin.Context) {
		value, err := store.Event(c.Request.Context(), c.Param("id"))
		reply(c, value, err)
	})
	r.GET("/trees/:id", func(c *gin.Context) {
		id, ok := parseID(c)
		if !ok {
			return
		}
		value, err := store.Tree(c.Request.Context(), id)
		reply(c, value, err)
	})
	r.GET("/groups", func(c *gin.Context) {
		value, err := store.Groups(c.Request.Context())
		reply(c, value, err)
	})
	r.GET("/groups/:id", func(c *gin.Context) {
		id, ok := parseID(c)
		if !ok {
			return
		}
		value, err := store.Islands(c.Request.Context(), id)
		reply(c, value, err)
	})
	r.GET("/islands/:id", func(c *gin.Context) {
		id, ok := parseID(c)
		if !ok {
			return
		}
		value, err := store.Island(c.Request.Context(), id)
		reply(c, value, err)
	})
	r.PATCH("/islands/:id", func(c *gin.Context) {
		id, ok := parseID(c)
		if !ok {
			return
		}
		var input domain.IslandEdit
		if !bind(c, &input) {
			return
		}
		reply(c, nil, store.EditIsland(c.Request.Context(), id, input))
	})
	r.PATCH("/trees/:id", func(c *gin.Context) {
		id, ok := parseID(c)
		if !ok {
			return
		}
		var input domain.TreeEdit
		if !bind(c, &input) {
			return
		}
		reply(c, nil, store.EditTree(c.Request.Context(), id, input))
	})
	r.GET("/days/:date", func(c *gin.Context) {
		after, err := strconv.ParseInt(c.DefaultQuery("after", "0"), 10, 64)
		if err != nil || after < 0 {
			reply(c, nil, domain.ErrInvalid)
			return
		}
		value, err := store.Day(c.Request.Context(), c.Param("date"), after)
		reply(c, value, err)
	})
	r.POST("/timer", func(c *gin.Context) {
		var input domain.TimerCommand
		if !bind(c, &input) {
			return
		}
		value, err := store.ChangeTimer(c.Request.Context(), input, time.Now())
		reply(c, value, err)
	})
	r.GET("/timer", func(c *gin.Context) {
		value, err := store.Timer(c.Request.Context(), time.Now())
		reply(c, value, err)
	})
	r.POST("/ornaments/:id", func(c *gin.Context) {
		reply(c, nil, store.Redeem(c.Request.Context(), c.Param("id"), time.Now()))
	})
}

func bind(c *gin.Context, input any) bool {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 4096)
	if err := c.ShouldBindJSON(input); err != nil {
		reply(c, nil, domain.ErrInvalid)
		return false
	}
	return true
}

func parseID(c *gin.Context) (int64, bool) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id < 0 {
		reply(c, nil, domain.ErrInvalid)
		return 0, false
	}
	return id, true
}

func reply(c *gin.Context, value any, err error) {
	switch {
	case err == nil && value == nil:
		c.Status(http.StatusNoContent)
	case err == nil:
		c.JSON(http.StatusOK, value)
	case errors.Is(err, domain.ErrInvalid):
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
	case errors.Is(err, domain.ErrConflict):
		c.JSON(http.StatusConflict, gin.H{"error": domain.ErrConflict.Error()})
	default:
		log.Printf("[Forest-API] 个人森林操作失败: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "个人森林操作失败，请查看网关运行日志"})
	}
}
