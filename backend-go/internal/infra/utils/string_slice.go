package utils

import (
	"sort"
	"strings"
)

// DedupeStrings 去重字符串切片并保持首次出现顺序，空字符串按普通值参与判定。
func DedupeStrings(items []string) []string {
	if len(items) <= 1 {
		return items
	}
	seen := make(map[string]struct{}, len(items))
	result := make([]string, 0, len(items))
	for _, item := range items {
		if _, exists := seen[item]; exists {
			continue
		}
		seen[item] = struct{}{}
		result = append(result, item)
	}
	return result
}

// DedupeSortedStrings 去除首尾空白并丢弃空项，去重后按字典序升序排列。
// 适用于模型列表等要求稳定输出顺序的场景；需要保留输入顺序时改用 DedupeStrings。
func DedupeSortedStrings(items []string) []string {
	seen := make(map[string]bool, len(items))
	result := make([]string, 0, len(items))
	for _, item := range items {
		item = strings.TrimSpace(item)
		if item == "" || seen[item] {
			continue
		}
		seen[item] = true
		result = append(result, item)
	}
	sort.Strings(result)
	return result
}
