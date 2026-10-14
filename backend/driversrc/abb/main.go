// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

// Command iot-driver-abb 是 ABB 机器人 Robot Web Services (RWS) 协议的进程外插件实现。
//
// ABB 控制器（IRC5 / RobotWare 6 与 OmniCore / RobotWare 7）通过 RWS 暴露
// REST 接口：IO 信号、RAPID 变量、控制器面板状态、运动状态、系统信息都是
// 一条 HTTP GET。本插件把这些资源映射成网关的"地址 → 值"模型，
// 纳入标准的采集 → 推送 → 报警回路。
//
// 本插件自带全部协议实现（见 ./abbdrv）：地址解析、Digest 认证、HAL JSON
// 容错解析、批量索引与限流都在插件进程内，只依赖 iot-gateway/driver 这一份
// 插件与网关共用的契约（Driver 接口、DataType、RangeSig）与插件开发套件
// iot-gateway/driversdk。**没有新增任何 go.mod 依赖**（Digest 与令牌桶都是
// 自实现的，各约几十行）。
//
// 与其它插件的两处结构差异：
//
//  1. 协议名与 scope 是**全新的**（ABB.RWS / abb）—— 这是新协议而非搬迁，
//     没有内置时代的原值要沿用。名字一旦发布就冻结：它同时是
//     iot_protocol.name 与 device.protocol_id 的解析目标，改一个字就让
//     既有设备静默采不到值（钉住它的是 main_test.go 的 TestProtocolIdentityPinned）。
//  2. 底层是 HTTP 而非长连接，因此不实现 SerialExclusive（该接口的方法没写，
//     采集引擎按非独占驱动处理）。连接状态由驱动自维护，见 abbdrv/driver.go。
//
// 构建：
//
//	cd backend && go build -o ./plugins/iot-driver-abb ./driversrc/abb
//
// 注意：Serve 之前不做任何耗时自检。父进程消失时插件靠 stdin EOF 退出，
// 若在调用 Serve 前就卡在自检里，Unix 侧没有兜底机制，会留下孤儿进程。
package main

import (
	"encoding/json"

	"iot-gateway/driversdk"

	// 目录名 abbdrv 与包名 abb 刻意不一致，沿用本仓库其它插件的约定
	// （finsdrv/package fins、s7drv/package s7）：目录名决定产物名与
	// import 路径，包名留给实现。这里必须显式起别名，否则标识符是 abb。
	abbdrv "iot-gateway/driversrc/abb/abbdrv"
)

const (
	// protocolName 协议名，同时是 iot_protocol.name 与 device.protocol_id
	// 的解析目标。新协议（无内置前身），但一旦发布即冻结。
	protocolName = "ABB.RWS"

	// pluginScope 类型注册 scope，与协议名一起冻结。
	pluginScope = "abb"
)

