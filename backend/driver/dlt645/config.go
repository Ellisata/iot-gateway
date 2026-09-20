// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package dlt645

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// rawInt 容错整数：form-create 的 inputNumber 产出 JSON 数字，
// select 产出的却是字符串（如 "2400"），两种都要能解析。
// 非法值不报错、保持零值，由解析层「仅合法值覆盖默认」的逻辑兜底。
// set 区分「字段缺席」与「显式 0」——对 0 本身有意义的字段（如前导字节数、
// 收发间延时）必须据此判断，否则缺席字段会被误当成 0 而清掉默认值。
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
		return nil
	}
	r.set = true
	r.val = v
	return nil
}

// rawBool 容错布尔：兼容 JSON 布尔（true/false）、数字（1/0）与字符串
// （"true"/"false"/"1"/"0"）。set 区分「字段缺席」与「显式 false」。
type rawBool struct {
	set bool
	val bool
}

func (r *rawBool) UnmarshalJSON(b []byte) error {
	s := strings.ToLower(strings.Trim(strings.TrimSpace(string(b)), `"`))
	if s == "" || s == "null" {
		return nil
	}
	r.set = true
	switch s {
	case "true", "1", "on", "yes", "y", "是":
		r.val = true
	case "false", "0", "off", "no", "n", "否":
		r.val = false
	default:
		r.set = false // 无法识别，视为未配置
	}
	return nil
}

// dlt645ConfigRaw 匹配 protocol_json 原始数据格式。
// 除字符串字段外一律用容错类型，兼容 form-create 各控件产出的 JSON 形态差异。
type dlt645ConfigRaw struct {
	Version      string  `json:"protocolVersion"`
	MeterAddress string  `json:"meterAddress"`
	Host         string  `json:"host"`
	Port         rawInt  `json:"port"`
	ComPort      string  `json:"comPort"`
	BaudRate     rawInt  `json:"baudRate"`
	DataBits     rawInt  `json:"dataBits"`
	StopBits     rawInt  `json:"stopBits"`
	Parity       string  `json:"parity"`
	TimeoutMS    rawInt  `json:"timeoutMs"`
	Timeout      rawInt  `json:"timeout"` // 兼容旧字段名
	InterFrameMS rawInt  `json:"interFrameDelayMs"`
	Preamble     rawInt  `json:"preambleBytes"`
	MaxDIs       rawInt  `json:"maxDIsPerRead"`
	MaxFollowUp  rawInt  `json:"maxFollowUpFrames"`
	CheckCS      rawBool `json:"checkChecksum"`
}

