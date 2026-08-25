package middleware

import (
	"bytes"
	"io"
	"net/http"
	"path"
	"time"

	"github.com/gin-gonic/gin"

	"iot-gateway/logger"
)

// 限制日志中记录的 body 大小（防止大文件/大数据撑爆日志）
const maxLogBodySize = 2 * 1024 // 2KB

// 静态文件扩展名集合——这些前端资源请求跳过日志记录，避免刷屏
var staticExtensions = map[string]bool{
	".js": true, ".css": true, ".html": true,
	".svg": true, ".png": true, ".jpg": true, ".jpeg": true, ".gif": true, ".ico": true,
	".json": true,
	".woff": true, ".woff2": true, ".ttf": true, ".eot": true,
}

// isStaticFile 判断请求路径是否指向前端静态资源
func isStaticFile(urlPath string) bool {
	if urlPath == "/" {
		return true
	}
	return staticExtensions[path.Ext(urlPath)]
}

// bodyLogWriter 包装 gin.ResponseWriter，缓存响应体用于日志记录
type bodyLogWriter struct {
	gin.ResponseWriter
	body      *bytes.Buffer
	truncated bool // 标记响应体是否因超出 maxLogBodySize 而被截断
}

func (w *bodyLogWriter) Write(data []byte) (int, error) {
	// 只缓存前 maxLogBodySize 字节用于日志
	if w.body.Len() < maxLogBodySize {
		space := maxLogBodySize - w.body.Len()
		if len(data) > space {
			w.body.Write(data[:space])
			w.truncated = true
		} else {
			w.body.Write(data)
		}
	} else {
		w.truncated = true
	}
	return w.ResponseWriter.Write(data)
}

// WriteString 实现 io.StringWriter 接口，确保 c.JSON / c.String 等走 Write 方法缓存 body
// Gin 的 ResponseWriter 实现了 WriteString，嵌入后如不覆盖会直接透传到底层，绕过上面的 body 缓存
func (w *bodyLogWriter) WriteString(s string) (int, error) {
	return w.Write([]byte(s))
}

// readRequestBody 安全地读取请求 body 并恢复流
// 注意：必须完整读取 body 后恢复，不能截断读取再恢复，
// 否则下游 handler 的 ShouldBindJSON 会拿到不完整的 JSON 导致 unexpected EOF。
// 日志记录时仅取前 maxLogBodySize 字节，防止撑爆日志。
func readRequestBody(c *gin.Context) string {
	if c.Request.Body == nil || c.Request.Body == http.NoBody {
		return ""
	}

	// 完整读取 body，确保下游 handler 能拿到全部数据
	bodyBytes, err := io.ReadAll(c.Request.Body)
	if err != nil {
		return "[read error]"
	}

	// 恢复完整的 body 流，供后续 handler 重新读取
	c.Request.Body = io.NopCloser(bytes.NewBuffer(bodyBytes))

	// 仅截取前 maxLogBodySize 字节用于日志展示，避免大请求体撑爆日志
	if len(bodyBytes) > maxLogBodySize {
		return string(bodyBytes[:maxLogBodySize]) + "...(truncated)"
	}
	return string(bodyBytes)
}

// RequestLogMiddleware 请求日志中间件（记录请求参数与响应体）
func RequestLogMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		// 不记录 OPTIONS 预检请求 和 前端静态资源请求（JS/CSS/图片/字体等）
		if c.Request.Method == "OPTIONS" || isStaticFile(c.Request.URL.Path) {
			c.Next()
			return
		}

		start := time.Now()
		path := c.Request.URL.Path
		method := c.Request.Method
		clientIP := c.ClientIP()

		// 1. 收集请求参数
		queryParams := c.Request.URL.RawQuery

		bodyStr := ""
		// 仅对可能包含 body 的请求类型读取 body
		if method == "POST" || method == "PUT" || method == "PATCH" {
			bodyStr = readRequestBody(c)
		}

		// 2. 包装 ResponseWriter 以截获响应体
		blw := &bodyLogWriter{
			ResponseWriter: c.Writer,
			body:           bytes.NewBufferString(""),
		}
		c.Writer = blw

		// 3. 处理请求
		c.Next()

		// 4. 收集响应信息
		latency := time.Since(start)
		statusCode := blw.Status()
		respBody := blw.body.String()

		// SPA 前端路由（如 /dashboard）返回 HTML 但路径无扩展名，
		// 此时记录整个 HTML 响应体没有意义，跳过它以保持日志整洁
		if respBody != "" && respBody[0] == '<' {
			respBody = ""
		}

		// 响应体超出 maxLogBodySize 被截断时，追加截断标记，避免看起来像日志没记全
		if blw.truncated {
			respBody += "...(resp truncated)"
		}

		// 5. 构造日志消息
		// 格式: METHOD /path [status] latency clientIP | query:?a=1 | body:{"k":"v"} | resp:{"code":"0"}
		logMsg := "%s %s %d %v %s"
		logArgs := []interface{}{method, path, statusCode, latency, clientIP}

		if queryParams != "" {
			logMsg += " | query:%s"
			logArgs = append(logArgs, queryParams)
		}
		if bodyStr != "" {
			logMsg += " | body:%s"
			logArgs = append(logArgs, bodyStr)
		}
		if respBody != "" {
			logMsg += " | resp:%s"
			logArgs = append(logArgs, respBody)
		}

		// 按状态码分级记录日志
		if statusCode >= 500 {
			logger.Error(logMsg, logArgs...)
		} else if statusCode >= 400 {
			logger.Warn(logMsg, logArgs...)
		} else {
			logger.Info(logMsg, logArgs...)
		}
	}
}
