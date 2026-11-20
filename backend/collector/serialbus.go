// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package collector

import (
	"sort"
	"strings"

	"github.com/Ellisata/iot-gateway-sdk/driver"

	"iot-gateway/logger"
	"iot-gateway/model/po"
)

// ==================== 串口总线级互斥 ====================
//
// RS-485 挂多台设备是常态（DL/T 645 靠表号、Modbus RTU 靠站号寻址），而采集
// 引擎的互斥粒度此前是**设备**：同一端口上的两台设备各拿各的锁、各自重连，
// 半双工总线上的帧会交错，插件侧两个会话的点对点请求-应答也会互相打断。
//
// 这里把串口独占驱动的锁键从"设备"提升到"物理端口"：同一端口上的所有设备
// （含控制下发）共用同一个 deviceGate，等价于整条总线串行。
//
// 它**不能单独修好"两台设备共用一个串口"**：同一插件进程内的修复是共享句柄
// （SDK 的 driversdk/serialbus），跨插件的修复是网关代理串口（插件经 serial.*
// RPC 收发字节，物理句柄只存在于网关主进程）。网关这层负责两件插件做不到的事：
//   - 跨插件（进程）同端口的收发错开，并在全部协议都支持代理时打开代理；
//   - 在采集开始前把"这个口上有多台设备/混了不同协议"讲清楚（见
//     configureSerialBusSharing），因为现场现象是"两台表轮流超时"，极难反推。

// serialBusLockKey 返回设备应共享的总线锁键；不适用时返回空串。
//
// 只有「声明了串口独占 + 能按 §3.6 约定取到 comPort」的设备才归并：
//   - 未声明独占的驱动（TCP/S7）不应该因为配置里残留 comPort 而被串行化；
//   - 取不到端口的设备退回设备级锁，行为与改动前一致。
func serialBusLockKey(dev po.Device, drv driver.Driver) string {
	if !serialExclusive(drv) {
		return ""
	}
	port := driver.CanonicalSerialPort(driver.ExtractSerialPort(dev.ProtocolJSON))
	if port == "" {
		return ""
	}
	return "serial:" + port
}

// deviceLockKey 返回设备在采集/控制路径上应使用的互斥键。
// 串口独占驱动按总线聚合，其余按设备。
func deviceLockKey(dev po.Device, drv driver.Driver) string {
	if key := serialBusLockKey(dev, drv); key != "" {
		return key
	}
	return "device:" + dev.ID
}

// buildLockKeys 为任务里每台有驱动的设备算好互斥键（构建期一次，之后只读）。
//
// 无驱动的设备不登记：它们不会进入采集调度，也就不会走 deviceLock。
func buildLockKeys(devices []po.Device, drivers map[string]driver.Driver) map[string]string {
	keys := make(map[string]string, len(drivers))
	for _, dev := range devices {
		drv, ok := drivers[dev.ID]
		if !ok {
			continue
		}
		keys[dev.ID] = deviceLockKey(dev, drv)
	}
	return keys
}

// configureSerialBusSharing 建任务时诊断串口分组，并在跨协议共用时启用网关代理。
//
// 为什么必须在这里做：多台设备共用一个串口时，现场现象是"两台表轮流超时、
// 日志里串口反复连接/关闭"，而"它们其实在同一条总线上"极难从现象反推出来。
//
// 三种情形：
//   - 同协议共用：合法（总线型协议的设计用法），插件进程内共享一个句柄，
//     INFO 告知采集按总线串行；
//   - 不同协议共用且**全部协议都支持代理**：打开网关串口代理 —— 物理句柄只由
//     网关主进程持有，各插件经 serial.* RPC 收发字节。这是跨进程的正确终局；
//   - 不同协议共用但混有不支持代理的插件：保持各自直连，WARN 说明冲突仍在。
func (t *gatewayTask) configureSerialBusSharing() {
	type bus struct {
		port    string
		devices []po.Device
	}

	buses := make(map[string]*bus)
	for _, dev := range t.devices {
		drv, ok := t.drivers[dev.ID]
		if !ok || !serialExclusive(drv) {
			continue
		}
		port := driver.CanonicalSerialPort(driver.ExtractSerialPort(dev.ProtocolJSON))
		if port == "" {
			continue
		}
		b := buses[port]
		if b == nil {
			b = &bus{port: port}
			buses[port] = b
		}
		b.devices = append(b.devices, dev)
	}

	ports := make([]string, 0, len(buses))
	for port := range buses {
		ports = append(ports, port)
	}
	sort.Strings(ports)

	for _, port := range ports {
		b := buses[port]
		if len(b.devices) < 2 {
			continue
		}

		names := make([]string, 0, len(b.devices))
		protocolSet := make(map[string]bool)
		for _, dev := range b.devices {
			names = append(names, dev.Name)
			protocolSet[t.protocols[dev.ID]] = true
		}
		sort.Strings(names)
		joined := strings.Join(names, "、")

		if len(protocolSet) == 1 {
			logger.Info("collector: 串口 %s 上有 %d 台设备（%s），采集按总线串行执行",
				port, len(b.devices), joined)
			continue
		}

		protocols := make([]string, 0, len(protocolSet))
		for p := range protocolSet {
			protocols = append(protocols, p)
		}
		sort.Strings(protocols)
		joinedProtocols := strings.Join(protocols, "、")

		if !t.enableSerialProxy(b.devices) {
			logger.Warn("collector: 串口 %s 被 %d 台设备共用（%s），且分属不同协议（%s）："+
				"所用插件未全部支持网关串口代理，各进程仍各持一个句柄、端口参数会互相干扰；"+
				"请升级驱动插件，或为每个协议分配独立的串口 / DTU",
				port, len(b.devices), joined, joinedProtocols)
			continue
		}

		logger.Info("collector: 串口 %s 被 %d 台设备共用（%s），分属不同协议（%s）；"+
			"已启用网关串口代理，物理句柄由网关持有，采集按总线串行执行",
			port, len(b.devices), joined, joinedProtocols)
	}
}

// enableSerialProxy 在**全部**设备都支持代理时置位，返回是否成功。
//
// 混入一个不支持代理的驱动就不启用：只让一半设备走代理更糟 —— 网关持有一个
// fd、旧插件再开一个，问题原样复现，而且更难排查。
func (t *gatewayTask) enableSerialProxy(devices []po.Device) bool {
	for _, dev := range devices {
		drv, ok := t.drivers[dev.ID]
		if !ok {
			return false
		}
		capable, ok := drv.(driver.SerialProxyCapable)
		if !ok || !capable.SupportsSerialProxy() {
			return false
		}
		if _, ok := drv.(driver.SerialProxySetter); !ok {
			return false
		}
	}
	for _, dev := range devices {
		t.drivers[dev.ID].(driver.SerialProxySetter).SetSerialProxy(true)
	}
	return true
}
