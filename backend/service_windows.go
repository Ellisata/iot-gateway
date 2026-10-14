//go:build windows

// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package main

import (
	"os"
	"path/filepath"

	"golang.org/x/sys/windows/svc"

	"iot-gateway/logger"
)

// 服务名：必须与 install.bat / uninstall.bat 中的 SERVICE_NAME 保持一致
const serviceName = "iot-gateway"

// runAsWindowsService 检测当前是否由服务控制管理器（SCM）启动：
//   - 是：切换工作目录到可执行文件所在目录（sc 服务默认工作目录是 System32，
//     而 default.yaml / data / log 均为相对路径，需落到安装目录才能与安装行为一致），
//     随后以 Windows 服务方式运行并阻塞，返回 true。
//   - 否：返回 false，由 main 走前台模式。
func runAsWindowsService() bool {
	isService, err := svc.IsWindowsService()
	if err != nil || !isService {
		return false
	}
	if exe, err := os.Executable(); err == nil {
		if err := os.Chdir(filepath.Dir(exe)); err != nil {
			logger.Error("chdir to executable directory failed: %v", err)
		}
	}
	if err := svc.Run(serviceName, &gatewayService{}); err != nil {
		logger.Error("windows service run failed: %v", err)
	}
	return true
}

// gatewayService 实现 svc.Handler，将网关生命周期接入 Windows 服务控制管理器。
type gatewayService struct{}

// Execute 由 svc.Run 调用：向 SCM 报告服务状态，响应控制请求并驱动网关启停。
func (g *gatewayService) Execute(args []string, r <-chan svc.ChangeRequest, changes chan<- svc.Status) (bool, uint32) {
	const cmdsAccepted = svc.AcceptStop | svc.AcceptShutdown
	changes <- svc.Status{State: svc.StartPending}

	// 启动网关：stop 关闭时优雅停机；done 收到网关自行退出（致命错误）的结果
	stop := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- runGateway(stop)
	}()

	changes <- svc.Status{State: svc.Running, Accepts: cmdsAccepted}

	var runErr error
loop:
	for {
		select {
		case c := <-r:
			switch c.Cmd {
			case svc.Interrogate:
				changes <- c.CurrentStatus
			case svc.Stop, svc.Shutdown:
				changes <- svc.Status{State: svc.StopPending}
				close(stop)
				runErr = <-done
				break loop
			}
		case runErr = <-done:
			// 网关自行退出（如服务器致命错误），无需再发送停止信号
			break loop
		}
	}

	// 等待 runGateway 返回后再报告 Stopped，保证引擎已停止、数据已落盘
	if runErr != nil {
		logger.Error("gateway exited with error: %v", runErr)
	}
	changes <- svc.Status{State: svc.Stopped}
	if runErr != nil {
		return false, 1
	}
	return false, 0
}
