package intent

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"insistanan/api-proxy/internal/storage/atomicfile"
)

// LocalManifest 由管理员提供已核对的 CPU 运行时及小型 GGUF 向量模型。
// 运行时压缩包可含配套动态库；主网关仍为纯 Go 单二进制。
type LocalManifest struct {
	RuntimeFormat  string   `json:"runtimeFormat,omitempty"`
	RuntimeMirrors []string `json:"runtimeMirrors,omitempty"`
	ModelMirrors   []string `json:"modelMirrors,omitempty"`
	RuntimeURL     string   `json:"runtimeUrl"`
	RuntimeSHA256  string   `json:"runtimeSha256"`
	Executable     string   `json:"executable"`
	ModelURL       string   `json:"modelUrl"`
	ModelSHA256    string   `json:"modelSha256"`
	ModelName      string   `json:"modelName"`
	Pooling        string   `json:"pooling"`
}

type LocalStatus struct {
	DownloadedBytes int64  `json:"downloadedBytes"`
	TotalBytes      int64  `json:"totalBytes"`
	Notice          string `json:"notice,omitempty"`

	Installed   bool          `json:"installed"`
	Running     bool          `json:"running"`
	Ready       bool          `json:"ready"`
	Busy        bool          `json:"busy"`
	Error       string        `json:"error"`
	Platform    string        `json:"platform"`
	InstallPath string        `json:"installPath"`
	PresetsURL  string        `json:"presetsUrl"`
	Phase       string        `json:"phase"`
	Manifest    LocalManifest `json:"manifest"`
}

type LocalRuntime struct {
	mu              sync.Mutex
	dir             string
	manifest        LocalManifest
	cmd             *exec.Cmd
	done            chan struct{}
	endpoint        string
	ready           bool
	stopping        bool
	busy            bool
	lastError       string
	downloadedBytes int64
	totalBytes      int64
	notice          string
	phase           string
	presets         *presetCatalog
	queue           chan struct{}
	active          chan struct{}
	client          *http.Client
}

func NewLocal(dir string) (*LocalRuntime, error) {
	absDir, err := filepath.Abs(dir)
	if err != nil {
		return nil, fmt.Errorf("解析本地向量安装路径失败: %w", err)
	}
	l := &LocalRuntime{dir: absDir, phase: "idle", presets: newPresetCatalog(), queue: make(chan struct{}, 3), active: make(chan struct{}, 1), client: &http.Client{Timeout: 10 * time.Second, Transport: &http.Transport{Proxy: nil, MaxConnsPerHost: 1, MaxIdleConns: 1, IdleConnTimeout: 60 * time.Second}}}
	l.presets.loadURL(absDir)
	data, err := os.ReadFile(filepath.Join(l.dir, "manifest.json"))
	if errors.Is(err, os.ErrNotExist) {
		return l, nil
	}
	if err != nil {
		return nil, fmt.Errorf("读取本地向量配置失败: %w", err)
	}
	if err := json.Unmarshal(data, &l.manifest); err != nil {
		return nil, err
	}
	if err := validateManifest(l.manifest); err != nil {
		return nil, err
	}
	l.phase = "installed"
	return l, nil
}

func validateManifest(m LocalManifest) error {
	if m.RuntimeFormat != "" && m.RuntimeFormat != "zip" && m.RuntimeFormat != "tar.gz" {
		return fmt.Errorf("运行时格式必须为 zip 或 tar.gz")
	}
	if len(m.RuntimeMirrors) > 3 || len(m.ModelMirrors) > 3 {
		return fmt.Errorf("每个资源最多配置三个镜像源")
	}
	urls := append([]string{m.RuntimeURL, m.ModelURL}, m.RuntimeMirrors...)
	urls = append(urls, m.ModelMirrors...)
	for _, raw := range urls {
		if err := validateHTTPS(raw); err != nil {
			return fmt.Errorf("下载%s", err)
		}
	}
	for _, hash := range []string{m.RuntimeSHA256, m.ModelSHA256} {
		decoded, err := hex.DecodeString(hash)
		if err != nil || len(decoded) != 32 {
			return fmt.Errorf("需要完整 SHA-256 校验值")
		}
	}
	if !filepath.IsLocal(m.Executable) || strings.Contains(m.Executable, ":") || !strings.HasPrefix(filepath.Base(m.Executable), "llama-server") {
		return fmt.Errorf("可执行文件必须是压缩包内的 llama-server 相对路径")
	}
	if m.ModelName == "" || len(m.ModelName) > 100 {
		return fmt.Errorf("请填写模型名称")
	}
	if m.Pooling != "cls" && m.Pooling != "mean" && m.Pooling != "last" {
		return fmt.Errorf("池化方式必须为 cls、mean 或 last")
	}
	return nil
}

