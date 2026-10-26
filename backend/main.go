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
	_ "iot-gateway/push/opcuaserver"
	_ "iot-gateway/push/tdengine/v3"
	_ "iot-gateway/push/webhook"

	"github.com/Ellisata/iot-gateway-sdk/driversdk"
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

	// 构建期版本（-X main.version）必须在任何读取网关身份的代码之前注入，
	// 否则身份行会先按配置文件里的旧值落库。
	configFile.SetBuildVersion(version)

	// 2. 初始化日志
	logger.InitLogger(cfg)

	// 3. 初始化国际化
	i18n.InitI18n()

	logger.Info("application starting... (version %s, plugin APIVersion %d)",
		configFile.ResolveGatewayVersion(cfg), driversdk.APIVersion)
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

	// 5.1 启动外部 MQTT 订阅管理器。
	//
	// 必须在推送引擎之后：订阅数据转换完成后直接投递给时序数据库通道，
	// 推送引擎尚未加载通道时入队会被静默丢弃（见 push.Engine.PushRecordsToTimeSeries）。
	// Start 同步加载一次订阅配置，broker 不可达只影响该订阅自身的重连，
	// 不阻塞启动；配置非法只记日志并跳过该条。
	if err := deps.Subscription.Start(); err != nil {
		logger.Error("mqtt subscription manager start failed: %v", err)
	}

	// 5.2 启动大盘遥测采样器。
	//
	// 越早启动越好：趋势图的横轴从「本次运行」开始，晚启动只会让前几分钟
	// 永远缺采样点。采样器只读各组件状态，不改变任何启动顺序。
	deps.DashboardSampler.Start()

	// 5.3 启动 SQLite WAL 定期维护（先 PASSIVE 合并，确认无读者后再 TRUNCATE）。
	//
	// 只是维护型 PRAGMA，不改变任何业务启动顺序；首次执行在一个间隔之后，
	// 启动期的迁移写入由 SQLite 自己的 wal_autocheckpoint 兜底。
	deps.WALCheckpointer.Start()

	// 6. 启动报警通知器（把报警推送到钉钉/企微/飞书等 Webhook）。
	//
	// 必须在报警巡检与采集引擎之前启动：Dispatcher 未启动时 Notify 会把事件
	// 直接计为丢弃（见 notify.Dispatcher.Notify 的 running 判定），
	// 启动窗口内产生的报警就发不出去了。
	// Start 内部同步加载一次 alarm_webhook 配置，失败只记日志、不中断启动。
	if err := deps.AlarmNotifier.Start(); err != nil {
		logger.Error("alarm notifier start failed: %v", err)
	}

	// 7. 启动推送通道断联报警巡检器（依赖通道已加载）
	deps.AlarmMonitor.Start()

	// 8. 启动外部协议驱动管理器（进程外插件）。
	//
	// 必须在采集引擎之前启动：插件驱动要先完成注册，采集引擎建任务时
	// 才能为使用插件协议的设备创建驱动；否则这些设备在首轮会被判为
	// 「协议不支持」而跳过（该跳过是非致命的，见 collector.newGatewayTask）。
	//
	// 默认关闭（plugin.enabled=false），此时 Start 是静默无操作。
	if err := deps.DriverManager.Start(); err != nil {
		logger.Error("driver plugin manager start failed: %v", err)
	}

	// 9. 启动采集引擎
	go func() {
		if err := deps.CollectorEngine.Start(); err != nil {
			logger.Error("collection engine start failed: %v", err)
		}
	}()

	// 10. 启动局域网自动发现广播器（UDP 广播 + 探询应答）。
	//
	// 不依赖任何引擎，放哪儿都行；失败只影响管理平台能否自动发现本网关，
	// 完全不影响采集与上报 —— 与 DriverManager 一样只记日志，不中断启动。
	// 常见失败是发现端口被占（同主机跑了第二个实例），见 default.yaml 的说明。
	//
	// discovery.enabled=false 时是静默无操作。
	if err := deps.Discovery.Start(); err != nil {
		logger.Error("lan discovery start failed: %v", err)
	}

	// 停止顺序：先停自动发现，再停报警巡检（避免扫描关闭中的通道），然后停推送、
	// 停采集，最后停插件管理器。
	// defer 后进先出，所以这里从后往前注册：先注册的后执行、最后注册的最先执行。
	//
	// Discovery 注册在靠后位置，因此很早停止：网关一开始停机就先安静下来，
	// 而不是继续向平台广播一台正在关闭的自己。
	// DriverManager 注册在最前，因此最后执行 —— 那时采集已完全停止
	//（驱动已 Close、无 Read 在途），杀插件进程才安全。
	//
	// AlarmNotifier 在它之前注册，因此比它更晚停止：此时采集与报警巡检都已停，
	// 不可能再有新事件进来，Stop 内部再花至多 5s 排空在途投递。
	// **这一条不能挪到本块末尾** —— defer 后进先出，挪过去它就会最先停，
	// 而巡检与采集仍在运行，期间的报警只能被计为丢弃。
	defer deps.AlarmNotifier.Stop()
	defer deps.DriverManager.Stop()
	// DeviceScan 注册在 DriverManager **之后**，因此比它先停止：
	// 扫描的在飞探测是直接发给插件进程的，必须在插件被退役之前取消并收尾，
	// 否则那些请求会写进一个正在关闭的管道。
	//
	// 它没有 Start()（没人发起扫描之前不持有任何资源），所以这里只有 Stop。
	defer deps.DeviceScan.Stop()
	defer deps.CollectorEngine.Stop()
	// Subscription 注册在 PushEngine 之后，因此比它先停止：先断开订阅、
	// 不再有新的外部记录进入推送队列，再停推送引擎排空在途批次。
	defer deps.Subscription.Stop()
	defer deps.PushEngine.Stop()
	defer deps.AlarmMonitor.Stop()
	defer deps.Discovery.Stop()
	// RuntimeConfig 注册在靠后位置，因此很早就执行：先关掉「运行开关」的更新入口，
	// 避免停机过程中还有请求把刚停掉的 discovery 重新拉起来。
	defer deps.RuntimeConfig.Close()
	// 采样器最后注册、最先停止：停机过程中不再往趋势里写入半停机状态的采样点。
	defer deps.DashboardSampler.Stop()
	// WAL 维护器注册在最后，因此比采样器**更先**停止：停机期间不再发起新的
	// checkpoint 去抢唯一的那条数据库连接，后续排空 outbox / 停采集的写入不会
	// 撞上一次等待读者的 TRUNCATE（最长 busy_timeout=5s）。
	defer deps.WALCheckpointer.Stop()

	// 10. 启动 HTTP 服务器。使用 http.Server 而非 gin 的 Run，以便服务模式下优雅停机。
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
