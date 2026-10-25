// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package pluginhost

import (
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"time"

	"gorm.io/gorm"

	"github.com/Ellisata/iot-gateway-sdk/driversdk"
	"iot-gateway/logger"
	"iot-gateway/model/po"
)

// ErrProtocolNotProvisioned 当前没有任何已加载的插件声明该协议。
var ErrProtocolNotProvisioned = errors.New("没有已加载的插件提供该协议")

// provisionProtocols 依据插件 describe 结果维护 iot_protocol 行，
// 使插件协议自动出现在前端的协议下拉里。一个插件可能提供多个协议，逐个维护。
//
// 某个协议供给失败不阻断其余协议：驱动本身已经可用（注册在供给之前完成），
// 失败只影响前端下拉里缺这一行。返回首个错误供上层记录。
func (m *Manager) provisionProtocols(meta driversdk.Meta) error {
	var firstErr error
	for _, p := range meta.Protocols {
		if err := m.provisionProtocol(p, meta); err != nil {
			if firstErr == nil {
				firstErr = err
			}
			logger.Error("pluginhost: 维护协议 %s 的 iot_protocol 行失败: %v", p.Name, err)
		}
	}
	return firstErr
}

// provisionProtocol 维护单个协议的 iot_protocol 行。
//
// 策略：**只插入，绝不覆盖**。
//   - 无同名行：插入，sort=100 让插件协议排在内置协议之后；
//   - 有同名行但 description / form_json 为空：补全（视作占位或旧数据）；
//   - 有同名行且内容不同：保留库内值并记警告。
//
// 理由：iot_protocol 行是运维数据，运维可能已按现场需求改过表单；
// 插件自带的 form_json 是代码。让一次网关重启静默回滚运维的编辑是不可接受的。
// 插件更新表单后要采纳，由运维走「恢复默认表单」（service.ResetProtocolForm
// → Manager.PluginProtocolForm）主动覆盖，而不是靠 provision 自动回滚。
//
// 直接走 *gorm.DB 而不复用 service.ProtocolService：后者会拖入
// service/dto/vo/enums/response 整条依赖边，而这里要的只是一条按名字的
// 插入/补空语句 —— 那点逻辑不值得把整条依赖边拉进插件宿主。
func (m *Manager) provisionProtocol(proto driversdk.Protocol, meta driversdk.Meta) error {
	var existing po.IotProtocol
	err := m.db.Where("name = ?", proto.Name).First(&existing).Error
	switch {
	case errors.Is(err, gorm.ErrRecordNotFound):
		return m.insertProtocol(proto, meta)
	case err != nil:
		return fmt.Errorf("查询协议行: %w", err)
	}

	// 已存在：只补空字段，不覆盖任何已有内容。
	updates := map[string]any{}
	if desc := proto.Describe(meta.Description); existing.Description == "" && desc != "" {
		updates["description"] = desc
	}
	if isBlankForm(existing.FormJSON) && len(proto.FormJSON) > 0 {
		if !json.Valid(proto.FormJSON) {
			return fmt.Errorf("插件表单不是合法 JSON，已跳过补全")
		}
		updates["form_json"] = string(proto.FormJSON)
	}

	if len(updates) == 0 {
		if formDiffers(existing, proto) {
			logger.Warn("pluginhost: 协议 %s 的 iot_protocol 行与插件自带的表单不一致，"+
				"保留库内值（运维可能已手工调整）；如需改用插件表单，"+
				"请在协议列表里对该协议执行「恢复默认表单」", proto.Name)
		}
		return nil
	}

	updates["updated_at"] = time.Now().Format("2006-01-02 15:04:05")
	if err := m.db.Model(&po.IotProtocol{}).Where("id = ?", existing.ID).Updates(updates).Error; err != nil {
		return fmt.Errorf("补全协议行: %w", err)
	}
	logger.Info("pluginhost: 已补全协议 %s 的 iot_protocol 行", proto.Name)
	return nil
}

func (m *Manager) insertProtocol(proto driversdk.Protocol, meta driversdk.Meta) error {
	formJSON := "{}"
	if len(proto.FormJSON) > 0 {
		if !json.Valid(proto.FormJSON) {
			return fmt.Errorf("插件表单不是合法 JSON")
		}
		formJSON = string(proto.FormJSON)
	}

	now := time.Now().Format("2006-01-02 15:04:05")
	row := &po.IotProtocol{
		Name:        proto.Name,
		Description: proto.Describe(meta.Description),
		FormJSON:    formJSON,
		Sort:        defaultPluginRowSort, // 排在内置协议之后
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	if err := m.db.Create(row).Error; err != nil {
		return fmt.Errorf("插入协议行: %w", err)
	}
	logger.Info("pluginhost: 已为插件协议 %s 创建 iot_protocol 行", proto.Name)
	return nil
}

// PluginProtocolForm 返回已加载插件为协议 name 声明的表单 JSON。
//
// 这是「恢复插件默认表单」的读取入口。之所以需要它：provisionProtocol 的策略是
// 「只插入、只补空、绝不覆盖」，运维一旦改过表单，插件侧更新的表单就再也不会自动
// 生效（见该函数的注释）。本方法让运维能主动采纳插件当前声明的表单。
//
// 插件声明了该协议但没给表单时返回 (nil, nil) —— 这不是错误，
// 由调用方决定提示成「无默认表单可恢复」还是别的。
// 返回副本，调用方改写不会污染已加载插件的 meta。
func (m *Manager) PluginProtocolForm(name string) ([]byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	for _, lp := range m.plugins {
		for _, p := range lp.meta.Protocols {
			if p.Name != name {
				continue
			}
			if len(p.FormJSON) == 0 {
				return nil, nil
			}
			if !json.Valid(p.FormJSON) {
				return nil, fmt.Errorf("插件 %s 为协议 %s 声明的表单不是合法 JSON",
					filepath.Base(lp.path), name)
			}
			return append([]byte(nil), p.FormJSON...), nil
		}
	}
	return nil, ErrProtocolNotProvisioned
}

func isBlankForm(form string) bool {
	return form == "" || form == "{}"
}

// formDiffers 判断库内表单是否与插件自带的不同（仅在双方都非空时有意义）。
func formDiffers(existing po.IotProtocol, proto driversdk.Protocol) bool {
	if isBlankForm(existing.FormJSON) || len(proto.FormJSON) == 0 {
		return false
	}
	// 归一化后比较，避免仅空白/字段顺序不同就告警。
	var a, b any
	if err := json.Unmarshal([]byte(existing.FormJSON), &a); err != nil {
		return true
	}
	if err := json.Unmarshal(proto.FormJSON, &b); err != nil {
		return true
	}
	ca, err1 := json.Marshal(a)
	cb, err2 := json.Marshal(b)
	if err1 != nil || err2 != nil {
		return true
	}
	return string(ca) != string(cb)
}
