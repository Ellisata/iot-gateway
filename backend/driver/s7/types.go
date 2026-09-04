// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package s7

import "time"

// S7 存储区域常量（对应 gos7 的区域读取方法）
// 每个区域对应一个读取原语：
//
//	S7AreaDB → client.AGReadDB(dbNumber, start, size, buf)
//	S7AreaM  → client.AGReadMB(start, size, buf)
//	S7AreaI  → client.AGReadEB(start, size, buf)   // 输入区（PE）
//	S7AreaQ  → client.AGReadAB(start, size, buf)   // 输出区（PA）
type s7Area byte

const (
	S7AreaDB s7Area = 0x84 // DB 数据块
	S7AreaM  s7Area = 0x83 // Merker / 标志位区（M）
	S7AreaI  s7Area = 0x81 // 输入映像区（I）
	S7AreaQ  s7Area = 0x82 // 输出映像区（Q）
)

// S7 连接类型（对应 gos7/Snap7 的 CONNTYPE，写入 ISO-on-TCP 的远程 TSAP 高字节）。
// 不同连接类型占用 PLC 上对应的连接资源，S7-300/400 的远程 TSAP 也据此计算。
const (
	ConnectionTypePG    = 1 // 编程设备/工程师站（gos7 默认）
	ConnectionTypeOP    = 2 // 操作面板/HMI
	ConnectionTypeBasic = 3 // 基本连接（中文资料常称 PC 连接）
)

// S7Config S7 协议配置（对应 device.protocol_json 反序列化）
// 所有字段均可通过协议表单配置，未设置的字段使用默认值。
type S7Config struct {
	Host           string        `json:"host"`           // PLC IP 地址，默认 "127.0.0.1"
	Port           int           `json:"port"`           // ISO-on-TCP 端口，默认 "102"
	Rack           int           `json:"rack"`           // 机架号，默认 0（S7-300/400 常见 0）
	Slot           int           `json:"slot"`           // 插槽号，默认 1（S7-1200/1500 常见 1，S7-300 常见 2）
	ConnectionType int           `json:"connectionType"` // 连接类型：1=PG，2=OP，3=PC/BASIC，默认 1（PG）
	TimeoutMS      int           `json:"timeoutMs"`      // 超时毫秒数，默认 5000
	StringLen      int           `json:"stringLen"`      // STRING 类型最大读取长度，默认 254
	MaxGap         int           `json:"maxGap"`         // 区间合并最大允许字节间隙，默认 8（0=关闭）
	Timeout        time.Duration `json:"-"`              // 超时时间（由 TimeoutMS 转换）
}

// DefaultS7Config 返回默认 S7 配置
func DefaultS7Config() *S7Config {
	return &S7Config{
		Host:           "127.0.0.1",
		Port:           102,
		Rack:           0,
		Slot:           1,
		ConnectionType: ConnectionTypePG,
		TimeoutMS:      5000,
		StringLen:      254,
		MaxGap:         8,
	}
}

// S7Address 解析后的 S7 地址。
// 由 ParseS7Address 从点位 name 解析得到结构信息（Area/DB/ByteOffset/BitOffset/
// Width/StringLen/IsString），Span 由 CalcS7Ranges 结合 DataType 计算后填充。
type S7Address struct {
	Area       s7Area // 存储区域
	DB         int    // DB 号（仅 DB 区有效，其余为 0）
	ByteOffset int    // 字节偏移（0-based）
	BitOffset  int    // 位偏移（0..7），非位地址为 -1
	Width      int    // 地址名隐含宽度：位=0, B=1, W=2, D=4, STRING=0
	StringLen  int    // STRING 显式长度（地址携带，如 DB1.STRING0.50），未指定为 0
	Span       int    // 最终读取跨度（字节数），由 CalcS7Ranges 填充；位=1，STRING=2+长度
	IsString   bool   // 是否 STRING 点位（地址名使用 STRING 语法）
}

// IsBit 是否为位地址（BOOL）
func (a *S7Address) IsBit() bool {
	return a.BitOffset >= 0
}

// S7Range 读取区间计算结果。
// 由 CalcS7Ranges 返回，供 Read 读取整段字节后按偏移解码。
type S7Range struct {
	Area        s7Area
	DB          int
	StartOffset int // 起始字节偏移（含）
	EndOffset   int // 结束字节偏移（不含），区间大小 = EndOffset - StartOffset
	// AddressMap 将 DeviceAddress.ID 映射为 S7Address。
	// 解码函数直接查此 map 避免重复解析 name。
	AddressMap map[string]S7Address
}
