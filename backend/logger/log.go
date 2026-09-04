// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package logger

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"iot-gateway/configFile"
)

const (
	LevelDebug = "DEBUG"
	LevelInfo  = "INFO"
	LevelWarn  = "WARN"
	LevelError = "ERROR"
	LevelFatal = "FATAL"

	// logDateKey 日志文件名中的日期键（app-20060102.log）
	logDateKey = "20060102"
	// maintenanceInterval 日志维护周期：跨天切换检查 + 过期文件清理
	maintenanceInterval = time.Hour
)

// logFilePattern 匹配日志文件命名 app-YYYYMMDD.log / app-YYYYMMDD-N.log
var logFilePattern = regexp.MustCompile(`^app-(\d{8})(?:-\d+)?\.log$`)

var (
	logger     *Logger
	loggerOnce sync.Once
)

// Logger 日志器
type Logger struct {
	mu        sync.RWMutex
	level     string
	outputDir string
	maxSizeMB int // 单文件大小上限（MB），<=0 不按大小滚动
	maxDays   int // 日志保留天数，<=0 不清理

	file     *os.File      // 当前日志文件
	fileSize int64         // 当前文件大小（字节）
	fileDate string        // 当前文件对应日期 "20060102"
	stop     chan struct{} // 通知维护协程退出
	closed   bool          // 是否已调用 Close（防止重复 close(stop)）
}

// InitLogger 显式初始化日志器（推荐在 main 中调用）
func InitLogger(cfg *configFile.Config) *Logger {
	l := &Logger{
		level:     LevelDebug,
		outputDir: "log",
		stop:      make(chan struct{}),
	}
	if cfg != nil {
		l.level = cfg.Log.Level
		if cfg.Log.OutputDir != "" {
			l.outputDir = cfg.Log.OutputDir
		}
		l.maxSizeMB = cfg.Log.MaxSizeMB
		l.maxDays = cfg.Log.MaxDays
	}
	// 创建日志目录
	if err := os.MkdirAll(l.outputDir, 0755); err != nil {
		panic("failed to create log directory: " + err.Error())
	}
	// 打开今日日志文件
	l.mu.Lock()
	l.openFileLocked(time.Now())
	l.mu.Unlock()
	// 后台维护：清理过期日志 + 跨天切换
	go l.maintenanceLoop()
	logger = l
	return logger
}

// openFileLocked 关闭旧文件并按日期打开新的日志文件（追加模式）。
// 调用方须持有 l.mu。
func (l *Logger) openFileLocked(now time.Time) {
	l.closeFileLocked()
	dateKey := now.Format(logDateKey)
	l.fileDate = dateKey
	name := filepath.Join(l.outputDir, fmt.Sprintf("app-%s.log", dateKey))
	f, err := os.OpenFile(name, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[logger] open log file %s failed: %v\n", name, err)
		return
	}
	l.file = f
	if st, err := f.Stat(); err == nil {
		l.fileSize = st.Size()
	} else {
		l.fileSize = 0
	}
}

func (l *Logger) closeFileLocked() {
	if l.file != nil {
		_ = l.file.Close()
		l.file = nil
	}
}

// rotateFileLocked 当前文件达到 maxSizeMB 时，滚动到当日下一个编号文件：
// app-20060102.log → app-20060102-1.log → app-20060102-2.log
// 调用方须持有 l.mu。
func (l *Logger) rotateFileLocked() {
	l.closeFileLocked()
	idx := l.nextFileIndexLocked(l.fileDate)
	name := filepath.Join(l.outputDir, fmt.Sprintf("app-%s-%d.log", l.fileDate, idx))
	f, err := os.OpenFile(name, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[logger] open log file %s failed: %v\n", name, err)
		return
	}
	l.file = f
	l.fileSize = 0
}

// nextFileIndexLocked 扫描当日 app-YYYYMMDD-N.log 编号文件，返回下一个可用编号。
// 调用方须持有 l.mu。
func (l *Logger) nextFileIndexLocked(dateKey string) int {
	maxIdx := 0
	prefix := fmt.Sprintf("app-%s-", dateKey)
	entries, err := os.ReadDir(l.outputDir)
	if err != nil {
		return 1
	}
	for _, e := range entries {
		name := e.Name()
		if !strings.HasPrefix(name, prefix) || !strings.HasSuffix(name, ".log") {
			continue
		}
		n, err := strconv.Atoi(strings.TrimSuffix(strings.TrimPrefix(name, prefix), ".log"))
		if err != nil {
			continue
		}
		if n > maxIdx {
			maxIdx = n
		}
	}
	return maxIdx + 1
}

// maintenanceLoop 周期性执行日志维护（跨天切换、大小校正、过期清理）。
// 启动时先执行一次，随后按 maintenanceInterval 周期运行。
func (l *Logger) maintenanceLoop() {
	l.maintenance()
	ticker := time.NewTicker(maintenanceInterval)
	defer ticker.Stop()
	for {
		select {
		case <-l.stop:
			return
		case <-ticker.C:
			l.maintenance()
		}
	}
}