// formRWS ABB RWS 的协议配置表单（form-create schema）。
//
// 形状对齐 driversrc/opcua/main.go 的 formOcUa。字段名与 abbdrv/config.go 的
// rwsConfigRaw 的 json tag 一一对应 —— 表单里的 field 就是 Connect 收到的
// protocolJSON 的键，改一边必须改另一边。
//
// 几个刻意的取舍：
//   - password 保留 props，否则 form-create 会把它渲染成明文输入框；
//   - useHttps / insecureSkipVerify / bulkRead / sessionRequest 用 select 而不是
//     开关：前两者需要"自动"这个第三态（现场不该被迫先知道控制器是哪一代）；
//   - 每一项都显式写了 "value" 默认值。这不是装饰：**密码字段缺失**与
//     **显式空密码**在驱动侧是两种语义（见 abbdrv/config.go 的 Password 指针），
//     表单不预填的话，用户原样提交就会把出厂默认密码 robotics 清掉；
//   - 内部调参（并发上限、回退预算、各类缓存 TTL）**不在这里出现**：
//     它们是驱动的安全边界而非用户配置，暴露出去只会让人在不理解后果的
//     情况下把控制器的限流绕过去。要改就改 abbdrv/config.go 里的常量。
const formRWS = `{
  "rule": [
    {"type":"input","field":"endpoint","title":"端点地址","$required":false,"value":""},
    {"type":"input","field":"host","title":"设备IP","$required":true,"value":""},
    {"type":"inputNumber","field":"port","title":"端口(0=自动)","$required":false,"value":0},
    {"type":"select","field":"useHttps","title":"协议","$required":false,"value":"auto",
     "options":[{"label":"自动","value":"auto"},{"label":"仅HTTPS","value":"true"},{"label":"仅HTTP","value":"false"}]},
    {"type":"select","field":"rwsVersion","title":"RWS版本","$required":false,"value":"auto",
     "options":[{"label":"自动","value":"auto"},{"label":"RWS 1.0 (IRC5/RW6)","value":"1.0"},{"label":"RWS 2.0 (OmniCore/RW7)","value":"2.0"}]},
    {"type":"input","field":"username","title":"用户名","$required":false,"value":"Default User"},
    {"type":"input","field":"password","title":"密码","$required":false,"value":"robotics","props":{"type":"password"}},
    {"type":"inputNumber","field":"timeoutMs","title":"请求超时(ms)","$required":false,"value":5000},
    {"type":"select","field":"insecureSkipVerify","title":"跳过证书校验","$required":false,"value":"true",
     "options":[{"label":"是","value":"true"},{"label":"否","value":"false"}]},
    {"type":"select","field":"bulkRead","title":"批量读取","$required":false,"value":"true",
     "options":[{"label":"开启","value":"true"},{"label":"关闭","value":"false"}]},
    {"type":"select","field":"sessionRequest","title":"创建RWS会话","$required":false,"value":"auto",
     "options":[{"label":"自动","value":"auto"},{"label":"是","value":"true"},{"label":"否","value":"false"}]},
    {"type":"inputNumber","field":"maxRequestsPerSecond","title":"每秒请求上限","$required":false,"value":8},
    {"type":"select","field":"cartesianSource","title":"笛卡尔来源","$required":false,"value":"robtarget",
     "options":[{"label":"robtarget","value":"robtarget"},{"label":"cartesian","value":"cartesian"}]},
    {"type":"input","field":"cartesianTool","title":"工具坐标系","$required":false,"value":"tool0"},
    {"type":"input","field":"cartesianWobj","title":"工件坐标系","$required":false,"value":"wobj0"},
    {"type":"select","field":"cartesianCoordinate","title":"坐标系","$required":false,"value":"Base",
     "options":[{"label":"Base","value":"Base"},{"label":"World","value":"World"},{"label":"Tool","value":"Tool"},{"label":"Wobj","value":"Wobj"}]}
  ],
  "options": {
    "form":{"inline":false,"hideRequiredAsterisk":false,"labelPosition":"right","size":"default","labelWidth":"150px"},
    "resetBtn":{"show":false,"innerText":"重置"},
    "submitBtn":{"show":false,"innerText":"提交"}
  }
}`

// meta 插件的协议清单。等价于内置驱动在 init() 里做的事：
// 驱动注册（由工厂承担）、类型注册、类型映射、协议配置表单。
//
// 单协议、非串行独占：RWS 是 HTTP，不存在"总线上的帧被交错"这回事，
// 并发安全由驱动内部的 cookie jar / 限流器 / digest 计数器各自保证。
func meta() driversdk.Meta {
	return driversdk.Meta{
		APIVersion: driversdk.APIVersion,
		Protocols: []driversdk.Protocol{
			{
				Name:            protocolName,
				SerialExclusive: false,
				Description:     "ABB 机器人 Robot Web Services",
				FormJSON:        json.RawMessage(formRWS),
			},
		},
		Scope:       pluginScope,
		Description: "ABB RWS",
		Version:     "1.0.0",
		Types:       abbdrv.Types(),
		TypeMap:     abbdrv.CommonTypeMap(),
	}
}

// factories 协议名 → 工厂。键必须与 meta().Protocols[].Name 逐一对应，
// 否则 Serve 会在启动时报错（main_test.go 也钉住了这条不变式）。
func factories() driversdk.Factories {
	return driversdk.Factories{
		protocolName: abbdrv.NewDriver,
	}
}

func main() {
	driversdk.Main(factories(), meta(), driversdk.Capabilities{})
}
