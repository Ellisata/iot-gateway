// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package pluginhost

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"iot-gateway/logger"
)

// ErrPluginNotFound 指定文件既不在磁盘上，也不是已加载插件。
var ErrPluginNotFound = errors.New("插件文件不存在")

// Uninstall 卸载一个插件：停掉它的进程、摘除运行时注册、删除磁盘上的文件。
//
// 参数是插件目录里的**文件名**（basename，如 iot-driver-modbus.exe），
// 不接受路径 —— 调用方来自管理端请求体，放行路径等于把「删任意文件」的
// 能力交给一个上传过密钥的账号。目录由 Manager 自己解析，符号链接逃逸
// 由 resolveCandidate 挡住。
//
// 与扫描路径的关系：整个过程持有 scanMu，与 scanOnce（加载/热升级/消失检测）
// 互斥，因此不会出现「扫描刚把进程拉起来、这边正在删文件」的交错。
// 进程必须先退役再删文件：Windows 上正在运行的 exe 不能删除（共享冲突），
// 这与热升级用「先改名让位」绕过的限制是同一个 —— 但卸载没有后继，
// 等进程退出后直接删是正确形态。
//
// 失败语义：进程已退役、注册已摘除，但文件删不掉时返回错误。此时文件仍在，
// 下一轮扫描会把它重新加载并补回协议行，状态回到卸载前 —— 调用方看到的是
// 「卸载失败」，而不是一个半死不活的插件。
//
// **协议行不在这里删**：本包只认识驱动注册表，不认识设备引用；由
// service.PluginService 在确认没有设备引用后删除 iot_protocol 行。
// 这是有意分层，不要在 pluginhost 里引入设备表查询。
func (m *Manager) Uninstall(name string) error {
	if !m.cfg.Enabled {
		return ErrPluginDisabled
	}
	if m.baseDir == "" {
		return errors.New("插件目录尚未初始化（管理器未启动）")
	}
	if m.stopped.Load() {
		return errors.New("网关正在停止，无法卸载插件")
	}

	base := filepath.Base(name)
	if base != name || base == "." || base == ".." || base == "" {
		return fmt.Errorf("非法的插件文件名 %q（只接受插件目录下的文件名）", name)
	}
	if ok, err := filepath.Match(m.cfg.Glob, base); err != nil || !ok {
		return fmt.Errorf("插件文件名 %s 不匹配 %s", base, m.cfg.Glob)
	}

	target := filepath.Join(m.baseDir, base)
	// 已加载插件的键是 resolveCandidate 解析后的路径；目录本身可能经符号链接
	// 访问，不解析就会查不到 lp，表现为「注册没摘、进程没停」。
	resolved := target
	if p, err := m.resolveCandidate(target); err == nil {
		resolved = p
	}

	var (
		changed bool
		err     error
	)
	m.withScanLock(func() {
		changed, err = m.uninstallLocked(resolved)
	})
	if err != nil {
		return err
	}
	if changed {
		// 驱动集变化，重建采集任务。用防抖版本：Refresh 会停掉全部驱动并
		// 重开独占串口，立即刷新与后续扫描的加载重叠时会多中断一次。
		m.scheduleRefresh()
	}
	return nil
}

// uninstallLocked 执行卸载主体。调用方必须持有 scanMu。
//
// 返回驱动集是否发生变化（仅「确实卸载了一个已加载插件」时为 true）。
func (m *Manager) uninstallLocked(target string) (bool, error) {
	// 只删普通文件：同名的目录/设备文件不该被「卸载插件」顺手删掉。
	// 目录本就不会被扫描加载，不存在「先卸载再删」的必要。
	if info, err := os.Stat(target); err == nil && !info.Mode().IsRegular() {
		return false, fmt.Errorf("%s 不是普通文件，拒绝删除", filepath.Base(target))
	}

	m.mu.Lock()
	lp := m.plugins[target]
	delete(m.plugins, target)
	delete(m.failed, target)
	m.mu.Unlock()

	if lp != nil {
		// 顺序同扫描器的消失分支：先摘注册、再停进程。反向会让采集侧在
		// 极短窗口内继续拿协议名创建出指向已死进程的驱动实例。
		m.unregister(lp)
		lp.link.retireCurrent()
	}

	_, statErr := os.Stat(target)
	exists := statErr == nil
	if !exists && lp == nil {
		return false, ErrPluginNotFound
	}

	if exists {
		if err := os.Remove(target); err != nil {
			// 文件还在 → 下一轮扫描会重新加载，不会留下「注册已摘但文件在」的怪状态。
			return lp != nil, fmt.Errorf("删除插件文件 %s 失败: %w",
				filepath.Base(target), err)
		}
	}

	// 热升级留下的让位文件：不在扫描 glob 内，但同一次卸载顺手清掉。
	if err := os.Remove(filepath.Join(filepath.Dir(target),
		"."+filepath.Base(target)+asideSuffix)); err != nil && !errors.Is(err, fs.ErrNotExist) {
		logger.Warn("pluginhost: 清理插件让位文件失败（可忽略）: %v", err)
	}

	if lp != nil {
		logger.Warn("pluginhost: 已卸载插件 %s（协议 %s）；"+
			"使用这些协议的设备将被跳过，协议行由管理端服务层删除",
			filepath.Base(target), protocolNames(lp.meta))
	}
	return lp != nil, nil
}
