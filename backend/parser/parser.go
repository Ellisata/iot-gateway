// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

// Package parser 定义「外部报文 → collector.CollectedRecord」的解析契约。
//
// 解析与报文来源（MQTT 订阅、将来的 HTTP 接入等）解耦：来源只负责把原始报文
// 连同 topic/到达时间交给 Parser，输出固定为 CollectedRecord 白名单，因此下游
// 的时序通道、断网缓存与设备点位元信息索引无需感知解析方式的差异。
//
// 实现按类型注册（见 Register），当前内置 builtin（声明式映射）与 script
// （goja 脚本）；进程外解析插件将来以 plugin:<name> 类型接入，契约不变。
package parser

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Ellisata/iot-gateway-sdk/driver"
	"iot-gateway/collector"
)

// 解析方式（Config.Type）。空类型按 builtin 处理，兼容历史配置。
const (
	TypeBuiltin = "builtin"
	TypeScript  = "script"
)

// CollectedRecord 可映射的目标字段名。映射 DSL 与脚本共用这一份白名单，
// 两侧各自维护会漂移成「同一个字段两种写法，只有一种能落库」。
const (
	FieldDeviceID           = "deviceId"
	FieldDeviceName         = "deviceName"
	FieldDeviceAddressID    = "deviceAddressId"
	FieldDeviceAddressName  = "deviceAddressName"
	FieldDeviceAddressLabel = "deviceAddressLabel"
	FieldValue              = "value"
	// FieldCollectedAt 采集时间（可选）。报文里没有时间时留空，下游按收到报文的时间落库。
	FieldCollectedAt = "collectedAt"
	FieldDataType    = "dataType"
	FieldKind        = "kind"
	FieldProtocol    = "protocol"
	FieldQuality     = "quality"
)

// FieldNames 全部合法的目标字段，顺序即前端映射表格的展示顺序。
var FieldNames = []string{
	FieldDeviceID,
	FieldDeviceName,
	FieldDeviceAddressID,
	FieldDeviceAddressName,
	FieldDeviceAddressLabel,
	FieldValue,
	FieldCollectedAt,
	FieldDataType,
	FieldKind,
	FieldProtocol,
	FieldQuality,
}

const (
	// DefaultMaxPayloadBytes 单条报文大小上限，防止单条超大报文占满解析队列。
	DefaultMaxPayloadBytes = 1 << 20
	// DefaultMaxBatchMessages 单批解析的报文数上限。脚本可逐条处理，
	// 进程外插件需要批量 RPC，因此契约以批为单位。
	DefaultMaxBatchMessages = 256
	// DefaultMaxRecordsPerMessage 单条报文允许产出的记录数上限。
	DefaultMaxRecordsPerMessage = 200
	// HardMaxRecordsPerMessage 配置可调上限的硬顶，避免一条报文撑爆时序通道批次。
	HardMaxRecordsPerMessage = 1000
	// MaxValueBytes 单个 value 的长度上限。
	MaxValueBytes = 4096
	// DefaultTimeout 单批解析的兜底超时。
	DefaultTimeout = 5 * time.Second
)

// Message 一条待解析的外部报文。
type Message struct {
	Topic      string
	Payload    []byte
	ReceivedAt time.Time
	QoS        byte
	Retained   bool
}

// MessageError 批内单条报文的解析失败。
//
// 单条失败不整批丢弃：数组报文里一条坏记录不该让其余几百条一起丢。
type MessageError struct {
	Index  int
	Topic  string
	Reason string
}

// Result 一批报文的解析结果。
type Result struct {
	Records []collector.CollectedRecord
	Errors  []MessageError
	// Skipped 报文内被跳过的记录数（例如数组里部分元素缺字段）。
	Skipped int
}

// Parser 解析器实例。
//
// 实例与订阅一一对应，由单个 worker 顺序调用，因此可以持有跨报文状态
// （窗口聚合、增量还原等）；**不要求并发安全**。Close 之后不得再调用 Parse。
type Parser interface {
	Parse(ctx context.Context, batch []Message) (Result, error)
	Close() error
}

// Config 一次解析的配置。Type 为空按 builtin 处理。
type Config struct {
	Type   string
	Config json.RawMessage
}

// Factory 解析器工厂。Validate 只校验配置、不产生运行实例，供保存前调用；
// New 与运行期预览必须走同一套构造路径。
type Factory interface {
	Validate(cfg json.RawMessage) error
	New(cfg json.RawMessage) (Parser, error)
}

var (
	registryMu sync.RWMutex
	registry   = map[string]Factory{}
)

// Register 注册一种解析方式。重复注册是编程错误，直接 panic。
func Register(typeName string, f Factory) {
	typeName = strings.TrimSpace(typeName)
	if typeName == "" || f == nil {
		panic("parser: 注册解析方式需要非空的类型名与工厂")
	}
	registryMu.Lock()
	defer registryMu.Unlock()
	if _, exists := registry[typeName]; exists {
		panic(fmt.Sprintf("parser: 解析方式 %q 重复注册", typeName))
	}
	registry[typeName] = f
}

// Resolve 查找解析器工厂。
func Resolve(typeName string) (Factory, error) {
	typeName = normalizeType(typeName)
	registryMu.RLock()
	defer registryMu.RUnlock()
	f, ok := registry[typeName]
	if !ok {
		return nil, fmt.Errorf("未知的解析方式 %q", typeName)
	}
	return f, nil
}

