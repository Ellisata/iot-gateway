// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package main

// 插件装载：让 loadtest 在「协议驱动全部由进程外插件提供」之后仍能跑通全链路。
//
// 采集引擎按 iot_protocol.name 从 driver 注册表取驱动，而内置驱动已随插件化
// 全部移除（见 docs/driver-plugins.md），因此压测前必须先把插件加载进来。
// 这里复用网关自己的 pluginhost.Manager，不复刻一套加载逻辑 —— 压测要测的
// 正是网关真实使用的那条链路（含进程外 RPC 与帧序列化），任何简化都会让
// 测出来的容量失去意义。

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"

	"iot-gateway/configFile"
	dbsqlite "iot-gateway/database/sqlite"
	"iot-gateway/driver"
	"iot-gateway/pluginhost"
)

// pluginScanIntervalMs 管理器后台扫描间隔（毫秒）。
//
// 压测期间插件目录不会变化，周期扫描纯属浪费；更要紧的是任何一次「驱动集变化」
// 都会在防抖窗口后触发采集配置热刷新，而 loadtest 的采集引擎是按用例临时
// 建/停的，被插一脚会污染测量窗口。设成 1 小时，等于只保留 Start() 里那次
// 同步首扫。
const pluginScanIntervalMs = 3600000

// noopReloader 实现 pluginhost.Reloader，但从不触发刷新。
//
// 为什么不按网关那样把 collector.Engine 接上去：loadtest 的引擎是 runCase
// 里按用例新建、用完即停的，每个用例的库也是新建后现场造数，而插件在
// Manager.Start()（早于任何一次 engine.Start()）时就已全部注册完毕 ——
// 引擎每次建任务读到的都是最新注册表，**不存在需要热刷新的窗口**。
// 反过来，若接真引擎，Start() 首扫置位的「驱动集变化」会在 2s 防抖后
// （恰好落在第一个用例的测量窗口内）把采集任务整体重建，直接毁掉那组读数。
type noopReloader struct{}

func (noopReloader) Refresh() error  { return nil }
func (noopReloader) IsRunning() bool { return false }

// resolvePluginsDir 把 -plugins-dir 解析为绝对路径。
//
// pluginhost 只认「相对 os.Executable() 所在目录」（网关部署形态下这是对的，
// 见 docs/driver-plugins.md §4），但 loadtest 二进制在 `go run` 下被产在临时
// 目录，相对路径会落进临时目录里必然落空。所以这里显式解析，按序尝试：
//
//  1. 绝对路径：原样使用；
//  2. 可执行文件旁（= pluginhost 的语义）：`go build` 出的二进制与 plugins/ 同层时命中；
//  3. invocationDir（**调用 loadtest 时**的工作目录，不是 cwd）：`go run ./cmd/loadtest`
//     在 backend/ 下执行时命中。注意不能在这里取 os.Getwd()：main 早在解析插件之前
//     就把 cwd 切到了临时目录（自包含运行，避免读到运行目录的 default.yaml）。
//
// 都没命中直接报错返回，**不做「找不到就当没有插件」的降级** —— 那会一路跑出
// 「协议不支持、全 0 记录」的假结果，比直接失败有害得多。
func resolvePluginsDir(dir, invocationDir string) (string, error) {
	if strings.TrimSpace(dir) == "" {
		return "", fmt.Errorf("-plugins-dir 不能为空")
	}
	if filepath.IsAbs(dir) {
		return filepath.Clean(dir), nil
	}

	var tried []string
	if exe, err := os.Executable(); err == nil {
		tried = append(tried, filepath.Join(filepath.Dir(exe), dir))
	}
	if invocationDir != "" {
		tried = append(tried, filepath.Join(invocationDir, dir))
	}
	for _, cand := range tried {
		if st, err := os.Stat(cand); err == nil && st.IsDir() {
			return filepath.Clean(cand), nil
		}
	}

	abs := make([]string, 0, len(tried))
	for _, c := range tried {
		abs = append(abs, filepath.Clean(c))
	}
	return "", fmt.Errorf("插件目录 %q 不存在（已尝试：%s）", dir, strings.Join(abs, " 与 "))
}

