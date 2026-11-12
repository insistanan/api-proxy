package hooks

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/insistanan/api-proxy/internal/config"
	"github.com/insistanan/api-proxy/internal/semantic"
	"github.com/insistanan/api-proxy/internal/sensitive"
)

// reviewSemanticCommands 不将“相似安全样本”作为放行授权。确定性规则独立执行，
// 语义候选需复核完整上下文；直接阻断还要求命中样本内已审核的核验正则。
func reviewSemanticCommands(ctx context.Context, service *semantic.Service, recorder BlockedLogRecorder, settings config.DangerousCmdConfig, metadata HookContext, commands []sensitive.CommandMatch, review func(sensitive.CommandMatch) *ContentSafetyError) error {
	p := settings.Semantic
	if !settings.Enabled || len(settings.EnabledRules) == 0 || p == nil || !p.Enabled {
		return nil
	}
	seen := map[string]bool{}
	for _, command := range commands {
		if command.Rule != "" {
			return dangerousCommandError(command)
		}
		identity := sensitive.CommandIdentity(command)
		if seen[identity] {
			continue
		}
		seen[identity] = true
		if strings.TrimSpace(command.Command) == "" {
			continue
		}
		tool := strings.ToLower(command.ToolName)
		if i := strings.LastIndex(tool, "__"); i >= 0 {
			tool = tool[i+2:]
		}
		if i := strings.LastIndex(tool, "."); i >= 0 {
			tool = tool[i+1:]
		}
		var result semantic.Result
		var err error
		if service == nil {
			err = fmt.Errorf("工具语义检测未绑定数据集服务")
		} else {
			// 只把脱敏后的查询副本送往辅助向量服务；授权身份和最终审查仍保留完整参数。
			result, err = service.SearchTool(ctx, p.DatasetIDs, p.Embedding, sensitive.RedactAuditText(command.Command), metadata.RequestID, tool, settings.EnabledRules)
		}
		policy, _ := json.Marshal(p)
		if err != nil {
			command.Rule = "semantic_unavailable"
			command.SemanticEvidence = "语义检查失败: " + err.Error() + "；策略=" + string(policy)
			if p.Mode == "observe" {
				if err := recordSemanticObservation(ctx, recorder, metadata, command); err != nil {
					return err
				}
				continue
			}
			if p.FailurePolicy == "review" {
				if safetyErr := review(command); safetyErr != nil {
					return safetyErr
				}
				continue
			}
			return &ContentSafetyError{BlockType: sensitive.BlockTypeDangerousCmd, RuleName: command.Rule, Snippet: command.SemanticEvidence, Command: command.Command, CommandHash: sensitive.CommandIdentity(command)}
		}
		var pendingReview *sensitive.CommandMatch
		for _, hit := range result.Hits {
			if hit.Sample.Negative || hit.Score < p.Threshold {
				continue
			}
			negative := -1.0
			for _, other := range result.Hits {
				if other.Sample.Label == hit.Sample.Label && other.Sample.Negative {
					negative = other.Score
				}
			}
			candidate := command
			candidate.Rule = hit.Sample.Label
			candidate.SemanticEvidence = fmt.Sprintf("数据集=%s 修订=%d 样本=%s 正例相似度=%.5f 反例相似度=%.5f 索引=%s 策略=%s；相似度仅为线索，不代表命令危险概率。", hit.DatasetID, hit.Revision, hit.Sample.ID, hit.Score, negative, result.Version, policy)
			if p.Mode == "observe" {
				if err := recordSemanticObservation(ctx, recorder, metadata, candidate); err != nil {
					return err
				}
				continue
			}
			if p.Mode == "block" && hit.Sample.Pattern != "" && hit.Score-negative >= p.Margin {
				pattern, err := regexp.Compile(hit.Sample.Pattern)
				if err != nil {
					return fmt.Errorf("语义核验规则无效: %w", err)
				}
				if pattern.MatchString(command.Command) {
					safetyErr := dangerousCommandError(candidate)
					safetyErr.Snippet = candidate.SemanticEvidence
					return safetyErr
				}
			}
			if pendingReview == nil {
				pendingReview = &candidate
			}
		}
		if pendingReview != nil {
			// 先检查所有确定性阻断条件，再对完整脚本进行一次复核。
			if safetyErr := review(*pendingReview); safetyErr != nil {
				return safetyErr
			}
		}
	}
	return nil
}

func recordSemanticObservation(ctx context.Context, recorder BlockedLogRecorder, metadata HookContext, command sensitive.CommandMatch) error {
	if recorder == nil {
		return fmt.Errorf("语义观察模式未绑定审计记录器")
	}
	key := "semantic:" + sensitive.CommandIdentity(command)
	if metadata.eventDeduper.contains(key) {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, gateStoreTimeout)
	defer cancel()
	_, err := recorder.Record(ctx, sensitive.BlockedLog{APIType: metadata.APIType, BlockType: sensitive.BlockTypeSemanticObserve, RuleName: command.Rule, PromptSnippet: "mode=observe source=tool_argument\n" + sensitive.RedactAuditText(command.SemanticEvidence), Command: sensitive.RedactAuditText(command.Command), CommandHash: sensitive.CommandIdentity(command), ChannelName: metadata.ChannelName, Model: metadata.Model, RequestID: metadata.RequestID, ConversationID: metadata.ConversationID})
	if err != nil {
		return fmt.Errorf("记录语义观察结果失败: %w", err)
	}
	return metadata.eventDeduper.mark(key, metadata.ConversationID == "")
}
