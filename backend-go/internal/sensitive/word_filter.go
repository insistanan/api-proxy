package sensitive

import (
	"sync/atomic"

	"github.com/insistanan/api-proxy/internal/config"
)

// WordMatch 描述一次敏感词命中。Start 和 End 是原始 UTF-8 字符串的字节偏移，End 不包含在命中范围内。
type WordMatch struct {
	Word      string       `json:"word"`
	Canonical string       `json:"canonical"`
	Category  WordCategory `json:"category"`
	Start     int          `json:"start"`
	End       int          `json:"end"`
}

// WordFilter 保存不可变的已编译词库；Reload 成功后以原子方式替换当前快照。
type WordFilter struct {
	snapshot atomic.Pointer[compiledWordFilter]
}

type compiledWordFilter struct {
	enabled   bool
	automaton *acAutomaton
}

// NewWordFilter 根据设置编译敏感词过滤器。
func NewWordFilter(settings config.SensitiveWordConfig) (*WordFilter, error) {
	filter := &WordFilter{}
	if err := filter.Reload(settings); err != nil {
		return nil, err
	}
	return filter, nil
}

// Reload 先完整编译新词库，仅在编译成功后替换当前快照。
func (f *WordFilter) Reload(settings config.SensitiveWordConfig) error {
	patterns, err := compileWordPatterns(settings)
	if err != nil {
		return err
	}
	compiled := &compiledWordFilter{
		enabled:   settings.Enabled,
		automaton: buildACAutomaton(patterns),
	}
	f.snapshot.Store(compiled)
	return nil
}

// Enabled 返回当前快照的全局开关状态。
func (f *WordFilter) Enabled() bool {
	compiled := f.snapshot.Load()
	return compiled != nil && compiled.enabled
}

// PatternCount 返回当前快照编译后的词条数量，包含变体和自定义词。
func (f *WordFilter) PatternCount() int {
	compiled := f.snapshot.Load()
	if compiled == nil || compiled.automaton == nil {
		return 0
	}
	return len(compiled.automaton.patterns)
}

// FindFirst 返回扫描顺序中的第一个命中。
func (f *WordFilter) FindFirst(text string) (WordMatch, bool) {
	compiled := f.snapshot.Load()
	if compiled == nil || !compiled.enabled || compiled.automaton == nil {
		return WordMatch{}, false
	}
	var first WordMatch
	found := false
	compiled.automaton.scan(text, func(match WordMatch) bool {
		first = match
		found = true
		return false
	})
	return first, found
}

// FindAll 返回文本中的全部命中，包括重叠词条。
func (f *WordFilter) FindAll(text string) []WordMatch {
	compiled := f.snapshot.Load()
	if compiled == nil || !compiled.enabled || compiled.automaton == nil {
		return nil
	}
	matches := make([]WordMatch, 0)
	compiled.automaton.scan(text, func(match WordMatch) bool {
		matches = append(matches, match)
		return true
	})
	return matches
}

type acAutomaton struct {
	nodes    []acNode
	patterns []compiledPattern
}

type acNode struct {
	next    map[byte]int
	failure int
	outputs []int
}

func buildACAutomaton(patterns []compiledPattern) *acAutomaton {
	automaton := &acAutomaton{
		nodes:    []acNode{{next: make(map[byte]int)}},
		patterns: append([]compiledPattern(nil), patterns...),
	}

	for patternIndex, pattern := range automaton.patterns {
		state := 0
		for _, value := range []byte(pattern.text) {
			nextState, exists := automaton.nodes[state].next[value]
			if !exists {
				nextState = len(automaton.nodes)
				automaton.nodes[state].next[value] = nextState
				automaton.nodes = append(automaton.nodes, acNode{next: make(map[byte]int)})
			}
			state = nextState
		}
		automaton.nodes[state].outputs = append(automaton.nodes[state].outputs, patternIndex)
	}

	queue := make([]int, 0, len(automaton.nodes))
	for _, child := range automaton.nodes[0].next {
		queue = append(queue, child)
	}
	for head := 0; head < len(queue); head++ {
		state := queue[head]
		for value, nextState := range automaton.nodes[state].next {
			queue = append(queue, nextState)
			failure := automaton.nodes[state].failure
			for failure != 0 {
				if _, exists := automaton.nodes[failure].next[value]; exists {
					break
				}
				failure = automaton.nodes[failure].failure
			}
			if candidate, exists := automaton.nodes[failure].next[value]; exists && candidate != nextState {
				automaton.nodes[nextState].failure = candidate
			}
			failureOutputs := automaton.nodes[automaton.nodes[nextState].failure].outputs
			automaton.nodes[nextState].outputs = append(automaton.nodes[nextState].outputs, failureOutputs...)
		}
	}
	return automaton
}

func (a *acAutomaton) scan(text string, yield func(WordMatch) bool) {
	state := 0
	for index := 0; index < len(text); index++ {
		value := text[index]
		for state != 0 {
			if _, exists := a.nodes[state].next[value]; exists {
				break
			}
			state = a.nodes[state].failure
		}
		if nextState, exists := a.nodes[state].next[value]; exists {
			state = nextState
		} else {
			state = 0
		}

		for _, patternIndex := range a.nodes[state].outputs {
			pattern := a.patterns[patternIndex]
			end := index + 1
			if !yield(WordMatch{
				Word:      pattern.text,
				Canonical: pattern.canonical,
				Category:  pattern.category,
				Start:     end - len(pattern.text),
				End:       end,
			}) {
				return
			}
		}
	}
}
