package admin

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/insistanan/api-proxy/internal/clients"
	"github.com/insistanan/api-proxy/internal/infra/config"
	"github.com/stretchr/testify/require"
)

func setupExtensionsAdminTest(t *testing.T) (
	root string,
	homeDir string,
	cfgManager *config.ConfigManager,
	skillsAPI *clients.SkillsAPI,
	mcpAPI *clients.MCPAPI,
	gitAPI *ExtensionsGitAPI,
	router *gin.Engine,
) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	root = t.TempDir()
	homeDir = filepath.Join(root, "home")
	require.NoError(t, os.MkdirAll(homeDir, 0700))

	t.Setenv("HOME", homeDir)
	t.Setenv("USERPROFILE", homeDir)
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(homeDir, ".claude"))
	t.Setenv("CODEX_HOME", filepath.Join(homeDir, ".codex"))
	t.Setenv("PI_CODING_AGENT_DIR", filepath.Join(homeDir, ".pi", "agent"))
	t.Setenv("OPENCODE_CONFIG", "")
	t.Setenv("OPENCODE_CONFIG_DIR", filepath.Join(homeDir, ".config", "opencode"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(homeDir, ".config"))
	t.Setenv("DSH_HOME", filepath.Join(homeDir, ".dsh"))

	cfgPath := filepath.Join(root, ".config", "config.json")
	var err error
	cfgManager, err = config.NewConfigManager(cfgPath)
	require.NoError(t, err)
	t.Cleanup(func() { _ = cfgManager.Close() })

	initialSkillsDir := filepath.Join(root, "skills-a")
	require.NoError(t, os.MkdirAll(initialSkillsDir, 0700))
	settings := cfgManager.GetSettings()
	settings.Extensions = config.ExtensionsSettings{SkillsDir: initialSkillsDir}
	require.NoError(t, cfgManager.UpdateSettings(settings))

	skillsDirFn := func() (string, error) {
		return config.ResolveSkillsDir(cfgManager.GetSettings().Extensions.SkillsDir)
	}
	skillsAPI = clients.NewSkillsAPI(skillsDirFn)
	mcpAPI = clients.NewDynamicMCPAPI(func() (string, error) {
		dir, resolveErr := skillsDirFn()
		if resolveErr != nil {
			return "", resolveErr
		}
		return filepath.Join(dir, "mcp.json"), nil
	})
	gitAPI = NewExtensionsGitAPI(cfgManager, skillsAPI, mcpAPI)

	router = gin.New()
	router.PUT("/settings", UpdateSettings(cfgManager, skillsAPI, mcpAPI))
	router.GET("/extensions/git/status", gitAPI.Status())
	router.POST("/extensions/git/init", gitAPI.Init())
	router.POST("/extensions/git/clone", gitAPI.Clone())
	router.POST("/extensions/git/pull", gitAPI.Pull())
	router.POST("/extensions/git/push", gitAPI.Push())
	return root, homeDir, cfgManager, skillsAPI, mcpAPI, gitAPI, router
}

func doJSONRequest(t *testing.T, router *gin.Engine, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var buf bytes.Buffer
	if body != nil {
		require.NoError(t, json.NewEncoder(&buf).Encode(body))
	}
	req := httptest.NewRequest(method, path, &buf)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	return rec
}

func fileGitURL(dir string) string {
	slashed := filepath.ToSlash(dir)
	if strings.HasPrefix(slashed, "/") {
		return "file://" + slashed
	}
	return "file:///" + slashed
}

func TestUpdateSettingsSkillsDirValidation(t *testing.T) {
	root, _, cfgManager, _, _, _, router := setupExtensionsAdminTest(t)
	t.Chdir(root)
	oldDir := cfgManager.GetSettings().Extensions.SkillsDir

	for _, skillsDir := range []string{"skills", "relative/skills", "../skills", "~", "~/skills", `~\skills`} {
		t.Run(skillsDir, func(t *testing.T) {
			rec := doJSONRequest(t, router, http.MethodPut, "/settings", map[string]any{
				"extensions": map[string]string{"skillsDir": skillsDir},
			})
			require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
			require.JSONEq(t, `{"error":"Skills 目录必须填写绝对路径；留空使用工作区下的 skills/"}`, rec.Body.String())
			require.Equal(t, oldDir, cfgManager.GetSettings().Extensions.SkillsDir)
		})
	}

	rec := doJSONRequest(t, router, http.MethodPut, "/settings", map[string]any{
		"extensions": map[string]string{"skillsDir": ""},
	})
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Empty(t, cfgManager.GetSettings().Extensions.SkillsDir)
	resolved, err := config.ResolveSkillsDir(cfgManager.GetSettings().Extensions.SkillsDir)
	require.NoError(t, err)
	require.Equal(t, filepath.Join(root, "skills"), resolved)
}

