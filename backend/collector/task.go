// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package collector

import (
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"gorm.io/gorm"

	"github.com/Ellisata/iot-gateway-sdk/driver"
	"iot-gateway/driveradapter"
	"iot-gateway/logger"
	"iot-gateway/model/po"
	"iot-gateway/workerPool"
)

// maxPointsPerRead 单次驱动 Read 的最大点位上限。
// 超过时按批顺序流式处理：读取 → 转换 → 推送逐批进行，
// 限制单次 Read 与每轮整设备累积的瞬时内存峰值，
// 并将单个异常点位的故障隔离在所属批次内。
// 后续可提升为配置项（configFile.Config）。
const maxPointsPerRead = 2000

// forEachBatch 将点位按 maxPointsPerRead 分批，顺序对每批执行 fn。
// 对 addrs 底层数组零拷贝切片；任一批 fn 返回错误即停止并回传该错误。
// 供 doPoll 流式处理大批量点位：逐批读取/转换/推送，避免整台设备点位一次性累积。
func forEachBatch(addrs []po.DeviceAddress, fn func(batch []po.DeviceAddress) error) error {
	for i := 0; i < len(addrs); i += maxPointsPerRead {
		end := i + maxPointsPerRead
		if end > len(addrs) {
			end = len(addrs)
		}
		if err := fn(addrs[i:end]); err != nil {
			return err
		}
	}
	return nil
}

// frequencyGroup 同一采集频率的点位集合
// 每个频率分组拥有独立的 ticker 与调度 goroutine，按点位自身配置的 ScanFrequency 触发采集；
// 实际读取以「每台设备一个任务」提交到共享 workerPool，设备之间互不阻塞。
type frequencyGroup struct {
	interval   time.Duration                 // 采集间隔（基于 ScanFrequency）
	addrGroups map[string][]po.DeviceAddress // deviceID -> 该频率下的点位列表
	task       *gatewayTask

	inflightMu sync.Mutex
	inflight   map[string]bool // deviceID -> 该组是否已有在途轮询（防慢设备轮询堆积）

	quit chan struct{}
	done chan struct{}
}

// gatewayTask 轮询任务
// 对每个活跃设备创建对应的协议驱动，按点位自身的采集频率分组调度；
// 分组 goroutine 只负责调度，实际读取经共享 workerPool 按设备并发执行。
type gatewayTask struct {
	devices []po.Device
	drivers map[string]driver.Driver // deviceID -> protocol driver
	groups  []*frequencyGroup        // 按采集频率分组的采集任务

	// addrNameMap: deviceID -> addressID -> addressName（快速名称查找）
	addrNameMap map[string]map[string]string
	// addrLabelMap: deviceID -> addressID -> 地址 name 的中文说明（如温度、电流）
	addrLabelMap map[string]map[string]string

	// protocols: deviceID -> 协议名称（iot_protocol.name，如 "ModBus.TCP"）。
	// 透传到采集记录，供下游区分协议命名空间（dataType 为协议内部类型名）。
	protocols map[string]string

	db        *gorm.DB
	sink      RecordSink      // 采集数据接收者（推送引擎等），可为 nil
	stateSink DeviceStateSink // 设备在线状态接收者（断联报警），可为 nil
	pointSink PointStateSink  // 点位质量接收者（点位质量报警），可为 nil

	// pointValueSink 点位采集值接收者（点位阈值报警），可为 nil。
	// 与 pointSink 分开：质量与值是两类不同的事实，且分开才能各自独立地绑或不绑。
	pointValueSink PointValueSink
	pool           *workerPool.WorkerPool

	// devMuMap: deviceID -> 设备级互斥锁。串行化对同一设备驱动的重连与读取。
	// 独占串行总线驱动（RTU）整轮持锁；线程安全驱动（TCP/S7）仅对重连+单次 Read 加锁，
	// 避免不同频率分组的并发轮询在驱动上互相干扰（见 pollDevice）。
	devMuMapMu sync.Mutex
	devMuMap   map[string]*sync.Mutex

	// wg 统计在途轮询任务，Stop 时先排空再关闭驱动，保证无轮询访问已关闭连接。
	wg sync.WaitGroup

	// recMu / reconnect: 设备断线重连的指数退避状态（deviceID -> state）
	recMu     sync.Mutex
	reconnect map[string]reconnectState

	mu              sync.RWMutex
	running         bool
	lastPollTime    time.Time
	lastSuccessTime time.Time
	lastSuccessAt   map[string]time.Time // deviceID -> 最近一次成功采集时间（内存态，统计用）
	// readErrLog: deviceID -> 上次记录过的失败文案。同一台设备连续失败且原因未变时
	// 不重复记录；成功后清除（见 logReadError / clearReadErrLocked）。
	readErrLog map[string]string
	// reconnectLog: deviceID -> 重连日志的限流状态（见 logReconnect）。
	reconnectLog   map[string]reconnectLogState
	errorCount     int
	consecutiveErr int

	quit chan struct{}
}

// reconnectState 单台设备的断线重连退避状态。
type reconnectState struct {
	lastAttempt time.Time
	delay       time.Duration
}

// 重连退避参数：首次失败延迟 1s，逐次翻倍，上限 60s。
// 避免设备掉线时每个采集周期都发起一次连接尝试（占用 worker 且放大日志）。
const (
	reconnectMinDelay = time.Second
	reconnectMaxDelay = 60 * time.Second
)

