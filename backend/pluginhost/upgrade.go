// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package pluginhost

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/Ellisata/iot-gateway-sdk/driversdk"
	"iot-gateway/logger"
)

// ==================== 注册面比对 ====================

// typeSig 一个内部类型的可比较指纹。
//
// Kind/Size 其实由插件自用（网关侧从不调用 Decode/Encode/Format），但一并
// 比较是保守做法：宁可多走一次重注册，也不要漏掉一次真实变化。
type typeSig struct {
	kind string
	size int
}

// registrationSig 是 Meta 中与 driver 运行时注册表有关的部分。
//
// 只有它变化才需要动 driver.* 注册表；Description / Version / FormJSON 只影响
// iot_protocol 供给，而供给从不覆盖已有行，因此不参与比较。
type registrationSig struct {
	scope     string
	protocols map[string]bool    // 协议名 → SerialExclusive
	types     map[string]typeSig // 小写内部类型名 → {Kind,Size}
	typeMap   map[string]string  // 通用类型名 → 内部类型名
}

// registrationSigOf 从 Meta 派生注册面指纹。
//
// 协议名与 TypeMap 的 key 都按**大小写敏感**比较：
//   - driver.registry 的键是原始字符串（无 ToLower），换大小写等于换协议；
//   - AvailableTypes 用 CommonDataTypes 里的规范名直接 types[name] 取值，
//     key 换大小写会让该条目**静默**从类型下拉里消失。
//
// Types 的名字则按小写归一（TypeRegistry.Register 自己会 ToLower）。
// 协议列表顺序不参与比较：RegisterScope 内部会排序。
func registrationSigOf(m driversdk.Meta) registrationSig {
	sig := registrationSig{
		scope:     m.Scope,
		protocols: make(map[string]bool, len(m.Protocols)),
		types:     make(map[string]typeSig, len(m.Types)),
		typeMap:   make(map[string]string, len(m.TypeMap)),
	}
	for _, p := range m.Protocols {
		sig.protocols[p.Name] = p.SerialExclusive
	}
	for _, dt := range m.Types {
		if dt == nil {
			continue
		}
		sig.types[strings.ToLower(dt.Name)] = typeSig{kind: dt.Kind, size: dt.Size}
	}
	for k, v := range m.TypeMap {
		sig.typeMap[k] = v
	}
	return sig
}

func (s registrationSig) equal(o registrationSig) bool {
	if s.scope != o.scope {
		return false
	}
	if len(s.protocols) != len(o.protocols) {
		return false
	}
	for k, v := range s.protocols {
		if ov, ok := o.protocols[k]; !ok || ov != v {
			return false
		}
	}
	if len(s.types) != len(o.types) {
		return false
	}
	for k, v := range s.types {
		if ov, ok := o.types[k]; !ok || ov != v {
			return false
		}
	}
	if len(s.typeMap) != len(o.typeMap) {
		return false
	}
	for k, v := range s.typeMap {
		if ov, ok := o.typeMap[k]; !ok || ov != v {
			return false
		}
	}
	return true
}

// needsTaskRebuild 报告升级是否必须重建采集任务。
//
// 采集任务在建任务时就固化了两件事：每台设备的锁粒度（serialExclusive，见
// collector/task.go 的 serialExclusive）与「哪些设备能建出驱动」（协议名集合）。
// 其余（类型表、TypeMap、scope）都是查询时现算的，换了立刻生效。
//
// 注意这是 registrationSig 的**真子集**：只改类型表不会走到这里。
func needsTaskRebuild(oldMeta, newMeta driversdk.Meta) bool {
	if len(oldMeta.Protocols) != len(newMeta.Protocols) {
		return true
	}
	oldSerial := make(map[string]bool, len(oldMeta.Protocols))
	for _, p := range oldMeta.Protocols {
		oldSerial[p.Name] = p.SerialExclusive
	}
	for _, p := range newMeta.Protocols {
		serial, ok := oldSerial[p.Name]
		if !ok || serial != p.SerialExclusive {
			return true
		}
	}
	return false
}

// ==================== 升级 ====================

// spawnAndDescribe 拉起一个新进程实例并完成 describe 握手。
//
// 失败时会把刚拉起的实例收干净再返回错误，调用方无需善后。
//
// expectSHA 是磁盘上应当存在的内容哈希，由 proc.spawn 在每次拉起前复核（见
// process.go 的 expectSHA 与 errBinaryReplaced）。世代号基数不在这里给：
// 热升级的基数由 retireAndSwap 在旧实例退役后确定，理由见该方法。
func (m *Manager) spawnAndDescribe(path, expectSHA string) (*pluginProcess, driversdk.Meta, error) {
	proc := newPluginProcessAt(path, m.opts, expectSHA)
	proc.start()

	ctx, cancel := context.WithTimeout(context.Background(), m.opts.Timeouts.Handshake+2*time.Second)
	defer cancel()
	if _, err := proc.acquire(ctx); err != nil {
		proc.retire()
		return nil, driversdk.Meta{}, fmt.Errorf("插件未能在超时内就绪: %w", err)
	}
	return proc, proc.describeMeta(), nil
}

