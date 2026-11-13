package skills

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Locations 枚举当前运行用户的已知 Agent Skill 目录，避免把任意路径暴露为文件浏览器。
// openCodeSkillsDir 由调用方传入：OpenCode 配置路径的多级回退规则归 handlers 的 OpenCode
// 配置页所有，本包不重复实现；其余客户端目录（Claude Code / Codex）本包按各自环境变量自行解析。
func Locations(openCodeSkillsDir string) ([]Location, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	claudeHome := filepath.Join(home, ".claude")
	if configured := strings.TrimSpace(os.Getenv("CLAUDE_CONFIG_DIR")); configured != "" {
		claudeHome = absPath(configured)
	}
	codexHome := filepath.Join(home, ".codex")
	if configured := strings.TrimSpace(os.Getenv("CODEX_HOME")); configured != "" {
		codexHome = absPath(configured)
	}
	openCodeDir := openCodeSkillsDir
	projectDir, err := projectRoot()
	if err != nil {
		return nil, err
	}
	locations := []Location{
		{Key: "project", Agent: "项目 Skills 目录", Path: projectDir, SourceType: "project", ReadOnly: false},
		{Key: "agents", Agent: "通用 Agent Skills", Path: filepath.Join(home, ".agents", "skills"), SourceType: "user", ReadOnly: false},
		{Key: "claude-code", Agent: "Claude Code 用户目录", Path: filepath.Join(claudeHome, "skills"), SourceType: "user", ReadOnly: false},
		{Key: "codex", Agent: "Codex 用户目录", Path: filepath.Join(codexHome, "skills"), SourceType: "user", ReadOnly: false},
		{Key: "opencode", Agent: "OpenCode 用户目录", Path: openCodeDir, SourceType: "user", ReadOnly: false},
		{Key: "cursor", Agent: "Cursor 用户目录", Path: filepath.Join(home, ".cursor", "skills"), SourceType: "user", ReadOnly: false},
		{Key: "codex-system", Agent: "Codex 内置 Skill", Path: filepath.Join(codexHome, "skills", ".system"), SourceType: "codex-system", ReadOnly: true},
	}

	pluginLocations, err := discoverClaudePluginLocations(claudeHome)
	if err != nil {
		return nil, err
	}
	locations = append(locations, pluginLocations...)
	for index := range locations {
		_, statErr := os.Stat(locations[index].Path)
		locations[index].Exists = statErr == nil
	}
	return locations, nil
}

func discoverClaudePluginLocations(claudeHome string) ([]Location, error) {
	type installedPlugin struct {
		InstallPath string `json:"installPath"`
	}
	type installedPluginsFile struct {
		Plugins map[string][]installedPlugin `json:"plugins"`
	}

	content, err := os.ReadFile(filepath.Join(claudeHome, "plugins", "installed_plugins.json"))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var installed installedPluginsFile
	if err := json.Unmarshal(content, &installed); err != nil {
		return nil, fmt.Errorf("解析 Claude Code 已安装插件清单失败: %w", err)
	}

	locations := make([]Location, 0)
	seen := make(map[string]struct{})
	for pluginID, versions := range installed.Plugins {
		pluginName := strings.SplitN(pluginID, "@", 2)[0]
		for _, plugin := range versions {
			root := filepath.Clean(plugin.InstallPath)
			if root == "." || root == "" {
				continue
			}
			if _, err := os.Stat(root); errors.Is(err, fs.ErrNotExist) {
				continue
			} else if err != nil {
				return nil, err
			}
			err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
				if walkErr != nil {
					return walkErr
				}
				if !entry.IsDir() {
					return nil
				}
				name := strings.ToLower(entry.Name())
				if name == ".git" || name == "node_modules" {
					return fs.SkipDir
				}
				if name != "skills" || filepath.Clean(path) == root {
					return nil
				}
				relative, err := filepath.Rel(root, path)
				if err != nil {
					return err
				}
				key := "claude-plugin-cache:" + filepath.ToSlash(root) + ":" + filepath.ToSlash(relative)
				if _, exists := seen[key]; !exists {
					seen[key] = struct{}{}
					locations = append(locations, Location{Key: key, Agent: "Claude 插件 · " + pluginName, Path: path, SourceType: "claude-plugin-cache", ReadOnly: true})
				}
				return fs.SkipDir
			})
			if err != nil {
				return nil, err
			}
		}
	}
	sort.Slice(locations, func(i, j int) bool { return locations[i].Key < locations[j].Key })
	return locations, nil
}

func projectRoot() (string, error) {
	workingDir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	if filepath.Base(workingDir) == "backend-go" {
		workingDir = filepath.Dir(workingDir)
	}
	return filepath.Join(workingDir, "skills"), nil
}

// Resolve 把「目录 key + Skill 名」解析为具体条目，并校验路径不逃逸出所属目录。
func Resolve(locationKey, name, openCodeSkillsDir string) (Item, error) {
	if !namePattern.MatchString(name) {
		return Item{}, errors.New("Skill 名称必须为小写字母、数字和连字符")
	}
	locations, err := Locations(openCodeSkillsDir)
	if err != nil {
		return Item{}, err
	}
	for _, location := range locations {
		if location.Key == locationKey {
			path := filepath.Join(location.Path, name)
			if filepath.Dir(path) != filepath.Clean(location.Path) {
				return Item{}, errors.New("Skill 路径无效")
			}
			if _, err := os.Stat(filepath.Join(path, "SKILL.md")); err != nil {
				return Item{}, fmt.Errorf("Skill 不存在: %s", name)
			}
			return Item{LocationKey: location.Key, Agent: location.Agent, Name: name, Path: path, SourceType: location.SourceType, ReadOnly: location.ReadOnly}, nil
		}
	}
	return Item{}, errors.New("未知 Skill 目录")
}
