// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package devicescan

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Ellisata/iot-gateway-sdk/driver"
	"github.com/Ellisata/iot-gateway-sdk/driversdk"
	"iot-gateway/configFile"
	"iot-gateway/logger"
	"iot-gateway/runtimecfg"
)

// 规划期的错误。服务层逐一映射成业务错误码 —— 分开是为了让用户看到
// "该协议不支持扫描"而不是笼统的"参数错误"。
var (
	// ErrDisabled 扫描功能被配置关闭（deviceScan.enabled=false）。
	ErrDisabled = errors.New("devicescan: 局域网扫描功能未启用")
	// ErrBusy 已有扫描任务在运行。
	//
	// 刻意只允许一个任务：同一时刻扫两个网段/两个协议，除了把网关与局域网
	// 的并发压力翻倍之外没有别的效果，而它们本来也要排在同一个 worker 池上。
	// 前端据此提示"先等当前扫描结束或取消它"。
	ErrBusy = errors.New("devicescan: 已有扫描任务在运行")
	// ErrJobNotFound 任务不存在，或已过保留期被清理。
	ErrJobNotFound = errors.New("devicescan: 扫描任务不存在或已过期")
	// ErrUnsupportedProtocol 该协议没有声明扫描能力。
	ErrUnsupportedProtocol = errors.New("devicescan: 该协议不支持局域网扫描")
	// ErrNoTargets 没有可扫的目标（本机没有可用网段，或选中的网段展开为空）。
	ErrNoTargets = errors.New("devicescan: 没有可扫描的目标")
	// ErrInvalidPort 端口越界。
	ErrInvalidPort = errors.New("devicescan: 端口非法")
	// ErrUnknownSubnet 指定的网段不在本机网段列表里。
	ErrUnknownSubnet = errors.New("devicescan: 指定的网段不是本机网段")
)

// 默认值与上限。
//
// 与 default.yaml 的 deviceScan 段保持一致（两处默认值必须人工对齐，
// 与 pluginhost/manager.go 里 defaultPluginDir 那组常量是同一个约定）。
// <=0 一律视为「未配置 → 用默认值」而不是「禁用」。
const (
	defaultMaxConcurrentProbes = 8
	maxMaxConcurrentProbes     = 32

	defaultMaxTargets = 1024
	maxMaxTargets     = 65536

	defaultJobTTL = 10 * time.Minute

	// stopWait 停机时等待在飞探测收尾的上限。
	// 与 pluginhost 的 pluginShutdownTimeout 同一取舍：取消之后各个探测会立刻
	// 从 client.call 的 ctx.Done 分支返回，正常情况下是毫秒级，留这个上限只是
	// 为了让一个卡住的插件不至于拖住整个网关的停机。
	stopWait = 2 * time.Second
)

// SpecSource 提供「哪些协议可以扫描、各自的规格是什么」。
// 生产实现是 *pluginhost.Manager。
type SpecSource interface {
	DiscoverySpecs() []driver.ProtocolDiscovery
}

// Prober 把一个目标交给提供该协议的插件进程探测。
// 生产实现是 *pluginhost.Manager。
type Prober interface {
	ProbeTargets(ctx context.Context, protocol string, options json.RawMessage,
		target driver.ScanTarget) (*driversdk.ProbeReply, error)
}

// SubmitRequest 发起一次扫描的输入。
type SubmitRequest struct {
	// Protocol 要使用的协议名（必须是声明了扫描能力的协议之一）。
	Protocol string
	// Options 扫描选项原文，形状由该协议的 DiscoverySpec.FormJSON 定义，
	// 原样透给插件。
	Options json.RawMessage
	// Subnets 要扫描的网段（SubnetInfo.CIDR）。空 = 全部本机网段。
	Subnets []string
	// Port 覆盖协议的 DefaultPort。0 = 用协议的默认端口。
	Port int
}

// Engine 扫描任务引擎。
//
// **没有 Start()**：与 alarm/discovery 那两个引擎不同，本引擎在没有人发起扫描
// 之前不持有任何资源（不起 goroutine、不绑端口），因此没有需要启动的东西。
// 任务过期的清理也是**惰性**的（在 Submit/Status 时判一次），这样既不需要
// 一个 reaper goroutine，也不需要把它的生命周期接到 main.go 的停机顺序上。
// 代价是过期任务在被下一次请求碰到之前仍占着内存 —— 最多一个任务的结果，
// 可以忽略。
//
// 只需 Stop()：停机时必须取消在跑的任务，否则在飞探测会继续往一个正在退役的
// 插件进程的管道里写帧。
type Engine struct {
	enabled       bool
	maxConcurrent int
	maxTargets    int
	ttl           time.Duration

	// policy 非 nil 时，enabled 走运行期配置（可在「运行开关」页动态切换）。
	// 其余三个数值仍来自启动期配置 —— 它们不是开关，改动需重启。
	policy runtimecfg.Reader

	specs  SpecSource
	prober Prober

	// subnets 与 now 是测试缝，生产路径分别取本机网段与系统时钟。
	// 形如 discovery.Announcer 的 targets/listen 那一对。
	subnets func() []SubnetInfo
	now     func() time.Time

	mu  sync.Mutex
	job *job
	seq atomic.Uint64

	stopOnce sync.Once
	stopped  bool
}

