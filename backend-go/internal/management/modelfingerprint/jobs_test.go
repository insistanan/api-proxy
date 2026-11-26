package modelfingerprint

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	lmfpd "github.com/Ikaleio/lm-detector/sdk/go"
	"github.com/stretchr/testify/require"
	"insistanan/api-proxy/internal/infra/config"
)

func TestBackgroundTaskPersistsResultWithoutCredentials(t *testing.T) {
	dir := t.TempDir()
	jobs := newTestJobs(t, dir)
	t.Cleanup(func() { require.NoError(t, jobs.Close()) })
	started, release := make(chan struct{}), make(chan struct{})
	jobs.run = func(ctx context.Context, _ *Target, emit func(Event)) {
		emit(Event{Type: "ready"})
		emit(Event{Type: "sample", Sample: &Sample{Index: 0, State: "waiting"}})
		close(started)
		select {
		case <-ctx.Done():
			return
		case <-release:
		}
		emit(Event{Type: "sample", Sample: &Sample{Index: 0, State: "complete", Text: "247,83,319"}})
		emit(Event{Type: "result", Analysis: &lmfpd.Analysis{Decision: "partial", Results: []lmfpd.AnalysisResult{}}})
	}
	target := &Target{ChannelID: "ch_d37fdd4be92844109dba796ba0613e72", Model: "selected", apiKey: "secret-fixture",
		upstream: &config.UpstreamConfig{APIKeys: []string{"secret-fixture"}}}
	created, err := jobs.Start(target)
	require.NoError(t, err)
	<-started
	latest, err := jobs.Latest(target.ChannelID)
	require.NoError(t, err)
	require.Equal(t, created.ID, latest.ID)
	require.Equal(t, "running", latest.Status)
	latest.Samples[0].State = "failed"
	latest, err = jobs.Latest(target.ChannelID)
	require.NoError(t, err)
	require.Equal(t, "waiting", latest.Samples[0].State, "读取快照不能修改后台任务")
	_, err = jobs.Start(target)
	require.ErrorIs(t, err, ErrTaskRunning)
	close(release)
	jobs.workers.Wait()
	reopened := newTestJobs(t, dir)
	saved, err := reopened.Latest(target.ChannelID)
	require.NoError(t, err)
	require.Equal(t, "completed", saved.Status)
	require.Equal(t, "247,83,319", saved.Samples[0].Text)
	data, err := os.ReadFile(jobs.taskPath(target.ChannelID))
	require.NoError(t, err)
	require.NotContains(t, string(data), "secret-fixture")
}

func TestExplicitCancellationPersistsAndReleasesWorker(t *testing.T) {
	jobs := newTestJobs(t, t.TempDir())
	t.Cleanup(func() { require.NoError(t, jobs.Close()) })
	started := make(chan struct{})
	jobs.run = func(ctx context.Context, _ *Target, emit func(Event)) {
		emit(Event{Type: "sample", Sample: &Sample{Index: 0, State: "waiting"}})
		close(started)
		<-ctx.Done()
	}
	target := &Target{ChannelID: "d37fdd4b-e928-4410-9dba-796ba0613e72"}
	task, err := jobs.Start(target)
	require.NoError(t, err)
	<-started
	_, err = jobs.Cancel(target.ChannelID, "other-task")
	require.Error(t, err)
	cancelled, err := jobs.Cancel(target.ChannelID, task.ID)
	require.NoError(t, err)
	require.Equal(t, "cancelled", cancelled.Status)
	require.Equal(t, "cancelled", cancelled.Samples[0].State)
	require.NoError(t, jobs.Close())
	saved, err := newTestJobs(t, jobs.dir).Latest(target.ChannelID)
	require.NoError(t, err)
	require.Equal(t, "cancelled", saved.Status)
}

func TestRestartDoesNotResubmitUnfinishedTask(t *testing.T) {
	jobs := newTestJobs(t, t.TempDir())
	target := &Target{ChannelID: "d37fdd4b-e928-4410-9dba-796ba0613e72"}
	require.NoError(t, jobs.writeTaskFile(Task{ID: "prior-task", Status: "running", Target: target,
		Samples: []Sample{{Index: 0, State: "complete", Text: "saved-answer"}, {Index: 1, State: "waiting"}}}))
	jobs.run = func(context.Context, *Target, func(Event)) { t.Error("查看旧任务不能重发请求") }
	saved, err := jobs.Latest(target.ChannelID)
	require.NoError(t, err)
	require.Equal(t, "interrupted", saved.Status)
	require.Equal(t, "saved-answer", saved.Samples[0].Text)
	require.Equal(t, "cancelled", saved.Samples[1].State)
}

func TestTaskSaveFailurePreventsSampling(t *testing.T) {
	path := filepath.Join(t.TempDir(), "not-a-directory")
	require.NoError(t, os.WriteFile(path, []byte("blocked"), 0600))
	jobs := newTestJobs(t, path)
	jobs.run = func(context.Context, *Target, func(Event)) { t.Error("保存失败不能开始采样") }
	_, err := jobs.Start(&Target{ChannelID: "d37fdd4b-e928-4410-9dba-796ba0613e72"})
	require.Error(t, err)
}