// startPluginManager 启动外部协议驱动管理器：解析目录 → 同步首扫加载插件 → 注册协议。
//
// 必须在任何 collector.Engine.Start() **之前**调用：Manager.Start 的首次扫描是
// 同步的，插件驱动注册完毕之后，采集引擎建任务时才能为对应协议的设备建出驱动；
// 否则这些设备首轮会被判为「协议不支持」而静默跳过（时序要求同 backend/main.go
// 的 deps.DriverManager.Start()）。
//
// 返回的 stop 停管理器并关掉它自己那份临时库。调用时序上 stop 必须晚于全部
// collector.Engine.Stop()（见 pluginhost.Manager.Stop 的注释）：那时采集已完全
// 停止、无 RPC 在途，回收插件进程才安全。loadtest 在 main 里 defer，
// 每个用例的引擎都在 runCase 内停妥，因此天然满足。
func startPluginManager(pluginsDir string) (m *pluginhost.Manager, stop func(), err error) {
	cfg := configFile.MustGetConfig()
	// 直接改内存配置：loadtest 的 configFile 用自身嵌入的 YAML（只含 log 段），
	// 这里是这份配置里 plugin 段的唯一来源。
	cfg.Plugin.Enabled = true
	cfg.Plugin.Dir = pluginsDir
	// 压测插件是本地现编的产物，不对目录权限提要求：Unix 下 backend/plugins-*
	// 常是组可写，Windows 本就不校验 ACL。取值只影响 checkDirSecurity。
	cfg.Plugin.AllowInsecureDir = true
	cfg.Plugin.ScanIntervalMs = pluginScanIntervalMs

	// Manager 需要 *gorm.DB 只为维护 iot_protocol 行，与各用例的造数库无关，
	// 所以单独给它一个临时库，避免与 runCase 里反复建/删的库纠缠。
	db, dbPath, derr := newPluginDB()
	if derr != nil {
		return nil, nil, derr
	}
	cleanupDB := func() {
		if sqlDB, e := db.DB(); e == nil {
			sqlDB.Close()
		}
		os.Remove(dbPath)
		os.Remove(dbPath + "-wal")
		os.Remove(dbPath + "-shm")
	}

	m = pluginhost.NewManager(db, cfg, noopReloader{})
	if err := m.Start(); err != nil {
		cleanupDB()
		return nil, nil, fmt.Errorf("启动插件管理器: %w", err)
	}
	return m, func() {
		m.Stop()
		cleanupDB()
	}, nil
}

// newPluginDB 建 Manager 专用的临时库（真实迁移，含 iot_protocol 表）。
func newPluginDB() (*gorm.DB, string, error) {
	dbPath := filepath.Join(os.TempDir(),
		fmt.Sprintf("iotgw-loadtest-plugin-%d-%d.db", os.Getpid(), time.Now().UnixNano()))
	db := dbsqlite.InitDB(&configFile.Config{
		SQLite: configFile.SQLiteConfig{Path: dbPath},
		Log:    configFile.LogConfig{Level: "WARN"},
	})
	if db == nil {
		return nil, "", fmt.Errorf("init plugin sqlite db failed")
	}
	db.Logger = gormlogger.Default.LogMode(gormlogger.Silent)
	return db, dbPath, nil
}

// requireProtocol 校验本用例的协议已由插件注册，未注册时返回可读的错误。
//
// 采集引擎对「协议不支持」的设备是非致命的：它只记日志并跳过。也就是说，
// 一个漏装插件的 loadtest 会顺顺利利跑完矩阵，输出一张 drop%=100（或全 0）的
// 表 —— 压测工具给出错误结论比直接失败有害得多，所以这里宁可硬失败。
func requireProtocol(pa protocolAdapter, pluginsDir string) error {
	registered := driver.Registered()
	if slices.Contains(registered, pa.name) {
		return nil
	}

	list := "（无）"
	if len(registered) > 0 {
		list = strings.Join(registered, ", ")
	}
	bin := pa.pluginBinary()
	return fmt.Errorf("\n协议 %s 未注册：插件目录 %s 里没有成功加载到提供该协议的插件。\n"+
		"  当前已注册协议：%s\n"+
		"  需要的插件产物：%s（源码 %s）\n"+
		"  编译：cd backend && go build -o ./%s/%s %s\n"+
		"  （或在仓库根执行 ./build.sh，一次性编出全部插件）\n"+
		"  插件产物名必须以 iot-driver- 开头，否则目录扫描匹配不到；\n"+
		"  若文件已存在却仍报此错，请查看上方 pluginhost 日志里的加载失败原因。\n",
		pa.name, pluginsDir, list, bin, pa.pluginSrc,
		filepath.Base(pluginsDir), bin, pa.pluginSrc)
}
