package modelfingerprint

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"insistanan/api-proxy/internal/infra/config"
)

func TestJobs_GlobalAdmissionAndSlowPersistenceIsolation(t *testing.T) {
	jobs := newTestJobs(t, t.TempDir())
	jobs.run = func(ctx context.Context, _ *Target, _ func(Event)) { <-ctx.Done() }
	for i := 0; i < maxActiveTasks; i++ {
		_, err := jobs.Start(&Target{ChannelID: fmt.Sprint(i)})
		require.NoError(t, err)
	}
	_, err := jobs.Start(&Target{ChannelID: "overflow"})
	require.ErrorIs(t, err, ErrTaskCapacity)
	first, err := jobs.Latest("0")
	require.NoError(t, err)
	started, release := make(chan struct{}), make(chan struct{})
	write := jobs.writeTask
	jobs.writeTask = func(task Task) error {
		if task.Target.ChannelID == "0" {
			close(started)
			<-release
		}
		return write(task)
	}
	cancelled := make(chan error, 1)
	go func() { _, err := jobs.Cancel("0", first.ID); cancelled <- err }()
	<-started
	queried := make(chan error, 1)
	go func() { _, err := jobs.Latest("1"); queried <- err }()
	select {
	case err := <-queried:
		require.NoError(t, err)
	case <-time.After(time.Second):
		close(release)
		t.Fatal("单任务慢盘阻塞了其他任务查询")
	}
	close(release)
	require.NoError(t, <-cancelled)
}

func TestJobs_RetentionProtectsActiveTask(t *testing.T) {
	jobs := newTestJobs(t, t.TempDir())
	jobs.run = func(ctx context.Context, _ *Target, _ func(Event)) { <-ctx.Done() }
	active, err := jobs.Start(&Target{ChannelID: "active"})
	require.NoError(t, err)
	oldTime := time.Now().Add(-2 * taskRetention)
	require.NoError(t, os.Chtimes(jobs.taskPath(active.Target.ChannelID), oldTime, oldTime))
	old := Task{ID: "old", Target: &Target{ChannelID: "old"}, Status: "completed"}
	require.NoError(t, jobs.writeTaskFile(old))
	require.NoError(t, os.Chtimes(jobs.taskPath("old"), oldTime, oldTime))
	require.NoError(t, jobs.pruneFiles())
	require.FileExists(t, jobs.taskPath("active"))
	require.NoFileExists(t, jobs.taskPath("old"))
}

func TestJobs_RetentionIncludesLegacyFiles(t *testing.T) {
	jobs := newTestJobs(t, t.TempDir())
	legacyPath := filepath.Join(jobs.dir, uuid.NewString()+".json")
	unrelatedPath := filepath.Join(jobs.dir, "notes.json")
	old := time.Now().Add(-2 * taskRetention)
	for _, path := range []string{legacyPath, unrelatedPath} {
		require.NoError(t, os.WriteFile(path, []byte(`{}`), 0600))
		require.NoError(t, os.Chtimes(path, old, old))
	}
	require.NoError(t, jobs.pruneFiles())
	require.NoFileExists(t, legacyPath)
	require.FileExists(t, unrelatedPath)
}

func TestRestoreTarget_RejectsInProcessConfigurationChange(t *testing.T) {
	service, id := newFixtureService(t, config.UpstreamConfig{ServiceType: "openai", BaseURL: "https://example.test", APIKeys: []string{"fixture"}, ProxyMode: "direct"})
	target, err := service.Prepare(id, Request{Model: "test"})
	require.NoError(t, err)
	target.ConfigDigest = "different-configuration"
	_, err = service.restoreTarget(target)
	require.Error(t, err, "内存里的上游快照不能跳过当前配置核验")
}
