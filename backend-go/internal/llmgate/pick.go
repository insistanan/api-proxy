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
	// Permit 是本次选择取得的调度器许可（R2-b）：持有 in-flight 预留与
	// 可能的半开探测名额。调用方在整个内部调用（含全部重试）结束后必须
	// RecordHealth + Release；仅探测可用性时应立即 Release（中性释放名额）。
	// 可能为 nil（直查路径不经过选渠编排，无许可）。
	Permit *scheduler.ChannelPermit
}

// PickChannel 挑选一个可用的内部调用渠道。
// channelID 非空时只认该稳定 ID（不存在或当前不可用即返回错误，不回退——
// 显式指定代表用户意图）。
// channelID 为空且 model 非空时，按 messages → responses → chat → gemini 顺序
// 复用主链路编排选渠（scheduler.SelectChannel：分组匹配 → 优先级 → 熔断/健康
// 过滤 → 兜底分组与组内降级）——model 即请求的模型 ID（分组匹配词）。
// channelID 与 model 均空（内部调用自动模式）时**不走分组路由**：主链路的
// 空模型语义只进兜底分组，挂在命名分组的渠道会被整体跳过——与内部调用
// "按四协议优先级找一个能用渠道"的意图不符（用户场景：messages 池渠道全部
// 挂在命名分组，自动模式却降级到 chat）。改为按 messages → responses →
// chat → gemini × 配置顺序全池扫描，取第一个"可用 + 能解析出模型"的渠道。
// failedChannels 按 kind 排除已失败的渠道（自动模式换渠道重试用）；nil 不排除。
func PickChannel(ctx context.Context, cfgManager *config.ConfigManager, channelScheduler *scheduler.ChannelScheduler, channelID string, model string, failedChannels map[scheduler.ChannelKind]map[int]bool) (*SelectedChannel, error) {
	if cfgManager == nil {
		return nil, fmt.Errorf("内部调用未绑定配置管理器")
	}
	channelID = strings.TrimSpace(channelID)
	model = strings.TrimSpace(model)
	if channelID != "" {
		return pickByID(cfgManager, channelScheduler, channelID, model)
	}
	if channelScheduler != nil && model != "" {
		return pickByOrchestration(ctx, channelScheduler, model, failedChannels)
	}
	for _, kind := range preferredKinds {
		var kindFailed map[int]bool
		if failedChannels != nil {
			kindFailed = failedChannels[kind]
		}
		selected, err := firstAvailableChannel(cfgManager, channelScheduler, kind, "", model, kindFailed)
		if err != nil || selected != nil {
			return selected, err
		}
	}
	return nil, fmt.Errorf("没有可用的内部调用渠道（按 messages → responses → chat → gemini 顺序查找均失败）")
}

// pickByOrchestration 按四协议优先级复用主链路编排选渠。
// SelectChannel 自带分组匹配、优先级顺序、熔断与健康过滤、兜底分组与组内
// 降级（ExcludeFromConversation 的公用图片理解渠道天然被排除），这里只负责
// 跨协议的优先级与失败聚合，以及内部调用特有的"模型可解析"过滤：
// 主链路允许空模型选中无 DefaultModel 的渠道（由请求阶段处理），
// 但内部调用必须发出一个具体模型，解析不出模型的渠道直接排除重选。
// 注意：SelectChannel 在渠道处于熔断半开（HalfOpen）时可能占用唯一的探测
// 名额（AllowChannelByCircuit → circuit.Allow），该名额必须由调用方在本次
// 内部调用结束时以 Circuit 记账（Success/Failure/Neutral）释放——真实调用
// 见 Caller.callOnChannel，探测见 Caller.PreviewChannel；漏记会把渠道卡死在
// HalfOpen，表现为"状态栏显示该渠道、实际请求却选不中它"。
func pickByOrchestration(ctx context.Context, channelScheduler *scheduler.ChannelScheduler, model string, failedChannels map[scheduler.ChannelKind]map[int]bool) (*SelectedChannel, error) {
	var lastErr error
	for _, kind := range preferredKinds {
		// 组内排除集：入参 failedChannels（跨渠道重试）+ 本轮模型过滤排除，
		// SelectChannel 按 failedChannels 语义自然推进到下一渠道。
		excluded := make(map[int]bool)
		if failedChannels != nil {
			for index := range failedChannels[kind] {
				excluded[index] = true
			}
		}
		for {
			result, err := channelScheduler.SelectChannel(ctx, "", excluded, kind, model)
			if err != nil {
				lastErr = err
				break
			}
			if result == nil || result.Upstream == nil {
				break
			}
			if canResolveInternalModel(model, result.Upstream) {
				return &SelectedChannel{
					Kind:         kind,
					Upstream:     result.Upstream.Clone(),
					ChannelIndex: result.ChannelIndex,
					Permit:       result.Permit,
				}, nil
			}
			// 该渠道解析不出模型（无 DefaultModel 且映射不命中），调用必然
			// 失败：释放本次选渠占用的许可（in-flight 预留 + 探测名额），
			// 排除后重选。
			if result.Permit != nil {
				result.Permit.Release()
			}
			result.Permit = nil
			excluded[result.ChannelIndex] = true
		}
	}
	if lastErr != nil {
		return nil, fmt.Errorf("按 messages → responses → chat → gemini 编排选渠均失败: %w", lastErr)
	}
	return nil, fmt.Errorf("没有可用的内部调用渠道（按 messages → responses → chat → gemini 顺序查找均失败）")
}

