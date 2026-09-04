// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"iot-gateway/configFile"
	"iot-gateway/i18n"
	"iot-gateway/logger"

	// 推送通道子包空导入：触发各通道在 push.Register 中注册，新增类型在此追加
	_ "iot-gateway/push/influxdb/v3"
	_ "iot-gateway/push/mqtt"
	_ "iot-gateway/push/tdengine/v3"

	"github.com/gin-gonic/gin"
)

func main() {
	// Windows 服务模式：由服务控制管理器（SCM）启动时以服务方式运行并阻塞；
	// 非服务环境（终端/调试）走前台模式。
	if runAsWindowsService() {
		return
	}
	runGatewayForeground()
}

// runGatewayForeground 前台运行模式：监听 Ctrl+C / SIGTERM 触发优雅停机
func runGatewayForeground() {
	stop := make(chan struct{})
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sigCh
		logger.Info("received interrupt signal, shutting down...")
		close(stop)
	}()

	if err := runGateway(stop); err != nil {
		logger.Fatal("failed to start server: %v", err)
	}
}

// runGateway 启动完整网关并阻塞运行：
//   - stop 通道关闭时（服务停止 / Ctrl+C）优雅停机：HTTP Shutdown → 引擎按序停止
//   - 服务器致命错误时返回 error
func runGateway(stop <-chan struct{}) error {
	// 1. 加载配置（注入嵌入的 default.yaml，再按优先级合并外部配置）
	configFile.SetDefaultConfig(defaultYAML)
	cfg := configFile.InitConfig()

	// 2. 初始化日志
	logger.InitLogger(cfg)

	// 3. 初始化国际化
	i18n.InitI18n()

	logger.Info("application starting...")
	if strings.ToUpper(cfg.Log.Level) == "DEBUG" {
		gin.SetMode(gin.DebugMode)
	} else if strings.ToUpper(cfg.Log.Level) == "INFO" {
		gin.SetMode(gin.ReleaseMode)
	}

	// 4. Wire 构建完整依赖图
	deps, cleanup := InitializeApp()
	defer cleanup()

	// 5. 启动推送引擎（接收采集数据并推送到各通道）。
	// 先同步完成通道加载，再启动采集：否则采集首轮早于通道就绪时，
	// PushRecords 会因 len(chans)==0 直接 return 而丢弃首轮数据（见 push/engine.go）。
	if err := deps.PushEngine.Start(); err != nil {
		logger.Error("push engine start failed: %v", err)
	}

	// 6. 启动推送通道断联报警巡检器（依赖通道已加载）
	deps.AlarmMonitor.Start()

	// 7. 启动采集引擎
	go func() {
		if err := deps.CollectorEngine.Start(); err != nil {
			logger.Error("collection engine start failed: %v", err)
		}
	}()

	// 停止顺序：先停报警巡检（避免扫描关闭中的通道），再停推送、最后停采集。
	// defer 后进先出，先注册 Collector，使 AlarmMonitor/PushEngine 的 Stop 先执行。
	defer deps.CollectorEngine.Stop()
	defer deps.PushEngine.Stop()
	defer deps.AlarmMonitor.Stop()

	// 8. 启动 HTTP 服务器。使用 http.Server 而非 gin 的 Run，以便服务模式下优雅停机。
	addr := fmt.Sprintf(":%d", cfg.Server.Port)
	srv := &http.Server{
		Addr:    addr,
		Handler: deps.Engine,
	}
	errCh := make(chan error, 1)
	go func() {
		logger.Info("server starting on %s", addr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	select {
	case err := <-errCh:
		return err
	case <-stop:
		logger.Info("shutting down...")
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := srv.Shutdown(ctx); err != nil {
			logger.Error("server shutdown: %v", err)
		}
		return nil
	}
}
