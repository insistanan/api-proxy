package clients

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/insistanan/api-proxy/internal/api/common"
	"io"
	"net/http"
	"net/url"
	pathpkg "path"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/insistanan/api-proxy/internal/clients/skills"
)

const (
	skillsSearchEndpoint        = "https://skills.sh/api/search"
	githubAPIEndpoint           = "https://api.github.com"
	githubCodeloadEndpoint      = "https://codeload.github.com"
	remoteSkillAPITimeout       = 30 * time.Second
	remoteSkillArchiveTimeout   = 120 * time.Second
	remoteSkillDownloadAttempts = 3
	// skills.sh 实测响应 10~25 秒且偶发 TLS 中断，30 秒单次超时过紧。
	skillsSearchTimeout      = 45 * time.Second
	skillsSearchAttempts     = 2
	maxSkillSearchResponse   = 2 << 20
	maxGitHubTreeResponse    = 12 << 20
	maxRemoteArchiveBytes    = 128 << 20
	maxRemoteSkillFiles      = 128
	maxRemoteSkillTotalBytes = 20 << 20
	maxRemoteSkillFileBytes  = 5 << 20
)

var (
	githubSegmentPattern = regexp.MustCompile(`^[A-Za-z0-9_.-]+$`)
	commitSHAPattern     = regexp.MustCompile(`^[0-9a-fA-F]{40,64}$`)
	// remoteSkillHTTPClient 复用连接池，超时由每次请求的 context 控制。
	remoteSkillHTTPClient = &http.Client{}
	// remoteSkillRetryBaseDelay 是归档下载重试的起始退避，测试可覆盖。
	remoteSkillRetryBaseDelay = 500 * time.Millisecond
)

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
	Commit          string                `json:"commit"`
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
	Commit          string
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