// reconnectLogInterval 「device reconnected」这行日志的限流窗口（见 logReconnect）。
//
// 取 60s，与 reconnectMaxDelay 同量级：**连不上的设备**本来就被退避压到最多一分钟
// 一次尝试，这里取同一量级，让"退避压得住的"和"退避压不住的"两种抖动在日志里以
// 相近的频率出现 —— 否则同一种现象会因为成因不同而看起来一密一疏。
const reconnectLogInterval = 60 * time.Second

// reconnectLogState 单台设备的"重连成功"日志限流状态。
type reconnectLogState struct {
	at    time.Time // 上次真正输出这行的时间（零值 = 还没输出过）
	count int       // 自上次输出以来累计的重连次数（含下一次将要输出的那次）
}

// logReadError 记一行读取失败，但**同一台设备只在失败状态发生变化时**记：
// 首次失败，或失败文案与上次不同。成功会清掉记录（clearReadErrLocked），
// 所以"好了又坏"会重新记一次，间歇性失败也逐次可见。
//
// 为什么**不**用时间窗口限流：一台间歇性故障的设备（失败几轮、成功一轮）正是最该
// 被看见的那种，时间窗口会把它的中间几次失败一并吃掉；而"每轮都失败"的永久故障
// 用"状态是否变化"就能完全压住。判据与驱动侧的 warnOnce 一致（首次或内容变化）。
//
// 记录的是**已格式化的错误文案**而不是 err 本身：判等需要的正是"原因有没有变"，
// 而变化的载体就是文案（同一台设备同类失败但错误文本里带了不同地址/状态码时，
// 那是真的换了原因，该重新记）。
func (t *gatewayTask) logReadError(dev *po.Device, err error) {
	if !t.shouldLogReadError(dev.ID, err.Error()) {
		return
	}
	logger.Error("collector: read device %s failed: %v", dev.Name, err)
}

// shouldLogReadError 判断这次失败该不该记一行，并顺手登记 —— 判据与理由见 logReadError。
// 拆出来是为了能直接测：这条规则靠"等下一轮"，没有可注入的时钟或输出的重放装置时，
// 只有把判断本身暴露出来才测得到（回归见 task_logdedup_test.go）。
func (t *gatewayTask) shouldLogReadError(deviceID, text string) bool {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.readErrLog == nil {
		t.readErrLog = make(map[string]string)
	}
	prev, seen := t.readErrLog[deviceID]
	t.readErrLog[deviceID] = text
	return !seen || prev != text
}

// clearReadErrLocked 一轮采集成功后清掉失败记录，让下一次失败能重新记一行。
// 调用方须持有 t.mu（与 lastSuccessAt 同一处更新）。
func (t *gatewayTask) clearReadErrLocked(deviceID string) {
	delete(t.readErrLog, deviceID)
}

// logReconnect 记一行重连成功，但**每台设备每个窗口最多一行**。
//
// 为什么不能每轮都记：设备进入"每轮断开 → 每轮重连"的状态时这一行就按轮询周期
// 无限重复（真机验证过：设备在封装层拒绝命令 → 库 Reset 掉会话 → 采集侧下一轮
// 只能重连，见 driversrc/rockwellcip 的 encapError）。而它讲的"连上了"本身不是
// 新事实 —— 在线状态另有 reportState 与断联报警在管。
//
// 但**被压掉的次数必须在下一次输出时带出来**：一台设备"每分钟连一次"与"每轮都连"
// 是两件完全不同的事，只留一行会让人把后者误读成前者 —— 而"稳不稳"恰恰是运维据此
// 判断要不要去查现场的判据。
func (t *gatewayTask) logReconnect(dev *po.Device) {
	n, due := t.shouldLogReconnect(dev.ID, time.Now())
	if !due {
		return
	}
	if n > 1 {
		logger.Info("collector: device %s reconnected（近 %s 内第 %d 次）", dev.Name, reconnectLogInterval, n)
		return
	}
	logger.Info("collector: device %s reconnected", dev.Name)
}

// shouldLogReconnect 判断这次重连该不该记一行，并给出自上次输出以来累计的重连次数
// （n>1 时调用方会把它带进日志）。now 由调用方传入，测试才能把窗口"拨"过去
// —— 理由同 shouldLogReadError。
//
// due=false 时 n 无意义。
func (t *gatewayTask) shouldLogReconnect(deviceID string, now time.Time) (n int, due bool) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.reconnectLog == nil {
		t.reconnectLog = make(map[string]reconnectLogState)
	}
	st := t.reconnectLog[deviceID]
	st.count++
	if !st.at.IsZero() && now.Sub(st.at) < reconnectLogInterval {
		t.reconnectLog[deviceID] = st
		return 0, false
	}
	n = st.count
	t.reconnectLog[deviceID] = reconnectLogState{at: now}
	return n, true
}

// deviceLock 返回指定设备的互斥锁（懒初始化，供手工构造的 task 直接使用）。
func (t *gatewayTask) deviceLock(deviceID string) *sync.Mutex {
	t.devMuMapMu.Lock()
	defer t.devMuMapMu.Unlock()
	if t.devMuMap == nil {
		t.devMuMap = make(map[string]*sync.Mutex)
	}
	m, ok := t.devMuMap[deviceID]
	if !ok {
		m = &sync.Mutex{}
		t.devMuMap[deviceID] = m
	}
	return m
}

