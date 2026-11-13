package system

import (
	"embed"
	"io/fs"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

// ServeFrontend 提供前端静态文件服务
func ServeFrontend(r *gin.Engine, frontendFS embed.FS) {
	// 从嵌入的文件系统中提取 frontend/dist 子目录
	distFS, err := fs.Sub(frontendFS, "frontend/dist")
	if err != nil {
		// 如果提取失败，返回错误页面
		r.GET("/", func(c *gin.Context) {
			c.Data(503, "text/html; charset=utf-8", []byte(getErrorPage()))
		})
		return
	}

	// index.html 和 SPA 路由必须每次重新校验，避免升级 exe 后浏览器继续使用旧前端。
	// 带内容哈希的 assets 文件可长期缓存。
	r.Use(func(c *gin.Context) {
		if strings.HasPrefix(c.Request.URL.Path, "/assets/") {
			c.Header("Cache-Control", "public, max-age=31536000, immutable")
		}
		c.Next()
	})

	// 使用 Gin 的静态文件服务 - /assets 路由
	r.StaticFS("/assets", http.FS(distFS))

	// 根路径返回 index.html
	r.GET("/", func(c *gin.Context) {
		setFrontendEntryNoCache(c)
		indexContent, err := fs.ReadFile(distFS, "index.html")
		if err != nil {
			c.Data(503, "text/html; charset=utf-8", []byte(getErrorPage()))
			return
		}
		c.Data(200, "text/html; charset=utf-8", indexContent)
	})

	// NoRoute 处理器 - 智能SPA支持
	r.NoRoute(func(c *gin.Context) {
		path := c.Request.URL.Path

		// API 路由优先处理 - 返回 JSON 格式的 404
		if isAPIPath(path) {
			c.JSON(http.StatusNotFound, gin.H{
				"error":   "API endpoint not found",
				"path":    path,
				"message": "请求的API端点不存在",
			})
			return
		}

		// 去掉开头的 /
		if len(path) > 0 && path[0] == '/' {
			path = path[1:]
		}

		// 尝试从嵌入的文件系统读取文件
		fileContent, err := fs.ReadFile(distFS, path)
		if err == nil {
			// 文件存在，根据扩展名设置正确的 Content-Type
			if strings.HasSuffix(strings.ToLower(path), ".html") {
				setFrontendEntryNoCache(c)
			}
			contentType := getContentType(path)
			c.Data(200, contentType, fileContent)
			return
		}

		// 文件不存在，返回 index.html (SPA 路由支持)
		setFrontendEntryNoCache(c)
		indexContent, err := fs.ReadFile(distFS, "index.html")
		if err != nil {
			c.Data(503, "text/html; charset=utf-8", []byte(getErrorPage()))
			return
		}
		c.Data(200, "text/html; charset=utf-8", indexContent)
	})
}

func setFrontendEntryNoCache(c *gin.Context) {
	c.Header("Cache-Control", "no-cache, no-store, must-revalidate")
	c.Header("Pragma", "no-cache")
	c.Header("Expires", "0")
}

// isAPIPath 检查路径是否为 API 端点
func isAPIPath(path string) bool {
	// API 路由前缀列表
	apiPrefixes := []string{
		"/v1/",    // 代理 API 端点
		"/api/",   // Web 管理界面 API
		"/admin/", // 管理端点
	}

	for _, prefix := range apiPrefixes {
		if strings.HasPrefix(path, prefix) {
			return true
		}
	}

	return false
}

// getContentType 根据文件扩展名返回 Content-Type
func getContentType(path string) string {
	if len(path) == 0 {
		return "text/html; charset=utf-8"
	}

	// 从路径末尾查找扩展名
	ext := ""
	for i := len(path) - 1; i >= 0 && path[i] != '/'; i-- {
		if path[i] == '.' {
			ext = path[i:]
			break
		}
	}

	switch ext {
	case ".html":
		return "text/html; charset=utf-8"
	case ".css":
		return "text/css; charset=utf-8"
	case ".js":
		return "application/javascript; charset=utf-8"
	case ".json":
		return "application/json; charset=utf-8"
	case ".png":
		return "image/png"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".gif":
		return "image/gif"
	case ".svg":
		return "image/svg+xml"
	case ".ico":
		return "image/x-icon"
	case ".woff":
		return "font/woff"
	case ".woff2":
		return "font/woff2"
	case ".ttf":
		return "font/ttf"
	case ".eot":
		return "application/vnd.ms-fontobject"
	default:
		return "application/octet-stream"
	}
}

// getErrorPage 获取错误页面
func getErrorPage() string {
	return `<!DOCTYPE html>
<html>
<head>
  <title>API Proxy - 配置错误</title>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <style>
    body { font-family: system-ui; padding: 40px; background: #f5f5f5; }
    .error { max-width: 600px; margin: 0 auto; background: white; padding: 40px; border-radius: 8px; }
    h1 { color: #dc3545; }
    code { background: #f8f9fa; padding: 2px 6px; border-radius: 3px; }
    pre { background: #f8f9fa; padding: 16px; border-radius: 4px; overflow-x: auto; }
  </style>
</head>
<body>
  <div class="error">
    <h1>前端资源未找到</h1>
    <p>无法找到前端构建文件。请执行以下步骤之一：</p>
    <h3>方案1: 重新构建(推荐)</h3>
    <pre>./build.sh</pre>
    <h3>方案2: 禁用Web界面</h3>
    <p>在 <code>.env</code> 文件中设置: <code>ENABLE_WEB_UI=false</code></p>
    <p>然后只使用API端点: <code>/v1/messages</code></p>
  </div>
</body>
</html>`
}
