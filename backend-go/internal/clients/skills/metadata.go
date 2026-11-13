package skills

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// BackupInput 是保存译文备份所需的调用方输入（HTTP 层去壳后传入）。
type BackupInput struct {
	Translated  string
	Model       string
	ChannelName string
}

// SaveTranslationBackup 把原文与译文并列写入项目目录的备份目录，并同步保留一份完整原 Skill。
// 刻意不回写来源 Agent 的 SKILL.md：来源目录始终保持上游原样，项目目录才是可恢复的统一来源。
// 完整目录复制失败时（源 Skill 缺附属资源或读取失败）降级为只同步 SKILL.md，保证项目目录始终有副本。
// 返回本次备份目录路径。
func SaveTranslationBackup(ref Item, original []byte, input BackupInput) (string, error) {
	backupDir, err := ProjectBackupDir(ref.Name)
	if err != nil {
		return "", fmt.Errorf("创建备份目录失败: %w", err)
	}
	metadata, err := json.MarshalIndent(BackupMetadata{
		Name: ref.Name, SourcePath: ContentPath(ref), SourceSHA256: ContentSHA256(original), Agent: ref.Agent,
		Model: strings.TrimSpace(input.Model), ChannelName: strings.TrimSpace(input.ChannelName), CreatedAt: time.Now().Format(time.RFC3339),
	}, "", "  ")
	if err != nil {
		return "", fmt.Errorf("序列化备份元数据失败: %w", err)
	}
	if err := os.WriteFile(filepath.Join(backupDir, "original.md"), original, 0600); err != nil {
		return "", fmt.Errorf("保存原文备份失败: %w", err)
	}
	if err := os.WriteFile(filepath.Join(backupDir, "translated.zh-CN.md"), []byte(input.Translated), 0600); err != nil {
		return "", fmt.Errorf("保存译文备份失败: %w", err)
	}
	if err := os.WriteFile(filepath.Join(backupDir, "metadata.json"), append(metadata, '\n'), 0600); err != nil {
		return "", fmt.Errorf("保存备份元数据失败: %w", err)
	}
	if copyErr := CopyDir(ref.Path, ProjectRootForName(ref.Name), true); copyErr != nil {
		if writeErr := WriteFilesPreserving(ProjectRootForName(ref.Name), map[string][]byte{"SKILL.md": original}); writeErr != nil {
			return "", fmt.Errorf("保存项目 Skill 副本失败: %w", writeErr)
		}
	}
	return backupDir, nil
}

func ProjectBackupDir(name string) (string, error) {
	root, err := projectBackupRoot(name)
	if err != nil {
		return "", err
	}
	dir := filepath.Join(root, time.Now().Format("20060102-150405.000000000"))
	return dir, os.MkdirAll(dir, 0700)
}

func metadataPath(name string) (string, error) {
	if !namePattern.MatchString(name) {
		return "", errors.New("Skill 名称无效")
	}
	root, err := projectRoot()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, ".metadata", name+".json"), nil
}

func readGlobalMetadata(name string) (GlobalMetadata, error) {
	path, err := metadataPath(name)
	if err != nil {
		return GlobalMetadata{}, err
	}
	content, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return GlobalMetadata{}, nil
	}
	if err != nil {
		return GlobalMetadata{}, err
	}
	var metadata GlobalMetadata
	if err := json.Unmarshal(content, &metadata); err != nil {
		return GlobalMetadata{}, err
	}
	return metadata, nil
}

func WriteGlobalMetadata(name string, metadata GlobalMetadata) error {
	path, err := metadataPath(name)
	if err != nil {
		return err
	}
	content, err := json.MarshalIndent(metadata, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	return os.WriteFile(path, append(content, '\n'), 0600)
}

func ProjectRootForName(name string) string {
	root, err := projectRoot()
	if err != nil {
		return filepath.Join("skills", name)
	}
	return filepath.Join(root, name)
}

func projectBackupRoot(name string) (string, error) {
	if !namePattern.MatchString(name) {
		return "", errors.New("Skill 名称无效")
	}
	root, err := projectRoot()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, name, backupsDirName), nil
}

type Backup struct {
	Translated string
	Path       string
	Metadata   BackupMetadata
}

func LatestBackup(ref Item, original []byte) (Backup, bool, error) {
	backupRoot, err := projectBackupRoot(ref.Name)
	if err != nil {
		return Backup{}, false, err
	}
	entries, err := projectBackupDirectories(filepath.Dir(backupRoot))
	if err != nil {
		return Backup{}, false, err
	}
	expectedPath := filepath.Clean(filepath.Join(ref.Path, "SKILL.md"))
	expectedHash := ContentSHA256(original)
	for _, backupPath := range entries {
		metadataBytes, err := os.ReadFile(filepath.Join(backupPath, "metadata.json"))
		if err != nil {
			continue
		}
		var metadata BackupMetadata
		if err := json.Unmarshal(metadataBytes, &metadata); err != nil {
			continue
		}
		sourceMatches := metadata.SourceSHA256 != "" && metadata.SourceSHA256 == expectedHash
		if metadata.SourceSHA256 == "" {
			sourceMatches = filepath.Clean(metadata.SourcePath) == expectedPath
		}
		if !sourceMatches {
			continue
		}
		if metadata.SourceSHA256 != "" && metadata.SourceSHA256 != expectedHash {
			continue
		}
		translated, err := os.ReadFile(filepath.Join(backupPath, "translated.zh-CN.md"))
		if err != nil || strings.TrimSpace(string(translated)) == "" {
			continue
		}
		return Backup{Translated: string(translated), Path: backupPath, Metadata: metadata}, true, nil
	}
	return Backup{}, false, nil
}

func ContentSHA256(content []byte) string {
	sum := sha256.Sum256(content)
	return fmt.Sprintf("%x", sum)
}
