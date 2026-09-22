// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package dlt645

import (
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/goburrow/serial"

	"iot-gateway/logger"
)

// 帧定界与数据域偏移
const (
	frameStart   = 0x68 // 帧起始符（地址域前后各一个）
	frameEnd     = 0x16 // 帧结束符
	preambleByte = 0xFE // 前导唤醒字节

	// 帧固定开销：起始符(1) + 地址域(6) + 起始符(1) + 控制码(1) + 长度(1)
	// + 校验码(1) + 结束符(1) = 12 字节
	frameOverhead = 12

	// frameHeaderMin 判定帧头并读出控制码 C 与长度 L 所需的最小字节数：
	// 起始符(1) + 地址域(6) + 起始符(1) + C(1) + L(1) = 10。
	// 注意不能只按「看到第二个 68H」的 8 字节判——那样还会去读 C/L 两个字节而越界。
	frameHeaderMin = 10
)

// 控制码。高位含义两版一致：D7 传输方向（0=主站→从站，1=从站→主站）、
// D6 从站应答标志（0=正确应答，1=异常应答）、D5 后续帧标志。
const (
	// 2007 版
	ctrl2007Read     = 0x11 // 读数据
	ctrl2007ReadNext = 0x12 // 读后续数据
	ctrl2007OK       = 0x91 // 读数据正常应答，无后续帧
	ctrl2007More     = 0xB1 // 读数据正常应答，有后续帧
	ctrl2007Abnormal = 0xD1 // 读数据异常应答

	// 读后续数据（0x12）的应答：功能码同为 10010B，故应答码为 0x92 / 0xB2 / 0xD2，
	// 不是读数据的 0x91 / 0xB1 / 0xD1——只差功能码，混淆会让整条后续帧路径失效。
	// 当前驱动不支持拼接后续帧（见 hasFollowUp），此处仅保留有后续帧标志用于判定。
	ctrl2007NextMore = 0xB2 // 读后续数据正常应答，有后续帧

	// 1997 版
	ctrl1997Read     = 0x01 // 读数据
	ctrl1997ReadNext = 0x02 // 读后续数据
	ctrl1997OK       = 0x81 // 读数据正常应答
	ctrl1997Abnormal = 0xC1 // 读数据异常应答
)

// abnormalError 表示电表**正常应答但拒绝本次请求**（异常应答控制码）。
//
// 这类错误说明链路与设备都是通的，绝不能据此判定连接断开（否则采集引擎会把
// 一台能通但拒绝某个数据标识的电表整台判离线）。与 Modbus 驱动的协议异常
// 语义完全一致：仅将该点位标记为 Quality=0，继续读取其余点位。
//
// 不含数据标识：异常应答的数据域是否回显 DI、回显哪一个，各厂商实现不一致
// （调用方本来就持有本次请求的 DI，日志里由调用方自行标注）。
type abnormalError struct {
	Code byte // 数据域第一个字节，偏移错误码
	// Raw 应答原始帧（含帧起始符、前导与校验码）。
	//
	// 「错误码 01：其他错误」本身不携带任何可定位信息——表号不对、数据标识
	// 没实现、表处于非抄读状态，报出来都是 01。现场只能靠原始报文与手册/抓包
	// 比对，所以把它一路带到调用方，由调用方在**首次**告警时打印（重复告警
	// 已被 problemLog 抑制，不会因此刷屏）。
	Raw []byte
}

func (e *abnormalError) Error() string {
	return fmt.Sprintf("dlt645: 电表异常应答（错误码 %02X：%s）",
		e.Code, abnormalCodeText(e.Code))
}

// abnormalCodeText 返回 2007 版规定的异常应答错误码含义。
func abnormalCodeText(code byte) string {
	switch code {
	case 0x01:
		return "其他错误"
	case 0x02:
		return "无请求数据"
	case 0x03:
		return "密码错/未授权"
	case 0x04:
		return "通信速率不能更改"
	case 0x05:
		return "年时区数超"
	case 0x06:
		return "日时段数超"
	case 0x07:
		return "费率数超"
	default:
		return "未定义错误码"
	}
}

// isAbnormalErr 判断错误是否为电表异常应答（设备可达但拒绝请求）。
func isAbnormalErr(err error) bool {
	var e *abnormalError
	return errors.As(err, &e)
}

// response 一个解析完成的应答帧。
type response struct {
	C    byte   // 控制码
	Data []byte // 数据域（已 -0x33 还原），异常应答时为错误码与数据标识
	// Raw 完整原始帧（含前导、帧起始符与校验码）。应答缓冲区在解析过程中
	// 会被复用与截断，故必然是拷贝而非切片视图。
	Raw []byte
}

