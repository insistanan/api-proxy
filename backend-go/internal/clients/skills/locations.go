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

// BaseLocations 枚举当前运行用户的基础 Agent Skill 目录，独立于插件发现。
// opts 中的 OpenCodeSkillsDir / PiSkillsDir / ProjectDir 由调用方传入，未传时按默认规则回退；
// 其余客户端目录（Claude Code / Codex / Cursor）本包按各自环境变量自行解析。
func BaseLocations(opts LocationOptions) ([]Location, error) {
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
	openCodeDir := opts.OpenCodeSkillsDir
	piDir := strings.TrimSpace(opts.PiSkillsDir)
	if piDir == "" {
		piDir = defaultPiSkillsDir(home)
	} else {
		piDir = absPath(piDir)
	}
	projectDir, err := resolveProjectDir(opts.ProjectDir)
	if err != nil {
		return nil, err
	}
	locations := []Location{
		{Key: "project", Agent: "项目 Skills 目录", Path: projectDir, SourceType: "project", ReadOnly: false},
		{Key: "agents", Agent: "通用 Agent Skills", Path: filepath.Join(home, ".agents", "skills"), SourceType: "user", ReadOnly: false},
		{Key: "claude-code", Agent: "Claude Code 用户目录", Path: filepath.Join(claudeHome, "skills"), SourceType: "user", ReadOnly: false},
		{Key: "codex", Agent: "Codex 用户目录", Path: filepath.Join(codexHome, "skills"), SourceType: "user", ReadOnly: false},
		{Key: "opencode", Agent: "OpenCode 用户目录", Path: openCodeDir, SourceType: "user", ReadOnly: false},
		{Key: "pi", Agent: "pi 用户目录", Path: piDir, SourceType: "user", ReadOnly: false},
		{Key: "cursor", Agent: "Cursor 用户目录", Path: filepath.Join(home, ".cursor", "skills"), SourceType: "user", ReadOnly: false},
		{Key: "codex-system", Agent: "Codex 内置 Skill", Path: filepath.Join(codexHome, "skills", ".system"), SourceType: "codex-system", ReadOnly: true},
	}

	for index := range locations {
		_, statErr := os.Stat(locations[index].Path)
		locations[index].Exists = statErr == nil
		if statErr != nil && !errors.Is(statErr, fs.ErrNotExist) {
			locations[index].Error = statErr.Error()
		}
	}
	return locations, nil
}

// Locations 供列表发现使用；插件错误作为独立位置返回，不阻断基础目录操作。
func Locations(opts LocationOptions) ([]Location, error) {
	locations, err := BaseLocations(opts)
	if err != nil {
		return nil, err
	}
	var claudeHome string
	for _, location := range locations {
		if location.Key == "claude-code" {
			claudeHome = filepath.Dir(location.Path)
		}
	}
	plugins, err := discoverClaudePluginLocations(claudeHome)
	if err != nil {
		locations = append(locations, Location{Key: "claude-plugin-error", Agent: "Claude 插件", ReadOnly: true, Error: err.Error()})
	}
	return append(locations, plugins...), nil
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
				locations = append(locations, Location{Key: "claude-plugin-cache:" + root, Agent: "Claude 插件 · " + pluginName, Path: root, ReadOnly: true, Error: err.Error()})
				continue
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
					locations = append(locations, Location{Key: key, Agent: "Claude 插件 · " + pluginName, Path: path, SourceType: "claude-plugin-cache", ReadOnly: true, Exists: true})
				}
				return fs.SkipDir
			})
			if err != nil {
				locations = append(locations, Location{Key: "claude-plugin-error:" + root, Agent: "Claude 插件 · " + pluginName, Path: root, ReadOnly: true, Error: err.Error()})
			}
		}
	}
	sort.Slice(locations, func(i, j int) bool { return locations[i].Key < locations[j].Key })
	return locations, nil
}

func defaultPiSkillsDir(home string) string {
	for _, envName := range []string{"PI_CODING_AGENT_DIR", "PI_AGENT_CONFIG_DIR"} {
		if configured := strings.TrimSpace(os.Getenv(envName)); configured != "" {
			return filepath.Join(absPath(configured), "skills")
		}
	}
	return filepath.Join(home, ".pi", "agent", "skills")
}

// ProjectDir 返回按技能目录规则解析的项目根目录。
func ProjectDir(projectDir string) (string, error) {
	return resolveProjectDir(projectDir)
}

func resolveProjectDir(projectDir string) (string, error) {
	if trimmed := strings.TrimSpace(projectDir); trimmed != "" {
		return absPath(trimmed), nil
	}
	return projectRoot()
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
func Resolve(locationKey, name string, opts LocationOptions) (Item, error) {
	if !namePattern.MatchString(name) {
		return Item{}, errors.New("Skill 名称必须为小写字母、数字和连字符")
	}
	locations, err := BaseLocations(opts)
	if strings.HasPrefix(locationKey, "claude-plugin-cache:") {
		locations, err = Locations(opts)
	}
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
				return Item{}, fmt.Errorf("读取 Skill %s 失败: %w", name, err)
			}
			return Item{LocationKey: location.Key, Agent: location.Agent, Name: name, Path: path, SourceType: location.SourceType, ReadOnly: location.ReadOnly}, nil
		}
	}
	return Item{}, errors.New("未知 Skill 目录")
}