// NewEngine 创建扫描引擎。cfg 为 nil 时全部走默认值（测试与最小装配用）。
func NewEngine(cfg *configFile.Config, specs SpecSource, prober Prober) *Engine {
	return NewEngineWithPolicy(cfg, specs, prober, nil)
}

// NewEngineWithPolicy 同 NewEngine，但 enabled 从运行期配置读取。
// policy 为 nil 时行为与 NewEngine 完全一致（读 cfg 快照）。
func NewEngineWithPolicy(cfg *configFile.Config, specs SpecSource, prober Prober,
	policy runtimecfg.Reader) *Engine {
	e := &Engine{
		enabled:       true,
		maxConcurrent: defaultMaxConcurrentProbes,
		maxTargets:    defaultMaxTargets,
		ttl:           defaultJobTTL,
		specs:         specs,
		prober:        prober,
		policy:        policy,
		subnets:       localSubnets,
		now:           time.Now,
	}
	if cfg != nil {
		dc := cfg.DeviceScan
		e.enabled = dc.Enabled
		e.maxConcurrent = clampInt(dc.MaxConcurrentProbes, defaultMaxConcurrentProbes, 1, maxMaxConcurrentProbes)
		e.maxTargets = clampInt(dc.MaxTargets, defaultMaxTargets, 1, maxMaxTargets)
		if dc.JobTTLMinutes > 0 {
			e.ttl = time.Duration(dc.JobTTLMinutes) * time.Minute
		}
	}
	return e
}

// clampInt 把配置值夹进 [lo,hi]；未配置（<=0）时取默认值。
//
// 静默夹取而不是报错：这几个值是性能旋钮，配成 0 或配成 100000 都不会产生
// 错误的结果，只是快慢而已 —— 为它们在启动期中断网关不值得。越界时记一条
// 日志，让"我改了配置怎么没效果"有个线索。
func clampInt(v, def, lo, hi int) int {
	if v <= 0 {
		return def
	}
	if v < lo {
		logger.Warn("devicescan: 配置值 %d 小于下限 %d，已按 %d 处理", v, lo, lo)
		return lo
	}
	if v > hi {
		logger.Warn("devicescan: 配置值 %d 超过上限 %d，已按 %d 处理", v, hi, hi)
		return hi
	}
	return v
}

// WithSubnetSource 覆盖网段来源，返回自身以便链式调用。
//
// 生产路径用本机网卡枚举（discovery.LocalSubnets）。留这个口子有两个用途：
//   - 跨包测试需要**确定性**的网段（本机有哪些网卡因机器而异，
//     拿真机网段写断言必然在 CI 上飘）；
//   - 将来若要支持"手工指定一个网段去扫"，改的就是这一处。
func (e *Engine) WithSubnetSource(fn func() []SubnetInfo) *Engine {
	if fn != nil {
		e.subnets = fn
	}
	return e
}

// Enabled 报告扫描功能是否启用。
//
// 每次现读运行期配置：关闭只拦**新**扫描，正在跑的任务不受影响（它已经
// 规划好了目标，半途取消会让用户看到一份不完整的结果却不知道为什么）。
func (e *Engine) Enabled() bool {
	if e.policy != nil {
		return e.policy.Bool(runtimecfg.KeyDeviceScanEnabled)
	}
	return e.enabled
}

// Subnets 返回本机可扫描的网段（供前端网段多选）。
func (e *Engine) Subnets() []SubnetInfo { return e.subnets() }

// Specs 返回支持扫描的协议及其规格（供前端协议下拉）。
//
// 每次现算、透传底层实现：插件热升级会换掉 Meta，缓存一份就会陈旧
// （详见 pluginhost.Manager.DiscoverySpecs 的说明）。
func (e *Engine) Specs() []driver.ProtocolDiscovery {
	if e.specs == nil {
		return nil
	}
	return e.specs.DiscoverySpecs()
}

// MaxTargets 单次扫描的目标数上限（前端用它提前提示"这个网段会被截断"）。
func (e *Engine) MaxTargets() int { return e.maxTargets }

