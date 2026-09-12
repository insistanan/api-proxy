package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
)

// CLI 工具版本检测：为客户端配置页（GPT / Claude Code / OpenCode / pi）提供
// "当前版本 + npm 最新版本 + 安装命令"展示数据。
//
// 设计要点（参考 cc-switch 的实现并按本项目情况裁剪）：
//   - 当前版本：exec.LookPath 定位后跑 `{tool} --version`，正则提取版本号；
//     Windows 上 npm 全局安装的 CLI 是 .cmd/.bat 脚本，需经 cmd /c（同 codex_models.go）。
//     未找到命令视为"未安装"，不算错误。
//   - 最新版本：npm registry 轻量 dist-tags 端点，只拿 latest，不加载完整包文档。
//   - 缓存：per-tool 互斥锁 + 结果缓存，TTL 1 小时；锁内探测，同工具并发请求在锁上
//     排队，拿锁后先复查缓存避免重复探测；force=true 绕过 TTL。
//   - 当前版本与最新版本独立探测、独立过期，一端失败不影响另一端的有效缓存。

const (
	// toolVersionCacheTTL 版本检测结果的有效期（含网络探测结果）。
	toolVersionCacheTTL = time.Hour
	// toolVersionProbeTimeout 单次 `--version` 子进程超时。
	toolVersionProbeTimeout = 10 * time.Second
	// toolVersionHTTPTimeout npm registry 请求超时。
	toolVersionHTTPTimeout = 15 * time.Second
)

// toolVersionRe 从 `--version` 输出提取首个 x.y.z 版本号（允许 -beta.1 之类预发布后缀）。
var toolVersionRe = regexp.MustCompile(`\d+\.\d+\.\d+(-[\w.]+)?`)

// toolSpec 受支持工具的静态描述。
type toolSpec struct {
	// npmPackage npm registry 包名，用于最新版本查询。
	npmPackage string
	// displayName 页面展示名。
	displayName string
	// installWindows Windows 安装命令。
	installWindows string
	// installUnix macOS / Linux 安装命令。
	installUnix string
}

// unixNpmInstall 构造 npm 全局安装命令。
func unixNpmInstall(pkg string) string {
	return fmt.Sprintf("npm i -g %s@latest", pkg)
}

// unixScriptInstall 用 curl 下载官方安装脚本到临时文件后交给 bash 执行。
// 不用 `curl ... | bash` 管道形式：默认 shell 可能是 dash/ash，无法假设支持
// `set -o pipefail`，curl 失败无法稳定传导为整条命令失败（参考 cc-switch 的做法）。
func unixScriptInstall(url string) string {
	return fmt.Sprintf(
		"bash -c 'tmp=$(mktemp) && curl -fsSL %s -o $tmp && bash $tmp; status=$?; rm -f $tmp; exit $status'",
		url,
	)
}

var toolSpecs = map[string]toolSpec{
	"claude": {
		npmPackage:     "@anthropic-ai/claude-code",
		displayName:    "Claude Code",
		installWindows: unixNpmInstall("@anthropic-ai/claude-code"),
		installUnix:    unixScriptInstall("https://claude.ai/install.sh"),
	},
	"codex": {
		npmPackage:     "@openai/codex",
		displayName:    "Codex",
		installWindows: unixNpmInstall("@openai/codex"),
		installUnix:    unixNpmInstall("@openai/codex"),
	},
	"opencode": {
		npmPackage:     "opencode-ai",
		displayName:    "OpenCode",
		installWindows: unixNpmInstall("opencode-ai"),
		installUnix:    unixScriptInstall("https://opencode.ai/install"),
	},
	"pi": {
		npmPackage:     "@earendil-works/pi-coding-agent",
		displayName:    "Pi",
		installWindows: unixNpmInstall("@earendil-works/pi-coding-agent"),
		installUnix:    unixNpmInstall("@earendil-works/pi-coding-agent"),
	},
}

