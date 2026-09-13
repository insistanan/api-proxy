// Package llmgate 为代理内部功能（危险命令 AI 闸门、AI 配置助手）提供
// "选一个大模型渠道并发一段提示词" 的通用调用能力。
//
// 与 visionlayer（图片理解旁路）和 eval.Sender（评测原生发送）同形但不同域：
// 三者都做"代理自己发起上游请求"，但 vision 耦合图片语义与 VisionCapable
// 过滤，eval 耦合探针/评分闭环；llmgate 只关心文本提示词的进出与渠道健康，
// 不引入任何业务判断。上游失败重试判定复用 utils.ClassifyUpstreamStatus +
// 响应体谓词（两层判断，唯一出处），不自行只看状态码。
package llmgate

import (
	"context"
	"fmt"
	"strings"

	"github.com/BenedictKing/api-proxy/internal/config"
	"github.com/BenedictKing/api-proxy/internal/scheduler"
)

// 渠道挑选顺序：messages → responses → chat → gemini。
// images 不参与（生成图片，不是文本对话）。
var preferredKinds = []scheduler.ChannelKind{
	scheduler.ChannelKindMessages,
	scheduler.ChannelKindResponses,
	scheduler.ChannelKindChat,
	scheduler.ChannelKindGemini,
}

// SelectedChannel 描述一次内部调用选中的渠道。
type SelectedChannel struct {
	Kind         scheduler.ChannelKind
	Upstream     *config.UpstreamConfig
	ChannelIndex int
	// Reserved 表示本次选择占用了调度器的 in-flight 预留（编排选渠产生）。
	// 调用方在整个内部调用（含全部重试）结束后必须 ReleaseChannelReservation；
	// 仅探测可用性时应立即释放（见 Caller.PreviewChannel）。
	Reserved bool
}

// PickChannel 挑选一个可用的内部调用渠道。
// channelID 非空时只认该稳定 ID（不存在或当前不可用即返回错误，不回退——
// 显式指定代表用户意图）。
// channelID 为空且调度器可用时，按 messages → responses → chat → gemini 顺序
// 复用主链路编排选渠（scheduler.SelectChannel：分组匹配 → 优先级 → 熔断/健康
// 过滤 → 兜底分组与组内降级）；model 即请求的模型 ID（分组匹配词），为空时
// 与主链路对未匹配模型的行为一致——走各协议兜底分组。
// channelID 为空且调度器不可用（单测环境）时，退化为按切片顺序扫描第一个
// "启用 + 有 Key"的渠道（不经过编排）。
func PickChannel(ctx context.Context, cfgManager *config.ConfigManager, channelScheduler *scheduler.ChannelScheduler, channelID string, model string) (*SelectedChannel, error) {
	if cfgManager == nil {
		return nil, fmt.Errorf("内部调用未绑定配置管理器")
	}
	channelID = strings.TrimSpace(channelID)
	if channelID != "" {
		return pickByID(cfgManager, channelScheduler, channelID)
	}
	if channelScheduler != nil {
		return pickByOrchestration(ctx, channelScheduler, model)
	}
	for _, kind := range preferredKinds {
		selected, err := firstAvailableChannel(cfgManager, channelScheduler, kind, "")
		if err != nil || selected != nil {
			return selected, err
		}
	}
	return nil, fmt.Errorf("没有可用的内部调用渠道（按 messages → responses → chat → gemini 顺序查找均失败）")
}

// pickByOrchestration 按四协议优先级复用主链路编排选渠。
// SelectChannel 自带分组匹配、优先级顺序、熔断与健康过滤、兜底分组与组内
// 降级（ExcludeFromConversation 的公用图片理解渠道天然被排除），这里只负责
// 跨协议的优先级与失败聚合。
func pickByOrchestration(ctx context.Context, channelScheduler *scheduler.ChannelScheduler, model string) (*SelectedChannel, error) {
	var lastErr error
	for _, kind := range preferredKinds {
		result, err := channelScheduler.SelectChannel(ctx, "", nil, kind, model)
		if err != nil {
			lastErr = err
			continue
		}
		if result == nil || result.Upstream == nil {
			continue
		}
		return &SelectedChannel{
			Kind:         kind,
			Upstream:     result.Upstream.Clone(),
			ChannelIndex: result.ChannelIndex,
			Reserved:     result.Reserved,
		}, nil
	}
	if lastErr != nil {
		return nil, fmt.Errorf("按 messages → responses → chat → gemini 编排选渠均失败: %w", lastErr)
	}
	return nil, fmt.Errorf("没有可用的内部调用渠道（按 messages → responses → chat → gemini 顺序查找均失败）")
}

