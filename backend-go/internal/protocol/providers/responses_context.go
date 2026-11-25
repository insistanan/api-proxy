package providers

import (
	"github.com/gin-gonic/gin"
	"github.com/insistanan/api-proxy/internal/protocol/converters"
	"github.com/insistanan/api-proxy/internal/protocol/types"
)

const responseToolContextKey = "responses_conversion_tools"
const responseSessionKey = "responses_session_snapshot"

func BindResponseSession(c *gin.Context, snapshot *types.Session) {
	c.Set(responseSessionKey, snapshot)
}
func ResponseSessionForRequest(c *gin.Context) *types.Session {
	value, _ := c.Get(responseSessionKey)
	snapshot, _ := value.(*types.Session)
	return snapshot
}

func ResponseToolsForRequest(c *gin.Context) *converters.ResponseToolContext {
	value, _ := c.Get(responseToolContextKey)
	tools, _ := value.(*converters.ResponseToolContext)
	return tools
}
