// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package router

import (
	"net/http"
	"sort"

	"github.com/gin-gonic/gin"

	"iot-gateway/model/dto"
	"iot-gateway/response"
)

// 本文件是 /agentApi 暴露面的**机器可读分类表**：dsh 靠它判断哪些接口可以直接调、
// 哪些必须先取得用户确认。
//
// 为什么需要一张表，而不能让 dsh 按 HTTP 方法自己推：
//
//	GET  /device/deleteDevice/:id      GET，但删数据
//	POST /openApi/device/names         POST，纯查询
//	POST /device/testDeviceConnection  POST，不改状态，但真实连一次 PLC
//	POST /alarmWebhook/testSend        POST，不改状态，但**真的往外发一条消息**
//
// 四条反例分别打在"GET=只读"和"POST=写"两个方向的假设上。方法在本项目里
// **完全不能作为只读/可写的判据**，分类只能由人逐个判定。
//
// 表里还有第二条信息：**参数表**（Params）。分类回答"能不能直接调"，参数回答
// "怎么调" —— 只有前者时，dsh 背后的模型只能按字段名编请求体，而本项目里
// rwPermission、multipart 字段名、items[].index 这类东西靠猜一律是错的。
// 参数不手写，用反射从控制器绑定的 DTO 导出，细节与理由见 agentApiParams.go。
//
// 表的完整性由 backend/agentApiRoutes_test.go 守住：新增路由却没登记会失败，
// 删了路由却留着陈旧条目也会失败，登记了却忘了写参数（Params 为 nil）同样会失败。
// **这是让表不漂移的唯一机制** —— 没有它，这张表会在几次迭代后悄悄与真实路由脱节，
// 而 dsh 会照着错的分类行事。

// AccessLevel 一个接口对网关的影响等级。取值同时是对 dsh 的指令。
type AccessLevel string

const (
	// AccessRead 直接调即可：不改网关任何状态，无对外副作用，代价可忽略。
	AccessRead AccessLevel = "read"

	// AccessProbe 调之前应取得用户确认：不改网关状态，但**有真实代价或对外副作用**
	// （连设备/下游、扫网段、给别人发消息、占用采集会话）。
	AccessProbe AccessLevel = "probe"

	// AccessWrite 调之前应取得用户确认：**改变网关状态** —— 要么改持久化数据，
	// 要么启停/热刷新运行行为。
	AccessWrite AccessLevel = "write"
)

// SurfaceEntry 一条接口的分类与参数。确认与否由等级推导（read 之外都要确认），
// 故不在此重复声明 Confirm —— 两处写就有两处可以写反。
type SurfaceEntry struct {
	Access AccessLevel

	// Params 本接口的参数。**nil 表示忘了声明，空切片表示确实没有** ——
	// 这个区分由测试咬住（见 argNone 的说明）。路径参数不必写在这里，
	// AgentApiSurface 会从路由串里导出。
	Params []ParamSpec

	// Note 人类可读的补充说明。写给 dsh 背后的模型看，不是给机器解析的：
	// 只写"光看字段名看不出来"的事（敏感字段、阻塞时长、GET 删除这类反直觉行为）。
	// 逐字段的说明放 ParamSpec.Desc，这里留给"整个接口"层面的事。
	Note string
}

// SurfaceRoute 清单端点输出的单条记录。
type SurfaceRoute struct {
	Method string      `json:"method"`
	Path   string      `json:"path"`
	Access AccessLevel `json:"access"`
	// Confirm 是否建议先取得用户确认。恒等于 Access != AccessRead，
	// 由 AgentApiSurface 统一推导，不单独维护。
	Confirm bool `json:"confirm"`
	// Params 路径参数在前（按 URL 里的出现顺序），其余按 DTO 字段顺序。
	// 恒非 nil：空数组表示"这个接口不收参数"。
	Params []ParamSpec `json:"params"`
	Note   string      `json:"note,omitempty"`
}

// surfacePath 清单端点自身的路径（相对 /agentApi）。
const surfacePath = "/__surface"

