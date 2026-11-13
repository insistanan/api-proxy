// Package piagent 管理 pi-agent（Pi Coding Agent）的模型供应商配置。
// pi-agent 使用本地目录 ~/.pi/agent 保存 models.json / auth.json / settings.json，
// 本模块负责对该目录的固定路径解析、安全读写、备份与脱敏，避免与 api-proxy
// 自身渠道配置耦合。
package piagent

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// FileKind 标识 pi-agent 目录下的三种配置文件。
type FileKind string

const (
	FileModels   FileKind = "models.json"
	FileAuth     FileKind = "auth.json"
	FileSettings FileKind = "settings.json"
)

var allFileKinds = []FileKind{FileModels, FileAuth, FileSettings}

// Config 描述 pi-agent 配置目录的解析结果。
type Config struct {
	// ConfigDir 解析后的绝对路径（~/.pi/agent 或 PI_AGENT_CONFIG_DIR 指定）。
	ConfigDir string
	// Exists 表示该目录是否存在。
	Exists bool
}

// ResolveConfigDir 解析 pi-agent 配置目录。
// 优先使用 PI_AGENT_CONFIG_DIR 环境变量（容器挂载场景），
// 否则回退到用户主目录下的 .pi/agent。禁止客户端传入任意路径。
func ResolveConfigDir() (string, error) {
	if configured := strings.TrimSpace(os.Getenv("PI_AGENT_CONFIG_DIR")); configured != "" {
		abs, err := filepath.Abs(configured)
		if err != nil {
			return "", fmt.Errorf("解析 PI_AGENT_CONFIG_DIR 失败: %w", err)
		}
		return abs, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("无法解析用户主目录: %w", err)
	}
	return filepath.Join(home, ".pi", "agent"), nil
}

// FilePath 返回指定配置文件的绝对路径。
func (c Config) FilePath(kind FileKind) string {
	return filepath.Join(c.ConfigDir, string(kind))
}

// Status 描述 pi-agent 配置目录及三个文件的存在与可写状态。
type Status struct {
	ConfigDir string                `json:"configDir"`
	Exists    bool                  `json:"exists"`
	Writable  bool                  `json:"writable"`
	Files     map[string]FileStatus `json:"files"`
}

// FileStatus 描述单个配置文件的存在、可写性与 SHA-256 revision。
type FileStatus struct {
	Path     string `json:"path"`
	Exists   bool   `json:"exists"`
	Writable bool   `json:"writable"`
	Revision string `json:"revision"`
}

// InspectStatus 探测配置目录与三个文件的状态，不修改任何文件。
func InspectStatus() (Status, error) {
	dir, err := ResolveConfigDir()
	if err != nil {
		return Status{}, err
	}
	status := Status{
		ConfigDir: dir,
		Exists:    dirExists(dir),
		Writable:  dirWritable(dir),
		Files:     make(map[string]FileStatus, len(allFileKinds)),
	}
	for _, kind := range allFileKinds {
		path := filepath.Join(dir, string(kind))
		fileStatus := FileStatus{
			Path:     path,
			Exists:   fileExists(path),
			Writable: dirWritable(dir),
		}
		if fileStatus.Exists {
			revision, err := RevisionOf(path)
			if err == nil {
				fileStatus.Revision = revision
			}
		}
		status.Files[string(kind)] = fileStatus
	}
	return status, nil
}

func dirExists(dir string) bool {
	info, err := os.Stat(dir)
	return err == nil && info.IsDir()
}

func dirWritable(dir string) bool {
	if !dirExists(dir) {
		// 状态探测保持只读；实际写入时由 acquireManagerLock 创建目录。
		return false
	}
	probe := filepath.Join(dir, ".piagent-write-probe")
	f, err := os.OpenFile(probe, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600)
	if err != nil {
		return false
	}
	f.Close()
	os.Remove(probe)
	return true
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}