func TestTaskStorage_ChannelIDsStayInsideTaskDirectory(t *testing.T) {
	dir := t.TempDir()
	jobs := newTestJobs(t, filepath.Join(dir, "tasks"))
	channelIDs := []string{"ch_d37fdd4be92844109dba796ba0613e72", "legacy-channel", "../outside", `..\outside`, "D:\\outside", "渠道一"}
	paths := make(map[string]bool)
	for _, channelID := range channelIDs {
		t.Run(channelID, func(t *testing.T) {
			path := jobs.taskPath(channelID)
			require.Equal(t, jobs.dir, filepath.Dir(path))
			require.False(t, paths[path], "不同 ID 不能共用任务文件")
			paths[path] = true
			task := Task{ID: "saved-task", Status: "completed", Target: &Target{ChannelID: channelID}}
			require.NoError(t, jobs.writeTaskFile(task))
			saved, err := newTestJobs(t, jobs.dir).Latest(channelID)
			require.NoError(t, err)
			require.Equal(t, channelID, saved.Target.ChannelID)
		})
	}
	_, err := os.Stat(filepath.Join(dir, "outside.json"))
	require.ErrorIs(t, err, os.ErrNotExist)
}

func TestTaskStorage_LegacyRecordRules(t *testing.T) {
	dir := t.TempDir()
	channelID := "d37fdd4b-e928-4410-9dba-796ba0613e72"
	legacy := Task{ID: "legacy-task", Status: "completed", Target: &Target{ChannelID: channelID}}
	data, err := json.Marshal(legacy)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(dir, channelID+".json"), data, 0600))
	jobs := newTestJobs(t, dir)
	saved, err := jobs.Latest(channelID)
	require.NoError(t, err)
	require.Equal(t, "legacy-task", saved.ID)
	legacy.ID = "new-task"
	require.NoError(t, jobs.writeTaskFile(legacy))
	saved, err = newTestJobs(t, dir).Latest(channelID)
	require.NoError(t, err)
	require.Equal(t, "new-task", saved.ID)
	require.NoError(t, os.WriteFile(jobs.taskPath(channelID), []byte("broken"), 0600))
	_, err = newTestJobs(t, dir).Latest(channelID)
	require.ErrorContains(t, err, "解析指纹检测任务失败", "新记录损坏时不能静默回退到旧结果")

	outside := "../outside"
	data, err = json.Marshal(Task{ID: "outside-task", Status: "completed", Target: &Target{ChannelID: outside}})
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "outside.json"), data, 0600))
	saved, err = newTestJobs(t, filepath.Join(dir, "tasks")).Latest(outside)
	require.NoError(t, err)
	require.Nil(t, saved, "任务目录之外的旧文件不能被读取")
}

func TestShutdownCancelsTaskAndPreservesCompletedSamples(t *testing.T) {
	jobs := newTestJobs(t, t.TempDir())
	started := make(chan struct{})
	jobs.run = func(ctx context.Context, _ *Target, emit func(Event)) {
		emit(Event{Type: "sample", Sample: &Sample{Index: 0, State: "complete", Text: "saved-answer"}})
		emit(Event{Type: "sample", Sample: &Sample{Index: 1, State: "waiting"}})
		close(started)
		<-ctx.Done()
	}
	target := &Target{ChannelID: "d37fdd4b-e928-4410-9dba-796ba0613e72"}
	_, err := jobs.Start(target)
	require.NoError(t, err)
	<-started
	require.NoError(t, jobs.Close())
	saved, err := newTestJobs(t, jobs.dir).Latest(target.ChannelID)
	require.NoError(t, err)
	require.Equal(t, "interrupted", saved.Status)
	require.Equal(t, "saved-answer", saved.Samples[0].Text)
	require.Equal(t, "cancelled", saved.Samples[1].State)
	_, err = jobs.Start(target)
	require.ErrorContains(t, err, "服务正在关闭")
}

