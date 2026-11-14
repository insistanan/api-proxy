package clients

import (
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/insistanan/api-proxy/internal/clients/piagent"
	"github.com/insistanan/api-proxy/internal/clients/skills"
)

// SkillsAPI 聚合 skills 管理的互斥锁与项目目录解析，由 main.go 构造并注入路由，
// 替代包级全局状态；互斥锁串行化对多个 agent 技能目录的读写操作。
type SkillsAPI struct {
	mu          sync.Mutex
	remoteCache remoteSkillCache
	skillsDirFn func() (string, error)
}

// NewSkillsAPI 创建 skills 管理 API；skillsDirFn 返回当前配置的项目 Skills 目录路径。
func NewSkillsAPI(skillsDirFn func() (string, error)) *SkillsAPI {
	return &SkillsAPI{skillsDirFn: skillsDirFn}
}

// WithLock 在 SkillsAPI 互斥锁保护下执行目录迁移或 Git 操作，避免与并发 Skill IO 竞态。
func (s *SkillsAPI) WithLock(fn func() error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return fn()
}

// SyncOnStartup 在服务启动或 Git 拉取后，按项目目录中的 skills-manifest.json 自动同步到各 Agent 目录。
func (s *SkillsAPI) SyncOnStartup() (skills.SyncReport, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	opts, err := s.locationOptions()
	if err != nil {
		return skills.SyncReport{}, err
	}
	return skills.SyncManifestToAgents(opts)
}

func (s *SkillsAPI) locationOptions() (skills.LocationOptions, error) {
	projectDir := ""
	if s.skillsDirFn != nil {
		resolved, err := s.skillsDirFn()
		if err != nil {
			return skills.LocationOptions{}, fmt.Errorf("解析项目 Skills 目录失败: %w", err)
		}
		projectDir = resolved
	}
	return skills.LocationOptions{
		OpenCodeSkillsDir: openCodeSkillsDir(),
		PiSkillsDir:       piSkillsDir(),
		ProjectDir:        projectDir,
	}, nil
}

// openCodeSkillsDir 由本层解析：OpenCode 配置路径的多级回退规则归 OpenCode 配置页所有，
// internal/clients/skills 不重复实现该规则，改为按参数接收。
func openCodeSkillsDir() string {
	return filepath.Join(filepath.Dir(resolveOpenCodeConfigPath()), "skills")
}

func piSkillsDir() string {
	if dir := strings.TrimSpace(os.Getenv("PI_CODING_AGENT_DIR")); dir != "" {
		return filepath.Join(absolutePath(dir), "skills")
	}
	if dir, err := piagent.ResolveConfigDir(); err == nil && dir != "" {
		return filepath.Join(dir, "skills")
	}
	return ""
}

type skillImportRequest struct {
	FileName      string   `json:"fileName"`
	ContentBase64 string   `json:"contentBase64"`
	Targets       []string `json:"targets"`
}

type skillReferenceRequest struct {
	LocationKey string `json:"locationKey"`
	Name        string `json:"name"`
}

type skillBackupRequest struct {
	LocationKey string `json:"locationKey"`
	Name        string `json:"name"`
	Original    string `json:"original"`
	Translated  string `json:"translated"`
	Model       string `json:"model"`
	ChannelName string `json:"channelName"`
}

type skillNoteRequest struct {
	Name string `json:"name"`
	Note string `json:"note"`
}

type skillCopyRequest struct {
	LocationKey string   `json:"locationKey"`
	Name        string   `json:"name"`
	Targets     []string `json:"targets"`
}

type skillToggleRequest struct {
	Name    string `json:"name"`
	Agent   string `json:"agent"`
	Enabled *bool  `json:"enabled"`
}

