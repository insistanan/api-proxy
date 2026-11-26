package extensions

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"

	"insistanan/api-proxy/internal/clients/filetxn"
	"insistanan/api-proxy/internal/clients/skills"
	"insistanan/api-proxy/internal/storage/atomicfile"
)

type migrationRecord struct {
	Version      int    `json:"version"`
	Source       string `json:"source"`
	Target       string `json:"target"`
	Stage        string `json:"stage"`
	Digest       string `json:"digest"`
	TargetDigest string `json:"targetDigest"`
	Staging      string `json:"staging,omitempty"`
}

func migrationRecordPath(target string) string {
	sum := sha256.Sum256([]byte(target))
	return filepath.Join(filepath.Dir(target), fmt.Sprintf(".skills-migration-%x.json", sum[:8]))
}

func writeMigration(record migrationRecord) error {
	path := migrationRecordPath(record.Target)
	previous, err := filetxn.Read(path)
	if err != nil {
		return err
	}
	raw, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return err
	}
	return filetxn.Replace(path, previous, append(raw, '\n'), "", 0600)
}

func prepareMigration(source, target string) (prepared bool, rollback func() error, resultErr error) {
	digest, err := treeDigest(source, false)
	if err != nil {
		return false, nil, err
	}
	if err := os.MkdirAll(filepath.Dir(target), 0700); err != nil {
		return false, nil, err
	}
	staging, err := os.MkdirTemp(filepath.Dir(target), ".skills-migration-stage-*")
	if err != nil {
		return false, nil, err
	}
	defer func() {
		if err := removeAllForce(staging); err != nil {
			resultErr = errors.Join(resultErr, fmt.Errorf("清理迁移暂存目录 %s 失败: %w", staging, err))
		}
	}()
	record := migrationRecord{Version: 1, Source: source, Target: target, Stage: "copying", Digest: digest, Staging: staging}
	if err := writeMigration(record); err != nil {
		return false, nil, err
	}
	if err := copyDirRecursive(source, staging); err != nil {
		return false, nil, err
	}
	copied, err := treeDigest(staging, true)
	if err != nil {
		return false, nil, err
	}
	current, err := treeDigest(source, false)
	if err != nil {
		return false, nil, err
	}
	if current != digest || copied != digest {
		return false, nil, fmt.Errorf("%w: Skills 来源在迁移期间变化", filetxn.ErrConflict)
	}
	if err := skills.RebaseProjectOwnership(staging, source, target); err != nil {
		return false, nil, err
	}
	record.TargetDigest, err = treeDigest(staging, true)
	if err != nil {
		return false, nil, err
	}
	record.Stage = "publishing"
	if err := writeMigration(record); err != nil {
		return false, nil, err
	}
	if err := atomicfile.RenameDirectory(staging, target); err != nil {
		if atomicfile.IsCommitted(err) {
			return true, nil, &filetxn.CommitError{Files: []string{target}, Err: err}
		}
		return false, nil, fmt.Errorf("发布 Skills 目标 %s 失败，源目录保留完整: %w", target, err)
	}
	record.Stage = "prepared"
	record.Staging = ""
	if err := writeMigration(record); err != nil {
		return true, nil, &filetxn.CommitError{Files: []string{target}, Err: err}
	}
	return true, migrationRollback(record), nil
}

func migrationRollback(record migrationRecord) func() error {
	return func() error {
		target := record.Target
		current, err := treeDigest(target, false)
		if err != nil {
			return err
		}
		if current != record.TargetDigest {
			return fmt.Errorf("目标副本已变化，保留目录供核对: %s", target)
		}
		if err := removeAllForce(target); err != nil {
			return fmt.Errorf("源目录仍完整，清理未启用目标 %s 失败: %w", target, err)
		}
		record.Stage = "aborted"
		return writeMigration(record)
	}
}

