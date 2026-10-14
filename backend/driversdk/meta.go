// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

// Package driversdk 是进程外协议驱动插件的开发套件。
//
// 一个插件是一个独立的可执行文件，通过 stdin/stdout 与网关做长度前缀的
// JSON-RPC 通信（帧编解码见 iot-gateway/pluginwire）。插件作者只需实现
// driver.Driver 接口并调用 Main 一行注册：
//
//	func main() {
//	    driversdk.Main(
//	        driversdk.Factories{
//	            "Acme.PLC.TCP":    func() driver.Driver { return &acmeTCP{} },
//	            "Acme.PLC.Serial": func() driver.Driver { return &acmeSerial{} },
//	        },
//	        driversdk.Meta{
//	            APIVersion: driversdk.APIVersion,
//	            Scope:      "acme",   // 见 Scope 字段说明
//	            Protocols: []driversdk.Protocol{
//	                {Name: "Acme.PLC.TCP"},
//	                {Name: "Acme.PLC.Serial", SerialExclusive: true},
//	            },
//	            Types:   []*driver.DataType{ ... },
//	            TypeMap: map[string]string{"Boolean": "bool"},
//	        },
//	        driversdk.Capabilities{ ... },   // 扫描能力实现，不需要就传零值
//	    )
//	}
//
// 一个插件可以提供多个协议，它们共用同一个 Scope 的类型表
// （如 Modbus 的 TCP 与 RTU 共用 scope "modbus"，但只有 RTU 独占串行总线）。
//
// 本包只依赖 iot-gateway/pluginwire、iot-gateway/driver、
// iot-gateway/model/po 与标准库，以便插件的独立 module 能以最小成本依赖它。
//
// 本包只描述**驱动插件**的语义；与具体插件种类无关的传输层在
// iot-gateway/pluginwire，网关侧的宿主在 iot-gateway/pluginhost。
package driversdk

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"iot-gateway/driver"
)

// APIVersion 当前线协议版本。网关拒绝版本不匹配的插件，
// 避免用旧套件构建的插件在网关升级后静默错配。
//
// v1 → v2：帧信封由 JSON 改为二进制，read 的载荷（ReadParams/ReadReply）
// 亦改为二进制。**升级网关后旧插件二进制必须重新编译**，替换二进制即热升级。
// 版本判据由本常量承担（pluginhost/process.go 调 meta.Validate）；
// 帧魔数 IGP1 → IGP2 是同一件事的第二层，见 pluginwire.FrameMagic。
//
// v2 → v3：read 应答的载荷新增 driver.ReadResult.QualityCause 一维（采集质量异常的
// 成因）。该载荷是**位置式**二进制，解码端又用 Done() 强制精确消费字节，故新旧两版
// 载荷无法互相解读 —— 必须整体重编，否则握手期就会因版本不符被拒。
// 本次只动载荷，帧魔数仍为 IGP2。
//
// v3 → v4：v2→v3 引入的那一维**又被删掉了** —— 成因改为并进 driver.ReadResult.Quality
// 这个整数（见 driver.QualityXxx：类别在高 2 位、成因在低 6 位），不再单独过一个
// 字符串字段。同样是位置式载荷，同样新旧无法互相解读，同样必须整体重编。
// 帧魔数依旧保持 IGP2。
const APIVersion = 4