// toolVersionResult 一次检测的完整结果（也是缓存值与 API 响应体）。
type toolVersionResult struct {
	Tool         string `json:"tool"`
	DisplayName  string `json:"displayName"`
	Current      string `json:"current"`      // 当前版本，空 = 未安装
	CurrentError string `json:"currentError"` // 当前版本探测失败原因（未安装不算）
	Latest       string `json:"latest"`       // npm latest 版本，空 = 未知
	LatestError  string `json:"latestError"`  // 最新版本查询失败原因
	InstallCmd   string `json:"installCmd"`   // 当前平台的安装命令
	// DetectedAt 结果生成时间（Unix 毫秒），前端可展示"检测于 xx:xx"。
	DetectedAt int64 `json:"detectedAt"`
}

// toolVersionState 单个工具的锁与缓存。
// current / latest 分别是"以 current 探测为准"和"以 latest 探测为准"的完整快照；
// 锁同时承担合并并发请求的职责——探测在锁内执行，排队者拿锁后复查缓存即可
// 拿到结果。两端探测独立过期，避免一端失败作废另一端的有效结果。
type toolVersionState struct {
	mu        sync.Mutex
	current   toolVersionResult
	latest    toolVersionResult
	currentAt time.Time
	latestAt  time.Time
	hasCur    bool
	hasLat    bool
}

var (
	toolVersionStatesMu sync.Mutex
	toolVersionStates   = map[string]*toolVersionState{}
	// toolVersionHTTPGet 包级变量便于测试注入。
	toolVersionHTTPGet = httpGetDefault
	// toolVersionProbeLocal 包级变量便于测试注入。
	toolVersionProbeLocal = probeLocalToolVersion
)

func getToolVersionState(tool string) *toolVersionState {
	toolVersionStatesMu.Lock()
	defer toolVersionStatesMu.Unlock()
	if state, ok := toolVersionStates[tool]; ok {
		return state
	}
	state := &toolVersionState{}
	toolVersionStates[tool] = state
	return state
}

func httpGetDefault(ctx context.Context, url string) ([]byte, int, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, 0, err
	}
	request.Header.Set("User-Agent", "api-proxy-tool-version")
	request.Header.Set("Accept", "application/vnd.npm.install-v1+json")
	client := &http.Client{Timeout: toolVersionHTTPTimeout}
	response, err := client.Do(request)
	if err != nil {
		return nil, 0, err
	}
	defer response.Body.Close()
	// 1 MiB 读取上限，dist-tags 响应仅几 KB。
	body, err := readLimitedBody(response.Body, 1<<20)
	if err != nil {
		return nil, response.StatusCode, err
	}
	return body, response.StatusCode, nil
}

// GetToolVersion 返回指定 CLI 工具的当前版本、最新版本与安装命令。
// query force=true 时绕过 TTL 强制重新探测。
func GetToolVersion() gin.HandlerFunc {
	return func(c *gin.Context) {
		tool := c.Param("tool")
		spec, ok := toolSpecs[tool]
		if !ok {
			c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("不支持的工具: %s", tool)})
			return
		}
		force := c.Query("force") == "true"
		result := getToolVersionResult(c.Request.Context(), tool, spec, force)
		c.JSON(http.StatusOK, result)
	}
}