// Validate 校验解析器配置（不构造运行实例）。
func Validate(cfg Config) error {
	f, err := Resolve(cfg.Type)
	if err != nil {
		return err
	}
	name := normalizeType(cfg.Type)
	if len(strings.TrimSpace(string(cfg.Config))) == 0 {
		return fmt.Errorf("解析方式 %q 的配置不能为空", name)
	}
	if err := f.Validate(cfg.Config); err != nil {
		return fmt.Errorf("%s 解析配置无效: %w", name, err)
	}
	return nil
}

// New 构造解析器实例。调用方负责在不再使用时 Close。
func New(cfg Config) (Parser, error) {
	f, err := Resolve(cfg.Type)
	if err != nil {
		return nil, err
	}
	name := normalizeType(cfg.Type)
	if len(strings.TrimSpace(string(cfg.Config))) == 0 {
		return nil, fmt.Errorf("解析方式 %q 的配置不能为空", name)
	}
	p, err := f.New(cfg.Config)
	if err != nil {
		return nil, fmt.Errorf("%s 解析配置无效: %w", name, err)
	}
	return p, nil
}

func normalizeType(t string) string {
	t = strings.TrimSpace(t)
	if t == "" {
		return TypeBuiltin
	}
	return t
}

// RecordFromMap 把脚本产出的字段表转成 CollectedRecord。
//
// 用于进程内脚本层：字段名必须在白名单内，必填字段齐全，value 长度受限。
// 数字走 UseNumber 口径（脚本层传入的 int64/float64 已经是精确值），
// 不会经过一次 float64 中转。
func RecordFromMap(in map[string]any) (collector.CollectedRecord, error) {
	var rec collector.CollectedRecord
	var valueKind string
	hasQuality := false

	for k, v := range in {
		var err error
		switch k {
		case FieldDeviceID:
			rec.DeviceID, err = scalarField(k, v)
		case FieldDeviceName:
			rec.DeviceName, err = scalarField(k, v)
		case FieldDeviceAddressID:
			rec.DeviceAddressID, err = scalarField(k, v)
		case FieldDeviceAddressName:
			rec.DeviceAddressName, err = scalarField(k, v)
		case FieldDeviceAddressLabel:
			rec.DeviceAddressLabel, err = scalarField(k, v)
		case FieldValue:
			rec.Value, valueKind, err = scalarValue(v)
		case FieldCollectedAt:
			rec.CollectedAt, err = scalarField(k, v)
		case FieldDataType:
			rec.DataType, err = scalarField(k, v)
		case FieldKind:
			rec.Kind, err = scalarField(k, v)
		case FieldProtocol:
			rec.Protocol, err = scalarField(k, v)
		case FieldQuality:
			hasQuality = true
			rec.Quality, err = qualityField(v)
		default:
			return rec, fmt.Errorf("未知的目标字段 %q", k)
		}
		if err != nil {
			return rec, err
		}
	}

	rec.DeviceID = strings.TrimSpace(rec.DeviceID)
	rec.DeviceAddressID = strings.TrimSpace(rec.DeviceAddressID)
	if rec.DeviceID == "" {
		return rec, errors.New("deviceId 为空")
	}
	if rec.DeviceAddressID == "" {
		return rec, errors.New("deviceAddressId 为空")
	}
	if rec.Value == "" {
		return rec, errors.New("value 为空")
	}
	if len(rec.Value) > MaxValueBytes {
		return rec, fmt.Errorf("value 超过 %d 字节上限", MaxValueBytes)
	}
	if !hasQuality {
		rec.Quality = driver.QualityGood
	}
	if rec.Kind == "" {
		rec.Kind = valueKind
	}
	return rec, nil
}

func scalarField(field string, v any) (string, error) {
	switch v.(type) {
	case map[string]any, []any:
		return "", fmt.Errorf("字段 %s 需要标量，实际是对象或数组", field)
	}
	return ScalarString(v), nil
}

func scalarValue(v any) (value, kind string, err error) {
	switch t := v.(type) {
	case map[string]any, []any:
		return "", "", errors.New("value 需要标量，实际是对象或数组")
	case bool:
		return strconv.FormatBool(t), driver.KindBool, nil
	case int:
		return strconv.Itoa(t), driver.KindInt, nil
	case int64:
		return strconv.FormatInt(t, 10), driver.KindInt, nil
	case float64:
		return strconv.FormatFloat(t, 'f', -1, 64), driver.KindFloat, nil
	case float32:
		return strconv.FormatFloat(float64(t), 'f', -1, 32), driver.KindFloat, nil
	case json.Number:
		if _, err := t.Int64(); err == nil {
			return t.String(), driver.KindInt, nil
		}
		return t.String(), driver.KindFloat, nil
	default:
		return ScalarString(v), driver.KindString, nil
	}
}

func qualityField(v any) (int, error) {
	s := strings.TrimSpace(ScalarString(v))
	n, err := strconv.ParseInt(s, 10, 32)
	if err != nil {
		return 0, fmt.Errorf("quality %q 不是整数质量码", s)
	}
	return int(n), nil
}

// ScalarString 将任意 JSON 标量渲染为字符串。
//
// json.Number 走 String() 保留原始字面量，避免经 float64 中转丢精度。
func ScalarString(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return t
	case json.Number:
		return t.String()
	case bool:
		return strconv.FormatBool(t)
	case float64:
		return strconv.FormatFloat(t, 'f', -1, 64)
	case float32:
		return strconv.FormatFloat(float64(t), 'f', -1, 32)
	case int:
		return strconv.Itoa(t)
	case int64:
		return strconv.FormatInt(t, 10)
	case uint64:
		return strconv.FormatUint(t, 10)
	default:
		return fmt.Sprintf("%v", t)
	}
}
