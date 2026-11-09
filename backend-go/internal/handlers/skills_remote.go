package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	pathpkg "path"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/insistanan/api-proxy/internal/skills"
)

const (
	skillsSearchEndpoint     = "https://skills.sh/api/search"
	githubAPIEndpoint        = "https://api.github.com"
	remoteSkillHTTPTimeout   = 30 * time.Second
	maxSkillSearchResponse   = 2 << 20
	maxGitHubTreeResponse    = 12 << 20
	maxRemoteSkillFiles      = 128
	maxRemoteSkillTotalBytes = 20 << 20
	maxRemoteSkillFileBytes  = 5 << 20
)

var githubSegmentPattern = regexp.MustCompile(`^[A-Za-z0-9_.-]+$`)

type skillSearchResult struct {
	ID            string `json:"id"`
	SkillID       string `json:"skillId"`
	Name          string `json:"name"`
	Source        string `json:"source"`
	Installs      int64  `json:"installs"`
	RepositoryURL string `json:"repositoryUrl,omitempty"`
	SkillsURL     string `json:"skillsUrl,omitempty"`
}

type skillSearchAPIResponse struct {
	Query      string              `json:"query"`
	SearchType string              `json:"searchType"`
	Skills     []skillSearchResult `json:"skills"`
}

type remoteSkillRequest struct {
	ID string `json:"id"`
}

type remoteSkillInstallRequest struct {
	ID      string   `json:"id"`
	Targets []string `json:"targets"`
}

type remoteSkillFileInfo struct {
	Path string `json:"path"`
	Size int64  `json:"size"`
}

type remoteSkillPreview struct {
	ID              string                `json:"id"`
	Name            string                `json:"name"`
	Description     string                `json:"description"`
	Source          string                `json:"source"`
	RepositoryURL   string                `json:"repositoryUrl"`
	SkillURL        string                `json:"skillUrl"`
	License         string                `json:"license,omitempty"`
	Files           []remoteSkillFileInfo `json:"files"`
	ContainsScripts bool                  `json:"containsScripts"`
}

type remoteSkillPackage struct {
	ID              string
	Name            string
	Description     string
	Source          string
	RepositoryURL   string
	SkillURL        string
	License         string
	Files           map[string][]byte
	FileInfo        []remoteSkillFileInfo
	ContainsScripts bool
}

type githubRepositoryResponse struct {
	DefaultBranch string `json:"default_branch"`
	HTMLURL       string `json:"html_url"`
	License       *struct {
		Name   string `json:"name"`
		SPDXID string `json:"spdx_id"`
	} `json:"license"`
}

type githubTreeResponse struct {
	Tree      []githubTreeEntry `json:"tree"`
	Truncated bool              `json:"truncated"`
}

type githubTreeEntry struct {
	Path string `json:"path"`
	Type string `json:"type"`
	Size int64  `json:"size"`
}

// SearchSkills 对接 skills.sh 的公开搜索接口。搜索由后端发起，避免浏览器端的 CORS 和网络错误分散处理。
func (s *SkillsAPI) Search() gin.HandlerFunc {
	return func(c *gin.Context) {
		query := strings.TrimSpace(c.Query("q"))
		if query == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "搜索关键词不能为空"})
			return
		}
		if len([]rune(query)) > 100 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "搜索关键词不能超过 100 个字符"})
			return
		}

		endpoint := skillsSearchEndpoint + "?q=" + url.QueryEscape(query)
		var payload skillSearchAPIResponse
		if err := fetchJSON(c.Request.Context(), endpoint, maxSkillSearchResponse, &payload); err != nil {
			c.JSON(http.StatusBadGateway, gin.H{"error": fmt.Sprintf("skills.sh 搜索失败: %v", err)})
			return
		}
		for index := range payload.Skills {
			result := &payload.Skills[index]
			result.RepositoryURL, result.SkillsURL = remoteSkillLinks(result.ID, result.Source)
		}
		if payload.Skills == nil {
			payload.Skills = []skillSearchResult{}
		}
		c.JSON(http.StatusOK, payload)
	}
}