// getToolVersionResult 检测入口：持锁查缓存，过期（或 force）则探测后更新。
// 探测使用脱离请求生命周期的独立 context：结果会写入全局缓存，若绑定到某个
// 请求的 context，该请求被取消（如用户关闭页面）会让注定失败的探测结果
// 污染缓存一小时。各探测函数自行施加超时。
func getToolVersionResult(_ context.Context, tool string, spec toolSpec, force bool) toolVersionResult {
	state := getToolVersionState(tool)
	state.mu.Lock()
	defer state.mu.Unlock()

	now := time.Now()
	curValid := !force && state.hasCur && now.Sub(state.currentAt) < toolVersionCacheTTL
	latValid := !force && state.hasLat && now.Sub(state.latestAt) < toolVersionCacheTTL
	if curValid && latValid {
		return state.current
	}

	// 从仍有效的一端起步，保留其探测结果，只刷新失效端。
	// 单端有效时另一端必然在此刷新，写回后该快照即包含两端的最新结果。
	result := state.current
	if !curValid && latValid {
		result = state.latest
	}
	result.Tool = tool
	result.DisplayName = spec.displayName
	result.InstallCmd = installCommandFor(spec)

	if !curValid {
		result.Current, result.CurrentError = toolVersionProbeLocal(context.Background(), tool)
		state.currentAt = time.Now()
		state.hasCur = true
	}
	if !latValid {
		result.Latest, result.LatestError = fetchNpmLatestVersion(context.Background(), spec.npmPackage)
		state.latestAt = time.Now()
		state.hasLat = true
	}
	result.DetectedAt = time.Now().UnixMilli()
	state.current = result
	state.latest = result
	return result
}

// probeLocalToolVersion 运行 `{tool} --version` 并提取版本号。
// 返回 ("", "") 表示未安装。
// 包级变量 toolVersionProbeLocal 指向本函数，测试中可替换。
func probeLocalToolVersion(ctx context.Context, tool string) (string, string) {
	ctx, cancel := context.WithTimeout(ctx, toolVersionProbeTimeout)
	defer cancel()

	executable, err := exec.LookPath(tool)
	if err != nil {
		return "", "" // 未安装，不算错误
	}
	args := []string{"--version"}
	var cmd *exec.Cmd
	if ext := strings.ToLower(filepath.Ext(executable)); ext == ".cmd" || ext == ".bat" {
		// Windows 上 npm 全局安装的 CLI 是 .cmd/.bat 脚本，CreateProcess 无法直接
		// 执行，需经 cmd /c（同 codex_models.go 的做法）。
		cmd = exec.CommandContext(ctx, "cmd", append([]string{"/c", executable}, args...)...)
	} else {
		cmd = exec.CommandContext(ctx, executable, args...)
	}
	output, err := cmd.Output()
	if err != nil {
		return "", fmt.Sprintf("执行 %s --version 失败: %v", tool, err)
	}
	version := toolVersionRe.FindString(string(output))
	if version == "" {
		return "", fmt.Sprintf("无法从 %s --version 输出中解析版本号", tool)
	}
	return version, ""
}

// fetchNpmLatestVersion 查询 npm registry dist-tags 端点的 latest。
// `/-/package/{name}/dist-tags` 端点要求包名是单一路径段：scoped 包的 `/`
// 必须转义为 %2F（npm CLI 同样用 encodeURIComponent 编码；字面量斜杠会被
// registry 解析为多余路径段而 404）。url.PathEscape 恰好只转义 `/`，
// `@` 保持原样，registry 两种形式都接受。
func fetchNpmLatestVersion(ctx context.Context, pkg string) (string, string) {
	url := fmt.Sprintf("https://registry.npmjs.org/-/package/%s/dist-tags", url.PathEscape(pkg))
	body, status, err := toolVersionHTTPGet(ctx, url)
	if err != nil {
		return "", fmt.Sprintf("查询 npm registry 失败: %v", err)
	}
	if status < 200 || status >= 300 {
		return "", fmt.Sprintf("查询 npm registry 失败: HTTP %d", status)
	}
	var distTags map[string]string
	if err := json.Unmarshal(body, &distTags); err != nil {
		return "", fmt.Sprintf("解析 npm dist-tags 响应失败: %v", err)
	}
	latest, ok := distTags["latest"]
	if !ok || strings.TrimSpace(latest) == "" {
		return "", "npm dist-tags 中缺少 latest"
	}
	return latest, ""
}

// installCommandFor 返回当前平台的安装命令。
func installCommandFor(spec toolSpec) string {
	if runtime.GOOS == "windows" {
		return spec.installWindows
	}
	return spec.installUnix
}
