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

	"insistanan/api-proxy/internal/clients/filetxn"
	"insistanan/api-proxy/internal/storage/atomicfile"
)

// ContentPath 返回 Skill 主文件（SKILL.md）的路径。
func ContentPath(ref Item) string {
	return filepath.Join(ref.Path, "SKILL.md")
}

// ReadContent 读取 Skill 主文件内容。
func ReadContent(ref Item) ([]byte, error) {
	return os.ReadFile(ContentPath(ref))
}

// CopyDir 流式复制源 Skill 到临时目录，成功后再替换目标。
// preserveBackups 为 true 时（用于项目目录）保留目标现有的翻译备份。
// 拒绝符号链接、校验单文件大小、校验目标路径不逃逸，并要求源目录包含 SKILL.md。
func CopyDir(source, destination string, preserveBackups bool) error {
	if err := validateDestinations(source, []string{destination}); err != nil {
		return err
	}
	item, err := stageCopy(source, destination, preserveBackups)
	if err != nil {
		return err
	}
	defer item.abort()
	return commitInstalls([]*stagedInstall{item})
}
func stageCopy(source, destination string, preserveBackups bool) (*stagedInstall, error) {
	before, err := directoryFingerprint(destination)
	if err != nil {
		return nil, err
	}
	sourceRevision, err := directoryFingerprint(source)
	if err != nil {
		return nil, err
	}
	sourceInfo, err := os.Stat(source)
	if err != nil {
		return nil, err
	}
	if !sourceInfo.IsDir() {
		return nil, errors.New("Skill 来源不是目录")
	}

	parent := filepath.Dir(destination)
	if err := os.MkdirAll(parent, 0700); err != nil {
		return nil, err
	}
	staging, err := os.MkdirTemp(parent, ".skill-stage-*")
	if err != nil {
		return nil, err
	}
	staging = filepath.Clean(staging)
	install := &stagedInstall{destination: destination, staging: staging, before: before}
	ready := false
	defer func() {
		if !ready {
			install.abort()
		}
	}()

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
		return nil, err
	}
	if !sawSkillMD {
		return nil, errors.New("Skill 缺少 SKILL.md")
	}
	if preserveBackups {
		if err := copyPreservedBackups(destination, staging); err != nil {
			return nil, fmt.Errorf("保留翻译备份失败: %w", err)
		}
	}
	stagedRevision, err := directoryFingerprint(staging)
	if err != nil {
		return nil, err
	}
	if stagedRevision != sourceRevision {
		return nil, fmt.Errorf("%w: Skill 复制期间来源发生变化", filetxn.ErrConflict)
	}
	ready = true
	return install, nil
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
	info, err := sourceFile.Stat()
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("不支持复制特殊文件: %s", source)
	}
	destinationFile, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600|info.Mode().Perm()&0111)
	if err != nil {
		return err
	}
	if _, err := io.Copy(destinationFile, sourceFile); err != nil {
		destinationFile.Close()
		return err
	}
	return errors.Join(destinationFile.Sync(), destinationFile.Close())
}

// stageInstallTargets 在修改清单前完成所有目标的路径校验和文件暂存。
func stageInstallTargets(locations []Location, name, source string, files map[string][]byte, modes map[string]fs.FileMode) ([]*stagedInstall, error) {
	if !ValidName(name) {
		return nil, errors.New("Skill 名称无效")
	}
	var destinations []string
	for _, location := range locations {
		if location.ReadOnly {
			return nil, fmt.Errorf("目标只读: %s", location.Agent)
		}
		destinations = append(destinations, filepath.Join(location.Path, name))
	}
	if err := validateDestinations(source, destinations); err != nil {
		return nil, err
	}
	staged := make([]*stagedInstall, 0, len(locations))
	ready := false
	defer func() {
		if !ready {
			for _, item := range staged {
				item.abort()
			}
		}
	}()
	for index, location := range locations {
		var item *stagedInstall
		var err error
		if source == "" {
			item, err = stageFiles(location, name, files, modes)
		} else {
			item, err = stageCopy(source, destinations[index], location.Key == "project")
		}
		if err != nil {
			return nil, err
		}
		item.location = location
		staged = append(staged, item)
	}
	ready = true
	return staged, nil
}