// InspectRemoteSkill 只下载并校验远程 Skill 的元数据和文件清单，不写入本机目录。
func (s *SkillsAPI) InspectRemote() gin.HandlerFunc {
	return func(c *gin.Context) {
		var req remoteSkillRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "远程 Skill 请求无效"})
			return
		}
		pkg, err := loadRemoteSkill(c.Request.Context(), req.ID)
		if err != nil {
			c.JSON(http.StatusBadGateway, gin.H{"error": fmt.Sprintf("读取远程 Skill 失败: %v", err)})
			return
		}
		c.JSON(http.StatusOK, remoteSkillPreview{
			ID:              pkg.ID,
			Name:            pkg.Name,
			Description:     pkg.Description,
			Source:          pkg.Source,
			RepositoryURL:   pkg.RepositoryURL,
			SkillURL:        pkg.SkillURL,
			License:         pkg.License,
			Files:           pkg.FileInfo,
			ContainsScripts: pkg.ContainsScripts,
		})
	}
}

// InstallRemoteSkill 从 GitHub 暂存并校验远程 Skill，然后安装到用户可写的目标目录。
func (s *SkillsAPI) InstallRemote() gin.HandlerFunc {
	return func(c *gin.Context) {
		var req remoteSkillInstallRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "远程 Skill 安装请求无效"})
			return
		}
		if len(req.Targets) == 0 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "至少选择一个安装目标"})
			return
		}
		pkg, err := loadRemoteSkill(c.Request.Context(), req.ID)
		if err != nil {
			c.JSON(http.StatusBadGateway, gin.H{"error": fmt.Sprintf("读取远程 Skill 失败: %v", err)})
			return
		}

		s.mu.Lock()
		defer s.mu.Unlock()
		locations, err := skills.Locations(openCodeSkillsDir())
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		locationByKey := make(map[string]skills.Location, len(locations))
		for _, location := range locations {
			locationByKey[location.Key] = location
		}
		targets := skills.EnsureProjectTarget(req.Targets)
		for _, target := range targets {
			location, exists := locationByKey[target]
			if !exists {
				c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("未知安装目标: %s", target)})
				return
			}
			if location.ReadOnly {
				c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("安装目标 %s 为只读目录，请选择用户 Skill 目录", location.Agent)})
				return
			}
		}
		for _, target := range targets {
			location := locationByKey[target]
			if err := skills.WriteFilesForLocation(location, pkg.Name, pkg.Files); err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("安装到 %s 失败: %v", location.Agent, err)})
				return
			}
		}
		c.JSON(http.StatusOK, gin.H{
			"success":       true,
			"name":          pkg.Name,
			"source":        pkg.Source,
			"repositoryUrl": pkg.RepositoryURL,
		})
	}
}

func remoteSkillLinks(id, source string) (string, string) {
	source = strings.TrimSpace(source)
	if source == "" {
		parts := strings.Split(strings.Trim(id, "/"), "/")
		if len(parts) >= 2 {
			source = parts[0] + "/" + parts[1]
		}
	}
	if source == "" {
		return "", "https://skills.sh/" + strings.Trim(id, "/")
	}
	return "https://github.com/" + source, "https://skills.sh/" + strings.Trim(id, "/")
}