// ListSkills 仅枚举当前运行用户的已知 Agent Skill 目录，避免把任意路径暴露为文件浏览器。
func (s *SkillsAPI) List() gin.HandlerFunc {
	return func(c *gin.Context) {
		s.mu.Lock()
		defer s.mu.Unlock()

		opts, err := s.locationOptions()
		if err != nil {
			c.JSON(500, gin.H{"error": err.Error()})
			return
		}
		locations, err := skills.Locations(opts)
		if err != nil {
			c.JSON(500, gin.H{"error": fmt.Sprintf("解析 Skills 目录失败: %v", err)})
			return
		}

		items := make([]skills.Item, 0)
		for _, location := range locations {
			if location.Key == "project" {
				if err := skills.RestoreProjectBackups(location.Path); err != nil {
					c.JSON(500, gin.H{"error": fmt.Sprintf("整理项目 Skills 备份失败: %v", err)})
					return
				}
			}
			found, err := skills.Scan(location, opts.ProjectDir)
			if err != nil {
				c.JSON(500, gin.H{"error": fmt.Sprintf("扫描 %s 失败: %v", location.Path, err)})
				return
			}
			items = append(items, found...)
		}
		manifest, err := skills.ReadManifest(opts.ProjectDir)
		if err != nil {
			c.JSON(500, gin.H{"error": err.Error()})
			return
		}
		sort.Slice(items, func(i, j int) bool {
			if items[i].Agent == items[j].Agent {
				return items[i].Name < items[j].Name
			}
			return items[i].Agent < items[j].Agent
		})
		c.JSON(200, gin.H{
			"locations":     locations,
			"skills":        items,
			"manifest":      manifest.Skills,
			"managedAgents": skills.ManagedAgentKeys,
		})
	}
}

func (s *SkillsAPI) GetContent() gin.HandlerFunc {
	return func(c *gin.Context) {
		s.mu.Lock()
		defer s.mu.Unlock()
		opts, err := s.locationOptions()
		if err != nil {
			c.JSON(500, gin.H{"error": err.Error()})
			return
		}
		ref, ok := bindSkillReference(c, opts)
		if !ok {
			return
		}
		content, err := skills.ReadContent(ref)
		if err != nil {
			c.JSON(500, gin.H{"error": fmt.Sprintf("读取 Skill 失败: %v", err)})
			return
		}
		c.JSON(200, gin.H{"content": string(content), "path": skills.ContentPath(ref)})
	}
}

// GetLatestSkillBackup 返回与当前内容匹配的最近一份译文。
// 相同原文的不同 Agent 副本共享译文；原文内容变化时不恢复旧译文。
func (s *SkillsAPI) GetLatestBackup() gin.HandlerFunc {
	return func(c *gin.Context) {
		s.mu.Lock()
		defer s.mu.Unlock()
		opts, err := s.locationOptions()
		if err != nil {
			c.JSON(500, gin.H{"error": err.Error()})
			return
		}
		ref, ok := bindSkillReference(c, opts)
		if !ok {
			return
		}
		original, err := skills.ReadContent(ref)
		if err != nil {
			c.JSON(500, gin.H{"error": fmt.Sprintf("读取原 Skill 失败: %v", err)})
			return
		}
		backup, found, err := skills.LatestBackup(opts.ProjectDir, ref, original)
		if err != nil {
			c.JSON(500, gin.H{"error": fmt.Sprintf("读取翻译备份失败: %v", err)})
			return
		}
		if !found {
			c.JSON(200, gin.H{"found": false})
			return
		}
		c.JSON(200, gin.H{
			"found": true, "translated": backup.Translated, "path": backup.Path,
			"createdAt": backup.Metadata.CreatedAt, "model": backup.Metadata.Model, "channelName": backup.Metadata.ChannelName,
		})
	}
}

