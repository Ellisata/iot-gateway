// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

// Package discovery 把网关"发布"到局域网：周期性以 UDP 广播发出自身身份，
// 并单播应答管理平台发来的探询，使其他管理平台无需预配置、无需凭据即可
// 自动发现本网关并接入。
//
// 报文格式是**对外契约**（管理平台要照着它实现接收端），完整说明见
// specs/discovery.md；本文件是那份契约的唯一真源。
//
// 能力边界：只做单向发布与探询应答。收到别的网关的广播一律忽略 ——
// "局域网里有哪些网关"那份名单是管理平台的事，网关不需要维护它。
package discovery

import "encoding/json"

// 报文识别与协议版本。
//
// protocol 用来把本协议的报文从该端口上的陌生流量里认出来（9083 不是保留端口，
// 同一网段上跑别的程序占用了它完全可能）；version 决定要不要处理 ——
// 收到不认识的版本一律静默丢弃，而不是"尽力解析出认识的字段"，
// 这样将来 v2 才有可能在不破坏既有平台的前提下共存。
const (
	// Protocol 报文标识，接收方据此过滤该端口上的非本协议流量。
	Protocol = "iot-gateway-discovery"
	// ProtocolVersion 当前协议版本。
	ProtocolVersion = 1
)

// 报文类型。
const (
	// TypeNotify 网关 → 子网广播地址：周期发布"我是谁"。
	TypeNotify = "notify"
	// TypeProbe 管理平台 → 广播：探询"谁在？"。
	TypeProbe = "probe"
	// TypeReply 网关 → 单播回探询源：应答 TypeProbe。
	TypeReply = "reply"
)

// maxDatagram 单条报文的读取缓冲上限。
//
// 定长读取是刻意的：绝不让对端用报文长度决定本机的内存分配。
// 2000 字节远大于实际报文（描述字段上限 500 字符），留余量是为了将来加字段时
// 不必同步改这里 —— 超长报文会被 ReadFrom 静默截断，随后在 decode 阶段
// 因 JSON 不完整而丢弃。
const maxDatagram = 2048

// Message 发现报文。notify / reply 携带完整身份，probe 只带协议头。
//
// 字段名是对外契约的一部分（管理平台按这些键解析），改动等同于破坏兼容：
// **新增字段请一律带 omitempty**，让旧版接收方仍能解析出它认识的部分。
type Message struct {
	Protocol string `json:"protocol"`
	Version  int    `json:"version"`
	Type     string `json:"type"`

	// GatewayID 网关唯一标识（iot_gateway.id）。管理平台据此把同一台网关的
	// notify 与 reply 归并成一条记录，也是它区别于其他网关的主键。
	GatewayID string `json:"gatewayId,omitempty"`
	// Name 网关名称。
	Name string `json:"name,omitempty"`
	// IP 本端地址。notify 中填的是**本次广播所用网卡的地址**（即"这个网段上
	// 我在哪"），与接收方从哪个网段收到这条报文正好对应；reply 中填的是
	// 路由选出的、探询方能回连的地址。
	//
	// **接收方应以 UDP 源地址为准**：源地址由内核填写、无法在应用层伪造，
	// 而本字段只是应用层写的一个参考值（也确实是参考值 —— 取不到本地地址时
	// 它就是空的）。
	IP string `json:"ip,omitempty"`
	// Port 网关 HTTP 服务端口。注意是**当前运行值**，不是数据库里那一列
	//（那列只在首次启动时写入，改过 server.port 之后它就不再准了）。
	Port int `json:"port,omitempty"`
	// FirmwareVersion 网关软件版本。
	FirmwareVersion string `json:"firmwareVersion,omitempty"`
	// Description 网关描述。
	Description string `json:"description,omitempty"`
	// ApiKey 默认开放接口密钥（open_api_secret 里名为 default 那一条的内容）。
	//
	// 管理平台据此**免人工录入**直接调用本网关的开放接口 —— 这正是本字段存在的
	// 理由。代价要说清楚：这是一条无认证的广播通道，凡是能收到本网段广播的主机
	// 都能读到这个密钥。这是刻意的取舍，不是疏漏（见 specs/discovery.md §1）；
	// 也正因为已经让出了一个凭据，**这条通道上不得再出现任何其他敏感字段**。
	//
	// 没有默认密钥（老库未升级、被人为删库）时留空，接收方需自行处理这种情况。
	ApiKey string `json:"apiKey,omitempty"`
	// Timestamp 发出时刻的 Unix 毫秒。
	//
	// **仅供参考，接收方不应信任**：它只用于日志排查与粗略判断新鲜度，
	// 既不参与校验也不参与排序 —— 跨主机的时钟不可比，拿它做超时判定
	// 会把时钟慢的那台网关判成"下线"。
	Timestamp int64 `json:"timestamp,omitempty"`
}

// encode 序列化报文。只有 json.Marshal 失败时才返回错误 —— Message 全是基本
// 类型，现实中不会失败，保留 error 只是不去吞掉它。
func encode(m Message) ([]byte, error) {
	return json.Marshal(m)
}

// decode 解析并校验报文。
//
// 返回 (Message, bool) 而不是 (Message, error)：该端口上出现畸形报文、
// 陌生协议、将来版本的报文都是**常态而非异常**，而调用方对这三种情况的处置
// 完全一样（丢弃、继续读）。给个 error 只会诱使调用方去区分它们，
// 从而为"某种解析失败"写出无意义的特殊分支。
func decode(b []byte) (Message, bool) {
	var m Message
	// Unmarshal 对未知字段是宽容的（直接忽略），这正是版本演进的余地：
	// 旧接收方收到带新字段的报文也能解析出它认识的部分。
	if err := json.Unmarshal(b, &m); err != nil {
		return Message{}, false
	}
	// 先认协议再认版本：别的程序占用同一端口时，其报文通常连 protocol 都对不上，
	// 这一步就把它们挡掉了，不必等到版本判断。
	if m.Protocol != Protocol || m.Version != ProtocolVersion {
		return Message{}, false
	}
	if m.Type == "" {
		return Message{}, false
	}
	return m, true
}