type githubCommitResponse struct {
	SHA string `json:"sha"`
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

// remoteSkillFile 是 Skill 包内的文件及其相对 Skill 根目录的路径。
type remoteSkillFile struct {
	entry    githubTreeEntry
	relative string
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
		if err := fetchJSONWithRetry(c.Request.Context(), endpoint, maxSkillSearchResponse, &payload, skillsSearchTimeout, skillsSearchAttempts); err != nil {
			c.JSON(http.StatusBadGateway, gin.H{"error": skillsSearchErrorMessage(err)})
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
		pkg, err := s.loadRemoteSkill(c.Request.Context(), req.ID)
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
			Commit:          pkg.Commit,
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
		pkg, err := s.loadRemoteSkill(c.Request.Context(), req.ID)
		if err != nil {
			c.JSON(http.StatusBadGateway, gin.H{"error": fmt.Sprintf("读取远程 Skill 失败: %v", err)})
			return
		}

		s.mu.Lock()
		defer s.mu.Unlock()
		opts, err := s.locationOptions()
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		locations, err := skills.Locations(opts)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		locationByKey := make(map[string]skills.Location, len(locations))
		for _, location := range locations {
			locationByKey[location.Key] = location
		}
		targets := skills.EnsureProjectTarget(req.Targets)
		targetLocations := make([]skills.Location, 0, len(targets))
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
			targetLocations = append(targetLocations, location)
		}
		if err := skills.InstallFiles(targetLocations, pkg.Name, pkg.Files); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("安装失败: %v", err)})
			return
		}
		if err := skills.RecordSkillTargets(opts.ProjectDir, pkg.Name, targets, true); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("记录 Skill 启用清单失败: %v", err)})
			return
		}
		if _, err := skills.SyncManifestToAgents(opts); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("同步 Skill 到 Agent 失败: %v", err)})
			return
		}
		c.JSON(http.StatusOK, gin.H{
			"success":       true,
			"name":          pkg.Name,
			"source":        pkg.Source,
			"repositoryUrl": pkg.RepositoryURL,
			"commit":        pkg.Commit,
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

// loadRemoteSkill 读取远程 Skill 并缓存完整文件包：预览与安装共用同一份
// 已固定 commit 的内容，避免重复下载，也避免两次请求之间仓库更新导致版本漂移。
func (s *SkillsAPI) loadRemoteSkill(ctx context.Context, id string) (*remoteSkillPackage, error) {
	owner, repository, requestedPath, err := parseRemoteSkillID(id)
	if err != nil {
		return nil, err
	}
	cacheKey := owner + "/" + repository + "/" + requestedPath
	if pkg, ok := s.remoteCache.get(cacheKey); ok {
		return pkg, nil
	}
	pkg, err := loadRemoteSkillFromGitHub(ctx, owner, repository, requestedPath, strings.Trim(id, "/"))
	if err != nil {
		return nil, err
	}
	s.remoteCache.put(cacheKey, pkg)
	return pkg, nil
}

func loadRemoteSkillFromGitHub(ctx context.Context, owner, repository, requestedPath, id string) (*remoteSkillPackage, error) {
	var repositoryInfo githubRepositoryResponse
	if err := fetchJSON(ctx, githubAPIPath("repos", owner, repository), maxSkillSearchResponse, &repositoryInfo); err != nil {
		return nil, fmt.Errorf("读取 GitHub 仓库信息失败: %w", err)
	}
	branch := strings.TrimSpace(repositoryInfo.DefaultBranch)
	if branch == "" {
		return nil, errors.New("GitHub 仓库没有默认分支")
	}

	// 先固定 commit，再用同一 commit 读取文件树和归档，保证预览内容与安装内容一致。
	var commitInfo githubCommitResponse
	if err := fetchJSON(ctx, githubAPIPath("repos", owner, repository, "commits", branch), maxSkillSearchResponse, &commitInfo); err != nil {
		return nil, fmt.Errorf("读取 GitHub 提交信息失败: %w", err)
	}
	commit := strings.TrimSpace(commitInfo.SHA)
	if !commitSHAPattern.MatchString(commit) {
		return nil, fmt.Errorf("GitHub 返回的提交哈希无效: %q", commit)
	}

	var tree githubTreeResponse
	if err := fetchJSON(ctx, githubAPIPath("repos", owner, repository, "git", "trees", commit)+"?recursive=1", maxGitHubTreeResponse, &tree); err != nil {
		return nil, fmt.Errorf("读取 GitHub 文件树失败: %w", err)
	}
	if tree.Truncated {
		return nil, errors.New("GitHub 文件树过大，无法安全定位 Skill；请直接上传 ZIP")
	}
	skillFile, skillRoot, err := findRemoteSkillFile(tree.Tree, requestedPath)
	if err != nil {
		return nil, err
	}

	fileEntries, err := collectRemoteSkillFiles(tree.Tree, skillFile, skillRoot)
	if err != nil {
		return nil, err
	}
	if len(fileEntries) == 0 {
		return nil, errors.New("远程 Skill 没有可安装的文件")
	}
	if len(fileEntries) > maxRemoteSkillFiles {
		return nil, fmt.Errorf("远程 Skill 文件数量超过 %d 个", maxRemoteSkillFiles)
	}

	wanted := make(map[string]string, len(fileEntries))
	for _, file := range fileEntries {
		if file.entry.Size > maxRemoteSkillFileBytes {
			return nil, fmt.Errorf("远程 Skill 文件过大: %s", file.relative)
		}
		wanted[file.entry.Path] = file.relative
	}

	// 一次下载仓库归档，只保留 Skill 子树内的文件；避免逐文件请求的累计延迟与单文件超时。
	files, err := downloadRemoteSkillArchive(ctx, remoteSkillArchiveURL(owner, repository, commit), wanted)
	if err != nil {
		return nil, err
	}

	var totalSize int64
	fileInfo := make([]remoteSkillFileInfo, 0, len(files))
	containsScripts := false
	for relative, content := range files {
		totalSize += int64(len(content))
		if totalSize > maxRemoteSkillTotalBytes {
			return nil, fmt.Errorf("远程 Skill 总大小超过 %d MB", maxRemoteSkillTotalBytes/(1<<20))
		}
		fileInfo = append(fileInfo, remoteSkillFileInfo{Path: relative, Size: int64(len(content))})
		lower := strings.ToLower(relative)
		if strings.HasPrefix(lower, "scripts/") || strings.Contains(lower, "/scripts/") {
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
	skillURL := remoteSkillTreeURL(repositoryURL, commit, skillRoot)
	license := ""
	if repositoryInfo.License != nil {
		license = strings.TrimSpace(repositoryInfo.License.SPDXID)
		if license == "" {
			license = strings.TrimSpace(repositoryInfo.License.Name)
		}
	}
	sort.Slice(fileInfo, func(i, j int) bool { return fileInfo[i].Path < fileInfo[j].Path })
	return &remoteSkillPackage{
		ID:              id,
		Name:            name,
		Description:     description,
		Source:          owner + "/" + repository,
		RepositoryURL:   repositoryURL,
		SkillURL:        skillURL,
		Commit:          commit,
		License:         license,
		Files:           files,
		FileInfo:        fileInfo,
		ContainsScripts: containsScripts,
	}, nil
}

func remoteSkillArchiveURL(owner, repository, commit string) string {
	return strings.TrimSuffix(githubCodeloadEndpoint, "/") + "/" + escapeURLPath(owner) + "/" + escapeURLPath(repository) + "/tar.gz/" + escapeURLPath(commit)
}

// downloadRemoteSkillArchive 下载 GitHub 仓库归档并只提取 Skill 需要的文件。
// 网络错误、5xx 与 429 会重试；4xx 直接失败。
func downloadRemoteSkillArchive(ctx context.Context, endpoint string, wanted map[string]string) (map[string][]byte, error) {
	var lastErr error
	for attempt := 1; attempt <= remoteSkillDownloadAttempts; attempt++ {
		files, err := downloadRemoteSkillArchiveOnce(ctx, endpoint, wanted)
		if err == nil {
			return files, nil
		}
		lastErr = err
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if !retryableRemoteSkillError(err) {
			return nil, err
		}
		if attempt == remoteSkillDownloadAttempts {
			break
		}
		delay := remoteSkillRetryBaseDelay * time.Duration(1<<(attempt-1))
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(delay):
		}
	}
	return nil, fmt.Errorf("下载 GitHub 归档失败（已重试 %d 次）: %w", remoteSkillDownloadAttempts, lastErr)
}

func downloadRemoteSkillArchiveOnce(ctx context.Context, endpoint string, wanted map[string]string) (map[string][]byte, error) {
	attemptCtx, cancel := context.WithTimeout(ctx, remoteSkillArchiveTimeout)
	defer cancel()
	request, err := http.NewRequestWithContext(attemptCtx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("User-Agent", "api-proxy-skill-manager")
	response, err := remoteSkillHTTPClient.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, &remoteHTTPStatusError{StatusCode: response.StatusCode}
	}

	counter := &countingReader{reader: response.Body}
	files, err := extractRemoteSkillArchive(io.LimitReader(counter, maxRemoteArchiveBytes+1), wanted)
	if counter.read > maxRemoteArchiveBytes {
		return nil, fmt.Errorf("GitHub 仓库归档超过 %d MB", maxRemoteArchiveBytes/(1<<20))
	}
	if err != nil {
		return nil, err
	}
	return files, nil
}

// extractRemoteSkillArchive 从 GitHub 归档中提取 wanted 指定的文件。
// wanted 的键是仓库内路径，值是相对 Skill 根目录的路径；归档顶层目录会被剥离。
func extractRemoteSkillArchive(reader io.Reader, wanted map[string]string) (map[string][]byte, error) {
	gzipReader, err := gzip.NewReader(reader)
	if err != nil {
		return nil, fmt.Errorf("归档不是有效的 gzip: %w", err)
	}
	defer gzipReader.Close()

	files := make(map[string][]byte, len(wanted))
	var totalSize int64
	tarReader := tar.NewReader(gzipReader)
	for {
		header, err := tarReader.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("读取归档失败: %w", err)
		}
		repositoryPath, ok := archiveRepositoryPath(header.Name)
		if !ok {
			continue
		}
		relative, needed := wanted[repositoryPath]
		if !needed {
			continue
		}
		if _, exists := files[relative]; exists {
			return nil, fmt.Errorf("归档中存在重复文件: %s", relative)
		}

		var content []byte
		switch header.Typeflag {
		case tar.TypeReg, tar.TypeRegA:
			if header.Size > maxRemoteSkillFileBytes {
				return nil, fmt.Errorf("远程 Skill 文件过大: %s", relative)
			}
			content, err = common.ReadLimitedBody(tarReader, maxRemoteSkillFileBytes)
			if err != nil {
				return nil, fmt.Errorf("读取归档文件 %s 失败: %w", relative, err)
			}
		case tar.TypeSymlink:
			// GitHub 归档把符号链接保留为链接本身；与 raw 下载一致，安装链接文本而不是目标内容。
			content = []byte(header.Linkname)
		default:
			return nil, fmt.Errorf("远程 Skill 包含不支持的文件类型: %s", relative)
		}
		totalSize += int64(len(content))
		if totalSize > maxRemoteSkillTotalBytes {
			return nil, fmt.Errorf("远程 Skill 总大小超过 %d MB", maxRemoteSkillTotalBytes/(1<<20))
		}
		files[relative] = content
		if len(files) == len(wanted) {
			break
		}
	}

	missing := make([]string, 0)
	for _, relative := range wanted {
		if _, exists := files[relative]; !exists {
			missing = append(missing, relative)
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		return nil, fmt.Errorf("GitHub 归档缺少文件: %s", strings.Join(missing, ", "))
	}
	return files, nil
}

// archiveRepositoryPath 剥离 GitHub 归档的顶层目录（{repo}-{sha}/），返回仓库内路径。
func archiveRepositoryPath(name string) (string, bool) {
	cleaned := pathpkg.Clean(strings.ReplaceAll(name, "\\", "/"))
	if cleaned == "." || cleaned == ".." || strings.HasPrefix(cleaned, "../") || strings.HasPrefix(cleaned, "/") {
		return "", false
	}
	parts := strings.SplitN(cleaned, "/", 2)
	if len(parts) != 2 || parts[1] == "" {
		return "", false
	}
	return parts[1], true
}

type remoteHTTPStatusError struct {
	StatusCode int
	Message    string
}

func (e *remoteHTTPStatusError) Error() string {
	if e.Message != "" {
		return fmt.Sprintf("HTTP %d: %s", e.StatusCode, e.Message)
	}
	return fmt.Sprintf("HTTP %d", e.StatusCode)
}

func retryableRemoteSkillError(err error) bool {
	var statusErr *remoteHTTPStatusError
	if errors.As(err, &statusErr) {
		return statusErr.StatusCode == http.StatusTooManyRequests || statusErr.StatusCode >= 500
	}
	return true
}

type countingReader struct {
	reader io.Reader
	read   int64
}

func (r *countingReader) Read(buffer []byte) (int, error) {
	count, err := r.reader.Read(buffer)
	r.read += int64(count)
	return count, err
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
	lowerSuffix := strings.ToLower(suffix)
	matched := ""
	for _, entry := range tree {
		if entry.Type != "blob" || !strings.HasSuffix(strings.ToLower(entry.Path), lowerSuffix) {
			continue
		}
		if matched == "" || len(entry.Path) < len(matched) {
			matched = entry.Path
		}
	}
	if matched != "" {
		return matched, pathpkg.Dir(matched), nil
	}

	// skills.sh 的第三段是 Skill slug，不保证等于仓库内目录名。单技能仓库的
	// SKILL.md 可能位于根目录或任意目录（例如 skill/），此时全仓库只有一个
	// SKILL.md，可以无歧义地回退到它。
	skillFiles := make([]string, 0, 1)
	for _, entry := range tree {
		if entry.Type == "blob" && strings.EqualFold(pathpkg.Base(entry.Path), "SKILL.md") {
			skillFiles = append(skillFiles, entry.Path)
		}
	}
	if len(skillFiles) == 1 {
		return skillFiles[0], pathpkg.Dir(skillFiles[0]), nil
	}
	if len(skillFiles) > 1 {
		return "", "", fmt.Errorf("GitHub 仓库中存在多个 SKILL.md，无法确定 %s 对应的 Skill 目录；请直接上传 ZIP", requestedPath)
	}
	return "", "", fmt.Errorf("GitHub 仓库中找不到 %s/SKILL.md", requestedPath)
}

// collectRemoteSkillFiles 收集 Skill 包内的文件并计算相对 Skill 根目录的路径。
// 根目录用 "." 表示，此时仓库内所有 blob 都属于该 Skill；普通目录只收集其子树。
func collectRemoteSkillFiles(tree []githubTreeEntry, skillFile, skillRoot string) ([]remoteSkillFile, error) {
	prefix := ""
	if skillRoot != "" && skillRoot != "." {
		prefix = skillRoot + "/"
	}
	files := make([]remoteSkillFile, 0)
	for _, entry := range tree {
		if entry.Type != "blob" {
			continue
		}
		var relative string
		switch {
		case entry.Path == skillFile:
			relative = "SKILL.md"
		case prefix == "":
			relative = entry.Path
		case strings.HasPrefix(entry.Path, prefix):
			relative = strings.TrimPrefix(entry.Path, prefix)
		default:
			continue
		}
		if !validRemoteRelativePath(relative) {
			return nil, fmt.Errorf("远程 Skill 包含无效文件路径: %s", entry.Path)
		}
		files = append(files, remoteSkillFile{entry: entry, relative: relative})
	}
	return files, nil
}

// remoteSkillTreeURL 生成 Skill 在仓库中的目录链接。根目录 Skill 直接指向仓库树，
// 避免拼出 /tree/main/. 这类无效地址。
func remoteSkillTreeURL(repositoryURL, branch, skillRoot string) string {
	target := repositoryURL + "/tree/" + escapeURLPath(branch)
	if skillRoot == "" || skillRoot == "." {
		return target
	}
	return target + "/" + escapeURLPath(skillRoot)
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
	return fetchJSONWithRetry(ctx, endpoint, maxBytes, target, remoteSkillAPITimeout, 1)
}

// fetchJSONWithRetry 发起 JSON GET 请求；网络错误、超时、429 与 5xx 按指数退避重试。
func fetchJSONWithRetry(ctx context.Context, endpoint string, maxBytes int64, target interface{}, timeout time.Duration, attempts int) error {
	if attempts < 1 {
		attempts = 1
	}
	var lastErr error
	for attempt := 1; attempt <= attempts; attempt++ {
		lastErr = fetchJSONOnce(ctx, endpoint, maxBytes, target, timeout)
		if lastErr == nil {
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if !retryableJSONRequestError(lastErr) || attempt == attempts {
			break
		}
		delay := remoteSkillRetryBaseDelay * time.Duration(1<<(attempt-1))
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(delay):
		}
	}
	if attempts > 1 && retryableJSONRequestError(lastErr) {
		return fmt.Errorf("请求失败（已尝试 %d 次）: %w", attempts, lastErr)
	}
	return lastErr
}

func fetchJSONOnce(ctx context.Context, endpoint string, maxBytes int64, target interface{}, timeout time.Duration) error {
	requestCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	request, err := http.NewRequestWithContext(requestCtx, http.MethodGet, endpoint, nil)
	if err != nil {
		return err
	}
	request.Header.Set("Accept", "application/vnd.github+json")
	request.Header.Set("User-Agent", "api-proxy-skill-manager")
	response, err := remoteSkillHTTPClient.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	body, err := common.ReadLimitedBody(response.Body, maxBytes)
	if err != nil {
		return err
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		var detail struct {
			Message string `json:"message"`
		}
		_ = json.Unmarshal(body, &detail)
		return &remoteHTTPStatusError{StatusCode: response.StatusCode, Message: strings.TrimSpace(detail.Message)}
	}
	if err := json.Unmarshal(body, target); err != nil {
		return fmt.Errorf("响应格式无效: %w", err)
	}
	return nil
}

// retryableJSONRequestError 只重试可能瞬时恢复的失败，响应格式错误不重试。
func retryableJSONRequestError(err error) bool {
	var statusErr *remoteHTTPStatusError
	if errors.As(err, &statusErr) {
		return statusErr.StatusCode == http.StatusTooManyRequests || statusErr.StatusCode >= 500
	}
	var urlErr *url.Error
	return errors.As(err, &urlErr) || errors.Is(err, context.DeadlineExceeded) || errors.Is(err, io.ErrUnexpectedEOF)
}

func skillsSearchErrorMessage(err error) string {
	if errors.Is(err, context.DeadlineExceeded) {
		return "skills.sh 响应超时，请稍后重试"
	}
	if errors.Is(err, context.Canceled) {
		return "skills.sh 搜索已取消"
	}
	return fmt.Sprintf("skills.sh 搜索失败: %v", err)
}

func escapeURLPath(value string) string {
	parts := strings.Split(strings.Trim(value, "/"), "/")
	for index, part := range parts {
		parts[index] = url.PathEscape(part)
	}
	return strings.Join(parts, "/")
}