// Meta 描述插件提供的协议。等价于进程内驱动在 init() 中所做的全部事情：
// 驱动注册、类型注册、类型映射、协议配置表单。由 describe 一次性上报网关。
type Meta struct {
	// APIVersion 线协议版本，应填 APIVersion 常量。
	APIVersion int `json:"apiVersion"`

	// Protocols 本插件提供的协议。**一个插件可提供多个协议**，
	// 它们共用同一个 Scope 的类型注册
	// （如 Modbus 的 TCP 与 RTU 共用 scope "modbus"，但只有 RTU
	// 独占串行总线，因此 SerialExclusive 是每协议独立的）。
	// 跨插件全局唯一。内置协议已全部搬成插件，所以原先那条"不得与内置
	// 协议重名"的约束随之消失 —— 现在只剩下"不得与另一个插件重名"。
	Protocols []Protocol `json:"protocols"`

	// Scope 类型注册 scope 前缀（TypeRegistry.ForProtocol 用）。
	//
	// **沿用原 scope 是允许且推荐的做法**：内置基线已随内置驱动一起清空
	// （见 driver/dataTypeMap.go），原先那条"必须是全新前缀"的约束随之反转
	// —— 沿用原值才能让既有 iot_protocol 行与 device.protocol_id 无需迁移。
	//
	// 唯一仍然成立的约束：**不得与另一个已注册的插件 scope 撞名（含大小写
	// 变体）**。两个插件共用 scope 时，它们的类型条目落在同一个命名空间里
	// 互相覆盖，症状是「某台设备的点位永远采不到值」，而两个插件的加载日志
	// 上都写着成功 —— 见 driver.RegisterScope 的说明。
	Scope string `json:"scope"`

	// Description 插件说明。Protocol 未单独指定描述时用作各协议行的
	// iot_protocol.description。
	Description string `json:"description"`

	// Version 插件自身版本号，仅用于诊断与日志。
	Version string `json:"version"`

	// Types 本插件注册的内部类型（裸名），由全部协议共用。
	// 仅 name/kind/size 过线，编解码函数由插件进程内部使用、不出进程
	// （见 driver.DataType 的序列化契约）。
	Types []*driver.DataType `json:"types"`

	// TypeMap 通用/扩展类型名 → 内部裸名，由全部协议共用。
	// key 使用 driver.CommonDataTypes 中的规范名者会出现在类型下拉的
	// 「通用」组并按全局展示顺序排列；其余进「扩展」组按字母序。
	// 约束：每个 value 必须出现在 Types 中。
	TypeMap map[string]string `json:"typeMap"`
}

// Protocol 插件提供的一个协议。
type Protocol struct {
	// Name driver.Register 名，也是 iot_protocol.name。
	Name string `json:"name"`

	// SerialExclusive 该协议的底层连接是否独占串行总线（如 RS-485），
	// 决定采集引擎的设备锁粒度，语义见 driver.SerialExclusive。
	// TCP 类协议为 false，串口类为 true。
	SerialExclusive bool `json:"serialExclusive"`

	// Description 该协议的说明，写入 iot_protocol.description；
	// 留空时回退到 Meta.Description。
	Description string `json:"description,omitempty"`

	// FormJSON 该协议的配置表单（form-create schema），原样写入
	// iot_protocol.form_json。形状可参考
	// database/sqlite/migrations/013_add_protocol_20260831.sql。
	//
	// **按协议而非按插件**：同一插件的不同协议配置项通常不同
	//（Modbus TCP 填 host/port，RTU 填串口号/波特率）。
	FormJSON json.RawMessage `json:"formJson,omitempty"`

	// Discovery 该协议的**局域网扫描发现**能力。nil = 不支持扫描，
	// 该协议不会出现在前端扫描页的协议下拉里。
	//
	// 声明了它就必须在 driversdk.Probers 里给出对应实现，否则 Serve 启动即失败
	//（与"每个协议都必须有工厂"是同一条校验，见 serve.go）。
	//
	// 这是**可选字段**，所以它的引入不递增 APIVersion：新网关 + 旧插件走 nil
	// 分支（照旧不能被扫描），旧网关 + 新插件忽略这个未知字段。递增版本号会让
	// 全部旧插件二进制在网关升级后失效，是一次没必要的破坏性升级。
	//
	// 串口协议不能声明它：扫描的目标模型是 host/port，表达不了 comPort，
	// 而"串口扫描"本身也不成立（一条 RS-485 总线上有什么设备，只能逐站号问，
	// 与 IP 网段无关）。Meta.Validate 会拒绝这种组合。
	Discovery *driver.DiscoverySpec `json:"discovery,omitempty"`

	// PointDiscovery 该协议的**点位范围扫描**能力。nil = 不支持，
	// 该协议的设备在「设备地址标签」页上不会出现「自动发现点位」按钮。
	//
	// 声明了它就必须在 Capabilities.PointFinders 里给出对应实现，否则启动即失败。
	//
	// **与 Discovery 相反，串口协议可以（且应当）声明它** —— 这条不对称是刻意的：
	// Discovery 的目标是"网段里的一个 IP"，与串口无关；而点位扫描的目标是
	// **一台已存在的设备**，串口设备同样有"它有哪些点位"这个问题，
	// 且串口恰恰是会话复用最重要的场景。详见 specs/point-discovery.md。
	PointDiscovery *driver.PointDiscoverySpec `json:"pointDiscovery,omitempty"`
}