// buildFrame 组装完整请求帧：68H | 地址域(6) | 68H | C | L | 数据域 | CS | 16H
//
// data 为**未偏移**的原始数据域内容，函数内部施加 +0x33 偏移。
// CS 为从第一个 68H 到数据域末字节的算术和 mod 256；前导唤醒字节不计入。
func buildFrame(cfg *DLT645Config, addr [6]byte, ctrl byte, data []byte) []byte {
	wire := offsetData(append([]byte(nil), data...))
	frame := make([]byte, 0, frameOverhead+len(wire))
	frame = append(frame, frameStart)
	frame = append(frame, addr[:]...)
	frame = append(frame, frameStart, ctrl, byte(len(wire)))
	frame = append(frame, wire...)
	frame = append(frame, checksum(frame), frameEnd)
	return frame
}

// buildReadFrame 组装读数据请求。specs 为本次要读的数据标识（2007 可多个、1997 仅一个）。
func buildReadFrame(cfg *DLT645Config, addr [6]byte, specs []DISpec) []byte {
	diBytes := cfg.DIBytes()
	data := make([]byte, 0, diBytes*len(specs))
	for i := range specs {
		data = append(data, specs[i].DIWireBytes(diBytes)...)
	}
	ctrl := byte(ctrl2007Read)
	if !cfg.Is2007() {
		ctrl = ctrl1997Read
	}
	return buildFrame(cfg, addr, ctrl, data)
}

// checksum 计算校验码：从第一个帧起始符到当前末字节的算术和 mod 256。
func checksum(frame []byte) byte {
	var sum byte
	for _, b := range frame {
		sum += b
	}
	return sum
}

// isRequestCtrl 判断控制码是否为主站请求码（用于识别半双工链路回显的请求帧）。
func isRequestCtrl(ctrl byte) bool {
	switch ctrl {
	case ctrl2007Read, ctrl2007ReadNext, ctrl1997Read, ctrl1997ReadNext:
		return true
	default:
		return false
	}
}

// expectedOKCtrl 返回本版本读数据的正常应答控制码。
func expectedOKCtrl(cfg *DLT645Config) byte {
	if cfg.Is2007() {
		return ctrl2007OK
	}
	return ctrl1997OK
}

// expectedAbnormalCtrl 返回本版本读数据的异常应答控制码。
func expectedAbnormalCtrl(cfg *DLT645Config) byte {
	if cfg.Is2007() {
		return ctrl2007Abnormal
	}
	return ctrl1997Abnormal
}

// parseState 描述在缓冲区某一偏移处的解析结论。
type parseState int

const (
	parseFound     parseState = iota // 命中一个可用应答帧
	parseNeedMore                    // 前缀像帧头但数据尚不完整，需继续读取
	parseSkipFrame                   // 该处是一个完整但不需要的帧（回显 / 他人地址 / 控制码不识别）
	parseSkipByte                    // 该处不是帧起始，跳过 1 字节继续扫描
)

