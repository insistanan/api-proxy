package config

import (
	"fmt"
	"math"
	"slices"
)

// IntentGroupCapability 按协议记录分组的任务范围，不猜测渠道所用模型的能力。
type IntentGroupCapability struct {
	PoolID string   `json:"poolId"`
	Tasks  []string `json:"tasks"`
	Tiers  []int    `json:"tiers"`
	Cost   float64  `json:"cost"`
}

type IntentTask struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
}

// IntentTasks 与内置意图数据集使用相同类别；名称和解释由后端统一提供。
func IntentTasks() []IntentTask {
	return []IntentTask{
		{"translation", "翻译", "在语言之间翻译内容，保留原意和术语"},
		{"writing", "写作润色", "撰写或改写自然语言文章、邮件和文案"},
		{"summarization", "总结提炼", "总结材料、提取要点和行动项"},
		{"code_generation", "代码实现", "实现功能、编写程序或生成代码"},
		{"debugging", "调试排错", "定位和修复错误、故障或性能问题"},
		{"code_review", "代码审查", "审查已有代码的正确性、安全性和兼容性"},
		{"architecture", "架构设计", "设计系统架构、模块关系和技术方案"},
		{"testing", "测试验证", "编写测试、设计测试用例和验证行为"},
		{"data_analysis", "数据分析", "对数据或指标做统计、比较和分析"},
		{"explanation", "解释答疑", "讲解概念、代码、原理或用法"},
		{"formatting", "格式转换", "格式化、排序或转换文本与数据格式"},
		{"general_chat", "日常交流", "问候、闲聊和一般生活建议"},
	}
}

func validateIntentGroups(value IntentRoutingConfig, validPool func(string) bool) error {
	if value.TaskMatcher != "" && value.TaskMatcher != "builtin" && value.TaskMatcher != "semantic" {
		return fmt.Errorf("自动任务识别来源无效")
	}
	if len(value.Groups) > 100 {
		return fmt.Errorf("分组能力最多配置 100 项")
	}
	seen := map[string]bool{}
	for _, group := range value.Groups {
		if !validPool(group.PoolID) || seen[group.PoolID] {
			return fmt.Errorf("分组能力引用不存在或重复的分组 %s，请先解除引用", group.PoolID)
		}
		seen[group.PoolID] = true
		if len(group.Tasks) == 0 || len(group.Tasks) > 32 || len(group.Tiers) == 0 || len(group.Tiers) > 3 {
			return fmt.Errorf("分组 %s 需要任务类型和可处理难度", group.PoolID)
		}
		tasks := map[string]bool{}
		for _, task := range group.Tasks {
			if !semanticID.MatchString(task) || task == "unmatched" || tasks[task] {
				return fmt.Errorf("分组任务类型无效或重复: %s", task)
			}
			tasks[task] = true
		}
		for i, tier := range group.Tiers {
			if tier < 0 || tier > 2 || slices.Contains(group.Tiers[:i], tier) {
				return fmt.Errorf("分组难度必须是互不重复的简单、常规或复杂")
			}
		}
		if group.Cost < 0 || math.IsNaN(group.Cost) || math.IsInf(group.Cost, 0) {
			return fmt.Errorf("分组相对成本无效")
		}
	}
	if value.Enabled && value.Strategy == "auto" && len(value.Groups) == 0 {
		return fmt.Errorf("启用自动综合匹配前，请先配置分组能力")
	}
	if value.Enabled && value.Strategy == "auto" && value.TaskMatcher == "semantic" {
		if len(value.DatasetIDs) == 0 {
			return fmt.Errorf("向量任务识别需要选择已发布的数据集")
		}
		if err := ValidateSemanticEmbedding(SemanticEmbeddingConfig{Source: value.EmbeddingSource, Channel: value.EmbeddingChannel, Model: value.EmbeddingModel, ModelVersion: value.EmbeddingVersion, QueryPrefix: value.QueryPrefix, ExamplePrefix: value.ExamplePrefix, TimeoutMS: value.TimeoutMS}); err != nil {
			return err
		}
	}
	return nil
}
