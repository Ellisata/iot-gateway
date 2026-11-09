// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package service

import (
	"encoding/json"
	"strings"

	"gorm.io/gorm"

	"iot-gateway/appError"
	"iot-gateway/enums"
	"iot-gateway/model/po"
)

// ensureCollectorGroupNotReferencedByPushChannels 删除采集组前拦截推送通道订阅。
//
// 消息类通道（mqtt / http / kafka）在 config_json.groupIds 里按组 ID 订阅：
// 组行删掉之后通道配置仍在，这个 ID 永远匹配不到任何批次，表现为“通道悄悄不再
// 推送这组数据”而没有任何报错。表间没有外键，所以删除动作必须在引用解除之前
// 被拒绝 —— 用户先从通道配置里移除该组，再回来删组。
//
// 不区分通道启停：停用的通道重新启用时同样要求引用完整（与触发规则同一口径）。
// 通道表是配置数据、行数很小，这里整表加载后逐条解析；不用 LIKE 去猜 JSON 里的
// ID（JSON 转义与空白会让匹配不可靠）。
func ensureCollectorGroupNotReferencedByPushChannels(tx *gorm.DB, groupID string) error {
	if groupID == "" {
		return nil
	}
	subscribed, err := pushChannelSubscribedGroupIDs(tx)
	if err != nil {
		return err
	}
	if _, ok := subscribed[groupID]; ok {
		return collectorGroupInUseByChannelErr()
	}
	return nil
}

// ensureDevicesNotReferencedByPushChannels 删除设备前拦截推送通道对其采集组的订阅。
//
// 设备删除会级联删掉名下采集组（见 DeviceService.DeleteDevice 的说明），若只守
// 单组删除，删设备就能绕过去、在通道配置里留下悬空的组 ID。单条与批量删除用同一
// 判据：任意一台设备名下有任意一个组被订阅，整批拒绝。
//
// 与 ensureCollectorGroupNotReferencedByPushChannels 一样不区分通道启停。
func ensureDevicesNotReferencedByPushChannels(tx *gorm.DB, deviceIDs []string) error {
	if len(deviceIDs) == 0 {
		return nil
	}

	var groups []po.CollectorGroup
	if err := tx.Select("id").Where("device_id IN ?", deviceIDs).Find(&groups).Error; err != nil {
		return err
	}
	if len(groups) == 0 {
		return nil
	}

	subscribed, err := pushChannelSubscribedGroupIDs(tx)
	if err != nil {
		return err
	}
	for _, g := range groups {
		if _, ok := subscribed[g.ID]; ok {
			return deviceReferencedByPushChannelErr()
		}
	}
	return nil
}

// pushChannelSubscribedGroupIDs 汇总所有通道订阅的采集组 ID（已去空白）。
func pushChannelSubscribedGroupIDs(tx *gorm.DB) (map[string]struct{}, error) {
	var channels []po.PushChannel
	if err := tx.Select("id", "config_json").Find(&channels).Error; err != nil {
		return nil, err
	}
	subscribed := make(map[string]struct{})
	for i := range channels {
		for _, id := range pushChannelGroupIDs(channels[i].ConfigJSON) {
			subscribed[id] = struct{}{}
		}
	}
	return subscribed, nil
}

// pushChannelGroupIDs 取一条通道配置里订阅的组 ID。
//
// groupIds 是各消息类通道 config_json 的公共字段（顶层）。顺手兼容逗号分隔的
// 字符串写法：配置格式一旦放宽，删除守卫不能悄悄漏掉引用。
func pushChannelGroupIDs(configJSON string) []string {
	var probe struct {
		GroupIDs json.RawMessage `json:"groupIds"`
	}
	if err := json.Unmarshal([]byte(configJSON), &probe); err != nil || len(probe.GroupIDs) == 0 {
		// JSON 坏掉的通道引擎本来就会跳过，不让它挡住删除。
		return nil
	}

	var ids []string
	if err := json.Unmarshal(probe.GroupIDs, &ids); err != nil {
		var csv string
		if err := json.Unmarshal(probe.GroupIDs, &csv); err != nil {
			return nil
		}
		ids = strings.Split(csv, ",")
	}
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		if id = strings.TrimSpace(id); id != "" {
			out = append(out, id)
		}
	}
	return out
}

func collectorGroupInUseByChannelErr() error {
	return appError.NewAppErrorCtx(
		enums.CollectorGroupInUseByChannelEnum.GetCode(),
		enums.CollectorGroupInUseByChannelEnum.GetMessage(),
		enums.CollectorGroupInUseByChannelEnum.GetMsgKey())
}

func deviceReferencedByPushChannelErr() error {
	return appError.NewAppErrorCtx(
		enums.DeviceReferencedByPushChannelEnum.GetCode(),
		enums.DeviceReferencedByPushChannelEnum.GetMessage(),
		enums.DeviceReferencedByPushChannelEnum.GetMsgKey())
}
