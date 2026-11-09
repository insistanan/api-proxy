package sensitive

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/insistanan/api-proxy/internal/config"
)

// WordCategory 表示敏感词所属分类。
type WordCategory string

const (
	WordCategoryPornography    WordCategory = "pornography"
	WordCategoryGambling       WordCategory = "gambling"
	WordCategoryDrugs          WordCategory = "drugs"
	WordCategoryViolenceTerror WordCategory = "violence_terror"
	WordCategoryPolitical      WordCategory = "political"
	WordCategoryIllegalCrime   WordCategory = "illegal_crime"
	WordCategoryCustom         WordCategory = "custom"
)

var builtinCategoryOrder = []WordCategory{
	WordCategoryPornography,
	WordCategoryGambling,
	WordCategoryDrugs,
	WordCategoryViolenceTerror,
	WordCategoryPolitical,
	WordCategoryIllegalCrime,
}

type wordDefinition struct {
	word     string
	variants []string
}

type compiledPattern struct {
	text      string
	canonical string
	category  WordCategory
}

func compileWordPatterns(settings config.SensitiveWordConfig) ([]compiledPattern, error) {
	if !settings.Enabled {
		seen := make(map[string]struct{}, len(settings.CustomWords))
		for _, word := range settings.CustomWords {
			if strings.TrimSpace(word) == "" || !utf8.ValidString(word) {
				return nil, fmt.Errorf("自定义敏感词无效")
			}
			if _, exists := seen[word]; exists {
				return nil, fmt.Errorf("自定义敏感词 %q 重复", word)
			}
			seen[word] = struct{}{}
		}
		return nil, nil
	}

	patterns := make([]compiledPattern, 0, 128+len(settings.CustomWords))
	seen := make(map[string]compiledPattern, cap(patterns))
	add := func(text, canonical string, category WordCategory) error {
		if strings.TrimSpace(text) == "" {
			return fmt.Errorf("%s 分类包含空敏感词", category)
		}
		if !utf8.ValidString(text) {
			return fmt.Errorf("%s 分类包含无效 UTF-8 敏感词", category)
		}
		if previous, exists := seen[text]; exists {
			return fmt.Errorf("敏感词 %q 在 %s 与 %s 中重复", text, previous.category, category)
		}
		pattern := compiledPattern{text: text, canonical: canonical, category: category}
		seen[text] = pattern
		patterns = append(patterns, pattern)
		return nil
	}

	for _, category := range builtinCategoryOrder {
		if !categoryEnabled(settings, category) {
			continue
		}
		for _, definition := range builtinWordDefinitions[category] {
			if err := add(definition.word, definition.word, category); err != nil {
				return nil, err
			}
			for _, variant := range definition.variants {
				if err := add(variant, definition.word, category); err != nil {
					return nil, err
				}
			}
		}
	}

	for _, word := range settings.CustomWords {
		if err := add(word, word, WordCategoryCustom); err != nil {
			return nil, fmt.Errorf("自定义敏感词无效: %w", err)
		}
	}
	return patterns, nil
}

func categoryEnabled(settings config.SensitiveWordConfig, category WordCategory) bool {
	switch category {
	case WordCategoryPornography:
		return settings.PornographyEnabled
	case WordCategoryGambling:
		return settings.GamblingEnabled
	case WordCategoryDrugs:
		return settings.DrugsEnabled
	case WordCategoryViolenceTerror:
		return settings.ViolenceTerrorEnabled
	case WordCategoryPolitical:
		return settings.PoliticalEnabled
	case WordCategoryIllegalCrime:
		return settings.IllegalCrimeEnabled
	default:
		return false
	}
}
