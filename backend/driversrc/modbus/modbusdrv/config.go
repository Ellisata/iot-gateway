// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package modbusdrv

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// ==================== Modbus TCP ====================

// ModbusTcpConfig Modbus TCP 协议配置（对应 device.protocol_json 反序列化）
type ModbusTcpConfig struct {
	Host         string        `json:"host"`         // 设备主机地址，默认 "127.0.0.1"
	Port         int           `json:"port"`         // 设备端口，默认 502
	UnitID       byte          `json:"unitId"`       // 从站地址，默认 1
	FunctionCode byte          `json:"functionCode"` // 功能码，默认 3 (ReadHoldingRegisters)
	StartAddress uint16        `json:"startAddress"` // 起始地址，默认 0
	Quantity     uint16        `json:"quantity"`     // 读取数量（寄存器数），默认 10
	ByteOrder    string        `json:"byteOrder"`    // 字节序：BIG_ENDIAN / LITTLE_ENDIAN
	WordOrder    string        `json:"wordOrder"`    // 字序：BIG_ENDIAN / LITTLE_ENDIAN
	Timeout      time.Duration `json:"-"`            // 超时时间（由 TimeoutMS 转换）
	TimeoutMS    int           `json:"timeoutMs"`    // 超时毫秒数，默认 5000
	// MergeWindow 自动模式下的合并窗口（单位同功能码：FC3/4 为寄存器、FC1/2 为位）。
	// >0 时，把跨度在窗口内但有小空洞的相邻段合并进同一区间，减少串行往返；
	// =0 时仅按严格连续性聚类。默认 125（单帧寄存器上限）。
	MergeWindow uint16 `json:"mergeWindow"`
	// StringLen string 类型单点读取字节数（每个寄存器 2 字节）。默认 16。
	// 地址名不含长度声明，string 点位的读取跨度由此值决定；
	// 自动模式下 CalcReadRanges 会把区间末端扩展到覆盖该跨度。
	StringLen int `json:"stringLen"`
}

// DefaultModbusTcpConfig 返回默认 Modbus TCP 配置
func DefaultModbusTcpConfig() *ModbusTcpConfig {
	return &ModbusTcpConfig{
		Host:         "127.0.0.1",
		Port:         502,
		UnitID:       1,
		FunctionCode: FuncCodeReadHoldingRegisters,
		StartAddress: 0,
		Quantity:     10,
		ByteOrder:    ByteOrderBigEndian,
		WordOrder:    WordOrderBigEndian,
		TimeoutMS:    5000,
		MergeWindow:  defaultMergeWindow,
		StringLen:    defaultStringLen,
	}
}

// modbusConfigRaw 匹配 protocol_json 原始数据格式
// （数值字段为原生 JSON 类型：unitId/mergeWindow/stringLen，不再是全字符串）
type modbusConfigRaw struct {
	Host        string `json:"host"`
	Port        int    `json:"port"`
	UnitID      byte   `json:"unitId"`
	ByteOrder   string `json:"byteOrder"`
	WordOrder   string `json:"wordOrder"`
	MergeWindow uint16 `json:"mergeWindow"`
	StringLen   int    `json:"stringLen"`
}

// ParseModbusTcpConfig 从 device.protocol_json JSON 字符串解析 Modbus 配置
// 未设置的字段使用默认值
func ParseModbusTcpConfig(protocolJSON string) (*ModbusTcpConfig, error) {
	cfg := DefaultModbusTcpConfig()

	if protocolJSON == "" {
		return cfg, nil
	}

	// 先解析为原始格式，校验 JSON 合法性
	var raw modbusConfigRaw
	if err := json.Unmarshal([]byte(protocolJSON), &raw); err != nil {
		return nil, fmt.Errorf("modbus config: invalid JSON: %w", err)
	}

	// 如果 JSON 为空，直接返回默认值
	if raw == (modbusConfigRaw{}) {
		return cfg, nil
	}

	cfg.Host = raw.Host
	cfg.Port = raw.Port
	// 从站地址（原生 byte；未配置时保持默认 1）
	if raw.UnitID != 0 {
		cfg.UnitID = raw.UnitID
	}
	// 独立配置优先：byteOrder/wordOrder 非空时覆盖默认值，
	// 允许按需组合（如字节大端 + 字小端的 "CD AB" 变体）。
	if raw.ByteOrder != "" {
		if strings.EqualFold(raw.ByteOrder, ByteOrderLittleEndian) {
			cfg.ByteOrder = ByteOrderLittleEndian
		} else {
			cfg.ByteOrder = ByteOrderBigEndian
		}
	}
	if raw.WordOrder != "" {
		if strings.EqualFold(raw.WordOrder, WordOrderLittleEndian) {
			cfg.WordOrder = WordOrderLittleEndian
		} else {
			cfg.WordOrder = WordOrderBigEndian
		}
	}

	// 合并窗口：未配置时保持默认 125
	if raw.MergeWindow > 0 {
		cfg.MergeWindow = raw.MergeWindow
	}

	// string 读取长度：未配置时保持默认 16
	if raw.StringLen > 0 {
		cfg.StringLen = raw.StringLen
	}

	// 设置 Timeout
	if cfg.TimeoutMS <= 0 {
		cfg.TimeoutMS = 5000
	}
	cfg.Timeout = time.Duration(cfg.TimeoutMS) * time.Millisecond

	return cfg, nil
}

