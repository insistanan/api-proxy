package skills

import (
	"archive/zip"
	"bytes"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"regexp"
	"strings"
	"unicode"

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

var nonProse = regexp.MustCompile("`[^`]*`|https?://[^\\s)]+")

// ContentLanguage 根据可读正文判断原文语言。技能标识符和代码不参与判断。
func ContentLanguage(content []byte) string {
	text := strings.ReplaceAll(string(content), "\r\n", "\n")
	_, description, _ := ParseFrontmatter(content)
	if _, body, ok := strings.Cut(strings.TrimPrefix(text, "---\n"), "\n---\n"); ok {
		text = body
	}
	var fence byte
	han, latin := 0, 0
	count := func(line string) {
		for _, char := range nonProse.ReplaceAllString(line, "") {
			switch {
			case unicode.Is(unicode.Han, char):
				han++
			case unicode.Is(unicode.Latin, char):
				latin++
			}
		}
	}
	count(description)
	for _, line := range strings.Split(text, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~") {
			if fence == 0 {
				fence = trimmed[0]
			} else if fence == trimmed[0] {
				fence = 0
			}
			continue
		}
		if fence == 0 {
			count(line)
		}
	}
	// 英文单词包含多个字母，以汉字对英文词的近似数量比较。
	if han > 0 && han*4 >= latin {
		return "zh"
	}
	return "en"
}

func displayName(content []byte, language string) string {
	text := strings.ReplaceAll(string(content), "\r\n", "\n")
	if !strings.HasPrefix(text, "---\n") {
		return ""
	}
	frontmatter, body, ok := strings.Cut(strings.TrimPrefix(text, "---\n"), "\n---")
	if !ok {
		return ""
	}
	var fields struct {
		DisplayName string `yaml:"display_name"`
	}
	if err := yaml.Unmarshal([]byte(frontmatter), &fields); err == nil && strings.TrimSpace(fields.DisplayName) != "" {
		return strings.TrimSpace(fields.DisplayName)
	}
	title := strings.TrimSpace(body)
	if strings.HasPrefix(title, "# ") {
		title, _, _ = strings.Cut(title[2:], "\n")
		hasHan := strings.ContainsFunc(title, func(r rune) bool { return unicode.Is(unicode.Han, r) })
		if hasHan == (language == "zh") {
			return strings.TrimSpace(title)
		}
	}
	return ""
}

// ValidateTranslation 拒绝无法作为另一语言显示的自动译文。
func ValidateTranslation(name string, original []byte, translated string) error {
	content := []byte(translated)
	translatedName, _, err := ParseFrontmatter(content)
	if err != nil {
		return fmt.Errorf("模型未返回完整的 Skill 译文: %w", err)
	}
	if translatedName != name {
		return errors.New("译文改变了 Skill 标识")
	}
	language := ContentLanguage(content)
	if language == ContentLanguage(original) {
		return errors.New("模型未返回目标语言的译文")
	}
	if displayName(content, language) == "" {
		return errors.New("译文缺少可显示的名称")
	}
	return nil
}
