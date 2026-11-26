package extensions

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	adminshared "insistanan/api-proxy/internal/api/admin/shared"
	"insistanan/api-proxy/internal/clients"
	extensionfiles "insistanan/api-proxy/internal/clients/extensions"
	"insistanan/api-proxy/internal/clients/mcpsync"
	"insistanan/api-proxy/internal/clients/skills"
	"insistanan/api-proxy/internal/infra/config"
)

const gitCommandTimeout = 90 * time.Second

var errOriginRemoteMismatch = errors.New("仓库 origin 地址与设置中的仓库地址不一致，已停止操作且未修改 origin；请将设置改为当前仓库地址，或确认换仓库后手动修改 origin")

// ExtensionsGitAPI 为项目 Skills / MCP 目录提供显式的 Git 状态查询、初始化、克隆、拉取与推送接口。
// 与 PUT /api/settings 解耦：配置保存在离线时始终可用，Git 网络同步由用户显式触发。
type ExtensionsGitAPI struct {
	cfgManager *config.ConfigManager
	skillsAPI  *clients.SkillsAPI
	mcpAPI     *clients.MCPAPI
}

// NewExtensionsGitAPI 创建扩展目录 Git 同步处理器。
func NewExtensionsGitAPI(cfgManager *config.ConfigManager, skillsAPI *clients.SkillsAPI, mcpAPI *clients.MCPAPI) *ExtensionsGitAPI {
	return &ExtensionsGitAPI{
		cfgManager: cfgManager,
		skillsAPI:  skillsAPI,
		mcpAPI:     mcpAPI,
	}
}

// ExtensionsGitStatus 描述当前项目 Skills 目录的 Git 仓库状态。
type ExtensionsGitStatus struct {
	Migration      *extensionfiles.MigrationState `json:"migration,omitempty"`
	ExportFiles    []string                       `json:"exportFiles,omitempty"`
	ExportRevision string                         `json:"exportRevision,omitempty"`
	ExportError    string                         `json:"exportError,omitempty"`
	GitAvailable   bool                           `json:"gitAvailable"`
	SkillsDir      string                         `json:"skillsDir"`
	RepoURL        string                         `json:"repoUrl"`
	DirExists      bool                           `json:"dirExists"`
	IsGitRepo      bool                           `json:"isGitRepo"`
	Branch         string                         `json:"branch,omitempty"`
	RemoteURL      string                         `json:"remoteUrl,omitempty"`
	Dirty          bool                           `json:"dirty"`
	ChangedFiles   []string                       `json:"changedFiles,omitempty"`
	Ahead          int                            `json:"ahead"`
	Behind         int                            `json:"behind"`
	HasCommits     bool                           `json:"hasCommits"`
	Error          string                         `json:"error,omitempty"`
}

type extensionsGitSyncResponse struct {
	Status       ExtensionsGitStatus   `json:"status"`
	Message      string                `json:"message,omitempty"`
	SkillsReport *skills.SyncReport    `json:"skillsReport,omitempty"`
	MCPAgents    []mcpsync.AgentResult `json:"mcpAgents,omitempty"`
}

// Status 返回当前项目 Skills 目录的 Git 仓库状态。
func (api *ExtensionsGitAPI) Status() gin.HandlerFunc {
	return func(c *gin.Context) {
		var status ExtensionsGitStatus
		err := adminshared.WithExtensionsLocks(api.skillsAPI, api.mcpAPI, func() error {
			var inspectErr error
			status, inspectErr = api.inspectStatus(c.Request.Context())
			return inspectErr
		})
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, status)
	}
}