// ParseDLT645Config 从 device.protocol_json JSON 字符串解析 DL/T 645 配置。
// transport 由协议注册名固定（ProtocolDLT645Serial / ProtocolDLT645TCP），
// 覆盖 JSON 中的 transport 字段。未设置或非法的字段使用默认值。
func ParseDLT645Config(protocolJSON, transport string) (*DLT645Config, error) {
	cfg := DefaultDLT645Config(transport)

	if protocolJSON == "" {
		return cfg, nil
	}

	var raw dlt645ConfigRaw
	if err := json.Unmarshal([]byte(protocolJSON), &raw); err != nil {
		return nil, fmt.Errorf("dlt645 config: invalid JSON: %w", err)
	}

	// ---- 协议方言 ----
	// 仅显式 "1997" 切换到 1997 方言，其余（含空、非法、大小写变体）一律回退 2007。
	if strings.TrimSpace(raw.Version) == Version1997 {
		cfg.Version = Version1997
	} else {
		cfg.Version = Version2007
	}
	if addr, ok := normalizeMeterAddress(raw.MeterAddress); ok {
		cfg.MeterAddress = addr
	}

	// ---- 以太网 ----
	if raw.Host != "" {
		cfg.Host = raw.Host
	}
	if raw.Port.val > 0 {
		cfg.Port = raw.Port.val
	}

	// ---- 串口 ----
	// 与 host 一致：字段缺席时保留默认值，而不是清成空串
	// （否则串口设备会丢掉默认的 COM1，直到 Connect 才报「comPort 未配置」）。
	if raw.ComPort != "" {
		cfg.ComPort = raw.ComPort
	}
	if raw.BaudRate.val > 0 {
		cfg.BaudRate = raw.BaudRate.val
	}
	if v := raw.DataBits.val; v == 5 || v == 6 || v == 7 || v == 8 {
		cfg.DataBits = v
	}
	if v := raw.StopBits.val; v == 1 || v == 2 {
		cfg.StopBits = v
	}
	if p := strings.ToUpper(strings.TrimSpace(raw.Parity)); p == "N" || p == "E" || p == "O" {
		cfg.Parity = p
	}

	// ---- 时序 ----
	// 优先标准字段名 timeoutMs，兼容旧字段名 timeout
	switch {
	case raw.TimeoutMS.set && raw.TimeoutMS.val > 0:
		cfg.TimeoutMS = raw.TimeoutMS.val
	case raw.Timeout.set && raw.Timeout.val > 0:
		cfg.TimeoutMS = raw.Timeout.val
	}
	// 超时下限保护：至少覆盖一个典型应答帧的线路时间。
	// 645 表速率普遍偏低（默认 2400bps），若配置的超时小于帧传输时间，
	// 帧会被截断且现象是「偶发解析失败」而非明确的超时，极难排查。
	if floor := minTimeoutMS(cfg.BaudRate, cfg.DataBits, cfg.StopBits); cfg.TimeoutMS < floor {
		cfg.TimeoutMS = floor
	}
	// 收发间延时：0 是合法值（不延时），仅显式配置时覆盖
	if raw.InterFrameMS.set && raw.InterFrameMS.val >= 0 {
		cfg.InterFrameDelayMS = raw.InterFrameMS.val
	}
	cfg.Timeout = time.Duration(cfg.TimeoutMS) * time.Millisecond
	cfg.InterFrameDelay = time.Duration(cfg.InterFrameDelayMS) * time.Millisecond

	// ---- 前导唤醒字节 ----
	// 0 是合法值（关闭前导），仅显式配置时覆盖。
	// 串口默认 4（唤醒），TCP 默认 0（透传链路无需唤醒）。
	if raw.Preamble.set {
		cfg.PreambleBytes = clamp(raw.Preamble.val, 0, maxPreambleBytes)
	}

	// ---- 读优化与容错 ----
	if raw.MaxDIs.set && raw.MaxDIs.val > 0 {
		cfg.MaxDIsPerRead = clamp(raw.MaxDIs.val, 1, maxDIsPerReadLimit)
	}
	if raw.MaxFollowUp.set && raw.MaxFollowUp.val > 0 {
		cfg.MaxFollowUpFrames = clamp(raw.MaxFollowUp.val, 0, maxFollowUpLimit)
	}
	if raw.CheckCS.set {
		cfg.CheckChecksum = raw.CheckCS.val
	}

	return cfg, nil
}

// minTimeoutMS 返回按波特率折算的单帧应答超时下限（毫秒）。
// 以「典型应答帧约 30 字节」估算线路传输时间并留 2 倍余量（表处理 + 换向），
// 下限不低于 200ms。8E1 每字节 11 位（1 起始 + 8 数据 + 1 校验 + 1 停止）。
func minTimeoutMS(baud, dataBits, stopBits int) int {
	if baud <= 0 {
		baud = defaultBaudRate
	}
	if dataBits <= 0 {
		dataBits = defaultDataBits
	}
	if stopBits <= 0 {
		stopBits = defaultStopBits
	}
	const typicalFrameBytes = 30
	const parityBits = 1
	const startBits = 1
	bitsPerByte := startBits + dataBits + parityBits + stopBits
	ms := 2 * typicalFrameBytes * bitsPerByte * 1000 / baud
	if ms < 200 {
		ms = 200
	}
	return ms
}

// normalizeMeterAddress 规范化表号：去除空白与常见分隔符，校验为 1~12 位十进制，
// 左补零到 12 位。返回 false 表示非法（调用方保持默认值）。
func normalizeMeterAddress(s string) (string, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", false
	}
	// 容忍现场常见的书写形式：0000-0000-0001 / 00 00 00 00 00 01
	s = strings.NewReplacer("-", "", " ", "", "\t", "").Replace(s)
	if len(s) > maxMeterDigits {
		return "", false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return "", false
		}
	}
	if len(s) < maxMeterDigits {
		s = strings.Repeat("0", maxMeterDigits-len(s)) + s
	}
	return s, true
}

// clamp 将 v 限制到 [lo, hi]。
func clamp(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