// ==================== Modbus RTU ====================

// rawInt 容错整数：同时接受 JSON 数字与数字字符串。
//
// form-create 各控件产出的 JSON 形态并不一致：inputNumber 给数字，select 给
// **字符串**（"9600"）。若这里声明成 int，一份带字符串波特率的 protocol_json
// 会让 json.Unmarshal 整体报错 —— 不是那一个字段失效，而是**整份配置解析失败**、
// 设备再也连不上。所以凡是对应表单里 select 控件的数值字段，都必须用本类型。
//
// set 区分「字段缺席」与「显式配了 0」：例如 timeoutMs 配 0 是"不超时"还是
// "没填"，只有 set 分得出来。
//
// 与 dlt645drv 的同名类型是两处独立实现（各驱动包自持一份，不跨包共享，
// 避免为一个小工具类型引入包依赖）。
type rawInt struct {
	set bool
	val int
}

func (r *rawInt) UnmarshalJSON(b []byte) error {
	s := strings.Trim(strings.TrimSpace(string(b)), `"`)
	if s == "" || s == "null" {
		return nil
	}
	v, err := strconv.Atoi(s)
	if err != nil {
		// 认不出就当作没配，走默认值。这几个值错了的表现本来就只是"不通"，
		// 不该再多一层报错。
		return nil
	}
	r.set = true
	r.val = v
	return nil
}

// 单帧超时（timeoutMs）的允许区间。
//
// 上限不是审美选择而是硬约束：网关侧 pluginhost 的 readTimeoutMs 默认 60s，
// 协议自身超时若接近它，每次读取都会在网关侧先超时而插件仍在跑，重试会在
// 串行总线上堆积（见 docs/driver-plugins.md §4）。下限防的是"超时小于一帧
// 传输时间"，症状是偶发 CRC 校验错而非明确超时，极难排查。
const (
	minRTUTimeoutMS = 200
	maxRTUTimeoutMS = 30000
)

// modbusRTUConfigRaw 匹配 protocol_json 原始数据格式
// （unitId/mergeWindow/stringLen 为原生 JSON 类型，与 TCP 一致；
// 串口链路参数例外，用 rawInt —— 它们在表单里是 select）
type modbusRTUConfigRaw struct {
	ComPort     string `json:"comPort"`
	BaudRate    rawInt `json:"baudRate"`
	DataBits    rawInt `json:"dataBits"`
	StopBits    rawInt `json:"stopBits"`
	Parity      string `json:"parity"`
	TimeoutMS   rawInt `json:"timeoutMs"`
	UnitID      uint8  `json:"unitId"`
	ByteOrder   string `json:"byteOrder"`
	WordOrder   string `json:"wordOrder"`
	MergeWindow uint16 `json:"mergeWindow"`
	StringLen   int    `json:"stringLen"`
}