// Describe 返回该协议写入 iot_protocol.description 的文本。
func (p Protocol) Describe(fallback string) string {
	if p.Description != "" {
		return p.Description
	}
	return fallback
}

// Validate 校验 Meta 可安全用于握手与网关侧注册。
//
// 首要目的是把「DataType 少了 json:"-" 标签」变成一条明确的启动失败，
// 否则它会在握手期报 `json: unsupported type: func(...)`，完全不指向根因。
//
// 网关侧在注册前也会调用它，因此约束是双向强制的。
func (m Meta) Validate() error {
	// 表单类 RawMessage 的合法性必须排在序列化检查**之前**。
	//
	// 一份写坏的表单（手写 form-create schema 时少一个引号）会让下面的
	// json.Marshal 先失败，而那条错误的文案是"DataType 的 Decode/Encode/Format
	// 必须带 json:\"-\" 标签" —— 把作者引向一个与表单毫无关系的地方，
	// 而真正的根因只是某个 field 拼错。宁可在这一层多花几行。
	if err := validateForms(m); err != nil {
		return err
	}
	if _, err := json.Marshal(m); err != nil {
		return fmt.Errorf("driversdk: Meta 无法序列化（DataType 的 Decode/Encode/Format 必须带 json:\"-\" 标签）: %w", err)
	}
	if m.APIVersion != APIVersion {
		return fmt.Errorf("driversdk: APIVersion = %d, 本套件为 %d", m.APIVersion, APIVersion)
	}
	if len(m.Protocols) == 0 {
		return errors.New("driversdk: Protocols 不能为空")
	}
	seenProto := make(map[string]string, len(m.Protocols))
	for _, p := range m.Protocols {
		if p.Name == "" {
			return errors.New("driversdk: Protocols 中存在空的 Name")
		}
		// 同 Scope：协议名首尾空白会让 driver.RegisterExternal 用带空白的键注册，
		// 而 ScopeOf（按小写化协议名查 scope）查不到它 —— 采集照跑，类型下拉坏掉。
		if strings.TrimSpace(p.Name) != p.Name {
			return fmt.Errorf("driversdk: 协议名 %q 首尾有空白字符", p.Name)
		}
		lower := strings.ToLower(p.Name)
		if prev, ok := seenProto[lower]; ok {
			return fmt.Errorf("driversdk: 协议名 %q 与 %q 仅大小写不同", prev, p.Name)
		}
		seenProto[lower] = p.Name
	}
	if m.Scope == "" {
		return errors.New("driversdk: Scope 不能为空")
	}
	// 首尾空白一律拒绝，而不是替作者 trim 掉。
	//
	// driver.RegisterScope 内部会 TrimSpace 后再存，而网关侧到处都用 Meta 里的
	// 原样值（ForProtocol 拼前缀、UnregisterScope 摘除、ScopeOf 查询），两边一
	// 错位就是：类型注册到了 "acme .bool"、scope 表记的是 "acme"，前端类型下拉
	// 全空；插件文件被移除时 UnregisterScope("acme ") 又摘不掉（精确匹配），
	// 条目永久泄漏，再放回去就永远撞 "already registered"，只能重启网关。
	// 在启动期报错，比让作者去猜「为什么插件再也加载不上了」便宜得多。
	if strings.TrimSpace(m.Scope) != m.Scope {
		return fmt.Errorf("driversdk: Scope %q 首尾有空白字符", m.Scope)
	}
	if len(m.Types) == 0 {
		return errors.New("driversdk: Types 不能为空")
	}
	if len(m.TypeMap) == 0 {
		return errors.New("driversdk: TypeMap 不能为空")
	}

	for i, dt := range m.Types {
		if dt == nil {
			return fmt.Errorf("driversdk: Types[%d] 为 nil", i)
		}
		if dt.Name == "" {
			return fmt.Errorf("driversdk: Types[%d].Name 不能为空", i)
		}
		if dt.Kind == "" {
			return fmt.Errorf("driversdk: Types[%d] (%s).Kind 不能为空", i, dt.Name)
		}
	}

	// TypeMap 的每个 value 必须能在 Types 中找到。
	// 悬空映射会被网关的地址校验接受、写进设备地址，然后在 Read 里解析失败。
	//
	// 按大小写不敏感比对：注册与查询链路（driver.TypeRegistry.Register /
	// ProtocolScope.Get）一律 ToLower，插件里写成 "Int16" 而在 TypeMap 里写
	// "int16" 是完全可用的组合，这里不该把它判成悬空 —— 那会拒绝一个本该正常
	// 加载的插件，且报错指向的"根因"根本不成立。
	known := make(map[string]struct{}, len(m.Types))
	for _, dt := range m.Types {
		known[strings.ToLower(dt.Name)] = struct{}{}
	}
	dangling := make([]string, 0)
	for name, internal := range m.TypeMap {
		if _, ok := known[strings.ToLower(internal)]; !ok {
			dangling = append(dangling, fmt.Sprintf("%s→%s", name, internal))
		}
	}
	if len(dangling) > 0 {
		sort.Strings(dangling)
		return fmt.Errorf("driversdk: TypeMap 指向了未在 Types 中声明的内部类型: %v", dangling)
	}
	for _, p := range m.Protocols {
		if err := validateDiscovery(p); err != nil {
			return err
		}
		if err := validatePointDiscovery(p, m.TypeMap); err != nil {
			return err
		}
	}
	return nil
}

