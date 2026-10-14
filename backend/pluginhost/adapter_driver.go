// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package pluginhost

// ==================== 驱动插件适配层 ====================
//
// 本文件是宿主里**唯一认识「驱动」这个概念**的一块：把 describe 上报的
// driversdk.Meta 翻译成 driver 包的三张运行时注册表。
//
// 边界要对齐清楚，否则「宿主」这个词会慢慢变成一句空话：
//
//   - 与本文件同包但**通用**的部分 —— process.go（拉起/监管/退避重启/
//     Windows Job Object 孤儿防护）、upgrade.go（热升级）、client.go（帧 RPC）、
//     link.go、env_*.go、manager.go 的扫描与生命周期 —— 不认识任何插件种类。
//     它们只依赖 pluginwire。
//   - 本文件 + provision.go（iot_protocol 行供给）+ remote.go（把 RPC 调用
//     包装成 driver.Driver 的 remoteDriver）构成完整的驱动侧适配。
//
// 将来推送通道也进程外化时，对应的适配放 adapter_push.go（把 describe 翻译成
// push.Register 的工厂），**不要**去复制上面那组通用文件：进程监管与热升级是
// 这套东西里最难写对的部分，两份拷贝一定会漂移。

import (
	"fmt"
	"strings"

	"iot-gateway/driver"
	"iot-gateway/driversdk"
)

// register 把插件写入三个运行时注册表。
//
// 顺序不可颠倒：**类型映射 scope → 类型注册表 → 驱动注册表**。
//
// 为什么 scope 必须排在最前：它是唯一会因「名字撞车」而失败的步骤，而
// TypeRegistry.Register 是**覆盖写**。把类型注册放在前面的话，一个 scope 与
// 别的插件撞车的插件（例如声明成 "MODBUS" 或 "modbus"）会先把它同前缀的类型
// 整批换成自己那份，然后才被拒绝加载 —— 拒绝得干干净净，破坏却永久留下了
// （TypeRegistry 没有回滚路径）。之后那个先到的插件按前缀扫注册表建缓存，
// 读出来的解码函数已经换了人，症状是「某台设备的点位永远采不到值」，
// 而两个插件的加载日志上都写着成功，没人会往这里想。
//
// 排在最后两个的仍然是「先类型、后驱动」，这样任何观察者都不会看到"驱动已可
// 创建、但它的类型还查不到"的中间态（那样前端类型下拉会是空的）。scope 先于类型
// 发布只会让下拉在极短的窗口里为空，不产生错误数据。
//
// 一个插件可提供多个协议，它们共用同一个 scope 与类型表；
// SerialExclusive 是**每协议独立**的（Modbus TCP 非独占、RTU 独占串行总线）。
//
// 任何一步失败都会回滚前面已完成的步骤，不留部分生效的状态。
// link 而非具体的 *pluginProcess：热升级要能把驱动实例从旧进程挪到新进程，
// 而工厂闭包按值捕获这里传进来的对象（见 pluginLink）。
func (m *Manager) register(meta driversdk.Meta, link processLink) error {
	names := make([]string, 0, len(meta.Protocols))
	for _, p := range meta.Protocols {
		names = append(names, p.Name)
	}

	// 1. 类型映射 scope（scope 冲突、协议名撞其它插件都在这里被拒）。
	//    一次注册全部协议名：它们共用同一套类型。**在动类型注册表之前**完成，
	//    理由见函数头。
	if err := driver.RegisterScope(meta.Scope, names, meta.TypeMap); err != nil {
		return fmt.Errorf("注册类型 scope: %w", err)
	}

	// 2. 类型注册表。函数字段留空是安全的：网关侧从不调用
	// Decode/Encode/Format（见 driver.DataType 的序列化契约注释）。
	scope := driver.GetTypeRegistry().ForProtocol(meta.Scope)
	for _, dt := range meta.Types {
		scope.Register(&driver.DataType{Name: dt.Name, Kind: dt.Kind, Size: dt.Size})
	}

	// 3. 驱动注册表。顶替已注册的协议名会在此被 ErrProtocolTaken 拒绝，
	//    不存在覆盖路径（插件重名时后到的那个加载失败，先到的毫发无损）。
	//    逐个注册；中途失败则把已注册的与步骤 1 一并回滚。
	for _, p := range meta.Protocols {
		protocol := p.Name
		serial := p.SerialExclusive
		if err := driver.RegisterExternal(protocol, func() driver.Driver {
			return newRemoteDriver(link, protocol, serial, m.opts.Timeouts)
		}); err != nil {
			for _, done := range names {
				if done == protocol {
					break
				}
				driver.Unregister(done)
			}
			driver.UnregisterScope(meta.Scope)
			return fmt.Errorf("注册驱动 %s: %w", protocol, err)
		}
	}
	return nil
}

// unregister 摘除插件的运行时能力。
//
// 刻意不清理 TypeRegistry 中的类型条目：可能仍有设备地址引用这些内部类型名，
// 保留孤儿条目无害（仅占内存），删除会破坏 NormalizeAddressType 对既有地址的判定。
func (m *Manager) unregister(lp *loadedPlugin) {
	for _, p := range lp.meta.Protocols {
		driver.Unregister(p.Name)
	}
	driver.UnregisterScope(lp.meta.Scope)
}

// protocolNameList 返回插件的全部协议名。
func protocolNameList(meta driversdk.Meta) []string {
	names := make([]string, 0, len(meta.Protocols))
	for _, p := range meta.Protocols {
		names = append(names, p.Name)
	}
	return names
}

// protocolNames 返回插件的全部协议名（逗号分隔），用于日志。
func protocolNames(meta driversdk.Meta) string {
	return strings.Join(protocolNameList(meta), ", ")
}
