package proxycore

import (
	"fmt"
	"log"

	"github.com/BenedictKing/api-proxy/internal/config"
	"github.com/BenedictKing/api-proxy/internal/scheduler"
	"github.com/BenedictKing/api-proxy/internal/types"
	"github.com/gin-gonic/gin"
)

// MultiChannelAttemptResult 描述一次“选中渠道”的尝试结果（用于多渠道 failover 外壳复用）。
type MultiChannelAttemptResult struct {
	Handled           bool
	Attempted         bool
	SuccessKey        string
	SuccessBaseURLIdx int
	FailoverError     *FailoverError
	Usage             *types.Usage
	LastError         error
	// Health 是本渠道的最终健康结果（R2-a）。渠道级熔断记账只消费该字段，
	// 不得从 SuccessKey / LastError 推断。Unset 时外壳跳过记账并记录警告
	// （重复或缺失记账都是契约错误）。
	Health channelHealthResult
}

// TrySelectedChannelFunc 尝试一次选中的渠道，返回该渠道的尝试结果。
type TrySelectedChannelFunc func(selection *scheduler.SelectionResult) MultiChannelAttemptResult

// OnMultiChannelHandledFunc 在请求被“处理完成”时回调（成功或非 failover 错误都会触发）。
type OnMultiChannelHandledFunc func(selection *scheduler.SelectionResult, result MultiChannelAttemptResult)

// HandleAllFailedFunc 处理“所有渠道都失败”的返回逻辑（不同入口可能有不同错误格式）。
type HandleAllFailedFunc func(c *gin.Context, failoverErr *FailoverError, lastError error)

