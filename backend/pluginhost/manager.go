// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package pluginhost

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"gorm.io/gorm"

	"iot-gateway/configFile"
	"iot-gateway/driver"
	"iot-gateway/driversdk"
	"iot-gateway/logger"
)

// Reloader 触发采集配置热刷新。*collector.Engine 满足此接口
// （Refresh 与 IsRunning 见 collector/engine.go）。
//
// 抽成接口而非直接依赖 *collector.Engine，是为了让 Manager 的测试能注入
// 假实现断言"防抖合并"与"启动期不刷新"，同时避免包级强耦合。
type Reloader interface {
	Refresh() error
	IsRunning() bool
}

// refreshDebounce 驱动集变化后的刷新防抖窗口。
//
// 必须防抖而非立即刷新：Refresh 会重建整个采集任务（停掉全部驱动、
// 重开那些独占串行总线的插件串口）。一批插件文件被同时放入时，
// 逐次刷新会造成多次采集中断。
const refreshDebounce = 2 * time.Second

// 默认配置值，与 default.yaml 的 plugin 段保持一致。
const (
	defaultPluginDir      = "plugins"
	defaultPluginGlob     = "iot-driver-*"
	defaultScanInterval   = 10 * time.Second
	defaultMaxRestarts    = 10
	defaultStableAfter    = 60 * time.Second
	defaultPluginRowSort  = 100 // 插件自动建立的协议行排在内置播种行之后
	dirPermissionMask     = 0o002
	pluginShutdownTimeout = 2 * time.Second
)

// loadedPlugin 一个已加载的插件。
//
// 持有 link 而非 *pluginProcess：热升级要把驱动实例从旧进程挪到新进程，而
// 驱动实例是在 driver.Create 时由工厂闭包捕获 link 建出来的（见 pluginLink）。
type loadedPlugin struct {
	path string
	meta driversdk.Meta
	link *pluginLink
	sha  string

	// fp 加载（或上次成功热升级）时记下的文件指纹。只由 scanOnce 写入
	//（该路径由 scanMu 串行化），不在别处读，因此不需要额外同步。
	fp fileFingerprint

	// warnedChange 仅在 plugin.disableHotUpgrade=true 时使用：二进制被替换后
	// 只警告一次，避免每轮扫描刷屏。同样在 Manager.mu 下读写。
	warnedChange bool

	// 以下热升级审计字段均在 Manager.mu 下读写。
	previousSHA    string    // 被替换掉的上一版 sha256
	upgradedAt     time.Time // 最近一次成功升级的时间；零值表示从未升级
	upgradeCount   int       // 累计成功升级次数
	lastUpgradeErr string    // 最近一次失败原因；失败时旧版本仍在运行
}

// Manager 外部协议驱动管理器：目录扫描、增量加载/卸载、协议行供给、
// 以及驱动集变化时触发采集配置热刷新。
type Manager struct {
	db     *gorm.DB
	cfg    configFile.PluginConfig
	reload Reloader

	baseDir string
	opts    processOptions
	scan    time.Duration

	// scanMu 串行化 scanOnce。Reload() 已被注释预告「供后续管理接口使用」，
	// 一旦真有并发调用，没有它就从「重复空操作」升级为「双重热升级」。
	// 锁序只允许 scanMu → mu，不得反向嵌套（见 Stop）。
	scanMu sync.Mutex

	mu      sync.Mutex
	plugins map[string]*loadedPlugin
	// failed 记录失败的文件指纹：指纹未变则不再重试，避免每轮扫描刷屏。
	// 加载失败与**热升级失败**共用它 —— 后者失败时旧版本仍在运行，
	// 记录新二进制指纹是为了不再对它反复拉起进程 + 等满握手超时。
	failed map[string]fileFingerprint

	started  bool
	stopped  atomic.Bool
	stopCh   chan struct{}
	doneCh   chan struct{}
	stopOnce sync.Once

	refreshMu sync.Mutex
	// refreshTimer 非 nil 表示已有一轮刷新在等待，后续变化并入该轮。
	refreshTimer *time.Timer
	// refreshDelay 防抖窗口，默认 refreshDebounce；测试可缩短。
	refreshDelay time.Duration
}