func loadRemoteSkill(ctx context.Context, id string) (*remoteSkillPackage, error) {
	owner, repository, requestedPath, err := parseRemoteSkillID(id)
	if err != nil {
		return nil, err
	}

	var repositoryInfo githubRepositoryResponse
	if err := fetchJSON(ctx, githubAPIPath("repos", owner, repository), maxSkillSearchResponse, &repositoryInfo); err != nil {
		return nil, fmt.Errorf("读取 GitHub 仓库信息失败: %w", err)
	}
	branch := strings.TrimSpace(repositoryInfo.DefaultBranch)
	if branch == "" {
		return nil, errors.New("GitHub 仓库没有默认分支")
	}

	var tree githubTreeResponse
	if err := fetchJSON(ctx, githubAPIPath("repos", owner, repository, "git", "trees", branch)+"?recursive=1", maxGitHubTreeResponse, &tree); err != nil {
		return nil, fmt.Errorf("读取 GitHub 文件树失败: %w", err)
	}
	if tree.Truncated {
		return nil, errors.New("GitHub 文件树过大，无法安全定位 Skill；请直接上传 ZIP")
	}
	skillFile, skillRoot, err := findRemoteSkillFile(tree.Tree, requestedPath)
	if err != nil {
		return nil, err
	}

	fileEntries := make([]githubTreeEntry, 0)
	for _, entry := range tree.Tree {
		if entry.Type != "blob" || (entry.Path != skillFile && !strings.HasPrefix(entry.Path, skillRoot+"/")) {
			continue
		}
		if !validRemoteRelativePath(strings.TrimPrefix(entry.Path, skillRoot+"/")) && entry.Path != skillFile {
			return nil, fmt.Errorf("远程 Skill 包含无效文件路径: %s", entry.Path)
		}
		fileEntries = append(fileEntries, entry)
	}
	if len(fileEntries) == 0 {
		return nil, errors.New("远程 Skill 没有可安装的文件")
	}
	if len(fileEntries) > maxRemoteSkillFiles {
		return nil, fmt.Errorf("远程 Skill 文件数量超过 %d 个", maxRemoteSkillFiles)
	}

	files := make(map[string][]byte, len(fileEntries))
	fileInfo := make([]remoteSkillFileInfo, 0, len(fileEntries))
	var totalSize int64
	containsScripts := false
	for _, entry := range fileEntries {
		relative := "SKILL.md"
		if entry.Path != skillFile {
			relative = strings.TrimPrefix(entry.Path, skillRoot+"/")
		}
		if relative == "" || !validRemoteRelativePath(relative) {
			return nil, fmt.Errorf("远程 Skill 包含无效文件路径: %s", entry.Path)
		}
		if entry.Size > maxRemoteSkillFileBytes {
			return nil, fmt.Errorf("远程 Skill 文件过大: %s", relative)
		}
		content, err := fetchRemoteFile(ctx, owner, repository, branch, entry.Path)
		if err != nil {
			return nil, fmt.Errorf("下载远程文件 %s 失败: %w", relative, err)
		}
		totalSize += int64(len(content))
		if totalSize > maxRemoteSkillTotalBytes {
			return nil, fmt.Errorf("远程 Skill 总大小超过 %d MB", maxRemoteSkillTotalBytes/(1<<20))
		}
		files[relative] = content
		fileInfo = append(fileInfo, remoteSkillFileInfo{Path: relative, Size: int64(len(content))})
		if strings.HasPrefix(strings.ToLower(relative), "scripts/") || strings.Contains(strings.ToLower(relative), "/scripts/") {
			containsScripts = true
		}
	}
	skillContent, exists := files["SKILL.md"]
	if !exists {
		return nil, errors.New("远程 Skill 缺少 SKILL.md")
	}
	name, description, err := skills.ParseFrontmatter(skillContent)
	if err != nil {
		return nil, fmt.Errorf("远程 SKILL.md 校验失败: %w", err)
	}
	repositoryURL := strings.TrimSpace(repositoryInfo.HTMLURL)
	if repositoryURL == "" {
		repositoryURL = "https://github.com/" + owner + "/" + repository
	}
	skillURL := repositoryURL + "/tree/" + escapeURLPath(branch) + "/" + escapeURLPath(skillRoot)
	license := ""
	if repositoryInfo.License != nil {
		license = strings.TrimSpace(repositoryInfo.License.SPDXID)
		if license == "" {
			license = strings.TrimSpace(repositoryInfo.License.Name)
		}
	}
	sort.Slice(fileInfo, func(i, j int) bool { return fileInfo[i].Path < fileInfo[j].Path })
	return &remoteSkillPackage{
		ID:              strings.Trim(id, "/"),
		Name:            name,
		Description:     description,
		Source:          owner + "/" + repository,
		RepositoryURL:   repositoryURL,
		SkillURL:        skillURL,
		License:         license,
		Files:           files,
		FileInfo:        fileInfo,
		ContainsScripts: containsScripts,
	}, nil
}

func parseRemoteSkillID(raw string) (string, string, string, error) {
	value := strings.TrimSpace(raw)
	if strings.HasPrefix(value, "http://") || strings.HasPrefix(value, "https://") {
		parsed, err := url.Parse(value)
		if err != nil || parsed.Host != "skills.sh" {
			return "", "", "", errors.New("远程 Skill 只支持 skills.sh 的 Skill ID")
		}
		value = strings.Trim(parsed.Path, "/")
	}
	parts := strings.Split(value, "/")
	if len(parts) < 3 || !githubSegmentPattern.MatchString(parts[0]) || !githubSegmentPattern.MatchString(parts[1]) {
		return "", "", "", errors.New("远程 Skill ID 必须为 owner/repository/skill")
	}
	requestedPath := strings.Join(parts[2:], "/")
	if !validRemoteRelativePath(requestedPath) {
		return "", "", "", errors.New("远程 Skill 路径无效")
	}
	return parts[0], parts[1], requestedPath, nil
}