// 点位扫描的取值区间。
//
//	下界 500ms：再短连一次分块读取都完不成；
//	上界 30s：留给"串口 + 大范围"的空间，但注意**网关侧还会再钳一次**
//	（pluginhost 的 pointScanGatewayBudgetCapMs，因为这条链路是同步 HTTP）。
const (
	pointBudgetFloor    = 500
	pointBudgetCeiling  = 30000
	pointMaxPointsFloor = 1
	pointMaxPointsCeil  = 20000
)

// validatePointDiscovery 校验一个协议的点位扫描声明。
//
// **与 validateDiscovery 分开写、而不是合并**：两者有一条关键的不对称 ——
// Discovery 拒绝串口协议，PointDiscovery 则允许甚至鼓励串口声明。
// 合并成一个函数后，那条不对称会变成两个 if 之间的微妙差别，读代码的人
// 只会看到"同样是扫描能力，为什么一个禁串口一个不禁"，然后把它改齐。
// 分开写 + 各自写明理由，才能让这条差异活下来。
func validatePointDiscovery(p Protocol, typeMap map[string]string) error {
	d := p.PointDiscovery
	if d == nil {
		return nil
	}
	if strings.TrimSpace(d.Title) == "" {
		return fmt.Errorf("driversdk: 协议 %q 声明了 PointDiscovery 但 Title 为空", p.Name)
	}
	if d.MaxBudgetMs < pointBudgetFloor || d.MaxBudgetMs > pointBudgetCeiling {
		return fmt.Errorf("driversdk: 协议 %q 的 PointDiscovery.MaxBudgetMs = %d，应在 [%d,%d]（毫秒）",
			p.Name, d.MaxBudgetMs, pointBudgetFloor, pointBudgetCeiling)
	}
	if d.MaxPoints < pointMaxPointsFloor || d.MaxPoints > pointMaxPointsCeil {
		return fmt.Errorf("driversdk: 协议 %q 的 PointDiscovery.MaxPoints = %d，应在 [%d,%d]",
			p.Name, d.MaxPoints, pointMaxPointsFloor, pointMaxPointsCeil)
	}
	// DefaultCommonDataType 是前端下拉的初值，必须是**通用类型名**，
	// 即 TypeMap 的 key（不是 value —— value 是内部裸名，前端不认识）。
	// 写错的症状是"下拉默认选中项是空的"，用户得自己挑一次，不报任何错。
	if d.DefaultCommonDataType == "" {
		return fmt.Errorf("driversdk: 协议 %q 的 PointDiscovery.DefaultCommonDataType 不能为空", p.Name)
	}
	if _, ok := typeMap[d.DefaultCommonDataType]; !ok {
		return fmt.Errorf("driversdk: 协议 %q 的 PointDiscovery.DefaultCommonDataType = %q 不在 TypeMap 里"+
			"（它必须是通用类型名，即 TypeMap 的 key）", p.Name, d.DefaultCommonDataType)
	}
	return nil
}