func (l *LocalRuntime) Status() LocalStatus {
	l.mu.Lock()
	defer l.mu.Unlock()
	return LocalStatus{Installed: l.manifest.ModelSHA256 != "", Running: l.cmd != nil, Ready: l.ready, Busy: l.busy,
		DownloadedBytes: l.downloadedBytes, TotalBytes: l.totalBytes, Notice: l.notice,
		Error: l.lastError, Platform: runtime.GOOS + "/" + runtime.GOARCH, Manifest: l.manifest,
		InstallPath: l.dir, PresetsURL: l.presets.currentURL(), Phase: l.phase}
}

func (l *LocalRuntime) setPhase(phase string) {
	l.mu.Lock()
	l.phase = phase
	l.downloadedBytes, l.totalBytes = 0, 0
	l.mu.Unlock()
}

func (l *LocalRuntime) Space() (string, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if !l.ready {
		return "", fmt.Errorf("本地向量助手尚未启动或预热未完成")
	}
	return digest("bounded-chunks-v1|" + l.manifest.ModelSHA256 + "|" + l.manifest.RuntimeSHA256 + "|" + l.manifest.Pooling), nil
}

func (l *LocalRuntime) installedFilesPresent(manifest LocalManifest) (bool, error) {
	for _, path := range []string{
		filepath.Join(l.dir, strings.ToLower(manifest.RuntimeSHA256), manifest.Executable),
		filepath.Join(l.dir, strings.ToLower(manifest.ModelSHA256)+".gguf"),
	} {
		info, err := os.Stat(path)
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		if err != nil {
			return false, fmt.Errorf("检查本地资源失败: %w", err)
		}
		if !info.Mode().IsRegular() || info.Size() == 0 {
			return false, fmt.Errorf("本地资源不是有效文件：%s", path)
		}
	}
	return true, nil
}

func (l *LocalRuntime) Install(ctx context.Context, manifest LocalManifest) (err error) {
	if err := validateManifest(manifest); err != nil {
		return err
	}
	l.mu.Lock()
	if l.busy || l.cmd != nil {
		l.mu.Unlock()
		return fmt.Errorf("请先停止本地助手并等待当前操作完成")
	}
	l.busy, l.lastError = true, ""
	l.notice = ""
	l.downloadedBytes, l.totalBytes = 0, 0
	l.phase = "downloading_runtime"
	l.mu.Unlock()
	defer func() {
		l.mu.Lock()
		l.busy = false
		if err != nil {
			l.lastError = err.Error()
			l.phase = "failed"
		} else {
			l.phase = "installed"
		}
		l.mu.Unlock()
	}()
	ctx, cancel := context.WithTimeout(ctx, 15*time.Minute)
	defer cancel()
	archivePath := filepath.Join(l.dir, strings.ToLower(manifest.RuntimeSHA256)+".archive")
	if err := l.downloadResource(ctx, append([]string{manifest.RuntimeURL}, manifest.RuntimeMirrors...), manifest.RuntimeSHA256, 128<<20, archivePath, ""); err != nil {
		return err
	}
	runtimeDir := filepath.Join(l.dir, strings.ToLower(manifest.RuntimeSHA256))
	l.setPhase("extracting_runtime")
	manifest.Executable, err = extractRuntime(ctx, archivePath, runtimeDir, manifest.RuntimeFormat, manifest.Executable)
	if err != nil {
		return err
	}
	modelPath := filepath.Join(l.dir, strings.ToLower(manifest.ModelSHA256)+".gguf")
	l.setPhase("downloading_model")
	if err := l.downloadResource(ctx, append([]string{manifest.ModelURL}, manifest.ModelMirrors...), manifest.ModelSHA256, 1<<30, modelPath, "GGUF"); err != nil {
		return err
	}
	data, err := json.Marshal(manifest)
	if err != nil {
		return err
	}
	if err := atomicfile.Write(filepath.Join(l.dir, "manifest.json"), data, atomicfile.Options{Mode: 0600}); err != nil {
		if atomicfile.IsCommitted(err) {
			l.mu.Lock()
			l.manifest = manifest
			l.mu.Unlock()
		}
		return err
	}
	l.mu.Lock()
	l.manifest = manifest
	l.mu.Unlock()
	return nil
}

