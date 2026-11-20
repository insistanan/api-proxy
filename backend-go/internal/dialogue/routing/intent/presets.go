package intent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"time"

	apiproxy "github.com/insistanan/api-proxy"
	"github.com/insistanan/api-proxy/internal/storage/atomicfile"
)

// 空地址明确选择随二进制附带的推荐清单；自定义 HTTPS 地址失败仍显式报错。
const DefaultPresetsURL = ""
const presetBodyLimit = 256 << 10
const presetCacheTTL = 10 * time.Minute
const presetsURLFile = "presets-url.txt"

type EmbeddingPresets struct {
	Schema  string            `json:"$schema,omitempty"`
	Version int               `json:"version"`
	Presets []EmbeddingPreset `json:"presets"`
}

type EmbeddingPreset struct {
	ID          string                   `json:"id"`
	Name        string                   `json:"name"`
	Description string                   `json:"description"`
	SizeBytes   int64                    `json:"sizeBytes"`
	Platforms   map[string]LocalManifest `json:"platforms"`
}

// 页面只取得当前平台的公开推荐信息，安装参数始终由后端重新解析。
type PresetOption struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	SizeBytes   int64  `json:"sizeBytes"`
}

type PresetsStatus struct {
	Version           int            `json:"version"`
	Presets           []PresetOption `json:"presets"`
	PresetsURL        string         `json:"presetsUrl"`
	DefaultPresetsURL string         `json:"defaultPresetsUrl"`
	InstallPath       string         `json:"installPath"`
	Platform          string         `json:"platform"`
	Error             string         `json:"error"`
	Stale             bool           `json:"stale"`
}

type presetCatalog struct {
	mu        sync.Mutex
	url       string
	urlFile   string
	loadError string
	client    *http.Client
	manifest  EmbeddingPresets
	fetchedAt time.Time
}

func newPresetCatalog() *presetCatalog {
	return &presetCatalog{url: DefaultPresetsURL, client: &http.Client{
		Timeout: 10 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 8 {
				return fmt.Errorf("预设清单重定向次数超限")
			}
			return validateHTTPS(req.URL.String())
		},
	}}
}

// loadURL 读取用户保存的清单地址；损坏或非 HTTPS 时保留默认地址并显式回报，
// 不因为一个可选的清单地址让网关启动失败。
func (p *presetCatalog) loadURL(dir string) {
	p.urlFile = filepath.Join(dir, presetsURLFile)
	data, err := os.ReadFile(p.urlFile)
	if errors.Is(err, os.ErrNotExist) {
		return
	}
	if err != nil {
		p.loadError = fmt.Sprintf("读取清单地址失败，已使用默认地址：%v", err)
		return
	}
	raw := strings.TrimSpace(string(data))
	if raw == "" {
		return
	}
	if err := validateHTTPS(raw); err != nil {
		p.loadError = fmt.Sprintf("保存的清单地址无效，已使用默认地址：%v", err)
		return
	}
	p.url = raw
}

func (p *presetCatalog) currentURL() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.url
}

// SetPresetsURL 保存自定义清单地址；空字符串恢复默认地址。
func (l *LocalRuntime) SetPresetsURL(raw string) error {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		raw = DefaultPresetsURL
	}
	if raw != DefaultPresetsURL {
		if err := validateHTTPS(raw); err != nil {
			return fmt.Errorf("清单地址无效：%w", err)
		}
	}
	p := l.presets
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.urlFile == "" {
		return fmt.Errorf("清单地址存储尚未初始化")
	}
	var writeErr error
	if raw == DefaultPresetsURL {
		if err := os.Remove(p.urlFile); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("恢复默认清单地址失败：%w", err)
		}
	} else {
		writeErr = atomicfile.Write(p.urlFile, []byte(raw), atomicfile.Options{Mode: 0o600})
		if writeErr != nil && !atomicfile.IsCommitted(writeErr) {
			return fmt.Errorf("保存清单地址失败：%w", writeErr)
		}
	}
	p.url = raw
	p.manifest = EmbeddingPresets{}
	p.fetchedAt = time.Time{}
	p.loadError = ""
	return writeErr
}

func validateHTTPS(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.Fragment != "" {
		return fmt.Errorf("地址必须为不含凭据或片段的 HTTPS URL")
	}
	return nil
}