// UpdateSkillNote 保存项目级备注。同名 Skill 无论位于哪个 Agent 目录都会读取这份备注。
func (s *SkillsAPI) UpdateNote() gin.HandlerFunc {
	return func(c *gin.Context) {
		var req skillNoteRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(400, gin.H{"error": "Skill 备注请求无效"})
			return
		}
		if !skills.ValidName(req.Name) {
			c.JSON(400, gin.H{"error": "Skill 名称必须为小写字母、数字和连字符"})
			return
		}
		note := strings.TrimSpace(req.Note)
		if len([]rune(note)) > 500 {
			c.JSON(400, gin.H{"error": "备注不能超过 500 个字符"})
			return
		}
		s.mu.Lock()
		defer s.mu.Unlock()
		opts, err := s.locationOptions()
		if err != nil {
			c.JSON(500, gin.H{"error": err.Error()})
			return
		}
		if err := skills.WriteGlobalMetadata(opts.ProjectDir, req.Name, skills.GlobalMetadata{Note: note, UpdatedAt: time.Now().Format(time.RFC3339)}); err != nil {
			c.JSON(500, gin.H{"error": fmt.Sprintf("保存 Skill 备注失败: %v", err)})
			return
		}
		c.JSON(200, gin.H{"success": true, "note": note})
	}
}

// ConsolidateSkills 将已安装在各 Agent 目录中的有效 Skill 去重后归档到项目目录。
// 已存在的项目副本优先保留，避免其他来源的同名版本覆盖项目中的统一版本。
func (s *SkillsAPI) Consolidate() gin.HandlerFunc {
	return func(c *gin.Context) {
		s.mu.Lock()
		defer s.mu.Unlock()

		opts, err := s.locationOptions()
		if err != nil {
			c.JSON(500, gin.H{"error": err.Error()})
			return
		}
		locations, err := skills.Locations(opts)
		if err != nil {
			c.JSON(500, gin.H{"error": fmt.Sprintf("解析 Skills 目录失败: %v", err)})
			return
		}
		var projectLocation *skills.Location
		for index := range locations {
			if locations[index].Key == "project" {
				projectLocation = &locations[index]
				break
			}
		}
		if projectLocation == nil {
			c.JSON(500, gin.H{"error": "未找到项目 Skills 目录"})
			return
		}
		if err := skills.RestoreProjectBackups(projectLocation.Path); err != nil {
			c.JSON(500, gin.H{"error": fmt.Sprintf("整理项目 Skills 备份失败: %v", err)})
			return
		}

		selected := make(map[string]skills.Item)
		duplicates := make(map[string]struct{})
		agentPresence := make(map[string][]string)
		for _, location := range locations {
			items, err := skills.Scan(location, opts.ProjectDir)
			if err != nil {
				c.JSON(500, gin.H{"error": fmt.Sprintf("扫描 %s 失败: %v", location.Path, err)})
				return
			}
			for _, item := range items {
				if !item.Valid {
					continue
				}
				if skills.IsManagedAgentKey(location.Key) {
					agentPresence[item.Name] = append(agentPresence[item.Name], location.Key)
				}
				if existing, found := selected[item.Name]; found {
					if existing.LocationKey != item.LocationKey {
						duplicates[item.Name] = struct{}{}
					}
					continue
				}
				selected[item.Name] = item
			}
		}

		manifest, err := skills.ReadManifest(opts.ProjectDir)
		if err != nil {
			c.JSON(500, gin.H{"error": err.Error()})
			return
		}
		manifestChanged := false

		names := make([]string, 0, len(selected))
		for name := range selected {
			names = append(names, name)
		}
		sort.Strings(names)
		imported := 0
		for _, name := range names {
			item := selected[name]
			if item.LocationKey != "project" {
				destination := filepath.Join(projectLocation.Path, item.Name)
				if err := skills.CopyDir(item.Path, destination, true); err != nil {
					c.JSON(500, gin.H{"error": fmt.Sprintf("归档 %s 失败: %v", item.Name, err)})
					return
				}
				imported++
			}
			if _, exists := manifest.Skills[name]; !exists {
				manifest.Skills[name] = agentPresence[name]
				manifestChanged = true
			}
		}
		if manifestChanged {
			if err := skills.WriteManifest(opts.ProjectDir, manifest); err != nil {
				c.JSON(500, gin.H{"error": err.Error()})
				return
			}
		}
		c.JSON(200, gin.H{"success": true, "total": len(selected), "imported": imported, "duplicates": len(duplicates)})
	}
}

