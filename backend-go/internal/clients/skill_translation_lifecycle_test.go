package clients

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestSkillTranslation_CancellationPublishesFailuresAndRemainingWork(t *testing.T) {
	root := t.TempDir()
	api := NewSkillsAPI(func() (string, error) { return root, nil })
	for _, name := range []string{"alpha", "beta", "gamma"} {
		path := filepath.Join(root, name)
		require.NoError(t, os.Mkdir(path, 0700))
		require.NoError(t, os.WriteFile(filepath.Join(path, "SKILL.md"), []byte(fmt.Sprintf("---\nname: %s\ndescription: test\n---\n# Instructions\n", name)), 0600))
	}
	_, targets, _, err := api.projectTranslationTargets()
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done, started := make(chan struct{}), make(chan struct{})
	api.translations.task = &skillTranslationTask{ID: "current", Running: true}
	api.translations.cancel, api.translations.done = cancel, done
	go api.runSkillTranslation(ctx, root, targets, skillTranslationRequest{}, func(ctx context.Context, _, _, _, _, _ string) (string, string, error) {
		if api.translations.task.Index == 1 {
			return "", "", errors.New("first failed")
		}
		close(started)
		<-ctx.Done()
		return "", "", ctx.Err()
	})
	<-started
	api.translations.mu.Lock()
	require.Len(t, api.translations.task.Failures, 1, "失败应逐项发布")
	api.translations.mu.Unlock()
	callCancel := func(id string) int {
		response := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(response)
		c.Params = gin.Params{{Key: "taskId", Value: id}}
		api.CancelSkillTranslation(c)
		return response.Code
	}
	require.Equal(t, http.StatusConflict, callCancel("stale"))
	require.NoError(t, ctx.Err())
	require.Equal(t, http.StatusAccepted, callCancel("current"))
	<-done
	require.Equal(t, "cancelled", api.translations.task.Status)
	require.Equal(t, 1, api.translations.task.Remaining)
	require.Len(t, api.translations.task.Failures, 2)
}
