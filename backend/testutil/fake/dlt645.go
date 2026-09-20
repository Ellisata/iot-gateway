// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package fake

import (
	"net"
	"sync/atomic"
	"time"
)

// dlt645ReadFrames 累计应答的读请求帧数。
//
// 压测诊断用：DL/T 645 的容量自变量是**帧数**而不是点数（帧数 = ceil(点数/maxDIsPerRead)），
// 从「记录/秒」反推帧率要经过引擎的批次与调度，中间任何一环理解错都会得出错误结论。
// 直接数帧可以把「每帧耗时」这个关键量与上层调度解耦开。
var dlt645ReadFrames atomic.Int64

// DLT645ReadFrames 返回进程内所有假表累计应答的读请求帧数。
func DLT645ReadFrames() int64 { return dlt645ReadFrames.Load() }

// DL/T 645 帧定界与数据域偏移常量（与 driver/dlt645/codec.go 保持一致）。
const (
	dltFrameStart    = 0x68 // 帧起始符（地址域前后各一个）
	dltFrameEnd      = 0x16 // 帧结束符
	dltPreamble      = 0xFE // 前导唤醒字节
	dltFrameOverhead = 12   // 起始(1)+地址域(6)+起始(1)+控制码(1)+长度(1)+校验(1)+结束(1)
	dltHeaderMin     = 10   // 判定帧头并读出 C 与 L 所需的最小字节数
	dltDataOffset    = 0x33 // 数据域收发各 +/- 0x33
	dltMaxDataLen    = 200  // 规范规定读数据 L ≤ 200

	dltCtrlRead2007    = 0x11 // 2007 读数据请求
	dltCtrlRead1997    = 0x01 // 1997 读数据请求
	dltCtrlReadOK2007  = 0x91 // 2007 读数据正常应答
	dltCtrlReadOK1997  = 0x81 // 1997 读数据正常应答
	dltDefaultDIBytes  = 4    // 2007 版数据标识长度
	dltDefaultDataSize = 4    // 每个数据标识的应答数据字节数（与 loadtest 地址 `%08X:4:2` 约定一致）
)

// dlt645Opts 假表行为参数。
type dlt645Opts struct {
	latency time.Duration // 每次读事务注入的固定延迟（模拟表处理时间 + 线路往返）
	dataLen int           // 每个数据标识的应答数据字节数
}

// NewDLT645 启动一个 DL/T 645-2007 电能表模拟器（真实 TCP 监听）。
//
// 只实现读数据命令（2007=0x11 / 1997=0x01）：回显请求中的每个数据标识并各附
// dataLen 字节数据。数据内容由数据标识决定（见 dltDataFor），因此不同点位会取到
// 不同值——互操作测试据此断言逐点映射正确，而不是「碰巧都能读通」。
//
// 应答的数据长度是**约定**而非从请求推导：645 的读命令只携带数据标识，不携带
// 数据长度，长度由电表手册规定。压测造数用 `%08X:4:2` 形式显式声明 4 字节，
// 与此处的 dltDefaultDataSize 对应；改地址公式时必须同步改这里。
func NewDLT645() (*Server, error) {
	return newServer(func(c net.Conn) {
		handleDLT645Conn(c, dlt645Opts{dataLen: dltDefaultDataSize})
	})
}

// NewDLT645WithLatency 同 NewDLT645，但每次读事务注入固定延迟以模拟真实线路往返。
//
// 对 DL/T 645 而言这是容量评估的主导项而非可选项：645 表普遍跑 2400bps，
// 一次「请求 16 字节 + 应答 20 字节」的往返在线路上就要 160ms 量级，
// 比网关侧处理成本高三个数量级。压测时用 -latency 把这个物理约束搬进来。
func NewDLT645WithLatency(d time.Duration) (*Server, error) {
	return newServer(func(c net.Conn) {
		handleDLT645Conn(c, dlt645Opts{dataLen: dltDefaultDataSize, latency: d})
	})
}

// handleDLT645Conn 单连接处理循环：按帧定界解析请求，逐帧合成应答。
//
// 与串口/DTU 一致地把连接当作无边界字节流处理，帧定界完全自建（645 没有传输层语义）。
func handleDLT645Conn(conn net.Conn, o dlt645Opts) {
	buf := make([]byte, 0, 512)
	tmp := make([]byte, 512)

	for {
		n, err := conn.Read(tmp)
		if n > 0 {
			buf = append(buf, tmp[:n]...)
		}
		if err != nil {
			return
		}

		// 尽量榨干缓冲：一次 Read 可能带回多个请求帧
		for len(buf) > 0 {
			consumed, resp, ok := dlt645Next(buf, o)
			if !ok {
				break // 数据不足，等下一次 Read
			}
			buf = append(buf[:0], buf[consumed:]...) // 就地前移，避免切片头持续前移导致底层数组泄漏
			if resp == nil {
				continue // 噪声或非读命令：只消费字节，不应答
			}
			if o.latency > 0 {
				time.Sleep(o.latency)
			}
			if _, err := conn.Write(resp); err != nil {
				return
			}
		}
	}
}

