// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package devicescan

import (
	"context"
	"errors"
	"net"
	"strconv"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/Ellisata/iot-gateway-sdk/driver"
)

// ==================== 阶段 1：TCP 可达性预筛 ====================
//
// 一个网段里绝大多数地址不可达，而插件对每个目标都要先拨号、等满自己的超时。
// 阶段 2（插件 probe）因此把绝大部分时间花在"确认一个空地址确实没人"上。
//
// 预筛做的事和插件的第一步完全相同 —— 一次裸 TCP connect —— 但：
//   - 并发高一个量级（独立于 maxConcurrentProbes，不占插件进程）；
//   - 超时短（预筛自己的配置，不是 probe 预算）；
//   - 连不上就判死，只把端口开着的主机交给插件。
//
// **它不是设备判定**：端口开着不等于对端是该协议的设备，插件仍是唯一判据；
// 反过来，插件要成功也必须先连通，所以"连不上"是可靠的否定证据。
//
// 只对插件声明了 Transport=tcp 的协议启用；广播目标永远绕过（对广播地址
// 做 TCP connect 没有意义）。UDP 或未声明传输层的协议整批不预筛。

// prefilterTargets 对当前批次做 TCP 可达性预筛，返回存活目标（保持原顺序，
// 广播目标原样排在最前）。
//
// 判死的目标计入 scannedInBatch，**不写 errors** —— 与插件"拨号失败 = 没扫到"
// 同语义。取消导致的失败不计数。
func (e *Engine) prefilterTargets(j *job) []driver.ScanTarget {
	targets := j.targets
	if e.prefilterDial == nil || len(targets) == 0 {
		return targets
	}

	keep := make([]bool, len(targets))
	sem := make(chan struct{}, e.prefilterConcurrency)
	var wg sync.WaitGroup
	var dialed, alive, skipped, failOpen atomic.Int64

dispatch:
	for i := range targets {
		if targets[i].Broadcast {
			keep[i] = true // 广播目标不进预筛
			continue
		}
		select {
		case <-j.ctx.Done():
			break dispatch
		case sem <- struct{}{}:
		}

		dialed.Add(1)
		wg.Add(1)
		go func(i int, t driver.ScanTarget) {
			defer wg.Done()
			defer func() { <-sem }()

			err := e.prefilterDial(j.ctx, t)
			if err == nil {
				keep[i] = true
				alive.Add(1)
				return
			}
			// 取消造成的失败既不算扫过、也不算"没人"：与 probeOne 同一取舍。
			if j.ctx.Err() != nil {
				return
			}
			// 网关自己的资源问题（fd 耗尽等）不能当作目标不可达的证据。
			// 放行给插件：插件是独立进程、有自己的 fd 配额，可能仍然拨得通。
			if isLocalDialError(err) {
				keep[i] = true
				failOpen.Add(1)
				return
			}
			if e.countPrefilteredDead(j) {
				skipped.Add(1)
			}
		}(i, targets[i])
	}
	wg.Wait()

	if n := dialed.Load(); n > 0 && e.logf != nil {
		e.logf("devicescan: %s TCP 预筛 %d 个目标：%d 可达，%d 判死，%d 预筛异常转插件探测",
			j.protocol, n, alive.Load(), skipped.Load(), failOpen.Load())
	}

	out := make([]driver.ScanTarget, 0, len(targets))
	for i := range targets {
		if keep[i] {
			out = append(out, targets[i])
		}
	}
	return out
}

// countPrefilteredDead 把一个被预筛判死的目标计入本批进度。
//
// 只动 scannedInBatch：offset/requested/planned/truncated 与结果清单都不碰。
// 取消之后不再计数（在飞的 dial 可能刚刚才返回）。
func (e *Engine) countPrefilteredDead(j *job) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	if j.ctx.Err() != nil {
		return false
	}
	j.scannedInBatch++
	return true
}

// dialForPrefilter 生产实现的单目标可达性检查：连上即算可达，然后立刻关闭。
//
// **不绑定 target.LocalIP**：插件单播 probe 也不绑。预筛必须走与阶段 2 完全
// 相同的拨号路径，否则"预筛能通、插件不能通"只会白跑一次 probe，而绑定反过来
// 可能把插件本来拨得通的地址误杀。多网卡下源地址选择交给操作系统的路由表。
func (e *Engine) dialForPrefilter(ctx context.Context, t driver.ScanTarget) error {
	d := &net.Dialer{Timeout: time.Duration(e.prefilterTimeoutMs) * time.Millisecond}
	conn, err := d.DialContext(ctx, "tcp", net.JoinHostPort(t.Host, strconv.Itoa(t.Port)))
	if err != nil {
		return err
	}
	_ = conn.Close()
	return nil
}

// isLocalDialError 报告这个拨号失败是不是网关自身资源问题，而不是目标不可达。
//
// 这类错误在目标一侧看来什么都没发生，把它们判死会让一次 fd 抖动静默吞掉
// 整批活设备 —— 与"没扫到不是错误"的契约冲突。宁可多跑一次插件 probe。
func isLocalDialError(err error) bool {
	var errno syscall.Errno
	if !errors.As(err, &errno) {
		return false
	}
	switch errno {
	case syscall.EMFILE, syscall.ENFILE, syscall.ENOBUFS, syscall.ENOMEM, syscall.EADDRNOTAVAIL:
		return true
	default:
		return false
	}
}
