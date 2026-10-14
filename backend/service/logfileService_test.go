// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package service

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// newLogDirSvc 构造指向临时目录的 LogFileService
func newLogDirSvc(t *testing.T) *LogFileService {
	t.Helper()
	return &LogFileService{logDir: t.TempDir()}
}

func writeLogFile(t *testing.T, svc *LogFileService, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(svc.logDir, name), []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}

func TestLogFileService_ListFiles(t *testing.T) {
	svc := newLogDirSvc(t)

	// 显式设置修改时间，保证排序可判定
	writeLogFile(t, svc, "app-20260821.log", "new\n")
	writeLogFile(t, svc, "app-20260820.log", "old\n")
	writeLogFile(t, svc, "readme.txt", "not a log\n") // 应被过滤
	now := time.Now()
	_ = os.Chtimes(filepath.Join(svc.logDir, "app-20260821.log"), now, now)
	_ = os.Chtimes(filepath.Join(svc.logDir, "app-20260820.log"), now.Add(-time.Hour), now.Add(-time.Hour))

	resp, err := svc.ListFiles(1, 10)
	if err != nil {
		t.Fatalf("ListFiles error: %v", err)
	}
	if resp.Total != 2 {
		t.Fatalf("total = %d, want 2", resp.Total)
	}
	if len(resp.Records) != 2 {
		t.Fatalf("expect 2 .log files, got %d: %+v", len(resp.Records), resp.Records)
	}
	if resp.Records[0].FileName != "app-20260821.log" || resp.Records[1].FileName != "app-20260820.log" {
		t.Fatalf("expect newest first, got [%s, %s]", resp.Records[0].FileName, resp.Records[1].FileName)
	}
	if resp.Records[0].Size != 4 { // "new\n"
		t.Fatalf("size = %d, want 4", resp.Records[0].Size)
	}
}

func TestLogFileService_ListFilesPaging(t *testing.T) {
	svc := newLogDirSvc(t)
	base := time.Now()
	for i := 0; i < 3; i++ {
		name := fmt.Sprintf("app-%03d.log", i)
		writeLogFile(t, svc, name, "x\n")
		_ = os.Chtimes(filepath.Join(svc.logDir, name), base.Add(time.Duration(i)*time.Minute), base.Add(time.Duration(i)*time.Minute))
	}

	// 第 2 页，每页 2 条 → 只剩最旧 1 条
	resp, err := svc.ListFiles(2, 2)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Total != 3 || resp.Page != 2 || resp.Size != 2 {
		t.Fatalf("got page=%d size=%d total=%d, want 2/2/3", resp.Page, resp.Size, resp.Total)
	}
	if len(resp.Records) != 1 {
		t.Fatalf("page2 records = %d, want 1", len(resp.Records))
	}
	if resp.Records[0].FileName != "app-000.log" { // 修改时间最旧
		t.Fatalf("page2 record = %s, want app-000.log", resp.Records[0].FileName)
	}

	// 超出范围的页 → 空 records，total 仍正确
	resp, err = svc.ListFiles(9, 10)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Total != 3 || len(resp.Records) != 0 {
		t.Fatalf("overflow page: total=%d records=%d, want 3/0", resp.Total, len(resp.Records))
	}

	// page/size 缺省 → 默认 1/20，全量返回
	resp, err = svc.ListFiles(0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Page != 1 || resp.Size != 20 || resp.Total != 3 {
		t.Fatalf("defaults: page=%d size=%d total=%d, want 1/20/3", resp.Page, resp.Size, resp.Total)
	}
}

func TestLogFileService_ReadFileTail(t *testing.T) {
	svc := newLogDirSvc(t)
	writeLogFile(t, svc, "app.log", "l1\nl2\nl3\nl4\nl5\n")

	got, err := svc.ReadFile("app.log", 3)
	if err != nil {
		t.Fatalf("ReadFile error: %v", err)
	}
	if got.TotalLines != 5 {
		t.Fatalf("totalLines = %d, want 5", got.TotalLines)
	}
	want := "l3,l4,l5"
	if strings.Join(got.Lines, ",") != want {
		t.Fatalf("lines = %v, want [%s]", got.Lines, want)
	}
}

func TestLogFileService_ReadFileTailDefaultAndCap(t *testing.T) {
	svc := newLogDirSvc(t)
	writeLogFile(t, svc, "app.log", strings.Repeat("x\n", 30))

	// tail=0 → 默认 200，文件仅 30 行应全量返回
	got, err := svc.ReadFile("app.log", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Lines) != 30 {
		t.Fatalf("tail=0 default: got %d lines, want 30", len(got.Lines))
	}

	// tail 超上限 → 钳制
	writeLogFile(t, svc, "big.log", strings.Repeat("y\n", maxTail+100))
	got, err = svc.ReadFile("big.log", 99999)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Lines) != maxTail {
		t.Fatalf("cap: got %d lines, want %d", len(got.Lines), maxTail)
	}
}

func TestLogFileService_ReadFileNoTrailingNewline(t *testing.T) {
	svc := newLogDirSvc(t)
	writeLogFile(t, svc, "app.log", "l1\nl2\nlast-without-nl") // 无结尾换行

	got, err := svc.ReadFile("app.log", 10)
	if err != nil {
		t.Fatal(err)
	}
	if got.TotalLines != 3 {
		t.Fatalf("totalLines = %d, want 3", got.TotalLines)
	}
	if strings.Join(got.Lines, ",") != "l1,l2,last-without-nl" {
		t.Fatalf("lines = %v", got.Lines)
	}
}

func TestLogFileService_ReadFileTraversalRejected(t *testing.T) {
	svc := newLogDirSvc(t)
	for _, name := range []string{
		"../default.yaml",
		"..\\default.yaml",
		"sub/app.log",
		"/etc/passwd",
		"",
	} {
		if _, err := svc.ReadFile(name, 10); err == nil {
			t.Errorf("fileName %q should be rejected", name)
		} else if !strings.Contains(err.Error(), "非法") {
			t.Errorf("fileName %q err = %v, want 非法", name, err)
		}
	}
}

func TestLogFileService_ReadFileNotExists(t *testing.T) {
	svc := newLogDirSvc(t)
	if _, err := svc.ReadFile("nope.log", 10); err == nil {
		t.Fatal("expect error for missing file")
	} else if !strings.Contains(err.Error(), "不存在") {
		t.Fatalf("err = %v, want 不存在", err)
	}
}
