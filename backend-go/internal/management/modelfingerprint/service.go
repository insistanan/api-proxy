// Package modelfingerprint 直接采样指定上游，不进入代理、安全处理或路由编排。
package modelfingerprint

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strings"

	lmfpd "github.com/Ikaleio/lm-detector/sdk/go"
	"github.com/insistanan/api-proxy/internal/infra/config"
)

// Request 只接受明确的检测目标，不继承渠道的模型映射或生成参数。
type Request struct {
	Model    string `json:"model"`
	URLIndex int    `json:"urlIndex"`
	KeyIndex int    `json:"keyIndex"`
	Effort   string `json:"effort"`
}

// Target 持有整轮不变的配置快照；凭据只在后端使用。
type Target struct {
	ChannelID     string `json:"channelId"`
	ChannelName   string `json:"channelName"`
	Protocol      string `json:"protocol"`
	Model         string `json:"model"`
	URLIndex      int    `json:"urlIndex"`
	KeyIndex      int    `json:"keyIndex"`
	Compatibility bool   `json:"compatibility"`
	Effort        string `json:"effort"`
	ConfigDigest  string `json:"configDigest,omitempty"`
	upstream      *config.UpstreamConfig
	apiKey        string
	proxyURL      string
}

type Sample struct {
	Index         int    `json:"index"`
	State         string `json:"state"`
	Prompt        string `json:"prompt"`
	Text          string `json:"text"`
	ExpectedCount int    `json:"expectedCount"`
	Count         int    `json:"count"`
	DurationMS    int64  `json:"durationMs"`
	ResponseModel string `json:"responseModel,omitempty"`
	Error         string `json:"error,omitempty"`
}

type Event struct {
	Type       string          `json:"type"`
	Target     *Target         `json:"target,omitempty"`
	Sample     *Sample         `json:"sample,omitempty"`
	Analysis   *lmfpd.Analysis `json:"analysis,omitempty"`
	DataBuilt  string          `json:"dataBuilt,omitempty"`
	DataSHA256 string          `json:"dataSha256,omitempty"`
	Error      string          `json:"error,omitempty"`
}

type analyzer interface {
	Analyze([]lmfpd.Output, bool) (*lmfpd.Analysis, error)
	BuiltAt() string
	DataSHA256() string
}

// DataDirectory 与其他运行数据一样，相对于启动工作目录保存。
const DataDirectory = ".config/model-fingerprint"

type Service struct {
	cfg      *config.ConfigManager
	loadGate chan struct{}
	detector analyzer
	load     func(context.Context) (analyzer, error)
}

func New(cfg *config.ConfigManager) *Service {
	return &Service{cfg: cfg, loadGate: make(chan struct{}, 1), load: loadReference}
}

