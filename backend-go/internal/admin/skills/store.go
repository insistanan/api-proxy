package skills

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// ContentPath 返回 Skill 主文件（SKILL.md）的路径。
func ContentPath(ref Item) string {
	return filepath.Join(ref.Path, "SKILL.md")
}

// ReadContent 读取 Skill 主文件内容。
func ReadContent(ref Item) ([]byte, error) {
	return os.ReadFile(ContentPath(ref))
}

// Remove 删除整个 Skill 目录。调用方负责先拒绝只读来源（插件缓存 / 内置目录）。
func Remove(ref Item) error {
	return os.RemoveAll(ref.Path)
}

// CopyDir 把源 Skill 目录流式拷贝到目标目录，不把文件内容收进内存。
// preserveBackups 为 true 时（用于项目目录）保留翻译备份目录，清理其余旧文件后写入。
// 拒绝符号链接、校验单文件大小、校验目标路径不逃逸，并要求源目录包含 SKILL.md。
func CopyDir(source, destination string, preserveBackups bool) error {
	sourceInfo, err := os.Stat(source)
	if err != nil {
		return err
	}
	if !sourceInfo.IsDir() {
		return errors.New("Skill 来源不是目录")
	}

	if err := os.MkdirAll(destination, 0700); err != nil {
		return err
	}
	if preserveBackups {
		if err := cleanDirPreservingBackups(destination); err != nil {
			return err
		}
	} else if err := os.RemoveAll(destination); err != nil {
		return err
	}
	if err := os.MkdirAll(destination, 0700); err != nil {
		return err
	}

	cleanDest := filepath.Clean(destination)
	sawSkillMD := false
	err = filepath.WalkDir(source, func(current string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			if current != source && (entry.Name() == backupsDirName || backupDirPattern.MatchString(entry.Name())) {
				return fs.SkipDir
			}
			return nil
		}
		if entry.Type()&fs.ModeSymlink != 0 {
			return fmt.Errorf("不支持复制符号链接: %s", entry.Name())
		}
		relative, relErr := filepath.Rel(source, current)
		if relErr != nil {
			return relErr
		}
		relative = filepath.ToSlash(relative)
		if relative == "." || strings.HasPrefix(relative, "../") || strings.Contains(relative, "\\") {
			return errors.New("Skill 文件路径无效")
		}
		fileInfo, infoErr := entry.Info()
		if infoErr != nil {
			return infoErr
		}
		if fileInfo.Size() > maxCopyFileBytes {
			return fmt.Errorf("Skill 文件过大: %s", relative)
		}
		target := filepath.Join(cleanDest, filepath.FromSlash(relative))
		if !strings.HasPrefix(target, cleanDest+string(os.PathSeparator)) {
			return errors.New("Skill 文件路径无效")
		}
		if relative == "SKILL.md" {
			sawSkillMD = true
		}
		return copyFile(current, target)
	})
	if err != nil {
		return err
	}
	if !sawSkillMD {
		return errors.New("Skill 缺少 SKILL.md")
	}
	return nil
}

// copyFile 用 io.Copy 逐文件拷贝，避免把整个 Skill 收进内存。
func copyFile(source, target string) error {
	if err := os.MkdirAll(filepath.Dir(target), 0700); err != nil {
		return err
	}
	sourceFile, err := os.Open(source)
	if err != nil {
		return err
	}
	defer sourceFile.Close()
	destinationFile, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600)
	if err != nil {
		return err
	}
	if _, err := io.Copy(destinationFile, sourceFile); err != nil {
		destinationFile.Close()
		return err
	}
	return destinationFile.Close()
}

// cleanDirPreservingBackups 清理目录内的非备份条目，保留新旧格式的翻译备份目录。
func cleanDirPreservingBackups(destination string) error {
	entries, err := os.ReadDir(destination)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.Name() == backupsDirName || backupDirPattern.MatchString(entry.Name()) {
			continue
		}
		if err := os.RemoveAll(filepath.Join(destination, entry.Name())); err != nil {
			return err
		}
	}
	return nil
}

func writeFiles(destination string, files map[string][]byte) error {
	if err := os.RemoveAll(destination); err != nil {
		return err
	}
	if err := os.MkdirAll(destination, 0700); err != nil {
		return err
	}
	for name, content := range files {
		path := filepath.Join(destination, filepath.FromSlash(name))
		if !strings.HasPrefix(filepath.Clean(path), filepath.Clean(destination)+string(os.PathSeparator)) {
			return errors.New("Skill 文件路径无效")
		}
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			return err
		}
		if err := os.WriteFile(path, content, 0600); err != nil {
			return err
		}
	}
	return nil
}

func WriteFilesForLocation(location Location, name string, files map[string][]byte) error {
	destination := filepath.Join(location.Path, name)
	if location.Key == "project" {
		return WriteFilesPreserving(destination, files)
	}
	return writeFiles(destination, files)
}

// WriteFilesPreserving 更新项目 Skill 的主体文件，并保留专用翻译备份目录。
func WriteFilesPreserving(destination string, files map[string][]byte) error {
	if err := os.MkdirAll(destination, 0700); err != nil {
		return err
	}
	entries, err := os.ReadDir(destination)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		// 保留新旧格式的翻译备份，其余文件必须与当前 Skill 包完全一致。
		if entry.Name() == backupsDirName || backupDirPattern.MatchString(entry.Name()) {
			continue
		}
		if err := os.RemoveAll(filepath.Join(destination, entry.Name())); err != nil {
			return err
		}
	}
	for name, content := range files {
		path := filepath.Join(destination, filepath.FromSlash(name))
		if !strings.HasPrefix(filepath.Clean(path), filepath.Clean(destination)+string(os.PathSeparator)) {
			return errors.New("Skill 文件路径无效")
		}
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			return err
		}
		if err := os.WriteFile(path, content, 0600); err != nil {
			return err
		}
	}
	return nil
}

func EnsureProjectTarget(targets []string) []string {
	result := make([]string, 0, len(targets)+1)
	seen := make(map[string]struct{}, len(targets)+1)
	for _, target := range targets {
		if _, exists := seen[target]; exists {
			continue
		}
		seen[target] = struct{}{}
		result = append(result, target)
	}
	if _, exists := seen["project"]; !exists {
		result = append(result, "project")
	}
	return result
}
