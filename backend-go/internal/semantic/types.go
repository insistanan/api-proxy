// Package semantic 管理可审核的数据集、持久化向量快照和离线评测。
// 分类与内容安全共用检索能力；本包不决定路由分组或是否放行工具。
package semantic

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/insistanan/api-proxy/internal/config"
)

const (
	maxSamples           = 5000
	maxDatasetBytes      = 4 << 20
	maxDatasetTotalBytes = 64 << 20
	maxIndexBytes        = 512 << 20
	maxMemoryIndexBytes  = 128 << 20
	maxCachedVectors     = 8192
)

type Sample struct {
	ID       string `json:"id"`
	Label    string `json:"label"`
	Text     string `json:"text"`
	Negative bool   `json:"negative"`
	Split    string `json:"split"`
	Family   string `json:"family"`
	Approved bool   `json:"approved"`
	Source   string `json:"source"`
	Tool     string `json:"tool,omitempty"`
	Platform string `json:"platform,omitempty"`
	Pattern  string `json:"pattern,omitempty"`
}

type Dataset struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Kind        string   `json:"kind"`
	Description string   `json:"description"`
	Revision    int64    `json:"revision"`
	Samples     []Sample `json:"samples"`
}

type Summary struct {
	ID       string        `json:"id"`
	Name     string        `json:"name"`
	Kind     string        `json:"kind"`
	Revision int64         `json:"revision"`
	Count    int           `json:"count"`
	Approved int           `json:"approved"`
	Labels   []string      `json:"labels"`
	Indexes  []IndexStatus `json:"indexes"`
}

type IndexStatus struct {
	DatasetID string                         `json:"datasetId"`
	Revision  int64                          `json:"revision"`
	Profile   config.SemanticEmbeddingConfig `json:"profile"`
	Space     string                         `json:"space"`
	Count     int                            `json:"count"`
	Dimension int                            `json:"dimension"`
	Reused    int                            `json:"reused"`
	BuiltAt   time.Time                      `json:"builtAt"`
}

type snapshot struct {
	IndexStatus
	Samples []Sample    `json:"samples"`
	Vectors [][]float32 `json:"-"`
}

type Hit struct {
	DatasetID string  `json:"datasetId"`
	Revision  int64   `json:"revision"`
	Sample    Sample  `json:"sample"`
	Score     float64 `json:"score"`
}

type Result struct {
	Hits       []Hit  `json:"hits"`
	DurationMS int64  `json:"durationMs"`
	Version    string `json:"version"`
}

type Embedder interface {
	EmbeddingSpace(config.SemanticEmbeddingConfig) (string, error)
	EmbedSamples(context.Context, config.SemanticEmbeddingConfig, []string, string) ([][]float64, error)
}

func hash(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func profileKey(profile config.SemanticEmbeddingConfig) string {
	// 超时只影响调用，不能让相同模型的向量失效。
	profile.TimeoutMS = 0
	data, _ := json.Marshal(profile)
	return hash(string(data))
}

func vectorKey(space, text string) string { return hash(space + "\x00" + text) }

func cloneDataset(value Dataset) Dataset { value.Samples = slices.Clone(value.Samples); return value }

var validID = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,64}$`)

func ValidateDataset(d Dataset) error {
	if !validID.MatchString(d.ID) || strings.TrimSpace(d.Name) == "" || len(d.Name) > 128 {
		return fmt.Errorf("数据集 ID 或名称无效")
	}
	if d.Kind != "intent" && d.Kind != "safety" {
		return fmt.Errorf("数据集用途须为 intent 或 safety")
	}
	if len(d.Description) > 2000 || len(d.Samples) > maxSamples {
		return fmt.Errorf("描述过长或样本超过 %d 条", maxSamples)
	}
	data, err := json.Marshal(d)
	if err != nil || len(data) > maxDatasetBytes {
		return fmt.Errorf("数据集不能超过 4 MiB")
	}
	ids, texts, families := map[string]bool{}, map[string]string{}, map[string]string{}
	for _, row := range d.Samples {
		if !validID.MatchString(row.ID) || ids[row.ID] || !validID.MatchString(row.Label) {
			return fmt.Errorf("样本 ID 重复或样本类别无效: %q", row.ID)
		}
		ids[row.ID] = true
		if strings.TrimSpace(row.Text) == "" || len(row.Text) > 16000 {
			return fmt.Errorf("样本 %s 文本为空或超过 16000 字节", row.ID)
		}
		if row.Split != "index" && row.Split != "eval" {
			return fmt.Errorf("样本 %s 需指定 index 或 eval 集合", row.ID)
		}
		if strings.TrimSpace(row.Family) == "" || len(row.Family) > 128 || strings.TrimSpace(row.Source) == "" || len(row.Source) > 256 || len(row.Tool) > 128 {
			return fmt.Errorf("样本 %s 需要有效的样本族和来源标识", row.ID)
		}
		if row.Platform != "" && !slices.Contains([]string{"windows", "posix", "any"}, row.Platform) {
			return fmt.Errorf("样本平台无效")
		}
		if previous := families[row.Family]; previous != "" && previous != row.Split {
			return fmt.Errorf("样本族 %s 同时出现在索引与评测中，请整族划分", row.Family)
		}
		families[row.Family] = row.Split
		textKey := hash(strings.TrimSpace(row.Text))
		if previous := texts[textKey]; previous != "" && previous != row.Split {
			return fmt.Errorf("同一文本不能同时作为索引和评测样本")
		}
		texts[textKey] = row.Split
		if d.Kind == "safety" && !slices.Contains([]string{"destructive", "download_execute", "reverse_shell", "privilege_escalation", "environment_tampering"}, row.Label) {
			return fmt.Errorf("安全类别 %s 未对应已有危险命令规则", row.Label)
		}
		if row.Pattern != "" {
			if d.Kind != "safety" || row.Negative || len(row.Pattern) > 2048 {
				return fmt.Errorf("只有安全正例可配置不超过 2048 字节的核验正则")
			}
			if _, err := regexp.Compile(row.Pattern); err != nil {
				return fmt.Errorf("样本 %s 核验正则无效: %w", row.ID, err)
			}
		}
	}
	return nil
}