// fileFingerprint 文件指纹。
//
// 用 (大小, 修改时间) 而非内容哈希：它只需要做「是否变化」的信号，
// 每 10s 对每个插件算一次 SHA-256 是纯浪费；SHA-256 只在加载时算一次，
// 用于审计日志与 Status。
type fileFingerprint struct {
	size    int64
	modTime int64
}

func (f fileFingerprint) equal(o fileFingerprint) bool {
	return f.size == o.size && f.modTime == o.modTime
}

// NewManager 创建外部协议驱动管理器（未启动）。
func NewManager(db *gorm.DB, cfg *configFile.Config, reload Reloader) *Manager {
	dc := cfg.Plugin
	m := &Manager{
		db:      db,
		cfg:     dc,
		reload:  reload,
		baseDir: "",
		plugins: make(map[string]*loadedPlugin),
		failed:  make(map[string]fileFingerprint),
		stopCh:  make(chan struct{}),
		doneCh:  make(chan struct{}),
		scan:    defaultScanInterval,

		refreshDelay: refreshDebounce,
	}

	if m.cfg.Dir == "" {
		m.cfg.Dir = defaultPluginDir
	}
	if m.cfg.Glob == "" {
		m.cfg.Glob = defaultPluginGlob
	}
	if m.cfg.ScanIntervalMs > 0 {
		m.scan = time.Duration(m.cfg.ScanIntervalMs) * time.Millisecond
	}

	m.opts = processOptions{
		Timeouts: Timeouts{
			Handshake: msOrDefault(dc.HandshakeTimeoutMs, 10*time.Second),
			Connect:   msOrDefault(dc.ConnectTimeoutMs, 30*time.Second),
			Ping:      msOrDefault(dc.ConnectTimeoutMs, 30*time.Second),
			Read:      msOrDefault(dc.ReadTimeoutMs, 60*time.Second),
			Close:     2 * time.Second,
		},
		Watchdog:    5 * time.Second,
		RestartMin:  msOrDefault(dc.RestartMinDelayMs, time.Second),
		RestartMax:  msOrDefault(dc.RestartMaxDelayMs, 30*time.Second),
		MaxRestarts: maxRestartsOrDefault(dc.MaxRestarts),
		StableAfter: defaultStableAfter,
		KillGrace:   pluginShutdownTimeout,
		Infof:       logger.Info,
		Warnf:       logger.Warn,
	}

	return m
}

func msOrDefault(ms int, def time.Duration) time.Duration {
	if ms > 0 {
		return time.Duration(ms) * time.Millisecond
	}
	return def
}

func maxRestartsOrDefault(n int) int {
	if n > 0 {
		return n
	}
	return defaultMaxRestarts
}