func validatePresets(data []byte) (EmbeddingPresets, error) {
	var manifest EmbeddingPresets
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&manifest); err != nil {
		return manifest, fmt.Errorf("预设清单 JSON 无效: %w", err)
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return manifest, fmt.Errorf("预设清单必须是单个 JSON 对象")
	}
	if manifest.Version != 1 || manifest.Presets == nil {
		return manifest, fmt.Errorf("预设清单需要 version=1 和 presets 数组")
	}
	idPattern := regexp.MustCompile(`^[a-zA-Z0-9_-]{1,64}$`)
	platformPattern := regexp.MustCompile(`^[a-z0-9]+/[a-z0-9]+$`)
	ids := make(map[string]bool)
	for _, preset := range manifest.Presets {
		if !idPattern.MatchString(preset.ID) || ids[preset.ID] || preset.Name == "" || len(preset.Name) > 200 || preset.Description == "" || len(preset.Description) > 2000 || preset.SizeBytes <= 0 || len(preset.Platforms) == 0 {
			return manifest, fmt.Errorf("预设 %q 的标识、名称、说明、大小或平台项无效", preset.ID)
		}
		ids[preset.ID] = true
		for platform, entry := range preset.Platforms {
			if !platformPattern.MatchString(platform) {
				return manifest, fmt.Errorf("预设 %s 的平台键 %q 无效", preset.ID, platform)
			}
			if err := validateManifest(entry); err != nil {
				return manifest, fmt.Errorf("预设 %s (%s) 无效: %w", preset.ID, platform, err)
			}
		}
	}
	return manifest, nil
}

func (p *presetCatalog) fetch(ctx context.Context) (EmbeddingPresets, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.fetchedAt.IsZero() && time.Since(p.fetchedAt) < presetCacheTTL {
		return p.manifest, nil
	}
	manifest, err := p.download(ctx)
	if err != nil {
		// 上一次成功结果仍可展示，但错误显式返回，安装不会使用失败的刷新结果。
		return p.manifest, fmt.Errorf("拉取发布预设清单失败: %w", err)
	}
	p.manifest, p.fetchedAt = manifest, time.Now()
	return manifest, nil
}

func (p *presetCatalog) download(ctx context.Context) (EmbeddingPresets, error) {
	if p.url == DefaultPresetsURL {
		data, err := apiproxy.FrontendFS.ReadFile("frontend/dist/embedding-presets.json")
		if err != nil {
			return EmbeddingPresets{}, fmt.Errorf("读取内置向量预设清单失败，请通过项目复制脚本重新构建: %w", err)
		}
		return validatePresets(data)
	}
	if err := validateHTTPS(p.url); err != nil {
		return EmbeddingPresets{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.url, nil)
	if err != nil {
		return EmbeddingPresets{}, err
	}
	resp, err := p.client.Do(req)
	if err != nil {
		return EmbeddingPresets{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return EmbeddingPresets{}, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	if resp.ContentLength > presetBodyLimit {
		return EmbeddingPresets{}, fmt.Errorf("清单超过 256 KiB")
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, presetBodyLimit+1))
	if err != nil {
		return EmbeddingPresets{}, fmt.Errorf("读取清单失败: %w", err)
	}
	if len(data) > presetBodyLimit {
		return EmbeddingPresets{}, fmt.Errorf("清单超过 256 KiB")
	}
	return validatePresets(data)
}

func (l *LocalRuntime) Presets(ctx context.Context) PresetsStatus {
	manifest, err := l.presets.fetch(ctx)
	l.presets.mu.Lock()
	url, loadError := l.presets.url, l.presets.loadError
	l.presets.mu.Unlock()
	result := PresetsStatus{Version: manifest.Version, Presets: []PresetOption{}, PresetsURL: url, DefaultPresetsURL: DefaultPresetsURL,
		InstallPath: l.dir, Platform: runtime.GOOS + "/" + runtime.GOARCH}
	if err != nil {
		result.Error, result.Stale = err.Error(), manifest.Version != 0
	} else if loadError != "" {
		result.Error = loadError
	}
	for _, preset := range manifest.Presets {
		if _, ok := preset.Platforms[result.Platform]; ok {
			result.Presets = append(result.Presets, PresetOption{preset.ID, preset.Name, preset.Description, preset.SizeBytes})
		}
	}
	return result
}

func (l *LocalRuntime) InstallPreset(ctx context.Context, id string) error {
	manifest, err := l.presets.fetch(ctx)
	if err != nil {
		return err
	}
	for _, preset := range manifest.Presets {
		if preset.ID != id {
			continue
		}
		entry, ok := preset.Platforms[runtime.GOOS+"/"+runtime.GOARCH]
		if !ok {
			return fmt.Errorf("预设 %q 不支持当前平台", id)
		}
		if err := l.Install(ctx, entry); err != nil {
			return fmt.Errorf("安装预设失败: %w", err)
		}
		return l.Start(ctx)
	}
	return fmt.Errorf("发布清单中没有预设 %q", id)
}
