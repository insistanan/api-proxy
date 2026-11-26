package intent

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

type LocalCache struct {
	TotalBytes     int64 `json:"totalBytes"`
	UnusedBytes    int64 `json:"unusedBytes"`
	UnusedEntries  int   `json:"unusedEntries"`
	RemovedBytes   int64 `json:"removedBytes"`
	RemovedEntries int   `json:"removedEntries"`
}

// LocalCache 只统计、清理内容寻址的托管资源。当前清单始终受保护，操作期间禁止安装和启动。
func (l *LocalRuntime) LocalCache(ctx context.Context, cleanup bool) (result LocalCache, err error) {
	l.mu.Lock()
	if l.busy || cleanup && l.cmd != nil {
		l.mu.Unlock()
		return result, fmt.Errorf("请等待当前操作完成，并在清理前停止本地助手")
	}
	manifest := l.manifest
	l.busy = true
	l.mu.Unlock()
	defer func() { l.mu.Lock(); l.busy = false; l.mu.Unlock() }()
	root, err := os.OpenRoot(l.dir)
	if errors.Is(err, os.ErrNotExist) {
		return result, nil
	}
	if err != nil {
		return result, err
	}
	defer root.Close()
	entries, err := fs.ReadDir(root.FS(), ".")
	if err != nil {
		return result, err
	}
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		name := entry.Name()
		hash := strings.TrimSuffix(strings.TrimSuffix(name, ".archive"), ".gguf")
		if len(hash) != 64 || name != hash && name != hash+".archive" && name != hash+".gguf" {
			continue
		}
		if _, err := hex.DecodeString(hash); err != nil {
			continue
		}
		if entry.Type()&os.ModeSymlink != 0 {
			continue
		}
		if name == hash && !entry.IsDir() || name != hash && !entry.Type().IsRegular() {
			continue
		}
		var size int64
		if err := fs.WalkDir(root.FS(), name, func(_ string, item fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if err := ctx.Err(); err != nil {
				return err
			}
			if item.Type().IsRegular() {
				info, err := item.Info()
				if err != nil {
					return err
				}
				size += info.Size()
			}
			return nil
		}); err != nil {
			return result, fmt.Errorf("统计缓存 %s 失败: %w", name, err)
		}
		result.TotalBytes += size
		if strings.EqualFold(hash, manifest.ModelSHA256) || strings.EqualFold(hash, manifest.RuntimeSHA256) {
			continue
		}
		result.UnusedBytes += size
		result.UnusedEntries++
		if cleanup {
			// Root 将操作约束在已打开的目录内，RemoveAll 不跟随符号链接。
			if err := root.RemoveAll(filepath.Clean(name)); err != nil {
				return result, fmt.Errorf("清理缓存 %s 未完整完成，部分文件可能已删除，请刷新核对: %w", name, err)
			}
			result.RemovedBytes += size
			result.RemovedEntries++
		}
	}
	result.TotalBytes -= result.RemovedBytes
	result.UnusedBytes -= result.RemovedBytes
	result.UnusedEntries -= result.RemovedEntries
	return result, nil
}
