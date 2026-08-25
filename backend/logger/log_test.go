package logger

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestSizeRotation 验证单文件超过 maxSizeMB 后滚动为当日编号文件。
func TestSizeRotation(t *testing.T) {
	dir := t.TempDir()
	l := &Logger{outputDir: dir, maxSizeMB: 1, stop: make(chan struct{})}
	l.mu.Lock()
	l.openFileLocked(time.Now())
	l.mu.Unlock()

	// write 会镜像到 os.Stdout，测试期间重定向到 NUL 避免刷屏
	oldStdout := os.Stdout
	nullFile, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		t.Fatalf("open null: %v", err)
	}
	os.Stdout = nullFile
	defer func() {
		os.Stdout = oldStdout
		nullFile.Close()
	}()

	// 每次写入 1MB，共 4 次：首文件写满后，后 3 次各触发一次滚动
	chunk := make([]byte, 1024*1024)
	for i := 0; i < 4; i++ {
		l.write(chunk) // write 内部自行加锁
	}
	// 关闭句柄，避免 Windows 下 t.TempDir 清理失败
	l.mu.Lock()
	l.closeFileLocked()
	l.mu.Unlock()

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read dir: %v", err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	// 期望 app-日期.log + app-日期-1.log + app-日期-2.log + app-日期-3.log
	dateKey := time.Now().Format(logDateKey)
	want := []string{
		"app-" + dateKey + "-1.log",
		"app-" + dateKey + "-2.log",
		"app-" + dateKey + "-3.log",
		"app-" + dateKey + ".log",
	}
	if len(names) != len(want) {
		t.Fatalf("got %d files %v, want %d", len(names), names, len(want))
	}
	for i, w := range want {
		if names[i] != w {
			t.Fatalf("file #%d = %s, want %s", i, names[i], w)
		}
	}
}

// TestCleanupExpired 验证超过 maxDays 天的旧日志文件被删除。
func TestCleanupExpired(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{
		"app-20200101.log",    // 过期
		"app-20200101-3.log",  // 过期（编号文件）
		"app-20260823.log",    // 恰好等于 cutoff，应删除
		"app-20260824.log",    // 保留
		"not-a-log.txt",       // 非匹配文件不受影响
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}

	l := &Logger{outputDir: dir, maxDays: 2}
	// maxDays=2，now=20260825 → cutoff=20260823，删除日期 <= 20260823 的文件
	l.cleanupExpired(time.Date(2026, 8, 25, 10, 0, 0, 0, time.Local))

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read dir: %v", err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	want := []string{"app-20260824.log", "not-a-log.txt"}
	if len(names) != len(want) {
		t.Fatalf("got files %v, want %v", names, want)
	}
	for i, w := range want {
		if names[i] != w {
			t.Fatalf("file #%d = %s, want %s", i, names[i], w)
		}
	}
}
