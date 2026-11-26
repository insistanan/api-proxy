package filetxn

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// Identity 只归一化比较身份；实际文件路径保留原大小写。
func Identity(path string) (string, error) {
	resolved, err := resolvePath(path)
	if err != nil {
		return "", err
	}
	if runtime.GOOS == "windows" {
		resolved = strings.ToLower(resolved)
	}
	return resolved, nil
}

// resolvePath 解析存在的祖先目录链接，再补回尚未创建的路径。
func resolvePath(path string) (string, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	current := filepath.Clean(absolute)
	var suffix []string
	for {
		resolved, err := filepath.EvalSymlinks(current)
		if err == nil {
			for i := len(suffix) - 1; i >= 0; i-- {
				resolved = filepath.Join(resolved, suffix[i])
			}
			return filepath.Clean(resolved), nil
		}
		if !errors.Is(err, os.ErrNotExist) {
			return "", fmt.Errorf("解析实际文件路径失败: %w", err)
		}
		parent := filepath.Dir(current)
		if parent == current {
			return "", fmt.Errorf("路径没有可访问的根目录: %s", path)
		}
		suffix = append(suffix, filepath.Base(current))
		current = parent
	}
}

func Overlap(left, right string) (bool, error) {
	a, err := Identity(left)
	if err != nil {
		return false, err
	}
	b, err := Identity(right)
	if err != nil {
		return false, err
	}
	separator := string(filepath.Separator)
	return a == b || strings.HasPrefix(a, strings.TrimSuffix(b, separator)+separator) || strings.HasPrefix(b, strings.TrimSuffix(a, separator)+separator), nil
}
