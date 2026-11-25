package conversation

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
)

func cloneRecord(src *Record) *Record {
	if src == nil {
		return nil
	}
	dst := *src
	if src.RouteOverride != nil {
		override := *src.RouteOverride
		dst.RouteOverride = &override
	}
	if src.LastResolved != nil {
		resolved := *src.LastResolved
		dst.LastResolved = &resolved
	}
	if src.Prompts != nil {
		dst.Prompts = make([]string, len(src.Prompts))
		copy(dst.Prompts, src.Prompts)
	}
	if src.ImageFingerprints != nil {
		dst.ImageFingerprints = append([]string{}, src.ImageFingerprints...)
	}
	return &dst
}

func sortRecords(records []*Record) {
	// 按 ID 固定顺序，避免 LastSeenAt/状态变化导致列表跳动
	sort.Slice(records, func(i, j int) bool {
		return records[i].ID < records[j].ID
	})
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func truncate(value string, limit int) string {
	value = strings.TrimSpace(value)
	runes := []rune(value)
	if limit <= 0 || len(runes) <= limit {
		return value
	}
	return string(runes[:limit]) + "..."
}

func generateID(prefix string) (string, error) {
	buf := make([]byte, 8)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("生成对话 ID 失败: %w", err)
	}
	return fmt.Sprintf("%s_%s", prefix, hex.EncodeToString(buf)), nil
}
