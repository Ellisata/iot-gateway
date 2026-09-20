// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package dlt645

import "time"

// 协议注册名。
// 传输层由协议注册名固定（Serial=RS-485/RS-232 串口、TCP=串口服务器/DTU 透传），
// 不随 protocol_json 的 transport 字段切换；两个协议共享同一 DL/T 645 应用层
// （帧编解码、数据标识解析、数值解码），仅底层 I/O 不同。
const (
	ProtocolDLT645Serial = "DLT645.Serial"
	ProtocolDLT645TCP    = "DLT645.TCP"
)

// 传输层标识（cfg.Transport 取值）。
const (
	TransportSerial = "SERIAL"
	TransportTCP    = "TCP"
)

// 协议版本（cfg.Version 取值）。
//
// 两版差异集中在：读数据控制码（2007=0x11 / 1997=0x01）、正常应答码
// （0x91 / 0x81）、异常应答码（0xD1 / 0xC1）、数据标识 DI 长度（4 字节 / 2 字节），
// 以及 2007 独有的「读后续数据」机制（0x12）。
const (
	Version2007 = "2007"
	Version1997 = "1997"
)

// 默认值与硬上限
const (
	defaultVersion       = Version2007
	defaultMeterAddress  = "000000000001" // 12 位十进制表号，左补零
	defaultHost          = "127.0.0.1"
	defaultPort          = 8899 // 串口服务器 / DTU 透传端口
	defaultComPort       = "COM1"
	defaultBaudRate      = 2400 // 645 电表出厂默认速率，多数现场沿用
	defaultDataBits      = 8
	defaultStopBits      = 1
	defaultParity        = "E"  // 645 电表几乎全为偶校验，与仓库内其它驱动的 "N" 默认不同
	defaultTimeoutMS     = 3000 // 单帧应答超时
	defaultInterFrameMS  = 30   // 收发间延时：RS-485 收发器换向 + 表处理时间
	defaultPreambleBytes = 4    // 串口前导 0xFE 唤醒字节数
	defaultMaxDIsPerRead = 1    // 默认一个 DI 一个请求（见 plan.go 说明）
	defaultMaxFollowUp   = 2    // 2007 后续帧上限
	maxPreambleBytes     = 4    // 标准规定前导 0xFE 为 1~4 字节
	maxMeterDigits       = 12   // 表号 6 字节 BCD = 12 位十进制
	maxDIsPerReadLimit   = 12   // 单请求最多打包的数据标识个数
	maxFollowUpLimit     = 8    // 后续帧上限的合法取值上界
	maxDataLen2007       = 200  // 标准规定读数据 L ≤ 200
	maxDataLen1997       = 52   // 1997 帧总长 ≤ 64 字节，扣除 12 字节固定开销
)

// DLT645Config DL/T 645 协议配置（对应 device.protocol_json 反序列化）。
// 传输层由协议注册名固定，transport 字段保留仅作兼容。
type DLT645Config struct {
	Transport string `json:"transport"` // 传输层（由协议注册名固定，此字段值被覆盖）

	// ---- 协议方言 ----
	Version      string `json:"protocolVersion"` // "2007" / "1997"，默认 2007
	MeterAddress string `json:"meterAddress"`    // 表号，1~12 位十进制，默认 "000000000001"

	// ---- 以太网 ----
	Host string `json:"host"` // 串口服务器 / DTU 的 IP
	Port int    `json:"port"` // 串口服务器 / DTU 的端口，默认 8899

	// ---- 串口 ----
	ComPort  string `json:"comPort"`  // 串口名称，如 "COM1"、"/dev/ttyUSB0"
	BaudRate int    `json:"baudRate"` // 波特率，默认 2400
	DataBits int    `json:"dataBits"` // 数据位（5/6/7/8），默认 8
	StopBits int    `json:"stopBits"` // 停止位（1 或 2），默认 1
	Parity   string `json:"parity"`   // 校验位："N"=无、"E"=偶、"O"=奇，默认 "E"

	// ---- 时序 ----
	TimeoutMS int           `json:"timeoutMs"` // 单帧应答超时毫秒数，默认 3000
	Timeout   time.Duration `json:"-"`         // 超时时间（由 TimeoutMS 转换）
	// InterFrameDelayMS 发送请求后、开始读应答前的等待毫秒数。
	// RS-485 需要收发器换向时间，且 645 表处理慢（尤其 2400bps），过短会丢首字节。
	InterFrameDelayMS int           `json:"interFrameDelayMs"` // 默认 30
	InterFrameDelay   time.Duration `json:"-"`                 // 由 InterFrameDelayMS 转换

	// PreambleBytes 帧前导 0xFE 唤醒字节个数（0~4）。串口默认 4，TCP 默认 0。
	// 前导字节不计入校验和。
	PreambleBytes int `json:"preambleBytes"`

	// ---- 读优化与容错 ----
	// MaxDIsPerRead 单请求打包的数据标识个数，默认 1。
	// 2007 的读命令理论支持一次读多个 DI，但异常应答不回显 DI、无法定位失败项，
	// 故默认逐个读；设为 >1 时批量请求遇异常应答会自动降级为逐个重读（见 plan.go）。
	MaxDIsPerRead int `json:"maxDIsPerRead"`
	// MaxFollowUpFrames 收到「有后续帧」标志（0xB1 / 0xB2）时的续读次数上限，默认 2。
	//
	// 当前实现**不支持拼接后续帧**（后续帧应答是否回显帧序号、回显在哪个位置，
	// 各厂商手册描述不一，臆测布局会静默取到错位的数据，见 codec.go 的 hasFollowUp），
	// 故该字段暂不参与读取逻辑，仅保留以兼容已下发的表单配置。
	// 收到后续帧标志时，该组点位直接标记为异常并告警，设备连接不受影响。1997 忽略。
	MaxFollowUpFrames int `json:"maxFollowUpFrames"`
	// CheckChecksum 是否校验帧校验和 CS。默认 true。
	// 少数廉价表的 CS 计算不规范，可关闭改为仅校验帧结构（首尾 68H/16H 与长度）。
	CheckChecksum bool `json:"checkChecksum"`
}