func TestUpdateSettingsMigratesSkillsDirAndRejectsNonEmptyTarget(t *testing.T) {
	root, _, cfgManager, _, _, _, router := setupExtensionsAdminTest(t)

	oldDir := cfgManager.GetSettings().Extensions.SkillsDir
	skillDir := filepath.Join(oldDir, "hello-skill")
	require.NoError(t, os.MkdirAll(skillDir, 0700))
	require.NoError(t, os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("---\nname: hello-skill\ndescription: demo\n---\n\nhi\n"), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(oldDir, "mcp.json"), []byte(`{"version":1,"servers":[]}`), 0600))
	require.NoError(t, os.MkdirAll(filepath.Join(oldDir, ".git"), 0700))
	require.NoError(t, os.WriteFile(filepath.Join(oldDir, ".git", "HEAD"), []byte("ref: refs/heads/main\n"), 0600))

	// 目标非空时拒绝并保留原目录
	nonEmptyTarget := filepath.Join(root, "occupied")
	require.NoError(t, os.MkdirAll(nonEmptyTarget, 0700))
	require.NoError(t, os.WriteFile(filepath.Join(nonEmptyTarget, "keep.txt"), []byte("keep"), 0600))
	rec := doJSONRequest(t, router, http.MethodPut, "/settings", map[string]any{
		"extensions": map[string]string{"skillsDir": nonEmptyTarget, "skillsRepoUrl": ""},
	})
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Equal(t, oldDir, cfgManager.GetSettings().Extensions.SkillsDir)
	_, err := os.Stat(filepath.Join(oldDir, "mcp.json"))
	require.NoError(t, err)

	// 迁移到新路径，连同 .git 与 mcp.json 一并迁走
	newDir := filepath.Join(root, "skills-b")
	rec = doJSONRequest(t, router, http.MethodPut, "/settings", map[string]any{
		"extensions": map[string]string{"skillsDir": newDir, "skillsRepoUrl": ""},
	})
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Equal(t, newDir, cfgManager.GetSettings().Extensions.SkillsDir)

	_, err = os.Stat(oldDir)
	require.True(t, os.IsNotExist(err))
	_, err = os.Stat(filepath.Join(newDir, "hello-skill", "SKILL.md"))
	require.NoError(t, err)
	_, err = os.Stat(filepath.Join(newDir, "mcp.json"))
	require.NoError(t, err)
	_, err = os.Stat(filepath.Join(newDir, ".git", "HEAD"))
	require.NoError(t, err)
}