func (s *SkillsAPI) Import() gin.HandlerFunc {
	return func(c *gin.Context) {
		var req skillImportRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(400, gin.H{"error": "Skill 导入请求无效"})
			return
		}
		if len(req.Targets) == 0 {
			c.JSON(400, gin.H{"error": "至少选择一个安装目标"})
			return
		}
		content, err := base64.StdEncoding.DecodeString(req.ContentBase64)
		if err != nil || len(content) == 0 {
			c.JSON(400, gin.H{"error": "导入文件内容无效"})
			return
		}
		if len(content) > skills.MaxImportBytes {
			c.JSON(400, gin.H{"error": "导入文件不能超过 20 MB"})
			return
		}

		s.mu.Lock()
		defer s.mu.Unlock()
		opts, err := s.locationOptions()
		if err != nil {
			c.JSON(500, gin.H{"error": err.Error()})
			return
		}
		locations, err := skills.Locations(opts)
		if err != nil {
			c.JSON(500, gin.H{"error": err.Error()})
			return
		}
		locationByKey := make(map[string]skills.Location, len(locations))
		for _, location := range locations {
			locationByKey[location.Key] = location
		}

		files, skillName, err := skills.ParseImported(req.FileName, content)
		if err != nil {
			c.JSON(400, gin.H{"error": err.Error()})
			return
		}
		targets := skills.EnsureProjectTarget(req.Targets)
		targetLocations := make([]skills.Location, 0, len(targets))
		for _, target := range targets {
			location, exists := locationByKey[target]
			if !exists {
				c.JSON(400, gin.H{"error": fmt.Sprintf("未知安装目标: %s", target)})
				return
			}
			if location.ReadOnly {
				c.JSON(400, gin.H{"error": fmt.Sprintf("安装目标 %s 为只读目录，请选择用户 Skill 目录", location.Agent)})
				return
			}
			targetLocations = append(targetLocations, location)
		}
		if err := skills.InstallFiles(targetLocations, skillName, files); err != nil {
			c.JSON(500, gin.H{"error": fmt.Sprintf("安装失败: %v", err)})
			return
		}
		if err := skills.RecordSkillTargets(opts.ProjectDir, skillName, targets, true); err != nil {
			c.JSON(500, gin.H{"error": fmt.Sprintf("记录 Skill 启用清单失败: %v", err)})
			return
		}
		if _, err := skills.SyncManifestToAgents(opts); err != nil {
			c.JSON(500, gin.H{"error": fmt.Sprintf("同步 Skill 到 Agent 失败: %v", err)})
			return
		}
		c.JSON(200, gin.H{"success": true, "name": skillName})
	}
}

func (s *SkillsAPI) Delete() gin.HandlerFunc {
	return func(c *gin.Context) {
		s.mu.Lock()
		defer s.mu.Unlock()
		opts, err := s.locationOptions()
		if err != nil {
			c.JSON(500, gin.H{"error": err.Error()})
			return
		}
		ref, ok := bindSkillReference(c, opts)
		if !ok {
			return
		}
		if ref.ReadOnly {
			c.JSON(400, gin.H{"error": "该 Skill 来自插件缓存或内置目录，不能直接删除；请通过原 Agent 的插件管理功能移除"})
			return
		}
		if err := skills.Remove(ref); err != nil {
			c.JSON(500, gin.H{"error": fmt.Sprintf("删除 Skill 失败: %v", err)})
			return
		}
		if ref.LocationKey == "project" {
			if err := skills.RemoveSkillFromManifest(opts.ProjectDir, ref.Name); err != nil {
				c.JSON(500, gin.H{"error": fmt.Sprintf("更新 Skill 清单失败: %v", err)})
				return
			}
		} else if skills.IsManagedAgentKey(ref.LocationKey) {
			if err := skills.RemoveAgentFromSkillManifest(opts.ProjectDir, ref.Name, ref.LocationKey); err != nil {
				c.JSON(500, gin.H{"error": fmt.Sprintf("更新 Skill 清单失败: %v", err)})
				return
			}
		}
		c.JSON(200, gin.H{"success": true})
	}
}

