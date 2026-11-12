package config

import (
	"fmt"
	"math"
	"regexp"
	"slices"
	"strings"
)

// SemanticEmbeddingConfig 标识一个独立向量空间。远程模型升级时需修改版本并重建索引。
type SemanticEmbeddingConfig struct {
	Source        string `json:"source"`
	Channel       string `json:"channel"`
	Model         string `json:"model"`
	ModelVersion  string `json:"modelVersion"`
	QueryPrefix   string `json:"queryPrefix"`
	ExamplePrefix string `json:"examplePrefix"`
	TimeoutMS     int    `json:"timeoutMs"`
}

type SemanticSafetyConfig struct {
	Enabled       bool                    `json:"enabled"`
	Mode          string                  `json:"mode"`
	DatasetIDs    []string                `json:"datasetIds"`
	Embedding     SemanticEmbeddingConfig `json:"embedding"`
	Threshold     float64                 `json:"threshold"`
	Margin        float64                 `json:"margin"`
	FailurePolicy string                  `json:"failurePolicy"`
}

var semanticID = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,64}$`)

func ValidateDatasetIDs(ids []string) error {
	if len(ids) > 8 {
		return fmt.Errorf("一次最多选择 8 个数据集")
	}
	seen := make(map[string]bool)
	for _, id := range ids {
		if !semanticID.MatchString(id) || seen[id] {
			return fmt.Errorf("数据集 ID 无效或重复: %q", id)
		}
		seen[id] = true
	}
	return nil
}

func ValidateSemanticEmbedding(p SemanticEmbeddingConfig) error {
	if p.Source != "local" && p.Source != "remote" {
		return fmt.Errorf("向量来源必须是 local 或 remote")
	}
	if p.Source == "remote" && p.Channel == "" {
		return fmt.Errorf("请选择 Embeddings 渠道")
	}
	if strings.TrimSpace(p.Model) == "" || len(p.Model) > 256 || len(p.ModelVersion) > 128 {
		return fmt.Errorf("向量模型为空或模型标识过长")
	}
	if len(p.QueryPrefix) > 512 || len(p.ExamplePrefix) > 512 {
		return fmt.Errorf("向量输入前缀不能超过 512 字节")
	}
	if p.TimeoutMS < 100 || p.TimeoutMS > 30000 {
		return fmt.Errorf("向量计算超时须为 100–30000 毫秒")
	}
	return nil
}

func validateSemanticSafety(cmd DangerousCmdConfig) error {
	p := cmd.Semantic
	if p == nil {
		return nil
	}
	if !slices.Contains([]string{"observe", "review", "block"}, p.Mode) {
		return fmt.Errorf("工具语义检测模式无效")
	}
	if !slices.Contains([]string{"block", "review"}, p.FailurePolicy) {
		return fmt.Errorf("工具语义检测失败时必须阻止或转 AI 审核")
	}
	for _, n := range []float64{p.Threshold, p.Margin} {
		if math.IsNaN(n) || math.IsInf(n, 0) || n < 0 || n > 1 {
			return fmt.Errorf("工具语义阈值与差值须为 0–1")
		}
	}
	if err := ValidateDatasetIDs(p.DatasetIDs); err != nil {
		return err
	}
	if !p.Enabled {
		return nil
	}
	if !cmd.Enabled || len(p.DatasetIDs) == 0 {
		return fmt.Errorf("工具语义检测需要启用危险命令检测并选择数据集")
	}
	if p.Mode != "observe" && !cmd.AIGate.Enabled {
		return fmt.Errorf("语义审核和拦截模式需要启用 AI 闸门，用于核验相似但不能确定的操作")
	}
	return ValidateSemanticEmbedding(p.Embedding)
}
