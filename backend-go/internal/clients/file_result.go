package clients

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	"insistanan/api-proxy/internal/clients/filetxn"
)

var errClientEdit = errors.New("客户端配置修改无效")

func respondClientFileError(c *gin.Context, err error) {
	status := http.StatusInternalServerError
	body := gin.H{"error": err.Error(), "committed": filetxn.Committed(err)}
	if filetxn.Committed(err) {
		body["error"] = err.Error() + "；已有文件写入，请刷新核对实际状态后再操作"
		body["code"] = "CLIENT_FILES_COMMITTED"
	}
	if !filetxn.Committed(err) && errors.Is(err, filetxn.ErrConflict) {
		status = http.StatusConflict
		body["code"] = "revision_conflict"
	}
	if errors.Is(err, filetxn.ErrRevisionRequired) {
		status = http.StatusPreconditionRequired
		body["code"] = "revision_required"
	}
	if !filetxn.Committed(err) && errors.Is(err, errClientEdit) {
		status = http.StatusBadRequest
	}
	var partial *filetxn.CommitError
	if errors.As(err, &partial) {
		body["completedFiles"] = partial.Files
	}
	c.JSON(status, body)
}

func requireClientRevision(c *gin.Context, expected, current string) bool {
	if expected == "" {
		c.JSON(http.StatusPreconditionRequired, gin.H{"error": "保存需要读取时的 revision，请刷新配置", "code": "revision_required"})
		return false
	}
	if expected != current {
		c.JSON(http.StatusConflict, gin.H{"error": filetxn.ErrConflict.Error(), "code": "revision_conflict"})
		return false
	}
	return true
}