func TestExtensionsGitLifecycleInitPushClonePull(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git 未安装，跳过 Git 集成测试")
	}

	root, homeDir, cfgManager, _, _, _, router := setupExtensionsAdminTest(t)
	ctx := context.Background()

	// 创建裸远程仓库
	bareRepo := filepath.Join(root, "remote.git")
	_, err := runGit(ctx, root, "init", "--bare", bareRepo)
	require.NoError(t, err)
	repoURL := fileGitURL(bareRepo)

	// 1. 即使在其它 Git 仓库内部，未 init 的 skillsDir 也必须报 IsGitRepo=false
	parentRepo := filepath.Join(root, "parent-repo")
	childSkills := filepath.Join(parentRepo, "skills")
	require.NoError(t, os.MkdirAll(childSkills, 0700))
	_, err = runGit(ctx, parentRepo, "init")
	require.NoError(t, err)
	rec := doJSONRequest(t, router, http.MethodPut, "/settings", map[string]any{
		"extensions": map[string]string{"skillsDir": childSkills, "skillsRepoUrl": repoURL},
	})
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	rec = doJSONRequest(t, router, http.MethodGet, "/extensions/git/status", nil)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var status ExtensionsGitStatus
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &status))
	require.True(t, status.DirExists)
	require.False(t, status.IsGitRepo, "不得把宿主父仓库识别为 Skills 独立仓库")

	// 2. Init + 写入 Skill/Manifest/MCP 后 Push
	rec = doJSONRequest(t, router, http.MethodPost, "/extensions/git/init", map[string]string{"repoUrl": repoURL})
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	require.NoError(t, os.MkdirAll(filepath.Join(childSkills, "demo-skill"), 0700))
	require.NoError(t, os.WriteFile(filepath.Join(childSkills, "demo-skill", "SKILL.md"), []byte("---\nname: demo-skill\ndescription: v1\n---\n\nv1\n"), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(childSkills, "skills-manifest.json"), []byte("{\n  \"version\": 1,\n  \"skills\": {\n    \"demo-skill\": [\"claude-code\"]\n  }\n}\n"), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(childSkills, "mcp.json"), []byte(`{"version":1,"servers":[{"name":"demo-mcp","transport":"stdio","command":"npx","args":["-y","demo"],"agents":["claude-code"]}]}`), 0600))

	rec = doJSONRequest(t, router, http.MethodPost, "/extensions/git/push", map[string]string{"message": "init skills"})
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	// 3. 切换到空目录并 Clone，验证自动同步到 ~/.claude
	cloneTarget := filepath.Join(root, "cloned-skills")
	settings := cfgManager.GetSettings()
	settings.Extensions = config.ExtensionsSettings{SkillsDir: cloneTarget, SkillsRepoURL: repoURL}
	require.NoError(t, cfgManager.UpdateSettings(settings))

	rec = doJSONRequest(t, router, http.MethodPost, "/extensions/git/clone", map[string]string{"repoUrl": repoURL})
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	var cloneResp extensionsGitSyncResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &cloneResp))
	require.True(t, cloneResp.Status.IsGitRepo)
	require.NotNil(t, cloneResp.SkillsReport)
	require.Equal(t, 1, cloneResp.SkillsReport.Synced)

	claudeSkill, err := os.ReadFile(filepath.Join(homeDir, ".claude", "skills", "demo-skill", "SKILL.md"))
	require.NoError(t, err)
	require.Contains(t, string(claudeSkill), "v1")
	claudeMCP, err := os.ReadFile(filepath.Join(homeDir, ".claude", ".claude.json"))
	require.NoError(t, err)
	require.Contains(t, string(claudeMCP), "demo-mcp")

	// 4. 原目录更新后再 Push，cloned-skills 执行 Pull 后验证同步 v2；若存在脏工作区则拒绝拉取
	require.NoError(t, os.WriteFile(filepath.Join(childSkills, "demo-skill", "SKILL.md"), []byte("---\nname: demo-skill\ndescription: v2\n---\n\nv2\n"), 0600))
	_, err = runGit(ctx, childSkills, "add", "-A")
	require.NoError(t, err)
	commitArgs := gitCommitArgsWithFallbackAuthor(ctx, childSkills, "update v2")
	_, err = runGit(ctx, childSkills, commitArgs...)
	require.NoError(t, err)
	_, err = runGit(ctx, childSkills, "push")
	require.NoError(t, err)

	dirtyFile := filepath.Join(cloneTarget, "dirty.txt")
	require.NoError(t, os.WriteFile(dirtyFile, []byte("uncommitted"), 0600))
	rec = doJSONRequest(t, router, http.MethodPost, "/extensions/git/pull", nil)
	require.Equal(t, http.StatusConflict, rec.Code, rec.Body.String())
	require.NoError(t, os.Remove(dirtyFile))

	rec = doJSONRequest(t, router, http.MethodPost, "/extensions/git/pull", nil)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	claudeSkillV2, err := os.ReadFile(filepath.Join(homeDir, ".claude", "skills", "demo-skill", "SKILL.md"))
	require.NoError(t, err)
	require.Contains(t, string(claudeSkillV2), "v2")
}