func downloadVerified(ctx context.Context, rawURL, expected string, limit int64, dst io.Writer, magic string, verifying ...func()) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return err
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.ResponseHeaderTimeout = 30 * time.Second
	defer transport.CloseIdleConnections()
	client := &http.Client{Timeout: 10 * time.Minute, Transport: transport, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) > 8 || req.URL.Scheme != "https" || req.URL.User != nil {
			return fmt.Errorf("下载重定向无效")
		}
		return nil
	}}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("下载失败: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("下载返回 HTTP %d", resp.StatusCode)
	}
	if resp.ContentLength > limit {
		return fmt.Errorf("下载文件超过大小限制")
	}
	if progress, ok := dst.(*downloadProgress); ok {
		progress.total(resp.ContentLength)
	}
	return copyVerified(resp.Body, expected, limit, dst, magic, verifying...)
}

func copyVerified(src io.Reader, expected string, limit int64, dst io.Writer, magic string, verifying ...func()) error {
	header := make([]byte, len(magic))
	if _, err := io.ReadFull(src, header); err != nil {
		return fmt.Errorf("读取下载文件头失败: %w", err)
	}
	if string(header) != magic {
		return fmt.Errorf("下载内容不是 %s 文件", magic)
	}
	hash := sha256.New()
	reader := io.MultiReader(bytes.NewReader(header), src)
	n, err := io.Copy(io.MultiWriter(dst, hash), io.LimitReader(reader, limit+1))
	if err != nil {
		return fmt.Errorf("写入下载内容失败: %w", err)
	}
	if n > limit {
		return fmt.Errorf("下载文件超过大小限制")
	}
	for _, notify := range verifying {
		notify()
	}
	if !strings.EqualFold(hex.EncodeToString(hash.Sum(nil)), expected) {
		return fmt.Errorf("SHA-256 校验不匹配，未安装文件")
	}
	return nil
}

func (l *LocalRuntime) Start(ctx context.Context) (err error) {
	l.mu.Lock()
	if l.busy || l.cmd != nil {
		l.mu.Unlock()
		return fmt.Errorf("本地助手已运行或正在操作")
	}
	if l.manifest.ModelSHA256 == "" {
		l.mu.Unlock()
		return fmt.Errorf("请先下载并校验运行时和模型")
	}
	m := l.manifest
	l.busy, l.lastError = true, ""
	l.phase = "starting"
	l.mu.Unlock()
	defer func() {
		l.mu.Lock()
		l.busy = false
		if err != nil {
			l.lastError = err.Error()
			l.phase = "failed"
		} else {
			l.phase = "ready"
		}
		l.mu.Unlock()
	}()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return err
	}
	port := listener.Addr().(*net.TCPAddr).Port
	if err := listener.Close(); err != nil {
		return err
	}
	executable, err := filepath.Abs(filepath.Join(l.dir, strings.ToLower(m.RuntimeSHA256), m.Executable))
	if err != nil {
		return err
	}
	modelPath, err := filepath.Abs(filepath.Join(l.dir, strings.ToLower(m.ModelSHA256)+".gguf"))
	if err != nil {
		return err
	}
	cmd := exec.Command(executable, "--model", modelPath, "--alias", m.ModelName, "--embedding", "--pooling", m.Pooling,
		"--host", "127.0.0.1", "--port", fmt.Sprint(port), "--threads", "2", "--threads-batch", "2", "--parallel", "1",
		"--ctx-size", "512", "--batch-size", "512", "--ubatch-size", "512", "--n-gpu-layers", "0", "--cache-ram", "0")
	cmd.Dir = filepath.Dir(executable)
	diagnostic := &processDiagnostic{}
	cmd.Stderr = diagnostic
	hideProcessWindow(cmd)
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("启动本地助手失败: %w", err)
	}
	done := make(chan struct{})
	l.mu.Lock()
	l.cmd, l.done, l.endpoint = cmd, done, fmt.Sprintf("http://127.0.0.1:%d/v1/embeddings", port)
	l.mu.Unlock()
	go func() {
		waitErr := cmd.Wait()
		l.mu.Lock()
		l.cmd, l.ready = nil, false
		if waitErr != nil && !l.stopping {
			l.lastError = "本地助手已退出: " + waitErr.Error() + "；" + diagnostic.String()
			l.phase = "failed"
		} else if !l.busy {
			l.phase = "stopped"
		}
		l.stopping = false
		l.mu.Unlock()
		close(done)
	}()
	warmCtx, cancel := context.WithTimeout(ctx, 90*time.Second)
	l.setPhase("warming")
	defer cancel()
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	var lastWarmupError error
	for {
		select {
		case <-done:
			return fmt.Errorf("本地助手预热前退出，请检查 CPU、系统库和模型池化方式：%s", diagnostic.String())
		case <-warmCtx.Done():
			return errors.Join(fmt.Errorf("本地助手预热失败: %w", warmCtx.Err()), lastWarmupError, l.Stop())
		case <-ticker.C:
			if _, callErr := l.postEmbedding(warmCtx, []string{"向量模型预热"}); callErr == nil {
				l.mu.Lock()
				ready := l.cmd == cmd && !l.stopping && warmCtx.Err() == nil
				l.ready = ready
				l.mu.Unlock()
				if !ready {
					return errors.Join(fmt.Errorf("本地助手预热结束时已停止或取消"), l.Stop())
				}
				return nil
			} else {
				lastWarmupError = callErr
			}
		}
	}
}

