package modelfingerprint

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"
	"sync"
	"time"

	lmfpd "github.com/Ikaleio/lm-detector/sdk/go"
	"github.com/google/uuid"
	"github.com/insistanan/api-proxy/internal/storage/atomicfile"
)

var ErrTaskRunning = errors.New("该渠道已有指纹检测正在进行")

// Task 保存每个渠道最近一轮检测；凭据和内部上游配置不会序列化。
type Task struct {
	ID             string          `json:"id"`
	Status         string          `json:"status"`
	Target         *Target         `json:"target"`
	Samples        []Sample        `json:"samples"`
	Analysis       *lmfpd.Analysis `json:"analysis,omitempty"`
	DataBuilt      string          `json:"dataBuilt,omitempty"`
	DataSHA256     string          `json:"dataSha256,omitempty"`
	DetectorSHA256 string          `json:"detectorSha256,omitempty"`
	SDKVersion     string          `json:"sdkVersion,omitempty"`
	Error          string          `json:"error,omitempty"`
	CreatedAt      time.Time       `json:"createdAt"`
	UpdatedAt      time.Time       `json:"updatedAt"`
}

type taskRun struct {
	task   Task
	cancel context.CancelFunc
}

// Jobs 的生命周期由进程管理，HTTP 断开只停止查看，不取消检测。
type Jobs struct {
	mu       sync.Mutex
	workers  sync.WaitGroup
	dir      string
	tasks    map[string]*taskRun
	closed   bool
	closeErr error
	run      func(context.Context, *Target, func(Event))
	retry    func(context.Context, *Target, Task, []int, func(Event))
	restore  func(*Target) (*Target, error)
}

func NewJobs(service *Service, dir string) *Jobs {
	return &Jobs{dir: dir, tasks: make(map[string]*taskRun), run: service.Run, retry: service.RetrySamples, restore: service.restoreTarget}
}

func taskActive(task Task) bool {
	return task.Status == "loading" || task.Status == "running"
}

func detectorSDKVersion() string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return ""
	}
	for _, dependency := range info.Deps {
		if dependency.Path == "github.com/Ikaleio/lm-detector/sdk/go" {
			if dependency.Replace != nil {
				return dependency.Replace.Version
			}
			return dependency.Version
		}
	}
	return ""
}

func taskSnapshot(task Task) *Task {
	task.Samples = append([]Sample{}, task.Samples...)
	return &task
}

// 业务 ID 不限定为 UUID；摘要文件名避免旧 ID 中的路径字符影响存储位置。
func (j *Jobs) taskPath(channelID string) string {
	return filepath.Join(j.dir, fmt.Sprintf("%x.json", sha256.Sum256([]byte(channelID))))
}

func (j *Jobs) saveLocked(task Task) error {
	if task.Target == nil || strings.TrimSpace(task.Target.ChannelID) == "" {
		return fmt.Errorf("保存指纹检测任务失败：缺少渠道 ID")
	}
	data, err := json.Marshal(task)
	if err != nil {
		return fmt.Errorf("编码指纹检测任务失败：%w", err)
	}
	if err := atomicfile.Write(j.taskPath(task.Target.ChannelID), data, atomicfile.Options{}); err != nil {
		return fmt.Errorf("保存指纹检测任务失败：%w", err)
	}
	return nil
}

