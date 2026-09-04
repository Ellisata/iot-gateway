// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package fins

import (
	"encoding/json"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"
)

// FINSConfig FINS 协议配置（对应 device.protocol_json 反序列化）。
// 传输层由协议注册名固定（Omron.Net.FINS.UDP / TCP / Serial），
// transport 字段保留仅作兼容，实际以注册名为准。
type FINSConfig struct {
	Transport string `json:"transport"` // 传输层（由协议注册名固定，此字段值被覆盖）
	Host      string `json:"host"`      // 以太网：PLC IP 地址
	Port      int    `json:"port"`      // 以太网端口，UDP/TCP 默认 9600
	DstNode   byte   `json:"dstNode"`   // DA1：PLC 节点号（未配置时 UDP/TCP 自动取 Host IP 末段）
	SrcNode   byte   `json:"srcNode"`   // SA1：网关节点号
	DstUnit   byte   `json:"dstUnit"`   // DA2：目标单元号，默认 0（CPU）
	SrcUnit   byte   `json:"srcUnit"`   // SA2：源单元号，默认 0

	ComPort  string `json:"comPort"`  // 串口：名称，如 "COM1"、"/dev/ttyUSB0"
	BaudRate int    `json:"baudRate"` // 串口波特率，默认 9600
	DataBits int    `json:"dataBits"` // 数据位（5/6/7/8），默认 8
	StopBits int    `json:"stopBits"` // 停止位（1 或 2），默认 1
	Parity   string `json:"parity"`   // 校验位："N"=无、"E"=偶、"O"=奇，默认 "N"
	UnitNo   byte   `json:"unitNo"`   // Host Link 单元号（0..31）
	FCSMode  string `json:"fcsMode"`  // Host Link FCS 计算窗口：FULL=含@(手册标准) / BODY=仅hex体 / NOSID=含@但排除SID(默认，真机核实)

	TimeoutMS    int           `json:"timeoutMs"`    // 超时毫秒数，默认 5000
	Timeout      time.Duration `json:"-"`            // 超时时间（由 TimeoutMS 转换）
	MergeWindow  uint16        `json:"mergeWindow"`  // 自动模式跨段合并最大窗口（字），默认 100
	MaxReadWords int           `json:"maxReadWords"` // 单请求最大读取字数（FINS 分块），默认 100
	StringLen    int           `json:"stringLen"`    // string 类型单点读取字节数，默认 16
	ByteOrder    string        `json:"byteOrder"`    // 字节序：BIG_ENDIAN / LITTLE_ENDIAN
	WordOrder    string        `json:"wordOrder"`    // 字序（32/64 位）：BIG_ENDIAN / LITTLE_ENDIAN
}

// DefaultFINSConfig 返回默认 FINS 配置
func DefaultFINSConfig() *FINSConfig {
	return &FINSConfig{
		Transport:    TransportUDP,
		Host:         "127.0.0.1",
		Port:         defaultPort,
		ComPort:      "COM1",
		BaudRate:     9600,
		DataBits:     8,
		StopBits:     1,
		Parity:       "N",
		FCSMode:      fcsModeNoSID,
		TimeoutMS:    defaultTimeoutMS,
		MergeWindow:  defaultMergeWindow,
		MaxReadWords: defaultMaxReadWords,
		StringLen:    defaultStringLen,
		ByteOrder:    ByteOrderBigEndian,
		WordOrder:    WordOrderBigEndian,
	}
}

// finsConfigRaw 匹配 protocol_json 原始数据格式（port 为数字，其余值为字符串）
type finsConfigRaw struct {
	Transport    string `json:"transport"`
	Host         string `json:"host"`
	Port         int    `json:"port"`
	DstNode      string `json:"dstNode"`
	SrcNode      string `json:"srcNode"`
	DstUnit      string `json:"dstUnit"`
	SrcUnit      string `json:"srcUnit"`
	ComPort      string `json:"comPort"`
	BaudRate     string `json:"baudRate"`
	DataBits     string `json:"dataBits"`
	StopBits     string `json:"stopBits"`
	Parity       string `json:"parity"`
	UnitNo       string `json:"unitNo"`
	FCSMode      string `json:"fcsMode"`
	Timeout      string `json:"timeout"` // 兼容字段名
	TimeoutMS    string `json:"timeoutMs"`
	MergeWindow  string `json:"mergeWindow"`
	MaxReadWords string `json:"maxReadWords"`
	StringLen    string `json:"stringLen"`
	ByteOrder    string `json:"byteOrder"`
	WordOrder    string `json:"wordOrder"`
}

