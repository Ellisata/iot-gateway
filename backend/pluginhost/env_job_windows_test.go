// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

//go:build windows

package pluginhost

import (
	"fmt"
	"sync"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// x/sys/windows 没有导出 IsProcessInJob，直接从 kernel32 取。
var (
	kernel32           = windows.NewLazySystemDLL("kernel32.dll")
	procIsProcessInJob = kernel32.NewProc("IsProcessInJob")
)

// isProcessInJob 查询进程是否已属于指定 Job。
func isProcessInJob(process, job windows.Handle) (bool, error) {
	var result int32
	ret, _, callErr := procIsProcessInJob.Call(
		uintptr(process), uintptr(job), uintptr(unsafe.Pointer(&result)))
	if ret == 0 {
		return false, fmt.Errorf("IsProcessInJob: %w", callErr)
	}
	return result != 0, nil
}

// closeSharedJobForTest 关闭共享 Job Object 并重置单例，模拟「网关进程终止时
// 内核关闭了它持有的 Job 句柄」这一效果。仅测试使用。
//
// 副作用：此刻仍挂在旧 Job 上的插件进程会被内核一并终止。测试按顺序执行且
// 各用例都会回收自己的进程，因此不影响其它用例；重置单例则是为了让后续用例
// 能正常创建新的 Job。
func closeSharedJobForTest(t *testing.T) {
	t.Helper()

	h, err := sharedJob()
	if err != nil {
		t.Fatalf("创建 Job Object: %v", err)
	}
	if err := windows.CloseHandle(h); err != nil {
		t.Fatalf("关闭 Job Object 句柄: %v", err)
	}

	jobOnce = sync.Once{}
	jobHandle = 0
	jobErr = nil
}

// waitProcessExit 等待指定 PID 的进程退出；进程已不存在也算退出。
func waitProcessExit(t *testing.T, pid int, timeout time.Duration) bool {
	t.Helper()

	h, err := windows.OpenProcess(windows.SYNCHRONIZE, false, uint32(pid))
	if err != nil {
		return true // 已经查不到这个进程了 = 已退出
	}
	defer windows.CloseHandle(h) //nolint:errcheck

	event, err := windows.WaitForSingleObject(h, uint32(timeout.Milliseconds()))
	if err != nil {
		t.Fatalf("等待进程退出: %v", err)
	}
	return event == windows.WAIT_OBJECT_0
}

// TestJobObjectKillsPluginThatNeverReadsStdin 是本兜底存在的**唯一理由**。
//
// 主机制（插件读到 stdin EOF 自行退出）对这些插件无效：它们卡在
// driversdk.Serve 之前，根本不会去读 stdin，因此父进程消失时毫无察觉。
// 只有内核级的 Job Object 能在网关进程终止时把它们一并带走。
func TestJobObjectKillsPluginThatNeverReadsStdin(t *testing.T) {
	path := installFixture(t)

	opts := fixtureOptions(t, path)
	// sleep 模式的夹具进程启动后直接 select{}，不跑协议栈、不读 stdin。
	opts.Env = append(pluginEnv(), envFixture+"=sleep")
	// 握手超时给得极大：确保进程是「卡在握手上」而不是「握手失败被回收」。
	opts.Timeouts.Handshake = 10 * time.Minute

	p := newPluginProcess(path, opts)
	p.start()
	t.Cleanup(p.stop)

	waitFor(t, 10*time.Second, "夹具进程被拉起", func() bool { return p.currentCmd() != nil })

	cmd := p.currentCmd()
	pid := cmd.Process.Pid

	// 前置条件：进程确实还活着（否则本测试什么也没验证）。
	if waitProcessExit(t, pid, 0) {
		t.Fatal("前置条件不成立：夹具进程应当仍在运行")
	}

	// 断言**生产路径**（spawn）真的把它挂进了 Job：这里刻意不自己调
	// pluginAfterStart —— 那样写的话，即使 spawn 里漏了这一步测试也会通过。
	job, err := sharedJob()
	if err != nil {
		t.Fatalf("获取共享 Job: %v", err)
	}
	ph, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		t.Fatalf("打开进程句柄: %v", err)
	}
	inJob, err := isProcessInJob(ph, job)
	_ = windows.CloseHandle(ph) //nolint:errcheck
	if err != nil {
		t.Fatalf("查询进程是否在 Job 内: %v", err)
	}
	if !inJob {
		t.Fatal("插件进程不在 Job Object 内，兜底机制未生效")
	}

	// 模拟网关进程终止：内核关闭 Job 句柄 → 成员进程连坐终止。
	closeSharedJobForTest(t)

	if !waitProcessExit(t, pid, 10*time.Second) {
		t.Error("关闭 Job 句柄后插件进程仍存活 —— 孤儿防护兜底失效")
	}
}