// validateForms 校验各协议的表单字段是合法 JSON。
//
// 单列一个函数是为了让它能排在 Meta 的序列化检查之前执行，理由见 Validate。
func validateForms(m Meta) error {
	for _, p := range m.Protocols {
		if len(p.FormJSON) > 0 && !json.Valid(p.FormJSON) {
			return fmt.Errorf("driversdk: 协议 %q 的 FormJSON 不是合法 JSON", p.Name)
		}
		// 两个能力各自独立判，**不能写 if p.Discovery == nil { continue }** ——
		// 那会让"没声明 Discovery、只声明了 PointDiscovery"的协议（串口协议正是
		// 这一形态，因为 Discovery 对串口是禁用的）跳过下面那段校验，
		// 于是一份写坏的扫描表单会一路走到 json.Marshal 才炸，
		// 而那条错误的文案是"DataType 必须带 json:\"-\" 标签"，指向完全错误的地方。
		if p.Discovery != nil && len(p.Discovery.FormJSON) > 0 && !json.Valid(p.Discovery.FormJSON) {
			return fmt.Errorf("driversdk: 协议 %q 的 Discovery.FormJSON 不是合法 JSON", p.Name)
		}
		if p.PointDiscovery != nil && len(p.PointDiscovery.FormJSON) > 0 && !json.Valid(p.PointDiscovery.FormJSON) {
			return fmt.Errorf("driversdk: 协议 %q 的 PointDiscovery.FormJSON 不是合法 JSON", p.Name)
		}
	}
	return nil
}

// discoveryBudgetFloor/Ceiling 插件自报的单目标探测预算取值范围（毫秒）。
//
// 网关侧的 probe RPC 超时 = 插件自报的 MaxBudgetMs + 固定余量（见 pluginhost 的
// probeRPCGrace），因此这个范围同时就是"网关侧超时值"的范围。设上下界是为了让
// 超时值天然有界，从而**不需要**再引入一个全局的 plugin.probeTimeoutMs 配置项 ——
// 那种全局旋钮配小了，症状是 RPC 先超时、插件还在跑、用户看到"扫不到设备"而
// 日志里只有一条 timeout，是典型的"配错了就静默不工作"。
//
//	下界 500ms：再短连一次 TCP 拨号都完不成，只会得到满屏假失败；
//	上界 60s：单目标探测超过一分钟，扫一个 /24 就要按小时计，不是可用的功能。
const (
	discoveryBudgetFloor   = 500
	discoveryBudgetCeiling = 60000
)

// validateDiscovery 校验一个协议的扫描声明。
//
// 形状错了要在**插件启动时**报错，而不是等运维点开扫描页才发现 ——
// 与 Meta 里其余校验同一条取舍。
func validateDiscovery(p Protocol) error {
	d := p.Discovery
	if d == nil {
		return nil
	}
	if strings.TrimSpace(d.Title) == "" {
		return fmt.Errorf("driversdk: 协议 %q 声明了 Discovery 但 Title 为空", p.Name)
	}
	// 串口协议不能声明扫描能力：目标模型是 host/port，表达不了 comPort。
	// 串口上"有哪些设备"只能逐站号问，与 IP 网段无关，是另一个功能。
	if p.SerialExclusive {
		return fmt.Errorf("driversdk: 协议 %q 是串口独占协议（SerialExclusive），不能声明 Discovery —— "+
			"扫描的目标模型是 host/port，表达不了串口", p.Name)
	}
	if d.DefaultPort < 1 || d.DefaultPort > 65535 {
		return fmt.Errorf("driversdk: 协议 %q 的 Discovery.DefaultPort = %d，应在 [1,65535]",
			p.Name, d.DefaultPort)
	}
	switch d.TargetMode {
	case "", driver.TargetModeUnicast, driver.TargetModeBroadcast, driver.TargetModeBoth:
	default:
		return fmt.Errorf("driversdk: 协议 %q 的 Discovery.TargetMode = %q，只接受 unicast / broadcast / both",
			p.Name, d.TargetMode)
	}
	if d.MaxBudgetMs < discoveryBudgetFloor || d.MaxBudgetMs > discoveryBudgetCeiling {
		return fmt.Errorf("driversdk: 协议 %q 的 Discovery.MaxBudgetMs = %d，应在 [%d,%d]（毫秒）—— "+
			"它同时决定网关侧的 probe RPC 超时，因此不能任意取值",
			p.Name, d.MaxBudgetMs, discoveryBudgetFloor, discoveryBudgetCeiling)
	}
	// Discovery.FormJSON 的合法性由 validateForms 在更早处判（理由见该函数）。
	return nil
}