// Start 启动管理器：解析目录、做安全检查、首次扫描，然后进入周期扫描。
//
// 必须在采集引擎启动**之前**调用：驱动注册要先于 collector 建任务，
// 否则使用插件协议的设备在首轮会被判为「协议不支持」而跳过
// （这一跳过是非致命的，见 collector.newGatewayTask）。
func (m *Manager) Start() error {
	if !m.cfg.Enabled {
		// 全部协议驱动都已搬成插件，关掉这一项等于网关不支持任何协议 ——
		// 设备会被逐个跳过，而症状只有一行日志。所以目录里躺着一堆插件却
		// 忘了开开关，必须被喊出来，不能像以前那样记一条 INFO 了事。
		if dir, err := resolvePluginDir(m.cfg.Dir); err == nil {
			if n := m.countCandidatePlugins(dir); n > 0 {
				logger.Warn("pluginhost: 外部协议插件未启用（plugin.enabled=false），"+
					"但目录 %s 里有 %d 个匹配 %s 的文件 —— 网关将不支持任何协议，"+
					"所有设备都会被跳过。启用后重启即可。", dir, n, m.cfg.Glob)
				return nil
			}
		}
		logger.Warn("pluginhost: 外部协议插件未启用（plugin.enabled=false）且目录 %s 里没有插件，"+
			"网关将不支持任何协议（协议驱动全部由插件提供，主程序已不含内置实现）", m.cfg.Dir)
		return nil
	}

	dir, err := resolvePluginDir(m.cfg.Dir)
	if err != nil {
		return err
	}
	if err := m.checkDirSecurity(dir); err != nil {
		return err
	}
	m.baseDir = dir

	m.started = true
	m.stopped.Store(false)
	m.scanOnce()

	go m.loop()
	logger.Info("pluginhost: 外部协议插件已启用，目录 %s（匹配 %s，扫描间隔 %s）", dir, m.cfg.Glob, m.scan)

	// 零协议是**能启动但什么都不做**的状态，比启动失败更难发现：
	// 网关照常监听、前端照常能建设备，只是每台设备都在采集时被静默跳过。
	// 这里必须显式喊一声，并给出可直接照做的补救。
	if protocols := driver.Registered(); len(protocols) == 0 {
		logger.Warn("pluginhost: 目录 %s 里没有成功加载任何协议驱动，网关将不支持任何设备采集。"+
			"请确认 plugins/ 下存在 iot-driver-* 可执行文件（发行包中随附），"+
			"且其平台与网关一致。", dir)
	} else {
		logger.Info("pluginhost: 已加载 %d 个协议：%s", len(protocols), strings.Join(protocols, ", "))
	}
	return nil
}

// countCandidatePlugins 数一下目录里有多少个匹配 glob 的候选文件。
//
// 只用于「开关关着但插件在场」的告警，因此刻意不复用扫描路径的全部检查
// （安全检查、符号链接解析等）：那些是加载才需要的前提，而这里问的是
// 「用户是不是把一包插件放进去了却没启用」。
func (m *Manager) countCandidatePlugins(dir string) int {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0
	}
	n := 0
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if ok, err := filepath.Match(m.cfg.Glob, e.Name()); err == nil && ok {
			n++
		}
	}
	return n
}

// Stop 停止扫描、卸载全部插件并回收进程。
//
// 调用时序上它必须晚于 CollectorEngine.Stop()：那时采集已完全停止
// （驱动已 Close、无 Read 在途），杀插件进程才安全。
func (m *Manager) Stop() {
	m.stopOnce.Do(func() {
		// 停机位置位后，迟到的 Reload() 不会再拉起任何进程。
		m.stopped.Store(true)
		if !m.started {
			close(m.doneCh)
			return
		}
		close(m.stopCh)
		<-m.doneCh

		// 与一次在途的 Reload() 驱动的 scanOnce 会合：loop 已退出不代表
		// Reload 触发的扫描已结束，而那次扫描可能正在热升级途中拉起进程。
		// 必须**先**取得扫描锁再动 m.mu，保持 scanMu → mu 的锁序。
		m.withScanLock(func() {})

		m.refreshMu.Lock()
		if m.refreshTimer != nil {
			m.refreshTimer.Stop()
			m.refreshTimer = nil
		}
		m.refreshMu.Unlock()

		m.mu.Lock()
		plugins := make([]*loadedPlugin, 0, len(m.plugins))
		for _, lp := range m.plugins {
			plugins = append(plugins, lp)
		}
		m.plugins = make(map[string]*loadedPlugin)
		m.mu.Unlock()

		for _, lp := range plugins {
			m.unregister(lp)
			lp.link.retireCurrent()
		}
		logger.Info("pluginhost: 已停止，卸载 %d 个插件", len(plugins))
	})
}