func pickByID(cfgManager *config.ConfigManager, channelScheduler *scheduler.ChannelScheduler, channelID string) (*SelectedChannel, error) {
	for _, kind := range preferredKinds {
		selected, err := firstAvailableChannel(cfgManager, channelScheduler, kind, channelID)
		if err != nil || selected != nil {
			return selected, err
		}
	}
	return nil, fmt.Errorf("内部调用渠道 %q 不存在或不在 messages/responses/chat/gemini 四类之中", channelID)
}

// firstAvailableChannel 在单个 kind 的渠道切片中找可用渠道。
// channelID 非空时只匹配该 ID；匹配到但不可用时返回错误（显式指定不静默跳过）。
// channelID 为空时返回第一个可用渠道；该 kind 无可用渠道时返回 (nil, nil)，
// 由调用方继续尝试下一个 kind。
func firstAvailableChannel(cfgManager *config.ConfigManager, channelScheduler *scheduler.ChannelScheduler, kind scheduler.ChannelKind, channelID string) (*SelectedChannel, error) {
	cfg := cfgManager.GetConfig()
	var upstreams []config.UpstreamConfig
	switch kind {
	case scheduler.ChannelKindMessages:
		upstreams = cfg.Upstream
	case scheduler.ChannelKindResponses:
		upstreams = cfg.ResponsesUpstream
	case scheduler.ChannelKindChat:
		upstreams = cfg.ChatUpstream
	case scheduler.ChannelKindGemini:
		upstreams = cfg.GeminiUpstream
	default:
		return nil, fmt.Errorf("内部调用不支持渠道类型 %s", kind)
	}
	for index := range upstreams {
		upstream := &upstreams[index]
		if channelID != "" && upstream.ID != channelID {
			continue
		}
		selected, unavailable := checkChannelAvailable(channelScheduler, kind, upstream, index)
		if selected != nil {
			return selected, nil
		}
		// 显式指定时，匹配到 ID 但不可用要立刻报错，不能静默跳到别的 kind。
		if channelID != "" {
			return nil, unavailable
		}
		return nil, nil
	}
	return nil, nil
}

// checkChannelAvailable 校验渠道可用性（启用 + 有 Key + 健康 + 未熔断）。
// 可用返回选中结果；不可用返回原因。
func checkChannelAvailable(channelScheduler *scheduler.ChannelScheduler, kind scheduler.ChannelKind, upstream *config.UpstreamConfig, index int) (*SelectedChannel, error) {
	if config.GetChannelStatus(upstream) != config.ChannelStatusActive {
		return nil, fmt.Errorf("渠道 %q 未启用", upstream.Name)
	}
	if len(upstream.APIKeys) == 0 {
		return nil, fmt.Errorf("渠道 %q 没有可用的 API Key", upstream.Name)
	}
	if channelScheduler != nil {
		if !channelScheduler.AllowChannelByCircuit(kind, index, upstream.Name) {
			return nil, fmt.Errorf("渠道 %q 当前处于熔断状态", upstream.Name)
		}
		metricsManager := channelScheduler.MetricsManager(kind)
		if metricsManager != nil && !metricsManager.IsChannelHealthyWithKeys(upstream.BaseURL, upstream.APIKeys, index) {
			return nil, fmt.Errorf("渠道 %q 当前不健康", upstream.Name)
		}
	}
	return &SelectedChannel{Kind: kind, Upstream: upstream.Clone(), ChannelIndex: index}, nil
}
