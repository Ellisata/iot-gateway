// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package sqlite

import (
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"

	appLogger "iot-gateway/logger"
	"iot-gateway/model/po"
	"iot-gateway/utils"
)

// defaultOpenApiSecretName 默认开放接口密钥的名称。
//
// 它同时是这条记录的**身份**：删除与改名的守卫都按这个名字判定（见
// service.DeleteOpenApiSecret / UpdateOpenApiSecretName），因此刻意与
// service 包里的 defaultApiKeyName 取同一个字面量。两处各写一次而不是互相
// import：database 层不该依赖 service 层，而这个名字一旦要改，两侧是同一处
// 改动 —— 守卫与播种对不上时，表现是"默认密钥能被删掉"，测试会立刻发现。
const defaultOpenApiSecretName = "default"

// seedDefaultOpenApiSecret 启动时确保存在一条名为 default 的开放接口密钥。
//
// 这条密钥有两个用途，所以不能交给运维手工建：
//   - 它是开放接口的兜底凭据（管理平台自动发现后直接用它调本网关的接口）；
//   - 它随局域网广播一起发布（见 discovery/announcer.go 的 apiKey 字段），
//     "启动就存在"是管理平台能立刻接入的前提。
//
// 幂等性由「按名称查到即返回」保证：改名、改库、删掉数据文件重建都能落到正确
// 分支上。密钥内容**只在首次创建时生成**，此后永不刷新 —— 它是已分发给管理
// 平台的凭据，一次重启就换掉会让对方静默失效（与 seedGatewayIdentity 只刷新
// firmware_version 相反的取舍，理由正在于此：身份行的版本是二进制自身的属性，
// 而密钥是别人手里的凭据）。
//
// 与 seedGatewayIdentity 一样**不 Fatal**：缺失只让发现报文少一个字段、开放
// 接口少一条可用凭据，为它让网关起不来（Fatal 即 os.Exit(1)，运维只能删库）
// 不划算。
func seedDefaultOpenApiSecret(db *gorm.DB) error {
	var existing po.OpenApiSecret
	err := db.Where("name = ?", defaultOpenApiSecretName).First(&existing).Error
	switch {
	case err == nil:
		return nil
	case !errors.Is(err, gorm.ErrRecordNotFound):
		return fmt.Errorf("load default open api secret: %w", err)
	}

	key, err := utils.GenerateSecretKey()
	if err != nil {
		return fmt.Errorf("generate default open api secret key: %w", err)
	}

	now := time.Now().Format("2006-01-02 15:04:05")
	record := &po.OpenApiSecret{
		Name:      defaultOpenApiSecretName,
		Key:       key,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := db.Create(record).Error; err != nil {
		return fmt.Errorf("create default open api secret: %w", err)
	}

	// 刻意**不把密钥内容打进日志**：它会随发现广播公开，但日志会落到运维的
	// 日志文件与采集系统里，多一份明文副本没有收益（对比 seedDefaultAdmin 记
	// 口令 —— 那是登录的唯一入口，不记运维就进不去）。
	appLogger.Info("default open api secret created: name=%s", record.Name)
	return nil
}
