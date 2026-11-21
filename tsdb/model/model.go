// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

// Package model 定义时序入库组件的中性数据模型。
//
// 它不依赖任何网关内部包，供组件自身与外部调用方共用。网关侧用类型别名
// （collector.CollectedRecord / push.PushBatch）直接复用这些类型，转换零成本。
package model

// Record 一条待入库的采集记录。
type Record struct {
	DeviceID string `json:"deviceID"`
	// DeviceCode 设备编码（device.code），未编码的旧数据为空。
	// 带 json tag 保证断网缓存回放后仍能拼出以编码为主题段的 topic。
	DeviceCode         string `json:"deviceCode,omitempty"`
	DeviceName         string `json:"deviceName"`
	DeviceAddressID    string `json:"deviceAddressID"`
	DeviceAddressName  string `json:"deviceAddressName"`
	DeviceAddressLabel string `json:"deviceAddressLabel"` // 地址 name 的中文说明（如温度、电流）
	// Value 采集值。点位配置了工程量变换（device_address.transform_json）时，
	// 这里已经是变换后的工程值，Kind 也可能被提升为 float；未配置时与驱动返回值一致。
	Value    string `json:"value"`
	DataType string `json:"dataType"`
	// Kind 数据类型类别（driver.KindXxx），由驱动从已解析类型填充。
	// 携带 json tag 以保证断网缓存（spool）序列化回放后不丢失。
	Kind string `json:"kind"`
	// Protocol 协议名称（iot_protocol.name，如 "ModBus.TCP"），由 collector 填充，
	// 供下游区分协议命名空间（dataType 为协议内部类型名）。
	Protocol string `json:"protocol"`
	// Quality 采集质量码（driver.QualityXxx：类别在高 2 位、成因在低 6 位）。
	// 带 json tag 以保证断网缓存（spool）序列化回放后不丢失。
	Quality int `json:"quality"`
	// CollectedAt 采集时间（毫秒精度本地时间，如 "2026-10-19 17:05:30.123"）。
	//
	// 本地点位采集留空：推送引擎按批次落地时间统一补齐。
	// 外部 MQTT 订阅可在映射里配置 collectedAt，携带报文自带的时间；
	// 时序通道逐条优先用它，留空/无法解析时才回落到批次时间。
	CollectedAt string `json:"collectedAt,omitempty"`
	// ThingModelCode 点位绑定的属性所属物模型编码（thing_model.code），未绑定为空。
	// 带 json tag 以保证断网缓存（spool）序列化回放后不丢失。
	ThingModelCode string `json:"thingModelCode,omitempty"`
	// PropertyCode 点位绑定的物模型属性标识符（thing_model_property.identifier），
	// 未绑定为空。
	PropertyCode string `json:"propertyCode,omitempty"`
	// GroupID / GroupName / GroupIntervalMs 采集组身份（未分组为空/0）。
	//
	// 带 json tag 保证断网缓存（spool）序列化回放后不丢失 —— 推送侧靠它
	// 按 (device, group) 分帧、选择 topic 与通道订阅。组周期对绑定点位权威。
	GroupID string `json:"groupID,omitempty"`
	// GroupCode 采集组编码（collector_group.code），未分组为空。
	GroupCode       string `json:"groupCode,omitempty"`
	GroupName       string `json:"groupName,omitempty"`
	GroupIntervalMs int    `json:"groupIntervalMs,omitempty"`
	// Trigger 事件触发来源。只有事件触发规则定向推送的动作记录才携带；
	// 正常轮询与外部订阅恒为 nil，因此不会改变既有载荷。
	// 随记录一起序列化进断网缓存（records_json），补发不丢元数据。
	Trigger *RecordTrigger `json:"trigger,omitempty"`
}

// RecordTrigger 一条事件触发记录的业务来源。
type RecordTrigger struct {
	Source    string `json:"source"`
	RuleID    string `json:"ruleId"`
	RuleCode  string `json:"ruleCode"`
	RuleName  string `json:"ruleName"`
	TriggerID string `json:"triggerId"`
	FiredAt   string `json:"firedAt"`
}

// Batch 一批待入库数据（按设备/采集组分组）。
// 携带结构化采集记录与统一采集时间，由各后端按自身线上格式序列化。
//
// 组身份是批次级冗余（同一批次内的记录共享同一个组），供通道做分帧与断网补发；
// 未分组批次 GroupID/GroupName 为空、Interval 为 0。
type Batch struct {
	DeviceID string
	// DeviceCode / ThingModelCode / GroupCode 供主题模板的编码占位符使用；
	// 空值由通道按约定回落（_unencoded / _unmodeled / legacy）。
	DeviceCode      string
	ThingModelCode  string
	GroupID         string
	GroupCode       string
	GroupName       string
	GroupIntervalMs int
	CollectedAt     string
	Records         []Record
}
