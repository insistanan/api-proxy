package skills

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"time"
)

func Scan(location Location) ([]Item, error) {
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
		if metadata, metadataErr := readGlobalMetadata(item.Name); metadataErr == nil {
			item.Note = metadata.Note
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
				if backup, found, backupErr := LatestBackup(item, content); backupErr == nil && found {
					item.TranslatedName = translatedDisplayName([]byte(backup.Translated))
				}
			}
		}
		_ = filepath.WalkDir(path, func(current string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil || entry.IsDir() {
				return walkErr
			}
			if info, err := entry.Info(); err == nil {
				item.Size += info.Size()
				if info.ModTime().After(timeFromString(item.ModifiedAt)) {
					item.ModifiedAt = info.ModTime().Format(time.RFC3339)
				}
			}
			return nil
		})
		items = append(items, item)
	}
	return items, nil
}

// RestoreProjectBackups 兼容早期仅保存 original.md 的翻译备份，
// 使其成为项目目录中可查看、可复制的标准 Skill。
func RestoreProjectBackups(root string) error {
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
				continue
			}
			name, _, err := ParseFrontmatter(original)
			if err != nil || name != entry.Name() {
				continue
			}
			if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), original, 0600); err != nil {
				return err
			}
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