// dlt645Next 尝试从缓冲头部解析一个请求帧并合成应答。
//
// 返回 ok=false 表示数据不足需继续读取（此时 consumed 无意义）。
// resp 为 nil 且 ok=true 表示该帧合法但无需应答（噪声、非读命令），调用方消费掉即可。
//
// 容错策略与驱动侧对称：前导 0xFE 与噪声逐字节丢弃，校验不符按噪声跳过 1 字节重新同步。
func dlt645Next(buf []byte, o dlt645Opts) (consumed int, resp []byte, ok bool) {
	if buf[0] == dltPreamble || buf[0] != dltFrameStart {
		return 1, nil, true
	}
	if len(buf) < dltHeaderMin {
		return 0, nil, false
	}
	if buf[7] != dltFrameStart {
		return 1, nil, true // 数据里的孤立 68H，不是帧头
	}

	ctrl := buf[8]
	dataLen := int(buf[9])
	if dataLen == 0 || dataLen > dltMaxDataLen {
		return 1, nil, true
	}
	total := dltFrameOverhead + dataLen
	if len(buf) < total {
		return 0, nil, false
	}
	frame := buf[:total]
	if dltChecksum(frame[:total-2]) != frame[total-2] {
		return 1, nil, true // 校验码不符视为线路噪声
	}

	// 只应答读数据命令；其余控制码（写/校时等）合法但本模拟器不实现
	if ctrl != dltCtrlRead2007 && ctrl != dltCtrlRead1997 {
		return total, nil, true
	}

	// 还原数据域偏移（+0x33 → 原值）
	reqData := make([]byte, dataLen)
	for i := 0; i < dataLen; i++ {
		reqData[i] = frame[10+i] - dltDataOffset
	}

	dlt645ReadFrames.Add(1)

	respCtrl, diBytes := byte(dltCtrlReadOK2007), dltDefaultDIBytes
	if ctrl == dltCtrlRead1997 {
		respCtrl, diBytes = dltCtrlReadOK1997, 2
	}
	// 表地址原样回显：驱动侧允许任意 meterAddress，回显才能让配置自由取值
	return total, dltReadResponse(frame[1:7], reqData, diBytes, respCtrl, o), true
}

// dltReadResponse 合成读数据正常应答：数据域 = 每个数据标识 + 对应数据，再施加 +0x33 偏移。
func dltReadResponse(meterAddr, reqData []byte, diBytes int, respCtrl byte, o dlt645Opts) []byte {
	payload := make([]byte, 0, len(reqData)/diBytes*(diBytes+o.dataLen))
	for p := 0; p+diBytes <= len(reqData); p += diBytes {
		di := reqData[p : p+diBytes]
		payload = append(payload, di...)
		payload = append(payload, dltDataFor(di, o.dataLen)...)
	}
	for i := range payload {
		payload[i] += dltDataOffset
	}

	frame := make([]byte, 0, dltFrameOverhead+len(payload))
	frame = append(frame, dltFrameStart)
	frame = append(frame, meterAddr...)
	frame = append(frame, dltFrameStart, respCtrl, byte(len(payload)))
	frame = append(frame, payload...)
	return append(frame, dltChecksum(frame), dltFrameEnd)
}

// dltDataFor 按数据标识合成 n 字节数据：最低字节（线上首字节）置为该数据标识
// 最低字节 % 100 的合法 BCD，其余字节为 0。
//
// 用数据标识决定数值，是为了让互操作测试能断言「第 i 个点位取到第 i 个值」——
// 若所有点位都返回同一常量，映射错位（错位取值是本驱动最危险的失效模式）不会被发现。
// 取模 100 后转 BCD 保证 nibble 恒在 0~9，不会触发驱动的 BCD 非法位保护。
func dltDataFor(di []byte, n int) []byte {
	out := make([]byte, n)
	if n == 0 {
		return out
	}
	v := int(di[0]) % 100 // 线上低字节在前，di[0] 即数据标识最低字节
	out[0] = byte(v/10)<<4 | byte(v%10)
	return out
}

// dltChecksum 计算校验码：从第一个帧起始符到当前末字节的算术和 mod 256。
func dltChecksum(frame []byte) byte {
	var sum byte
	for _, b := range frame {
		sum += b
	}
	return sum
}