func (s *Service) Prepare(channelID string, input Request) (*Target, error) {
	located, ok := s.cfg.FindChannelByID(channelID)
	if !ok {
		return nil, fmt.Errorf("渠道不存在或已被删除")
	}
	u := located.Upstream.Clone()
	if config.GetChannelStatus(u) == config.ChannelStatusDeleted || u.BuiltinProvider != "" {
		return nil, fmt.Errorf("该渠道不支持独立模型指纹检测")
	}
	switch located.Kind {
	case "messages", "responses", "chat", "gemini":
	default:
		return nil, fmt.Errorf("仅支持对话类渠道的文本模型检测")
	}
	switch u.ServiceType {
	case "claude", "responses", "openai", "gemini":
	default:
		return nil, fmt.Errorf("不支持的上游协议：%s", u.ServiceType)
	}
	model := strings.TrimSpace(input.Model)
	if model == "" || len(model) > 256 || strings.ContainsAny(model, "\r\n") {
		return nil, fmt.Errorf("请提供有效的模型名称（最多 256 字符）")
	}
	urls := u.GetAllBaseURLs()
	if input.URLIndex < 0 || input.URLIndex >= len(urls) || strings.TrimSpace(urls[input.URLIndex]) == "" {
		return nil, fmt.Errorf("指定的上游地址不存在")
	}
	if input.KeyIndex < 0 || input.KeyIndex >= len(u.APIKeys) || strings.TrimSpace(u.APIKeys[input.KeyIndex]) == "" {
		return nil, fmt.Errorf("指定的 API Key 不存在或为空")
	}
	key := u.APIKeys[input.KeyIndex]
	effort := strings.TrimSpace(input.Effort)
	switch effort {
	case "", "none", "low", "high", "xhigh", "max":
	default:
		return nil, fmt.Errorf("不支持的思考等级：%s", effort)
	}
	proxyURL, err := s.cfg.ResolveUpstreamProxyURL(u)
	if err != nil {
		return nil, fmt.Errorf("解析渠道代理失败：%w", err)
	}
	u.BaseURL = urls[input.URLIndex]
	u.BaseURLs = []string{u.BaseURL}
	u.APIKeys = []string{key}
	// 冻结本轮伪装开关，沿用快捷测试的身份规则，但不进入路由和正文处理管线。
	compatibility := (u.ServiceType == "claude" || u.ServiceType == "responses") && s.cfg.ClientCompatibilityEnabled(u)
	u.ClientIdentityMode = "api"
	if compatibility {
		u.ClientIdentityMode = "compatibility"
	}
	u.ModelMapping = nil
	u.ModelMappingChannels = nil
	u.DefaultModel = ""
	target := &Target{ChannelID: u.ID, ChannelName: u.Name, Protocol: u.ServiceType,
		Model: model, URLIndex: input.URLIndex, KeyIndex: input.KeyIndex, Compatibility: compatibility,
		Effort: effort, upstream: u, apiKey: key, proxyURL: proxyURL}
	// 仅记录目标配置的摘要，重启后核验地址与凭据未变化，不把密钥写入任务文件。
	identity, err := json.Marshal(struct {
		URL, Key, Proxy, Protocol, Model, Effort string
		Compatibility, InsecureTLS, FullURL      bool
	}{u.BaseURL, key, proxyURL, u.ServiceType, model, effort, compatibility, u.InsecureSkipVerify, u.UseFullURL})
	if err != nil {
		return nil, fmt.Errorf("编码采样目标摘要失败：%w", err)
	}
	target.ConfigDigest = fmt.Sprintf("%x", sha256.Sum256(identity))
	if _, err := buildRequest(context.Background(), target, "校验请求地址"); err != nil {
		return nil, err
	}
	return target, nil
}

func (s *Service) restoreTarget(previous *Target) (*Target, error) {
	if previous.upstream != nil {
		return previous, nil
	}
	target, err := s.Prepare(previous.ChannelID, Request{Model: previous.Model, URLIndex: previous.URLIndex,
		KeyIndex: previous.KeyIndex, Effort: previous.Effort})
	if err != nil {
		return nil, err
	}
	if previous.ConfigDigest == "" || target.ConfigDigest != previous.ConfigDigest {
		return nil, fmt.Errorf("无法确认原采样配置未变，请重新开始整轮检测")
	}
	return target, nil
}

