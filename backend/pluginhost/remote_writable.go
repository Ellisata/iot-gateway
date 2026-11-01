// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package pluginhost

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/Ellisata/iot-gateway-sdk/driver"
	"github.com/Ellisata/iot-gateway-sdk/driversdk"
	"github.com/Ellisata/iot-gateway-sdk/pluginwire"
)

// remoteWritableDriver 给 remoteDriver 补上 driver.Writer。
//
// 只有 describe 里声明了 Writable 的协议才用这个包装类型（见 adapter_driver.go）。
// 这样网关的 `drv.(driver.Writer)` 断言既能表达"这个协议能写"，又不会对
// 只读协议误判为可写。
type remoteWritableDriver struct {
	*remoteDriver
}

var _ driver.Writer = (*remoteWritableDriver)(nil)

func newRemoteWritableDriver(proc processLink, protocol string, serial bool, to Timeouts) *remoteWritableDriver {
	if to.Write <= 0 {
		to.Write = 15 * time.Second
	}
	return &remoteWritableDriver{remoteDriver: newRemoteDriver(proc, protocol, serial, to)}
}

// Write 通过当前采集会话执行一次控制下发。
//
// 与 Read 完全相同的会话/世代校验：写入绝不能落到一个已退役进程的陈旧
// 会话上 —— 那个 id 在新进程里可能属于另一台设备。
func (d *remoteWritableDriver) Write(reqs []driver.WriteRequest) ([]driver.WriteResult, error) {
	d.mu.Lock()
	sess := d.session
	d.mu.Unlock()

	if sess == 0 || d.stale() {
		d.connected.Store(false)
		return nil, ErrNotConnected
	}

	ctx, cancel := context.WithTimeout(context.Background(), d.to.Write)
	defer cancel()

	cl, err := d.proc.acquire(ctx)
	if err != nil {
		d.connected.Store(false)
		return nil, err
	}
	if cl.epoch.Load() != d.epochAt.Load() {
		d.connected.Store(false)
		return nil, ErrNotConnected
	}

	var reply driversdk.WriteReply
	callErr := cl.call(ctx, pluginwire.MethodWrite, driversdk.WriteParams{
		Session: sess,
		Writes:  reqs,
	}, &reply)

	var driverErr *pluginwire.RemoteError
	switch {
	case callErr == nil || errors.As(callErr, &driverErr):
		// 帧完好：应答里的 connected 是权威值，与 Read 同义。
		d.connected.Store(reply.Connected)
	case errors.Is(callErr, context.DeadlineExceeded):
		d.connected.Store(false)
		return nil, callErr
	default:
		d.connected.Store(false)
		return nil, callErr
	}

	if callErr != nil {
		// 驱动侧报错（链路失败）时仍返回已产生的逐点结果：
		// 调用方据此把已确认的点与未知状态区分开。
		return reply.Results, callErr
	}

	if len(reply.Results) != len(reqs) {
		return nil, fmt.Errorf("pluginhost: %s returned %d write results for %d requests",
			d.protocol, len(reply.Results), len(reqs))
	}
	// 与 Read 相同的多重集校验：挡住顺序错位与 id 张冠李戴。
	want := make(map[string]int, len(reqs))
	for _, r := range reqs {
		want[r.DeviceAddressID]++
	}
	for i := range reply.Results {
		id := reply.Results[i].DeviceAddressID
		n, ok := want[id]
		if !ok {
			return nil, fmt.Errorf("pluginhost: %s returned write result for unknown address id %q", d.protocol, id)
		}
		if n == 0 {
			return nil, fmt.Errorf("pluginhost: %s returned write result for address id %q more than once", d.protocol, id)
		}
		want[id] = n - 1
	}
	return reply.Results, nil
}