// upgrade 把已加载插件替换为磁盘上的新二进制。
//
// 分两个阶段，关键是**阶段 1 完全不触碰正在运行的旧实例**：一个坏掉的新
// 二进制不得干扰旧版本，否则一次失手的替换就会让采集直接中断。
//
//	阶段 1  hashFile → 内容未变则只记一条日志返回；否则拉起新进程、describe
//	阶段 2  注册面相同 → 只换进程；不同 → 摘旧挂新（失败回滚到旧 Meta）后换进程
//
// 失败时旧实例保持运行，调用方（scanOnce）把新指纹记入 failed 抑制重试。
//
// 为什么不需要 collector.Refresh()：新实例的世代号基数在 retireAndSwap 里
// 取自**旧实例退役后的终值**（见 pluginLink.retireAndSwap），而每个既有驱动的
// epochAt 都取自它当时对话的那个客户端、必然 ≤ 该值。于是换入生效的瞬间，
// 所有既有驱动的 stale() 一起翻真、IsConnected() 返回 false，采集引擎既有的
// 1s→60s 重连退避把设备接到新进程上 —— 其它协议与内置驱动完全不受影响。
//
// 只有「协议名集合」或「SerialExclusive」变化时才需要重建采集任务：前者
// 决定哪些设备能建出驱动，后者是建任务时固化的锁粒度。其余变化（类型表、
// TypeMap、scope）都是查询时现算的，无需中断采集。
func (m *Manager) upgrade(lp *loadedPlugin, fp fileFingerprint) error {
	base := filepath.Base(lp.path)

	sha, err := hashFile(lp.path)
	if err != nil {
		return fmt.Errorf("计算 SHA-256: %w", err)
	}

	m.mu.Lock()
	oldSHA, oldMeta := lp.sha, lp.meta
	m.mu.Unlock()

	// 指纹是 (size, mtime)，touch、备份工具、重复拷贝同一构建都会让它变化。
	// 没有这条短路，一次 touch 就让该插件下所有设备白白重连一轮。
	if sha == oldSHA {
		logger.Info("pluginhost: 插件 %s 的文件属性已变化但内容未变（sha256 %s），不重启驱动",
			base, sha)
		// 新指纹必须记下来当作基线，否则下一轮扫描仍会认为"文件变过"：
		// 每 10s 重读一遍整个插件二进制算 SHA-256、并重复刷这一行日志，
		// 直到内容真的变化为止。上面这条日志是给运维看的，不该变成常态。
		m.mu.Lock()
		lp.fp = fp
		// 内容与正在运行的版本一致，就不存在"磁盘上的新版本没上线"这回事，
		// 上一次失败留下的诊断信息到这里已经过时。
		lp.lastUpgradeErr = ""
		m.mu.Unlock()
		return nil
	}

	link := lp.link
	next, newMeta, err := m.spawnAndDescribe(lp.path, sha)
	if err != nil {
		return err
	}

	oldSig, newSig := registrationSigOf(oldMeta), registrationSigOf(newMeta)
	if !oldSig.equal(newSig) {
		logger.Warn("pluginhost: 插件 %s 的注册面已变化（scope / 协议 / 类型 / 类型映射），将重注册驱动；"+
			"引用被移除协议名的设备会被跳过", base)

		m.unregister(lp)
		if regErr := m.register(newMeta, link); regErr != nil {
			next.retire()
			// 回滚：旧进程一直没被碰过，重新挂回旧 Meta 即可恢复原状。
			if rbErr := m.register(oldMeta, link); rbErr != nil {
				// 回滚也失败：运行时注册表处于部分生效状态，必须大声报出来，
				// 否则症状是「某个协议莫名不可用」而无人知道原因。
				logger.Error("pluginhost: 回滚插件 %s 的旧注册失败，其协议可能不可用: %v", base, rbErr)
			}
			return fmt.Errorf("重注册驱动: %w", regErr)
		}
	}

	link.retireAndSwap(next)

	m.mu.Lock()
	lp.meta = newMeta
	lp.previousSHA = oldSHA
	lp.sha = sha
	lp.fp = fp // 新基线：下一轮扫描据此判断文件是否又变了
	lp.upgradedAt = time.Now()
	lp.upgradeCount++
	lp.lastUpgradeErr = ""
	upgrades := lp.upgradeCount
	m.mu.Unlock()

	if !m.cfg.DisableProtocolRowProvisioning {
		if err := m.provisionProtocols(newMeta); err != nil {
			// 供给失败不回滚升级：驱动已可用，只是前端下拉里缺这些行。
			logger.Error("pluginhost: 热升级后维护 iot_protocol 行失败（协议 %s）: %v",
				protocolNames(newMeta), err)
		}
		// 供给「只插入、绝不覆盖」，所以改了 FormJSON 的新版本不会更新已有行。
		// 这是刻意策略（见 provision.go），但也是升级后最像故障的正常现象，
		// 必须显式说清楚，否则会被当成「升级没生效」。
		logger.Info("pluginhost: 插件的 iot_protocol 行已按新版本核对；已有行的表单不会被覆盖，" +
			"如需改用插件新表单请在协议列表里执行「恢复默认表单」")
	}

	if needsTaskRebuild(oldMeta, newMeta) {
		m.scheduleRefresh()
	}

	logger.Info("pluginhost: 已热升级插件 %s（第 %d 次，版本 %s → %s，sha256 %s → %s，世代号 %d）",
		base, upgrades, oldMeta.Version, newMeta.Version, shortSHA(oldSHA), shortSHA(sha), link.epoch())
	return nil
}

// shortSHA 取 sha256 的前 12 位，供日志使用。
func shortSHA(sha string) string {
	if len(sha) > 12 {
		return sha[:12]
	}
	return sha
}
