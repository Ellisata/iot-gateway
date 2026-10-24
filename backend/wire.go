//go:build wireinject
// +build wireinject

// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

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
	"iot-gateway/database/sqlite"
	"iot-gateway/devicescan"
	"iot-gateway/discovery"
	"iot-gateway/metrics"
	"iot-gateway/notify"
	"iot-gateway/pluginhost"
	"iot-gateway/push"
	// 推送通道子包空导入：触发各通道 init 注册；新增通道在此追加。
	_ "iot-gateway/push/influxdb/v3"
	_ "iot-gateway/push/mqtt"
	_ "iot-gateway/push/opcuaserver"
	_ "iot-gateway/push/tdengine/v3"
	_ "iot-gateway/push/webhook"
	"iot-gateway/realtime"
	"iot-gateway/router"
	"iot-gateway/runtimecfg"
	"iot-gateway/service"
	"iot-gateway/subscribe"
	"iot-gateway/workerPool"
)

// AppDependencies 应用启动所需的所有依赖
type AppDependencies struct {
	Engine *gin.Engine
	// CollectorEngine 采集引擎。同时作为 pluginhost.Reloader 的实现，
	// 供外部驱动管理器在驱动集变化时触发 Refresh。
	CollectorEngine *collector.Engine
	PushEngine      *push.Engine
	AlarmMonitor    *alarm.ChannelMonitor
	// AlarmNotifier 报警通知器（把报警推送到钉钉/企微/飞书等 Webhook）
	AlarmNotifier *notify.Dispatcher
	// DriverManager 外部协议驱动（进程外插件）管理器
	DriverManager *pluginhost.Manager
	// Discovery 局域网自动发现（UDP 广播）广播器
	Discovery *discovery.Announcer
	// DeviceScan 局域网 PLC 扫描发现引擎（与 Discovery 方向相反：
	// Discovery 广播"我在哪"，DeviceScan 去问"网段里谁在"）
	DeviceScan *devicescan.Engine
	// Subscription 外部 MQTT 订阅管理器：把订阅报文转换为采集记录后
	// 定向投递到时序数据库类推送通道
	Subscription *subscribe.Manager
	// RuntimeConfig 运行期配置开关（default.yaml 作默认值，system_config 表作覆盖）。
	// 停机时先 Close 关闭更新入口，再按既有顺序停各组件。
	RuntimeConfig *runtimecfg.Manager
	// DashboardSampler 大盘遥测采样器：每分钟把在线率、采集吞吐与推送积压
	// 合成一个内存采样点，供 /dashboard/trend 查询。
	DashboardSampler *metrics.Sampler
	// WALCheckpointer SQLite WAL 定期维护：合并已 checkpoint 的帧，空闲时把 -wal
	// 截断到 0；外部读者钉住 WAL 时打 WARN。见 database/sqlite/walCheckpoint.go。
	WALCheckpointer *sqlite.WALCheckpointer
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
		// 运行期配置：default.yaml 的默认值 + system_config 表的覆盖值。
		// 所有需要动态开关的组件都依赖它，Wire 因此把它排在这些组件之前构造。
		runtimecfg.NewManager,
		wire.Bind(new(runtimecfg.Reader), new(*runtimecfg.Manager)),
		wire.Bind(new(runtimecfg.Runtime), new(*runtimecfg.Manager)),

		// 推送引擎（实现 collector.RecordSink，接收采集数据）
		push.NewEngineWithPolicy,
		// 外部 MQTT 订阅管理器：订阅数据经映射转换后只投递到时序数据库通道。
		// Sink 绑定到推送引擎的 PushRecordsToTimeSeries（见 push/engine.go）。
		subscribe.NewManager,
		wire.Bind(new(subscribe.Sink), new(*push.Engine)),
		// 实时值快照（实现 collector.RecordSink，供前端实时数据页查询）
		realtime.NewStore,
		// 大盘遥测计数器（实现 collector.RecordSink）+ 每分钟采样器。
		// 采样器依赖设备/采集/推送三个数据源，必须排在它们之后构造；
		// 它不依赖采集引擎本身，因此不会与 NewRecordSink 形成环。
		metrics.NewRecorder,
		metrics.NewSampler,
		wire.Bind(new(metrics.OverviewSource), new(*service.DeviceService)),
		wire.Bind(new(metrics.CollectionSource), new(*service.CollectionService)),
		wire.Bind(new(metrics.PushSource), new(*push.Engine)),
		// 采集记录的扇出：快照 + 遥测计数 + 推送。采集引擎只接受单个 RecordSink，
		// 三个消费方由 NewRecordSink 组装，各自不必感知对方。
		NewRecordSink,
		// 断联报警引擎（实现 collector.DeviceStateSink，接收设备在线状态）
		alarm.NewEngine,
		wire.Bind(new(collector.DeviceStateSink), new(*alarm.Engine)),
		// 点位质量报警引擎（实现 collector.PointStateSink，接收每个点位的采集质量）。
		// 与设备断联报警共用同一个 Tracker 实现、同一张 alarm 表、同一个通知出口，
		// 只是 target_type=point、判定的是"点位本身好不好"而不是"设备连不连得上"。
		alarm.NewPointEngine,
		wire.Bind(new(collector.PointStateSink), new(*alarm.PointEngine)),
		// 点位阈值报警引擎（实现 collector.PointValueSink，接收每个点位配了阈值的采集值）。
		// 与点位质量报警是两个 target_type（point_threshold / point）：Tracker 的落库键
		// 不含 alarm_type，共用 point 会让两者互相覆盖（见 alarm.TypePointThreshold）。
		// 它同时把越限样本落进 point_threshold_record 表。
		alarm.NewThresholdEngine,
		wire.Bind(new(collector.PointValueSink), new(*alarm.ThresholdEngine)),
		// 推送通道断联报警巡检器（复用 push.Engine.GetStatus 作为连接状态源）
		alarm.NewChannelMonitor,
		wire.Bind(new(alarm.StatusSource), new(*push.Engine)),

		// 报警通知器（实现 alarm.NotifySink，把报警推送到钉钉/企微/飞书等 Webhook）。
		// 必须与上面的 alarm.NewEngine / alarm.NewChannelMonitor 在同一次 wire.Build 中，
		// 否则会生成两个 Dispatcher 实例：配置写入触发的 Refresh 打不到状态机所用的那个。
		notify.NewDispatcher,
		wire.Bind(new(alarm.NotifySink), new(*notify.Dispatcher)),

		// 外部协议驱动（进程外插件）管理器。驱动集变化时调用采集引擎的
		// Refresh 热加载，因此把 *collector.Engine 绑定为 Reloader。
		pluginhost.NewManagerWithPolicy,
		wire.Bind(new(pluginhost.Reloader), new(*collector.Engine)),
		// 协议服务经此接口读取「插件声明的表单」（恢复默认表单用）
		wire.Bind(new(service.ProtocolFormSource), new(*pluginhost.Manager)),

		// 局域网自动发现广播器。两个来源都复用本图里已有的 provider：
		// 身份来自 GatewayService.GetGateway，默认密钥来自
		// OpenApiSecretService.DefaultOpenApiKey（随广播一起发布，供管理平台
		// 免人工录入直接调开放接口）。各自绑定到广播器声明的两个并列接口上。
		discovery.NewAnnouncerWithPolicy,
		wire.Bind(new(discovery.IdentitySource), new(*service.GatewayService)),
		wire.Bind(new(discovery.ApiKeySource), new(*service.OpenApiSecretService)),

		// 局域网 PLC 扫描发现引擎。两个依赖都落在插件管理器上：
		// 协议的能力声明（DiscoverySpecs）与目标的实际探测（ProbeTargets）。
		devicescan.NewEngineWithPolicy,
		wire.Bind(new(devicescan.SpecSource), new(*pluginhost.Manager)),
		wire.Bind(new(devicescan.Prober), new(*pluginhost.Manager)),

		// 点位范围扫描（设备地址页的「自动发现点位」）也要插件管理器，但用的是
		// 另一组方法（PointDiscoverySpecs / DiscoverPoints）—— 与上面两条是并列的
		// 绑定，不是同一个接口。接口形态的理由见 service.PointScanSource。
		wire.Bind(new(service.PointScanSource), new(*pluginhost.Manager)),

		// 领域路由集中注册，新增模块仅改 ProvideRouteOptions
		ProvideRouteOptions,

		router.SetupRouter,
		wire.Struct(new(AppDependencies), "*"),
	)
	return nil, nil
}

