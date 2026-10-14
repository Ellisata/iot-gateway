// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package cip

import (
	"errors"
	"fmt"

	"github.com/iceisfun/goindustrial/protocol/ethernetip/cip"
)

// cipGeneralStatus 常用 CIP 通用状态码的可读描述（现场日志诊断用）。
var cipGeneralStatus = map[uint8]string{
	0x01: "connection failure (连接失败)",
	0x02: "resource unavailable (资源不足)",
	0x03: "invalid parameter value (参数值非法)",
	0x04: "path segment error (路径段错误)",
	0x05: "path destination unknown (标签路径不存在，常见于标签名/Program 作用域错误或设备不支持该作用域)",
	0x06: "partial transfer (部分传输)",
	0x08: "service not supported (服务不支持)",
	0x09: "invalid attribute value (属性值非法)",
	0x0C: "object state conflict (对象状态冲突)",
	0x14: "attribute not supported (属性不支持)",
	0x15: "too much data (数据过多)",
	0x16: "object does not exist (对象不存在)",
}

// logixExtStatus Logix 常见扩展状态字（首字）的可读描述。
var logixExtStatus = map[cip.UINT]string{
	0x2105: "path segment error",
	0x2106: "path destination unknown (标签不存在或 Program 名错误)",
	0x2107: "path segment error: port (端口段错误)",
	0x2108: "path segment error: link address (链路地址错误)",
}

// encapStatus 常用 EtherNet/IP **封装层**状态码的可读描述。
//
// 与上面的 cipGeneralStatus 是**两层不同的东西**，现场最容易混，因为两者都会以
// "read tag ... failed" 的样子出现在日志里：
//
//	cipGeneralStatus  CIP 报文路由器回的状态 —— 设备**解析了**请求、拒绝执行
//	                  （标签不存在、长度不符…），由 cip.Error 承载，连接仍可用；
//	encapStatus       封装层回的状态 —— 设备**连这条封装命令都没处理**。
//	                  0x006F SendRRData 被拒说明它没有 CIP 报文路由器，
//	                  即根本不支持标签寻址（真机实证：连标准 Identity Object
//	                  的厂商号都读不到）。库随即把它当传输故障、Reset 掉会话。
var encapStatus = map[uint32]string{
	0x01: "invalid or unsupported encapsulation command (设备不接受这条封装命令)",
	0x02: "insufficient memory (资源不足)",
	0x03: "incorrect data (封装头字段不合法)",
	0x64: "invalid session handle (会话句柄非法)",
	0x65: "invalid length (长度字段不合法)",
	0x69: "unsupported protocol revision (封装协议版本不支持)",
}

// describeEncapStatus 返回封装层状态码的可读描述，未知码返回原始十六进制。
func describeEncapStatus(status uint32) string {
	if s, ok := encapStatus[status]; ok {
		return s
	}
	return fmt.Sprintf("unknown encapsulation status 0x%08X", status)
}

// encapError 把"设备在封装层拒绝了命令"与普通传输错误分开。
//
// 为什么必须单独包一层：库的 do() 对传输类错误（封装层拒绝正是这一类 ——
// 它不是 cip.Error）会先 transport.Reset 掉会话、只用 logger.Warn 打出真实原因，
// **最终上抛的只是 "operation failed after N retries" 兜底文案** —— 状态码根本
// 不在错误链里。状态码由 bridgelogger.go 的 encapTap 从那条 Warn 里截出来，
// 再由 client.go 在持锁的调用点裹进这里（见 encapTap 的说明与退路）。
type encapError struct {
	status uint32
	err    error
}

func (e *encapError) Error() string {
	// 半角括号与 describeCIPError 一致（同一层输出的措辞不另立风格）。
	return fmt.Sprintf("%v (封装层 status 0x%02X: %s)", e.err, e.status, describeEncapStatus(e.status))
}

func (e *encapError) Unwrap() error { return e.err }

// encapStatusOf 在错误链上取封装层状态码。
func encapStatusOf(err error) (uint32, bool) {
	var ee *encapError
	if errors.As(err, &ee) {
		return ee.status, true
	}
	return 0, false
}

// describeCIPStatus 返回 CIP 通用状态码的可读描述，未知码返回原始十六进制。
func describeCIPStatus(status cip.USINT) string {
	if s, ok := cipGeneralStatus[uint8(status)]; ok {
		return s
	}
	return fmt.Sprintf("unknown status 0x%02X", uint8(status))
}

// describeCIPError 从错误链提取 cip.Error 并附加状态码可读描述，
// 便于现场日志直接区分"标签不存在"与"路径非法"等原因；非 CIP 状态错误原样返回。
func describeCIPError(err error) string {
	if err == nil {
		return ""
	}
	var cipErr cip.Error
	if !asCIPError(err, &cipErr) {
		return err.Error()
	}
	if len(cipErr.ExtStatus) == 0 {
		return fmt.Sprintf("%v (%s)", err, describeCIPStatus(cipErr.Status))
	}
	if desc, ok := logixExtStatus[cipErr.ExtStatus[0]]; ok {
		return fmt.Sprintf("%v (%s; ext=%04X %s)", err, describeCIPStatus(cipErr.Status), cipErr.ExtStatus[0], desc)
	}
	return fmt.Sprintf("%v (%s; ext=%04X)", err, describeCIPStatus(cipErr.Status), cipErr.ExtStatus[0])
}