// parseAt 尝试从 buf[pos] 处解析一个帧。
//
// 返回 (应答, 消耗字节数, 状态)。只有 parseFound 时应答非空；
// parseSkipFrame 时消耗字节数为该帧完整长度，parseSkipByte 时为 1。
//
// 容错策略（现场报文并不总是规整）：
//   - 前导 0xFE 唤醒字节：逐字节跳过；
//   - 第二个 0x68 必须落在 pos+7，否则判为非帧头（避免把数据里的孤立 0x68 当帧头）；
//   - 长度 L 做版本相关的合理性检查，超范围直接判为非帧头；
//   - 校验码不符 → 视作噪声，跳过 1 字节重新同步（不用 L 跳过，避免 L 本身被干扰时误跳过真实帧）；
//   - 结束符 0x16 缺失但校验码正确 → 接受（校验码是权威），记 Debug 日志；
//   - 主站请求控制码 → 判定为半双工链路回显，整帧跳过继续等真正应答；
//   - 地址域不匹配 → 总线上其它表的应答，整帧跳过。
func parseAt(buf []byte, pos int, cfg *DLT645Config, addr [6]byte) (*response, int, parseState) {
	if pos >= len(buf) {
		return nil, 0, parseNeedMore
	}
	if buf[pos] == preambleByte {
		return nil, 1, parseSkipByte
	}
	if buf[pos] != frameStart {
		return nil, 1, parseSkipByte
	}
	// 帧头判定需要看到地址域之后的第二个 0x68，且要读到 C 与 L 才能继续
	if len(buf)-pos < frameHeaderMin {
		return nil, 0, parseNeedMore
	}
	if buf[pos+7] != frameStart {
		return nil, 1, parseSkipByte
	}

	ctrl := buf[pos+8]
	dataLen := int(buf[pos+9])
	// 长度合理性：读数据应答的数据域不可能为 0（至少含数据标识或错误码），
	// 读后续数据应答同样至少含数据标识。长度为 0 的帧一律判为非帧头。
	if dataLen == 0 || dataLen > cfg.maxDataLen() {
		return nil, 1, parseSkipByte
	}

	total := frameOverhead + dataLen
	avail := len(buf) - pos

	// 结束符缺失容错：部分表的实现瑕疵导致不发最后一个 0x16，帧只有 total-1 字节。
	// 校验码本身就是帧体的最后一字节，收到它并通过校验即可认定帧已完整——
	// 若坚持等到 total 字节，这类表会永远读不到应答（表现是稳定超时）。
	if avail == total-1 {
		cs := buf[pos+total-2]
		if !cfg.CheckChecksum || checksum(buf[pos:pos+total-2]) == cs {
			logger.Debug("dlt645: 结束符缺失但校验码正确，接受该帧 raw=% X", buf[pos:pos+total-1])
			return newResponse(ctrl, buf[pos:pos+total-1], dataLen, addr, total-1)
		}
		return nil, 1, parseSkipByte
	}
	if avail < total {
		return nil, 0, parseNeedMore
	}

	frame := buf[pos : pos+total]
	if cfg.CheckChecksum {
		if want := checksum(frame[:total-2]); want != frame[total-2] {
			// 校验不符视为线路噪声。只跳过 1 字节重新同步而非按 L 跳过：
			// L 自身也可能被干扰，按它跳有可能越过真正的帧起始。
			logger.Debug("dlt645: 校验码不符（收到 %02X，计算 %02X），跳过并重新同步 raw=% X",
				frame[total-2], want, frame)
			return nil, 1, parseSkipByte
		}
	}
	if frame[total-1] != frameEnd {
		// 校验码正确即认可该帧；结束符异常是部分厂商表的实现瑕疵。
		logger.Debug("dlt645: 结束符异常（收到 %02X），但校验码正确，接受该帧 raw=% X",
			frame[total-1], frame)
	}

	return newResponse(ctrl, frame, dataLen, addr, total)
}

// newResponse 从已确认完整（校验通过）的候选帧构造应答，
// 途中排除回显的请求帧与总线上其它设备的应答。
func newResponse(ctrl byte, frame []byte, dataLen int, addr [6]byte, consumed int) (*response, int, parseState) {
	// 半双工链路 / 不回显抑制的转换器会把请求原样回显，必须先排除
	if isRequestCtrl(ctrl) {
		logger.Debug("dlt645: 忽略回显的请求帧 raw=% X", frame)
		return nil, consumed, parseSkipFrame
	}

	// 总线上可能挂了多块表，只接受本设备的应答
	if !equalAddr(frame[1:7], addr) {
		logger.Debug("dlt645: 忽略非本机地址的应答（% X）raw=% X", frame[1:7], frame)
		return nil, consumed, parseSkipFrame
	}

	data := unoffsetData(append([]byte(nil), frame[10:10+dataLen]...))
	return &response{
		C:    ctrl,
		Data: data,
		Raw:  append([]byte(nil), frame[:consumed]...),
	}, consumed, parseFound
}