func findRemoteSkillFile(tree []githubTreeEntry, requestedPath string) (string, string, error) {
	requestedPath = strings.Trim(pathpkg.Clean(strings.ReplaceAll(requestedPath, "\\", "/")), "/")
	candidates := []string{requestedPath + "/SKILL.md", "skills/" + requestedPath + "/SKILL.md"}
	for _, candidate := range candidates {
		for _, entry := range tree {
			if entry.Type == "blob" && strings.EqualFold(entry.Path, candidate) {
				return entry.Path, pathpkg.Dir(entry.Path), nil
			}
		}
	}
	suffix := "/" + requestedPath + "/SKILL.md"
	matched := ""
	for _, entry := range tree {
		if entry.Type != "blob" || !strings.HasSuffix(strings.ToLower(entry.Path), strings.ToLower(suffix)) {
			continue
		}
		if matched == "" || len(entry.Path) < len(matched) {
			matched = entry.Path
		}
	}
	if matched != "" {
		return matched, pathpkg.Dir(matched), nil
	}
	return "", "", fmt.Errorf("GitHub 仓库中找不到 %s/SKILL.md", requestedPath)
}

func validRemoteRelativePath(value string) bool {
	if value == "" || strings.ContainsRune(value, '\x00') || strings.HasPrefix(value, "/") || strings.Contains(value, "\\") {
		return false
	}
	cleaned := pathpkg.Clean(value)
	return cleaned == value && cleaned != "." && cleaned != ".." && !strings.HasPrefix(cleaned, "../")
}

func githubAPIPath(parts ...string) string {
	result := strings.TrimSuffix(githubAPIEndpoint, "/")
	for _, part := range parts {
		result += "/" + url.PathEscape(part)
	}
	return result
}

func fetchJSON(ctx context.Context, endpoint string, maxBytes int64, target interface{}) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return err
	}
	request.Header.Set("Accept", "application/vnd.github+json")
	request.Header.Set("User-Agent", "api-proxy-skill-manager")
	client := &http.Client{Timeout: remoteSkillHTTPTimeout}
	response, err := client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	body, err := readLimitedBody(response.Body, maxBytes)
	if err != nil {
		return err
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		var detail struct {
			Message string `json:"message"`
		}
		if json.Unmarshal(body, &detail) == nil && strings.TrimSpace(detail.Message) != "" {
			return fmt.Errorf("HTTP %d: %s", response.StatusCode, detail.Message)
		}
		return fmt.Errorf("HTTP %d", response.StatusCode)
	}
	if err := json.Unmarshal(body, target); err != nil {
		return fmt.Errorf("响应格式无效: %w", err)
	}
	return nil
}

func fetchRemoteFile(ctx context.Context, owner, repository, branch, filePath string) ([]byte, error) {
	endpoint := "https://raw.githubusercontent.com/" + escapeURLPath(owner) + "/" + escapeURLPath(repository) + "/" + escapeURLPath(branch) + "/" + escapeURLPath(filePath)
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("User-Agent", "api-proxy-skill-manager")
	client := &http.Client{Timeout: remoteSkillHTTPTimeout}
	response, err := client.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	body, err := readLimitedBody(response.Body, maxRemoteSkillFileBytes)
	if err != nil {
		return nil, err
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, fmt.Errorf("HTTP %d", response.StatusCode)
	}
	return body, nil
}

func escapeURLPath(value string) string {
	parts := strings.Split(strings.Trim(value, "/"), "/")
	for index, part := range parts {
		parts[index] = url.PathEscape(part)
	}
	return strings.Join(parts, "/")
}

func readLimitedBody(reader io.Reader, maxBytes int64) ([]byte, error) {
	if maxBytes <= 0 {
		return nil, errors.New("响应大小限制无效")
	}
	body, err := io.ReadAll(io.LimitReader(reader, maxBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(body)) > maxBytes {
		return nil, fmt.Errorf("响应超过 %d MB", maxBytes/(1<<20))
	}
	return body, nil
}
