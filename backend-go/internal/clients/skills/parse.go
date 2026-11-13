package skills

import (
	"archive/zip"
	"bytes"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

func ParseImported(fileName string, content []byte) (map[string][]byte, string, error) {
	if strings.EqualFold(filepath.Ext(fileName), ".zip") {
		return parseZip(content)
	}
	if !strings.EqualFold(filepath.Ext(fileName), ".md") {
		return nil, "", errors.New("仅支持 ZIP 压缩包或 Markdown 文件")
	}
	name, _, err := ParseFrontmatter(content)
	if err != nil {
		return nil, "", err
	}
	return map[string][]byte{"SKILL.md": content}, name, nil
}

func parseZip(content []byte) (map[string][]byte, string, error) {
	reader, err := zip.NewReader(bytes.NewReader(content), int64(len(content)))
	if err != nil {
		return nil, "", errors.New("ZIP 文件无效")
	}
	files := make(map[string][]byte)
	var skillPath string
	for _, file := range reader.File {
		name := filepath.ToSlash(file.Name)
		if strings.HasPrefix(name, "/") || strings.Contains(name, "../") || file.FileInfo().IsDir() {
			continue
		}
		if strings.EqualFold(filepath.Base(name), "SKILL.md") {
			if skillPath != "" {
				return nil, "", errors.New("ZIP 中只能包含一个 SKILL.md")
			}
			skillPath = filepath.Dir(name)
		}
	}
	if skillPath == "" {
		return nil, "", errors.New("ZIP 中缺少 SKILL.md")
	}
	for _, file := range reader.File {
		name := filepath.ToSlash(file.Name)
		if file.FileInfo().IsDir() || strings.HasPrefix(name, "/") || strings.Contains(name, "../") {
			continue
		}
		if skillPath != "." {
			prefix := skillPath + "/"
			if !strings.HasPrefix(name, prefix) {
				continue
			}
			name = strings.TrimPrefix(name, prefix)
		}
		if name == "" {
			continue
		}
		if file.UncompressedSize64 > MaxImportBytes {
			return nil, "", errors.New("ZIP 内文件不能超过 20 MB")
		}
		input, err := file.Open()
		if err != nil {
			return nil, "", err
		}
		data, readErr := io.ReadAll(io.LimitReader(input, MaxImportBytes+1))
		input.Close()
		if readErr != nil || len(data) > MaxImportBytes {
			return nil, "", errors.New("读取 ZIP 内容失败或文件过大")
		}
		files[name] = data
	}
	skill, exists := files["SKILL.md"]
	if !exists {
		return nil, "", errors.New("ZIP 中的 SKILL.md 无效")
	}
	name, _, err := ParseFrontmatter(skill)
	if err != nil {
		return nil, "", err
	}
	return files, name, nil
}

func ParseFrontmatter(content []byte) (string, string, error) {
	text := string(content)
	if !strings.HasPrefix(text, "---\n") && !strings.HasPrefix(text, "---\r\n") {
		return "", "", errors.New("SKILL.md 必须以 YAML frontmatter 开头")
	}
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	end := -1
	for index, line := range lines[1:] {
		if strings.TrimSpace(line) == "---" {
			end = index + 1
			break
		}
	}
	if end == -1 {
		return "", "", errors.New("SKILL.md 的 YAML frontmatter 没有结束标记")
	}
	var frontmatter struct {
		Name        string `yaml:"name"`
		Description string `yaml:"description"`
	}
	if err := yaml.Unmarshal([]byte(strings.Join(lines[1:end], "\n")), &frontmatter); err != nil {
		return "", "", fmt.Errorf("SKILL.md 的 YAML frontmatter 无效: %w", err)
	}
	name := strings.TrimSpace(frontmatter.Name)
	description := strings.TrimSpace(frontmatter.Description)
	if !namePattern.MatchString(name) {
		return "", "", errors.New("SKILL.md 缺少合法的 name（小写字母、数字和连字符）")
	}
	if description == "" {
		return "", "", errors.New("SKILL.md 缺少 description")
	}
	return name, description, nil
}

func translatedDisplayName(content []byte) string {
	text := string(content)
	if !strings.HasPrefix(text, "---\n") && !strings.HasPrefix(text, "---\r\n") {
		return ""
	}
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	end := -1
	for index, line := range lines[1:] {
		if strings.TrimSpace(line) == "---" {
			end = index + 1
			break
		}
	}
	if end == -1 {
		return ""
	}
	var frontmatter struct {
		DisplayName string `yaml:"display_name"`
	}
	if err := yaml.Unmarshal([]byte(strings.Join(lines[1:end], "\n")), &frontmatter); err != nil {
		return ""
	}
	return strings.TrimSpace(frontmatter.DisplayName)
}