// equalAddr 比较地址域。
func equalAddr(got []byte, want [6]byte) bool {
	if len(got) != 6 {
		return false
	}
	for i := 0; i < 6; i++ {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

// readResponse 从传输层读取一个完整的、属于本机的应答帧。
//
// 串口与 TCP 共用此实现：两者都只提供「带超时的字节流读」，帧定界必须自建。
// 逐字节同步扫描，任何解析失败都只跳过 1 字节重新同步，
// 保证解析器在遇到噪声、分片、回显时都不会死锁。
func readResponse(t dlt645Transport, cfg *DLT645Config, addr [6]byte, deadline time.Time) (*response, error) {
	// 缓冲上界：足以容纳一个最大帧；超出说明链路在持续吐垃圾数据，截断保留尾部。
	maxBufBytes := frameOverhead + cfg.maxDataLen() + 64
	buf := make([]byte, 0, 128)
	scanned := 0 // buf 中已确认不含帧起始的前缀长度
	tmp := make([]byte, 256)

	for {
		for scanned < len(buf) {
			resp, n, state := parseAt(buf, scanned, cfg, addr)
			if state == parseFound {
				return resp, nil
			}
			if state == parseNeedMore {
				// 该位置像帧头但还不完整，保留在此等待更多数据
				break
			}
			// 该处是回显 / 总线上他人的应答 / 线路噪声——本次交互之外的东西也在这条
			// 链路上，记一笔，让下一轮开始前真正去清一次缓冲（见 MarkDirty 的说明）。
			t.MarkDirty()
			if state == parseSkipFrame {
				scanned += n
			} else {
				scanned++
			}
		}

		if !time.Now().Before(deadline) {
			// 超时是「迟到的应答」最主要的来源：这一轮没等到，对方之后答过来的
			// 字节会留在缓冲里，必须在下一轮发请求前清掉，否则会被当成下一轮的应答。
			t.MarkDirty()
			return nil, fmt.Errorf("dlt645: 等待应答超时（%s 内未收到完整合法帧，已收 %d 字节）",
				cfg.Timeout, len(buf))
		}

		// 丢弃已确认无效的前缀，保持缓冲有界
		if scanned > 0 {
			buf = append(buf[:0], buf[scanned:]...)
			scanned = 0
		}
		if len(buf) > maxBufBytes {
			buf = append(buf[:0], buf[len(buf)-maxBufBytes:]...)
		}

		if err := t.SetReadDeadline(deadline); err != nil {
			t.MarkDirty()
			return nil, fmt.Errorf("dlt645: 设置读超时失败: %w", err)
		}
		n, err := t.Read(tmp)
		if n > 0 {
			buf = append(buf, tmp[:n]...)
		}
		if err != nil {
			// 单次读超时是正常现象（串口按短周期轮询以复核帧截止时间），
			// 继续循环直到由 deadline 统一裁决；其余 I/O 错误立即上报。
			if n > 0 || isTimeoutErr(err) {
				continue
			}
			t.MarkDirty()
			return nil, fmt.Errorf("dlt645: 读应答失败: %w", err)
		}
	}
}

// isTimeoutErr 判断错误是否为读超时（串口与 TCP 各自的超时错误）。
func isTimeoutErr(err error) bool {
	return errors.Is(err, serial.ErrTimeout) ||
		errors.Is(err, os.ErrDeadlineExceeded)
}

// exchange 执行一次「清缓冲 → 发送 → 等待间隔 → 读应答」的完整交互。
//
// 发送前的 Drain 是必需的：RS-485 上迟到的上一轮应答留在接收缓冲里，
// 不清理就会被当作本轮应答，表现为「值总是慢一拍」的诡异现象。
func exchange(t dlt645Transport, cfg *DLT645Config, addr [6]byte, req []byte) (*response, error) {
	t.Drain()

	frame := req
	if cfg.PreambleBytes > 0 {
		frame = make([]byte, 0, cfg.PreambleBytes+len(req))
		for i := 0; i < cfg.PreambleBytes; i++ {
			frame = append(frame, preambleByte)
		}
		frame = append(frame, req...)
	}

	logger.Debug("dlt645: 发送 raw=% X", frame)
	if _, err := t.Write(frame); err != nil {
		// 写失败时请求可能已部分发出，对方仍可能作答——链路状态未知。
		t.MarkDirty()
		return nil, fmt.Errorf("dlt645: 发送请求失败: %w", err)
	}

	// 收发间延时：RS-485 收发器换向 + 电表处理时间
	if cfg.InterFrameDelay > 0 {
		time.Sleep(cfg.InterFrameDelay)
	}

	resp, err := readResponse(t, cfg, addr, time.Now().Add(cfg.Timeout))
	if err != nil {
		return nil, err
	}
	logger.Debug("dlt645: 收到 C=%02X data=% X", resp.C, resp.Data)

	switch {
	case resp.C == expectedOKCtrl(cfg):
		return resp, nil
	case cfg.Is2007() && resp.C == ctrl2007More:
		// 数据项跨帧，由调用方决定是否继续读后续帧
		return resp, nil
	case resp.C == expectedAbnormalCtrl(cfg):
		var code byte
		if len(resp.Data) > 0 {
			code = resp.Data[0]
		}
		return resp, &abnormalError{Code: code, Raw: resp.Raw}
	default:
		return nil, fmt.Errorf("dlt645: 未识别的应答控制码 %02X", resp.C)
	}
}

// hasFollowUp 判断应答是否还有后续帧（2007 独有）。
//
// 首帧的标志在读数据应答里（0xB1），后续帧的标志在读后续数据应答里（0xB2）。
//
// 当前驱动**不支持拼接后续帧**，调用方据此把该组点位标记为异常而非解码：
// 后续帧应答的数据域除了数据标识与数据，是否还回显帧序号（SEQ）以及 SEQ 的位置，
// 各厂商手册描述不一。按臆测的布局拼接，会把序号字节当成数据拼进去，
// 得到一个看似合理实则错位的值——这比取不到值危险得多。
// 因此这里只做「识别并放弃」，等有真机报文核实后再实现续读。
func hasFollowUp(cfg *DLT645Config, resp *response) bool {
	if !cfg.Is2007() || resp == nil {
		return false
	}
	return resp.C == ctrl2007More || resp.C == ctrl2007NextMore
}
