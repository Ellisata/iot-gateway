package mitsubishi

import (
	"encoding/binary"
	"fmt"
)

// 串口 4C 帧 Format5（二进制）封帧。
//
// QnA 兼容 4C 帧是 C24 串口模块使用的 MC 协议帧；Format5 为二进制格式，
// 与 3E TCP 帧共享应用层编码（buildMCBody），仅外部封帧不同：
//
//	请求:  DLE STX | 网络(1) PC(1) IO(2) 站(1) | 数据长度(2 BE) | [请求体] | 和校验(1) | DLE ETX
//	响应:  DLE STX | 响应ID(2 = FFFFH) | 网络(1) PC(1) IO(2) 站(1) | 数据长度(2 BE) | 结束码(2) | 数据 | 和校验(1) | DLE ETX
//
// DLE 填充：帧体（DLE STX 之后至帧尾）内出现 0x10 时，传输前插入额外 0x10；
// 和校验按未填充原值累加低 8 位，且校验字节本身一并 DLE 填充。
// 这样全段 0x10 恒被双写，帧尾 DLE ETX（0x10 0x03）在字节流上无歧义
// （否则和校验字节恰为 0x10 时会与帧尾 DLE 组成假双写对，读取器无法分辨）。
//
// 注意：4C Format5 各 PLC 系列（Q/L/FX 经 C24）存在字节级方言差异，
// 真机联调时如有出入只需调整本文件的封帧/解帧布局（对齐 fins 驱动「真机核实」做法）。
const (
	dleByte = 0x10 // DLE
	stxByte = 0x02 // STX
	etxByte = 0x03 // ETX
)

// maxSerialFrameLen 串口响应帧长度硬上限（防止异常数据无界累积）。
const maxSerialFrameLen = 4096

// buildSerialFrame 构建 4C Format5 串口读请求帧。
func buildSerialFrame(dev mcDevice, head uint32, points uint16, bitMode bool) []byte {
	body := buildMCBody(dev, head, points, bitMode)
	// 帧体（待填充与校验）：网络/PC/IO/站 + 数据长度(请求体字节数)
	// 4C Format5 二进制与 3E 一致，多字节字段均小端：I/O 号 FF 03、数据长度低字节在前。
	inner := make([]byte, 0, 5+2+len(body))
	inner = append(inner, mcNetNo, mcPCNo, mcIONoLo, mcIONoHi, mcStation)
	inner = append(inner, byte(len(body)), byte(len(body)>>8))
	inner = append(inner, body...)

	// 和校验：inner 全字节累加低 8 位（未填充原值）
	sum := byte(0)
	for _, b := range inner {
		sum += b
	}

	frame := make([]byte, 0, 2+len(inner)+2+2)
	frame = append(frame, dleByte, stxByte) // 帧头
	frame = append(frame, dleStuff(inner)...)
	frame = append(frame, dleStuff([]byte{sum})...) // 和校验字节一并填充
	frame = append(frame, dleByte, etxByte)         // 帧尾
	return frame
}

// dleStuff 在 0x10 字节前插入额外 0x10（DLE 填充）。
func dleStuff(b []byte) []byte {
	out := make([]byte, 0, len(b)+len(b)/16)
	for _, x := range b {
		if x == dleByte {
			out = append(out, dleByte)
		}
		out = append(out, x)
	}
	return out
}

// dleUnstuff 移除 DLE 填充（每对 0x10 0x10 还原为单个 0x10）。
func dleUnstuff(b []byte) []byte {
	out := make([]byte, 0, len(b))
	for i := 0; i < len(b); i++ {
		out = append(out, b[i])
		if b[i] == dleByte && i+1 < len(b) {
			i++ // 跳过填充的 0x10
		}
	}
	return out
}

// parseSerialResponse 解析 4C Format5 串口响应。
// frame 为 reader 解填充后的帧体（响应 ID + 网络/PC/IO/站 + 数据长度 + 结束码 + 数据 + 和校验）。
// 返回数据字节（去除所有头部与结束码）。结束码非 0 时返回 mcEndCodeError。
func parseSerialResponse(frame []byte) ([]byte, error) {
	// 最小帧：ID(2) + 头(5) + 长度(2) + 结束码(2) + 和校验(1) = 12 字节
	if len(frame) < 12 {
		return nil, fmt.Errorf("mc serial: short response (%d bytes)", len(frame))
	}

	// 和校验：ID 至数据末尾（不含和校验自身）累加低 8 位
	sum := byte(0)
	for _, b := range frame[:len(frame)-1] {
		sum += b
	}
	if sum != frame[len(frame)-1] {
		return nil, fmt.Errorf("mc serial: sum check mismatch (want 0x%02X, got 0x%02X)",
			sum, frame[len(frame)-1])
	}

	// 数据长度（2 小端）= 结束码(2) + 数据字节数
	length := int(binary.LittleEndian.Uint16(frame[7:9]))
	if length < 2 {
		return nil, fmt.Errorf("mc serial: invalid data length %d", length)
	}
	// 帧体总长 = ID(2)+头(5)+长度(2) + length + 和校验(1)
	if len(frame) < 9+length+1 {
		return nil, fmt.Errorf("mc serial: response truncated (length=%d, have=%d)", length, len(frame))
	}

	endCode := binary.LittleEndian.Uint16(frame[9:11])
	if endCode != 0 {
		return nil, newEndCodeError(endCode)
	}

	dataLen := length - 2
	data := make([]byte, dataLen)
	copy(data, frame[11:11+dataLen])
	return data, nil
}