// ModbusRTUConfig Modbus RTU 协议配置（对应 device.protocol_json 反序列化）
type ModbusRTUConfig struct {
	ComPort      string        `json:"comPort"`      // 串口名称，如 "COM1"、"/dev/ttyUSB0"
	BaudRate     int           `json:"baudRate"`     // 波特率，默认 9600
	DataBits     int           `json:"dataBits"`     // 数据位（5/6/7/8），默认 8
	StopBits     int           `json:"stopBits"`     // 停止位（1 或 2），默认 1
	Parity       string        `json:"parity"`       // 校验位："N"=无、"E"=偶、"O"=奇，默认 "N"
	UnitID       uint8         `json:"unitId"`       // 从站地址，默认 1
	FunctionCode byte          `json:"functionCode"` // 功能码，默认 3 (ReadHoldingRegisters)
	StartAddress uint16        `json:"startAddress"` // 起始地址，默认 0
	Quantity     uint16        `json:"quantity"`     // 读取数量（寄存器数），默认 10
	ByteOrder    string        `json:"byteOrder"`    // 字节序：BIG_ENDIAN / LITTLE_ENDIAN
	WordOrder    string        `json:"wordOrder"`    // 字序：BIG_ENDIAN / LITTLE_ENDIAN
	Timeout      time.Duration `json:"-"`            // 超时时间（由 TimeoutMS 转换）
	TimeoutMS    int           `json:"timeoutMs"`    // 超时毫秒数，默认 5000
	MergeWindow  uint16        `json:"mergeWindow"`  // 自动模式下的合并窗口，默认 125
	StringLen    int           `json:"stringLen"`    // string 类型单点读取字节数，默认 16
}

// DefaultModbusRTUConfig 返回默认 Modbus RTU 配置
func DefaultModbusRTUConfig() *ModbusRTUConfig {
	return &ModbusRTUConfig{
		ComPort:      "COM1",
		BaudRate:     9600,
		DataBits:     8,
		StopBits:     1,
		Parity:       "N",
		UnitID:       1,
		FunctionCode: FuncCodeReadHoldingRegisters,
		StartAddress: 0,
		Quantity:     10,
		ByteOrder:    ByteOrderBigEndian,
		WordOrder:    WordOrderBigEndian,
		TimeoutMS:    5000,
		MergeWindow:  defaultMergeWindow,
		StringLen:    defaultStringLen,
	}
}

// ParseModbusRTUConfig 从 JSON 字符串解析 Modbus RTU 配置
// 未设置的字段使用默认值
func ParseModbusRTUConfig(protocolJSON string) (*ModbusRTUConfig, error) {
	cfg := DefaultModbusRTUConfig()

	if protocolJSON == "" {
		return cfg, nil
	}

	// 先解析为原始格式，校验 JSON 合法性
	var raw modbusRTUConfigRaw
	if err := json.Unmarshal([]byte(protocolJSON), &raw); err != nil {
		return nil, fmt.Errorf("modbus rtu config: invalid JSON: %w", err)
	}

	// 如果 JSON 为空，直接返回默认值
	if raw == (modbusRTUConfigRaw{}) {
		return cfg, nil
	}

	// 串口名：**带非空守卫**。无条件赋值会让一份不含 comPort 的 JSON
	// （如 {"unitId":1}）把默认的 COM1 清成空串，直到 Connect 才报
	// "comPort is required" —— 错误跑到离现场很远的地方才冒出来。
	// 已知后果：用户在前端把端口框**显式清空**也会回退到默认 COM1。
	// 想区分"缺席→默认"与"显式空→报错"需要四个串口协议一起改用带 set 的
	// 字符串类型，本次不做。
	if raw.ComPort != "" {
		cfg.ComPort = raw.ComPort
	}

	// 串口链路参数：非法值一律回退默认值，而不是让整条配置解析失败 ——
	// 这几个值错了的表现是"不通"，本来就够难排查了，不该再多一层报错。
	// 它们同时参与 Ping 的连接复用判定（见 ModbusRTUClient.matchConfig），
	// 所以必须如实反映到 cfg 上，否则复用判定拿到的永远是默认值。
	if raw.BaudRate.set && raw.BaudRate.val > 0 {
		cfg.BaudRate = raw.BaudRate.val
	}
	if v := raw.DataBits.val; raw.DataBits.set && (v == 5 || v == 6 || v == 7 || v == 8) {
		cfg.DataBits = v
	}
	if v := raw.StopBits.val; raw.StopBits.set && (v == 1 || v == 2) {
		cfg.StopBits = v
	}
	if p := strings.ToUpper(strings.TrimSpace(raw.Parity)); p == "N" || p == "E" || p == "O" {
		cfg.Parity = p
	}

	// 从站地址（原生 byte；未配置时保持默认 1）
	if raw.UnitID != 0 {
		cfg.UnitID = raw.UnitID
	}

	// 字节序/字序：只由显式 byteOrder/wordOrder 决定
	if raw.ByteOrder != "" {
		if strings.EqualFold(raw.ByteOrder, ByteOrderLittleEndian) {
			cfg.ByteOrder = ByteOrderLittleEndian
		} else {
			cfg.ByteOrder = ByteOrderBigEndian
		}
	}
	if raw.WordOrder != "" {
		if strings.EqualFold(raw.WordOrder, WordOrderLittleEndian) {
			cfg.WordOrder = WordOrderLittleEndian
		} else {
			cfg.WordOrder = WordOrderBigEndian
		}
	}

	// 合并窗口：未配置时保持默认 125
	if raw.MergeWindow > 0 {
		cfg.MergeWindow = raw.MergeWindow
	}

	// string 读取长度：未配置时保持默认 16
	if raw.StringLen > 0 {
		cfg.StringLen = raw.StringLen
	}

	// 单帧超时：未配置保持默认，配了则夹到允许区间内。
	// 夹紧而不是报错：超时"过大"的危害（网关侧先超时、重试在串行总线上堆积）
	// 用户看不见，把它挡在区间外比让现场去猜一个安全的数字更有用。
	if raw.TimeoutMS.set && raw.TimeoutMS.val > 0 {
		cfg.TimeoutMS = raw.TimeoutMS.val
	}
	cfg.TimeoutMS = clampTimeoutMS(cfg.TimeoutMS)
	cfg.Timeout = time.Duration(cfg.TimeoutMS) * time.Millisecond

	return cfg, nil
}

