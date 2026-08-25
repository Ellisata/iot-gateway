//go:build wireinject
// +build wireinject

package main

import (
	"runtime"

	"github.com/gin-gonic/gin"
	"github.com/google/wire"

	"iot-gateway/alarm"
	"iot-gateway/collector"
	"iot-gateway/configFile"
	"iot-gateway/controller"
	"iot-gateway/database"
	"iot-gateway/push"
	"iot-gateway/router"
	"iot-gateway/service"
	"iot-gateway/workerPool"
)

// AppDependencies 应用启动所需的所有依赖
type AppDependencies struct {
	Engine          *gin.Engine
	CollectorEngine *collector.Engine
	PushEngine      *push.Engine
	AlarmMonitor    *alarm.ChannelMonitor
}

// InitializeApp 构建完整的应用依赖图
func InitializeApp() (*AppDependencies, func()) {
	wire.Build(
		configFile.InitConfig,
		database.DatabaseProviderSet,
		service.ServiceProviderSet,
		controller.ControllerProviderSet,

		// Worker 池
		NewWorkerPool,
		// 采集引擎
		collector.NewEngine,
		// 推送引擎（实现 collector.RecordSink，接收采集数据）
		push.NewEngine,
		wire.Bind(new(collector.RecordSink), new(*push.Engine)),
		// 断联报警引擎（实现 collector.DeviceStateSink，接收设备在线状态）
		alarm.NewEngine,
		wire.Bind(new(collector.DeviceStateSink), new(*alarm.Engine)),
		// 推送通道断联报警巡检器（复用 push.Engine.GetStatus 作为连接状态源）
		alarm.NewChannelMonitor,
		wire.Bind(new(alarm.StatusSource), new(*push.Engine)),

		// 领域路由集中注册，新增模块仅改 ProvideRouteOptions
		ProvideRouteOptions,

		router.SetupRouter,
		wire.Struct(new(AppDependencies), "*"),
	)
	return nil, nil
}

// NewWorkerPool 从配置创建 Worker 池
func NewWorkerPool(cfg *configFile.Config) *workerPool.WorkerPool {
	pool := workerPool.NewWorkerPool(runtime.NumCPU()*2, 1024)
	pool.Start()
	return pool
}

// ProvideRouteOptions 组装所有领域路由选项，新增模块在此追加
func ProvideRouteOptions(
	uc *controller.UserController,
	dc *controller.DeviceController,
	ac *controller.DeviceAddressController,
	cc *controller.CollectionController,
	pc *controller.ProtocolController,
	pc2 *controller.PushChannelController,
	pcf *controller.PushChannelFormController,
	pus *controller.PushController,
	ac2 *controller.AlarmController,
	lc *controller.LogFileController,
) []router.RouteOption {
	return []router.RouteOption{
		router.WithUserRoutes(uc),
		router.WithDeviceRoutes(dc),
		router.WithDeviceAddressRoutes(ac),
		router.WithCollectionRoutes(cc),
		router.WithProtocolRoutes(pc),
		router.WithPushChannelRoutes(pc2),
		router.WithPushChannelFormRoutes(pcf),
		router.WithPushRoutes(pus),
		router.WithAlarmRoutes(ac2),
		router.WithLogFileRoutes(lc),
	}
}
