// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

//go:build windows

package pluginhost

import (
	"fmt"
	"os/exec"
	"sync"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// platformEnvNames Windows 必需的系统变量。
// SystemRoot/windir 用于定位系统 DLL 与 socket 库，缺失会让子进程起不来。
var platformEnvNames = []string{"SystemRoot", "windir", "NUMBER_OF_PROCESSORS"}

// pluginSysProcAttr 隐藏插件进程的控制台窗口。
// 网关在 Windows 上以服务方式运行，弹出控制台窗口既不专业也会干扰运维。
func pluginSysProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{HideWindow: true}
}

var (
	jobOnce   sync.Once
	jobHandle windows.Handle
	jobErr    error
)

// pluginAfterStart 把插件进程放进一个「句柄关闭即连坐终止」的 Job Object。
//
// 这是防孤儿进程的兜底。主要机制是插件读到 stdin EOF 自行退出，但它只对
// **已经跑起来并读取 stdin** 的插件有效：若插件在调用 driversdk.Serve 之前
// 就卡住，它不会注意到父进程已经消失，此时只有内核级的 Job Object 能保证它
// 随网关一起消失 —— 包括网关被 TerminateProcess 强杀的情况。
//
// 句柄由本进程持有且**永不主动关闭**，它的生命周期就等于网关进程的生命周期：
// 网关无论以何种方式终止，内核都会关闭该句柄，并连带终止 Job 内的全部进程。
func pluginAfterStart(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return fmt.Errorf("进程尚未启动")
	}

	h, err := sharedJob()
	if err != nil {
		return err
	}

	ph, err := windows.OpenProcess(
		windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, uint32(cmd.Process.Pid))
	if err != nil {
		return fmt.Errorf("打开进程句柄: %w", err)
	}
	defer windows.CloseHandle(ph) //nolint:errcheck // 关闭失败无补救手段

	if err := windows.AssignProcessToJobObject(h, ph); err != nil {
		return fmt.Errorf("加入 Job Object: %w", err)
	}
	return nil
}

// sharedJob 惰性创建本进程共享的 Job Object。
//
// 幂等且进程内共用：一个 Job 可以容纳全部插件进程，没必要每个进程建一个。
func sharedJob() (windows.Handle, error) {
	jobOnce.Do(func() {
		h, err := windows.CreateJobObject(nil, nil)
		if err != nil {
			jobErr = err
			return
		}

		info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{
			BasicLimitInformation: windows.JOBOBJECT_BASIC_LIMIT_INFORMATION{
				LimitFlags: windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE,
			},
		}
		if _, err := windows.SetInformationJobObject(
			h,
			windows.JobObjectExtendedLimitInformation,
			uintptr(unsafe.Pointer(&info)),
			uint32(unsafe.Sizeof(info)),
		); err != nil {
			_ = windows.CloseHandle(h)
			jobErr = err
			return
		}
		jobHandle = h
	})
	return jobHandle, jobErr
}
