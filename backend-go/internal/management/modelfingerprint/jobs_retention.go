package modelfingerprint

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
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
		err := j.pruneFiles()
		j.mu.Lock()
		previous := j.retentionErr
		j.retentionErr = err
		j.mu.Unlock()
		if err != nil && (previous == nil || previous.Error() != err.Error()) {
			log.Printf("[Fingerprint] 后台清理失败: %v", err)
		} else if err == nil && previous != nil {
			log.Printf("[Fingerprint] 后台清理已恢复")
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
		if !entry.Type().IsRegular() || !strings.HasSuffix(name, ".json") {
			continue
		}
		if _, ok := taskFileStripe(name); !ok {
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
		first, _ := taskFileStripe(file.Name())
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
			if filepath.Base(j.taskPath(id)) == file.Name() || id+".json" == file.Name() {
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

// 旧 UUID 文件只读兼容，和新文件共同受份数及 30 天期限约束。
// 同一渠道的两种文件名必须使用同一把磁盘锁。
func taskFileStripe(name string) (byte, bool) {
	id := strings.TrimSuffix(name, ".json")
	if len(id) == 64 {
		digest, err := hex.DecodeString(id)
		if err == nil {
			return digest[0], true
		}
	}
	if len(id) == 36 {
		if _, err := uuid.Parse(id); err == nil {
			digest := sha256.Sum256([]byte(id))
			return digest[0], true
		}
	}
	return 0, false
}