// Reload 立即执行一轮扫描（供测试与后续管理接口使用）。
//
// 同步执行，因此**继承 scanOnce 的全部耗时**：一次热升级最坏约
// Handshake+2s（拉起新进程并等握手）+ 2×KillGrace（退役旧进程）。
// 后续若接管理接口，不可挂在同步 HTTP handler 上，应由后台任务驱动。
func (m *Manager) Reload() {
	if m.baseDir == "" {
		return
	}
	m.scanOnce()
}

// PluginStatus 一个插件的诊断信息。
type PluginStatus struct {
	Path string `json:"path"`
	// Protocols 本插件提供的全部协议名（一个插件可有多个）。
	Protocols []string `json:"protocols"`
	Scope     string   `json:"scope"`
	Version   string   `json:"version"`
	SHA256    string   `json:"sha256"`
	// Up 当前世代是否在服务（热升级期间恒为 true —— 新旧实例交接无空档）。
	Up bool `json:"up"`
	// Epoch 当前进程实例的世代号。**跨热升级严格单调**，每次成功升级 +1，
	// 崩溃重启同样递增；可用来确认一次升级是否真的换了进程。
	Epoch uint64 `json:"epoch"`

	// Upgrades 累计成功热升级次数；UpgradedAt 为最近一次成功升级时间
	//（从未升级为空串）。PreviousSHA256 是被替换掉的上一版，供审计。
	Upgrades       int    `json:"upgrades"`
	UpgradedAt     string `json:"upgradedAt"`
	PreviousSHA256 string `json:"previousSha256"`
	// LastUpgradeError 最近一次热升级失败的原因。
	//
	// 这一项最要紧：失败时旧版本仍在运行、Up 仍为 true，若只看 Up 会以为
	// 磁盘上的新版本已经生效。非空即表示「磁盘上的新二进制没能上线」。
	LastUpgradeError string `json:"lastUpgradeError"`
}

// Status 返回当前已加载插件的诊断快照。
func (m *Manager) Status() []PluginStatus {
	m.mu.Lock()
	defer m.mu.Unlock()

	out := make([]PluginStatus, 0, len(m.plugins))
	for _, lp := range m.plugins {
		st := PluginStatus{
			Path:             lp.path,
			Protocols:        protocolNameList(lp.meta),
			Scope:            lp.meta.Scope,
			Version:          lp.meta.Version,
			SHA256:           lp.sha,
			Up:               lp.link.isUp(),
			Epoch:            lp.link.epoch(),
			Upgrades:         lp.upgradeCount,
			PreviousSHA256:   lp.previousSHA,
			LastUpgradeError: lp.lastUpgradeErr,
		}
		if !lp.upgradedAt.IsZero() {
			st.UpgradedAt = lp.upgradedAt.Format("2006-01-02 15:04:05")
		}
		out = append(out, st)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}

// resolvePluginDir 把插件目录解析为绝对路径。
//
// 空或相对路径一律相对 os.Executable() 所在目录，而不是 cwd：
//   - Windows 服务用 sc create 注册，除 binPath 外没有工作目录概念，
//     SCM 给服务进程的 cwd 是 System32（见 install.bat 的 sc create 行），
//     仅靠 service_windows.go 早期的一次 os.Chdir 不足以作为路径解析依据；
//   - systemd 单元虽有 WorkingDirectory，但「前台调试」与「服务运行」
//     必须解析到同一位置。
//
// 绝对路径原样返回。目录不存在不算错误（视为无插件），由调用方处理。
func resolvePluginDir(dir string) (string, error) {
	if dir == "" {
		dir = defaultPluginDir
	}
	if filepath.IsAbs(dir) {
		return filepath.Clean(dir), nil
	}
	exe, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("pluginhost: 解析可执行文件路径: %w", err)
	}
	return filepath.Join(filepath.Dir(exe), dir), nil
}

