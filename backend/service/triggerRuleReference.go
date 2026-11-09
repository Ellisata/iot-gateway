// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package service

import (
	"encoding/json"

	"gorm.io/gorm"

	"iot-gateway/appError"
	"iot-gateway/enums"
	"iot-gateway/model/po"
	"iot-gateway/triggerrule"
)

// ensureDevicesNotReferencedByTriggerRules 删除设备前拦截触发规则引用。
//
// 触发规则用设备/点位 ID 保存引用，表间没有外键：设备连同点位删掉之后，规则仍在库里，
// 引擎每次重载快照都会打 "condition address ... not found"，而这条规则从此再也不会触发
// （条件点位的样本永远不会到）。所以删除动作必须在引用解除之前被拒绝 —— 用户先改规则
// 或删规则，再回来删设备。
//
// 覆盖三种引用方式：
//   - action_device_id：动作设备本身；
//   - action_address_ids：动作点位；
//   - conditions_json：条件点位（DNF，组内 AND / 组间 OR）。
//
// 不区分规则启停：停用的规则重新启用时同样要求引用完整。
//
// 规则表是配置数据、行数很小，这里整表加载后逐条解析；不必用 LIKE 去猜 JSON 里的 ID
// （JSON 转义与空白会让匹配不可靠）。
func ensureDevicesNotReferencedByTriggerRules(tx *gorm.DB, deviceIDs []string) error {
	if len(deviceIDs) == 0 {
		return nil
	}

	targets := make(map[string]struct{}, len(deviceIDs))
	for _, id := range deviceIDs {
		targets[id] = struct{}{}
	}

	// 设备名下的点位：即使设备行已经不在了（孤立点位），也要把这些 ID 纳入检查。
	var addrs []po.DeviceAddress
	if err := tx.Select("id", "device_id").Where("device_id IN ?", deviceIDs).Find(&addrs).Error; err != nil {
		return err
	}
	addrIDs := make(map[string]struct{}, len(addrs))
	for _, a := range addrs {
		addrIDs[a.ID] = struct{}{}
	}

	return ensureNoTriggerRuleReferences(tx, targets, addrIDs, deviceReferencedByTriggerRuleErr)
}

// ensureAddressesNotReferencedByTriggerRules 删除点位前拦截触发规则引用。
//
// 与设备删除同理（见 ensureDevicesNotReferencedByTriggerRules）：点位被规则的条件或
// 动作引用时直接删掉，规则会在引擎重载时持续报 "condition address ... not found"，
// 条件点位再也不会产生样本。动作设备引用与点位无关，这里不查。
//
// 调用方必须先确认这些点位确实存在且会被删除：对"本就不存在"的点位做引用检查会
// 得到与存在性校验相反的错误（参见 BatchDeleteDeviceAddress 的过滤）。
func ensureAddressesNotReferencedByTriggerRules(tx *gorm.DB, addressIDs []string) error {
	if len(addressIDs) == 0 {
		return nil
	}
	addrIDs := make(map[string]struct{}, len(addressIDs))
	for _, id := range addressIDs {
		addrIDs[id] = struct{}{}
	}
	return ensureNoTriggerRuleReferences(tx, nil, addrIDs, addressReferencedByTriggerRuleErr)
}

// ensureNoTriggerRuleReferences 整表加载规则并逐条判定；命中任意一条即返回 mkErr()。
func ensureNoTriggerRuleReferences(tx *gorm.DB, deviceIDs, addrIDs map[string]struct{},
	mkErr func() error) error {

	var rules []po.TriggerRule
	if err := tx.Select("id", "action_device_id", "action_address_ids", "conditions_json").
		Find(&rules).Error; err != nil {
		return err
	}
	for i := range rules {
		if triggerRuleReferencesRule(&rules[i], deviceIDs, addrIDs) {
			return mkErr()
		}
	}
	return nil
}

// triggerRuleReferencesRule 判断一条规则是否引用了目标设备（或其名下点位）。
func triggerRuleReferencesRule(rule *po.TriggerRule, deviceIDs, addrIDs map[string]struct{}) bool {
	if _, ok := deviceIDs[rule.ActionDeviceID]; ok {
		return true
	}

	var actionIDs []string
	if err := json.Unmarshal([]byte(rule.ActionAddressIDs), &actionIDs); err == nil {
		for _, id := range actionIDs {
			if _, ok := addrIDs[id]; ok {
				return true
			}
		}
	}

	// JSON 坏掉的规则引擎本来就会跳过，不让它挡住删除。
	conds, err := triggerrule.ParseConditions([]byte(rule.ConditionsJSON))
	if err != nil {
		return false
	}
	for _, g := range conds.Groups {
		for _, c := range g.Conditions {
			if _, ok := addrIDs[c.AddressID]; ok {
				return true
			}
		}
	}
	return false
}

func deviceReferencedByTriggerRuleErr() error {
	return appError.NewAppErrorCtx(
		enums.DeviceReferencedByTriggerRuleEnum.GetCode(),
		enums.DeviceReferencedByTriggerRuleEnum.GetMessage(),
		enums.DeviceReferencedByTriggerRuleEnum.GetMsgKey())
}

func addressReferencedByTriggerRuleErr() error {
	return appError.NewAppErrorCtx(
		enums.DeviceAddressReferencedByTriggerRuleEnum.GetCode(),
		enums.DeviceAddressReferencedByTriggerRuleEnum.GetMessage(),
		enums.DeviceAddressReferencedByTriggerRuleEnum.GetMsgKey())
}
