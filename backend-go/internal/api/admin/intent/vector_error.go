package intent

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/insistanan/api-proxy/internal/dialogue/routing/semantic"
)

func respondVectorRequired(c *gin.Context, err error) bool {
	var required *semantic.VectorModelRequiredError
	if !errors.As(err, &required) {
		return false
	}
	c.JSON(http.StatusConflict, gin.H{"code": "VECTOR_MODEL_REQUIRED", "message": required.Message, "installPath": required.InstallPath, "presetsUrl": required.PresetsURL})
	return true
}
