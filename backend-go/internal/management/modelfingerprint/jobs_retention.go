package modelfingerprint

import (
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

func (j *Jobs) activeLocked() int {
	count := 0
	for _, current := range j.tasks {
		if current.cancel != nil {
			count++
		}
	}
	return count
}

func (j *Jobs) makeRoomLocked() {
	for len(j.tasks) >= maxCachedTasks {
		oldestID := ""
		var oldest time.Time
		for id, current := range j.tasks {
			if current.cancel == nil && (oldest.IsZero() || current.task.UpdatedAt.Before(oldest)) {
				oldestID, oldest = id, current.task.UpdatedAt
			}
		}
		if oldestID == "" {
			return
		}
		delete(j.tasks, oldestID)
	}
}

func (j *Jobs) retentionLoop() {
	defer close(j.done)
	ticker := time.NewTicker(time.Hour)
	defer ticker.Stop()
	for {
		select {
		case <-j.stop:
			return
		case <-ticker.C:
		case <-j.pruneWake:
		}
		if err := j.pruneFiles(); err != nil {
			j.mu.Lock()
			j.closeErr = errors.Join(j.closeErr, err)
			j.mu.Unlock()
		}
	}
}

func (j *Jobs) wakeRetention() {
	select {
	case j.pruneWake <- struct{}{}:
	default:
	}
}

func (j *Jobs) pruneFiles() error {
	entries, err := os.ReadDir(j.dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("读取指纹任务保留目录失败: %w", err)
	}
	var files []os.FileInfo
	for _, entry := range entries {
		name := entry.Name()
		if !entry.Type().IsRegular() || !strings.HasSuffix(name, ".json") || len(name) != 69 {
			continue
		}
		if _, err := hex.DecodeString(strings.TrimSuffix(name, ".json")); err != nil {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		files = append(files, info)
	}
	sort.Slice(files, func(i, k int) bool { return files[i].ModTime().After(files[k].ModTime()) })
	for index, file := range files {
		if index < maxStoredTasks && time.Since(file.ModTime()) < taskRetention {
			continue
		}
		var first byte
		if _, err := fmt.Sscanf(file.Name()[:2], "%02x", &first); err != nil {
			continue
		}
		disk := &j.diskMu[int(first)%len(j.diskMu)]
		disk.Lock()
		// 扫描后文件可能刚被重写，旧扫描结果不能删除新的检测结果。
		path := filepath.Join(j.dir, file.Name())
		latest, statErr := os.Lstat(path)
		if errors.Is(statErr, os.ErrNotExist) || statErr == nil && (!latest.Mode().IsRegular() || !latest.ModTime().Equal(file.ModTime())) {
			disk.Unlock()
			continue
		}
		if statErr != nil {
			disk.Unlock()
			return fmt.Errorf("检查指纹任务保留文件失败: %w", statErr)
		}
		j.mu.Lock()
		active := false
		for id, current := range j.tasks {
			if filepath.Base(j.taskPath(id)) == file.Name() {
				active = current.cancel != nil
				if !active {
					delete(j.tasks, id)
				}
				break
			}
		}
		j.mu.Unlock()
		if !active {
			err = os.Remove(path)
		}
		disk.Unlock()
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("清理指纹任务失败: %w", err)
		}
	}
	return nil
}