// maintenance 执行一轮日志维护。
func (l *Logger) maintenance() {
	l.mu.Lock()
	now := time.Now()
	if today := now.Format(logDateKey); today != l.fileDate {
		// 跨天切换日志文件
		l.openFileLocked(now)
	} else if l.file != nil {
		// 校正文件大小（文件可能被外部截断/重建）
		if st, err := l.file.Stat(); err == nil {
			l.fileSize = st.Size()
		}
	}
	l.mu.Unlock()

	if l.maxDays > 0 {
		l.cleanupExpired(now)
	}
}

// cleanupExpired 删除超过 maxDays 天的旧日志文件。
// 按文件名中的日期（app-YYYYMMDD）判定，日期早于或等于 today-maxDays 即删除。
func (l *Logger) cleanupExpired(now time.Time) {
	cutoff := now.AddDate(0, 0, -l.maxDays).Format(logDateKey)
	entries, err := os.ReadDir(l.outputDir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		m := logFilePattern.FindStringSubmatch(e.Name())
		if m == nil || m[1] > cutoff {
			continue
		}
		_ = os.Remove(filepath.Join(l.outputDir, e.Name()))
	}
}

// Close 关闭日志文件并停止后台维护协程。幂等，可安全重复调用。
func Close() {
	if logger == nil {
		return
	}
	l := logger
	l.mu.Lock()
	defer l.mu.Unlock()
	if !l.closed {
		l.closed = true
		close(l.stop)
	}
	l.closeFileLocked()
}

func getLogger() *Logger {
	loggerOnce.Do(func() {
		// 未调用 InitLogger 时的兜底初始化
		cfg, err := configFile.GetConfig()
		if err == nil && cfg != nil {
			InitLogger(cfg)
		} else {
			InitLogger(nil)
		}
	})
	return logger
}

func log(level, format string, v ...interface{}) {
	l := getLogger()
	l.mu.RLock()
	currentLevel := l.level
	l.mu.RUnlock()

	if !shouldLog(currentLevel, level) {
		return
	}

	// 获取调用者信息
	_, file, line, ok := runtime.Caller(2)
	if !ok {
		file = "unknown"
		line = 0
	}
	fileName := filepath.Base(file)

	msg := fmt.Sprintf(format, v...)
	now := time.Now().Format("2006-01-02 15:04:05")
	lineText := fmt.Sprintf("[%s] %s %s:%d %s\n", level, now, fileName, line, msg)
	l.write([]byte(lineText))

	if level == LevelFatal {
		os.Exit(1)
	}
}

// write 写入一条日志：控制台 + 文件，任一失败不影响另一路。
// 文件侧按日期/大小滚动（跨天切换、达到 maxSizeMB 时切换当日下一个文件）。
func (l *Logger) write(p []byte) {
	l.mu.Lock()
	defer l.mu.Unlock()

	// 控制台输出：Windows 服务模式（SCM 启动）无控制台句柄，Write 返回错误但可忽略，
	// 不能因控制台失败而中断文件写入。
	_, _ = os.Stdout.Write(p)

	if l.file == nil {
		return
	}
	now := time.Now()
	// 跨天：切换到新日期文件
	if today := now.Format(logDateKey); today != l.fileDate {
		l.openFileLocked(now)
	}
	// 达到大小上限：滚动到当日下一个编号文件
	if l.maxSizeMB > 0 && l.fileSize+int64(len(p)) > int64(l.maxSizeMB)*1024*1024 {
		l.rotateFileLocked()
	}
	if l.file != nil {
		n, _ := l.file.Write(p)
		l.fileSize += int64(n)
	}
}

func shouldLog(currentLevel, targetLevel string) bool {
	levels := []string{LevelDebug, LevelInfo, LevelWarn, LevelError, LevelFatal}
	currentIdx := indexOf(levels, currentLevel)
	targetIdx := indexOf(levels, targetLevel)
	if currentIdx < 0 {
		currentIdx = 0
	}
	return targetIdx >= currentIdx
}

func indexOf(slice []string, item string) int {
	for i, s := range slice {
		if strings.EqualFold(s, item) {
			return i
		}
	}
	return -1
}

// Debug 调试日志
func Debug(format string, v ...interface{}) {
	log(LevelDebug, format, v...)
}

// Info 信息日志
func Info(format string, v ...interface{}) {
	log(LevelInfo, format, v...)
}

// Warn 警告日志
func Warn(format string, v ...interface{}) {
	log(LevelWarn, format, v...)
}

// Error 错误日志
func Error(format string, v ...interface{}) {
	log(LevelError, format, v...)
}

// Fatal 致命错误日志
func Fatal(format string, v ...interface{}) {
	log(LevelFatal, format, v...)
}