// canResolveInternalModel 判断渠道能否为内部调用解析出非空模型：
// prompt.Model 能命中映射，或渠道配置了 DefaultModel。两者皆无的渠道
// （空模型 + 无配置）在调用期必然报"未能解析可用模型"，选渠阶段即排除。
func canResolveInternalModel(model string, upstream *config.UpstreamConfig) bool {
	if upstream == nil {
		return false
	}
	return strings.TrimSpace(config.ResolveUpstreamModel(model, upstream)) != ""
}

// pickByID 按稳定渠道 ID 选渠。同 canResolveInternalModel 口径：解析不出
// 模型的渠道（无 DefaultModel 且映射不命中）显式报错，不静默换渠道。
func pickByID(cfgManager *config.ConfigManager, channelScheduler *scheduler.ChannelScheduler, channelID string, model string) (*SelectedChannel, error) {
	for _, kind := range preferredKinds {
		selected, err := firstAvailableChannel(cfgManager, channelScheduler, kind, channelID, model, nil)
		if err != nil || selected != nil {
			return selected, err
		}
	}
	return nil, fmt.Errorf("内部调用渠道 %q 不存在或不在 messages/responses/chat/gemini 四类之中", channelID)
}

// firstAvailableChannel 在单个 kind 的渠道切片中按配置顺序找可用渠道。
// channelID 非空时只匹配该 ID；匹配到但不可用时返回错误（显式指定不静默跳过）。
// channelID 为空时（自动模式全池扫描）：跳过 failedChannels 排除的渠道与
// 解析不出模型的渠道，按配置顺序取第一个可用渠道；本 kind 无可用渠道时
// 返回 (nil, nil)，由调用方继续尝试下一个 kind。
func firstAvailableChannel(cfgManager *config.ConfigManager, channelScheduler *scheduler.ChannelScheduler, kind scheduler.ChannelKind, channelID, model string, failedChannels map[int]bool) (*SelectedChannel, error) {
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
		if channelID == "" && failedChannels != nil && failedChannels[index] {
			continue
		}
		// 纯配置过滤必须先于消耗半开探测名额的可用性检查。
		if !canResolveInternalModel(model, upstream) {
			if channelID != "" {
				return nil, fmt.Errorf("内部调用渠道 %q 解析不出模型（请配置默认模型或模型映射）", upstream.Name)
			}
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
	}
	return nil, nil
}

// checkChannelAvailable 校验渠道可用性（启用 + 有 Key + 健康 + 未熔断）。
// 可用返回选中结果；不可用返回原因。
// 熔断放行走 CircuitAllow（消耗式），渠道处于半开（HalfOpen）时占用唯一
// 探测名额并封装为许可句柄（持有取得时的状态代次）——调用方必须在调用
// 结束/探测结束时以许可 RecordHealth + Release 释放（真实调用按结果记账，
// 探测恒中性释放）。
func checkChannelAvailable(channelScheduler *scheduler.ChannelScheduler, kind scheduler.ChannelKind, upstream *config.UpstreamConfig, index int) (*SelectedChannel, error) {
	if config.GetChannelStatus(upstream) != config.ChannelStatusActive {
		return nil, fmt.Errorf("渠道 %q 未启用", upstream.Name)
	}
	if len(upstream.APIKeys) == 0 {
		return nil, fmt.Errorf("渠道 %q 没有可用的 API Key", upstream.Name)
	}
	if channelScheduler != nil {
		circuitResult := channelScheduler.CircuitAllow(kind, index, upstream.Name)
		if !circuitResult.Allowed {
			return nil, fmt.Errorf("渠道 %q 当前处于熔断状态", upstream.Name)
		}
		metricsManager := channelScheduler.MetricsManager(kind)
		if metricsManager != nil && !metricsManager.IsChannelHealthyWithKeys(upstream.BaseURL, upstream.APIKeys, index) {
			if circuitResult.Probe {
				channelScheduler.RecordCircuitNeutral(kind, index)
			}
			return nil, fmt.Errorf("渠道 %q 当前不健康", upstream.Name)
		}
		selected := &SelectedChannel{Kind: kind, Upstream: upstream.Clone(), ChannelIndex: index}
		if circuitResult.Probe {
			selected.Permit = channelScheduler.NewCircuitProbePermit(kind, index, circuitResult.Generation)
		}
		return selected, nil
	}
	return &SelectedChannel{Kind: kind, Upstream: upstream.Clone(), ChannelIndex: index}, nil
}