// Submit 发起一次扫描，**登记任务即返回**，不等扫描结束。
//
// 调用方（HTTP handler）绝不能同步等结果：http.Server.Shutdown 不会杀掉在飞的
// handler，一个几十秒的扫描会把停机拖到超时。
//
// 已有终态任务时会被直接替换 —— 用户主动点了"开始扫描"，就是不要上一轮的结果了。
// 仍在运行的任务则返回 ErrBusy（见该错误的说明）。
func (e *Engine) Submit(req SubmitRequest) (*Snapshot, error) {
	if !e.Enabled() {
		return nil, ErrDisabled
	}

	e.mu.Lock()
	defer e.mu.Unlock()

	if e.stopped {
		return nil, ErrDisabled
	}
	if e.job != nil && !e.job.terminal() {
		return nil, ErrBusy
	}

	now := e.now()
	j, err := e.plan(req, now)
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithCancel(context.Background())
	j.ctx, j.cancel = ctx, cancel
	j.doneCh = make(chan struct{})
	j.state = JobRunning
	e.job = j

	go e.run(j)
	return j.snapshot(now, true), nil
}

// plan 规划一次任务：校验协议、定端口、选网段、展开目标。
//
// 规划期的错误在**发起时**就返回给用户，而不是变成一条任务失败 ——
// "网段选错了"这类问题应当在点下按钮的那一刻就说清楚。
func (e *Engine) plan(req SubmitRequest, now time.Time) (*job, error) {
	spec, ok := e.specOf(req.Protocol)
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrUnsupportedProtocol, req.Protocol)
	}

	port := req.Port
	if port == 0 {
		port = spec.DefaultPort
	}
	if port < 1 || port > 65535 {
		return nil, fmt.Errorf("%w: %d", ErrInvalidPort, port)
	}

	selected, err := selectSubnets(e.subnets(), req.Subnets)
	if err != nil {
		return nil, err
	}
	// 目标的形状由插件的 TargetMode 决定：广播型协议（EtherNet/IP 的 ListIdentity、
	// FINS/UDP 的节点查询）要的是各网段的子网广播地址，不是两百多个主机地址。
	// 见 expandTargets 的说明 —— 这个字段曾经被声明、被校验、却没有被使用。
	targets, requested, truncated := expandTargets(selected, port, e.maxTargets, spec.TargetMode)
	if len(targets) == 0 {
		return nil, ErrNoTargets
	}

	// 最坏耗时的上界：全部目标走满插件的单目标预算，且并发度用满。
	estimate := requested
	if truncated {
		estimate = len(targets)
	}
	estimateMs := 0
	if e.maxConcurrent > 0 {
		batches := (estimate + e.maxConcurrent - 1) / e.maxConcurrent
		estimateMs = batches * spec.MaxBudgetMs
	}

	return &job{
		id:            "scan-" + strconv.FormatUint(e.seq.Add(1), 10),
		protocol:      req.Protocol,
		options:       req.Options,
		port:          port,
		requested:     requested,
		planned:       len(targets),
		truncated:     truncated,
		targets:       targets,
		startedAt:     now,
		estimateMaxMs: estimateMs,
		seen:          make(map[string]struct{}, 16),
	}, nil
}

// specOf 在插件声明的能力里查这个协议。
func (e *Engine) specOf(protocol string) (driver.DiscoverySpec, bool) {
	if e.specs == nil {
		return driver.DiscoverySpec{}, false
	}
	for _, pd := range e.specs.DiscoverySpecs() {
		if pd.Protocol == protocol {
			return pd.Spec, true
		}
	}
	return driver.DiscoverySpec{}, false
}

// run 执行一次扫描：并发探测全部目标，等它们收尾后置终态。
//
// 并发度由 maxConcurrent 个信号量槽位控制，**不复用 workerPool.WorkerPool**：
// 那个池是网关全局单例、与采集共用（扫描会跟采集抢 worker），它的 Submit 不带
// ctx（任务无法取消），且 Stop 会等所有排队任务跑完（一个上千目标的扫描会直接
// 挂住停机）。
func (e *Engine) run(j *job) {
	defer close(j.doneCh)

	sem := make(chan struct{}, e.maxConcurrent)
	var wg sync.WaitGroup

dispatch:
	for i := range j.targets {
		// 提交前先看取消：取消之后不该再往插件里灌新的探测。
		select {
		case <-j.ctx.Done():
			break dispatch
		case sem <- struct{}{}:
		}

		wg.Add(1)
		go func(t driver.ScanTarget) {
			defer wg.Done()
			defer func() { <-sem }()
			e.probeOne(j, t)
		}(j.targets[i])
	}

	wg.Wait()
	e.finish(j)
}

