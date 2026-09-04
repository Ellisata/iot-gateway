// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package fins

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"strings"

	"iot-gateway/logger"
)

// 串口直连（Host Link）FINS 帧常量
const (
	serialICF       = 0x80 // 串口直连 ICF：命令帧、需响应（与 FINS/TCP 的 finsICFCommand 一致）
	serialHeader    = "FA" // Host Link 头码（FINS 命令）
	serialWaitTime  = "0"  // 响应等待时间（10ms 单位），0 = 不等待
	serialUnitChars = 2    // 单元号 ASCII 字符数
	serialFAChars   = 2    // "FA" 头码字符数
	serialWaitChars = 1    // 等待时间字符数
)

// Host Link FCS 计算窗口（config.fcsMode）。
//
// 不同设备对 FCS 取窗不同，按目标设备选择：
//   - fcsModeFull：从 @ 起至 FCS 前的全部 ASCII 字符（Omron 手册标准）
//   - fcsModeBody：仅 hex 体（@00FA0 之后的 FINS 数据区，真机核实部分设备如此）
//   - fcsModeNoSID：从 @ 起至 FCS 前的全部 ASCII 字符，但排除 SID 字段（真机核实：
//     经串口服务器透传的欧姆龙兼容 PLC，响应 FCS 不随 SID 变化）
const (
	fcsModeFull  = "FULL"  // 手册标准：@ 起至 FCS 前的全部字符
	fcsModeBody  = "BODY"  // 仅 hex 体（部分设备如此）
	fcsModeNoSID = "NOSID" // @ 起至 FCS 前的全部字符，排除 SID 字段（默认，真机核实）
)

// buildSerialFrame 构建 Host Link 串口帧（FINS 命令 over serial）。
//
// 帧格式（除 @、FA、等待时间外，FINS 字节全部 ASCII-hex 编码）：
//
//	@ <单元号:2hex> FA <等待:1> <ICF:2hex> <DA2:2hex> <SA2:2hex> <SID:2hex> <FINS 命令:hex> <FCS:2hex> * CR
//
// FCS 计算窗口由 mode 决定（fcsModeFull 含 @；fcsModeBody 仅 hex 体），见 serialFCS。
//
// 注：串口直连时 FINS 头退化为 4 字节（ICF/DA2/SA2/SID，无网络路由字段），
// finsBody 为命令码 + 参数（不含 FINS 头）。
func buildSerialFrame(mode string, unitNo, dstUnit, srcUnit, sid byte, finsBody []byte) []byte {
	body := []byte{serialICF, dstUnit, srcUnit, sid}
	body = append(body, finsBody...)

	hexBody := hexBytes(body)
	text := "@" + hexByte(unitNo) + serialHeader + serialWaitTime + hexBody

	// SID 位于 hexBody 第 6~8 字符（body 第 3 字节）；前缀 "@<单元2>FA<等待1>" 固定 6 字符。
	sidStart := len(text) - len(hexBody) + 6
	fcs := serialFCS(mode, []byte(text), []byte(hexBody), sidStart, sidStart+2)

	return []byte(text + fcs + "*" + "\r")
}

// serialFCS 按 mode 计算 Host Link FCS（各 ASCII 字符 8 位 XOR，转大写 2 位 hex）：
//
//	fcsModeFull：从 @ 起至 FCS 前的全部字符（Omron 手册标准）
//	fcsModeBody：仅 hex 体（@00FA0 之后的 FINS 数据区，真机核实部分设备如此）
//	fcsModeNoSID：从 @ 起至 FCS 前的全部字符，但排除 [sidStart, sidEnd) 区间的 SID 字段
func serialFCS(mode string, full, body []byte, sidStart, sidEnd int) string {
	if strings.EqualFold(mode, fcsModeFull) {
		return computeFCS(full)
	}
	if strings.EqualFold(mode, fcsModeNoSID) {
		return computeFCSExcl(full, sidStart, sidEnd)
	}
	return computeFCS(body)
}

// computeFCS 计算 Host Link FCS：payload 内各 ASCII 字符的 8 位 XOR，转大写 2 位 hex。
// FCS 取窗由 buildSerialFrame / parseSerialResponse 经 serialFCS 控制。
func computeFCS(payload []byte) string {
	var fcs byte
	for _, c := range payload {
		fcs ^= c
	}
	return fmt.Sprintf("%02X", fcs)
}

