package skills

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
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

// CopyDir 流式复制源 Skill 到临时目录，成功后再替换目标。
// preserveBackups 为 true 时（用于项目目录）保留目标现有的翻译备份。
// 拒绝符号链接、校验单文件大小、校验目标路径不逃逸，并要求源目录包含 SKILL.md。
func CopyDir(source, destination string, preserveBackups bool) error {
	sourceInfo, err := os.Stat(source)
	if err != nil {
		return err
	}
	if !sourceInfo.IsDir() {
		return errors.New("Skill 来源不是目录")
	}

	parent := filepath.Dir(destination)
	if err := os.MkdirAll(parent, 0700); err != nil {
		return err
	}
	staging, err := os.MkdirTemp(parent, ".skill-stage-*")
	if err != nil {
		return err
	}
	staging = filepath.Clean(staging)
	install := &stagedInstall{destination: destination, staging: staging}
	defer install.abort()

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
		target := filepath.Join(staging, filepath.FromSlash(relative))
		if !strings.HasPrefix(target, staging+string(os.PathSeparator)) {
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
	if preserveBackups {
		if err := copyPreservedBackups(destination, staging); err != nil {
			return fmt.Errorf("保留翻译备份失败: %w", err)
		}
	}
	if err := install.commit(); err != nil {
		return err
	}
	install.cleanup()
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

// InstallFiles 把同一份 Skill 文件原子写入多个目标目录：先全部暂存，再逐个替换；
// 任一目标替换失败时回滚已替换的目标，避免留下半套文件。
func InstallFiles(locations []Location, name string, files map[string][]byte) error {
	staged := make([]*stagedInstall, 0, len(locations))
	defer func() {
		for _, item := range staged {
			item.abort()
		}
	}()
	for _, location := range locations {
		item, err := stageFiles(location, name, files)
		if err != nil {
			return err
		}
		staged = append(staged, item)
	}

	committed := make([]*stagedInstall, 0, len(staged))
	for _, item := range staged {
		if err := item.commit(); err != nil {
			for index := len(committed) - 1; index >= 0; index-- {
				if rollbackErr := committed[index].rollback(); rollbackErr != nil {
					return fmt.Errorf("%w；回滚 %s 失败: %v", err, committed[index].label(), rollbackErr)
				}
			}
			return err
		}
		committed = append(committed, item)
	}
	for _, item := range committed {
		item.cleanup()
	}
	return nil
}

// stageFiles 把 Skill 文件写入目标目录同级的临时目录，不触碰现有安装。
func stageFiles(location Location, name string, files map[string][]byte) (*stagedInstall, error) {
	destination := filepath.Join(location.Path, name)
	if err := os.MkdirAll(location.Path, 0700); err != nil {
		return nil, fmt.Errorf("创建 %s 目录失败: %w", location.Agent, err)
	}
	staging, err := os.MkdirTemp(location.Path, ".skill-stage-*")
	if err != nil {
		return nil, fmt.Errorf("暂存 %s 失败: %w", location.Agent, err)
	}
	if err := writeFilesTo(staging, files); err != nil {
		_ = os.RemoveAll(staging)
		return nil, fmt.Errorf("暂存 %s 失败: %w", location.Agent, err)
	}
	if location.Key == "project" {
		if err := copyPreservedBackups(destination, staging); err != nil {
			_ = os.RemoveAll(staging)
			return nil, fmt.Errorf("保留 %s 的翻译备份失败: %w", location.Agent, err)
		}
	}
	return &stagedInstall{location: location, destination: destination, staging: staging}, nil
}

// stagedInstall 表示一个已暂存、等待替换的目标目录。
type stagedInstall struct {
	location    Location
	destination string
	staging     string
	old         string
	committed   bool
}

func (s *stagedInstall) label() string {
	if s.location.Agent != "" {
		return s.location.Agent
	}
	return s.destination
}

// commit 用暂存目录替换目标目录；旧目录保留到 cleanup 或 rollback。
func (s *stagedInstall) commit() error {
	if s.committed {
		return fmt.Errorf("安装目标 %s 已提交", s.label())
	}
	if _, err := os.Lstat(s.destination); err == nil {
		old, err := reserveSiblingPath(filepath.Dir(s.destination), ".skill-old-*")
		if err != nil {
			return fmt.Errorf("准备替换 %s 失败: %w", s.label(), err)
		}
		if err := os.Rename(s.destination, old); err != nil {
			return fmt.Errorf("替换 %s 失败: %w", s.label(), err)
		}
		s.old = old
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("检查 %s 失败: %w", s.label(), err)
	}
	if err := os.Rename(s.staging, s.destination); err != nil {
		if s.old != "" {
			if restoreErr := os.Rename(s.old, s.destination); restoreErr != nil {
				return fmt.Errorf("替换 %s 失败: %w；恢复旧目录失败: %v", s.label(), err, restoreErr)
			}
			s.old = ""
		}
		return fmt.Errorf("替换 %s 失败: %w", s.label(), err)
	}
	s.committed = true
	return nil
}

// rollback 撤销已提交的替换，恢复旧目录。
func (s *stagedInstall) rollback() error {
	if !s.committed {
		return nil
	}
	if err := os.RemoveAll(s.destination); err != nil {
		return fmt.Errorf("回滚 %s 失败: %w", s.label(), err)
	}
	if s.old != "" {
		if err := os.Rename(s.old, s.destination); err != nil {
			return fmt.Errorf("恢复 %s 旧目录失败: %w", s.label(), err)
		}
		s.old = ""
	}
	s.committed = false
	return nil
}

// cleanup 删除替换后遗留的旧目录；清理失败不影响已完成的安装。
func (s *stagedInstall) cleanup() {
	if s.old == "" {
		return
	}
	if err := os.RemoveAll(s.old); err != nil {
		log.Printf("[Skills-Write] 清理旧目录失败: %v", err)
	}
	s.old = ""
}

// abort 清理尚未提交的暂存目录；已提交的安装不受影响。
func (s *stagedInstall) abort() {
	if s.staging == "" {
		return
	}
	_ = os.RemoveAll(s.staging)
	s.staging = ""
}

func reserveSiblingPath(parent, pattern string) (string, error) {
	path, err := os.MkdirTemp(parent, pattern)
	if err != nil {
		return "", err
	}
	if err := os.Remove(path); err != nil {
		return "", err
	}
	return path, nil
}

func writeFilesTo(directory string, files map[string][]byte) error {
	cleanDir := filepath.Clean(directory)
	for name, content := range files {
		path := filepath.Join(cleanDir, filepath.FromSlash(name))
		if !strings.HasPrefix(path, cleanDir+string(os.PathSeparator)) {
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

// copyPreservedBackups 把目标目录中的翻译备份复制到暂存目录，随新 Skill 一起替换。
func copyPreservedBackups(destination, staging string) error {
	entries, err := os.ReadDir(destination)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.Name() != backupsDirName && !backupDirPattern.MatchString(entry.Name()) {
			continue
		}
		if err := copyTree(filepath.Join(destination, entry.Name()), filepath.Join(staging, entry.Name())); err != nil {
			return err
		}
	}
	return nil
}

func copyTree(source, destination string) error {
	return filepath.WalkDir(source, func(current string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(source, current)
		if err != nil {
			return err
		}
		target := destination
		if relative != "." {
			target = filepath.Join(destination, relative)
		}
		if entry.IsDir() {
			return os.MkdirAll(target, 0700)
		}
		if entry.Type()&fs.ModeSymlink != 0 {
			return fmt.Errorf("不支持复制符号链接: %s", current)
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if info.Size() > maxCopyFileBytes {
			return fmt.Errorf("备份文件过大: %s", current)
		}
		return copyFile(current, target)
	})
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