// DefaultDLT645Config 返回默认 DL/T 645 配置。
// transport 决定与传输相关的默认值（前导唤醒字节数）。
func DefaultDLT645Config(transport string) *DLT645Config {
	preamble := 0
	if transport == TransportSerial {
		preamble = defaultPreambleBytes
	}
	return &DLT645Config{
		Transport:         transport,
		Version:           defaultVersion,
		MeterAddress:      defaultMeterAddress,
		Host:              defaultHost,
		Port:              defaultPort,
		ComPort:           defaultComPort,
		BaudRate:          defaultBaudRate,
		DataBits:          defaultDataBits,
		StopBits:          defaultStopBits,
		Parity:            defaultParity,
		TimeoutMS:         defaultTimeoutMS,
		Timeout:           time.Duration(defaultTimeoutMS) * time.Millisecond,
		InterFrameDelayMS: defaultInterFrameMS,
		InterFrameDelay:   time.Duration(defaultInterFrameMS) * time.Millisecond,
		PreambleBytes:     preamble,
		MaxDIsPerRead:     defaultMaxDIsPerRead,
		MaxFollowUpFrames: defaultMaxFollowUp,
		CheckChecksum:     true,
	}
}

// Is2007 返回配置是否为 2007 版方言。
func (c *DLT645Config) Is2007() bool {
	return c.Version != Version1997
}

// DIBytes 返回该版本数据标识 DI 的字节数。
func (c *DLT645Config) DIBytes() int {
	if c.Is2007() {
		return 4
	}
	return 2
}

// maxDataLen 返回该版本数据域长度的合理上限（解析时的合理性检查用）。
func (c *DLT645Config) maxDataLen() int {
	if c.Is2007() {
		return maxDataLen2007
	}
	return maxDataLen1997
}

// DISpec 一个点位解析后的数据标识规格。
//
// DL/T 645 的数据长度、小数位、符号与编码方式由**数据标识 DI 本身**决定，
// 而非配置界面的数据类型下拉。因此本规格是解码的唯一事实来源，
// 通用数据类型（CommonDataType）只决定输出形态与 Kind（见 decode.go）。
type DISpec struct {
	DI      uint32 // 规范化数据标识：2007 为 4 字节值（如 0x02010100），1997 为 2 字节值（如 0x9010）
	Version string // 协议版本，仅用于日志里把数据标识按该版本的位数打印

	Name     string // 数据项名称（来自内置字典，未命中为空）
	Unit     string // 单位（来自内置字典，未命中为空）
	Bytes    int    // 数据字节数
	Decimals int    // 小数位数（工程值 = 整数原始值 / 10^Decimals）
	Signed   bool   // 有符号（BCD 时为最高字节 bit7 符号位）
	Binary   bool   // 二进制编码（默认 BCD）
	Layout   string // 日期时间布局（仅时间类 DI 使用）
}

// readPlan 单批次点位的读取规划（结构见 plan.go 的 buildPlan）。
type readPlan struct {
	// entries 与传入的 addrs 一一对应（下标相同），含各点位解析出的数据标识规格。
	entries []planEntry
	// groups 请求分组；每组含 1..maxDIsPerRead 个 entries 下标。
	groups []readGroup
}

// readGroup 一次请求读取的数据标识集合。
type readGroup struct {
	idx []int // readPlan.entries 下标
}
