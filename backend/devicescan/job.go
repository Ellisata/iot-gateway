// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package devicescan

import (
	"context"
	"encoding/json"
	"strconv"
	"time"

	"iot-gateway/driver"
)

// JobState 扫描任务状态。
type JobState string

const (
	// JobRunning 正在扫描。
	JobRunning JobState = "running"
	// JobDone 正常扫完（一个目标都没扫到也是 done —— "没扫到"不是失败）。
	JobDone JobState = "done"
	// JobCanceled 被用户取消或网关停机。**已扫到的结果保留**：
	// 用户点了取消不代表那些结果没用。
	JobCanceled JobState = "canceled"
	// JobFailed 任务本身失败了（规划阶段就出错，一个目标都没发出去）。
	JobFailed JobState = "failed"
)

// TargetError 单个目标的探测失败。
//
// 与"没扫到"是两回事：没扫到（目标不可达、不是该协议的设备）不进这里，
// 它只是没有产出设备而已。这里装的是探测本身出错了（选项非法、插件内部错误），
// 用户需要看到原因。
type TargetError struct {
	Host   string `json:"host"`
	Reason string `json:"reason"`
}

// Snapshot 任务快照，HTTP 响应与前端轮询都用它。
type Snapshot struct {
	JobID    string   `json:"jobId"`
	Protocol string   `json:"protocol"`
	State    JobState `json:"state"`
	// Reason 失败或取消的原因，仅终态非空。
	Reason string `json:"reason,omitempty"`

	// Requested 用户所选网段展开后的目标总数（不受上限影响）。
	Requested int `json:"requestedTargets"`
	// Planned 本次实际会扫的目标数（上限截断后）。
	Planned int `json:"plannedTargets"`
	// Truncated 为 true 表示只扫了前 Planned 个，前端**必须**把这件事显示出来。
	Truncated bool `json:"truncated"`

	// Total 等于 Planned，单独给出是为了前端的进度条不必做除法。
	Total int `json:"total"`
	// Scanned 已完成探测的目标数。**进度以它为准**，不是以 Found 为准：
	// 广播型协议一个目标可能一次产出几十台设备，用设备数算进度会跳变。
	Scanned int `json:"scanned"`
	// Found 已发现的设备数（含同一个 IP 的多台，如多个站号）。
	Found int `json:"found"`

	// ElapsedMs 已耗时（运行中）或总耗时（终态）。
	ElapsedMs int `json:"elapsedMs"`
	// EstimateMaxMs 最坏耗时的估算：全部目标都走满插件的单目标预算、且并发度
	// 用满时的墙钟时间。是**上界**而非期望 —— 不可达的目标才会走满拨号超时，
	// 而一个网段里绝大多数地址都不可达，所以实际通常远小于它。
	EstimateMaxMs int `json:"estimateMaxMs"`

	// Devices 已发现的设备。运行中也会返回，用户能边扫边看到结果。
	//
	// **可能为 nil**：轻量快照不带结果清单（见 Engine.Progress）。非 nil 时
	// 一定是数组，可能为空 —— 调用方要能把"清单是空的"与"这一份没带清单"
	// 分开，前者要清空自己持有的那份，后者要保留。
	Devices []driver.DiscoveredDevice `json:"devices"`
	Errors  []TargetError             `json:"errors,omitempty"`

	StartedAt string `json:"startedAt"`
	EndedAt   string `json:"endedAt,omitempty"`
}

// job 一次扫描任务。
//
// 字段分两类，同步要求不同：
//   - 规划期写入、之后**只读**的（id/protocol/options/port/targets/requested/
//     planned/truncated/startedAt/estimateMaxMs）—— 并发读安全；
//   - 运行期被 worker 改写的（state/reason/scanned/devices/errors/endedAt）
//     —— 一律在 Engine.mu 下读写。
type job struct {
	id       string
	protocol string
	options  json.RawMessage
	port     int

	requested int
	planned   int
	truncated bool
	targets   []driver.ScanTarget
	startedAt time.Time
	// estimateMaxMs 见 Snapshot 的同名字段。
	estimateMaxMs int

	// ctx/cancel 控制本次任务的全部在飞探测。
	ctx    context.Context
	cancel context.CancelFunc
	// doneCh 全部 worker 退出后关闭，供 Stop 等待。
	doneCh chan struct{}

	// 以下字段在 Engine.mu 下读写。
	state   JobState
	reason  string
	scanned int
	devices []driver.DiscoveredDevice
	errors  []TargetError
	endedAt time.Time
	// seen 已接受设备的去重键（见 acceptDevice），同样在 Engine.mu 下读写。
	seen map[string]struct{}
}