func (l *LocalRuntime) Stop() error {
	l.client.CloseIdleConnections()
	l.mu.Lock()
	cmd, done := l.cmd, l.done
	if cmd == nil && l.busy {
		l.mu.Unlock()
		return fmt.Errorf("本地助手正在安装或启动，请等待当前操作完成后停止")
	}
	l.ready = false
	if !l.busy {
		l.phase = "stopped"
	}
	if cmd != nil {
		l.stopping = true
	}
	l.mu.Unlock()
	if cmd == nil {
		return nil
	}
	if err := cmd.Process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
		return err
	}
	select {
	case <-done:
		return nil
	case <-time.After(10 * time.Second):
		return fmt.Errorf("等待本地助手退出超时")
	}
}

func (l *LocalRuntime) Embed(ctx context.Context, inputs []string) ([][]float64, error) {
	if _, err := l.Space(); err != nil {
		return nil, err
	}
	select {
	case l.queue <- struct{}{}:
		defer func() { <-l.queue }()
	default:
		return nil, fmt.Errorf("本地向量等待队列已满（最多 1 执行、2 等待）")
	}
	select {
	case l.active <- struct{}{}:
		defer func() { <-l.active }()
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	// 小型 CPU 模型逐条运行，避免一个千条例句批次撑大 KV 和工作内存。
	result := make([][]float64, 0, len(inputs))
	for _, input := range inputs {
		var combined []float64
		for _, chunk := range embeddingChunks(input) {
			vectors, err := l.postEmbedding(ctx, []string{chunk})
			if err != nil {
				return nil, err
			}
			vector := vectors[0]
			if combined == nil {
				combined = make([]float64, len(vector))
			}
			if len(combined) != len(vector) {
				return nil, fmt.Errorf("分段向量维度发生变化")
			}
			weight := float64(utf8.RuneCountInString(chunk))
			for i, value := range vector {
				combined[i] += weight * value
			}
		}
		norm := 0.0
		for _, value := range combined {
			norm += value * value
		}
		if norm == 0 || math.IsNaN(norm) || math.IsInf(norm, 0) {
			return nil, fmt.Errorf("分段向量聚合无效，请检查输入和向量模型")
		}
		for i := range combined {
			combined[i] /= math.Sqrt(norm)
		}
		result = append(result, combined)
	}
	return result, nil
}

// 以 UTF-8 字节预算约束小型模型输入，给特殊 token 留出余量，不依赖未知的 tokenizer。
// 每段保留全部字符，查询和样本共用相同的长度加权聚合，并纳入向量空间版本。
func embeddingChunks(input string) []string {
	const budget = 384
	chunks := []string{}
	for len(input) > budget {
		end := budget
		for !utf8.RuneStart(input[end]) {
			end--
		}
		chunks = append(chunks, input[:end])
		input = input[end:]
	}
	if input != "" {
		chunks = append(chunks, input)
	}
	return chunks
}

func (l *LocalRuntime) postEmbedding(ctx context.Context, inputs []string) ([][]float64, error) {
	l.mu.Lock()
	endpoint, model := l.endpoint, l.manifest.ModelName
	l.mu.Unlock()
	body, err := json.Marshal(map[string]any{"model": model, "input": inputs, "encoding_format": "float"})
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	response, err := l.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("本地向量助手返回 HTTP %d；请检查输入长度和模型池化方式", response.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, (1<<20)+1))
	if err != nil {
		return nil, err
	}
	if len(data) > 1<<20 {
		return nil, fmt.Errorf("本地向量响应超限")
	}
	return DecodeEmbeddings(data, len(inputs))
}