// checkDirSecurity 校验插件目录权限。
//
// 加载可执行文件等于以网关的 OS 权限执行任意代码，因此目录必须只对管理员可写。
func (m *Manager) checkDirSecurity(dir string) error {
	info, err := os.Stat(dir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			// 启动期不自动创建：避免"网关静默创建一个可执行目录"。
			//
			// 唯一的例外是管理端的插件上传（见 install.go 的 ensurePluginDir）：
			// 那是管理员的一次显式动作，不创建反而让"点了按钮却没反应"。
			logger.Warn("pluginhost: 插件目录 %s 不存在，视为无插件（不会自动创建）", dir)
			return nil
		}
		return fmt.Errorf("pluginhost: 检查插件目录: %w", err)
	}
	if !info.IsDir() {
		return fmt.Errorf("pluginhost: %s 不是目录", dir)
	}

	if runtime.GOOS == "windows" {
		// Windows 上 os.FileMode 的权限位无意义（一律读作 0666），
		// 明确说明不做 ACL 校验，而不是假装检查过。
		logger.Warn("pluginhost: Windows 下不校验插件目录 ACL，请自行确保 %s 仅管理员可写", dir)
		return nil
	}

	if m.cfg.AllowInsecureDir {
		return nil
	}
	if info.Mode().Perm()&dirPermissionMask != 0 {
		return fmt.Errorf("pluginhost: 插件目录 %s 对其他用户可写（权限 %v），"+
			"加载插件等于执行任意代码；请收紧权限，或显式设置 plugin.allowInsecureDir=true",
			dir, info.Mode().Perm())
	}
	return nil
}

// loop 周期扫描。
func (m *Manager) loop() {
	defer close(m.doneCh)

	ticker := time.NewTicker(m.scan)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			m.scanOnce()
		case <-m.stopCh:
			return
		}
	}
}

// withScanLock 在 fn 执行期间独占扫描。
//
// 同一时刻只允许一轮扫描在跑：热升级涉及「拉起新进程 → 退役旧进程 → 换入」
// 这一串非幂等动作，两轮交错会同时拉起两个新进程。
func (m *Manager) withScanLock(fn func()) {
	m.scanMu.Lock()
	defer m.scanMu.Unlock()
	fn()
}

// scanOnce 执行一轮扫描：加载新插件、热升级被替换的插件、卸载消失的插件。
func (m *Manager) scanOnce() {
	m.withScanLock(m.scanOnceLocked)
}