func resumePreparedMigration(source, target string) (bool, func() error, error) {
	raw, err := filetxn.Read(migrationRecordPath(target))
	if err != nil {
		return false, nil, err
	}
	if raw == nil {
		return false, nil, nil
	}
	var record migrationRecord
	if err := json.Unmarshal(raw, &record); err != nil {
		return false, nil, err
	}
	if record.Version != 1 || record.Source != source || record.Target != target {
		return false, nil, errors.New("现有迁移记录不匹配，请先核对迁移状态")
	}
	if record.Stage != "prepared" && record.Stage != "publishing" {
		return false, nil, nil
	}
	if _, err := os.Stat(target); errors.Is(err, fs.ErrNotExist) {
		return false, nil, nil
	} else if err != nil {
		return false, nil, err
	}
	before, err := treeDigest(source, false)
	if err != nil {
		return false, nil, err
	}
	after, err := treeDigest(target, false)
	if err != nil {
		return false, nil, err
	}
	if before != record.Digest || after != record.TargetDigest {
		return false, nil, fmt.Errorf("%w: 中断迁移的源或目标已变化，阶段记录: %s", filetxn.ErrConflict, migrationRecordPath(target))
	}
	record.Stage = "prepared"
	if err := writeMigration(record); err != nil {
		return false, nil, err
	}
	return true, migrationRollback(record), nil
}

type MigrationState struct {
	Source     string `json:"source"`
	Target     string `json:"target"`
	Stage      string `json:"stage"`
	RecordPath string `json:"recordPath"`
}

// MigrationStatus 暴露当前目标的持久阶段，供管理页在重启后核对未完成的源清理。
func MigrationStatus(target string) (*MigrationState, error) {
	identity, err := filetxn.Identity(target)
	if err != nil {
		return nil, err
	}
	path := migrationRecordPath(identity)
	raw, err := filetxn.Read(path)
	if err != nil || raw == nil {
		return nil, err
	}
	var record migrationRecord
	if err := json.Unmarshal(raw, &record); err != nil {
		return nil, err
	}
	if record.Version != 1 || record.Target != identity {
		return nil, errors.New("迁移阶段记录无效")
	}
	return &MigrationState{Source: record.Source, Target: record.Target, Stage: record.Stage, RecordPath: path}, nil
}

// FinalizeSkillsMigration 仅在配置已指向 target 后调用；源清理失败不再影响活动目录。
func FinalizeSkillsMigration(source, target string) error {
	source, err := filetxn.Identity(source)
	if err != nil {
		return err
	}
	target, err = filetxn.Identity(target)
	if err != nil {
		return err
	}
	path := migrationRecordPath(target)
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var record migrationRecord
	if err := json.Unmarshal(raw, &record); err != nil {
		return err
	}
	if record.Version != 1 || record.Source != source || record.Target != target || record.Stage != "prepared" {
		return fmt.Errorf("迁移记录与本次操作不匹配: %s", path)
	}
	current, err := treeDigest(source, false)
	if err != nil {
		return err
	}
	if current != record.Digest {
		return fmt.Errorf("原目录已变化，已保留供人工核对: %s", source)
	}
	copied, err := treeDigest(target, false)
	if err != nil {
		return err
	}
	if copied != record.TargetDigest {
		return fmt.Errorf("目标副本已变化，保留原目录供核对: %s", source)
	}
	return finishMigration(record, removeAllForce)
}

func finishMigration(record migrationRecord, removeSource func(string) error) error {
	record.Stage = "configured"
	if err := writeMigration(record); err != nil {
		return err
	}
	if err := removeSource(record.Source); err != nil {
		return fmt.Errorf("配置已使用完整目标 %s；清理原目录 %s 失败，恢复阶段记录在 %s: %w", record.Target, record.Source, migrationRecordPath(record.Target), err)
	}
	if err := atomicfile.SyncDirectory(filepath.Dir(record.Source)); err != nil {
		return fmt.Errorf("原目录已清理，但同步父目录失败: %w", err)
	}
	record.Stage = "complete"
	return writeMigration(record)
}

func treeDigest(root string, syncDirectories bool) (string, error) {
	hash := sha256.New()
	var directories []string
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if entry.IsDir() {
			directories = append(directories, path)
			fmt.Fprintf(hash, "D:%s\x00", filepath.ToSlash(rel))
			return nil
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("迁移不支持特殊文件: %s", path)
		}
		file, err := os.Open(path)
		if err != nil {
			return err
		}
		contentHash := sha256.New()
		_, readErr := io.Copy(contentHash, file)
		if err := errors.Join(readErr, file.Close()); err != nil {
			return err
		}
		fmt.Fprintf(hash, "F:%s:%o:%x\x00", filepath.ToSlash(rel), info.Mode().Perm()&0111, contentHash.Sum(nil))
		return nil
	})
	if err != nil {
		return "", err
	}
	if syncDirectories {
		slices.Reverse(directories)
		for _, path := range directories {
			if err := atomicfile.SyncDirectory(path); err != nil {
				return "", err
			}
		}
	}
	return fmt.Sprintf("%x", hash.Sum(nil)), nil
}