// HandleMultiChannelFailover 处理多渠道 failover 外壳逻辑（选渠道 + 聚合错误 + Trace 亲和）。
// 具体“渠道内 Key/BaseURL 轮转”由 trySelectedChannel 实现（通常调用 UpstreamAttempt.TryWithModelMappingFailover）。
//
// 选渠成功后会占用 in-flight 预留：
// - 若该渠道最终 Handled（成功/客户端取消等已写回响应），请求结束后释放预留
// - Fuzzy 模式下若该渠道失败并继续 failover，立即释放预留再选下一个
// 这样并发新对话在选渠瞬间就能看到彼此的占用，避免都打到同一供应商。
func HandleMultiChannelFailover(
	c *gin.Context,
	envCfg *config.EnvConfig,
	channelScheduler *scheduler.ChannelScheduler,
	kind scheduler.ChannelKind,
	apiType string,
	conversationID string,
	requestedModel string,
	fuzzyMode bool,
	trySelectedChannel TrySelectedChannelFunc,
	onHandled OnMultiChannelHandledFunc,
	handleAllFailed HandleAllFailedFunc,
) {
	if c == nil || envCfg == nil || channelScheduler == nil || trySelectedChannel == nil {
		return
	}
	if handleAllFailed == nil {
		handleAllFailed = func(c *gin.Context, failoverErr *FailoverError, lastError error) {
			HandleAllChannelsFailed(c, false, failoverErr, lastError, apiType)
		}
	}

	failedChannels := make(map[int]bool)
	var lastError error
	var lastFailoverError *FailoverError

	maxChannelAttempts := channelScheduler.GetActiveChannelCountForModel(kind, requestedModel)
	// 非 Fuzzy 模式锁定第一个渠道：渠道内 Key/模型候选耗尽后直接报错阻断，
	// 不切到下一个渠道、也不进入兜底分组。Fuzzy 模式才按配置位次顺序转移
	// （命中分组 → 兜底分组），并由 SelectChannel 负责选区。
	if !fuzzyMode {
		maxChannelAttempts = 1
	}

	for channelAttempt := 0; channelAttempt < maxChannelAttempts; channelAttempt++ {
		// 检查客户端是否已断开连接
		select {
		case <-c.Request.Context().Done():
			if envCfg.ShouldLog("info") {
				log.Printf("[%s-Cancel] 请求已取消，停止渠道 failover", apiType)
			}
			return
		default:
			// 继续正常流程
		}

		selection, err := channelScheduler.SelectChannel(c.Request.Context(), conversationID, failedChannels, kind, requestedModel)
		if err != nil {
			lastError = err
			break
		}

		upstream := selection.Upstream
		channelIndex := selection.ChannelIndex
		// releaseReservation 仅释放 in-flight 预留（探测名额由
		// recordChannelHealth 记账释放，R2-b 分离语义）。
		releaseReservation := func() {
			if selection != nil && selection.Permit != nil {
				selection.Permit.ReleaseReservation()
			}
		}

		if envCfg.ShouldLog("info") && upstream != nil {
			log.Printf("[%s-Select] 选择渠道: [%d] %s (原因: %s, 尝试 %d/%d, inFlight: %d)",
				apiType, channelIndex, upstream.Name, selection.Reason, channelAttempt+1, maxChannelAttempts,
				channelScheduler.GetChannelInFlight(kind, channelIndex))
		}

		result := trySelectedChannel(selection)
		if result.Handled {
			// 请求已完成（成功或已写回非 failover 错误），释放选渠预留。
			// 注意：上游实际 ActiveRequests 仍由 ProfileManager 独立维护。
			releaseReservation()
			// 渠道级熔断记账（R2-a）：只消费显式 Health 结果，不再从
			// SuccessKey / LastError 推断；Unset 视为契约缺陷，跳过记账并可见。
			recordChannelHealth(channelScheduler, kind, channelIndex, result.Health, result.Attempted, apiType, selection.Permit)
			if selection.Permit != nil {
				selection.Permit.Release()
				selection.Permit = nil
			}
			if onHandled != nil {
				onHandled(selection, result)
			}
			// 只有真正成功的请求才设置 Trace 亲和（客户端取消时 SuccessKey 为空）
			if result.SuccessKey != "" {
				// 对话级亲和已由 onHandled 回调（protocol.go 的 MarkConversationSuccess）写入，
				// 这里只维护会话级 Trace 亲和。
				// 仅在以下情况设置会话级亲和：
				// 1. 该会话没有亲和记录（新会话）
				// 2. 当前选择的原因是 trace_affinity（续期现有亲和）
				// 3. 当前选择的原因不是 trace_affinity，但该会话原来的亲和渠道在本次已失败（failover后建立新亲和）
				shouldSetAffinity := false
				affinityMgr := channelScheduler.GetTraceAffinityManager()
				if affinityMgr == nil {
					shouldSetAffinity = true
				} else {
					if _, hasAffinity := affinityMgr.GetPreferredChannelForKind(string(kind), conversationID); !hasAffinity {
						// 情况1：新会话，建立亲和
						shouldSetAffinity = true
					} else if selection.Reason == "trace_affinity" {
						// 情况2：使用了亲和渠道成功，续期
						shouldSetAffinity = true
					} else {
						// 情况3：检查原亲和渠道是否在本次失败
						if oldChannelIdx, ok := affinityMgr.GetPreferredChannelForKind(string(kind), conversationID); ok {
							if failedChannels[oldChannelIdx] {
								// 原亲和渠道失败，允许建立新亲和
								shouldSetAffinity = true
							}
						}
					}
				}

				if shouldSetAffinity {
					channelScheduler.SetTraceAffinityForKind(kind, conversationID, channelIndex)
				}
				channelScheduler.ConsumePromotionCount(channelIndex, kind)
			}
			return
		}

		// 当前渠道失败，释放预留后再尝试下一渠道，避免”失败渠道”继续占负载。
		releaseReservation()
		failedChannels[channelIndex] = true
		// 渠道级熔断记账（R2-a）：只消费显式 Health 结果。
		recordChannelHealth(channelScheduler, kind, channelIndex, result.Health, result.Attempted, apiType, selection.Permit)
		if selection.Permit != nil {
			selection.Permit.Release()
			selection.Permit = nil
		}

		if result.FailoverError != nil {
			lastFailoverError = result.FailoverError
			if upstream != nil {
				lastError = fmt.Errorf("渠道 [%d] %s 失败", channelIndex, upstream.Name)
			} else {
				lastError = fmt.Errorf("渠道 [%d] 失败", channelIndex)
			}
		}

		if result.Attempted && upstream != nil {
			log.Printf("[%s-Failover] 警告: 渠道 [%d] %s 所有密钥都失败，尝试下一个渠道", apiType, channelIndex, upstream.Name)
		}
	}

	log.Printf("[%s-Error] 所有渠道都失败了", apiType)
	handleAllFailed(c, lastFailoverError, lastError)
}

// recordChannelHealth 按显式健康结果做渠道级熔断记账（R2-a 唯一出口，
// R2-b 经许可句柄带代次记账）。Unset 且确有尝试（Attempted）说明调用方
// 没有给出渠道结束结论——跳过记账并记录警告，让契约缺陷可见而不是补成
// 默认成功/失败。
func recordChannelHealth(channelScheduler *scheduler.ChannelScheduler, kind scheduler.ChannelKind, channelIndex int, health channelHealthResult, attempted bool, apiType string, permit *scheduler.ChannelPermit) {
	var outcome func()
	if permit != nil {
		switch health {
		case channelHealthSuccess:
			outcome = permit.RecordHealthSuccess
		case channelHealthFailure:
			outcome = permit.RecordHealthFailure
		case channelHealthNeutral:
			outcome = permit.RecordHealthNeutral
		}
	}
	if outcome != nil {
		outcome()
		return
	}
	switch health {
	case channelHealthSuccess:
		channelScheduler.RecordCircuitSuccess(kind, channelIndex)
	case channelHealthFailure:
		channelScheduler.RecordCircuitFailure(kind, channelIndex)
	case channelHealthNeutral:
		channelScheduler.RecordCircuitNeutral(kind, channelIndex)
	default:
		if attempted {
			log.Printf("[%s-Circuit] 警告: 渠道 [%d] 尝试结束但未携带健康结果，跳过熔断记账", apiType, channelIndex)
		}
	}
}