func (m *Manager) scanOnceLocked() {
	if m.stopped.Load() {
		return // 已停机，迟到的 Reload() 不得再拉起任何进程
	}

	matches, err := filepath.Glob(filepath.Join(m.baseDir, m.cfg.Glob))
	if err != nil {
		logger.Error("pluginhost: 扫描插件目录失败: %v", err)
		return
	}
	sort.Strings(matches) // 保证加载顺序确定

	seen := make(map[string]bool, len(matches))
	setChanged := false

	for _, candidate := range matches {
		path, err := m.resolveCandidate(candidate)
		if err != nil {
			logger.Warn("pluginhost: 跳过 %s: %v", candidate, err)
			continue
		}
		seen[path] = true

		fp, err := fingerprintOf(path)
		if err != nil {
			logger.Warn("pluginhost: 读取 %s 属性失败: %v", path, err)
			continue
		}

		m.mu.Lock()
		lp, loaded := m.plugins[path]
		failedFp, failedBefore := m.failed[path]
		m.mu.Unlock()

		switch {
		case loaded && lp.fingerprintEqual(fp):
			// 未变化：无操作。

		case loaded && failedBefore && failedFp.equal(fp):
			// 磁盘上这一版**升级失败过**：不重试。
			//
			// 必须排在 case loaded 之前 —— 否则每轮扫描都重试一次，代价是
			// 拉起一个进程 + 等满一个握手超时（默认 10s），每 10s 一次。
			// 重试的逃逸口是让文件指纹再变一次（touch 即可，内容未变会被
			// upgrade 的同内容短路识别为无操作）。

		case loaded && !m.cfg.DisableHotUpgrade:
			// 二进制被原地替换：热升级。
			//
			// 失败时**不置 setChanged** —— 运行时注册表没有变化（upgrade 会
			// 自行回滚），触发 Refresh 只会白白中断全部采集。
			if err := m.upgrade(lp, fp); err != nil {
				logger.Error("pluginhost: 热升级 %s 失败，继续运行旧版本: %v", path, err)
				m.mu.Lock()
				m.failed[path] = fp
				lp.lastUpgradeErr = err.Error()
				m.mu.Unlock()
				continue
			}
			m.mu.Lock()
			delete(m.failed, path)
			m.mu.Unlock()
			// 不置 setChanged：热升级走世代号自动重连，不需要 Refresh；
			// upgrade 内部只有在协议名集合或 SerialExclusive 变化时才
			// 自行 scheduleRefresh。

		case loaded:
			// 热升级已关闭（plugin.disableHotUpgrade=true）：只警告一次，
			// 继续跑旧版本，新版本在下次重启网关后生效。
			m.mu.Lock()
			needWarn := !lp.warnedChange
			lp.warnedChange = true
			m.mu.Unlock()
			if needWarn {
				logger.Warn("pluginhost: 插件 %s 已在磁盘上被替换，但 plugin.disableHotUpgrade=true，"+
					"网关不会热升级已在运行的驱动；新版本将在下次重启网关后生效", path)
			}

		case failedBefore && failedFp.equal(fp):
			// 同一版本已失败过：不重试、不重复刷日志。

		default:
			if err := m.load(path, fp); err != nil {
				logger.Error("pluginhost: 加载 %s 失败: %v", path, err)
				m.mu.Lock()
				m.failed[path] = fp
				m.mu.Unlock()
				continue
			}
			m.mu.Lock()
			delete(m.failed, path)
			m.mu.Unlock()
			setChanged = true
		}
	}

	// 消失的插件：注销并停进程，但**不动 iot_protocol 行**（见 unload 注释）。
	m.mu.Lock()
	var gone []*loadedPlugin
	for path, lp := range m.plugins {
		if !seen[path] {
			gone = append(gone, lp)
			delete(m.plugins, path)
		}
	}
	for path := range m.failed {
		if !seen[path] {
			delete(m.failed, path)
		}
	}
	m.mu.Unlock()

	for _, lp := range gone {
		logger.Warn("pluginhost: 插件 %s 已移除，注销协议 %s；使用这些协议的设备将被跳过，"+
			"iot_protocol 行保留以便恢复或人工清理", lp.path, protocolNames(lp.meta))
		m.unregister(lp)
		lp.link.retireCurrent()
		setChanged = true
	}

	if setChanged {
		m.scheduleRefresh()
	}
}

// resolveCandidate 把候选路径解析为绝对路径，并阻止符号链接逃逸。
//
// 单层目录、不递归：候选文件解析符号链接后的父目录必须等于解析后的插件目录。
// 这挡住"在插件目录里放一个指向 /bin/sh 或 cmd.exe 的符号链接"。
func (m *Manager) resolveCandidate(candidate string) (string, error) {
	resolved, err := filepath.EvalSymlinks(candidate)
	if err != nil {
		return "", fmt.Errorf("解析符号链接: %w", err)
	}
	resolved, err = filepath.Abs(resolved)
	if err != nil {
		return "", err
	}

	base, err := filepath.EvalSymlinks(m.baseDir)
	if err != nil {
		base = m.baseDir
	}
	base, err = filepath.Abs(base)
	if err != nil {
		return "", err
	}

	if filepath.Dir(resolved) != base {
		return "", fmt.Errorf("解析后位于插件目录之外（%s），拒绝加载", resolved)
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return "", err
	}
	if info.IsDir() {
		return "", errors.New("是目录")
	}
	return resolved, nil
}

func fingerprintOf(path string) (fileFingerprint, error) {
	info, err := os.Stat(path)
	if err != nil {
		return fileFingerprint{}, err
	}
	return fileFingerprint{size: info.Size(), modTime: info.ModTime().UnixNano()}, nil
}

