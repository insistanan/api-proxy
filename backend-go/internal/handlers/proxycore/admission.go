package proxycore

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/insistanan/api-proxy/internal/upstreamcontrol"
)

func localAdmissionResult(c *gin.Context, err error, allowFailover bool) UpstreamAttemptResult {
	result := newUpstreamAttemptResult(false, "", 0, nil, nil, err).withHealth(channelHealthNeutral)
	if errors.Is(err, upstreamcontrol.ErrBudget) {
		allowFailover = false
		result.AbortFailover = true
	}
	if !allowFailover {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": gin.H{"type": "local_capacity_error", "message": err.Error()}})
		result.Handled = true
	}
	return result
}