func (j *Jobs) loadLocked(channelID string) (*taskRun, error) {
	if strings.TrimSpace(channelID) == "" {
		return nil, fmt.Errorf("渠道 ID 不能为空")
	}
	if current := j.tasks[channelID]; current != nil {
		return current, nil
	}
	data, err := os.ReadFile(j.taskPath(channelID))
	// 兼容旧版已经保存的 UUID 文件，仅在新文件不存在时读取安全的旧文件名。
	if errors.Is(err, os.ErrNotExist) && !strings.ContainsAny(channelID, `/\:`) {
		if _, parseErr := uuid.Parse(channelID); parseErr == nil {
			data, err = os.ReadFile(filepath.Join(j.dir, channelID+".json"))
		}
	}
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("读取指纹检测任务失败：%w", err)
	}
	var task Task
	if err := json.Unmarshal(data, &task); err != nil {
		return nil, fmt.Errorf("解析指纹检测任务失败：%w", err)
	}
	if task.ID == "" || task.Target == nil || task.Target.ChannelID != channelID {
		return nil, fmt.Errorf("指纹检测任务数据无效")
	}
	if task.DetectorSHA256 == "" && task.Analysis != nil {
		task.DetectorSHA256 = task.Analysis.ModelVersion.BaseSHA256
	}
	// 进程重启后保留已完成样本，不自动重发会产生费用的请求。
	if taskActive(task) {
		stopTask(&task, "interrupted", "服务重启，检测已中断")
		if err := j.saveLocked(task); err != nil {
			return nil, err
		}
	}
	current := &taskRun{task: task}
	j.tasks[channelID] = current
	return current, nil
}

func (j *Jobs) Latest(channelID string) (*Task, error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	current, err := j.loadLocked(channelID)
	if err != nil || current == nil {
		return nil, err
	}
	return taskSnapshot(current.task), nil
}

func (j *Jobs) Start(target *Target) (*Task, error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.closed {
		return nil, fmt.Errorf("服务正在关闭，无法开始检测")
	}
	current, err := j.loadLocked(target.ChannelID)
	if err != nil {
		return nil, err
	}
	if current != nil && (taskActive(current.task) || current.cancel != nil) {
		return nil, ErrTaskRunning
	}
	now := time.Now().UTC()
	task := Task{ID: uuid.NewString(), Status: "loading", Target: target, Samples: []Sample{}, SDKVersion: detectorSDKVersion(), CreatedAt: now, UpdatedAt: now}
	if err := j.saveLocked(task); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	current = &taskRun{task: task, cancel: cancel}
	j.tasks[target.ChannelID] = current
	j.workers.Add(1)
	go j.execute(ctx, current, func(emit func(Event)) { j.run(ctx, target, emit) })
	return taskSnapshot(task), nil
}

func (j *Jobs) Retry(channelID, taskID string, index int) (*Task, error) {
	return j.retrySamples(channelID, taskID, []int{index})
}

func (j *Jobs) RetryFailed(channelID, taskID string) (*Task, error) {
	return j.retrySamples(channelID, taskID, nil)
}

func sampleRetryable(sample Sample) bool {
	return sample.State == "failed" || sample.State == "truncated" || sample.State == "cancelled"
}

func (j *Jobs) retrySamples(channelID, taskID string, indexes []int) (*Task, error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.closed {
		return nil, fmt.Errorf("服务正在关闭，无法重试")
	}
	current, err := j.loadLocked(channelID)
	if err != nil {
		return nil, err
	}
	if current == nil || current.task.ID != taskID {
		return nil, fmt.Errorf("检测任务不存在或已被替换，请刷新后重试")
	}
	if taskActive(current.task) || current.cancel != nil {
		return nil, ErrTaskRunning
	}
	if indexes == nil {
		for index, sample := range current.task.Samples {
			if sampleRetryable(sample) {
				indexes = append(indexes, index)
			}
		}
	}
	if len(indexes) == 0 {
		return nil, fmt.Errorf("没有需要重新检测的失败样本")
	}
	for _, index := range indexes {
		if index < 0 || index >= len(current.task.Samples) {
			return nil, fmt.Errorf("指定样本不存在")
		}
		sample := current.task.Samples[index]
		if !sampleRetryable(sample) {
			return nil, fmt.Errorf("只能重试失败、截断或已停止的样本")
		}
		if sample.Prompt == "" || sample.ExpectedCount <= 0 || current.task.DataBuilt == "" {
			return nil, fmt.Errorf("原挑战或参考库版本缺失，请重新开始整轮检测")
		}
	}
	if current.task.SDKVersion != "" && current.task.SDKVersion != detectorSDKVersion() {
		return nil, fmt.Errorf("检测 SDK 版本已变化，请重新开始整轮检测")
	}
	target, err := j.restore(current.task.Target)
	if err != nil {
		return nil, err
	}
	previous := *taskSnapshot(current.task)
	next := *taskSnapshot(current.task)
	next.Status, next.Error, next.Analysis = "loading", "", nil
	next.UpdatedAt = time.Now().UTC()
	if err := j.saveLocked(next); err != nil {
		return nil, err
	}
	current.task = next
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	current.cancel = cancel
	j.workers.Add(1)
	go j.execute(ctx, current, func(emit func(Event)) { j.retry(ctx, target, previous, indexes, emit) })
	return taskSnapshot(next), nil
}

