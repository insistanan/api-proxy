// Package skills 管理各 Agent（Claude Code / Codex / OpenCode / pi / Cursor 等）的
// Agent Skill 目录：目录发现、扫描校验、导入复制、译文备份、项目级元数据与多端启用清单。
// 本包只做领域逻辑，不含任何 HTTP 语义；HTTP 层在 internal/clients/skills.go。
package skills

import (
	"path/filepath"
	"regexp"
)

// MaxImportBytes 限制单次导入的文件大小（含 ZIP 内单个文件）。
const MaxImportBytes = 20 << 20

const (
	// maxCopyFileBytes 限制单次复制单个文件大小，防止误把超大文件塞进 Skill 目录后流式拷贝爆盘。
	maxCopyFileBytes = 30 << 20
	backupsDirName   = ".backups"
)

var (
	namePattern      = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)
	backupDirPattern = regexp.MustCompile(`^\d{8}-\d{6}\.\d+$`)
)

// ValidName 校验 Skill 名（小写字母、数字与连字符），HTTP 层入参校验复用它，
// 避免把 namePattern 这个包内正则暴露出去。
func ValidName(name string) bool { return namePattern.MatchString(name) }

// absPath 解析为绝对路径，失败时原样返回。与 piagent 包同样的就地解析方式：
// 这类环境变量目录解析属于各领域包自身职责，不上升为公共能力。
func absPath(path string) string {
	abs, err := filepath.Abs(path)
	if err != nil {
		return path
	}
	return abs
}

// LocationOptions 为目录发现与同步提供调用方上下文。
// OpenCode / pi 配置目录的多级回退规则由调用方传入（未传时使用环境变量或默认用户目录）；
// ProjectDir 为空时回退到工作区根下的 skills/。
type LocationOptions struct {
	OpenCodeSkillsDir string
	PiSkillsDir       string
	ProjectDir        string
}

// Location 描述一个受管的 Skill 目录。ReadOnly 为 true 的来源（插件缓存、内置目录）
// 由外部工具管理，不允许作为写入目标。
type Location struct {
	Key        string `json:"key"`
	Agent      string `json:"agent"`
	Path       string `json:"path"`
	SourceType string `json:"sourceType"`
	ReadOnly   bool   `json:"readOnly"`
	Exists     bool   `json:"exists"`
}

// Item 描述一个 Skill 条目。Valid 为 false 时 Issue 说明原因。
type Item struct {
	LocationKey           string `json:"locationKey"`
	Agent                 string `json:"agent"`
	Name                  string `json:"name"`
	DisplayName           string `json:"displayName,omitempty"`
	Language              string `json:"language,omitempty"`
	HasTranslation        bool   `json:"hasTranslation,omitempty"`
	TranslatedName        string `json:"translatedName,omitempty"`
	TranslatedDescription string `json:"translatedDescription,omitempty"`
	TranslationLanguage   string `json:"translationLanguage,omitempty"`
	Note                  string `json:"note,omitempty"`
	Description           string `json:"description"`
	Path                  string `json:"path"`
	Size                  int64  `json:"size"`
	ModifiedAt            string `json:"modifiedAt"`
	Valid                 bool   `json:"valid"`
	Issue                 string `json:"issue,omitempty"`
	SourceType            string `json:"sourceType"`
	ReadOnly              bool   `json:"readOnly"`
}

// BackupMetadata 是译文备份目录里 metadata.json 的结构。
type BackupMetadata struct {
	Name         string `json:"name"`
	SourcePath   string `json:"sourcePath"`
	SourceSHA256 string `json:"sourceSha256,omitempty"`
	Agent        string `json:"agent"`
	Model        string `json:"model"`
	ChannelName  string `json:"channelName"`
	CreatedAt    string `json:"createdAt"`
}

// GlobalMetadata 是项目级、跨 Agent 共享的 Skill 备注。
type GlobalMetadata struct {
	Note      string `json:"note"`
	UpdatedAt string `json:"updatedAt"`
}
