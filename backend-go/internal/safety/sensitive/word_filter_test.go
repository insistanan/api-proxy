package sensitive

import (
	"strings"
	"sync"
	"testing"

	"github.com/insistanan/api-proxy/internal/infra/config"
)

func TestWordFilterFindsCategoriesAndVariants(t *testing.T) {
	filter, err := NewWordFilter(defaultSensitiveWordSettings())
	if err != nil {
		t.Fatalf("创建过滤器失败: %v", err)
	}

	tests := []struct {
		name      string
		text      string
		category  WordCategory
		word      string
		canonical string
	}{
		{name: "色情", text: "请不要发布裸聊内容", category: WordCategoryPornography, word: "裸聊", canonical: "裸聊"},
		{name: "赌博变体", text: "网络赌搏平台", category: WordCategoryGambling, word: "赌搏", canonical: "赌博"},
		{name: "毒品", text: "拒绝冰毒交易", category: WordCategoryDrugs, word: "冰毒", canonical: "冰毒"},
		{name: "暴力恐怖", text: "禁止讨论炸弹制作", category: WordCategoryViolenceTerror, word: "炸弹制作", canonical: "炸弹制作"},
		{name: "政治敏感", text: "禁止煽动分裂国家", category: WordCategoryPolitical, word: "煽动分裂国家", canonical: "煽动分裂国家"},
		{name: "违法犯罪", text: "电信诈骗受害者", category: WordCategoryIllegalCrime, word: "电信诈骗", canonical: "电信诈骗"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			match, ok := filter.FindFirst(test.text)
			if !ok {
				t.Fatalf("未命中 %q", test.text)
			}
			if match.Category != test.category || match.Word != test.word || match.Canonical != test.canonical {
				t.Fatalf("命中结果错误: %+v", match)
			}
			if test.text[match.Start:match.End] != test.word {
				t.Fatalf("命中偏移错误: %+v", match)
			}
		})
	}
}

func TestWordFilterCategoryAndGlobalSwitches(t *testing.T) {
	settings := defaultSensitiveWordSettings()
	settings.GamblingEnabled = false
	filter, err := NewWordFilter(settings)
	if err != nil {
		t.Fatalf("创建过滤器失败: %v", err)
	}
	if _, ok := filter.FindFirst("赌博"); ok {
		t.Fatal("关闭赌博分类后仍然命中")
	}
	if _, ok := filter.FindFirst("色情交易"); !ok {
		t.Fatal("其他分类被错误关闭")
	}

	settings.Enabled = false
	if err := filter.Reload(settings); err != nil {
		t.Fatalf("关闭全局开关失败: %v", err)
	}
	if filter.Enabled() {
		t.Fatal("全局关闭后过滤器仍报告启用")
	}
	if _, ok := filter.FindFirst("色情交易"); ok {
		t.Fatal("关闭全局开关后仍然命中")
	}
}

func TestWordFilterCustomWordsAndFailedReload(t *testing.T) {
	settings := config.SensitiveWordConfig{Enabled: true, CustomWords: []string{"内部密令"}}
	filter, err := NewWordFilter(settings)
	if err != nil {
		t.Fatalf("创建自定义过滤器失败: %v", err)
	}
	match, ok := filter.FindFirst("前缀内部密令后缀")
	if !ok || match.Category != WordCategoryCustom || match.Word != "内部密令" {
		t.Fatalf("自定义词命中错误: %+v, %v", match, ok)
	}
	if match.Start != len("前缀") || match.End != len("前缀内部密令") {
		t.Fatalf("自定义词偏移错误: %+v", match)
	}

	invalid := defaultSensitiveWordSettings()
	invalid.CustomWords = []string{"赌博"}
	if err := filter.Reload(invalid); err == nil {
		t.Fatal("内置词重复时应拒绝重载")
	}
	if _, ok := filter.FindFirst("内部密令"); !ok {
		t.Fatal("重载失败后旧词库未保留")
	}
}

func TestWordFilterConcurrentReloadAndRead(t *testing.T) {
	filter, err := NewWordFilter(defaultSensitiveWordSettings())
	if err != nil {
		t.Fatalf("创建过滤器失败: %v", err)
	}
	settings := defaultSensitiveWordSettings()
	settings.GamblingEnabled = false

	var waitGroup sync.WaitGroup
	waitGroup.Add(2)
	go func() {
		defer waitGroup.Done()
		for range 100 {
			_ = filter.FindAll("普通文本与赌博")
		}
	}()
	go func() {
		defer waitGroup.Done()
		for range 100 {
			if err := filter.Reload(settings); err != nil {
				t.Errorf("并发重载失败: %v", err)
				return
			}
		}
	}()
	waitGroup.Wait()
}

func BenchmarkWordFilterTenThousandRunes(b *testing.B) {
	filter, err := NewWordFilter(defaultSensitiveWordSettings())
	if err != nil {
		b.Fatalf("创建过滤器失败: %v", err)
	}
	text := strings.Repeat("正常文本", 2500)
	b.ResetTimer()
	for range b.N {
		filter.FindFirst(text)
	}
}

func defaultSensitiveWordSettings() config.SensitiveWordConfig {
	return config.SensitiveWordConfig{
		Enabled:               true,
		PornographyEnabled:    true,
		GamblingEnabled:       true,
		DrugsEnabled:          true,
		ViolenceTerrorEnabled: true,
		PoliticalEnabled:      true,
		IllegalCrimeEnabled:   true,
		CustomWords:           []string{},
	}
}
