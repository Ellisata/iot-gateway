package web

import (
	"embed"
	"io/fs"
	"net/http"
	"path"
	"strings"

	"github.com/gin-gonic/gin"
)

//go:embed dist
//go:embed dist/assets/_plugin*
var distFS embed.FS

var (
	// subFS 剥离 "dist" 前缀后的文件系统
	subFS fs.FS
	// indexHTML 预缓存的 index.html，避免每次请求重新读取
	indexHTML []byte
)

func init() {
	var err error
	subFS, err = fs.Sub(distFS, "dist")
	if err != nil {
		panic("failed to create sub filesystem from embedded dist: " + err.Error())
	}
	indexHTML, err = fs.ReadFile(subFS, "index.html")
	if err != nil {
		panic("failed to read embedded index.html: " + err.Error())
	}
}

// mimeTypes 映射文件扩展名到 MIME 类型（嵌入模式无需依赖外部 mime 包）
var mimeTypes = map[string]string{
	".js":    "application/javascript",
	".css":   "text/css",
	".html":  "text/html; charset=utf-8",
	".svg":   "image/svg+xml",
	".png":   "image/png",
	".jpg":   "image/jpeg",
	".jpeg":  "image/jpeg",
	".gif":   "image/gif",
	".ico":   "image/x-icon",
	".json":  "application/json",
	".woff":  "font/woff",
	".woff2": "font/woff2",
	".ttf":   "font/ttf",
	".eot":   "application/vnd.ms-fontobject",
}

// ServeStatic 返回 Gin 处理器，用于分发嵌入的 Vue SPA。
//
//	prefix 为 URL 前缀（如 "/app"），分发前会自动剥离。
//	处理逻辑（按优先级）：
//	 1. 若请求路径在 embed FS 中存在对应文件（JS/CSS/图片等），直接返回该文件
//	 2. 否则返回 index.html，由前端 Vue Router 处理客户端路由
func ServeStatic(prefix string) gin.HandlerFunc {
	return func(c *gin.Context) {
		// 剥离前缀，在嵌入 FS 中查找
		upath := strings.TrimPrefix(c.Request.URL.Path, prefix)
		upath = strings.TrimPrefix(upath, "/")

		// 1) 空路径 -> index.html
		if upath == "" {
			c.Data(http.StatusOK, "text/html; charset=utf-8", indexHTML)
			return
		}

		// 2) 尝试返回真实文件（assets/* 等）
		data, err := fs.ReadFile(subFS, upath)
		if err == nil {
			ext := path.Ext(upath)
			contentType := mimeTypes[ext]
			if contentType == "" {
				contentType = http.DetectContentType(data)
			}
			c.Data(http.StatusOK, contentType, data)
			return
		}

		// 3) SPA 回退: index.html
		c.Data(http.StatusOK, "text/html; charset=utf-8", indexHTML)
	}
}