// agentApiSurface 分类表。键是 "METHOD <相对路径>"，路径与 gin 注册时一致
// （含 :id 这类参数占位符），不含 /agentApi 前缀。
//
// 分组的顺序与 agentApiRouter.go 的挂载顺序一致，便于对照。
var agentApiSurface = map[string]SurfaceEntry{
	// ==================== 用户 ====================
	"POST /user/login": {
		Access: AccessRead,
		Params: argBody(dto.LoginDTO{}, map[string]string{
			"password": "RSA-OAEP 加密后的密文（网关公钥加密），不是明文",
		}),
		Note: "换取 JWT 用。dsh 走 /agentApi 不需要它" +
			"（本分组无鉴权），保留是因为内部路由被整组镜像过来了",
	},
	"GET /profile":     {Access: AccessRead, Params: argNone()},
	"GET /users":       {Access: AccessRead, Params: argQuery(dto.PageDTO{}, nil)},
	"GET /users/:id":   {Access: AccessRead, Params: argNone()},
	"POST /createUser": {Access: AccessWrite, Params: argBody(dto.CreateUserDTO{}, nil)},
	"PUT /users/:id":   {Access: AccessWrite, Params: argBody(dto.UpdateUserDTO{}, nil)},
	"PUT /user/password": {
		Access: AccessWrite,
		Params: argBody(dto.ChangePasswordDTO{}, map[string]string{
			"oldPassword":     "RSA-OAEP 加密后的密文",
			"newPassword":     "RSA-OAEP 加密后的密文，明文长度须 ≥6",
			"confirmPassword": "RSA-OAEP 加密后的密文，须与 newPassword 一致",
		}),
		Note: "改的是 **JWT 令牌所属**那个用户的密码（用户名取自令牌，请求体里不带）",
	},

	// ==================== 设备 ====================
	"GET /device/pageDevice": {
		Access: AccessRead,
		Params: argQuery(dto.PageDeviceDTO{}, nil),
		Note:   "响应带 protocolJson（PLC 连接配置，敏感）",
	},
	"GET /device/getDeviceById/:id": {
		Access: AccessRead,
		Params: argNone(),
		Note:   "响应带 protocolJson（PLC 连接配置，敏感）",
	},
	"GET /device/overview":       {Access: AccessRead, Params: argNone()},
	"GET /device/importTemplate": {Access: AccessRead, Params: argNone(), Note: "返回 .xlsx 文件流，不是 JSON 信封"},
	"POST /device/createDevice": {
		Access: AccessWrite,
		Params: argBody(dto.CreateDeviceDTO{}, map[string]string{
			"protocolJson": "该协议的连接配置，字段结构见 /agentApi/protocol/getProtocolByName 返回的 formJson",
		}),
		Note: "成功时 data 为 null，不回新设备 id —— 要 id 须按名字回查 /device/pageDevice",
	},
	"POST /device/updateDevice": {
		Access: AccessWrite,
		Params: argBody(dto.UpdateDeviceDTO{}, map[string]string{
			"protocolJson": "不传 = 不改连接配置",
			"status":       "1 启用 0 停用；省略/null = 不改",
		}),
	},
	"GET /device/deleteDevice/:id": {
		Access: AccessWrite,
		Params: argNone(),
		Note:   "**用 GET 实现**，按方法判只读会误删。连带删掉该设备全部点位",
	},
	"POST /device/batchDeleteDevice": {
		Access: AccessWrite,
		Params: argBody(dto.BatchDeleteDeviceDTO{}, nil),
		Note:   "连带删点位，响应里的 addresses 就是被连带删掉的行数",
	},
	"POST /device/importDevice": {
		Access: AccessWrite,
		Params: args(argFile("file", ".xlsx 文件")),
		Note:   "multipart 上传，**没有 JSON 请求体**",
	},
	"POST /device/testDeviceConnection": {
		Access: AccessProbe,
		Params: argBody(dto.TestDeviceConnectionDTO{}, map[string]string{
			"protocolJson": "要测试的连接配置，结构同 createDevice 的 protocolJson",
		}),
		Note: "真实连接一次 PLC，耗时取决于设备可达性（不可达时会等满超时）",
	},

	// ==================== 设备点位 ====================
	"GET /deviceAddress/pageDeviceAddress": {
		Access: AccessRead,
		Params: argQuery(dto.PageDeviceAddressDTO{}, map[string]string{
			"deviceId": "所属设备 id",
		}),
	},
	"GET /deviceAddress/getDeviceAddressById/:id": {Access: AccessRead, Params: argNone()},
	"GET /deviceAddress/importDeviceAddressTemplate": {
		Access: AccessRead,
		Params: argNone(),
		Note:   "返回 .xlsx 文件流，不是 JSON 信封",
	},
	"POST /deviceAddress/createDeviceAddress": {
		Access: AccessWrite,
		Params: argBody(dto.CreateDeviceAddressDTO{}, map[string]string{
			"name":           "**设备地址写法**（如 %MW100、DB1.DBW100、D100），具体写法由设备的协议决定；服务端不校验写法，写坏会让这个点位永远没有值（只报「地址无效」），需改配置恢复",
			"label":          "地址的中文说明（如「温度」「电流」）",
			"commonDataType": "通用类型名（如 Word），服务端结合协议映射成内部类型",
			"dataType":       "兼容旧请求的字段；新调用方应传 commonDataType，传其一即可",
			"rwPermission":   "只接受 R / W / RW",
			"scanFrequency":  "扫描频率（毫秒）；<=0 按默认 1000 处理",
		}),
	},
	"POST /deviceAddress/updateDeviceAddress": {
		Access: AccessWrite,
		Params: argBody(dto.UpdateDeviceAddressDTO{}, map[string]string{
			"name":           "设备地址写法；不传 = 不改",
			"label":          "**必填，且无条件覆盖** —— 不是「不传就不改」",
			"commonDataType": "不传 = 不改；与 dataType 传其一即可",
			"dataType":       "兼容旧请求；新调用方应传 commonDataType",
			"rwPermission":   "只接受 R / W / RW；不传 = 不改",
			"scanFrequency":  "毫秒；**<=0 = 不改**（与创建不同：创建时 <=0 按默认 1000 处理）",
			"description":    "**无条件覆盖**：空串会清空描述",
			"status":         "1 启用 0 停用；省略/null = 不改",
		}),
	},
	"GET /deviceAddress/deleteDeviceAddress/:id": {
		Access: AccessWrite,
		Params: argNone(),
		Note:   "**用 GET 实现**。删除静默生效（下一轮热加载就少一个点）",
	},
	"POST /deviceAddress/batchDeleteDeviceAddress": {
		Access: AccessWrite,
		Params: argBody(dto.BatchDeleteDeviceAddressDTO{}, map[string]string{
			"deviceId": "删除范围的**前置约束**：ids 必须属于这台设备才会被删",
		}),
	},
	"POST /deviceAddress/importDeviceAddress": {
		Access: AccessWrite,
		Params: args(
			argFile("file", ".xlsx 文件"),
			argFormStr("deviceId", true, "这些点位挂在哪台设备下（普通表单字段，不是文件）"),
		),
		Note: "multipart 上传，**没有 JSON 请求体**",
	},

	// ==================== 采集控制 ====================
	"GET /collection/status": {Access: AccessRead, Params: argNone()},
	"POST /collection/start": {
		Access: AccessWrite,
		Params: argNone(),
		Note:   "启动采集引擎 —— 影响产线数据流，最需要确认的一类",
	},
	"POST /collection/stop": {Access: AccessWrite, Params: argNone(), Note: "停止采集引擎，全设备停采"},
	"POST /collection/refresh": {
		Access: AccessWrite,
		Params: argNone(),
		Note:   "热刷新采集配置（重建在跑的任务）。通常无需手动调，配置变更后 10s 内自动生效",
	},

	// ==================== 实时值 ====================
	"GET /realtime/snapshot": {
		Access: AccessRead,
		Params: argQuery(dto.RealtimeSnapshotDTO{}, map[string]string{
			"deviceId": "要查哪台设备的实时值",
			"name":     "点位名或说明（模糊匹配）",
		}),
		Note: "取的是**内存中的最新值**（秒级轮询用），不是点位定义",
	},

	// ==================== 大盘聚合 ====================
	"GET /dashboard/trend": {
		Access: AccessRead,
		Params: argQuery(dto.DashboardTrendDTO{}, map[string]string{
			"hours": "回看小时数，1..24；省略按 6 处理",
		}),
		Note: "每分钟一个采样点的内存趋势（最长 24h），网关重启后从空开始",
	},

	// ==================== 协议 ====================
	"GET /protocol/pageProtocol":        {Access: AccessRead, Params: argQuery(dto.PageProtocolDTO{}, nil)},
	"GET /protocol/getProtocolById/:id": {Access: AccessRead, Params: argNone()},
	"GET /protocol/getProtocolByName": {
		Access: AccessRead,
		Params: args(argQueryStr("name", true, "协议名，如 ModBus.TCP")),
	},
	"GET /protocol/getDataTypes": {
		Access: AccessRead,
		Params: args(
			argQueryStr("protocolName", false, "协议名；与 protocolId 二选一"),
			argQueryStr("protocolId", false, "协议 id；与 protocolName 二选一"),
		),
	},
	"POST /protocol/updateProtocol": {
		Access: AccessWrite,
		Params: argBody(dto.UpdateProtocolDTO{}, map[string]string{
			"name":        "仅用于校验它没被改动，服务端**不写回**；改名会返回 50003",
			"description": "**无条件覆盖**：空串会清空描述",
			"formJson":    "**服务端完全忽略此字段** —— 改表单请用 updateProtocolFormById / updateProtocolFormByName",
			"sort":        "排序值；省略/null = 不改",
		}),
	},
	"POST /protocol/updateProtocolFormById":   {Access: AccessWrite, Params: argBody(dto.UpdateProtocolFormByIdDTO{}, nil)},
	"POST /protocol/updateProtocolFormByName": {Access: AccessWrite, Params: argBody(dto.UpdateProtocolFormByNameDTO{}, nil)},
	"POST /protocol/resetProtocolForm/:id": {
		Access: AccessWrite,
		Params: argNone(),
		Note:   "把表单恢复为插件当前声明的那一份，会丢弃运维的自定义",
	},

	// ==================== 推送通道 ====================
	"GET /pushChannel/pagePushChannel": {
		Access: AccessRead,
		Params: argQuery(dto.PagePushChannelDTO{}, nil),
		Note:   "响应带 configJson（含 MQTT 口令等凭据）",
	},
	"GET /pushChannel/getPushChannelById/:id": {
		Access: AccessRead,
		Params: argNone(),
		Note:   "响应带 configJson（含 MQTT 口令等凭据）",
	},
	"POST /pushChannel/createPushChannel": {
		Access: AccessWrite,
		Params: argBody(dto.CreatePushChannelDTO{}, map[string]string{
			"configJson": "该通道类型的连接配置（含口令等凭据），字段结构见 /agentApi/pushChannelForm/getPushChannelFormByName",
		}),
	},
	"POST /pushChannel/updatePushChannel": {
		Access: AccessWrite,
		Params: argBody(dto.UpdatePushChannelDTO{}, map[string]string{
			"configJson": "不传 = 不改连接配置",
			"status":     "1 启用 0 停用；省略/null = 不改",
		}),
	},
	"GET /pushChannel/deletePushChannel/:id": {
		Access: AccessWrite,
		Params: argNone(),
		Note:   "**用 GET 实现**。删通道会让数据不再推往下游",
	},
	"POST /pushChannel/testConnectivity": {
		Access: AccessProbe,
		Params: argBody(dto.TestPushChannelDTO{}, map[string]string{
			"type":       "通道类型；旧客户端可省略并把类型放在 name 里",
			"configJson": "要测试的连接配置",
		}),
		Note: "真实连接下游（MQTT/HTTP 等），不保存配置；HTTP 只验证 TCP/TLS 可达",
	},
	"GET /pushChannel/listTypes": {
		Access: AccessRead,
		Params: argNone(),
		Note:   "返回已注册通道类型、是否消息类、可选载荷格式与预览/测试发送能力",
	},
	"POST /pushChannel/preview": {
		Access: AccessRead,
		Params: argBody(dto.PreviewPushChannelDTO{}, map[string]string{
			"configJson": "通道配置，可包含 format/mapping",
		}),
		Note: "用第一台启用设备/点位试编码，不发送、不落库",
	},
	"POST /pushChannel/previewMapping": {
		Access: AccessRead,
		Params: argBody(dto.PreviewPushChannelMappingDTO{}, map[string]string{
			"mapping": "出站映射 JSON（version/emit/envelope/batchFields/fields）；不需要通道类型或连接配置",
		}),
		Note: "只用第一台启用设备/点位试编码映射，不发送、不落库；映射/编码错误放在 data.error",
	},
	"POST /pushChannel/testSend": {
		Access: AccessProbe,
		Params: argBody(dto.TestSendPushChannelDTO{}, map[string]string{
			"configJson": "通道配置，可包含 format/mapping",
		}),
		Note: "**真的向配置的下游发送一条样例数据**，不保存配置",
	},
	"GET /push/status": {Access: AccessRead, Params: argNone()},
	"POST /push/refresh": {
		Access: AccessWrite,
		Params: argNone(),
		Note:   "热刷新推送配置（重建通道）。通常无需手动调",
	},

	// ==================== MQTT 订阅 ====================
	"GET /mqttSubscription/pageMqttSubscription": {
		Access: AccessRead,
		Params: argQuery(dto.PageMqttSubscriptionDTO{}, nil),
		Note:   "响应带 broker 口令等凭据",
	},
	"GET /mqttSubscription/pageMqttSubscriptionDevicePoint": {
		Access: AccessRead,
		Params: argQuery(dto.PageMqttSubscriptionDevicePointDTO{}, map[string]string{
			"source": "manual 手工维护 | auto 订阅报文自动收录；空 = 全部",
		}),
		Note: "订阅报文里出现过的设备/点位元信息索引，不含采样值",
	},
	"GET /mqttSubscription/importMqttSubscriptionDevicePointTemplate": {
		Access: AccessRead,
		Params: argNone(),
		Note:   "返回 .xlsx 文件流，不是 JSON 信封",
	},
	"POST /mqttSubscription/importMqttSubscriptionDevicePoint": {
		Access: AccessWrite,
		Params: args(argFile("file", ".xlsx 文件")),
		Note:   "multipart 上传，**没有 JSON 请求体**。按设备ID+点位ID upsert：已存在则覆盖元信息，导入行标记为 manual，后续订阅报文不再覆盖",
	},
	"POST /mqttSubscription/createMqttSubscriptionDevicePoint": {
		Access: AccessWrite,
		Params: argBody(dto.CreateMqttSubscriptionDevicePointDTO{}, map[string]string{
			"deviceId":           "设备 ID（自然键，创建后不可修改）",
			"deviceAddressId":    "点位 ID（自然键，创建后不可修改）",
			"deviceAddressLabel": "点位标签/说明",
			"kind":               "bool/int/uint/float/string/time；留空表示未知",
		}),
		Note: "自然键重复返回 60024；创建后该行标记为 manual，订阅报文不再覆盖其元信息",
	},
	"POST /mqttSubscription/updateMqttSubscriptionDevicePoint": {
		Access: AccessWrite,
		Params: argBody(dto.UpdateMqttSubscriptionDevicePointDTO{}, map[string]string{
			"deviceId":           "仅用于定位，不是可改字段",
			"deviceAddressId":    "仅用于定位，不是可改字段",
			"deviceName":         "**无条件覆盖**：空串会清空",
			"deviceAddressLabel": "**无条件覆盖**：空串会清空",
			"kind":               "改值须为 bool/int/uint/float/string/time；留空 = 清空；与库中现值相同时允许保留",
		}),
		Note: "成功后 source 置为 manual，订阅报文不再覆盖该行元信息",
	},
	"GET /mqttSubscription/deleteMqttSubscriptionDevicePoint": {
		Access: AccessWrite,
		Params: argQuery(dto.DeleteMqttSubscriptionDevicePointDTO{}, map[string]string{
			"deviceId":        "设备 ID（自然键之一）",
			"deviceAddressId": "点位 ID（自然键之一）",
		}),
		Note: "**用 GET 实现**，自然键走查询参数。只删手工维护（source=manual）的行：" +
			"自动收录的行删了会被下一条报文重新收录，故返回 60026 拒绝；" +
			"行不存在返回 60025",
	},
	"POST /mqttSubscription/restoreMqttSubscriptionDevicePointAuto": {
		Access: AccessWrite,
		Params: argBody(dto.RestoreMqttSubscriptionDevicePointAutoDTO{}, map[string]string{
			"deviceId":        "设备 ID（自然键之一）",
			"deviceAddressId": "点位 ID（自然键之一）",
		}),
		Note: "把手工维护的行交还给自动收录（source 置回 auto），并作废落库器的元信息指纹：" +
			"下一条报文到达时会重新 upsert，按报文值覆盖元信息。已是 auto 的行幂等成功；" +
			"行不存在返回 60025。",
	},
	"POST /mqttSubscription/batchDeleteMqttSubscriptionDevicePoint": {
		Access: AccessWrite,
		Params: argBody(dto.BatchDeleteMqttSubscriptionDevicePointDTO{}, map[string]string{
			"items": "要删除的自然键对，最多 200 条；每项 {deviceId, deviceAddressId}",
		}),
		Note: "单条删除的批量版。只删 source=manual 的行；自动行与不存在的行不视为错误，" +
			"返回值 deleted 可能小于 requested，前端据此提示少删了几条。",
	},
	"GET /mqttSubscription/getMqttSubscriptionById/:id": {
		Access: AccessRead,
		Params: argNone(),
		Note:   "响应带 broker 口令等凭据",
	},
	"GET /mqttSubscription/status": {
		Access: AccessRead,
		Params: argNone(),
		Note:   "各订阅的连接状态与收包/入库计数（只在内存中）",
	},
	"GET /mqttSubscription/recentMessages": {
		Access: AccessRead,
		Params: argQuery(dto.RecentMqttSubscriptionMessagesDTO{}, map[string]string{
			"id":    "订阅 ID（status 接口返回的 id；停用/未加载的订阅返回 active=false）",
			"limit": "返回条数，省略 = 全部；最大 100",
		}),
		Note: "**响应 payload 是外部 MQTT 报文原文预览（最多 2 KiB）**，可能含敏感数据；" +
			"仅内存，每订阅保留最近 100 条，重建订阅（连接参数变化）后清空",
	},
	"POST /mqttSubscription/createMqttSubscription": {
		Access: AccessWrite,
		Params: argBody(dto.CreateMqttSubscriptionDTO{}, map[string]string{
			"mappingJson": "JSON → CollectedRecord 的映射规则（root/fields/filter）",
			"status":      "1 启用 0 停用；省略 = 启用",
		}),
	},
	"POST /mqttSubscription/updateMqttSubscription": {
		Access: AccessWrite,
		Params: argBody(dto.UpdateMqttSubscriptionDTO{}, map[string]string{
			"mappingJson": "不传 = 沿用原映射规则",
			"status":      "1 启用 0 停用；省略/null = 不改",
		}),
	},
	"GET /mqttSubscription/deleteMqttSubscription/:id": {
		Access: AccessWrite,
		Params: argNone(),
		Note:   "**用 GET 实现**。删除会断开该订阅并停止入库",
	},
	"POST /mqttSubscription/testConnectivity": {
		Access: AccessProbe,
		Params: argBody(dto.TestMqttSubscriptionDTO{}, nil),
		Note:   "真实连接外部 broker 并立即断开，不保存配置",
	},
	"POST /mqttSubscription/preview": {
		Access: AccessRead,
		Params: argBody(dto.PreviewMqttSubscriptionDTO{}, nil),
		Note:   "仅用样例报文试跑映射规则，不改网关状态",
	},

	// ==================== 推送通道表单 ====================
	"GET /pushChannelForm/getPushChannelFormByName": {
		Access: AccessRead,
		Params: argQuery(dto.GetPushChannelFormByNameDTO{}, nil),
	},
	"POST /pushChannelForm/createPushChannelForm": {Access: AccessWrite, Params: argBody(dto.CreatePushChannelFormDTO{}, nil)},
	"POST /pushChannelForm/updatePushChannelForm": {Access: AccessWrite, Params: argBody(dto.UpdatePushChannelFormDTO{}, nil)},

	// ==================== 报警 ====================
	"GET /alarm/pageAlarm": {
		Access: AccessRead,
		Params: argQuery(dto.PageAlarmDTO{}, map[string]string{
			"targetName": "目标名称（模糊匹配）",
			"targetType": "device | channel | point | point_threshold；空 = 全部",
			"alarmType":  "offline | quality | threshold | recover",
			"status":     "active | cleared",
		}),
	},
	"GET /alarm/listActive": {Access: AccessRead, Params: argNone(), Note: "当前活跃离线报警 = 离线设备列表"},
	"GET /alarm/pageThresholdRecord": {
		Access: AccessRead,
		Params: argQuery(dto.PageThresholdRecordDTO{}, map[string]string{
			"deviceId": "所属设备；空 = 全部",
			"name":     "点位名称（模糊匹配）",
		}),
		Note: "点位越限样本明细（按原始越限逐条留证，与 pageAlarm 的去抖报警不同）",
	},
	"GET /alarm/deviceReliability": {
		Access: AccessRead,
		Params: argQuery(dto.DeviceReliabilityDTO{}, map[string]string{
			"from":  "YYYY-MM-DD，含当天起点（必填）",
			"to":    "YYYY-MM-DD，含当天终点（必填）",
			"limit": "只取前 N 名（按可用率升序）；**省略 = 不截断**（与 pointAbnormal 的默认值语义相反）",
		}),
		Note: "设备可靠性 KPI：可用率 / 故障段数 / 累计停机秒 / MTTR，按可用率升序（最差在前）。" +
			"**可用率是百分数（0~100，两位小数）**，如 99.17 即 99.17%；" +
			"downSeconds / mttrSeconds 是四舍五入的整秒（mttrSeconds 无已完成故障时为 -1）。" +
			"名次按**取整前**的可用率定序，只有输出值取整。" +
			"响应是 `{gatewayId, gatewayName, records[]}` —— **榜单在 records 里**，" +
			"外层两个字段是本网关自身身份（iot_gateway），跨多台网关汇总时用它归位；" +
			"身份行缺失时为空串，但报表照样返回。" +
			"**口径是「报警口径」** —— 只统计去抖后落库的故障段，低于去抖阈值的抖动不计入，" +
			"所以算出的可用率略高于真实值（这是与报警定义保持一致的有意取舍，另见 specs/alarm.md）。" +
			"可用率分母是整窗口，**未扣除网关自身停机**（网关停机期间不轮询、不产生任何报警行），" +
			"需要扣除的请用返回的 windowSeconds 按自己的分母重算。" +
			"窗口内一次故障都没有的设备**不在结果里**（等价于可用率 100%），" +
			"所以这是「最差设备排行」而不是全量设备清单。窗口上限 90 天。",
	},
	"GET /alarm/pointAbnormal": {
		Access: AccessRead,
		Params: argQuery(dto.PointAbnormalDTO{}, map[string]string{
			"deviceId": "只统计该设备下的点位；空 = 全部设备",
			"from":     "YYYY-MM-DD，含当天起点（必填）",
			"to":       "YYYY-MM-DD，含当天终点（必填）",
			"limit":    "top-N 条数，默认 20，上限 500",
		}),
		Note: "点位异常 top-N（按异常段数降序）。" +
			"响应是 `{gatewayId, gatewayName, records[]}`，与 deviceReliability 同形：" +
			"**榜单在 records 里**，外层是本网关自身身份（跨网关汇总时用它归位）。" +
			"qualityEpisodes 与 thresholdEpisodes " +
			"**分开计数，不要合并成一个「非正常次数」** —— 前者要找运维（地址/解析问题），" +
			"后者可能只是工艺波动。targetName 是「设备名.点位名」拼串，**直接展示用，不要拆**" +
			"（名字含点号时会歧义），分组与过滤请用 deviceId。" +
			"排序**不是速率**：采样频率高的点位在抖动时更容易上榜（永久性坏点不受影响）。" +
			"窗口上限 90 天。",
	},
	"GET /alarmWebhook/pageAlarmWebhook": {
		Access: AccessRead,
		Params: argQuery(dto.PageAlarmWebhookDTO{}, map[string]string{
			"type":   "dingtalk | wecom | feishu | custom",
			"status": "1 启用 0 停用",
		}),
	},
	"GET /alarmWebhook/getAlarmWebhookById/:id": {
		Access: AccessRead,
		Params: argNone(),
		Note:   "secret 永不回显（恒为空串），只有 hasSecret；但 url 含凭据",
	},
	"GET /alarmWebhook/listTypes": {Access: AccessRead, Params: argNone(), Note: "支持的 webhook 类型与各自的 msgTypes"},
	"GET /alarmWebhook/status":    {Access: AccessRead, Params: argNone()},
	"POST /alarmWebhook/createAlarmWebhook": {
		Access: AccessWrite,
		Params: argBody(dto.CreateAlarmWebhookDTO{}, map[string]string{
			"type":    "取值集合由 notify 注册表决定，先调 /agentApi/alarmWebhook/listTypes",
			"url":     "必须是 http/https URL（其他 scheme 会被拒）",
			"secret":  "加签密钥（可选）",
			"msgType": "各类型支持的取值见 listTypes 的 msgTypes",
			"atList":  "逗号分隔的 @ 列表（钉钉为手机号、飞书为 open_id）",
		}),
	},
	"POST /alarmWebhook/updateAlarmWebhook": {
		Access: AccessWrite,
		Params: argBody(dto.UpdateAlarmWebhookDTO{}, map[string]string{
			"name":        "空串 = 不改（不是清空）",
			"type":        "空串 = 不改",
			"url":         "空串 = 不改",
			"secret":      "**指针语义**：省略/null = 不改，传 \"\" = 清空密钥",
			"atAll":       "**指针语义**：省略/null = 不改，传 false = 关闭全员 @",
			"atList":      "**指针语义**：省略/null = 不改，传 \"\" = 清空 @ 列表",
			"status":      "**指针语义**：省略/null = 不改，传 0 = 停用",
			"description": "空串 = 不改",
		}),
	},
	"GET /alarmWebhook/deleteAlarmWebhook/:id": {Access: AccessWrite, Params: argNone(), Note: "**用 GET 实现**"},
	"POST /alarmWebhook/refresh": {
		Access: AccessWrite,
		Params: argNone(),
		Note:   "热刷新报警通知配置。通常无需手动调",
	},
	"POST /alarmWebhook/testSend": {
		Access: AccessProbe,
		Params: argBody(dto.TestSendAlarmWebhookDTO{}, map[string]string{
			"id":         "非空时以库中配置为基准、请求字段覆盖之（改完先测再存）；为空则完全使用请求字段（还没保存先测）",
			"alarmType":  "offline | recover，用于预览文案",
			"targetType": "device | channel",
			"targetName": "用于预览文案",
		}),
		Note: "⚠️ **真的往外发一条报警消息**到钉钉/企微/飞书 —— 对网关无副作用，但群里会有人看见",
	},
	"GET /alarmWebhookForm/getAlarmWebhookFormByName": {Access: AccessRead, Params: argQuery(dto.GetAlarmWebhookFormByNameDTO{}, nil)},
	"POST /alarmWebhookForm/createAlarmWebhookForm":   {Access: AccessWrite, Params: argBody(dto.CreateAlarmWebhookFormDTO{}, nil)},
	"POST /alarmWebhookForm/updateAlarmWebhookForm":   {Access: AccessWrite, Params: argBody(dto.UpdateAlarmWebhookFormDTO{}, nil)},

	// ==================== 运维 ====================
	"GET /log/listFiles": {
		Access: AccessRead,
		Params: argQuery(dto.ListLogFileDTO{}, map[string]string{
			"page": "可省略（服务端兜底 1）",
			"size": "可省略（服务端兜底 20）",
		}),
	},
	"GET /log/readFile": {
		Access: AccessRead,
		Params: argQuery(dto.ReadLogDTO{}, map[string]string{
			"fileName": "要读的日志文件名（取自 /agentApi/log/listFiles）",
			"tail":     "取末尾 N 行；默认 200，上限 5000",
		}),
	},
	"GET /log/downloadFile": {
		Access: AccessRead,
		Params: argQuery(dto.DownloadLogDTO{}, map[string]string{
			"fileName": "要下载的日志文件名（取自 /agentApi/log/listFiles）",
		}),
		Note: "响应是**原始日志文件**（Content-Disposition: attachment），不是 JSON 信封；支持 Range 断点续传",
	},
	"GET /gateway/get": {Access: AccessRead, Params: argNone()},
	"POST /gateway/update": {
		Access: AccessWrite,
		Params: argBody(dto.UpdateGatewayDTO{}, nil),
		Note:   "只开放 name 与 description；改完无需重启（局域网广播每轮重读该行）",
	},
	"GET /openApiSecret/list": {
		Access: AccessRead,
		Params: argNone(),
		Note:   "响应含**明文 AppKey** —— 拿到它就能调 /openApi 的只读接口",
	},
	"GET /openApiSecret/doc":         {Access: AccessRead, Params: argNone(), Note: "data 是渲染好的 HTML 字符串，不是 JSON 对象"},
	"POST /openApiSecret/create":     {Access: AccessWrite, Params: argBody(dto.CreateOpenApiSecretDTO{}, nil)},
	"POST /openApiSecret/updateName": {Access: AccessWrite, Params: argBody(dto.UpdateOpenApiSecretNameDTO{}, nil)},
	"GET /openApiSecret/delete/:id": {
		Access: AccessWrite,
		Params: argNone(),
		Note:   "**用 GET 实现**。删密钥会让持有它的外部系统立即失去访问",
	},
	"GET /plugin/info": {Access: AccessRead, Params: argNone(), Note: "插件目录 / 命名规则 / 总开关"},
	"POST /plugin/importPlugin": {
		Access: AccessWrite,
		Params: args(argFile("files", "插件可执行文件；**字段名是复数 files**，可多个同名部件重复出现，总上限 512MiB")),
		Note: "⚠️ 上传并加载**可执行文件**，等价于远程代码执行。本分组里风险最高的一条。" +
			"multipart 上传，**没有 JSON 请求体**",
	},

	// ==================== 局域网设备扫描 ====================
	"GET /deviceScan/capabilities": {
		Access: AccessRead,
		Params: argNone(),
		Note:   "含 protocols 与 subnets —— 扫网段的协议与网段清单都在这里（没有单独的 /protocols、/subnets 接口）",
	},
	"GET /deviceScan/status": {
		Access: AccessRead,
		Params: argQuery(dto.ScanJobQueryDTO{}, map[string]string{
			"jobId":       "扫描任务 id（取自 /agentApi/deviceScan/start）",
			"withDevices": "省略 = 完整快照；轮询**应传 false** 拿轻量快照（devices/errors 为 null）",
		}),
	},
	"POST /deviceScan/start": {
		Access: AccessProbe,
		Params: argBody(dto.StartDeviceScanDTO{}, map[string]string{
			"protocol": "协议名，**只能取 /agentApi/deviceScan/capabilities 的 protocols**",
			"options":  "扫描选项，形状由该协议的 scanFormJson 定义，原样透传给插件",
			"subnets":  "要扫的网段（CIDR），**只能取 capabilities 的 subnets**；空 = 全部本机网段",
			"port":     "覆盖协议默认端口；0 或省略 = 用默认端口",
		}),
		Note: "**扫本机网段**，重且慢；同一时刻只允许一个任务（否则 40022）",
	},
	"POST /deviceScan/cancel": {
		Access: AccessProbe,
		Params: argBody(dto.CancelDeviceScanDTO{}, nil),
		Note:   "取消在跑的扫描。它只影响内存里的任务，不写库——故归 probe 而非 write",
	},
	"POST /deviceScan/createDevices": {
		Access: AccessWrite,
		Params: argBody(dto.CreateDevicesFromScanDTO{}, map[string]string{
			"items.index":      "**任务快照里 devices 数组的下标**（不是设备 id，也不是扫描结果里的某个 id）",
			"items.name":       "留空时服务端按「协议-IP」生成一个",
			"confirmDuplicate": "省略 = 重复地址的行**不建**，只回 code=addressExists 的明细；用户确认后带 true 重提",
		}),
		Note: "连接配置（protocolJson）由服务端从自己持有的任务快照里取，**不采信客户端**",
	},

	// ==================== 设备点位扫描 ====================
	"GET /deviceScan/pointCapability": {
		Access: AccessRead,
		Params: argQuery(dto.PointCapabilityQueryDTO{}, map[string]string{
			"protocolName": "协议名（决定显示哪个协议的能力与表单，不决定去连哪台设备）",
		}),
	},
	"POST /deviceScan/discoverPoints": {
		Access: AccessProbe,
		Params: argBody(dto.DiscoverPointsDTO{}, map[string]string{
			"deviceId": "要扫描的设备。**只收 id，不收连接配置** —— protocolJson 由服务端从 device 行读取",
			"options":  "扫描选项，形状由该协议的 pointFormJson 定义，原样透传给插件",
		}),
		Note: "**同步阻塞**，最长约 7s（网关侧硬钳）。且复用该设备的采集会话，" +
			"期间会占住会话锁 —— 6 条 probe 里唯一会干扰正在跑的采集的一条。" +
			"dsh 侧 HTTP 超时必须 > 8s",
	},
	"POST /deviceScan/createAddresses": {
		Access: AccessWrite,
		Params: argBody(dto.CreateAddressesFromScanDTO{}, map[string]string{
			"items.name":           "设备地址写法；写坏会让这个点位永远没有值（只报「地址无效」）",
			"items.label":          "留空时服务端兜底为 name",
			"items.commonDataType": "通用类型名（如 Word）",
			"items.rwPermission":   "R / W / RW",
			"items.scanFrequency":  "毫秒；<=0 按 1000 处理",
		}),
		Note: "与 createDevices 相反：**采信客户端的全部 items 字段**，唯一的服务端权威是 deviceId",
	},

	// ==================== 运行期配置开关 ====================
	"GET /systemConfig/list": {
		Access: AccessRead,
		Params: argNone(),
		Note: "返回每个开关的生效值、default.yaml 默认值、来源（default / override）、" +
			"是否立即生效以及是否需要重启",
	},
	"POST /systemConfig/update": {
		Access: AccessWrite,
		Params: argBody(dto.UpdateSystemConfigDTO{}, map[string]string{
			"key": "开关标识；只能取 /systemConfig/list 里的 key",
			"value": "JSON 标量，当前只接受 true / false。" +
				"不要传字符串 \"true\" 或数字 1 —— 类型不符会被拒",
		}),
		Note: "修改会写入数据库并覆盖 default.yaml。discovery.enabled / deviceScan.enabled / " +
			"push.changeOnly / agentApi.enabled 立即生效；plugin.enabled 需**重启网关**后才生效" +
			"（响应里 requiresRestart=true）。应用失败会回滚数据库与运行状态并返回错误。" +
			"注意：agentApi.enabled=false 时本分组整体不可达（包括本接口），" +
			"重新开启必须走需 JWT 的内部接口 POST /systemConfig/update",
	},
	"POST /systemConfig/reset": {
		Access: AccessWrite,
		Params: argBody(dto.ResetSystemConfigDTO{}, map[string]string{
			"key": "开关标识；只能取 /systemConfig/list 里的 key",
		}),
		Note: "删除覆盖值，回到 default.yaml 的默认值；没有覆盖值时是幂等的无操作",
	},

	// ==================== 清单自身 ====================
	"GET " + surfacePath: {
		Access: AccessRead,
		Params: argNone(),
		Note:   "本清单。登记在这里是为了让「每个 /agentApi 路由都有分类」这条不变量无例外地成立",
	},
}