// computeFCSExcl 计算 Host Link FCS：对 payload 内各 ASCII 字符 8 位 XOR，
// 跳过 [start, end) 区间（NOSID 模式下用于排除 SID 字段）。
func computeFCSExcl(payload []byte, start, end int) string {
	var fcs byte
	for i, c := range payload {
		if i >= start && i < end {
			continue
		}
		fcs ^= c
	}
	return fmt.Sprintf("%02X", fcs)
}

// parseSerialResponse 解析 Host Link 串口响应帧，返回 FINS 响应数据字节。
//
// 响应帧格式（与命令帧不同：无「响应等待时间」位，且 FA 后带 1 字节 Host Link 响应码）：
//
//	@ <单元号:2> FA <响应码:2> <ICF:2> <DA2:2> <SA2:2> <SID:2> <命令:4> <结束码:4> <数据:hex> <FCS:2> * CR
//
// 结束码非 0 时返回 finsEndCodeError。
func parseSerialResponse(frame []byte, sid byte, mode string) ([]byte, error) {
	if len(frame) < 10 || frame[0] != '@' {
		return nil, fmt.Errorf("fins serial: invalid response frame %q", string(frame))
	}

	star := bytes.IndexByte(frame, '*')
	if star < 2 {
		return nil, fmt.Errorf("fins serial: missing '*' terminator")
	}

	// 响应帧无「响应等待时间」位：跳过 @、单元号、FA 后，从响应码起解码 hex
	hexStart := 1 + serialUnitChars + serialFAChars
	if star < hexStart {
		return nil, fmt.Errorf("fins serial: short frame header")
	}

	// 校验 FCS：取窗与发送端 buildSerialFrame 一致（FCS 位于 * 前 2 字符）。
	// SID 位于 hex 体第 8~10 字符（响应码 2 + ICF 2 + DA2 2 + SA2 2 之后）。
	sidStart := hexStart + 8
	fcsStr := string(frame[star-2 : star])
	calc := serialFCS(mode, frame[:star-2], frame[hexStart:star-2], sidStart, sidStart+2)
	if !strings.EqualFold(calc, fcsStr) {
		// 真机联调诊断：打出原始响应帧与解码体，确认设备实际 FCS 取窗
		hexBody := frame[hexStart : star-2]
		var dec []byte
		var decXor byte
		if len(hexBody)%2 == 0 {
			dec, _ = hex.DecodeString(string(hexBody))
			for _, b := range dec {
				decXor ^= b
			}
		}
		logger.Warn("fins serial: FCS mismatch raw=%q (mode=%s, hexStart=%d, calc=%s, dev=%s, hexBody=%q, dec=% X, decXor=%02X)",
			string(frame), mode, hexStart, calc, fcsStr, hexBody, dec, decXor)
		return nil, fmt.Errorf("fins serial: FCS mismatch (want %s, got %s)", calc, fcsStr)
	}
	body, err := hex.DecodeString(string(frame[hexStart : star-2]))
	if err != nil {
		return nil, fmt.Errorf("fins serial: invalid hex body: %w", err)
	}

	// body[0] 为 Host Link 层响应码（00=成功，13=FCS 错误、16=命令不支持、18=帧长错误等）。
	// 非 0 说明设备在 Host Link 层拒绝了该帧（多为配置/协议不匹配），给出明确错误。
	if len(body) >= 1 && body[0] != 0 {
		return nil, fmt.Errorf("fins serial: host link end code 0x%02X", body[0])
	}

	// body: 响应码(1) ICF(1) DA2(1) SA2(1) SID(1) 命令回显(2) 结束码(2) 数据...
	// FINS 结束码见 body[7:9]。
	if len(body) < 9 {
		return nil, fmt.Errorf("fins serial: short body (%d bytes)", len(body))
	}
	if body[4] != sid {
		return nil, fmt.Errorf("fins serial: SID mismatch (want 0x%02X, got 0x%02X)", sid, body[4])
	}
	if body[5] != cmdMemoryAreaReadHi || body[6] != cmdMemoryAreaReadLo {
		return nil, fmt.Errorf("fins serial: unexpected command echo 0x%02X%02X", body[5], body[6])
	}
	endCode := binary.BigEndian.Uint16(body[7:9])
	if endCode != 0 {
		return nil, newEndCodeError(endCode)
	}
	return body[9:], nil
}

// hexByte 单字节转 2 位大写 hex
func hexByte(b byte) string {
	return fmt.Sprintf("%02X", b)
}

// hexBytes 字节切片转连续大写 hex 字符串
func hexBytes(bs []byte) string {
	var sb strings.Builder
	sb.Grow(len(bs) * 2)
	for _, b := range bs {
		sb.WriteString(fmt.Sprintf("%02X", b))
	}
	return sb.String()
}
