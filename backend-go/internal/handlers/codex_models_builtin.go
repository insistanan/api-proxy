// Codex 内置模型目录获取、指令模板选择与进程内缓存。
package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// codexBuiltinCatalog 是通过 `codex debug models` 动态获取的内置模型清单。
// 不硬编码内置模型：不同 codex 版本的清单与指令模板不同，以运行时实际输出为准。
type codexBuiltinCatalog struct {
	entries []json.RawMessage
	index   map[string]json.RawMessage
}

func (b *codexBuiltinCatalog) has(slug string) bool {
	_, ok := b.index[slug]
	return ok
}

func (b *codexBuiltinCatalog) entryMeta(slug string) (codexModelEntryMeta, error) {
	raw, ok := b.index[slug]
	if !ok {
		return codexModelEntryMeta{}, newCodexInputError("模型 %s 不存在", slug)
	}
	return codexCatalogEntryMeta(raw)
}

// modelMessages 挑选一份通用指令模板作为自定义条目的 model_messages 来源：
// 优先 gpt-5.6-*（无插值变量、指令最新），否则退回第一个携带非空模板的条目。
func (b *codexBuiltinCatalog) modelMessages() (json.RawMessage, error) {
	var fallback json.RawMessage
	for _, entry := range b.entries {
		var wrapper struct {
			Slug          string          `json:"slug"`
			ModelMessages json.RawMessage `json:"model_messages"`
		}
		if err := json.Unmarshal(entry, &wrapper); err != nil || len(wrapper.ModelMessages) == 0 {
			continue
		}
		var parsed codexEntryModelMessages
		if err := json.Unmarshal(wrapper.ModelMessages, &parsed); err != nil || parsed.InstructionsTemplate == "" {
			continue
		}
		raw := append(json.RawMessage(nil), wrapper.ModelMessages...)
		if fallback == nil {
			fallback = raw
		}
		if strings.HasPrefix(wrapper.Slug, "gpt-5.6-") {
			return raw, nil
		}
	}
	if fallback == nil {
		return nil, fmt.Errorf("内置清单中没有任何携带 model_messages.instructions_template 的条目")
	}
	return fallback, nil
}

// builtinViews 输出内置清单视图（含 visibility=hide 条目，展示与否由前端决定）。
func (b *codexBuiltinCatalog) builtinViews() []codexModelView {
	views := make([]codexModelView, 0, len(b.entries))
	for _, entry := range b.entries {
		meta, err := codexCatalogEntryMeta(entry)
		if err != nil {
			continue
		}
		views = append(views, meta.view(true))
	}
	sort.Slice(views, func(i, j int) bool { return views[i].Slug < views[j].Slug })
	return views
}

// fetchCodexBuiltinCatalog 在隔离的 CODEX_HOME 下执行 `codex debug models` 获取内置清单。
// 必须用空目录隔离：model_catalog_json 是替换语义，用户已设置时直接执行会输出用户目录。
// 包级变量便于测试注入。
var fetchCodexBuiltinCatalog = func(ctx context.Context) (*codexBuiltinCatalog, error) {
	executable, err := exec.LookPath("codex")
	if err != nil {
		return nil, fmt.Errorf("未找到 codex 命令，无法获取内置模型清单: %w", err)
	}
	home, err := os.MkdirTemp("", "codex-home-*")
	if err != nil {
		return nil, fmt.Errorf("创建临时 CODEX_HOME 失败: %w", err)
	}
	defer os.RemoveAll(home)

	// Windows 上 npm 全局安装的 codex 是 .cmd 脚本，CreateProcess 无法直接执行，需经 cmd /c。
	args := []string{"debug", "models"}
	var cmd *exec.Cmd
	if ext := strings.ToLower(filepath.Ext(executable)); ext == ".cmd" || ext == ".bat" {
		cmd = exec.CommandContext(ctx, "cmd", append([]string{"/c", executable}, args...)...)
	} else {
		cmd = exec.CommandContext(ctx, executable, args...)
	}
	cmd.Env = append(os.Environ(), "CODEX_HOME="+home)
	stdout, err := cmd.Output()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && len(exitErr.Stderr) > 0 {
			return nil, fmt.Errorf("codex debug models 执行失败: %v: %s", err, bytes.TrimSpace(exitErr.Stderr))
		}
		return nil, fmt.Errorf("codex debug models 执行失败: %w", err)
	}
	var doc struct {
		Models []json.RawMessage `json:"models"`
	}
	if err := json.Unmarshal(stdout, &doc); err != nil {
		return nil, fmt.Errorf("解析 codex debug models 输出失败: %w", err)
	}
	if len(doc.Models) == 0 {
		return nil, fmt.Errorf("codex debug models 输出为空，无法获取内置模型清单")
	}
	catalog := &codexBuiltinCatalog{entries: doc.Models, index: make(map[string]json.RawMessage, len(doc.Models))}
	for _, entry := range doc.Models {
		meta, err := codexCatalogEntryMeta(entry)
		if err != nil {
			return nil, fmt.Errorf("内置清单条目无法解析: %w", err)
		}
		catalog.index[meta.Slug] = entry
	}
	return catalog, nil
}

const codexBuiltinCacheTTL = 5 * time.Minute

var (
	codexBuiltinMu    sync.Mutex
	codexBuiltinCache *codexBuiltinCatalog
	codexBuiltinAt    time.Time
)

// loadCodexBuiltinCatalog 返回内置清单，带进程内缓存。
// 写入操作（upsert / setDefault）必须 force 刷新，避免 codex 升级后抄到陈旧指令。
func loadCodexBuiltinCatalog(ctx context.Context, force bool) (*codexBuiltinCatalog, error) {
	codexBuiltinMu.Lock()
	defer codexBuiltinMu.Unlock()
	if !force && codexBuiltinCache != nil && time.Since(codexBuiltinAt) < codexBuiltinCacheTTL {
		return codexBuiltinCache, nil
	}
	catalog, err := fetchCodexBuiltinCatalog(ctx)
	if err != nil {
		return nil, err
	}
	codexBuiltinCache = catalog
	codexBuiltinAt = time.Now()
	return catalog, nil
}