// Init 在项目 Skills 目录初始化独立的 Git 仓库，并按需配置 origin 远程地址。
func (api *ExtensionsGitAPI) Init() gin.HandlerFunc {
	return func(c *gin.Context) {
		var req struct {
			RepoURL string `json:"repoUrl"`
		}
		_ = c.ShouldBindJSON(&req)

		settings := api.cfgManager.GetSettings().Extensions
		repoURL := strings.TrimSpace(req.RepoURL)
		if repoURL == "" {
			repoURL = strings.TrimSpace(settings.SkillsRepoURL)
		}
		if err := config.ValidateGitRepoURL(repoURL); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		if _, err := exec.LookPath("git"); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "未检测到 git 命令，请先安装 Git"})
			return
		}

		var status ExtensionsGitStatus
		err := adminshared.WithExtensionsLocks(api.skillsAPI, api.mcpAPI, func() error {
			skillsDir, err := config.ResolveSkillsDir(settings.SkillsDir)
			if err != nil {
				return err
			}
			if err := os.MkdirAll(skillsDir, 0700); err != nil {
				return fmt.Errorf("创建 Skills 目录失败: %w", err)
			}
			ctx, cancel := context.WithTimeout(c.Request.Context(), gitCommandTimeout)
			defer cancel()

			if !isStandaloneGitRepo(ctx, skillsDir) {
				if _, err := runGit(ctx, skillsDir, "init"); err != nil {
					return fmt.Errorf("初始化 Git 仓库失败: %w", err)
				}
			}
			if repoURL != "" {
				if err := ensureOriginRemote(ctx, skillsDir, repoURL); err != nil {
					return err
				}
			}
			if err := ensureGitignore(skillsDir); err != nil {
				return err
			}
			status, err = api.inspectStatus(ctx)
			return err
		})
		if err != nil {
			httpCode := http.StatusInternalServerError
			if errors.Is(err, errOriginRemoteMismatch) {
				httpCode = http.StatusConflict
			}
			c.JSON(httpCode, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, extensionsGitSyncResponse{Status: status, Message: "Git 仓库已就绪"})
	}
}

// Clone 将远程仓库克隆到项目 Skills 目录（仅当目录不存在或为空时允许），并立即同步 Skills 与 MCP 到各 Agent。
func (api *ExtensionsGitAPI) Clone() gin.HandlerFunc {
	return func(c *gin.Context) {
		var req struct {
			RepoURL string `json:"repoUrl"`
		}
		_ = c.ShouldBindJSON(&req)

		settings := api.cfgManager.GetSettings().Extensions
		repoURL := strings.TrimSpace(req.RepoURL)
		if repoURL == "" {
			repoURL = strings.TrimSpace(settings.SkillsRepoURL)
		}
		if repoURL == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "请先配置 Git 仓库地址"})
			return
		}
		if err := config.ValidateGitRepoURL(repoURL); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		if _, err := exec.LookPath("git"); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "未检测到 git 命令，请先安装 Git"})
			return
		}

		httpCode := http.StatusInternalServerError
		var status ExtensionsGitStatus
		err := adminshared.WithExtensionsLocks(api.skillsAPI, api.mcpAPI, func() error {
			skillsDir, err := config.ResolveSkillsDir(settings.SkillsDir)
			if err != nil {
				httpCode = http.StatusBadRequest
				return err
			}
			if info, statErr := os.Stat(skillsDir); statErr == nil {
				if !info.IsDir() {
					httpCode = http.StatusBadRequest
					return errors.New("当前 Skills 路径不是目录")
				}
				entries, readErr := os.ReadDir(skillsDir)
				if readErr != nil {
					return fmt.Errorf("检查 Skills 目录失败: %w", readErr)
				}
				if len(entries) > 0 {
					httpCode = http.StatusBadRequest
					return errors.New("当前 Skills 目录非空，请先切换到一个空目录或清空后再克隆")
				}
			} else if !errors.Is(statErr, fs.ErrNotExist) {
				return fmt.Errorf("检查 Skills 目录失败: %w", statErr)
			}

			if err := os.MkdirAll(filepath.Dir(skillsDir), 0700); err != nil {
				return fmt.Errorf("创建父目录失败: %w", err)
			}
			ctx, cancel := context.WithTimeout(c.Request.Context(), gitCommandTimeout)
			defer cancel()
			// 明确指定目标根目录，等价于在空目录内 clone 到 .，不追加远程仓库名。
			if _, err := runGit(ctx, filepath.Dir(skillsDir), "clone", "--", repoURL, skillsDir); err != nil {
				return fmt.Errorf("克隆远程仓库失败: %w", err)
			}
			status, err = api.inspectStatus(ctx)
			return err
		})
		if err != nil {
			c.JSON(httpCode, gin.H{"error": err.Error()})
			return
		}

		skillsReport, mcpAgents := api.syncAgentsAfterUpdate()
		c.JSON(http.StatusOK, extensionsGitSyncResponse{
			Status:       status,
			Message:      "克隆完成并已同步到本地 Agent",
			SkillsReport: skillsReport,
			MCPAgents:    mcpAgents,
		})
	}
}