// fingerprintEqual 报告磁盘上的文件是否仍与加载时记录的一致。
//
// 必须与 lp.fp（加载或上次升级时记下的）比。**不要**改成重新 stat 一次
// lp.path 再与入参比：入参本来就是 scanOnce 当场 stat 出来的，两次结果出自
// 同一路径的相邻时刻，恒等 —— 那样这个判断永远为真，「文件被替换」这条分支
// 一次都进不去，热升级与「替换后警告」都会静默失效。
func (lp *loadedPlugin) fingerprintEqual(fp fileFingerprint) bool {
	return lp.fp.equal(fp)
}

// load 首次加载单个插件：拉起进程、握手、注册三个注册表、供给协议行。
//
// 已加载插件的新版本走 upgrade，不走这里。
func (m *Manager) load(path string, fp fileFingerprint) error {
	sha, err := hashFile(path)
	if err != nil {
		return fmt.Errorf("计算 SHA-256: %w", err)
	}

	// 首次加载无前身，世代号从 0 起（首次成功握手后为 1）。
	// sha 一并交给进程实例：此后每次拉起（含崩溃重启）都要复核磁盘上的内容，
	// 见 process.go 的 expectSHA / errBinaryReplaced。
	proc, meta, err := m.spawnAndDescribe(path, sha)
	if err != nil {
		return err
	}

	link := newPluginLink(path, proc)
	if err := m.register(meta, link); err != nil {
		proc.retire()
		return err
	}

	lp := &loadedPlugin{path: path, meta: meta, link: link, sha: sha, fp: fp}
	m.mu.Lock()
	m.plugins[path] = lp
	m.mu.Unlock()

	logger.Info("pluginhost: 已加载插件 %s（协议 %s，scope %s，版本 %s，sha256 %s）",
		filepath.Base(path), protocolNames(meta), meta.Scope, meta.Version, sha)

	if !m.cfg.DisableProtocolRowProvisioning {
		if err := m.provisionProtocols(meta); err != nil {
			// 供给失败不回滚注册：驱动可用，只是前端下拉里缺这些行。
			logger.Error("pluginhost: 维护 iot_protocol 行失败（协议 %s）: %v", protocolNames(meta), err)
		}
	}
	return nil
}

// scheduleRefresh 合并短时间内的多次驱动集变化，最多每 refreshDebounce 触发一次
// collector.Refresh()。
//
// 必须防抖：Refresh 会重建整个采集任务（停旧驱动、重开串口），
// 抖动会导致反复中断采集。
func (m *Manager) scheduleRefresh() {
	m.refreshMu.Lock()
	defer m.refreshMu.Unlock()

	if m.refreshTimer != nil {
		return // 已有待触发的刷新，本次并入
	}
	m.refreshTimer = time.AfterFunc(m.refreshDelay, func() {
		m.refreshMu.Lock()
		m.refreshTimer = nil
		m.refreshMu.Unlock()
		m.doRefresh()
	})
}

// doRefresh 触发采集配置热刷新。
//
// 调用它时不持有任何 Manager 锁：Refresh 会取 collector.Engine 的写锁，
// 而采集侧不回调 Manager，因此不存在死锁；反之若持 m.mu 调用则有风险。
func (m *Manager) doRefresh() {
	select {
	case <-m.stopCh:
		return // 已停机
	default:
	}

	if m.reload == nil {
		return
	}
	if !m.reload.IsRunning() {
		// 采集引擎尚未启动（首次发现早于 CollectorEngine.Start）：无需刷新。
		// 插件的驱动与协议行此时都已就位，collector 建首个任务时自然生效。
		return
	}
	if err := m.reload.Refresh(); err != nil {
		logger.Warn("pluginhost: 驱动集变化后刷新采集配置失败: %v", err)
	}
}

func hashFile(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return sha256Hex(data), nil
}