// findDevice 按 ID 查找设备（活跃设备列表线性查找，数量少、频率低，可接受）。
func (t *gatewayTask) findDevice(deviceID string) *po.Device {
	for i := range t.devices {
		if t.devices[i].ID == deviceID {
			return &t.devices[i]
		}
	}
	return nil
}

// reconnectAllowed 判断设备当前是否允许发起重连尝试（退避窗口内返回 false）。
// 仅由 ensureConnected 调用（持有该设备的 deviceLock），check→act 无竞态。
func (t *gatewayTask) reconnectAllowed(deviceID string) bool {
	t.recMu.Lock()
	defer t.recMu.Unlock()
	st := t.reconnect[deviceID] // 读 nil map 返回零值，无需初始化
	return st.lastAttempt.IsZero() || time.Since(st.lastAttempt) >= st.delay
}

// recordReconnectFail 记录一次重连失败并扩大退避延迟。
func (t *gatewayTask) recordReconnectFail(deviceID string) {
	t.recMu.Lock()
	defer t.recMu.Unlock()
	if t.reconnect == nil {
		t.reconnect = make(map[string]reconnectState)
	}
	st := t.reconnect[deviceID]
	st.lastAttempt = time.Now()
	switch {
	case st.delay <= 0:
		st.delay = reconnectMinDelay
	case st.delay*2 > reconnectMaxDelay:
		st.delay = reconnectMaxDelay
	default:
		st.delay *= 2
	}
	t.reconnect[deviceID] = st
}

// recordReconnectSuccess 重连成功后清空退避状态。
func (t *gatewayTask) recordReconnectSuccess(deviceID string) {
	t.recMu.Lock()
	defer t.recMu.Unlock()
	if t.reconnect == nil {
		t.reconnect = make(map[string]reconnectState)
	}
	st := t.reconnect[deviceID]
	st.lastAttempt = time.Now()
	st.delay = 0
	t.reconnect[deviceID] = st
}

// newGatewayTask 创建新的轮询任务
func newGatewayTask(db *gorm.DB, sink RecordSink, pool *workerPool.WorkerPool, stateSink DeviceStateSink, pointSink PointStateSink, pointValueSink PointValueSink) (*gatewayTask, error) {
	// 加载所有活跃设备（允许为空：构建空任务，engine 空转并持续热加载，
	// 否则 Start/Refresh 在无活跃设备时失败，热加载 watcher 无法感知后续设备启用）
	var devices []po.Device
	if err := db.Where("status = 1").Find(&devices).Error; err != nil {
		return nil, fmt.Errorf("collector: load devices failed: %w", err)
	}
	if len(devices) == 0 {
		logger.Warn("collector: no active devices, collector will idle until a device is enabled")
	}

	// 预加载所有协议名称映射
	protocolMap := make(map[string]string)
	var protocols []po.IotProtocol
	if err := db.Find(&protocols).Error; err != nil {
		return nil, fmt.Errorf("collector: load protocols failed: %w", err)
	}
	for _, p := range protocols {
		protocolMap[p.ID] = p.Name
	}

	// 为每个设备创建协议驱动（只创建不连接）。
	// 连接改为懒加载：首次轮询（或热刷新后）由 doPollDevice 按需 Connect。
	// 这样 Refresh 建新任务时不会与仍持串口的旧任务冲突（RTU 二次打开报 Access denied）。
	drvMap := make(map[string]driver.Driver, len(devices))
	devProtocols := make(map[string]string) // deviceID -> 协议名称（透传到采集记录）
	for _, dev := range devices {
		protocolName := protocolMap[dev.ProtocolID]
		if protocolName == "" {
			logger.Warn("collector: device %s protocol_id %q not found, skip",
				dev.Name, dev.ProtocolID)
			continue
		}
		drv, err := driver.Create(protocolName)
		if err != nil {
			logger.Warn("collector: device %s protocol %q unsupported, skip: %v",
				dev.Name, protocolName, err)
			continue
		}
		drvMap[dev.ID] = drv
		devProtocols[dev.ID] = protocolName
	}

	if len(drvMap) == 0 {
		logger.Warn("collector: no devices with supported protocol driver, collector will idle until a supported device is enabled")
	}

	// 加载设备地址并按采集频率分组。
	// 仅对拥有驱动的设备加载地址，避免协议不支持/驱动创建失败的设备
	// 每周期仍提交 worker 任务后空转返回。
	freqToDevAddrs := make(map[int]map[string][]po.DeviceAddress) // frequency -> deviceID -> addrs
	addrNameMap := make(map[string]map[string]string)             // deviceID -> addressID -> name
	addrLabelMap := make(map[string]map[string]string)            // deviceID -> addressID -> 中文说明

	for _, dev := range devices {
		if _, ok := drvMap[dev.ID]; !ok {
			continue // 无可用驱动，跳过该设备的地址加载与调度
		}

		var addrs []po.DeviceAddress
		if err := db.Where("device_id = ? AND status = 1", dev.ID).Find(&addrs).Error; err != nil {
			return nil, fmt.Errorf("collector: load addresses for device %s failed: %w", dev.ID, err)
		}

		addrNameMap[dev.ID] = make(map[string]string)
		addrLabelMap[dev.ID] = make(map[string]string)
		protocolName := devProtocols[dev.ID]
		for i := range addrs {
			addr := &addrs[i]

			// data_type 是写入时派生的协议内部类型名缓存，可能因协议类型注册名变更、
			// 旧数据或外部改库而漂移。加载时统一回退修正（data_type 仍可解析则保留原值），
			// 修正结果回写库自愈，避免采集时驱动解码失败、寄存器/字数算错或类型码错取。
			if normalized := driver.NormalizeAddressType(protocolName, driveradapter.FromPO(*addr)); normalized != addr.DataType {
				addr.DataType = normalized
				if err := db.Model(&po.DeviceAddress{}).Where("id = ?", addr.ID).
					UpdateColumn("data_type", normalized).Error; err != nil {
					logger.Warn("collector: fix data_type for address %s (device %s) failed: %v",
						addr.ID, dev.ID, err)
				}
			}

			addrNameMap[dev.ID][addr.ID] = addr.Name
			addrLabelMap[dev.ID][addr.ID] = addr.Label

			// 按采集频率分组
			freq := addr.ScanFrequency
			if freq <= 0 {
				freq = 1000 // 默认 1 秒
			}
			if freqToDevAddrs[freq] == nil {
				freqToDevAddrs[freq] = make(map[string][]po.DeviceAddress)
			}
			freqToDevAddrs[freq][dev.ID] = append(freqToDevAddrs[freq][dev.ID], *addr)
		}
	}

	task := &gatewayTask{
		devices:        devices,
		drivers:        drvMap,
		addrNameMap:    addrNameMap,
		addrLabelMap:   addrLabelMap,
		protocols:      devProtocols,
		db:             db,
		sink:           sink,
		stateSink:      stateSink,
		pointSink:      pointSink,
		pointValueSink: pointValueSink,
		pool:           pool,
		lastSuccessAt:  make(map[string]time.Time),
		quit:           make(chan struct{}),
	}

	// 创建频率分组
	for freq, devAddrs := range freqToDevAddrs {
		group := &frequencyGroup{
			interval:   time.Duration(freq) * time.Millisecond,
			addrGroups: devAddrs,
			task:       task,
			inflight:   make(map[string]bool),
			quit:       make(chan struct{}),
			done:       make(chan struct{}),
		}
		task.groups = append(task.groups, group)
	}

	return task, nil
}