// probeOne 探测一个目标，把结果并进任务。
func (e *Engine) probeOne(j *job, target driver.ScanTarget) {
	reply, err := e.prober.ProbeTargets(j.ctx, j.protocol, j.options, target)

	e.mu.Lock()
	defer e.mu.Unlock()

	// 取消导致的返回既不计进度也不计错误：那个目标**没有被扫过**，
	// 把它算进 scanned 会让进度条假装扫完了，把它的错误报给用户更是误导
	//（用户已经知道自己点了取消）。
	if j.ctx.Err() != nil {
		return
	}
	j.scanned++

	if err != nil {
		j.errors = append(j.errors, TargetError{Host: target.Host, Reason: err.Error()})
		return
	}
	if reply == nil {
		return
	}
	for _, dev := range reply.Devices {
		if !j.acceptDevice(dev) {
			continue
		}
		j.devices = append(j.devices, dev)
	}
	for _, pe := range reply.Errors {
		host := pe.Host
		if host == "" {
			host = target.Host
		}
		j.errors = append(j.errors, TargetError{Host: host, Reason: pe.Reason})
	}
}

// finish 把任务置为正常结束。
//
// 若任务已被 Cancel/Stop 置成终态，这里什么都不做 —— 终态只能被设置一次，
// 否则用户点取消之后又看到"完成"，会以为取消没生效。
func (e *Engine) finish(j *job) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if j.terminal() {
		return
	}
	j.state = JobDone
	j.endedAt = e.now()
}

// Status 返回任务快照，含已发现的设备与探测失败清单。
// 任务不存在、或已过保留期，返回 ErrJobNotFound（前端据此停止轮询并提示）。
func (e *Engine) Status(jobID string) (*Snapshot, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.statusLocked(jobID, e.now(), true)
}

// Progress 返回**不带结果清单**的任务快照：状态、进度、计数、耗时。
//
// 轮询用（前端的轮询走这里）。两条理由：
//   - 传输量：devices 是只增的，一个扫到几百台的任务每 800ms 回传整份清单，
//     而绝大多数拍子并没有新设备；
//   - 锁竞争：带清单的快照必须在 Engine.mu 下浅拷贝整个切片，而**这把锁正是
//     探测 worker 追加结果用的那把** —— 每拍拷一次，就是在跟探测抢锁。
//
// 计数（Found/Scanned）与 Status 完全同源（都是 len(j.devices) / j.scanned），
// 前端据 Found 变没变决定要不要补一次 Status。两份计数一旦各算一套，
// 补请求的时机就会漏 —— 漏了的表现是"扫到的东西一直不出现"。
func (e *Engine) Progress(jobID string) (*Snapshot, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.statusLocked(jobID, e.now(), false)
}

func (e *Engine) statusLocked(jobID string, now time.Time, includeResults bool) (*Snapshot, error) {
	if e.job == nil || e.job.id != jobID {
		return nil, ErrJobNotFound
	}
	// 惰性过期：没有 reaper goroutine，过期判定放在每次访问时做。
	// 判完即丢，下一个任务请求自然把它挤掉。
	if e.job.expired(now, e.ttl) {
		e.job = nil
		return nil, ErrJobNotFound
	}
	return e.job.snapshot(now, includeResults), nil
}

// Cancel 取消任务。已结束的任务取消是幂等的成功（用户的意图已达成）。
func (e *Engine) Cancel(jobID string) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	if e.job == nil || e.job.id != jobID {
		return ErrJobNotFound
	}
	if e.job.terminal() {
		return nil
	}
	e.markCanceledLocked(e.job, "已取消")
	return nil
}

// markCanceledLocked 把任务置为已取消并掐掉在飞探测。调用方必须持有 e.mu。
func (e *Engine) markCanceledLocked(j *job, reason string) {
	j.state = JobCanceled
	j.reason = reason
	j.endedAt = e.now()
	if j.cancel != nil {
		j.cancel()
	}
}

// Stop 停机：取消在跑的任务并等待 worker 收尾（有上限）。
//
// 必须在 DriverManager.Stop() **之前**执行（main.go 里 defer 的注册顺序决定了
// 这一点）：否则在飞探测会继续往一个正在退役的插件进程的管道里写帧。
func (e *Engine) Stop() {
	e.stopOnce.Do(func() {
		e.mu.Lock()
		// 先置停机标记：此后到达的 Submit 一律拒绝，不会再产生新任务。
		e.stopped = true
		var wait <-chan struct{}
		if j := e.job; j != nil {
			if !j.terminal() {
				e.markCanceledLocked(j, "网关停机")
			}
			wait = j.doneCh
		}
		e.mu.Unlock()

		if wait == nil {
			return
		}
		select {
		case <-wait:
		case <-time.After(stopWait):
			logger.Warn("devicescan: 停止超时（%s），仍有探测在途；它们的应答将被丢弃", stopWait)
		}
	})
}
