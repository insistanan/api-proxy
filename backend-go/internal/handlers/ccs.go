// Package handlers 提供 HTTP 处理器
package handlers

import (
	"context"
	"log"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/insistanan/api-proxy/internal/config"
)

// pickDialogTimeout 文件选择对话框等待上限。
// 原生对话框会阻塞 PowerShell 进程直到用户关闭，超时后主动回收。
const pickDialogTimeout = 5 * time.Minute

// pickDialogMu 串行化文件选择对话框：同一时间只允许弹一个，
// 避免并发点击叠出多个对话框难以管理。
var pickDialogMu sync.Mutex

// PickCcsPath 弹出原生文件选择对话框，让用户选择 CC Switch 可执行文件。
// 前端 <input type="file"> 出于浏览器安全策略拿不到绝对路径，
// 因此由本机部署的后端经 PowerShell 调用 WinForms 对话框代为选择。
// 仅 Windows 支持；其他平台返回明确错误，走手动输入路径。
func PickCcsPath(cfgManager *config.ConfigManager) gin.HandlerFunc {
	return func(c *gin.Context) {
		if runtime.GOOS != "windows" {
			c.JSON(400, gin.H{"error": "文件选择对话框仅支持 Windows，请在输入框中手动填写路径"})
			return
		}

		pickDialogMu.Lock()
		defer pickDialogMu.Unlock()

		ctx, cancel := context.WithTimeout(c.Request.Context(), pickDialogTimeout)
		defer cancel()

		// -STA：WinForms OpenFileDialog 要求单线程套间
		// -NoProfile：跳过用户 profile 加快启动且避免 profile 脚本干扰输出
		ps := exec.CommandContext(ctx, "powershell", "-NoProfile", "-STA", "-Command",
			`[Console]::OutputEncoding=[System.Text.Encoding]::UTF8;
Add-Type -AssemblyName System.Windows.Forms | Out-Null;
$dlg = New-Object System.Windows.Forms.OpenFileDialog;
$dlg.Title = '选择 CC Switch 可执行文件';
$dlg.Filter = '可执行文件 (*.exe)|*.exe|所有文件 (*.*)|*.*';
if ($dlg.ShowDialog() -eq [System.Windows.Forms.DialogResult]::OK) { Write-Output $dlg.FileName }`)

		out, err := ps.Output()
		if ctx.Err() == context.DeadlineExceeded {
			c.JSON(408, gin.H{"error": "文件选择超时，请重试或手动填写路径"})
			return
		}
		if err != nil {
			log.Printf("[CCS-Pick] 文件选择对话框失败: %v", err)
			c.JSON(500, gin.H{"error": "无法打开文件选择对话框，请手动填写路径"})
			return
		}

		// 用户取消时对话框不输出任何内容（可能带换行）
		path := strings.TrimSpace(string(out))
		if path == "" {
			c.JSON(200, gin.H{"path": ""})
			return
		}
		if _, err := os.Stat(path); err != nil {
			c.JSON(400, gin.H{"error": "所选文件不存在或不可访问"})
			return
		}

		// 顺手保存，省一次手动点保存
		settings := cfgManager.GetSettings()
		settings.Integration.CCSwitchPath = path
		if err := cfgManager.UpdateSettings(settings); err != nil {
			c.JSON(500, gin.H{"error": "保存路径失败: " + err.Error()})
			return
		}
		log.Printf("[CCS-Pick] CC Switch 路径已保存: %s", path)

		c.JSON(200, gin.H{"path": path})
	}
}

// ccsImportRequest 导入请求体。deeplink 由前端按 cc-switch v1 协议生成，
// 后端只校验 scheme 后转交 CC Switch，不感知具体字段。
type ccsImportRequest struct {
	URL string `json:"url" binding:"required"`
}

// ImportToCcs 以 deeplink 为参数拉起 CC Switch。
// CC Switch 的 single-instance 回调会扫描命令行参数中的 ccswitch:// URL，
// 无论协议注册与否、无论是否已在运行，都能触发其导入确认框；
// 这也是便携版（无安装器写协议注册表）唯一可靠的导入通道。
func ImportToCcs(cfgManager *config.ConfigManager) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req ccsImportRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(400, gin.H{"error": "无效的请求参数"})
			return
		}

		req.URL = strings.TrimSpace(req.URL)
		if !strings.HasPrefix(req.URL, "ccswitch://") {
			c.JSON(400, gin.H{"error": "仅接受 ccswitch:// 链接"})
			return
		}

		exePath := strings.TrimSpace(cfgManager.GetSettings().Integration.CCSwitchPath)
		if exePath == "" {
			c.JSON(400, gin.H{"error": "尚未配置 CC Switch 路径，请先在 设置 → 集成 中选择"})
			return
		}
		if _, err := os.Stat(exePath); err != nil {
			c.JSON(400, gin.H{"error": "CC Switch 路径无效或文件已移动，请重新配置"})
			return
		}

		// Start 后不 Wait 会留僵尸进程，交给后台 goroutine 回收；
		// 退出码无需关心——CC Switch 唤起已有实例时会立即退出。
		cmd := exec.Command(exePath, req.URL)
		if err := cmd.Start(); err != nil {
			log.Printf("[CCS-Import] 拉起 CC Switch 失败: %v", err)
			c.JSON(500, gin.H{"error": "拉起 CC Switch 失败: " + err.Error()})
			return
		}
		go func() {
			if err := cmd.Wait(); err != nil {
				log.Printf("[CCS-Import] CC Switch 进程退出: %v", err)
			}
		}()
		log.Printf("[CCS-Import] 已拉起 CC Switch，等待用户在确认框中确认导入")

		c.JSON(200, gin.H{"success": true})
	}
}