// NewRecordSink 组装采集记录的扇出接收者。
//
// 顺序：实时值快照在前 —— 它是纯内存写，先落快照再交推送引擎，
// 下游入队/落盘变慢时快照也不会滞后。
func NewRecordSink(store *realtime.Store, recorder *metrics.Recorder, pushEngine *push.Engine) collector.RecordSink {
	// 计数器排在推送之前：推送阻塞或丢弃时，采集吞吐仍然准确 ——
	// 这两个数字回答的是不同的问题（读到了多少 / 发出去多少）。
	return collector.MultiSink{store, recorder, pushEngine}
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
	rc *controller.RealtimeController,
	pc *controller.ProtocolController,
	pc2 *controller.PushChannelController,
	pcf *controller.PushChannelFormController,
	msc *controller.MqttSubscriptionController,
	pus *controller.PushController,
	ac2 *controller.AlarmController,
	awc *controller.AlarmWebhookController,
	awfc *controller.AlarmWebhookFormController,
	lc *controller.LogFileController,
	oasc *controller.OpenApiSecretController,
	oass *service.OpenApiSecretService,
	oac *controller.OpenApiController,
	gc *controller.GatewayController,
	plc *controller.PluginController,
	dsc *controller.DeviceScanController,
	scc *controller.SystemConfigController,
	dbc *controller.DashboardController,
) []router.RouteOption {
	return []router.RouteOption{
		router.WithUserRoutes(uc),
		router.WithDeviceRoutes(dc),
		router.WithDeviceAddressRoutes(ac),
		router.WithCollectionRoutes(cc),
		router.WithRealtimeRoutes(rc),
		router.WithProtocolRoutes(pc),
		router.WithPushChannelRoutes(pc2),
		router.WithPushChannelFormRoutes(pcf),
		router.WithMqttSubscriptionRoutes(msc),
		router.WithPushRoutes(pus),
		router.WithAlarmRoutes(ac2),
		router.WithAlarmWebhookRoutes(awc),
		router.WithAlarmWebhookFormRoutes(awfc),
		router.WithLogFileRoutes(lc),
		router.WithOpenApiSecretRoutes(oasc),
		router.WithOpenApiRoutes(oass, oac),
		router.WithGatewayRoutes(gc),
		router.WithPluginRoutes(plc),
		router.WithDeviceScanRoutes(dsc),
		router.WithSystemConfigRoutes(scc),
		router.WithDashboardRoutes(dbc),
		// dsh（AI Agent）调用面：把上述领域路由再挂一遍到 /agentApi（**无鉴权**）。
		// 刻意放在最后 —— 它复用同一批 controller，与前面各 With* 无顺序依赖。
		// 风险与暴露清单见 router/agentApiRouter.go。
		router.WithAgentApiRoutes(uc, dc, ac, cc, rc, pc, pc2, pcf, msc, pus, ac2, awc, awfc, lc, oasc, gc, plc, dsc, scc, dbc),
	}
}
