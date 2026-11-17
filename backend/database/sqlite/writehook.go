// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package sqlite

import (
	"sync"

	"gorm.io/gorm"
)

// 本文件把「配置表发生写入」这一事实暴露给进程内的观察者。
//
// 为什么用 GORM 回调而不是 PRAGMA data_version：后者只告诉你库变过，报警、
// push_outbox、control_log 的写入同样会触发，配置备份会被打成每轮采集一次。
// 回调能拿到具体表名，按配置表白名单过滤即可。
//
// 已知边界：`db.Exec` / `db.Raw(...).Exec` 这类**原生 SQL 写入不经过 GORM
// 回调**，不会被观察到。当前业务写配置全部走 GORM 模型方法（Create/Save/
// Updates/Delete）；迁移与播种在回调安装之前完成，也不会误触发。若以后新增
// 原生 SQL 写配置，需要在写完后调用 NotifyWrite。
var (
	writeHookMu sync.RWMutex
	writeHook   func(table string)
)

// RegisterWriteHook 设置配置写入观察者；传 nil 取消。
//
// 同一进程只保留一个观察者（备份管理器）。重复注册以最后一次为准，避免包级
// 全局累积多个回调。
func RegisterWriteHook(fn func(table string)) {
	writeHookMu.Lock()
	writeHook = fn
	writeHookMu.Unlock()
}

// NotifyWrite 手工上报一次表写入。供不走 GORM 回调的原生 SQL 使用。
func NotifyWrite(table string) {
	notifyWrite(table)
}

func notifyWrite(table string) {
	if table == "" {
		return
	}
	writeHookMu.RLock()
	fn := writeHook
	writeHookMu.RUnlock()
	if fn != nil {
		fn(table)
	}
}

// installWriteCallbacks 在迁移与播种完成后安装写回调。
//
// 必须放在 InitDB 的最后：迁移（CREATE/ALTER/INSERT schema_migrations）与
// 首次播种不是「配置变更」，不应触发配置备份。
func installWriteCallbacks(db *gorm.DB) {
	after := func(tx *gorm.DB) {
		if tx.Error != nil || tx.Statement == nil {
			return
		}
		notifyWrite(tx.Statement.Table)
	}
	_ = db.Callback().Create().After("gorm:create").Register("iot_gateway:config_write", after)
	_ = db.Callback().Update().After("gorm:update").Register("iot_gateway:config_write", after)
	_ = db.Callback().Delete().After("gorm:delete").Register("iot_gateway:config_write", after)
}