// Start 启动所有频率分组的轮询
func (t *gatewayTask) Start() {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.running {
		return
	}

	t.running = true
	t.errorCount = 0
	t.consecutiveErr = 0

	for _, g := range t.groups {
		go g.runLoop()
	}

	logger.Info("collector: started task with %d frequency groups, %d devices",
		len(t.groups), len(t.drivers))
}

// Stop 停止所有频率分组的轮询
func (t *gatewayTask) Stop() {
	t.mu.Lock()
	if !t.running {
		t.mu.Unlock()
		return
	}
	t.running = false
	close(t.quit)
	// 关键：不能在持有 t.mu 时等待 g.done。
	// doPollDevice 内部需要 t.mu 更新 lastPollTime/lastSuccessTime/errorCount，
	// 否则采集协程会阻塞在锁上而永不退出，导致 Stop 死锁、旧任务无法真正停止。
	t.mu.Unlock()

	// 等待所有频率分组调度 goroutine 结束（此后不再有 wg.Add）
	for _, g := range t.groups {
		<-g.done
	}

	// 排空 workerPool 中在途的轮询任务，避免其访问已关闭的驱动连接
	t.wg.Wait()

	// 关闭所有协议驱动（所有轮询已退出，可安全关闭）
	t.mu.Lock()
	for id, drv := range t.drivers {
		drv.Close()
		delete(t.drivers, id)
	}
	t.mu.Unlock()

	logger.Info("collector: stopped task")
}

// IsRunning 返回任务是否在运行
func (t *gatewayTask) IsRunning() bool {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.running
}

// Status 返回任务状态（协议无关）
func (t *gatewayTask) Status() (running bool, lastPoll time.Time, lastSuccess time.Time, errCount int, deviceCount int) {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.running, t.lastPollTime, t.lastSuccessTime, t.errorCount, len(t.drivers)
}

// deviceLastSuccess 返回指定设备最近一次成功采集的时间（无记录返回零值）。
// lastSuccessAt 为内存态：网关重启、热刷新新建任务后会清空，直到下一轮成功采集（≤1 个采集周期）。
func (t *gatewayTask) deviceLastSuccess(deviceID string) time.Time {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.lastSuccessAt[deviceID]
}

// activeDeviceIDs 返回当前参与采集调度的设备 ID（有协议驱动且至少一个活跃地址）。
// addrGroups 建任务后不可变，读无需加组内锁；返回无序列表。
func (t *gatewayTask) activeDeviceIDs() []string {
	t.mu.RLock()
	defer t.mu.RUnlock()
	seen := make(map[string]bool)
	for _, g := range t.groups {
		for id := range g.addrGroups {
			seen[id] = true
		}
	}
	ids := make([]string, 0, len(seen))
	for id := range seen {
		ids = append(ids, id)
	}
	return ids
}

// GetDriver 返回指定设备的协议驱动实例（设备不存在或未创建驱动时返回 nil）
func (t *gatewayTask) GetDriver(deviceID string) driver.Driver {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.drivers[deviceID]
}

// SerialPortHolder 一台"声明了串口、且确有驱动实例"的设备。
//
// 值类型而非 *po.Device：本结构会跨出 t.mu 交给调用方使用，而 devices 会在
// 热刷新（Refresh 建新任务）时整体换掉，指针逸出就会读到一个已退役的任务快照。
type SerialPortHolder struct {
	DeviceID   string
	DeviceName string
	Protocol   string
	Port       string
	// Connected 该设备是否报告链路可用。
	//
	// 它**不是**"是否持有句柄"的判据：串口读失败只把连接标记为断开、并不释放
	// 句柄，那个端口在重连之前仍然被占着 —— 而这恰恰是最需要提示的时刻。
	// 所以它只用来分档措辞（"占用" vs "可能仍占用"），不用来过滤。
	Connected bool
}