// CopySkill 将用户目录中的 Skill（包含 SKILL.md 及其 references/scripts 等资源）复制到其他可写 Agent 目录。
// 插件缓存和内置 Skill 是外部管理的只读来源，不允许作为复制源或复制目标。
func (s *SkillsAPI) Copy() gin.HandlerFunc {
	return func(c *gin.Context) {
		var req skillCopyRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(400, gin.H{"error": "Skill 复制请求无效"})
			return
		}
		if len(req.Targets) == 0 {
			c.JSON(400, gin.H{"error": "至少选择一个复制目标"})
			return
		}

		s.mu.Lock()
		defer s.mu.Unlock()
		opts, err := s.locationOptions()
		if err != nil {
			c.JSON(500, gin.H{"error": err.Error()})
			return
		}
		locations, err := skills.Locations(opts)
		if err != nil {
			c.JSON(500, gin.H{"error": err.Error()})
			return
		}
		locationByKey := make(map[string]skills.Location, len(locations))
		for _, location := range locations {
			locationByKey[location.Key] = location
		}
		sourceLocation, exists := locationByKey[req.LocationKey]
		if !exists {
			c.JSON(400, gin.H{"error": "未知 Skill 来源目录"})
			return
		}
		if sourceLocation.ReadOnly {
			c.JSON(400, gin.H{"error": "内置或插件 Skill 为只读来源，不能复制；请先复制用户目录中的 Skill"})
			return
		}
		if !skills.ValidName(req.Name) {
			c.JSON(400, gin.H{"error": "Skill 名称必须为小写字母、数字和连字符"})
			return
		}
		sourcePath := filepath.Join(sourceLocation.Path, req.Name)
		if filepath.Dir(sourcePath) != filepath.Clean(sourceLocation.Path) {
			c.JSON(400, gin.H{"error": "Skill 路径无效"})
			return
		}
		for _, target := range req.Targets {
			location, ok := locationByKey[target]
			if !ok {
				c.JSON(400, gin.H{"error": fmt.Sprintf("未知复制目标: %s", target)})
				return
			}
			if location.ReadOnly {
				c.JSON(400, gin.H{"error": fmt.Sprintf("复制目标 %s 为只读目录", location.Agent)})
				return
			}
			if target == req.LocationKey {
				c.JSON(400, gin.H{"error": "复制目标不能与来源相同"})
				return
			}
		}
		for _, target := range req.Targets {
			location := locationByKey[target]
			destination := filepath.Join(location.Path, req.Name)
			if filepath.Dir(destination) != filepath.Clean(location.Path) {
				c.JSON(400, gin.H{"error": "Skill 目标路径无效"})
				return
			}
			if err := skills.CopyDir(sourcePath, destination, location.Key == "project"); err != nil {
				c.JSON(500, gin.H{"error": fmt.Sprintf("复制到 %s 失败: %v", location.Agent, err)})
				return
			}
		}
		if req.LocationKey == "project" {
			if err := skills.RecordSkillTargets(opts.ProjectDir, req.Name, req.Targets, true); err != nil {
				c.JSON(500, gin.H{"error": fmt.Sprintf("更新 Skill 清单失败: %v", err)})
				return
			}
		} else if slices.Contains(req.Targets, "project") {
			targets := append([]string{req.LocationKey}, req.Targets...)
			if err := skills.RecordSkillTargets(opts.ProjectDir, req.Name, targets, true); err != nil {
				c.JSON(500, gin.H{"error": fmt.Sprintf("更新 Skill 清单失败: %v", err)})
				return
			}
		}
		c.JSON(200, gin.H{"success": true, "name": req.Name, "targets": req.Targets})
	}
}