func (s *Service) reference(ctx context.Context) (analyzer, error) {
	select {
	case s.loadGate <- struct{}{}:
		defer func() { <-s.loadGate }()
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if s.detector == nil {
		detector, err := s.load(ctx)
		if err != nil {
			return nil, fmt.Errorf("加载指纹参考库失败：%w", err)
		}
		s.detector = detector
	}
	return s.detector, nil
}

// Run 顺序发送三条独立请求；固定目标失败时不切换 URL、Key、模型或渠道。
func (s *Service) Run(ctx context.Context, target *Target, emit func(Event)) {
	emit(Event{Type: "loading", Target: target})
	detector, err := s.reference(ctx)
	if err != nil {
		emit(Event{Type: "error", Error: err.Error()})
		return
	}
	challenges, err := lmfpd.GenerateChallenges(3)
	if err != nil {
		emit(Event{Type: "error", Error: fmt.Sprintf("生成检测挑战失败：%v", err)})
		return
	}
	emit(Event{Type: "ready", Target: target, DataBuilt: detector.BuiltAt(), DataSHA256: detector.DataSHA256()})
	samples := make([]Sample, len(challenges))
	for i, challenge := range challenges {
		if ctx.Err() != nil {
			return
		}
		sample := Sample{Index: i, State: "waiting", Prompt: challenge.Prompt, ExpectedCount: challenge.ExpectedCount}
		emit(sampleEvent(sample))
		sample = s.sample(ctx, target, challenge, i)
		emit(sampleEvent(sample))
		samples[i] = sample
	}
	emitAnalysis(ctx, detector, samples, emit)
}

// Retry 复用原挑战和同一参考库，只重新请求指定失败样本，随后重新计算整轮结果。
func (s *Service) Retry(ctx context.Context, target *Target, previous Task, index int, emit func(Event)) {
	s.RetrySamples(ctx, target, previous, []int{index}, emit)
}

// RetrySamples 在同一个后台任务内补测所选失败样本，全部尝试后只评分一次。
func (s *Service) RetrySamples(ctx context.Context, target *Target, previous Task, indexes []int, emit func(Event)) {
	if len(indexes) == 0 {
		emit(Event{Type: "error", Error: "没有需要重试的样本"})
		return
	}
	for _, index := range indexes {
		if index < 0 || index >= len(previous.Samples) {
			emit(Event{Type: "error", Error: "指定样本不存在"})
			return
		}
		if !sampleRetryable(previous.Samples[index]) {
			emit(Event{Type: "error", Error: "只能重试失败、截断或已停止的样本"})
			return
		}
	}
	detector, err := s.reference(ctx)
	if err != nil {
		emit(Event{Type: "error", Error: err.Error()})
		return
	}
	if previous.DataBuilt == "" || detector.BuiltAt() != previous.DataBuilt {
		emit(Event{Type: "error", Error: "参考库版本已变化，不能混合重试，请重新开始整轮检测"})
		return
	}
	if previous.DataSHA256 != "" && previous.DataSHA256 != detector.DataSHA256() {
		emit(Event{Type: "error", Error: "参考库数据摘要已变化，请重新开始整轮检测"})
		return
	}
	// 同一构建日期下替换评分器时也不能把新旧版本的结果混为同一轮。
	if previous.DetectorSHA256 != "" {
		current, err := detector.Analyze(sampleOutputs(previous.Samples), false)
		if err != nil {
			emit(Event{Type: "error", Error: fmt.Sprintf("核验参考评分器失败：%v", err)})
			return
		}
		if current == nil || current.ModelVersion.BaseSHA256 != previous.DetectorSHA256 {
			emit(Event{Type: "error", Error: "参考评分器已变化，请重新开始整轮检测"})
			return
		}
	}
	if ctx.Err() != nil {
		return
	}
	emit(Event{Type: "ready", DataBuilt: detector.BuiltAt(), DataSHA256: detector.DataSHA256()})
	for _, index := range indexes {
		if ctx.Err() != nil {
			return
		}
		sample := previous.Samples[index]
		emit(sampleEvent(Sample{Index: index, State: "waiting", Prompt: sample.Prompt, ExpectedCount: sample.ExpectedCount}))
		sample = s.sample(ctx, target, lmfpd.Challenge{Prompt: sample.Prompt, ExpectedCount: sample.ExpectedCount}, index)
		previous.Samples[index] = sample
		emit(sampleEvent(sample))
	}
	emitAnalysis(ctx, detector, previous.Samples, emit)
}

func sampleOutputs(samples []Sample) []lmfpd.Output {
	outputs := make([]lmfpd.Output, len(samples))
	for index, sample := range samples {
		outputs[index].ExpectedCount = sample.ExpectedCount
		if sample.State == "complete" {
			outputs[index].Text = sample.Text
		}
	}
	return outputs
}

func emitAnalysis(ctx context.Context, detector analyzer, samples []Sample, emit func(Event)) {
	if ctx.Err() != nil {
		return
	}
	analysis, err := detector.Analyze(sampleOutputs(samples), false)
	if err != nil {
		emit(Event{Type: "error", Error: fmt.Sprintf("指纹评分失败：%v", err)})
		return
	}
	if analysis == nil {
		emit(Event{Type: "error", Error: "指纹评分器未返回分析结果"})
		return
	}
	if analysis.Results == nil {
		analysis.Results = []lmfpd.AnalysisResult{}
	}
	emit(Event{Type: "result", Analysis: analysis, DataBuilt: detector.BuiltAt(), DataSHA256: detector.DataSHA256()})
}

func sampleEvent(sample Sample) Event {
	return Event{Type: "sample", Sample: &sample}
}