// ParseFINSConfig 从 device.protocol_json JSON 字符串解析 FINS 配置。
// 未设置的字段使用默认值，非法数值容错回退默认值。
func ParseFINSConfig(protocolJSON string) (*FINSConfig, error) {
	cfg := DefaultFINSConfig()

	if protocolJSON == "" {
		return cfg, nil
	}

	// 先解析为原始格式，校验 JSON 合法性
	var raw finsConfigRaw
	if err := json.Unmarshal([]byte(protocolJSON), &raw); err != nil {
		return nil, fmt.Errorf("fins config: invalid JSON: %w", err)
	}

	// 如果 JSON 为空，直接返回默认值
	if raw == (finsConfigRaw{}) {
		return cfg, nil
	}

	// 传输方式：仅接受 UDP / TCP / Serial，非法值回退默认 UDP
	if raw.Transport != "" {
		switch strings.ToUpper(raw.Transport) {
		case TransportTCP, TransportSerial:
			cfg.Transport = strings.ToUpper(raw.Transport)
		default:
			cfg.Transport = TransportUDP
		}
	}
	cfg.Host = raw.Host
	if raw.Port > 0 {
		cfg.Port = raw.Port
	}
	if raw.DstNode != "" {
		if v, err := strconv.Atoi(raw.DstNode); err == nil && v >= 0 && v <= 254 {
			cfg.DstNode = byte(v)
		}
	} else if cfg.Transport == TransportUDP || cfg.Transport == TransportTCP {
		// 未显式配置时按 FINS 惯例自动取目标 Host IP 末段（仅以太网传输；
		// Serial/HostLink 不使用 DA1，见 resolveDstNode 注释）
		cfg.DstNode = resolveDstNode(cfg.Host)
	}
	if raw.SrcNode != "" {
		if v, err := strconv.Atoi(raw.SrcNode); err == nil && v >= 0 && v <= 254 {
			cfg.SrcNode = byte(v)
		}
	}
	if raw.DstUnit != "" {
		if v, err := strconv.Atoi(raw.DstUnit); err == nil && v >= 0 && v <= 255 {
			cfg.DstUnit = byte(v)
		}
	}
	if raw.SrcUnit != "" {
		if v, err := strconv.Atoi(raw.SrcUnit); err == nil && v >= 0 && v <= 255 {
			cfg.SrcUnit = byte(v)
		}
	}

	// 串口配置
	cfg.ComPort = raw.ComPort
	if raw.BaudRate != "" {
		if v, err := strconv.Atoi(raw.BaudRate); err == nil && v > 0 {
			cfg.BaudRate = v
		}
	}
	if raw.DataBits != "" {
		if v, err := strconv.Atoi(raw.DataBits); err == nil && (v == 5 || v == 6 || v == 7 || v == 8) {
			cfg.DataBits = v
		}
	}
	if raw.StopBits != "" {
		if v, err := strconv.Atoi(raw.StopBits); err == nil && (v == 1 || v == 2) {
			cfg.StopBits = v
		}
	}
	if raw.Parity != "" {
		p := strings.ToUpper(raw.Parity)
		if p == "N" || p == "E" || p == "O" {
			cfg.Parity = p
		}
	}
	if raw.UnitNo != "" {
		if v, err := strconv.Atoi(raw.UnitNo); err == nil && v >= 0 && v <= 31 {
			cfg.UnitNo = byte(v)
		}
	}
	if raw.FCSMode != "" {
		switch {
		case strings.EqualFold(raw.FCSMode, fcsModeFull):
			cfg.FCSMode = fcsModeFull
		case strings.EqualFold(raw.FCSMode, fcsModeBody):
			cfg.FCSMode = fcsModeBody
		case strings.EqualFold(raw.FCSMode, fcsModeNoSID):
			cfg.FCSMode = fcsModeNoSID
		default:
			cfg.FCSMode = fcsModeNoSID // 未识别值回退默认（与 DefaultFINSConfig 一致）
		}
	}

	// 超时：优先标准字段名 timeoutMs，兼容旧字段名 timeout
	if raw.TimeoutMS != "" {
		if v, err := strconv.Atoi(raw.TimeoutMS); err == nil && v > 0 {
			cfg.TimeoutMS = v
		}
	} else if raw.Timeout != "" {
		if v, err := strconv.Atoi(raw.Timeout); err == nil && v > 0 {
			cfg.TimeoutMS = v
		}
	}
	if raw.MergeWindow != "" {
		if v, err := strconv.Atoi(raw.MergeWindow); err == nil && v > 0 {
			cfg.MergeWindow = uint16(v)
		}
	}
	if raw.MaxReadWords != "" {
		if v, err := strconv.Atoi(raw.MaxReadWords); err == nil && v > 0 {
			cfg.MaxReadWords = v
		}
	}
	if raw.StringLen != "" {
		if v, err := strconv.Atoi(raw.StringLen); err == nil && v > 0 {
			cfg.StringLen = v
		}
	}
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

	// 设置 Timeout
	if cfg.TimeoutMS <= 0 {
		cfg.TimeoutMS = defaultTimeoutMS
	}
	cfg.Timeout = time.Duration(cfg.TimeoutMS) * time.Millisecond

	return cfg, nil
}

// ParseFINSConfigRaw 直接从字节解析
func ParseFINSConfigRaw(data []byte) (*FINSConfig, error) {
	return ParseFINSConfig(string(data))
}

// resolveDstNode 解析 FINS 帧 DA1 目标节点号的默认值（UDP/TCP 传输使用）。
//
// FINS 以太网惯例：节点号 = IP 末段（PLC 以太网单元开启自动节点分配时的默认映射）。
// 仅在未显式配置 dstNode 时调用，显式配置优先。Host 为域名或非 IPv4 时无法推导，
// 保持默认 0（FINS 帧 DA1 允许为 0，真机核实此类设备可正常响应）。
func resolveDstNode(host string) byte {
	if host == "" {
		return 0
	}
	if ip4 := net.ParseIP(host).To4(); ip4 != nil {
		// 0/255 为保留值，非合法节点号（1..254）
		if n := ip4[3]; n != 0 && n != 255 {
			return n
		}
	}
	return 0
}