// Pull 从远程仓库 fast-forward 拉取最新 Skills 与 MCP 配置，并立即同步到各 Agent。
func (api *ExtensionsGitAPI) Pull() gin.HandlerFunc {
	return func(c *gin.Context) {
		if _, err := exec.LookPath("git"); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "未检测到 git 命令，请先安装 Git"})
			return
		}
		settings := api.cfgManager.GetSettings().Extensions
		httpCode := http.StatusInternalServerError
		var status ExtensionsGitStatus
		err := adminshared.WithExtensionsLocks(api.skillsAPI, api.mcpAPI, func() error {
			skillsDir, err := config.ResolveSkillsDir(settings.SkillsDir)
			if err != nil {
				httpCode = http.StatusBadRequest
				return err
			}
			ctx, cancel := context.WithTimeout(c.Request.Context(), gitCommandTimeout)
			defer cancel()

			if !isStandaloneGitRepo(ctx, skillsDir) {
				httpCode = http.StatusBadRequest
				return errors.New("当前 Skills 目录尚未初始化 Git 仓库")
			}
			if settings.SkillsRepoURL != "" {
				if err := ensureOriginRemote(ctx, skillsDir, settings.SkillsRepoURL); err != nil {
					if errors.Is(err, errOriginRemoteMismatch) {
						httpCode = http.StatusConflict
					}
					return err
				}
			}
			porcelain, err := runGit(ctx, skillsDir, "status", "--porcelain=v1")
			if err != nil {
				return fmt.Errorf("检查 Git 状态失败: %w", err)
			}
			if strings.TrimSpace(porcelain) != "" {
				httpCode = http.StatusConflict
				return errors.New("Skills 目录存在未提交的本地变更，请先在本地提交；若远程也有更新，合并历史后再推送")
			}
			if _, err := runGit(ctx, skillsDir, "pull", "--ff-only"); err != nil {
				return fmt.Errorf("Git 快进拉取失败，请检查上游分支；若历史已分叉，在仓库目录手动合并并解决冲突后再推送: %w", err)
			}
			status, err = api.inspectStatus(ctx)
			return err
		})
		if err != nil {
			c.JSON(httpCode, gin.H{"error": err.Error()})
			return
		}

		skillsReport, mcpAgents := api.syncAgentsAfterUpdate()
		c.JSON(http.StatusOK, extensionsGitSyncResponse{
			Status:       status,
			Message:      "已拉取更新并同步到本地 Agent",
			SkillsReport: skillsReport,
			MCPAgents:    mcpAgents,
		})
	}
}

