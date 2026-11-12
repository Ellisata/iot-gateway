// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package service

import (
	"encoding/json"
	"sort"
	"strings"

	"iot-gateway/model/po"
	"iot-gateway/utils/canonicaljson"
)

// 本文件是「网关侧配置语义对象」的唯一出处。管理平台（iot-gateway-admin）
// 的 cfgCodec.go 必须构造逐字相同的对象，否则 contentHash 无法对齐。
//
// 约定：
//   - 字段名与开放接口 payload 完全一致；
//   - 数组按自然键排序后再计算（属性 identifier / 点位 name / 组 name）；
//   - 空字符串 / 空数组 / 空对象使用确定值，不用 nil；
//   - 密钥字段原样放入对象，由 canonicaljson.HashWithType 统一脱敏。

// devicePointSemantic 设备点位的 hash 输入，已解析掉内部 ID。
type devicePointSemantic struct {
	Name               string
	Label              string
	CommonDataType     string
	RwPermission       string
	ScanFrequency      int
	Description        string
	Status             int
	PropertyIdentifier string
	GroupName          string
	ThresholdEnabled   bool
	ThresholdOp        string
	ThresholdValues    []string
	Transforms         []any
}

func thingModelConfigHash(m po.ThingModel, props []po.ThingModelProperty) (string, error) {
	properties := make([]map[string]any, 0, len(props))
	for _, p := range props {
		properties = append(properties, map[string]any{
			"identifier":  p.Identifier,
			"name":        p.Name,
			"kind":        p.Kind,
			"accessMode":  p.AccessMode,
			"unit":        p.Unit,
			"description": p.Description,
			"sortOrder":   p.SortOrder,
			"status":      p.Status,
		})
	}
	sort.Slice(properties, func(i, j int) bool {
		return properties[i]["identifier"].(string) < properties[j]["identifier"].(string)
	})
	return canonicaljson.HashWithType("thing_model", map[string]any{
		"code":        m.Code,
		"name":        m.Name,
		"category":    m.Category,
		"version":     m.Version,
		"description": m.Description,
		"status":      m.Status,
		"properties":  properties,
	})
}

func pushChannelConfigHash(c po.PushChannel) (string, error) {
	cfg, err := parseJSONValueOrEmptyObject(c.ConfigJSON)
	if err != nil {
		return "", err
	}
	return canonicaljson.HashWithType("push_channel", map[string]any{
		"type":        c.Type,
		"name":        c.Name,
		"description": c.Description,
		"config":      cfg,
		"status":      c.Status,
	})
}

func alarmWebhookConfigHash(w po.AlarmWebhook) (string, error) {
	return canonicaljson.HashWithType("alarm_webhook", map[string]any{
		"name":        w.Name,
		"type":        w.Type,
		"description": w.Description,
		"url":         w.URL,
		"secret":      w.Secret,
		"msgType":     w.MsgType,
		"atAll":       w.AtAll == 1,
		"atList":      decodeAtList(w.AtList),
		"status":      w.Status,
	})
}

func deviceConfigHash(d po.Device, protocolName, modelCode string, groups []po.CollectorGroup, points []devicePointSemantic) (string, error) {
	protocolJSON, err := parseJSONValueOrEmptyObject(d.ProtocolJSON)
	if err != nil {
		return "", err
	}

	groupItems := make([]map[string]any, 0, len(groups))
	for _, g := range groups {
		groupItems = append(groupItems, map[string]any{
			"code":        g.Code,
			"name":        g.Name,
			"intervalMs":  g.IntervalMs,
			"description": g.Description,
			"status":      g.Status,
		})
	}
	sort.Slice(groupItems, func(i, j int) bool {
		return groupItems[i]["name"].(string) < groupItems[j]["name"].(string)
	})

	pointItems := make([]map[string]any, 0, len(points))
	for _, p := range points {
		transforms := p.Transforms
		if transforms == nil {
			transforms = []any{}
		}
		pointItems = append(pointItems, map[string]any{
			"name":               p.Name,
			"label":              p.Label,
			"commonDataType":     p.CommonDataType,
			"rwPermission":       p.RwPermission,
			"scanFrequency":      p.ScanFrequency,
			"description":        p.Description,
			"status":             p.Status,
			"propertyIdentifier": p.PropertyIdentifier,
			"groupName":          p.GroupName,
			"threshold": map[string]any{
				"enabled": p.ThresholdEnabled,
				"op":      p.ThresholdOp,
				"values":  nonNilStrings(p.ThresholdValues),
			},
			"transform": transforms,
		})
	}
	sort.Slice(pointItems, func(i, j int) bool {
		return pointItems[i]["name"].(string) < pointItems[j]["name"].(string)
	})

	return canonicaljson.HashWithType("device", map[string]any{
		"code":         d.Code,
		"name":         d.Name,
		"protocolName": protocolName,
		"protocolJson": protocolJSON,
		"modelCode":    modelCode,
		"description":  d.Description,
		"status":       d.Status,
		"groups":       groupItems,
		"points":       pointItems,
	})
}

func runtimeConfigHash(key string, value any) (string, error) {
	return canonicaljson.HashWithType("runtime", map[string]any{
		"key":   key,
		"value": value,
	})
}

// parseJSONValueOrEmptyObject 解析对象/数组 JSON；空串返回空对象。
func parseJSONValueOrEmptyObject(raw string) (any, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return map[string]any{}, nil
	}
	v, err := canonicaljson.Decode([]byte(raw))
	if err != nil {
		return nil, err
	}
	if v == nil {
		return map[string]any{}, nil
	}
	return v, nil
}

// parseJSONArrayOrEmpty 解析 JSON 数组文本；空串或解析失败返回空数组。
// 用于 threshold_value / transform_json 这类「库内是字符串」的列。
func parseJSONArrayOrEmpty(raw string) []any {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return []any{}
	}
	v, err := canonicaljson.Decode([]byte(raw))
	if err != nil {
		return []any{}
	}
	arr, ok := v.([]any)
	if !ok {
		return []any{}
	}
	return arr
}

func parseStringArrayOrEmpty(raw string) []string {
	arr := parseJSONArrayOrEmpty(raw)
	out := make([]string, 0, len(arr))
	for _, v := range arr {
		if s, ok := v.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

func decodeAtList(raw string) []string {
	return parseStringArrayOrEmpty(raw)
}

func encodeAtList(values []string) string {
	if len(values) == 0 {
		return ""
	}
	raw, err := json.Marshal(values)
	if err != nil {
		return ""
	}
	return string(raw)
}

func nonNilStrings(in []string) []string {
	if in == nil {
		return []string{}
	}
	return in
}