// stageFiles 把 Skill 文件写入目标目录同级的临时目录，不触碰现有安装。
func stageFiles(location Location, name string, files map[string][]byte, modes ...map[string]fs.FileMode) (*stagedInstall, error) {
	destination := filepath.Join(location.Path, name)
	before, err := directoryFingerprint(destination)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(location.Path, 0700); err != nil {
		return nil, fmt.Errorf("创建 %s 目录失败: %w", location.Agent, err)
	}
	staging, err := os.MkdirTemp(location.Path, ".skill-stage-*")
	if err != nil {
		return nil, fmt.Errorf("暂存 %s 失败: %w", location.Agent, err)
	}
	if err := writeFilesTo(staging, files, modes...); err != nil {
		return nil, fmt.Errorf("暂存 %s 失败: %w", location.Agent, errors.Join(err, os.RemoveAll(staging)))
	}
	if location.Key == "project" {
		if err := copyPreservedBackups(destination, staging); err != nil {
			return nil, fmt.Errorf("保留 %s 的翻译备份失败: %w", location.Agent, errors.Join(err, os.RemoveAll(staging)))
		}
	}
	return &stagedInstall{location: location, destination: destination, staging: staging, before: before}, nil
}

// stagedInstall 表示一个已暂存、等待替换的目标目录。
type stagedInstall struct {
	before      string
	after       string
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
	current, err := directoryFingerprint(s.destination)
	if err != nil {
		return err
	}
	if current != s.before {
		return fmt.Errorf("%w: %s", filetxn.ErrConflict, s.destination)
	}
	s.after, err = directoryFingerprint(s.staging)
	if err != nil {
		return err
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
	if err := atomicfile.RenameDirectory(s.staging, s.destination); err != nil {
		if atomicfile.IsCommitted(err) {
			s.committed = true
			return &filetxn.CommitError{Files: []string{s.destination}, Err: err}
		}
		if s.old != "" {
			if restoreErr := os.Rename(s.old, s.destination); restoreErr != nil {
				return &filetxn.CommitError{Files: []string{s.destination}, Err: fmt.Errorf("替换 %s 失败: %w；旧副本位于 %s，恢复失败: %w", s.label(), err, s.old, restoreErr)}
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
	current, err := directoryFingerprint(s.destination)
	if err != nil {
		return err
	}
	if current != s.after {
		return fmt.Errorf("%w: 回滚目标 %s 已变化，旧副本保留于 %s", filetxn.ErrConflict, s.destination, s.old)
	}
	if err := os.RemoveAll(s.destination); err != nil {
		return fmt.Errorf("回滚 %s 失败: %w", s.label(), err)
	}
	if s.old != "" {
		if err := atomicfile.RenameDirectory(s.old, s.destination); err != nil {
			return fmt.Errorf("恢复 %s 旧目录失败: %w", s.label(), err)
		}
		s.old = ""
	}
	s.committed = false
	return nil
}

// cleanup 删除替换后遗留的旧目录；清理失败不影响已完成的安装。
func (s *stagedInstall) cleanup() error {
	if s.old == "" {
		return nil
	}
	if err := os.RemoveAll(s.old); err != nil {
		return fmt.Errorf("清理旧目录 %s 失败: %w", s.old, err)
	}
	s.old = ""
	return nil
}

// abort 清理尚未提交的暂存目录；已提交的安装不受影响。
func (s *stagedInstall) abort() {
	if s.staging == "" {
		return
	}
	if err := os.RemoveAll(s.staging); err != nil {
		log.Printf("[Skills-Write] 清理未提交的暂存目录 %s 失败: %v", s.staging, err)
	}
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

func writeFilesTo(directory string, files map[string][]byte, modes ...map[string]fs.FileMode) error {
	cleanDir := filepath.Clean(directory)
	for name, content := range files {
		path := filepath.Join(cleanDir, filepath.FromSlash(name))
		if !strings.HasPrefix(path, cleanDir+string(os.PathSeparator)) {
			return errors.New("Skill 文件路径无效")
		}
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			return err
		}
		mode := fs.FileMode(0600)
		if len(modes) > 0 {
			mode |= modes[0][name] & 0111
		}
		if err := filetxn.Replace(path, nil, content, "", mode); err != nil {
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