// Toggle 切换单个项目 Skill 在指定 Agent 上的启用状态，并立即写入 manifest 与对应 Agent 目录。
func (s *SkillsAPI) Toggle() gin.HandlerFunc {
	return func(c *gin.Context) {
		var req skillToggleRequest
		if err := c.ShouldBindJSON(&req); err != nil || req.Enabled == nil {
			c.JSON(400, gin.H{"error": "Skill 开关请求无效"})
			return
		}
		s.mu.Lock()
		defer s.mu.Unlock()
		opts, err := s.locationOptions()
		if err != nil {
			c.JSON(500, gin.H{"error": err.Error()})
			return
		}
		manifest, err := skills.ToggleAgent(opts, req.Name, req.Agent, *req.Enabled)
		if err != nil {
			c.JSON(400, gin.H{"error": err.Error()})
			return
		}
		c.JSON(200, gin.H{"success": true, "manifest": manifest.Skills})
	}
}

// Sync 按项目目录中的 skills-manifest.json 全量同步 Skill 到各 Agent 目录。
func (s *SkillsAPI) Sync() gin.HandlerFunc {
	return func(c *gin.Context) {
		s.mu.Lock()
		defer s.mu.Unlock()
		opts, err := s.locationOptions()
		if err != nil {
			c.JSON(500, gin.H{"error": err.Error()})
			return
		}
		report, err := skills.SyncManifestToAgents(opts)
		if err != nil {
			c.JSON(500, gin.H{"error": fmt.Sprintf("同步 Skills 失败: %v", err)})
			return
		}
		c.JSON(200, gin.H{"success": true, "report": report})
	}
}

// BackupSkill 将原文和翻译文本并列保存，并同步项目目录副本，不回写来源 Agent 的 SKILL.md。
func (s *SkillsAPI) Backup() gin.HandlerFunc {
	return func(c *gin.Context) {
		var req skillBackupRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(400, gin.H{"error": "Skill 备份请求无效"})
			return
		}
		if strings.TrimSpace(req.Translated) == "" {
			c.JSON(400, gin.H{"error": "翻译文本不能为空"})
			return
		}
		s.mu.Lock()
		defer s.mu.Unlock()
		opts, err := s.locationOptions()
		if err != nil {
			c.JSON(500, gin.H{"error": err.Error()})
			return
		}
		ref, err := skills.Resolve(req.LocationKey, req.Name, opts)
		if err != nil {
			c.JSON(400, gin.H{"error": err.Error()})
			return
		}
		original, err := skills.ReadContent(ref)
		if err != nil {
			c.JSON(500, gin.H{"error": fmt.Sprintf("读取原 Skill 失败: %v", err)})
			return
		}
		if req.Original == "" || skills.ContentSHA256([]byte(req.Original)) != skills.ContentSHA256(original) {
			c.JSON(409, gin.H{"error": "原 Skill 已变更，请重新打开后再保存译文"})
			return
		}
		backupDir, err := skills.SaveTranslationBackup(opts.ProjectDir, ref, original, skills.BackupInput{
			Translated: req.Translated, Model: req.Model, ChannelName: req.ChannelName,
		})
		if err != nil {
			c.JSON(500, gin.H{"error": err.Error()})
			return
		}
		c.JSON(200, gin.H{"success": true, "path": backupDir})
	}
}

func bindSkillReference(c *gin.Context, opts skills.LocationOptions) (skills.Item, bool) {
	var req skillReferenceRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(400, gin.H{"error": "Skill 引用无效"})
		return skills.Item{}, false
	}
	ref, err := skills.Resolve(req.LocationKey, req.Name, opts)
	if err != nil {
		c.JSON(400, gin.H{"error": err.Error()})
		return skills.Item{}, false
	}
	return ref, true
}