func (j *Jobs) execute(ctx context.Context, current *taskRun, run func(func(Event))) {
	defer j.workers.Done()
	defer func() {
		panicValue := recover()
		j.mu.Lock()
		defer j.mu.Unlock()
		current.cancel()
		if taskActive(current.task) {
			message := "检测提前结束，未生成完整结果"
			if panicValue != nil {
				message = fmt.Sprintf("指纹检测发生异常：%v", panicValue)
			} else if errors.Is(ctx.Err(), context.DeadlineExceeded) {
				message = "指纹检测超过 10 分钟，已停止采样"
			}
			stopTask(&current.task, "failed", message)
			j.persistRunLocked(current)
		}
		current.cancel = nil
	}()
	run(func(event Event) {
		j.mu.Lock()
		defer j.mu.Unlock()
		if !taskActive(current.task) {
			return
		}
		if event.DataBuilt != "" {
			current.task.DataBuilt = event.DataBuilt
		}
		if event.DataSHA256 != "" {
			current.task.DataSHA256 = event.DataSHA256
		}
		switch event.Type {
		case "ready":
			current.task.Status = "running"
		case "sample":
			if event.Sample == nil || event.Sample.Index < 0 || event.Sample.Index > len(current.task.Samples) {
				stopTask(&current.task, "failed", "检测返回了无效样本")
				current.cancel()
				break
			}
			if event.Sample.Index == len(current.task.Samples) {
				current.task.Samples = append(current.task.Samples, *event.Sample)
			} else {
				current.task.Samples[event.Sample.Index] = *event.Sample
			}
		case "result":
			if event.Analysis == nil {
				stopTask(&current.task, "failed", "检测未返回评分结果")
				break
			}
			current.task.Analysis = event.Analysis
			current.task.DetectorSHA256 = event.Analysis.ModelVersion.BaseSHA256
			current.task.Status = "completed"
		case "error":
			stopTask(&current.task, "failed", event.Error)
		}
		current.task.UpdatedAt = time.Now().UTC()
		j.persistRunLocked(current)
	})
}

// 保存失败时显式结束任务，避免继续产生无法保留的采样结果。
func (j *Jobs) persistRunLocked(current *taskRun) {
	if err := j.saveLocked(current.task); err != nil {
		stopTask(&current.task, "failed", err.Error())
		current.cancel()
		j.closeErr = errors.Join(j.closeErr, err)
	}
}

func stopTask(task *Task, status, message string) {
	task.Status = status
	task.Error = message
	task.UpdatedAt = time.Now().UTC()
	for index := range task.Samples {
		if task.Samples[index].State == "waiting" {
			task.Samples[index].State = "cancelled"
		}
	}
}

func (j *Jobs) Cancel(channelID, taskID string) (*Task, error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	current, err := j.loadLocked(channelID)
	if err != nil {
		return nil, err
	}
	if current == nil || current.task.ID != taskID {
		return nil, fmt.Errorf("检测任务不存在或已被替换，请刷新后重试")
	}
	if taskActive(current.task) {
		stopTask(&current.task, "cancelled", "检测已停止")
		current.cancel()
		if err := j.saveLocked(current.task); err != nil {
			return nil, err
		}
	}
	return taskSnapshot(current.task), nil
}

func (j *Jobs) Close() error {
	j.mu.Lock()
	j.closed = true
	for _, current := range j.tasks {
		if taskActive(current.task) {
			stopTask(&current.task, "interrupted", "服务停止，检测已中断")
			j.persistRunLocked(current)
		}
		if current.cancel != nil {
			current.cancel()
		}
	}
	j.mu.Unlock()
	j.workers.Wait()
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.closeErr
}
