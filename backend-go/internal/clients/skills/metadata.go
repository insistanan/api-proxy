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

	"insistanan/api-proxy/internal/clients/filetxn"
	"insistanan/api-proxy/internal/storage/atomicfile"
)

// BackupInput 是保存译文备份所需的调用方输入（HTTP 层去壳后传入）。
type BackupInput struct {
	Translated  string
	Model       string
	ChannelName string
}

// SaveTranslationBackup 在项目目录保存原文和译文，只有项目中没有同名 Skill 目录时才归档完整来源。
func SaveTranslationBackup(projectDir string, ref Item, original []byte, input BackupInput) (result string, resultErr error) {
	var archived string
	defer func() {
		if resultErr != nil && archived != "" {
			resultErr = &filetxn.CommitError{Files: []string{archived}, Err: resultErr}
		}
	}()
	backupRoot, err := projectBackupRoot(projectDir, ref.Name)
	if err != nil {
		return "", err
	}
	if ref.LocationKey != "project" {
		projectTarget := filepath.Dir(backupRoot)
		info, err := os.Lstat(projectTarget)
		switch {
		case errors.Is(err, fs.ErrNotExist):
			if err := CopyDir(ref.Path, projectTarget, false); err != nil {
				return "", fmt.Errorf("归档项目 Skill 失败: %w", err)
			}
			archived = projectTarget
		case err != nil:
			return "", fmt.Errorf("检查项目 Skill 失败: %w", err)
		case !info.IsDir():
			return "", fmt.Errorf("项目 Skill 路径不是目录: %s", projectTarget)
		}
	}
	createdAt := time.Now()
	backupDir := filepath.Join(backupRoot, createdAt.Format("20060102-150405.000000000"))
	metadata, err := json.MarshalIndent(BackupMetadata{
		Name: ref.Name, SourcePath: ContentPath(ref), SourceSHA256: ContentSHA256(original), Agent: ref.Agent,
		Model: strings.TrimSpace(input.Model), ChannelName: strings.TrimSpace(input.ChannelName), CreatedAt: createdAt.Format(time.RFC3339),
	}, "", "  ")
	if err != nil {
		return "", fmt.Errorf("序列化备份元数据失败: %w", err)
	}
	if err := os.MkdirAll(backupRoot, 0700); err != nil {
		return "", fmt.Errorf("创建备份目录失败: %w", err)
	}
	staging, err := os.MkdirTemp(backupRoot, ".backup-stage-*")
	if err != nil {
		return "", err
	}
	defer func() {
		if err := os.RemoveAll(staging); err != nil {
			resultErr = errors.Join(resultErr, fmt.Errorf("清理备份暂存目录失败: %w", err))
		}
	}()
	if err := filetxn.Replace(filepath.Join(staging, "original.md"), nil, original, "", 0600); err != nil {
		return "", fmt.Errorf("保存原文备份失败: %w", err)
	}
	if err := filetxn.Replace(filepath.Join(staging, "translated.md"), nil, []byte(input.Translated), "", 0600); err != nil {
		return "", fmt.Errorf("保存译文备份失败: %w", err)
	}
	if err := filetxn.Replace(filepath.Join(staging, "metadata.json"), nil, append(metadata, '\n'), "", 0600); err != nil {
		return "", fmt.Errorf("保存备份元数据失败: %w", err)
	}
	if _, err := os.Lstat(backupDir); !errors.Is(err, fs.ErrNotExist) {
		if err == nil {
			return "", errors.New("备份目录已存在，请重新保存")
		}
		return "", err
	}
	if err := atomicfile.RenameDirectory(staging, backupDir); err != nil {
		if atomicfile.IsCommitted(err) {
			return backupDir, &filetxn.CommitError{Files: []string{backupDir}, Err: err}
		}
		return "", err
	}
	return backupDir, nil
}

func metadataPath(projectDir, name string) (string, error) {
	if !namePattern.MatchString(name) {
		return "", errors.New("Skill 名称无效")
	}
	root, err := resolveProjectDir(projectDir)
	if err != nil {
		return "", err
	}
	return filepath.Join(root, ".metadata", name+".json"), nil
}

func readGlobalMetadata(projectDir, name string) (GlobalMetadata, error) {
	path, err := metadataPath(projectDir, name)
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
	var metadata *GlobalMetadata
	if err := json.Unmarshal(content, &metadata); err != nil {
		return GlobalMetadata{}, err
	}
	if metadata == nil {
		return GlobalMetadata{}, errors.New("Skill 备注必须是对象，不能是 null")
	}
	return *metadata, nil
}

func WriteGlobalMetadata(projectDir, name string, metadata GlobalMetadata) error {
	path, err := metadataPath(projectDir, name)
	if err != nil {
		return err
	}
	previous, err := filetxn.Read(path)
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
	return filetxn.Replace(path, previous, append(content, '\n'), "", 0600)
}

func projectBackupRoot(projectDir, name string) (string, error) {
	if !namePattern.MatchString(name) {
		return "", errors.New("Skill 名称无效")
	}
	root, err := resolveProjectDir(projectDir)
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

func (b Backup) Presentation() (name, description, language string) {
	content := []byte(b.Translated)
	language = ContentLanguage(content)
	name = displayName(content, language)
	_, description, _ = ParseFrontmatter(content)
	return
}

func LatestBackup(projectDir string, ref Item, original []byte) (Backup, bool, error) {
	backupRoot, err := projectBackupRoot(projectDir, ref.Name)
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
			return Backup{}, false, fmt.Errorf("读取备份 %s 元数据失败: %w", backupPath, err)
		}
		var metadata BackupMetadata
		if err := json.Unmarshal(metadataBytes, &metadata); err != nil {
			return Backup{}, false, fmt.Errorf("备份 %s 元数据损坏: %w", backupPath, err)
		}
		sourceMatches := metadata.SourceSHA256 != "" && metadata.SourceSHA256 == expectedHash
		if metadata.SourceSHA256 == "" {
			sourceMatches = filepath.Clean(metadata.SourcePath) == expectedPath
		}
		if !sourceMatches {
			continue
		}
		translated, err := os.ReadFile(filepath.Join(backupPath, "translated.md"))
		if errors.Is(err, fs.ErrNotExist) {
			translated, err = os.ReadFile(filepath.Join(backupPath, "translated.zh-CN.md"))
		}
		if err != nil {
			return Backup{}, false, fmt.Errorf("读取备份 %s 译文失败: %w", backupPath, err)
		}
		if strings.TrimSpace(string(translated)) == "" {
			return Backup{}, false, fmt.Errorf("备份 %s 的译文为空", backupPath)
		}
		return Backup{Translated: string(translated), Path: backupPath, Metadata: metadata}, true, nil
	}
	return Backup{}, false, nil
}

func ContentSHA256(content []byte) string {
	sum := sha256.Sum256(content)
	return fmt.Sprintf("%x", sum)
}
