package config

import (
	"fmt"
	"slices"
)

// IntentRecognitionStages 只返回固定顺序，配置不能改变尝试次序。
// 空模式保留旧配置语义，由旧分类路径处理。
func IntentRecognitionStages(value IntentRoutingConfig) []string {
	if value.RecognitionMode == "hybrid" {
		stages := []string{}
		for _, stage := range []string{"builtin", "semantic", "assistant"} {
			if slices.Contains(value.HybridMatchers, stage) {
				stages = append(stages, stage)
			}
		}
		return stages
	}
	if value.RecognitionMode != "" {
		return []string{value.RecognitionMode}
	}
	return nil
}

func IntentUsesVector(value IntentRoutingConfig) bool {
	if value.RecognitionMode != "" {
		return slices.Contains(IntentRecognitionStages(value), "semantic")
	}
	return value.Strategy == "semantic" || value.Strategy == "auto" && value.TaskMatcher == "semantic"
}

func validateIntentRecognition(value IntentRoutingConfig) error {
	if value.RecognitionMode == "" {
		return nil
	}
	if value.Strategy != "auto" || !slices.Contains([]string{"builtin", "semantic", "assistant", "hybrid"}, value.RecognitionMode) {
		return fmt.Errorf("识别方式必须为内置规则、向量匹配、辅助模型或混合模式，并使用分组能力路由")
	}
	stages := IntentRecognitionStages(value)
	if value.RecognitionMode == "hybrid" {
		if len(stages) < 2 || len(stages) != len(value.HybridMatchers) || !slices.Equal(stages, value.HybridMatchers) {
			return fmt.Errorf("混合模式须选择至少两种不同方式，并按内置规则、向量匹配、辅助模型的固定顺序配置")
		}
	} else if len(value.HybridMatchers) != 0 {
		return fmt.Errorf("只有混合模式可以配置多种识别方式")
	}
	if slices.Contains(stages, "assistant") && !slices.Contains([]string{"llm", "decisions"}, value.Classifier) {
		return fmt.Errorf("辅助模型只支持 LLM 或 Decisions 决策模型")
	}
	if value.Enabled && slices.Contains(stages, "assistant") && (value.ClassifierChannel == "" || value.ClassifierModel == "") {
		return fmt.Errorf("请选择辅助模型的渠道和模型")
	}
	if value.Enabled && slices.Contains(stages, "semantic") && (len(value.DatasetIDs) == 0 || value.EmbeddingModel == "" || value.EmbeddingSource == "remote" && value.EmbeddingChannel == "") {
		return fmt.Errorf("向量匹配需要数据集和同一向量模型的已发布索引")
	}
	if value.Enabled && slices.Contains(stages, "semantic") {
		return ValidateSemanticEmbedding(SemanticEmbeddingConfig{Source: value.EmbeddingSource, Channel: value.EmbeddingChannel, Model: value.EmbeddingModel, ModelVersion: value.EmbeddingVersion, QueryPrefix: value.QueryPrefix, ExamplePrefix: value.ExamplePrefix, TimeoutMS: value.TimeoutMS})
	}
	return nil
}
