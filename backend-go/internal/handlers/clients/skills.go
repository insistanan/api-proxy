package clients

import (
	"encoding/base64"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/insistanan/api-proxy/internal/admin/skills"
)

// SkillsAPI 聚合 skills 管理的互斥锁，由 main.go 构造并注入路由，
// 替代包级全局状态；互斥锁串行化对多个 agent 技能目录的读写操作。
type SkillsAPI struct {
	mu sync.Mutex
}

// NewSkillsAPI 创建 skills 管理 API。
func NewSkillsAPI() *SkillsAPI {
	return &SkillsAPI{}
}

// openCodeSkillsDir 由本层解析：OpenCode 配置路径的多级回退规则归 OpenCode 配置页所有，
// internal/admin/skills 不重复实现该规则，改为按参数接收。
func openCodeSkillsDir() string {
	return filepath.Join(filepath.Dir(resolveOpenCodeConfigPath()), "skills")
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

// ListSkills 仅枚举当前运行用户的已知 Agent Skill 目录，避免把任意路径暴露为文件浏览器。
func (s *SkillsAPI) List() gin.HandlerFunc {
	return func(c *gin.Context) {
		s.mu.Lock()
		defer s.mu.Unlock()

		locations, err := skills.Locations(openCodeSkillsDir())
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
			found, err := skills.Scan(location)
			if err != nil {
				c.JSON(500, gin.H{"error": fmt.Sprintf("扫描 %s 失败: %v", location.Path, err)})
				return
			}
			items = append(items, found...)
		}
		sort.Slice(items, func(i, j int) bool {
			if items[i].Agent == items[j].Agent {
				return items[i].Name < items[j].Name
			}
			return items[i].Agent < items[j].Agent
		})
		c.JSON(200, gin.H{"locations": locations, "skills": items})
	}
}

func (s *SkillsAPI) GetContent() gin.HandlerFunc {
	return func(c *gin.Context) {
		ref, ok := bindSkillReference(c)
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
		ref, ok := bindSkillReference(c)
		if !ok {
			return
		}
		original, err := skills.ReadContent(ref)
		if err != nil {
			c.JSON(500, gin.H{"error": fmt.Sprintf("读取原 Skill 失败: %v", err)})
			return
		}
		backup, found, err := skills.LatestBackup(ref, original)
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
		if err := skills.WriteGlobalMetadata(req.Name, skills.GlobalMetadata{Note: note, UpdatedAt: time.Now().Format(time.RFC3339)}); err != nil {
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

		locations, err := skills.Locations(openCodeSkillsDir())
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
		for _, location := range locations {
			items, err := skills.Scan(location)
			if err != nil {
				c.JSON(500, gin.H{"error": fmt.Sprintf("扫描 %s 失败: %v", location.Path, err)})
				return
			}
			for _, item := range items {
				if !item.Valid {
					continue
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

		names := make([]string, 0, len(selected))
		for name := range selected {
			names = append(names, name)
		}
		sort.Strings(names)
		imported := 0
		for _, name := range names {
			item := selected[name]
			if item.LocationKey == "project" {
				continue
			}
			destination := filepath.Join(projectLocation.Path, item.Name)
			if err := skills.CopyDir(item.Path, destination, true); err != nil {
				c.JSON(500, gin.H{"error": fmt.Sprintf("归档 %s 失败: %v", item.Name, err)})
				return
			}
			imported++
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
		locations, err := skills.Locations(openCodeSkillsDir())
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
			if err := skills.WriteFilesForLocation(location, skillName, files); err != nil {
				c.JSON(500, gin.H{"error": fmt.Sprintf("安装到 %s 失败: %v", location.Agent, err)})
				return
			}
		}
		c.JSON(200, gin.H{"success": true, "name": skillName})
	}
}

func (s *SkillsAPI) Delete() gin.HandlerFunc {
	return func(c *gin.Context) {
		ref, ok := bindSkillReference(c)
		if !ok {
			return
		}
		s.mu.Lock()
		defer s.mu.Unlock()
		if ref.ReadOnly {
			c.JSON(400, gin.H{"error": "该 Skill 来自插件缓存或内置目录，不能直接删除；请通过原 Agent 的插件管理功能移除"})
			return
		}
		if err := skills.Remove(ref); err != nil {
			c.JSON(500, gin.H{"error": fmt.Sprintf("删除 Skill 失败: %v", err)})
			return
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
		locations, err := skills.Locations(openCodeSkillsDir())
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
		c.JSON(200, gin.H{"success": true, "name": req.Name, "targets": req.Targets})
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
		ref, err := skills.Resolve(req.LocationKey, req.Name, openCodeSkillsDir())
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
		backupDir, err := skills.SaveTranslationBackup(ref, original, skills.BackupInput{
			Translated: req.Translated, Model: req.Model, ChannelName: req.ChannelName,
		})
		if err != nil {
			c.JSON(500, gin.H{"error": err.Error()})
			return
		}
		c.JSON(200, gin.H{"success": true, "path": backupDir})
	}
}

func bindSkillReference(c *gin.Context) (skills.Item, bool) {
	var req skillReferenceRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(400, gin.H{"error": "Skill 引用无效"})
		return skills.Item{}, false
	}
	ref, err := skills.Resolve(req.LocationKey, req.Name, openCodeSkillsDir())
	if err != nil {
		c.JSON(400, gin.H{"error": err.Error()})
		return skills.Item{}, false
	}
	return ref, true
}
