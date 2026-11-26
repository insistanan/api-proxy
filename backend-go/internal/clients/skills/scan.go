package skills

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"time"

	"insistanan/api-proxy/internal/clients/filetxn"
)

func Scan(location Location, projectDir string) ([]Item, error) {
	if location.Error != "" {
		return nil, errors.New(location.Error)
	}
	entries, err := os.ReadDir(location.Path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	items := make([]Item, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() || !namePattern.MatchString(entry.Name()) {
			continue
		}
		path := filepath.Join(location.Path, entry.Name())
		content, err := os.ReadFile(filepath.Join(path, "SKILL.md"))
		item := Item{LocationKey: location.Key, Agent: location.Agent, Name: entry.Name(), Path: path, SourceType: location.SourceType, ReadOnly: location.ReadOnly}
		var issues []error
		if metadata, metadataErr := readGlobalMetadata(projectDir, item.Name); metadataErr == nil {
			item.Note = metadata.Note
		} else {
			issues = append(issues, fmt.Errorf("读取备注失败: %w", metadataErr))
		}
		if err != nil {
			item.Issue = "缺少或无法读取 SKILL.md"
		} else {
			name, description, parseErr := ParseFrontmatter(content)
			item.Description = description
			if parseErr != nil || name != entry.Name() {
				item.Issue = "frontmatter 的 name 必须与目录名一致"
			} else {
				item.Valid = true
				item.Language = ContentLanguage(content)
				item.DisplayName = displayName(content, item.Language)
				if backup, found, backupErr := LatestBackup(projectDir, item, content); backupErr != nil {
					issues = append(issues, fmt.Errorf("读取译文失败: %w", backupErr))
				} else if found {
					item.HasTranslation = true
					item.TranslatedName, item.TranslatedDescription, item.TranslationLanguage = backup.Presentation()
				}
			}
		}
		walkErr := filepath.WalkDir(path, func(current string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil || entry.IsDir() {
				return walkErr
			}
			if info, err := entry.Info(); err == nil {
				item.Size += info.Size()
				if info.ModTime().After(timeFromString(item.ModifiedAt)) {
					item.ModifiedAt = info.ModTime().Format(time.RFC3339)
				}
			} else {
				return err
			}
			return nil
		})
		if walkErr != nil {
			issues = append(issues, fmt.Errorf("扫描资源失败: %w", walkErr))
		}
		if item.Issue != "" {
			issues = append(issues, errors.New(item.Issue))
		}
		if err := errors.Join(issues...); err != nil {
			item.Issue = err.Error()
			item.Valid = false
		}
		items = append(items, item)
	}
	return items, nil
}

// RestoreProjectBackups 兼容早期仅保存 original.md 的翻译备份，
// 使其成为项目目录中可查看、可复制的标准 Skill。
func RestoreProjectBackups(root string) (resultErr error) {
	var restored []string
	defer func() {
		if resultErr != nil && len(restored) > 0 {
			resultErr = &filetxn.CommitError{Files: restored, Err: resultErr}
		}
	}()
	entries, err := os.ReadDir(root)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if !entry.IsDir() || !namePattern.MatchString(entry.Name()) {
			continue
		}
		skillDir := filepath.Join(root, entry.Name())
		if _, err := os.Stat(filepath.Join(skillDir, "SKILL.md")); err == nil {
			continue
		} else if !errors.Is(err, fs.ErrNotExist) {
			return err
		}
		backupDirs, err := projectBackupDirectories(skillDir)
		if err != nil {
			return err
		}
		for _, backupDir := range backupDirs {
			original, err := os.ReadFile(filepath.Join(backupDir, "original.md"))
			if err != nil {
				return fmt.Errorf("读取旧备份 %s 失败: %w", backupDir, err)
			}
			name, _, err := ParseFrontmatter(original)
			if err != nil || name != entry.Name() {
				continue
			}
			if err := filetxn.Replace(filepath.Join(skillDir, "SKILL.md"), nil, original, "", 0600); err != nil {
				return err
			}
			restored = append(restored, filepath.Join(skillDir, "SKILL.md"))
			break
		}
	}
	return nil
}

// projectBackupDirectories 同时读取当前 .backups 目录和早期的扁平时间目录。
func projectBackupDirectories(skillDir string) ([]string, error) {
	entries, err := os.ReadDir(skillDir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	directories := make([]string, 0)
	for _, entry := range entries {
		if entry.IsDir() && backupDirPattern.MatchString(entry.Name()) {
			directories = append(directories, filepath.Join(skillDir, entry.Name()))
		}
	}
	backupRoot := filepath.Join(skillDir, backupsDirName)
	backupEntries, err := os.ReadDir(backupRoot)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	for _, entry := range backupEntries {
		if entry.IsDir() && backupDirPattern.MatchString(entry.Name()) {
			directories = append(directories, filepath.Join(backupRoot, entry.Name()))
		}
	}
	sort.Slice(directories, func(i, j int) bool { return filepath.Base(directories[i]) > filepath.Base(directories[j]) })
	return directories, nil
}

func timeFromString(value string) time.Time {
	parsed, _ := time.Parse(time.RFC3339, value)
	return parsed
}