// acceptDevice 决定一台刚发现的设备要不要并进结果，并在接受时登记去重键。
//
// 去重不是可有可无的：TargetMode=both 下同一个网段的广播目标与逐主机目标会
// **各自**报出同一台设备（广播产出一批，单播又命中其中一台），用户看到的会是
// 两份一模一样的设备行。而一旦真出现这种情况，用户无从判断是多协议还是重复 ——
// 只能靠人工比对列表，正是本功能最该避免的一类困惑。
//
// 键取 host + port + unitId 三个字段，而不是只看 host：
//   - 同一 IP 上的不同端口是不同设备（Modbus 网关后面的多台从站、串口服务器
//     转发多路 PLC）；
//   - 同一 IP:端口 上的不同站号是不同设备（Modbus 的多站号探测、广播探测里
//     一台 PLC 的多个 FINS 节点）—— 这两种都是**协议定义的真实区分**，
//     合并它们会把本来该建成多台设备的发现压成一台。
//
// 先到者胜：两者的 host/port/unitId 相同即视为同一台，先后没有区别。
// 调用方必须持有 Engine.mu。
func (j *job) acceptDevice(dev driver.DiscoveredDevice) bool {
	key := dev.Host + "\x00" + strconv.Itoa(dev.Port) + "\x00" + strconv.Itoa(dev.UnitID)
	if _, dup := j.seen[key]; dup {
		return false
	}
	j.seen[key] = struct{}{}
	return true
}

// snapshot 生成快照。调用方必须持有 Engine.mu。
//
// includeResults 为 false 时不拷结果清单（Devices/Errors 保持 nil），只给计数与
// 进度 —— 轮询的绝大多数拍子并没有新设备，而整份清单在"扫到几百台"时要每拍
// 重新序列化一遍。**计数（Found）与清单无关，任何一份快照里都是准的**：
// 前端正是靠它判断要不要补一次带清单的请求（见 Engine.Progress）。
//
// 切片一律**拷贝一份**再交出去：worker 会继续 append 到 job.devices 上，
// 直接把内部切片暴露给调用方，调用方在锁外遍历时就会与 append 撞车
// （读到零值、或读到下一次扩容前的半成品）。元素本身是值类型，
// 且 Extra 只在创建时赋值、之后从不改写，因此浅拷贝即可。
//
// 带清单时**空清单也分配**（`make([]T, 0, n)` 而不是留 nil）：nil 是"这一份没带
// 清单"的标记，用它表示"扫了但没有结果"会让调用方分不清两种情况。
func (j *job) snapshot(now time.Time, includeResults bool) *Snapshot {
	s := &Snapshot{
		JobID:         j.id,
		Protocol:      j.protocol,
		State:         j.state,
		Reason:        j.reason,
		Requested:     j.requested,
		Planned:       j.planned,
		Truncated:     j.truncated,
		Total:         j.planned,
		Scanned:       j.scanned,
		Found:         len(j.devices),
		EstimateMaxMs: j.estimateMaxMs,
		StartedAt:     formatTime(j.startedAt),
	}
	if includeResults {
		s.Devices = append(make([]driver.DiscoveredDevice, 0, len(j.devices)), j.devices...)
		s.Errors = append(make([]TargetError, 0, len(j.errors)), j.errors...)
	}

	end := j.endedAt
	if end.IsZero() {
		end = now
	}
	s.ElapsedMs = int(end.Sub(j.startedAt).Milliseconds())
	if !j.endedAt.IsZero() {
		s.EndedAt = formatTime(j.endedAt)
	}
	return s
}

// terminal 报告任务是否已结束（不再有 worker 在写它）。
func (j *job) terminal() bool { return j.state != JobRunning }

// expired 报告一个终态任务是否已超过保留期。运行中的任务永不过期。
func (j *job) expired(now time.Time, ttl time.Duration) bool {
	return j.terminal() && !j.endedAt.IsZero() && now.Sub(j.endedAt) > ttl
}

func formatTime(t time.Time) string {
	return t.Format("2006-01-02 15:04:05")
}