// AgentApiSurface 返回排序后的接口清单，供 dsh 消费。
//
// Confirm 在此统一推导（read 之外都要确认），不放进表里：一处规则好过 83 处抄写。
// 路径参数同样在此从路由串导出，不写进表 —— 路径串本身就是真源。
func AgentApiSurface() []SurfaceRoute {
	out := make([]SurfaceRoute, 0, len(agentApiSurface))
	for key, entry := range agentApiSurface {
		method, rel, ok := cutKey(key)
		if !ok {
			// 键写错格式是**编码错误**，不是运行时状况。静默跳过会让这条目
			// 从清单里消失而无人察觉，故直接 panic（在测试里必然被发现）。
			panic("agentApiSurface 键格式非法（应为 \"METHOD /path\"）: " + key)
		}
		// 与上面的键格式同理：Params 为 nil 是"忘了声明"，不是"没有参数"。
		// 无参数的接口必须显式写 argNone() —— 否则这条目会带着一张空表发出去，
		// 而 dsh 分不清"不用传参数"和"这张表没写"。
		if entry.Params == nil {
			panic("agentApiSurface 条目未声明参数（无参数请写 argNone()）: " + key)
		}
		params := pathParams(rel)
		params = append(params, entry.Params...)

		out = append(out, SurfaceRoute{
			Method:  method,
			Path:    AgentApiPrefix + rel,
			Access:  entry.Access,
			Confirm: entry.Access != AccessRead,
			Params:  params,
			Note:    entry.Note,
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Path != out[j].Path {
			return out[i].Path < out[j].Path
		}
		return out[i].Method < out[j].Method
	})
	return out
}

// cutKey 把 "METHOD /path" 拆成两段。不用 strings.Cut 是为了避免在
// 本文件额外引入 strings —— 调用方只有一处，手写更直白。
func cutKey(key string) (method, path string, ok bool) {
	for i := 0; i < len(key); i++ {
		if key[i] == ' ' {
			return key[:i], key[i+1:], true
		}
	}
	return "", "", false
}

// SurfaceHandler 返回 /agentApi 的接口清单（无鉴权，与所在分组一致）。
//
// 做成**端点**而不是静态文件，是为了让 dsh 拿到的清单永远与**正在跑的那个网关**
// 一致：路由一变清单跟着变，没有"文件忘了同步"这种失败模式。
func SurfaceHandler(c *gin.Context) {
	c.JSON(http.StatusOK, response.Success(AgentApiSurface()))
}