// EndpointHolder 一台"配了以太网目标（host:port）、且确有驱动实例"的设备。
//
// 与 SerialPortHolder 是同一次反推（见 eachLiveDevice），但**语义不同，别合并**：
// 串口是独占资源，"配了 comPort 且有实例"就足够可疑；TCP 目标不是独占资源 ——
// 网关自己就是那个开连接的人，同一台 PLC 上同时挂多条连接完全正常。所以这里的
// Connected 不表示"目标被占用"，只表示"该设备此刻确实持有一条到该地址的连接"，
// 调用方据它**分档措辞**（见 service 的 endpointBusyHint），不据它过滤。
type EndpointHolder struct {
	DeviceID   string
	DeviceName string
	Protocol   string
	Endpoint   string // host:port，原样保留（host 的大小写归一由比较方负责）
	// Connected 见类型注释：它说的是"这台设备的链路此刻是通的"。
	// remoteDriver.IsConnected 是纯原子读、不走 RPC。
	Connected bool
}

// eachLiveDevice 遍历"确有驱动实例"的设备，回调里带上实例与协议名。
//
// 两个 holder 方法共用它：**"无驱动实例要跳过"这条筛选规则只有一份**。分开写
// 迟早会有一边漏掉 —— 而漏掉的症状是提示指向一台不可能持有任何连接的设备
// （协议不支持的设备在 newGatewayTask 的 skip 分支里根本没有实例）。
//
// 回调在 t.mu 持锁期间执行：不得回头再取 t.mu，也不得调用驱动的阻塞方法
// （drv.IsConnected 是纯原子读，可以）。
func (t *gatewayTask) eachLiveDevice(fn func(dev po.Device, drv driver.Driver, protocol string)) {
	t.mu.RLock()
	defer t.mu.RUnlock()

	for _, dev := range t.devices {
		drv, ok := t.drivers[dev.ID] // 无驱动 → 不可能持有连接/句柄
		if !ok {
			continue
		}
		fn(dev, drv, t.protocols[dev.ID])
	}
}

// serialPortHolders 返回本任务里所有声明了串口的设备。
//
// 网关口拿不到插件进程里的句柄，只能反推：**配了 comPort 且确有驱动实例**
// 的设备才可能持有串口。
//
// 已知盲区（写在这里以免被当成 bug 反复排查）：
//   - 驱动是懒连接的（newGatewayTask 只 Create 不 Connect），所以"有实例"≠
//     "句柄已打开"。首次采集尚未发生、或设备正在重连退避窗口内时，本函数会
//     指向一台并没有打开串口的设备。故调用方拼出的提示只说"当前由…占用"这一
//     事实，不承诺因果。
//   - 端口被网关之外的进程占用时（现场工具、另一个网关实例）这里看不到，
//     提示也就不会出现。
func (t *gatewayTask) serialPortHolders() []SerialPortHolder {
	out := make([]SerialPortHolder, 0, 8)
	t.eachLiveDevice(func(dev po.Device, drv driver.Driver, protocol string) {
		port := driver.ExtractSerialPort(dev.ProtocolJSON)
		if port == "" {
			return
		}
		out = append(out, SerialPortHolder{
			DeviceID:   dev.ID,
			DeviceName: dev.Name,
			Protocol:   protocol,
			Port:       port,
			Connected:  drv.IsConnected(),
		})
	})
	return out
}

// endpointHolders 返回本任务里所有配了以太网目标（host:port）的设备。
//
// 盲区与 serialPortHolders 同一条：驱动是懒连接的，"有实例"≠"此刻连着"。
// 连不上的设备照样返回（Connected=false）—— 设备侧被一条**已死**的连接占住槽位时，
// 网关侧恰恰没有任何"正在采集"的设备，而它才是那个地址上唯一的历史持有者。
// 故这里不过滤，由调用方按 Connected 分档措辞（见 service 的 endpointBusyHint）。
func (t *gatewayTask) endpointHolders() []EndpointHolder {
	out := make([]EndpointHolder, 0, 8)
	t.eachLiveDevice(func(dev po.Device, drv driver.Driver, protocol string) {
		endpoint := driver.ExtractEndpoint(dev.ProtocolJSON)
		if endpoint == "" {
			return
		}
		out = append(out, EndpointHolder{
			DeviceID:   dev.ID,
			DeviceName: dev.Name,
			Protocol:   protocol,
			Endpoint:   endpoint,
			Connected:  drv.IsConnected(),
		})
	})
	return out
}

// TotalAddressCount 返回所有分组的地址总数
func (t *gatewayTask) TotalAddressCount() int {
	t.mu.RLock()
	defer t.mu.RUnlock()
	count := 0
	for _, g := range t.groups {
		for _, addrs := range g.addrGroups {
			count += len(addrs)
		}
	}
	return count
}

// runLoop 频率分组调度 goroutine：只负责按周期把每台设备的采集提交到共享 workerPool，
// 设备之间互不阻塞（一台慢/掉线设备不再拖垮同频率组其它设备）。
func (g *frequencyGroup) runLoop() {
	defer close(g.done)

	ticker := time.NewTicker(g.interval)
	defer ticker.Stop()

	// 启动后立即调度一轮
	g.schedulePoll()

	for {
		select {
		case <-g.task.quit:
			return
		case <-ticker.C:
			g.schedulePoll()
		}
	}
}