// Push 自动提交项目 Skills 目录的变更并推送到远程仓库。
func (api *ExtensionsGitAPI) Push() gin.HandlerFunc {
	return func(c *gin.Context) {
		var req struct {
			Message        string `json:"message"`
			ExportRevision string `json:"exportRevision"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Git 推送请求无效"})
			return
		}
		commitMsg := strings.TrimSpace(req.Message)
		if commitMsg == "" {
			commitMsg = "chore: sync shareable skills"
		}
		if _, err := exec.LookPath("git"); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "未检测到 git 命令，请先安装 Git"})
			return
		}

		settings := api.cfgManager.GetSettings().Extensions
		httpCode := http.StatusInternalServerError
		var status ExtensionsGitStatus
		message := "已推送到远程仓库"

		err := adminshared.WithExtensionsLocks(api.skillsAPI, api.mcpAPI, func() error {
			skillsDir, err := config.ResolveSkillsDir(settings.SkillsDir)
			if err != nil {
				httpCode = http.StatusBadRequest
				return err
			}
			ctx, cancel := context.WithTimeout(c.Request.Context(), gitCommandTimeout)
			defer cancel()

			if !isStandaloneGitRepo(ctx, skillsDir) {
				httpCode = http.StatusBadRequest
				return errors.New("当前 Skills 目录尚未初始化 Git 仓库")
			}
			if settings.SkillsRepoURL != "" {
				if err := ensureOriginRemote(ctx, skillsDir, settings.SkillsRepoURL); err != nil {
					if errors.Is(err, errOriginRemoteMismatch) {
						httpCode = http.StatusConflict
					}
					return err
				}
			}
			if _, err := runGit(ctx, skillsDir, "remote", "get-url", "origin"); err != nil {
				httpCode = http.StatusBadRequest
				return errors.New("未配置 Git 远程仓库地址，请先在设置中填写仓库地址")
			}
			unmerged, err := runGit(ctx, skillsDir, "diff", "--name-only", "--diff-filter=U")
			if err != nil {
				return fmt.Errorf("检查 Git 合并冲突失败: %w", err)
			}
			if strings.TrimSpace(unmerged) != "" {
				httpCode = http.StatusConflict
				return errors.New("仓库仍有未解决的合并冲突，请编辑冲突文件、移除冲突标记并逐个暂存，完成合并提交后再推送")
			}
			// 推送前获取真实远程状态，避免旧引用把远程领先误判为“无需推送”。
			// 在自动暂存、提交之前拒绝远程领先，保留用户的工作区与提交边界。
			if _, err := runGit(ctx, skillsDir, "fetch", "origin"); err != nil {
				return fmt.Errorf("获取远程仓库状态失败: %w", err)
			}
			branch, err := currentGitBranch(ctx, skillsDir)
			if err != nil {
				return err
			}
			remoteRef := "refs/remotes/origin/" + branch
			remoteBranch, err := runGit(ctx, skillsDir, "for-each-ref", "--format=%(refname)", remoteRef)
			if err != nil {
				return fmt.Errorf("检查远程分支失败: %w", err)
			}
			if strings.TrimSpace(remoteBranch) == remoteRef {
				if _, err := runGit(ctx, skillsDir, "rev-parse", "--verify", "HEAD"); err != nil {
					httpCode = http.StatusConflict
					return errors.New("远程分支已有提交，本地尚无提交；请从远程克隆到空目录后导入本地 Skills")
				}
				behind, err := runGit(ctx, skillsDir, "rev-list", "--count", "HEAD.."+remoteRef)
				if err != nil {
					return fmt.Errorf("检查远程领先提交失败: %w", err)
				}
				if strings.TrimSpace(behind) != "0" {
					httpCode = http.StatusConflict
					return errors.New("远程分支有本地未包含的提交，本次未自动提交或推送。若本地无修改且历史未分叉，请先拉取；否则先本地提交，再手动合并远程分支、解决冲突后推送")
				}
			}

			plan, err := prepareExport(ctx, skillsDir)
			if err != nil {
				return err
			}
			if req.ExportRevision == "" || plan.Revision != req.ExportRevision {
				httpCode = http.StatusConflict
				return errors.New("导出文件范围或内容已变化，请刷新预览后推送")
			}
			if err := validateExportHistory(ctx, skillsDir); err != nil {
				return err
			}
			if err := stageExport(ctx, skillsDir, plan); err != nil {
				return err
			}
			currentPlan, err := prepareExport(ctx, skillsDir)
			if err != nil {
				return err
			}
			if currentPlan.Revision != plan.Revision {
				httpCode = http.StatusConflict
				return errors.New("暂存期间导出内容发生变化，请刷新预览后推送")
			}
			hasStaged, err := hasStagedChanges(ctx, skillsDir)
			if err != nil {
				return err
			}
			if hasStaged {
				commitArgs := gitCommitArgsWithFallbackAuthor(ctx, skillsDir, commitMsg)
				if _, err := runGit(ctx, skillsDir, commitArgs...); err != nil {
					return fmt.Errorf("提交变更失败: %w", err)
				}
			}
			if _, err := runGit(ctx, skillsDir, "rev-parse", "--verify", "HEAD"); err != nil {
				httpCode = http.StatusBadRequest
				return errors.New("当前 Skills 目录为空，没有可推送的提交")
			}

			if err := validateExportHistory(ctx, skillsDir); err != nil {
				return err
			}
			shouldPush := true
			if strings.TrimSpace(remoteBranch) == remoteRef {
				ahead, err := runGit(ctx, skillsDir, "rev-list", "--count", remoteRef+"..HEAD")
				if err != nil {
					return fmt.Errorf("检查待推送提交失败: %w", err)
				}
				shouldPush = strings.TrimSpace(ahead) != "0"
			}
			if shouldPush {
				// 固定推送已检查的当前分支，避免 push.default / followTags 扩大导出范围。
				if _, err := runGit(ctx, skillsDir, "push", "--no-follow-tags", "-u", "origin", "HEAD:refs/heads/"+branch); err != nil {
					return fmt.Errorf("推送到远程仓库失败，本地提交已保留；请核对远程状态后重试: %w", err)
				}
			} else {
				message = "已是最新，无需推送"
			}
			status, err = api.inspectStatus(ctx)
			return err
		})
		if err != nil {
			c.JSON(httpCode, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, extensionsGitSyncResponse{Status: status, Message: message})
	}
}

func (api *ExtensionsGitAPI) syncAgentsAfterUpdate() (*skills.SyncReport, []mcpsync.AgentResult) {
	var skillsReport *skills.SyncReport
	if api.skillsAPI != nil {
		if report, err := api.skillsAPI.SyncOnStartup(); err == nil {
			skillsReport = &report
		} else {
			skillsReport = &skills.SyncReport{Errors: []string{err.Error()}}
		}
	}
	var mcpAgents []mcpsync.AgentResult
	if api.mcpAPI != nil {
		if results, err := api.mcpAPI.SyncToAgents(); err == nil {
			mcpAgents = results
		} else {
			mcpAgents = []mcpsync.AgentResult{{Name: "MCP", OK: false, Error: err.Error()}}
		}
	}
	return skillsReport, mcpAgents
}

func (api *ExtensionsGitAPI) inspectStatus(ctx context.Context) (ExtensionsGitStatus, error) {
	settings := api.cfgManager.GetSettings().Extensions
	skillsDir, err := config.ResolveSkillsDir(settings.SkillsDir)
	if err != nil {
		return ExtensionsGitStatus{}, err
	}
	_, lookErr := exec.LookPath("git")
	status := ExtensionsGitStatus{
		GitAvailable: lookErr == nil,
		SkillsDir:    skillsDir,
		RepoURL:      settings.SkillsRepoURL,
	}
	status.Migration, err = extensionfiles.MigrationStatus(skillsDir)
	if err != nil {
		return status, err
	}
	if info, statErr := os.Stat(skillsDir); statErr == nil && info.IsDir() {
		status.DirExists = true
	}
	if !status.GitAvailable || !status.DirExists {
		return status, nil
	}
	if !isStandaloneGitRepo(ctx, skillsDir) {
		return status, nil
	}
	status.IsGitRepo = true
	if plan, err := prepareExport(ctx, skillsDir); err != nil {
		status.ExportError = err.Error()
	} else {
		status.ExportFiles, status.ExportRevision = plan.Files, plan.Revision
	}

	if remoteURL, err := runGit(ctx, skillsDir, "remote", "get-url", "origin"); err == nil {
		status.RemoteURL = strings.TrimSpace(remoteURL)
	}
	if branch, err := currentGitBranch(ctx, skillsDir); err == nil {
		status.Branch = branch
	}
	if _, err := runGit(ctx, skillsDir, "rev-parse", "--verify", "HEAD"); err == nil {
		status.HasCommits = true
	}
	if porcelain, err := runGit(ctx, skillsDir, "status", "--porcelain=v1"); err == nil {
		for _, line := range strings.Split(strings.TrimRight(porcelain, "\r\n"), "\n") {
			trimmed := strings.TrimSpace(line)
			if trimmed != "" {
				status.ChangedFiles = append(status.ChangedFiles, trimmed)
			}
		}
		status.Dirty = len(status.ChangedFiles) > 0
	}
	if status.HasCommits {
		if counts, err := runGit(ctx, skillsDir, "rev-list", "--left-right", "--count", "HEAD...@{upstream}"); err == nil {
			fields := strings.Fields(counts)
			if len(fields) == 2 {
				status.Ahead, _ = strconv.Atoi(fields[0])
				status.Behind, _ = strconv.Atoi(fields[1])
			}
		}
	}
	return status, nil
}

// isStandaloneGitRepo 确认 skillsDir 自身是独立 Git 仓库根目录，避免向上继承到宿主工作区（如 api-proxy 自身）的 .git。
func isStandaloneGitRepo(ctx context.Context, skillsDir string) bool {
	if _, err := os.Stat(filepath.Join(skillsDir, ".git")); err != nil {
		return false
	}
	top, err := runGit(ctx, skillsDir, "rev-parse", "--show-toplevel")
	if err != nil {
		return false
	}
	topClean := strings.TrimSpace(top)
	expectedClean := filepath.Clean(skillsDir)
	if resolved, err := filepath.EvalSymlinks(topClean); err == nil {
		topClean = resolved
	}
	if resolved, err := filepath.EvalSymlinks(expectedClean); err == nil {
		expectedClean = resolved
	}
	return config.SamePath(topClean, expectedClean)
}

func ensureGitignore(skillsDir string) error {
	path := filepath.Join(skillsDir, ".gitignore")
	if _, err := os.Stat(path); err == nil {
		return nil
	} else if !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("检查 .gitignore 失败: %w", err)
	}
	content := ".skill-stage-*\n.skill-old-*\n.skill-removed-*\n*.tmp\n/mcp.json\n/.state/\n/.metadata/\n**/.backups/\n"
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		return fmt.Errorf("创建 .gitignore 失败: %w", err)
	}
	return nil
}

func ensureOriginRemote(ctx context.Context, skillsDir, repoURL string) error {
	repoURL = strings.TrimSpace(repoURL)
	existing, err := runGit(ctx, skillsDir, "remote", "get-url", "origin")
	if err != nil {
		if _, addErr := runGit(ctx, skillsDir, "remote", "add", "origin", "--", repoURL); addErr != nil {
			return fmt.Errorf("添加 origin 远程仓库失败: %w", addErr)
		}
		return nil
	}
	if strings.TrimSpace(existing) != repoURL {
		return errOriginRemoteMismatch
	}
	return nil
}

func hasStagedChanges(ctx context.Context, skillsDir string) (bool, error) {
	cmd := exec.CommandContext(ctx, "git", "-C", skillsDir, "diff", "--cached", "--quiet")
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	err := cmd.Run()
	if err == nil {
		return false, nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
		return true, nil
	}
	return false, fmt.Errorf("检查暂存变更失败: %w", err)
}

func gitCommitArgsWithFallbackAuthor(ctx context.Context, skillsDir, msg string) []string {
	name, _ := runGit(ctx, skillsDir, "config", "user.name")
	email, _ := runGit(ctx, skillsDir, "config", "user.email")
	args := make([]string, 0, 7)
	if strings.TrimSpace(name) == "" {
		args = append(args, "-c", "user.name=api-proxy")
	}
	if strings.TrimSpace(email) == "" {
		args = append(args, "-c", "user.email=api-proxy@local")
	}
	args = append(args, "commit", "-m", msg)
	return args
}

func currentGitBranch(ctx context.Context, skillsDir string) (string, error) {
	branch, err := runGit(ctx, skillsDir, "symbolic-ref", "--short", "HEAD")
	if err == nil && strings.TrimSpace(branch) != "" {
		return strings.TrimSpace(branch), nil
	}
	branch, err = runGit(ctx, skillsDir, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		return "", fmt.Errorf("获取当前分支失败: %w", err)
	}
	return strings.TrimSpace(branch), nil
}

func runGit(ctx context.Context, dir string, args ...string) (string, error) {
	cmdArgs := append([]string{"-C", dir}, args...)
	cmd := exec.CommandContext(ctx, "git", cmdArgs...)
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		errText := strings.TrimSpace(stderr.String())
		if errText == "" {
			errText = strings.TrimSpace(stdout.String())
		}
		if errText == "" {
			errText = err.Error()
		}
		return "", fmt.Errorf("%s: %w", errText, err)
	}
	return stdout.String(), nil
}