func TestRetryRunsOnlySpecifiedSampleAndKeepsTaskIdentity(t *testing.T) {
	jobs := newTestJobs(t, t.TempDir())
	t.Cleanup(func() { require.NoError(t, jobs.Close()) })
	target := &Target{ChannelID: "ch_d37fdd4be92844109dba796ba0613e72", Model: "selected",
		upstream: &config.UpstreamConfig{APIKeys: []string{"original-key"}}}
	previous := Task{ID: "original-task", Status: "completed", Target: target, DataBuilt: "fixture-version", Samples: []Sample{
		{Index: 0, State: "complete", Prompt: "第一道", Text: "saved-first", ExpectedCount: 302},
		{Index: 1, State: "failed", Prompt: "第二道", Error: "timeout", ExpectedCount: 322},
		{Index: 2, State: "complete", Prompt: "第三道", Text: "saved-third", ExpectedCount: 330},
	}}
	jobs.tasks[target.ChannelID] = &taskRun{task: previous}
	jobs.restore = func(target *Target) (*Target, error) { return target, nil }
	started := make(chan Task, 1)
	release := make(chan struct{})
	jobs.run = func(context.Context, *Target, func(Event)) { t.Error("单条重试不能重新运行整轮") }
	jobs.retry = func(ctx context.Context, _ *Target, saved Task, indexes []int, emit func(Event)) {
		if len(indexes) != 1 || indexes[0] != 1 {
			t.Errorf("只应重试第二条，实际为 %v", indexes)
		}
		started <- saved
		select {
		case <-ctx.Done():
			return
		case <-release:
		}
		emit(Event{Type: "sample", Sample: &Sample{Index: 1, State: "complete", Prompt: "第二道", Text: "retried-second", ExpectedCount: 322}})
		emit(Event{Type: "result", Analysis: &lmfpd.Analysis{Decision: "scored", UsedOutputs: 3, Results: []lmfpd.AnalysisResult{}}})
	}
	_, err := jobs.Retry(target.ChannelID, previous.ID, 0)
	require.ErrorContains(t, err, "只能重试")
	_, err = jobs.Retry(target.ChannelID, "stale-task", 1)
	require.ErrorContains(t, err, "已被替换")
	task, err := jobs.Retry(target.ChannelID, previous.ID, 1)
	require.NoError(t, err)
	require.Equal(t, previous.ID, task.ID)
	saved := <-started
	require.Equal(t, "saved-first", saved.Samples[0].Text)
	require.Equal(t, "saved-third", saved.Samples[2].Text)
	_, err = jobs.Retry(target.ChannelID, previous.ID, 1)
	require.ErrorIs(t, err, ErrTaskRunning)
	close(release)
	jobs.workers.Wait()
	final, err := jobs.Latest(target.ChannelID)
	require.NoError(t, err)
	require.Equal(t, previous.ID, final.ID)
	require.Equal(t, "completed", final.Status)
	require.Equal(t, "saved-first", final.Samples[0].Text)
	require.Equal(t, "retried-second", final.Samples[1].Text)
	require.Equal(t, "saved-third", final.Samples[2].Text)
	require.Equal(t, 3, final.Analysis.UsedOutputs)
}

func TestRetryFailed_OnlyQueuesFailedSamplesAndPreservesSuccess(t *testing.T) {
	jobs := newTestJobs(t, t.TempDir())
	t.Cleanup(func() { require.NoError(t, jobs.Close()) })
	target := &Target{ChannelID: "ch_retry_failed", upstream: &config.UpstreamConfig{}}
	previous := Task{ID: "same-task", Status: "completed", Target: target, DataBuilt: "fixture-version", Samples: []Sample{
		{Index: 0, State: "complete", Prompt: "已成功的挑战", Text: "保留回答", ExpectedCount: 302},
		{Index: 1, State: "failed", Prompt: "失败挑战", ExpectedCount: 322},
		{Index: 2, State: "truncated", Prompt: "截断挑战", ExpectedCount: 330},
	}}
	jobs.tasks[target.ChannelID] = &taskRun{task: previous}
	jobs.restore = func(target *Target) (*Target, error) { return target, nil }
	jobs.run = func(context.Context, *Target, func(Event)) { t.Error("重新检测失败项不能启动整轮采样") }
	queued := make(chan []int, 1)
	release := make(chan struct{})
	jobs.retry = func(ctx context.Context, _ *Target, saved Task, indexes []int, emit func(Event)) {
		queued <- indexes
		select {
		case <-ctx.Done():
			return
		case <-release:
		}
		for _, index := range indexes {
			sample := saved.Samples[index]
			sample.State, sample.Text = "complete", "补测回答"
			emit(sampleEvent(sample))
		}
		emit(Event{Type: "result", Analysis: &lmfpd.Analysis{Decision: "scored", UsedOutputs: 3}})
	}
	created, err := jobs.RetryFailed(target.ChannelID, previous.ID)
	require.NoError(t, err)
	require.Equal(t, previous.ID, created.ID)
	require.Equal(t, []int{1, 2}, <-queued)
	_, err = jobs.RetryFailed(target.ChannelID, previous.ID)
	require.ErrorIs(t, err, ErrTaskRunning)
	close(release)
	jobs.workers.Wait()
	saved, err := newTestJobs(t, jobs.dir).Latest(target.ChannelID)
	require.NoError(t, err)
	require.Equal(t, previous.Samples[0], saved.Samples[0])
	require.Equal(t, "补测回答", saved.Samples[1].Text)
	require.Equal(t, "补测回答", saved.Samples[2].Text)
	_, err = jobs.RetryFailed(target.ChannelID, previous.ID)
	require.ErrorContains(t, err, "没有需要重新检测")
}

// 每个测试显式收回后台保留任务，避免临时目录与后台清理并行退出。
func newTestJobs(t *testing.T, dir string) *Jobs {
	t.Helper()
	jobs := NewJobs(&Service{}, dir)
	t.Cleanup(func() { _ = jobs.Close() })
	return jobs
}