// schedulePoll 将本频率组内的每台设备作为独立任务提交到 workerPool。
func (g *frequencyGroup) schedulePoll() {
	for deviceID, addrs := range g.addrGroups {
		g.tryScheduleDevice(deviceID, addrs)
	}
}

// tryScheduleDevice 提交单台设备的轮询任务。
// 同组同设备已有一个在途轮询时跳过本轮（防慢设备轮询堆积）；workerPool 满时丢弃本轮。
func (g *frequencyGroup) tryScheduleDevice(deviceID string, addrs []po.DeviceAddress) {
	g.inflightMu.Lock()
	if g.inflight == nil {
		g.inflight = make(map[string]bool)
	}
	if g.inflight[deviceID] {
		g.inflightMu.Unlock()
		return
	}
	g.inflight[deviceID] = true
	g.inflightMu.Unlock()

	g.task.wg.Add(1)
	if !g.task.pool.TrySubmit(func() {
		defer g.task.wg.Done()
		defer g.clearInflight(deviceID)
		g.pollDevice(deviceID, addrs)
	}) {
		// 池满：回滚计数与在途标记
		g.task.wg.Done()
		g.clearInflight(deviceID)
		logger.Warn("collector: worker pool full, skip device %s poll this cycle", deviceID)
	}
}

func (g *frequencyGroup) clearInflight(deviceID string) {
	g.inflightMu.Lock()
	delete(g.inflight, deviceID)
	g.inflightMu.Unlock()
}

// pollDevice 单台设备轮询入口（在 workerPool 协程中执行）。
// 用设备级互斥锁串行化对同一设备驱动的重连与读取，避免不同频率分组的并发轮询互相干扰。
// 锁粒度按驱动底层连接区分：
//   - 独占串行总线驱动（Modbus RTU）：整轮持锁（重连 + 全部批次读取 + 转换 + 推送），
//     保证串口上的帧不被交错；
//   - 内部线程安全驱动（Modbus TCP / S7）：仅对「重连 + 单次 Read」加锁，
//     批量间的转换与推送不持锁——同一设备慢频率分组的大批量轮询不会长时间占用设备锁，
//     避免拖住快频率分组的实际采集周期。
func (g *frequencyGroup) pollDevice(deviceID string, addrs []po.DeviceAddress) {
	dm := g.task.deviceLock(deviceID)
	if serialExclusive(g.task.GetDriver(deviceID)) {
		dm.Lock()
		defer dm.Unlock()
		g.doPollDevice(deviceID, addrs)
		return
	}
	g.doPollDeviceConcurrent(deviceID, addrs, dm)
}

// doPoll 同步执行一次该频率分组的采集（逐设备轮询，供测试/同步场景直接调用）。
func (g *frequencyGroup) doPoll() {
	for deviceID, addrs := range g.addrGroups {
		g.pollDevice(deviceID, addrs)
	}
}

// serialExclusive 判断驱动是否独占串行总线（需整轮持锁）。
// 未实现 driver.SerialExclusive 的驱动视为线程安全，仅对单次 Read 加锁。
func serialExclusive(d driver.Driver) bool {
	se, ok := d.(driver.SerialExclusive)
	return ok && se.SerialExclusive()
}

// doPollDevice 采集单台设备本频率分组下的点位（独占串行总线路径）。
// 调用方需持有该设备的 deviceLock（整轮持锁）。
func (g *frequencyGroup) doPollDevice(deviceID string, addrs []po.DeviceAddress) {
	t := g.task

	t.mu.Lock()
	t.lastPollTime = time.Now()
	t.mu.Unlock()

	drv := t.GetDriver(deviceID)
	if drv == nil {
		return // 此设备协议不支持或驱动创建失败
	}
	dev := t.findDevice(deviceID)
	if dev == nil {
		return
	}

	// 检查连接，断开时按指数退避重连（整轮已持设备锁）
	if !t.ensureConnected(drv, dev) {
		t.reportState(dev, false)
		return
	}

	// 通过协议驱动读取本频率分组的点位数据。
	// 分批流式处理：每批读取 → 转换 → 推送，避免整台设备点位一次性累积内存。
	err := forEachBatch(addrs, func(batch []po.DeviceAddress) error {
		results, err := drv.Read(driveradapter.FromPOs(batch))
		if err != nil {
			return err
		}

		// 转换为采集记录
		recs := t.toRecords(dev, batch, results)
		// 点位质量报警（纯内存状态机，先于推送，避免慢下游让它滞后）
		t.reportPointQuality(dev, recs)
		// 点位阈值报警（同样先于推送；只对配了阈值的点位上报）
		t.reportPointValues(dev, batch, recs)
		// 将采集数据交给接收者（推送引擎等），非阻塞
		if len(recs) > 0 && t.sink != nil {
			t.sink.PushRecords(recs)
		}
		return nil
	})
	if err != nil {
		t.logReadError(dev, err)
		t.incError()
		t.reportState(dev, false)
		return
	}

	t.mu.Lock()
	t.lastSuccessTime = time.Now()
	t.consecutiveErr = 0
	if t.lastSuccessAt == nil {
		t.lastSuccessAt = make(map[string]time.Time)
	}
	t.lastSuccessAt[deviceID] = time.Now()
	// 成功即清掉失败记录，让下一次失败能重新记一行（见 logReadError）
	t.clearReadErrLocked(deviceID)
	t.mu.Unlock()

	t.reportState(dev, true)
}

