// 本文件实现模型映射"显式目标渠道"分支：把映射命中的（目标模型，目标渠道）交给目标渠道执行。
// 语义为单跳：目标渠道以替换后的单一映射（请求模型 -> 目标模型）执行，不再解析其自身的
// ModelMappingChannels，因此不会形成渠道间递归。目标渠道的在途预留、熔断探测与健康结果
// 归目标渠道所有，在本文件内经目标许可记账；源渠道的健康结论由 upstream_failover.go 决定。
package proxycore

import (
	"fmt"
	"log"
)

// tryMappedChannelTarget 把一个"目标模型 + 显式目标渠道"交给目标渠道执行。
// 返回的 error 仅表示目标渠道在发送前不可用（不存在、禁用、无密钥、熔断、自指等），
// 此时未向上游发送任何请求；调用方据 Fuzzy 模式决定转入下一候选还是直接报错。
// 目标渠道已发送请求后的结果放在 UpstreamAttemptResult 中返回，其 Health 为目标渠道自身的结论，
// 调用方不得把它当作源渠道的健康结果。
func (a UpstreamAttempt) tryMappedChannelTarget(targetModel string, channelID string) (UpstreamAttemptResult, error) {
	if a.Upstream != nil && channelID == a.Upstream.ID {
		return UpstreamAttemptResult{}, fmt.Errorf("映射目标渠道不能是本渠道自身")
	}

	selection, err := a.ChannelScheduler.SelectMappedChannel(a.Kind, channelID)
	if err != nil {
		return UpstreamAttemptResult{}, err
	}
	permit := selection.Permit
	targetIdx := selection.ChannelIndex
	targetCopy := selection.Upstream

	baseURLs := targetCopy.GetAllBaseURLs()
	if len(baseURLs) == 0 {
		// 本地配置问题：中性归还许可（含探测名额），不按渠道失败记账。
		permit.RecordHealthNeutral()
		permit.Release()
		return UpstreamAttemptResult{}, fmt.Errorf("映射目标渠道 %q 没有可用 BaseURL", targetCopy.Name)
	}

	// 以单一映射替换目标渠道的映射：目标模型必须是用户在映射中选定的那一个。
	// DefaultModel 同样清空，否则目标渠道的默认模型会覆盖映射指定的目标模型。
	targetCopy.ModelMapping = map[string][]string{
		a.RequestedModel: {targetModel},
	}
	targetCopy.ModelMappingChannels = nil
	targetCopy.DefaultModel = ""

	mapped := a
	mapped.Upstream = targetCopy
	mapped.Permit = permit
	mapped.URLResults = a.ChannelScheduler.GetSortedURLsForChannel(a.Kind, targetIdx, baseURLs)
	mapped.LogContext.ChannelIndex = targetIdx

	// 闭包按目标渠道下标重新绑定：密钥降级与 URL 健康必须写到目标渠道，而不是源渠道。
	if a.DeprioritizeKey != nil {
		mapped.DeprioritizeKey = func(apiKey string) {
			if err := a.ConfigManager.MoveAPIKeyToBottomForKind(string(a.Kind), targetIdx, apiKey); err != nil {
				log.Printf("[%s-Key] 警告: 密钥降级失败: %v", a.APIType, err)
			}
		}
	}
	if a.MarkURLFailure != nil {
		mapped.MarkURLFailure = func(url string) {
			a.ChannelScheduler.MarkURLFailure(a.Kind, targetIdx, url)
		}
	}
	if a.MarkURLSuccess != nil {
		mapped.MarkURLSuccess = func(url string) {
			a.ChannelScheduler.MarkURLSuccess(a.Kind, targetIdx, url)
		}
	}

	log.Printf("[%s-ModelMapping] 模型 %s 指定目标渠道 [%d] %s，目标模型 %s",
		a.APIType, a.RequestedModel, targetIdx, targetCopy.Name, targetModel)

	result := mapped.tryWithAllKeys()
	// 目标渠道的健康结论在此经目标许可记账并释放，不经过源渠道的外壳。
	recordChannelHealth(a.ChannelScheduler, a.Kind, targetIdx, result.Health, true, a.APIType, permit)
	permit.Release()
	return result, nil
}

// mappingSourceHealth 计算源渠道在映射循环中的健康结论。
// 源渠道只对自身发起过的本地尝试负责：
//   - 没有任何候选转交目标渠道时，保持原有语义（记失败）；
//   - 转交过目标渠道且源渠道自身有本地失败时，记失败；
//   - 仅有目标渠道参与（目标渠道已各自记账）时，记中性，不改变源渠道熔断状态。
func mappingSourceHealth(localAttempted bool, mappedRouted bool) channelHealthResult {
	if !mappedRouted || localAttempted {
		return channelHealthFailure
	}
	return channelHealthNeutral
}