// sharedTCPConfig 把 RTU 配置投影成 TCP 的配置视图。
//
// RTU 的 Read 与点位扫描都复用 TCP 那一套地址范围计算与解析逻辑，而它们的入参
// 是 *ModbusTcpConfig —— 投影只做一份，免得两处的字段清单各自漂移
// （漏掉一个字段的症状是"某类点位在 RTU 上算出来的区间不对"，极难定位）。
//
// 只投影两边都有的字段：Host/Port/Timeout 是传输层专有的，本视图里没有意义。
func sharedTCPConfig(cfg *ModbusRTUConfig) *ModbusTcpConfig {
	return &ModbusTcpConfig{
		FunctionCode: cfg.FunctionCode,
		StartAddress: cfg.StartAddress,
		Quantity:     cfg.Quantity,
		ByteOrder:    cfg.ByteOrder,
		WordOrder:    cfg.WordOrder,
		UnitID:       cfg.UnitID,
		MergeWindow:  cfg.MergeWindow,
		StringLen:    cfg.StringLen,
	}
}

// clampTimeoutMS 把单帧超时夹到 [minRTUTimeoutMS, maxRTUTimeoutMS]。
func clampTimeoutMS(v int) int {
	if v < minRTUTimeoutMS {
		return minRTUTimeoutMS
	}
	if v > maxRTUTimeoutMS {
		return maxRTUTimeoutMS
	}
	return v
}

// configFieldsSerial / configFieldsTCP 该传输层协议表单**面向用户**的字段名。
//
// 全部字段都在表单里，故这里就是全集（对照 dlt645drv，那边有两个带理由的例外）。
// functionCode/startAddress/quantity 是配置结构体里留给内置时代的兼容字段，
// 表单从不暴露、raw 也不声明，因此天然不在本清单中。
//
// 用途：main_test.go 的 TestFormsMatchConfigStruct 按本清单与 form_json 做集合
// 比对，防止"表单里填的值被解析器静默丢弃"这一类缺陷 —— ModBus.RTU 的
// baudRate/dataBits/stopBits/parity 曾经整整缺了一轮就是这么来的。
var configFieldsSerial = []string{
	"comPort", "baudRate", "dataBits", "stopBits", "parity", "timeoutMs",
	"unitId", "byteOrder", "wordOrder", "mergeWindow", "stringLen",
}

var configFieldsTCP = []string{
	"host", "port", "unitId", "byteOrder", "wordOrder", "mergeWindow", "stringLen",
}

// ConfigFieldNames 返回该传输层协议表单应暴露的配置字段名（protocol_json 的键）。
func ConfigFieldNames(transport string) []string {
	if strings.EqualFold(transport, "RTU") {
		return configFieldsSerial
	}
	return configFieldsTCP
}