// doPollDeviceConcurrent 采集单台线程安全设备（Modbus TCP / S7）本频率分组下的点位。
// 锁粒度收敛到「重连 + 单次 Read」：
//   - 重连段持锁，与其它频率分组在途的 Read/重连互斥，避免 Connect 关闭旧连接打断在途读取；
//   - 每批仅在 Read 期间持锁，批量间的转换与推送不持锁，
//     同设备其它频率分组的轮询可在本组转换/推送期间并行执行。
//
// 驱动底层连接需线程安全：Modbus TCP 的 tcpTransporter.Send、gos7 的 transporter.Send
// 均对单次请求+响应内部持锁，并发 Read 安全（见 pollDevice 注释）。
func (g *frequencyGroup) doPollDeviceConcurrent(deviceID string, addrs []po.DeviceAddress, dm *sync.Mutex) {
	t := g.task

	t.mu.Lock()
	t.lastPollTime = time.Now()
	t.mu.Unlock()

	drv := t.GetDriver(deviceID)
	if drv == nil {
		return // 此设备协议不支持或驱动创建失败
	}
	dev := t.findDevice(deviceID)
	if dev == nil {
		return
	}

	// 重连段持锁：与其它频率分组在途的 Read/重连互斥
	dm.Lock()
	connected := t.ensureConnected(drv, dev)
	dm.Unlock()
	if !connected {
		t.reportState(dev, false)
		return
	}

	// 分批流式处理：每批仅在 Read 期间持锁，转换与推送不持锁。
	err := forEachBatch(addrs, func(batch []po.DeviceAddress) error {
		dm.Lock()
		results, err := drv.Read(driveradapter.FromPOs(batch))
		dm.Unlock()
		if err != nil {
			return err
		}

		// 转换为采集记录
		recs := t.toRecords(dev, batch, results)
		// 点位质量报警（纯内存状态机，先于推送，避免慢下游让它滞后）
		t.reportPointQuality(dev, recs)
		// 点位阈值报警（同样先于推送；只对配了阈值的点位上报）
		t.reportPointValues(dev, batch, recs)
		// 将采集数据交给接收者（推送引擎等），非阻塞
		if len(recs) > 0 && t.sink != nil {
			t.sink.PushRecords(recs)
		}
		return nil
	})
	if err != nil {
		t.logReadError(dev, err)
		t.incError()
		t.reportState(dev, false)
		return
	}

	t.mu.Lock()
	t.lastSuccessTime = time.Now()
	t.consecutiveErr = 0
	if t.lastSuccessAt == nil {
		t.lastSuccessAt = make(map[string]time.Time)
	}
	t.lastSuccessAt[deviceID] = time.Now()
	// 成功即清掉失败记录，让下一次失败能重新记一行（见 logReadError）
	t.clearReadErrLocked(deviceID)
	t.mu.Unlock()

	t.reportState(dev, true)
}

// ensureConnected 检查设备连接，断开时按指数退避重连。
// 返回 false 表示不应继续本轮采集（重连失败或处于退避窗口内）。
// 调用方需持有该设备的 deviceLock（整轮持锁或重连/单次 Read 临界段）。
func (t *gatewayTask) ensureConnected(drv driver.Driver, dev *po.Device) bool {
	if drv.IsConnected() {
		return true
	}
	if !t.reconnectAllowed(dev.ID) {
		return false // 退避窗口内，跳过本轮，避免每周期都发连接请求
	}
	if err := drv.Connect(dev.ProtocolJSON); err != nil {
		t.recordReconnectFail(dev.ID)
		logger.Error("collector: device %s reconnect failed: %v", dev.Name, err)
		t.incError()
		return false
	}
	t.recordReconnectSuccess(dev.ID)
	t.logReconnect(dev)
	return true
}

// toRecords 将驱动返回的 ReadResult 转换为 CollectorRecord。
//
// 驱动保证返回结果与 addrs 按索引一一对应（各协议驱动重建结果时保持原始点位顺序），
// 名称/说明直接取 addrs[i]，避免每记录两次字符串 map 查询——海量点位下这是主要 CPU 开销
// （profile 实测 toRecords + mapaccess 合计 ~20%）。仅当驱动返回顺序与 addrs 不一致
// （防御异常驱动）时回退到按 DeviceAddressID 的 map 查询。
func (t *gatewayTask) toRecords(dev *po.Device, addrs []po.DeviceAddress, results []driver.ReadResult) []CollectedRecord {
	records := make([]CollectedRecord, 0, len(results))
	protocol := t.protocols[dev.ID]
	for i, r := range results {
		name, label := "", ""
		if i < len(addrs) && addrs[i].ID == r.DeviceAddressID {
			name = addrs[i].Name
			label = addrs[i].Label
		} else {
			// 索引未对齐：回退 map 查询（正常驱动不会走到这里）
			if nameMap, ok := t.addrNameMap[dev.ID]; ok {
				name = nameMap[r.DeviceAddressID]
			}
			if labelMap, ok := t.addrLabelMap[dev.ID]; ok {
				label = labelMap[r.DeviceAddressID]
			}
		}
		records = append(records, CollectedRecord{
			DeviceID:           dev.ID,
			DeviceName:         dev.Name,
			DeviceAddressID:    r.DeviceAddressID,
			DeviceAddressName:  name,
			DeviceAddressLabel: label,
			Value:              r.Value,
			DataType:           r.DataType,
			Kind:               r.Kind,
			Protocol:           protocol,
			Quality:            r.Quality,
		})
	}
	return records
}

