package intent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"insistanan/api-proxy/internal/storage/atomicfile"
)

// ImportResource 只接收当前平台预设的原始资源，校验后写入内容寻址缓存。
// 不接受客户端文件路径，也不自动执行导入的运行时。
func (l *LocalRuntime) ImportResource(ctx context.Context, presetID, resource string, src io.Reader) (err error) {
	manifest, err := l.presetManifest(ctx, presetID)
	if err != nil {
		return err
	}
	expected, suffix, magic, limit := "", "", "", int64(0)
	switch resource {
	case "runtime":
		expected, suffix, limit = manifest.RuntimeSHA256, ".archive", 128<<20
	case "model":
		expected, suffix, magic, limit = manifest.ModelSHA256, ".gguf", "GGUF", 1<<30
	default:
		return fmt.Errorf("请选择运行时或向量模型资源")
	}
	l.mu.Lock()
	if l.busy || l.cmd != nil {
		l.mu.Unlock()
		return fmt.Errorf("请先停止本地助手并等待当前操作完成")
	}
	l.busy, l.lastError, l.notice = true, "", ""
	l.phase, l.downloadedBytes, l.totalBytes = "importing", 0, 0
	l.mu.Unlock()
	defer func() {
		l.mu.Lock()
		defer l.mu.Unlock()
		l.busy = false
		if err != nil {
			l.phase, l.lastError = "failed", err.Error()
		} else {
			l.phase, l.notice = "imported", "离线资源已校验并缓存，请继续一键准备"
		}
	}()
	path := filepath.Join(l.dir, strings.ToLower(expected)+suffix)
	return atomicfile.WriteFunc(path, func(w io.Writer) error {
		return copyVerified(contextReader{ctx: ctx, reader: src}, expected, limit, &downloadProgress{writer: w, local: l}, magic)
	}, atomicfile.Options{Mode: 0600})
}

type downloadProgress struct {
	writer io.Writer
	local  *LocalRuntime
}

func (p *downloadProgress) total(total int64) {
	p.local.mu.Lock()
	p.local.downloadedBytes, p.local.totalBytes = 0, max(0, total)
	p.local.mu.Unlock()
}

func (p *downloadProgress) Write(data []byte) (int, error) {
	n, err := p.writer.Write(data)
	p.local.mu.Lock()
	p.local.downloadedBytes += int64(n)
	p.local.mu.Unlock()
	return n, err
}

type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r contextReader) Read(data []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(data)
}

func (l *LocalRuntime) downloadResource(ctx context.Context, urls []string, expected string, limit int64, path, magic string) error {
	file, err := os.Open(path)
	if err == nil {
		hash := sha256.New()
		n, readErr := io.Copy(hash, io.LimitReader(contextReader{ctx, file}, limit+1))
		closeErr := file.Close()
		if readErr != nil || closeErr != nil {
			return errors.Join(readErr, closeErr)
		}
		if n <= limit && strings.EqualFold(hex.EncodeToString(hash.Sum(nil)), expected) {
			l.mu.Lock()
			l.downloadedBytes, l.totalBytes = n, n
			l.notice = "已校验并复用本地缓存"
			l.mu.Unlock()
			return nil
		}
		l.mu.Lock()
		l.notice = "本地缓存校验未通过，正在重新下载"
		l.mu.Unlock()
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("读取下载缓存失败: %w", err)
	}
	var failures []error
	client, err := l.downloadClient(10 * time.Minute)
	if err != nil {
		return err
	}
	defer client.CloseIdleConnections()
	for i, rawURL := range urls {
		if err := ctx.Err(); err != nil {
			return err
		}
		if i > 0 {
			l.mu.Lock()
			l.notice = fmt.Sprintf("下载源失败，正在尝试第 %d 个源；所有源使用同一 SHA-256 校验", i+1)
			l.mu.Unlock()
		}
		err := atomicfile.WriteFunc(path, func(w io.Writer) error {
			return downloadVerified(ctx, client, rawURL, expected, limit, &downloadProgress{w, l}, magic)
		}, atomicfile.Options{Mode: 0600})
		if err == nil {
			return nil
		}
		if atomicfile.IsCommitted(err) {
			return err
		}
		failures = append(failures, fmt.Errorf("下载源 %d 失败: %w", i+1, err))
	}
	return fmt.Errorf("资源下载未完成，可重试或导入离线资源：%w", errors.Join(failures...))
}
