package scheduler

import (
	"fmt"
	"strings"

	"github.com/insistanan/api-proxy/internal/infra/config"
)

// SelectMappedChannel 为模型映射中显式指定的目标渠道取得许可（按稳定渠道 ID 定位）。
//
// 与 SelectChannel 的区别：目标渠道不参与分组路由、优先级编排与会话亲和，
// 只检查它自身是否可执行——处于 active 状态、配置了 API Key、未被熔断。
// 通过后与选渠路径一致地预留 in-flight；处于 HalfOpen 时经 CircuitAllow 正式占用
// 探测名额，并记录在许可句柄上。许可由调用方在目标渠道结束时记账并释放。
// 任一检查失败都返回错误，由调用方决定是否转入正常调度。
func (s *ChannelScheduler) SelectMappedChannel(kind ChannelKind, channelID string) (*SelectionResult, error) {
	channelID = strings.TrimSpace(channelID)
	if channelID == "" {
		return nil, fmt.Errorf("映射目标渠道 ID 为空")
	}

	upstreams, _, err := s.configManager.GetChannelConfig(string(kind))
	if err != nil {
		return nil, err
	}
	index := -1
	for i := range upstreams {
		if upstreams[i].ID == channelID {
			index = i
			break
		}
	}
	if index < 0 {
		return nil, fmt.Errorf("映射目标渠道 %q 不存在", channelID)
	}

	upstream := &upstreams[index]
	if status := config.GetChannelStatus(upstream); status != config.ChannelStatusActive {
		return nil, fmt.Errorf("映射目标渠道 %q 当前状态为 %s", upstream.Name, status)
	}
	if len(upstream.APIKeys) == 0 {
		return nil, fmt.Errorf("映射目标渠道 %q 没有可用 API 密钥", upstream.Name)
	}

	allow := s.CircuitAllow(kind, index, upstream.Name)
	if !allow.Allowed {
		return nil, fmt.Errorf("映射目标渠道 %q 处于熔断保护状态", upstream.Name)
	}

	s.ReserveChannel(kind, index)
	permit := s.newChannelPermit(kind, index, allow, true)
	return &SelectionResult{
		Upstream:     upstream.Clone(),
		ChannelIndex: index,
		Reason:       "model_mapping_target",
		Kind:         kind,
		Permit:       permit,
	}, nil
}