// incError 递增错误计数
func (t *gatewayTask) incError() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.errorCount++
	t.consecutiveErr++
}

// reportState 上报单台设备本轮采集成败给在线状态接收者（断联报警）。
// stateSink 为 nil 时为空操作；由采集协程同步调用，实现方需保证轻量不阻塞。
func (t *gatewayTask) reportState(dev *po.Device, ok bool) {
	if t.stateSink == nil {
		return
	}
	t.stateSink.ReportDevicePoll(dev.ID, dev.Name, ok)
}

// reportPointQuality 把一批采集记录的点位质量上报给点位质量接收者（点位质量报警）。
// pointSink 为 nil 时为空操作。
//
// 只报事实（quality/cause），不走任何判定 —— "哪些算故障"是报警策略，归实现方
// （见 collector.PointStateSink 与 alarm.PointEngine）。这里刻意不预先过滤，
// 因为**过滤掉"看起来正常"的点位会让状态机永远无法恢复**：恢复信号就是那个 192。
//
// 本函数在采集协程里按点位逐个调用（海量点位下是热点路径），故只做取值与转发。
func (t *gatewayTask) reportPointQuality(dev *po.Device, recs []CollectedRecord) {
	if t.pointSink == nil {
		return
	}
	for i := range recs {
		r := &recs[i]
		// 点位名优先用中文说明（温度、电流），没有才退回协议地址（%MW100）——
		// 报警文案是给人看的，地址串只有排障时才有意义。
		name := r.DeviceAddressLabel
		if name == "" {
			name = r.DeviceAddressName
		}
		t.pointSink.ReportPointQuality(dev.ID, dev.Name, r.DeviceAddressID, name, r.Quality)
	}
}

// reportPointValues 把一批采集记录里**配了阈值**的点位交给点位阈值接收者（点位阈值报警）。
// pointValueSink 为 nil 时为空操作。
//
// 与 reportPointQuality 的关键差别：这里**跳过配置为空（未启用 / 两侧都没配）的点位**。
// 这不是替实现方做报警策略 —— 一个没配阈值的点位没有任何状态需要维护，发信号纯属浪费
// （本函数在采集协程里按点位逐个调用）。真正"该不该算越限"的判定仍在实现方。
//
// 配置与记录的对应靠 addrs（与结果同序，与 toRecords 同一套对齐假设）：阈值三列就搭在
// 采集侧手里那份 po.DeviceAddress 上，因此阈值不需要第二份缓存，也不会与采集侧漂移。
// 索引未对齐（防御异常驱动）时放弃本轮阈值上报而不是猜归属。
//
// 注意与 reportPointQuality 的另一处不同：这里没有"必须上报正常值否则状态机无法恢复"
// 的顾虑 —— 配置为空时采集侧本就不再发任何信号，状态机原地冻结，恢复靠运维改配置时
// 显式清除活跃报警（见 DeviceAddressService.UpdateDeviceAddress）。
func (t *gatewayTask) reportPointValues(dev *po.Device, addrs []po.DeviceAddress, recs []CollectedRecord) {
	if t.pointValueSink == nil {
		return
	}
	for i := range recs {
		if i >= len(addrs) || addrs[i].ID != recs[i].DeviceAddressID {
			continue
		}
		a := &addrs[i]
		// 廉价的预筛：绝大多数点位没开报警条件，两个字段比较就能跳过，
		// 从而**不必**为它们付下面那次 JSON 解析。
		if a.ThresholdEnabled != 1 || a.ThresholdOp == "" {
			continue
		}
		th := PointThreshold{Enabled: true, Op: a.ThresholdOp, Values: parseThresholdValues(a.ThresholdValue, a.ID)}
		if th.Inert() {
			continue
		}
		r := &recs[i]
		// 点位名优先用中文说明（温度、电流），没有才退回协议地址 —— 同 reportPointQuality。
		name := r.DeviceAddressLabel
		if name == "" {
			name = r.DeviceAddressName
		}
		t.pointValueSink.ReportPointValue(dev.ID, dev.Name, r.DeviceAddressID, name,
			r.Value, r.Kind, r.Quality, th)
	}
}

// parseThresholdValues 解析 device_address.threshold_value（操作数的 JSON 数组）。
//
// 放在这里（而不是建任务时预解析）是刻意的取舍：解析只对**开了报警条件**的点位发生
// （上面的预筛已滤掉其余），而小数组的 json.Unmarshal 是微秒级；换成建任务时预解析
// 就得再维护一份 addressID -> []string 的映射，在采集热路径上多一次 map 查询
// （toRecords 的注释里记着 mapaccess 是这台网关的主要 CPU 开销之一）。
//
// 解析失败返回 nil（→ Inert → 不上报）：坏配置不该让采集出错，也不该猜一个条件出来。
func parseThresholdValues(raw, addressID string) []string {
	if raw == "" {
		return nil
	}
	var vals []string
	if err := json.Unmarshal([]byte(raw), &vals); err != nil {
		logger.Warn("collector: address %s threshold_value is not a JSON array, alarm disabled for it: %v", addressID, err)
		return nil
	}
	return vals
}
